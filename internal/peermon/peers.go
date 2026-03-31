package peermon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxPeers = 128

// PeerDirection indicates how this node relates to a peer.
type PeerDirection string

const (
	Inbound  PeerDirection = "inbound"
	Outbound PeerDirection = "outbound"
	Unknown  PeerDirection = "unknown"
)

// Peer represents a known peer with its last-seen timestamp.
type Peer struct {
	IP         string                   `json:"ip"`
	Port       int                      `json:"port,omitempty"`
	Directions map[PeerDirection]bool   `json:"directions"`
	LastSeen   time.Time                `json:"last_seen"`
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
	if net.ParseIP(ip) == nil {
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

// All returns a snapshot of all peers.
func (ps *PeerSet) All() []Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	out := make([]Peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		out = append(out, *p)
	}
	return out
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

// Load reads peers from disk. Returns nil on missing file.
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

	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i := range peers {
		p := peers[i]
		if p.IP == "" {
			continue
		}
		if len(ps.peers) >= maxPeers {
			break
		}
		if p.Directions == nil {
			p.Directions = map[PeerDirection]bool{}
		}
		ps.peers[p.IP] = &p
	}
	return nil
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
		peers = append(peers, *p)
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

// evictOldest removes the peer with the oldest LastSeen. Must be called with mu held.
func (ps *PeerSet) evictOldest() string {
	var oldestIP string
	var oldestTime time.Time

	for ip, p := range ps.peers {
		if oldestIP == "" || p.LastSeen.Before(oldestTime) {
			oldestIP = ip
			oldestTime = p.LastSeen
		}
	}

	if oldestIP != "" {
		delete(ps.peers, oldestIP)
	}
	return oldestIP
}
