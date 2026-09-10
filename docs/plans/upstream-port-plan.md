# Upstream Port Plan (validaoxyz v3.0.0 to v4.1.1)

**Date:** 2026-09-10
**Commit:** 447a838 (main), upstream/main at b150dcc (v4.1.1)
**Status:** phases 1 and 2 landed on branch `upstream-port-sep26` (commits `9bd985e`, `1696ea7`), unreleased. Phase 3 is next; see Handoff. Phases are release-sized units; version numbers are assigned at release prep, not here.

## TL;DR

Our fork descends from upstream v2.0.0 (upstream commit `7ba273a`). Since March 2026 upstream shipped v3.0.0, v3.1.0, v4.0.6, v4.0.7, v4.1.0 and v4.1.1, roughly 41k added lines. Three of those releases fix hl-node log schema changes that our parsers do not handle, so several of our metrics are silently zero or wrong on current nodes. The two git histories share no merge base, so nothing cherry-picks; every item below is a manual, content-level port. This plan lists what to port, where the source lives, and in what order, split into tiers by urgency. Decisions that were open in the first draft are settled in the Decisions section. It also adds a routine for tracking upstream and the hl-node log surface so the drift does not recur.

## Problem Statement

Upstream has diverged into a large rewrite while our fork added peer monitoring on top of the v2.0.0 base. Concretely:

- hl-node changed the `current_stakes` status shape, the round-advance reason encoding, the consensus wrapper identity key, and added action types. Upstream parses all of these. We do not.
- Upstream fixed a set of correctness bugs (operation counts inflated 2 to 6x, stubbed proposer names, wrong vote-age semantics, wrong update-check comparison) that exist verbatim in our tree.
- Upstream measured about 195% CPU from recursive directory walks in EOF loops and removed them. We still have 14 call sites of `utils.GetLatestFile` (a `filepath.Walk`), several inside 10 ms loops.
- We have no panic recovery. One panic in any of about 18 monitor goroutines kills the process.
- Upstream added monitors for process liveness, crash taxonomy, disk, jailing config, snapshot age and more, plus exporter self-observability, CI hardening and generated docs.

**Constraints:**

- No merge base. `git cherry-pick` will not apply. Use `git show upstream/main:<path>` and `git show <commit>` as source and port by hand.
- Upstream renamed `cmd/hyperliquid-exporter` to `cmd/hl-exporter`, split EVM parsing into `internal/evm/parser.go`, and replaced tailing with `internal/monitors/stream.go` and `internal/utils/latest_files.go`. Paths in upstream citations are upstream paths.
- Upstream declares native Prometheus instruments (`internal/metrics/prometheus_instruments.go`). We are OTel-instrument-first. Any ported monitor needs its metrics redeclared in our `internal/metrics/instruments.go` style.
- Keep our metric names. Do not follow the v4.0.6 rename migration (see Non-Goals).
- Keep `internal/peermon`. Upstream's own audit recommends our probing and persistence design.

## Non-Goals

- Adopting upstream's v4.0.6 metric renames (about 35 breaking renames and label drops, listed in `git show upstream/main:UPGRADING.md`). Cost is every dashboard, alert and charm cutover; benefit is compatibility with an alert bundle we cannot use without their source-state layer. Use upstream names only for metrics we port fresh (see Decisions).
- Adopting upstream's `stream.go`, `source_state.go` and `health.go` wholesale (about 1200 lines plus a convention every monitor must follow). Borrow the mechanics where a tier calls for them.
- Porting `tokio_runtime`, `tcp_lz4`, `tmp_dir`, `subsystem_steps`, `consensus_rpc`, `validator_connections`, `peer_set` monitors. Low operator value or coupled to upstream's rewritten consensus monitor.
- Rebasing onto upstream or re-forking.

## Proposed Solution

Port in eight tiers. Tiers 0 to 2 are bug fixes to code we already have and should land first. Tiers 3 and 4 fix existing metric semantics and our peer monitoring. Tier 5 lifts new monitors. Tier 6 is infra. Tier 7 establishes a routine so this review does not need repeating from scratch.

Source locations used throughout:

| Reference | Meaning |
|---|---|
| `up:<path>` | `git show upstream/main:<path>` (v4.1.1, `b150dcc`) |
| `<hash>` | upstream commit, `git show <hash>` |
| `ours:<path>` | our tree at `447a838` (line numbers verified at that commit) |

Upstream release to commit map:

| Release | Date | Commits |
|---|---|---|
| v3.0.0 | 2026-05-26 | `c937bbf` (single squash) |
| v3.1.0 | 2026-07-04 | `2cb58cd`, `f38ddb6`, `d9f74f4`, `7314f28`, `85d9da3`, `7b3009a`, `28660ea`, `7de3849`, `4de8f5a`, `4dc9637`, `2dc5aa9`, `c792055` |
| v4.0.6 | 2026-08-09 | `8813a56` through `d4f7ffb` (metrics audit sweep), incl. `c7a8ca4`, `ff31e33`, `628cdf0`, `302f0d6`, `d078382`, `4f075a8`, `caaaf96`, `69f73bd` |
| v4.0.7 | 2026-08-27 | `cd9e6aa`, `0af6b36` |
| v4.1.0 | 2026-09-09 | `10cc834`, `706b52c`, `85ee802` |
| v4.1.1 | 2026-09-10 | `d30125a`, `3be57c9`, `0ad0e88`, `88b74b3` |

Changelog: `up:CHANGELOG.md`. Migration notes: `up:UPGRADING.md`. Their audit rationale: `up:HL_NODE_METRICS_AUDIT.md`.

### Decisions

Settled here so implementers do not re-derive them.

- **Naming.** Existing metrics keep our names. Metrics ported fresh in Tier 5 and 6 use the upstream v4 name verbatim, so upstream alert rules for those metrics apply unchanged. Record new names in `docs/metrics-overview.md` (or its generated successor), not `BREAKING_CHANGES.md`; nothing existing breaks.
- **Flags.** One flag per new monitor. Default on for monitors that only read local files or procfs (process, child_stderr, visor, node_state, disk, operator_config, crit_msg, snapshot_status). Default off for anything that touches the network (`--binary-metrics`, `--probe-info-endpoint`). No umbrella `--extended-metrics` flag.
- **`--binary-metrics` default off, now.** The current update checker downloads hl-visor every cycle and compares the wrong binaries. Off is the correct default until the charm exposes the flag.
- **Validator-only monitors** (operator_config, accumulator_consensus) start only when `config.IsValidator` is true. No new flag; the identity check at `cmd/hyperliquid-exporter/main.go:145` already exists.
- **Series sweep gate.** Peer and connectivity series already remove themselves (`peermon/monitor.go:195`, `gossip_monitor.go:228-305`, `parent_peer_monitor.go:182`, `consensus_monitor.go:854`). The only unbounded labeled families are per-validator gauges, so 3.4 is the sole prerequisite for deleting the sweep in 2.3.
- **Rescan gate value (2.1).** 2 s for call sites inside EOF loops (block, consensus, replica, evm, proposal, round_advance, validator_status). No gate for pollers that already sleep 30 s or more (gossip, validator_ip).
- **0.3 before 4.1.** 0.3 is the hl-node schema break and ships in phase 1 as a two-line patch. 4.1 rewrites the same file but depends on the 2.1 resolver, so it stays in phase 2 and absorbs 0.3 there.
- **Fixtures for schema watch (7.2).** Commit a trimmed sample (about 20 lines per stream) under `internal/monitors/testdata/schema/` so the drift test runs in CI. The full hour pulled by the routine stays gitignored under `testdata/live/` for local runs.
- **Cross-repo CI survey** is out of scope for this plan. See Follow-ups.

---

## Tier 0: hl-node schema breaks

**Goal:** stop emitting silently wrong values on current hl-node builds. Every item here is small and independent.

### 0.1 `current_stakes` wrapped as `{"validator_to_stake": [...]}`

- Upstream: `2cb58cd` (v3.1.0). Decoder at `up:internal/monitors/validator_status_monitor.go:22-47`.
- Ours: `CurrentStakes [][]any` at `ours:internal/monitors/validator_status_monitor.go:24`, plus the same shape at lines 126, 271, 338.
- Change: decode `current_stakes` into `json.RawMessage`, sniff the leading byte (`[` legacy rows, `{` new object with `validator_to_stake`), and normalize to the existing `[][]any`. Apply at all four declaration sites.

```go
type currentStakes [][]any

func (c *currentStakes) UnmarshalJSON(b []byte) error // accepts [[...]] or {"validator_to_stake": [[...]]}
```

### 0.2 Status last-line reader 64 KiB scanner limit

- Upstream: `2cb58cd`. `scanner.Buffer(make([]byte, 1<<20), 8<<20)` at `up:internal/monitors/validator_status_monitor.go:537`.
- Ours: bare `bufio.NewScanner(file)` at `ours:internal/monitors/validator_status_monitor.go:217`.
- Change: one line, same buffer sizes. Status lines carry the whole validator set.

### 0.3 Round-advance reason is a tagged object

- Upstream: `628cdf0` (v4.0.6). `parseRoundAdvanceReason` at `up:internal/monitors/consensus_monitor.go:601-645`.
- Ours: `reason, _ := payload["reason"].(string)` at `ours:internal/monitors/round_advance_monitor.go:138`, requires `"timeout"`. Current builds emit `{"reason":{"Tc":{...}}}`, so timeout rounds are always zero.
- Change: lift `parseRoundAdvanceReason` verbatim, keep our `metrics.IncrementTimeoutRounds` sink. Note this monitor is also rewritten in 4.1; do 0.3 first as a two-line patch, then fold into the rewrite.

### 0.4 Consensus wrapper identity in `sender` or `source`

- Upstream: `d30125a` (v4.1.1). `up:internal/monitors/consensus_monitor.go:452-475`.
- Ours: `Source string \`json:"source"\`` only, at `ours:internal/monitors/consensus_monitor.go:311`.
- Change: add `Sender` field, resolve `identity := firstNonEmpty(Sender, Source)`. Skip upstream's `normalizeWireAddress` conflict rejection unless we want the validation half.

### 0.5 New action types `outcomeDeploy` and `trailingStop`

- Upstream: `cd9e6aa` (v4.0.7). Package `up:internal/actiontypes/actiontypes.go` (86 lines). `outcomeDeploy` is deployment, `trailingStop` is trading.
- Ours: inline switch at `ours:internal/replica/parser.go:182-200`.
- Change: either add the two cases inline, or adopt the 86-line package. Adopt the package: it becomes the single place Tier 7 updates when hl-node adds actions. Lift fixture `up:internal/monitors/testdata/testnet_new_action_types.constructed.jsonl`.

### 0.6 Validator-latency same-path rewrite

- Upstream: `302f0d6` (v4.0.6), `up:internal/monitors/validator_latency_monitor.go`.
- Ours: `filePos{path, pos}` at `ours:internal/monitors/validator_latency_monitor.go:31-35` resets on path change only.
- Change: also reset when file size is below the stored offset or inode changes.

---

## Tier 1: crash safety and hardening

### 1.1 Panic recovery per monitor goroutine

- Upstream: `c937bbf` (v3.0.0). `up:internal/exporter/safego.go` (about 40 lines) and `up:internal/monitors/safego.go` (47 lines).
- Ours: `grep -rn 'recover()' internal cmd` returns nothing. `Start` in `ours:internal/exporter/exporter.go:44-128` launches about 18 bare `go` statements.
- Change: one shared package `internal/safego` (`Go(name, fn)`) used by exporter, monitors and peermon instead of upstream's two per-package files. Wrap every launch in `exporter.go` and every inner goroutine in monitors and peermon. Add `hl_exporter_monitor_panics_total{monitor}` counter to `instruments.go`.

```go
// internal/exporter/safego.go
func runMonitor(ctx context.Context, name string, errCh chan<- error, fn func(context.Context) error)
// internal/monitors/safego.go
func SafeGo(name string, fn func())
```

Highest value per line in this plan. Land it first; it makes every later change non-fatal. Test: a unit test on the wrapper with a function that panics, asserting the counter increments and the test process survives. The wrapper logs the panic directly instead of feeding the per-monitor error channel, whose consumer only logs. No test-only monitor or build tag.

### 1.2 Contract resolver send on closed channel

Dropped. `internal/contracts/` was removed in `447a838` (Unreleased), taking the bug with it.

### 1.3 Public IP lookup blocks startup

- Upstream: `2cb58cd`. `up:internal/metrics/identity.go:17-55`: read `$NODE_HOME/last_known_public_ip.json` first, ipify with 5 s timeout as fallback, failure only logs.
- Ours: bare `http.Get("https://api.ipify.org")` in `getPublicIP` at `ours:internal/metrics/identity.go:10`, no timeout.
- Change: port the file body.

### 1.4 Fatal on metrics listener failure

- Upstream: `28660ea` (v3.1.0).
- Ours: `ListenAndServe` error pushed to an error channel at `ours:internal/exporter/exporter.go:118` and logged. A bound port leaves a metric-less process running.
- Change: exit non-zero on listener error.

### 1.5 Unbounded consensus maps

- Upstream: `c937bbf`. 1 h last-seen TTL on a 10 min housekeeping ticker; `validatorCache` to LRU.
- Ours: `qcSignatures`, `tcVotes`, `validatorCache` plain maps at `ours:internal/monitors/consensus_monitor.go:38-40`, grown at lines 443, 486, 518, never pruned.
- Change: add `lastSeen map[string]time.Time`, prune on a 10 min ticker; use `internal/cache` LRU for `validatorCache`.

### 1.6 EVM gas-limit warning per block

- Upstream: `c937bbf`. Adds a `small` bucket (3M) and silences `other`.
- Ours: `logger.WarningComponent("evm", "Unexpected gas limit: %d", ...)` at `ours:internal/monitors/evm_monitor.go:303`.
- Change: downgrade to debug, add the bucket.

### 1.7 `--contract-metrics-limit` does not cap series

Dropped. The flag and `hl_evm_contract_tx_total` were removed in `447a838` together with `internal/contracts/`.

### 1.8 Peer registry load race

- Ours only. `peerMon.Start` (called at `ours:internal/exporter/exporter.go:97` area) runs `Load()` asynchronously (`ours:internal/peermon/monitor.go:71`) while gossip producers are already calling `Register`. `Load` at `ours:internal/peermon/peers.go:252` writes `ps.peers[p.IP] = &p` unconditionally, clobbering fresher entries, with no TTL or IP validation on the load path.
- Change: call `Load()` synchronously before producers start; apply the same validation `Register` does at `peers.go:67`; skip entries past TTL.

---

## Tier 2: performance

### 2.1 Recursive directory walk in hot loops

- Upstream: `f38ddb6` (v3.1.0). Replaces `GetLatestFile` with `up:internal/utils/latest_files.go` (185 lines, non-recursive `os.ReadDir` resolvers with lexicographic date and numeric hour sort) and `up:internal/monitors/stream.go` (451 lines, tailer with 2 to 5 s rescan gating). A smaller self-contained resolver is `latestHourlyFile` at `up:internal/monitors/visor_monitor.go:295-328`.
- Ours: `GetLatestFile` at `ours:internal/utils/utils.go:10` is `filepath.Walk` over the whole tree. Call sites: `block_monitor.go:93,265`, `consensus_monitor.go:537,694`, `validator_status_monitor.go:72,235,315`, `replica_monitor.go:93`, `evm_monitor.go:110`, `validator_ip_monitor.go:106`, `gossip_monitor.go:318,321`, `proposal_monitor.go:50`, `round_advance_monitor.go:49`. Several sit inside 10 ms EOF loops.
- Change (cheap 80% variant, one file): rewrite `GetLatestFile` as a two-level `ReadDir` resolver (`<date>/<hour>` layout) and add a per-caller rescan gate (2 s in EOF loops, none for slow pollers; see Decisions).
- Done as `utils.LatestFile`, a depth-agnostic greatest-name descent (replica_cmds has three levels), plus `utils.LatestFileCache` for the seven EOF-loop call sites. `getLatestHourlyFile` deleted.

```go
// internal/utils/utils.go
func LatestHourlyFile(root string) (string, error)          // ReadDir date dirs desc, ReadDir hour files numeric desc
type LatestFileCache struct { root string; path string; next time.Time; every time.Duration }
func (c *LatestFileCache) Get() (string, error)              // re-resolves at most once per `every`
```

Also replace `getLatestHourlyFile` in `ours:internal/monitors/gossip_monitor.go:314`, which formats today's date with local time against UTC-named directories and falls back to a full walk when the directory is missing.

### 2.2 Forced GC and stop-the-world MemStats on scrape

- Upstream: `2cb58cd`, `f38ddb6`. Cached MemStats snapshot at `up:internal/metrics/memory.go:72-95`.
- Ours: `runtime.GC()` in the 30 s cleanup ticker at `ours:internal/metrics/types.go:129`; `runtime.ReadMemStats` at `ours:internal/metrics/memory.go:72,102` on the scrape path.
- Change: delete the GC call; refresh a MemStats snapshot every 30 s and serve gauges from it.

### 2.3 Labeled-series sweep

- Upstream: `2cb58cd` removed the sweep entirely.
- Ours: sweep at `ours:internal/metrics/types.go:141-176` caps every labeled family at 200 by LRU. Above 200 validators it drops stake, jailed and latency series every 30 s. Tests pinning this: `ours:internal/metrics/cleanup_test.go:53,101`.
- Change: delete the sweep and its tests. Prerequisite: 3.4 (validator reconciliation). See Decisions for why nothing else gates it.

### 2.4 Consensus per-line lock churn

- Upstream: `f38ddb6`. Batch metric updates once per EOF pause; gate QC participation recalculation to once per 2 s.
- Ours: `statsMutex` taken twice per line plus a global metrics lock per setter, `ours:internal/monitors/consensus_monitor.go:240-253`.
- Change: accumulate per-batch deltas, flush at EOF. Medium effort.

---

## Tier 3: existing metric correctness

### 3.1 `hl_core_operations_total` inflated 2 to 6x

- Upstream: `2dc5aa9`. Real array decode in `up:internal/replica/parser.go`.
- Ours: `countJSONArrayElements` at `ours:internal/replica/parser.go:208-251` counts commas at depth 1 but never increments depth on `{`, so every comma inside an order object counts as an element.
- Change: replace with `json.Decoder` token walk (`Token()` for `[`, then `More()` loop with `Decode(&json.RawMessage{})`). About 40 lines. Add a test with one order containing six fields, expect 1.

### 3.2 Proposer moniker stub

- Upstream: `2cb58cd`.
- Ours: `GetValidatorName` at `ours:internal/metrics/getters.go:23-27` returns `""` unconditionally, so `hl_consensus_proposer_count_total` never gets a `name` label.
- Change: look up `validatorInfoCache` (populated by `validator_api_monitor.go`).

### 3.3 Torn trailing lines in six monitors

- Upstream: `2cb58cd`, `c792055`, `up:internal/monitors/stream.go`.
- Ours: `readCommittedLines` at `ours:internal/monitors/log_tail.go:14` handles partials correctly, but `consensus_monitor.go:226-256`, `block_monitor.go`, `evm_monitor.go`, `replica_monitor.go`, `proposal_monitor.go`, `round_advance_monitor.go` bypass it with raw `ReadString('\n')` and discard the partial on EOF while the offset has advanced. One lost line per file boundary.
- Change: retrofit `readCommittedLines` into all six. No upstream code needed.

### 3.4 Per-validator gauges never reconciled

- Upstream: `d9f74f4`. Validators leaving the set are removed instead of freezing.
- Ours: `Remove*` helpers in `ours:internal/metrics/setters.go` exist only for peer and connectivity series (around lines 895, 1210-1436). Nothing removes stake, jailed, active, latency.
- Change: `validator_api_monitor.go` and `validator_latency_monitor.go` compute `previous - current` address sets after each snapshot and call new `RemoveValidatorSeries(addr)`.

### 3.5 `hl_consensus_vote_time_diff_seconds` semantics

- Upstream: `2cb58cd`. Emits age of last observed vote at scrape time; trims after 24 h silence.
- Ours: stores exporter parse lag per vote at `ours:internal/monitors/consensus_monitor.go:382`, setter at `setters.go:722-745`. Sits near zero forever.
- Change: keep `lastVote map[addr]time.Time`, register an observable gauge computing `now - lastVote`.

### 3.6 Validator-latency date file uses local time

- Upstream: `2cb58cd`, `d9f74f4`.
- Ours: `time.Now().Format("20060102")` at `ours:internal/monitors/validator_latency_monitor.go:120,211`. Our 2.2.0 changelog fix was the offset reset, not the timezone.
- Change: `time.Now().UTC()`. Two lines.

### 3.7 `hl_software_up_to_date` wrong comparison and binary download

- Upstream: `2cb58cd` (compare local hl-visor to published hl-visor, ETag conditional requests, no curl), `10cc834` (v4.1.0, behind `--binary-metrics`, off by default). `up:internal/monitors/update_checker.go:21,113-165`, `up:internal/monitors/version_monitor.go`.
- Ours: downloads `hl-visor` to a temp file every cycle at `ours:internal/monitors/update_checker.go:62-68`; compares against local hl-node.
- Change: port both behind `--binary-metrics`, default off (Decisions).

### 3.8 Heartbeat ack correlation

- Upstream: `3be57c9`, `0ad0e88`, `88b74b3` (v4.1.1), heartbeat section of `up:internal/monitors/consensus_monitor.go`. Join acks to a unique outgoing heartbeat by random ID and round; preserve full vs abbreviated identities; reject ambiguous joins.
- Ours: `map[float64]heartbeatInfo` keyed on random ID only at `ours:internal/monitors/consensus_monitor.go:29-30,55,86,339`; `RecordHeartbeatAckDelay` at `setters.go:841` ignores both validator arguments.
- Change: port the idea (key on `{randomID, round}`, drop on ambiguity), not the diff. The data structures have diverged too far for line-level porting. Add `hl_consensus_heartbeat_ack_ambiguous_total`.
- Test: fixture with two outgoing heartbeats sharing a random ID in different rounds and one ack each; expect two distinct delays keyed to the right validator. A second fixture with an ack matching both expects zero delays and the ambiguity counter at 1.

---

## Tier 4: peer monitoring

Keep `internal/peermon` and our metric names. Borrow upstream mechanics for tailing, rollover and selection stability.

### 4.1 `round_advance_monitor.go` rewrite

- Resolved in phase 1: the file was never launched and read the wrong stream. Round advance events are parsed by the consensus monitor's existing tailer (0.3); the standalone monitor is deleted.

### 4.2 Startup replay of the current hour

- Ours: `processGossipFile(filePath, 0)` at `ours:internal/monitors/gossip_monitor.go:79` and `processFile(filePath, 0)` at `ours:internal/monitors/gossip_connections_monitor.go:56` replay up to an hour of `_total` increments on every restart. The tcp_traffic readers already guard this with a seeding flag.
- Change: seek to EOF on first open, one line each.
- Done differently: replay the current hour with a `seeding` flag that suppresses counters but keeps peer registration and child peer state, matching the tcp_traffic readers. Plain EOF seek would leave child peer gauges empty until the next snapshot.

### 4.3 Hour rollover drops the old file's tail

- Upstream: two stable EOFs on the old inode before switching, `up:internal/monitors/stream.go:386-408`.
- Ours: jump to the new file and reset offset at `gossip_monitor.go:102-106`, `gossip_connections_monitor.go:79-83`, `outbound_peers_monitor.go:84-88`, `parent_peer_monitor.go:78-82`. Up to 30 s of the previous hour is lost.
- Change: shared helper in `log_tail.go` that drains the previous path once more before switching. Done as `tailState.poll`.

```go
type hourlyTail struct { path string; offset int64 }
func (t *hourlyTail) advance(latest string, fn func([]byte)) error // drains t.path to EOF, then switches to latest at 0
```

### 4.4 Gossip event allowlist and parse-error counters

- Upstream: `up:internal/monitors/gossip_connections_monitor.go:17-33` (15-tag allowlist), `198-301` (per-tag payload validation). Schema fixes in `d078382`, `4f075a8`, `caaaf96`, `69f73bd` (v4.0.4 to v4.0.6): `closing gossip stream because no quorum yet` and `dropping connection after sending abci state` changed shape; `sending evm kvs` and `marking node_ip as verified` are object-only.
- Ours: switches on two event names positionally at `ours:internal/monitors/gossip_connections_monitor.go:129-151`; unknown shapes are silently ignored.
- Change: add the allowlist with an `other` bucket, `hl_p2p_gossip_unknown_events_total`, `hl_p2p_gossip_parse_errors_total{stage}`.

### 4.5 Parent selection stability

- Upstream: `28660ea` (v3.1.0), `up:internal/monitors/parent_peer_monitor.go:89-129`. Per-IP aggregation across ports, EWMA alpha 0.3, 1.2x switch hysteresis, 90 s stale clear, deterministic tie-break, share and challenger ratios.
- Ours: single-sample largest inbound flow at `ours:internal/monitors/parent_peer_monitor.go:97-127,164-172`, ambiguity warning every 30 s at line 118.
- Change: port the selection function, keep `hl_node_parent_peer_*` names, add `hl_node_parent_peer_share_ratio` and `hl_node_parent_peer_challenger_ratio` as evidence.

Context: `up:HL_NODE_METRICS_AUDIT.md:323-347,409-421` argues `tcp_traffic` In/Out are byte directions, not connection roles, so "parent" is an inference. Upstream renamed to `hl_p2p_dominant_inbound_*` and dropped lag and degraded attribution as non-causal. Keep ours, but document the inference in `docs/metrics-overview.md` next to `hl_node_parent_peer_block_lag_seconds` (`ours:internal/monitors/parent_quality.go:85-101`).

### 4.6 Shared `tcp_traffic` snapshot and strict row parser

- Upstream: `up:internal/monitors/tcp_traffic_monitor.go:77-102` (single parse, shared snapshot), `385-479` (arity checks, `netip.ParseAddr().Unmap()`, NaN/Inf/negative rejection, whole-record fail), `288-319` (two-fresh-positive admission gate).
- Ours: `outbound_peers_monitor.go:109` and `parent_peer_monitor.go:97` each parse the same file.
- Change: one reader producing a snapshot consumed by both, plus the admission gate before `peerMon.Register`.

### 4.7 Small fixes

- `currentPeers` accumulates across all `child_peers status` lines in one poll at `ours:internal/monitors/gossip_monitor.go:130,168`; reset per line. Done in phase 2.
- Per-IP gossip metrics publish regardless of `--peer-latency` (`ours:internal/exporter/exporter.go:105-108`); gate them.
- `ours:internal/peermon/prober.go:11-15` scans 443, 80, 3001, 3002 then 4000-4010, up to 15 dials per peer per cycle. Prefer the observed `tcp_traffic` port first.
- Source-health envelope for the four peer monitors: `*_source_up`, `*_sample_age_seconds`, `*_errors_total{stage}`. Today we cannot distinguish "no peers" from "source unreadable".

---

## Tier 5: new monitors

All are absent from our tree. Ranked by operator value. Each needs its metrics redeclared in `ours:internal/metrics/instruments.go` under the upstream name; strip upstream's `RegisterSource`/`source_state` calls or stub them. Flag and gating policy per Decisions.

| Rank | Monitor | Upstream file (LOC) | Reads | Key metrics | Notes |
|---|---|---|---|---|---|
| 1 | process | `up:internal/monitors/process_monitor.go` (223) + `process_procfs.go` (324) + `_linux.go`/`_other.go` | `/proc/<pid>/{stat,status,fd,comm}` for hl-node, hl-visor | `hl_node_process_up`, cpu, rss, fds | Linux only, no infra deps |
| 2 | child_stderr | `child_stderr_monitor.go` (306) | `data/visor_child_stderr/` | `hl_node_child_crashes{reason}`, `hl_node_child_last_crash_seconds` | `app_hash_mismatch`, `config_error` taxonomy |
| 3 | visor + node_state | `visor_monitor.go` (378), `node_state_monitor.go` (165) | `hyperliquid_data/visor_abci_state.json`, `freeze_abci_height`, `evm_db_hub_*/cp_checkpoint_height` | `hl_visor_height`, hardfork version, freeze height, fast/slow gap | Coupled pair |
| 4 | disk | `disk_monitor.go` (295) + statfs/allocation stubs | statfs + allowlisted subdir walk every 120 s | free/used/allocated bytes | Tune subdir allowlist |
| 5 | operator_config | `operator_config_monitor.go` (265) | `file_mod_time_tracker/`, `heartbeat_jailing_config.json` | `hl_node_jailing_threshold_seconds`, `hl_node_jailing_dry_run`, config ages | Validator only. Headroom recipe in `up:CHANGELOG.md` v3.1.0 |
| 6 | crit_msg + crit_locations | `crit_msg_monitor.go` (263), `critical_generation.go` (84), `crit_locations_monitor.go` (287) | `data/crit_msg_stats/{hl-node,hl-visor}/<date>` | `hl_node_bugs_total`, `hl_node_crits_total`, top locations | Three-file coupling |
| 7 | snapshot_status | `snapshot_status_monitor.go` (172) | `data/periodic_abci_state_statuses/` | snapshot age, last height | |
| 8 | accumulator_consensus | `accumulator_consensus_monitor.go` (361) | per-bucket accumulator files | `hl_consensus_committed_*`, `hl_consensus_dropped_txs` | Validator only. Sum `delta`, not `n` (v3.1.0 fix) |
| 9 | info_probe | `info_probe_monitor.go` (239) | POST `{"type":"meta"}` to `:3001/info` | `hl_info_endpoint_up`, latency | Behind `--probe-info-endpoint` |
| 10 | log_lines, public_ip, optional_streams, rate_limited, replay, replica_runs | 131 to 187 each | see `up:CHANGELOG.md` v3.0.0 table | | Trivial, self-contained |

Also lift fixtures: `up:internal/evm/testdata/testnet_hl-node_2026-08-07_current.jsonl` for our EVM parser tests.

Deliver 1 to 5 first. 6 to 10 as time allows.

---

## Tier 6: infra and self-observability

### 6.1 CI

- Upstream: `up:.github/workflows/ci.yaml`. Runs `go mod tidy -diff`, `go mod verify`, `go test` plain and `-race`, `go vet`, staticcheck, `go generate` drift check, govulncheck, actionlint, promtool check and test rules, amd64 and arm64 build matrix, SHA-pinned actions, `GOTOOLCHAIN: local`.
- Ours: `ours:.github/workflows/ci-tests.yml` runs golangci-lint, a heavy `-race -v` test and a 16x flake run. No govulncheck, vet, arm64, or pinning.
- Change: add govulncheck, `go mod tidy -diff`, arm64 build, SHA pins. Copy `up:.github/dependabot.yml` verbatim.

### 6.2 Release

- Upstream: `up:.github/workflows/release.yaml`, `4dc9637`. Stamps `-ldflags -X ...metrics.BuildVersion/BuildCommit` from tag and SHA, publishes amd64 and arm64 binaries, `.tar.gz`, `SHA256SUMS`.
- Ours: `ours:.github/workflows/release.yml`, manual dispatch with a VERSION-file gate. We already stamp via Makefile ldflags.
- Change: add arm64 and checksums.

### 6.3 Exporter self-observability

- Upstream: `up:internal/metrics/prometheus.go` (107 lines), `c7a8ca4`, `28660ea`. `hl_exporter_build_info{version,commit,go_version}`, `hl_exporter_config_info{chain}`, `/livez`, `/readyz`, `promhttp.HandlerFor` with `MaxRequestsInFlight`, `ReadHeaderTimeout`/`WriteTimeout`/`IdleTimeout`, `--pprof` on the metrics listener.
- Ours: `ours:internal/metrics/prometheus.go` (50 lines): `/metrics` with `TimeoutHandler`, static `/health`. Version, commit and build time exist in `cmd/hyperliquid-exporter/main.go` but are never exported.
- Change: export build info, add server timeouts and in-flight bound, add `--pprof`, add `/livez` and `/readyz` backed by the Tier 1.1 monitor registry.

### 6.4 Alerts

- Upstream: `up:alerts/*.rules.yml` (6 files) with promtool tests in `up:alerts/tests/` and `up:alerts/rules_test.go`.
- Only `HyperliquidCoreHeightStalled` and `HyperliquidCoreHeightSlow` (in `up:alerts/hyperliquid-core.rules.yml`) work against our names today. Process-down rules become usable after Tier 5.1.
- Change: create `alerts/` with those two rules plus a promtool test step in CI. Add rules as monitors land.

### 6.5 Generated metric docs

- Upstream: `up:internal/metrics/inventory_generate.go` (go:generate), `up:internal/metrics/cmd/metricdocs/main.go` (137), `up:internal/metrics/metricinventory/inventory.go` (395, AST-parses instrument declarations), producing `up:docs/metrics.md`. Drift tests: `inventory_test.go`, `promlint_test.go`.
- Ours: hand-written `ours:docs/metrics-overview.md`.
- Change: port the generator, adapted to our OTel declaration style in `instruments.go`. Add the drift test to CI. This is also a Tier 7 input.

### 6.6 Dependencies

Nothing to take. We are on Go 1.26.7, upstream on 1.25.13. Upstream is one point release ahead on `prometheus/client_golang` and `go.opentelemetry.io/otel`; dependabot (6.1) handles that.

---

## Tier 7: routines and docs

**Goal:** keep up with upstream and the hl-node log surface without repeating this review from scratch.

### 7.1 `docs/routines/upstream-sync.md`

Monthly, or on any upstream tag:

1. `git fetch upstream --tags` (note: our v2.x tags collide with upstream's; fetch tags with `--no-tags` and list with `git ls-remote --tags upstream` instead of clobbering).
2. `git diff <last-reviewed-upstream-sha>..upstream/main --stat -- internal/monitors internal/replica internal/evm internal/actiontypes internal/metrics` and read `up:CHANGELOG.md` sections newer than the recorded release.
3. Classify each upstream change as schema-compat, correctness, perf, new-monitor, infra, or skip, using the tier headings above.
4. Record the reviewed SHA and release in the routine doc's log table, and file items into `docs/plans/` or the changelog Unreleased section.

Seed the log table with `b150dcc` (v4.1.1, reviewed 2026-09-10).

### 7.2 `docs/routines/hl-node-schema-watch.md`

The hl-node log surface changes without notice (`current_stakes`, round-advance reason, gossip payloads, new action types, all within six months). Routine on each hl-node release:

1. Pull one hour of each consumed stream from a mainnet and a testnet node: `status/hourly`, `consensus/hourly`, `replica_cmds`, `gossip_rpc`, `gossip_connections`, `tcp_traffic`, `evm_block_and_receipts`, `validator_latency`. Store as gitignored fixtures under `internal/monitors/testdata/live/`.
2. Run parsers against them with a test that fails on any unknown event, action type, or decode error (Tier 4.4 counters and Tier 0.5 `actiontypes` give the hooks). The same test runs in CI against the committed trimmed samples in `testdata/schema/`; refresh those from the live pull when a shape changes.
3. Watch upstream's `HL_NODE_METRICS_AUDIT.md` and `CHANGELOG.md` "accept current ... shape" commits as an early signal; they track hl-node changes closely.
4. Add a `hl_exporter_parse_errors_total{stream,stage}` counter (Tier 4.7 envelope generalized) and an alert on it, so drift is visible in production, not only in review.

### 7.3 Docs hygiene

- Document every metric ported under an upstream name in the metrics doc with its upstream origin. `BREAKING_CHANGES.md` is only for renames of existing metrics, of which this plan has none.
- Once 6.5 lands, retire the hand-written `docs/metrics-overview.md` in favor of the generated file.
- Note in `README.md` that the fork is intentionally divergent and point to this plan and the routines.

---

## Affected Files

Tier 0 to 3 (modify): `internal/monitors/validator_status_monitor.go`, `round_advance_monitor.go`, `consensus_monitor.go`, `validator_latency_monitor.go`, `evm_monitor.go`, `update_checker.go`, `version_monitor.go`, `validator_api_monitor.go`, `block_monitor.go`, `replica_monitor.go`, `proposal_monitor.go`, `internal/replica/parser.go`, `internal/metrics/identity.go`, `getters.go`, `setters.go`, `types.go`, `memory.go`, `instruments.go`, `cleanup_test.go`, `internal/utils/utils.go`, `internal/exporter/exporter.go`, `internal/peermon/monitor.go`, `peers.go`.
Tier 0 to 3 (new): `internal/exporter/safego.go`, `internal/monitors/safego.go`, `internal/actiontypes/actiontypes.go`, `internal/monitors/testdata/testnet_new_action_types.constructed.jsonl`.
Tier 4 (modify): `internal/monitors/log_tail.go`, `gossip_monitor.go`, `gossip_connections_monitor.go`, `outbound_peers_monitor.go`, `parent_peer_monitor.go`, `parent_quality.go`, `internal/peermon/prober.go`, `internal/exporter/exporter.go`.
Tier 5 (new): one file per monitor under `internal/monitors/`, instrument declarations in `internal/metrics/instruments.go`, wiring in `internal/exporter/exporter.go`, flags in `internal/config/config.go` and `cmd/hyperliquid-exporter/main.go`.
Tier 6: `.github/workflows/ci-tests.yml`, `release.yml`, `.github/dependabot.yml` (new), `internal/metrics/prometheus.go`, `alerts/` (new), `internal/metrics/cmd/metricdocs/` (new), `docs/metrics.md` (generated).
Tier 7 (new): `docs/routines/upstream-sync.md`, `docs/routines/hl-node-schema-watch.md`, `internal/monitors/testdata/schema/`; modify `README.md`, `docs/metrics-overview.md`.

## Implementation Phases

Each phase leaves the tree green and is one release. Version numbers are assigned at release prep; every phase is a minor bump under SemVer since each adds metrics or flags. Nothing in this plan has shipped; the Unreleased changelog section currently holds only the contracts removal.

Phases intentionally cut across tiers. Tiers rank items by severity and category; phases group them by what can land together safely. Three rules drive the grouping: dependency order (the series sweep in 2.3 is only removed after validator reconciliation in 3.4 exists), shared code surface (the resolver in 2.1 and the tailing fixes in 4.1 to 4.3 touch the same files and ship together), and release size small enough to verify on a live node. Safego (1.1) leads phase 1 because it makes every later change non-fatal.

1. **Safety and schema (done, `9bd985e`):** 1.1, Tier 0 complete, 1.3, 1.4, 1.6, 3.6. Verified on a mainnet non-validator node: clean startup, no panics. Validator-side checks (validator count, timeout rounds, signer mapping) still need a validator node.
2. **Perf and tailing (done, `1696ea7`):** 2.1, 2.2, 4.1, 4.2, 4.3, 4.7 child reset. No pre-change CPU baseline was recorded. Post-change on a mainnet non-validator node with EVM, replica and peer latency enabled: about 13% CPU, 34 MB RSS, 28 goroutines.
3. **Metric correctness:** 3.1, 3.2, 3.5, 1.5, 3.4, then 2.3. Changelog must call out that operation counts drop 2 to 6x.
4. **Peer quality:** 4.4, 4.5, 4.6, rest of 4.7, 1.8.
5. **New monitors:** Tier 5 ranks 1 to 5, each behind its own flag per Decisions. Charm gains the new flags.
6. **Infra and routines:** Tier 6, Tier 7. 3.7, 3.8, 2.4 and Tier 5 ranks 6 to 10 as capacity allows.

## Handoff

Read this before starting phase 3. Everything above the phase list is still the spec; this section is what an implementer needs that the spec does not say.

**Where things are.** Branch `upstream-port-sep26`, two commits on top of `447a838`. `CHANGELOG.md` Unreleased holds entries for both phases plus the earlier contracts removal; keep adding there. `docs/metrics-overview.md` is hand-maintained until 6.5 lands; update it for every metric or label change.

**Conventions established in phases 1 and 2.**

- Every goroutine launch goes through `safego.Go(component, fn)`. Package `metrics` cannot use it (import cycle); its cleanup ticker stays bare.
- Latest-file lookup is `utils.LatestFile(root)`; tight EOF loops use `utils.NewLatestFileCache(root, latestFileRescan)`. Do not reintroduce `filepath.Walk`.
- Polling monitors keep their position in a `tailState` and read through `tailState.poll`; startup seeding passes `seeding=true` to suppress counters. New tailers follow the same shape.
- Action types pass through `actiontypes.Normalize`; categories come from `actiontypes.Category`.
- Round advance events are handled in `consensus_monitor.processConsensusLine` (`round_advance.go`). There is no standalone round advance monitor.
- Tests use `initTestMetrics(t)` from `helpers_test.go`; fixtures are inline strings shaped like real log lines. Every schema change gets a case in the old and the new shape.
- Each phase ends with `make lint`, `make test RACE=1`, a low-effort multi-engine review, fixes, then one commit with a single-line conventional message. Do not mention the plan or phase in the message.

**Phase 3 pointers (metric correctness).** Order: 3.1, 3.2, 3.5, 1.5, 3.4, then 2.3.

- 3.1: `countJSONArrayElements` in `internal/replica/parser.go` is the bug; `TestCountNestedObjects` in `parser_test.go` currently pins the wrong behavior and must change. Changelog must say operation counts drop 2 to 6x.
- 3.2: `GetValidatorName` in `internal/metrics/getters.go` returns "". `validatorInfoCache` is populated by `validator_api_monitor.go`; check what key it uses before wiring.
- 3.5: the vote-age gauge needs an observable gauge with a callback; see `internal/metrics/callbacks.go` for the existing pattern.
- 1.5: consensus maps `qcSignatures`, `tcVotes`, `validatorCache` in `consensus_monitor.go`; `internal/cache` has the LRU.
- 3.4: only per-validator gauges are unbounded now (Decisions). Add `RemoveValidatorSeries` next to the existing `Remove*` helpers in `setters.go` and call it from `validator_api_monitor.go` and `validator_latency_monitor.go` on set difference.
- 2.3: delete the sweep in `internal/metrics/types.go` and `cleanup_test.go` only after 3.4 is in.

**Phase 4 note.** 4.1 is done. 4.7's child reset is done. The rest of Tier 4 is untouched.

**Open verification debt.** Validator-node checks from phase 1 acceptance, and a CPU before/after with the pre-phase-2 build if a baseline is still wanted.

## Testing Decisions

- Seam: monitor parse functions driven with real or constructed log lines, asserting on the metric setter calls or returned structs. Prior art: `internal/monitors/*_test.go` and `internal/replica/parser_test.go`.
- Every Tier 0 item gets a fixture line in both old and new shape.
- Tailing changes (4.1 to 4.3) get an end-to-end test that writes an hour file, rolls to the next, and asserts no line is lost or duplicated. Upstream's `up:internal/monitors/stream_test.go` (662 lines) is a source of cases.
- Do not add tests that pin the 200-series sweep; delete the existing ones in 2.3.

## Acceptance Criteria

- On a current mainnet node: signer to validator mapping populated, `hl_timeout_rounds_total` increments, proposer counters carry `name`, one order counts as one operation.
- A deliberate `panic` injected in any monitor increments `hl_exporter_monitor_panics_total` and does not exit the process (unit test on `runMonitor`).
- Heartbeat fixtures in 3.8 pass: distinct delays for same-ID different-round heartbeats, ambiguous joins dropped and counted.
- The schema-watch test in 7.2 runs green in CI against committed samples.
- Exporter CPU on a live validator under 20% (upstream reported 195% before and single digits after their fix).
- Restart does not increment `hl_p2p_*_total` counters by the replayed hour.
- `make lint`, `make test RACE=1`, govulncheck clean.
- `docs/routines/` exists with both routines and a seeded review log.

## Open Questions

- Should `hl_node_parent_peer_*` gain an explicit `inferred` note in HELP text given upstream's causality argument (4.5)? Leaning yes; decide when 4.5 lands.

## Follow-ups

Out of scope here, tracked so they are not lost:

- Survey our other Go repos for CI improvements made since this repo's workflows were written: `bcm-probe`, `dwellir-admin-dashboard` (has `schema-drift.yml`), `hyperliquid-archiver`, `hyperliquid-index`, `hyperliquid-l1-gateway`, `hyperliquid-rest-server`, `iris`. Diff each `.github/workflows/` against ours and pick up shared steps. Separate plan; touches Tier 6.1 only.
