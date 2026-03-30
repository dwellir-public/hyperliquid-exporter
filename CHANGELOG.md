# Changelog

All notable changes to the Hyperliquid Exporter will be documented in this file.

## [2.1.0]

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

