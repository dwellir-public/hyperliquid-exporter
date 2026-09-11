// Package monitors provides monitoring implementations for various Hyperliquid node data sources
package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

var (
	// track the last block time for calculating block time differences
	lastEVMBlockTime time.Time

	// block type metrics flag
	blockTypeMetricsEnabled bool
)

const evmStream = "evm_block_and_receipts"

// 1. Reads from evm_block_and_receipts files
// 2. Parses height, gas, fees, success, gas usage
// 3. Updates all EVM-related prometheus metrics
func StartEVMMonitor(ctx context.Context, cfg config.Config, errCh chan<- error) {
	// initialize config
	blockTypeMetricsEnabled = cfg.EVMBlockTypeMetrics

	logger.InfoComponent("evm", "EVM monitor config: blockTypeMetrics=%v", blockTypeMetricsEnabled)

	// wait for validator status to be determined
	time.Sleep(60 * time.Second)

	// log a warning if running EVM monitoring on a validator node
	if metrics.IsValidator() {
		logger.WarningComponent("evm", "Running EVM monitoring on a validator node - this may impact performance")
	}

	// use the new unified data source
	evmDataDir := filepath.Join(cfg.NodeHome, "data/evm_block_and_receipts/hourly")
	logger.InfoComponent("evm", "Starting unified EVM monitoring in directory: %s", evmDataDir)

	safego.Go("evm", func() {
		t := streamTailer{stream: evmStream, component: "evm", dir: evmDataDir}
		t.run(ctx, func(line []byte) error {
			return processEVMBlockAndReceiptsLine(string(line))
		})
	})
}

func processEVMBlockAndReceiptsLine(line string) error {
	var data []any
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return fmt.Errorf("error unmarshaling EVM data: %w", err)
	}

	if len(data) < 2 {
		return fmt.Errorf("invalid data format: expected at least 2 elements, got %d", len(data))
	}

	// element 0: ISO timestamp (e.g., "2025-05-27T12:00:00.602996317")
	timestampStr, ok := data[0].(string)
	if !ok {
		return fmt.Errorf("invalid timestamp format: expected string, got %T", data[0])
	}

	// hl-node writes the timestamp without a zone; parseVisorTime accepts
	// both that and RFC3339. A zero time falls back to the block header.
	timestamp, ok := parseVisorTime(timestampStr)
	if !ok {
		logger.Debug("failed to parse ISO timestamp %q, will extract from block", timestampStr)
	}

	blockData := data[1]

	var receiptsData any
	if len(data) >= 3 {
		receiptsData = data[2]
		logger.DebugComponent("evm", "Line has receipts data: %v", receiptsData != nil)
	} else {
		logger.DebugComponent("evm", "Line has no receipts data (only %d elements)", len(data))
	}

	blockType, err := processBlockData(blockData, timestamp)
	if err != nil {
		return fmt.Errorf("error processing block data: %w", err)
	}

	if receiptsData != nil {
		logger.DebugComponent("evm", "Processing receipts data")
		if err := processReceiptsData(receiptsData, blockType); err != nil {
			// Log but don't fail - receipts might be null for empty blocks
			logger.Debug("error processing receipts: %v", err)
		}
	} else {
		logger.DebugComponent("evm", "No receipts data to process")
	}

	return nil
}

func processBlockData(blockData any, isoTimestamp time.Time) (string, error) {
	blockMap, ok := blockData.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid block data format")
	}

	block, ok := blockMap["block"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing block field")
	}

	// support different block formats
	var blockContent map[string]any
	for _, v := range block {
		blockContent, ok = v.(map[string]any)
		if ok {
			break
		}
	}

	if blockContent == nil {
		return "", fmt.Errorf("no valid block content found")
	}

	// extract header info
	headerWrapper, ok := blockContent["header"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing header field")
	}

	header, ok := headerWrapper["header"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing inner header field")
	}

	blockNumberHex, ok := header["number"].(string)
	if !ok {
		return "", fmt.Errorf("missing block number")
	}

	blockNumber, err := strconv.ParseInt(strings.TrimPrefix(blockNumberHex, "0x"), 16, 64)
	if err != nil {
		return "", fmt.Errorf("invalid block number: %w", err)
	}

	// set block height metric
	metrics.SetEVMBlockHeight(blockNumber)

	// determine block type early so we can use it throughout
	var blockType string
	var gasLimit int64
	if gasLimitHex, ok := header["gasLimit"].(string); ok {
		gasLimit, err = strconv.ParseInt(strings.TrimPrefix(gasLimitHex, "0x"), 16, 64)
		if err == nil {
			// gas limit distribution
			metrics.RecordGasLimitDistribution(float64(gasLimit))

			// track high gas limit blocks
			if gasLimit >= 30_000_000 {
				metrics.IncrementHighGasLimitBlocks("30m")
				// extract gas used for high gas block tracking
				var gasUsed int64
				if gasUsedHex, ok := header["gasUsed"].(string); ok {
					gasUsed, _ = strconv.ParseInt(strings.TrimPrefix(gasUsedHex, "0x"), 16, 64)
				}
				metrics.SetLastHighGasBlock(blockNumber, gasLimit, gasUsed, isoTimestamp)
			}

			metrics.UpdateMaxGasLimit(gasLimit)

			if blockTypeMetricsEnabled {
				switch {
				case gasLimit == 3_000_000:
					blockType = "small"
				case gasLimit <= 5_000_000:
					blockType = "standard"
				case gasLimit >= 30_000_000:
					blockType = "high"
				default:
					blockType = "other"
					logger.DebugComponent("evm", "Unexpected gas limit: %d", gasLimit)
				}
				logger.DebugComponent("evm", "Block %d: gasLimit=%d, blockType=%s", blockNumber, gasLimit, blockType)
			}
		}
	}

	var blockTimestamp time.Time
	if !isoTimestamp.IsZero() {
		blockTimestamp = isoTimestamp
	} else {
		// fallback to hex timestamp from header
		if timestampHex, ok := header["timestamp"].(string); ok {
			ts, err := strconv.ParseInt(strings.TrimPrefix(timestampHex, "0x"), 16, 64)
			if err == nil {
				blockTimestamp = time.Unix(ts, 0).UTC()
			}
		}
	}

	if !blockTimestamp.IsZero() {
		// calculate block time difference
		if !lastEVMBlockTime.IsZero() {
			diffMs := blockTimestamp.Sub(lastEVMBlockTime).Milliseconds()
			if diffMs > 0 {
				metrics.RecordEVMBlockTime(float64(diffMs))
			}
		}
		lastEVMBlockTime = blockTimestamp
		metrics.SetEVMLatestBlockTime(blockTimestamp.Unix())
	}

	// extract gas metrics
	if gasUsedHex, ok := header["gasUsed"].(string); ok {
		gasUsed, err := strconv.ParseInt(strings.TrimPrefix(gasUsedHex, "0x"), 16, 64)
		if err == nil && gasLimit > 0 {
			// use the block type determined earlier
			if blockTypeMetricsEnabled && blockType != "" {
				metrics.SetEVMGasUsage(gasUsed, gasLimit, blockType)
			} else {
				metrics.SetEVMGasUsage(gasUsed, gasLimit)
			}
		}
	}

	// extract base fee
	if baseFeeHex, ok := header["baseFeePerGas"].(string); ok {
		baseFeeWei, err := strconv.ParseInt(strings.TrimPrefix(baseFeeHex, "0x"), 16, 64)
		if err == nil && baseFeeWei > 0 {
			baseFeeGwei := float64(baseFeeWei) / 1e9
			if blockTypeMetricsEnabled && blockType != "" {
				metrics.SetEVMBaseFeeGwei(baseFeeGwei, blockType)
			} else {
				metrics.SetEVMBaseFeeGwei(baseFeeGwei)
			}
		}
	}

	// process transactions
	if body, ok := blockContent["body"].(map[string]any); ok {
		if err := processTransactions(body, blockType); err != nil {
			logger.Debug("error processing transactions: %v", err)
		}
	}

	return blockType, nil
}

// extracts transaction metrics from the block body
func processTransactions(body map[string]any, blockType string) error {
	transactions, ok := body["transactions"].([]any)
	if !ok {
		return nil // No transactions in block
	}

	txCount := len(transactions)
	if txCount > 0 {
		if blockTypeMetricsEnabled && blockType != "" {
			metrics.RecordEVMTxPerBlock(txCount, blockType)
		} else {
			metrics.RecordEVMTxPerBlock(txCount)
		}
	}

	var maxPriorityFeeWei int64

	for _, tx := range transactions {
		txMap, ok := tx.(map[string]any)
		if !ok {
			continue
		}

		// extract transaction details
		if transaction, ok := txMap["transaction"].(map[string]any); ok {
			// process transaction type and details
			for txType, txData := range transaction {
				if blockTypeMetricsEnabled && blockType != "" {
					metrics.IncrementEVMTxType(txType, blockType)
				} else {
					metrics.IncrementEVMTxType(txType)
				}

				if txDataMap, ok := txData.(map[string]any); ok {
					// check for contract creation (empty or zero 'to' address)
					if to, ok := txDataMap["to"].(string); ok {
						addr := strings.ToLower(to)
						if addr == "" || strings.TrimPrefix(addr, "0x") == "" ||
							strings.TrimPrefix(addr, "0x") == strings.Repeat("0", 40) {
							if blockTypeMetricsEnabled && blockType != "" {
								metrics.IncrementEVMContractCreations(blockType)
							} else {
								metrics.IncrementEVMContractCreations()
							}
						}
					}

					// extract max priority fee for EIP-1559 transactions
					if txType == "Eip1559" {
						if maxPriorityFeeHex, ok := txDataMap["maxPriorityFeePerGas"].(string); ok {
							fee, err := strconv.ParseInt(strings.TrimPrefix(maxPriorityFeeHex, "0x"), 16, 64)
							if err == nil && fee > maxPriorityFeeWei {
								maxPriorityFeeWei = fee
							}
						}
					}
				}
			}
		}
	}

	// set max priority fee metric
	if maxPriorityFeeWei > 0 {
		maxPriorityFeeGwei := float64(maxPriorityFeeWei) / 1e9
		if blockTypeMetricsEnabled && blockType != "" {
			metrics.SetEVMMaxPriorityFeeGwei(maxPriorityFeeGwei, blockType)
		} else {
			metrics.SetEVMMaxPriorityFeeGwei(maxPriorityFeeGwei)
		}
	}

	return nil
}

func processReceiptsData(receiptsData any, blockType string) error {
	receiptsMap, ok := receiptsData.(map[string]any)
	if !ok {
		logger.DebugComponent("evm", "Receipts data is not a map: %T", receiptsData)
		return fmt.Errorf("invalid receipts format")
	}

	receipts, ok := receiptsMap["receipts"].([]any)
	if !ok {
		logger.DebugComponent("evm", "No receipts array in receipts map")
		return nil // No receipts
	}

	logger.DebugComponent("evm", "Found %d receipts (likely 0 due to null data)", len(receipts))

	return nil
}
