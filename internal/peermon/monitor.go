package peermon

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	probeInterval     = 1 * time.Minute
	initialProbeDelay = 2 * time.Second
	maxConcurrent     = 10
)

var peerProbeTimeout = 5 * time.Second

var (
	removePeerMetrics = metrics.RemovePeerMetrics
	setPeerCount      = metrics.SetPeerMonitoredCount
)

// Monitor probes known peers for TCP latency and exposes metrics.
type Monitor struct {
	peers        *PeerSet
	probeRunning atomic.Bool
	probeWG      sync.WaitGroup
	parentIP     atomic.Value // string: current parent peer IP
	runProbe     func(context.Context, []Peer)
}

// New creates a Monitor that persists peers under dataDir.
func New(dataDir string) *Monitor {
	m := &Monitor{
		peers: NewPeerSet(dataDir),
	}
	m.runProbe = m.probeAll
	return m
}

// SetParentPeer records the current parent peer IP for dedicated latency tracking.
// Safe to call from any goroutine.
func (m *Monitor) SetParentPeer(ip string) {
	m.parentIP.Store(ip)
}

// Register adds a peer IP to the monitored set with a direction.
// Safe to call from any goroutine — the underlying PeerSet is mutex-protected.
func (m *Monitor) Register(ip string, dir PeerDirection) {
	evictedIP, evicted := m.peers.Register(ip, dir)
	if evicted {
		removePeerMetrics(evictedIP)
	}
	setPeerCount(int64(m.peers.Len()))
}

// Start runs the monitor loop until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context, errCh chan<- error) {
	if err := m.peers.Load(); err != nil {
		logger.WarningComponent("peer-latency", "Failed to load peers from disk: %v", err)
	} else if n := m.peers.Len(); n > 0 {
		logger.InfoComponent("peer-latency", "Loaded %d peers from disk", n)
	}
	setPeerCount(int64(m.peers.Len()))

	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()

	initialProbe := time.After(initialProbeDelay)

	for {
		select {
		case <-ctx.Done():
			m.probeWG.Wait()
			if err := m.peers.Save(); err != nil {
				logger.WarningComponent("peer-latency", "Failed to save peers on shutdown: %v", err)
			}
			return

		case <-initialProbe:
			initialProbe = nil
			m.saveIfDirty()
			if _, skipped := m.startProbeCycle(ctx); skipped {
				logger.WarningComponent("peer-latency", "Previous probe cycle still running, skipping initial probe")
			}

		case <-ticker.C:
			m.saveIfDirty()
			if _, skipped := m.startProbeCycle(ctx); skipped {
				logger.WarningComponent("peer-latency", "Previous probe cycle still running, skipping tick")
			}
		}
	}
}

func (m *Monitor) saveIfDirty() {
	if !m.peers.Dirty() {
		return
	}
	if err := m.peers.Save(); err != nil {
		logger.WarningComponent("peer-latency", "Failed to save peers: %v", err)
	}
}

func (m *Monitor) startProbeCycle(ctx context.Context) (started bool, skipped bool) {
	if !m.probeRunning.CompareAndSwap(false, true) {
		return false, true
	}

	peers := m.peers.All()
	setPeerCount(int64(m.peers.Len()))
	if len(peers) == 0 {
		m.probeRunning.Store(false)
		return false, false
	}

	m.probeWG.Add(1)
	go func() {
		defer m.probeWG.Done()
		defer m.probeRunning.Store(false)
		m.runProbe(ctx, peers)
	}()

	return true, false
}

func (m *Monitor) probeAll(ctx context.Context, peers []Peer) {
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, p := range peers {
		select {
		case <-ctx.Done():
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(peer Peer) {
			defer wg.Done()
			defer func() { <-sem }()

			probeCtx, cancel := context.WithTimeout(ctx, peerProbeTimeout)
			defer cancel()

			dirs := directionsOrUnknown(peer.Directions)

			metrics.IncrementPeerProbes(peer.IP)
			result := Probe(probeCtx, peer.IP, peer.Port)

			if result.Reachable {
				latencyMs := float64(result.Latency.Milliseconds())
				for _, dir := range dirs {
					metrics.SetPeerLatency(peer.IP, dir, latencyMs)
					metrics.SetPeerReachable(peer.IP, dir, true)
				}
				if parentIP, ok := m.parentIP.Load().(string); ok && parentIP == peer.IP {
					metrics.SetParentPeerLatency(peer.IP, latencyMs)
				}
				m.peers.UpdatePort(peer.IP, result.Port)
			} else {
				for _, dir := range dirs {
					metrics.RemovePeerLatency(peer.IP, dir)
					metrics.SetPeerReachable(peer.IP, dir, false)
				}
				metrics.IncrementPeerProbeFailures(peer.IP)
			}
		}(p)
	}

	wg.Wait()
}

// directionsOrUnknown returns the peer's directions as strings,
// falling back to ["unknown"] if none are set.
func directionsOrUnknown(dirs map[PeerDirection]bool) []string {
	if len(dirs) == 0 {
		return []string{string(Unknown)}
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, string(d))
	}
	return out
}
