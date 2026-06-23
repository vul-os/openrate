# API reference

openrate serves a small, read-only JSON API under `/api/v1`. All responses are
`application/json` with `Access-Control-Allow-Origin: *` (browser-friendly).

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
| `amount` | `1` | Amount to convert |

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
path connects the two currencies in the current snapshot.

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
| `hops` | Number of edges traversed in the graph (1 = direct quote) |
| `as_of` | Timestamp of the freshest edge on the path |
| `age_sec` | Seconds since `as_of` (staleness — matters on weekends) |
| `path` | The currency chain, e.g. `["ZAR","USD"]` |
| `sources` | Distinct sources of the edges on the path |
| `legs` | Each hop's actual rate + source + age (the calculation, step by step) |
| `quotes` | Per-source **direct** quotes behind the pair, for cross-checking |
| `quality` | Grade (A–D), confidence, freshness, directness, corroboration, caveats — see [Accuracy](../ACCURACY.md) |

See [the graph model](graph-model.md) for how `path`/`legs`/`hops` are chosen,
and [Accuracy & quality](../ACCURACY.md) for the full `quality` block.
