package monitors

import (
	"sort"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	// parentPeerEWMAAlpha smooths per-IP inbound volume across tcp_traffic
	// samples so one noisy interval cannot flip the parent.
	parentPeerEWMAAlpha = 0.3
	// parentPeerSwitchRatio is the hysteresis: a challenger must exceed the
	// incumbent's smoothed value by this factor to take over.
	parentPeerSwitchRatio = 1.2
	// parentPeerPruneBelow drops decayed peers from the EWMA map.
	parentPeerPruneBelow = 1e-9
	// parentPeerMaxAge clears the parent when no record with positive
	// inbound traffic has been seen for this long.
	parentPeerMaxAge = 90 * time.Second
)

// ParentPeerMonitor infers the node's upstream ("parent") peer from
// tcp_traffic inbound byte volumes. "In" is a byte direction, not a
// connection role, so the parent is an inference: the endpoint that
// dominates smoothed inbound traffic. It consumes records from
// TCPTrafficMonitor rather than reading the file itself.
type ParentPeerMonitor struct {
	currentParent string
	parentSince   time.Time
	ewma          map[string]float64
	lastPositive  time.Time // receipt time of the last record with inbound traffic
	setParentPeer func(string)
}

func NewParentPeerMonitor(setParentPeer func(string)) *ParentPeerMonitor {
	return &ParentPeerMonitor{
		ewma:          make(map[string]float64),
		setParentPeer: setParentPeer,
	}
}

type rankedPeer struct {
	ip    string
	value float64
}

// rankEWMA orders peers by smoothed value descending, IP ascending on ties,
// so selection is deterministic regardless of map iteration order.
func rankEWMA(values map[string]float64) []rankedPeer {
	out := make([]rankedPeer, 0, len(values))
	for ip, v := range values {
		if v > 0 {
			out = append(out, rankedPeer{ip: ip, value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].value != out[j].value {
			return out[i].value > out[j].value
		}
		return ipLess(out[i].ip, out[j].ip)
	})
	return out
}

func (m *ParentPeerMonitor) onRecord(rec tcpTrafficRecord, receivedAt time.Time, seeding bool) {
	// an all-zero interval is not evidence for anyone and leaves the
	// smoothed values untouched; staleness clears the parent if it persists
	if len(rec.inbound) == 0 || rec.inbound[0].value <= 0 {
		return
	}
	m.lastPositive = receivedAt

	latest := make(map[string]float64, len(rec.inbound))
	for _, s := range rec.inbound {
		latest[s.ip] = s.value
		m.ewma[s.ip] = parentPeerEWMAAlpha*s.value + (1-parentPeerEWMAAlpha)*m.ewma[s.ip]
	}

	// decay peers absent from this record
	for ip, v := range m.ewma {
		if _, seen := latest[ip]; seen {
			continue
		}
		v *= 1 - parentPeerEWMAAlpha
		if v < parentPeerPruneBelow {
			delete(m.ewma, ip)
		} else {
			m.ewma[ip] = v
		}
	}

	ranked := rankEWMA(m.ewma)
	if len(ranked) == 0 {
		return
	}
	best := ranked[0]
	switch {
	case m.currentParent == "":
		m.adopt(best.ip, receivedAt, "Initial parent peer identified: %s")
	case best.ip != m.currentParent && best.value > m.ewma[m.currentParent]*parentPeerSwitchRatio:
		metrics.RemoveParentPeer(m.currentParent)
		metrics.IncrementParentPeerSwitches()
		logger.InfoComponent("parent-peer", "Parent peer changed: %s -> %s", m.currentParent, best.ip)
		m.adopt(best.ip, receivedAt, "")
	}

	if !seeding {
		metrics.AddParentPeerTrafficVolume(m.currentParent, latest[m.currentParent])
	}
	m.publish(receivedAt, latest[m.currentParent], ranked)
}

func (m *ParentPeerMonitor) adopt(ip string, now time.Time, logFmt string) {
	if logFmt != "" {
		logger.InfoComponent("parent-peer", logFmt, ip)
	}
	m.currentParent = ip
	m.parentSince = now
	quality.SetParent(ip)
	if m.setParentPeer != nil {
		m.setParentPeer(ip)
	}
}

func (m *ParentPeerMonitor) publish(now time.Time, latest float64, ranked []rankedPeer) {
	selected := m.ewma[m.currentParent]
	var total, challenger float64
	for _, p := range ranked {
		total += p.value
		if p.ip != m.currentParent && p.value > challenger {
			challenger = p.value
		}
	}
	share, ratio := 0.0, 0.0
	if total > 0 {
		share = selected / total
	}
	if selected > 0 {
		ratio = challenger / selected
	}

	metrics.SetParentPeer(m.currentParent)
	metrics.SetParentPeerTraffic(m.currentParent, latest)
	metrics.SetParentPeerTenure(now.Sub(m.parentSince).Seconds())
	metrics.SetParentPeerRatios(share, ratio)
}

// onPoll clears the parent once inbound traffic has been absent for
// parentPeerMaxAge, so a dead source does not keep the role forever.
func (m *ParentPeerMonitor) onPoll(now time.Time) {
	if m.currentParent == "" || now.Sub(m.lastPositive) <= parentPeerMaxAge {
		quality.Flush()
		return
	}
	logger.InfoComponent("parent-peer", "No inbound traffic for %s, clearing parent peer %s", parentPeerMaxAge, m.currentParent)
	metrics.RemoveParentPeer(m.currentParent)
	metrics.SetParentPeerTenure(0)
	metrics.SetParentPeerRatios(0, 0)
	m.currentParent = ""
	m.parentSince = time.Time{}
	clear(m.ewma)
	quality.SetParent("")
	quality.Flush()
	if m.setParentPeer != nil {
		m.setParentPeer("")
	}
}
