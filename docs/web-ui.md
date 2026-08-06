# Web UI

The interface is a single hand-written HTML document, `web/ui.html` — inline
`<style>` and inline `<script>`, vanilla JS, **no build step, no npm, no
bundler, no `node_modules`**. It is embedded into the binary verbatim via
`go:embed` and served at `/`. There is nothing to install and nothing to
compile: cloning the repo gets you the exact bytes a browser loads.

## What's in it

- **Converter** — amount, from/to currency, live result, and the rate's
  quality grade as a badge. A **"Show the working"** disclosure expands into
  the graph path taken, each hop's rate/source/age, the individual source
  quotes behind the pair, and their spread — the same provenance the API
  returns, laid out for reading. On a triangulated pair it also multiplies the
  legs *as displayed* against the rate *as displayed* and prints the
  **residual** between them. That residual is display rounding and nothing
  else — the engine's product is exact at full precision — but it is real, it
  is usually non-zero, and showing it beats letting a reader find it with a
  calculator. See [the graph model](graph-model.md).
- **Rates board** — every currency reachable from a base, sortable by grade,
  age, hop count, rate or code, and filterable by code or currency name.
  Expanding a row shows the same "working" panel as the converter.
- **Theme** — dark and light, following the system preference by default, with
  a manual toggle. The choice is stored under the `or-theme` `localStorage`
  key, the same one the marketing site uses, so it carries across both.
- A small **licenses** link in the topbar points at `/licenses.txt` — the
  binary's own copy of `THIRD-PARTY-NOTICES.txt` (see below).

No webfonts ship with the UI — it uses system font stacks
(`ui-sans-serif`/`ui-monospace` and friends) only.

## Why hand-written HTML instead of the fleet's React+Vite

**This is not a ratified architecture decision.** The migration from a
Vite/React app to this single `web/ui.html` landed in commit `95a706c`, whose
own message says it was "recovered from an uncommitted working tree — made in
another session and left unstaged... described here from the diff rather than
from intent, since the work is not mine," and that "the served UI was NOT
exercised." `CHANGELOG.md`'s entry for the same change (`web/ui.html`, 590
lines at the time) documents *what* changed — the file, the removed
`web/dist`/npm toolchain, the reimplemented converter and rates board — but,
like the commit, carries no stated reason for choosing a hand-written,
buildless file over the fleet-standard React+Vite stack. No other commit,
issue, or doc in this repo records one either.

So: the current state is real and in production (`go:embed`, no npm, no
`node_modules`, pinned by `web/embed_test.go`), but the choice to get there
was never made by a reviewing human — it was recovered, not decided. Plausible
reasons exist (no build step for a single-page interface with two views;
nothing to keep in sync with a separate frontend toolchain; smaller attack
surface with no third-party JS dependencies to audit or update) but they are
this document's speculation, not the original author's stated intent, and are
deliberately not presented as settled rationale.

**This entry exists to flag that gap, not close it.** Reverting to a built
frontend, or affirmatively ratifying the hand-written approach, are both still
open — a human should make that call and replace this section with the real
reasoning once they do.

**Not in it, on purpose:** no in-binary docs viewer, no accuracy-methodology
page, no marketing footer, and no interest-rate/"Policy" UI.
`/api/v1/interest/*` is unaffected and still serves data — it currently just
has no page rendering it in this UI.

## Developing

There is no dev server and nothing to watch/rebuild. Edit `web/ui.html`
directly and reload the browser:

```bash
go run ./cmd/openrate    # serves the UI at http://localhost:8080/
```

`web/embed_test.go` (run via `go test ./web`) pins the markup and script the
product depends on — the converter, the board, the API calls they issue, the
theme key — so a careless edit that drops one of them fails a Go test rather
than only being noticed by eye. It also asserts the page references no
external origin (no CDN scripts, no remote fonts, no third-party fetches).

## Serving from the binary

The compiled binary serves the UI at `/` — every GET/HEAD request gets
`web/ui.html`, except `/licenses.txt`, which serves a separate embedded file
(`web.Licenses`, `text/plain`, `X-Content-Type-Options: nosniff`) — the same
`THIRD-PARTY-NOTICES.txt` generated at the repo root, copied into `web/` by
`scripts/gen-notices.sh` because `go:embed` patterns can't reach outside
`web/` with `..`. `web/embed_test.go`'s `TestLicensesInSyncWithRoot` fails
`go test ./web` if that copy ever drifts from the root file. The UI itself
talks to `/api/v1/*` directly. When [embedding openrate as a
library](library.md), the UI is **off by default** — set `Options{ServeUI:
true}` to mount it (and `/licenses.txt` along with it).

## Related

- [API reference](api.md) — the endpoints the UI calls
- [Accuracy & quality](../ACCURACY.md) — the model behind the grade the UI
  shows (there's no in-app page for it any more; this is the full writeup)
- [THIRD-PARTY-NOTICES.txt](../THIRD-PARTY-NOTICES.txt) — what `/licenses.txt`
  serves
