# contracts

Resolves EVM contract and token addresses to human-readable names via the Hyperscan API.

Uses a non-blocking design: `GetContractInfo` returns immediately with cached data or "unknown", while a background worker pool (5 goroutines) fetches missing entries asynchronously. Results are stored in a 5000-entry LRU cache with 24h TTL. A dedup window prevents redundant fetches of the same address within 5 minutes.
