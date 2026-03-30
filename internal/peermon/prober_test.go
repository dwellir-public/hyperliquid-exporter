package peermon

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_Reachable(t *testing.T) {
	// Start a TCP listener on an ephemeral port in the probe range.
	ln, err := net.Listen("tcp", "127.0.0.1:4000")
	if err != nil {
		t.Skip("cannot bind port 4000:", err)
	}
	defer func() { _ = ln.Close() }()

	// Accept connections in background to complete the handshake.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	// Skip if any priority port is unexpectedly open (would win the race).
	for _, port := range []int{3001, 3002, 443, 80} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Skipf("port %d unexpectedly open", port)
		}
	}

	result := Probe(context.Background(), "127.0.0.1", 0)
	assert.True(t, result.Reachable)
	assert.Greater(t, result.Latency, time.Duration(0))
	assert.Equal(t, "127.0.0.1", result.IP)
	assert.Equal(t, 4000, result.Port)
}

func TestProbe_Unreachable(t *testing.T) {
	// Use a non-routable address to get fast failure.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Find an unused port — nothing should be listening on 4000-4010 at 127.0.0.254.
	result := Probe(ctx, "127.0.0.254", 0)
	assert.False(t, result.Reachable)
	assert.Zero(t, result.Latency)
}

func TestProbe_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result := Probe(ctx, "10.0.0.1", 0)
	assert.False(t, result.Reachable)
}

func TestProbe_FallsThrough(t *testing.T) {
	// Listen on port 4005 (not 4000), verify prober finds it.
	ln, err := net.Listen("tcp", "127.0.0.1:4005")
	if err != nil {
		t.Skip("cannot bind port 4005:", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	// Ensure no other candidate ports are listening.
	for _, port := range []int{3001, 3002, 443, 80, 4000, 4001, 4002, 4003, 4004} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Skipf("port %d unexpectedly open", port)
		}
	}

	result := Probe(context.Background(), "127.0.0.1", 0)
	require.True(t, result.Reachable)
	assert.Equal(t, 4005, result.Port)
}

func TestProbe_TriesPreferredPortFirst(t *testing.T) {
	restore := dialAddr
	t.Cleanup(func() { dialAddr = restore })

	var mu sync.Mutex
	var attempts []int
	dialAddr = func(ctx context.Context, addr string) (net.Conn, error) {
		_, portStr, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		port, err := strconv.Atoi(portStr)
		require.NoError(t, err)
		mu.Lock()
		attempts = append(attempts, port)
		mu.Unlock()
		if port != 4005 {
			return nil, fmt.Errorf("closed")
		}

		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}

	result := Probe(context.Background(), "127.0.0.1", 4005)
	require.True(t, result.Reachable)
	assert.Equal(t, 4005, result.Port)
	assert.Equal(t, []int{4005}, attempts)
}

func TestProbe_FallsBackAfterStalePreferredPort(t *testing.T) {
	restore := dialAddr
	t.Cleanup(func() { dialAddr = restore })

	var mu sync.Mutex
	var attempts []int
	dialAddr = func(ctx context.Context, addr string) (net.Conn, error) {
		_, portStr, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		port, err := strconv.Atoi(portStr)
		require.NoError(t, err)
		mu.Lock()
		attempts = append(attempts, port)
		mu.Unlock()
		if port != 4002 {
			return nil, fmt.Errorf("closed")
		}

		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}

	result := Probe(context.Background(), "127.0.0.1", 4001)
	require.True(t, result.Reachable)
	assert.Equal(t, 4002, result.Port)
	require.NotEmpty(t, attempts)
	assert.Equal(t, 4001, attempts[0])
	assert.Contains(t, attempts, 4002)
}

func TestProbe_ReachesLaterFallbackPortWhenEarlierPortHangs(t *testing.T) {
	restoreDial := dialAddr
	restoreAttemptTimeout := probeAttemptTimeout
	t.Cleanup(func() {
		dialAddr = restoreDial
		probeAttemptTimeout = restoreAttemptTimeout
	})

	probeAttemptTimeout = 50 * time.Millisecond

	dialAddr = func(ctx context.Context, addr string) (net.Conn, error) {
		_, portStr, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		port, err := strconv.Atoi(portStr)
		require.NoError(t, err)

		switch port {
		case 4000:
			<-ctx.Done()
			return nil, ctx.Err()
		case 4005:
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		default:
			return nil, fmt.Errorf("closed")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result := Probe(ctx, "127.0.0.1", 0)
	require.True(t, result.Reachable)
	assert.Equal(t, 4005, result.Port)
}
