# cache

Generic thread-safe LRU cache with optional per-entry TTL.

Uses a doubly-linked list plus hashmap for O(1) get/set/delete. Expired entries are lazily evicted on access and can also be bulk-cleaned via `CleanupExpired()`.

Used throughout the exporter for caching contract info, validator data, signer mappings, and parsed file contents.
