# Which mode should I choose

There are four ways to run openrate. They are not tiers — none is the "real"
one and none is a consolation prize — and the right answer is usually decided
by one fact about your program rather than by a feature comparison.

## The short version

| | You get | Choose it when |
|---|---|---|
| **Go library** | `Engine`, `Refresher`, optionally `serve` | your program is Go |
| **CLI / server** | one binary, JSON API, web console | you want a service, or you are exploring |
| **Sidecar** | the same binary, on loopback, spoken to over HTTP | your program is not Go, or you want openrate restartable and crash-isolated |
| **C ABI** | `libopenrate.{so,dylib,dll}`, six functions | your program is not Go **and** the per-call cost matters |

## The decision, in order

**1. Is your program written in Go?**
Then use the [library](library.md). There is no benefit to any other mode:
`NewEngine` starts nothing, importing it costs about 4.7 MB of binary (most of
which is `net/http` and TLS, which you almost certainly already link), and you
skip serialization entirely. If you also want to expose the JSON API to
something else, add `serve` — it answers from the same `Engine`.

**2. Do you want a service, not a dependency?**
Run the binary. `go run ./cmd/openrate`, `docker run`, or a release artifact.
This is also the right answer when several programs need the same rates: one
refresher, one set of upstream calls, one snapshot everybody sees.

**3. Not Go, and 30 microseconds per conversion does not matter to you?**
Run it as a **sidecar** and talk to it over loopback. This is the default
recommendation for every non-Go host, and it is not a compromise — it is
simpler, it has none of the costs in step 4, and it makes openrate
independently restartable and upgradable.

**4. Not Go, and the per-call cost genuinely matters?**
Use the [C ABI](c-abi.md). In-process conversion measured **3.7 µs mean**
against **33.5 µs** for the same conversion over a warm loopback HTTP
connection — about 9×, or 30 µs saved per call. Read the costs on that page
first; they are real, they include "not fork-safe", and for many hosts they
outweigh the saving.

## Cost, measured

Both sides of the timing comparison were driven by [one C
program](../ffi/bench/bench.c) against the same snapshot, asking for the same
conversion, checking both answers before believing either timing. 30,000
iterations after 1,000 warm-up calls, Apple M-series darwin/arm64, Go 1.25.12.

| | mean | p50 | p99 |
|---|---|---|---|
| in-process (C ABI) | **3.7 µs** | 3.5 µs | 8.5 µs |
| loopback HTTP | 33.5 µs | 31.5 µs | 66.8 µs |

Read that gap as a **floor**. The HTTP side is deliberately flattered: one
keep-alive connection for the whole run, no TLS, no proxy, same machine, and a
client that allocates nothing per request. A real sidecar — fresh connections,
a language's HTTP library, another container, a service mesh — is slower, often
by a lot.

And 30 µs is 30 µs. Converting a handful of numbers per web request, it is
invisible next to everything else you do. Pricing a book of orders in a loop,
it is the whole cost.

## Size, measured

| | Size | Note |
|---|---|---|
| `cmd/openrate`, default | 10,035,714 bytes | engine + API + console |
| `cmd/openrate`, `-tags noui` | 9,969,458 bytes | **66,256 bytes** less |
| a Go host importing `openrate`, never `serve` | 7,115,330 bytes | **zero** console bytes linked |
| `libopenrate` darwin/arm64 | 6,682,274 bytes | the shared library |
| `libopenrate` darwin/amd64 | 7,120,680 bytes | built, not executed on this machine |

Two things that are easy to get wrong:

- **`serve.Options{UI: false}` does not make the binary smaller.** It decides
  what is mounted at runtime; `//go:embed` has already happened by then.
- **`-tags noui` only matters to a program that imports `serve`.** A host that
  imports the library and never touches `serve` links none of the console
  either way. See [Proving it sends nothing](zero-network.md) for how that is
  gated rather than asserted.

## What each mode can reach

Not every surface exists in every mode, and the gap is on one side only.

| Surface | Library | CLI / sidecar | C ABI |
|---|---|---|---|
| Convert, with full provenance | ✅ `Engine.Convert` | ✅ `GET /api/v1/convert` | ✅ `convert` |
| All rates against a base | ✅ `Engine.Rates` | ✅ `GET /api/v1/rates` | ✅ `rates` |
| Metadata and source status | ✅ `Refresher.Status` | ✅ `GET /api/v1/meta` | ✅ `meta` |
| Install your own rates | ✅ `Engine.Load` | ❌ the server is read-only | ✅ `load` |
| Fetch on demand / on a schedule | ✅ `Refresher` | ✅ built in | ✅ refresher handles |
| Web console | ✅ via `serve` | ✅ | ❌ |
| **Interest rates** | ❌ **`internal/`, serve-only** | ✅ `/api/v1/interest/*` | ❌ |

That last row is the honest gap. The interest-rate stack — `rates`,
`ratesources`, `ratestore`, `ratequality` — lives under `internal/` and is
wired up only by `serve/interest`. There is no importable `Engine`/`Refresher`
pair for it, and the only in-process path is the deprecated `Start`, which
binds a listener and fetches. If you need policy and reference rates in
process today, you are running the server whether or not it looks like it. See
[Interest rates](interest-rates.md).

## Mixing modes

These are not exclusive, and the combination people actually want is common
enough to name:

**Embed the engine, expose the API.** Import `openrate` and `serve` in your own
program, wire `serve.Routes(mux)` onto a mux you already own, and openrate
becomes one API among several in a service you already operate — no second
process, no second port, your own middleware.

**Embed the engine, feed it yourself.** Import `openrate` and `fx`, never
`fxsource`, and drive `Load` from your own upstream. openrate then contributes
the graph, the triangulation and the grading, and contributes nothing to your
egress. This is also the smallest build: `fx` alone adds 104,672 bytes over an empty
`main`.

## Related

- [Quickstart](quickstart.md) — the first ten minutes in each mode
- [Embed as a Go library](library.md)
- [Use it from another language](c-abi.md)
- [Configuration](configuration.md) — flags and environment for the binary
- [Troubleshooting](troubleshooting.md)
