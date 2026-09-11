# Upstream Sync Routine

This fork descends from [validaoxyz/hyperliquid-exporter](https://github.com/validaoxyz/hyperliquid-exporter) v2.0.0 and is intentionally divergent: it keeps its own metric names, peer monitoring (`internal/peermon`) and OTel-first instrument declarations. The two histories share no merge base, so nothing cherry-picks. Every upstream change is ported by hand at content level, or deliberately skipped.

Run this monthly, or whenever upstream tags a release.

## Steps

1. Fetch upstream without tags. Our `v2.x` tags collide with upstream's, so never let `git fetch` write them:

   ```sh
   git fetch --no-tags upstream
   git ls-remote --tags upstream | grep -v '\^{}' | tail -5
   ```

2. Diff the consumed surface since the last reviewed SHA (table below) and read the newer `CHANGELOG.md` sections:

   ```sh
   LAST=b150dcc
   git diff --stat $LAST..upstream/main -- internal/monitors internal/replica internal/evm internal/actiontypes internal/metrics
   git show upstream/main:CHANGELOG.md | sed -n '1,120p'
   git show upstream/main:UPGRADING.md
   ```

   Upstream paths differ from ours: `cmd/hl-exporter`, `internal/evm/parser.go`, `internal/monitors/stream.go`, `internal/utils/latest_files.go`. Metric names differ from v4.0.6 on (`UPGRADING.md` lists the renames); we keep ours.

3. Classify each change:

   | Class | Meaning | Action |
   |---|---|---|
   | schema-compat | hl-node changed a log or file shape | Port now. Add a fixture in old and new shape. Also run the [schema watch](hl-node-schema-watch.md) |
   | correctness | wrong value in a metric we also have | Port, keep our metric name |
   | perf | CPU, memory, lock churn | Port if we have the same code path |
   | new-monitor | a monitor we lack | File under `docs/plans/` following the standing decisions below |
   | infra | CI, release, docs tooling | Adopt only if a sibling repo runs it or a plan needs it |
   | skip | rewritten consensus internals, source-state layer, renames, alerts | Note the SHA and the reason in the log |

4. Record the review below. File ported items in the changelog Unreleased section; file deferred items in `docs/plans/`.

## Standing decisions

Settled during the 2026-09 port; apply them to every later review so they are not re-derived.

- **Names.** Existing metrics keep our names. Metrics ported fresh use the upstream name verbatim, so upstream alert rules for them apply unchanged. Document the upstream origin in `docs/metrics-overview.md`; `BREAKING_CHANGES.md` is only for renames of existing metrics.
- **Flags.** One flag per new monitor. Default on for monitors that read only local files or procfs, default off for anything that touches the network. No umbrella flag. Validator-only monitors start on `config.IsValidator`, no extra flag.
- **Source health.** Ported monitors use the generic `hl_exporter_source_up`, `hl_exporter_source_sample_age_seconds`, `hl_exporter_parse_errors_total{stream,stage}` and `hl_exporter_source_errors_total{stream,stage}` envelope, not upstream's per-monitor `*_scan_up`, `*_walk_up`, `*_errors_total` and `*_last_observation_age_seconds` gauges. Strip upstream's `RegisterSource`/`MarkSource*` calls when lifting a monitor.
- **Not adopted, do not re-review:** the v4.0.6 metric renames; `stream.go`, `source_state.go`, `health.go` wholesale; the `tokio_runtime`, `tcp_lz4`, `tmp_dir`, `subsystem_steps`, `consensus_rpc`, `validator_connections`, `peer_set`, `info_probe`, `log_lines`, `public_ip`, `optional_streams`, `rate_limited`, `replay` and `replica_runs` monitors; alert rules (managed centrally); generated metric docs; rebasing or re-forking.
- **CI and release.** House practice in sibling repos (`iris`, `hyperliquid-l1-gateway`) sets the bar, not upstream. Rejected so far: `go mod tidy -diff`, standalone `go vet`/staticcheck, arm64 builds, SHA-pinned actions, dependabot, actionlint, `GOTOOLCHAIN: local`, checksums.

## Review log

| Reviewed | Upstream release | SHA | Outcome |
|---|---|---|---|
| 2026-09-10 | v4.1.1 | `b150dcc` | Full review, ported over six phases; see `docs/reports/upstream-port-v3-to-v4.md` |
