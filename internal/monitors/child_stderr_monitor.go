package monitors

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	childStderrStream       = "child_stderr"
	childStderrPollInterval = 60 * time.Second
	// childStderrHeadBytes bounds the per-file read. A larger artifact is
	// reported as truncated; its classification is evidence from the prefix.
	childStderrHeadBytes = 4096

	childStderrStateEmpty      = "empty"
	childStderrStateReadable   = "readable"
	childStderrStateTruncated  = "truncated"
	childStderrStateUnreadable = "unreadable"

	childStderrReasonNone    = "none"
	childStderrReasonUnknown = "unknown"
	childStderrReasonPanic   = "panic"
)

// childCrashReasons is a finite evidence taxonomy checked before generic
// panic signatures. The needles come from retained validator artifacts;
// they are not a list of every possible child exit.
var childCrashReasons = []struct {
	reason  string
	needles [][]byte
}{
	{"app_hash_mismatch", [][]byte{[]byte("computed app hash"), []byte("does not match quorum")}},
	{"hardfork_upgrade", [][]byte{[]byte("observed qc for newer hardfork")}},
	{"sync_overflow", [][]byte{[]byte("too many blocks to request")}},
	{"config_error", [][]byte{[]byte("is not in node_ips"), []byte("could not read"), []byte("configuration error")}},
	{"network", [][]byte{[]byte("connection timeout"), []byte("upstream connect error"), []byte("invalid ip address")}},
}

var explicitPanicNeedles = [][]byte{[]byte("panicked at"), []byte("panicked:")}

// childStderrReasons lists every reason label: none, the classes, panic, unknown.
func childStderrReasons() []string {
	reasons := []string{childStderrReasonNone}
	for _, class := range childCrashReasons {
		reasons = append(reasons, class.reason)
	}
	return append(reasons, childStderrReasonPanic, childStderrReasonUnknown)
}

// classifyChildStderr maps a bounded stderr prefix to a finite reason. An
// unmatched message is unknown; panic is reserved for explicit panic text.
func classifyChildStderr(head []byte) string {
	head = bytes.ToLower(head)
	for _, class := range childCrashReasons {
		for _, needle := range class.needles {
			if bytes.Contains(head, needle) {
				return class.reason
			}
		}
	}
	for _, needle := range explicitPanicNeedles {
		if bytes.Contains(head, needle) {
			return childStderrReasonPanic
		}
	}
	return childStderrReasonUnknown
}

type childStderrState struct {
	state   string
	reason  string
	size    int64
	modTime time.Time
}

// StartChildStderrMonitor watches $NODE_HOME/data/visor_child_stderr, where
// hl-visor retains one artifact per child start. Date directory names are a
// node-internal schedule and may be future-dated, so recency uses mtimes.
func StartChildStderrMonitor(ctx context.Context, cfg *config.Config) {
	root := filepath.Join(cfg.NodeHome, "data", "visor_child_stderr")
	logger.InfoComponent("child-stderr", "Watching %s", root)
	seen := map[string]*childStderrState{}

	ticker := time.NewTicker(childStderrPollInterval)
	defer ticker.Stop()

	tickChildStderr(root, seen)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickChildStderr(root, seen)
		}
	}
}

func tickChildStderr(root string, seen map[string]*childStderrState) bool {
	next, err := scanChildStderr(root, seen)
	if err != nil {
		metrics.SetSourceUp(childStderrStream, false)
		if !errors.Is(err, fs.ErrNotExist) {
			metrics.IncrementSourceErrors(childStderrStream, "read")
		}
		logger.DebugComponent("child-stderr", "directory scan incomplete; retaining last complete snapshot: %v", err)
		return false
	}

	clear(seen)
	for path, state := range next {
		seen[path] = state
	}
	publishChildStderr(seen)
	metrics.SetSourceUp(childStderrStream, true)
	metrics.MarkSourceSample(childStderrStream, time.Now())
	return true
}

// scanChildStderr stages a complete snapshot of retained files. Directory or
// metadata failure rejects the scan; a per-file content read failure is an
// explicit artifact state. Unchanged files (same size and mtime) reuse the
// previous classification without rereading.
func scanChildStderr(root string, previous map[string]*childStderrState) (map[string]*childStderrState, error) {
	next := make(map[string]*childStderrState)
	dateDirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, dateDir := range dateDirs {
		if !dateDir.IsDir() {
			continue
		}
		datePath := filepath.Join(root, dateDir.Name())
		nDirs, err := os.ReadDir(datePath)
		if err != nil {
			return nil, err
		}
		for _, nDir := range nDirs {
			if !nDir.IsDir() {
				continue
			}
			dir := filepath.Join(datePath, nDir.Name())
			files, err := os.ReadDir(dir)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				info, err := file.Info()
				if err != nil {
					return nil, err
				}
				path := filepath.Join(dir, file.Name())
				if prior := previous[path]; prior != nil && prior.state != childStderrStateUnreadable &&
					prior.size == info.Size() && prior.modTime.Equal(info.ModTime()) {
					cp := *prior
					next[path] = &cp
					continue
				}
				next[path] = classifyArtifact(path, info)
			}
		}
	}
	return next, nil
}

func classifyArtifact(path string, info fs.FileInfo) *childStderrState {
	state := &childStderrState{size: info.Size(), modTime: info.ModTime()}
	if info.Size() == 0 {
		state.state, state.reason = childStderrStateEmpty, childStderrReasonNone
		return state
	}
	head, err := readHead(path, childStderrHeadBytes)
	if err != nil {
		state.state, state.reason = childStderrStateUnreadable, childStderrReasonNone
		return state
	}
	state.state = childStderrStateReadable
	if info.Size() > childStderrHeadBytes {
		state.state = childStderrStateTruncated
	}
	state.reason = classifyChildStderr(head)
	return state
}

type childStderrSeries struct{ state, reason string }

// childStderrSeriesCensus is the fixed set of (state, reason) series, so
// every series is present at 0 rather than appearing on first sighting.
func childStderrSeriesCensus() []childStderrSeries {
	reasons := childStderrReasons()
	series := []childStderrSeries{
		{childStderrStateEmpty, childStderrReasonNone},
		{childStderrStateUnreadable, childStderrReasonNone},
	}
	for _, state := range []string{childStderrStateReadable, childStderrStateTruncated} {
		for _, reason := range reasons[1:] {
			series = append(series, childStderrSeries{state, reason})
		}
	}
	return series
}

func publishChildStderr(seen map[string]*childStderrState) {
	counts := make(map[childStderrSeries]int)
	newest := make(map[childStderrSeries]time.Time)
	crashCounts := make(map[string]int)
	crashNewest := make(map[string]time.Time)

	for _, s := range seen {
		key := childStderrSeries{s.state, s.reason}
		counts[key]++
		if s.modTime.After(newest[key]) {
			newest[key] = s.modTime
		}
		if s.state != childStderrStateReadable && s.state != childStderrStateTruncated {
			continue
		}
		if s.reason == childStderrReasonUnknown || s.reason == childStderrReasonNone {
			continue
		}
		crashCounts[s.reason]++
		if s.modTime.After(crashNewest[s.reason]) {
			crashNewest[s.reason] = s.modTime
		}
	}

	metrics.SetGauge(metrics.HLNodeChildStarts, float64(len(seen)))
	for _, series := range childStderrSeriesCensus() {
		labels := []attribute.KeyValue{attribute.String("state", series.state), attribute.String("reason", series.reason)}
		metrics.SetGaugeSeries(metrics.HLNodeChildStderrArtifacts, float64(counts[series]), labels...)
		if ts := newest[series]; ts.IsZero() {
			metrics.ClearGaugeSeries(metrics.HLNodeChildStderrLastArtifactTS, labels...)
		} else {
			metrics.SetGaugeSeries(metrics.HLNodeChildStderrLastArtifactTS, float64(ts.Unix()), labels...)
		}
	}
	reasons := childStderrReasons()
	for _, reason := range reasons[1 : len(reasons)-1] { // classes plus panic; not none or unknown
		label := attribute.String("reason", reason)
		metrics.SetGaugeSeries(metrics.HLNodeChildCrashes, float64(crashCounts[reason]), label)
		last := 0.0
		if ts := crashNewest[reason]; !ts.IsZero() {
			last = float64(ts.Unix())
		}
		metrics.SetGaugeSeries(metrics.HLNodeChildLastCrashSeconds, last, label)
	}
}

func readHead(path string, n int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, int64(n)))
}
