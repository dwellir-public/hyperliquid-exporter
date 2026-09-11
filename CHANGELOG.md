# Changelog

All notable changes to the Hyperliquid Exporter will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Six node-host monitors ported from upstream v4.1.1 under upstream metric names, each on by default and disabled with `--<flag>=false`:
  - `--process-metrics`: `hl_node_process_*` liveness, CPU, memory, threads, file descriptors and IO deltas for `hl-node` and `hl-visor` from `/proc`
  - `--child-stderr-metrics`: `hl_node_child_starts`, `hl_node_child_crashes{reason}`, `hl_node_child_last_crash_seconds{reason}` and `hl_node_child_stderr_artifacts{state,reason}` from `data/visor_child_stderr`, with the `app_hash_mismatch`, `hardfork_upgrade`, `sync_overflow`, `config_error`, `network`, `panic` taxonomy
  - `--visor-metrics`: `hl_visor_height`, `hl_visor_initial_height`, `hl_visor_blocks_applied`, `hl_visor_hardfork_version`, `hl_visor_scheduled_freeze_height`, `hl_visor_consensus_ahead_of_wall_seconds`, `hl_visor_reference_lag_seconds` from `visor_abci_state.json`
  - `--node-state-metrics`: `hl_node_persisted_abci_height{source_class}`, `hl_node_persisted_abci_height_gap`, `hl_node_persisted_freeze_abci_height`, `hl_node_visor_height_above_persisted_freeze` and `hl_node_persisted_state_file_available{file}` from the single-integer files under `hyperliquid_data`
  - `--disk-metrics`: `hl_node_disk_{used,allocated,free,total}_bytes`, `hl_node_disk_subdir_bytes{subdir}`, `hl_node_disk_subdir_allocated_bytes{path}`, `hl_node_disk_path_state{path,state}` and `hl_node_disk_last_complete_timestamp_seconds` from a 120 s walk of `NODE_HOME`
  - `--operator-config-metrics` (validator nodes only): `hl_node_operator_config_{present,age_seconds,failed_load}{file}` from `file_mod_time_tracker/`, and `hl_node_jailing_threshold_seconds` and `hl_node_jailing_dry_run` from `heartbeat_jailing_config.json`
- `hl_exporter_source_errors_total{stream,stage}` for non-parse source failures (stat, read, walk, statfs, decode, schema). The source-health envelope (`hl_exporter_source_up`, `hl_exporter_source_sample_age_seconds`) now also covers the `process`, `child_stderr`, `visor`, `node_state`, `disk` and `operator_config` streams
- `hl_p2p_gossip_events_total{event_type}` and `hl_p2p_gossip_unknown_events_total`: gossip_connections events are matched against the 15-tag allowlist from upstream v4.1.1 with per-tag payload validation (both the pre- and post-2026-08 shapes of `closing gossip stream because no quorum yet`, `dropping connection after sending abci state`, `sending evm kvs` and `marking node_ip as verified` are accepted). Unknown tags land in `other` and bump the unknown counter, so hl-node adding an event type is visible
- `hl_exporter_source_up{stream}`, `hl_exporter_source_sample_age_seconds{stream}` and `hl_exporter_parse_errors_total{stream,stage}` for the `gossip_rpc`, `gossip_connections` and `tcp_traffic` streams. "No peers" and "source unreadable" were previously indistinguishable. The generalized parse-error counter stands in for upstream's per-stream `hl_p2p_gossip_parse_errors_total`; use `stream="gossip_connections"`
- `hl_node_parent_peer_share_ratio` and `hl_node_parent_peer_challenger_ratio` expose the evidence behind the current parent choice
- `hl_exporter_monitor_panics_total{monitor}`: every monitor goroutine now runs with panic recovery. A panic is logged with its stack and counted instead of killing the process
- `hl_timeout_rounds_total` is now actually populated: the consensus log's `["round advance", ...]` events are parsed in the consensus monitor. The previous standalone round-advance monitor read the wrong stream and was never started. Both the legacy string reason and the current tagged-object reason (`{"Tc": {...}}`) are accepted; `suspect` carries hl-node's enum (e.g. `NoVote`)
- `internal/actiontypes`: bounded action-type vocabulary ported from upstream v4.0.7, adding `outcomeDeploy` and `trailingStop`. Unknown action types are reported as `other` instead of becoming raw labels
- EVM `block_type` gained a `small` bucket for 3M gas-limit blocks; unexpected gas limits no longer log a warning per block

### Changed

- Block, consensus, status, replica, EVM and proposal log tailers share one implementation that keeps a torn trailing line until its newline arrives and drains the previous hour file before switching. Previously each tailer discarded the partial line it had already consumed at EOF, losing one record per file boundary, and dropped any line written to the old file after the rollover check
- Latest-file resolution no longer walks whole log trees. `utils.LatestFile` descends into the greatest-named entry at each level (`os.ReadDir`), and tailers in EOF loops re-resolve at most every 2 s. Upstream measured about 195% CPU from the previous `filepath.Walk` in 10 ms loops
- Metrics cleanup no longer forces a garbage collection every 30 s, and Go memory gauges are served from a snapshot refreshed every 30 s instead of calling `runtime.ReadMemStats` on every scrape
- Gossip, gossip connections, outbound peer and parent peer tailers drain the previous hour file once more before switching to the new one, so lines written just before rollover are not lost
- Gossip and gossip connections monitors seed from the current hour on restart with counters suppressed, matching the tcp_traffic readers. `hl_p2p_incoming_requests_total`, `hl_p2p_stream_connections_total` and `hl_p2p_verifications_total` no longer replay up to an hour of increments on every restart
- Child peer state is reconciled per `child_peers status` snapshot instead of accumulating across all snapshots in a poll
- Parent peer selection is stable: per-IP inbound volume is aggregated across ports and smoothed with an EWMA (alpha 0.3), a challenger needs 1.2x the incumbent's smoothed value to take over, exact ties break on the lowest IP, and the parent is cleared after 90 s without inbound traffic. Previously the largest single-sample flow won each poll and ties depended on map iteration order. The ambiguity warning log is replaced by the challenger ratio gauge. `hl_node_parent_peer_traffic_volume_total` now accrues to the selected parent rather than the per-line top flow
- `tcp_traffic` is parsed once per poll and shared by peer discovery and parent selection. The parser is strict: any malformed row rejects the whole record (arity, IP via `netip` with IPv4-mapped addresses unmapped, port, NaN/Inf/negative values, unknown direction), counted in `hl_exporter_parse_errors_total{stream="tcp_traffic"}`
- Peers discovered from `tcp_traffic` are admitted to the latency set only after two consecutive samples with positive traffic while in the top 16 endpoints of their direction. Previously every endpoint in every sample was registered, including zero-volume ones
- The peer prober tries the port observed in `tcp_traffic` before the fallback list when a peer has no probe-proven port yet
- Per-`peer_ip` gossip series (`hl_p2p_incoming_requests_total`, `hl_p2p_incoming_peer_last_seen`, `hl_p2p_child_peer_connected`, `hl_p2p_child_peer_connections`, `hl_p2p_stream_connections_total`, `hl_p2p_verifications_total`) are published only with `--peer-latency`. Without the flag they created unbounded label cardinality with no consumer; aggregate gauges are unaffected
- Public IP lookup reads `$NODE_HOME/last_known_public_ip.json` first and falls back to ipify with a 5 s timeout. Failure logs a warning instead of aborting startup
- `hl_consensus_heartbeat_ack_ambiguous_total`: heartbeat acknowledgements are joined to a unique outgoing heartbeat by random ID and round instead of random ID alone. hl-node reuses random IDs across rounds, so acks previously attached to whichever heartbeat was registered last. Acks that fit more than one heartbeat are dropped and counted; a responder's repeat ack of the same heartbeat and acks that predate the heartbeat are dropped silently. The `random_id` field is decoded as an unsigned 64-bit integer; as a float it lost precision above 2^53
- The source-health envelope (`hl_exporter_source_up`, `hl_exporter_source_sample_age_seconds`, `hl_exporter_parse_errors_total`, `hl_exporter_source_errors_total`) now covers every hl-node log stream: `node_fast_block_times`, `node_slow_block_times`, `block_times`, `consensus`, `status`, `validator_status`, `replica_cmds` and `evm_block_and_receipts`. A rising parse-error counter on a healthy node means hl-node changed a log shape
- `TestSchemaFixtures` runs every stream parser over committed samples under `internal/monitors/testdata/schema/` and over gitignored live pulls under `testdata/live/`, failing on any rejected line, unknown gossip tag or unknown action type. `docs/operations/hl-node-schema-watch.md` describes the routine; `docs/operations/upstream-sync.md` describes the upstream review routine
- CI and release workflows run govulncheck, use a read-only token outside the release job, refuse to release from any ref other than `main`, queue concurrent releases, smoke-test the built binary's `--version`, and read the Go version from `go.mod`
- A metrics port that cannot be bound now fails startup with a non-zero exit instead of leaving a metric-less process running
- Validator latency files are keyed by UTC date, matching hl-node, instead of local time
- Operation `category` labels follow the upstream mapping: `spotDeploy` moved from `transfer` to `deployment`, `perpDeploy` from `other` to `deployment`
- `hl_consensus_vote_time_diff_seconds` now reports the age of the last observed vote at scrape time (now minus the vote's log timestamp) and drops validators silent for 24 h. Previously it recorded the exporter's own parse lag per vote and sat near zero forever
- Per-validator series (stake, jailed, active, RTT, QC participation, vote round, vote age, heartbeat status, latency, latency round, latency EMA) are removed when a validator leaves the set returned by the validator API. Latency series are also removed when a validator's latency directory disappears. Previously they froze at their last value
- The 30 s labeled-series sweep that capped every labeled family at 200 entries by recency is removed. Above 200 validators it dropped stake, jailed and latency series every 30 s. Peer and connectivity series already remove themselves, and validator series are now reconciled explicitly
- Consensus monitor signer tracking is bounded: signers unseen in a QC for 1 h are pruned every 10 min, and the signer-to-validator memo is an LRU with 1 h TTL. The unused TC vote counter map is removed

### Fixed

- `hl_core_operations_total` counted every comma inside an order, cancel or modify object as a separate operation, inflating counts 2 to 6x. Elements are now counted with a JSON decoder; one order is one operation. Expect the counter's rate to drop accordingly after upgrading
- `hl_consensus_proposer_count_total` now carries the `name` label. The moniker lookup was stubbed to return an empty string
- Status log `current_stakes` wrapped as `{"validator_to_stake": [...]}` (hl-node builds since mid-2026) is now decoded; previously the validator set and signer mappings silently came up empty on current nodes
- Status log lines over 64 KiB no longer fail to read; the last-line scanner buffer is raised to 8 MiB
- Consensus wrapper identity under the new `sender` key (hl-node builds since 2026-09) is accepted alongside the old `source` key, restoring heartbeat ack attribution
- Validator latency reader resets its offset when the file is truncated or replaced in place, not only when the date changes
- The persisted peer set is loaded before any log producer can register a peer, and loading skips invalid IPs and entries unseen for over 48 h instead of overwriting a fresher in-memory entry

### Removed

- `--contract-metrics`, `--contract-metrics-limit`, the `hl_evm_contract_tx_total` metric, and the `internal/contracts/` resolver. Contract names came from Hyperscan's Blockscout API, which no longer exists (the domain redirects to hl.eco and Blockscout retired free per-instance APIs on 2026-07-01), so the feature failed at startup for every user. `hl_evm_contract_create_total` is unaffected. Service args still passing the removed flags fail to parse, drop them before upgrading

## [2.4.0] - 2026-09-10

### Added

- `hl_core_block_propagation_latency_milliseconds{state_type,peer_ip}`: histogram of `begin_block_wall_time` minus `block_time` per block from the fast/slow block time files, i.e. how long a block took to reach this node. `peer_ip` is the parent peer delivering blocks at the time (`unknown` without `--peer-latency`), so percentiles can be compared per upstream

### Changed

- Upgraded dependencies:
  - Go 1.26.7
  - golangci-lint 2.13.2
  - General library update

## [2.3.0] - 2026-07-10

### Added

- `hl_node_parent_peer_block_lag_seconds{peer_ip}` (requires `--peer-latency`): EMA of block apply lag (wall clock minus chain block timestamp) attributed to the current parent peer. Catches steady-but-behind drift, which produces normal inter-block gaps and is invisible to the rate-band check
- Apply lag above 30s now also counts as degraded time in `hl_node_parent_peer_degraded_seconds_total`

### Changed

- Peers with 5+ consecutive probe failures are probed every 15 minutes instead of every minute
- Peers unseen in node logs for 48h that are also unreachable are expired from the monitored set, ex-parents included (they remain exempt from LRU eviction only)
- `peers.json` entries gained `consec_fails` and `last_probe` fields for probe bookkeeping

## [2.2.0] - 2026-07-08

### Added

- Per-peer traffic counters (require `--peer-latency`): `hl_peer_traffic_volume_total{peer_ip,direction}` and `hl_peer_active_seconds_total{peer_ip}` accumulate each peer's `tcp_traffic` volume (raw units, unconfirmed) and active time
- Parent tenure quality counters (require `--peer-latency`): `hl_node_parent_peer_tenure_seconds_total`, `hl_node_parent_peer_degraded_seconds_total`, `hl_node_parent_peer_blocks_total`, and `hl_node_parent_peer_traffic_volume_total`, all labeled `peer_ip`, attribute parent service time, degraded block-rate time, applied blocks, and delivered volume to whichever peer was parent
- Degraded block-rate detection: fast/slow EMA rate-band (short-window block rate below 80% of the long-run baseline) plus outright-stall detection, with a warmup period to avoid false verdicts after restarts
- `was_parent` flag persisted in `peers.json`: peers that have served as parent are exempt from LRU eviction so their quality history survives peer churn

### Changed

- Upgraded dependencies:
  - Go 1.26.4
  - golangci-lint 2.12.2
  - General library update
- Peer set capacity increased from 128 to 256
- Traffic counter accounting skips the startup replay of the current hourly `tcp_traffic` file, so `increase()` windows spanning an exporter restart no longer double-count pre-restart traffic

### Removed

- `Dockerfile` and `docker-compose.yml`: deployment is handled by the [hyperliquid-metrics-exporter Juju charm](https://github.com/dwellir-public/ops/tree/main/juju/charms/hyperliquid-metrics-exporter), which runs the binary as a systemd service

### Fixed

- Peer map race crash, random labeled-metric eviction, empty API retry bodies, fd leaks on log rotation, validator latency staleness after midnight rollover

## [2.1.4] - 2026-03-31

### Added

- Parent peer identification: new monitor that analyzes `tcp_traffic` byte volumes to identify the node's primary upstream peer (the one delivering all block data)
- New metrics (all require `--peer-latency`):
  - `hl_node_parent_peer` — info gauge identifying the current parent peer IP
  - `hl_node_parent_peer_traffic` — inbound traffic volume from the parent peer per interval (unit unknown, raw value from node logs)
  - `hl_node_parent_peer_tenure_seconds` — how long the current parent has held the role
  - `hl_node_parent_peer_switches_total` — counter for parent peer changes
  - `hl_node_parent_peer_latency_ms` — dedicated TCP probe latency for the parent peer
- Warning log when parent peer selection is ambiguous (runner-up has >10% of the top peer's bytes)

## [2.1.3] - 2026-03-31

### Added

- Direction labeling for peer latency metrics: `hl_peer_latency_ms` and `hl_peer_reachable` now include a `direction` label (`inbound`, `outbound`, or `unknown`)
  - A peer seen in both directions gets two Prometheus series with the same latency value (one TCP probe per IP)
  - Direction is inferred from the discovery source: child peers and outgoing TCP traffic are outbound, incoming requests and inbound TCP traffic are inbound

### Changed

- Peer set capacity increased from 100 to 128
- `peermon.Register` now accepts a `PeerDirection` parameter; `Peer` struct tracks a set of observed directions

## [2.1.2] - 2026-03-31

### Added

- Outbound peer discovery from `tcp_traffic` logs: non-validator nodes with no inbound connections now automatically discover peers for latency monitoring
- New outbound peers monitor extracts peer IPs from the node's TCP traffic data, logging each discovered peer on first sight

### Changed

- `peermon.Register` is now a direct mutex-protected call instead of a buffered channel send, removing the 256-entry buffer limit

## [2.1.1] - 2026-03-30

### Changed

- Peer latency prober now tries ports 3001, 3002, 443, 80 before the 4000-4010 range, improving reachability in environments with restrictive firewall rules

## [2.1.0] - 2026-03-30

Initial release post-fork.

### Added

- Peer latency monitoring (`--peer-latency`): TCP-based latency probes against all known peers once per minute
- New metrics: `hl_peer_latency_ms`, `hl_peer_reachable`, `hl_peer_probes_total`, `hl_peer_probe_failures_total`, `hl_peer_monitored_count`
- Persistent peer set (`.hyperliquid-exporter/peers.json`) survives restarts with LRU eviction at 100 peers
- New `internal/peermon` package for peer latency monitoring
- Per-peer P2P metrics: `hl_p2p_incoming_requests_total`, `hl_p2p_incoming_peer_last_seen`, `hl_p2p_incoming_peers_active`, `hl_p2p_child_peer_connected`, `hl_p2p_child_peer_connections`
- Gossip connection metrics: `hl_p2p_stream_connections_total`, `hl_p2p_verifications_total`
- New gossip connections monitor for tracking stream connections and verifications
- CI test workflow
- Test coverage across cache, config, metrics, replica, utils, abci, logger, hyperliquid-api, contracts, and monitors

### Changed

- Binary renamed from `hl_exporter` to `hyperliquid-exporter` (cmd directory, Dockerfile, CI, docs)
- Improved gossip monitor line tailer and disconnect state preservation
- Upgraded to Go 1.26.1 with dependency updates
- Improved Makefile build system
- Updated CI release workflow
- Refined CLI help messages
- Lint fixes throughout codebase

## [2.0.0] - 2025-08-03

### Added

#### Consensus Monitoring

- Realtime consensus monitoring with 20+ new consensus metrics
- Validator connectivity tracking with heartbeats
- QC participation
- TC tracking
- Validator latency measurements

#### HyperCore Tx and order metrics

- Moved to direct msgpack parsing (previously used binary)
- Monitor tps, orders per second
- See breakdown of order types

#### EVM

- Comprehensive gas metrics (base fee, priority fee, utilization)
- Per-contract transaction tracking with configurable limits
- High gas block detection and tracking
- EVM account growth monitoring

#### System Monitoring

- Go runtime memory metrics (heap, goroutines, system memory)
- P2P network peer connection tracking
- LRU caching system for improved performance
- Processing latency and throughput metrics

#### New CLI Flags

- `--replica-metrics` - Enable replica command transaction metrics
- `--contract-metrics` - Enable per-contract transaction metrics
- `--contract-metrics-limit` - Maximum contract labels to retain (default: 20)
- `--validator-rtt` - Enable validator RTT monitoring

### Changed

#### Metrics Organization (BREAKING CHANGES)

- All metrics reorganized with categorical prefixes:
  - `hl_core_*` - Core blockchain metrics
  - `hl_consensus_*` - Consensus-related metrics
  - `hl_metal_*` - Implementation-specific metrics
  - `hl_evm_*` - EVM chain metrics
- Total metrics increased from 20 to 82 (310% increase)

#### Complete List of Renamed Metrics

- `hl_block_height` → `hl_core_block_height`
- `hl_block_time_milliseconds` → `hl_core_block_time_milliseconds`
- `hl_latest_block_time` → `hl_core_latest_block_time`
- `hl_apply_duration` → `hl_metal_apply_duration`
- `hl_apply_duration_milliseconds` → `hl_metal_apply_duration_milliseconds`
- `hl_proposer_count_total` → `hl_consensus_proposer_count_total`
- `hl_validator_count` → `hl_consensus_validator_count`
- `hl_validator_jailed_status` → `hl_consensus_validator_jailed_status`
- `hl_validator_stake` → `hl_consensus_validator_stake`
- `hl_validator_active_status` → `hl_consensus_validator_active_status`
- `hl_validator_rtt` → `hl_consensus_validator_rtt`
- `hl_total_stake` → `hl_consensus_total_stake`
- `hl_jailed_stake` → `hl_consensus_jailed_stake`
- `hl_not_jailed_stake` → `hl_consensus_not_jailed_stake`
- `hl_active_stake` → `hl_consensus_active_stake`
- `hl_inactive_stake` → `hl_consensus_inactive_stake`

..with addition of many more brand new metrics

#### CLI Flags

- `--enable-otlp` renamed to `--otlp`
- `--evm` renamed to `--evm-metrics`
- `--otlp-endpoint` default value removed (now required when OTLP enabled)

### Removed

- `--enable-prom` flag (Prometheus now always enabled)
- `--disable-prom` flag (Prometheus now always enabled)
- `hl_evm_transactions_total` metric (replaced by `hl_evm_tx_type_total`)

[Unreleased]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.4.0...HEAD
[2.4.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.3.0...v2.4.0
[2.3.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.4...v2.2.0
[2.1.4]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.3...v2.1.4
[2.1.3]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.2...v2.1.3
[2.1.2]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.1...v2.1.2
[2.1.1]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v1.2.5...v2.0.0
