package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/cache"
	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

// track last block time separately for fast and slow states
var (
	lastBlockTimeMu sync.RWMutex
	lastBlockTimes  *cache.LRUCache
	// keep track of block heights for cleanup
	blockHeightsByState *cache.LRUCache
	maxBlockHistory     = 100
)

func StartBlockMonitor(ctx context.Context, cfg config.Config, errCh chan<- error) {
	// init LRU caches
	lastBlockTimes = cache.NewLRUCache(10, 0)      // No TTL, just size limit
	blockHeightsByState = cache.NewLRUCache(10, 0) // No TTL, just size limit

	// check if new dual-state directories exist
	fastDir := filepath.Join(cfg.NodeHome, "data", "node_fast_block_times")
	slowDir := filepath.Join(cfg.NodeHome, "data", "node_slow_block_times")
	oldDir := filepath.Join(cfg.NodeHome, "data", "block_times")

	fastExists := false
	slowExists := false
	oldExists := false

	if _, err := os.Stat(fastDir); err == nil {
		fastExists = true
	}
	if _, err := os.Stat(slowDir); err == nil {
		slowExists = true
	}
	if _, err := os.Stat(oldDir); err == nil {
		oldExists = true
	}

	// if new directories exist, use dual-state monitoring
	if fastExists || slowExists {
		logger.InfoComponent("core", "Detected new dual-state block time directories")
		if fastExists {
			safego.Go("core", func() { monitorBlockState(ctx, cfg, errCh, "fast", "node_fast_block_times") })
		}
		if slowExists {
			safego.Go("core", func() { monitorBlockState(ctx, cfg, errCh, "slow", "node_slow_block_times") })
		}
	} else if oldExists {
		// fallback to old single-directory format for backward compatibility
		logger.InfoComponent("core", "Using legacy single block_times directory (node not yet upgraded)")
		safego.Go("core", func() { monitorLegacyBlockState(ctx, cfg, errCh) })
	} else {
		logger.WarningComponent("core", "No block time directories found - block monitoring disabled")
	}
}

func monitorBlockState(ctx context.Context, cfg config.Config, _ chan<- error, stateType string, dirName string) {
	blockTimeDir := filepath.Join(cfg.NodeHome, "data", dirName)
	logger.InfoComponent("core", "Starting %s state block monitor for directory: %s", stateType, blockTimeDir)

	t := streamTailer{stream: dirName, component: "core", dir: blockTimeDir}
	t.run(ctx, func(line []byte) error {
		return parseBlockTimeLine(ctx, string(line), stateType)
	})
}

// blockTimeLayout is the timestamp format the node writes to block time files (UTC, no zone).
const blockTimeLayout = "2006-01-02T15:04:05.999999999"

// recordPropagation records begin_block_wall_time minus block_time as
// propagation latency attributed to the current parent peer. Skipped when the
// field is absent (older node versions) or the delta is negative (clock skew).
func recordPropagation(beginWall string, blockTime time.Time, stateType string) {
	if beginWall == "" {
		return
	}
	began, err := time.Parse(blockTimeLayout, beginWall)
	if err != nil {
		logger.DebugComponent("core", "Skipping %s propagation sample, bad begin_block_wall_time %q: %v", stateType, beginWall, err)
		return
	}
	latencyMs := float64(began.UTC().Sub(blockTime).Microseconds()) / 1000
	if latencyMs < 0 {
		logger.DebugComponent("core", "Skipping %s propagation sample, negative latency %.3f ms", stateType, latencyMs)
		return
	}
	recordPropagationLatency(latencyMs, stateType, quality.Parent())
}

func parseBlockTimeLine(ctx context.Context, line string, stateType string) error {
	var data map[string]any
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return fmt.Errorf("error parsing block time line: %w", err)
	}

	height, ok := data["height"].(float64)
	if !ok {
		return fmt.Errorf("height not found or not a number")
	}

	blockTime, ok := data["block_time"].(string)
	if !ok {
		return fmt.Errorf("block time not found or not a string")
	}

	applyDuration, ok := data["apply_duration"].(float64)
	if !ok {
		return fmt.Errorf("apply duration not found or not a number")
	}

	// optional: wall clock when the node began applying the block
	beginBlockWallTime, _ := data["begin_block_wall_time"].(string)

	// convert applyDuration from seconds to milliseconds
	applyDurationMs := applyDuration * 1000

	// parse block_time to Unix timestamp
	parsedTime, err := time.Parse(blockTimeLayout, blockTime)
	if err != nil {
		return fmt.Errorf("error parsing block time: %w", err)
	}

	// assume the time is in UTC if no timezone is specified
	parsedTime = parsedTime.UTC()

	recordPropagation(beginBlockWallTime, parsedTime, stateType)

	// calculate block time difference for this state type
	lastBlockTimeMu.Lock()
	lastTimeIface, exists := lastBlockTimes.Get(stateType)
	if exists {
		lastTime := lastTimeIface.(time.Time)
		if !lastTime.IsZero() {
			blockTimeDiff := parsedTime.Sub(lastTime).Milliseconds()
			if blockTimeDiff > 0 {
				metrics.RecordBlockTimeWithLabel(float64(blockTimeDiff), stateType)
				logger.DebugComponent("core", "%s state block time difference: %d milliseconds", stateType, blockTimeDiff)
			} else {
				logger.WarningComponent("core", "Invalid %s state block time difference: %d milliseconds", stateType, blockTimeDiff)
			}
		}
	}
	lastBlockTimes.Set(stateType, parsedTime)

	// keep track of block heights for cleanup
	var blockHeights []int64
	blockHeightsIface, exists := blockHeightsByState.Get(stateType)
	if exists {
		blockHeights = blockHeightsIface.([]int64)
	}
	blockHeights = append(blockHeights, int64(height))

	// Cleanup old entries if we exceed maxBlockHistory
	if len(blockHeights) > maxBlockHistory {
		// Remove oldest entries
		blockHeights = blockHeights[len(blockHeights)-maxBlockHistory:]
	}
	blockHeightsByState.Set(stateType, blockHeights)
	lastBlockTimeMu.Unlock()

	// update metrics with state type label
	// only update block height from fast state to avoid conflicts
	if stateType == "fast" {
		metrics.SetBlockHeight(int64(height))
		metrics.SetLatestBlockTime(parsedTime.Unix())
		quality.OnBlock(parsedTime)
	}

	// record apply duration with state type label
	metrics.RecordApplyDurationWithLabel(applyDurationMs, stateType)

	logger.DebugComponent("core", "Updated %s state metrics: height=%.0f, apply_duration=%.6f, block_time=%s UTC, begin_block_wall_time=%s",
		stateType, height, applyDuration, parsedTime.Format(time.RFC3339), beginBlockWallTime)

	return nil
}

func monitorLegacyBlockState(ctx context.Context, cfg config.Config, _ chan<- error) {
	blockTimeDir := filepath.Join(cfg.NodeHome, "data", "block_times")
	logger.InfoComponent("core", "Starting legacy block monitor for directory: %s", blockTimeDir)

	t := streamTailer{stream: "block_times", component: "core", dir: blockTimeDir}
	t.run(ctx, func(line []byte) error {
		return parseLegacyBlockTimeLine(ctx, string(line))
	})
}

// for backward compatibility
func parseLegacyBlockTimeLine(ctx context.Context, line string) error {
	var data map[string]any
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return fmt.Errorf("error parsing block time line: %w", err)
	}

	height, ok := data["height"].(float64)
	if !ok {
		return fmt.Errorf("height not found or not a number")
	}

	blockTime, ok := data["block_time"].(string)
	if !ok {
		return fmt.Errorf("block time not found or not a string")
	}

	applyDuration, ok := data["apply_duration"].(float64)
	if !ok {
		return fmt.Errorf("apply duration not found or not a number")
	}

	// convert applyDuration from seconds to milliseconds
	applyDurationMs := applyDuration * 1000

	// parse block_time to Unix timestamp
	parsedTime, err := time.Parse(blockTimeLayout, blockTime)
	if err != nil {
		return fmt.Errorf("error parsing block time: %w", err)
	}

	// assume the time is in UTC if no timezone is specified
	parsedTime = parsedTime.UTC()

	// calculate block time difference (use "legacy" as key)
	lastBlockTimeMu.Lock()
	lastTimeIface, exists := lastBlockTimes.Get("legacy")
	if exists {
		lastTime := lastTimeIface.(time.Time)
		if !lastTime.IsZero() {
			blockTimeDiff := parsedTime.Sub(lastTime).Milliseconds()
			if blockTimeDiff > 0 {
				metrics.RecordBlockTime(float64(blockTimeDiff))
				logger.DebugComponent("core", "Block time difference: %d milliseconds", blockTimeDiff)
			} else {
				logger.WarningComponent("core", "Invalid block time difference: %d milliseconds", blockTimeDiff)
			}
		}
	}
	lastBlockTimes.Set("legacy", parsedTime)
	quality.OnBlock(parsedTime)

	// keep track of block heights for cleanup
	var blockHeights []int64
	blockHeightsIface, exists := blockHeightsByState.Get("legacy")
	if exists {
		blockHeights = blockHeightsIface.([]int64)
	}
	blockHeights = append(blockHeights, int64(height))

	// cleanup old entries if we exceed maxBlockHistory
	if len(blockHeights) > maxBlockHistory {
		// remove oldest entries
		blockHeights = blockHeights[len(blockHeights)-maxBlockHistory:]
	}
	blockHeightsByState.Set("legacy", blockHeights)
	lastBlockTimeMu.Unlock()

	// update metrics without labels (backward compatible)
	metrics.SetBlockHeight(int64(height))
	metrics.RecordApplyDuration(applyDurationMs)
	metrics.SetLatestBlockTime(parsedTime.Unix())

	logger.DebugComponent("core", "Updated metrics: height=%.0f, apply_duration=%.6f, block_time=%s UTC",
		height, applyDuration, parsedTime.Format(time.RFC3339))

	return nil
}
