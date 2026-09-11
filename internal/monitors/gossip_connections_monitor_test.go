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
	newOffset, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_VerifiedGossipRPC(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	newOffset, err := m.processFile(f, 0, false)
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
	_, err := m.processFile(f, 0, false)
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
	newOffset, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_OffsetTracking(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	lines := []string{
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
	}

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)

	offset1, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Greater(t, offset1, int64(0))

	// no new data
	offset2, err := m.processFile(f, offset1, false)
	require.NoError(t, err)
	assert.Equal(t, offset1, offset2)

	// append new line
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = fh.WriteString(`["2026-03-30T05:01:03.415",["verified gossip rpc",{"Ip":"10.0.0.5"}]]` + "\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	offset3, err := m.processFile(f, offset2, false)
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
	newOffset, err := m.processFile(f, 0, false)
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
	newOffset, err := m.processFile(f, 0, false)
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

	offset1, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Zero(t, offset1)

	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = fh.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	offset2, err := m.processFile(f, offset1, false)
	require.NoError(t, err)
	assert.Greater(t, offset2, offset1)
}

func TestProcessConnectionsFile_TruncationResetsOffset(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)

	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"),
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
	)

	offset1, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Greater(t, offset1, int64(0))

	require.NoError(t, os.WriteFile(f, []byte(`["2026-03-30T05:01:03.415",["verified gossip rpc",{"Ip":"10.0.0.5"}]]`+"\n"), 0o644))

	offset2, err := m.processFile(f, offset1, false)
	require.NoError(t, err)
	assert.Greater(t, offset2, int64(0))
}

func TestProcessConnectionsFile_SeedingRegistersWithoutCounting(t *testing.T) {
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
		`["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`,
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`,
	}
	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"), lines...)
	offset, err := m.processFile(f, 0, true)
	require.NoError(t, err)
	assert.Greater(t, offset, int64(0))

	mu.Lock()
	defer mu.Unlock()
	assert.ElementsMatch(t, []string{"192.168.108.167", "192.168.108.236"}, seen)
}

func TestParseGossipConnectionLine(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		event string
		known bool
		stage string
	}{
		{"handle stream", `["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850","gossip"]]`, "handle_stream_connection", true, ""},
		{"verified rpc", `["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`, "verified_gossip_rpc", true, ""},
		{"finished checks legacy", `["2026-03-30T05:00:09.841",["finished checks",{"Ip":"1.2.3.4"},true]]`, "finished_checks", true, ""},
		{"no quorum legacy", `["2026-03-30T05:00:09.841",["closing gossip stream because no quorum yet",{"Ip":"1.2.3.4"},false]]`, "closing_gossip_stream_no_quorum_yet", true, ""},
		{"no quorum new", `["2026-03-30T05:00:09.841",["closing gossip stream because no quorum yet","1.2.3.4:4001",["a"]]]`, "closing_gossip_stream_no_quorum_yet", true, ""},
		{"abci drop bool", `["2026-03-30T05:00:09.841",["dropping connection after sending abci state",{"Ip":"1.2.3.4"},true]]`, "dropping_connection_after_sending_abci_state", true, ""},
		{"abci drop string", `["2026-03-30T05:00:09.841",["dropping connection after sending abci state",{"Ip":"1.2.3.4"},"done"]]`, "dropping_connection_after_sending_abci_state", true, ""},
		{"evm kvs object only", `["2026-03-30T05:00:09.841",["sending evm kvs",{"Ip":"1.2.3.4"}]]`, "sending_evm_kvs", true, ""},
		{"verified object only rejected", `["2026-03-30T05:00:09.841",["finished checks",{"Ip":"1.2.3.4"}]]`, "", true, "payload"},
		{"unknown tag", `["2026-03-30T05:00:09.841",["brand new event",{"Ip":"1.2.3.4"}]]`, "other", false, ""},
		{"handle stream arity", `["2026-03-30T05:00:03.399",["handle_stream_connection","192.168.108.167:50850"]]`, "", true, "payload"},
		{"not json", `nope`, "", false, "json"},
		{"outer arity", `["2026-03-30T05:00:03.399"]`, "", false, "shape"},
		{"bad timestamp", `["soon",["verified gossip rpc",{"Ip":"1.2.3.4"}]]`, "", false, "timestamp"},
		{"null tag", `["2026-03-30T05:00:03.399",[null]]`, "", false, "shape"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, stage := parseGossipConnectionLine([]byte(tc.line))
			assert.Equal(t, tc.stage, stage)
			if stage != "" {
				return
			}
			assert.Equal(t, tc.event, ev.event)
			assert.Equal(t, tc.known, ev.known)
		})
	}
}

func TestProcessConnectionsFile_UnknownEventStillProcessed(t *testing.T) {
	m := newTestGossipConnectionsMonitor(t)
	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"),
		`["2026-03-30T05:00:09.841",["brand new event",{"Ip":"1.2.3.4"}]]`,
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`,
	)
	newOffset, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))
}

func TestProcessConnectionsFile_PerIPCountersGated(t *testing.T) {
	initTestMetrics(t)
	var registered []string
	register := func(ip string, _ peermon.PeerDirection) { registered = append(registered, ip) }

	// without --peer-latency the peer is still discovered for registration
	m := NewGossipConnectionsMonitor(&config.Config{NodeHome: t.TempDir()}, register)
	assert.False(t, m.perIP)
	f := writeGossipFile(t, filepath.Join(m.dir, "20260330"),
		`["2026-03-30T05:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]`)
	_, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"192.168.108.236"}, registered)

	m = NewGossipConnectionsMonitor(&config.Config{NodeHome: t.TempDir(), EnablePeerLatency: true}, register)
	assert.True(t, m.perIP)
}
