# openrate for Swift

Two modes, both supported, both with a runnable example.

| | what it is | the type |
| --- | --- | --- |
| **Direct** | `dlopen`s `libopenrate` and runs the engine **inside your process** | `OpenRate.Engine` / `OpenRate.Refresher` |
| **Sidecar** | spawns and supervises `openrate serve`, talks HTTP | `OpenRate.Sidecar` |

## Tested on

Everything below was executed on this machine, not inferred:

| | |
| --- | --- |
| Swift | Apple Swift **6.1.2** (swiftlang-6.1.2.1.2, clang-1700.0.13.5) |
| swift-driver | 1.120.5 |
| Target | `arm64-apple-macosx15.0` |
| macOS | **15.7.3** (build 24G419), Apple silicon |
| Xcode | **not installed** — Command Line Tools only. See [Testing](#testing) |
| Package | SwiftPM, tools-version 5.9, platform floor macOS 13 |
| openrate | 0.1.2, `libopenrate-darwin-arm64.dylib` 6,682,274 bytes |

## The engine/refresher split is the reason to embed

Not latency — openrate's compute is a graph lookup either way. The interesting
property is what your process *cannot* do.

```
Engine     computes.  openrate_new() starts no thread, opens no socket, reads no
                      environment variable and sends no packet.
                      convert, rates, meta, load.

Refresher  fetches.   openrate_refresher_new() is a SEPARATE call with its own
                      handle and lifetime, and it is the only thing here that
                      can open a socket. Even constructing it sends nothing.
                      status, refresh, start, stop, ready.
```

Enforced **at the ABI**, not by convention — an engine handle refuses
`"refresh"`:

```
refuse:    openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
```

Swift puts a **second fence in front of the first**: `refresh()` exists only on
`Refresher`, so the mistake does not compile. Reaching the ABI's refusal at all
takes the escape hatch `engine.call("refresh", …)`, which is what the example
does to show it is really there.

## Run the examples

```
./sdks/swift/run.sh direct     # zero packets
./sdks/swift/run.sh refresh    # then a live ECB fetch
./sdks/swift/run.sh sidecar
./sdks/swift/run.sh test
```

Real output:

```
==> direct, engine only (no Refresher, so no socket opens)
library:   /Users/pc/code/vulos/openrate/dist/ffi/libopenrate-darwin-arm64.dylib
abi:       0.1.2
engine:    handle 1
handles:   1 open
empty:     openrate: convert USD->ZAR: unknown or unreachable currency pair
refuse:    openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
load:      {"built_at":"2026-08-08T16:00:00Z","currencies":["EUR","GBP","USD","ZAR"]}
USD->ZAR:  result=1842.0000000000002 rate=18.42 hops=1 via USD->ZAR grade=C
EUR->ZAR:  result=2001.3330000000003 rate=20.013330000000003 hops=2 via EUR->USD->ZAR grade=C
JPY->ZAR:  openrate: convert JPY->ZAR: unknown or unreachable currency pair
rates XXX: openrate: rates base XXX: unknown base currency   (HTTP would answer 200 with an empty book)
rates ZAR: 1588 bytes

no Refresher was constructed, so this process opened no socket.
handles:   1 open

==> direct, with a Refresher (THIS FETCHES FROM ECB)
refresher: handle 2, no packet sent yet
status:    {"sources":[{"name":"ecb","edges":0,"last_ok":"0001-01-01T00:00:00Z"}]}
handles:   2 open
refresh:   {"sources":[{"name":"ecb","edges":29,"last_ok":"2026-08-09T14:18:48.119642Z"}]}
EUR->USD:  result=115.35 rate=1.1535 hops=1 via EUR->USD grade=C

==> sidecar (child process over HTTP)
sidecar:   http://127.0.0.1:60837
healthz:   ok  (liveness only — no rates implied)
ready:     after 0.716s
EUR->USD:  result=115.35 rate=1.1535 hops=1 grade=C
rates EUR: 24279 bytes
rates XXX: HTTP 200, empty book = true   (the C ABI returns "unknown base currency")
bogus:     HTTP 404: {"error":"unknown or unreachable currency pair"}
stopped:   child reaped
```

`EUR->ZAR` has `hops=2` because that pair exists only as EUR→USD→ZAR. The hop
count, the path and the per-leg provenance are part of the answer, not something
you reconstruct.

## Direct

```swift
import OpenRate

let eng = try Engine(configJSON: #"{"base":"ZAR","quiet":true}"#)
_ = try eng.load(#"{"edges":[{"from":"USD","to":"ZAR","rate":18.42,"source":"mine"}]}"#)
let json = try eng.convert(#"{"from":"USD","to":"ZAR","amount":100}"#)

// Only now can this process reach the network:
let refresher = try eng.refresher(configJSON: #"{"sources":"ecb"}"#)
_ = try refresher.refresh()
```

**Closing is `deinit`.** ARC is the RAII: each handle is released when its last
reference goes away, on a `throw` and on an early `return` as much as on the
happy path. A `Refresher` keeps a strong reference to its `Engine`, so the
engine cannot be closed underneath it whatever order the caller drops things in
— and closing an engine also closes its refreshers, so the reverse order cannot
leak a running loop either.

**Every returned string is freed.** Results and error messages alike go back
through `openrate_free` and nothing else. `Library.takeError` copies the message
into a Swift `String` and frees the original *before* the `Error` is constructed
— the step a hand-written binding usually misses, because it is easy to forget
that error strings are malloc'd exactly like results. `openrate_abi_version` is
the one exception: a pointer owned by the library that must **not** be freed,
and is not.

`openHandles()` exposes the library's own diagnostic counter, so a host test
suite can assert it closed what it opened. The suite here does.

### No module map, no `unsafeFlags`

The C ABI is reached with `dlopen`/`dlsym` and `@convention(c)` function types.
Three consequences, all of which matter:

- `swift build` works with nothing but a Swift toolchain — no header, no `-L`.
- The library is located at **run** time, so one build works wherever
  `libopenrate` happens to be.
- **This package can be a dependency of another package.** A target carrying
  `unsafeFlags` cannot be — the usual reason a Swift C-interop package that
  "works locally" cannot be consumed.

Resolution: `$OPENRATE_LIBRARY`, then `dist/ffi/` walking up from the working
directory, then the bare name handed to the loader. Note the file naming:
openrate builds `libopenrate-darwin-arm64.dylib`, where llmux builds
`darwin_arm64/libllmux.dylib`. Two products, two build scripts, two conventions.

Probe the version at startup with `Engine(expectedVersion: "0.1.2")`.

### The library is loaded once and never unloaded

There is no `dlclose` in this SDK. `libopenrate` is a Go `c-shared` object:
loading it starts the Go runtime and its threads, and Go has no way to shut that
down, so unloading would unmap code those threads are still executing. The Rust
binding for llmux's equivalent library was written the other way first and a
200-cycle open/close loop **hung**. `manyOpenCloseCyclesStayFast` is the guard.

## No streaming

There is no `openrate_stream` and no `AsyncSequence` here. openrate answers from
a snapshot it already holds, so there is no incremental operation to stream.
llmux, which shares this ABI shape, does define `llmux_stream` because chat
streaming is its main event. Stated rather than left as a gap a reader has to
notice.

## Sidecar

```swift
let sc = try Sidecar(base: "EUR", sources: "ecb")
try sc.waitReady(timeout: 45)                       // NOT the same as healthz
let json = try sc.convert(from: "EUR", to: "USD", amount: 100)
```

`deinit` terminates and reaps the child, so an early `throw` cannot leave a
serving openrate holding a port and an hourly ECB fetch running forever.

Two implementation notes, both patterns worth *not* copying blindly:

- The calls bridge `URLSession`'s callback API to a synchronous API with a
  `DispatchSemaphore`. Safe **here**, because the completion runs on a
  `URLSession` delegate queue and never on the thread being blocked. Do not
  copy it into code on Swift's cooperative pool.
- `freePort()` binds port 0, reads the port, closes — inherently racy with
  anything that takes the port before the child binds. Every "find a free port"
  helper has this race; the alternative is passing the listening socket to the
  child, which openrate does not support.

## Five things that bit this SDK, so they will bite yours

Every one was found by running the code, not by reading it.

**1. `/healthz` is liveness, not readiness.** It answers `ok` the instant the
listener binds, *before* any source has been fetched. Treat it as readiness and
your program asks for a conversion and is told the pair is unknown — a false
green that also disguises itself as a bad currency code. `waitReady` polls
`/api/v1/meta` until the currency list is non-empty, and **throws** rather than
returning something empty. In-process the equivalent is `Refresher.ready`, which
blocks until the engine holds at least one currency and does not itself fetch.

**2. `openrate serve` rate-limits its own readiness poll.** The anti-scraping
limiter is on by default at **120 requests per minute per IP**. A poll every
200 ms is 300 a minute and gets `HTTP 429 {"error":"rate limited — slow down."}`
a few seconds in. `waitReady` backs off 100 ms → 2 s.

**3. The HTTP API pretty-prints. The C ABI does not.** `GET /api/v1/meta`
answers with indentation, so `"currencies": [`; `openrate_call(h, "meta", …)`
returns `"currencies":[`. A substring check written against one shape silently
never matches the other. `compact(_:)` normalises whitespace outside string
literals, `theCABIReturnsCompactJSON` pins the ABI side, and the sidecar tests
use **verbatim captured** responses rather than hand-written JSON — because the
hand-written fixture is exactly what let this ship green elsewhere.

**4. `_ = expr` releases immediately in Swift.** The handle-accounting test did
`_ = try eng.refresher(…)` and then asserted two open handles. It saw one: the
refresher was deallocated on the same line, `deinit` ran, the handle closed. Bind
it (`let refresher = …`) and use `withExtendedLifetime` around the assertion,
because ARC may also end a lifetime at last use rather than at scope exit.

**5. Serialising a swift-testing suite needs actual nesting.** See below.

## Testing

```
swift test
```

21 tests, all passing on the machine above, in ~0.02s. Gated tests **say which
way they went** rather than reporting a silent pass:

```
libopenrate found at …/dist/ffi/libopenrate-darwin-arm64.dylib — direct tests RAN
✔ Test run with 21 tests passed after 0.009 seconds.
```

**swift-testing, not XCTest — forced rather than chosen.** XCTest ships with
Xcode; it is **not** in the Command Line Tools. On a CLT-only machine
`import XCTest` fails with "no such module 'XCTest'" and `swift test` cannot
build at all. swift-testing is part of the Swift 6 toolchain. Worth knowing
before you write a suite that only runs on machines with a 10 GB IDE.

**The whole suite is `.serialized`, and getting there took three attempts.**
`openrate_open_handles()` is a process-global counter, and most tests here open
an engine, so any assertion on it needs exclusivity.

1. Putting only the handle tests in a nested `@Suite(.serialized)` serialises
   tests *within* that suite and does nothing about the free `@Test` functions
   in the same file. They kept racing.
2. Reaching for the Rust SDK's fix — a separate test binary — **does not
   transfer.** SwiftPM links every `testTarget` into one
   `<Package>PackageTests.xctest` and runs it in a single process, so a second
   target shares the counter just as much.
3. Declaring an empty `@Suite(.serialized) struct` beside the tests serialises
   **nothing**: swift-testing groups tests by lexical containment, not by file.
   It compiles, it is green, and it is a no-op — the exact shape of a guard that
   checks nothing.

The fix is nesting every `@Test` inside the suite. It costs nothing at this size.

## Platform coverage — narrower than llmux's

**Do not read one matrix for both products.**

| target | openrate | (llmux, for contrast) |
| --- | --- | --- |
| darwin/arm64 | **built, smoke-tested, benchmarked** — 6,682,274 bytes | built and smoke-tested |
| darwin/amd64 | **built but NEVER EXECUTED** — 7,120,680 bytes. Not tested | not built |
| linux/arm64 | **built nowhere** | built and smoke-tested |
| linux/amd64 | not built locally; a CI job exists but **has never run** | tested in CI |
| windows/amd64 | built nowhere | built nowhere |

**Exactly one target has ever been executed**, and it is the one this SDK was
written on. If you are not on darwin/arm64, treat direct mode as unverified and
use the sidecar — `openrate` is a plain Go binary and cross-compiles anywhere.

iOS is a special case with a firm answer: it permits neither `dlopen` of an
arbitrary dylib nor spawning child processes, so **neither mode works on
device**. Point an iOS app at a remote `openrate serve` over the network.

## The costs of direct mode

1. **The Go runtime lives in your process** — GC, scheduler, and handlers for
   `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPROF`. The Swift runtime does not install
   competing handlers, so this is quieter for Swift than for a JVM; a crash
   reporter or sanitizer build in the same process can still conflict.
2. **Not fork-safe.** After `fork()` without `exec()` the runtime in the child
   is broken. `Foundation.Process` always `exec`s and there is no idiomatic
   Swift pre-fork worker model, so the practical victim is a direct `fork(2)` in
   your own C interop. If you do fork, load the library **after** the fork.
3. **The shared library is ~6.7 MB.**
4. **One executed target.** See above.
5. **Latency is not the reason.** The reason is the zero-packet guarantee.
6. **When the sidecar is genuinely better:** several processes sharing **one**
   refresher (four processes each refreshing is four times the load on ECB and
   SARB, from four IPs, on four unsynchronised cadences); wanting the HTTP
   shell's rate limiter, CORS policy and trusted-proxy handling; wanting
   openrate restartable independently; or being on any platform except
   darwin/arm64.

## Layout

```
sdks/swift/
  Package.swift                          no dependencies, no unsafeFlags
  Sources/OpenRate/Direct.swift          the C ABI binding — Engine, Refresher, deinit
  Sources/OpenRate/Sidecar.swift         spawn/supervise `openrate serve`, plus compact/hasCurrencies
  Sources/openrate-direct-example/
  Sources/openrate-sidecar-example/
  Tests/OpenRateTests/DirectTests.swift  swift-testing, one serialized suite
  run.sh
```
