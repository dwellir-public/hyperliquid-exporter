package config

import (
	"path/filepath"
	"testing"
)

func TestPeerDataDir(t *testing.T) {
	for _, flags := range []*Flags{nil, {}, {NodeHome: "/override/hl"}} {
		t.Run("configured-and-default", func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("NODE_HOME", "/node/hl")
			t.Setenv("EXPORTER_STATE_DIR", "/var/lib/hyperliquid-exporter")
			cfg := LoadConfig(flags)
			if got := cfg.PeerDataDir(); got != "/var/lib/hyperliquid-exporter" {
				t.Fatalf("PeerDataDir() = %q, want independent exporter state", got)
			}
			t.Setenv("EXPORTER_STATE_DIR", "")
			cfg = LoadConfig(flags)
			want := filepath.Join(filepath.Dir(cfg.NodeHome), ".hyperliquid-exporter")
			if got := cfg.PeerDataDir(); got != want {
				t.Fatalf("PeerDataDir() = %q, want legacy path %q", got, want)
			}
		})
	}
}
