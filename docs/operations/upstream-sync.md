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
   | new-monitor | a monitor we lack | File under `docs/plans/` with the flag policy from the port plan (local-only sources default on, network default off, upstream metric names verbatim) |
   | infra | CI, release, docs tooling | Adopt only if a sibling repo runs it or a plan needs it |
   | skip | rewritten consensus internals, source-state layer, renames, alerts | Note the SHA and the reason in the log |

4. Record the review below. File ported items in the changelog Unreleased section; file deferred items in `docs/plans/`.

## Review log

| Reviewed | Upstream release | SHA | Outcome |
|---|---|---|---|
| 2026-09-10 | v4.1.1 | `b150dcc` | Full review, ported over six phases; see `docs/plans/upstream-port-plan.md` |
