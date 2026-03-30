# exporter

Central orchestrator that initializes and launches all monitoring goroutines.

`Start()` is the single entry point: it selectively starts 16+ monitors based on configuration, wires up error channels, and handles graceful shutdown via context cancellation. Monitor startup order matters -- validator API data is populated before monitors that depend on signer mappings.
