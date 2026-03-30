# Peer Latency Monitoring — Implementation Plan

**Date:** 2026-03-30
**Commit:** 98c4d2d (main)
**Branch:** main (clean)

## Problem Statement

The exporter now tracks which peers exist (via gossip and connection logs), but not whether those peers are reachable or how fast the network path to them is. Operators cannot answer: "Is my upstream peer healthy right now?", "Has latency to a specific peer degraded?", or "Which of my peers has the most stable connection?"

The existing validator RTT monitor (`validator_ip_monitor.go:182-211`) measures latency to the top 50 validators by public IP, but:

- Only covers validators, not the node's actual gossip peers
- Uses a hardcoded top-50 selection, not the node's real peer set
- Runs in the `monitors` package with no reuse path

We need per-peer latency monitoring driven by the peers the node actually communicates with — child peers, incoming peers, and gossip connection peers — with persistence across restarts so long-term trends are visible.

**Constraints:**

- Peers expose TCP services on ports 4000-4010 (same range used by validator RTT)
- Peer set is small (typically 1-10 peers), but should handle up to ~50 with an upper bound
- Must not block or slow down existing monitors — latency probing runs independently
- Peer IPs come from three sources already in the codebase, all in `internal/monitors/`

## Non-Goals

- Replacing the existing validator RTT monitor (different purpose, different peer selection)
- ICMP ping (requires root or `CAP_NET_RAW`; TCP connect works unprivileged)
- Alerting logic (belongs in Grafana/Alertmanager)
- Latency to non-peer IPs (validators not in our peer set)

## Background: Existing Peer Discovery

Three monitors already discover peer IPs:

| Monitor | File | Peer source | How discovered |
|---|---|---|---|
| `GossipMonitor` | `gossip_monitor.go:164-252` | Incoming request peers | `"incoming request"` events, IP extracted via `net.SplitHostPort` |
| `GossipMonitor` | `gossip_monitor.go:160-231` | Child/downstream peers | `"child_peers status"` events, IP from `PeerInfo.IP` |
| `GossipConnectionsMonitor` | `gossip_connections_monitor.go:121-145` | Stream connection peers | `"handle_stream_connection"` and `"verified gossip rpc"` events |

Currently these monitors call metric setters directly. There is no shared peer registry — each monitor tracks peers in its own maps (`peerLastSeen`, `knownChildPeers`).

### Existing RTT Pattern

`measureRTT` in `validator_ip_monitor.go:182-211`:
```go
for port := 4000; port <= 4010; port++ {
    start := time.Now()
    conn, err := net.DialTimeout("tcp", address, 2*time.Second)
    if err == nil {
        latency = float64(time.Since(start).Microseconds())
        _ = conn.Close()
        break
    }
}
```

This pattern works — TCP connect to the first responding port in 4000-4010, measure dial time. We reuse this approach.

## Proposed Solution

A new `internal/peermon/` package that:

1. Maintains a bounded, persistent set of known peers (IP + last-seen timestamp)
2. Accepts peer registrations from existing monitors via a channel
3. Runs periodic TCP latency probes against all known peers (once per minute)
4. Exposes latency and reachability metrics per peer IP
5. Persists the peer set to disk so it survives restarts

### Architecture

```
                     +-----------------+
                     |  GossipMonitor  |---+
                     +-----------------+   |
                                           |   Register(ip)
                     +-----------------+   +---> +-------------+
                     | GossipConnMon   |-------> | PeerMonitor |---> metrics
                     +-----------------+         +------+------+
                                                        |
                                                   load / save
                                                        |
                                                 peers.json (disk)
```

The `PeerMonitor` is a standalone goroutine. Monitors push peer IPs into it; it probes them on its own schedule.

## Detailed Design

### 1) Peer Set (`internal/peermon/peers.go`)

Thread-safe peer registry with disk persistence.

```go
type Peer struct {
    IP       string    `json:"ip"`
    LastSeen time.Time `json:"last_seen"`
}

type PeerSet struct {
    mu       sync.RWMutex
    peers    map[string]*Peer // keyed by IP
    maxPeers int
    path     string           // persistence file path
}
```

**Operations:**

- `Register(ip string)` — add or update last-seen. If at capacity, evict peer with oldest `LastSeen`.
- `All() []Peer` — return snapshot for probing.
- `Load()` — read `peers.json` from disk on startup.
- `Save()` — write `peers.json` to disk. Called after registration changes, debounced (at most once per minute).

**Persistence format** (`peers.json`):
```json
[
  {"ip": "192.168.108.167", "last_seen": "2026-03-30T12:00:00Z"},
  {"ip": "15.235.231.247", "last_seen": "2026-03-30T11:55:00Z"}
]
```

File location: `{cwd}/.hyperliquid-exporter/peers.json` (relative to the binary's working directory). Create directory on first save.

**Capacity:** Default 50 peers max. No CLI flag — constant is sufficient given peer counts are naturally small.

**Eviction:** When `Register` is called at capacity and the IP is new, find the peer with the oldest `LastSeen` and remove it. This ensures the set naturally retains recently-active peers.

### 2) Latency Prober (`internal/peermon/prober.go`)

```go
type ProbeResult struct {
    IP        string
    Latency   time.Duration // zero if unreachable
    Reachable bool
    Port      int           // which port responded
}

func Probe(ctx context.Context, ip string) ProbeResult
```

Reuses the proven pattern from `validator_ip_monitor.go:195-206`:
- Try TCP connect to ports 4000-4010 sequentially
- 2-second timeout per port
- Return latency of first successful connect
- If all ports fail, return `Reachable: false`

### 3) Peer Monitor (`internal/peermon/monitor.go`)

Main orchestrator. Owns the `PeerSet`, runs the probe loop, writes metrics.

```go
type Monitor struct {
    peers    *PeerSet
    register chan string    // buffered channel for peer registration
    interval time.Duration // probe interval (1 minute)
}

func New(dataDir string) *Monitor

func (m *Monitor) Start(ctx context.Context, errCh chan<- error)

func (m *Monitor) Register(ip string)
```

**`Start` loop:**
1. Load `peers.json` from disk
2. Log loaded peer count
3. Enter ticker loop (1 minute interval):
   a. Snapshot all peers via `PeerSet.All()`
   b. Probe each peer concurrently (bounded to 10 goroutines)
   c. Update metrics for each result
   d. Update aggregate metrics (active count, avg latency)
4. Concurrently drain `register` channel, calling `PeerSet.Register`
5. On context cancellation, save peers to disk and return

**Concurrency model:**
- Registration channel is buffered (256). Monitors call `Register(ip)` which sends on the channel — non-blocking with a fallback log warning if full.
- Probe goroutines bounded by a semaphore (10 concurrent). Each probe takes at most 22s worst case (11 ports x 2s timeout), but typically <100ms.
- `PeerSet` mutex protects the map; probing uses a snapshot so the lock isn't held during I/O.

### 4) Metrics (`internal/metrics/`)

**New instruments** (add to `instruments.go` after the existing P2P section):

```
hl_peer_latency_us{peer_ip}          — gauge, TCP connect latency in microseconds (0 if unreachable)
hl_peer_reachable{peer_ip}           — gauge, 1 if last probe succeeded, 0 if not
hl_peer_probes_total{peer_ip}        — counter, total probe attempts per peer
hl_peer_probe_failures_total{peer_ip} — counter, failed probes per peer
hl_peer_monitored_count              — gauge, number of peers in the set
```

Microseconds matches the existing `hl_consensus_validator_rtt` unit for consistency.

**Setters** (add to `setters.go`):

```go
func SetPeerLatency(peerIP string, latencyUs float64)
func SetPeerReachable(peerIP string, reachable bool)
func IncrementPeerProbes(peerIP string)
func IncrementPeerProbeFailures(peerIP string)
func SetPeerMonitoredCount(count int64)
```

### 5) Integration Points

**Feed peers from monitors** — modify the three peer-discovering monitors to call `peerMonitor.Register(ip)` when they see a peer IP:

- `gossip_monitor.go` — after extracting IP from `incoming request` (~line 247) and from `child_peers status` (~line 220)
- `gossip_connections_monitor.go` — after extracting IP from `handle_stream_connection` (~line 132) and `verified gossip rpc` (~line 143)

The `Monitor` is passed to these monitors as a dependency. Since monitors currently receive `*config.Config`, the simplest integration is to pass the `*peermon.Monitor` as an additional argument to the `Start*` functions.

**Exporter startup** (`exporter.go`):

```go
var peerMon *peermon.Monitor
if cfg.EnablePeerLatency {
    peerMon = peermon.New(".hyperliquid-exporter")
    peerLatencyErrCh := make(chan error, 1)
    go peerMon.Start(monitorCtx, peerLatencyErrCh)
}

// pass peerMon to gossip monitors (nil-safe — monitors check before calling Register)
go monitors.StartGossipMonitor(monitorCtx, &cfg, gossipErrCh, peerMon)
go monitors.StartGossipConnectionsMonitor(monitorCtx, &cfg, gossipConnErrCh, peerMon)
```

**CLI flag:** `--peer-latency` (opt-in, like `--validator-rtt`). Adds `EnablePeerLatency bool` to `Config` and `*bool` to `Flags`.

## Implementation Phases

### Phase 1: Core peer set with persistence

**Files:** `internal/peermon/peers.go`, `internal/peermon/peers_test.go`

1. Implement `PeerSet` struct with `Register`, `All`, `Len` methods
2. Implement `Load`/`Save` with JSON file I/O and atomic writes (write to temp, rename)
3. Implement eviction of oldest `LastSeen` when at capacity
4. Debounced save: track dirty flag, save at most once per minute
5. Tests: register, eviction ordering, load/save round-trip, capacity limits, concurrent access

### Phase 2: TCP prober

**Files:** `internal/peermon/prober.go`, `internal/peermon/prober_test.go`

1. Implement `Probe` function (TCP connect to ports 4000-4010, 2s timeout)
2. Return `ProbeResult` with latency, reachability, responding port
3. Tests: probe against localhost listener, probe unreachable host (short timeout), context cancellation

### Phase 3: Metrics

**Files:** `internal/metrics/instruments.go`, `internal/metrics/setters.go`

1. Define 5 new instruments (3 labeled gauges/counters + 1 aggregate gauge)
2. Add 5 setter functions following existing patterns
3. Register in `InitMetrics`

### Phase 4: Monitor orchestrator

**Files:** `internal/peermon/monitor.go`, `internal/peermon/monitor_test.go`

1. Implement `Monitor` struct with `New`, `Start`, `Register`
2. Registration channel with non-blocking send
3. Probe loop: snapshot peers, probe concurrently (semaphore-bounded), update metrics
4. Save peers on shutdown
5. Tests: register flow, probe scheduling, graceful shutdown

### Phase 5: Integration

**Files:** `internal/exporter/exporter.go`, `internal/monitors/gossip_monitor.go`, `internal/monitors/gossip_connections_monitor.go`

1. Create `peermon.Monitor` in `exporter.Start()`, launch goroutine
2. Add error channel and select case
3. Add `peerMon *peermon.Monitor` parameter to `StartGossipMonitor` and `StartGossipConnectionsMonitor`
4. Call `peerMon.Register(ip)` at each peer discovery point
5. Update existing tests to pass `nil` peer monitor (no-op when nil)

### Phase 6: Documentation

**Files:** `docs/metrics-overview.md`, `CHANGELOG.md`, `CLAUDE.md`

1. Document new metrics in metrics overview
2. Add changelog entry
3. Update CLAUDE.md architecture section to mention `internal/peermon/`

## Edge Cases & Safety

- **Private/unreachable IPs:** Peers behind NAT will fail probes — this is expected and useful signal. The `hl_peer_reachable` metric captures it.
- **Port churn:** Same as existing peer metrics — strip port, track by IP only.
- **Disk write failures:** Log warning, continue operating with in-memory set. Don't crash.
- **Slow probes:** 11 ports x 2s timeout = 22s worst case per peer. With 50 peers and 10 concurrent probers, worst case is ~110s — just over the 1-minute interval. In practice, most probes complete in <100ms. If a cycle overruns, skip to next tick rather than queuing.
- **Nil peer monitor:** Gossip monitors should check for `nil` before calling `Register`, allowing the feature to be omitted without code changes to monitors.
- **File corruption:** If `peers.json` is invalid JSON, log warning and start with empty set.
- **Atomic writes:** Write to `peers.json.tmp` then `os.Rename` to prevent partial writes on crash.

## Decisions

- **CLI flag:** Opt-in via `--peer-latency` (same pattern as `--validator-rtt`)
- **Port range:** Hardcoded 4000-4010, same as validator RTT
- **Latency metric type:** Per-peer gauge (no histogram for now — Grafana can graph trends from gauge over time)
