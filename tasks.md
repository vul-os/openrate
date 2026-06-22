# openrate — tasks

## Done
- [x] Currency graph + BFS all-pairs materialization (no single canonical base)
- [x] ECB daily source (live), SARB source (stub), pluggable `Source` interface
- [x] Ingest loop + snapshot store, refresh interval configurable
- [x] JSON API: `/rates`, `/convert`, `/meta`, `/healthz` with per-rate provenance
- [x] React JSX UI (converter + rates table + freshness), embedded via `go:embed`
- [x] Logo, README, CLOUD.md (cloud deferred to Vulos Cloud)

## Sources (the "open way" — see SOURCES.md for the full survey)
- [x] **Coinbase** — free/no-auth real-time fiat incl. ZAR (best open intraday source)
- [x] **Luno** — SA exchange, live BTC/ZAR & ETH/ZAR, bridges to fiat via BTC
- [x] **Frankfurter** — JSON ECB mirror (opt-in)
- [x] **Yahoo Finance** — unofficial ~1min quotes (opt-in; IP-rate-limited)
- [x] **Source registry + `-sources` flag** — pick the enabled set
- [ ] **SARB live** — authoritative ZAR pairs; wire the web statistical query/web service
- [ ] **More central banks** — Fed H.10, BoE, BoC Valet, SNB, RBA, IMF SDR (daily, free files)
- [ ] **More crypto venues** — Kraken/Bitstamp fiat books; **Wise** mid-market (free token)
- [ ] **Stooq** — revisit free FX CSV (endpoint 404'd from sandbox IP)
- [ ] **Generic scraper source** — HTML/JSON scraper for page-only banks (robots/ToS-gated)

## Freshness roadmap (how to get below daily — partly DONE)
- [x] **Intraday the open way** — Coinbase gives real-time fiat incl. ZAR; graph prefers
      the fresh direct edge, so `USD→ZAR` is now ~seconds old, not ECB's day-old cross
- [ ] **Push/streaming** — move ingest from poll to push (crypto WebSocket now, fiat later);
      re-materialize the matrix on tick (throttled), don't poll
- [ ] **Per-source refresh** — fast tick for Coinbase/Luno, daily for ECB (today: one interval)
- [ ] **Staleness alerts** — flag a source past its expected cadence (engine has `as_of`)

## Engine hardening
- [ ] History: persist daily snapshots for `?date=` and time-series (Cloud owns long retention)
- [ ] `/api/v1/pairs/{from}/{to}` convenience route + bulk convert
- [ ] Tests: graph BFS correctness, triangulation-vs-direct preference, ECB XML parse
- [ ] Dockerfile + fly.toml (match sibling Vulos services)
- [ ] Backfill ECB 90-day file on boot for non-empty history

## Cloud
See [CLOUD.md](CLOUD.md) — hosted/multi-tenant absorbed into Vulos Cloud, not here.
