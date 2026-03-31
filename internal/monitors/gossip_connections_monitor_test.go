package monitors

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
)

func newTestGossipConnectionsMonitor(t *testing.T) *GossipConnectionsMonitor {
	t.Helper()
	initTestMetrics(t)
	return NewGossipConnectionsMonitor(&config.Config{NodeHome: t.TempDir()}, nil)
}

func TestProcessConnectionsFile_HandleStreamConnection(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
		`["2026-03-30T05:00:13.408",["handle_stream_connection","10.0.0.1:34460","gossip"]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	newOffset, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_VerifiedGossipRPC(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	newOffset, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_RegistersPeer(t *testing.T) {
	initTestMetrics(t)

	var (
		mu   sync.Mutex
		seen []string
	)

	m := NewGossipConnectionsMonitor(&config.Config{NodeHome: t.TempDir()}, func(ip string, _ peermon.PeerDirection) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, ip)
	})

	lines := []string{
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	_, err := m.processFile(f, 0)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"192.168.108.236"}, seen)
}

func TestProcessConnectionsFile_SkipsPerformingChecks(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:03.399",["performing checks on stream","192.168.108.167","gossip"]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	newOffset, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_OffsetTracking(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)

	offset1, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, offset1, int64(0))

	// no new data
	offset2, err := m.processFile(f, offset1)
	require.NoError(t, err)
	assert.Equal(t, offset1, offset2)

	// append new line
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = fh.WriteString(`["2026-03-30T05:01:03.415",["verified gossip rpc",{"Ip":"10.0.0.5"}]]` + "\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	offset3, err := m.processFile(f, offset2)
	require.NoError(t, err)
	assert.Greater(t, offset3, offset2)
}

func TestProcessConnectionsFile_MixedEvents(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
		`["2026-03-30T05:00:03.399",["performing checks on stream","192.168.108.167","gossip"]]`,
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	newOffset, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_MalformedLines(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`not json`,
		`["2026-03-30T05:00:03.399"]`,
		`["2026-03-30T05:00:03.399",["handle_stream_connection"]]`,                                  // missing IP and type
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`, // valid
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	newOffset, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_PartialLineRetry(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)
	dir := filepath.Join(m.dir, "20260330")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	f := filepath.Join(dir, "0")
	partial := `["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`
	require.NoError(t, os.WriteFile(f, []byte(partial), 0o644))

	offset1, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Zero(t, offset1)

	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = fh.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	offset2, err := m.processFile(f, offset1)
	require.NoError(t, err)
	assert.Greater(t, offset2, offset1)
}

func TestProcessConnectionsFile_TruncationResetsOffset(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"),
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
	)

	offset1, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, offset1, int64(0))

	require.NoError(t, os.WriteFile(f, []byte(`["2026-03-30T05:01:03.415",["verified gossip rpc",{"Ip":"10.0.0.5"}]]`+"\n"), 0o644))

	offset2, err := m.processFile(f, offset1)
	require.NoError(t, err)
	assert.Greater(t, offset2, int64(0))
}
