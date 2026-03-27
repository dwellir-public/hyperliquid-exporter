package config

import (
	"os"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

// chdir to a temp dir so godotenv.Load() finds no .env file
func isolateEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestDefaultsNilFlags(t *testing.T) {
	isolateEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(nil)

	if want := home + "/hl"; cfg.NodeHome != want {
		t.Errorf("NodeHome = %q, want %q", cfg.NodeHome, want)
	}
	if want := home + "/hl-node"; cfg.NodeBinary != want {
		t.Errorf("NodeBinary = %q, want %q", cfg.NodeBinary, want)
	}
	if cfg.LiveStateCheckInterval != 5*time.Second {
		t.Errorf("LiveStateCheckInterval = %v, want 5s", cfg.LiveStateCheckInterval)
	}
	if cfg.ReplicaBufferSize != 8 {
		t.Errorf("ReplicaBufferSize = %d, want 8", cfg.ReplicaBufferSize)
	}
}

func TestDefaultsEmptyFlags(t *testing.T) {
	isolateEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{})

	if want := home + "/hl"; cfg.NodeHome != want {
		t.Errorf("NodeHome = %q, want %q", cfg.NodeHome, want)
	}
	if cfg.EnableValidatorRTT {
		t.Error("EnableValidatorRTT should default to false")
	}
}

func TestEnvVarOverride(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/unused")
	t.Setenv("NODE_HOME", "/custom/node")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{})

	if cfg.NodeHome != "/custom/node" {
		t.Errorf("NodeHome = %q, want /custom/node", cfg.NodeHome)
	}
	if want := "/custom/node/data/replica_cmds"; cfg.ReplicaDataDir != want {
		t.Errorf("ReplicaDataDir = %q, want %q", cfg.ReplicaDataDir, want)
	}
}

func TestFlagOverridesEnvVar(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/unused")
	t.Setenv("NODE_HOME", "/from-env")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{NodeHome: "/from-flag"})

	if cfg.NodeHome != "/from-flag" {
		t.Errorf("NodeHome = %q, want /from-flag (flag should win)", cfg.NodeHome)
	}
}

func TestValidatorRTTNil(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/tmp")
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{EnableValidatorRTT: nil})
	if cfg.EnableValidatorRTT {
		t.Error("nil *bool should leave EnableValidatorRTT as false")
	}
}

func TestValidatorRTTTrue(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/tmp")
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{EnableValidatorRTT: boolPtr(true)})
	if !cfg.EnableValidatorRTT {
		t.Error("EnableValidatorRTT should be true")
	}
}

func TestValidatorRTTFalse(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/tmp")
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{EnableValidatorRTT: boolPtr(false)})
	if cfg.EnableValidatorRTT {
		t.Error("EnableValidatorRTT should be false")
	}
}

func TestReplicaDataDirDerived(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/h")
	t.Setenv("NODE_HOME", "/mynode")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{})

	if want := "/mynode/data/replica_cmds"; cfg.ReplicaDataDir != want {
		t.Errorf("ReplicaDataDir = %q, want %q", cfg.ReplicaDataDir, want)
	}
}

func TestReplicaBufferSizeHardcoded(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/tmp")
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{ReplicaBufferSize: 64})

	if cfg.ReplicaBufferSize != 8 {
		t.Errorf("ReplicaBufferSize = %d, want 8 (hardcoded)", cfg.ReplicaBufferSize)
	}
}

func TestEVMBlockTypeFollowsEVM(t *testing.T) {
	isolateEnv(t)
	t.Setenv("HOME", "/tmp")
	t.Setenv("NODE_HOME", "")
	t.Setenv("BINARY_HOME", "")
	t.Setenv("NODE_BINARY", "")

	cfg := LoadConfig(&Flags{EnableEVM: true, EVMBlockTypeMetrics: false})

	if !cfg.EVMBlockTypeMetrics {
		t.Error("EVMBlockTypeMetrics should follow EnableEVM, not the flag")
	}
}
