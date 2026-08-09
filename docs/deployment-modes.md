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
| **C ABI** | `libopenrate.so` / `libopenrate.dylib`, six functions | your program is not Go, the per-call cost matters, **and** you want the no-network guarantee held structurally |

The last two are not something you have to build. **Fifteen languages have a
package already written** — and every one of them implements both, behind one
API, so choosing again later is a constructor change rather than a rewrite. The
index, with the right default for each language and the reasoning behind it, is
[`sdks/README.md`](../sdks/README.md).

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

Packages for both of those live in [`sdks/`](../sdks/README.md) — **bun, C,
C++, Deno, .NET, Elixir, Go, Java, Kotlin, Node, PHP, Python, Ruby, Rust and
Swift** — and most of them can start and supervise the sidecar process for you,
so "run a sidecar" is a constructor call rather than an ops task.

**4. Not Go, and you want the engine in your own address space?**
Use the [C ABI](c-abi.md). There are two reasons, and the second is the better
one.

The measurable reason: in-process conversion measured **3.7 µs mean** against
**33.5 µs** for the same conversion over a warm loopback HTTP connection —
about 9×, or 30 µs saved per call.

The structural reason: **an engine handle refuses `"refresh"`**, checked in Go
and again in C. Over the sidecar, "this deployment fetches nothing" is a
configuration you maintain; in-process it is a property of the handle you are
holding, and there is no argument you can pass to make an engine fetch. If what
you need is a currency calculator that provably talks to nobody — behind a
feature flag, in an air-gapped build, inside an audit — that is the mode that
gives it to you. See [Proving it sends nothing](zero-network.md).

Read the costs on the C ABI page before either: they are real, they include
"not fork-safe" and "one target has ever been executed", and for many hosts
they outweigh both reasons above.

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

So do the division before you decide. If a conversion sits behind a database
query, an outbound API call, or a model inference, the saving is somewhere
around a thousandth of the request and **9× is not an argument for embedding** —
take the sidecar and keep the fork-safety, the crash isolation and the empty
platform matrix. The ratio only starts to mean something when conversions are
the loop rather than a step inside one. Where the C ABI wins regardless of
timing is the row at the bottom of the surface table above: an engine handle
that cannot be made to fetch.

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
| Know when a conversion would succeed | ✅ `Refresher.Ready(ctx)`, blocking, no polling | ✅ poll `GET /readyz` — 200 when the snapshot has currencies, 503 carrying each source's `last_error` | ✅ `ready` on a refresher handle |
| **Fetch nothing, structurally** | ✅ build no `Refresher` | ❌ a running server is configured not to fetch | ✅ an engine handle **refuses** `"refresh"` |
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
