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

// trafficCall and activeCall record test-double invocations of the
// AddPeerTrafficVolume / AddPeerActiveSeconds setter func vars, since OTel
// counters have no read API.
type trafficCall struct {
	ip        string
	direction string
	volume    float64
}

type activeCall struct {
	ip      string
	seconds float64
}

func newOutboundPeersMonitorWithStubs(cfg *config.Config, registerPeer func(string, peermon.PeerDirection)) (*OutboundPeersMonitor, *[]trafficCall, *[]activeCall) {
	m := NewOutboundPeersMonitor(cfg, registerPeer)
	var traffic []trafficCall
	var active []activeCall
	m.addTrafficVolume = func(ip, dir string, volume float64) {
		traffic = append(traffic, trafficCall{ip, dir, volume})
	}
	m.addActiveSeconds = func(ip string, seconds float64) {
		active = append(active, activeCall{ip, seconds})
	}
	return m, &traffic, &active
}

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

	newOffset, err := m.processFile(filepath.Join(dateDir, "6"), 0, false)
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

	_, err := m.processFile(f, 0, false)
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

func TestOutboundPeersMonitor_TrafficVolume(t *testing.T) {
	initTestMetrics(t)

	m, traffic, _ := newOutboundPeersMonitorWithStubs(
		&config.Config{NodeHome: t.TempDir()},
		func(string, peermon.PeerDirection) {},
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	content := `["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5],[["Out","10.0.0.2",4002],2.0]]]
["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5],[["Out","10.0.0.2",4002],1.0]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0, false)
	require.NoError(t, err)

	sums := make(map[trafficCall]float64)
	for _, c := range *traffic {
		key := trafficCall{ip: c.ip, direction: c.direction}
		sums[key] += c.volume
	}

	assert.InDelta(t, 2.0, sums[trafficCall{ip: "10.0.0.1", direction: string(peermon.Inbound)}], 0.001)
	assert.InDelta(t, 3.0, sums[trafficCall{ip: "10.0.0.2", direction: string(peermon.Outbound)}], 0.001)
}

func TestOutboundPeersMonitor_ActiveSeconds(t *testing.T) {
	initTestMetrics(t)

	m, _, active := newOutboundPeersMonitorWithStubs(
		&config.Config{NodeHome: t.TempDir()},
		func(string, peermon.PeerDirection) {},
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	// line 1: peer A active, peer B silent. line 2 (+30s): peer A active, peer B active.
	// line 3 (+45s): peer B only.
	content := `["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5],[["Out","10.0.0.2",4002],0.0]]]
["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5],[["Out","10.0.0.2",4002],2.0]]]
["2026-03-31T06:01:15.000",[[["In","10.0.0.1",4001],0.0],[["Out","10.0.0.2",4002],1.0]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0, false)
	require.NoError(t, err)

	sums := make(map[string]float64)
	for _, c := range *active {
		sums[c.ip] += c.seconds
	}

	// first line contributes no active time (no prior lastLineTS)
	// line2 delta=30s: peers A and B both active -> A+30, B+30
	// line3 delta=45s: peer B active -> B+45
	assert.InDelta(t, 30.0, sums["10.0.0.1"], 0.001)
	assert.InDelta(t, 75.0, sums["10.0.0.2"], 0.001)
}

func TestOutboundPeersMonitor_SeedSuppression(t *testing.T) {
	initTestMetrics(t)

	var registered []string
	m, traffic, active := newOutboundPeersMonitorWithStubs(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string, _ peermon.PeerDirection) { registered = append(registered, ip) },
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	content := `["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5]]]
["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	// seed pass: counters suppressed, discovery still happens
	_, err := m.processFile(f, 0, true)
	require.NoError(t, err)

	assert.Empty(t, *traffic, "seed pass must not emit traffic volume counters")
	assert.Empty(t, *active, "seed pass must not emit active-seconds counters")
	assert.Contains(t, registered, "10.0.0.1")
	assert.False(t, m.lastLineTS.IsZero(), "lastLineTS must be set from seed lines")

	// simulate the next live line arriving 10s after the last seed line
	liveContent := content + `["2026-03-31T06:00:40.000",[[["In","10.0.0.1",4001],0.2]]]
`
	require.NoError(t, os.WriteFile(f, []byte(liveContent), 0o644))

	_, err = m.processFile(f, m.lastOffset, false)
	require.NoError(t, err)

	require.Len(t, *active, 1)
	assert.Equal(t, "10.0.0.1", (*active)[0].ip)
	assert.InDelta(t, 10.0, (*active)[0].seconds, 0.001)

	require.Len(t, *traffic, 1)
	assert.InDelta(t, 0.2, (*traffic)[0].volume, 0.001)
}
