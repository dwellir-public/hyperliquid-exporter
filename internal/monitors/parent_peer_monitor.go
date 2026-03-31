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
)

// ParentPeerMonitor identifies the node's primary upstream peer by analyzing
// tcp_traffic byte volumes. The peer delivering the most inbound data is the parent.
type ParentPeerMonitor struct {
	dir           string
	lastFile      string
	lastOffset    int64
	currentParent string
	parentSince   time.Time
	setParentPeer func(string)
}

func NewParentPeerMonitor(cfg *config.Config, setParentPeer func(string)) *ParentPeerMonitor {
	return &ParentPeerMonitor{
		dir:           filepath.Join(cfg.NodeHome, "data", "tcp_traffic", "hourly"),
		setParentPeer: setParentPeer,
	}
}

func StartParentPeerMonitor(ctx context.Context, cfg *config.Config, setParentPeer func(string)) {
	m := NewParentPeerMonitor(cfg, setParentPeer)

	if _, err := os.Stat(m.dir); os.IsNotExist(err) {
		logger.InfoComponent("parent-peer", "tcp_traffic directory not found, parent peer monitor disabled")
		return
	}

	logger.InfoComponent("parent-peer", "Starting parent peer monitor from tcp_traffic")
	m.monitor(ctx)
}

func (m *ParentPeerMonitor) monitor(ctx context.Context) {
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// seed from latest file on startup
	if filePath, err := getLatestHourlyLogFile(m.dir); err == nil && filePath != "" {
		m.lastFile = filePath
		logger.InfoComponent("parent-peer", "Processing %s", filePath)
		if newOffset, err := m.processFile(filePath, 0); err != nil {
			logger.DebugComponent("parent-peer", "Error processing tcp_traffic: %v", err)
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

func (m *ParentPeerMonitor) poll() {
	filePath, err := getLatestHourlyLogFile(m.dir)
	if err != nil || filePath == "" {
		return
	}

	if filePath != m.lastFile {
		logger.InfoComponent("parent-peer", "Switching to %s", filePath)
		m.lastFile = filePath
		m.lastOffset = 0
	}

	if _, err := m.processFile(filePath, m.lastOffset); err != nil {
		logger.DebugComponent("parent-peer", "Error reading tcp_traffic: %v", err)
	}
}

// processFile reads tcp_traffic lines and identifies the parent peer by max inbound bytes.
// Each line is an interval snapshot; the last line's winner is used as the most recent state.
func (m *ParentPeerMonitor) processFile(filePath string, offset int64) (int64, error) {
	var bestIP, runnerIP string
	var bestBytes, runnerBytes float64

	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		topIP, topBytes, secondIP, secondBytes := findTopInboundPeer(line)
		if topIP != "" {
			bestIP = topIP
			bestBytes = topBytes
			runnerIP = secondIP
			runnerBytes = secondBytes
		}
	})
	if err != nil {
		return offset, err
	}

	if bestIP != "" {
		if runnerIP != "" && runnerBytes > bestBytes*0.1 {
			logger.WarningComponent("parent-peer",
				"Ambiguous parent peer: %s (%.4f) vs runner-up %s (%.4f)",
				bestIP, bestBytes, runnerIP, runnerBytes)
		}
		m.updateParent(bestIP, bestBytes)
	}

	m.lastOffset = newOffset
	return newOffset, nil
}

// findTopInboundPeer parses a single tcp_traffic line and returns the top two "In" peers by bytes.
func findTopInboundPeer(line []byte) (topIP string, topBytes float64, secondIP string, secondBytes float64) {
	var entry [2]json.RawMessage
	if err := json.Unmarshal(line, &entry); err != nil {
		return
	}

	var flows []json.RawMessage
	if err := json.Unmarshal(entry[1], &flows); err != nil {
		return
	}

	for _, flow := range flows {
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
		if dirStr != "In" {
			continue
		}
		if err := json.Unmarshal(key[1], &ip); err != nil {
			continue
		}
		var bytes float64
		if err := json.Unmarshal(pair[1], &bytes); err != nil {
			continue
		}
		if bytes > topBytes {
			secondIP = topIP
			secondBytes = topBytes
			topBytes = bytes
			topIP = ip
		} else if bytes > secondBytes {
			secondBytes = bytes
			secondIP = ip
		}
	}
	return
}

func (m *ParentPeerMonitor) updateParent(ip string, bytes float64) {
	now := time.Now()

	if ip != m.currentParent {
		if m.currentParent != "" {
			metrics.RemoveParentPeer(m.currentParent)
			metrics.IncrementParentPeerSwitches()
			logger.InfoComponent("parent-peer", "Parent peer changed: %s -> %s", m.currentParent, ip)
		} else {
			logger.InfoComponent("parent-peer", "Initial parent peer identified: %s", ip)
		}
		m.currentParent = ip
		m.parentSince = now

		if m.setParentPeer != nil {
			m.setParentPeer(ip)
		}
	}

	metrics.SetParentPeer(ip)
	metrics.SetParentPeerTraffic(ip, bytes)
	metrics.SetParentPeerTenure(now.Sub(m.parentSince).Seconds())
}
