package monitors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
)

func TestParentPeerMonitor_IdentifiesParent(t *testing.T) {
	initTestMetrics(t)

	var parentIP string
	m := NewParentPeerMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string) { parentIP = ip },
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	// 162.55.245.102 has massive In bytes, everything else is zero or negligible
	content := `["2026-03-31T06:00:15.263",[[["In","162.55.245.102",4001],1.318],[["Out","192.168.108.236",4001],1.318],[["In","92.62.119.169",4001],1.67e-7],[["Out","157.230.45.219",4002],0.0],[["In","185.247.137.82",4002],0.0]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0)
	require.NoError(t, err)

	assert.Equal(t, "162.55.245.102", m.currentParent)
	assert.Equal(t, "162.55.245.102", parentIP)
	assert.False(t, m.parentSince.IsZero())
}

func TestParentPeerMonitor_ParentSwitch(t *testing.T) {
	initTestMetrics(t)

	var parents []string
	m := NewParentPeerMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string) { parents = append(parents, ip) },
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	// first line: peer A is dominant, second line: peer B takes over
	content := `["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],1.5],[["In","10.0.0.2",4001],0.0]]]
["2026-03-31T06:00:45.263",[[["In","10.0.0.1",4001],0.0],[["In","10.0.0.2",4001],2.0]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0)
	require.NoError(t, err)

	// last line wins: 10.0.0.2 is the parent
	assert.Equal(t, "10.0.0.2", m.currentParent)
	// callback was called for both parent assignments
	assert.Equal(t, []string{"10.0.0.2"}, parents)
}

func TestParentPeerMonitor_IgnoresOutTraffic(t *testing.T) {
	initTestMetrics(t)

	var parentIP string
	m := NewParentPeerMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string) { parentIP = ip },
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	// huge Out to 192.168.108.236, small In from 10.0.0.1
	content := `["2026-03-31T06:00:15.263",[[["Out","192.168.108.236",4001],5.0],[["In","10.0.0.1",4001],0.5]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0)
	require.NoError(t, err)

	assert.Equal(t, "10.0.0.1", parentIP)
}

func TestParentPeerMonitor_MalformedLines(t *testing.T) {
	initTestMetrics(t)

	var parentIP string
	m := NewParentPeerMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(ip string) { parentIP = ip },
	)

	dateDir := filepath.Join(m.dir, "20260331")
	require.NoError(t, os.MkdirAll(dateDir, 0o755))

	content := `not json
["2026-03-31T06:00:26.471","not an array"]
["2026-03-31T06:00:26.471",[[["In","10.0.0.1",4001],0.5]]]
["2026-03-31T06:00:26.471",[["broken"]]]
`
	f := filepath.Join(dateDir, "6")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o644))

	_, err := m.processFile(f, 0)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", parentIP)
}

func TestParentPeerMonitor_EmptyDir(t *testing.T) {
	initTestMetrics(t)

	m := NewParentPeerMonitor(
		&config.Config{NodeHome: t.TempDir()},
		func(string) {},
	)

	// dir doesn't exist yet
	m.poll() // should not panic
}

func TestFindTopInboundPeer(t *testing.T) {
	line := []byte(`["2026-03-31T06:00:15.263",[[["In","10.0.0.1",4001],1.5],[["In","10.0.0.2",4001],0.3],[["Out","10.0.0.3",4001],5.0],[["In","10.0.0.4",4001],0.0]]]`)

	topIP, topBytes, secondIP, secondBytes := findTopInboundPeer(line)

	assert.Equal(t, "10.0.0.1", topIP)
	assert.InDelta(t, 1.5, topBytes, 0.001)
	assert.Equal(t, "10.0.0.2", secondIP)
	assert.InDelta(t, 0.3, secondBytes, 0.001)
}

func TestFindTopInboundPeer_NoInTraffic(t *testing.T) {
	line := []byte(`["2026-03-31T06:00:15.263",[[["Out","10.0.0.1",4001],1.5]]]`)

	topIP, topBytes, _, _ := findTopInboundPeer(line)

	assert.Empty(t, topIP)
	assert.Zero(t, topBytes)
}
