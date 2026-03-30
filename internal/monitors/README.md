# monitors

Collection of independent monitoring goroutines, each tracking a specific aspect of the Hyperliquid node.

Monitors include: block times, consensus participation (QC/TC signatures with sliding windows), EVM blocks and gas, proposals, validator status and RTT, gossip/heartbeat activity, software version, replica command streaming, and round advances.

Each monitor runs in its own goroutine with context-based cancellation and reports errors through dedicated channels. File-based monitors use buffered I/O and timestamp tracking to efficiently detect and process new data.
