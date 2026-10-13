# Package provisioning and charm integration

The user requested package changes and a documented integration contract,
with the principal charm migration deferred to a separate PR.

1. Inspect the existing subordinate's account, unit, environment-file and
   telemetry behavior, and the principal's runtime-home and ACL helpers.
2. Add regression coverage for independent peer-state configuration and
   package naming/configuration. Confirm tests fail before implementation.
3. Name the APT package `hyperliquid-metrics-exporter`, keeping the existing
   binary/service name. Provision the private state directory in `postinst`.
   Use `/etc/default/hyperliquid-exporter` and direct charm runtime paths.
4. Add `EXPORTER_STATE_DIR` with backward-compatible standalone behavior and
   use it for peer persistence. Remove the packaging symlink workaround and
   its disk-monitor change from the PR.
5. Rewrite installation instructions around APT and charm-owned node
   configuration. Document ACL, telemetry, lifecycle and legacy-unit migration
   requirements in `packaging/ubuntu/charm-integration.md`.
6. Update archive/container tests, run Go and package checks, rebuild the real
   package and verify automatic provisioning and custom runtime paths in a
   fresh Ubuntu systemd container. Review, then push to draft PR #12.

The package owns account, private state, unit and binary. The principal charm
owns the repository credentials, selected version, node-specific environment,
read grants, service policy and scrape relations. No charm files or deployed
applications change in this PR.
