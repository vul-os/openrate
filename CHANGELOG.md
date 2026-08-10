# Changelog

All notable changes to openrate will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.7] - 2026-08-10

### Changed

- **The C ABI version string is derived, not declared.** `ffi/abi/version.go`
  used to hold a hand-typed `const Version`, and in v0.1.3 it came apart from
  `VERSION`: the release was tagged with the constant a patch behind, so the
  library told every host it was old when it was current. It now reads
  `openrate.Version`, `go:embed`ed from `VERSION` at compile time. The runtime
  string can no longer drift.

  **The header macro is a different case, and the distinction is worth stating.**
  A C consumer needs a literal to `strcmp` the runtime string against, and the
  preprocessor cannot read a file — so one literal is unavoidable. What is gone
  is a *human* typing it: `OPENRATE_ABI_VERSION` is generated from
  `openrate.Version` via `go generate ./...`, and four independent things now
  fail on a stale header — `check-ffi.sh` before any library is built, the C
  smoke test's own `strcmp`, a root-module test asserting regeneration is a
  no-op, and `ffi/abi`'s independent re-read. That is **detected, not
  impossible**, and it is the honest limit.

### Fixed

- **The release workflow's tag check could not fail.** It built with
  `-X main.Version=${TAG#v}` and then asserted the binary reported `${TAG#v}` —
  it injected the value it went on to check, so a tag disagreeing with `VERSION`
  passed. Now the tag is compared to the `VERSION` file first. **This can fail
  where it previously could not**; artifacts are otherwise byte-identical.

## [0.1.6] - 2026-08-10

### Changed

- **BREAKING (HTTP API): `GET /api/v1/rates` with a base the snapshot does not
  know is now `404 {"error":"unknown base currency"}`.** It answered `200` with
  `"rates": {}`, which a caller checking only the status code read as "no rates
  available" — and which is also exactly what a server that has not fetched yet
  answers, so the two were indistinguishable.

  `base` is not a filter over a collection; it is the pivot of the computation.
  Every entry means "1 base = rate units of X", so an unknown base does not
  describe an empty table — it makes the document's own definition false while
  echoing the invented code back in the `base` field. `Engine.Rates` and the C
  ABI have always refused it, and `/convert` has since 0.1.5, so one API version
  gave `NOTACCY` two different meanings at two endpoints.

  A snapshot with genuinely *no* currencies is still `200` with an empty book on
  all three surfaces. That is "nothing yet", and `/readyz` is the question for
  it. `ffi/include/openrate.h` and the eleven SDKs that documented the old
  behaviour moved in the same commit.
- **The interest-rate `[-1e12, 1e12]` band is applied at ingest** by
  `rates.Materialize`, not only by the grading layer. A source publishing a
  mis-scaled level no longer reaches a published `Series`, is not counted as
  corroboration, and a series with no admissible observation is dropped rather
  than published. `ratequality` keeps its own check, because `Assess` takes a
  `Series` a caller may have built by hand.

### Security

- **DNS rebinding.** 0.1.5 refused redirects, but nothing pinned the resolved
  address: a hostname answering with a public IP on the first lookup and
  `169.254.169.254` on the second still reached the metadata service. A source
  *hostname* must now resolve to a public address. An **IP literal** in a
  configured URL is dialled as written — that split is what makes the guard
  complete without refusing an operator's deliberately-private feed — and a host
  named in `HTTP(S)_PROXY` skips the check, because with a proxy the name is
  resolved at the far end and the check would protect nothing. Opt out with
  `OPENRATE_ALLOW_PRIVATE_SOURCES=1`.

  The refusal **names no address**. `Dialer.Control` is the right hook, but its
  `*net.OpError` carries the address it refused, and that string reaches the
  unauthenticated `/readyz` and `/api/v1/meta`.

### Fixed

- **C ABI: `openrate_close()` now cancels and waits for an in-flight `"refresh"`
  or `"ready"`**, so neither can put a request on the wire or write into an
  engine after the close has returned. A plain `closed` flag was tried and
  rejected on evidence: it is check-then-act and still leaked on 21 of 2001
  races. A fetch that ignores cancellation delays the close — that is the
  guarantee working, not a hang. `"stop"` remains reversible.

## [0.1.5] - 2026-08-10

A security release. Three HTTP responses change shape — see Changed.

### Security

- **Blind-SSRF oracle via `/readyz`.** None of the 16 HTTP clients set
  `CheckRedirect`, so the default policy followed up to 10 hops to any host,
  including `169.254.169.254` and loopback — and the resulting error landed in
  `Status.LastError`, published unauthenticated at `/readyz` and
  `/api/v1/meta`. All clients now refuse redirects. jsDelivr's `@latest` was
  checked on the network first and resolves server-side, so no allow-list was
  needed. The refusal uses `ErrUseLastResponse` rather than returning an error,
  because net/http wraps a returned error in a `*url.Error` **carrying the
  attacker-chosen Location** — the exact string that would have reached
  `/readyz`.
- **ECB and Frankfurter had no currency allow-list** where every other adapter
  does. A compromised feed injecting ~30k codes drives the N×N BFS to OOM.
- **IPv6 rate-limit buckets were per /128**: 1000 requests from one /64 at
  `rpm=1` were all allowed. Now /64 for IPv6, /32 for IPv4, with `::ffff:`
  unmapped. The bucket map is capped at 65536, and **only a bucket refilled to
  full is evictable** — a naive LRU would evict the attacker's drained bucket
  first and hand back the allowance.

### Fixed

- **A finite rate could produce an unencodable response.** `usable` admitted any
  positive finite value, so two sources quoting one pair at `1e-306` and `16.5`
  made the spread `+Inf`. v0.1.4 turned that from a silent empty 200 into a 500;
  this bounds the input that causes it. FX rates are admitted in
  **`[1e-18, 1e18]`** — symmetric in log space, so the band is closed under
  reciprocal, which is what makes edge admission bidirectional. The previous
  guard admitted values whose inverse overflowed, so `A→B` existed and `B→A`
  did not. Interest levels are `[-1e12, 1e12]`, symmetric about zero because
  policy rates are legitimately negative.
- **Four C ABI lifecycle bugs.** A `"start"` racing `openrate_close` left a
  refresh loop nothing could stop — reproduced making 5,166 requests in the
  half-second *after* close returned, with `openrate_open_handles()` reporting
  0. Under `-race` that reproduces 31.7% of the time while the detector itself
  reports nothing, because it is not a data race. Also: refreshers orphaned by
  a racing engine close, refreshers never detached from their engine, and an
  `interval_ms` overflow that turned the most conservative cadence a host can
  ask for into a 1 ms one.
- **The Yahoo adapter had never emitted a single edge.** Its length check was
  unsatisfiable — `"USDZAR=X"` is 8 characters and the check demanded 9 — behind
  an error message that read as a transient upstream problem. A second bug was
  found while fixing it: `From` was hard-coded `"USD"`, so a configured
  `EURGBP=X` would have produced a wrong `USD→GBP` edge. The feed stays opt-in.

### Changed

- **`/api/v1/convert` with an unknown currency code returns 404**, not 200 with
  rate 1 and grade B. The library and the C ABI both already refused it; only
  the HTTP layer did not. **`/api/v1/rates?base=…` deliberately keeps the old
  behaviour** — it is a published contract asserted across five SDKs and the
  docs, and changing it belongs in one commit that moves all of them together.
  Both are now pinned by tests.
- **An out-of-range `amount` returns 400.** `1e999` silently became `1.0`.
- Clients sharing a /64 now share a rate-limit budget.
- C ABI: `"start"` on a closed handle returns an error; `"meta"`'s `sources`
  covers open refreshers only; `interval_ms`/`fetch_timeout_ms` above
  9223372036854 are refused rather than wrapping.

## [0.1.4] - 2026-08-10

### Fixed

- **v0.1.3 shipped a library that misreported its own version.** `VERSION` was
  bumped to 0.1.3; `ffi/abi/version.go` and `ffi/include/openrate.h` were not, so
  `openrate_abi_version()` answered `0.1.2`. That breaks the staleness detection
  the symbol exists to provide, in the direction that reports "old" for a current
  library.

  Every check run before tagging was green, because **`ffi/` is a separate Go
  module and `go test ./...` at the root cannot see it** — the pin that would
  have caught this (`ffi/abi`'s `TestVersionMatchesTheVERSIONFile`) lives inside
  the module that was wrong. `TestFFIABIVersionTracksTheVERSIONFile` now runs in
  the root module and reads ffi/'s source as text, precisely because importing
  across that boundary is not possible.
- **A stale binary could pass a readiness check.** An openrate predating
  `/readyz` has no such route, so its console catch-all answers `200 text/html`,
  and a probe testing only the status code read that as ready against an engine
  holding zero rates — the same false green `/readyz` was added to remove.
  Readiness now requires the document to say `"ready": true`, and a non-JSON 200
  is reported as "this binary predates the readiness endpoint".
- **`ffi/README.md` claimed `linux/amd64` was "built and smoke-tested by the
  `ffi` job on `ubuntu-latest`".** The job is configured and has never run.
- **Dead streaming code in `sdks/c`.** `http_sse` and a comment about
  reassembling "a streamed answer from its chunks" were carried over from llmux
  into a product whose own README states it has no streaming operation. Nothing
  called them.

## [0.1.3] - 2026-08-09

Additive. Nothing that compiled or called against v0.1.2 changes behaviour.

### Added

- **`GET /readyz` — a real readiness endpoint.** `/healthz` answers the instant
  the listener binds, before any source has been fetched. That is correct for a
  liveness probe and wrong for a client deciding when to send its first request,
  and the gap was not theoretical: every managed sidecar in [`sdks/`](sdks/) was
  written against `/healthz`, saw a `200`, converted immediately, got
  `unknown or unreachable currency pair` for every pair, and **exited 0** — a
  false green that also disguises itself as a bad currency code.

  `/readyz` returns `200` once the snapshot has currencies and `503` before
  that, and the `503` carries each source's `last_error`, so a stuck start
  prints `ecb: dial tcp: connection refused` rather than an unexplained timeout.
  It sits **outside `/api/`** deliberately: the rate limiter covers `/api/` only,
  so polling readiness cannot exhaust the budget you are waiting to use — which
  is exactly what polling `/api/v1/meta`, the workaround it replaces, could do.
  In-process the equivalent is unchanged and needs no polling:
  `Refresher.Ready(ctx)`.

  Both guarantees are mutation-tested. Reporting ready on an empty engine fails
  `TestReadyIsNotReadyBeforeAnyRateArrives`; extending the limiter to cover
  `/readyz` fails `TestReadyzIsNotRateLimited` on poll 3, and that test carries a
  control proving the limiter is genuinely on.

### Fixed

- **`docs/api.md` described `/healthz` as "used as a readiness probe".** That
  sentence was the defect above, written down.
- **All fifteen SDKs now wait on `/readyz`.** Every one of them had a readiness
  bug: `go`/`rust`/`swift` polled `/api/v1/meta` behind a backoff that existed
  purely to dodge the 120/min limiter (both now unnecessary); `node`/`bun`/`deno`
  discarded the cause and **exited 0** on timeout — the same false green this
  release removes; `python`/`ruby`/`php`/`elixir` had no readiness wait in the
  library at all, only a copy-pasted loop in each example. Timeouts now name the
  source and its error rather than reporting an empty currency list.
- **Two real client bugs found while doing it.** `HTTP_PROXY` was honoured for
  loopback by python's urllib and .NET's `HttpClient`, so a user behind a proxy
  could not reach their own child process; both now bypass it, and bun/deno
  document `NO_PROXY=127.0.0.1` since neither has a clean per-request opt-out.
  Three SDKs also carried a comment claiming `/healthz` "goes through the same
  per-IP limiter as the API" — untrue, and deleted.
- **One JSON encoding, not two.** `/readyz`'s 503 was hand-rolled and shipped
  compact next to an indented 200. Every body now goes through one encoder.

### Added (docs)

- **Five new guides**, taking the published set from nine pages to fourteen:
  [docs/quickstart.md](docs/quickstart.md) (one starting point per audience),
  [docs/deployment-modes.md](docs/deployment-modes.md) (library / CLI /
  sidecar / C ABI, with the measured cost and size of each),
  [docs/zero-network.md](docs/zero-network.md) (the Engine/Refresher split as
  a counted guarantee, with the tests and their controls),
  [docs/c-abi.md](docs/c-abi.md) (the map into `ffi/`, with the costs in one
  screen) and [docs/troubleshooting.md](docs/troubleshooting.md).
- **`scripts/check-contrast.mjs`** — recomputes every text tier's WCAG
  contrast from the hex values that ship. `--selftest` breaks it eight ways.
- **`scripts/check-docs-chrome.mjs`** — no `<footer>` on the docs page, the
  sidebar pinned and the shell packed left, no origin under `site/` that a
  browser would fetch, the sidebar's groups agreeing with the viewer's `DOCS`
  array, and every fenced language present in the vendored highlighter.
  `--selftest` breaks it seventeen ways. The suite's canonical chrome gate
  remains `vulos-cloud/scripts/check-suite-chrome.mjs`; this mirrors exactly
  one of its rules, and only because that gate cannot run in this repo's CI.

### Fixed

- **The two muted text tiers no longer carry text below WCAG AA**
  (`OWNER-DECISIONS.md` §6). `--text-5` measured **2.04:1** at worst against
  the page and was painting the docs' ordered-list markers, the `×` and `=` in
  the reconciliation equation and its explanatory note; those are now
  `--text-4`/`--text-3` at **4.53–5.83:1**. The one remaining use is a
  decorative `·`, now `aria-hidden` in the markup so the exemption is
  structural. Dead `.policy-card` rules deleted.
- **Both pages overflowed a 320px viewport by 10px.** The header's own
  contents — the wordmark plus three 44px controls — came to 330px. Neither
  the tap-target floor nor the Vulos mark may give, so the gaps and the
  wordmark's size do, below 380px.
- **The landing displayed "33.5 MS" where it meant "33.5 µs".**
  `text-transform: uppercase` maps `µ` to a capital Greek Mu, which renders as
  an M — so the one performance claim on the page read as milliseconds.
- **`c` and `python` code blocks rendered unhighlighted.** The vendored
  highlight.js is a custom build and carried neither; rebuilt at 34,055 bytes
  with both, and the new gate fails on any fence the bundle cannot serve.

### Changed

- **The docs viewer's sidebar is a real index**: five collapsible groups
  remembered across navigation, the current page's headings nested underneath
  it at the widths where the on-this-page rail is not drawn, search ranked
  title → heading → prose with heading hits navigating into the page, full
  keyboard control of the results (arrows, Enter, Escape, `/`), and the
  sidebar's own scroll position preserved.

## [0.1.2] - 2026-08-09

Every change below is **additive to the public API**. `Options` is unchanged
field-for-field from v0.1.1 and `Start` still works, so anything that compiled
against v0.1.1 still compiles. The version stays on the 0.1 line for that
reason.

### Changed

- **Library-first: `Engine` computes, `Refresher` fetches, `serve` is optional.**
  The root package used to have exactly one embedding path, `Start`, which
  bound a loopback listener and began fetching from the network before it
  returned — none of that optional, none of it skippable. It is now three
  separate, explicit types: `NewEngine` builds an inert value that starts no
  goroutine and opens no socket (see `Convert`, `Rates`, `Load`);
  `NewRefresher` is the only thing that touches the network, and only once
  `Refresh` or `Run` is called; `serve.New` answers HTTP from an `Engine` it
  does not own. `Start` still works — it is now built on top of these three —
  but is deprecated in favour of composing them directly. See
  [docs/library.md](docs/library.md).
- **The pure core and the sources are now importable on their own.**
  `internal/graph` and `internal/quality` moved to `fx` (so it is
  `fx.Assessment`, not `quality.Assessment`), and `internal/sources` moved to
  `fxsource`. Both packages import nothing outside the standard library and
  read no environment variable — `fxsource.Build` is pure; the one
  `os.Getenv` in the module is behind the explicit `fxsource.FromEnv`/
  `FromEnvSpec` opt-in.
- **`internal/store` is deleted**, replaced by `Refresher`, which is
  constructed explicitly instead of started implicitly.
- **The HTTP layer moved under `serve/`**: `internal/api` → `serve`,
  `internal/ratesapi` → `serve/interest`, `internal/ratelimit` →
  `serve/ratelimit`, `web/` → `serve/web`. The interest-rate stack itself
  (`rates`, `ratesources`, `ratestore`, `ratequality`) and `redact` stay
  under `internal/` and serve-only — there is no importable
  `Engine`/`Refresher` equivalent for interest rates yet, only the
  deprecated `Start(Options{Interest: true})`.
- **The web console can be compiled out.** A build tagged `noui` removes
  `serve/web/ui.html` and its bundled `THIRD-PARTY-NOTICES.txt` from the
  binary entirely, rather than merely not serving them, saving 66,256 bytes
  on this checkout's `cmd/openrate` build. More importantly for embedders: a
  program that imports `openrate` for `NewEngine`/`NewRefresher` and never
  touches `serve` links zero bytes of the console regardless of the tag —
  Go's linker drops the embed when nothing calls the handler that references
  it. See [docs/library.md](docs/library.md#the-noui-build-tag) for the
  measured host-program sizes.
- Test count: 213 → 254.

## [0.1.1] - 2026-08-09

### Added

- **Signed, checksummed releases — and a verifier that fails closed.** Nothing
  in the release pipeline previously vouched for the bytes it published. The
  release workflow now stages the published archive into `release/`, emits a
  `SHA256SUMS` manifest **over that directory** (so "published" and "covered" are
  the same set by construction, not two hand-maintained lists), asserts one
  manifest line per staged asset, and attaches a sigstore build-provenance
  attestation minted from the workflow's OIDC identity — no long-lived signing
  key exists, so there is none to leak, own or rotate. A release that staged
  nothing, or whose manifest does not cover what it staged, is now a **red**
  release rather than a green one with an empty manifest.
  `scripts/verify.sh` is the user-facing half: it fetches the manifest, looks up
  the **exact** entry for the requested asset (string comparison on field 2 — a
  substring/regex match would let `…tar.gz.sig` answer for `…tar.gz`) and
  compares digests. Two outcomes only, verified or non-zero with a distinct
  diagnostic; there is no `--skip-verify` and **no path where an absent
  `SHA256SUMS` means "nothing to check"** — that shrug is the bug the file exists
  not to have, because it converts *"I don't know"* into *"it's fine"*. The
  release job runs `verify.sh` against its own output before publishing, so
  producer and consumer cannot drift apart silently.
- **A CI failure matrix for the verifier** (`bash scripts/verify.sh --selftest`,
  also a release-job step) — 24 synthetic-origin cases covering every refusal:
  manifest 404, manifest served as an HTML error page (both by content-type and
  by sniffing a lying one), empty/junk/truncated manifest, no entry for the
  asset, the `.sig` and regex-wildcard name traps (one arranged so a naive
  substring match would report **exit 0 on an artifact nobody vouched for**),
  asset 404, asset served as HTML, truncated download, digest mismatch,
  plaintext origin, missing curl or digest tool, and `--attest` with no `gh`
  installed. Each case asserts the exit code **and** that a diagnostic was
  printed — a guard that aborts silently reads as a crash, not a refusal, and
  "died at a pipeline under `set -e`" is precisely how a sibling installer's
  unreachable guard shipped.

- **Policy rates have a UI.** `/api/v1/interest/*` has shipped in the binary for
  some time with nothing rendering it. The app now has a **Policy rates**
  section: featured areas as cards with the rate, its grade, a stepped history
  sparkline and the change over the trailing year, plus a disclosure listing
  every other area carried. The sparkline is stepped rather than smoothed
  because a policy rate holds flat and jumps at a meeting — interpolating
  between decisions would draw moves that never happened. The grade earns its
  keep here more than anywhere: the BIS still publishes legacy pre-euro national
  series last observed in the 1990s, and the grade plus an explicit observation
  date is what stops a 1998 number reading as today's.
- **The rate board is sortable and filterable.** Sort by grade, age, hop count,
  rate or code; filter by code *or* full currency name. The useful question is
  rarely "what is 1 ZAR in AED", it is "which of these numbers should I not lean
  on", and that needs sorting by grade.
- **`npm --prefix web run shots`** (`web/scripts/shots.mjs`) captures every
  screenshot on the README and the site from a running engine, in both themes
  and at phone width, and re-encodes them to WebP. Nothing shown to a reader is
  drawn by hand, so the images cannot drift from the app.
- E2E coverage for the policy section, board sorting and board filtering,
  including the disabled-interest-engine configuration
  (`-interest-sources ""`), which must degrade to a message rather than throw.

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

- **One logo, not two.** The repo had drifted into carrying two different marks:
  the two offset arcs used by the app, the site, the favicon and the README, and
  a separate square tile in `brand/logo.svg` drawing two swapping arrows. The
  arcs win and the arrows are retired — a rate is a *ratio*, two quantities held
  against each other, which is what the offset arcs show, whereas swapping
  arrows are the commonest glyph in the category and collapse into mush at
  16 px. `brand/logo.svg` is now the arcs on the standard Vulos product tile
  (128 box, `rx` 28, near-black ground tinted toward the product's own hue).
  That ground moved from `#0F1D2E` to `#08111D`: the old value sat at roughly
  twice the luminance of the fleet's other tiles and disappeared against the
  dark product grid these are displayed on. The bare mark's title and
  `aria-label` are now "openrate" rather than "open rate", matching the product
  name, in all four byte-identical copies.
- **The interface was rebuilt around showing the working.** A rate's path
  through the currency graph is now drawn — each node a currency, each hop
  carrying its own rate, source and age — and cross-source disagreement is
  plotted on a scale zoomed to the quotes, with the mean marked. This is the
  product's actual claim and it was previously buried in a table of numbers.
  The same panel is shared by the converter and every expanded board row, so
  the two cannot drift.
- **New art direction, in the app and on the site.** Security-print engraving:
  a generated guilloché rosette (a real hypotrochoid, drawn in SVG), hairline
  rules, grades struck as seals, and a warm banknote-paper light theme rather
  than a cold white one. Type is Instrument Serif for display, Archivo for the
  interface (its width axis gives the board genuinely condensed lettering) and
  JetBrains Mono for every figure. **Inter has been dropped.** All faces stay
  vendored via `@fontsource` — never fetched from Google Fonts.
- **The site left the shared dark product template.** `site/index.html` and
  `site/docs.html` are rebuilt on the app's own theme and share
  `site/assets/site.css`; the docs viewer gains cross-document search, per-block
  copy buttons and the same theme switch, with the choice shared with the app.
- **The embedded UI is now one hand-written HTML file, not a Vite/React app.**
  `web/ui.html` (590 lines, inline `<style>` and `<script>`, vanilla JS, no
  build step) replaces the compiled `web/dist` bundle. `web/embed.go` now
  exposes `Handler()` (was `FS()`); `cmd/openrate/main.go` and root
  `openrate.go` were updated to call it. The converter and the rates board —
  including the "show the working" panel (graph path, hops, sources, spread)
  and the live A–D grade badge — are reimplemented in plain HTML and carried
  over intact; what a reader sees of the FX side is unchanged, only how it
  ships. No webfonts ship with it any more — system font stacks only.

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

### Removed

- `quality.Assessment.Explain` and `ratequality.Assessment.Explain`: dead code
  with no non-test caller in an `internal/` package, whose doc comment claimed
  it was "used in docs/tooltips" when the tooltip is built independently in
  `web/src/App.jsx`.
- **The vendored mermaid bundle (3.5 MB).** `site/docs.html` loaded it eagerly on
  every documentation page to render diagrams in documents that contain zero
  diagrams — not one ```` ```mermaid ```` fence exists in the repo. Its
  third-party notice and the old shared-template stylesheet went with it.
- **Stale pricing and hosted-plan claims from the structured data.** The JSON-LD
  in `web/index.html` still advertised $9/$39/$149 tiers and an "openrate Cloud"
  with an SLA, months after commit `333882e` removed those claims from
  everywhere a human could see them. Search engines could still read them.
- **The Node/Vite toolchain and every npm-derived byte in `web/`.** Deleted
  `web/dist/`, `web/src/`, `web/e2e/`, `web/public/`, `web/scripts/`,
  `web/tools/`, `web/index.html`, `web/test-results/`, `package.json`,
  `package-lock.json`, `vite.config.js` and `playwright.config.js`. `web/` now
  ships zero npm-derived code, with no build step, no npm, and no
  `node_modules` anywhere in the build path. `scripts/gen-notices.sh` no
  longer shells out to `npm ci`/`license-checker` against `web/`; the
  Geist-font and vendored-JS attribution it still owes now reads directly off
  the files `site/` vendors under `site/assets/fonts/` and
  `site/assets/vendor/`, which were the ones actually requiring it all along.
- **The interest-rate ("Policy rates") UI**, added above earlier in this same
  Unreleased cycle, is gone along with the in-binary docs viewer, the accuracy
  explainer page, the marketing footer, the Vulos mark and the guilloché
  decoration in the app. `/api/v1/interest/*` is untouched and still serves
  data — it just has no page rendering it right now.

### Fixed

- **Every screenshot on the site rendered stretched.** The stylesheet reset
  paired `max-width: 100%` with no `height: auto`, so an `<img>` carrying an
  explicit `height` attribute — which they all do, to reserve layout before they
  load — had that height applied literally while its width was constrained.
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

- Fixed a goroutine and `time.Ticker` leak in the rate limiter: `Limiter` now
  has a `done` channel and `Stop()`, `gc()` selects on it, and `Local.Close()`
  drains the background sweep goroutine on shutdown.
- `New(0, _)` no longer divides by zero — `rpm`/`burst` are clamped to at
  least 1 so `Retry-After` is always finite.
- `GET /api/v1/convert` now rejects non-finite (`Inf`/`NaN`) `amount` values
  with a clean `400` instead of letting them poison the arithmetic and produce
  a truncated `200` response body.

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

### Testing / CI

- **Removed a race from the E2E suite.** The app issues three independent
  requests on mount (`/meta`, `/rates`, and the interest pair) and each replaces
  a different part of the tree as it resolves. Tests that clicked as soon as
  their own target appeared were racing the other two: Playwright passed the
  actionability check, React swapped the node underneath, and the click landed
  on a detached element. It reproduced roughly once in forty runs and only under
  parallel load. Interaction tests now establish the precondition explicitly via
  a shared `settled()` helper rather than relying on timing; verified over 260
  consecutive executions with no failure.
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
- **`web-e2e` (Node/Playwright/`vite build`/dist-staleness) is gone from CI**,
  replaced by a `go test ./web -v` step. `web/embed_test.go` asserts the
  embedded UI is present, non-trivial, exposes the converter/board markup and
  the `/api/v1/*` calls they issue, and references no external origin — the
  same boot-guard properties the Playwright suite checked, now against the
  embedded bytes directly and with nothing to install.

## [0.1.0] - 2026-06-28

Initial release.

[Unreleased]: https://github.com/vul-os/openrate/compare/v0.1.7...HEAD
[0.1.7]: https://github.com/vul-os/openrate/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/vul-os/openrate/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/vul-os/openrate/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/vul-os/openrate/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/vul-os/openrate/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/vul-os/openrate/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/vul-os/openrate/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/vul-os/openrate/releases/tag/v0.1.0
