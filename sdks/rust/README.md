# openrate for Rust

Two modes, both supported, both with a runnable example in [`examples/`](examples).

| | what it is | the module |
| --- | --- | --- |
| **Direct** | `dlopen`s `libopenrate` and runs the engine **inside your process** | [`openrate::direct`](src/direct.rs) |
| **Sidecar** | spawns and supervises `openrate serve`, talks HTTP | [`openrate::sidecar`](src/sidecar.rs) |

## The engine/refresher split is the reason to embed

Not latency. openrate's compute is a graph lookup either way; the interesting
property is what your process *cannot* do.

```
Engine     computes.  openrate_new() starts no thread, opens no socket, reads no
                      environment variable and sends no packet.
                      Methods: convert, rates, meta, load.

Refresher  fetches.   openrate_refresher_new() is a SEPARATE call with its own
                      handle and lifetime, and it is the only thing here that
                      can open a socket. Even constructing it sends nothing.
                      Methods: status, refresh, start, stop, ready.
```

That split is enforced **at the ABI**, not by convention. An engine handle
refuses `"refresh"`:

```
refuse:    openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
```

`tests/direct.rs::an_engine_handle_refuses_refresher_methods` asserts it for all
five refresher methods, and `examples/direct.rs` prints it. A program that never
calls `Engine::refresher` has no code path to the network — and in Rust the type
system says so too, because `refresh` exists only on `Refresher`.

Feed an engine without a refresher with `Engine::load`: rates from a cache, a
file, a vendor feed, a fixture.

## Run the examples

```
./sdks/rust/examples/run.sh direct     # zero packets
./sdks/rust/examples/run.sh refresh    # then a live ECB fetch
./sdks/rust/examples/run.sh sidecar
```

Real output — darwin/arm64, rustc 1.97.1, Go 1.25.12, openrate 0.1.2,
`libopenrate-darwin-arm64.dylib` 6,682,274 bytes:

```
==> direct, engine only (no Refresher, so no socket opens)
library:   /Users/pc/code/vulos/openrate/dist/ffi/libopenrate-darwin-arm64.dylib
abi:       0.1.2
engine:    handle 1
handles:   1 open
empty:     openrate: convert USD->ZAR: unknown or unreachable currency pair
refuse:    openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
load:      {"built_at":"2026-08-08T16:00:00Z","currencies":["EUR","GBP","USD","ZAR"]}
USD->ZAR:  result=1842.0000000000002 rate=18.42 hops=1 grade=C
EUR->ZAR:  result=2001.3330000000003 rate=20.013330000000003 hops=2 grade=C
JPY->ZAR:  openrate: convert JPY->ZAR: unknown or unreachable currency pair
rates XXX: openrate: rates base XXX: unknown base currency   (HTTP would answer 200 with an empty book)
rates ZAR: 1588 bytes

no Refresher was constructed, so this process opened no socket.
handles:   1 open

==> direct, with a Refresher (THIS FETCHES FROM ECB)
refresher: handle 2, no packet sent yet
status:    {"sources":[{"name":"ecb","edges":0,"last_ok":"0001-01-01T00:00:00Z"}]}
handles:   2 open
refresh:   {"sources":[{"name":"ecb","edges":29,"last_ok":"2026-08-09T13:45:00.034469Z"}]}
EUR->USD:  result=115.35 rate=1.1535 hops=1 grade=C

==> sidecar (child process over HTTP)
sidecar:   http://127.0.0.1:52487
healthz:   "ok"  (liveness only — no rates implied)
ready:     after 724.324ms
EUR->USD:  result=115.35 rate=1.1535 hops=1 grade=C
rates EUR: 24292 bytes
rates XXX: HTTP 200, empty book = true   (the C ABI returns "unknown base currency")
bogus:     HTTP 404: {"error":"unknown or unreachable currency pair"}
stopping:  Drop kills and reaps the child
```

Note `EUR->ZAR` above: it has `hops=2` because that pair exists only as
EUR→USD→ZAR. The hop count, the path and the per-leg provenance are part of the
answer, not something you reconstruct.

## Direct

```rust
use openrate::direct::Engine;

let eng = Engine::open(Some(r#"{"base":"ZAR","quiet":true}"#))?;
eng.load(r#"{"edges":[{"from":"USD","to":"ZAR","rate":18.42,"source":"mine"}]}"#)?;
let json = eng.convert(r#"{"from":"USD","to":"ZAR","amount":100}"#)?;

// Only now can this process reach the network:
let refresher = eng.refresher(Some(r#"{"sources":"ecb"}"#))?;
refresher.refresh(None)?;
# Ok::<(), openrate::direct::Error>(())
```

**Handles close on `Drop`.** `Engine` and `Refresher` each own a `u64` and
release it when they go out of scope — on the happy path, on every `?`, and on a
panic unwind. A `Refresher` holds an `Arc` of its engine, so the engine cannot
be closed underneath it; and closing an engine also closes its refreshers, so
closing in the "wrong" order cannot leak a running loop either.

**Every returned string is freed.** Results and error messages alike go back
through `openrate_free` and nothing else. `Error::from_c` copies the message
into a `String` and frees the original *before* constructing the `Error` — the
step a hand-written binding usually misses, because it is easy to forget that
error strings are malloc'd exactly like results.

`open_handles()` exposes the library's own diagnostic counter, and
`tests/handles.rs` uses it to assert the SDK closed everything it opened.

### The library is loaded once and never unloaded

`libopenrate` is a Go `c-shared` object: loading it starts the Go runtime and
its threads, and Go has no way to shut that down, so `dlclose` would unmap code
that threads are still executing. `Api::shared` caches one `&'static Api` per
library path for the life of the process and leaks the mapping on purpose.

This is not caution for its own sake. llmux's equivalent binding was written the
other way first — `Library` owned per handle, dropped with it — and a 200-cycle
open/close loop **hung**, each iteration slower than the last, until the process
had to be killed. `tests/handles.rs::many_open_close_cycles_stay_fast` is the
guard here.

### Finding the library

1. `$OPENRATE_LIBRARY` — an explicit path always wins.
2. `dist/ffi/libopenrate-<goos>-<goarch>.<ext>`, walking up from the crate.
3. The bare file name, handed to the platform loader.

Note the file naming: openrate's build script emits
`libopenrate-darwin-arm64.dylib`, where llmux's emits
`darwin_arm64/libllmux.dylib`. Two products, two build scripts, two conventions.
Probe the version at startup with `Engine::open_checked("0.1.2", …)`.

## Sidecar

```rust
let sc = openrate::sidecar::Sidecar::start(Default::default())?;
sc.wait_ready(std::time::Duration::from_secs(45))?;
let json = sc.convert("EUR", "USD", 100.0)?;
// Drop stops and reaps the child.
# Ok::<(), Box<dyn std::error::Error>>(())
```

`Sidecar` owns the process and kills it in `Drop`, including on an early return
and on a panic. Rust has no `defer`, and an SDK that leaves a serving openrate
behind after a failed request is a bug that only surfaces as a port conflict
later.

## Four things that bit this SDK, so they will bite yours

Every one of these was found by running the examples, not by reading the code.
They are the reason each has a test with a **verbatim captured** fixture rather
than hand-written JSON.

**1. The HTTP API pretty-prints. The C ABI does not.**
`GET /api/v1/meta` answers with two-space indentation, so `"currencies": [`.
`openrate_call(h, "meta", …)` returns `"currencies":[`. The first `wait_ready`
checked for the compact form and timed out after 45 seconds against a server
that had been serving 30 currencies the whole time — **and its unit test passed**,
because the fixture was hand-written compact JSON. `sidecar::compact()` now
normalises whitespace outside string literals, and the tests use responses
captured from the running binary.

**2. `openrate serve` rate-limits its own readiness poll.**
The anti-scraping limiter is on by default at **120 requests per minute per IP**.
A readiness loop at 200 ms is 300 a minute, so it gets
`HTTP 429 {"error":"rate limited — slow down."}` a few seconds in. `wait_ready`
backs off 100 ms → 2 s, about 25 requests over a 45-second wait.

**3. Error strings embed quoted URLs.**
`last_error` values are Go `net/http` errors like
`Get \"https://…\": dial tcp: i/o timeout`. A scanner that reads to the first
`"` stops after `Get \` — the useless half. `read_json_string` honours
backslash escapes.

**4. `convert` nests its provenance, and an unknown base disagrees across
surfaces.** `result` is top level but `rate`, `hops`, `path` and `quality` are
inside a `"rate"` object, on both surfaces — the Go library's `fx.Conversion` is
the flat one. And `rates` with an unknown base is an **error** over the ABI and
a **200 with an empty book** over HTTP. A client checking only the status code
reads "no rates" as success.

## The costs of direct mode

1. **The Go runtime lives in your process** — GC, scheduler, and handlers for
   `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPROF`. A Rust program with its own crash
   reporter, sampling profiler or sanitizer build can conflict.
2. **Not fork-safe.** After `fork()` without `exec()` the Go runtime in the
   child is broken. In Rust the concrete victims are a direct `libc::fork()` and
   anything built on it; this is a much shorter list than Python's, because
   idiomatic Rust concurrency is threads and `std::process::Command` always
   `exec`s. If you do fork, load the library **after** the fork.
3. **The shared library is ~6.7 MB.**
4. **Platform coverage, and it is NARROWER than llmux's.** Do not read one
   matrix for both products:

   | target | openrate | (llmux, for contrast) |
   | --- | --- | --- |
   | darwin/arm64 | **built, smoke-tested, benchmarked** — 6,682,274 bytes | built and smoke-tested |
   | darwin/amd64 | **built but NEVER EXECUTED** — 7,120,680 bytes. Do not treat it as tested | not built |
   | linux/arm64 | **built nowhere** | built and smoke-tested |
   | linux/amd64 | not built locally; a CI job exists but **has never run** | tested in CI |
   | windows/amd64 | built nowhere | built nowhere |

   One target has actually been executed. If you are not on darwin/arm64, treat
   direct mode as unverified and use the sidecar — `openrate` is a plain Go
   binary and cross-compiles anywhere.
5. **Latency is not the reason.** See the top of this file.
6. **When the sidecar is genuinely better:** several processes sharing **one**
   refresher (four processes each refreshing is four times the load on ECB and
   SARB, from four IPs, on four unsynchronised cadences); wanting the HTTP
   shell's rate limiter, CORS policy and trusted-proxy handling; wanting
   openrate restartable independently; or being on any platform except
   darwin/arm64.

## Would slipscan be better off with direct mode?

`slipscan` is the real in-suite Rust consumer of openrate, and this SDK does not
touch it. The question was asked, so here is the answer with the evidence.

**Yes — this is the case where direct mode genuinely fits, and it fits unusually
well. But the platform matrix says not yet.**

Why it fits, which is worth stating because it is the strongest argument for
embedding anywhere in the suite:

1. **slipscan's core is already the thing an openrate engine is.**
   `slipscan-core`'s Cargo.toml has no reqwest, no hyper, no TLS — its dev-dep
   comment says outright "the crate itself performs no I/O". An `Engine` handle
   is exactly that shape: a value that computes and provably cannot reach the
   network. The two designs are the same design, arrived at independently.
2. **The seam is already at the right altitude.** `FxTransport::get(url) ->
   {status, bytes}` is injected **per call** into
   `Service::fx_fetch_rate(&self, transport: &dyn FxTransport, …)`, not stored.
   The direct equivalent is passing a `&Refresher` on the same call, with the
   same "core cannot fetch unless you hand it something that can" property, and
   the same 503 for a caller that has nothing to hand it.
3. **The cadence suits it.** slipscan documents "cache, never silently refresh"
   and "refreshing is always an explicit user action" — conversions read a local
   cache and only `fx_fetch_rate` touches the wire. An engine plus explicit
   `Refresher::refresh` is that policy expressed in types instead of in prose.
4. **It removes a class of bug slipscan documented.** slipscan's transport hands
   back `Vec<u8>` specifically because openrate's `writeJSON` sets a 200 header
   and *then* encodes, so a mid-body failure yields a successful status with a
   truncated body. Over the C ABI there is no status code and no partial write:
   a call either returns a complete document or returns NULL with an error.

Why not yet:

- **slipscan ships a Tauri desktop app** (`apps/desktop/src-tauri`). Direct mode
  has one executed platform. A desktop app cannot depend on that.
- **The FX endpoint is user-configurable** — slipscan points at *an* openrate
  deployment, possibly someone else's, and a shared library cannot be a URL.
- **`?Send` would have to be revisited.** `FxTransport` is `async_trait(?Send)`
  and the direct calls are blocking, so they belong in `spawn_blocking` — the
  same treatment slipscan already gives its core at every edge, but a change.

**Recommendation:** keep HTTP for the desktop app and for any user-pointed
endpoint. If `slipscan-server` ever runs on darwin/arm64 or a future-built
linux/arm64 and wants offline conversion with no FX endpoint configured at all,
add an `Engine`-backed second path behind the existing `FxTransportFactory`
seam — which is already an `Option`, and already returns 503 when absent. That
is a small change, and slipscan's architecture is closer to ready for it than
any other consumer in the suite.

## Layout

```
sdks/rust/
  src/lib.rs       module docs and the mode-choice guidance
  src/direct.rs    the C ABI binding — Engine, Refresher, Drop, Result
  src/sidecar.rs   spawn/supervise `openrate serve`, plus the HTTP calls
  src/http.rs      a std-only HTTP/1.1 client (shared verbatim with llmux's SDK)
  examples/direct.rs
  examples/sidecar.rs
  examples/run.sh
  tests/direct.rs  gated on a real libopenrate; skips loudly when there is none
  tests/handles.rs handle accounting — own binary AND a mutex, see its header
```

`cargo test` runs 28 tests. The gated ones **say which way they went** rather
than reporting a silent pass — run with `--nocapture` to see `direct tests RAN`
or `direct tests SKIPPED`.

## Dependencies

One: `libloading`, for the direct path. `dlopen`/`LoadLibrary` are platform
primitives std does not expose. No HTTP crate and no JSON crate — openrate's
contract is JSON documents the caller parses with whatever it already uses, so
this crate does not impose `serde_json` on a consumer that prefers `simd-json`,
or a TLS stack on one that only ever talks to `127.0.0.1`.
