package monitors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

func TestClassifyChildStderr(t *testing.T) {
	cases := []struct {
		name string
		head string
		want string
	}{
		{"app hash", "ERROR computed app hash 0xab does not match quorum", "app_hash_mismatch"},
		{"hardfork", "Observed QC for newer hardfork, exiting", "hardfork_upgrade"},
		{"sync overflow", "too many blocks to request", "sync_overflow"},
		{"config", "ip 1.2.3.4 is not in node_ips", "config_error"},
		{"network", "upstream connect error or disconnect", "network"},
		{"class beats panic", "thread panicked at src/x.rs: computed app hash mismatch does not match quorum", "app_hash_mismatch"},
		{"explicit panic", "thread 'main' panicked at 'boom'", "panic"},
		{"panic word alone", "panic reporting mode enabled", "unknown"},
		{"empty-ish", "hello", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyChildStderr([]byte(tc.head)))
		})
	}
}

func writeArtifact(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestScanChildStderr(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, "20270724/0/stderr", "")
	crash := writeArtifact(t, root, "20270724/1/stderr", "computed app hash x does not match quorum")
	writeArtifact(t, root, "20270725/0/stderr", strings.Repeat("x", childStderrHeadBytes+1)+"panicked at")
	writeArtifact(t, root, "20270725/1/stderr", "nothing we recognize")
	require.NoError(t, os.WriteFile(filepath.Join(root, "20270724", "stray"), []byte("x"), 0o644)) // non-dir at level 2: ignored

	seen, err := scanChildStderr(root, nil)
	require.NoError(t, err)
	require.Len(t, seen, 4)
	assert.Equal(t, childStderrStateEmpty, seen[filepath.Join(root, "20270724/0/stderr")].state)
	assert.Equal(t, "app_hash_mismatch", seen[crash].reason)
	// oversized artifact: truncated, classified from the prefix only
	big := seen[filepath.Join(root, "20270725/0/stderr")]
	assert.Equal(t, childStderrStateTruncated, big.state)
	assert.Equal(t, childStderrReasonUnknown, big.reason, "panic text beyond the head is not seen")
	assert.Equal(t, childStderrReasonUnknown, seen[filepath.Join(root, "20270725/1/stderr")].reason)

	// unchanged files reuse the prior classification without a reread
	prior := seen[crash]
	prior.reason = "sentinel"
	again, err := scanChildStderr(root, seen)
	require.NoError(t, err)
	assert.Equal(t, "sentinel", again[crash].reason)

	// a grown file is reclassified
	require.NoError(t, os.WriteFile(crash, []byte("observed qc for newer hardfork"), 0o644))
	require.NoError(t, os.Chtimes(crash, time.Now(), time.Now().Add(time.Second)))
	again, err = scanChildStderr(root, seen)
	require.NoError(t, err)
	assert.Equal(t, "hardfork_upgrade", again[crash].reason)
}

func TestScanChildStderr_MissingRoot(t *testing.T) {
	_, err := scanChildStderr(filepath.Join(t.TempDir(), "nope"), nil)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTickChildStderr_Publishes(t *testing.T) {
	initTestMetrics(t)
	root := t.TempDir()
	writeArtifact(t, root, "20270724/0/stderr", "")
	writeArtifact(t, root, "20270724/1/stderr", "connection timeout")
	writeArtifact(t, root, "20270724/2/stderr", "connection timeout")

	seen := map[string]*childStderrState{}
	require.True(t, tickChildStderr(root, seen))

	starts, ok := metrics.GaugeValue(metrics.HLNodeChildStarts)
	require.True(t, ok)
	assert.Equal(t, 3.0, starts)
	crashes, _ := metrics.GaugeSeriesValue(metrics.HLNodeChildCrashes, attribute.String("reason", "network"))
	assert.Equal(t, 2.0, crashes)
	last, _ := metrics.GaugeSeriesValue(metrics.HLNodeChildLastCrashSeconds, attribute.String("reason", "network"))
	assert.InDelta(t, float64(time.Now().Unix()), last, 5)
	none, _ := metrics.GaugeSeriesValue(metrics.HLNodeChildCrashes, attribute.String("reason", "panic"))
	assert.Equal(t, 0.0, none, "every reason series is present at zero")
	empty, _ := metrics.GaugeSeriesValue(metrics.HLNodeChildStderrArtifacts, attribute.String("state", "empty"), attribute.String("reason", "none"))
	assert.Equal(t, 1.0, empty)

	// a failed scan retains the previous snapshot
	require.NoError(t, os.RemoveAll(root))
	require.False(t, tickChildStderr(root, seen))
	assert.Len(t, seen, 3)
}
