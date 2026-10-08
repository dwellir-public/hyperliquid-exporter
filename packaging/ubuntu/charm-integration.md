# Hyperliquid charm integration contract

The intended deployment is one principal `hyperliquid` charm that installs
`hyperliquid-metrics-exporter` through APT and configures its packaged
`hyperliquid-exporter.service`. The package replaces binary downloading, account
creation and unit templating from the `hyperliquid-metrics-exporter` subordinate.
The principal charm migration is a separate change.

## Ownership

| Responsibility | Owner |
| --- | --- |
| `/usr/bin/hyperliquid-exporter` and `/usr/lib/systemd/system/hyperliquid-exporter.service` | Package |
| `hyperliquid-exporter` account and `/var/lib/hyperliquid-exporter`, mode 0700 | Package |
| Initial `/etc/default/hyperliquid-exporter`, mode 0600 | Package conffile |
| Cloudsmith repository/key/authentication and selected package version | Principal charm provisioning |
| Actual node paths, network, exporter flags and conffile contents | Principal charm |
| Read access to node files and traversal of their ancestors | Principal charm |
| Exporter start/stop, enablement and configuration reconciliation | Principal charm |
| Scrape relations, Juju topology labels and port access | Principal charm |

The binary and service names deliberately remain `hyperliquid-exporter`.
The APT package name is `hyperliquid-metrics-exporter`. The service uses a
dedicated account, whereas the existing subordinate runs as `hyperliquid`.
The principal charm must grant read access rather than sharing node ownership.

## Configuration

Write this root-owned mode 0600 environment file atomically to
`/etc/default/hyperliquid-exporter`, using the resolved runtime home from the
principal charm's stored state. For the default runtime home:

```ini
CHAIN=mainnet
NODE_HOME=/home/hyperliquid/hl
NODE_BINARY=/home/hyperliquid/hl-node
BINARY_HOME=/home/hyperliquid
EXPORTER_STATE_DIR=/var/lib/hyperliquid-exporter
EXPORTER_ARGS="--evm-metrics --replica-metrics --peer-latency"
```

Set `CHAIN` from the charm's `network`. Set `NODE_HOME` to `<runtime_home>/hl`,
`NODE_BINARY` to `<runtime_home>/hl-node`, and `BINARY_HOME` to `<runtime_home>`.
`data-dir` can place the runtime home outside `/home/hyperliquid`; use the
resolved value rather than guessing from the node service's user home.
Keep `EXPORTER_STATE_DIR` at the package default. No symlink is needed and the
exporter never needs write access to node files for peer persistence.

Enable optional metrics deliberately, preserving the chosen subordinate
`service-args` during migration. Translate `METRICS_EXPORTER_CLI_ARGS` into
`EXPORTER_ARGS`, extracting `--chain` into `CHAIN`. Node-path flags remain
charm-managed. Reject line breaks/NUL and serialize values for systemd's
EnvironmentFile syntax, without executing a shell. The unit supplies `HOME`
and the default state directory independently of the node's runtime home.

## Node access and lifecycle

After APT has created the exporter account, the principal charm grants it
read/search ACLs for consumed node files, plus traversal of runtime-home
ancestors. Default ACLs on node directories cover newly created files.
Reconcile access when logs/directories or node binaries are recreated.
Exclude `.gnupg` and other private node credentials. The existing
`HyperliquidNodeProvider` already has ACL-based grants that exclude `.gnupg`;
its reusable access logic can support the exporter without a subordinate
relation. Preserve grants for other consumers such as nanoreth.

The charm must not need to create the exporter account or state directory,
install downloader tools, copy a binary, or generate a unit. APT owns those
tasks. Check the configured node directory is readable before enabling the
exporter and verify `http://127.0.0.1:8086/metrics` after startup.

On first deployment, install, configure, grant access, then enable/start the
exporter. Later config changes restart only an already running exporter.
Package upgrades preserve edited configuration and try-restart a running
service; stopped services stay stopped. `upgrade-charm` must preserve the
node charm's existing rule against workload reconfiguration or restarts.
Node start/stop actions and exporter lifecycle need an explicit policy in the
principal charm; never restart the blockchain node just to change monitoring.

## Migration from the subordinate

The subordinate owns `/etc/systemd/system/hyperliquid-exporter.service`,
`/etc/default/hyperliquid-exporter` and
`/home/hyperliquid/hyperliquid-exporter`. Its unit under `/etc` overrides the
package unit under `/usr/lib`. APT deliberately does not delete an
administrator-owned unit or a node-owned executable.

The principal charm migration must therefore:

1. Preserve the selected exporter arguments, network, node paths and desired
   running/stopped state. Arrange Cloudsmith APT access and version selection.
2. Move `hyperliquid-prometheus` and `cos-agent` scrape publication into the
   principal charm, including the existing labels and metrics endpoint on 8086.
   Keep consumers scraping exactly once during the switch.
3. Retire the subordinate and stop its exporter before taking over that
   service. Ensure it can no longer rewrite the unit or environment file.
4. Remove only the identified legacy exporter unit from `/etc/systemd/system`
   and reload systemd. Retire the legacy exporter binary separately, without
   changing node binaries or node data. Account for any old exporter drop-ins.
5. Install `hyperliquid-metrics-exporter`, write the new environment file,
   reconcile node read access and verify the effective unit comes from
   `/usr/lib/systemd/system`. Handle APT's pre-existing conffile prompt using
   the charm's normal noninteractive configuration policy.
6. If preserving peer history, copy the legacy cache from
   `<runtime_home>/.hyperliquid-exporter/peers.json` into package state and
   set ownership to `hyperliquid-exporter`. Preserve the original until
   verification succeeds.
7. Restore the intended service state and verify the local metrics endpoint
   and downstream scrape relations. Do not remove the principal charm's
   `local-hyperliquid-node` interface used by other consumers.

These steps describe the future charm migration. This package does not alter
deployed Juju applications or install Cloudsmith credentials.
