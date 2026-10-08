# Ubuntu package for hyperliquid-exporter

This recipe follows the Taiko packages' Python builder and debhelper pattern.
It produces a Debian binary package for Ubuntu 24.04 Noble, built natively on
amd64 or arm64. It does not install a Hyperliquid node.

| Component | Path |
| --- | --- |
| Binary | `/usr/bin/hyperliquid-exporter` |
| Service and system account | `hyperliquid-exporter` |
| Configuration | `/etc/default/hyperliquid-exporter` |
| Private exporter state | `/var/lib/hyperliquid-exporter` |
| APT package | `hyperliquid-metrics-exporter` |

## Build and verify

On Ubuntu 24.04, install the build prerequisites and Go with support for the
version in `go.mod`:

```sh
sudo apt-get update
sudo apt-get install debhelper dpkg-dev fakeroot build-essential python3 git ca-certificates
make test-deb
make deb
```

The builder selects the Go toolchain from `go.mod`, uses `-mod=readonly` and
`-trimpath`, and disables CGO. It embeds the upstream version, source commit
and build time. Defaults are the version from `VERSION` with revision `-1`,
maintainer `Erik Lonroth <erik@dwellir.com>` and output directory `dist/ubuntu`.
Override them with `make deb DEB_ARGS='--version 2.5.1-2 --output /tmp/packages'`.
`--maintainer 'Name <email>'` and `--jobs N` are also supported.

Builds emit `.deb`, `.deb.sha256` and `.deb.build-info.json`. The provenance
includes the source commit, dirty-checkout status, toolchain and input binary
hash. Use a clean checkout for release builds. The same provenance is installed
under `/usr/share/doc/hyperliquid-metrics-exporter`. `SOURCE_DATE_EPOCH` overrides the
build timestamp; otherwise the source commit timestamp is used. Builds never
upload packages and refuse to overwrite existing outputs. Dependency downloads
may require network access.

To package an existing native ELF executable:

```sh
python3 packaging/ubuntu/build.py --binary ./bin/hyperliquid-exporter --version 2.5.1-1
```

Prebuilt mode verifies architecture and records the input hash, but does not
assert that the binary came from the recorded source commit. Debhelper computes
shared-library dependencies if a prebuilt binary needs them. Source builds are
static Go executables. The build does not modify or strip the input binary.

CI runs archive tests and source builds on native Noble amd64 and arm64 runners,
checks the extracted binary's version and commit, and retains package artifacts.
It does not publish releases or upload to Cloudsmith.

## Install and configure

With the chosen Cloudsmith repository configured, install the package:

```sh
sudo apt-get install hyperliquid-metrics-exporter
```

APT installs the binary and unit, creates the `hyperliquid-exporter` system
account, and provisions `/var/lib/hyperliquid-exporter` with mode 0700 and the
correct ownership. It installs `/etc/default/hyperliquid-exporter` as a
root-owned mode 0600 conffile. No directory creation, symlink or additional
exporter dependencies need to be managed after installation.

For a Juju deployment, the Hyperliquid charm supplies the node-specific
configuration and read permissions, then enables the service. Operators do not
need to run post-install commands. See the [charm integration contract](charm-integration.md)
for the exact responsibilities and migration from the subordinate. That charm
migration is a separate change; it is not implemented by this package PR.

Defaults match the existing charm's `/home/hyperliquid` runtime home. For a
standalone installation, configure the actual node paths, network and optional
flags in `/etc/default/hyperliquid-exporter`, arrange read access to the node
files, then start the service:

```sh
sudoedit /etc/default/hyperliquid-exporter
sudo systemctl enable --now hyperliquid-exporter.service
```

The service stays stopped and disabled on first installation so the owner can
configure it before startup. Environment-file syntax is systemd syntax, not
shell syntax. `EXPORTER_ARGS` accepts extra flags without a shell, for example
`EXPORTER_ARGS="--evm-metrics --replica-metrics --peer-latency"`.

`EXPORTER_STATE_DIR=/var/lib/hyperliquid-exporter` keeps optional peer-cache
writes separate from `NODE_HOME`. The exporter reads the real node directory
directly. Its unit makes node files read-only, including files under `/home`.
`BINARY_HOME` points to the node's runtime home for optional binary-version
metrics. No node files, permissions or ownership are changed by APT.

```sh
systemctl status hyperliquid-exporter
journalctl -u hyperliquid-exporter -f
curl --fail http://127.0.0.1:8086/metrics
```

The exporter listens on `:8086/metrics`. The principal charm owns scrape
relations and host access controls. Host procfs settings such as `hidepid` can
restrict process metrics even when node files are readable.

### Environment-file settings

The package ships [a commented template](config) directly as
`/etc/default/hyperliquid-exporter`. APT preserves edits on upgrade; there is no
template to copy manually.
An untouched reference copy is also installed as
`/usr/share/doc/hyperliquid-metrics-exporter/config`.

These are the supported package settings:

| Setting | Shipped value | What it controls |
| --- | --- | --- |
| `CHAIN` | `mainnet` | Network, `mainnet` or `testnet`. The unit passes it as `--chain`; it selects network-specific API and binary sources and OTLP chain labels. |
| `NODE_HOME` | `/home/hyperliquid/hl` | Existing node data directory containing `data` and `hyperliquid_data`. Local-file monitors read this tree. |
| `NODE_BINARY` | `/home/hyperliquid/hl-node` | Existing `hl-node` executable used for node version metrics. If unset or empty, falls back to `<BINARY_HOME>/hl-node`. |
| `BINARY_HOME` | `/home/hyperliquid` | Directory containing `hl-visor` for optional `--binary-metrics` checks. Also supplies the `NODE_BINARY` fallback. If unset or empty, falls back to `HOME`. |
| `EXPORTER_STATE_DIR` | `/var/lib/hyperliquid-exporter` | Directory containing `peers.json` when `--peer-latency` is enabled. Keep this package-provisioned writable path; other paths need matching permissions and systemd overrides. If explicitly empty, the application uses the legacy `.hyperliquid-exporter` directory beside `NODE_HOME`. |
| `EXPORTER_ARGS` | Empty | Additional `start` flags, listed below. Empty preserves application defaults. |
| `HOME` | `/var/lib/hyperliquid-exporter`, supplied by the unit | Fallback for unset `NODE_HOME` as `<HOME>/hl`, and for unset `BINARY_HOME`. Leave unchanged in package deployments; set node paths explicitly instead. |

Use `NAME=value`, without `export`. Quote values containing spaces. This file
does not expand `$HOME`, `${VARIABLE}`, `~` or shell commands; use absolute paths
and literal values. `EXPORTER_ARGS` is split into arguments by systemd, without
a shell. Put all desired flags in one assignment; later assignments replace
earlier ones. Keep `CHAIN`, `NODE_HOME` and `NODE_BINARY` in their dedicated
settings rather than adding duplicate flags.

After a standalone edit, run `sudo systemctl restart hyperliquid-exporter` to
apply it. `daemon-reload` is unnecessary for environment-file edits. On a
Juju-managed host, change the principal charm configuration so its next hook
does not overwrite a manual edit. A service restart starts a stopped service;
use `sudo systemctl try-restart hyperliquid-exporter` to preserve stopped state.

### Flags in EXPORTER_ARGS

Boolean flags accept `--flag`, `--flag=true` or `--flag=false`. To disable a
default-on monitor, use `=false`; `--flag false` does not disable it.

| Flag | Default | What it controls |
| --- | --- | --- |
| `--log-level` | `info` | Logging verbosity: `debug`, `info`, `warning` or `error`. |
| `--evm-metrics` | `false` | EVM blocks, receipts and account monitoring. |
| `--replica-metrics` | `false` | Transaction/action metrics parsed from local `replica_cmds`; the node must produce this stream. |
| `--validator-rtt` | `false` | Validator IP discovery and RTT monitoring. |
| `--peer-latency` | `false` | TCP latency probes to known peers and persistence under `EXPORTER_STATE_DIR`. |
| `--binary-metrics` | `false` | Compare local `<BINARY_HOME>/hl-visor` with the network's published binary every 30 minutes. Downloads the published binary when it changes; does not upgrade the node. |
| `--process-metrics` | `true` | `hl-node` and `hl-visor` process liveness and resource usage from procfs. |
| `--child-stderr-metrics` | `true` | Child crash artifacts under `data/visor_child_stderr`. |
| `--visor-metrics` | `true` | Visor synchronization state from `visor_abci_state.json`. |
| `--node-state-metrics` | `true` | Persisted checkpoint and freeze heights under `hyperliquid_data`. |
| `--disk-metrics` | `true` | Node directory sizes and filesystem free/total capacity. |
| `--operator-config-metrics` | `true` | Operator configuration presence, age and jailing threshold, on validator nodes. |
| `--crit-msg-metrics` | `true` | `bug!` and `crit!` counts and top locations from `crit_msg_stats`. |
| `--otlp` | `false` | Also export metrics through OTLP/HTTP. Requires `--otlp-endpoint`, `--alias` and a configured `CHAIN`; Prometheus remains enabled. |
| `--otlp-endpoint` | Empty | Collector host and port for OTLP/HTTP, for example `collector.example:4318`. Required when OTLP is enabled. |
| `--alias` | Empty | Operator-chosen node identifier in OTLP resource attributes. Required when OTLP is enabled. |
| `--otlp-insecure` | `false` | Use HTTP instead of TLS for OTLP export. |
| `--chain` | Supplied from `CHAIN` | CLI equivalent of the network setting; use `CHAIN` in this file. |
| `--node-home` | Empty | CLI override of `NODE_HOME`; use the environment setting in package deployments. |
| `--node-binary` | Empty | CLI override of `NODE_BINARY`; use the environment setting in package deployments. |

For local EVM/transaction metrics plus peer monitoring:

```ini
EXPORTER_ARGS="--evm-metrics --replica-metrics --peer-latency"
```

For OTLP/HTTP over TLS, with local EVM metrics:

```ini
CHAIN=mainnet
EXPORTER_ARGS="--evm-metrics --otlp --otlp-endpoint=collector.example:4318 --alias=my-node"
```

Use `hyperliquid-exporter start --help` to inspect the installed executable's
flags. There are no separate `LOG_LEVEL`, `ENABLE_EVM`, `OTLP_ENDPOINT` or
metrics-port settings in this environment-file contract; pass the corresponding
flags through `EXPORTER_ARGS`. The Prometheus listener remains `:8086/metrics`.

## Upgrade and removal

APT preserves edited configuration as a conffile and try-restarts only an
already running service on upgrade. Stopped/disabled services stay stopped or
disabled. Removal stops the service. Purge removes packaged configuration but
keeps the service account and exporter state. It never
removes node data. Delete retained state and accounts only as a separate action.

`tests/container_smoke.py` tests first install, account and permissions,
configuration preservation, running upgrades, stopped reinstalls, removal and purge.
Run it as root only in a fresh disposable Ubuntu 24.04 systemd container with
Python 3 and `acl` installed. Pass
two packages with increasing versions. It installs and removes the package and
uses a temporary service probe to test the unit's filesystem restrictions.
It verifies the real exporter's local metrics endpoint against fixture node
files, then uses a probe for lifecycle checks. It does not prove node readiness.

Inside that disposable container, with both package files copied to `/tmp`:

```sh
python3 container_smoke.py /tmp/hyperliquid-metrics-exporter_2.5.1-1_amd64.deb /tmp/hyperliquid-metrics-exporter_2.5.1-2_amd64.deb
```

## Future Cloudsmith publication

Authenticate the Cloudsmith CLI outside this checkout. After validation, upload
the same package bytes to the chosen Dwellir repository:

```sh
cloudsmith push deb dwellir/deb-testing/ubuntu/noble dist/ubuntu/hyperliquid-metrics-exporter_2.5.1-1_amd64.deb
# After testing, promote the same artifact:
cloudsmith push deb dwellir/deb-staging/ubuntu/noble dist/ubuntu/hyperliquid-metrics-exporter_2.5.1-1_amd64.deb
# After staging validation:
cloudsmith push deb dwellir/deb-stable/ubuntu/noble dist/ubuntu/hyperliquid-metrics-exporter_2.5.1-1_amd64.deb
```

Upload the arm64 artifact separately when available. Keep the version and
checksum unchanged when moving through testing, staging and stable. Use a new
Debian revision when package contents change. For release candidates use a
version such as `2.5.2~rc.1-1`, which APT sorts below `2.5.2-1`.

Use each repository's generated APT instructions for signing keys and private
repository authentication, then install with
`sudo apt-get install hyperliquid-metrics-exporter=2.5.1-1`. Keep credentials outside
the repository. See the [Cloudsmith Debian repository documentation](https://docs.cloudsmith.com/formats/debian-repository)
for CLI upload syntax and repository-specific consumer setup.
