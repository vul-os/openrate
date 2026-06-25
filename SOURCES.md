# Open exchange-rate sources

A catalog of ways to get rates **the open way** — free and self-servable without
a paid commercial rate API, including what's reachable by scraping. Verified live
June 2026 (probes from this repo + a research sweep). Spot check across sources:
**USD/ZAR ≈ 16.44–16.48** (consistent). Base/quote direction varies per source —
always read the source's stated convention and `date`.

> openrate prefers the **freshest direct edge** in the graph. With Coinbase +
> SARB enabled, `USD→ZAR` resolves to the live Coinbase quote (~seconds old)
> while `EUR/GBP/JPY→ZAR` resolve to SARB's authoritative direct quotes — each
> chosen automatically, no special-casing.

## Implemented (`-sources`)

| Source | Default | Status | Format | Cadence | ZAR | Auth | Notes |
|---|---|---|---|---|---|---|---|
| **ecb** | ✅ | live | static XML | daily ~16:00 CET, weekdays | via EUR | none | Canonical free fiat reference. `eurofxref-daily.xml`. |
| **coinbase** | ✅ | live | JSON | real-time, 24/7 | **direct** | none | `v2/exchange-rates?currency=USD`. Best open intraday fiat (incl. ZAR) + crypto. Indicative (~minute), not tick. |
| **luno** | ✅ | live | JSON | real-time, 24/7 | **direct (crypto)** | none | SA exchange. `api/1/tickers`. BTC/ETH/USDT/USDC vs ZAR; bridges to fiat via BTC. WS needs a key — we poll REST. |
| **sarb** | ✅ | live | JSON | daily | **authoritative** | none | `SarbWebApi/WebIndicators/HomePageRates`. Official ZAR per USD/GBP/EUR/JPY. **Slow/flaky host** — bounded dialer + 3 retries. |
| **frankfurter** | — | live | JSON | daily | via EUR | none | `api.frankfurter.dev/v1/latest`. Clean ECB JSON mirror (host moved `.app`→`.dev`). Self-hostable. |
| **erapi** | — | live | JSON | daily **incl. weekends** | direct | none | `open.er-api.com/v6/latest/USD`. Fills the ECB Fri→Mon gap. |
| **fawazahmed0** | — | live | JSON (CDN) | daily | direct | none | jsDelivr + pages.dev fallback, ~400 currencies, no limits. |
| **boc** | — | live | JSON | daily | direct | none | Bank of Canada Valet, base CAD. Cleanest central-bank REST; independent cross-check. |
| **yahoo** | — | ⚠️ | JSON (unofficial) | ~1 min | direct | none | `query1.../v8/finance/chart/USDZAR=X`. **ToS-prohibited** (robots.txt disallows crawlers incl. ClaudeBot), IP-rate-limited. Last resort only. |

Default set: `ecb,coinbase,luno,sarb`. Enable extras with
`-sources ecb,coinbase,luno,sarb,erapi,fawazahmed0,boc`.

## Ranked recommendations

**Broad daily coverage (open, no key):** Frankfurter (ECB-backed, self-hostable) →
fawazahmed0 (best uptime, dual-CDN) → open.er-api (weekend coverage). Cross-check
with ECB XML + Bank of Canada Valet.

**Intraday / hourly the open way:** Coinbase `v2/exchange-rates` (multi-fiat incl.
ZAR, one no-auth call) as baseline; **Luno/VALR ZAR legs ÷ Kraken/Bitstamp USD legs**
for true real-time crosses. This is the only *fully open* intraday path — re-polling
ECB hourly is pointless (it changes daily).

**Authoritative ZAR:** SARB Web API (official daily) → Luno `XBTZAR`/`USDTZAR` (live
24/7) → ECB `ZAR` + BoC `FXZARCAD` (independent daily cross-checks).

## Verified but not used

| Source | Why |
|---|---|
| **exchangerate.host** | now needs `access_key` (apilayer, 100 req/mo free) — dropped from no-key list |
| **Wise** `api.wise.com/v1/rates` | true mid-market, but `/rates` is gated to **Affiliate partners**; a free personal token won't reach it |
| **Stooq** CSV | endpoint 404 + JS proof-of-work anti-bot now; `robots.txt: Disallow: /` — no longer reliably open |
| **IMF** SDMX | header-gated (403/501), and ZAR is not in the SDR basket (only via IFS dataflow) — finicky |

## Not yet wired (open, worth adding — see tasks.md)

- **More crypto venues with fiat books:** Kraken (`XXBTZUSD`, USD/EUR/GBP legs, no ZAR),
  Bitstamp (USD/EUR/GBP + direct `eurusd`/`gbpusd`), VALR (SA, ZAR — Luno failover),
  Binance (`EURUSDT`/`GBPUSDT`; ZAR symbols are geo/regulation-dependent — verify).
- **More central-bank files:** US Fed H.10 (script-unfriendly `.aspx`; easier via FRED
  `DEXSFUS` with a free key), Bank of England IADB (`XUDLZRD`; Akamai-gated, needs real
  UA), SNB (`data.snb.ch`, mind per-100 unit scaling), RBA F11 CSV.

## Scraping tier — fallback only, legally flagged

Real HTML scraping is a **last resort** — fragile and usually ToS-prohibited:

- **Yahoo Finance** v8 chart works no-auth and gives 1-min FX, **but ToS prohibits
  automated extraction and robots.txt disallows crawlers** — implemented behind an
  opt-in flag with that warning; do not enable unless your use is permitted.
- **x-rates.com** — scrapeable (table paths allowed by robots), but delayed/low-cadence; medium risk.
- **xe.com, investing.com, Google Finance** — scraping explicitly prohibited / aggressive
  anti-bot / litigious history. **Do not scrape.**
- **SA retail banks** (Standard Bank, FNB, Nedbank, Absa) — indicative FX only as HTML/PDF
  marketing pages, no public API; not viable engine sources.

Everything in the implemented + recommended sections is a file or free API, so real
scraping should be rare, not the strategy.

---

# Open interest-rate sources

A separate engine (interest rates are flat time series, not a currency graph) served
under `/api/v1/interest/*`. Same philosophy: open data first, optional keys for more
depth, every series carries a confidence grade. Verified live June 2026.

## Implemented (`-interest-sources`)

| Source | Default | Status | Format | Cadence | Coverage | Auth | Notes |
|---|---|---|---|---|---|---|---|
| **bis** | ✅ | live | CSV (SDMX) | ~weekly | **49 central banks' policy rates** + daily history, one call | none | BIS Stats `WS_CBPOL`, daily series, all reference areas. The open backbone for worldwide breadth. Missing days come back as literal `NaN` — skipped. |
| **sarbrates** | ✅ | live¹ | JSON | daily | ZA ZARONIA overnight + 1W/1M/3M/6M/9M/12M compounded + index, deep history | none | Port of the standalone amortini scraper's ZARONIA path. `resbank.co.za/bin/sarb/ratereform`. **Slow/flaky host** — bounded dialer + 3 retries. |
| **fred** | key | live | JSON | daily | US SOFR/EFFR/OBFR/prime/2Y/10Y Treasury, deep history | `OPENRATE_FRED_API_KEY` (free) | Auto-enables when the key is present. The "bring your own key for more datapoints" path; enriches the open BIS breadth with high-frequency US benchmarks. |

¹ `sarbrates` is faithful to the proven scraper but the SARB host is geo/latency-restricted
from some networks (incl. this sandbox); it resolves once deployed where SARB is reachable.

Default set: `bis,sarbrates`. `fred` adds itself when its key is set. Enable explicitly
with `-interest-sources bis,sarbrates,fred`.

## Coverage today

- **Policy rates:** 49 areas (AR AT AU BE BR CA CH CL CN CO CZ DE DK ES FR GB GR HK HR HU
  ID IL IN IS IT JP KR KW MA MK MX MY NL NO NZ PE PH PL PT RO RS RU SA SE TH TR US XM ZA)
  via a single BIS call, each with daily history.
- **Reference rates:** ZA ZARONIA family (sarbrates) and US SOFR/EFFR/OBFR (fred).
- **Lending / bond:** US prime + 2Y/10Y Treasury (fred).

## Worth adding next (open or keyed — trivial via the registry)

- **ECB Data Portal** (SDMX): €STR, EURIBOR, ECB policy facilities — base `xm`.
- **Bank of England** IADB: SONIA, Bank Rate. **Bank of Canada** Valet: CORRA, overnight.
- **OECD / IMF IFS** (SDMX): lending & deposit rates for ~150–200 economies (lower
  frequency) — fills the long tail beyond BIS's policy-rate set.
- **Direct national feeds** for the markets where you want issuer-grade (rank 4) depth
  rather than BIS's aggregator view (rank 3).
- **Commercial keyed APIs** (interestratesapi.com, Eulerpool, Twelve Data) as key-gated
  enrichment for intraday / very deep history.

Each new provider is one file implementing `ratesources.Source` (`Name()` + `Fetch()`)
plus a line in `ratesources/registry.go` — same plugin shape as the FX sources.
