# openrate from C

C is the ground truth here. `openrate_new`, `openrate_call` and the rest are a
**C ABI**; every other binding in `sdks/` is doing what `direct_convert.c` does,
wrapped in that language's ceremony.

There is nothing to install for C. The header is
[`ffi/include/openrate.h`](../../ffi/include/openrate.h) and it is the whole
surface: six functions.

```c
uint64_t openrate_new(const char *config_json, char **err);
uint64_t openrate_refresher_new(uint64_t engine, const char *config_json, char **err);
char*    openrate_call(uint64_t h, const char *method, const char *request_json, char **err);
void     openrate_close(uint64_t h);
void     openrate_free(char *p);
const char* openrate_abi_version(void);
uint64_t openrate_open_handles(void);          /* diagnostic */
```

**There is no `openrate_stream`.** openrate answers from a snapshot it already
holds, so there is no incremental operation to stream. llmux, which shares this
ABI shape, does define one. The omission is deliberate, not a gap.

## Two kinds of handle, and the difference is the design

**An ENGINE computes.** `openrate_new` starts no thread, opens no socket, reads
no environment variable and sends no packet. It answers from the snapshot it
holds and says "unknown or unreachable currency pair" until something gives it
one. Feed it with the `load` method — rates you obtained yourself.

**A REFRESHER fetches.** It is a separate construction over an engine, with its
own handle and its own lifetime, and it is the only object in openrate that can
open a socket. A program that never calls `openrate_refresher_new` cannot make
this library touch the network. That is not a convention: an engine handle
**refuses** the `refresh` method, and there is no other code path.

`direct_convert.c` is shaped around that. Everything before `--fetch` is a
complete, useful conversion program that opens nothing.

| handle | methods |
|---|---|
| engine | `convert`, `rates`, `meta`, `load` |
| refresher | `status`, `refresh`, `start`, `stop`, `ready` |

Closing an engine also stops and releases every refresher built over it, so
closing in the "wrong" order cannot leak a running loop.

## The two examples

| file | mode | what it shows |
|---|---|---|
| `direct_convert.c` | direct | version probe, an empty engine refusing to guess, `load`, `convert`, a crossed pair reporting its path, an engine refusing `refresh`, the error path, one cleanup label, `openrate_open_handles()` back at zero |
| `sidecar_convert.c` | sidecar | spawn `openrate` on a free loopback port, wait for `/healthz` and then for `/readyz`, convert, read the book, HTTP errors, kill the child on every path |

**Two waits, because there are two questions.** `/healthz` is liveness: it
answers 200 the instant the listener binds, with an empty book behind it.
`/readyz` is readiness: 200 once the snapshot holds a currency, and 503 with a
JSON body — `reason`, plus `last_error` per source — until then. Converting on
the strength of `/healthz` is how this example used to get *unknown or
unreachable currency pair* for every pair and still exit 0, so
`sidecar_convert.c` waits for both and prints the 503's reasons if the second
wait runs out:

```
openrate has no rates after 30s: no rates yet: no source has returned a usable quote (ecb: Get "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml": proxyconnect tcp: dial tcp 127.0.0.1:1: connect: connection refused)
```

Neither endpoint is under `/api/`, and the per-IP limiter guards only `/api/`,
so polling readiness costs nothing from the budget the first real call wants —
which is why the poll is a flat 150 ms with no backoff. The child is spawned
with `OPENRATE_RATELIMIT=0` for the same family of reasons: it serves exactly
one client over loopback, so there is no stranger to throttle, and the 120/min
default is small enough that an honest batch of conversions would take a 429
from our own sidecar. Set `OPENRATE_RATELIMIT` in the environment to put it
back.

```bash
make                                 # build both
./direct_convert                     # no network at all
./direct_convert --fetch             # also builds a refresher (uses the network)
OPENRATE_BINARY=../../dist/openrate ./sidecar_convert
./sidecar_convert http://host:port   # a server you already run
make run                             # both
```

Build the library first: `scripts/build-ffi.sh --host-only` from the repo root
writes `dist/ffi/`. Point the Makefile elsewhere with
`make OPENRATE_LIB_DIR=/path/to/dir`.

**These are examples, not tests.** The test is
[`ffi/test/smoke.c`](../../ffi/test/smoke.c): it `dlopen()`s the library,
resolves every symbol **by name**, takes an OS-level socket census to check that
an engine opens nothing, plants deliberate controls so a measurement wired to
nothing fails loudly, and asserts 40 named checks and then asserts that 40 ran.
That is what catches a missing `//export` or a header that has drifted. If you
change the ABI, that file is the one that must fail. These examples link the
library instead of `dlopen`ing it, because that is how a program with an
installed library is actually written.

Real output, with no network:

```
abi version:  0.1.2 (this program was built against 0.1.2)
open handles: 0
engine:       handle 1

empty engine  openrate: convert USD->ZAR: unknown or unreachable currency pair
load          {"built_at":"2026-08-09T00:00:00Z","currencies":["EUR","GBP","USD","ZAR"]}
convert       100 USD = 1850.00 ZAR
              rate 18.5000, 1 hop(s), path USD -> ZAR
cross         1 GBP = 1.1651 EUR via GBP -> USD -> EUR
no fetching   openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
bad handle    openrate: handle 999999 is not open (never created, or already closed)

after close   open handles 0
```

## The rules, in C terms

**Ownership.** Everything the library hands back — results *and* error messages
— is released with `openrate_free` and nothing else. Not `free()`: it was not
allocated by your allocator. `openrate_free(NULL)` is safe, which is why the
cleanup block in `direct_convert.c` has no null checks. `openrate_abi_version`
is the exception; its string is static.

**Errors.** Fallible functions take a trailing `char** err`. The message is
plain UTF-8 **text, not JSON** — print it, do not parse it — and it is yours to
free. Passing `NULL` for `err` is allowed; the return value still reports the
failure.

**No RAII, so: one cleanup label.** `direct_convert.c` has exactly one
`goto done` target and no early `return` after the first handle exists.
`openrate_open_handles()` at the end turns "we cleaned up" from a claim into a
printed number — and it is the same function your own test suite should call.

**Handles are integers in a registry, never pointers**, and a closed handle's
number is retired rather than recycled. That is what makes use-after-close
readable: a stale handle can only produce "handle N is not open", never silent
access to whatever object was created next.

**Threading.** Every entry point is safe to call from multiple threads.

**Version probe.** The header defines `OPENRATE_ABI_VERSION`, compiled into your
program; `openrate_abi_version()` reports what the loaded library was built
from. `direct_convert.c` compares them at startup, which turns a stale
`libopenrate` earlier on the load path into a warning instead of behaviour that
looks like an openrate bug.

## Which mode to use from C

C has no FFI friction here, so prefer direct mode — especially since direct mode
is the only way to get **the engine that cannot fetch**. Prefer the sidecar
when:

- **Your process forks.** The Go runtime does not survive `fork()` without
  `exec()`. `sidecar_convert.c` forks, and is safe doing so *only because it
  never loads libopenrate*. Do not merge the two examples into one binary.
- **Your process handles its own signals** — a crash reporter, a sampling
  profiler, a sanitizer build. Go installs handlers for `SIGSEGV`, `SIGBUS`,
  `SIGFPE` and `SIGPROF`, and chains to a pre-existing handler in most cases.
  "Most" is the honest word.
- **Several processes should share one refreshing book.** Four workers each
  fetching their own copy is worse in every dimension.
- **You are not on darwin/arm64.** Read the next section before assuming.

## Platform reality for openrate

| target | status |
|---|---|
| darwin/arm64 | built, smoke-tested and benchmarked. 6,682,274 bytes |
| darwin/amd64 | **built (7,120,680 bytes) but NEVER EXECUTED** — the build machine cannot run it. "Compiled", not "supported" |
| linux/amd64 | **not built locally.** A CI job exists and has never run |
| linux/arm64 | **built nowhere.** (llmux has this target; openrate does not) |
| windows/amd64 | **built nowhere.** No mingw-w64 available. No DLL exists |

One row has been executed. That is not a support matrix, and this page will not
present it as one. On anything else, use the sidecar — a supported answer, not a
fallback.

**macOS wart.** `go build -buildmode=c-shared` gives the dylib a bare install
name with no `@rpath/` prefix, so `-rpath` is never consulted and the program
dies at startup with `Library not loaded: libopenrate-darwin-arm64.dylib`. The
Makefile fixes it in the executable:

```
install_name_tool -change libopenrate-darwin-arm64.dylib \
                  @rpath/libopenrate-darwin-arm64.dylib direct_convert
```

A packager should instead fix the library:
`install_name_tool -id @rpath/libopenrate-darwin-arm64.dylib <lib>`.

## Latency

Measured on darwin/arm64 over 30,000 iterations: **3.7 µs in-process against
33.5 µs over loopback HTTP** — about 9×, or ~30 µs saved per call. The HTTP side
was deliberately flattered (keep-alive on, no TLS), so that is a floor. If you
are doing a million conversions in a tight loop it adds up; if you are answering
a web request it is invisible. The reason to embed is the engine that cannot
fetch, plus no second process, no port and no loopback surface.

## The two helper files

`jsonpeek.c` and `mini_http.c` are here so the examples have no dependencies.
Neither is a component to reuse:

- **`jsonpeek`** is not a JSON parser. It scans for `"key":` and reads what
  follows. openrate's convert response nests — `"from"`, `"to"` and `"rate"`
  each occur again inside the rate path's legs — and the comments in
  `sidecar_convert.c` show the contortions that costs. Those comments are an
  argument for linking cJSON, jansson or yyjson in a real program, which is what
  you should do.
- **`mini_http`** is not an HTTP client. One request to `127.0.0.1` with
  `Connection: close`. No TLS, no chunked encoding, no keep-alive, no redirects,
  no retries. Real programs link libcurl. It does hand back the body whatever
  the status, which is not incidental: `/readyz` says why it is not ready in the
  body of a **503**, so a helper that dropped bodies on a bad status could only
  ever report a bare timeout.
