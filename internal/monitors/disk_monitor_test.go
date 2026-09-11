package monitors

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

func TestWalkSizes(t *testing.T) {
	home := t.TempDir()
	write := func(rel string, n int) {
		path := filepath.Join(home, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, make([]byte, n), 0o644))
	}
	write("data/replica_cmds/1", 100)
	write("data/replica_cmds/2", 50)
	write("hyperliquid_data/db_hub/Evm/x", 3000)
	write("hyperliquid_data/other", 7)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tmp"), 0o755)) // present, empty
	// a hardlink shares an inode: apparent size counts twice, allocation once
	require.NoError(t, os.Link(filepath.Join(home, "data/replica_cmds/1"), filepath.Join(home, "data/replica_cmds/link")))

	s, err := walkSizes(home, trackedSubdirs)
	require.NoError(t, err)

	assert.Equal(t, int64(100+50+3000+7+100), s.apparentTotal)
	assert.Equal(t, int64(250), s.apparentByPath["data/replica_cmds"])
	// nested prefixes: the file counts in both the rollup and the leaf
	assert.Equal(t, int64(3007), s.apparentByPath["hyperliquid_data"])
	assert.Equal(t, int64(3000), s.apparentByPath["hyperliquid_data/db_hub/Evm"])
	assert.Equal(t, int64(0), s.apparentByPath["data/tcp_traffic"])

	assert.Equal(t, diskPathPresentNonempty, s.pathState["data/replica_cmds"])
	assert.Equal(t, diskPathPresentEmpty, s.pathState["tmp"])
	assert.Equal(t, diskPathAbsent, s.pathState["data/tcp_traffic"])

	// allocation deduplicates the hardlink within a scope
	assert.Less(t, s.allocatedByPath["data/replica_cmds"], 2*s.allocatedTotal)
	assert.Greater(t, s.allocatedTotal, int64(0))
	single, err := walkSizes(home, []string{"data/replica_cmds"})
	require.NoError(t, err)
	assert.Equal(t, s.allocatedByPath["data/replica_cmds"], single.allocatedByPath["data/replica_cmds"])

	// missing root rejects the snapshot
	_, err = walkSizes(filepath.Join(home, "nope"), trackedSubdirs)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWalkSizes_SkipsEntriesVanishingMidWalk(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "data", "node_logs")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep"), make([]byte, 5), 0o644))

	// wrap the real walk so one extra entry reports ENOENT, as a file deleted
	// between listing and stat would
	vanishing := func(root string, fn fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && path == filepath.Join(dir, "keep") {
				if err := fn(filepath.Join(dir, "gone"), nil, fs.ErrNotExist); err != nil {
					return err
				}
			}
			return fn(path, d, err)
		})
	}
	s, err := walkSizesWith(home, trackedSubdirs, vanishing)
	require.NoError(t, err)
	assert.Equal(t, 1, s.skipped)
	assert.Equal(t, int64(5), s.apparentByPath["data/node_logs"])

	// any other error, or the root itself missing, still rejects the snapshot
	broken := func(root string, fn fs.WalkDirFunc) error {
		return fn(root, nil, errors.New("io error"))
	}
	_, err = walkSizesWith(home, trackedSubdirs, broken)
	require.Error(t, err)
	rootGone := func(root string, fn fs.WalkDirFunc) error {
		return fn(root, nil, fs.ErrNotExist)
	}
	_, err = walkSizesWith(home, trackedSubdirs, rootGone)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestTickDisk_Publishes(t *testing.T) {
	initTestMetrics(t)
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "data", "node_logs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "data", "node_logs", "f"), make([]byte, 10), 0o644))

	require.True(t, tickDisk(home))

	used, ok := metrics.GaugeValue(metrics.HLNodeDiskUsedBytes)
	require.True(t, ok)
	assert.Equal(t, 10.0, used)
	free, ok := metrics.GaugeValue(metrics.HLNodeDiskFreeBytes)
	require.True(t, ok)
	assert.Greater(t, free, 0.0)
	total, _ := metrics.GaugeValue(metrics.HLNodeDiskTotalBytes)
	assert.GreaterOrEqual(t, total, free)
	sub, _ := metrics.GaugeSeriesValue(metrics.HLNodeDiskSubdirBytes, attribute.String("subdir", "data/node_logs"))
	assert.Equal(t, 10.0, sub)
	state, _ := metrics.GaugeSeriesValue(metrics.HLNodeDiskPathState, attribute.String("path", "data/node_logs"), attribute.String("state", diskPathPresentNonempty))
	assert.Equal(t, 1.0, state)
	ts, _ := metrics.GaugeValue(metrics.HLNodeDiskLastCompleteTS)
	assert.InDelta(t, float64(time.Now().Unix()), ts, 5)

	// a failed walk keeps the last complete snapshot
	require.NoError(t, os.RemoveAll(home))
	require.False(t, tickDisk(home))
	used, _ = metrics.GaugeValue(metrics.HLNodeDiskUsedBytes)
	assert.Equal(t, 10.0, used)
}
