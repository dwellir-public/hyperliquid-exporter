package monitors

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

type registration struct {
	ip  string
	dir peermon.PeerDirection
}

type outboundStubs struct {
	m          *OutboundPeersMonitor
	registered []registration
	ports      map[string]int
	traffic    []trafficCall
	active     []activeCall
}

func newOutboundStubs(t *testing.T) *outboundStubs {
	t.Helper()
	initTestMetrics(t)
	s := &outboundStubs{ports: make(map[string]int)}
	s.m = NewOutboundPeersMonitor(
		func(ip string, dir peermon.PeerDirection) { s.registered = append(s.registered, registration{ip, dir}) },
		func(ip string, port int) { s.ports[ip] = port },
	)
	s.m.addTrafficVolume = func(ip, dir string, volume float64) {
		s.traffic = append(s.traffic, trafficCall{ip, dir, volume})
	}
	s.m.addActiveSeconds = func(ip string, seconds float64) {
		s.active = append(s.active, activeCall{ip, seconds})
	}
	return s
}

// feed parses each line and delivers it with receipts base + i*30s.
func (s *outboundStubs) feed(t *testing.T, base time.Time, seeding bool, lines ...string) {
	t.Helper()
	for i, line := range lines {
		rec, err := parseTCPTrafficRecord([]byte(line))
		require.NoError(t, err, "line %d", i)
		s.m.onRecord(rec, base.Add(time.Duration(i)*30*time.Second), seeding)
	}
}

func TestOutboundPeersMonitor_AdmitsAfterTwoPositiveRecords(t *testing.T) {
	s := newOutboundStubs(t)
	s.feed(t, time.Now(), false,
		`["2026-03-31T06:00:26.471",[[["In","192.168.108.167",4001],1.278],[["Out","192.168.108.167",4002],0.0],[["In","10.0.0.1",4001],0.5]]]`,
		`["2026-03-31T06:00:56.477",[[["In","192.168.108.167",4001],1.03],[["Out","10.0.0.2",4002],0.0]]]`,
	)

	// only 192.168.108.167 inbound carried positive traffic twice in a row;
	// zero-volume and single-appearance endpoints are not admitted
	assert.Equal(t, []registration{{"192.168.108.167", peermon.Inbound}}, s.registered)
	assert.Equal(t, 4001, s.ports["192.168.108.167"])
}

func TestOutboundPeersMonitor_StreakResetsOnAbsenceAndGap(t *testing.T) {
	s := newOutboundStubs(t)
	base := time.Now()
	s.feed(t, base, false,
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.0]]]`,
		`["2026-03-31T06:00:30.000",[[["In","10.0.0.2",4001],1.0]]]`, // 10.0.0.1 absent: streak reset
		`["2026-03-31T06:01:00.000",[[["In","10.0.0.1",4001],1.0]]]`,
	)
	assert.Empty(t, s.registered)

	// a receipt gap over tcpAdmissionMaxGap also resets
	rec, err := parseTCPTrafficRecord([]byte(`["2026-03-31T06:05:00.000",[[["In","10.0.0.1",4001],1.0]]]`))
	require.NoError(t, err)
	s.m.onRecord(rec, base.Add(time.Hour), false)
	assert.Empty(t, s.registered)

	rec, err = parseTCPTrafficRecord([]byte(`["2026-03-31T06:05:30.000",[[["In","10.0.0.1",4001],1.0]]]`))
	require.NoError(t, err)
	s.m.onRecord(rec, base.Add(time.Hour+30*time.Second), false)
	assert.Equal(t, []registration{{"10.0.0.1", peermon.Inbound}}, s.registered)
}

func TestOutboundPeersMonitor_TopNPerDirection(t *testing.T) {
	s := newOutboundStubs(t)
	var flows string
	for i := range tcpTrafficTopN + 4 {
		if i > 0 {
			flows += ","
		}
		// values descending so 10.0.1.0 is the busiest, 10.0.1.19 the quietest
		flows += `[["In","10.0.1.` + strconv.Itoa(i) + `",4001],` + strconv.Itoa(100-i) + `]`
	}
	line := `["2026-03-31T06:00:00.000",[` + flows + `]]`
	s.feed(t, time.Now(), false, line, line)

	assert.Len(t, s.registered, tcpTrafficTopN)
	for _, r := range s.registered {
		assert.NotContains(t, []string{"10.0.1.16", "10.0.1.17", "10.0.1.18", "10.0.1.19"}, r.ip)
	}
}

func TestOutboundPeersMonitor_TrafficVolume(t *testing.T) {
	s := newOutboundStubs(t)
	s.feed(t, time.Now(), false,
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5],[["Out","10.0.0.2",4002],2.0]]]`,
		`["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5],[["Out","10.0.0.2",4002],1.0]]]`,
	)

	sums := make(map[trafficCall]float64)
	for _, c := range s.traffic {
		sums[trafficCall{ip: c.ip, direction: c.direction}] += c.volume
	}
	assert.InDelta(t, 2.0, sums[trafficCall{ip: "10.0.0.1", direction: string(peermon.Inbound)}], 0.001)
	assert.InDelta(t, 3.0, sums[trafficCall{ip: "10.0.0.2", direction: string(peermon.Outbound)}], 0.001)
}

func TestOutboundPeersMonitor_ActiveSeconds(t *testing.T) {
	s := newOutboundStubs(t)
	// line 1: peer A active, peer B silent. line 2 (+30s): both active.
	// line 3 (+45s): peer B only.
	s.feed(t, time.Now(), false,
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5],[["Out","10.0.0.2",4002],0.0]]]`,
		`["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5],[["Out","10.0.0.2",4002],2.0]]]`,
		`["2026-03-31T06:01:15.000",[[["In","10.0.0.1",4001],0.0],[["Out","10.0.0.2",4002],1.0]]]`,
	)

	sums := make(map[string]float64)
	for _, c := range s.active {
		sums[c.ip] += c.seconds
	}
	// first line contributes no active time (no prior lastLineTS)
	assert.InDelta(t, 30.0, sums["10.0.0.1"], 0.001)
	assert.InDelta(t, 75.0, sums["10.0.0.2"], 0.001)
}

func TestOutboundPeersMonitor_SeedSuppression(t *testing.T) {
	s := newOutboundStubs(t)
	s.feed(t, time.Now(), true,
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5]]]`,
		`["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5]]]`,
	)

	// seed pass: counters suppressed, discovery still happens
	assert.Empty(t, s.traffic)
	assert.Empty(t, s.active)
	assert.Equal(t, []registration{{"10.0.0.1", peermon.Inbound}}, s.registered)

	// steady state after the seed: the seeded lastLineTS carries over
	s.feed(t, time.Now().Add(time.Minute), false,
		`["2026-03-31T06:01:00.000",[[["In","10.0.0.1",4001],0.5]]]`)
	assert.Len(t, s.traffic, 1)
	require.Len(t, s.active, 1)
	assert.InDelta(t, 30.0, s.active[0].seconds, 0.001)
}
