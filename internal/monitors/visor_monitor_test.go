package monitors

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const visorSnapshot = `{
  "initial_height": 100,
  "height": 250,
  "scheduled_freeze_height": null,
  "hardfork_version": 7,
  "consensus_time": "2026-09-10T12:00:03.500Z",
  "wall_clock_time": "2026-09-10T12:00:01.000Z",
  "reference_lag": 0.25,
  "unknown_field": {"x": 1}
}`

func TestDecodeVisorState(t *testing.T) {
	s, err := decodeVisorState([]byte(visorSnapshot))
	require.NoError(t, err)
	assert.Equal(t, int64(100), s.InitialHeight)
	assert.Equal(t, int64(250), s.Height)
	assert.Nil(t, s.ScheduledFreezeHeight)
	require.NotNil(t, s.HardforkVersion)
	assert.Equal(t, int64(7), *s.HardforkVersion)
	require.NotNil(t, s.ReferenceLagSeconds)
	assert.Equal(t, 0.25, *s.ReferenceLagSeconds)

	// a mistyped optional field does not suppress the valid height
	s, err = decodeVisorState([]byte(`{"height": 5, "hardfork_version": "seven", "scheduled_freeze_height": -1}`))
	require.NoError(t, err)
	assert.Equal(t, int64(5), s.Height)
	assert.Nil(t, s.HardforkVersion)
	assert.Nil(t, s.ScheduledFreezeHeight, "negative rejected")

	// a mistyped required field does
	_, err = decodeVisorState([]byte(`{"height": "5"}`))
	require.Error(t, err)
}

func TestLastFullLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"two lines", "a\nb\n", "b", true},
		{"single line", "line\n", "line", true},
		{"unterminated suffix ignored", "a\nb", "a", true},
		{"blank lines skipped", "a\n\n\n", "a", true},
		{"only blanks", "\n\n", "", false},
		{"no newline", "partial", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := lastFullLine([]byte(tc.in))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestReadLatestVisorState(t *testing.T) {
	home := t.TempDir()
	snapshot := filepath.Join(home, "hyperliquid_data", "visor_abci_state.json")
	hourly := filepath.Join(home, "data", "visor_abci_states", "hourly")

	// nothing yet
	_, _, err := readLatestVisorState(snapshot, hourly)
	require.ErrorIs(t, err, os.ErrNotExist)

	// hourly fallback: last complete line wins, unterminated suffix ignored
	dateDir := filepath.Join(hourly, "20260910")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dateDir, "11"), []byte(
		`["2026-09-10T11:59:00.000", {"height": 10, "initial_height": 1}]`+"\n"+
			`["2026-09-10T11:59:30.000", {"height": 20, "initial_height": 1}]`+"\n"+
			`["2026-09-10T12:00:00.000", {"height": 30`), 0o644))
	s, ts, err := readLatestVisorState(snapshot, hourly)
	require.NoError(t, err)
	assert.Equal(t, int64(20), s.Height)
	assert.Equal(t, time.Date(2026, 9, 10, 11, 59, 30, 0, time.UTC), ts)

	// live snapshot preferred, sample time from wall_clock_time
	require.NoError(t, os.MkdirAll(filepath.Dir(snapshot), 0o755))
	require.NoError(t, os.WriteFile(snapshot, []byte(visorSnapshot), 0o644))
	s, ts, err = readLatestVisorState(snapshot, hourly)
	require.NoError(t, err)
	assert.Equal(t, int64(250), s.Height)
	assert.Equal(t, time.Date(2026, 9, 10, 12, 0, 1, 0, time.UTC), ts)

	// an invalid snapshot falls through to the hourly log
	require.NoError(t, os.WriteFile(snapshot, []byte(`{"height": 0}`), 0o644))
	s, _, err = readLatestVisorState(snapshot, hourly)
	require.NoError(t, err)
	assert.Equal(t, int64(20), s.Height)
}

func TestPublishVisorState(t *testing.T) {
	initTestMetrics(t)
	s, err := decodeVisorState([]byte(visorSnapshot))
	require.NoError(t, err)
	publishVisorState(s)

	h, ok := metrics.GaugeValue(metrics.HLVisorHeight)
	require.True(t, ok)
	assert.Equal(t, 250.0, h)
	applied, _ := metrics.GaugeValue(metrics.HLVisorBlocksApplied)
	assert.Equal(t, 150.0, applied)
	hf, ok := metrics.GaugeSeriesValue(metrics.HLVisorHardforkVersion, attribute.String("source", "visor_state"))
	require.True(t, ok)
	assert.Equal(t, 7.0, hf)
	_, ok = metrics.GaugeValue(metrics.HLVisorScheduledFreezeHeight)
	assert.False(t, ok, "null freeze height is absent, not zero")
	ahead, _ := metrics.GaugeValue(metrics.HLVisorConsensusAheadOfWallSecond)
	assert.InDelta(t, 2.5, ahead, 1e-9)
	assert.Equal(t, int64(250), latestVisorHeight.Load())

	// fields withdrawn on omission
	s.HardforkVersion = nil
	s.ReferenceLagSeconds = nil
	publishVisorState(s)
	_, ok = metrics.GaugeSeriesValue(metrics.HLVisorHardforkVersion, attribute.String("source", "visor_state"))
	assert.False(t, ok)
	_, ok = metrics.GaugeValue(metrics.HLVisorReferenceLagSeconds)
	assert.False(t, ok)
}
