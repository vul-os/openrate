# Changelog

All notable changes to openrate will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **A non-finite rate could blank an entire API response.** Every FX source
  guards its parse with `rate <= 0`, which is false for `NaN` and `+Inf`, and
  `strconv.ParseFloat` accepts the literal strings `"NaN"`/`"Inf"` without an
  error (BIS already publishes literal `NaN` for missing days). Such a value
  entered the currency graph, multiplied into every path crossing that edge, and
  aborted the JSON encoder mid-write — and because `writeJSON` had already sent
  a `200` and discarded the encoder error, consumers received `200 OK` with an
  empty body. `internal/graph` now refuses any rate that is not positive and
  finite, in both the quoted and the derived (`1/rate`) direction.
- **Path arithmetic could overflow even when every leg was finite.** A
  triangulated rate is the product of its legs, which can reach `+Inf` (or
  underflow to `0`) from representable inputs. Such a path is now skipped rather
  than materialized; a longer representable path may still serve the pair.
- **`/api/v1/convert` returned `200` with an empty body for a large `amount`.**
  The existing guard rejected non-finite *input*, but a finite amount times a
  finite rate can still overflow. An unrepresentable result is now a `400`
  (`{"error":"amount out of range for this pair"}`).
- **`grade` could contradict the `confidence` published beside it.** The grade
  was computed from the raw factor product while the response carried the
  rounded value, so an ordinary day-old 2-hop exchange cross published
  `"confidence": 0.78` next to `"grade": "C"` — against the documented `B ≥ 0.78`
  band. Both quality engines now grade the confidence they actually publish.

### Documentation

- **`docs/api.md` described the `as_of` contract backwards.** It said `as_of` was
  the *freshest* edge on the path; the engine has always used the **oldest**, so
  `age_sec` is an upper bound on staleness. Corrected, and expanded with the
  full as-of contract, the identity-pair case, and why `as_of` is not a change
  cursor (`built_at` is).
- Documented the complete `quality` block for consumers writing their own
  client: every field and its values, that `caveats` is omitted (never `[]`)
  when empty, that `mean`/`min`/`max` appear only with ≥2 quotes and
  `stdev`/`stdev_bps` only when non-zero, and that `agree` (≤50 bps) uses a
  different threshold from the confidence bands (25/100/300 bps).
- `ACCURACY.md`: completed the source-class table, which omitted `polygon`,
  `tradermade`, `twelvedata` and `oxr`, and the `unknown` class; replaced the
  "Typical coverage" per-currency grade claims — which the model does not
  support — with the measured provenance table now pinned by tests.
- `SOURCES.md`: documented the four implemented key-gated FX sources (`oxr`,
  `twelvedata`, `polygon`, `tradermade`) that the catalog omitted entirely.
- `docs/interest-rates.md`: the worked example published `"confidence": 0.85`
  where the engine computes `0.78` (0.85 omits the US target-range caveat).
- Removed `"caveats": []` from every example — the key is never emitted empty.
- Fixed the broken `../LICENSE` link and MIT-only claim in `docs/README.md`
  (the project is MIT OR Apache-2.0), and the stale "SARB stub" layout note.
- Reconciled `site/docs/` with `docs/`, and rewrote its internal links to the
  hash-router anchors the site actually resolves — every one was broken.

### Removed

- `quality.Assessment.Explain` and `ratequality.Assessment.Explain`: dead code
  with no non-test caller in an `internal/` package, whose doc comment claimed
  it was "used in docs/tooltips" when the tooltip is built independently in
  `web/src/App.jsx`.

### Testing / CI

- New coverage for the invariants above: non-finite rejection at the graph,
  source and HTTP layers; an all-pairs "every materialized rate is finite and
  JSON-encodable" property; and exhaustive verification that `grade` and
  `confidence` agree across all 2160 (FX) and 250 (interest) factor combinations.
- Checksum-pinned the ISO 4217 currency table (`internal/sources/fiat.go`) so
  drift is detectable, plus structural checks and a guard that the web UI's
  separate currency table covers the engine's set.
- First tests for `internal/ratesources` (previously none): BIS CSV parsing
  including literal-`NaN` rows, ragged rows, name fallbacks, and error paths;
  plus registry resolution and key auto-enable.
- Asserted every registered source has a quality rank, so a new source cannot
  silently grade as `unknown`.
- CI now runs `gofmt`, `go test -race`, and verifies the committed `web/dist`
  matches a fresh build (it is embedded in the binary, so a stale bundle ships).

## [0.2.0] - 2026-07-17

### Added

- `THIRD-PARTY-NOTICES.txt` — generated, full-text third-party licence notices
  (Go stdlib/modules, npm packages including the OFL-1.1 webfonts, vendored
  site bundles), produced by `scripts/gen-notices.sh` and never hand-edited.
  Served by the binary at `/licenses.txt` and by the marketing site, both
  linked from their footers.
- Playwright end-to-end tests for the web UI (`web/e2e/boot.e2e.js`,
  `web/e2e/converter.e2e.js`) that boot the production-built bundle in real
  Chromium and fail on any uncaught exception, blank root, or SPA-fallback
  bug — wired into `npm test` and a new `web-e2e` CI job.
- Regression test coverage across `internal/ratelimit`, `internal/store`,
  `internal/ratestore`, and `internal/sources` (XFF spoofing, bucket sweep
  eviction, concurrent store access under `-race`, fixture-driven source
  `Fetch` parsing, and secret-leak redaction for paid sources).
- README "Deployment modes" section documenting the two current shapes:
  self-hosted binary and embedded Go library.

### Changed

- Rate-limiter `ClientIP` now walks `X-Forwarded-For` from the right and skips
  configured trusted-proxy hops instead of trusting the left-most (client
  forgeable) entry.
- Vendored the Inter and JetBrains Mono webfonts locally instead of loading
  them from Google Fonts at runtime, so a self-hosted instance never phones
  home for UI assets.
- Bumped `vite` ^5→^8 and `@vitejs/plugin-react` ^4→^6 (clears dev-tooling
  `npm audit` advisories); `vite.config.js` now preserves upstream `@license`
  banners in the shipped bundle (`output.comments.legal = true`).
- Bumped the Go toolchain to go1.25.12 to clear reachable stdlib
  vulnerabilities.
- README rewritten to be self-contained (dropped the "Part of VulOS" suite
  banner/product-map section, added a footer logo instead) and CLOUD.md/README
  updated to mark hosted, multi-tenant openrate as exploratory/deferred rather
  than a current Vulos product; stale mail/"Workspace" references renamed to
  "lilmail" and "Office" renamed to "Diwan" in the site footer.

### Fixed

- Fixed a goroutine and `time.Ticker` leak in the rate limiter: `Limiter` now
  has a `done` channel and `Stop()`, `gc()` selects on it, and `Local.Close()`
  drains the background sweep goroutine on shutdown.
- `New(0, _)` no longer divides by zero — `rpm`/`burst` are clamped to at
  least 1 so `Retry-After` is always finite.
- `GET /api/v1/convert` now rejects non-finite (`Inf`/`NaN`) `amount` values
  with a clean `400` instead of letting them poison the arithmetic and produce
  a truncated `200` response body.

## [0.1.0] - 2026-06-28

Initial release.

[Unreleased]: https://github.com/vul-os/openrate/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/vul-os/openrate/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/vul-os/openrate/releases/tag/v0.1.0
