package monitors

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
)

func TestOutboundPeersMonitor_ProcessFile(t *testing.T) {
	initTestMetrics(t)

	var mu sync.Mutex
	var registered []string
	register := func(ip string, _ peermon.PeerDirection) {
		mu.Lock()
		registered = append(registered, ip)
		mu.Unlock()
	}

	dir := t.TempDir()
	cfg := &config.Config{NodeHome: dir}
	m := NewOutboundPeersMonitor(cfg, register)

	// create tcp_traffic/hourly/20260331/6
	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	content := `["2026-03-31T06:00:26.471",[[["In","192.168.108.167",4001],1.278],[["Out","192.168.108.167",4002],0.0],[["In","10.0.0.1",4001],0.5]]]
["2026-03-31T06:00:56.477",[[["In","192.168.108.167",4001],1.03],[["Out","10.0.0.2",4002],0.0]]]
`
	require.NoError(t, os.WriteFile(filepath.Join(dateDir, "6"), []byte(content), 0o644))

	newOffset, err := m.processFile(filepath.Join(dateDir, "6"), 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))

	mu.Lock()
	sort.Strings(registered)
	mu.Unlock()

	// deduped per poll: 3 unique IPs, but 192.168.108.167 appears with both In and Out
	assert.Len(t, registered, 4)
	assert.Contains(t, registered, "192.168.108.167")
	assert.Contains(t, registered, "10.0.0.1")
	assert.Contains(t, registered, "10.0.0.2")
	assert.Len(t, m.seen, 3)
}

func TestOutboundPeersMonitor_LogsNewPeers(t *testing.T) {
	initTestMetrics(t)

	var peers []string
	m := NewOutboundPeersMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string, _ peermon.PeerDirection) { peers = append(peers, ip) },
	)

	m.register("10.0.0.1", peermon.Outbound)
	m.register("10.0.0.1", peermon.Outbound) // duplicate
	m.register("10.0.0.2", peermon.Inbound)

	// all three calls forwarded to registerPeer
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.1", "10.0.0.2"}, peers)
	// seen tracks unique
	assert.Len(t, m.seen, 2)
}

func TestOutboundPeersMonitor_MalformedLines(t *testing.T) {
	initTestMetrics(t)

	var registered []string
	m := NewOutboundPeersMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string, _ peermon.PeerDirection) { registered = append(registered, ip) },
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	content := `not json at all
["2026-03-31T06:00:26.471","not an array"]
["2026-03-31T06:00:26.471",[[["In","10.0.0.1",4001],0.5]]]
["2026-03-31T06:00:26.471",[["broken"]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.1"}, registered)
}

func TestOutboundPeersMonitor_EmptyDir(t *testing.T) {
	initTestMetrics(t)

	m := NewOutboundPeersMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(string, peermon.PeerDirection) {},
	)

	// dir doesn't exist yet
	m.poll() // should not panic
}
