package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	operatorConfigStream       = "operator_config"
	operatorConfigPollInterval = 5 * time.Minute
	jailingConfigFile          = "heartbeat_jailing_config.json"
	failedLoadSuffix           = "_FAILED_LOAD"
)

// operatorConfigFiles is the fixed set of operator-edited JSONs under
// file_mod_time_tracker/. Cardinality is bounded by this list.
var operatorConfigFiles = []string{
	"crit_msg_ignore.json",
	"firewall_ips.json",
	"node_firewall_ips.json",
	"ip_rate_limiter_alert_config.json",
	"n_gossip_peers.json",
	// validator-only: whether the node auto-jails peers by latency EMA
	jailingConfigFile,
	// gossip-auction ordering toggle
	"node_gossip_priority_config.json",
	// presence only; the body is versioned and not interpreted
	"bug_alert_ack.json",
}

type operatorConfigSnapshot struct {
	present map[string]float64 // 1 present, 0 absent, -1 stat failed
	age     map[string]float64 // seconds, present files only
	failed  map[string]int64   // by file label incl. "unknown"
	jailing *jailingConfig     // nil when the file is absent
}

type jailingConfig struct {
	DryRun                  *bool    `json:"dry_run"`
	LatencyEmaJailThreshold *float64 `json:"latency_ema_jail_threshold"`
}

type operatorConfigError struct {
	stage string
	err   error
}

func (e *operatorConfigError) Error() string { return e.stage + ": " + e.err.Error() }

// StartOperatorConfigMonitor publishes presence, age and failed-load sidecar
// counts for the operator-edited configs under $NODE_HOME/file_mod_time_tracker,
// plus the parsed heartbeat jailing threshold. Validator nodes only.
func StartOperatorConfigMonitor(ctx context.Context, cfg *config.Config) {
	root := filepath.Join(cfg.NodeHome, "file_mod_time_tracker")
	logger.InfoComponent("consensus", "Watching operator config under %s", root)

	ticker := time.NewTicker(operatorConfigPollInterval)
	defer ticker.Stop()

	tickOperatorConfig(root)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickOperatorConfig(root)
		}
	}
}

func tickOperatorConfig(root string) bool {
	snapshot, err := scanOperatorConfig(root, time.Now())
	if err != nil {
		metrics.SetSourceUp(operatorConfigStream, false)
		if errors.Is(err, os.ErrNotExist) {
			withdrawOperatorConfig()
			return false
		}
		stage := "read"
		var oerr *operatorConfigError
		if errors.As(err, &oerr) {
			stage = oerr.stage
		}
		metrics.IncrementSourceErrors(operatorConfigStream, stage)
		logger.DebugComponent("consensus", "operator config scan failed; retaining last snapshot: %v", err)
		return false
	}
	publishOperatorConfig(snapshot)
	metrics.SetSourceUp(operatorConfigStream, true)
	metrics.MarkSourceSample(operatorConfigStream, time.Now())
	return true
}

// scanOperatorConfig stages a snapshot. A missing root returns os.ErrNotExist;
// any other failure is tagged with its stage (stat, read, decode, schema).
func scanOperatorConfig(root string, now time.Time) (operatorConfigSnapshot, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return operatorConfigSnapshot{}, os.ErrNotExist
		}
		return operatorConfigSnapshot{}, &operatorConfigError{"stat", err}
	}
	if !info.IsDir() {
		return operatorConfigSnapshot{}, &operatorConfigError{"schema", errors.New("not a directory")}
	}

	snapshot := operatorConfigSnapshot{
		present: make(map[string]float64, len(operatorConfigFiles)),
		age:     make(map[string]float64, len(operatorConfigFiles)),
		failed:  make(map[string]int64, len(operatorConfigFiles)+1),
	}
	for _, file := range operatorConfigFiles {
		info, err := os.Stat(filepath.Join(root, file))
		switch {
		case os.IsNotExist(err):
			continue
		case err != nil || !info.Mode().IsRegular():
			// one unreadable file is reported as -1 and does not blank the rest
			snapshot.present[file] = -1
			metrics.IncrementSourceErrors(operatorConfigStream, "stat")
			continue
		}
		snapshot.present[file] = 1
		snapshot.age[file] = max(now.Sub(info.ModTime()).Seconds(), 0)
	}

	// hl-node leaves <file>_FAILED_LOAD when it rejects an operator-pushed
	// config: silent misconfiguration unless someone looks
	entries, err := os.ReadDir(root)
	if err != nil {
		return operatorConfigSnapshot{}, &operatorConfigError{"read", err}
	}
	known := make(map[string]struct{}, len(operatorConfigFiles))
	for _, file := range operatorConfigFiles {
		known[file] = struct{}{}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), failedLoadSuffix) {
			continue
		}
		file := strings.TrimSuffix(entry.Name(), failedLoadSuffix)
		if _, ok := known[file]; !ok {
			file = "unknown"
		}
		snapshot.failed[file]++
	}

	jailing, err := readJailingConfig(filepath.Join(root, jailingConfigFile))
	if err != nil {
		return operatorConfigSnapshot{}, err
	}
	snapshot.jailing = jailing
	return snapshot, nil
}

// readJailingConfig returns nil, nil when the file is absent. Both keys must
// be present and typed, or the tick is rejected rather than published with
// a plausible zero threshold.
func readJailingConfig(path string) (*jailingConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &operatorConfigError{"read", err}
	}
	var jc jailingConfig
	if err := json.Unmarshal(raw, &jc); err != nil {
		return nil, &operatorConfigError{"decode", err}
	}
	if jc.DryRun == nil || jc.LatencyEmaJailThreshold == nil {
		return nil, &operatorConfigError{"schema", errors.New("jailing config requires dry_run and latency_ema_jail_threshold")}
	}
	return &jc, nil
}

func publishOperatorConfig(s operatorConfigSnapshot) {
	for _, file := range operatorConfigFiles {
		label := attribute.String("file", file)
		if age, ok := s.age[file]; ok {
			metrics.SetGaugeSeries(metrics.HLNodeOperatorConfigAgeSeconds, age, label)
		} else {
			metrics.ClearGaugeSeries(metrics.HLNodeOperatorConfigAgeSeconds, label)
		}
		metrics.SetGaugeSeries(metrics.HLNodeOperatorConfigPresent, s.present[file], label)
		metrics.SetGaugeSeries(metrics.HLNodeOperatorConfigFailedLoad, float64(s.failed[file]), label)
	}
	metrics.SetGaugeSeries(metrics.HLNodeOperatorConfigFailedLoad, float64(s.failed["unknown"]), attribute.String("file", "unknown"))

	if s.jailing == nil {
		metrics.ClearGauge(metrics.HLNodeJailingThresholdSeconds)
		metrics.ClearGauge(metrics.HLNodeJailingDryRun)
		return
	}
	metrics.SetGauge(metrics.HLNodeJailingThresholdSeconds, *s.jailing.LatencyEmaJailThreshold)
	dryRun := 0.0
	if *s.jailing.DryRun {
		dryRun = 1
	}
	metrics.SetGauge(metrics.HLNodeJailingDryRun, dryRun)
}

// withdrawOperatorConfig removes every series when the tracker directory
// does not exist, so absence is not reported as "all files missing".
func withdrawOperatorConfig() {
	for _, file := range append(operatorConfigFiles, "unknown") {
		label := attribute.String("file", file)
		metrics.ClearGaugeSeries(metrics.HLNodeOperatorConfigPresent, label)
		metrics.ClearGaugeSeries(metrics.HLNodeOperatorConfigAgeSeconds, label)
		metrics.ClearGaugeSeries(metrics.HLNodeOperatorConfigFailedLoad, label)
	}
	metrics.ClearGauge(metrics.HLNodeJailingThresholdSeconds)
	metrics.ClearGauge(metrics.HLNodeJailingDryRun)
}
