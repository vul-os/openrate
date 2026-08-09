# libopenrate — the C ABI

openrate is a Go library. This directory is how everything else uses it
**in-process**: a shared library built with `go build -buildmode=c-shared`,
loaded with `dlopen`/`LoadLibrary`, called through six C functions.

There is a second way to use openrate from another language — run the HTTP
server as a **sidecar** and talk to it over loopback. That way is simpler, has
none of the costs listed below, and for a lot of programs it is the right
answer. [Read the costs first](#the-costs-read-these-before-you-choose), then
[the numbers](#in-process-versus-loopback-measured), then decide. This document
is not trying to sell you the shared library.

openrate and [llmux](https://github.com/vul-os/llmux) expose the **same ABI
shape** — integer handles, JSON in and JSON out, one dispatch function taking a
method name, one free function for everything the library returns. Learn one and
you know the other.

---

## The costs. Read these before you choose.

`-buildmode=c-shared` is not free, and none of this belongs in a footnote.

**1. The Go runtime lives in your process.** Loading this library starts Go's
garbage collector, its scheduler and its signal handlers inside your address
space. Measured against HotSpot (OpenJDK 26.0.2, darwin/arm64, this build mode),
Go **replaces exactly five** handlers — `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPIPE`
and `SIGURG` — chaining to whatever was installed, and leaves three more in
place with `SA_ONSTACK` added: `SIGILL`, `SIGXFSZ` and `SIGUSR2`. A host with
its own opinions about those signals can conflict, and the JVM is the classic
case: `SIGSEGV` is how HotSpot recovers elided null checks and grows stacks, and
`SIGUSR2` is its thread suspend/resume handler.

**`SIGPROF` is not touched, so sampling profilers are not affected.** This is
worth stating plainly because the opposite is widely assumed — and because this
document previously asserted it. Under `-buildmode=c-shared` Go's
`sigInstallGoHandler` installs only *synchronous* signals plus `SIGPIPE` and
`SIGURG`, and `SIGPROF` is neither. JFR, `py-spy`, `yappi` in wall-clock mode
and `stackprof` keep working. Do not write defensive code for a hazard that is
not there. The full per-signal measurement is in
[`sdks/java/README.md`](../sdks/java/README.md#the-jvm-and-gos-signal-handlers).

**2. It is not fork-safe.** After `fork()` without `exec()`, the Go runtime in
the child is broken — its threads did not come across, so anything that needs
the scheduler can hang or crash. In practice this bites:

| Host | Symptom | Fix |
|---|---|---|
| Python `multiprocessing` (default `fork` on Linux) | child hangs on first call | `multiprocessing.set_start_method("spawn")` |
| uWSGI with `--processes` and no `--lazy-apps` | workers hang after the master loads the library | `--lazy-apps`, so each worker loads it itself |
| Unicorn / Puma in clustered mode | same shape | load the library after forking, in the worker |
| Gunicorn `sync`/`gthread` with preload | same shape | `--preload` off, or load lazily per worker |

The general rule: **load the library after the fork, never before.** If you
cannot control that, use the sidecar.

**3. Building it needs cgo and a C toolchain, per target platform.** Consumers
only need the prebuilt artifact, but somebody builds it, and there is no
`GOOS=windows go build` that produces a `.dll` without mingw-w64 installed. See
[Building](#building) for exactly which targets have been built and which have
not.

**4. It is 6–8 MB.** A shared library is not free:

| Target | Size |
|---|---|
| darwin/arm64 | ~6.7 MB (~6.4 MiB) |
| darwin/amd64 | 7,120,680 bytes (6.8 MiB) |

Measured, not estimated — but the arm64 figure is **not a constant**. Two builds
of the same source here produced 6,682,274 and then 6,700,448 bytes, so it moves
a little with build paths and toolchain. Treat it as "about 6.7 MB" rather than
a number to assert in a test. Linux and Windows figures are not listed because
they have not been built on this machine — see [Building](#building).

**5. If any of that makes the sidecar the better choice for your language, take
it.** That is a real option, not a consolation prize. It is the right call when
your host forks and you cannot control when the library loads; when your runtime
has its own signal handling you do not want to disturb; when you want the
openrate process to be restartable, upgradable or crash-isolated independently
of yours; or when 30 microseconds per conversion is not a number you care about.

---

## No streaming

**There is no `openrate_stream`.** The shared ABI defines a streaming half, and
openrate does not implement it.

That is not an omission to be filled in later. openrate answers from a snapshot
it already holds — a conversion is a graph lookup and a multiplication, complete
the moment it is asked for. There is no incremental result to deliver, so a
callback entry point here would be a promise with nothing behind it. llmux
*does* define `llmux_stream`, because token-by-token chat streaming is its main
event.

If a future openrate operation genuinely produces results over time, it will get
`openrate_stream` with the shared signature rather than something invented for
the occasion.

---

## The ABI

Bind against **`ffi/include/openrate.h`**, the hand-written header. cgo also
emits one next to the library it builds (`libopenrate-<goos>-<goarch>.h`); that
one carries the Go type prologue and changes with the toolchain.

```c
uint64_t     openrate_new(const char *config_json, char **err);
uint64_t     openrate_refresher_new(uint64_t engine, const char *config_json, char **err);
char        *openrate_call(uint64_t h, const char *method, const char *request_json, char **err);
void         openrate_close(uint64_t h);
void         openrate_free(char *p);
const char  *openrate_abi_version(void);
uint64_t     openrate_open_handles(void);   /* diagnostic */
```

### Memory

Everything this library returns — **results and error strings alike** — is freed
with `openrate_free()` and nothing else. It came from Go's C allocator, not
yours. `openrate_free(NULL)` is safe.

Every fallible entry point takes a `char** err`. It is set to `NULL` before the
work starts and, on failure only, to a message. **The message is plain UTF-8
text, not JSON.** Do not parse it. Passing `NULL` for `err` is allowed; the
return value still reports the failure.

### Handles

A handle is a `uint64` key into a registry inside the library, never a pointer
cast to an integer. `0` is never valid.

**Closed handles are retired, never recycled.** That is what makes
use-after-close readable: a stale handle can only ever produce `handle 7 is not
open`, never silent access to whatever object was created next. Double closes,
closes of handles that never existed, and calls on closed handles are all clean
errors, because a host language's garbage collector will eventually free things
in an order nobody planned.

All entry points are safe to call from multiple threads, including
`openrate_close()` concurrently with a call on the handle it is closing. That
race resolves one of exactly two ways and never a third: the call completes and
the close then tears the object down, or the close wins and the call returns the
ordinary `handle 7 is not open`. Nothing this library started can outlive the
handle that authorized it — in particular `openrate_call(h, "start")` racing
`openrate_close(h)` cannot leave a refresh loop running, whichever order they
land in, and `openrate_refresher_new()` racing the close of its engine either
returns a fully-owned refresher or fails, never a handle nobody can close.

Those are not free properties and they were not free here: both were real
defects, both were invisible to `-race` because neither was a data race, and
they are held down by `abi/lifecycle_test.go`, which runs each race twelve
thousand times and asserts what a host can actually observe —
`openrate_open_handles()` back where it started, and the library's request count
frozen once the last close returned.

`openrate_close()` is terminal, and it waits: when it returns, any background
loop on that handle has been cancelled *and* has exited. `"stop"` is the
reversible one — a stopped refresher can be started again; a closed one cannot.

### Two kinds of handle, and why

This is the part of the ABI that is a design decision rather than a convention.

**An engine computes. A refresher fetches. They are separate constructions.**

`openrate_new()` builds an engine. It starts no thread, opens no socket, reads
no environment variable and sends no packet — not "unless configured", but
structurally: an engine holds no sources and has no code path to the network. It
answers from the snapshot it holds and says `unknown or unreachable currency
pair` until something gives it one.

`openrate_refresher_new()` builds the only object in openrate that can open a
socket, over an engine you already have, with its own handle and its own
lifetime. Even that does not fetch; `openrate_call(r, "refresh", ...)` and
`openrate_call(r, "start", ...)` do.

**There is deliberately no entry point that starts everything.** openrate used
to have one, in the shape of a `Start()` that bound a listener and began
fetching before it returned, and removing it is what the library's current shape
is *for*. Reintroducing it at the ABI would hand the flaw to every language that
binds this.

So: a host that only ever calls `openrate_new` and `openrate_call(h, "load")`
can prove its process talks to nothing. Two tests hold that claim from different
directions — `TestZeroPacketsThroughTheABI` counts HTTP round trips across the
whole engine sequence (with a deliberate-fetch control, so a counter wired to
nothing cannot read as a pass), and the C smoke test censuses socket file
descriptors around the same sequence in a really-loaded library (with a real
`socket()` call as its control).

Closing an engine also closes every refresher built over it, so closing in the
obvious order cannot leave a ticker running.

### Methods

`openrate_call(h, method, request_json, &err)` returns a `malloc`'d JSON
document. `request_json` may be `NULL`, meaning `{}`.

The JSON **is the JSON `/api/v1` already publishes**. That is deliberate: the
wire contract is reused rather than reinvented, one document format serves both
the shared library and the sidecar, and `TestWireParityWithTheHTTPAPI` asks one
engine the same questions over both and requires the answers to be equal.

#### Engine handles

| Method | Request | Response |
|---|---|---|
| `convert` | `{"from":"USD","to":"ZAR","amount":100}` | `{"from","to","amount","result","rate":{…}}` |
| `rates` | `{"base":"ZAR"}` | `{"base","built_at","rates":{"USD":{…},…}}` |
| `meta` | `{}` | `{"default_base","built_at","currencies","sources"}` |
| `load` | `{"edges":[…],"built_at":"…"}` | `{"built_at","currencies"}` |

`convert` and `meta` carry exactly the fields and values the HTTP responses do
— same keys, same numbers, to the bit. They are not byte-identical: the HTTP API
indents for a human reading `curl` output and this does not, and JSON object key
order is not part of either contract. The parity test compares by value for that
reason, and five mutations prove it can still tell two documents apart. Omitted
currencies mean the engine's default base; an omitted amount means 1.

`rates` has **one deliberate difference**: an unknown base is an error here,
where `GET /api/v1/rates` answers 200 with an empty book. The ABI follows
`Engine.Rates`, on the grounds that a caller who asked for rates against `ZZZ`
and got `{}` has been told nothing. An engine holding *no* rates at all still
returns an empty book and no error, because "nothing yet" is a readiness
question rather than a bad request.

`load` has no HTTP counterpart — the server is read-only. It is the zero-network
path: install rates you obtained yourself, from a file, a cache, your own feed.
An edge's `time` defaults to `built_at`, and `built_at` to now.

```jsonc
// load
{
  "built_at": "2026-08-09T12:00:00Z",
  "edges": [
    {"from":"USD","to":"ZAR","rate":18.5,"source":"mine","time":"2026-08-09T11:00:00Z"},
    {"from":"EUR","to":"USD","rate":1.09,"source":"mine","time":"2026-08-09T10:00:00Z"}
  ]
}
```

#### Refresher handles

| Method | Request | Response | Opens sockets |
|---|---|---|---|
| `status` | `{}` | `{"sources":[{"name","edges","last_ok","last_error"}]}` | no |
| `refresh` | `{"timeout_ms":30000}` | `{"sources":[…]}` | **yes** |
| `start` | `{}` | `{"running":true}` | **yes**, on a goroutine |
| `stop` | `{}` | `{"running":false}` | no — and it waits for the loop to exit |
| `ready` | `{"timeout_ms":5000}` | `{"ready":true}` | no |

`refresh` is synchronous: when it returns, the engine holds everything that
refresh could get. `start` runs the loop on the configured interval and is the
only thread this library ever creates on its own; starting twice is an error
rather than two tickers feeding one engine. `ready` blocks until the engine
holds at least one currency — it does **not** fetch, so something must be
refreshing or it simply waits until the timeout. `timeout_ms` of 0 or absent
means no deadline of the caller's own.

`meta`'s `sources` covers every **open** refresher over that engine. Closing a
refresher drops it out: its last status can never change again, and it would be
reported under a handle you can no longer address.

Every millisecond field — `interval_ms`, `fetch_timeout_ms`, `timeout_ms` — is
bounded by 9223372036854, which is everything a duration can hold (about 292
years). The two config fields **refuse** anything larger, with `*err` set;
`timeout_ms` on a request is clamped, since a 292-year deadline is
indistinguishable from the one you asked for. The bound is not pedantry: the
multiply into nanoseconds wraps, and the interesting values do not wrap into
something you would notice. `interval_ms` of 288230376151711745 comes out as
exactly 1ms, so the most conservative cadence a host can ask for became a loop
hammering every configured source a thousand times a second.

### Version

```c
if (strcmp(openrate_abi_version(), OPENRATE_ABI_VERSION) != 0) {
    /* the library on the load path is not the one you compiled against */
}
```

`openrate_abi_version()` returns openrate's package version, compiled into the
library. `OPENRATE_ABI_VERSION` is the same string, compiled into *you*. The
version lives in three places — `/VERSION`, `ffi/abi/version.go` and the header
— and tests tie all three together, so a release that bumps one and forgets the
others fails the build rather than teaching hosts to distrust a correct answer.

### A complete session, in C

```c
char *err = NULL;
uint64_t eng = openrate_new("{\"base\":\"ZAR\"}", &err);
if (eng == 0) { fprintf(stderr, "%s\n", err); openrate_free(err); return 1; }

/* Option A: bring your own rates. Nothing is sent anywhere. */
char *loaded = openrate_call(eng, "load", edges_json, &err);
openrate_free(loaded);

/* Option B: let openrate fetch. A second, explicit construction. */
uint64_t ref = openrate_refresher_new(eng, "{\"sources\":\"ecb,coinbase\"}", &err);
char *st = openrate_call(ref, "refresh", "{\"timeout_ms\":30000}", &err);
openrate_free(st);

char *out = openrate_call(eng, "convert",
                          "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}", &err);
if (out == NULL) { fprintf(stderr, "%s\n", err); openrate_free(err); }
else             { puts(out); openrate_free(out); }

openrate_close(eng);   /* closes ref with it */
```

A fuller one, with every error path exercised, is
[`ffi/test/smoke.c`](test/smoke.c).

---

## In-process versus loopback, measured

Both sides driven by [one C program](bench/bench.c), against the same snapshot,
asking for the same conversion, checking both answers before believing either
timing. The HTTP side is openrate's real `serve.Server` over a real TCP
connection to 127.0.0.1.

**Machine:** Apple M-series, darwin/arm64 (Darwin 24.6), Go 1.25.12, otherwise
idle. 30,000 iterations each after 1,000 warm-up calls. Timer granularity
measured at 41 ns and printed by the benchmark itself.

| | mean | p50 | p99 | min |
|---|---|---|---|---|
| in-process (C ABI) | **3.7 µs** | 3.5 µs | 8.5 µs | 3.2 µs |
| loopback HTTP | 33.5 µs | 31.5 µs | 66.8 µs | 24.3 µs |

**About 9×, or 30 µs saved per conversion.**

Read that as a floor on the gap, not a headline. The HTTP side is deliberately
flattered: one keep-alive connection held open for the whole run, no TLS, no
proxy, same machine, and a hand-rolled client that allocates nothing per
request. A real sidecar — a fresh connection, a language's HTTP library, another
container, a service mesh — is slower than this, sometimes by a lot.

And 30 µs is 30 µs. If you convert a handful of numbers per web request, this
difference is invisible next to everything else you do. If you are pricing a
book of orders in a loop, it is the whole cost.

Run it yourself:

```
scripts/bench-ffi.sh [iterations]
```

---

## Building

```
scripts/build-ffi.sh [-o OUTDIR] [--host-only]     # default OUTDIR: dist/ffi/
```

It builds the host target and every cross target it can find a compiler for,
then prints what it built, what it attempted and failed, and what it skipped and
why. A skipped target is reported as skipped.

**Built on the machine this was developed on** (darwin/arm64, Xcode clang):

- `darwin/arm64` — built, smoke-tested, benchmarked.
- `darwin/amd64` — built via `clang -arch x86_64`. **Not executed**; this
  machine cannot run it.

**Not built here, CI-only:**

- `linux/amd64` — an `ffi` job on `ubuntu-latest` is configured to build and
  smoke-test it. **That job has never run**, so treat this target as unbuilt
  until it does. Cross-building from macOS needs `x86_64-linux-gnu-gcc` or
  `zig cc`, neither of which was installed here.
- `linux/arm64`, `windows/amd64` — **not built anywhere yet.** Windows needs
  mingw-w64 and no runner in this repository's CI has it. The build script knows
  how to target it and will say so when the toolchain appears. Until then,
  treat Windows as untested.

The shared library is **not attached to GitHub releases** today; the release
workflow ships the `openrate` binary and source archives only. Build it
yourself with the script above.

---

## What is tested, and how it can fail

`ffi/` is a separate Go module — `github.com/vul-os/openrate-ffi`, a hyphen and
not a slash, so that Go's `internal/` rule applies to it exactly as it applies
to any third-party embedder. The shared library is therefore provably built from
openrate's public API. `TestTheInternalWallHoldsForTheABI` compiles
[`internalprobe`](internalprobe/probe.go) and fails if the compiler *stops*
refusing it.

Being a separate module also keeps the C ABI out of openrate's pure-Go build:
the root module's `go build ./...`, `go vet ./...` and `go test ./...` do not
descend into it, so nothing here can add a dependency or a cgo requirement to
the library everyone else consumes. (A build tag would not have achieved that —
a package whose every file is tag-excluded is a hard error, not a skip.) The
cost is that the root's `go test ./...` does not run these tests either, so CI
runs them through `scripts/go-test-gate.sh` with a floor and nineteen required
test names.

| What | Where |
|---|---|
| Wire parity with the HTTP API, seven questions | `abi/wire_parity_test.go` |
| …and five mutations proving that comparison can fail | same file |
| Zero packets through the ABI, with a fetch control | `abi/zero_packets_test.go` |
| Refresher fetches only when asked, over a stubbed network | `abi/refresher_test.go` |
| Version agrees across `/VERSION`, Go and the header | `abi/version_test.go` |
| Every `//export` is declared in the hand-written header | same file |
| Handles are never recycled, 50 cycles | `abi/module_test.go` |
| Close racing start, and close racing construction, 12,000 times | `abi/lifecycle_test.go` |
| 40 checks through a really-dlopen'd library | `test/smoke.c` |
| …and four mutations proving the smoke test can fail | `scripts/check-ffi.sh --selftest` |

```
scripts/check-ffi.sh --selftest    # build, dlopen, 40 checks, then break it four ways
```

The four deliberate defects are: every conversion returns twice the right
answer; the library reports a version the header does not; an engine answers
every method name including the refresher's; and close leaves the handle in the
registry. All four are caught. A passing smoke test only means something if it
is capable of failing, and that is a claim worth running rather than asserting.

---

## Bindings

Per-language SDKs live outside this directory and bind against
`ffi/include/openrate.h`. Each one's README should say, for that language,
whether direct or sidecar is the better default — and should say it honestly.
The precedent is patala-go's README, which names cackle as the repository that
correctly chose the sidecar.
