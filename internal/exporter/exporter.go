package exporter

import (
	"context"
	"path/filepath"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/monitors"
	"github.com/validaoxyz/hyperliquid-exporter/internal/peermon"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

const persistentFilesDir = ".hyperliquid-exporter"

func Start(ctx context.Context, cfg config.Config) {
	logger.InfoComponent("system", "Starting Hyperliquid exporter...")

	// create context with cancellation for monitor goroutines
	monitorCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// start monitors with error channels
	blockErrCh := make(chan error, 1)
	proposalErrCh := make(chan error, 1)
	validatorErrCh := make(chan error, 1)
	versionErrCh := make(chan error, 1)
	updateErrCh := make(chan error, 1)
	evmErrCh := make(chan error)
	evmTxsErrCh := make(chan error)
	evmAccountErrCh := make(chan error)
	validatorStatusErrCh := make(chan error)
	validatorIPErrCh := make(chan error, 1)
	coreTxErrCh := make(chan error, 1)
	consensusErrCh := make(chan error, 1)
	replicaErrCh := make(chan error, 1)
	latencyErrCh := make(chan error, 1)
	gossipErrCh := make(chan error, 1)
	gossipConnErrCh := make(chan error, 1)
	peerLatencyErrCh := make(chan error, 1)

	logger.InfoComponent("core", "Initializing block monitor...")
	safego.Go("core", func() { monitors.StartBlockMonitor(monitorCtx, cfg, blockErrCh) })

	// start val API monitor early to populate signer mappings
	logger.InfoComponent("consensus", "Initializing validator API monitor...")
	safego.Go("consensus", func() { monitors.StartValidatorMonitor(monitorCtx, cfg, validatorErrCh) })

	// only initialize proposal monitor if replica metrics are disabled
	if !cfg.EnableReplicaMetrics {
		logger.InfoComponent("consensus", "Initializing proposal monitor...")
		safego.Go("consensus", func() { monitors.StartProposalMonitor(monitorCtx, cfg, proposalErrCh) })
	} else {
		logger.InfoComponent("consensus", "Proposal monitor disabled - replica monitor will handle proposer counting")
	}

	logger.InfoComponent("system", "Initializing version monitor...")
	safego.Go("system", func() { monitors.StartVersionMonitor(monitorCtx, cfg, versionErrCh) })

	logger.InfoComponent("system", "Initializing update checker...")
	safego.Go("system", func() { monitors.StartUpdateChecker(monitorCtx, cfg, updateErrCh) })

	if cfg.EnableEVM {
		// use EVM monitor that reads from evm_block_and_receipts
		logger.InfoComponent("evm", "Initializing EVM monitor (using evm_block_and_receipts)...")
		safego.Go("evm", func() { monitors.StartEVMMonitor(monitorCtx, cfg, evmErrCh) })

		logger.InfoComponent("evm", "Initializing EVM Account monitor...")
		safego.Go("evm", func() { monitors.StartEVMAccountMonitor(monitorCtx, cfg, evmAccountErrCh) })
	}

	logger.InfoComponent("consensus", "Initializing Validator Status monitor...")
	monitors.StartValidatorStatusMonitor(monitorCtx, cfg, validatorStatusErrCh)

	if cfg.EnableValidatorRTT {
		logger.InfoComponent("consensus", "Initializing validator IP monitor...")
		safego.Go("consensus", func() { monitors.StartValidatorIPMonitor(monitorCtx, cfg, validatorIPErrCh) })
	} else {
		logger.InfoComponent("consensus", "Validator IP monitor disabled")
	}

	logger.InfoComponent("consensus", "Initializing comprehensive consensus monitor...")
	safego.Go("consensus", func() { monitors.StartConsensusMonitor(monitorCtx, &cfg, consensusErrCh) })

	// start validator latency monitor (only runs on validator nodes)
	logger.InfoComponent("latency", "Initializing validator latency monitor...")
	safego.Go("latency", func() { monitors.StartValidatorLatencyMonitor(monitorCtx, &cfg, latencyErrCh) })

	// start peer latency monitor if enabled (before gossip monitors so it can receive registrations)
	var registerPeer func(string, peermon.PeerDirection)
	var peerMon *peermon.Monitor
	if cfg.EnablePeerLatency {
		logger.InfoComponent("peer-latency", "Initializing peer latency monitor...")
		peerDataDir := filepath.Join(filepath.Dir(cfg.NodeHome), persistentFilesDir)
		peerMon = peermon.New(peerDataDir)
		peerMon.Load() // synchronously, before any producer registers
		registerPeer = peerMon.Register
		safego.Go("peer-latency", func() { peerMon.Start(monitorCtx, peerLatencyErrCh) })
	} else {
		logger.InfoComponent("peer-latency", "Peer latency monitor disabled")
	}

	// start gossip monitors (only run if respective log dirs exist)
	logger.InfoComponent("gossip", "Initializing gossip monitor...")
	safego.Go("gossip", func() { monitors.StartGossipMonitor(monitorCtx, &cfg, gossipErrCh, registerPeer) })

	logger.InfoComponent("gossip", "Initializing gossip connections monitor...")
	safego.Go("gossip", func() { monitors.StartGossipConnectionsMonitor(monitorCtx, &cfg, gossipConnErrCh, registerPeer) })

	if peerMon != nil {
		outbound := monitors.NewOutboundPeersMonitor(peerMon.Register, peerMon.SuggestPort)
		parent := monitors.NewParentPeerMonitor(peerMon.SetParentPeer)
		safego.Go("gossip", func() { monitors.StartTCPTrafficMonitor(monitorCtx, &cfg, outbound, parent) })
	}

	if cfg.EnableReplicaMetrics {
		logger.InfoComponent("replica", "Initializing replica commands monitor (streaming)...")
		replicaMonitor := monitors.NewReplicaMonitor(cfg.ReplicaDataDir, cfg.ReplicaBufferSize)
		safego.Go("replica", func() {
			if err := replicaMonitor.Start(monitorCtx); err != nil {
				replicaErrCh <- err
			}
		})
	}

	// node-host monitors: local files and procfs only, each behind its own flag
	if cfg.EnableProcess {
		safego.Go("process", func() { monitors.StartProcessMonitor(monitorCtx) })
	}
	if cfg.EnableChildStderr {
		safego.Go("child-stderr", func() { monitors.StartChildStderrMonitor(monitorCtx, &cfg) })
	}
	if cfg.EnableVisor {
		safego.Go("visor", func() { monitors.StartVisorMonitor(monitorCtx, &cfg) })
	}
	if cfg.EnableNodeState {
		safego.Go("node-state", func() { monitors.StartNodeStateMonitor(monitorCtx, &cfg) })
	}
	if cfg.EnableDisk {
		safego.Go("disk", func() { monitors.StartDiskMonitor(monitorCtx, &cfg) })
	}
	if cfg.EnableOperatorConfig && metrics.IsValidator() {
		safego.Go("consensus", func() { monitors.StartOperatorConfigMonitor(monitorCtx, &cfg) })
	}

	logger.InfoComponent("system", "Exporter is now running")

	// start memory monitoring
	safego.Go("system", func() { metrics.StartMemoryMonitoring(monitorCtx) })

	// start metrics cleanup to prevent unbounded map growth
	metrics.StartMetricsCleanup()
	defer metrics.StopMetricsCleanup()

	for {
		select {
		case err := <-blockErrCh:
			logger.ErrorComponent("core", "Block monitor error: %v", err)
		case err := <-proposalErrCh:
			logger.ErrorComponent("consensus", "Proposal monitor error: %v", err)
		case err := <-validatorErrCh:
			logger.ErrorComponent("consensus", "Validator monitor error: %v", err)
		case err := <-versionErrCh:
			logger.ErrorComponent("system", "Version monitor error: %v", err)
		case err := <-updateErrCh:
			logger.ErrorComponent("system", "Update checker error: %v", err)
		case err := <-evmErrCh:
			logger.ErrorComponent("evm", "EVM Monitor error: %v", err)
		case err := <-evmTxsErrCh:
			// This channel is no longer used but kept for compatibility
			logger.ErrorComponent("evm", "Legacy EVM Transactions Monitor error (should not happen): %v", err)
		case err := <-evmAccountErrCh:
			logger.ErrorComponent("evm", "EVM Account Monitor error: %v", err)
		case err := <-validatorStatusErrCh:
			logger.ErrorComponent("consensus", "Validator Status Monitor error: %v", err)
		case err := <-validatorIPErrCh:
			logger.ErrorComponent("consensus", "Validator IP Monitor error: %v", err)
		case err := <-coreTxErrCh:
			logger.ErrorComponent("core", "Core TX Monitor error: %v", err)
		case err := <-consensusErrCh:
			logger.ErrorComponent("consensus", "Consensus monitor error: %v", err)
		case err := <-replicaErrCh:
			logger.ErrorComponent("replica", "Replica monitor error: %v", err)
		case err := <-latencyErrCh:
			logger.ErrorComponent("latency", "Validator latency monitor error: %v", err)
		case err := <-gossipErrCh:
			logger.ErrorComponent("gossip", "Gossip monitor error: %v", err)
		case err := <-gossipConnErrCh:
			logger.ErrorComponent("gossip", "Gossip connections monitor error: %v", err)
		case err := <-peerLatencyErrCh:
			logger.ErrorComponent("peer-latency", "Peer latency monitor error: %v", err)
		case <-ctx.Done():
			logger.InfoComponent("system", "Shutting down monitors...")
			metrics.Shutdown()
			return
		}
		// small sleep to prevent tight loop in case of repeated errors
		time.Sleep(100 * time.Millisecond)
	}
}
