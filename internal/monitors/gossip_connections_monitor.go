package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"strings"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

type GossipConnectionsMonitor struct {
	config       *config.Config
	dir          string
	tail         tailState
	registerPeer func(string, peermon.PeerDirection)
}

func NewGossipConnectionsMonitor(cfg *config.Config, registerPeer func(string, peermon.PeerDirection)) *GossipConnectionsMonitor {
	return &GossipConnectionsMonitor{
		config:       cfg,
		dir:          filepath.Join(cfg.NodeHome, "data", "node_logs", "gossip_connections", "hourly"),
		registerPeer: registerPeer,
	}
}

func StartGossipConnectionsMonitor(ctx context.Context, cfg *config.Config, errCh chan<- error, registerPeer func(string, peermon.PeerDirection)) {
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

	// seed from the current hour on startup for peer discovery; counters are
	// suppressed because Prometheus already recorded those samples
	if filePath, err := utils.LatestFile(m.dir); err == nil && filePath != "" {
		logger.InfoComponent("gossip", "First run: processing gossip connections file %s", filePath)
		m.tail.path = filePath
		if newOffset, err := m.processFile(filePath, 0, true); err != nil {
			logger.ErrorComponent("gossip", "Initial gossip connections processing error: %v", err)
		} else {
			m.tail.offset = newOffset
		}
	}

	for {
		select {
		case <-ctx.Done():
			logger.InfoComponent("gossip", "Gossip connections monitor shutting down")
			return
		case <-ticker.C:
			filePath, err := utils.LatestFile(m.dir)
			if err != nil {
				logger.ErrorComponent("gossip", "Error getting latest gossip connections file: %v", err)
				continue
			}

			if filePath == "" {
				continue
			}

			if filePath != m.tail.path {
				logger.InfoComponent("gossip", "Switching to new gossip connections file: %s", filePath)
			}

			err = m.tail.poll(filePath, func(path string, offset int64) (int64, error) {
				return m.processFile(path, offset, false)
			})
			if err != nil {
				logger.ErrorComponent("gossip", "Error processing gossip connections file: %v", err)
				select {
				case errCh <- fmt.Errorf("gossip connections monitor: %w", err):
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// processFile tails filePath from offset. With seeding set, counters are not
// incremented but peers are still registered (startup replay).
func (m *GossipConnectionsMonitor) processFile(filePath string, offset int64, seeding bool) (int64, error) {
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
			if !seeding {
				metrics.IncrementStreamConnections(peerIP, connType)
			}
			if m.registerPeer != nil {
				m.registerPeer(peerIP, connTypeToDirection(connType))
			}

		case "verified gossip rpc":
			var peer struct {
				IP string `json:"Ip"`
			}
			if err := json.Unmarshal(eventData[1], &peer); err != nil {
				return
			}
			if !seeding {
				metrics.IncrementVerifications(peer.IP)
			}
			if m.registerPeer != nil {
				m.registerPeer(peer.IP, peermon.Unknown)
			}
		}
	})
	if err != nil {
		return offset, fmt.Errorf("failed to tail gossip connections file: %w", err)
	}

	return newOffset, nil
}

func connTypeToDirection(connType string) peermon.PeerDirection {
	switch strings.ToLower(connType) {
	case "inbound":
		return peermon.Inbound
	case "outbound":
		return peermon.Outbound
	default:
		return peermon.Unknown
	}
}
