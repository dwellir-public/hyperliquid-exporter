package monitors

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
)

type OutboundPeersMonitor struct {
	dir          string
	lastFile     string
	lastOffset   int64
	registerPeer func(string)
	seen         map[string]struct{}
}

func NewOutboundPeersMonitor(cfg *config.Config, registerPeer func(string)) *OutboundPeersMonitor {
	return &OutboundPeersMonitor{
		dir:          filepath.Join(cfg.NodeHome, "data", "tcp_traffic", "hourly"),
		registerPeer: registerPeer,
		seen:         make(map[string]struct{}),
	}
}

func StartOutboundPeersMonitor(ctx context.Context, cfg *config.Config, registerPeer func(string)) {
	m := NewOutboundPeersMonitor(cfg, registerPeer)

	if _, err := os.Stat(m.dir); os.IsNotExist(err) {
		logger.InfoComponent("gossip", "tcp_traffic directory not found, outbound peer discovery disabled")
		return
	}

	logger.InfoComponent("gossip", "Starting outbound peer discovery from tcp_traffic")
	m.monitor(ctx)
}

func (m *OutboundPeersMonitor) monitor(ctx context.Context) {
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// process the latest entry immediately to seed peers on startup
	if filePath, err := getLatestHourlyLogFile(m.dir); err == nil && filePath != "" {
		m.lastFile = filePath
		logger.InfoComponent("gossip", "Outbound peer monitor processing %s", filePath)
		if newOffset, err := m.processFile(filePath, 0); err != nil {
			logger.DebugComponent("gossip", "Error processing tcp_traffic: %v", err)
		} else {
			m.lastOffset = newOffset
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll()
		}
	}
}

func (m *OutboundPeersMonitor) poll() {
	filePath, err := getLatestHourlyLogFile(m.dir)
	if err != nil || filePath == "" {
		return
	}

	if filePath != m.lastFile {
		logger.InfoComponent("gossip", "Outbound peer monitor switching to %s", filePath)
		m.lastFile = filePath
		m.lastOffset = 0
	}

	if _, err := m.processFile(filePath, m.lastOffset); err != nil {
		logger.DebugComponent("gossip", "Error reading tcp_traffic: %v", err)
	}
}

// processFile reads tcp_traffic lines and extracts peer IPs.
// Format: ["timestamp",[[["In"|"Out","IP",port],bytes], ...]]
func (m *OutboundPeersMonitor) processFile(filePath string, offset int64) (int64, error) {
	batch := make(map[string]struct{})

	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		var entry [2]json.RawMessage
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}

		var flows []json.RawMessage
		if err := json.Unmarshal(entry[1], &flows); err != nil {
			return
		}

		for _, flow := range flows {
			// each flow: [["In"|"Out", "IP", port], bytes]
			var pair [2]json.RawMessage
			if err := json.Unmarshal(flow, &pair); err != nil {
				continue
			}
			var key [3]json.RawMessage
			if err := json.Unmarshal(pair[0], &key); err != nil {
				continue
			}
			var ip string
			if err := json.Unmarshal(key[1], &ip); err != nil {
				continue
			}
			batch[ip] = struct{}{}
		}
	})
	if err != nil {
		return offset, err
	}

	for ip := range batch {
		m.register(ip)
	}

	m.lastOffset = newOffset
	return newOffset, nil
}

func (m *OutboundPeersMonitor) register(ip string) {
	if _, ok := m.seen[ip]; !ok {
		m.seen[ip] = struct{}{}
		logger.InfoComponent("gossip", "Discovered peer %s from tcp_traffic", ip)
	}
	m.registerPeer(ip)
}
