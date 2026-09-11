package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

const (
	critMsgStream       = "crit_msg"
	critMsgPollInterval = 30 * time.Second
	// hl-node appends a daily record about every 5 min; a record older than
	// this means the process is gone or stuck and its counters are withdrawn
	critMsgStaleAfter = 15 * time.Minute
	// bounded cardinality for the per-location series
	critLocationCap = 32
)

// critMsgSources are the subdirectories under crit_msg_stats/ that hl-node
// and hl-visor each write.
var critMsgSources = []string{"hl-node", "hl-visor"}

// critLocationsPath is the rich per-location document hl-visor rewrites
// alongside its daily record. Only hl-visor produces it.
const critLocationsPath = "/tmp/crit_msg_latest_stats/hl-visor.json"

// critGeneration is one daily record:
// [<sample_iso>, [<base_iso>, n_bugs, n_crits, n_locations]]. The counts are
// cumulative since the process started at base time and reset on restart, so
// they are gauges: a restart zeroes the series instead of regressing a counter.
type critGeneration struct {
	SampleTime time.Time
	BaseTime   time.Time
	NBugs      int64
	NCrits     int64
	NLocations int64
}

type critLocation struct {
	File      string
	Line      int64
	N         int64
	IsIgnored bool
	LastSeen  time.Time
}

type critMsgMonitor struct {
	root      string
	richPath  string
	published map[string]bool
	// latest hl-visor daily generation, the join key for the rich document
	visorDaily     critGeneration
	visorAvailable bool
	activeLocs     map[[2]string]struct{}
}

// StartCritMsgMonitor publishes hl-node's own bug! and crit! counters from
// $NODE_HOME/data/crit_msg_stats/<source>/<YYYYMMDD> and the top crit
// locations from hl-visor's rich document. n_bugs above zero is the
// strongest page-someone signal hl-node emits; a rising crit count is an
// ongoing incident; a rising location count means a new call site started
// firing rather than the same one recurring.
func StartCritMsgMonitor(ctx context.Context, cfg *config.Config) {
	m := newCritMsgMonitor(filepath.Join(cfg.NodeHome, "data", "crit_msg_stats"), critLocationsPath)
	logger.InfoComponent("crit-msg", "Watching %s and %s", m.root, m.richPath)

	ticker := time.NewTicker(critMsgPollInterval)
	defer ticker.Stop()
	m.tick(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(time.Now())
		}
	}
}

func newCritMsgMonitor(root, richPath string) *critMsgMonitor {
	return &critMsgMonitor{root: root, richPath: richPath, published: make(map[string]bool), activeLocs: make(map[[2]string]struct{})}
}

func (m *critMsgMonitor) tick(now time.Time) {
	m.visorAvailable = false
	up := false
	for _, source := range critMsgSources {
		gen, err := m.readDaily(source)
		if err != nil {
			m.withdraw(source)
			if os.IsNotExist(err) {
				continue
			}
			logger.DebugComponent("crit-msg", "%s: %v", source, err)
			continue
		}
		up = true
		if now.Sub(gen.SampleTime) > critMsgStaleAfter {
			m.withdraw(source)
			continue
		}
		m.publish(source, gen)
		if source == "hl-visor" {
			m.visorDaily, m.visorAvailable = gen, true
		}
	}
	metrics.SetSourceUp(critMsgStream, up)

	if err := m.tickLocations(now); err != nil {
		logger.DebugComponent("crit-msg", "locations: %v", err)
	}
}

// readDaily returns the last complete record of the newest date file for
// source. A missing directory or file is os.ErrNotExist; other failures are
// counted as source errors.
func (m *critMsgMonitor) readDaily(source string) (critGeneration, error) {
	dir := filepath.Join(m.root, source)
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			metrics.IncrementSourceErrors(critMsgStream, "stat")
		}
		return critGeneration{}, err
	}
	path, err := utils.LatestFile(dir)
	if err != nil {
		metrics.IncrementSourceErrors(critMsgStream, "walk")
		return critGeneration{}, err
	}
	if path == "" {
		return critGeneration{}, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		metrics.IncrementSourceErrors(critMsgStream, "read")
		return critGeneration{}, err
	}
	line, ok := lastFullLine(data)
	if !ok {
		return critGeneration{}, fmt.Errorf("%s: no complete record yet", path)
	}
	gen, err := parseCritGeneration(line)
	if err != nil {
		metrics.IncrementParseErrors(critMsgStream, "shape")
		return critGeneration{}, fmt.Errorf("%s: %w", path, err)
	}
	metrics.MarkSourceSample(critMsgStream, gen.SampleTime)
	return gen, nil
}

func parseCritGeneration(line []byte) (critGeneration, error) {
	var outer []json.RawMessage
	if err := json.Unmarshal(line, &outer); err != nil || len(outer) != 2 {
		return critGeneration{}, fmt.Errorf("record is not a [sample, [base, n_bugs, n_crits, n_locations]] tuple")
	}
	var inner []json.RawMessage
	if err := json.Unmarshal(outer[1], &inner); err != nil || len(inner) != 4 {
		return critGeneration{}, fmt.Errorf("inner tuple must have four fields")
	}
	var sampleStr, baseStr string
	if err := unmarshalRequiredJSON(outer[0], &sampleStr); err != nil {
		return critGeneration{}, fmt.Errorf("sample timestamp: %w", err)
	}
	if err := unmarshalRequiredJSON(inner[0], &baseStr); err != nil {
		return critGeneration{}, fmt.Errorf("base timestamp: %w", err)
	}
	sampleTime, okSample := parseVisorTime(sampleStr)
	baseTime, okBase := parseVisorTime(baseStr)
	if !okSample || !okBase {
		return critGeneration{}, fmt.Errorf("unparseable timestamp")
	}
	gen := critGeneration{SampleTime: sampleTime, BaseTime: baseTime}
	for i, dst := range []*int64{&gen.NBugs, &gen.NCrits, &gen.NLocations} {
		if err := unmarshalRequiredJSON(inner[i+1], dst); err != nil || *dst < 0 {
			return critGeneration{}, fmt.Errorf("count %d is not a nonnegative integer", i)
		}
	}
	return gen, nil
}

func (m *critMsgMonitor) publish(source string, gen critGeneration) {
	label := attribute.String("source", source)
	metrics.SetGaugeSeries(metrics.HLNodeBugs, float64(gen.NBugs), label)
	metrics.SetGaugeSeries(metrics.HLNodeCrits, float64(gen.NCrits), label)
	metrics.SetGaugeSeries(metrics.HLNodeCritLocations, float64(gen.NLocations), label)
	metrics.SetGaugeSeries(metrics.HLNodeCriticalMessagesBaseTime, float64(gen.BaseTime.Unix()), label)
	metrics.SetGaugeSeries(metrics.HLNodeCriticalMessageSampleTS, float64(gen.SampleTime.Unix()), label)
	m.published[source] = true
}

func (m *critMsgMonitor) withdraw(source string) {
	if !m.published[source] {
		return
	}
	label := attribute.String("source", source)
	metrics.ClearGaugeSeries(metrics.HLNodeBugs, label)
	metrics.ClearGaugeSeries(metrics.HLNodeCrits, label)
	metrics.ClearGaugeSeries(metrics.HLNodeCritLocations, label)
	metrics.ClearGaugeSeries(metrics.HLNodeCriticalMessagesBaseTime, label)
	metrics.ClearGaugeSeries(metrics.HLNodeCriticalMessageSampleTS, label)
	delete(m.published, source)
}

// tickLocations publishes the top locations from the rich hl-visor document
// when it describes the same process generation as the daily hl-visor record
// (same base time and counts). Anything else, including a missing or stale
// document, withdraws the location series.
func (m *critMsgMonitor) tickLocations(now time.Time) error {
	info, err := os.Stat(m.richPath)
	if err != nil {
		m.replaceLocations(nil)
		if os.IsNotExist(err) {
			return nil
		}
		metrics.IncrementSourceErrors(critMsgStream, "stat")
		return err
	}
	if !m.visorAvailable || now.Sub(info.ModTime()) > critMsgStaleAfter {
		m.replaceLocations(nil)
		return nil
	}
	data, err := os.ReadFile(m.richPath)
	if err != nil {
		m.replaceLocations(nil)
		metrics.IncrementSourceErrors(critMsgStream, "read")
		return err
	}
	baseTime, nBugs, nCrits, locs, err := parseCritLocations(data)
	if err != nil {
		m.replaceLocations(nil)
		metrics.IncrementParseErrors(critMsgStream, "shape")
		return err
	}
	if !baseTime.Equal(m.visorDaily.BaseTime) || nBugs != m.visorDaily.NBugs || nCrits != m.visorDaily.NCrits {
		// the rich document lags or leads the daily record; wait for them to agree
		m.replaceLocations(nil)
		return nil
	}
	m.replaceLocations(locs)
	return nil
}

// parseCritLocations decodes the rich document and returns its top locations
// by count. Message text (first_msg) is never read into a label or value.
func parseCritLocations(data []byte) (baseTime time.Time, nBugs, nCrits int64, locs []critLocation, err error) {
	var doc struct {
		StartTime string              `json:"start_time"`
		NBugs     *int64              `json:"n_bugs"`
		NCrits    *int64              `json:"n_crits"`
		Stats     [][]json.RawMessage `json:"code_location_and_stats"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return time.Time{}, 0, 0, nil, fmt.Errorf("decode rich document: %w", err)
	}
	baseTime, ok := parseVisorTime(doc.StartTime)
	if !ok || doc.NBugs == nil || doc.NCrits == nil || doc.Stats == nil || *doc.NBugs < 0 || *doc.NCrits < 0 {
		return time.Time{}, 0, 0, nil, fmt.Errorf("rich document header is incomplete")
	}
	byLabel := make(map[[2]string]critLocation, len(doc.Stats))
	for i, pair := range doc.Stats {
		if len(pair) != 2 {
			return time.Time{}, 0, 0, nil, fmt.Errorf("location %d is not a [key, detail] pair", i)
		}
		var key struct {
			File string `json:"fln"`
			Line *int64 `json:"line"`
		}
		var detail struct {
			N         *int64 `json:"n"`
			IsIgnored bool   `json:"is_ignored"`
			LastSeen  string `json:"last_seen"`
		}
		if err := json.Unmarshal(pair[0], &key); err != nil || key.File == "" || key.Line == nil || *key.Line < 0 {
			return time.Time{}, 0, 0, nil, fmt.Errorf("location %d has an invalid key", i)
		}
		if err := json.Unmarshal(pair[1], &detail); err != nil || detail.N == nil || *detail.N < 0 {
			return time.Time{}, 0, 0, nil, fmt.Errorf("location %d has an invalid detail", i)
		}
		lastSeen, ok := parseVisorTime(detail.LastSeen)
		if !ok {
			return time.Time{}, 0, 0, nil, fmt.Errorf("location %d has an invalid last_seen", i)
		}
		loc := critLocation{File: filepath.Base(key.File), Line: *key.Line, N: *detail.N, IsIgnored: detail.IsIgnored, LastSeen: lastSeen}
		label := [2]string{loc.File, strconv.FormatInt(loc.Line, 10)}
		// basenames can collide across source directories: keep the larger count
		if prev, exists := byLabel[label]; !exists || loc.N > prev.N {
			byLabel[label] = loc
		}
	}
	locs = make([]critLocation, 0, len(byLabel))
	for _, loc := range byLabel {
		locs = append(locs, loc)
	}
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].N != locs[j].N {
			return locs[i].N > locs[j].N
		}
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		return locs[i].Line < locs[j].Line
	})
	if len(locs) > critLocationCap {
		locs = locs[:critLocationCap]
	}
	return baseTime, *doc.NBugs, *doc.NCrits, locs, nil
}

func (m *critMsgMonitor) replaceLocations(locs []critLocation) {
	current := make(map[[2]string]struct{}, len(locs))
	for _, loc := range locs {
		key := [2]string{loc.File, strconv.FormatInt(loc.Line, 10)}
		current[key] = struct{}{}
		labels := []attribute.KeyValue{attribute.String("file", key[0]), attribute.String("line", key[1])}
		metrics.SetGaugeSeries(metrics.HLNodeCritLocation, float64(loc.N), labels...)
		ignored := 0.0
		if loc.IsIgnored {
			ignored = 1
		}
		metrics.SetGaugeSeries(metrics.HLNodeCritLocationIgnored, ignored, labels...)
		metrics.SetGaugeSeries(metrics.HLNodeCritLocationLastSeenSeconds, float64(loc.LastSeen.Unix()), labels...)
	}
	for key := range m.activeLocs {
		if _, ok := current[key]; ok {
			continue
		}
		labels := []attribute.KeyValue{attribute.String("file", key[0]), attribute.String("line", key[1])}
		metrics.ClearGaugeSeries(metrics.HLNodeCritLocation, labels...)
		metrics.ClearGaugeSeries(metrics.HLNodeCritLocationIgnored, labels...)
		metrics.ClearGaugeSeries(metrics.HLNodeCritLocationLastSeenSeconds, labels...)
	}
	m.activeLocs = current
}
