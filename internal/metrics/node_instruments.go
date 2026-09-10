package metrics

import (
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
)

// Instruments for the node-host monitors ported from upstream v4.1.1
// (process, child_stderr, visor, node_state, disk, operator_config). Names
// follow upstream verbatim so its alert rules apply unchanged.
var (
	// process (label: process)
	HLNodeProcessUp              api.Float64ObservableGauge
	HLNodeProcessStartTime       api.Float64ObservableGauge
	HLNodeProcessCPUSeconds      api.Float64ObservableGauge
	HLNodeProcessRSSBytes        api.Float64ObservableGauge
	HLNodeProcessVirtBytes       api.Float64ObservableGauge
	HLNodeProcessThreads         api.Float64ObservableGauge
	HLNodeProcessOpenFDs         api.Float64ObservableGauge
	HLNodeProcessMaxFDs          api.Float64ObservableGauge
	HLNodeProcessOpenFDsRatio    api.Float64ObservableGauge
	HLNodeProcessEligibleMatches api.Float64ObservableGauge
	HLNodeProcessIOTotal         api.Int64Counter

	// child_stderr
	HLNodeChildStarts               api.Float64ObservableGauge
	HLNodeChildCrashes              api.Float64ObservableGauge
	HLNodeChildLastCrashSeconds     api.Float64ObservableGauge
	HLNodeChildStderrArtifacts      api.Float64ObservableGauge
	HLNodeChildStderrLastArtifactTS api.Float64ObservableGauge

	// visor
	HLVisorHeight                     api.Float64ObservableGauge
	HLVisorInitialHeight              api.Float64ObservableGauge
	HLVisorBlocksApplied              api.Float64ObservableGauge
	HLVisorHardforkVersion            api.Float64ObservableGauge
	HLVisorScheduledFreezeHeight      api.Float64ObservableGauge
	HLVisorConsensusAheadOfWallSecond api.Float64ObservableGauge
	HLVisorReferenceLagSeconds        api.Float64ObservableGauge

	// node_state
	HLNodePersistedABCIHeight          api.Float64ObservableGauge
	HLNodePersistedABCIHeightGap       api.Float64ObservableGauge
	HLNodePersistedFreezeABCIHeight    api.Float64ObservableGauge
	HLNodeVisorHeightAbovePersistedFrz api.Float64ObservableGauge
	HLNodePersistedStateFileAvailable  api.Float64ObservableGauge

	// disk
	HLNodeDiskUsedBytes            api.Float64ObservableGauge
	HLNodeDiskFreeBytes            api.Float64ObservableGauge
	HLNodeDiskTotalBytes           api.Float64ObservableGauge
	HLNodeDiskAllocatedBytes       api.Float64ObservableGauge
	HLNodeDiskSubdirBytes          api.Float64ObservableGauge
	HLNodeDiskSubdirAllocatedBytes api.Float64ObservableGauge
	HLNodeDiskPathState            api.Float64ObservableGauge
	HLNodeDiskLastCompleteTS       api.Float64ObservableGauge

	// operator_config
	HLNodeOperatorConfigPresent    api.Float64ObservableGauge
	HLNodeOperatorConfigAgeSeconds api.Float64ObservableGauge
	HLNodeOperatorConfigFailedLoad api.Float64ObservableGauge
	HLNodeJailingThresholdSeconds  api.Float64ObservableGauge
	HLNodeJailingDryRun            api.Float64ObservableGauge

	// non-parse failures of a consumed source, by stream and stage
	HLExporterSourceErrorsCounter api.Int64Counter

	nodeObservables []api.Observable
)

func createNodeInstruments() error {
	var errs []error
	nodeObservables = nil
	gauge := func(name, desc string) api.Float64ObservableGauge {
		g, err := meter.Float64ObservableGauge(name, api.WithDescription(desc))
		errs = append(errs, err)
		nodeObservables = append(nodeObservables, g)
		return g
	}
	counter := func(name, desc string) api.Int64Counter {
		c, err := meter.Int64Counter(name, api.WithDescription(desc))
		errs = append(errs, err)
		return c
	}

	HLNodeProcessUp = gauge("hl_node_process_up", "1 if the named hl process was found in /proc on the latest scan, 0 otherwise")
	HLNodeProcessStartTime = gauge("hl_node_process_start_time_seconds", "Unix timestamp at which the process started (boot time plus /proc/PID/stat starttime)")
	HLNodeProcessCPUSeconds = gauge("hl_node_process_cpu_seconds_total", "Cumulative CPU time consumed by the process (user plus kernel), in seconds")
	HLNodeProcessRSSBytes = gauge("hl_node_process_rss_bytes", "Resident set size of the process in bytes")
	HLNodeProcessVirtBytes = gauge("hl_node_process_virt_bytes", "Virtual memory size of the process in bytes")
	HLNodeProcessThreads = gauge("hl_node_process_threads", "Number of OS threads in the process")
	HLNodeProcessOpenFDs = gauge("hl_node_process_open_fds", "Open file descriptors held by the process")
	HLNodeProcessMaxFDs = gauge("hl_node_process_max_fds", "Soft open-file limit of the process; 0 when unavailable or unlimited")
	HLNodeProcessOpenFDsRatio = gauge("hl_node_process_open_fds_ratio", "Open file descriptors divided by the finite soft limit; 0 when the limit is unavailable or unlimited")
	HLNodeProcessEligibleMatches = gauge("hl_node_process_eligible_matches", "Processes whose comm and executable or argv0 matched the process name in the latest complete /proc scan")
	HLNodeProcessIOTotal = counter("hl_node_process_io_total", "Exporter-lifetime positive /proc/PID/io deltas for the selected process; a new process epoch establishes a baseline")

	HLNodeChildStarts = gauge("hl_node_child_starts", "Artifacts retained under visor_child_stderr, one per hl-node child start; prune-aware, do not rate")
	HLNodeChildCrashes = gauge("hl_node_child_crashes", "Retained child-stderr artifacts with a classified reason; a reason is bounded evidence, not a proven cause")
	HLNodeChildLastCrashSeconds = gauge("hl_node_child_last_crash_seconds", "mtime of the newest retained child-stderr artifact per classified reason; 0 when none")
	HLNodeChildStderrArtifacts = gauge("hl_node_child_stderr_artifacts", "Retained child-stderr artifacts by read state and reason from the last complete directory scan")
	HLNodeChildStderrLastArtifactTS = gauge("hl_node_child_stderr_last_artifact_timestamp_seconds", "Newest mtime among retained child-stderr artifacts for a state and reason; absent when none")

	HLVisorHeight = gauge("hl_visor_height", "Latest height observed by hl-visor (block height the node has applied)")
	HLVisorInitialHeight = gauge("hl_visor_initial_height", "initial_height from the latest valid visor state, scoped to that process generation")
	HLVisorBlocksApplied = gauge("hl_visor_blocks_applied", "height minus initial_height from the latest valid visor state; 0 when either is unavailable")
	HLVisorHardforkVersion = gauge("hl_visor_hardfork_version", "hardfork_version reported by the latest valid visor state (source=visor_state); absent when the field is missing")
	HLVisorScheduledFreezeHeight = gauge("hl_visor_scheduled_freeze_height", "scheduled_freeze_height from the latest valid visor state; absent while it is null")
	HLVisorConsensusAheadOfWallSecond = gauge("hl_visor_consensus_ahead_of_wall_seconds", "consensus_time minus wall_clock_time for the latest visor sample, in seconds")
	HLVisorReferenceLagSeconds = gauge("hl_visor_reference_lag_seconds", "Reference-node lag reported by the visor, in seconds; absent when not reported")

	HLNodePersistedABCIHeight = gauge("hl_node_persisted_abci_height", "Core/ABCI height read from a persisted checkpoint-height file under hyperliquid_data; not EVM block height")
	HLNodePersistedABCIHeightGap = gauge("hl_node_persisted_abci_height_gap", "Fast minus slow persisted checkpoint height when both files are readable in the same poll")
	HLNodePersistedFreezeABCIHeight = gauge("hl_node_persisted_freeze_abci_height", "Core/ABCI height read from the persisted freeze_abci_height file; persistence does not make it a current scheduled freeze")
	HLNodeVisorHeightAbovePersistedFrz = gauge("hl_node_visor_height_above_persisted_freeze", "Nonnegative latest visor height minus the persisted freeze_abci_height when both are available")
	HLNodePersistedStateFileAvailable = gauge("hl_node_persisted_state_file_available", "Whether a persisted node-state file was present, readable and held one nonnegative integer in the latest poll")

	HLNodeDiskUsedBytes = gauge("hl_node_disk_used_bytes", "Sum of regular file sizes under NODE_HOME from the last complete walk")
	HLNodeDiskFreeBytes = gauge("hl_node_disk_free_bytes", "Bytes available to unprivileged users on the filesystem holding NODE_HOME (statfs bavail times bsize)")
	HLNodeDiskTotalBytes = gauge("hl_node_disk_total_bytes", "Total bytes on the filesystem holding NODE_HOME (statfs blocks times bsize)")
	HLNodeDiskAllocatedBytes = gauge("hl_node_disk_allocated_bytes", "Unique filesystem blocks allocated to entries under NODE_HOME in the last complete walk; hardlinks counted once")
	HLNodeDiskSubdirBytes = gauge("hl_node_disk_subdir_bytes", "Sum of regular file sizes under a tracked NODE_HOME subdirectory")
	HLNodeDiskSubdirAllocatedBytes = gauge("hl_node_disk_subdir_allocated_bytes", "Unique allocated blocks under a tracked NODE_HOME path, deduplicated within that path")
	HLNodeDiskPathState = gauge("hl_node_disk_path_state", "One-hot presence state of a tracked NODE_HOME path from the last complete walk")
	HLNodeDiskLastCompleteTS = gauge("hl_node_disk_last_complete_timestamp_seconds", "Unix timestamp of the last complete NODE_HOME walk")

	HLNodeOperatorConfigPresent = gauge("hl_node_operator_config_present", "Presence of each fixed operator-config file under file_mod_time_tracker (1 present, 0 absent, -1 stat failed)")
	HLNodeOperatorConfigAgeSeconds = gauge("hl_node_operator_config_age_seconds", "Seconds since an operator-config file was last modified; absent when the file is absent")
	HLNodeOperatorConfigFailedLoad = gauge("hl_node_operator_config_failed_load", "Count of <file>_FAILED_LOAD sidecars hl-node left after rejecting an operator-pushed config; unknown names collapse to file=unknown")
	HLNodeJailingThresholdSeconds = gauge("hl_node_jailing_threshold_seconds", "latency_ema_jail_threshold from heartbeat_jailing_config.json: the heartbeat-ack EMA above which this validator votes to jail a peer; absent without the file")
	HLNodeJailingDryRun = gauge("hl_node_jailing_dry_run", "1 if heartbeat_jailing_config.json has dry_run=true (jail votes are logged, not cast); absent without the file")

	HLExporterSourceErrorsCounter = counter("hl_exporter_source_errors_total", "Failures reading or interpreting a consumed source, by stream and stage (stat, read, walk, statfs, decode, schema)")

	return errors.Join(errs...)
}

// Generic setters for the node-host gauges. Unlabeled gauges live in
// currentValues; labeled series are keyed by the joined label values.

func SetGauge(g api.Float64ObservableGauge, v float64) {
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	currentValues[g] = v
}

// ClearGauge withdraws an unlabeled gauge so it is absent, not zero.
func ClearGauge(g api.Float64ObservableGauge) {
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	delete(currentValues, g)
}

func SetGaugeSeries(g api.Float64ObservableGauge, v float64, labels ...attribute.KeyValue) {
	key := seriesKey(labels)
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	if _, exists := labeledValues[g]; !exists {
		labeledValues[g] = make(map[string]labeledValue)
	}
	labeledValues[g][key] = labeledValue{updatedAt: time.Now(), value: v, labels: labels}
}

func ClearGaugeSeries(g api.Float64ObservableGauge, labels ...attribute.KeyValue) {
	key := seriesKey(labels)
	metricsMutex.Lock()
	defer metricsMutex.Unlock()
	delete(labeledValues[g], key)
}

func AddCounter(c api.Int64Counter, n int64, labels ...attribute.KeyValue) {
	c.Add(sharedCtx, n, api.WithAttributes(labels...))
}

func IncrementSourceErrors(stream, stage string) {
	HLExporterSourceErrorsCounter.Add(sharedCtx, 1,
		api.WithAttributes(attribute.String("stream", stream), attribute.String("stage", stage)))
}

func seriesKey(labels []attribute.KeyValue) string {
	key := ""
	for i, l := range labels {
		if i > 0 {
			key += "\x00"
		}
		key += l.Value.String()
	}
	return key
}

// Read-side accessors, mainly for tests

func GaugeValue(g api.Float64ObservableGauge) (float64, bool) {
	metricsMutex.RLock()
	defer metricsMutex.RUnlock()
	v, ok := currentValues[g].(float64)
	return v, ok
}

func GaugeSeriesValue(g api.Float64ObservableGauge, labels ...attribute.KeyValue) (float64, bool) {
	key := seriesKey(labels)
	metricsMutex.RLock()
	defer metricsMutex.RUnlock()
	v, ok := labeledValues[g][key]
	return v.value, ok
}
