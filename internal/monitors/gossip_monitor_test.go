package monitors

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
)

func newTestGossipMonitor(t *testing.T) *GossipMonitor {
	t.Helper()
	initTestMetrics(t)
	return NewGossipMonitor(&config.Config{NodeHome: t.TempDir()}, nil)
}

func writeGossipFile(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f := filepath.Join(dir, "0")
	var content []byte
	for _, l := range lines {
		content = append(content, []byte(l+"\n")...)
	}
	require.NoError(t, os.WriteFile(f, content, 0o644))
	return f
}

// recentTS returns a timestamp string offset seconds before now, in the log format.
func recentTS(offsetSeconds int) string {
	t := time.Now().Add(-time.Duration(offsetSeconds) * time.Second)
	return t.Format("2006-01-02T15:04:05.000")
}

func TestProcessGossipFile_IncomingRequest(t *testing.T) {
	m := newTestGossipMonitor(t)

	lines := []string{
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:57648",false]]`, recentTS(30)),
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:32912",false]]`, recentTS(20)),
		fmt.Sprintf(`["%s",["incoming request","10.0.0.1:12345",false]]`, recentTS(10)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	newOffset, err := m.processGossipFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, newOffset, int64(0))

	// two distinct peers tracked
	assert.Len(t, m.peerLastSeen, 2)
	assert.Contains(t, m.peerLastSeen, "192.168.108.167")
	assert.Contains(t, m.peerLastSeen, "10.0.0.1")
}

func TestProcessGossipFile_IncomingRequestRegistersPeer(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)

	m := NewGossipMonitor(&config.Config{NodeHome: t.TempDir()}, func(ip string, _ peermon.PeerDirection) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, ip)
	})

	lines := []string{
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:57648",false]]`, recentTS(10)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"192.168.108.167"}, seen)
}

func TestProcessGossipFile_ChildPeersStatus(t *testing.T) {
	m := newTestGossipMonitor(t)

	lines := []string{
		fmt.Sprintf(`["%s",["child_peers status",[[{"Ip":"192.168.108.236"},{"verified":true,"connection_count":1}],[{"Ip":"10.0.0.2"},{"verified":false,"connection_count":2}]]]]`, recentTS(10)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)

	assert.Len(t, m.knownChildPeers, 2)
	assert.Contains(t, m.knownChildPeers, "192.168.108.236")
	assert.Contains(t, m.knownChildPeers, "10.0.0.2")
	assert.True(t, m.knownChildPeers["192.168.108.236"].verified)
	assert.False(t, m.knownChildPeers["10.0.0.2"].verified)
}

func TestProcessGossipFile_EmptyChildPeers(t *testing.T) {
	m := newTestGossipMonitor(t)

	lines := []string{
		fmt.Sprintf(`["%s",["child_peers status",[]]]`, recentTS(10)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)
	assert.Empty(t, m.knownChildPeers)
}

func TestProcessGossipFile_OffsetTracking(t *testing.T) {
	m := newTestGossipMonitor(t)

	lines := []string{
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:57648",false]]`, recentTS(30)),
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:32912",false]]`, recentTS(20)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)

	// first pass: read all lines
	offset1, err := m.processGossipFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, offset1, int64(0))

	// second pass from same offset: no new data
	offset2, err := m.processGossipFile(f, offset1)
	require.NoError(t, err)
	assert.Equal(t, offset1, offset2)

	// append a new line and read from previous offset
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = fmt.Fprintf(fh, `["%s",["incoming request","10.0.0.5:9999",false]]`+"\n", recentTS(5))
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	offset3, err := m.processGossipFile(f, offset2)
	require.NoError(t, err)
	assert.Greater(t, offset3, offset2)
	assert.Contains(t, m.peerLastSeen, "10.0.0.5")
}

func TestProcessGossipFile_MalformedLines(t *testing.T) {
	m := newTestGossipMonitor(t)

	lines := []string{
		`not json at all`,
		fmt.Sprintf(`["%s"]`, recentTS(30)),                   // missing second element
		fmt.Sprintf(`["%s",["unknown_event"]]`, recentTS(20)), // unknown event, only 1 field
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:1234",false]]`, recentTS(10)), // valid
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)

	// only the valid incoming request should be tracked
	assert.Len(t, m.peerLastSeen, 1)
	assert.Contains(t, m.peerLastSeen, "192.168.108.167")
}

func TestProcessGossipFile_MixedEvents(t *testing.T) {
	m := newTestGossipMonitor(t)

	lines := []string{
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:57648",false]]`, recentTS(40)),
		fmt.Sprintf(`["%s",["child_peers status",[[{"Ip":"192.168.108.236"},{"verified":true,"connection_count":1}]]]]`, recentTS(30)),
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:32912",false]]`, recentTS(20)),
		fmt.Sprintf(`["%s",["child_peers status",[]]]`, recentTS(10)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)

	// incoming peer tracked
	assert.Contains(t, m.peerLastSeen, "192.168.108.167")

	// child peer was present in first status, absent in second (empty list)
	// still within stale TTL so retained but marked disconnected
	assert.Contains(t, m.knownChildPeers, "192.168.108.236")
	assert.True(t, m.knownChildPeers["192.168.108.236"].verified)
}

func TestProcessGossipFile_ChildPeerStaleRemoval(t *testing.T) {
	m := newTestGossipMonitor(t)

	// manually add a "stale" child peer with zero time (well past 10 min TTL)
	m.knownChildPeers["old.peer.ip"] = childPeerState{lastSeen: time.Time{}, verified: true}

	lines := []string{
		fmt.Sprintf(`["%s",["child_peers status",[[{"Ip":"new.peer.ip"},{"verified":true,"connection_count":1}]]]]`, recentTS(10)),
	}

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"), lines...)
	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)

	// stale peer should be removed (zero time > 10 min ago)
	assert.NotContains(t, m.knownChildPeers, "old.peer.ip")
	// new peer should be present
	assert.Contains(t, m.knownChildPeers, "new.peer.ip")
}

func TestProcessGossipFile_PartialLineRetry(t *testing.T) {
	m := newTestGossipMonitor(t)
	dir := filepath.Join(m.gossipDir, "20260330")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	f := filepath.Join(dir, "0")
	partial := fmt.Sprintf(`["%s",["incoming request","192.168.108.167:57648",false]]`, recentTS(10))
	require.NoError(t, os.WriteFile(f, []byte(partial), 0o644))

	offset1, err := m.processGossipFile(f, 0)
	require.NoError(t, err)
	assert.Zero(t, offset1)
	assert.Empty(t, m.peerLastSeen)

	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = fh.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	offset2, err := m.processGossipFile(f, offset1)
	require.NoError(t, err)
	assert.Greater(t, offset2, offset1)
	assert.Contains(t, m.peerLastSeen, "192.168.108.167")
}

func TestProcessGossipFile_TruncationResetsOffset(t *testing.T) {
	m := newTestGossipMonitor(t)

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"),
		fmt.Sprintf(`["%s",["incoming request","192.168.108.167:57648",false]]`, recentTS(30)),
	)

	offset1, err := m.processGossipFile(f, 0)
	require.NoError(t, err)
	assert.Greater(t, offset1, int64(0))

	require.NoError(t, os.WriteFile(f, []byte(fmt.Sprintf(`["%s",["incoming request","10.0.0.8:9999",false]]`+"\n", recentTS(5))), 0o644))

	offset2, err := m.processGossipFile(f, offset1)
	require.NoError(t, err)
	assert.Greater(t, offset2, int64(0))
	assert.Contains(t, m.peerLastSeen, "10.0.0.8")
}

func TestProcessGossipFile_ChildPeerVerificationFlip(t *testing.T) {
	m := newTestGossipMonitor(t)

	f := writeGossipFile(t, filepath.Join(m.gossipDir, "20260330"),
		fmt.Sprintf(`["%s",["child_peers status",[[{"Ip":"192.168.108.236"},{"verified":true,"connection_count":1}]]]]`, recentTS(20)),
		fmt.Sprintf(`["%s",["child_peers status",[[{"Ip":"192.168.108.236"},{"verified":false,"connection_count":1}]]]]`, recentTS(10)),
	)

	_, err := m.processGossipFile(f, 0)
	require.NoError(t, err)
	assert.Contains(t, m.knownChildPeers, "192.168.108.236")
	assert.False(t, m.knownChildPeers["192.168.108.236"].verified)
}
