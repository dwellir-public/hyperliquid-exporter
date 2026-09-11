package monitors

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/actiontypes"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/replica"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

// replica verification stats
type ReplicaVerificationStats struct {
	LinesProcessed      int64
	BlocksProcessed     int64
	ActionsProcessed    int64
	OperationsProcessed int64
	ParseErrors         int64
	LastProcessedAt     time.Time
	LastBlockHeight     int64
	ActionCounts        map[string]int64
}

// replica monitor
const replicaStream = "replica_cmds"

type ReplicaMonitor struct {
	parser     *replica.Parser
	dataDir    string
	bufferSize int
	mu         sync.RWMutex

	// Metrics tracking
	lastRound int64
	lastTime  time.Time

	// Verification tracking
	verificationStats ReplicaVerificationStats
}

// creates a new streaming replica monitor
func NewReplicaMonitor(dataDir string, bufferSize int) *ReplicaMonitor {
	return &ReplicaMonitor{
		parser:     replica.NewParser(bufferSize),
		dataDir:    dataDir,
		bufferSize: bufferSize * 1024 * 1024, // Convert MB to bytes
		verificationStats: ReplicaVerificationStats{
			ActionCounts: make(map[string]int64),
		},
	}
}

// starts monitoring replica files using streaming approach
func (m *ReplicaMonitor) Start(ctx context.Context) error {
	logger.InfoComponent("replica", "Starting streaming replica monitor, dataDir: %s", m.dataDir)

	safego.Go("replica", func() { m.streamLoop(ctx) })
	return nil
}

// continuously streams from the latest replica file
func (m *ReplicaMonitor) streamLoop(ctx context.Context) {
	// wait a short time at startup to ensure signer mappings are populated
	// prevents early blocks from having incorrect validator labels
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
		logger.DebugComponent("replica", "Initial startup delay complete, beginning processing")
	}

	blocksProcessed := 0
	var totalParseTime float64
	var parseCount int

	t := streamTailer{
		stream:    replicaStream,
		component: "replica",
		dir:       m.dataDir,
		bufSize:   m.bufferSize,
		idle: func() {
			if blocksProcessed > 0 {
				logger.DebugComponent("replica", "Processed %d blocks, waiting for more data", blocksProcessed)
				blocksProcessed = 0
			}
			if parseCount > 0 {
				metrics.SetReplicaParseDuration(totalParseTime / float64(parseCount))
			}
		},
	}
	t.run(ctx, func(line []byte) error {
		m.mu.Lock()
		m.verificationStats.LinesProcessed++
		m.mu.Unlock()

		parseStart := time.Now()
		block, err := m.parser.ParseBlockFromLine(line)
		totalParseTime += time.Since(parseStart).Seconds()
		parseCount++
		if err != nil {
			m.mu.Lock()
			m.verificationStats.ParseErrors++
			m.mu.Unlock()
			return err
		}

		blockMetrics, err := m.parser.ExtractMetrics(block)
		m.parser.ReturnBlock(block)
		if err != nil {
			return err
		}

		m.processBlock(blockMetrics)
		blocksProcessed++
		if blocksProcessed%100 == 0 {
			logger.InfoComponent("replica", "Processed %d blocks so far", blocksProcessed)
		}
		return nil
	})
}

// processes metrics from a single block
func (m *ReplicaMonitor) processBlock(block *replica.BlockMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// update last processed info
	m.lastRound = block.Round
	m.lastTime = block.Time

	// update verification stats
	m.verificationStats.BlocksProcessed++
	m.verificationStats.LastProcessedAt = time.Now()
	m.verificationStats.LastBlockHeight = block.Round
	m.verificationStats.ActionsProcessed += int64(block.TotalActions)
	m.verificationStats.OperationsProcessed += int64(block.TotalOperations)

	// update counters
	metrics.IncCoreBlocksProcessed()
	metrics.IncCoreRoundsProcessed()

	// update proposer counter
	if block.Proposer != "" {
		metrics.IncrementProposerCounter(block.Proposer)
	}

	// update action counters
	orderCount := int64(0)
	cancelCount := int64(0)
	for actionType, count := range block.ActionCounts {
		metrics.IncCoreTxTotal(actionType, int64(count))

		// track in verification stats
		m.verificationStats.ActionCounts[actionType] += int64(count)

		// track orders separately
		if actionType == replica.ActionTypeOrder || actionType == replica.ActionTypeTwapOrder {
			orderCount += int64(count)
		}
		// track cancels
		if actionType == replica.ActionTypeCancel || actionType == replica.ActionTypeCancelByCloid {
			cancelCount += int64(count)
		}
	}

	// update operation counters (new)
	for actionType, count := range block.OperationCounts {
		category := actiontypes.Category(actionType)
		metrics.IncCoreOperationsTotal(actionType, category, int64(count))
	}

	// update dedicated order counter
	if orderCount > 0 {
		metrics.IncCoreOrdersTotal(orderCount)
	}

	// update histogram
	metrics.ObserveCoreTxPerBlock(float64(block.TotalActions))

	// update operations histogram (new)
	metrics.ObserveCoreOperationsPerBlock(float64(block.TotalOperations))

	// update last processed metrics
	metrics.SetCoreLastProcessedRound(float64(block.Round))
	metrics.SetCoreLastProcessedTime(float64(block.Time.Unix()))

	// also update implementation-specific metrics
	metrics.SetReplicaLastProcessedRound(float64(block.Round))
	metrics.SetReplicaLastProcessedTime(float64(block.Time.Unix()))
}

// returns current statistics
func (m *ReplicaMonitor) GetStats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]any{
		"last_round": m.lastRound,
		"last_time":  m.lastTime,
	}

	return stats
}

// returns current verification stats
func (m *ReplicaMonitor) GetVerificationStats() ReplicaVerificationStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// create a copy to avoid race conditions
	statsCopy := m.verificationStats
	statsCopy.ActionCounts = make(map[string]int64)
	maps.Copy(statsCopy.ActionCounts, m.verificationStats.ActionCounts)

	return statsCopy
}
