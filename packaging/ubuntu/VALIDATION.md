# Package validation, 2026-10-08

Validated on Ubuntu 24.04 amd64 against source based on commit `f5c08f0`,
including the package provisioning and independent exporter-state changes.

- `make test RACE=1` passed across all Go packages.
- `make test-deb` passed all four archive tests.
- A source build produced `hyperliquid-metrics-exporter_2.5.1-1_amd64.deb` using Go 1.26.7
  from `go.mod`. The extracted executable reported version 2.5.1 and the full
  source commit. Its package checksum verified successfully.
- Actionlint 1.7.11 accepted `.github/workflows/debian.yml`.
- In a fresh unprivileged Ubuntu 24.04 LXD container,
  `tests/container_smoke.py` passed initial install, real `/metrics` serving,
  disk usage at a custom private node directory, service account checks and
  `systemd-analyze verify`. The test confirmed APT created the correctly owned
  mode 0700 state directory before any service startup, without manual setup.
  A simulated principal charm only wrote configuration and granted node ACLs.
- The same container passed filesystem permission probes, upgrade from
  `2.5.1-1` to `2.5.1-2` while running, reinstall while stopped, configuration
  preservation, stop on removal and purge. Exporter state, the service account
  and fixture node files survived purge. The service could write exporter state
  but could not write node files or read its root-only configuration. The upgrade fixture
  reused the extracted source-built executable with a new Debian revision.
- Configuration tests check EXPORTER_STATE_DIR with nil, empty and node-path
  override flags, plus the legacy cache location when it is unset. Archive
  tests confirm the renamed APT package and unchanged service/binary names.
- The installed documentation includes the principal-charm integration contract.
  Both referenced charms were inspected to establish runtime paths, ACL grants,
  legacy unit precedence and scrape-publication responsibilities.
- The configuration reference covers all 20 declared start flags. Systemd
  successfully parsed the commented template with all six active settings at
  their original defaults. A rebuilt archive contained the exact template as
  the mode 0600 conffile and mode 0644 reference copy in the documentation.

Arm64 package construction is covered by the new native CI job and was not
run locally. No package was uploaded to Cloudsmith. The tests do not prove
Hyperliquid synchronization or production node permissions.

Debhelper emits an undefined `${shlibs:Depends}` warning for the static Go
source build because there are no shared libraries. The resulting dependency
list contains `adduser`, `ca-certificates` and `systemd (>= 249)`. Archive tests
also package a dynamically linked fixture and confirm a libc dependency.
