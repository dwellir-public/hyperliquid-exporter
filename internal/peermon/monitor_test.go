package peermon

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

var metricsOnce sync.Once

func initTestMetrics(t *testing.T) {
	t.Helper()
	metricsOnce.Do(func() {
		cfg := metrics.MetricsConfig{
			Chain: "test",
			Alias: "test-node",
		}
		if err := metrics.InitMetrics(context.Background(), cfg); err != nil {
			t.Fatalf("failed to init metrics: %v", err)
		}
	})
}

func TestMonitor_Register(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	m.Register("10.0.0.1", Outbound)
	m.Register("10.0.0.2", Outbound)

	assert.Equal(t, 2, m.peers.Len())
}

func TestMonitor_RegisterHighVolume(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	// simulate thousands of registrations for 5 unique IPs (like tcp_traffic startup)
	for i := range 5000 {
		m.Register("10.0.0."+strconv.Itoa(i%5+1), Outbound)
	}

	// all 5 unique IPs registered, no drops
	assert.Equal(t, 5, m.peers.Len())
}

func TestMonitor_StartAndShutdown(t *testing.T) {
	initTestMetrics(t)
	dir := t.TempDir()
	m := New(dir)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go m.Start(ctx, errCh)

	// Register a peer
	m.Register("127.0.0.1", Outbound)
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond) // let shutdown complete

	// Verify peers were saved to disk
	ps2 := NewPeerSet(dir)
	require.NoError(t, ps2.Load())
	assert.Equal(t, 1, ps2.Len())
}

func TestMonitor_ProbeAllWithPeers(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	// Register an unreachable peer
	_, _ = m.peers.Register("127.0.0.254", Outbound)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// probeAll should complete without panic
	m.probeAll(ctx, m.peers.All())
}

func TestMonitor_StartProbeCycleSkipsWhileRunning(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())
	_, _ = m.peers.Register("10.0.0.1", Outbound)

	startedCh := make(chan struct{})
	release := make(chan struct{})
	m.runProbe = func(ctx context.Context, peers []Peer) {
		close(startedCh)
		<-release
	}

	started, skipped := m.startProbeCycle(context.Background())
	require.True(t, started)
	require.False(t, skipped)
	<-startedCh

	started, skipped = m.startProbeCycle(context.Background())
	assert.False(t, started)
	assert.True(t, skipped)

	close(release)
	m.probeWG.Wait()
}

func TestMonitor_RegisterWhileProbeRunning(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())
	_, _ = m.peers.Register("10.0.0.1", Outbound)

	startedCh := make(chan struct{})
	release := make(chan struct{})
	m.runProbe = func(ctx context.Context, peers []Peer) {
		close(startedCh)
		<-release
	}

	started, skipped := m.startProbeCycle(context.Background())
	require.True(t, started)
	require.False(t, skipped)
	<-startedCh

	m.Register("10.0.0.2", Outbound)
	m.Register("10.0.0.3", Outbound)

	assert.Equal(t, 3, m.peers.Len())

	close(release)
	m.probeWG.Wait()
}

func TestMonitor_ProbeAllUsesPerPeerDeadline(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	restoreDial := dialAddr
	restoreTimeout := peerProbeTimeout
	t.Cleanup(func() {
		dialAddr = restoreDial
		peerProbeTimeout = restoreTimeout
	})

	peerProbeTimeout = 20 * time.Millisecond
	dialAddr = func(ctx context.Context, addr string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	start := time.Now()
	m.probeAll(context.Background(), []Peer{{IP: "10.0.0.1", Directions: map[PeerDirection]bool{}}})

	assert.Less(t, time.Since(start), 200*time.Millisecond)
}

func TestMonitor_RegisterEvictsOldest(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= maxPeers; i++ {
		ip := "10.0.0." + strconv.Itoa(i)
		m.peers.mu.Lock()
		m.peers.peers[ip] = &Peer{IP: ip, Directions: map[PeerDirection]bool{}, LastSeen: base.Add(time.Duration(i) * time.Second)}
		m.peers.mu.Unlock()
	}

	restoreRemove := removePeerMetrics
	restoreCount := setPeerCount
	t.Cleanup(func() {
		removePeerMetrics = restoreRemove
		setPeerCount = restoreCount
	})

	var removed string
	var count int64
	removePeerMetrics = func(ip string) {
		removed = ip
	}
	setPeerCount = func(v int64) {
		count = v
	}

	m.Register("10.0.1.1", Outbound)

	assert.Equal(t, "10.0.0.1", removed)
	assert.Equal(t, int64(maxPeers), count)
	assert.Equal(t, maxPeers, m.peers.Len())
}

func TestMonitor_StartSyncsLoadedPeerCount(t *testing.T) {
	initTestMetrics(t)
	dir := t.TempDir()

	ps := NewPeerSet(dir)
	_, _ = ps.Register("10.0.0.1", Outbound)
	_, _ = ps.Register("10.0.0.2", Outbound)
	require.NoError(t, ps.Save())

	restoreCount := setPeerCount
	t.Cleanup(func() {
		setPeerCount = restoreCount
	})

	countCh := make(chan int64, 1)
	setPeerCount = func(v int64) {
		select {
		case countCh <- v:
		default:
		}
	}

	m := New(dir)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Start(ctx, make(chan error, 1))
	}()

	require.Eventually(t, func() bool {
		select {
		case v := <-countCh:
			return v == 2
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	cancel()
	<-done
}

func TestMonitor_ProbeAllBacksOffUnreachable(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	restoreDial := dialAddr
	restoreTimeout := peerProbeTimeout
	t.Cleanup(func() {
		dialAddr = restoreDial
		peerProbeTimeout = restoreTimeout
	})

	peerProbeTimeout = 20 * time.Millisecond
	dialAddr = func(ctx context.Context, addr string) (net.Conn, error) {
		return nil, net.ErrClosed
	}

	m.peers.mu.Lock()
	m.peers.peers["10.0.0.1"] = &Peer{IP: "10.0.0.1", Directions: map[PeerDirection]bool{}, ConsecFails: failBackoffThreshold, LastProbe: time.Now()}
	m.peers.peers["10.0.0.2"] = &Peer{IP: "10.0.0.2", Directions: map[PeerDirection]bool{}, ConsecFails: failBackoffThreshold, LastProbe: time.Now().Add(-2 * backoffProbeInterval)}
	m.peers.peers["10.0.0.3"] = &Peer{IP: "10.0.0.3", Directions: map[PeerDirection]bool{}}
	m.peers.mu.Unlock()

	m.probeAll(context.Background(), m.peers.All())

	fails := map[string]int{}
	for _, p := range m.peers.All() {
		fails[p.IP] = p.ConsecFails
	}
	// 10.0.0.1 is in backoff and skipped; the other two are probed and fail
	assert.Equal(t, failBackoffThreshold, fails["10.0.0.1"])
	assert.Equal(t, failBackoffThreshold+1, fails["10.0.0.2"])
	assert.Equal(t, 1, fails["10.0.0.3"])
}

func TestMonitor_ExpireStaleRemovesMetrics(t *testing.T) {
	initTestMetrics(t)
	m := New(t.TempDir())

	stale := time.Now().Add(-peerTTL - time.Hour)
	m.peers.mu.Lock()
	m.peers.peers["10.0.0.1"] = &Peer{IP: "10.0.0.1", Directions: map[PeerDirection]bool{}, LastSeen: stale, ConsecFails: failBackoffThreshold}
	m.peers.peers["10.0.0.2"] = &Peer{IP: "10.0.0.2", Directions: map[PeerDirection]bool{}, LastSeen: time.Now()}
	m.peers.mu.Unlock()

	restoreRemove := removePeerMetrics
	restoreCount := setPeerCount
	t.Cleanup(func() {
		removePeerMetrics = restoreRemove
		setPeerCount = restoreCount
	})

	var removed []string
	var count int64
	removePeerMetrics = func(ip string) { removed = append(removed, ip) }
	setPeerCount = func(v int64) { count = v }

	m.expireStale()

	assert.Equal(t, []string{"10.0.0.1"}, removed)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, 1, m.peers.Len())
}
