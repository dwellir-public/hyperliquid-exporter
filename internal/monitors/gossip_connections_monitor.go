package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

type GossipConnectionsMonitor struct {
	config       *config.Config
	dir          string
	lastFile     string
	lastOffset   int64
	registerPeer func(string)
}

func NewGossipConnectionsMonitor(cfg *config.Config, registerPeer func(string)) *GossipConnectionsMonitor {
	return &GossipConnectionsMonitor{
		config:       cfg,
		dir:          filepath.Join(cfg.NodeHome, "data", "node_logs", "gossip_connections", "hourly"),
		registerPeer: registerPeer,
	}
}

func StartGossipConnectionsMonitor(ctx context.Context, cfg *config.Config, errCh chan<- error, registerPeer func(string)) {
	m := NewGossipConnectionsMonitor(cfg, registerPeer)

	if _, err := os.Stat(m.dir); os.IsNotExist(err) {
		logger.InfoComponent("gossip", "Gossip connections directory not found, monitoring disabled: %s", m.dir)
		return
	}

	logger.InfoComponent("gossip", "Starting gossip connections monitor")
	m.monitor(ctx, errCh)
}

func (m *GossipConnectionsMonitor) monitor(ctx context.Context, errCh chan<- error) {
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// process immediately on startup
	if filePath, err := getLatestHourlyLogFile(m.dir); err == nil && filePath != "" {
		logger.InfoComponent("gossip", "First run: processing gossip connections file %s", filePath)
		m.lastFile = filePath
		if newOffset, err := m.processFile(filePath, 0); err != nil {
			logger.ErrorComponent("gossip", "Initial gossip connections processing error: %v", err)
		} else {
			m.lastOffset = newOffset
		}
	}

	for {
		select {
		case <-ctx.Done():
			logger.InfoComponent("gossip", "Gossip connections monitor shutting down")
			return
		case <-ticker.C:
			filePath, err := getLatestHourlyLogFile(m.dir)
			if err != nil {
				logger.ErrorComponent("gossip", "Error getting latest gossip connections file: %v", err)
				continue
			}

			if filePath != m.lastFile && filePath != "" {
				logger.InfoComponent("gossip", "Switching to new gossip connections file: %s", filePath)
				m.lastFile = filePath
				m.lastOffset = 0
			}

			newOffset, err := m.processFile(filePath, m.lastOffset)
			if err != nil {
				logger.ErrorComponent("gossip", "Error processing gossip connections file: %v", err)
				select {
				case errCh <- fmt.Errorf("gossip connections monitor: %w", err):
				case <-ctx.Done():
					return
				}
			} else {
				m.lastOffset = newOffset
			}
		}
	}
}

func (m *GossipConnectionsMonitor) processFile(filePath string, offset int64) (int64, error) {
	if filePath == "" {
		return offset, fmt.Errorf("empty file path")
	}

	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		var entry []json.RawMessage
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}

		if len(entry) != 2 {
			return
		}

		var eventData []json.RawMessage
		if err := json.Unmarshal(entry[1], &eventData); err != nil {
			return
		}

		if len(eventData) < 2 {
			return
		}

		var eventType string
		if err := json.Unmarshal(eventData[0], &eventType); err != nil {
			return
		}

		switch eventType {
		case "handle_stream_connection":
			if len(eventData) < 3 {
				return
			}
			var ipPort, connType string
			if err := json.Unmarshal(eventData[1], &ipPort); err != nil {
				return
			}
			if err := json.Unmarshal(eventData[2], &connType); err != nil {
				return
			}
			peerIP, _, err := net.SplitHostPort(ipPort)
			if err != nil {
				peerIP = ipPort
			}
			metrics.IncrementStreamConnections(peerIP, connType)
			if m.registerPeer != nil {
				m.registerPeer(peerIP)
			}

		case "verified gossip rpc":
			var peer struct {
				IP string `json:"Ip"`
			}
			if err := json.Unmarshal(eventData[1], &peer); err != nil {
				return
			}
			metrics.IncrementVerifications(peer.IP)
			if m.registerPeer != nil {
				m.registerPeer(peer.IP)
			}
		}
	})
	if err != nil {
		return offset, fmt.Errorf("failed to tail gossip connections file: %w", err)
	}

	return newOffset, nil
}
