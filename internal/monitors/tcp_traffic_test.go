package monitors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	api "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// writeTCPTrafficFile writes content under a fresh tcp_traffic/hourly tree
// and returns the monitor (with consumers attached) and the file path.
func writeTCPTrafficFile(t *testing.T, content string, consumers ...tcpTrafficConsumer) (*TCPTrafficMonitor, string) {
	t.Helper()
	initTestMetrics(t)
	m := NewTCPTrafficMonitor(&config.Config{NodeHome: t.TempDir()}, consumers...)
	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))
	return m, f
}

func TestParseTCPTrafficRecord(t *testing.T) {
	line := []byte(`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],1.5],[["In","10.0.0.1",4002],0.5],[["In","10.0.0.2",4001],0.3],[["Out","10.0.0.3",4001],5.0],[["In","::ffff:10.0.0.4",4001],0.0]]]`)

	rec, err := parseTCPTrafficRecord(line)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 31, 6, 0, 15, 263000000, time.UTC), rec.timestamp)

	require.Len(t, rec.inbound, 3)
	// per-IP aggregation across ports, port of the largest flow kept
	assert.Equal(t, peerSample{ip: "10.0.0.1", port: 4001, value: 2.0}, rec.inbound[0])
	assert.Equal(t, peerSample{ip: "10.0.0.2", port: 4001, value: 0.3}, rec.inbound[1])
	// IPv4-mapped IPv6 is unmapped
	assert.Equal(t, peerSample{ip: "10.0.0.4", port: 4001, value: 0}, rec.inbound[2])

	require.Len(t, rec.outbound, 1)
	assert.Equal(t, "10.0.0.3", rec.outbound[0].ip)
}

func TestParseTCPTrafficRecord_PortOfLargestFlow(t *testing.T) {
	line := []byte(`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],6],[["In","10.0.0.1",4002],7],[["In","10.0.0.1",4003],8]]]`)
	rec, err := parseTCPTrafficRecord(line)
	require.NoError(t, err)
	require.Len(t, rec.inbound, 1)
	assert.Equal(t, peerSample{ip: "10.0.0.1", port: 4003, value: 21}, rec.inbound[0])
}

func TestParseTCPTrafficRecord_Rejects(t *testing.T) {
	cases := map[string]struct {
		line  string
		stage string
	}{
		"not json":          {`not json`, "record"},
		"arity":             {`["2026-03-31T06:00:15.263"]`, "record"},
		"bad timestamp":     {`["yesterday",[]]`, "timestamp"},
		"null timestamp":    {`[null,[]]`, "timestamp"},
		"flows not array":   {`["2026-03-31T06:00:15.263","x"]`, "record"},
		"flow arity":        {`["2026-03-31T06:00:15.263",[["broken"]]]`, "row"},
		"key arity":         {`["2026-03-31T06:00:15.263",[[["In","10.0.0.1"],1.0]]]`, "row"},
		"bad ip":            {`["2026-03-31T06:00:15.263",[[["In","nope",4001],1.0]]]`, "row"},
		"zoned ip":          {`["2026-03-31T06:00:15.263",[[["In","fe80::1%eth0",4001],1.0]]]`, "row"},
		"zero port":         {`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",0],1.0]]]`, "row"},
		"negative value":    {`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],-1.0]]]`, "row"},
		"null value":        {`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],null]]]`, "row"},
		"unknown dir":       {`["2026-03-31T06:00:15.263",[[["Sideways","10.0.0.1",4001],1.0]]]`, "row"},
		"one bad row":       {`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],1.0],[["In","bad",4001],1.0]]]`, "row"},
		"whole record lost": {`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],1.0],["broken"]]]`, "row"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec, err := parseTCPTrafficRecord([]byte(tc.line))
			require.Error(t, err)
			var perr *tcpTrafficParseError
			require.True(t, errors.As(err, &perr))
			assert.Equal(t, tc.stage, perr.stage)
			assert.Empty(t, rec.inbound)
		})
	}
}

type recordingConsumer struct {
	records []tcpTrafficRecord
	seeding []bool
	polls   int
}

func (c *recordingConsumer) onRecord(rec tcpTrafficRecord, _ time.Time, seeding bool) {
	c.records = append(c.records, rec)
	c.seeding = append(c.seeding, seeding)
}

func (c *recordingConsumer) onPoll(time.Time) { c.polls++ }

func TestTCPTrafficMonitor_FansOutOnce(t *testing.T) {
	a, b := &recordingConsumer{}, &recordingConsumer{}
	m, f := writeTCPTrafficFile(t, `["2026-03-31T06:00:00.000",[[["In","10.0.0.1",4001],1.5]]]
not json
["2026-03-31T06:00:30.000",[[["In","10.0.0.1",4001],0.5]]]
`, a, b)

	offset, err := m.processFile(f, 0, true)
	require.NoError(t, err)
	assert.Greater(t, offset, int64(0))

	for _, c := range []*recordingConsumer{a, b} {
		require.Len(t, c.records, 2, "malformed line skipped, both good records delivered")
		assert.Equal(t, []bool{true, true}, c.seeding)
	}

	// nothing new: no records, but every consumer still gets its poll tick
	m.tail.path, m.tail.offset = f, offset
	m.poll()
	assert.Len(t, a.records, 2)
	assert.Equal(t, 1, a.polls)
	assert.Equal(t, 1, b.polls)
}

func TestTCPTrafficMonitor_SeedingSkipsParseErrorCounter(t *testing.T) {
	m, f := writeTCPTrafficFile(t, "not json\n")
	calls := 0
	restore := metrics.HLExporterParseErrorsCounter
	t.Cleanup(func() { metrics.HLExporterParseErrorsCounter = restore })
	metrics.HLExporterParseErrorsCounter = &countingInt64Counter{onAdd: func() { calls++ }}

	_, err := m.processFile(f, 0, true)
	require.NoError(t, err)
	assert.Equal(t, 0, calls, "replayed history is not counted")

	_, err = m.processFile(f, 0, false)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

// countingInt64Counter is a test double for an OTel Int64Counter.
type countingInt64Counter struct {
	noop.Int64Counter
	onAdd func()
}

func (c *countingInt64Counter) Add(context.Context, int64, ...api.AddOption) { c.onAdd() }

func TestTCPTrafficMonitor_EmptyDir(t *testing.T) {
	initTestMetrics(t)
	c := &recordingConsumer{}
	m := NewTCPTrafficMonitor(&config.Config{NodeHome: t.TempDir()}, c)
	m.poll() // dir does not exist: no panic, consumers still ticked
	assert.Equal(t, 1, c.polls)
}
