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
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

const (
	gossipPollInterval    = 30 * time.Second
	incomingPeerActiveTTL = 5 * time.Minute
	childPeerStaleTTL     = 10 * time.Minute
)

type GossipMonitor struct {
	config          *config.Config
	gossipDir       string
	lastFile        string
	lastOffset      int64
	peerLastSeen    map[string]time.Time // tier 1: track active incoming peers
	knownChildPeers map[string]childPeerState
	registerPeer    func(string, peermon.PeerDirection)
}

type PeerInfo struct {
	IP string `json:"Ip"`
}

type PeerStatus struct {
	Verified        bool `json:"verified"`
	ConnectionCount int  `json:"connection_count"`
}

type childPeerState struct {
	lastSeen time.Time
	verified bool
}

func NewGossipMonitor(cfg *config.Config, registerPeer func(string, peermon.PeerDirection)) *GossipMonitor {
	return &GossipMonitor{
		config:          cfg,
		gossipDir:       filepath.Join(cfg.NodeHome, "data", "node_logs", "gossip_rpc", "hourly"),
		peerLastSeen:    make(map[string]time.Time),
		knownChildPeers: make(map[string]childPeerState),
		registerPeer:    registerPeer,
	}
}

func StartGossipMonitor(ctx context.Context, cfg *config.Config, errCh chan<- error, registerPeer func(string, peermon.PeerDirection)) {
	m := NewGossipMonitor(cfg, registerPeer)

	if _, err := os.Stat(m.gossipDir); os.IsNotExist(err) {
		logger.InfoComponent("gossip", "Gossip RPC directory not found, monitoring disabled: %s", m.gossipDir)
		return
	}

	logger.InfoComponent("gossip", "Starting gossip monitor")
	m.monitorGossipLogs(ctx, errCh)
}

func (m *GossipMonitor) monitorGossipLogs(ctx context.Context, errCh chan<- error) {
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// process immediately on startup
	if filePath, err := m.getLatestGossipLogFile(); err == nil && filePath != "" {
		logger.InfoComponent("gossip", "First run: processing gossip file %s", filePath)
		m.lastFile = filePath
		if newOffset, err := m.processGossipFile(filePath, 0); err != nil {
			logger.ErrorComponent("gossip", "Initial processing error: %v", err)
		} else {
			m.lastOffset = newOffset
		}
	}

	for {
		select {
		case <-ctx.Done():
			logger.InfoComponent("gossip", "Gossip monitor shutting down")
			return
		case <-ticker.C:
			filePath, err := m.getLatestGossipLogFile()
			if err != nil {
				logger.ErrorComponent("gossip", "Error getting latest gossip file: %v", err)
				continue
			}

			if filePath == "" {
				continue
			}

			if filePath != m.lastFile {
				logger.InfoComponent("gossip", "Switching to new gossip file: %s", filePath)
				m.lastFile = filePath
				m.lastOffset = 0
			}

			newOffset, err := m.processGossipFile(filePath, m.lastOffset)
			if err != nil {
				logger.ErrorComponent("gossip", "Error processing gossip file: %v", err)
				select {
				case errCh <- fmt.Errorf("gossip monitor: %w", err):
				case <-ctx.Done():
					return
				}
			} else {
				m.lastOffset = newOffset
			}
		}
	}
}

func (m *GossipMonitor) processGossipFile(filePath string, offset int64) (int64, error) {
	if filePath == "" {
		return offset, fmt.Errorf("empty file path")
	}

	var verifiedCount, unverifiedCount int64
	var lastUpdateTime time.Time
	currentPeers := make(map[string]PeerStatus)

	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		var entry []json.RawMessage
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}

		if len(entry) != 2 {
			return
		}

		var timestamp string
		if err := json.Unmarshal(entry[0], &timestamp); err != nil {
			return
		}

		entryTime, err := time.Parse("2006-01-02T15:04:05.999999999", timestamp)
		if err != nil {
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
		case "child_peers status":
			verifiedCount, unverifiedCount = m.processChildPeers(eventData[1], currentPeers)
			lastUpdateTime = entryTime

		case "incoming request":
			m.processIncomingRequest(eventData, entryTime)
		}
	})
	if err != nil {
		return offset, fmt.Errorf("failed to tail gossip file: %w", err)
	}

	// update aggregate child peer metrics
	if !lastUpdateTime.IsZero() {
		metrics.SetP2PNonValPeerConnections(true, verifiedCount)
		metrics.SetP2PNonValPeerConnections(false, unverifiedCount)
		metrics.SetP2PNonValPeersTotal(verifiedCount + unverifiedCount)

		// tier 2: mark absent child peers and age out stale ones
		m.updateChildPeerState(currentPeers)

		logger.DebugComponent("gossip", "Updated non-validator peer metrics: verified=%d, unverified=%d, total=%d",
			verifiedCount, unverifiedCount, verifiedCount+unverifiedCount)
	}

	// tier 1: compute active incoming peers
	m.updateActivePeers()

	return newOffset, nil
}

// processChildPeers parses the child_peers status peer list and sets per-peer metrics.
// Returns aggregate verified/unverified counts.
func (m *GossipMonitor) processChildPeers(raw json.RawMessage, currentPeers map[string]PeerStatus) (verified, unverified int64) {
	var peerList [][]json.RawMessage
	if err := json.Unmarshal(raw, &peerList); err != nil {
		return 0, 0
	}

	for _, peer := range peerList {
		if len(peer) != 2 {
			continue
		}

		var info PeerInfo
		if err := json.Unmarshal(peer[0], &info); err != nil {
			continue
		}

		var status PeerStatus
		if err := json.Unmarshal(peer[1], &status); err != nil {
			continue
		}

		if status.Verified {
			verified++
		} else {
			unverified++
		}

		if prev, exists := m.knownChildPeers[info.IP]; exists && prev.verified != status.Verified {
			metrics.RemoveChildPeerConnected(info.IP, prev.verified)
		}

		// tier 2: per-peer detail
		metrics.SetChildPeerConnected(info.IP, status.Verified, true)
		metrics.SetChildPeerConnections(info.IP, status.ConnectionCount)
		if m.registerPeer != nil {
			m.registerPeer(info.IP, peermon.Outbound)
		}
		currentPeers[info.IP] = status
	}

	return verified, unverified
}

// processIncomingRequest handles an "incoming request" event.
func (m *GossipMonitor) processIncomingRequest(eventData []json.RawMessage, entryTime time.Time) {
	if len(eventData) < 2 {
		return
	}

	var ipPort string
	if err := json.Unmarshal(eventData[1], &ipPort); err != nil {
		return
	}

	peerIP, _, err := net.SplitHostPort(ipPort)
	if err != nil {
		peerIP = ipPort // fallback: use as-is if no port
	}

	metrics.IncrementIncomingRequests(peerIP)
	metrics.SetIncomingPeerLastSeen(peerIP, float64(entryTime.Unix()))
	if m.registerPeer != nil {
		m.registerPeer(peerIP, peermon.Inbound)
	}
	m.peerLastSeen[peerIP] = entryTime
}

// updateChildPeerState marks absent child peers as disconnected and removes stale entries.
func (m *GossipMonitor) updateChildPeerState(currentPeers map[string]PeerStatus) {
	now := time.Now()

	// update known peers with current presence
	for ip, status := range currentPeers {
		m.knownChildPeers[ip] = childPeerState{
			lastSeen: now,
			verified: status.Verified,
		}
	}

	// mark absent peers as disconnected, remove stale ones
	for ip, state := range m.knownChildPeers {
		if _, ok := currentPeers[ip]; ok {
			continue
		}
		if now.Sub(state.lastSeen) > childPeerStaleTTL {
			metrics.RemoveChildPeerConnected(ip, state.verified)
			metrics.RemoveChildPeerConnections(ip)
			delete(m.knownChildPeers, ip)
		} else {
			metrics.SetChildPeerConnected(ip, state.verified, false)
			metrics.SetChildPeerConnections(ip, 0)
		}
	}
}

// updateActivePeers computes the count of incoming peers seen within the active TTL.
func (m *GossipMonitor) updateActivePeers() {
	cutoff := time.Now().Add(-incomingPeerActiveTTL)
	active := int64(0)

	for ip, lastSeen := range m.peerLastSeen {
		if lastSeen.After(cutoff) {
			active++
		} else {
			delete(m.peerLastSeen, ip)
			metrics.RemoveIncomingPeerLastSeen(ip)
		}
	}

	metrics.SetIncomingPeersActive(active)
}

// getLatestHourlyLogFile returns the latest log file in an hourly directory structure.
func getLatestHourlyLogFile(baseDir string) (string, error) {
	today := time.Now().Format("20060102")
	todayDir := filepath.Join(baseDir, today)

	if _, err := os.Stat(todayDir); os.IsNotExist(err) {
		return utils.GetLatestFile(baseDir)
	}

	return utils.GetLatestFile(todayDir)
}

func (m *GossipMonitor) getLatestGossipLogFile() (string, error) {
	return getLatestHourlyLogFile(m.gossipDir)
}
