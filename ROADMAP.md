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
  time-series lookups; the engine keeps this basic today and long-retention
  storage is explicitly a hosted-tier concern (see [CLOUD.md](CLOUD.md)).
- **Vulos Cloud absorption.** A hosted, multi-tenant, metered layer around
  this engine (API keys, quotas, billing) is an explored-but-deferred idea
  tracked in [CLOUD.md](CLOUD.md) — not a current Vulos product, and not
  built in this repo. The self-hosted binary and embedded Go library stay
  free and keyless regardless of whether that ever ships.
