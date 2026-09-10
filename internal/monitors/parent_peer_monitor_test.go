package monitors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feedParent parses each line and hands the record to the parent monitor,
// stamping receipts base + i*30s so staleness can be asserted.
func feedParent(t *testing.T, m *ParentPeerMonitor, base time.Time, lines ...string) {
	t.Helper()
	for i, line := range lines {
		rec, err := parseTCPTrafficRecord([]byte(line))
		require.NoError(t, err, "line %d", i)
		m.onRecord(rec, base.Add(time.Duration(i)*30*time.Second), false)
	}
}

func TestParentPeerMonitor_IdentifiesParent(t *testing.T) {
	initTestMetrics(t)
	var parentIP string
	m := NewParentPeerMonitor(func(ip string) { parentIP = ip })

	// 162.55.245.102 has massive In bytes, everything else is zero or negligible
	feedParent(t, m, time.Now(),
		`["2026-03-31T06:00:15.263",[[["In","162.55.245.102",4001],1.318],[["Out","192.168.108.236",4001],1.318],[["In","92.62.119.169",4001],1.67e-7],[["Out","157.230.45.219",4002],0.0],[["In","185.247.137.82",4002],0.0]]]`)

	assert.Equal(t, "162.55.245.102", m.currentParent)
	assert.Equal(t, "162.55.245.102", parentIP)
	assert.False(t, m.parentSince.IsZero())
}

func TestParentPeerMonitor_SwitchNeedsHysteresis(t *testing.T) {
	initTestMetrics(t)
	var parents []string
	m := NewParentPeerMonitor(func(ip string) { parents = append(parents, ip) })

	feedParent(t, m, time.Now(),
		// A leads
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5],[["In","10.0.0.2",4001],0.0]]]`,
		// B ahead this interval, but not 1.2x the smoothed incumbent
		`["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],1.0],[["In","10.0.0.2",4001],2.0]]]`,
	)
	assert.Equal(t, "10.0.0.1", m.currentParent, "one interval is not enough to switch")

	feedParent(t, m, time.Now(),
		`["2026-03-31T06:01:00.000",[[["In","10.0.0.1",4001],0.0],[["In","10.0.0.2",4001],2.0]]]`,
		`["2026-03-31T06:01:30.000",[[["In","10.0.0.1",4001],0.0],[["In","10.0.0.2",4001],2.0]]]`,
	)
	assert.Equal(t, "10.0.0.2", m.currentParent)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, parents)
}

func TestParentPeerMonitor_DeterministicTieBreak(t *testing.T) {
	initTestMetrics(t)
	m := NewParentPeerMonitor(nil)

	feedParent(t, m, time.Now(),
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.9",4001],1.0],[["In","2.0.0.1",4001],1.0],[["In","10.0.0.5",4001],1.0]]]`)
	assert.Equal(t, "2.0.0.1", m.currentParent, "numerically lowest IP wins an exact tie")
}

func TestParentPeerMonitor_AggregatesAcrossPorts(t *testing.T) {
	initTestMetrics(t)
	m := NewParentPeerMonitor(nil)

	// 10.0.0.1 has two small flows that together beat 10.0.0.2's single flow
	feedParent(t, m, time.Now(),
		`["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],0.6],[["In","10.0.0.1",4002],0.6],[["In","10.0.0.2",4001],1.0]]]`)
	assert.Equal(t, "10.0.0.1", m.currentParent)
}

func TestParentPeerMonitor_IgnoresOutTraffic(t *testing.T) {
	initTestMetrics(t)
	var parentIP string
	m := NewParentPeerMonitor(func(ip string) { parentIP = ip })

	feedParent(t, m, time.Now(),
		`["2026-03-31T06:00:15.263",[[["Out","192.168.108.236",4001],5.0],[["In","10.0.0.1",4001],0.5]]]`)
	assert.Equal(t, "10.0.0.1", parentIP)
}

func TestParentPeerMonitor_StaleClears(t *testing.T) {
	initTestMetrics(t)
	var parents []string
	m := NewParentPeerMonitor(func(ip string) { parents = append(parents, ip) })
	base := time.Now()

	feedParent(t, m, base, `["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5]]]`)
	require.Equal(t, "10.0.0.1", m.currentParent)

	// all-zero interval is not evidence; parent survives within the age window
	rec, err := parseTCPTrafficRecord([]byte(`["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.0]]]`))
	require.NoError(t, err)
	m.onRecord(rec, base.Add(30*time.Second), false)
	m.onPoll(base.Add(60 * time.Second))
	assert.Equal(t, "10.0.0.1", m.currentParent)
	assert.InDelta(t, 0.45, m.ewma["10.0.0.1"], 1e-9, "all-zero record leaves the EWMA untouched")

	m.onPoll(base.Add(parentPeerMaxAge + time.Second))
	assert.Empty(t, m.currentParent)
	assert.Empty(t, m.ewma)
	assert.Equal(t, []string{"10.0.0.1", ""}, parents)
}

func TestParentPeerMonitor_MalformedLines(t *testing.T) {
	var parentIP string
	p := NewParentPeerMonitor(func(ip string) { parentIP = ip })
	m, f := writeTCPTrafficFile(t, `not json
["2026-03-31T06:00:26.471","not an array"]
["2026-03-31T06:00:26.471",[[["In","10.0.0.1",4001],0.5]]]
["2026-03-31T06:00:26.471",[["broken"]]]
`, p)

	_, err := m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", parentIP)
}

func TestRankEWMA(t *testing.T) {
	ranked := rankEWMA(map[string]float64{"b": 1, "a": 1, "c": 2, "z": 0})
	require.Len(t, ranked, 3, "zero values excluded")
	assert.Equal(t, "c", ranked[0].ip)
	assert.Equal(t, "a", ranked[1].ip)
	assert.Equal(t, "b", ranked[2].ip)
}
