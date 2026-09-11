# Upstream Port: validaoxyz v3.0.0 to v4.1.1

**Date:** 2026-09-11
**Commit:** `8d1e2eb` on `upstream-port-sep26` (base `447a838`, upstream/main `b150dcc`)
**Scope:** how six upstream releases were ported by hand into this divergent fork over 2026-09-10 and 2026-09-11, what landed, what was skipped, how it was verified, and what a future port should copy or avoid. Does not cover the release itself; everything here is still in the changelog's Unreleased section.
**Status:** code complete and verified on a mainnet non-validator node. Validator-side verification and a second-engine review of the last five commits are still owed.
**Related:** `docs/operations/upstream-sync.md`, `docs/operations/hl-node-schema-watch.md`, `docs/metrics-overview.md`, `docs/TODO.md`, `CHANGELOG.md` (Unreleased)

## TL;DR

The fork descends from upstream v2.0.0 and shares no merge base with it, so nothing cherry-picks. Between March and September 2026 upstream shipped v3.0.0, v3.1.0, v4.0.6, v4.0.7, v4.1.0 and v4.1.1, about 41k added lines, including three hl-node log schema changes that had silently zeroed or frozen several of our metrics. Everything was ported at content level, item by item, in six phases and 12 code commits: 104 files, +8128/-2421 lines, 77 new test functions. About six hours of active work spread over 16 hours of wall clock. The plan doc that drove it was written first from a parallel survey of upstream, then used as the handoff medium between sessions. Two bugs that no review engine caught were found in the one hour of live-node verification at the end.

## 1. Context and Motivation

Three things forced the port.

- **hl-node schema drift.** `current_stakes` had become `{"validator_to_stake": [...]}`, the round-advance reason had become a tagged object `{"Tc": {...}}`, the consensus wrapper identity had moved from `source` to `sender`, and two action types had appeared. Upstream parsed all of these; we parsed none. On current nodes the validator set and signer mappings came up empty and timeout rounds were always zero.
- **Known correctness bugs shared with upstream's old tree.** Operation counts inflated 2 to 6x by a comma-counting array parser, a proposer moniker lookup stubbed to `""`, vote-age semantics that measured exporter parse lag, and an update checker comparing hl-visor against hl-node.
- **Robustness.** No panic recovery in about 18 monitor goroutines, a recursive `filepath.Walk` in 10 ms EOF loops (upstream measured about 195% CPU from the same code), and unbounded consensus maps.

Constraints that shaped every decision: keep our metric names (upstream renamed about 35 in v4.0.6; we have dashboards and alerts on ours), keep `internal/peermon`, keep our OTel-first instrument declarations, and do not adopt upstream's source-state layer wholesale.

## 2. Origin State

At `447a838` (2026-09-10 19:02, the commit that removed the dead Hyperscan contract resolver) the tree had:

- Bare `go` statements for every monitor launch in `internal/exporter/exporter.go`; `grep -rn 'recover()'` returned nothing.
- `utils.GetLatestFile` as a full-tree `filepath.Walk`, called from 14 sites, seven of them inside tight EOF loops.
- Six tailers using raw `ReadString('\n')` that discarded a torn trailing line at EOF, losing one record per hour-file boundary.
- A standalone `round_advance_monitor.go` that read the status log instead of the consensus log and, it turned out, was never started from the exporter at all.
- A 30 s labeled-series sweep capping every family at 200 entries, which above 200 validators dropped stake, jailed and latency series every cycle.
- 192 test functions across 25 test files.

## 3. Final State

At `8d1e2eb`:

- Every goroutine launch goes through `safego.Go` (`internal/safego/safego.go:20`), with `hl_exporter_monitor_panics_total{monitor}`.
- Latest-file lookup is `utils.LatestFile` (`internal/utils/utils.go:20`), a depth-agnostic greatest-name descent over `os.ReadDir`, with `LatestFileCache` gating re-resolution to once per 2 s in EOF loops.
- One `streamTailer` (`internal/monitors/log_tail.go:103`) serves the seven tight-loop streams; `tailState.poll` (`:78`) serves the polled gossip and tcp_traffic streams. Both keep torn lines and drain the old file at rollover.
- Every consumed source reports the health envelope `hl_exporter_source_up`, `hl_exporter_source_sample_age_seconds`, `hl_exporter_parse_errors_total{stream,stage}` and `hl_exporter_source_errors_total{stream,stage}`. `TestSchemaFixtures` (`internal/monitors/schema_test.go:95`) runs every parser over 11 committed fixture files.
- Seven new node-host monitors (process, child_stderr, visor, node_state, disk, operator_config, crit_msg) under upstream metric names, declared in `internal/metrics/node_instruments.go` (60 instruments), each behind a default-on flag. `--binary-metrics` (default off) gates the rewritten update checker.
- Parent selection is EWMA-smoothed with hysteresis; `tcp_traffic` is parsed once by `TCPTrafficMonitor` (`internal/monitors/tcp_traffic.go:172`) and fanned out to consumers; gossip events are matched against a 15-tag allowlist.
- Per-validator series reconcile via `dropMissing` (`internal/monitors/validator_api_monitor.go:125`) and `RemoveValidatorSeries`; the 200-series sweep is gone.
- Round advance parsing lives in `round_advance.go` inside the consensus monitor; the dead standalone monitor is deleted.
- CI runs govulncheck; the release workflow refuses non-`main` refs and smoke-tests `--version`.
- Two runbooks under `docs/operations/`, a `docs/TODO.md` tracker, a README section stating the fork is intentionally divergent.
- 269 test functions across 41 test files; `make lint` and `make test RACE=1` green.

## 4. Walkthrough

### 4.1 Survey and plan (2026-09-10 10:33 to 11:11)

Ten minutes of probing settled the method: `git merge-base main upstream/main` returned nothing, and `git fetch upstream --tags` was rejected because our v1.x and v2.x tags collide with upstream's. Conclusion: no cherry-picks, port by feature, read upstream via `git show upstream/main:<path>` pinned at `b150dcc`. Three parallel agents surveyed upstream's diff, changelog and audit doc into a scratch review, and the plan was written from that survey: eight tiers ranked by severity (schema breaks, crash safety, perf, correctness, peer monitoring, new monitors, infra, routines), then regrouped into six phases by dependency and shared code surface. The tier-versus-phase split needed an explanatory paragraph after the first read; write that up front next time.

### 4.2 Phase 1: safety and schema (`2b11c0c`, 19:51)

Panic recovery, the four schema fixes, `actiontypes` package, ipify timeout with `last_known_public_ip.json` first, fatal exit on a bound metrics port, UTC date for validator latency files. Discovery during the work: the round-advance events live in the consensus log as `["round advance", ...]`, and the standalone monitor was never launched. That retired a whole planned rewrite two phases early. Review caught unwrapped goroutines inside `peermon/prober.go`, the exact failure mode the phase existed to close.

### 4.3 Phase 2: perf and tailing (`1696ea7`, 23:28)

`LatestFile` and the cache, no forced GC, MemStats from a 30 s snapshot, drain-before-rollover for the four polled tailers, seeding with counters suppressed on restart. Two review legs independently found a real regression: the name sort fell back to lexicographic order for non-integer names, and `periodic_abci_states` holds `<height>.rmp`, so a stale snapshot would win at every digit boundary. Fixed with numeric-stem parsing.

### 4.4 Phases 3 to 5, unattended (`42b119e`, `28c558d`, `6911bde`, 00:39 to 01:28)

Run back to back in one session on an explicit go-ahead. Phase 3: JSON-decoder operation counting, proposer name from the validator info cache, vote age at scrape time, bounded signer tracking, validator series reconciliation, then the sweep deletion. Phase 4: gossip allowlist, EWMA parent selection, shared tcp_traffic snapshot with a strict row parser and two-sample admission gate, persisted peer set loaded synchronously and validated. Phase 5: six monitors from upstream, with `RegisterSource` and `MarkSource*` calls stripped and replaced by our envelope. The orientation agent for phase N+1 ran while phase N's reviews were still open, which is where most of the wall-clock saving came from.

### 4.5 Phase 6: infra and routines (`2e0bc49` through `2fabd92`, 09:15 to 10:36)

CI was benchmarked against sibling repos rather than upstream: govulncheck, read-only token, ref guard and smoke test were taken; arm64, dependabot, SHA pins, staticcheck, actionlint and `go mod tidy -diff` were rejected. Alert rules (managed centrally) and generated metric docs (too much machinery) were dropped outright. The torn-line tailer, which had no phase in the plan, was pulled in here because a parse-error counter is meaningless while torn lines count as parse errors. Then the three leftovers landed in the order the user chose: heartbeat ack join on `{randomID, round}` (`heartbeatKey`, `internal/monitors/consensus_monitor.go:36`), hl-visor update check behind `--binary-metrics`, crit_msg monitor. Listener timeouts and the in-flight cap closed the phase.

### 4.6 Triage and live verification (10:19 to 11:02)

Every remaining item was marked deferred or scratched. One hour on the mainnet non-validator produced the verification verdict in section 6 and the final fix commit `8d1e2eb`.

## 5. Design Choices

| Choice | Decision | Why |
|---|---|---|
| Metric names | Existing metrics keep ours; fresh ports use upstream v4 names verbatim | Nothing existing breaks; upstream alert rules apply to the new metrics unchanged |
| Flags | One per new monitor; local-only sources default on, network-touching default off | Operators can disable without a rebuild; no umbrella flag |
| Source health | One generic envelope keyed by `stream` instead of upstream's per-monitor `*_scan_up`, `*_walk_up`, `*_errors_total` | Fewer names, one alert; omitted names listed in `docs/metrics-overview.md` |
| Upstream infra | Borrow mechanics (safego, resolver, tailer semantics), not `stream.go`, `source_state.go`, `health.go` | About 1200 lines plus a convention every monitor must follow |
| CI | Sibling repos set the bar, not upstream | Avoid adopting steps because upstream runs them |
| Review threshold | Two engines agreeing on a low-severity finding promotes it to a fix | Single-engine low findings were declined with a stated reason |
| Commit style | One conventional single-line commit per phase, no phase or plan references | Messages describe content; the plan is transient |

Deviations from the original spec worth knowing: `hl_p2p_gossip_parse_errors_total` was never created (the generic counter covers it), runbooks went to `docs/operations/` rather than the planned `docs/routines/`, and the version monitor stayed default-on while only the update checker went behind `--binary-metrics`.

## 6. Verification

Only a mainnet non-validator was available. No validator was exercised.

**After phase 2:** about 13% CPU, 34 MB RSS, 28 goroutines, zero recovered panics. No pre-change CPU baseline existed, so the planned before/after comparison was never possible and was scratched.

**Final pass (2026-09-11 10:47 to 11:00), ssh plus PromQL:** pass with two defects.

| Check | Result |
|---|---|
| CPU / RSS / goroutines | about 15% / 36 MB / 34 |
| Scrape duration, listener timeouts | 18 ms, no failures |
| Source envelope | 17 streams reporting; the 3 zeros are validator-only streams |
| Restart replay | p2p counters +72 after vs +68 before, no hour replay |
| Proposer `name` label | populated |
| crit_msg counts | matched the on-disk stats file exactly |
| Parent share ratio | 0.99999 |

Both defects were pre-existing, not regressions, and neither was flagged by any of four review engines across six phases:

- hl-node's `tcp_traffic` lists `127.0.0.1` and the host's own IP with small positive rates, so the new admission gate admitted and probed the exporter's own host. `validPeerIP` only checked that the string parsed. Fixed to reject loopback, unspecified, multicast, link-local and any locally bound address; the predicate also runs on `Load()`, so stale `peers.json` entries self-clean.
- `hl_evm_last_high_gas_block_time` read the Go zero time (-62135596800) because hl-node writes zoneless timestamps and the RFC3339 parse failed silently. Fixed by routing through the shared `parseVisorTime`.

## 7. Process Observations

- **Per-phase recipe.** Orient (an Explore agent producing an ours-versus-upstream map with file:line), implement, snapshot the diff to `/tmp`, run four review legs in parallel (two Claude angles, one Codex, one opencode), apply verified findings, `make lint` and `make test RACE=1`, one commit, update plan and changelog. Phases took 13 to 26 minutes each except phase 6 at about 68.
- **Plan doc as handoff.** Six phases across five sessions with context cleared between them; every session resumed from the plan's handoff section, which held branch state and the established conventions. That section, not the tier spec, was the load-bearing part.
- **Stale review inputs.** Three review legs opened on a `/tmp/pN.diff` that no longer matched the working tree because editing continued after the snapshot. Hand reviewers a commit or frozen worktree.
- **Build-then-delete.** About 2.5 hours went into making the Hyperscan contract API configurable before the question "do we use this at all?" was asked. The answer was no, and two plan items plus six cross-references evaporated with the package.
- **Dead code hides in plans.** Two planned items (round advance rewrite, contract series cap) vanished once the code was actually read.
- **Codex was the quietest engine**, usually clean; its one substantive phase 6 finding (timing-based sleeps in tailer tests) was real and fixed with an `opened` hook. Claude legs and opencode produced most of the accepted findings.
- **Self-inflicted rework was small:** one test file overwritten and restored from HEAD, one opencode leg reading a wrong path, ordinary compile and test failures during iteration. No flaky tests survived the 16x shuffled run.

## 8. Follow-Ups and Known Debt

- **Validator verification** (open): validator count, timeout rounds, signer mapping, one order counts as one operation, jailing threshold, CPU under 20% on a validator. Also the first live schema-watch pull from a validator so `testdata/schema/` is refreshed from real lines.
- **Review debt:** `7ab2f4a`, `0fa9c7d`, `59f0958`, `2fabd92` and `8d1e2eb` shipped without the second-engine review the recipe calls for.
- **Deferred with spec retained:** consensus per-line lock batching (revisit only if a validator measurement shows the consensus stream hot), `snapshot_status` and `accumulator_consensus` monitors, build info plus `/livez`, `/readyz` and `--pprof` on the metrics listener, CI survey of the remaining sibling repos.
- **Scratched:** `info_probe` and six trivial upstream monitors (covered by the envelope or no operator ask), the pre-change CPU baseline.
- **Charm flags** for the new monitors: `docs/TODO.md` T1.

## 9. Upstream Release Map

For anyone tracing a ported item back to its upstream source (`git show <hash>` after `git fetch --no-tags upstream`).

| Release | Date | Commits |
|---|---|---|
| v3.0.0 | 2026-05-26 | `c937bbf` (single squash) |
| v3.1.0 | 2026-07-04 | `2cb58cd`, `f38ddb6`, `d9f74f4`, `7314f28`, `85d9da3`, `7b3009a`, `28660ea`, `7de3849`, `4de8f5a`, `4dc9637`, `2dc5aa9`, `c792055` |
| v4.0.6 | 2026-08-09 | `8813a56` through `d4f7ffb` (metrics audit sweep), incl. `c7a8ca4`, `ff31e33`, `628cdf0`, `302f0d6`, `d078382`, `4f075a8`, `caaaf96`, `69f73bd` |
| v4.0.7 | 2026-08-27 | `cd9e6aa`, `0af6b36` |
| v4.1.0 | 2026-09-09 | `10cc834`, `706b52c`, `85ee802` |
| v4.1.1 | 2026-09-10 | `d30125a`, `3be57c9`, `0ad0e88`, `88b74b3` |

Upstream paths differ from ours: `cmd/hl-exporter`, `internal/evm/parser.go`, `internal/monitors/stream.go`, `internal/utils/latest_files.go`. Their rationale for each metric lives in `HL_NODE_METRICS_AUDIT.md`, migration notes in `UPGRADING.md`.

## 10. Lessons for the Next Port

1. Spend the first ten minutes on merge base, tags and path mapping; that determines whether the job is a merge or a rewrite.
2. Write the phase-grouping rationale into the plan up front.
3. Capture a CPU and RSS baseline on a live node before the first code change.
4. Keep the handoff section current at the end of every phase; it is what the next session actually reads.
5. Freeze the diff before handing it to reviewers.
6. Ask "do we use this?" before fixing anything that talks to an external service.
7. Budget an hour of live-node time per release-sized phase; it finds what four review engines miss.
8. Record review debt in the tracker, not in chat, or it is lost at the next context clear.
9. Run the `docs/operations/upstream-sync.md` routine monthly so the next port is one release, not six.
