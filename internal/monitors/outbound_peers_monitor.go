package monitors

import (
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
)

const (
	// tcpTrafficTopN bounds admission to the busiest endpoints per direction
	// so a flood of transient connections cannot churn the peer set.
	tcpTrafficTopN = 16
	// tcpAdmissionStreak is how many consecutive records an endpoint must
	// carry positive traffic in before it is registered as a peer.
	tcpAdmissionStreak = 2
	// tcpAdmissionMaxGap resets streaks after a gap in records.
	tcpAdmissionMaxGap = 90 * time.Second
)

// OutboundPeersMonitor discovers peers from tcp_traffic records and accounts
// per-peer traffic volume and active time. It consumes records from
// TCPTrafficMonitor rather than reading the file itself.
type OutboundPeersMonitor struct {
	registerPeer func(string, peermon.PeerDirection)
	suggestPort  func(string, int)
	seen         map[string]struct{}
	lastLineTS   time.Time
	lastReceipt  time.Time
	admission    map[peerEntry]int // consecutive positive-traffic records per endpoint

	addTrafficVolume func(peerIP, direction string, volume float64)
	addActiveSeconds func(peerIP string, seconds float64)
}

func NewOutboundPeersMonitor(registerPeer func(string, peermon.PeerDirection), suggestPort func(string, int)) *OutboundPeersMonitor {
	return &OutboundPeersMonitor{
		registerPeer:     registerPeer,
		suggestPort:      suggestPort,
		seen:             make(map[string]struct{}),
		admission:        make(map[peerEntry]int),
		addTrafficVolume: metrics.AddPeerTrafficVolume,
		addActiveSeconds: metrics.AddPeerActiveSeconds,
	}
}

type peerEntry struct {
	ip  string
	dir peermon.PeerDirection
}

// onRecord accounts one tcp_traffic snapshot. During the startup seed pass
// counter emission is suppressed to avoid double-counting samples Prometheus
// recorded before the restart; admission and lastLineTS tracking still run.
func (m *OutboundPeersMonitor) onRecord(rec tcpTrafficRecord, receivedAt time.Time, seeding bool) {
	if !seeding && !m.lastLineTS.IsZero() {
		delta := clampDelta(rec.timestamp.Sub(m.lastLineTS).Seconds())
		active := make(map[string]struct{})
		for _, list := range [][]peerSample{rec.inbound, rec.outbound} {
			for _, s := range list {
				if s.value > 0 {
					active[s.ip] = struct{}{}
				}
			}
		}
		for ip := range active {
			m.addActiveSeconds(ip, delta)
		}
	}
	if !seeding {
		for _, s := range rec.inbound {
			m.addTrafficVolume(s.ip, string(peermon.Inbound), s.value)
		}
		for _, s := range rec.outbound {
			m.addTrafficVolume(s.ip, string(peermon.Outbound), s.value)
		}
	}
	m.lastLineTS = rec.timestamp

	m.admit(rec, receivedAt)
}

// admit registers endpoints that carried positive traffic in the top N of
// their direction for tcpAdmissionStreak consecutive records.
func (m *OutboundPeersMonitor) admit(rec tcpTrafficRecord, receivedAt time.Time) {
	if !m.lastReceipt.IsZero() && receivedAt.Sub(m.lastReceipt) > tcpAdmissionMaxGap {
		clear(m.admission)
	}
	m.lastReceipt = receivedAt

	eligible := make(map[peerEntry]int, 2*tcpTrafficTopN)
	for dir, list := range map[peermon.PeerDirection][]peerSample{peermon.Inbound: rec.inbound, peermon.Outbound: rec.outbound} {
		n := 0
		for _, s := range list {
			if s.value <= 0 || n >= tcpTrafficTopN {
				break
			}
			eligible[peerEntry{s.ip, dir}] = s.port
			n++
		}
	}
	for pe := range m.admission {
		if _, ok := eligible[pe]; !ok {
			delete(m.admission, pe)
		}
	}
	for pe, port := range eligible {
		m.admission[pe]++
		if m.admission[pe] < tcpAdmissionStreak {
			continue
		}
		if _, ok := m.seen[pe.ip]; !ok {
			m.seen[pe.ip] = struct{}{}
			logger.InfoComponent("gossip", "Discovered peer %s from tcp_traffic", pe.ip)
		}
		if m.registerPeer != nil {
			m.registerPeer(pe.ip, pe.dir)
		}
		if m.suggestPort != nil {
			m.suggestPort(pe.ip, port)
		}
	}
}

func (m *OutboundPeersMonitor) onPoll(time.Time) {}

func clampDelta(seconds float64) float64 {
	if seconds < 0 {
		return 0
	}
	if seconds > maxAccountGapSeconds {
		return maxAccountGapSeconds
	}
	return seconds
}
