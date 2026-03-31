# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
make build                # → bin/hyperliquid-exporter (embeds version, commit, build time via ldflags)
make test                 # go test -shuffle=on ./...
make test RACE=1          # with race detector
make test V=1 N=3         # verbose, run 3 times
make lint                 # golangci-lint (--build-tags="heavy")
make fmt                  # gofmt -s -w .
make clean                # remove bin/

# Run
./bin/hyperliquid-exporter start --chain mainnet [flags]

# Docker
docker compose up
```

## CLI Flags

`--chain` (mainnet|testnet), `--log-level`, `--node-home`, `--node-binary`, `--evm-metrics`, `--contract-metrics`, `--contract-metrics-limit`, `--replica-metrics`, `--validator-rtt`, `--peer-latency`, `--otlp`, `--otlp-endpoint`, `--otlp-insecure`, `--alias`. Config merges env vars (`.env` file via godotenv) with CLI flags.

## Architecture

**Prometheus metrics exporter for Hyperliquid blockchain nodes.** Reads node state from local files (block times, replica commands, EVM blocks, consensus logs) and exposes 80+ metrics on `:8086/metrics`. Optional OTLP export.

### Startup flow

`cmd/hyperliquid-exporter/main.go` → parses flags → `config.LoadConfig()` → resolves validator identity → `metrics.InitMetrics()` (Prometheus + optional OTLP) → `exporter.Start()` launches all monitor goroutines.

### Key packages

- **`internal/exporter/`** — Orchestrator. `Start()` launches ~14 monitor goroutines with error channels; handles graceful shutdown via context cancellation.
- **`internal/monitors/`** — One file per monitor (block, consensus, evm, replica, validator, gossip, etc.). Each runs in its own goroutine, reads node files or APIs, and calls metric setters.
- **`internal/metrics/`** — Metric definitions (`instruments.go`), update functions (`setters.go`), async callbacks (`callbacks.go`), cleanup loop, Prometheus server (`prometheus.go`), OTLP setup (`otlp.go`). Global state via `currentValues`/`labeledValues` maps. Cleanup runs every 30s, capping labeled values at 100 per metric.
- **`internal/peermon/`** — Peer latency monitoring (`--peer-latency`). Maintains bounded peer set (max 100) with disk persistence, probes peers via TCP connect once per minute. Fed peer IPs from gossip monitors and tcp_traffic logs (outbound peer discovery).
- **`internal/replica/`** — Parses msgpack-formatted `replica_cmds` files into block metrics. Object pooling for memory efficiency.
- **`internal/cache/`** — Thread-safe LRU cache with optional TTL. Used for signer→validator mappings, validator info, contract data.
- **`internal/config/`** — Merges `.env` + env vars + CLI flags into `Config` struct.
- **`internal/logger/`** — Component-aware colored logging (CORE, EVM, CONSENSUS, etc.).
- **`internal/contracts/`** — Resolves contract addresses to names/symbols.
- **`internal/hyperliquid-api/`** — Queries validator status and metadata from Hyperliquid API.

### Design patterns

- **Monitor-per-goroutine**: Each data source gets an independent goroutine reporting errors through channels.
- **LRU + periodic cleanup**: Prevents unbounded label cardinality in metrics.
- **Dual-state block monitoring**: Supports fast/slow block time directories with legacy fallback.
- **Sliding windows**: QC/TC participation rates calculated over configurable time windows.
- **Msgpack streaming**: Replica commands parsed from binary msgpack files, not JSON APIs.
