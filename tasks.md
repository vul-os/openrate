# openrate — tasks

## Done
- [x] Currency graph + BFS all-pairs materialization (no single canonical base)
- [x] ECB daily source (live), SARB source (stub), pluggable `Source` interface
- [x] Ingest loop + snapshot store, refresh interval configurable
- [x] JSON API: `/rates`, `/convert`, `/meta`, `/healthz` with per-rate provenance
- [x] React JSX UI (converter + rates table + freshness), embedded via `go:embed`
- [x] Logo, README, CLOUD.md (cloud deferred to Vulos Cloud)

## Sources roadmap (the "open way" — see SOURCES.md for the full survey)
- [ ] **SARB live** — authoritative ZAR pairs; wire the web statistical query/web service
- [ ] **Crypto WebSocket** — Binance/Coinbase/Kraken public feeds: free, real-time, 24/7
- [ ] **More central banks** — Fed H.10, BoE, BoC Valet, SNB, RBA, IMF SDR (daily, free files)
- [ ] **Scraper source** — generic HTML/JSON scraper behind the `Source` interface for
      banks that only publish to a page (robots/ToS-gated; see SOURCES.md §scraping)

## Freshness roadmap (how to get below daily)
- [ ] **Hourly** — re-fetch open files hourly + add intraday-capable sources; today's
      free fiat is daily ECB underneath, so hourly needs more than re-polling ECB
- [ ] **Push/streaming** — move ingest from poll to push for crypto WS now, fiat later;
      re-materialize the matrix on tick (throttled), don't poll
- [ ] **Staleness alerts** — flag a source past its expected cadence (engine has `as_of`)

## Engine hardening
- [ ] History: persist daily snapshots for `?date=` and time-series (Cloud owns long retention)
- [ ] `/api/v1/pairs/{from}/{to}` convenience route + bulk convert
- [ ] Tests: graph BFS correctness, triangulation-vs-direct preference, ECB XML parse
- [ ] Dockerfile + fly.toml (match sibling Vulos services)
- [ ] Backfill ECB 90-day file on boot for non-empty history

## Cloud
See [CLOUD.md](CLOUD.md) — hosted/multi-tenant absorbed into Vulos Cloud, not here.
