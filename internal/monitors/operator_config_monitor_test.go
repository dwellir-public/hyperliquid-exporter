package monitors

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

func TestReadJailingConfig(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		stage string // empty for success
	}{
		{"valid", `{"dry_run": true, "latency_ema_jail_threshold": 0.75}`, ""},
		{"extra fields tolerated", `{"dry_run": false, "latency_ema_jail_threshold": 1, "x": 1}`, ""},
		{"missing threshold", `{"dry_run": true}`, "schema"},
		{"null dry_run", `{"dry_run": null, "latency_ema_jail_threshold": 0.75}`, "schema"},
		{"wrong type", `{"dry_run": "yes", "latency_ema_jail_threshold": 0.75}`, "decode"},
		{"not json", `nope`, "decode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), jailingConfigFile)
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o644))
			jc, err := readJailingConfig(path)
			if tc.stage == "" {
				require.NoError(t, err)
				require.NotNil(t, jc)
				return
			}
			var oerr *operatorConfigError
			require.True(t, errors.As(err, &oerr))
			assert.Equal(t, tc.stage, oerr.stage)
		})
	}

	jc, err := readJailingConfig(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	assert.Nil(t, jc, "absent file is not an error")
}

func TestScanOperatorConfig(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	write := func(name, body string, age time.Duration) {
		path := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		require.NoError(t, os.Chtimes(path, now.Add(-age), now.Add(-age)))
	}
	write("firewall_ips.json", "[]", time.Hour)
	write("firewall_ips.json_FAILED_LOAD", "", 0)
	write("mystery.json_FAILED_LOAD", "", 0)
	write(jailingConfigFile, `{"dry_run": true, "latency_ema_jail_threshold": 0.75}`, time.Minute)

	s, err := scanOperatorConfig(root, now)
	require.NoError(t, err)
	assert.Equal(t, 1.0, s.present["firewall_ips.json"])
	assert.Equal(t, 0.0, s.present["n_gossip_peers.json"])
	assert.InDelta(t, 3600, s.age["firewall_ips.json"], 1)
	_, hasAge := s.age["n_gossip_peers.json"]
	assert.False(t, hasAge)
	assert.Equal(t, int64(1), s.failed["firewall_ips.json"])
	assert.Equal(t, int64(1), s.failed["unknown"])
	require.NotNil(t, s.jailing)
	assert.True(t, *s.jailing.DryRun)
	assert.Equal(t, 0.75, *s.jailing.LatencyEmaJailThreshold)

	// a tracked name that is a directory is a stat failure for that file only
	require.NoError(t, os.Mkdir(filepath.Join(root, "n_gossip_peers.json"), 0o755))
	s, err = scanOperatorConfig(root, now)
	require.NoError(t, err)
	assert.Equal(t, -1.0, s.present["n_gossip_peers.json"])
	assert.Equal(t, 1.0, s.present["firewall_ips.json"])

	// a broken jailing config rejects the tick with its stage
	write(jailingConfigFile, `{}`, 0)
	_, err = scanOperatorConfig(root, now)
	var oerr *operatorConfigError
	require.True(t, errors.As(err, &oerr))
	assert.Equal(t, "schema", oerr.stage)

	_, err = scanOperatorConfig(filepath.Join(root, "missing"), now)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTickOperatorConfig_PublishesAndWithdraws(t *testing.T) {
	initTestMetrics(t)
	root := filepath.Join(t.TempDir(), "file_mod_time_tracker")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, jailingConfigFile), []byte(`{"dry_run": false, "latency_ema_jail_threshold": 0.5}`), 0o644))

	require.True(t, tickOperatorConfig(root))
	thr, ok := metrics.GaugeValue(metrics.HLNodeJailingThresholdSeconds)
	require.True(t, ok)
	assert.Equal(t, 0.5, thr)
	dry, _ := metrics.GaugeValue(metrics.HLNodeJailingDryRun)
	assert.Equal(t, 0.0, dry)
	present, _ := metrics.GaugeSeriesValue(metrics.HLNodeOperatorConfigPresent, attribute.String("file", jailingConfigFile))
	assert.Equal(t, 1.0, present)
	present, _ = metrics.GaugeSeriesValue(metrics.HLNodeOperatorConfigPresent, attribute.String("file", "firewall_ips.json"))
	assert.Equal(t, 0.0, present)
	_, ok = metrics.GaugeSeriesValue(metrics.HLNodeOperatorConfigAgeSeconds, attribute.String("file", "firewall_ips.json"))
	assert.False(t, ok, "absent file has no age series")

	// jailing file removed: its gauges are withdrawn, not zeroed
	require.NoError(t, os.Remove(filepath.Join(root, jailingConfigFile)))
	require.True(t, tickOperatorConfig(root))
	_, ok = metrics.GaugeValue(metrics.HLNodeJailingThresholdSeconds)
	assert.False(t, ok)

	// tracker directory removed: everything is withdrawn
	require.NoError(t, os.RemoveAll(root))
	require.False(t, tickOperatorConfig(root))
	_, ok = metrics.GaugeSeriesValue(metrics.HLNodeOperatorConfigPresent, attribute.String("file", jailingConfigFile))
	assert.False(t, ok)
}
