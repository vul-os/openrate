# Accuracy model

openrate attaches a `quality` block to **every** rate it returns. An exchange
rate is only as trustworthy as its provenance, so we make that explicit: a letter
grade plus a 0–1 confidence, with the factors that produced it. The logic lives in
`internal/quality` and is mirrored in the web **Accuracy** page.

## Grade bands

| Grade | Confidence | Meaning |
|---|---|---|
| **A** | ≥ 0.90 | trust it |
| **B** | ≥ 0.78 | good |
| **C** | ≥ 0.60 | use with care |
| **D** | < 0.60 | weak / flagged |

Confidence is the product of five factors, clamped to [0, 1].

## The five factors

1. **Freshness** — age of the oldest edge on the path.
   `realtime` (<5 min, ×1.0) · `current` (<26 h, ×0.9) · `daily` (<4 days, ×0.72) ·
   `stale` (older, ×0.45). The 4-day "daily" window absorbs the weekend gap when
   fiat markets are closed.
2. **Directness** — hop count. `direct` (1 hop, ×1.0) · `cross` (2, ×0.9) ·
   `multi_cross` (3+, ×0.75). Each hop compounds the spread.
3. **Source authority** — the *weakest* source on the path.
   `official` (×1.0) > `exchange` (×0.96) > `aggregator` (×0.92) > `unofficial` (×0.7).
4. **Corroboration** — independent sources directly quoting the exact pair, and
   the spread between them (bps): ≤25 ×1.0, ≤100 ×0.93, ≤300 ×0.85, else ×0.72.
   A single uncorroborated source ×0.88; a purely derived pair is neutral (the
   directness factor already accounts for it).
5. **Currency caveats** — `NGN`/`EGP` (official vs parallel rate, ×0.7), `CNY`
   (managed onshore/offshore, ×0.7), defunct currencies (×0.2). Each adds a
   human-readable `caveats[]` entry.

## Source classes

| Class | Sources |
|---|---|
| official | sarb, ecb, boc, frankfurter (ECB data) |
| exchange | coinbase, luno |
| aggregator | erapi, fawazahmed0 |
| unofficial | yahoo |

## In the response

```json
GET /api/v1/convert?from=USD&to=ZAR
"rate": {
  "rate": 16.44, "hops": 1, "age_sec": 4, "sources": ["coinbase"],
  "quality": {
    "grade": "B", "confidence": 0.89,
    "freshness": "realtime", "directness": "direct", "source_class": "exchange",
    "corroboration": { "sources": 4, "spread_bps": 29, "agree": true },
    "caveats": []
  }
}
```

The same block appears on each entry of `/api/v1/rates`.

## Typical coverage

- **Grade A:** USD, EUR, GBP, JPY, CHF, AUD, CAD, ZAR (vs majors) — multi-source, direct, fresh.
- **Grade C:** NGN, KES, GHS, EGP, MAD, BWP, AED, SAR — fewer sources, triangulated.
- **Flagged:** NGN, EGP, CNY — official rate may differ from the transactable rate.

Grades are computed per request, not fixed — adding sources or freshness raises them.
