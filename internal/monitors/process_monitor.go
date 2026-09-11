package monitors

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// processNames are the hl processes looked up in /proc. Both run on
// validator and non-validator nodes; the monitor publishes
// hl_node_process_up=0 for a missing one so operators can alert on it.
var processNames = []string{"hl-node", "hl-visor"}

const (
	processStream       = "process"
	processPollInterval = 15 * time.Second
	procRoot            = "/proc"
)

var processIOOperations = []string{"read_bytes", "write_bytes", "read_syscalls", "write_syscalls"}

// StartProcessMonitor publishes liveness and resource usage for each known
// hl process from procfs.
func StartProcessMonitor(ctx context.Context) {
	if _, err := os.Stat(procRoot + "/self/stat"); err != nil {
		logger.InfoComponent("process", "process monitor disabled: /proc not available")
		metrics.SetSourceUp(processStream, false)
		return
	}
	logger.InfoComponent("process", "Watching %v via /proc", processNames)

	ticker := time.NewTicker(processPollInterval)
	defer ticker.Stop()
	state := newProcessMonitorState()

	state.tick(procRoot)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state.tick(procRoot)
		}
	}
}

func (s *processMonitorState) tick(root string) bool {
	selections, err := findProcessesAt(root, processNames)
	if err != nil {
		metrics.SetSourceUp(processStream, false)
		metrics.IncrementSourceErrors(processStream, "read")
		logger.DebugComponent("process", "procfs scan incomplete; retaining last complete snapshot: %v", err)
		return false
	}

	for _, name := range processNames {
		sel := selections[name]
		label := attribute.String("process", name)
		metrics.SetGaugeSeries(metrics.HLNodeProcessEligibleMatches, float64(sel.Eligible), label)
		if !sel.Found {
			publishMissingProcess(label)
			delete(s.io, name)
			continue
		}

		info := sel.Info
		metrics.SetGaugeSeries(metrics.HLNodeProcessUp, 1, label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessStartTime, float64(info.StartTimeUnix), label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessCPUSeconds, info.CPUSeconds, label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessRSSBytes, float64(info.RSSBytes), label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessVirtBytes, float64(info.VirtBytes), label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessThreads, float64(info.Threads), label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessOpenFDs, float64(info.OpenFDs), label)
		metrics.SetGaugeSeries(metrics.HLNodeProcessMaxFDs, float64(info.MaxFDs), label)
		ratio := 0.0
		if info.MaxFDs > 0 {
			ratio = float64(info.OpenFDs) / float64(info.MaxFDs)
		}
		metrics.SetGaugeSeries(metrics.HLNodeProcessOpenFDsRatio, ratio, label)

		// every operation series exists from the first complete scan, so an
		// idle or freshly started process still exposes the whole family
		delta, _ := s.observe(name, info)
		for op, n := range map[string]uint64{
			"read_bytes": delta.ReadBytes, "write_bytes": delta.WriteBytes,
			"read_syscalls": delta.ReadSyscalls, "write_syscalls": delta.WriteSyscalls,
		} {
			metrics.AddCounter(metrics.HLNodeProcessIOTotal, int64(n), label, attribute.String("operation", op))
		}
	}

	metrics.SetSourceUp(processStream, true)
	metrics.MarkSourceSample(processStream, time.Now())
	return true
}

// publishMissingProcess zeroes the current-value gauges so dashboards do
// not present the last live process as current.
func publishMissingProcess(label attribute.KeyValue) {
	for _, g := range []api.Float64ObservableGauge{
		metrics.HLNodeProcessUp,
		metrics.HLNodeProcessStartTime,
		metrics.HLNodeProcessCPUSeconds,
		metrics.HLNodeProcessRSSBytes,
		metrics.HLNodeProcessVirtBytes,
		metrics.HLNodeProcessThreads,
		metrics.HLNodeProcessOpenFDs,
		metrics.HLNodeProcessMaxFDs,
		metrics.HLNodeProcessOpenFDsRatio,
	} {
		metrics.SetGaugeSeries(g, 0, label)
	}
}

// processInfo is the OS-independent view the publisher consumes.
type processInfo struct {
	PID            int
	StartTimeTicks uint64
	StartTimeUnix  int64
	CPUSeconds     float64
	RSSBytes       int64
	VirtBytes      int64
	Threads        int64
	OpenFDs        int64
	MaxFDs         uint64
	IO             processIOValues
	IOValid        bool
}

type processSelection struct {
	Info     processInfo
	Eligible int
	Found    bool
}

type processIOValues struct {
	ReadBytes     uint64
	WriteBytes    uint64
	ReadSyscalls  uint64
	WriteSyscalls uint64
}

type processEpoch struct {
	PID            int
	StartTimeTicks uint64
}

type processIOBaseline struct {
	epoch processEpoch
	value processIOValues
}

type processMonitorState struct {
	io map[string]processIOBaseline
}

func newProcessMonitorState() *processMonitorState {
	return &processMonitorState{io: make(map[string]processIOBaseline, len(processNames))}
}

// observe returns positive deltas only within one exact process epoch
// (PID plus start time). A new epoch establishes a baseline. A field that
// rolls backwards is rebased independently so one procfs reset cannot
// produce a negative value.
func (s *processMonitorState) observe(name string, info processInfo) (processIOValues, bool) {
	if !info.IOValid {
		return processIOValues{}, false
	}
	epoch := processEpoch{PID: info.PID, StartTimeTicks: info.StartTimeTicks}
	previous, exists := s.io[name]
	s.io[name] = processIOBaseline{epoch: epoch, value: info.IO}
	if !exists || previous.epoch != epoch {
		return processIOValues{}, true
	}
	return processIOValues{
		ReadBytes:     positiveDelta(previous.value.ReadBytes, info.IO.ReadBytes),
		WriteBytes:    positiveDelta(previous.value.WriteBytes, info.IO.WriteBytes),
		ReadSyscalls:  positiveDelta(previous.value.ReadSyscalls, info.IO.ReadSyscalls),
		WriteSyscalls: positiveDelta(previous.value.WriteSyscalls, info.IO.WriteSyscalls),
	}, true
}

func positiveDelta(previous, current uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}
