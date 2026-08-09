# Use it from another language

openrate is a Go library. There are two ways to use it from a program that is
not Go, and this page is about choosing between them and getting started with
either.

**The full ABI reference lives in [`ffi/README.md`](../ffi/README.md)** — every
function, every method, the memory rules, the handle model, the measured
numbers and the honest caveats. This page is the map; that is the territory.

**If your language is one of the fifteen with a package already written, start
there instead.** [`sdks/README.md`](../sdks/README.md) is the index — it names
every language, which of the two modes that language should default to, and
why. This page is what those packages are built on, and what you would write
yourself for a language not on the list.

---

## Two ways, and the boring one is usually right

**Sidecar.** Run `openrate` as a process and talk to it over loopback HTTP. No
build toolchain, no linking, no shared-library loading rules, no interaction
with your runtime's signal handling. Every language already has an HTTP client.
In most of the fifteen packages you do not even run the process yourself:
something shaped like `Sidecar.start(…)` spawns it, waits until it is
**ready** rather than merely alive, and kills it when the object goes out of
scope.

**Shared library.** `libopenrate.dylib` / `libopenrate.so`, built with
`go build -buildmode=c-shared`, loaded with `dlopen`, called through six C
functions. (No `.dll` is built — see [what is built](#what-is-built-and-what-is-not)
below before you plan around Windows.)

The sidecar is the default recommendation. Take the shared library for either
of two reasons, and only when none of the costs below apply to you:

1. **The per-call cost is what you are optimizing.** Measured, in-process
   conversion is **3.7 µs mean** against **33.5 µs** over a warm loopback
   connection — about 9×, or 30 µs saved per call. Do the division before you
   act on it: behind a database query or a model call, 30 µs is not an
   argument for anything.
2. **You need "this process fetches nothing" to be structural.** An engine
   handle *cannot* be made to fetch — see
   [the split](#the-enginerefresher-split-is-enforced-at-the-abi) below. Over
   the sidecar that property is a configuration you maintain; in-process it is
   a property of the handle. This is the better reason, and it is the one that
   does not depend on your workload.

## The costs, in one screen

These are summarized here so that nobody chooses the shared library without
seeing them. Each is expanded in [`ffi/README.md`](../ffi/README.md).

| | What it means |
|---|---|
| **The Go runtime lives in your process** | its GC, its scheduler, and its signal handlers. It replaces `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPIPE` and `SIGURG` (chaining to whatever was there) and adds `SA_ONSTACK` to `SIGILL`, `SIGXFSZ` and `SIGUSR2`. A JVM host is the classic conflict. **`SIGPROF` is not touched, so profilers are unaffected** — measured, and the opposite of what most people expect. |
| **It is not fork-safe** | after `fork()` without `exec()` the Go runtime in the child is broken. Python `multiprocessing` on its default `fork` start method, uWSGI without `--lazy-apps`, preloading Gunicorn, clustered Unicorn/Puma — all hang. **Load the library after the fork, never before.** |
| **Building needs cgo and a C toolchain per target** | consumers need only the artifact, but somebody builds it, and there is no `GOOS=windows go build` that produces a `.dll` without mingw-w64. |
| **It is 6–8 MB** | ~6.7 MB on darwin/arm64; 7,120,680 bytes on darwin/amd64. Measured, not estimated — but the arm64 figure is not a constant: two builds of the same source here produced 6,682,274 and 6,700,448 bytes. |

If any of those are true of your host, the sidecar is not a fallback — it is
the better engineering answer.

## What is built, and what is not

This is the shortest section on the page and the one most worth reading before
you commit to the shared library. Only one target has ever been executed.

| Target | Status |
|---|---|
| `darwin/arm64` | **built, smoke-tested, benchmarked** — the only target any of these numbers came from |
| `darwin/amd64` | built (`clang -arch x86_64`), **never executed** — the dev machine cannot run it, so it is untested, not supported |
| `linux/amd64` | the `ffi` job exists in CI and **has never run**. Treat it as unbuilt until it has. |
| `linux/arm64` | **not built** |
| `windows/amd64` | **no DLL ships, anywhere.** Building one needs mingw-w64, which no runner here has. |

Read that table as it is written. "Built" is not "works": `darwin/amd64` has
been through a compiler and nothing else. A CI job that exists is not a CI job
that has run.

**[llmux](https://github.com/vul-os/llmux) publishes a different matrix** — the
two projects share an ABI shape, not a set of prebuilt artifacts, and neither
one's table covers the other. Do not read across.

None of this touches the **sidecar**, which needs only the `openrate` binary
for your platform and has no C toolchain, no linkage and no per-target matrix
at all. On Windows, and on linux/arm64 today, the sidecar is not the fallback —
it is the only mode.

The shared library is **not attached to GitHub releases**; the release workflow
ships the `openrate` binary and source archives only. Build it yourself:

```bash
scripts/build-ffi.sh [-o OUTDIR] [--host-only]   # default OUTDIR: dist/ffi/
```

It builds the host target and every cross target it finds a compiler for, then
prints what it built, what it attempted and failed, and what it skipped and
why. A skipped target is reported as skipped, not omitted.

## The six functions

```c
uint64_t     openrate_new(const char *config_json, char **err);
uint64_t     openrate_refresher_new(uint64_t engine, const char *config_json, char **err);
char        *openrate_call(uint64_t h, const char *method, const char *request_json, char **err);
void         openrate_close(uint64_t h);
void         openrate_free(char *p);
const char  *openrate_abi_version(void);

uint64_t     openrate_open_handles(void);   /* diagnostic — not part of the six */
```

That is the whole surface. `openrate_open_handles` is the seventh symbol and
is deliberately not counted among the six: it returns how many handles the
registry currently holds, which is a leak test and a debugging aid, not a way
to do anything. A binding that never exposes it is complete. A binding's test
suite that never calls it will not notice a handle it forgot to close — the
Rust package's [`tests/handles.rs`](../sdks/rust/tests/handles.rs) is what that
check looks like, including the part everyone gets wrong first: the counter is
**process-global**, so two tests reading it in parallel measure each other.

Bind against the hand-written header, **`ffi/include/openrate.h`** — not the
one cgo emits next to the library, which carries the Go type prologue and
changes with the toolchain.

Three rules that will save you a debugging session:

1. **Everything the library returns is freed with `openrate_free()`** —
   results and error strings alike. It came from Go's C allocator, not yours.
   `openrate_free(NULL)` is safe.
2. **Error strings are plain UTF-8 text, not JSON.** Do not parse them.
3. **Handles are retired, never recycled.** A stale handle can only ever
   produce `handle 7 is not open`, never silent access to whatever object was
   created next.

There is **no `openrate_stream`**. The shared ABI defines a streaming half and
openrate does not implement it — a conversion is a graph lookup and a
multiplication, complete the moment it is asked for, so there is no incremental
result to deliver. That is a design statement, not a gap to be filled later.

## The Engine/Refresher split is enforced at the ABI

This is the part worth knowing before you write any binding code, and it is
**the actual argument for direct mode**. Not the 30 microseconds — this.

`openrate_new()` builds an **engine**. It starts no thread, opens no socket,
reads no environment variable and sends no packet. It answers `convert`,
`rates`, `meta` and `load`, and that is the entire list.
`openrate_refresher_new()` builds the only object that can open a socket, over
an engine you already have, with its own handle and its own lifetime. Even that
does not fetch — `openrate_call(r, "refresh", …)` and
`openrate_call(r, "start", …)` do.

The split is not a convention the bindings agree to respect. **An engine handle
refuses `"refresh"`**, and it is checked twice, on both sides of the boundary:

| Where | What it asserts |
|---|---|
| Go — [`ffi/abi/abi_test.go`](../ffi/abi/abi_test.go) | `Call(engineHandle, "refresh", …)` must return an error, or "the split is not enforced at the ABI, so a host could make openrate fetch without ever constructing a refresher" |
| C — [`ffi/test/smoke.c`](../ffi/test/smoke.c) | the same call through a really-`dlopen`'d library: *an engine handle refuses `"refresh"` — fetching needs a refresher, always* |

The refusal is a plain error, and it names what the handle *can* do:

```
openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
```

The symmetric case holds too: a refresher handle refuses `"convert"`, and a
refresher cannot be built over another refresher.

**There is deliberately no entry point that starts everything.** So a host that
only ever calls `openrate_new` and `openrate_call(h, "load", …)` can prove its
process talks to nothing — the same guarantee the Go library gives, held by
`TestZeroPacketsThroughTheABI` (counting HTTP round trips, with a
deliberate-fetch control) and by the C smoke test (censusing socket file
descriptors in a really-`dlopen`'d library, with a real `socket()` call as its
control). See [Proving it sends nothing](zero-network.md).

This is what a structural guarantee buys that a documented one does not. "The
FX feature is switched off, therefore this process sends nothing" stops being a
promise in a comment and becomes a property of the handle you hold — in every
one of the thirteen non-Go [language packages](../sdks/README.md) with a direct
mode, because all thirteen call these same two constructors. It is why
`beepbite` could delete its HTTP client outright rather than leave it
configured-but-unused.

Closing an engine closes every refresher built over it, so closing in the
obvious order cannot leave a ticker running.

## The JSON is the JSON you already know

`openrate_call` takes a method name and a JSON request and returns a JSON
document — **the same document `/api/v1` publishes**. One wire format serves
both the shared library and the sidecar, and `TestWireParityWithTheHTTPAPI`
asks one engine the same questions over both and requires the answers to be
equal by value. So the [API reference](api.md) is also the ABI's response
reference.

Engine methods: `convert`, `rates`, `meta`, `load`.
Refresher methods: `status`, `refresh`, `start`, `stop`, `ready`.

`load` has no HTTP counterpart — the server is read-only — and it is the
zero-network path: install rates you obtained yourself. Full request and
response shapes are tabulated in [`ffi/README.md`](../ffi/README.md).

## A complete session

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

A fuller one, exercising every error path, is
[`ffi/test/smoke.c`](../ffi/test/smoke.c).

## If you take the sidecar instead: two things that are not obvious

Neither of these is about the ABI, and both cost a working afternoon if you
meet them for the first time in production.

**Wait for `/readyz`, never `/healthz`.** `/healthz` answers the instant the
listener binds, which is before any source has been fetched. A client that
starts the server, sees a `200`, and converts immediately gets
`{"error":"unknown or unreachable currency pair"}` for every pair — and exits
`0`. Every package in `sdks/` was written that way once and every one of them
was wrong. `GET /readyz` returns `200` only when the snapshot actually has
currencies in it, and its `503` carries each source's `last_error`, so a stuck
start prints `ecb: connection refused` instead of a bare timeout. It sits
outside `/api/`, so polling it can never spend the rate-limit budget you are
waiting to use. Full shape: [the API reference](api.md).

**Your HTTP client may send `127.0.0.1` to a proxy.** If `HTTP_PROXY` is set —
and on a corporate machine or in a CI image it usually is — Python's `urllib`
and .NET's `HttpClient` will both route a loopback request through it. Both
were doing exactly that, and both packages now bypass the proxy explicitly for
the sidecar's address; the Bun and Deno packages document `NO_PROXY=127.0.0.1`
instead, because their runtimes honour it. If you are writing your own client,
disable proxying for the loopback address before you debug anything else.

## The fifteen language packages

You almost certainly do not have to write any of this. Packages live in
[`sdks/`](../sdks/README.md), outside `ffi/`, and each one binds against
`ffi/include/openrate.h`. Fourteen of the fifteen offer **both** modes behind
one API — direct over the shared library, sidecar over loopback — and each
README argues which is the better default *for that language*, including when
the answer is "use the sidecar". Elixir offers only the sidecar, on purpose.

**[`sdks/README.md`](../sdks/README.md) is the index**: the fifteen languages,
the mechanism each uses to reach the ABI, and the per-language default. It is
the page to read before this one if you have a language in mind. What follows
is only the part of that list that bears on the ABI itself.

| How it reaches the ABI | Languages |
|---|---|
| Links or `dlopen`s the library directly | [C](../sdks/c/README.md) · [C++](../sdks/cpp/README.md) · [Rust](../sdks/rust/README.md) · [Swift](../sdks/swift/README.md) |
| Through the runtime's own FFI | [Python](../sdks/python/README.md) (`ctypes`) · [Ruby](../sdks/ruby/README.md) (`fiddle`) · [PHP](../sdks/php/README.md) (`FFI`) · [Java](../sdks/java/README.md) (FFM, JDK 22+) · [Kotlin](../sdks/kotlin/README.md) (over the Java binding) · [.NET](../sdks/dotnet/README.md) (`LibraryImport`) · [Node](../sdks/node/README.md) (koffi) · [Bun](../sdks/bun/README.md) (`bun:ffi`) · [Deno](../sdks/deno/README.md) (`Deno.dlopen`) |
| **Never touches the ABI** | [Go](../sdks/go/README.md) — imports the package · [Elixir](../sdks/elixir/README.md) — sidecar only, deliberately |

Three of those deserve a pointer of their own:

- **C is the ground truth.** Every other package is doing what
  [`sdks/c/direct_convert.c`](../sdks/c/direct_convert.c) does, in its own
  idiom. Read that file if a binding you are writing behaves oddly.
- **Go is not an FFI binding at all.** Go programs import the library, so no
  cost on this page applies to them — no shared library, no `dlopen`, no
  platform matrix. See [Embed as a Go library](library.md).
- **Elixir declines to build a NIF**, on the grounds that a crash inside a NIF
  takes the BEAM down with it and a NIF cannot be killed or timed out. Its
  README argues the case rather than assuming it. It is the one language here
  with no direct mode, and that is a conclusion, not a gap.

If your language is not among the fifteen, binding the six functions above is a
short afternoon anywhere with an FFI: `ctypes`/`cffi`, `libc`/`bindgen`, JNA,
`Fiddle`, `P/Invoke`. The ABI is six functions and a JSON document; there is
nothing clever to reproduce. Copy whichever of the fifteen is closest in shape.

openrate and [llmux](https://github.com/vul-os/llmux) deliberately expose the
**same ABI shape** — integer handles, JSON in and JSON out, one dispatch
function taking a method name, one free function for everything returned. Learn
one and you know the other.

## Verifying a build you did not make

```bash
scripts/check-ffi.sh --selftest   # build, dlopen, 40 checks, then break it four ways
```

The four deliberate defects it plants: every conversion returns twice the right
answer; the library reports a version the header does not; an engine answers
every method name including the refresher's; and close leaves the handle in the
registry. All four are caught. A passing smoke test only means something if it
is capable of failing.

## Related

- [`sdks/README.md`](../sdks/README.md) — the index for all fifteen languages
- [`ffi/README.md`](../ffi/README.md) — the reference this page points at
- [Which mode should I choose](deployment-modes.md)
- [API reference](api.md) — the same JSON, over HTTP
- [Proving it sends nothing](zero-network.md)
- [Troubleshooting](troubleshooting.md)
