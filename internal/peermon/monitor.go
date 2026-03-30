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
	probeInterval  = 1 * time.Minute
	registerBufLen = 256
	maxConcurrent  = 10
)

var peerProbeTimeout = 5 * time.Second

var (
	removePeerMetrics = metrics.RemovePeerMetrics
	setPeerCount      = metrics.SetPeerMonitoredCount
)

// Monitor probes known peers for TCP latency and exposes metrics.
type Monitor struct {
	peers        *PeerSet
	register     chan string
	probeRunning atomic.Bool
	probeWG      sync.WaitGroup
	runProbe     func(context.Context, []Peer)
}

// New creates a Monitor that persists peers under dataDir.
func New(dataDir string) *Monitor {
	m := &Monitor{
		peers:    NewPeerSet(dataDir),
		register: make(chan string, registerBufLen),
	}
	m.runProbe = m.probeAll
	return m
}

// Register queues a peer IP for addition to the monitored set.
// Non-blocking; drops the registration with a warning if the buffer is full.
func (m *Monitor) Register(ip string) {
	select {
	case m.register <- ip:
	default:
		logger.WarningComponent("peer-latency", "Registration channel full, dropping peer %s", ip)
	}
}

// Start runs the monitor loop until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context, errCh chan<- error) {
	if err := m.peers.Load(); err != nil {
		logger.WarningComponent("peer-latency", "Failed to load peers from disk: %v", err)
	} else if n := m.peers.Len(); n > 0 {
		logger.InfoComponent("peer-latency", "Loaded %d peers from disk", n)
	}
	m.syncMonitoredCount()

	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.drainRegistrations()
			m.probeWG.Wait()
			if err := m.peers.Save(); err != nil {
				logger.WarningComponent("peer-latency", "Failed to save peers on shutdown: %v", err)
			}
			return

		case ip := <-m.register:
			m.processRegistration(ip)

		case <-ticker.C:
			m.drainRegistrations()
			m.saveIfDirty()
			if _, skipped := m.startProbeCycle(ctx); skipped {
				logger.WarningComponent("peer-latency", "Previous probe cycle still running, skipping tick")
			}
		}
	}
}

// drainRegistrations processes all pending registrations without blocking.
func (m *Monitor) drainRegistrations() {
	for {
		select {
		case ip := <-m.register:
			m.processRegistration(ip)
		default:
			return
		}
	}
}

func (m *Monitor) processRegistration(ip string) {
	evictedIP, evicted := m.peers.Register(ip)
	if evicted {
		removePeerMetrics(evictedIP)
	}
	m.syncMonitoredCount()
}

func (m *Monitor) syncMonitoredCount() {
	setPeerCount(int64(m.peers.Len()))
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
	m.syncMonitoredCount()
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

			metrics.IncrementPeerProbes(peer.IP)
			result := Probe(probeCtx, peer.IP, peer.Port)

			if result.Reachable {
				latencyMs := float64(result.Latency.Milliseconds())
				metrics.SetPeerLatency(peer.IP, latencyMs)
				metrics.SetPeerReachable(peer.IP, true)
				m.peers.UpdatePort(peer.IP, result.Port)
			} else {
				metrics.RemovePeerLatency(peer.IP)
				metrics.SetPeerReachable(peer.IP, false)
				metrics.IncrementPeerProbeFailures(peer.IP)
			}
		}(p)
	}

	wg.Wait()
}
