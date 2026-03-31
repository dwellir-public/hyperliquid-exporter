---
last_edited: 2026-03-31
version: 2.1.4
commit: 5bfc9e9
---

# Hyperliquid Exporter Metrics Reference

## HyperCore Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_core_block_height` | Gauge | - | Current block height | - |
| `hl_core_blocks_processed` | Counter | - | Total blocks processed | `--replica-metrics` |
| `hl_core_block_time_milliseconds` | Histogram | `state_type` | Time between blocks in milliseconds | - |
| `hl_core_latest_block_time` | Gauge | - | Unix timestamp of latest block | - |
| `hl_core_last_processed_round` | Gauge | - | Last processed consensus round | `--replica-metrics` |
| `hl_core_last_processed_time` | Gauge | - | Unix timestamp of last processed block | `--replica-metrics` |
| `hl_core_operations_per_block` | Histogram | - | Distribution of operations per block | `--replica-metrics` |
| `hl_core_operations_total` | Counter | `type`, `category` | Total individual operations by type and category | `--replica-metrics` |
| `hl_core_orders_total` | Counter | - | Total orders placed | `--replica-metrics` |
| `hl_core_rounds_processed` | Counter | - | Total consensus rounds processed | `--replica-metrics` |
| `hl_core_tx_per_block` | Histogram | - | Distribution of transactions per block | `--replica-metrics` |
| `hl_core_tx_total` | Counter | `type` | Total transactions/actions by type | `--replica-metrics` |
| `hl_timeout_rounds_total` | Counter | `suspect` | Total number of timeout rounds | `--replica-metrics` |

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
| `hl_evm_contract_tx_total` | Counter | `contract_address`, `contract_name`, `is_token`, `type`, `symbol`, `block_type`* | Contract interactions by address | - |
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
| `hl_p2p_stream_connections_total` | Counter | `peer_ip`, `type` | Total stream connections per peer IP and type | gossip_connections logs |
| `hl_p2p_verifications_total` | Counter | `peer_ip` | Total gossip RPC verifications per peer IP | gossip_connections logs |

The `incoming_*` metrics are particularly useful for downstream (non-validator) nodes where `child_peers status` is always empty — the upstream peer only appears in `incoming request` events. The `stream_connections` and `verifications` metrics come from a separate log directory (`gossip_connections/`) and track the TCP connection lifecycle.

## Peer Latency Metrics

Requires `--peer-latency` flag. Probes all known peers via TCP connect (ports 4000-4010) once per minute. Peers are discovered from multiple sources and persisted to disk across restarts:

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

The `direction` label is one of `inbound`, `outbound`, or `unknown`. A peer seen in both directions gets two metric series with the same latency value (only one TCP probe is sent per IP). Direction is inferred from the discovery source: child peers and outgoing TCP traffic are `outbound`, incoming requests and inbound TCP traffic are `inbound`, and verified gossip RPCs where direction cannot be determined are `unknown`.

## Parent Peer Metrics

Requires `--peer-latency` flag. Identifies the node's primary upstream peer (the one delivering all block data) by analyzing `tcp_traffic` byte volumes. The peer with the highest inbound traffic value is the parent — in practice, the signal is ~7 orders of magnitude above noise.

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_node_parent_peer` | Gauge | `peer_ip` | Info-style gauge identifying the current parent peer (value=1) | `--peer-latency` |
| `hl_node_parent_peer_traffic` | Gauge | `peer_ip` | Inbound traffic volume from parent peer per interval | `--peer-latency` |
| `hl_node_parent_peer_tenure_seconds` | Gauge | - | How long the current parent peer has held the role | `--peer-latency` |
| `hl_node_parent_peer_switches_total` | Counter | - | Total number of parent peer changes | `--peer-latency` |
| `hl_node_parent_peer_latency_ms` | Gauge | `peer_ip` | TCP connect latency to the parent peer in milliseconds | `--peer-latency` |

**Note on `hl_node_parent_peer_traffic`:** The value is taken directly from the Hyperliquid node's `tcp_traffic` logs. The exact unit is unknown but is probably GB, based on the magnitude of observed values (~1.2-1.9 per 30s interval for the parent peer). The parent peer identification relies on the ratio between peers, not the absolute value.

When the parent changes, the old peer's labeled metrics are removed and the switch counter is incremented. A warning is logged if the runner-up peer has >10% of the top peer's traffic volume, indicating potential ambiguity.

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
| `hl_consensus_proposer_count_total` | Counter | `validator`, `signer`, `name` | Blocks proposed per validator | - |
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
| `hl_consensus_vote_time_diff_seconds` | Gauge | `validator`, `signer`, `name` | Seconds since validator's last vote | Validator node |

### Consensus Monitor Health Metrics

| Metric | Type | Labels | Description | Requirements |
|--------|------|--------|-------------|--------------|
| `hl_consensus_monitor_last_processed` | Gauge | `monitor_type` | Unix timestamp of last processed line by monitor type | Validator node |
| `hl_consensus_monitor_lines_processed_total` | Counter | `monitor_type` | Total lines processed by consensus monitor | Validator node |
| `hl_consensus_monitor_errors_total` | Counter | `monitor_type` | Total errors encountered by consensus monitor | Validator node |

## Label Definitions

### Common Labels
- `validator`: Validator address
- `signer`: Signer address
- `name`: Human-readable validator name
- `type`: Transaction or operation type
- `category`: Operation category (for operations_total)
- `block_type`: EVM block type (standard/high/other) - requires flag


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
