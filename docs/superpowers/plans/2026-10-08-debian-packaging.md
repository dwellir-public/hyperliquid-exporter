# Debian packaging implementation plan

**Goal:** Build an installable exporter package using the Taiko Ubuntu packaging pattern and open a draft PR.

**Architecture:** A Python builder compiles a native Go binary and stages a debhelper package in a temporary directory. A dedicated systemd account has private exporter state and read-only access to operator-provisioned node files. CI builds packages; Cloudsmith upload remains a documented manual operation.

**Tools:** Python standard library, Go, debhelper 13, dpkg, systemd, GitHub Actions.

- [ ] Add archive tests using a native fixture executable. Check metadata, ownership, conffiles, service lifecycle scripts, provenance, duplicate-output protection and invalid inputs. Run before adding the builder.
- [ ] Add `packaging/ubuntu/build.py`, Debian templates and exporter service/config. Compile with the go.mod toolchain, locked modules, version/commit/time metadata and CGO disabled. Reject unsupported build hosts and mismatched binary architectures.
- [ ] Add `make deb` and `make test-deb`, ignore generated artifacts and add amd64/arm64 package CI with native binary smoke checks.
- [ ] Document installation, node-file permissions, the node symlink used for peer-cache placement, lifecycle behavior and manual uploads to Dwellir's three Cloudsmith repositories.
- [ ] Run archive tests, Go tests, a source package build, extracted binary version checks and workflow validation. Test install, upgrade, remove and purge in a disposable Ubuntu systemd container if available.
- [ ] Review the diff, commit and push the branch, and create a draft PR with validation results and limitations.

The package targets Ubuntu 24.04, matching the reference. Initial installation leaves the service stopped and disabled. Upgrades preserve conffiles and only try-restart an already running service. Removal stops it; purge retains the service account and private state. No metric definitions change. The disk monitor resolves the root NODE_HOME symlink before walking so the packaged deployment reports real disk usage. The README provides repository-specific upload commands based on Cloudsmith's Debian format documentation.
