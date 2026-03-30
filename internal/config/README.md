# config

Centralizes configuration loading from `.env` files, environment variables, and CLI flags into a single `Config` struct.

CLI flags take precedence over environment variables. Covers node paths, chain selection, feature toggles (EVM, replica, validator RTT), metrics addresses, log level, and OTLP settings.
