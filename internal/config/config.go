package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
)

type Config struct {
	HomeDir                string
	NodeHome               string
	BinaryHome             string
	NodeBinary             string
	Chain                  string
	EnableEVM              bool
	EVMBlockTypeMetrics    bool
	EnableCoreTxMetrics    bool
	UseLiveState           bool
	LiveStateCheckInterval time.Duration // How often to check for updates
	EnableReplicaMetrics   bool
	ReplicaDataDir         string
	ReplicaBufferSize      int
	EnableValidatorRTT     bool
	EnablePeerLatency      bool
	EnableProcess          bool // hl-node and hl-visor liveness and resources from /proc
	EnableChildStderr      bool // hl-visor child crash artifacts
	EnableVisor            bool // visor sync state
	EnableNodeState        bool // persisted node-state heights under hyperliquid_data
	EnableDisk             bool // NODE_HOME size and filesystem capacity
	EnableOperatorConfig   bool // file_mod_time_tracker configs, validator nodes only
	EnableCritMsg          bool // bug! and crit! counters from crit_msg_stats
	EnableBinaryMetrics    bool // compare local hl-visor against the published one (network)
	LogLevel               string
}

type Flags struct {
	NodeHome             string
	NodeBinary           string
	Chain                string
	EnableEVM            bool
	EVMBlockTypeMetrics  bool
	EnableCoreTxMetrics  bool
	UseLiveState         bool
	EnableReplicaMetrics bool
	ReplicaDataDir       string
	ReplicaBufferSize    int
	EnableValidatorRTT   *bool // to distinguish between not set and false
	EnablePeerLatency    *bool
	EnableProcess        bool
	EnableChildStderr    bool
	EnableVisor          bool
	EnableNodeState      bool
	EnableDisk           bool
	EnableOperatorConfig bool
	EnableCritMsg        bool
	EnableBinaryMetrics  bool
	LogLevel             string
}

// load env vars and returns a Config struct
func LoadConfig(flags *Flags) Config {
	// load .env first
	if err := godotenv.Load(); err != nil {
		logger.Debug("No .env file found, using environment variables and flags")
	}

	homeDir := os.Getenv("HOME")

	nodeHome := os.Getenv("NODE_HOME")
	if nodeHome == "" {
		nodeHome = homeDir + "/hl" //default fallback
	}

	binaryHome := os.Getenv("BINARY_HOME")
	if binaryHome == "" {
		binaryHome = homeDir
	}

	nodeBinary := os.Getenv("NODE_BINARY")
	if nodeBinary == "" {
		nodeBinary = binaryHome + "/hl-node"
	}

	// always use default replica data dir
	replicaDataDir := nodeHome + "/data/replica_cmds"

	// always default buffer size
	replicaBufferSize := 8 // 8MB default

	if flags == nil {
		return Config{
			NodeHome:               nodeHome,
			BinaryHome:             binaryHome,
			NodeBinary:             nodeBinary,
			LiveStateCheckInterval: 5 * time.Second,
			ReplicaDataDir:         replicaDataDir,
			ReplicaBufferSize:      replicaBufferSize,
		}
	}

	config := Config{
		NodeHome:               nodeHome,
		BinaryHome:             binaryHome,
		NodeBinary:             nodeBinary,
		Chain:                  flags.Chain,
		EnableEVM:              flags.EnableEVM,
		EVMBlockTypeMetrics:    flags.EVMBlockTypeMetrics,
		EnableCoreTxMetrics:    flags.EnableCoreTxMetrics,
		UseLiveState:           flags.UseLiveState,
		LiveStateCheckInterval: 5 * time.Second,
		EnableReplicaMetrics:   flags.EnableReplicaMetrics,
		ReplicaDataDir:         replicaDataDir,
		ReplicaBufferSize:      replicaBufferSize,
		EnableValidatorRTT:     false,
		EnableProcess:          flags.EnableProcess,
		EnableChildStderr:      flags.EnableChildStderr,
		EnableVisor:            flags.EnableVisor,
		EnableNodeState:        flags.EnableNodeState,
		EnableDisk:             flags.EnableDisk,
		EnableOperatorConfig:   flags.EnableOperatorConfig,
		EnableCritMsg:          flags.EnableCritMsg,
		EnableBinaryMetrics:    flags.EnableBinaryMetrics,
		LogLevel:               flags.LogLevel,
	}

	if flags.NodeHome != "" {
		config.NodeHome = flags.NodeHome
	}
	if flags.NodeBinary != "" {
		config.NodeBinary = flags.NodeBinary
	}
	if flags.Chain != "" {
		config.Chain = flags.Chain
	}
	config.EVMBlockTypeMetrics = config.EnableEVM
	if flags.EnableCoreTxMetrics != config.EnableCoreTxMetrics {
		config.EnableCoreTxMetrics = flags.EnableCoreTxMetrics
	}
	if flags.UseLiveState != config.UseLiveState {
		config.UseLiveState = flags.UseLiveState
	}
	if flags.EnableReplicaMetrics != config.EnableReplicaMetrics {
		config.EnableReplicaMetrics = flags.EnableReplicaMetrics
	}
	if flags.EnableValidatorRTT != nil {
		config.EnableValidatorRTT = *flags.EnableValidatorRTT
	}
	if flags.EnablePeerLatency != nil {
		config.EnablePeerLatency = *flags.EnablePeerLatency
	}

	return config
}
