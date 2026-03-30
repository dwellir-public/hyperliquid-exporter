# abci

Reads and parses ABCI state files from the Hyperliquid node using MessagePack encoding.

Provides chain context (height, time, hardfork version), EVM account counts, and validator profiles (address, moniker, IP) by selectively decoding only the necessary nested fields from large state files.

Includes an LRU-based cache (100 entries, 1h TTL) keyed by file path and modification time to avoid redundant reads.
