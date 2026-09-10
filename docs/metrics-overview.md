---
last_edited: 2026-07-08
version: 2.2.0
commit: 37891c7
---

# Hyperliquid Exporter Metrics Reference

## HyperCore Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_core_block_height` | Gauge | - | Current block height | - |
| `hl_core_blocks_processed` | Counter | - | Total blocks processed | `--replica-metrics` |
| `hl_core_block_time_milliseconds` | Histogram | `state_type` | Time between blocks in milliseconds | - |
| `hl_core_block_propagation_latency_milliseconds` | Histogram | `state_type`, `peer_ip` | Block propagation latency (`begin_block_wall_time` minus `block_time`) labeled by the parent peer delivering the block; `peer_ip="unknown"` without `--peer-latency` | - |
| `hl_core_latest_block_time` | Gauge | - | Unix timestamp of latest block | - |
| `hl_core_last_processed_round` | Gauge | - | Last processed consensus round | `--replica-metrics` |
| `hl_core_last_processed_time` | Gauge | - | Unix timestamp of last processed block | `--replica-metrics` |
| `hl_core_operations_per_block` | Histogram | - | Distribution of operations per block | `--replica-metrics` |
| `hl_core_operations_total` | Counter | `type`, `category` | Total individual operations by type and category (one element of an `orders`, `cancels` or `modifies` array is one operation) | `--replica-metrics` |
| `hl_core_orders_total` | Counter | - | Total orders placed | `--replica-metrics` |
| `hl_core_rounds_processed` | Counter | - | Total consensus rounds processed | `--replica-metrics` |
| `hl_core_tx_per_block` | Histogram | - | Distribution of transactions per block | `--replica-metrics` |
| `hl_core_tx_total` | Counter | `type` | Total transactions/actions by type | `--replica-metrics` |
| `hl_timeout_rounds_total` | Counter | `suspect` | Consensus round advances by timeout certificate (Tc); `suspect` is hl-node's enum (e.g. `NoVote`) or `unknown` | Validator node |

Metrics marked with `--replica-metrics` also require hl-node to be running with `--replica-cmds-style actions-and-responses`.

## EVM Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_evm_account_count` | Gauge | - | Total number of EVM accounts | - |
| `hl_evm_base_fee_gwei` | Gauge | `block_type`* | Current base fee in Gwei | - |
| `hl_evm_base_fee_gwei_distribution` | Histogram | `block_type`* | Distribution of base fees (0-1000+ Gwei buckets) | - |
| `hl_evm_block_height` | Gauge | - | Current EVM block height | - |
| `hl_evm_block_time_milliseconds` | Histogram | - | Time between EVM blocks | - |
| `hl_evm_contract_create_total` | Counter | `block_type`* | Total contract creations | - |
| `hl_evm_gas_limit` | Gauge | `block_type`* | Gas limit per block | - |
| `hl_evm_gas_limit_distribution` | Histogram | - | Distribution of gas limits across blocks | - |
| `hl_evm_gas_used` | Gauge | `block_type`* | Gas used per block | - |
| `hl_evm_gas_util` | Gauge | `block_type`* | Gas utilization percentage | - |
| `hl_evm_high_gas_limit_blocks_total` | Counter | `threshold` | Count of high gas limit blocks by threshold | - |
| `hl_evm_last_high_gas_block_height` | Gauge | - | Height of last high gas block | - |
| `hl_evm_last_high_gas_block_limit` | Gauge | - | Gas limit of last high gas block | - |
| `hl_evm_last_high_gas_block_time` | Gauge | - | Unix timestamp of last high gas block | - |
| `hl_evm_last_high_gas_block_used` | Gauge | - | Gas used in last high gas block | - |
| `hl_evm_latest_block_time` | Gauge | - | Unix timestamp of latest EVM block | - |
| `hl_evm_max_gas_limit_seen` | Gauge | - | Maximum gas limit observed | - |
| `hl_evm_max_priority_fee` | Gauge | `block_type`* | Current maximum priority fee | - |
| `hl_evm_max_priority_fee_gwei_distribution` | Histogram | `block_type`* | Distribution of priority fees (0-1000+ Gwei buckets) | - |
| `hl_evm_tx_per_block` | Histogram | `block_type`* | Distribution of transactions per block | - |
| `hl_evm_tx_type_total` | Counter | `type`, `block_type`* | Transaction counts by type (Legacy, EIP-1559, etc.) | - |


## Machine Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_metal_apply_duration` | Gauge | `state_type` | Current block application duration | - |
| `hl_metal_apply_duration_milliseconds` | Histogram | `state_type` | Distribution of block application durations | - |
| `hl_metal_last_processed_round` | Gauge | - | Last round processed by replica monitor | `--replica-metrics` |
| `hl_metal_last_processed_time` | Gauge | - | Unix timestamp of last processing by replica monitor | `--replica-metrics` |
| `hl_metal_parse_duration` | Gauge | - | Duration of replica transaction parsing in seconds | `--replica-metrics` |

## Runtime Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_go_heap_objects` | Gauge | - | Number of allocated heap objects | - |
| `hl_go_heap_inuse_mb` | Gauge | - | Heap memory in use in MB | - |
| `hl_go_heap_idle_mb` | Gauge | - | Heap memory idle in MB | - |
| `hl_go_sys_mb` | Gauge | - | Total memory obtained from OS in MB | - |
| `hl_go_num_goroutines` | Gauge | - | Number of goroutines | - |

## P2P Network Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_p2p_non_val_peer_connections` | Gauge | `verified` | Number of non-validator peer connections by verification status | gossip_rpc logs |
| `hl_p2p_non_val_peers_total` | Gauge | - | Total number of connected non-validator peers | gossip_rpc logs |
| `hl_p2p_incoming_requests_total` | Counter | `peer_ip` | Total incoming gossip requests per peer IP | gossip_rpc logs |
| `hl_p2p_incoming_peer_last_seen` | Gauge | `peer_ip` | Unix timestamp of last incoming request per peer IP | gossip_rpc logs |
| `hl_p2p_incoming_peers_active` | Gauge | - | Number of incoming peers seen in last 5 minutes | gossip_rpc logs |
| `hl_p2p_child_peer_connected` | Gauge | `peer_ip`, `verified` | Whether a child peer is connected (1) or absent (0) | gossip_rpc logs |
| `hl_p2p_child_peer_connections` | Gauge | `peer_ip` | Number of connections per child peer | gossip_rpc logs |
| `hl_p2p_stream_connections_total` | Counter | `peer_ip`, `type` | Total stream connections per peer IP and type | gossip_connections logs, `--peer-latency` |
| `hl_p2p_verifications_total` | Counter | `peer_ip` | Total gossip RPC verifications per peer IP | gossip_connections logs, `--peer-latency` |
| `hl_p2p_gossip_events_total` | Counter | `event_type` | Gossip connection events by tag, counted after exporter start. `event_type` is one of 15 allowlisted snake_case tags or `other` | gossip_connections logs |
| `hl_p2p_gossip_unknown_events_total` | Counter | - | Gossip connection events whose tag is not in the allowlist. A rising value means hl-node added an event type | gossip_connections logs |

The `incoming_*` metrics are particularly useful for downstream (non-validator) nodes where `child_peers status` is always empty — the upstream peer only appears in `incoming request` events. The `stream_connections` and `verifications` metrics come from a separate log directory (`gossip_connections/`) and track the TCP connection lifecycle.

Every `peer_ip`-labeled series in this section is published only with `--peer-latency`, where the peer set is bounded and curated. Without the flag only the aggregate gauges and the `event_type` counters are emitted. Peer discovery for the latency monitor still runs off the same events.

## Peer Latency Metrics

Requires `--peer-latency` flag. Probes all known peers via TCP connect once per minute, trying the port last seen for that peer (from a successful probe or from `tcp_traffic`) before falling back to 3001, 3002, 443, 80 and 4000-4010. Peers discovered from `tcp_traffic` are admitted only after carrying positive traffic in two consecutive 30 s samples while in the top 16 endpoints of their direction, so transient connections do not churn the set. Peers are discovered from multiple sources and persisted to disk across restarts:

- **gossip_rpc logs**: child peer status and incoming request events (inbound connections)
- **gossip_connections logs**: stream connection and verification events (inbound connections)
- **tcp_traffic logs**: all IPs the node exchanges TCP data with (outbound and inbound)

The `tcp_traffic` source is particularly important for non-validator nodes that have no inbound peer connections (ports 4001/4002 not open) -- without it, the peer set would remain empty since all gossip log events are inbound-only.

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_peer_latency_ms` | Gauge | `peer_ip`, `direction` | TCP connect latency to peer in milliseconds | `--peer-latency` |
| `hl_peer_reachable` | Gauge | `peer_ip`, `direction` | Whether peer is reachable via TCP (1=yes, 0=no) | `--peer-latency` |
| `hl_peer_probes_total` | Counter | `peer_ip` | Total probe attempts per peer IP | `--peer-latency` |
| `hl_peer_probe_failures_total` | Counter | `peer_ip` | Total failed probes per peer IP | `--peer-latency` |
| `hl_peer_monitored_count` | Gauge | - | Number of peers in the monitored set | `--peer-latency` |
| `hl_peer_traffic_volume_total` | Counter | `peer_ip`, `direction` | Cumulative tcp_traffic volume per peer and direction (raw units, unconfirmed) | `--peer-latency` |
| `hl_peer_active_seconds_total` | Counter | `peer_ip` | Cumulative seconds a peer appeared in tcp_traffic logs with nonzero volume | `--peer-latency` |

The `direction` label is one of `inbound`, `outbound`, or `unknown`. A peer seen in both directions gets two metric series with the same latency value (only one TCP probe is sent per IP). Direction is inferred from the discovery source: child peers and outgoing TCP traffic are `outbound`, incoming requests and inbound TCP traffic are `inbound`, and verified gossip RPCs where direction cannot be determined are `unknown`.

**Note on `hl_peer_traffic_volume_total`:** same raw-unit caveat as `hl_node_parent_peer_traffic` below -- the value is summed directly from `tcp_traffic` log entries without any unit conversion. Use `increase(hl_peer_traffic_volume_total[...]) / increase(hl_peer_active_seconds_total[...])` to compare peers' average delivered traffic per active time; do not treat the raw cumulative value as a byte count until the unit is confirmed. These counters cover all discovered peers regardless of parent status -- they are an observational signal, not a capability signal, since an idle gossip peer produces near-zero traffic without that meaning it can't deliver.

## Parent Peer Metrics

Requires `--peer-latency` flag. Infers the node's primary upstream peer from `tcp_traffic` byte volumes. `In` in that log is a byte direction, not a connection role, so "parent" is an inference: the endpoint that dominates smoothed inbound traffic. In practice the signal is ~7 orders of magnitude above noise. Selection: per-IP inbound volume is aggregated across ports and smoothed with an EWMA (alpha 0.3); a challenger must exceed the incumbent's smoothed value by 1.2x to take over; ties break on the lowest IP; the parent is cleared after 90 s without any inbound traffic. `hl_node_parent_peer_share_ratio` and `hl_node_parent_peer_challenger_ratio` expose the evidence behind the current choice.

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_node_parent_peer` | Gauge | `peer_ip` | Info-style gauge identifying the current parent peer (value=1) | `--peer-latency` |
| `hl_node_parent_peer_traffic` | Gauge | `peer_ip` | Inbound traffic volume from parent peer per interval | `--peer-latency` |
| `hl_node_parent_peer_tenure_seconds` | Gauge | - | How long the current parent peer has held the role | `--peer-latency` |
| `hl_node_parent_peer_switches_total` | Counter | - | Total number of parent peer changes | `--peer-latency` |
| `hl_node_parent_peer_latency_ms` | Gauge | `peer_ip` | TCP connect latency to the parent peer in milliseconds | `--peer-latency` |
| `hl_node_parent_peer_share_ratio` | Gauge | - | Parent's smoothed inbound volume as a fraction of all smoothed inbound volume (1 = sole source) | `--peer-latency` |
| `hl_node_parent_peer_challenger_ratio` | Gauge | - | Strongest non-parent peer's smoothed inbound volume divided by the parent's; above 1.2 triggers a switch | `--peer-latency` |
| `hl_node_parent_peer_tenure_seconds_total` | Counter | `peer_ip` | Cumulative seconds each peer has served as parent | `--peer-latency` |
| `hl_node_parent_peer_degraded_seconds_total` | Counter | `peer_ip` | Cumulative seconds of degraded block rate attributed to the parent at the time (see definition below) | `--peer-latency` |
| `hl_node_parent_peer_blocks_total` | Counter | `peer_ip` | Blocks applied while each peer was parent | `--peer-latency` |
| `hl_node_parent_peer_traffic_volume_total` | Counter | `peer_ip` | Cumulative inbound tcp_traffic volume delivered by each peer while parent (raw units) | `--peer-latency` |

**Note on `hl_node_parent_peer_traffic` and `hl_node_parent_peer_traffic_volume_total`:** The value is taken directly from the Hyperliquid node's `tcp_traffic` logs. The exact unit is unknown but is probably GB, based on the magnitude of observed values (~1.2-1.9 per 30s interval for the parent peer). The parent peer identification relies on the ratio between peers, not the absolute value. `hl_node_parent_peer_traffic_volume_total` accumulates the same raw, unit-unconfirmed values reported by the point-in-time gauge -- no conversion is applied.

When the parent changes, the old peer's labeled metrics are removed and the switch counter is incremented. A warning is logged if the runner-up peer has >10% of the top peer's traffic volume, indicating potential ambiguity.

**Degraded time definition (`hl_node_parent_peer_degraded_seconds_total`):** a peer's tenure is counted as degraded while the short-window block rate falls below 80% of the long-run baseline rate (rate-band detection: fast EMA of inter-block gaps vs. a slow EMA baseline), or while blocks stall outright (no block for longer than the greater of a floor and 4x the baseline gap). The rate-band verdict requires a warmup period (no degraded verdicts on a fresh baseline) so it does not misfire immediately after an exporter restart or parent switch. `1 - increase(hl_node_parent_peer_degraded_seconds_total[...]) / increase(hl_node_parent_peer_tenure_seconds_total[...])` gives the fraction of a peer's tenure the block rate kept up.

## Software Version Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_software_up_to_date` | Gauge | - | Whether software is up to date (0=outdated, 1=current) | - |
| `hl_software_version` | Gauge | `date`, `commit` | Software version info (always 1, version in labels) | - |


## Consensus Metrics

### Basic Validator Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_consensus_active_stake` | Gauge | - | Total stake of active validators | - |
| `hl_consensus_inactive_stake` | Gauge | - | Total stake of inactive validators | - |
| `hl_consensus_jailed_stake` | Gauge | - | Total stake of jailed validators | - |
| `hl_consensus_not_jailed_stake` | Gauge | - | Total stake of non-jailed validators | - |
| `hl_consensus_proposer_count_total` | Counter | `validator`, `signer`, `name` | Blocks proposed per validator; `name` is omitted until the validator API has supplied the moniker | - |
| `hl_consensus_total_stake` | Gauge | - | Total network stake | - |
| `hl_consensus_validator_active_status` | Gauge | `validator`, `signer`, `name` | Validator active status (0=inactive, 1=active) | - |
| `hl_consensus_validator_count` | Gauge | - | Total number of validators | - |
| `hl_consensus_validator_jailed_status` | Gauge | `validator`, `signer`, `name` | Validator jail status (0=not jailed, 1=jailed) | - |
| `hl_consensus_validator_rtt` | Gauge | `validator`, `moniker`, `ip` | Validator response time in milliseconds | Requires RTT monitoring enabled (auto-enabled on validator nodes) |
| `hl_consensus_validator_stake` | Gauge | `validator`, `signer`, `moniker` | Stake amount per validator | - |

The above are available to all node types, while the below metrics require access to consensus (running a validator)

### Validator Node Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_consensus_current_round` | Gauge | - | Current consensus round from block messages | Validator node |
| `hl_consensus_heartbeat_ack_delay_ms` | Histogram | - | Heartbeat acknowledgement delays | Validator node |
| `hl_consensus_heartbeat_ack_received_total` | Counter | `from_validator`, `to_validator`, `from_name`, `to_name` | Heartbeat acknowledgments between validator pairs | Validator node |
| `hl_consensus_heartbeat_sent_total` | Counter | `validator`, `signer`, `name` | Total heartbeats sent by validators | Validator node |
| `hl_consensus_heartbeat_status` | Gauge | `validator`, `signer`, `name`, `status_type` | Heartbeat health metrics (status_type: since_last_success, last_ack_duration) | Validator node |
| `hl_consensus_qc_participation_rate` | Gauge | `validator`, `signer`, `name` | Percentage of recent blocks where validator signed QC (100 blocks sliding) | Validator node |
| `hl_consensus_qc_round_lag` | Gauge | - | Average difference between block round and QC round | Validator node |
| `hl_consensus_qc_signatures_total` | Counter | `validator`, `signer`, `name` | Cumulative QC signatures by each validator | Validator node |
| `hl_consensus_qc_size` | Histogram | - | Distribution of QC signer counts per block | Validator node |
| `hl_consensus_rounds_per_block` | Gauge | - | Average rounds needed to produce a block | Validator node |
| `hl_consensus_tc_blocks_total` | Counter | `proposer`, `signer`, `name` | Total blocks proposed containing timeout certificates | Validator node |
| `hl_consensus_tc_participation_total` | Counter | `validator`, `signer`, `name` | Total timeout votes sent by each validator | Validator node |
| `hl_consensus_tc_size` | Histogram | - | Distribution of timeout vote counts in TC blocks | Validator node |
| `hl_consensus_validator_connectivity` | Gauge | `validator`, `peer`, `validator_name`, `peer_name` | Real-time connectivity status (0=disconnected, 1=connected) | Validator node |
| `hl_consensus_validator_latency_ema_seconds` | Gauge | `validator`, `signer`, `name` | Exponential moving average of validator latency | Validator node with latency monitoring |
| `hl_consensus_validator_latency_round` | Gauge | `validator`, `signer`, `name` | Consensus round when latency was last measured | Validator node with latency monitoring |
| `hl_consensus_validator_latency_seconds` | Gauge | `validator`, `signer`, `name` | Current network latency to validator in seconds | Validator node with latency monitoring |
| `hl_consensus_vote_round` | Gauge | `validator`, `signer`, `name` | Last voting round for each validator | Validator node |
| `hl_consensus_vote_time_diff_seconds` | Gauge | `validator`, `signer`, `name` | Seconds since the validator's last observed vote (scrape time minus the vote's log timestamp); series dropped after 24 h without a vote | Validator node |

### Consensus Monitor Health Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_consensus_monitor_last_processed` | Gauge | `monitor_type` | Unix timestamp of last processed line by monitor type | Validator node |
| `hl_consensus_monitor_lines_processed_total` | Counter | `monitor_type` | Total lines processed by consensus monitor | Validator node |
| `hl_consensus_monitor_errors_total` | Counter | `monitor_type` | Total errors encountered by consensus monitor | Validator node |

## Exporter Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_exporter_monitor_panics_total` | Counter | `monitor` | Panics recovered in exporter monitor goroutines. Any non-zero value means a monitor stopped reporting until restart | - |
| `hl_exporter_source_up` | Gauge | `stream` | 1 when the last poll of a consumed log stream resolved and read a file without error. Distinguishes "no peers" from "source unreadable" | - |
| `hl_exporter_source_sample_age_seconds` | Gauge | `stream` | Seconds since the last well-formed record was read from the stream | - |
| `hl_exporter_parse_errors_total` | Counter | `stream`, `stage` | Records rejected by a stream parser. `stage` is `json`, `shape`, `timestamp`, `payload`, `record` or `row`. A rising value on a healthy node means hl-node changed a log shape | - |

`stream` is one of `gossip_rpc`, `gossip_connections`, `tcp_traffic`. Other streams will be added as their parsers gain the same envelope.

## Label Definitions

### Common Labels
- `validator`: Validator address
- `signer`: Signer address
- `name`: Human-readable validator name
- `type`: Transaction or operation type. Action types outside the known hl-node vocabulary (`internal/actiontypes`) are reported as `other`
- `category`: Operation category (for operations_total)
- `block_type`: EVM block type (small/standard/high/other) - requires flag


## Metric Requirements

### Replica Metrics
Metrics that require `--replica-metrics` flag AND hl-node running with `--replica-cmds-style actions-and-responses`:
- All `hl_core_tx_*` metrics
- All `hl_core_operations_*` metrics
- `hl_core_orders_total`
- `hl_core_*_processed_*` metrics
- `hl_metal_parse_duration`
- `hl_metal_last_processed_*` metrics

### Validator Node Metrics
Metrics that require running on a validator node with access to consensus/status logs:
- All `hl_consensus_*` metrics except basic info obtained through api.hyperliquid.xyz/info
