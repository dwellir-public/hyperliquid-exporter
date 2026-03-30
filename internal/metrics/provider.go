package metrics

import (
	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
)

var (
	meter api.Meter
)

func getAllObservables() []api.Observable {
	return []api.Observable{
		// Core L1 metrics
		HLCoreBlockHeightGauge,
		HLCoreLatestBlockTimeGauge,
		HLCoreLastProcessedRound,
		HLCoreLastProcessedTime,

		// Metal (machine specific) metrics
		HLMetalApplyDurationGauge,
		HLMetalParseDurationGauge,
		HLMetalLastProcessedRound,
		HLMetalLastProcessedTime,

		// Consensus metrics
		HLConsensusValidatorJailedStatus,
		HLConsensusValidatorStakeGauge,
		HLConsensusTotalStakeGauge,
		HLConsensusJailedStakeGauge,
		HLConsensusNotJailedStakeGauge,
		HLConsensusValidatorCountGauge,
		HLConsensusActiveStakeGauge,
		HLConsensusInactiveStakeGauge,
		HLConsensusValidatorActiveStatus,
		HLConsensusValidatorRTTGauge,

		// consensus monitoring metrics
		HLConsensusVoteRoundGauge,
		HLConsensusVoteTimeDiffGauge,
		HLConsensusCurrentRoundGauge,
		HLConsensusConnectivityGauge,
		HLConsensusHeartbeatStatusGauge,
		HLConsensusQCParticipationGauge,
		HLConsensusRoundsPerBlockGauge,
		HLConsensusQCRoundLagGauge,

		// val latency metrics
		HLConsensusValidatorLatencyGauge,
		HLConsensusValidatorLatencyRoundGauge,
		HLConsensusValidatorLatencyEMAGauge,

		// P2P metrics (non validator peers)
		HLP2PNonValPeerConnectionsGauge,
		HLP2PNonValPeersTotalGauge,

		// P2P per-peer metrics
		HLP2PIncomingPeerLastSeenGauge,
		HLP2PChildPeerConnectedGauge,
		HLP2PChildPeerConnectionsGauge,

		// hl-node client metrics
		HLSoftwareVersionInfo,
		HLSoftwareUpToDate,

		// EVM metrics
		HLEVMBlockHeightGauge,
		HLEVMLatestBlockTimeGauge,
		HLEVMBaseFeeGauge,
		HLEVMGasUsedGauge,
		HLEVMGasLimitGauge,
		HLEVMSGasUtilGauge,
		HLEVMMaxPriorityFeeGauge,
		HLEVMAccountCountGauge,
		HLEVMLastHighGasBlockHeight,
		HLEVMLastHighGasBlockLimit,
		HLEVMLastHighGasBlockUsed,
		HLEVMLastHighGasBlockTime,
		HLEVMMaxGasLimitSeen,

		// memory metrics
		HLGoHeapObjects,
		HLGoHeapInuseMB,
		HLGoHeapIdleMB,
		HLGoSysMB,
		HLGoNumGoroutines,

		// monitor health metrics
		HLConsensusMonitorLastProcessedGauge,
	}
}

func getCommonLabels() []attribute.KeyValue {
	return []attribute.KeyValue{}
}
