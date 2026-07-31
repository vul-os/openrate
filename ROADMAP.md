# Roadmap

openrate is an open-source exchange-rate engine: it models currencies as a
graph (no single canonical base), ingests from central-bank files and free
public venue feeds, and serves an all-pairs JSON API plus an embedded React UI
from a single Go binary — self-hosted or embedded as a Go library. This is a
living, honest snapshot of direction, not a commitment or a timeline.

See [CHANGELOG.md](CHANGELOG.md) for what has already shipped.

## Now

- **Source resilience.** Cache SARB's last-good response across restarts —
  the host intermittently drops TCP connects, and SARB is the authoritative
  ZAR quote.
- **Engine test coverage.** Direct tests for the currency graph itself (BFS
  correctness, triangulation-vs-direct preference, ECB XML parsing), on top of
  the source/store/ratelimit coverage already in place.
- **Freshness signalling.** Flag a source that has gone past its expected
  cadence, using the `as_of` timestamp the engine already tracks per edge.

## Next

- **Installable converter.** A web manifest and icons for `web/ui.html` so
  the converter can be added to a phone home screen or run as its own
  window — small, mostly-assets work; icons already have one source of
  truth in `brand/logo.svg`. Offline caching of last-known rates is a
  separate, bigger job (service worker, cache invalidation, update
  cycles) and isn't scoped yet — and it would need its own explicit
  staleness indicator, since serving a cached rate as if it were current
  would cut against the whole point of a `quality` grade on every number.
- **More open sources.** Additional crypto venues (VALR as a Luno failover for
  ZAR, Kraken/Bitstamp majors, verified Binance ZAR symbols) and additional
  central banks (Fed H.10 via a FRED key, BoE IADB, SNB, RBA) — all free,
  no-auth where possible, matching the "open way" sourcing model.
- **Per-source refresh cadence.** Move from one global refresh interval to a
  fast tick for real-time sources (Coinbase/Luno) and a daily tick for
  file-based sources (ECB/SARB), and re-materialize the matrix on tick rather
  than polling everything on the same clock.
- **Convenience API surface.** A `/api/v1/pairs/{from}/{to}` route and bulk
  convert, plus an on-boot ECB 90-day backfill so a fresh instance isn't
  empty of history.

## Later

- **Push/streaming ingestion.** Move off polling entirely for venues that
  support it (crypto WebSocket feeds first), keeping the graph model but
  cutting latency further.
- **Historical storage.** Persist daily snapshots for `?date=` queries and
  time-series lookups; the engine keeps this basic today.
