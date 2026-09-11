# hl-node Schema Watch

hl-node changes its log formats without notice. Within six months of 2026 it wrapped `current_stakes` in an object, turned the round-advance reason into a tagged object, reshaped four gossip_connections events and added two action types. Each change silently zeroed or froze a metric until someone noticed. This routine catches the next one.

Run it on every hl-node release, and whenever `hl_exporter_parse_errors_total` or `hl_exporter_source_errors_total` rises on a healthy node.

## Signals

Production: `hl_exporter_parse_errors_total{stream,stage}` counts rejected records per stream, `hl_exporter_source_up{stream}` and `hl_exporter_source_sample_age_seconds{stream}` show whether each stream is readable and fresh, and `hl_p2p_gossip_unknown_events_total` counts gossip_connections tags outside the allowlist. Replica actions outside `internal/actiontypes` land in the `other` label. The alert rules on these live in the central alert config, not in this repo.

CI: `TestSchemaFixtures` in `internal/monitors/schema_test.go` runs every consumed stream's parser over the committed samples in `internal/monitors/testdata/schema/<stream>.jsonl` and fails on the first rejected line, unknown gossip tag or unknown action type. The committed samples are trimmed and constructed from real line shapes; refresh them from a live pull when a shape changes.

## Steps

1. On a mainnet and a testnet node running the new hl-node, pull the newest file of each consumed stream into the gitignored live directory. The test globs `testdata/live/<stream>*.jsonl`, so suffix the name with the network and date:

   ```sh
   H=/path/to/hl NET=mainnet D=$(date -u +%Y%m%d)
   L=internal/monitors/testdata/live; mkdir -p $L
   newest() { find "$1" -type f | sort | tail -n1; }
   cp "$(newest $H/data/node_fast_block_times)"          $L/node_fast_block_times-$NET-$D.jsonl
   cp "$(newest $H/data/node_slow_block_times)"          $L/node_slow_block_times-$NET-$D.jsonl
   cp "$(newest $H/data/node_logs/consensus/hourly)"     $L/consensus-$NET-$D.jsonl
   cp "$(newest $H/data/node_logs/status/hourly)"        $L/status-$NET-$D.jsonl
   cp "$(newest $H/data/node_logs/status/hourly)"        $L/validator_status-$NET-$D.jsonl
   cp "$(newest $H/data/replica_cmds)"                   $L/replica_cmds-$NET-$D.jsonl
   cp "$(newest $H/data/evm_block_and_receipts/hourly)"  $L/evm_block_and_receipts-$NET-$D.jsonl
   cp "$(newest $H/data/node_logs/gossip_rpc/hourly)"    $L/gossip_rpc-$NET-$D.jsonl
   cp "$(newest $H/data/node_logs/gossip_connections/hourly)" $L/gossip_connections-$NET-$D.jsonl
   cp "$(newest $H/data/node_logs/tcp_traffic/hourly)"   $L/tcp_traffic-$NET-$D.jsonl
   ```

   `consensus`, `status`, `validator_status` and `replica_cmds` exist only on validator nodes. `block_times` is the pre-dual-state legacy layout and has no current source.

2. Run the drift test:

   ```sh
   go test ./internal/monitors/ -run TestSchemaFixtures -count=1
   ```

   Every failure names the file and line. A `json` stage means a torn or non-JSON line, usually the last line of a file still being written; trim it. Any other stage, an unknown gossip tag or an unknown action type is a real shape change.

3. For each shape change: fix the parser so it accepts both the old and the new shape, add a test case in each shape next to the existing ones, copy about 20 representative lines into the committed `testdata/schema/<stream>.jsonl`, and record it in the changelog. Also check upstream's `CHANGELOG.md` and `HL_NODE_METRICS_AUDIT.md` (`git show upstream/main:<file>` after the [upstream sync](upstream-sync.md) fetch step) for their handling of the same change; they track hl-node closely and often land the fix first.

4. Delete or keep the live files; they are gitignored either way.
