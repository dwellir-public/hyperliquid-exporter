# Changelog

All notable changes to the Hyperliquid Exporter will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.5.0] - 2026-09-11

Ports upstream v3.0.0 to v4.1.1; see the [port report](docs/reports/upstream-port-v3-to-v4.md). Every metric below is documented in the [metrics reference](docs/metrics-overview.md).

**Upgrading:**

- Remove `--contract-metrics` and `--contract-metrics-limit` from service args; they now fail to parse
- Seven new node-host monitors are on by default (see Added); disable individually with `--<flag>=false`
- Per-`peer_ip` gossip series now require `--peer-latency`
- `hl_core_operations_total` rate drops 2 to 6x (it was overcounting), and `category` moves `spotDeploy` and `perpDeploy` to `deployment`
- `hl_consensus_vote_time_diff_seconds` now means vote age at scrape time, not parse lag

### Added

- **Node host monitors**, ported under upstream metric names ([reference](docs/metrics-overview.md#node-host-metrics)):
  - `--process-metrics`: liveness, CPU, memory, threads, fds and IO for `hl-node` and `hl-visor` from `/proc`
  - `--child-stderr-metrics`: child starts, crashes by reason and stderr artifacts from `data/visor_child_stderr`
  - `--visor-metrics`: visor height, hardfork, scheduled freeze and lag from `visor_abci_state.json`
  - `--node-state-metrics`: persisted ABCI and freeze heights from `hyperliquid_data`
  - `--disk-metrics`: usage and per-subdir sizes from a 120 s walk of `NODE_HOME`
  - `--operator-config-metrics` (validators only): operator config presence and age, jailing config
  - `--crit-msg-metrics`: bug and crit counters and the top 32 crit locations from `data/crit_msg_stats`
- `--binary-metrics` (default off): `hl_software_up_to_date` compares local hl-visor against the CDN-published one via ETag conditional fetch
- **Source health** for every consumed stream: `hl_exporter_source_up`, `hl_exporter_source_sample_age_seconds`, `hl_exporter_parse_errors_total` and `hl_exporter_source_errors_total`. A rising parse-error count on a healthy node means hl-node changed a log shape. `hl_exporter_parse_errors_total{stream="gossip_connections"}` stands in for upstream's `hl_p2p_gossip_parse_errors_total`
- `hl_exporter_monitor_panics_total{monitor}`: monitor panics are recovered, logged and counted instead of killing the process
- `hl_p2p_gossip_events_total{event_type}` and `hl_p2p_gossip_unknown_events_total`: gossip events checked against upstream's 15-tag allowlist
- `hl_node_parent_peer_share_ratio` and `hl_node_parent_peer_challenger_ratio`: evidence behind the parent choice
- `hl_consensus_heartbeat_ack_ambiguous_total`: acks that match more than one heartbeat are dropped and counted
- Action types `outcomeDeploy` and `trailingStop`; unknown types report as `other` instead of raw labels
- EVM `block_type="small"` for 3M gas-limit blocks
- **Tooling**: `TestSchemaFixtures` schema-drift test with [schema watch](docs/operations/hl-node-schema-watch.md) and [upstream sync](docs/operations/upstream-sync.md) routines; govulncheck in CI; releases only from `main`

### Changed

- **Peers**
  - Parent selection is stable: per-IP EWMA of inbound volume, 1.2x challenger threshold, lowest-IP tiebreak, cleared after 90 s idle. `hl_node_parent_peer_traffic_volume_total` accrues to the selected parent
  - `tcp_traffic` is parsed once per poll with a strict parser; a malformed row rejects the record
  - Peers from `tcp_traffic` need two consecutive positive-traffic samples in their direction's top 16 before admission; the prober tries the observed port first
  - Per-`peer_ip` gossip series publish only with `--peer-latency`, removing unbounded cardinality without it
- **Consensus and validators**
  - `hl_consensus_vote_time_diff_seconds` reports the age of each validator's last vote at scrape time and drops validators silent for 24 h
  - Per-validator series are removed when a validator leaves the set instead of freezing at their last value
  - Operation `category`: `spotDeploy` and `perpDeploy` move to `deployment`, matching upstream
  - Validator latency files are keyed by UTC date, matching hl-node
- **Runtime**
  - Metrics listener has header, write and idle timeouts and serves at most 5 concurrent scrapes (503 beyond)
  - An unbindable metrics port exits non-zero instead of running without metrics
  - Public IP comes from `last_known_public_ip.json` first, then ipify with a 5 s timeout; failure warns instead of aborting startup
  - Latest-file lookup no longer walks whole log trees (upstream measured ~195% CPU from `filepath.Walk`)
  - `hl_software_version` re-runs `hl-node --version` only when the binary changes
  - No forced GC every 30 s; Go memory gauges read a 30 s snapshot; consensus signer tracking is bounded

### Fixed

- **Metric values**
  - `hl_core_operations_total` counted commas inside order objects as operations, inflating counts 2 to 6x
  - `hl_timeout_rounds_total` was never populated; round-advance events are now parsed from the consensus log
  - `hl_consensus_proposer_count_total` had an empty `name` label
  - `hl_evm_last_high_gas_block_time` reported the Go zero time on zoneless timestamps
  - Heartbeat acks attached to the wrong heartbeat when hl-node reused random IDs across rounds, and `random_id` lost precision above 2^53
  - Gossip counters replayed up to an hour of increments on every restart
  - Above 200 validators, a 30 s labeled-series sweep dropped stake, jailed and latency series; the sweep is removed
- **Log ingestion**
  - Tailers lost one record per file boundary and lines written just before hourly rollover; torn lines are now held and the old file drained
  - Status `current_stakes` wrapped as `{"validator_to_stake": [...]}` left the validator set empty on current hl-node builds
  - Consensus identity under the new `sender` key broke heartbeat ack attribution
  - Status lines over 64 KiB failed to read
  - Validator latency reader missed in-place truncation or replacement
- **Peers**
  - Loopback, link-local, multicast, unspecified and own-interface addresses were registered and probed
  - Persisted peer set could overwrite fresher in-memory entries and kept invalid or 48 h stale entries
  - Child peer state accumulated across snapshots within a poll

### Removed

- `--contract-metrics`, `--contract-metrics-limit`, `hl_evm_contract_tx_total` and the contract resolver: Hyperscan's Blockscout API no longer exists, so the feature failed at startup. `hl_evm_contract_create_total` is unaffected

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

[Unreleased]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.5.0...HEAD
[2.5.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.4.0...v2.5.0
[2.4.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.3.0...v2.4.0
[2.3.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.4...v2.2.0
[2.1.4]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.3...v2.1.4
[2.1.3]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.2...v2.1.3
[2.1.2]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.1...v2.1.2
[2.1.1]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/dwellir-public/hyperliquid-exporter/compare/v1.2.5...v2.0.0
