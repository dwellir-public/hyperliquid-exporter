# Hyperliquid Exporter

A Go-based exporter that collects and exposes metrics for Hyperliquid node operators. This exporter produces metrics for HyperCore, HyperEVM, and HyperBFT, covering block production, transaction flow, validator performance, stake distribution, EVM activity, and consensus events. For a full list of metrics, see [docs/metrics.md](docs/metrics.md).

## Quick Start

### Installation

```bash
git clone https://github.com/validaoxyz/hyperliquid-exporter.git $HOME/hyperliquid-exporter
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
  --contract-metrics   Per-contract transaction tracking
  --contract-metrics-limit N  Max contract labels to retain (default: 20)
  --validator-rtt      Enable validator RTT monitoring
  --peer-latency       Enable peer latency monitoring (TCP probes to known peers)
  --otlp               Enable OTLP export (requires --alias and --otlp-endpoint)
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

## Run with Docker

Use Docker to run `hyperliquid-exporter` in a container:

1. Edit `docker-compose.yml` to set the correct paths for your Hyperliquid node data directory and binary.

2. Build the image and run the container:
```bash
docker compose up -d
```

## Documentation

- [Metrics Reference](docs/metrics.md) - All metrics with descriptions and labels
