# Roadmap

openrate is an open-source exchange-rate engine and a Go library first: it
models currencies as a graph (no single canonical base), ingests from
central-bank files and free public venue feeds, and optionally serves an
all-pairs JSON API plus an embedded, hand-written HTML UI. Import `Engine`
and `Refresher` directly, or run the self-hosted binary, which wires the same
pieces together. This is a living, honest snapshot of direction, not a
commitment or a timeline.

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

- **Installable converter.** A web manifest and icons for `serve/web/ui.html` so
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

### Publishing the language packages

Nothing is published to any registry today, and that is a decision, not an
oversight. The coordinates below are written into the manifests and reserved by
intent only — **no account holds them**, so any of them can be taken by someone
else tomorrow. That is not hypothetical: plain `llmux` was lost on both PyPI and
crates.io to unrelated projects before anyone looked, and the crates.io one is a
same-category tool at 2.4.0.

| registry | coordinate |
|---|---|
| npm | `@vul-os/openrate`, `@vul-os/openrate-bun` |
| JSR | `@vul-os/openrate` |
| PyPI | `vul-os-openrate` |
| crates.io | `vul-os-openrate` |
| RubyGems | `vul-os-openrate` |
| NuGet | `VulOs.OpenRate` |
| Packagist | `vul-os/openrate` |
| Hex | `vul_os_openrate` (Hex forbids hyphens) |
| Maven | `org.vulos:openrate` |
| Go | `github.com/vul-os/openrate` — no registry, the module path is the coordinate |

Checked free across npm, JSR, PyPI, crates.io, RubyGems, NuGet, Packagist and
Hex on 2026-08-10, before being written down.

**Before anything is pushed**

1. **Claim the scopes first, publish second.** `@vul-os` has to exist as an
   organisation on npm and on JSR before a scoped package can go anywhere, and
   claiming a scope is free and reversible in a way that losing a name is not.
   NuGet ID-prefix reservation for `VulOs.*` is optional but the same logic.
2. **A release has to produce the artifacts.** Today the release workflow builds
   the binary and the C ABI bundles; it does not build a wheel, an npm tarball,
   a gem or a nupkg. Publishing without that step is publishing whatever happens
   to be in a working tree.
3. **Each package must install from a clean checkout.** This was false until
   recently and silently so — `pip install -e sdks/python` failed on every clone
   (hatchling force-includes `openrate/bin` and `openrate/lib`, both gitignored, and raises `FileNotFoundError` when either is absent), and `npm pack` produced a tarball containing only a README and a
   package.json. Both are fixed and verified against `git archive HEAD`; the
   others are not all verified.
4. **Decide `llmux.to`.** Several manifests carry it as the homepage. A domain
   question, not a registry one, but it ships in package metadata.

**When a package does go out**, delete its registry from `UNPUBLISHED` in
`scripts/check-sdk-versions.mjs`. That list exists to refuse documentation that
tells a reader to install something that is not there; it is meant to shrink,
and the entry is what keeps the docs honest until it does.

**Order.** Go needs no registry at all — a module path is the coordinate, so it
is already "published" by tagging. Of the rest, the ones whose artifacts are
verified installable are the only candidates for a first push.


## Later

- **Push/streaming ingestion.** Move off polling entirely for venues that
  support it (crypto WebSocket feeds first), keeping the graph model but
  cutting latency further.
- **Historical storage.** Persist daily snapshots for `?date=` queries and
  time-series lookups; the engine keeps this basic today.
