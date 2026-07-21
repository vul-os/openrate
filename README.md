<p align="center">
  <img src="assets/openrate.svg" width="84" alt="openrate" />
</p>

<h1 align="center">openrate</h1>

<p align="center">Open, ZAR-anchored exchange rates — the open way.</p>

<p align="center">
  <a href="docs/">Docs</a> ·
  <a href="docs/api.md">API</a> ·
  <a href="docs/configuration.md">Configuration</a> ·
  <a href="docs/library.md">Go library</a> ·
  <a href="docs/graph-model.md">Graph model</a> ·
  <a href="ACCURACY.md">Accuracy</a> ·
  <a href="CHANGELOG.md">Changelog</a> ·
  <a href="ROADMAP.md">Roadmap</a>
</p>

<p align="center">
  <sub>Current release: <a href="https://github.com/vul-os/openrate/releases/tag/v0.2.0">v0.2.0</a></sub>
</p>

---

**openrate** is an open-source exchange-rate engine. It ingests rates "the open
way" — from central-bank reference files and free public venue feeds, not by
reselling a paid API — models every currency as a **graph** rather than picking a
single canonical base, and serves an all-pairs JSON API plus an embedded React
UI from a single Go binary.

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

Config via flags or env: `OPENRATE_ADDR`, `OPENRATE_BASE`, `OPENRATE_REFRESH`,
`OPENRATE_SOURCES`, `OPENRATE_RATELIMIT`. Full reference:
[docs/configuration.md](docs/configuration.md).

With Docker:

```bash
docker build -t openrate . && docker run -p 8080:8080 openrate
```

## Embed as a Go library

Instead of running the binary, import the root package and run the engine
in-process — no subprocess, same store/sources/API/hardening as `cmd/openrate`:

```go
import "github.com/vul-os/openrate"

local, err := openrate.Start(openrate.Options{}) // ZAR base, hourly refresh, ephemeral port
if err != nil {
	log.Fatal(err)
}
defer local.Close()

resp, _ := http.Get(local.APIBaseURL() + "/rates") // or local.BaseURL + "/healthz"
```

`Options` mirrors the binary's flags (`Addr`, `Base`, `Refresh`, `Sources`,
`RateLimit`, `ServeUI`). `Start` returns once `/healthz` is serving. The engine's
building blocks stay under `internal/`; this package is the supported public API.

## Deployment modes

openrate ships as sovereign, self-contained infrastructure — run it two ways,
both fully open, keyless, and free:

| Shape | How |
|---|---|
| **Self-hosted binary** | `go run ./cmd/openrate` — keyless, all sources, hourly refresh |
| **Embedded Go library** | `openrate.Start(...)` in-process — the same engine, no subprocess |

## API

| Endpoint | Description |
|---|---|
| `GET /api/v1/rates?base=ZAR` | All currencies vs base; `rate` reads "1 base = rate CCY" |
| `GET /api/v1/convert?from=USD&to=ZAR&amount=100` | Convert, with rate provenance |
| `GET /api/v1/meta` | Sources, freshness, currency list |
| `GET /healthz` | Liveness |

Every rate includes `hops`, `as_of`, `age_sec`, the `path` and `sources`, plus a
**`quality`** block (grade A–D + confidence) — see below. Full request/response
shapes: [docs/api.md](docs/api.md).

### Interest rates (optional engine)

A separate, flat time-series engine (no currency graph) for central-bank policy
and reference rates worldwide. Enable with `-interest-sources` (binary) or
`Options{Interest: true}` (library). Served alongside the FX API:

| Endpoint | Description |
|---|---|
| `GET /api/v1/interest/rates?area=US&type=policy` | Latest value per series + confidence grade |
| `GET /api/v1/interest/series?id=us.policy` | One series with full history (timeseries) |
| `GET /api/v1/interest/meta` | Areas covered, series catalogue, source status |

Out of the box (`bis,sarbrates`, no keys) this covers **49 central banks' policy
rates with daily history** plus the South African ZARONIA family; set
`OPENRATE_FRED_API_KEY` to auto-enable US benchmark series. Each series carries an
interest-tuned `quality` grade. See [docs/interest-rates.md](docs/interest-rates.md).

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
openrate.go       public package: embed the engine in-process (Start/Close)
cmd/openrate      entrypoint: wires sources -> store -> api + UI
internal/graph    currency graph, BFS all-pairs materialization
internal/sources  pluggable open sources (ECB live, SARB stub)
internal/store    ingest loop + snapshot store
internal/api      JSON read endpoints
web               Vite + React JSX UI (embedded via go:embed)
```

## Documentation

Full documentation lives in **[`docs/`](docs/)**.

| Guide | What's inside |
|---|---|
| [API reference](docs/api.md) | Every endpoint, params, and full response shapes |
| [Configuration](docs/configuration.md) | Flags, env vars, and the source spec |
| [Go library](docs/library.md) | Embed the engine in-process with `Start`/`Close` |
| [Graph model](docs/graph-model.md) | Why currencies are a graph, not a base |
| [Accuracy & quality](ACCURACY.md) | The grade/confidence model behind every rate |
| [Sources](SOURCES.md) | Full source catalog, cadence, and provenance |
| [Web UI](docs/web-ui.md) | The embedded React dashboard |

## License

[MIT](LICENSE-MIT) OR [Apache-2.0](LICENSE-APACHE) — © VulOS. openrate is a VulOS
project; source and issues at [github.com/vul-os/openrate](https://github.com/vul-os/openrate).

### Third-party notices

openrate redistributes third-party software: the Go standard library and any Go
modules compiled into the binary, the npm packages bundled into the embedded
React UI (including the **Inter** and **JetBrains Mono** webfonts, whose OFL-1.1
licence must travel with the shipped `.woff2` files), and the mermaid/marked
bundles vendored into the marketing site. Their licences (MIT, BSD, Apache-2.0,
OFL-1.1) require the copyright notice and licence text to accompany every copy.

- [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt) — name, version, licence and
  full text for every component. Generated from the real dependency graph by
  `scripts/gen-notices.sh` (Go: go-licence-detector; npm: license-checker), never
  hand-edited.
- The binary serves it at **`/licenses.txt`** (linked from the app footer); the
  marketing site serves it too (linked from its footer).
- Vendored site bundles carry their upstream licence next to them, e.g.
  `site/assets/vendor/mermaid.min.js.LICENSE`.

---

<sub><img src="assets/vulos-logo.png" height="16" alt="VulOS"> · <strong>Built with purpose. Open by design.</strong></sub>

---

<p align="center">
  <a href="https://vulos.org"><img src="assets/vulos-logo.png" alt="vulos" height="20"></a><br>
  <sub><a href="https://vulos.org"><b>vulos</b></a> — open by design</sub>
</p>
