# openrate language packages

Use openrate from any of fifteen languages, **two ways**:

- **Direct** — in-process. Go imports the package. Every other language loads a
  C ABI shared library (`openrate_new`/`refresher_new`/`call`/`close`/`free`),
  built with `go build -buildmode=c-shared`. See [`../ffi/README.md`](../ffi/README.md).
- **Sidecar** — `openrate serve` as a separate process, either one you run or one
  the package spawns and manages for you on `127.0.0.1`.

**There is no streaming API.** openrate has no streaming operation, so there is
no `openrate_stream` — the omission is deliberate, not a gap.

| Language | Direct | Sidecar | Default |
|---|---|---|---|
| [go](go/) | package import — **no FFI, no shared library** | ✓ | **direct** |
| [c](c/) | ✓ links `libopenrate` | ✓ | **direct** |
| [cpp](cpp/) | ✓ header-only RAII (`openrate.hpp`) | ✓ | **direct** |
| [rust](rust/) | ✓ `libloading` | ✓ | direct |
| [swift](swift/) | ✓ SwiftPM, C interop | ✓ | direct |
| [deno](deno/) | ✓ `Deno.dlopen` | ✓ | direct |
| [bun](bun/) | ✓ `bun:ffi` | ✓ | direct |
| [node](node/) | ✓ koffi | ✓ | sidecar for servers |
| [python](python/) | ✓ `ctypes` | ✓ | **sidecar** |
| [java](java/) | ✓ FFM (JDK 22+) | ✓ | **sidecar** |
| [kotlin](kotlin/) | ✓ over the Java binding | ✓ | **sidecar** |
| [dotnet](dotnet/) | ✓ `LibraryImport` + `SafeHandle` | ✓ | **sidecar** |
| [ruby](ruby/) | ✓ `fiddle` (stdlib) | ✓ | depends — see README |
| [php](php/) | ✓ `FFI` extension | ✓ | **sidecar** |
| [elixir](elixir/) | **none, deliberately** | ✓ | **sidecar** |

The per-language reasoning is identical to llmux's and is written up once, in
[`../../llmux/sdks/README.md`](../../llmux/sdks/README.md): the Go runtime is not
fork-safe (so PHP-FPM, Unicorn and Python's default `fork` break), loading the
library replaces five of HotSpot's signal handlers, a Node thread that enters
the library never terminates, and an Elixir NIF cannot be killed or timed out.

## The engine/refresher split is enforced at the ABI

This is openrate's distinguishing property and the direct mode's real argument —
not speed.

`openrate_new` builds an **engine**: it starts no goroutine, opens no socket and
reads no environment. It answers `convert`, `rates`, `meta` and `load` from
whatever snapshot it holds. A **refresher** is a separate, explicit
`openrate_refresher_new` call with its own handle, and it is the only thing that
touches the network.

An engine handle **refuses** `"refresh"`. That is checked in Go and again in C,
so "the feature is off, therefore nothing is sent" is structural rather than a
promise in a comment. It is why `beepbite` was able to drop its HTTP client
entirely.

## Two things that will bite you

**`/healthz` is liveness, not readiness.** It answers before any rates have been
fetched. A sidecar that starts the server, sees a 200, and converts immediately
gets `{"error":"unknown or unreachable currency pair"}` for every call — and
exits 0. That false green is what `GET /readyz` exists to remove: it returns 200
only once the snapshot has currencies, and its 503 carries each source's
`last_error`, so a stuck start prints `ecb: connection refused` rather than a
bare timeout. Every sidecar package here polls it. `/readyz` sits outside
`/api/`, so unlike the `/api/v1/meta` poll it replaces, readiness polling never
touches the rate limiter. In-process the equivalent is `Refresher.Ready(ctx)`,
with no polling at all.

**The JSON API rate-limits `/api/` to 120 requests/minute per IP.** That is
anti-scraping for a public deployment and wrong for a loopback sidecar serving
one client, so the managed-sidecar packages pass `OPENRATE_RATELIMIT=0`; pass a
value to put it back. Neither `/healthz` nor `/readyz` is limited — only `/api/`
paths are — so neither a liveness check nor a readiness poll can spend it.

## Prebuilt libraries — what actually exists

| Target | Status |
|---|---|
| darwin/arm64 | built, smoke-tested, benchmarked |
| darwin/amd64 | **built but never executed** — not tested, not supported |
| linux/amd64 | CI job exists, has never run |
| **linux/arm64** | **not built** |
| **windows/amd64** | **does not exist — no DLL ships** |

**llmux's matrix is different** (it has linux/arm64 and no darwin/amd64). Do not
assume one covers the other. Build your own with
[`../scripts/build-ffi.sh`](../scripts/build-ffi.sh).

The sidecar path has none of these constraints — it needs only the `openrate`
binary for your platform.
