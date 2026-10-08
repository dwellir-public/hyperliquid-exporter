package monitors

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	diskStream = "disk"
	// diskPollInterval is slow on purpose: walking NODE_HOME visits hundreds
	// of thousands of inodes on a long-lived node and a full disk does not
	// change in seconds.
	diskPollInterval = 120 * time.Second

	diskPathPresentNonempty = "present_nonempty"
	diskPathPresentEmpty    = "present_empty"
	diskPathAbsent          = "absent"
)

var diskPathStates = []string{diskPathPresentNonempty, diskPathPresentEmpty, diskPathAbsent}

// trackedSubdirs is the allowlist of NODE_HOME subpaths published
// individually: the directories most likely to consume runaway disk, with
// both broad rollups and per-RocksDB subpaths so operators can tell which
// DB is bloating. Prefixes nest, so a file adds to every matching bucket.
var trackedSubdirs = []string{
	"data/replica_cmds",
	"data/evm_block_and_receipts",
	"data/block_times",
	"data/node_fast_block_times",
	"data/node_slow_block_times",
	"data/node_logs",
	"data/latency_buckets",
	"data/latency_summaries",
	"data/periodic_abci_states",
	"data/visor_abci_states",
	"data/tcp_traffic",
	"data/dhs",
	"hyperliquid_data",
	"hyperliquid_data/db_hub/Evm",
	"hyperliquid_data/db_hub/Exchange",
	"hyperliquid_data/db_hub/Rpc",
	"hyperliquid_data/evm_db_hub_fast",
	"hyperliquid_data/evm_db_hub_fast/EvmState",
	"hyperliquid_data/evm_db_hub_slow",
	"hyperliquid_data/evm_db_hub_slow/EvmState",
	"hyperliquid_data/evm_db_hub_slow/checkpoint",
	"tmp",
}

type fsStats struct{ bavail, blocks, bsize uint64 }

func statfs(path string) (fsStats, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return fsStats{}, err
	}
	return fsStats{bavail: s.Bavail, blocks: s.Blocks, bsize: uint64(s.Bsize)}, nil
}

type diskFileID struct{ device, inode uint64 }

// allocatedFileInfo returns the physical identity and allocation of one
// entry. st_blocks is in 512-byte units regardless of filesystem block size.
func allocatedFileInfo(info fs.FileInfo) (id diskFileID, bytes int64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 {
		return diskFileID{}, 0, false
	}
	return diskFileID{uint64(stat.Dev), stat.Ino}, stat.Blocks * 512, true
}

type diskSnapshot struct {
	apparentTotal   int64
	apparentByPath  map[string]int64
	allocatedTotal  int64
	allocatedByPath map[string]int64
	pathState       map[string]string
	skipped         int // entries that vanished mid-walk
}

// StartDiskMonitor walks NODE_HOME every two minutes and publishes its size
// alongside the filesystem's free and total bytes.
func StartDiskMonitor(ctx context.Context, cfg *config.Config) {
	logger.InfoComponent("disk", "Starting disk monitor for %s (every %s)", cfg.NodeHome, diskPollInterval)

	ticker := time.NewTicker(diskPollInterval)
	defer ticker.Stop()

	tickDisk(cfg.NodeHome)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickDisk(cfg.NodeHome)
		}
	}
}

// tickDisk publishes statfs first and unconditionally, so a slow or failed
// walk still leaves free and total current. A failed walk retains the last
// complete snapshot. Returns whether the walk completed.
func tickDisk(nodeHome string) bool {
	if fsys, err := statfs(nodeHome); err == nil {
		metrics.SetGauge(metrics.HLNodeDiskFreeBytes, float64(fsys.bavail)*float64(fsys.bsize))
		metrics.SetGauge(metrics.HLNodeDiskTotalBytes, float64(fsys.blocks)*float64(fsys.bsize))
	} else {
		metrics.IncrementSourceErrors(diskStream, "statfs")
		logger.DebugComponent("disk", "statfs failed: %v", err)
	}

	snapshot, err := walkSizes(nodeHome, trackedSubdirs)
	if err != nil {
		metrics.SetSourceUp(diskStream, false)
		if !errors.Is(err, fs.ErrNotExist) {
			metrics.IncrementSourceErrors(diskStream, "walk")
		}
		logger.DebugComponent("disk", "NODE_HOME walk incomplete; retaining last complete snapshot: %v", err)
		return false
	}
	if snapshot.skipped > 0 {
		logger.DebugComponent("disk", "%d entries vanished during the walk", snapshot.skipped)
	}
	publishDisk(snapshot, time.Now())
	return true
}

func publishDisk(snapshot diskSnapshot, completedAt time.Time) {
	metrics.SetGauge(metrics.HLNodeDiskUsedBytes, float64(snapshot.apparentTotal))
	metrics.SetGauge(metrics.HLNodeDiskAllocatedBytes, float64(snapshot.allocatedTotal))
	for _, sub := range trackedSubdirs {
		metrics.SetGaugeSeries(metrics.HLNodeDiskSubdirBytes, float64(snapshot.apparentByPath[sub]), attribute.String("subdir", sub))
		metrics.SetGaugeSeries(metrics.HLNodeDiskSubdirAllocatedBytes, float64(snapshot.allocatedByPath[sub]), attribute.String("path", sub))
		for _, state := range diskPathStates {
			v := 0.0
			if snapshot.pathState[sub] == state {
				v = 1
			}
			metrics.SetGaugeSeries(metrics.HLNodeDiskPathState, v, attribute.String("path", sub), attribute.String("state", state))
		}
	}
	metrics.SetGauge(metrics.HLNodeDiskLastCompleteTS, float64(completedAt.Unix()))
	metrics.SetSourceUp(diskStream, true)
	metrics.MarkSourceSample(diskStream, completedAt)
}

// walkSizes walks nodeHome once, attributing every regular file's size to
// the grand total and to each tracked prefix it lives under, and every
// entry's allocated blocks deduplicated by device and inode within each
// scope. Any walk or metadata error rejects the whole snapshot so a
// plausible-looking partial total is never published.
func walkSizes(nodeHome string, subs []string) (diskSnapshot, error) {
	return walkSizesWith(nodeHome, subs, filepath.WalkDir)
}

type walkDirFunc func(string, fs.WalkDirFunc) error

func walkSizesWith(nodeHome string, subs []string, walk walkDirFunc) (diskSnapshot, error) {
	snapshot := diskSnapshot{
		apparentByPath:  make(map[string]int64, len(subs)),
		allocatedByPath: make(map[string]int64, len(subs)),
		pathState:       make(map[string]string, len(subs)),
	}
	paths := make([]string, len(subs))
	prefixes := make([]string, len(subs))
	seen := make(map[diskFileID]struct{})
	seenByPath := make(map[string]map[diskFileID]struct{}, len(subs))
	for i, sub := range subs {
		paths[i] = filepath.Join(nodeHome, filepath.FromSlash(sub))
		prefixes[i] = paths[i] + string(filepath.Separator)
		snapshot.pathState[sub] = diskPathAbsent
		seenByPath[sub] = make(map[diskFileID]struct{})
	}

	err := walk(nodeHome, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// an entry deleted between listing and stat (log rotation, pruning)
			// is skipped; the root and everything else still rejects the walk
			if path != nodeHome && errors.Is(err, fs.ErrNotExist) {
				snapshot.skipped++
				return nil
			}
			return err
		}
		matching := make([]int, 0, 2)
		for i := range subs {
			switch {
			case path == paths[i]:
				snapshot.pathState[subs[i]] = diskPathPresentEmpty
				matching = append(matching, i)
			case strings.HasPrefix(path, prefixes[i]):
				snapshot.pathState[subs[i]] = diskPathPresentNonempty
				matching = append(matching, i)
			}
		}

		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				snapshot.skipped++
				return nil
			}
			return err
		}
		id, allocated, ok := allocatedFileInfo(info)
		if !ok {
			return errors.New("filesystem entry lacks allocation identity")
		}
		snapshot.allocatedTotal += addUnique(seen, id, allocated)
		for _, i := range matching {
			snapshot.allocatedByPath[subs[i]] += addUnique(seenByPath[subs[i]], id, allocated)
		}

		if d.IsDir() {
			return nil
		}
		size := info.Size()
		snapshot.apparentTotal += size
		for _, i := range matching {
			snapshot.apparentByPath[subs[i]] += size
			if size > 0 && path == paths[i] {
				snapshot.pathState[subs[i]] = diskPathPresentNonempty
			}
		}
		return nil
	})
	if err != nil {
		return diskSnapshot{}, err
	}
	return snapshot, nil
}

func addUnique(seen map[diskFileID]struct{}, id diskFileID, allocated int64) int64 {
	if _, exists := seen[id]; exists {
		return 0
	}
	seen[id] = struct{}{}
	return allocated
}
