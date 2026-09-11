# Hyperliquid Exporter

A Go-based exporter that collects and exposes metrics for Hyperliquid node operators. This exporter produces metrics for HyperCore, HyperEVM, and HyperBFT, covering block production, transaction flow, validator performance, stake distribution, EVM activity, and consensus events. For a full list of metrics, see [docs/metrics-overview.md](docs/metrics-overview.md).

## Quick Start

### Installation

```bash
git clone https://github.com/dwellir-public/hyperliquid-exporter.git $HOME/hyperliquid-exporter
cd $HOME/hyperliquid-exporter
make build
```

### Basic Usage

```bash
./bin/hyperliquid-exporter start --chain mainnet [OPTIONS]

OPTIONS:
  --chain              Chain type: 'mainnet' or 'testnet' (required)
  --replica-metrics    Transaction metrics (requires node --replica-cmds-style)
  --evm-metrics        EVM chain metrics
  --validator-rtt      Enable validator RTT monitoring
  --peer-latency       Enable peer latency monitoring (TCP probes to known peers)
  --otlp               Enable OTLP export (requires --alias and --otlp-endpoint)
  --binary-metrics     Compare local hl-visor against the published one (hl_software_up_to_date; network)

  Node-host monitors, on by default, each disabled with --<flag>=false:
  --process-metrics          hl-node and hl-visor liveness and resources from /proc
  --child-stderr-metrics     hl-visor child crash artifacts
  --visor-metrics            hl-visor sync state from visor_abci_state.json
  --node-state-metrics       Persisted checkpoint and freeze heights under hyperliquid_data
  --disk-metrics             NODE_HOME size per subdirectory and filesystem capacity
  --operator-config-metrics  Operator config presence, age and jailing threshold (validators)
  --crit-msg-metrics         hl-node bug! and crit! counters and top crit locations
```

Run `./bin/hyperliquid-exporter start --help` for a complete list of flags.

Example: `./bin/hyperliquid-exporter start --chain mainnet --replica-metrics --evm-metrics`.

By default, the exporter:
- Exposes Prometheus metrics on `:8086/metrics`
- Looks for log files in `$HOME/hl` and binaries in `$HOME/`
- Uses `info` log level
- Disables OTLP export


## Run with Systemd
To run the exporter as a systemd service:

Create a systemd service file:
```
echo "[Unit]
Description=HyperLiquid Prometheus Exporter
After=network.target

[Service]
WorkingDirectory=$HOME/hyperliquid-exporter

ExecStart=$HOME/hyperliquid-exporter/bin/hyperliquid-exporter start --chain $CHAIN [options]

Restart=always
RestartSec=10

User=$USER
Group=$USER

[Install]
WantedBy=multi-user.target" | sudo tee /etc/systemd/system/hyperliquid-exporter.service
```

## Deploy with Juju

In production, `hyperliquid-exporter` is deployed as a systemd service via the
[hyperliquid-metrics-exporter Juju charm](https://github.com/dwellir-public/ops/tree/main/juju/charms/hyperliquid-metrics-exporter).

## Documentation

- [Metrics Reference](docs/metrics-overview.md) - All metrics with descriptions and labels
- [Upstream Sync Routine](docs/operations/upstream-sync.md) - How and when to review upstream changes
- [hl-node Schema Watch](docs/operations/hl-node-schema-watch.md) - How to catch hl-node log format changes
- [Deferred Work](docs/TODO.md) - Triaged items not yet scheduled

## Relation to upstream

This repository is a fork of [validaoxyz/hyperliquid-exporter](https://github.com/validaoxyz/hyperliquid-exporter) and is intentionally divergent. It keeps its own metric names, adds peer latency monitoring, and ports upstream changes by hand on a schedule rather than tracking upstream releases. The 2026-09 port of upstream v3.0.0 to v4.1.1 is recorded in [docs/reports/upstream-port-v3-to-v4.md](docs/reports/upstream-port-v3-to-v4.md).
