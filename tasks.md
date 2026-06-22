# openrate — tasks

## Done
- [x] Currency graph + BFS all-pairs materialization (no single canonical base)
- [x] ECB daily source (live), SARB source (stub), pluggable `Source` interface
- [x] Ingest loop + snapshot store, refresh interval configurable
- [x] JSON API: `/rates`, `/convert`, `/meta`, `/healthz` with per-rate provenance
- [x] React JSX UI (converter + rates table + freshness), embedded via `go:embed`
- [x] Logo, README, CLOUD.md (cloud deferred to Vulos Cloud)
- [x] **Per-rate accuracy grade** (A–D + confidence) on every price: freshness, directness,
      source authority, cross-source corroboration, currency caveats — see ACCURACY.md
- [x] **Web Accuracy page** + grade badge in converter; dropped defunct HRK
- [x] **Store fix** — refresh no longer holds the lock during I/O; progressive materialization
- [x] **Vulos design system** — themed UI (near-black/teal/Inter), redesigned converter + accuracy + nav
- [x] **Anti-scraping** — per-IP token-bucket rate limiter (`-ratelimit`), robots.txt (Disallow /api/), security headers

## Sources (the "open way" — see SOURCES.md for the full survey)
- [x] **Coinbase** — free/no-auth real-time fiat incl. ZAR (best open intraday source)
- [x] **Luno** — SA exchange, live BTC/ZAR & ETH/ZAR, bridges to fiat via BTC
- [x] **Frankfurter** — JSON ECB mirror (opt-in)
- [x] **Yahoo Finance** — unofficial ~1min quotes (opt-in; IP-rate-limited)
- [x] **Source registry + `-sources` flag** — pick the enabled set
- [x] **SARB live** — authoritative ZAR (USD/GBP/EUR/JPY) via SarbWebApi; bounded dialer + retries for the slow host
- [x] **open.er-api** — daily incl. weekends (fills ECB Fri→Mon gap)
- [x] **fawazahmed0** — ~400 currencies, dual-CDN, no limits
- [x] **Bank of Canada Valet** — clean REST cross-check (FXZARCAD)
- [ ] **More crypto venues** — Kraken/Bitstamp USD-EUR-GBP legs, **VALR** (SA ZAR, Luno failover), Binance (verify ZAR symbols)
- [ ] **More central banks** — Fed H.10 (via FRED key), BoE IADB `XUDLZRD`, SNB cubes, RBA F11
- [ ] **Wise** mid-market — needs Affiliate-partner auth (not free personal token); revisit if onboarded
- [ ] **Generic scraper source** — HTML/JSON scraper for page-only banks (robots/ToS-gated)
- [ ] **SARB resilience** — cache last-good across restarts (host drops TCP connects intermittently)

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
