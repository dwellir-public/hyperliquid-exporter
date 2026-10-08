# Package validation, 2026-10-08

Validated on Ubuntu 24.04 amd64 against source based on commit `1ca7b57`,
including the packaging changes and root NODE_HOME symlink fix in this PR.

- `make test` passed across all Go packages.
- `make test RACE=1` passed across all Go packages.
- `make test-deb` passed all four archive tests.
- A source build produced `hyperliquid-exporter_2.5.1-1_amd64.deb` using Go 1.26.7
  from `go.mod`. The extracted executable reported version 2.5.1 and the full
  source commit. Its package checksum verified successfully.
- Actionlint 1.7.11 accepted `.github/workflows/debian.yml`.
- In a fresh unprivileged Ubuntu 24.04 LXD container,
  `tests/container_smoke.py` passed initial install, real `/metrics` serving,
  disk usage through the node-directory symlink, service account checks and
  `systemd-analyze verify`.
- The same container passed filesystem permission probes, upgrade from
  `2.5.1-1` to `2.5.1-2` while running, reinstall while stopped, configuration
  preservation, stop on removal and purge. Private state, the node symlink,
  service account and fixture node files survived purge. The upgrade fixture
  reused the extracted source-built executable with a new Debian revision.
- The disk symlink regression failed before the fix and passed afterward.
  It also verifies that links inside NODE_HOME remain unfollowed.

Arm64 package construction is covered by the new native CI job and was not
run locally. No package was uploaded to Cloudsmith. The tests do not prove
Hyperliquid synchronization or production node permissions.

Debhelper emits an undefined `${shlibs:Depends}` warning for the static Go
source build because there are no shared libraries. The resulting dependency
list contains `adduser`, `ca-certificates` and `systemd (>= 249)`. Archive tests
also package a dynamically linked fixture and confirm a libc dependency.
