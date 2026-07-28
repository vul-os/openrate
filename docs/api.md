# API reference

openrate serves a small, read-only JSON API under `/api/v1`. All responses are
`application/json` with `Access-Control-Allow-Origin: *` by default
(browser-friendly); the origin is configurable via `OPENRATE_CORS_ORIGIN` (see
[configuration](configuration.md#cors)).

| Endpoint | Description |
|---|---|
| [`GET /api/v1/rates`](#get-apiv1rates) | All currencies vs. a base |
| [`GET /api/v1/convert`](#get-apiv1convert) | Convert an amount between two currencies |
| [`GET /api/v1/meta`](#get-apiv1meta) | Sources, freshness, and currency list |
| [`GET /healthz`](#get-healthz) | Liveness probe |

All currency codes are case-insensitive and trimmed (`zar` → `ZAR`).

---

## `GET /api/v1/rates`

All currencies expressed against a base. Each `rate` reads as
**"1 base = rate units of CCY"**.

**Query params**

| Param | Default | Description |
|---|---|---|
| `base` | the server default (`ZAR`) | Presentation base currency |

**Response**

```json
{
  "base": "ZAR",
  "built_at": "2026-06-24T09:00:00Z",
  "rates": {
    "USD": {
      "rate": 0.054,
      "hops": 1,
      "as_of": "2026-06-24T08:59:58Z",
      "age_sec": 2.1,
      "path": ["ZAR", "USD"],
      "sources": ["coinbase"],
      "quality": { "grade": "B", "confidence": 0.89, "...": "see Accuracy" },
      "legs": [
        { "from": "ZAR", "to": "USD", "rate": 0.054, "source": "coinbase", "age_sec": 2.1 }
      ],
      "quotes": [
        { "source": "coinbase", "rate": 0.054, "age_sec": 2.1 }
      ]
    }
  }
}
```

---

## `GET /api/v1/convert`

Convert an amount between two currencies, with full provenance for the rate used.

**Query params**

| Param | Default | Description |
|---|---|---|
| `from` | the server default base | Source currency |
| `to` | the server default base | Target currency |
| `amount` | `1` | Amount to convert. Unparseable values fall back to `1`; non-finite values (`Inf`/`NaN`) are rejected with `400`. |

**Response**

```json
{
  "from": "USD",
  "to": "ZAR",
  "amount": 100,
  "result": 1851.85,
  "rate": { "rate": 18.5185, "hops": 1, "as_of": "...", "quality": { "...": "" } }
}
```

Returns **`404`** with `{"error":"unknown or unreachable currency pair"}` when no
path connects the two currencies in the current snapshot, and **`400`** with
`{"error":"invalid amount"}` when `amount` is non-finite (`Inf`/`NaN`).

---

## `GET /api/v1/meta`

Sources, freshness, and the list of currencies present in the snapshot.

```json
{
  "default_base": "ZAR",
  "built_at": "2026-06-24T09:00:00Z",
  "currencies": ["USD", "EUR", "GBP", "ZAR", "..."],
  "sources": [ { "name": "coinbase", "...": "freshness/status fields" } ]
}
```

---

## `GET /healthz`

Always returns `200 OK` with body `ok` once the server is listening. Used as a
readiness probe (the [Go library](library.md) waits on it during `Start`).

---

## The rate object

Every rate (in `rates`, and in `convert`'s `rate` field) carries provenance so
consumers can see exactly how the number was produced:

| Field | Meaning |
|---|---|
| `rate` | The exchange rate (units of target per 1 base/from) |
| `hops` | Number of edges traversed in the graph (1 = direct quote, 0 = identity) |
| `as_of` | Timestamp of the **oldest** edge on the path — see the as-of contract below |
| `age_sec` | Seconds since `as_of`; the age of the **weakest link**, not of the newest quote |
| `path` | The currency chain, e.g. `["ZAR","USD"]` |
| `sources` | Distinct sources of the edges on the path |
| `legs` | Each hop's actual rate + source + age (the calculation, step by step) |
| `quotes` | Per-source **direct** quotes behind the pair, for cross-checking |
| `quality` | Grade (A–D), confidence, freshness, directness, corroboration, caveats — see [Accuracy](../ACCURACY.md) |

### The as-of contract

`as_of` is the **oldest** edge timestamp on the path, and `age_sec` is measured
from it. This is deliberate and it is the opposite of "when was this rate last
updated":

- For a **direct** quote (`hops: 1`) there is one edge, so `as_of` is that
  quote's timestamp.
- For a **triangulated** quote (`hops: 2+`) the rate is only as current as its
  stalest leg. A EUR→ZAR built from a 3-second-old EUR→USD and a 20-hour-old
  USD→ZAR reports `age_sec ≈ 72000`, not 3.

So `age_sec` is an **upper bound on staleness**: the number is never fresher than
it claims. A consumer enforcing a maximum age can compare against `age_sec`
directly without inspecting `legs`. Per-leg ages are in `legs[].age_sec` if you
need to see which hop is dragging the number down.

Two consequences worth building for:

- `as_of` does **not** advance when a refresh leaves the stalest leg unchanged,
  so it is not a reliable "has this changed" cursor. The top-level `built_at`
  (when the snapshot was materialized) is, on `/rates` and `/meta` — `/convert`
  does not return it.
- Identity pairs (`from == to`) return `rate: 1`, `hops: 0`, an empty `legs`, and
  `as_of` set to the snapshot build time rather than to any quote — so `age_sec`
  there measures the snapshot, not market data.

See [the graph model](graph-model.md) for how `path`/`legs`/`hops` are chosen.

### The `quality` block

Every rate carries one. The full shape:

```json
"quality": {
  "grade": "B",
  "confidence": 0.89,
  "freshness": "realtime",
  "directness": "direct",
  "source_class": "exchange",
  "corroboration": {
    "sources": 4,
    "spread_bps": 29,
    "agree": true,
    "mean": 18.4991, "stdev": 0.0121, "stdev_bps": 6.54,
    "min": 18.48, "max": 18.53
  }
}
```

(There is no `caveats` key above because this pair has none — see the table.)

| Field | Type | Values / meaning |
|---|---|---|
| `grade` | string | `A` \| `B` \| `C` \| `D`. Always consistent with `confidence` (see bands below). |
| `confidence` | number | 0–1, rounded to 2 decimals. The product of the five factors. |
| `freshness` | string | `realtime` (<5 min) \| `current` (<26 h) \| `daily` (<4 days) \| `stale` |
| `directness` | string | `direct` (≤1 hop) \| `cross` (2) \| `multi_cross` (3+) |
| `source_class` | string | `official` \| `exchange` \| `aggregator` \| `unofficial` \| `unknown` — the **weakest** source on the path |
| `corroboration` | object | Cross-source agreement for the exact pair; see below |
| `caveats` | string[] | Human-readable warnings. The key is **omitted entirely** when there are none — it is never `[]`, so decode it as an optional field. |

Grade bands: **A** ≥ 0.90 · **B** ≥ 0.78 · **C** ≥ 0.60 · **D** < 0.60. The grade
is derived from the same rounded `confidence` that is published, so the two can
never disagree.

`source_class` is `unknown` when the engine does not recognise a source name on
the path. Treat it as "unrated", not "bad".

**`corroboration`** counts *independent direct quotes for that exact pair* — not
the sources on the path:

| Field | Notes |
|---|---|
| `sources` | Number of distinct sources directly quoting the pair. `0` for a purely triangulated pair. |
| `spread_bps` | `(max−min)/min` across those quotes, in basis points. `0` when `sources` ≤ 1. |
| `agree` | `spread_bps <= 50`. **`false` when `sources` is 1** (a lone quote is uncorroborated, not agreed) and `true` when `sources` is 0. |
| `mean`, `min`, `max` | Present only when `sources` ≥ 2; **absent** (not zero) otherwise. |
| `stdev`, `stdev_bps` | Sample standard deviation of the quotes. Present only when `sources` ≥ 2 **and** the value is non-zero — identical quotes omit them. |

Two traps worth coding around:

- `agree` and the confidence bands use **different thresholds**. `agree` is
  ≤ 50 bps; the confidence factor steps at 25 / 100 / 300 bps. A pair can report
  `agree: true` and still be penalised (50 bps agrees but scores ×0.93).
- `agree: true` with `sources: 0` means "nothing to disagree with", not
  "corroborated". Check `sources` first.

Full model, including every multiplier: [Accuracy & quality](../ACCURACY.md).
