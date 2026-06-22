# Open exchange-rate sources

A catalog of ways to get rates **the open way** — free and self-servable,
without paying a commercial rate API. Status reflects live probes from this repo
(June 2026). "Open" tiers run cleanest → most fragile.

## Implemented

| Source | Status | Format | Cadence | ZAR? | Auth | Notes |
|---|---|---|---|---|---|---|
| **ECB** daily file | ✅ live | static XML | daily ~16:00 CET, weekdays | via EUR | none | Canonical free fiat reference; backbone of most "free" APIs. `eurofxref-daily.xml`. |
| **Coinbase** exchange-rates | ✅ live | JSON | real-time, 24/7 | **direct** | none | `api.coinbase.com/v2/exchange-rates?currency=USD`. Real fiat (incl. ZAR=16.44) + crypto. **Best open intraday fiat source.** |
| **Luno** tickers | ✅ live | JSON | real-time, 24/7 | **direct (crypto)** | none | SA exchange. `api.luno.com/api/1/tickers`. BTC/ZAR, ETH/ZAR — live ZAR signal, bridges to fiat via BTC. |
| **Frankfurter** | ✅ live (opt-in) | JSON | daily (ECB mirror) | via EUR | none | `api.frankfurter.dev/v1/latest`. Clean JSON ECB mirror; redundant with ECB so off by default. |
| **Yahoo Finance** | ⚠️ implemented, opt-in | JSON (unofficial) | ~1 min, market hours | direct | none | `query1.finance.yahoo.com/v8/finance/chart/USDZAR=X`. **Rate-limits aggressively per IP** (HTTP 429 from shared egress here). ToS-gray. Off by default. |
| **SARB** | 🟡 stub | — | daily | **authoritative** | — | South African Reserve Bank — the official domestic ZAR reference. Endpoint wiring pending (see below). |

## How freshness actually works here

The graph prefers the **freshest direct edge**, so once Coinbase is enabled,
`USD→ZAR` resolves in **1 hop, seconds old** instead of ECB's day-old EUR cross.
Verified: with `ecb,coinbase,luno`, `USD→ZAR` = 16.441 via `[USD,ZAR]`, age ~4s.

- **Daily fiat, open:** ECB (+ Frankfurter mirror).
- **Intraday/real-time fiat, open:** Coinbase (free, no auth, includes ZAR, 24/7).
  This is the answer to "open hourly conversions" — re-polling ECB hourly is
  pointless (it changes daily); Coinbase moves continuously.
- **Authoritative ZAR:** SARB (official) + Luno (live SA market) + Coinbase (live).

## Probed but not used

| Source | Result |
|---|---|
| **exchangerate.host** | ✗ now requires `access_key` (became keyed/paid). |
| **Stooq** CSV (`stooq.com/q/l/?s=usdzar...`) | ✗ endpoint returned "page does not exist" / geo-blocked from here. Revisit; was historically a free FX CSV. |

## Not yet wired (open, worth adding)

- **More central-bank files** (daily, free, no scraping): US Fed H.10, Bank of
  England, Bank of Canada **Valet** API, SNB, RBA, IMF SDR.
- **More crypto venues with fiat books**: Kraken, Bitstamp (USD/EUR/GBP real-time).
- **Wise** mid-market rates (`api.wise.com`, free token) — close to interbank.

## Scraping tier (fallback only)

Real HTML scraping (xe.com, x-rates.com, investing.com, SA bank pages) is a
**last resort** — fragile and usually ToS-prohibited (check robots.txt + terms).
Everything above is a file or free API, so scraping should be rare, not the
strategy. The cleanest "scrape" is an unofficial JSON endpoint (Yahoo), already
implemented behind an opt-in flag.

> Probes run from a shared sandbox IP; Yahoo/Stooq results may differ from a
> normal host. A deeper verification sweep is tracked in `tasks.md`.
