# hyperliquid-api

Client for the Hyperliquid API, primarily used to fetch validator summaries (address, signer, name, stake, jail status).

Maintains three indexed views (summaries list, signer-to-validator map, validator-to-info map) behind an RWMutex. Responses are cached with a 1-minute freshness threshold and stale data is served as a fallback on API failure. Retries use quadratic backoff.
