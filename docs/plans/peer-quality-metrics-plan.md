# Peer Quality Metrics: Per-Peer Traffic, Parent Tenure Attribution, Eviction Protection

**Date:** 2026-07-08
**Commit:** b5d5b82 (main)

## TL;DR

The exporter tracks up to 128 peers but produces almost no per-peer quality data: per-peer byte volumes from `tcp_traffic` logs are parsed and discarded (only the max survives, as the parent-peer gauge), block-rate health is never attributed to the peer serving as parent, and per-peer history is destroyed by LRU eviction and parent switches. This plan adds six Prometheus counters that accumulate per-peer traffic, parent tenure, degraded time, and blocks delivered; adds a rate-band detector that classifies each moment of a parent's tenure as healthy or degraded; and protects ex-parent peers from LRU eviction (cap raised 128 → 256). Downstream, a Grafana "best known peers" ledger is built purely in PromQL from these counters (separate plan in the ops repo).

## Problem Statement

Operators need to rank Hyperliquid peers by demonstrated quality of service ("a ledger of best known peers") to (a) reason about node health and parent churn and (b) eventually pin proven-good peers in the node's gossip config. Today the only per-peer signal is TCP connect latency (`hl_peer_latency_ms`) plus reachability counters, which is too weak: latency says nothing about delivery capability. The data needed for better signals already flows through the exporter and is thrown away:

- `internal/monitors/outbound_peers_monitor.go` parses every `tcp_traffic` flow (direction, IP, bytes) but keeps only the IP+direction for peer discovery; bytes are discarded (`processFile`, outbound_peers_monitor.go:92).
- `internal/monitors/parent_peer_monitor.go` finds the top inbound peer per log line but emits only a point-in-time gauge (`hl_node_parent_peer_traffic`); volume history and runner-up data are lost.
- Parent tenure is a single label-less gauge of the *current* spell (`SetParentPeerTenure`, setters.go:1418); when the parent switches, that peer's accumulated service history vanishes.
- Block metrics (`hl_core_block_height`, block time histograms) are node-global; nothing links "did the block rate keep up" to the peer that was delivering blocks at the time.
- The `PeerSet` LRU (`internal/peermon/peers.go`, `maxPeers = 128`) evicts by oldest `LastSeen`, so churn from transient inbound peers can evict exactly the high-value ex-parent peers the ledger cares about.

**Constraints:**

- Prometheus counters are the ledger for this iteration. Ranking/scoring happens in PromQL/Grafana, not in Go. Counters survive exporter restarts via `increase()`; no new persistence beyond the existing `peers.json`.
- Two-tier quality model: all peers get traffic/active-time accounting (observational); only peers serving as parent get block-rate health attribution (score-grade). Non-parent traffic must NOT be presented as a capability signal — an idle gossip peer is not showing what it can deliver.
- The `tcp_traffic` byte field's unit is unconfirmed (docs/metrics-overview.md:125 guesses GB from magnitude). Do not bake a unit conversion into metric names; emit raw units and name metrics `*_volume_total`, consistent with the existing raw `hl_node_parent_peer_traffic` gauge.
- "Block rate remains constant" uses a rate-band definition: time where the short-window block rate falls below 80% of the long-run baseline counts as degraded (catches slowdowns, not just full stalls).
- Follow the existing OTel metrics pattern: instruments registered in `internal/metrics/instruments.go`, setters in `internal/metrics/setters.go`, counters used directly (`.Add`), observable gauges via the `labeledValues`/`currentValues` maps flushed in `internal/metrics/callbacks.go`.

## Non-Goals

- No composite score computed in Go. Weights live in Grafana/PromQL so they are tunable without redeploying.
- No unit conversion of tcp_traffic volumes (left as raw units until verified against interface counters on a live node).
- No changes to parent election (still max-inbound-bytes per line in `findTopInboundPeer`).
- No removal or renaming of existing metrics; `hl_node_parent_peer_traffic`, `hl_node_parent_peer_tenure_seconds` (current-spell gauge), and `hl_node_parent_peer_switches_total` keep their current behavior.
- No new persistence of counter state across restarts (Prometheus `increase()` handles resets).
- No Grafana/dashboard work (separate plan in the ops repo).

## Proposed Solution

Three components:

1. **Per-peer traffic accounting (all peers)** — extend `OutboundPeersMonitor` to accumulate the per-flow byte values it already parses into two new counters: `hl_peer_traffic_volume_total{peer_ip,direction}` and `hl_peer_active_seconds_total{peer_ip}`.
2. **Parent tenure quality attribution** — a new `parentQuality` accumulator in `internal/monitors/parent_quality.go` fed block events from `block_monitor.go` and parent identity from `parent_peer_monitor.go`. Emits `hl_node_parent_peer_tenure_seconds_total{peer_ip}`, `hl_node_parent_peer_degraded_seconds_total{peer_ip}`, `hl_node_parent_peer_blocks_total{peer_ip}`, and `hl_node_parent_peer_traffic_volume_total{peer_ip}`.
3. **Eviction protection for ex-parents** — raise `maxPeers` to 256 and make peers flagged `WasParent` eviction-exempt (evicted only if every peer is an ex-parent), with the flag persisted in `peers.json`.

All new metrics are active whenever the corresponding existing monitors run (gated by the existing `--peer-latency` flag wiring in `internal/exporter/exporter.go:90-113`); no new flags.

## Detailed Design

### 1) Per-peer traffic counters (all peers)

**Goal:** Stop discarding per-peer byte volumes; make "average delivered traffic per active time" answerable in PromQL as `increase(hl_peer_traffic_volume_total[...]) / increase(hl_peer_active_seconds_total[...])`.

**New instruments** (in `internal/metrics/instruments.go`, next to the existing peer-latency block at instruments.go:910-949):

- `HLPeerTrafficVolumeCounter` = `meter.Float64Counter("hl_peer_traffic_volume_total", ...)` — description: "Cumulative tcp_traffic volume per peer and direction (raw units from tcp_traffic logs, unit unconfirmed)".
- `HLPeerActiveSecondsCounter` = `meter.Float64Counter("hl_peer_active_seconds_total", ...)` — description: "Cumulative seconds a peer appeared in tcp_traffic logs with nonzero volume".

Declare the vars alongside the other peer instruments (instruments.go:130-140 area holds the counter fields).

**New setters** (in `internal/metrics/setters.go`, follow `IncrementPeerProbes` at setters.go:1324):

```go
func AddPeerTrafficVolume(peerIP, direction string, volume float64) {
    HLPeerTrafficVolumeCounter.Add(sharedCtx, volume,
        api.WithAttributes(
            attribute.String("peer_ip", peerIP),
            attribute.String("direction", direction)))
}

func AddPeerActiveSeconds(peerIP string, seconds float64) {
    HLPeerActiveSecondsCounter.Add(sharedCtx, seconds,
        api.WithAttributes(attribute.String("peer_ip", peerIP)))
}
```

Direction values: reuse the tcp_traffic mapping already in `trafficDirection` (outbound_peers_monitor.go:147): `"In"` → `inbound`, `"Out"` → `outbound` (i.e. pass `string(peermon.Inbound)` etc.).

**Integration — `OutboundPeersMonitor.processFile` (outbound_peers_monitor.go:92):**

The line callback already unmarshals `entry [2]json.RawMessage` where `entry[0]` is a timestamp string and each flow is `[["In"|"Out","IP",port], bytes]`. It currently ignores `entry[0]` and `pair[1]`. Changes:

1. Unmarshal the flow's byte value (`pair[1]`, a float64) in addition to direction and IP.
2. Accumulate per line: `volumes map[peerEntry]float64` (reuse the existing `peerEntry{ip, dir}` key type at outbound_peers_monitor.go:85) and a per-line set of active IPs (any IP with volume > 0 in either direction).
3. Parse the line timestamp from `entry[0]`. The format matches the block-time layout `"2006-01-02T15:04:05.999999999"` used at block_monitor.go:187 — confirm against a real tcp_traffic line in the test fixtures (`parent_peer_monitor_test.go` fixtures show the line shape) and fall back to skipping active-seconds for unparseable timestamps.
4. Track `lastLineTS time.Time` on the monitor struct. For each line after the first: `delta = ts.Sub(m.lastLineTS).Seconds()`, clamped to `[0, 900]` (15 min cap guards against file gaps and restarts overcounting). For every IP active in that line, call `metrics.AddPeerActiveSeconds(ip, delta)`. Then update `lastLineTS`.
5. For each `(ip, dir) → volume` accumulated from the line, call `metrics.AddPeerTrafficVolume(ip, string(dir), volume)`. tcp_traffic lines are interval snapshots (each line is one interval's volume — the parent monitor's per-line "top peer" logic at parent_peer_monitor.go:87-88 relies on this), so adding each line's value is correct cumulative accounting.
6. On seed processing at startup (`monitor()`, outbound_peers_monitor.go:47-56), the monitor replays the whole current hourly file from offset 0. Volume counters SHOULD count those lines (they are real traffic from this hour); active-seconds accumulate naturally since line timestamps drive the delta. On file switch (`poll()`, outbound_peers_monitor.go:74-78) keep `lastLineTS` — hourly files are continuous.

Do the accounting inside the existing line callback; do not add a second file reader. Only skip counter emission when volume parsing fails for that flow.

**Cardinality:** bounded by whatever IPs appear in tcp_traffic. The PeerSet cap doesn't bound counters, but tcp_traffic only contains actual TCP peers of the node (~130 observed), so this is safe. No removal mechanism exists for OTel counters and none is needed (same as the existing `hl_peer_probes_total`).

### 2) Parent tenure quality attribution

**Goal:** Accumulate, per peer IP, how long it served as parent, how much of that time the block rate was degraded, how many blocks were applied, and how much volume it delivered — so `1 - degraded/tenure` is a per-peer "% of tenure the block rate kept up" and `traffic_volume/tenure` is delivery throughput while parent.

**New instruments** (in the parent-peer block, instruments.go:951-990):

- `HLNodeParentPeerTenureTotalCounter` = `Float64Counter("hl_node_parent_peer_tenure_seconds_total")` — "Cumulative seconds each peer has served as parent".
- `HLNodeParentPeerDegradedTotalCounter` = `Float64Counter("hl_node_parent_peer_degraded_seconds_total")` — "Cumulative seconds of degraded block rate attributed to the parent at the time".
- `HLNodeParentPeerBlocksTotalCounter` = `Int64Counter("hl_node_parent_peer_blocks_total")` — "Blocks applied while each peer was parent".
- `HLNodeParentPeerTrafficTotalCounter` = `Float64Counter("hl_node_parent_peer_traffic_volume_total")` — "Cumulative inbound tcp_traffic volume delivered by each peer while parent (raw units)".

All labeled `peer_ip` only. Setters follow the `IncrementPeerProbes` pattern: `AddParentPeerTenure(ip string, s float64)`, `AddParentPeerDegraded(ip string, s float64)`, `IncrementParentPeerBlocks(ip string)`, `AddParentPeerTrafficVolume(ip string, v float64)`.

**New component — `internal/monitors/parent_quality.go`:**

```go
// parentQuality attributes block-rate health to whichever peer is parent.
// Degraded = short-window block rate below degradedRateFraction of the
// long-run baseline (rate-band), or an outright stall.
type parentQuality struct {
    mu          sync.Mutex
    now         func() time.Time // injectable for tests, defaults to time.Now
    parentIP    string
    lastAccount time.Time // wall clock of last tenure/degraded accounting
    lastBlock   time.Time // wall clock of last block event
    fastGapEMA  float64   // seconds, short-window inter-block gap
    slowGapEMA  float64   // seconds, long-run baseline gap
    samples     int       // blocks seen, for warmup
}

var quality = newParentQuality()

func (q *parentQuality) SetParent(ip string)          // switch attribution target
func (q *parentQuality) OnBlock(blockTime time.Time)  // called per fast-state block
func (q *parentQuality) Flush()                       // periodic accounting (stall coverage)
func (q *parentQuality) degraded() bool               // rate-band check, mu held
```

Constants (top of file):

```go
const (
    degradedRateFraction = 0.8             // short rate < 80% of baseline => degraded
    fastGapAlpha         = 0.2             // ~ last 10-20 blocks
    slowGapAlpha         = 0.005           // ~ last few hundred blocks
    emaWarmupBlocks      = 300             // no degraded verdicts before this
    stallFloorSeconds    = 3.0             // min gap before "no blocks" counts as stall
    maxAccountGapSeconds = 900.0           // clamp for accounting deltas
)
```

Semantics:

- `SetParent(ip)`: account elapsed time to the *old* parent first (see accounting below), then swap `parentIP` and reset `lastAccount`. Called with `ip == ""` never happens (parent monitor only calls on a concrete IP).
- `OnBlock(blockTime)`: compute `gap` between consecutive `blockTime` values (chain timestamps, monotonic per fast state); update both EMAs (`ema = alpha*gap + (1-alpha)*ema`, seeding both with the first gap); `samples++`. Then account elapsed wall time and increment `hl_node_parent_peer_blocks_total{parentIP}`. Update `lastBlock = q.now()`.
- `degraded()`: `samples >= emaWarmupBlocks && fastGapEMA > slowGapEMA/degradedRateFraction`. (Gap is inverse of rate: short-window rate < 80% of baseline ⇔ fast gap > baseline gap / 0.8.)
- Accounting (shared by `OnBlock`, `Flush`, `SetParent`): if `parentIP == ""` or `lastAccount.IsZero()`, just reset `lastAccount` and return. Otherwise `delta = min(now - lastAccount, maxAccountGapSeconds)`; add to `tenure_seconds_total{parentIP}`; add to `degraded_seconds_total{parentIP}` when degraded. For `Flush` specifically, also treat "stall" as degraded: `now - lastBlock > max(stallFloorSeconds, 4*slowGapEMA)` forces the degraded verdict for that delta even before warmup completes... no — keep warmup authoritative for the rate-band, but the stall check applies once at least one block has been seen (`!lastBlock.IsZero()`). Advance `lastAccount = now`.

**Integration:**

- `parseBlockTimeLine` (block_monitor.go:159): inside the existing `if stateType == "fast"` branch (block_monitor.go:231-234), add `quality.OnBlock(parsedTime)`. Only fast state — it drives `hl_core_block_height` today and avoids double-counting across state types. Note the same branch exists in the legacy path; grep for the second `stateType == "fast"` occurrence (block_monitor.go:398 area) and add the call there too.
- `ParentPeerMonitor.updateParent` (parent_peer_monitor.go:167): on parent change (the `ip != m.currentParent` branch), call `quality.SetParent(ip)` next to the existing `m.setParentPeer(ip)` call.
- `ParentPeerMonitor.poll` (parent_peer_monitor.go:70): call `quality.Flush()` after `processFile` so stalls are accounted every `gossipPollInterval` (30s, defined gossip_monitor.go:20) even when no blocks arrive.
- Parent traffic volume: in `ParentPeerMonitor.processFile`'s line callback (parent_peer_monitor.go:93-101), for each line whose `topIP` is non-empty, call `metrics.AddParentPeerTrafficVolume(topIP, topBytes)`. The top inbound peer per line *is* the parent by definition, so per-line accumulation under `topIP` is exactly "volume delivered while parent" — including correct attribution across a switch mid-batch.

**Startup replay caveat:** `ParentPeerMonitor.monitor` seeds from the full current hourly file (parent_peer_monitor.go:50-57), so up to an hour of `topBytes` is replayed into the traffic counter on restart. That matches the active-seconds/volume replay in component 1 and is correct "this hour really happened" accounting; tenure/degraded are wall-clock based and do not replay. Accept the small asymmetry (volume replays, tenure doesn't); ledger queries operate on multi-hour windows where it washes out.

### 3) Eviction protection for ex-parents

**Goal:** Stop LRU churn from evicting the peers whose quality history the ledger exists to keep; raise capacity headroom.

**Changes in `internal/peermon/peers.go`:**

1. `const maxPeers = 128` (peers.go:13) → `256`.
2. Add `WasParent bool \`json:"was_parent,omitempty"\`` to `Peer` (peers.go:25-30). Round-trips through `Load`/`Save` for free.
3. New method:

```go
// MarkParent flags a peer as having served as parent, exempting it from
// LRU eviction. Registers the IP first if unknown.
func (ps *PeerSet) MarkParent(ip string)
```

Implemented as `Register`-like: validate IP, lock, create the peer if absent (parents are by definition seen in tcp_traffic, but ordering between monitors isn't guaranteed), set `WasParent = true`, bump `gen`, mark dirty.

4. `evictOldest` (peers.go:218): evict the oldest `LastSeen` among peers with `!WasParent`; only if every peer has `WasParent` (practically unreachable — parents are a handful), fall back to oldest overall so `Register` can't fail.

**Changes in `internal/peermon/monitor.go`:**

- `Monitor.SetParentPeer` (monitor.go:46): in addition to `m.parentIP.Store(ip)`, call `m.peers.MarkParent(ip)` and `setPeerCount(int64(m.peers.Len()))` (MarkParent can add a peer).

Eviction still calls `removePeerMetrics(evictedIP)` (monitor.go:55), which clears only the latency/reachable gauges. Counters for evicted peers (probes, traffic, tenure) remain exported with their last values until process restart — same as today's behavior for `hl_peer_probes_total`; harmless for `increase()`-based queries.

**Docs:** update `internal/peermon/README.md` (cap 256, parent exemption, `was_parent` field) and `docs/metrics-overview.md` (six new metrics in the peer-latency/parent-peer tables at lines 79-127, including the degraded-time definition and the raw-unit caveat).

## Affected Files

- `internal/metrics/instruments.go` (modify) — 6 new counter instruments + var declarations
- `internal/metrics/setters.go` (modify) — 6 new counter setters
- `internal/monitors/outbound_peers_monitor.go` (modify) — volume + active-seconds accounting in `processFile`
- `internal/monitors/parent_quality.go` (new) — `parentQuality` accumulator + rate-band detector
- `internal/monitors/parent_peer_monitor.go` (modify) — `quality.SetParent` on switch, `quality.Flush` in poll, per-line parent volume counter
- `internal/monitors/block_monitor.go` (modify) — `quality.OnBlock(parsedTime)` in both fast-state branches
- `internal/peermon/peers.go` (modify) — cap 256, `WasParent`, `MarkParent`, eviction exemption
- `internal/peermon/monitor.go` (modify) — `SetParentPeer` marks the peer
- `internal/monitors/parent_quality_test.go` (new)
- `internal/monitors/outbound_peers_monitor_test.go` (modify) — volume/active-seconds cases
- `internal/peermon/peers_test.go` (modify) — exemption + cap cases
- `internal/peermon/README.md`, `docs/metrics-overview.md` (modify)

## Edge Cases & Safety

- **No parent yet / parent monitor disabled** (tcp_traffic dir missing, parent_peer_monitor.go:36): `quality.parentIP` stays empty; `OnBlock`/`Flush` still update EMAs but account nothing. Validator nodes without a parent produce no tenure series — correct.
- **Exporter restart:** counters reset to 0; `increase()`/`rate()` in Prometheus absorb the reset. EMA warmup (300 blocks ≈ a few minutes at sub-second blocks) suppresses degraded verdicts right after restart, avoiding false degraded attribution while the baseline re-forms.
- **Log replay after downtime:** the `maxAccountGapSeconds` clamp (15 min) bounds tenure/degraded deltas; the active-seconds clamp does the same for component 1. A multi-hour outage attributes at most 15 minutes to the pre-outage parent.
- **Ambiguous parent** (runner-up within 10%, parent_peer_monitor.go:107): unchanged behavior; volume still goes to the elected top peer. The existing warning log is the operator signal.
- **Concurrency:** `quality` is called from the block-monitor goroutine (`OnBlock`) and the parent-monitor goroutine (`SetParent`, `Flush`); everything inside `parentQuality` is mutex-guarded. Counter `.Add` calls are OTel-thread-safe. `PeerSet` methods are already mutex-guarded.
- **Non-monotonic block timestamps** (clock skew in chain time): negative gaps are skipped for EMA updates (guard `gap > 0`, mirroring the `blockTimeDiff > 0` guard at block_monitor.go:203).

## Testing Strategy

Metrics init in monitor tests goes through `initTestMetrics` (`internal/monitors/helpers_test.go:13`); peermon tests stub `removePeerMetrics`/`setPeerCount` (monitor.go:21-24). Follow those patterns.

**New tests:**

- `TestParentQuality_RateBand` — inject `now`; feed blocks at a steady gap past warmup, then slow the gap 2×; assert degraded seconds accumulate only after the slowdown. Table-driven over gap patterns.
- `TestParentQuality_Stall` — blocks stop entirely; `Flush` after > stall threshold attributes degraded time.
- `TestParentQuality_SwitchAttribution` — accumulate under parent A, `SetParent("B")`, assert A's tenure stops growing and the pre-switch delta landed on A.
- `TestParentQuality_WarmupSuppression` — degraded never true before `emaWarmupBlocks` samples (rate-band path; stall path still fires).
- `TestOutboundPeersMonitor_TrafficVolume` / `_ActiveSeconds` — fixture lines with two peers and known byte values + timestamps; assert per-(ip,direction) sums and per-ip active seconds (first line contributes no active time). Reuse the fixture-writing helpers from `parent_peer_monitor_test.go:14-115`.
- `TestPeerSet_EvictionSkipsParents` — fill to cap with one `MarkParent`ed peer as oldest; register a new IP; assert a non-parent was evicted.
- `TestPeerSet_MarkParentRegistersUnknown` and a Load/Save round-trip asserting `was_parent` persists (extend `TestPeerSet_LoadSaveRoundTrip`, peers_test.go:83).

Counter assertions: counters are global OTel instruments without a read API in the current setup — where asserting emitted values is awkward, hide setter calls behind small func vars on the monitor/quality structs (the `removePeerMetrics` stub pattern at monitor.go:21) and assert against test doubles.

**Commands:**

- `go test ./internal/monitors/ -run 'TestParentQuality|TestOutboundPeersMonitor'`
- `go test ./internal/peermon/ -run TestPeerSet`
- Full: `go test ./...` and `go vet ./...`

**Regression boundary (must keep passing):** all existing tests, in particular `TestParentPeerMonitor_ParentSwitch` (parent_peer_monitor_test.go:40), `TestPeerSet_EvictionOrder` (peers_test.go:54), `TestPeerSet_LoadLegacyJSONWithoutPort` (peers_test.go:119 — proves old peers.json files still load; `was_parent` absent must default false).

## Acceptance Criteria

- `curl :<metrics-port>/metrics` on a running non-validator node shows all six new series with `peer_ip` labels; `hl_peer_traffic_volume_total` covers (roughly) the same IP set as `hl_peer_probes_total`.
- `sum(increase(hl_node_parent_peer_tenure_seconds_total[1h]))` ≈ 3600 on a node with an uninterrupted parent (tenure accounting is wall-clock complete).
- `1 - degraded/tenure` sits near 1.0 during healthy operation and visibly dips during a known slow period.
- A peer marked `was_parent` in `peers.json` survives registering 300 fresh IPs.
- `go test ./...`, `go vet ./...`, `ruff`-equivalent for Go not applicable; existing CI green.
- `docs/metrics-overview.md` documents all six metrics including the degraded definition and raw-unit caveat.

## Open Questions

- tcp_traffic volume unit: verify once against host interface counters (`/proc/net/dev` delta over an hour vs summed `hl_peer_traffic_volume_total`); record the finding in docs/metrics-overview.md. If confirmed GB, a follow-up may convert at ingest and rename to `_bytes_total` — out of scope here.
- `degradedRateFraction`/EMA alphas are educated defaults; tune after observing a week of real data. They are constants, not flags, on purpose — revisit only if retuning becomes frequent.

## Follow-up Suggestions

- The Grafana ledger (recording-rule-free PromQL score + "Best Known Peers" table) is specified in the ops repo: `hyperliquid-dashboard-peer-ledger-plan.md`. It consumes exactly the metric names above — if any name or label changes during implementation, update that plan's "Expected metrics" table or tell the dashboard implementer.
- Long-term: export the ledger to a standalone dataset by querying these counters over the Prometheus HTTP API.
