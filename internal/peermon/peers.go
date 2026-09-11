package peermon

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxPeers = 256

	// failBackoffThreshold is the consecutive probe failures after which a
	// peer is probed at backoffProbeInterval instead of every cycle, and
	// becomes eligible for TTL expiry.
	failBackoffThreshold = 5
	// peerTTL is how long a peer may go unseen in node logs before an
	// unreachable peer is expired from the set.
	peerTTL = 48 * time.Hour
)

// PeerDirection indicates how this node relates to a peer.
type PeerDirection string

const (
	Inbound  PeerDirection = "inbound"
	Outbound PeerDirection = "outbound"
	Unknown  PeerDirection = "unknown"
)

// Peer represents a known peer with its last-seen timestamp.
type Peer struct {
	IP          string                 `json:"ip"`
	Port        int                    `json:"port,omitempty"`
	Directions  map[PeerDirection]bool `json:"directions"`
	LastSeen    time.Time              `json:"last_seen"`
	WasParent   bool                   `json:"was_parent,omitempty"`
	ConsecFails int                    `json:"consec_fails,omitempty"`
	LastProbe   time.Time              `json:"last_probe,omitzero"`
}

// validPeerIP is the single admission predicate for every path that adds a
// peer (Register, MarkParent, Load): a bare, routable IPv4 or IPv6 address
// that is not one of this host's own. hl-node's tcp_traffic lists loopback
// and the node's own interfaces with real byte rates, so without this the
// exporter would probe itself.
func validPeerIP(ip string) bool {
	addr := net.ParseIP(ip)
	if addr == nil || addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
		return false
	}
	return !localAddrs()[addr.String()]
}

var (
	localOnce sync.Once
	localSet  map[string]bool
)

// localAddrs returns the set of addresses bound to this host's interfaces,
// resolved once; the exporter's interfaces do not change at runtime.
func localAddrs() map[string]bool {
	localOnce.Do(func() {
		localSet = map[string]bool{}
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				localSet[n.IP.String()] = true
			}
		}
	})
	return localSet
}

// PeerSet is a thread-safe, bounded set of peers with JSON persistence.
type PeerSet struct {
	mu    sync.RWMutex
	peers map[string]*Peer
	path  string // file path for persistence
	dirty bool
	gen   uint64
}

// NewPeerSet creates a PeerSet that persists to dir/peers.json.
func NewPeerSet(dir string) *PeerSet {
	return &PeerSet{
		peers: make(map[string]*Peer),
		path:  filepath.Join(dir, "peers.json"),
	}
}

// Register adds or updates a peer. If at capacity and the IP is new,
// the peer with the oldest LastSeen is evicted and returned.
// Returns ("", false) silently for invalid IPs (not a bare IPv4/IPv6 address).
func (ps *PeerSet) Register(ip string, dir PeerDirection) (string, bool) {
	if !validPeerIP(ip) {
		return "", false
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	if p, exists := ps.peers[ip]; exists {
		p.LastSeen = time.Now()
		if dir != Unknown {
			p.Directions[dir] = true
		}
		ps.gen++
		ps.dirty = true
		return "", false
	}

	var evictedIP string
	if len(ps.peers) >= maxPeers {
		evictedIP = ps.evictOldest()
	}

	dirs := map[PeerDirection]bool{}
	if dir != Unknown {
		dirs[dir] = true
	}
	ps.peers[ip] = &Peer{IP: ip, Directions: dirs, LastSeen: time.Now()}
	ps.gen++
	ps.dirty = true
	return evictedIP, evictedIP != ""
}

// MarkParent flags a peer as having served as parent, exempting it from
// LRU eviction. Registers the IP first if unknown. Like Register, it
// returns the evicted peer's IP when adding the parent displaced one.
func (ps *PeerSet) MarkParent(ip string) (string, bool) {
	if !validPeerIP(ip) {
		return "", false
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	var evictedIP string
	p, exists := ps.peers[ip]
	if !exists {
		if len(ps.peers) >= maxPeers {
			evictedIP = ps.evictOldest()
		}
		p = &Peer{IP: ip, Directions: map[PeerDirection]bool{}, LastSeen: time.Now()}
		ps.peers[ip] = p
	}

	p.WasParent = true
	ps.gen++
	ps.dirty = true
	return evictedIP, evictedIP != ""
}

// UpdatePort records the last successful port for a peer.
func (ps *PeerSet) UpdatePort(ip string, port int) {
	if port <= 0 {
		return
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, exists := ps.peers[ip]
	if !exists || p.Port == port {
		return
	}

	p.Port = port
	ps.gen++
	ps.dirty = true
}

// RecordProbe updates a peer's probe bookkeeping: LastProbe is stamped and
// ConsecFails resets on success or increments on failure.
func (ps *PeerSet) RecordProbe(ip string, ok bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, exists := ps.peers[ip]
	if !exists {
		return
	}

	p.LastProbe = time.Now()
	if ok {
		p.ConsecFails = 0
	} else {
		p.ConsecFails++
	}
	ps.gen++
	ps.dirty = true
}

// ExpireStale removes peers unseen in node logs for peerTTL that are also
// unreachable (at or past failBackoffThreshold consecutive probe failures),
// returning the expired IPs. Ex-parents are not exempt: the WasParent LRU
// exemption preserves quality history through churn, but a dead ex-parent
// must not squat in the set forever.
func (ps *PeerSet) ExpireStale(now time.Time) []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	var expired []string
	for ip, p := range ps.peers {
		if now.Sub(p.LastSeen) > peerTTL && p.ConsecFails >= failBackoffThreshold {
			delete(ps.peers, ip)
			expired = append(expired, ip)
		}
	}
	if len(expired) > 0 {
		ps.gen++
		ps.dirty = true
	}
	return expired
}

// All returns a snapshot of all peers.
func (ps *PeerSet) All() []Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	out := make([]Peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		out = append(out, copyPeer(p))
	}
	return out
}

// copyPeer deep-copies a peer so callers can use it outside the lock.
func copyPeer(p *Peer) Peer {
	c := *p
	c.Directions = make(map[PeerDirection]bool, len(p.Directions))
	maps.Copy(c.Directions, p.Directions)
	return c
}

// Len returns the number of peers.
func (ps *PeerSet) Len() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.peers)
}

// Dirty reports whether the set has changed since last save.
func (ps *PeerSet) Dirty() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.dirty
}

// Load merges peers from disk into the set. Returns nil on missing file.
// Entries with an invalid IP or unseen for longer than peerTTL are skipped,
// and an entry never overrides a peer that was registered more recently.
func (ps *PeerSet) Load() error {
	data, err := os.ReadFile(ps.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read peers file: %w", err)
	}

	var peers []Peer
	if err := json.Unmarshal(data, &peers); err != nil {
		return fmt.Errorf("parse peers file: %w", err)
	}

	cutoff := time.Now().Add(-peerTTL)
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i := range peers {
		p := peers[i]
		if !validPeerIP(p.IP) || p.LastSeen.Before(cutoff) {
			continue
		}
		if p.Directions == nil {
			p.Directions = map[PeerDirection]bool{}
		}
		if live, exists := ps.peers[p.IP]; exists {
			maps.Copy(live.Directions, p.Directions)
			live.WasParent = live.WasParent || p.WasParent
			if live.Port == 0 {
				live.Port = p.Port
			}
			continue
		}
		if len(ps.peers) >= maxPeers {
			break
		}
		ps.peers[p.IP] = &p
	}
	return nil
}

// SuggestPort records a port observed in node logs for a peer that has no
// proven port yet, so the prober tries it first.
func (ps *PeerSet) SuggestPort(ip string, port int) {
	if port <= 0 {
		return
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, exists := ps.peers[ip]
	if !exists || p.Port != 0 {
		return
	}
	p.Port = port
	ps.gen++
	ps.dirty = true
}

// Save writes peers to disk atomically. No-op if not dirty.
func (ps *PeerSet) Save() error {
	peers, gen, ok := ps.snapshotForSave()
	if !ok {
		return nil
	}

	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peers: %w", err)
	}

	dir := filepath.Dir(ps.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create peers dir: %w", err)
	}

	tmp := ps.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp peers file: %w", err)
	}
	if err := os.Rename(tmp, ps.path); err != nil {
		return fmt.Errorf("rename peers file: %w", err)
	}

	ps.finishSave(gen)
	return nil
}

func (ps *PeerSet) snapshotForSave() ([]Peer, uint64, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if !ps.dirty {
		return nil, 0, false
	}

	peers := make([]Peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		peers = append(peers, copyPeer(p))
	}
	return peers, ps.gen, true
}

func (ps *PeerSet) finishSave(gen uint64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.gen == gen {
		ps.dirty = false
	}
}

// evictOldest removes the peer with the oldest LastSeen, preferring
// non-parent peers so ex-parents keep their quality history. Falls back to
// oldest overall only if every peer is an ex-parent (so Register can't
// fail). Must be called with mu held.
func (ps *PeerSet) evictOldest() string {
	oldestIP := ps.oldest(true)
	if oldestIP == "" {
		oldestIP = ps.oldest(false)
	}

	if oldestIP != "" {
		delete(ps.peers, oldestIP)
	}
	return oldestIP
}

// oldest returns the IP of the oldest-LastSeen peer. If skipParents is
// true, ex-parent peers are excluded from consideration.
func (ps *PeerSet) oldest(skipParents bool) string {
	var oldestIP string
	var oldestTime time.Time

	for ip, p := range ps.peers {
		if skipParents && p.WasParent {
			continue
		}
		if oldestIP == "" || p.LastSeen.Before(oldestTime) {
			oldestIP = ip
			oldestTime = p.LastSeen
		}
	}
	return oldestIP
}
