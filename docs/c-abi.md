# Use it from another language

openrate is a Go library. There are two ways to use it from a program that is
not Go, and this page is about choosing between them and getting started with
either.

**The full ABI reference lives in [`ffi/README.md`](../ffi/README.md)** — every
function, every method, the memory rules, the handle model, the measured
numbers and the honest caveats. This page is the map; that is the territory.

---

## Two ways, and the boring one is usually right

**Sidecar.** Run `openrate` as a process and talk to it over loopback HTTP. No
build toolchain, no linking, no shared-library loading rules, no interaction
with your runtime's signal handling. Every language already has an HTTP client.

**Shared library.** `libopenrate.{dylib,so,dll}`, built with
`go build -buildmode=c-shared`, loaded with `dlopen`/`LoadLibrary`, called
through six C functions.

The sidecar is the default recommendation. Take the shared library when the
per-call cost is the thing you are optimizing — measured, in-process conversion
is **3.7 µs mean** against **33.5 µs** over a warm loopback connection, about
9× or 30 µs saved per call — and when none of the costs below apply to you.

## The costs, in one screen

These are summarized here so that nobody chooses the shared library without
seeing them. Each is expanded in [`ffi/README.md`](../ffi/README.md).

| | What it means |
|---|---|
| **The Go runtime lives in your process** | its GC, its scheduler, and its handlers for `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPROF`. A JVM host, or a Python profiler using `SIGPROF`, can conflict. |
| **It is not fork-safe** | after `fork()` without `exec()` the Go runtime in the child is broken. Python `multiprocessing` on its default `fork` start method, uWSGI without `--lazy-apps`, preloading Gunicorn, clustered Unicorn/Puma — all hang. **Load the library after the fork, never before.** |
| **Building needs cgo and a C toolchain per target** | consumers need only the artifact, but somebody builds it, and there is no `GOOS=windows go build` that produces a `.dll` without mingw-w64. |
| **It is 6–8 MB** | 6,682,274 bytes on darwin/arm64; 7,120,680 on darwin/amd64. Measured, not estimated. |

If any of those are true of your host, the sidecar is not a fallback — it is
the better engineering answer.

## What is built, and what is not

| Target | Status |
|---|---|
| `darwin/arm64` | built, smoke-tested, benchmarked |
| `darwin/amd64` | built (`clang -arch x86_64`), **not executed** — the dev machine cannot run it |
| `linux/amd64` | built and smoke-tested by the `ffi` job in CI |
| `linux/arm64` | **not built anywhere yet** |
| `windows/amd64` | **not built anywhere yet** — needs mingw-w64, which no runner here has. Treat Windows as untested. |

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
```

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

## The Engine/Refresher split crosses the ABI intact

This is the part worth knowing before you write any binding code.

`openrate_new()` builds an **engine**. It starts no thread, opens no socket,
reads no environment variable and sends no packet.
`openrate_refresher_new()` builds the only object that can open one, over an
engine you already have, with its own handle and its own lifetime. Even that
does not fetch — `openrate_call(r, "refresh", …)` and
`openrate_call(r, "start", …)` do.

**There is deliberately no entry point that starts everything.** So a host that
only ever calls `openrate_new` and `openrate_call(h, "load", …)` can prove its
process talks to nothing — the same guarantee the Go library gives, held by
`TestZeroPacketsThroughTheABI` (counting HTTP round trips, with a
deliberate-fetch control) and by the C smoke test (censusing socket file
descriptors in a really-`dlopen`'d library, with a real `socket()` call as its
control). See [Proving it sends nothing](zero-network.md).

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

## Per-language bindings

Bindings live in [`sdks/`](../sdks), outside `ffi/`, and each binds against
`ffi/include/openrate.h`. Every one offers **both** modes — direct, over the
shared library, and sidecar, over loopback — and each README says which is the
better default *for that language*, including when the answer is "use the
sidecar".

| Language | README |
|---|---|
| C | [`sdks/c`](../sdks/c/README.md) — the ground truth; every other binding is doing what `direct_convert.c` does |
| C++ | [`sdks/cpp`](../sdks/cpp/README.md) — header-only C++17 RAII wrapper |
| Go | [`sdks/go`](../sdks/go/README.md) — no FFI, no shared library, no platform matrix: you import a package |
| Python | [`sdks/python`](../sdks/python/README.md) |
| Ruby | [`sdks/ruby`](../sdks/ruby/README.md) |
| PHP | [`sdks/php`](../sdks/php/README.md) |
| Rust | [`sdks/rust`](../sdks/rust/README.md) |
| Java | [`sdks/java`](../sdks/java/README.md) |
| Elixir | [`sdks/elixir`](../sdks/elixir/README.md) |

Two of those deserve a pointer of their own. **Go is not an FFI binding at
all** — Go programs import the library, so none of the costs on this page apply
to them; see [Embed as a Go library](library.md). And **Elixir declines to
build a NIF**, on the grounds that a crash inside a NIF takes the BEAM down
with it; its README argues the case rather than assuming it.

If your language is not listed, binding the six functions above is a short
afternoon anywhere with an FFI: `ctypes`/`cffi`, `libc`/`bindgen`, `ffi-napi`,
JNA, `Fiddle`, `P/Invoke`. The ABI is six functions and a JSON document; there
is nothing clever to reproduce.

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

- [`ffi/README.md`](../ffi/README.md) — the reference this page points at
- [Which mode should I choose](deployment-modes.md)
- [API reference](api.md) — the same JSON, over HTTP
- [Proving it sends nothing](zero-network.md)
- [Troubleshooting](troubleshooting.md)
