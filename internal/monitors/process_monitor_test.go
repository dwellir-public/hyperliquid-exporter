package monitors

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// fakeProc describes one PID under a fake /proc root.
type fakeProc struct {
	pid       int
	comm      string
	exe       string // readlink target; empty for none
	cmdline   string
	startTick uint64
	io        string // /proc/PID/io body; empty for none
	limits    string
}

func writeFakeProc(t *testing.T, root string, btime int64, procs ...fakeProc) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stat"), []byte("cpu 1 2 3\nbtime "+strconv.FormatInt(btime, 10)+"\n"), 0o644))
	for _, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(p.pid))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(p.comm+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(p.cmdline), 0o644))
		if p.exe != "" {
			require.NoError(t, os.Symlink(p.exe, filepath.Join(dir, "exe")))
		}
		// fields after comm: state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime ...
		stat := strconv.Itoa(p.pid) + " (" + p.comm + ") S 1 1 1 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 3 0 " +
			strconv.FormatUint(p.startTick, 10) + " 123456789 2000 18446744073709551615\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte("VmSize:\t 120000 kB\nVmRSS:\t 8000 kB\nThreads:\t7\n"), 0o644))
		for i := range 3 {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "fd", strconv.Itoa(i)), nil, 0o644))
		}
		if p.io != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "io"), []byte(p.io), 0o644))
		}
		if p.limits != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "limits"), []byte(p.limits), 0o644))
		}
	}
}

const fakeIO = "rchar: 1\nwchar: 2\nsyscr: 30\nsyscw: 40\nread_bytes: 1000\nwrite_bytes: 2000\n"
const fakeLimits = "Limit                     Soft Limit           Hard Limit           Units\nMax open files            1024                 4096                 files\n"

func TestFindProcessesAt_SelectsOldestValidatedProcess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proc")
	writeFakeProc(t, root, 1_700_000_000,
		fakeProc{pid: 300, comm: "hl-node", exe: "/opt/hl-node", cmdline: "/opt/hl-node\x00run\x00", startTick: 5000, io: fakeIO, limits: fakeLimits},
		fakeProc{pid: 200, comm: "hl-node", exe: "/opt/hl-node", cmdline: "/opt/hl-node\x00", startTick: 1000},
		// comm matches but neither exe nor argv0 does: an impostor, not eligible
		fakeProc{pid: 400, comm: "hl-node", exe: "/usr/bin/python3", cmdline: "python3\x00fake.py\x00", startTick: 10},
		// exe unreadable but argv0 matches
		fakeProc{pid: 500, comm: "hl-visor", cmdline: "./hl-visor\x00", startTick: 700},
	)

	sel, err := findProcessesAt(root, []string{"hl-node", "hl-visor"})
	require.NoError(t, err)

	node := sel["hl-node"]
	require.True(t, node.Found)
	assert.Equal(t, 2, node.Eligible)
	assert.Equal(t, 200, node.Info.PID, "oldest start time wins")
	assert.Equal(t, int64(1_700_000_010), node.Info.StartTimeUnix)
	assert.InDelta(t, 3.0, node.Info.CPUSeconds, 1e-9, "utime 250 + stime 50 ticks at 100 Hz")
	assert.Equal(t, int64(8000*1024), node.Info.RSSBytes, "status VmRSS overrides stat rss pages")
	assert.Equal(t, int64(7), node.Info.Threads)
	assert.Equal(t, int64(3), node.Info.OpenFDs)
	assert.False(t, node.Info.IOValid, "pid 200 has no io file")

	visor := sel["hl-visor"]
	require.True(t, visor.Found)
	assert.Equal(t, 500, visor.Info.PID)
}

func TestFindProcessesAt_OptionalFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proc")
	writeFakeProc(t, root, 1_700_000_000,
		fakeProc{pid: 300, comm: "hl-node", exe: "/opt/hl-node", cmdline: "/opt/hl-node\x00", startTick: 5000, io: fakeIO, limits: fakeLimits})

	sel, err := findProcessesAt(root, []string{"hl-node"})
	require.NoError(t, err)
	info := sel["hl-node"].Info
	assert.Equal(t, uint64(1024), info.MaxFDs)
	require.True(t, info.IOValid)
	assert.Equal(t, processIOValues{ReadBytes: 1000, WriteBytes: 2000, ReadSyscalls: 30, WriteSyscalls: 40}, info.IO)
}

func TestFindProcessesAt_FailsClosedWithoutBootTime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proc")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stat"), []byte("cpu 1 2 3\n"), 0o644))
	_, err := findProcessesAt(root, []string{"hl-node"})
	require.Error(t, err)
}

func TestProcessMonitorState_ObserveDeltas(t *testing.T) {
	s := newProcessMonitorState()
	base := processInfo{PID: 1, StartTimeTicks: 10, IOValid: true, IO: processIOValues{ReadBytes: 100, WriteSyscalls: 5}}

	_, ok := s.observe("hl-node", base)
	require.True(t, ok)

	next := base
	next.IO = processIOValues{ReadBytes: 150, WriteSyscalls: 3} // one field went backwards
	delta, ok := s.observe("hl-node", next)
	require.True(t, ok)
	assert.Equal(t, processIOValues{ReadBytes: 50, WriteSyscalls: 0}, delta)

	// a new epoch (restart) is a baseline, not a delta
	restarted := next
	restarted.PID = 2
	restarted.IO = processIOValues{ReadBytes: 10}
	delta, ok = s.observe("hl-node", restarted)
	require.True(t, ok)
	assert.Equal(t, processIOValues{}, delta)

	noIO := restarted
	noIO.IOValid = false
	_, ok = s.observe("hl-node", noIO)
	assert.False(t, ok)
}

func TestProcessMonitor_TickPublishesMissingAsZero(t *testing.T) {
	initTestMetrics(t)
	root := filepath.Join(t.TempDir(), "proc")
	writeFakeProc(t, root, 1_700_000_000,
		fakeProc{pid: 300, comm: "hl-node", exe: "/opt/hl-node", cmdline: "/opt/hl-node\x00", startTick: 5000})

	require.True(t, newProcessMonitorState().tick(root))

	up, ok := metrics.GaugeSeriesValue(metrics.HLNodeProcessUp, attribute.String("process", "hl-node"))
	require.True(t, ok)
	assert.Equal(t, 1.0, up)
	up, ok = metrics.GaugeSeriesValue(metrics.HLNodeProcessUp, attribute.String("process", "hl-visor"))
	require.True(t, ok)
	assert.Equal(t, 0.0, up)
	rss, _ := metrics.GaugeSeriesValue(metrics.HLNodeProcessRSSBytes, attribute.String("process", "hl-visor"))
	assert.Equal(t, 0.0, rss)
}
