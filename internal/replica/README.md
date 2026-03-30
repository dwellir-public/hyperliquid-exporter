# replica

Parses JSON-formatted `replica_cmds` files containing blockchain actions and blocks.

Extracts block-level metrics (height, round, time, proposer) and aggregates action counts by type (orders, cancels, EVM transactions, transfers, delegation, etc.). Uses `sync.Pool` for `ReplicaBlock` object reuse and `json.RawMessage` for lazy parsing of type-specific action data.
