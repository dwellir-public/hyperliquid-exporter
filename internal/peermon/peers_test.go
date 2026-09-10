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

	_, _ = ps.Register("10.0.0.1", Outbound)
	_, _ = ps.Register("10.0.0.2", Outbound)

	assert.Equal(t, 2, ps.Len())
	assert.True(t, ps.Dirty())
}

func TestPeerSet_RegisterRejectsInvalidIP(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	for _, bad := range []string{"", "not-an-ip", "10.0.0.1:4000", "10.0.0.1:port", "abc:def"} {
		_, evicted := ps.Register(bad, Outbound)
		assert.False(t, evicted, "should not evict for invalid IP %q", bad)
	}
	assert.Equal(t, 0, ps.Len())

	// Valid IPs should still work
	_, _ = ps.Register("10.0.0.1", Outbound)
	_, _ = ps.Register("::1", Outbound)
	assert.Equal(t, 2, ps.Len())
}

func TestPeerSet_RegisterUpdatesLastSeen(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	_, _ = ps.Register("10.0.0.1", Outbound)
	peers := ps.All()
	first := peers[0].LastSeen

	time.Sleep(time.Millisecond)
	_, _ = ps.Register("10.0.0.1", Outbound)
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
		ps.peers[ip] = &Peer{IP: ip, Directions: map[PeerDirection]bool{}, LastSeen: base.Add(time.Duration(i) * time.Second)}
		ps.dirty = true
		ps.mu.Unlock()
	}
	assert.Equal(t, maxPeers, ps.Len())

	// Adding one more should evict 10.0.0.1 (oldest: base+1s)
	evictedIP, evicted := ps.Register("10.0.1.1", Outbound)
	assert.Equal(t, maxPeers, ps.Len())
	assert.True(t, evicted)
	assert.Equal(t, "10.0.0.1", evictedIP)

	peers := ps.All()
	ips := make(map[string]bool)
	for _, p := range peers {
		ips[p.IP] = true
	}
	assert.False(t, ips["10.0.0.1"], "oldest peer should be evicted")
	assert.True(t, ips["10.0.1.1"], "new peer should be present")
}

func TestPeerSet_LoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()

	ps1 := NewPeerSet(dir)
	_, _ = ps1.Register("10.0.0.1", Outbound)
	_, _ = ps1.Register("10.0.0.2", Outbound)
	ps1.UpdatePort("10.0.0.2", 4005)
	ps1.MarkParent("10.0.0.2")
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
	var wasParent1, wasParent2 bool
	for _, p := range peers {
		if p.IP == "10.0.0.2" {
			port = p.Port
			wasParent2 = p.WasParent
		}
		if p.IP == "10.0.0.1" {
			wasParent1 = p.WasParent
		}
	}
	assert.Equal(t, 4005, port)
	assert.True(t, wasParent2, "10.0.0.2 should persist was_parent")
	assert.False(t, wasParent1, "10.0.0.1 should not be marked parent")
}

func TestPeerSet_EvictionSkipsParents(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= maxPeers; i++ {
		ip := "10.0.0." + fmt.Sprint(i)
		ps.mu.Lock()
		ps.peers[ip] = &Peer{IP: ip, Directions: map[PeerDirection]bool{}, LastSeen: base.Add(time.Duration(i) * time.Second)}
		ps.dirty = true
		ps.mu.Unlock()
	}
	// 10.0.0.1 is the oldest peer but is an ex-parent, so it must be exempt.
	ps.MarkParent("10.0.0.1")
	assert.Equal(t, maxPeers, ps.Len())

	evictedIP, evicted := ps.Register("10.0.1.1", Outbound)
	assert.True(t, evicted)
	assert.NotEqual(t, "10.0.0.1", evictedIP, "parent-flagged peer must not be evicted")
	// Next-oldest non-parent is 10.0.0.2.
	assert.Equal(t, "10.0.0.2", evictedIP)

	peers := ps.All()
	ips := make(map[string]bool)
	for _, p := range peers {
		ips[p.IP] = true
	}
	assert.True(t, ips["10.0.0.1"], "ex-parent peer must survive eviction")
	assert.True(t, ips["10.0.1.1"], "new peer should be present")
}

func TestPeerSet_MarkParentRegistersUnknown(t *testing.T) {
	ps := NewPeerSet(t.TempDir())

	ps.MarkParent("10.0.0.50")

	assert.Equal(t, 1, ps.Len())
	peers := ps.All()
	require.Len(t, peers, 1)
	assert.Equal(t, "10.0.0.50", peers[0].IP)
	assert.True(t, peers[0].WasParent)
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
  {"ip":"10.0.0.1","last_seen":"`+time.Now().Format(time.RFC3339)+`"}
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
	_, _ = ps.Register("10.0.0.1", Outbound)

	_, gen, ok := ps.snapshotForSave()
	require.True(t, ok)

	ps.finishSave(gen)
	assert.False(t, ps.Dirty())
}

func TestPeerSet_FinishSaveKeepsDirtyAfterConcurrentMutation(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	_, _ = ps.Register("10.0.0.1", Outbound)

	_, gen, ok := ps.snapshotForSave()
	require.True(t, ok)

	_, _ = ps.Register("10.0.0.2", Outbound)
	ps.finishSave(gen)

	assert.True(t, ps.Dirty())
	assert.Equal(t, 2, ps.Len())
}

func TestPeerSet_UpdatePortMarksDirtyOnlyOnChange(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	_, _ = ps.Register("10.0.0.1", Outbound)
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

	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = ps.Register("10.0.0."+fmt.Sprint(n%10+1), Outbound)
			_ = ps.All()
			_ = ps.Len()
		}(i)
	}

	wg.Wait()
	assert.LessOrEqual(t, ps.Len(), 10)
}

func TestPeerSet_RecordProbe(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	_, _ = ps.Register("10.0.0.1", Outbound)

	ps.RecordProbe("10.0.0.1", false)
	ps.RecordProbe("10.0.0.1", false)

	p := ps.All()[0]
	assert.Equal(t, 2, p.ConsecFails)
	assert.False(t, p.LastProbe.IsZero())

	ps.RecordProbe("10.0.0.1", true)
	assert.Equal(t, 0, ps.All()[0].ConsecFails)

	// unknown IP is a no-op
	ps.RecordProbe("10.9.9.9", false)
	assert.Equal(t, 1, ps.Len())
}

func TestPeerSet_ExpireStale(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	now := time.Now()
	stale := now.Add(-peerTTL - time.Hour)

	ps.mu.Lock()
	ps.peers["10.0.0.1"] = &Peer{IP: "10.0.0.1", LastSeen: stale, ConsecFails: failBackoffThreshold}
	ps.peers["10.0.0.2"] = &Peer{IP: "10.0.0.2", LastSeen: stale, ConsecFails: failBackoffThreshold, WasParent: true}
	ps.peers["10.0.0.3"] = &Peer{IP: "10.0.0.3", LastSeen: stale, ConsecFails: 0}                  // old but reachable
	ps.peers["10.0.0.4"] = &Peer{IP: "10.0.0.4", LastSeen: now, ConsecFails: failBackoffThreshold} // unreachable but fresh
	ps.mu.Unlock()

	expired := ps.ExpireStale(now)

	assert.ElementsMatch(t, []string{"10.0.0.1", "10.0.0.2"}, expired)
	assert.Equal(t, 2, ps.Len())
	assert.True(t, ps.Dirty())

	// nothing left to expire
	assert.Empty(t, ps.ExpireStale(now))
}

func TestPeerSet_LoadSkipsInvalidAndExpired(t *testing.T) {
	dir := t.TempDir()
	stale := time.Now().Add(-2 * peerTTL).Format(time.RFC3339)
	fresh := time.Now().Format(time.RFC3339)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "peers.json"), []byte(`[
  {"ip":"10.0.0.1","last_seen":"`+fresh+`"},
  {"ip":"not-an-ip","last_seen":"`+fresh+`"},
  {"ip":"10.0.0.2","last_seen":"`+stale+`"}
]`), 0o644))

	ps := NewPeerSet(dir)
	require.NoError(t, ps.Load())
	require.Equal(t, 1, ps.Len())
	assert.Equal(t, "10.0.0.1", ps.All()[0].IP)
}

func TestPeerSet_LoadDoesNotClobberLiveEntry(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "peers.json"), []byte(`[
  {"ip":"10.0.0.1","port":4005,"directions":{"outbound":true},"was_parent":true,"last_seen":"`+old.Format(time.RFC3339)+`"}
]`), 0o644))

	ps := NewPeerSet(dir)
	_, _ = ps.Register("10.0.0.1", Inbound) // producer got there first
	require.NoError(t, ps.Load())

	peers := ps.All()
	require.Len(t, peers, 1)
	p := peers[0]
	assert.True(t, p.LastSeen.After(old), "fresh registration kept")
	assert.True(t, p.Directions[Inbound], "live direction kept")
	assert.True(t, p.Directions[Outbound], "persisted direction merged")
	assert.True(t, p.WasParent)
	assert.Equal(t, 4005, p.Port, "persisted port adopted when live entry had none")
}

func TestPeerSet_SuggestPortOnlyWhenUnknown(t *testing.T) {
	ps := NewPeerSet(t.TempDir())
	ps.SuggestPort("10.0.0.1", 4001) // unknown peer: no-op
	assert.Equal(t, 0, ps.Len())

	_, _ = ps.Register("10.0.0.1", Inbound)
	ps.SuggestPort("10.0.0.1", 4001)
	assert.Equal(t, 4001, ps.All()[0].Port)

	ps.UpdatePort("10.0.0.1", 4007) // proven by a probe
	ps.SuggestPort("10.0.0.1", 4001)
	assert.Equal(t, 4007, ps.All()[0].Port, "a suggestion never overrides a proven port")
}
