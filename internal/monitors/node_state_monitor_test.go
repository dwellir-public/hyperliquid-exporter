package monitors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

func TestReadSingleInt(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int64
		ok   bool
	}{
		{"plain", "12345", 12345, true},
		{"trailing newline", "12345\n", 12345, true},
		{"leading whitespace", "  42 ", 42, true},
		{"empty", "", 0, false},
		{"blank", "\n", 0, false},
		{"non numeric", "abc", 0, false},
		{"float", "1.5", 0, false},
		{"negative", "-1", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o644))
			got, ok := readSingleInt(path)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
	_, ok := readSingleInt(filepath.Join(t.TempDir(), "missing"))
	assert.False(t, ok)
}

func TestTickNodeState(t *testing.T) {
	initTestMetrics(t)
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	fastClass := attribute.String("source_class", "evm_db_hub_fast_cp_checkpoint_height")
	slowClass := attribute.String("source_class", "evm_db_hub_slow_cp_checkpoint_height")
	gap := attribute.String("comparison", "fast_minus_slow")
	above := attribute.String("comparison", "visor_minus_persisted_freeze")
	freezeFile := attribute.String("file", "freeze_abci_height")

	// nothing readable: nothing published, every file marked unavailable
	require.False(t, tickNodeState(root))
	avail, ok := metrics.GaugeSeriesValue(metrics.HLNodePersistedStateFileAvailable, freezeFile)
	require.True(t, ok)
	assert.Equal(t, 0.0, avail)

	write("freeze_abci_height", "1000\n")
	write("evm_db_hub_fast/cp_checkpoint_height", "1500")
	write("evm_db_hub_slow/cp_checkpoint_height", "1400")
	latestVisorHeight.Store(0)
	require.True(t, tickNodeState(root))

	fast, _ := metrics.GaugeSeriesValue(metrics.HLNodePersistedABCIHeight, fastClass)
	assert.Equal(t, 1500.0, fast)
	slow, _ := metrics.GaugeSeriesValue(metrics.HLNodePersistedABCIHeight, slowClass)
	assert.Equal(t, 1400.0, slow)
	g, _ := metrics.GaugeSeriesValue(metrics.HLNodePersistedABCIHeightGap, gap)
	assert.Equal(t, 100.0, g)
	_, ok = metrics.GaugeSeriesValue(metrics.HLNodeVisorHeightAbovePersistedFrz, above)
	assert.False(t, ok, "no visor height yet")

	// visor reported: height above freeze clamps at zero below the replay floor
	latestVisorHeight.Store(900)
	require.True(t, tickNodeState(root))
	a, ok := metrics.GaugeSeriesValue(metrics.HLNodeVisorHeightAbovePersistedFrz, above)
	require.True(t, ok)
	assert.Equal(t, 0.0, a)
	latestVisorHeight.Store(1250)
	require.True(t, tickNodeState(root))
	a, _ = metrics.GaugeSeriesValue(metrics.HLNodeVisorHeightAbovePersistedFrz, above)
	assert.Equal(t, 250.0, a)

	// slow file gone: its series and the gap are withdrawn, fast stays
	require.NoError(t, os.Remove(filepath.Join(root, "evm_db_hub_slow", "cp_checkpoint_height")))
	require.True(t, tickNodeState(root))
	_, ok = metrics.GaugeSeriesValue(metrics.HLNodePersistedABCIHeight, slowClass)
	assert.False(t, ok)
	_, ok = metrics.GaugeSeriesValue(metrics.HLNodePersistedABCIHeightGap, gap)
	assert.False(t, ok)
	_, ok = metrics.GaugeSeriesValue(metrics.HLNodePersistedABCIHeight, fastClass)
	assert.True(t, ok)
}
