# Ubuntu package for hyperliquid-exporter

This recipe follows the Taiko packages' Python builder and debhelper pattern.
It produces a Debian binary package for Ubuntu 24.04 Noble, built natively on
amd64 or arm64. It does not install a Hyperliquid node.

| Component | Path |
| --- | --- |
| Binary | `/usr/bin/hyperliquid-exporter` |
| Service and system account | `hyperliquid-exporter` |
| Configuration | `/etc/hyperliquid-exporter/hyperliquid-exporter.conf` |
| Private exporter state | `/var/lib/hyperliquid-exporter` |
| Node-directory symlink | `/var/lib/hyperliquid-exporter/hl` |

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
under `/usr/share/doc/hyperliquid-exporter`. `SOURCE_DATE_EPOCH` overrides the
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

```sh
sudo apt-get install ./dist/ubuntu/hyperliquid-exporter_2.5.1-1_amd64.deb
sudo install -d -o hyperliquid-exporter -g hyperliquid-exporter -m 0700 /var/lib/hyperliquid-exporter
sudo ln -s /home/ubuntu/hl /var/lib/hyperliquid-exporter/hl
sudoedit /etc/hyperliquid-exporter/hyperliquid-exporter.conf
```

Substitute the actual node directory for `/home/ubuntu/hl`. The service stays
stopped and disabled after installation. Its startup condition requires the
node symlink to resolve to an existing directory. Set `CHAIN` to `mainnet` or
`testnet` and `NODE_BINARY` to the existing `hl-node` executable. Keep
`NODE_HOME=/var/lib/hyperliquid-exporter/hl`: optional peer-latency monitoring
stores `.hyperliquid-exporter/peers.json` beside this path, inside private
exporter state. Node files remain in their original location.

The service account needs traversal permission on the node's parent directories
and read/traversal permission on the files it monitors. Arrange read-only access
through your node provisioning. For example, on a host using ACLs:

```sh
sudo apt-get install acl
sudo setfacl -m u:hyperliquid-exporter:--x /home/ubuntu
sudo setfacl -R -m u:hyperliquid-exporter:r-X /home/ubuntu/hl
sudo find /home/ubuntu/hl -type d -exec setfacl -m d:u:hyperliquid-exporter:r-x {} +
sudo setfacl -m u:hyperliquid-exporter:r-x /home/ubuntu/hl-node
sudo -u hyperliquid-exporter test -r /var/lib/hyperliquid-exporter/hl/data
```

Adapt these paths and grant access only to required node data. Check permissions
again after node software updates and on newly created log directories. Do not
run the exporter as root to bypass missing permissions. The package does not
change node ownership or permissions. For process metrics, procfs access may
also be restricted by host settings such as `hidepid`.

The unit uses `ProtectSystem=strict` and `ProtectHome=read-only`, so the exporter
can read permitted files under `/home` without writing there. Its state directory
and private temporary directory remain writable. The configuration is a
root-owned, mode 0600 Debian conffile. It uses systemd EnvironmentFile syntax,
not shell syntax. Add optional flags via `EXPORTER_ARGS`, for example
`EXPORTER_ARGS="--evm-metrics --replica-metrics --peer-latency"`.
Systemd splits these arguments without invoking a shell.
If enabling `--binary-metrics`, also set `BINARY_HOME` to the directory containing
the existing `hl-visor` executable. Its default derives from `HOME`, which the
service sets to private exporter state.

```sh
sudo systemctl enable --now hyperliquid-exporter.service
systemctl status hyperliquid-exporter
journalctl -u hyperliquid-exporter -f
curl --fail http://127.0.0.1:8086/metrics
```

The exporter listens on `:8086/metrics` as in the existing application. Apply
your host's normal access controls for the monitoring endpoint.

## Upgrade and removal

APT preserves edited configuration as a conffile and try-restarts only an
already running service on upgrade. Stopped/disabled services stay stopped or
disabled. Removal stops the service. Purge removes packaged configuration but
keeps the service account, exporter state and node-directory symlink. It never
removes node data. Delete retained state and accounts only as a separate action.

`tests/container_smoke.py` tests first install, account and permissions,
configuration preservation, running upgrades, stopped reinstalls, removal and purge.
Run it as root only in a fresh disposable Ubuntu 24.04 systemd container. Pass
two packages with increasing versions. It installs and removes the package and
uses a temporary service probe to test the unit's filesystem restrictions.
It verifies the real exporter's local metrics endpoint against fixture node
files, then uses a probe for lifecycle checks. It does not prove node readiness.

Inside that disposable container, with both package files copied to `/tmp`:

```sh
python3 container_smoke.py /tmp/hyperliquid-exporter_2.5.1-1_amd64.deb /tmp/hyperliquid-exporter_2.5.1-2_amd64.deb
```

## Future Cloudsmith publication

Authenticate the Cloudsmith CLI outside this checkout. After validation, upload
the same package bytes to the chosen Dwellir repository:

```sh
cloudsmith push deb dwellir/deb-testing/ubuntu/noble dist/ubuntu/hyperliquid-exporter_2.5.1-1_amd64.deb
# After testing, promote the same artifact:
cloudsmith push deb dwellir/deb-staging/ubuntu/noble dist/ubuntu/hyperliquid-exporter_2.5.1-1_amd64.deb
# After staging validation:
cloudsmith push deb dwellir/deb-stable/ubuntu/noble dist/ubuntu/hyperliquid-exporter_2.5.1-1_amd64.deb
```

Upload the arm64 artifact separately when available. Keep the version and
checksum unchanged when moving through testing, staging and stable. Use a new
Debian revision when package contents change. For release candidates use a
version such as `2.5.2~rc.1-1`, which APT sorts below `2.5.2-1`.

Use each repository's generated APT instructions for signing keys and private
repository authentication, then install with
`sudo apt-get install hyperliquid-exporter=2.5.1-1`. Keep credentials outside
the repository. See the [Cloudsmith Debian repository documentation](https://docs.cloudsmith.com/formats/debian-repository)
for CLI upload syntax and repository-specific consumer setup.
