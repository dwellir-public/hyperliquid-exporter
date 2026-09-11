# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
make build                # → bin/hyperliquid-exporter (embeds version, commit, build time via ldflags)
make test                 # go test -shuffle=on ./...
make test RACE=1          # with race detector (CI race job)
make test N=16            # run 16 times (CI flaky job)
make test V=1 N=3         # verbose, run 3 times
make lint                 # golangci-lint
make fmt                  # gofmt -s -w .
make clean                # remove bin/

# Run
./bin/hyperliquid-exporter start --chain mainnet [flags]
```

## CLI Flags

`--chain` (mainnet|testnet), `--log-level`, `--node-home`, `--node-binary`, `--evm-metrics`, `--replica-metrics`, `--validator-rtt`, `--peer-latency`, `--otlp`, `--otlp-endpoint`, `--otlp-insecure`, `--alias`. Config merges env vars (`.env` file via godotenv) with CLI flags.

## Architecture

**Prometheus metrics exporter for Hyperliquid blockchain nodes.** Reads node state from local files (block times, replica commands, EVM blocks, consensus logs) and exposes 80+ metrics on `:8086/metrics`. Optional OTLP export.

### Startup flow

`cmd/hyperliquid-exporter/main.go` → parses flags → `config.LoadConfig()` → resolves validator identity → `metrics.InitMetrics()` (Prometheus + optional OTLP) → `exporter.Start()` launches all monitor goroutines.

### Key packages

- **`internal/exporter/`** — Orchestrator. `Start()` launches ~14 monitor goroutines with error channels; handles graceful shutdown via context cancellation.
- **`internal/monitors/`** — One file per monitor (block, consensus, evm, replica, validator, gossip, etc.). Each runs in its own goroutine, reads node files or APIs, and calls metric setters.
- **`internal/metrics/`** — Metric definitions (`instruments.go`), update functions (`setters.go`), async callbacks (`callbacks.go`), cleanup loop, Prometheus server (`prometheus.go`), OTLP setup (`otlp.go`). Global state via `currentValues`/`labeledValues` maps. A 30 s ticker prunes votes older than 24 h; Go memory gauges are served from a MemStats snapshot refreshed every 30 s. Ported node-host instruments live in `node_instruments.go`.
- **`internal/peermon/`** — Peer latency monitoring (`--peer-latency`). Maintains bounded peer set (max 256, ex-parent peers exempt from LRU eviction) with disk persistence, probes peers via TCP connect once per minute. Fed peer IPs from gossip monitors and tcp_traffic logs (outbound peer discovery).
- **`internal/replica/`** — Parses msgpack-formatted `replica_cmds` files into block metrics. Object pooling for memory efficiency.
- **`internal/cache/`** — Thread-safe LRU cache with optional TTL. Used for signer→validator mappings and validator info.
- **`internal/config/`** — Merges `.env` + env vars + CLI flags into `Config` struct.
- **`internal/logger/`** — Component-aware colored logging (CORE, EVM, CONSENSUS, etc.).
- **`internal/hyperliquid-api/`** — Queries validator status and metadata from Hyperliquid API.

### Design patterns

- **Monitor-per-goroutine**: Each data source gets an independent goroutine reporting errors through channels.
- **Explicit series reconciliation**: Per-validator series are removed via `dropMissing` + `RemoveValidatorSeries` when a validator leaves the set; peer and connectivity series remove themselves. There is no generic labeled-series sweep.
- **Dual-state block monitoring**: Supports fast/slow block time directories with legacy fallback.
- **Sliding windows**: QC/TC participation rates calculated over configurable time windows.
- **Msgpack streaming**: Replica commands parsed from binary msgpack files, not JSON APIs.

### Conventions

- **Goroutines**: every launch goes through `safego.Go(component, fn)` (`internal/safego`), which recovers panics and counts them in `hl_exporter_monitor_panics_total`. Package `metrics` cannot import it (cycle); its tickers stay bare.
- **Latest-file lookup**: `utils.LatestFile(root)`; tight EOF loops use `utils.NewLatestFileCache(root, latestFileRescan)` (2 s). Never reintroduce `filepath.Walk`.
- **Tailing**: tight-loop log streams run through `streamTailer.run(ctx, fn)` in `internal/monitors/log_tail.go`; polled hourly streams keep a `tailState` and read via `tailState.poll`. Both hold torn trailing lines and drain the old file at rollover. Startup seeding passes `seeding=true` to suppress counters, including parse-error counters.
- **tcp_traffic**: read once by `TCPTrafficMonitor` and fanned out to `tcpTrafficConsumer` implementations (`onRecord`, `onPoll`). Add a consumer, not a second tailer.
- **Parse helpers**: `internal/monitors/parse_helpers.go` (`unmarshalRequiredJSON`, `parseVisorTime`, `rawJSONArray`, `rawJSONObject`, `parseStage`). hl-node timestamps are zoneless; always use `parseVisorTime`.
- **Source-health envelope**: every consumed source calls `metrics.SetSourceUp(stream, bool)`, `MarkSourceSample(stream, at)` on a good record, `IncrementParseErrors(stream, stage)` for rejected records and `IncrementSourceErrors(stream, stage)` for stat/read/walk/decode failures. `streamTailer` does this automatically. Every stream has a fixture under `internal/monitors/testdata/schema/` checked by `TestSchemaFixtures`; a new stream needs a `schemaStreams` entry.
- **Ported instruments**: declared in `internal/metrics/node_instruments.go` with the local `gauge`/`counter` closures, published via `SetGauge`, `ClearGauge`, `SetGaugeSeries`, `ClearGaugeSeries`, `AddCounter`. A missing optional field withdraws its gauge (absent, not zero). `GaugeValue`/`GaugeSeriesValue` exist for tests.
- **Action types**: pass through `actiontypes.Normalize`; categories from `actiontypes.Category`. Add new hl-node actions there.
- **Round advance**: handled in `consensus_monitor.processConsensusLine` (`round_advance.go`). No standalone monitor.
- **Per-`peer_ip` gossip series** publish only with `--peer-latency` (`perIP` field).
- **Flags for new monitors**: one flag each; default on for local-file or procfs sources, off for anything touching the network. Validator-only monitors gate on `config.IsValidator`, no extra flag. Fresh ports keep upstream metric names verbatim; existing metrics keep ours.
- **Tests**: `initTestMetrics(t)` from `helpers_test.go`; fixtures are inline strings shaped like real log lines. Every schema change gets a case in the old and the new shape. Every metric or label change updates `docs/metrics-overview.md` by hand.
