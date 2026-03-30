# metrics

Central metrics infrastructure built on OpenTelemetry with Prometheus and optional OTLP exporters.

Defines 80+ instruments across categories (L1 core, consensus, validator latency, P2P, EVM, memory, machine). Global `currentValues` and `labeledValues` maps track state for async OTel callbacks. A cleanup loop runs every 30s, capping labeled values at 100 per metric to prevent unbounded cardinality growth.

Also manages validator identity resolution (signer-to-validator mapping) and node identity metadata exposed as resource attributes.
