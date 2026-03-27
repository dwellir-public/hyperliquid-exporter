# Hyperliquid Peer Metrics — Exporter Enhancement Plan

**Date:** 2026-03-27
**Commit:** 2d5e510e (main)
**Upstream:** https://github.com/validaoxyz/hyperliquid-exporter

## Problem Statement

The Hyperliquid metrics exporter exposes only aggregate peer counts (`hl_p2p_non_val_peers_total`, `hl_p2p_non_val_peer_connections`) derived from `child_peers status` events. These metrics:

- Show only **child/downstream** peers, not upstream connections
- Report **counts only**, not individual peer IPs or connection health
- Ignore two rich log sources entirely: `gossip_connections/` and `incoming request` events in `gossip_rpc/`

Operators cannot answer basic questions: "Which peers is my node connected to?", "Is my upstream peer healthy?", "When did a peer disconnect?"

**Constraints:**
- Exporter is a third-party Go binary — changes require forking `validaoxyz/hyperliquid-exporter`
- Per-peer IP labels introduce cardinality risk on high-peer-count nodes
- Log formats are undocumented and may change between HL node versions

## Non-Goals

- Modifying the Hyperliquid node itself to expose additional data
- Real-time alerting logic (that belongs in Grafana/Alertmanager)
- Validator-to-validator peering metrics (requires validator node access)

## Background: Data Sources

### Currently parsed

**`data/node_logs/gossip_rpc/hourly/YYYYMMDD/<hour>`** — `GossipMonitor` in
`internal/monitors/gossip_monitor.go` reads `child_peers status` events every 30s:

```json
["2026-03-27T09:00:06.135",["child_peers status",[[{"Ip":"192.168.108.236"},{"verified":true,"connection_count":1}]]]]
```

Only aggregate counts are extracted. Per-peer IP, verified flag, and connection_count are discarded.

### Not parsed: `incoming request` (same log)

```json
["2026-03-27T09:00:09.836",["incoming request","192.168.108.167:44508",false]]
```

- Logged per gossip RPC request from any peer (~10s interval per peer)
- Format: `[timestamp, ["incoming request", "IP:port", bool]]`
- The boolean likely indicates verification status
- **This is the only way a downstream node can identify its upstream** — downstream nodes have empty `child_peers status`

### Not parsed: `gossip_connections/hourly/` (separate log directory)

Completely ignored by the exporter. Three event types:

```json
["2026-03-27T09:00:07.550",["handle_stream_connection","15.235.231.247:57686","gossip"]]
["2026-03-27T09:00:07.551",["performing checks on stream","15.235.231.247","gossip"]]
["2026-03-27T09:00:09.841",["verified gossip rpc",{"Ip":"192.168.108.236"}]]
```

Provides the full connection lifecycle: TCP accept → verification check → verified/rejected.

### Topology observations

From two Dwellir nodes (`.236` downstream, `.167` upstream, same DC):

| Perspective | `child_peers status` | `incoming request` source | `gossip_connections` |
|---|---|---|---|
| `.167` (upstream) | Shows `.236` as verified child | `15.235.231.247` (external) | Connections from `.236` and `15.235.231.247` |
| `.236` (downstream) | Empty `[]` | `.167` every ~10s | Not checked yet |

Key insight: a downstream node's **only** indicator of its upstream peer is `incoming request` entries.

### Other relevant sources (already used by exporter)

- **`data/periodic_abci_states/`** — validator profiles with IPs, used for RTT pinging top 50 validators
- **`data/node_logs/consensus/hourly/`** — vote/block messages revealing validator communication patterns

## Proposed Solution

The **highest-priority metric** is identifying the upstream peer — the node this instance connects to for receiving chain data. A non-validator node's health depends entirely on this connection; if the upstream peer drops or becomes unreachable, the node stalls. Today there is no metric for this. The only evidence is `incoming request` entries in the gossip_rpc logs, where the upstream peer's IP appears as the source of periodic requests (~10s interval).

Enhance peer metrics in three tiers, ordered by operational value:

1. **Track upstream/incoming peers** — parse `incoming request` events to identify the data source peer (highest priority)
2. **Enrich child peer metrics** — extract per-peer detail from already-parsed `child_peers status`
3. **Add connection lifecycle metrics** — parse `gossip_connections/` for TCP-level visibility

Gate behind a `--peer-metrics` flag to control cardinality.

## Detailed Design

### 1) Track upstream/incoming peers (highest priority)

**Goal:** Identify the upstream peer this node receives chain data from.

**Parse target:** `incoming request` events in `gossip_rpc/hourly/`:

```json
["2026-03-27T09:00:09.836",["incoming request","192.168.108.167:44508",false]]
```

**New metrics:**

```
hl_p2p_incoming_requests_total{peer_ip="192.168.108.167"} 542
hl_p2p_incoming_peer_last_seen{peer_ip="192.168.108.167"} 1.711524009e+09
hl_p2p_incoming_peers_active 2
```

- `hl_p2p_incoming_requests_total` — counter per peer IP (strip port)
- `hl_p2p_incoming_peer_last_seen` — gauge, unix timestamp of last request
- `hl_p2p_incoming_peers_active` — gauge, count of peers seen in last 5 minutes

**Integration:** Add to existing `GossipMonitor`. The log file and tailing infrastructure is already in place — just add a branch for the `"incoming request"` event type alongside the existing `"child_peers status"` branch.

**Cardinality control:**
- Strip port from `IP:port` — track by IP only
- Cap tracked peers at 100 (configurable)
- Age out peers unseen for 10 minutes (configurable)

### 2) Enrich `child_peers status` parsing

**Goal:** Expose per-child-peer detail instead of just counts.

The existing `GossipMonitor` already deserializes the full `child_peers status` JSON. Extend it to register per-peer gauges.

**New metrics:**

```
hl_p2p_child_peer_connected{peer_ip="192.168.108.236", verified="true"} 1
hl_p2p_child_peer_connections{peer_ip="192.168.108.236"} 1
```

- `hl_p2p_child_peer_connected` — gauge, 1 when peer present in latest status, 0 when absent
- `hl_p2p_child_peer_connections` — gauge, `connection_count` from the status entry

**Stale peer handling:** Maintain a map of last-seen peers. On each `child_peers status` event, set absent peers to 0. Remove metrics for peers unseen for >10 minutes to prevent label accumulation.

**Integration:** Modify the existing callback in `GossipMonitor` that processes `child_peers status`. Keep existing aggregate metrics unchanged.

### 3) Parse `gossip_connections/` logs

**Goal:** Track TCP connection lifecycle for full peer visibility.

**New monitor:** `GossipConnectionsMonitor`, following the pattern of existing monitors.

**Log directory:** `data/node_logs/gossip_connections/hourly/YYYYMMDD/<hour>`

**Parse targets:**

| Event | Format | Metric |
|---|---|---|
| `handle_stream_connection` | `["...", ["handle_stream_connection", "IP:port", "gossip"]]` | `hl_p2p_stream_connections_total{peer_ip, type}` counter |
| `performing checks on stream` | `["...", ["performing checks on stream", "IP", "gossip"]]` | (contributes to connection rate, no separate metric needed) |
| `verified gossip rpc` | `["...", ["verified gossip rpc", {"Ip": "..."}]]` | `hl_p2p_verifications_total{peer_ip}` counter |

**New metrics:**

```
hl_p2p_stream_connections_total{peer_ip="15.235.231.247", type="gossip"} 847
hl_p2p_verifications_total{peer_ip="192.168.108.236"} 423
```

**Integration:** Register as a new monitor in the exporter's startup sequence. Follow the existing hourly-log-tailing pattern with seek-offset tracking.

## Implementation Phases

1. **Fork & scaffold** — fork `validaoxyz/hyperliquid-exporter`, add `--peer-metrics` flag, wire up to monitor initialization
2. **Tier 1: upstream peer tracking** — extend `GossipMonitor` to parse `incoming request`, expose per-peer counters and last-seen timestamps
3. **Tier 2: child peer enrichment** — modify `GossipMonitor`, add per-peer gauges from `child_peers status`, add stale-peer cleanup
4. **Tier 3: gossip_connections monitor** — new `GossipConnectionsMonitor`, register in startup
5. **Grafana panels** — add peer panels to `Hyperliquid.json` dashboard (in the `ops` repo: `grafana/dashboards/Hyperliquid.json`)
6. **Charm update** — expose `--peer-metrics` as a charm config option

## Edge Cases & Safety

- **High peer count (validators):** Cardinality bounded by configurable max peers + age-out. Tier 2–3 behind opt-in flag.
- **Log format changes:** HL node updates may change JSON structure. Parse defensively — log warnings on unknown formats, don't crash.
- **Log rotation / missing hours:** Existing monitors already handle hourly file transitions. New monitor follows same pattern.
- **Empty child_peers:** Downstream nodes always report `[]` — this is normal, not an error. Tier 2 metrics are what matter for these nodes.
- **Port churn:** `incoming request` shows different ports per request from the same peer. Always strip port before using as label.

## Open Questions

- Should `incoming request` boolean (third field) be exposed as a label? Needs more data points to confirm it means "verified."
- What is the upper bound on distinct peer IPs a production validator sees? This drives cardinality decisions.
- Should peer metrics be tagged with the peer's validator name (if resolvable from ABCI state)? Would be useful but adds complexity.

## Follow-up: Grafana Dashboard Gaps

The Grafana dashboard (`Hyperliquid.json`) lives in the `ops` repo (`grafana/dashboards/Hyperliquid.json`), not in this exporter repo. Panel changes should be made there.

The dashboard is missing panels for:
- Validator RTT/latency metrics (`hl_validator_ip_rtt_*`)
- All P2P peer metrics (existing and proposed)
- EVM metrics (if `--evm-metrics` is enabled)

These should be addressed separately from the peer metrics work.

## Summary

The Hyperliquid node writes rich peer connectivity data to three log locations, but the exporter only extracts aggregate counts from one. By enhancing the existing `GossipMonitor` to expose per-peer detail from `child_peers status` and `incoming request` events, and adding a new monitor for `gossip_connections/` logs, operators gain visibility into which peers their node communicates with, connection health, and upstream identification — all critical for troubleshooting connectivity in a multi-node deployment. The work is structured in three tiers behind an opt-in flag to manage cardinality risk.
