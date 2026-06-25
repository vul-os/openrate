# Interest rates

openrate ships a second, optional engine for **interest rates** — central-bank
policy rates, overnight reference rates (SOFR, €STR, ZARONIA), term and lending
rates. It runs alongside the FX engine and serves under `/api/v1/interest/*`.

## Why it's separate from FX

FX is a **graph**: currencies are nodes, quotes are edges, and any pair is
triangulated along the shortest path. Interest rates don't compose — a US policy
rate and a euro-area policy rate have no product. So this engine models rates as
**flat, independent time series**, each identified by a canonical ID and carrying
its own history. No graph, no hops, no triangulation.

## Series IDs

```
<area>.<type>[.<tenor>]
```

- `area` — ISO 3166 alpha-2 (`us`, `za`, `gb`) or an area code (`xm` = euro area)
- `type` — `policy` · `reference` · `interbank` · `lending` · `deposit` · `bond`
- `tenor` — optional: `on`, `1w`, `1m`, `3m`, `6m`, `12m`, `2y`, `10y`

Examples: `us.policy`, `za.ref.zaronia`, `za.ref.zaronia.3m`, `us.ref.sofr`,
`us.bond.10y`.

## Sources

Configured with `-interest-sources` (or `OPENRATE_INTEREST_SOURCES`). Default
`bis,sarbrates`. A keyed source auto-enables when its env var is set — the same
"`.env` and it just works" path as the FX side.

| Source | Key | Coverage |
|---|---|---|
| `bis` | — | 49 central banks' policy rates + daily history, one CSV call (BIS `WS_CBPOL`) |
| `sarbrates` | — | South African ZARONIA overnight + 1W–12M compounded + index, deep history |
| `fred` | `OPENRATE_FRED_API_KEY` | US SOFR, EFFR, OBFR, bank prime, 2Y/10Y Treasury |

Adding a source is one file implementing `ratesources.Source` (`Name()` +
`Fetch()` returning `[]rates.Observation`) and a line in `registry.go`.

## Confidence grade

Every series carries a `quality` block, tuned for interest rates (not FX):

- **source authority** — issuing central bank (`official_issuer`) > official
  aggregator like BIS/FRED (`official_aggregator`) > commercial > unofficial.
- **freshness** — judged against publication cadence (`current` < 3d, `recent`
  < 16d to absorb weekly aggregator updates, then `aging`/`stale`/`old`).
- **corroboration** — how many independent sources report the series, and their
  dispersion in **absolute basis points** (rates are levels, not ratios).
- **caveats** — definitional notes: US policy is a target-range midpoint, managed
  regimes (CN), high-inflation volatility (AR, TR), index-vs-rate, single-source.

Grades: **A** ≥ 0.90 · **B** ≥ 0.78 · **C** ≥ 0.60 · **D** < 0.60. A single
official-aggregator series grades ~B; add a second corroborating source (or the
issuing bank's own feed) and it climbs to A.

## Examples

```bash
# Every country's latest policy rate
curl 'localhost:8080/api/v1/interest/rates?type=policy'

# One series with full history (timeseries)
curl 'localhost:8080/api/v1/interest/series?id=za.policy'

# What's covered and how fresh each source is
curl 'localhost:8080/api/v1/interest/meta'
```

Response (trimmed):

```json
{
  "series": "us.policy",
  "area": "US",
  "type": "policy",
  "name": "United States — policy rate",
  "value": 3.625,
  "date": "2026-06-16T00:00:00Z",
  "source": "bis",
  "sources": ["bis"],
  "quality": {
    "grade": "B",
    "confidence": 0.85,
    "freshness": "recent",
    "source_class": "official_aggregator",
    "corroboration": { "sources": 1, "spread_bps": 0, "agree": false },
    "caveats": [
      "US policy rate is a target range; the published value is the midpoint",
      "single source — not independently corroborated"
    ]
  }
}
```

## Embedding

```go
local, _ := openrate.Start(openrate.Options{Interest: true})
defer local.Close()
// GET local.APIBaseURL() + "/interest/rates"
```
