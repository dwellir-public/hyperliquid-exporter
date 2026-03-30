package peermon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerSet_Register(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	_, _ = ps.Register("10.0.0.1")
	_, _ = ps.Register("10.0.0.2")

	assert.Equal(t, 2, ps.Len())
	assert.True(t, ps.Dirty())
}

func TestPeerSet_RegisterRejectsInvalidIP(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	for _, bad := range []string{"", "not-an-ip", "10.0.0.1:4000", "10.0.0.1:port", "abc:def"} {
		_, evicted := ps.Register(bad)
		assert.False(t, evicted, "should not evict for invalid IP %q", bad)
	}
	assert.Equal(t, 0, ps.Len())

	// Valid IPs should still work
	_, _ = ps.Register("10.0.0.1")
	_, _ = ps.Register("::1")
	assert.Equal(t, 2, ps.Len())
}

func TestPeerSet_RegisterUpdatesLastSeen(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	_, _ = ps.Register("10.0.0.1")
	peers := ps.All()
	first := peers[0].LastSeen

	time.Sleep(time.Millisecond)
	_, _ = ps.Register("10.0.0.1")
	peers = ps.All()
	assert.True(t, peers[0].LastSeen.After(first))
	assert.Equal(t, 1, ps.Len())
}

func TestPeerSet_EvictionOrder(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	// Fill to capacity with deterministic timestamps
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= maxPeers; i++ {
		ip := "10.0.0." + fmt.Sprint(i)
		ps.mu.Lock()
		ps.peers[ip] = &Peer{IP: ip, LastSeen: base.Add(time.Duration(i) * time.Second)}
		ps.dirty = true
		ps.mu.Unlock()
	}
	assert.Equal(t, maxPeers, ps.Len())

	// Adding one more should evict 10.0.0.1 (oldest: base+1s)
	evictedIP, evicted := ps.Register("10.0.0.200")
	assert.Equal(t, maxPeers, ps.Len())
	assert.True(t, evicted)
	assert.Equal(t, "10.0.0.1", evictedIP)

	peers := ps.All()
	ips := make(map[string]bool)
	for _, p := range peers {
		ips[p.IP] = true
	}
	assert.False(t, ips["10.0.0.1"], "oldest peer should be evicted")
	assert.True(t, ips["10.0.0.200"], "new peer should be present")
}

func TestPeerSet_LoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()

	ps1 := NewPeerSet(dir)
	_, _ = ps1.Register("10.0.0.1")
	_, _ = ps1.Register("10.0.0.2")
	ps1.UpdatePort("10.0.0.2", 4005)
	require.NoError(t, ps1.Save())

	ps2 := NewPeerSet(dir)
	require.NoError(t, ps2.Load())
	assert.Equal(t, 2, ps2.Len())

	peers := ps2.All()
	ips := make(map[string]bool)
	for _, p := range peers {
		ips[p.IP] = true
	}
	assert.True(t, ips["10.0.0.1"])
	assert.True(t, ips["10.0.0.2"])

	var port int
	for _, p := range peers {
		if p.IP == "10.0.0.2" {
			port = p.Port
		}
	}
	assert.Equal(t, 4005, port)
}

func TestPeerSet_LoadMissingFile(t *testing.T) {
	ps := NewPeerSet(filepath.Join(t.TempDir(), "nonexistent"))
	assert.NoError(t, ps.Load())
	assert.Equal(t, 0, ps.Len())
}

func TestPeerSet_LoadLegacyJSONWithoutPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")
	require.NoError(t, os.WriteFile(path, []byte(`[
  {"ip":"10.0.0.1","last_seen":"2026-01-01T00:00:00Z"}
]`), 0o644))

	ps := NewPeerSet(dir)
	require.NoError(t, ps.Load())

	peers := ps.All()
	require.Len(t, peers, 1)
	assert.Equal(t, "10.0.0.1", peers[0].IP)
	assert.Zero(t, peers[0].Port)
}

func TestPeerSet_SaveNoOpWhenClean(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	// No registrations — not dirty
	assert.NoError(t, ps.Save())
}

func TestPeerSet_FinishSaveClearsDirtyWithoutConcurrentMutation(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	_, _ = ps.Register("10.0.0.1")

	_, gen, ok := ps.snapshotForSave()
	require.True(t, ok)

	ps.finishSave(gen)
	assert.False(t, ps.Dirty())
}

func TestPeerSet_FinishSaveKeepsDirtyAfterConcurrentMutation(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	_, _ = ps.Register("10.0.0.1")

	_, gen, ok := ps.snapshotForSave()
	require.True(t, ok)

	_, _ = ps.Register("10.0.0.2")
	ps.finishSave(gen)

	assert.True(t, ps.Dirty())
	assert.Equal(t, 2, ps.Len())
}

func TestPeerSet_UpdatePortMarksDirtyOnlyOnChange(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	_, _ = ps.Register("10.0.0.1")
	require.NoError(t, ps.Save())
	assert.False(t, ps.Dirty())

	ps.UpdatePort("10.0.0.1", 4005)
	assert.True(t, ps.Dirty())

	require.NoError(t, ps.Save())
	assert.False(t, ps.Dirty())

	ps.UpdatePort("10.0.0.1", 4005)
	assert.False(t, ps.Dirty())
}

func TestPeerSet_ConcurrentAccess(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = ps.Register("10.0.0." + fmt.Sprint(n%10+1))
			_ = ps.All()
			_ = ps.Len()
		}(i)
	}

	wg.Wait()
	assert.LessOrEqual(t, ps.Len(), 10)
}
