package monitors

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

// timeLayout matches the tcp_traffic line timestamp format.
const timeLayout = "2006-01-02T15:04:05.999999999"

type OutboundPeersMonitor struct {
	dir          string
	tail         tailState
	registerPeer func(string, peermon.PeerDirection)
	seen         map[string]struct{}
	lastLineTS   time.Time

	addTrafficVolume func(peerIP, direction string, volume float64)
	addActiveSeconds func(peerIP string, seconds float64)
}

func NewOutboundPeersMonitor(cfg *config.Config, registerPeer func(string, peermon.PeerDirection)) *OutboundPeersMonitor {
	return &OutboundPeersMonitor{
		dir:              filepath.Join(cfg.NodeHome, "data", "tcp_traffic", "hourly"),
		registerPeer:     registerPeer,
		seen:             make(map[string]struct{}),
		addTrafficVolume: metrics.AddPeerTrafficVolume,
		addActiveSeconds: metrics.AddPeerActiveSeconds,
	}
}

func StartOutboundPeersMonitor(ctx context.Context, cfg *config.Config, registerPeer func(string, peermon.PeerDirection)) {
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
	if filePath, err := utils.LatestFile(m.dir); err == nil && filePath != "" {
		m.tail.path = filePath
		logger.InfoComponent("gossip", "Outbound peer monitor processing %s", filePath)
		if newOffset, err := m.processFile(filePath, 0, true); err != nil {
			logger.DebugComponent("gossip", "Error processing tcp_traffic: %v", err)
		} else {
			m.tail.offset = newOffset
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
	filePath, err := utils.LatestFile(m.dir)
	if err != nil || filePath == "" {
		return
	}

	if filePath != m.tail.path {
		logger.InfoComponent("gossip", "Outbound peer monitor switching to %s", filePath)
	}

	err = m.tail.poll(filePath, func(path string, offset int64) (int64, error) {
		return m.processFile(path, offset, false)
	})
	if err != nil {
		logger.DebugComponent("gossip", "Error reading tcp_traffic: %v", err)
	}
}

type peerEntry struct {
	ip  string
	dir peermon.PeerDirection
}

// processFile reads tcp_traffic lines, extracts peer IPs with direction, and
// accumulates per-peer traffic volume and active-seconds counters. During the
// startup seed pass (seeding=true), counter emission is suppressed to avoid
// double-counting samples Prometheus already recorded before the restart;
// peer discovery and lastLineTS tracking still happen.
// Format: ["timestamp",[[["In"|"Out","IP",port],bytes], ...]]
func (m *OutboundPeersMonitor) processFile(filePath string, offset int64, seeding bool) (int64, error) {
	batch := make(map[peerEntry]struct{})

	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		var entry [2]json.RawMessage
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}

		var flows []json.RawMessage
		if err := json.Unmarshal(entry[1], &flows); err != nil {
			return
		}

		volumes := make(map[peerEntry]float64)
		active := make(map[string]struct{})

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
			var dirStr, ip string
			if err := json.Unmarshal(key[0], &dirStr); err != nil {
				continue
			}
			if err := json.Unmarshal(key[1], &ip); err != nil {
				continue
			}
			dir := trafficDirection(dirStr)
			batch[peerEntry{ip, dir}] = struct{}{}

			var volume float64
			if err := json.Unmarshal(pair[1], &volume); err != nil {
				continue
			}
			volumes[peerEntry{ip, dir}] += volume
			if volume > 0 {
				active[ip] = struct{}{}
			}
		}

		var ts time.Time
		var tsOK bool
		var timestamp string
		if err := json.Unmarshal(entry[0], &timestamp); err == nil {
			if parsed, err := time.Parse(timeLayout, timestamp); err == nil {
				ts = parsed
				tsOK = true
			}
		}

		if !seeding {
			if tsOK && !m.lastLineTS.IsZero() {
				delta := ts.Sub(m.lastLineTS).Seconds()
				delta = clampDelta(delta)
				for ip := range active {
					m.addActiveSeconds(ip, delta)
				}
			}
			for pe, volume := range volumes {
				m.addTrafficVolume(pe.ip, string(pe.dir), volume)
			}
		}

		if tsOK {
			m.lastLineTS = ts
		}
	})
	if err != nil {
		return offset, err
	}

	for pe := range batch {
		m.register(pe.ip, pe.dir)
	}

	return newOffset, nil
}

func clampDelta(seconds float64) float64 {
	if seconds < 0 {
		return 0
	}
	if seconds > maxAccountGapSeconds {
		return maxAccountGapSeconds
	}
	return seconds
}

func (m *OutboundPeersMonitor) register(ip string, dir peermon.PeerDirection) {
	if _, ok := m.seen[ip]; !ok {
		m.seen[ip] = struct{}{}
		logger.InfoComponent("gossip", "Discovered peer %s from tcp_traffic", ip)
	}
	m.registerPeer(ip, dir)
}

func trafficDirection(s string) peermon.PeerDirection {
	switch s {
	case "Out":
		return peermon.Outbound
	case "In":
		return peermon.Inbound
	default:
		return peermon.Unknown
	}
}
