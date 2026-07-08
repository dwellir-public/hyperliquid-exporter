# peermon

Peer latency monitoring for Hyperliquid nodes. Enabled with `--peer-latency`.

## How it works

1. Peers are discovered from three sources via `Register(ip, direction)`: gossip_rpc logs (child peers as outbound, incoming requests as inbound), gossip_connections logs (stream connections with direction from connType, verifications as unknown), and tcp_traffic logs (direction parsed from the `"In"`/`"Out"` field). The tcp_traffic source ensures non-validator nodes with no inbound connections still discover outbound peers.
2. The `PeerSet` maintains a bounded set (max 256) of known peers keyed by IP, persisted to `.hyperliquid-exporter/peers.json`. Each peer tracks a set of directions it has been seen with. When full, the peer with the oldest last-seen timestamp is evicted, skipping peers flagged `was_parent` (peers that have ever served as parent) so their quality history isn't churned out; the fallback to oldest-overall (including parents) only triggers if every tracked peer is an ex-parent, which keeps `Register` from ever failing to add a new peer.
3. `SetParentPeer` flags the current parent peer via `MarkParent`, which registers the IP if not already known and sets its `was_parent` flag (persisted in `peers.json` as `"was_parent": true`).
4. Every minute, the monitor triggers a probe cycle if one is not already running. Each peer is probed once (5s deadline), tries the peer's last successful port first, and then probes the remaining ports 4000-4010 concurrently when needed.
5. Results are exposed as Prometheus metrics. `hl_peer_latency_ms` and `hl_peer_reachable` are emitted per direction (a peer seen both inbound and outbound gets two series with the same latency). `hl_peer_probes_total`, `hl_peer_probe_failures_total`, and `hl_peer_monitored_count` are per-IP.

## Package structure

- `peers.go` -- Thread-safe peer registry with JSON persistence and LRU eviction.
- `prober.go` -- TCP connect probe against port range 4000-4010.
- `monitor.go` -- Orchestrator: synchronous registration, probe loop, metric updates, shutdown save.
