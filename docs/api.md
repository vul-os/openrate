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
| [`GET /healthz`](#get-healthz) | Liveness — the process is up |
| [`GET /readyz`](#get-readyz) | Readiness — a conversion would actually succeed |

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

Returns **`404`** with `{"error":"unknown base currency"}` when the snapshot has
never heard of `base`. Since 0.1.6; it previously answered `200` with an empty
`rates` object, which is what `openrate.Engine.Rates` and the C ABI had always
refused.

**`base` is the pivot of this document, not a filter over it.** Every entry
means "1 base = rate units of X", so an unknown base does not describe a table
that happens to have no rows — it makes the response's own definition false,
while echoing the invented code back in the `base` field as though it were real.
And the old `200` was indistinguishable from a cold start, which answers `200`
with an empty book for *every* base: that ambiguity is why `/readyz` exists, and
overloading the same response with "your currency code is wrong" left a caller
no way to tell a typo from a feed outage.

**A snapshot with no currencies at all is still `200` with an empty book**, on
all three surfaces. That is "nothing yet" — a readiness question, not a bad
request. Ask [`/readyz`](#get-readyz).

---

## `GET /api/v1/convert`

Convert an amount between two currencies, with full provenance for the rate used.

**Query params**

| Param | Default | Description |
|---|---|---|
| `from` | the server default base | Source currency |
| `to` | the server default base | Target currency |
| `amount` | `1` | Amount to convert. Unparseable values (`notanumber`) fall back to `1`; non-finite (`Inf`/`NaN`) **and out-of-range (`1e999`)** values are rejected with `400`. |

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
path connects the two currencies in the current snapshot — **including when
either code is one the snapshot has never heard of**. Since 0.1.5 that covers
`?from=NOTACCY&to=NOTACCY`, which previously answered `200` with `rate: 1`,
`hops: 0` and a quality grade of `B`: an invented rate for a currency that does
not exist, dressed as a real one. `USD→USD` is unaffected — the code is known,
so the identity is a true statement about it.

Returns **`400`** with `{"error":"invalid amount"}` when `amount` is non-finite
(`Inf`/`NaN`) or **out of float64 range** (`1e999`). The out-of-range case also
changed in 0.1.5: `1e999` used to be silently converted as `1.0`, so a caller
asking about 1e999 units got a successful-looking answer about one. An
*unparseable* amount still falls back to `1` — that is long-standing documented
behaviour and a typo should not 400 a dashboard.

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

Always returns `200 OK` with body `ok` once the server is listening.

**This is liveness, not readiness.** It answers the instant the listener binds,
which is before any source has been fetched — so a client that treats a 200 here
as "ready to convert" will get `unknown or unreachable currency pair` for every
pair it asks about. Use [`/readyz`](#get-readyz) to decide when to send the first
request. Every managed sidecar in [`sdks/`](../sdks/) made exactly this mistake
before `/readyz` existed.

---

## `GET /readyz`

Whether the engine can actually serve a conversion yet.

`200 OK` once the snapshot has currencies in it:

```json
{ "ready": true, "currencies": 31, "built_at": "2026-08-09T21:04:11Z", "sources": [ ... ] }
```

`503 Service Unavailable` before that, carrying **why**:

```json
{
  "ready": false,
  "currencies": 0,
  "reason": "no rates yet: no source has returned a usable quote",
  "sources": [ { "name": "ecb", "last_error": "dial tcp: connection refused" } ]
}
```

Poll it until 200, then start converting. On timeout, print the `last_error` of
each source rather than a bare timeout — that field is the difference between
"openrate never became ready" and "ECB is unreachable from this host".

`/readyz` sits **outside `/api/`** deliberately, so the
[rate limiter](configuration.md) never applies to it: polling readiness cannot
exhaust the budget you are waiting to use. Polling `/api/v1/meta` — the
workaround this endpoint replaces — could and did.

In-process there is a direct equivalent and no polling: `Refresher.Ready(ctx)`
blocks until the first non-empty snapshot. See [library.md](library.md).

**All fifteen [language packages](../sdks/README.md) ship a wait on this
endpoint**, and on timeout every one of them raises the sources' `last_error`
text rather than a bare deadline. They differ on whether you have to ask:

- **Automatic** — python, php, ruby, elixir, java, kotlin, dotnet. The function
  that starts the managed sidecar does not return until `/readyz` answers `200`.
- **An explicit call** — node, deno and bun (`await waitForRates()`), rust
  (`Sidecar::wait_ready`), swift (`waitReady(timeout:)`). `start()` waits for
  the listener only.

If you are writing your own client, copy the wait — a start that returns before
readiness is the single most common way to get a green run that converted
nothing.

### Two things that bite HTTP clients on loopback

**Your language's HTTP client may route `127.0.0.1` through a proxy.** Several
do, and they do it silently: if `HTTP_PROXY` is set in the environment — which
it is, on most corporate machines and inside plenty of CI images — Python's
`urllib` and .NET's `HttpClient` will both send a loopback request to the
proxy, which then fails to reach a port on your machine. Both language packages
now bypass the proxy explicitly for the sidecar's address. The Bun and Deno
packages document `NO_PROXY=127.0.0.1` instead, because their runtimes read it.
Nothing about openrate causes this and openrate cannot fix it for a client it
did not write; the symptom is a connection error or a timeout against a sidecar
that is demonstrably listening.

**Neither `/healthz` nor `/readyz` is rate-limited, but everything under
`/api/` is** — 120 requests/minute per client network prefix by default (a `/64`
for IPv6, a `/32` for IPv4; see
[configuration](configuration.md#trusted-proxies--client-identity)). That is anti-scraping for
a public deployment and simply wrong for a loopback sidecar serving one
process, so the managed-sidecar packages start the server with
`OPENRATE_RATELIMIT=0`. Set it to a number to put the limiter back.

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

### Precision

`rate` is the product of `legs[].rate` **exactly** — the same `float64`, bit for
bit, because the engine accumulates it with the same left-to-right multiplication
in the same order the legs are listed. Recompute a cross rate from the legs in a
response and you get that response's `rate` back unchanged. Nothing is rounded on
the wire: the JSON carries the full-precision doubles.

Rounding is a display concern, and it is not free. Round each leg and the rate to
some number of decimals — the web UI uses six — and the rounded legs no longer
multiply to the rounded rate, because each value was rounded independently and
independent roundings do not compose. On live rates most two-hop crosses differ
in the last displayed place, and the size of the gap depends on the leg
magnitudes rather than on anything the engine did. If you round for presentation,
round the `rate` once from the value in the response; do not derive it from
rounded legs, and do not tell a user the two will agree.

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
