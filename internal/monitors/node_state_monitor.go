package monitors

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	nodeStateStream       = "node_state"
	nodeStatePollInterval = 30 * time.Second
)

// nodeStatePaths are the single-integer files under hyperliquid_data that
// the monitor publishes, keyed by the file label used in metrics.
type nodeStateFile struct {
	label string // hl_node_persisted_state_file_available{file}
	rel   string
}

var (
	nodeStateFreeze = nodeStateFile{"freeze_abci_height", "freeze_abci_height"}
	nodeStateFast   = nodeStateFile{"evm_db_hub_fast/cp_checkpoint_height", "evm_db_hub_fast/cp_checkpoint_height"}
	nodeStateSlow   = nodeStateFile{"evm_db_hub_slow/cp_checkpoint_height", "evm_db_hub_slow/cp_checkpoint_height"}
)

// StartNodeStateMonitor publishes persisted core/ABCI heights from three
// single-integer files under $NODE_HOME/hyperliquid_data: freeze_abci_height
// and the fast and slow evm_db_hub cp_checkpoint_height files. Despite their
// directory names these are core/ABCI heights, not EVM block heights.
func StartNodeStateMonitor(ctx context.Context, cfg *config.Config) {
	root := filepath.Join(cfg.NodeHome, "hyperliquid_data")
	logger.InfoComponent("node-state", "Watching persisted node state under %s", root)

	ticker := time.NewTicker(nodeStatePollInterval)
	defer ticker.Stop()

	tickNodeState(root)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickNodeState(root)
		}
	}
}

// tickNodeState reads the three files. Each is published independently; a
// missing or malformed file withdraws its own series and marks it
// unavailable. Returns whether anything was published.
func tickNodeState(root string) bool {
	published := false

	freeze, freezeOK := readSingleInt(filepath.Join(root, filepath.FromSlash(nodeStateFreeze.rel)))
	setFileAvailable(nodeStateFreeze, freezeOK)
	freezeSrc := attribute.String("source", "freeze_abci_height")
	above := attribute.String("comparison", "visor_minus_persisted_freeze")
	if freezeOK {
		metrics.SetGaugeSeries(metrics.HLNodePersistedFreezeABCIHeight, float64(freeze), freezeSrc)
		// a node below its replay floor (freeze above visor height, transient
		// during replay) reads a fresh 0 instead of holding a stale value
		if h := latestVisorHeight.Load(); h > 0 {
			metrics.SetGaugeSeries(metrics.HLNodeVisorHeightAbovePersistedFrz, float64(max(h-freeze, 0)), above)
		} else {
			metrics.ClearGaugeSeries(metrics.HLNodeVisorHeightAbovePersistedFrz, above)
		}
		published = true
	} else {
		metrics.ClearGaugeSeries(metrics.HLNodePersistedFreezeABCIHeight, freezeSrc)
		metrics.ClearGaugeSeries(metrics.HLNodeVisorHeightAbovePersistedFrz, above)
	}

	fast, fastOK := readSingleInt(filepath.Join(root, filepath.FromSlash(nodeStateFast.rel)))
	slow, slowOK := readSingleInt(filepath.Join(root, filepath.FromSlash(nodeStateSlow.rel)))
	setFileAvailable(nodeStateFast, fastOK)
	setFileAvailable(nodeStateSlow, slowOK)
	fastClass := attribute.String("source_class", "evm_db_hub_fast_cp_checkpoint_height")
	slowClass := attribute.String("source_class", "evm_db_hub_slow_cp_checkpoint_height")
	gap := attribute.String("comparison", "fast_minus_slow")
	if fastOK {
		metrics.SetGaugeSeries(metrics.HLNodePersistedABCIHeight, float64(fast), fastClass)
		published = true
	} else {
		metrics.ClearGaugeSeries(metrics.HLNodePersistedABCIHeight, fastClass)
	}
	if slowOK {
		metrics.SetGaugeSeries(metrics.HLNodePersistedABCIHeight, float64(slow), slowClass)
		published = true
	} else {
		metrics.ClearGaugeSeries(metrics.HLNodePersistedABCIHeight, slowClass)
	}
	if fastOK && slowOK {
		metrics.SetGaugeSeries(metrics.HLNodePersistedABCIHeightGap, float64(fast-slow), gap)
	} else {
		metrics.ClearGaugeSeries(metrics.HLNodePersistedABCIHeightGap, gap)
	}

	metrics.SetSourceUp(nodeStateStream, published)
	if published {
		metrics.MarkSourceSample(nodeStateStream, time.Now())
	}
	return published
}

func setFileAvailable(f nodeStateFile, ok bool) {
	v := 0.0
	if ok {
		v = 1
	}
	metrics.SetGaugeSeries(metrics.HLNodePersistedStateFileAvailable, v, attribute.String("file", f.label))
}

// readSingleInt parses the whole file as one nonnegative ASCII integer,
// whitespace trimmed. ok is false on any IO or parse error.
func readSingleInt(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
