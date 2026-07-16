# Changelog

All notable changes to openrate will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

No unreleased changes.

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
- README "Deployment modes" section documenting the two current shapes
  (self-hosted binary, embedded Go library) versus the planned, not-yet-built
  Vulos Cloud CP seam.

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
  than a current billed Vulos product; stale "Vulos Mail"/"Workspace"
  references and "Office" renamed to "Ofisi" in the site footer.

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
