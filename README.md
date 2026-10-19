# Hyperliquid Exporter

A Prometheus exporter for Hyperliquid node operators. It reads the files a running hl-node writes (block times, consensus and gossip logs, replica commands, EVM blocks, visor state) plus `/proc` and the Hyperliquid API, and publishes metrics for HyperCore, HyperEVM, HyperBFT and the node host: block production, transaction flow, validator performance, stake distribution, peer connectivity, process health and disk usage. For every metric, see the [metrics reference](docs/metrics-overview.md).

## Requirements

- Linux, on the same host as hl-node (the process monitors read `/proc`)
- Read access to the node's home directory (`$HOME/hl` by default)
- Go 1.27 or later, only when building from source

## Install

Each [release](https://github.com/dwellir-public/hyperliquid-exporter/releases) publishes a Linux amd64 binary named `hyperliquid-exporter-vX.Y.Z` with a `sha256sum` checksum file alongside it (releases after v2.5.1):

```bash
VERSION=vX.Y.Z    # pick from the releases page
BASE=https://github.com/dwellir-public/hyperliquid-exporter/releases/download/$VERSION
curl -fL -O "$BASE/hyperliquid-exporter-$VERSION" -O "$BASE/hyperliquid-exporter-$VERSION.sha256"
sha256sum -c "hyperliquid-exporter-$VERSION.sha256"
sudo install -m 0755 "hyperliquid-exporter-$VERSION" /usr/local/bin/hyperliquid-exporter
hyperliquid-exporter --version
```

To build from source instead:

```bash
git clone https://github.com/dwellir-public/hyperliquid-exporter.git
cd hyperliquid-exporter
make build
sudo install -m 0755 bin/hyperliquid-exporter /usr/local/bin/
```

## Usage

```bash
hyperliquid-exporter start --chain mainnet [flags]
```

Metrics are served on `:8086/metrics`, with a liveness check on `/health`. Run `hyperliquid-exporter start --help` for the full flag list.

### General flags

| Flag | Default | Description |
|------|---------|-------------|
| `--chain` | - | `mainnet` or `testnet`; selects the Hyperliquid API and binary endpoints |
| `--metrics-addr` | `:8086` | Listen address for the Prometheus endpoint |
| `--log-level` | `info` | `debug`, `info`, `warning` or `error` |
| `--node-home` | `$HOME/hl` | Node home directory; overrides `NODE_HOME` |
| `--node-binary` | `$HOME/hl-node` | hl-node binary, read for the version metric; overrides `NODE_BINARY` |
| `--alias` | - | Node identifier attached to OTLP exports |
| `--otlp` | `false` | Also push metrics over OTLP; requires `--chain`, `--alias` and `--otlp-endpoint` |
| `--otlp-endpoint` | - | OTLP HTTP endpoint |
| `--otlp-insecure` | `false` | Use plain HTTP for OTLP |

### Monitors

Block, consensus, gossip, proposer and validator-status metrics are always on. The validator-status monitor queries the Hyperliquid API. The flags below toggle the rest; disable a default-on monitor with `--<flag>=false`.

| Flag | Default | Source | Notes |
|------|---------|--------|-------|
| `--replica-metrics` | off | `data/replica_cmds` | Transaction and operation metrics; hl-node must run with `--replica-cmds-style actions-and-responses` |
| `--evm-metrics` | off | `data/evm_block_and_receipts`, ABCI state | HyperEVM block, gas and account metrics |
| `--validator-rtt` | off | ABCI state, TCP connects | RTT to each validator's IP; network |
| `--peer-latency` | off | gossip and `tcp_traffic` logs, TCP connects | Per-peer latency, traffic and parent-peer tracking; network |
| `--binary-metrics` | off | `binaries.hyperliquid.xyz` | Compares the local `hl-visor` against the published one every 30 min; network |
| `--process-metrics` | on | `/proc` | hl-node and hl-visor liveness and resource usage |
| `--child-stderr-metrics` | on | `data/visor_child_stderr` | hl-visor child crash artifacts |
| `--visor-metrics` | on | `hyperliquid_data/visor_abci_state.json` | hl-visor sync state |
| `--node-state-metrics` | on | `hyperliquid_data` | Persisted checkpoint and freeze heights |
| `--disk-metrics` | on | node home | Size per subdirectory and filesystem capacity |
| `--operator-config-metrics` | on | `file_mod_time_tracker` | Presence, age and jailing threshold; validators only |
| `--crit-msg-metrics` | on | `data/crit_msg_stats` | hl-node `bug!` and `crit!` counters and top crit locations |

### Environment

Flags win over environment variables. A `.env` file in the working directory is loaded at startup.

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_HOME` | `$HOME/hl` | Node home directory |
| `BINARY_HOME` | `$HOME` | Directory holding `hl-visor` and, by default, `hl-node` |
| `NODE_BINARY` | `$BINARY_HOME/hl-node` | hl-node binary |

## Run with systemd

```bash
sudo tee /etc/systemd/system/hyperliquid-exporter.service >/dev/null <<EOF
[Unit]
Description=Hyperliquid Prometheus Exporter
After=network.target

[Service]
User=$USER
Group=$USER
WorkingDirectory=$HOME
ExecStart=/usr/local/bin/hyperliquid-exporter start --chain mainnet --replica-metrics --evm-metrics
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now hyperliquid-exporter
```

`$HOME` and `$USER` expand when the unit is written. `WorkingDirectory` decides which `.env` is loaded.

## Development

```bash
make build    # bin/hyperliquid-exporter, with version, commit and build time embedded
make test     # go test -shuffle=on ./...; RACE=1 adds the race detector
make lint     # golangci-lint
```

Conventions for contributors and agents are in [AGENTS.md](AGENTS.md).

## Documentation

- [Metrics reference](docs/metrics-overview.md): every metric with its labels and requirements
- [Changelog](CHANGELOG.md)
- [Upstream sync](docs/operations/upstream-sync.md): how upstream changes are reviewed and ported
- [hl-node schema watch](docs/operations/hl-node-schema-watch.md): catching hl-node log format changes
- [Upstream port v3 to v4](docs/reports/upstream-port-v3-to-v4.md): record of the 2026-09 port
- [Deferred work](docs/TODO.md)

## Fork status

This is a permanent fork of [validaoxyz/hyperliquid-exporter](https://github.com/validaoxyz/hyperliquid-exporter) with its own metric names and version line. Upstream changes are reviewed by hand and ported selectively; see [upstream sync](docs/operations/upstream-sync.md).
