<p align="center">
  <img src="assets/openrate.svg" width="84" alt="openrate" />
</p>

<h1 align="center">openrate</h1>

<p align="center">Open, ZAR-anchored exchange rates — the open way.</p>

---

**openrate** is an open-source exchange-rate engine. It ingests rates "the open
way" — from central-bank reference files and free public venue feeds, not by
reselling a paid API — models every currency as a **graph** rather than picking a
single canonical base, and serves an all-pairs JSON API plus an embedded React
UI from a single Go binary.

Part of the [Vulos](https://github.com/vul-os) group. The hosted/multi-tenant
side is absorbed into **Vulos Cloud** — see [CLOUD.md](CLOUD.md).

## Why a graph, not a base

Most rate APIs pick one base currency (usually EUR/USD) and derive everything
through it. openrate keeps each source's quotes in their **native base** (ECB in
EUR, SARB in ZAR, …) as edges in a currency graph. Any pair is the product of
rates along the shortest path between them, so:

- **ZAR is the anchor for free** — it's just the default presentation base, a
  view over the same graph (`?base=ZAR`, or any other).
- **Directly quoted pairs win** — BFS reaches a pair by the fewest hops first, so
  a direct quote always beats a triangulated cross.
- **No single point of contamination** — a bad edge only affects paths through
  it, not every pair.
- **Provenance on every number** — each rate carries `hops`, `as_of`, and `age`,
  so consumers see exactly how stale it is (it matters: fiat is frozen on
  weekends).

## Run

```bash
go run ./cmd/openrate            # serves :8080, base ZAR, hourly refresh
# or
go build -o openrate ./cmd/openrate && ./openrate -addr :8080 -base ZAR -refresh 1h
```

Config via flags or env: `OPENRATE_ADDR`, `OPENRATE_BASE`, `OPENRATE_REFRESH`.

## API

| Endpoint | Description |
|---|---|
| `GET /api/v1/rates?base=ZAR` | All currencies vs base; `rate` reads "1 base = rate CCY" |
| `GET /api/v1/convert?from=USD&to=ZAR&amount=100` | Convert, with rate provenance |
| `GET /api/v1/meta` | Sources, freshness, currency list |
| `GET /healthz` | Liveness |

Every rate includes `hops`, `as_of`, `age_sec`, the `path` and `sources`, plus a
**`quality`** block (grade A–D + confidence) — see below.

## Accuracy

Every price carries a `quality` assessment so you know how much to trust it:

```json
"quality": {
  "grade": "B", "confidence": 0.89,
  "freshness": "realtime", "directness": "direct", "source_class": "exchange",
  "corroboration": { "sources": 4, "spread_bps": 29, "agree": true },
  "caveats": []
}
```

The grade combines **freshness** (edge age), **directness** (hop count),
**source authority** (official > exchange > aggregator > unofficial),
**cross-source agreement** (spread in bps), and per-currency **caveats**
(e.g. NGN/EGP/CNY official-vs-parallel-rate flags). Full model:
[ACCURACY.md](ACCURACY.md). The web UI shows the grade in the converter and a
dedicated **Accuracy** page documenting the methodology.

## Sources

Selectable with `-sources` (or `OPENRATE_SOURCES`). Default: `ecb,coinbase,luno,sarb`.

| Source | Default | Cadence | Notes |
|---|---|---|---|
| **ECB** daily file | ✅ | daily | EUR-base, ~30 currencies, ~16:00 CET |
| **Coinbase** | ✅ | real-time | free/no-auth fiat (incl. **ZAR**) + crypto — best open intraday source |
| **Luno** | ✅ | real-time | SA exchange, live BTC/ETH/USDT vs ZAR; bridges to fiat via BTC |
| **SARB** | ✅ | daily | **authoritative ZAR** (per USD/GBP/EUR/JPY); slow host → bounded dialer + retries |
| **Frankfurter** | opt-in | daily | clean JSON ECB mirror |
| **open.er-api** | opt-in | daily incl. **weekends** | fills the ECB Fri→Mon gap |
| **fawazahmed0** | opt-in | daily | ~400 currencies, dual-CDN, no limits |
| **Bank of Canada** | opt-in | daily | Valet REST, independent cross-check |
| **Yahoo Finance** | opt-in | ~1 min | unofficial, **ToS-prohibited**, rate-limited — last resort |

Because the graph prefers the freshest direct edge, `USD→ZAR` resolves to the
live Coinbase quote (~seconds old) while `EUR/GBP/JPY→ZAR` resolve to SARB's
authoritative direct quotes — each chosen automatically, no special-casing.
Add a source by implementing `sources.Source` and registering it in
`internal/sources/registry.go`. Full catalog + freshness notes: [SOURCES.md](SOURCES.md).

## Web UI

```bash
npm --prefix web install
npm --prefix web run dev      # Vite dev server, proxies /api to :8080
npm --prefix web run build    # regenerates web/dist, embedded into the binary
```

## Layout

```
cmd/openrate      entrypoint: wires sources -> store -> api + UI
internal/graph    currency graph, BFS all-pairs materialization
internal/sources  pluggable open sources (ECB live, SARB stub)
internal/store    ingest loop + snapshot store
internal/api      JSON read endpoints
web               Vite + React JSX UI (embedded via go:embed)
```

## License

MIT — see [LICENSE](LICENSE).
