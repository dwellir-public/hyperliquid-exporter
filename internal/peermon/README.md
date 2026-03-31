# peermon

Peer latency monitoring for Hyperliquid nodes. Enabled with `--peer-latency`.

## How it works

1. Peers are discovered from three sources via `Register(ip)`: gossip_rpc logs (child peers, incoming requests), gossip_connections logs (stream connections, verifications), and tcp_traffic logs (all TCP peer IPs). The tcp_traffic source ensures non-validator nodes with no inbound connections still discover outbound peers.
2. The `PeerSet` maintains a bounded set (max 100) of known peers, persisted to `.hyperliquid-exporter/peers.json`. When full, the peer with the oldest last-seen timestamp is evicted.
3. Every minute, the monitor triggers a probe cycle if one is not already running. Each peer probe has a 5s deadline, tries the peer's last successful port first, and then probes the remaining ports 4000-4010 concurrently when needed.
4. Results are exposed as Prometheus metrics: `hl_peer_latency_ms`, `hl_peer_reachable`, `hl_peer_probes_total`, `hl_peer_probe_failures_total`, `hl_peer_monitored_count`.

## Package structure

- `peers.go` -- Thread-safe peer registry with JSON persistence and LRU eviction.
- `prober.go` -- TCP connect probe against port range 4000-4010.
- `monitor.go` -- Orchestrator: synchronous registration, probe loop, metric updates, shutdown save.
