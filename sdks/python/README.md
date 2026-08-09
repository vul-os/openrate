# openrate for Python

Currency conversion with an auditable rate path — every answer carries the hops
it took, the sources behind each leg, how old they are, and a quality grade.

Two modes. The JSON is identical in both; only the transport differs.

| | **Sidecar** | **Direct** |
|---|---|---|
| what runs | the `openrate` binary as a child process on `127.0.0.1` | `libopenrate` loaded into your interpreter with `ctypes` |
| module | `openrate._sidecar` | `openrate._direct` |
| can it send a packet? | yes — a server refreshes on startup and on its interval | **only if you build a `Refresher`.** An `Engine` cannot |
| survives `fork()` | **yes** | **no** |
| extra bytes on disk | the binary you already have | a 6.7 MB shared library |
| platforms | wherever the binary is built | **darwin/arm64 only, in practice** (see below) |

## Which mode

**Start with the sidecar** unless you specifically want what direct mode
uniquely offers.

Direct mode's real argument is not speed — it is **the engine that provably
fetches nothing**:

```python
from openrate import Engine

with Engine({"base": "ZAR"}) as engine:
    engine.load(edges=[{"from": "USD", "to": "ZAR", "rate": 18.5, "source": "my-desk"}])
    engine.convert("USD", "ZAR", 100)["result"]     # 1850.0
```

That program starts no thread, opens no socket, reads no environment variable
and sends no packet. Not "does not by default" — an engine handle **refuses**
the `refresh` method, and fetching requires a second, explicit construction with
its own handle:

```python
with Engine() as engine, engine.refresher({"sources": "ecb"}) as refresher:
    refresher.refresh()                              # THIS OPENS SOCKETS
    engine.convert("EUR", "ZAR", 100)
```

That split is enforced at the ABI, not by convention, and it is the reason to
embed. If you are shipping into an audited environment, an airgapped one, or a
process that must not surprise anyone with outbound traffic, direct mode is the
mode that can be proved rather than promised.

**Prefer the sidecar when:**

- **Your process forks.** `multiprocessing` and `ProcessPoolExecutor` default to
  the `fork` start method on Linux for anything older than 3.14; uWSGI,
  Gunicorn's sync and gthread workers, and Celery's prefork pool all import your
  app in a master and then fork. Direct mode is **not fork-safe**.
- **You want a shared, refreshing rate book** for several processes or services.
  That is exactly what a server is, and running four fetchers in four workers to
  hold four copies of the same book is worse in every dimension.
- **You are not on darwin/arm64.** See the platform table.

## Install

```bash
pip install openrate
```

Sidecar mode also needs the binary: platform wheels bundle it at
`openrate/bin/openrate`; otherwise put `openrate` on `PATH` or set
`OPENRATE_BINARY`. Direct mode needs the shared library: build it from a
checkout with `scripts/build-ffi.sh` and point `OPENRATE_LIBRARY` at the result,
or drop it at `openrate/lib/`.

Nothing else. No dependencies: direct mode is `ctypes` and the sidecar client is
`urllib`, both standard library.

## Sidecar

```python
import openrate

client = openrate.Client()                            # spawns and manages one
print(client.convert("USD", "ZAR", 100)["result"])
print(client.rates("ZAR")["rates"]["USD"])
print(client.meta()["sources"])

remote = openrate.Client("https://openrate.example")  # spawns nothing
```

`openrate.start()` resolves the binary (`$OPENRATE_BINARY` → bundled
`bin/openrate` → `openrate` on `PATH`), picks a free loopback port, launches it
with `OPENRATE_ADDR=127.0.0.1:<port>` and the environment inherited, polls
`/healthz`, and terminates the child at exit. Start is lazy, singleton and
thread-safe. `Client.close()` stops a sidecar this process started and leaves a
server it was merely pointed at alone.

`start(base_currency=..., sources=..., refresh=...)` maps to `OPENRATE_BASE`,
`OPENRATE_SOURCES` and `OPENRATE_REFRESH`, exactly as for the standalone binary.

**"Healthy" and "useful" are different questions.** The server answers 200 from
the moment it is listening, with an empty book until its first fetch lands.
`examples/sidecar_convert.py` polls `/api/v1/meta` for a currency list rather
than assuming.

## Direct

```python
from openrate import Engine

with Engine({"base": "ZAR", "quiet": True}) as engine:
    engine.load(edges=[...], built_at="2026-08-09T00:00:00Z")
    engine.convert("USD", "ZAR", 100)     # == GET /api/v1/convert
    engine.rates("ZAR")                   # == GET /api/v1/rates
    engine.meta()                         # == GET /api/v1/meta
```

**Always use the context manager.** `__exit__` closes the handle on every path
including an exception. Closing an engine also stops and releases every
refresher built over it, so closing in the "wrong" order cannot leak a running
loop. Error strings are freed before the exception is raised — `OpenRateError`
carries a Python copy.

`openrate.open_handles()` reports how many handles the library currently holds.
It is a diagnostic, and it is what makes leak-freedom checkable: the test suite
asserts the count returns to its starting value after every test.

**One deliberate difference between the modes.** `Engine.rates("XXX")` on an
unknown base raises; `Client.rates("XXX")` answers 200 with an empty book. Direct
mode follows the Go library, the HTTP endpoint follows HTTP. Nothing else
differs.

**There is no `openrate_stream`.** openrate answers from a snapshot it already
holds, so there is no incremental operation to stream and no callback API here.
llmux, which shares this ABI shape, does have one. The omission is deliberate,
not a gap.

## The costs of direct mode — read these

1. **The Go runtime lives in your interpreter.** Its garbage collector, its
   scheduler, and its signal handlers for `SIGSEGV`, `SIGBUS`, `SIGFPE`,
   `SIGPROF` and others. A Python profiler or crash reporter with its own
   handling can conflict; Go chains to a pre-existing handler in most cases, and
   "most" is the honest word.

2. **It is not fork-safe.** After `fork()` without `exec()` the Go runtime in
   the child is broken. Concrete victims in Python:

   | victim | why | fix |
   |---|---|---|
   | `multiprocessing`, `ProcessPoolExecutor` | `fork` is the default start method on Linux before 3.14 | `multiprocessing.set_start_method("spawn")` |
   | uWSGI | master imports the app, then forks workers | load the library in `@postfork`, or use the sidecar |
   | Gunicorn sync / gthread workers | same pre-fork model | load in `post_fork`, or use the sidecar |
   | Celery prefork pool | same | `worker_process_init`, or use the sidecar |

   This was measured on the sibling product, llmux, whose library has the same
   shape, and the result is worth carrying over: **some calls still work in the
   forked child.** Memory-only calls returned fine while anything needing
   another thread hung forever. Applied here, a forked child would likely still
   answer `convert` from a loaded snapshot and hang in `Refresher.refresh()`. A
   smoke test that only converts is therefore not evidence that forking is safe.
   *(openrate's own fork behaviour has not been measured directly — that
   sentence is a prediction from a measurement on the sibling library, not a
   result, and it is written that way on purpose.)*

3. **The shared library is 6.7 MB** — 6,682,274 bytes on darwin/arm64.

4. **Platform reality is narrow.** For **openrate** specifically:

   | target | status |
   |---|---|
   | darwin/arm64 | built, smoke-tested and benchmarked |
   | darwin/amd64 | **built (7,120,680 bytes) but never executed** — the build machine cannot run it. Not "supported"; "compiled" |
   | linux/amd64 | **not built locally.** A CI job exists and has never run |
   | linux/arm64 | **built nowhere.** (llmux has this target; openrate does not) |
   | windows/amd64 | **built nowhere.** No mingw-w64 available. No DLL exists |

   Do not read that table as a support matrix. One row has been executed.

5. **Latency is not the reason to embed.** Measured on darwin/arm64 over 30,000
   iterations: 3.7 µs in-process against 33.5 µs over loopback HTTP, about 9×,
   or ~30 µs saved per call. The HTTP side was deliberately flattered —
   keep-alive on, no TLS — so that is a floor, not a headline. If you are doing
   a million conversions in a tight loop it adds up; if you are answering a web
   request it is invisible. Embed for **the engine that cannot fetch**, for no
   second process, no port and no loopback surface.

## Examples

```bash
python3 examples/direct_convert.py            # engine only — sends no packets
python3 examples/direct_convert.py --fetch    # also builds a refresher (network)
python3 examples/sidecar_convert.py           # spawns and manages a server
python3 examples/sidecar_convert.py https://openrate.example   # or use yours
```

Point them at a build with `OPENRATE_LIBRARY=` and `OPENRATE_BINARY=`.

Real output from `direct_convert.py`, with no network:

```
engine:       <openrate.Engine handle=1 open>
empty engine: openrate: convert USD->ZAR: unknown or unreachable currency pair
load          4 currencies: EUR, GBP, USD, ZAR
convert       100 USD = 1850 ZAR
              rate 18.5, 1 hop(s), path USD -> ZAR, sources ['my-desk']
cross         1 GBP = 1.1651 EUR via GBP -> USD -> EUR
no fetching   openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
```

## Tests

```bash
cd sdks/python && python3 -m unittest discover -s tests
```

39 tests, no network, no Go toolchain. `test_direct.py` drives the real shared
library and skips cleanly when none is resolvable; `test_sidecar.py` drives
`tests/fake_openrate.py`, a stdlib server that honours `OPENRATE_ADDR`. Set
`OPENRATE_BINARY_REAL` to also run the one integration test against the real
server.

The direct tests are about what a ctypes binding gets wrong, not about openrate:

- **ownership** — `openrate_call` returns malloc'd memory that only
  `openrate_free` may release, so a `c_char_p` restype would leak every result.
  One test counts the frees and asserts each pointer is freed exactly once.
- **handles** — every test asserts `open_handles()` returns to where it started.
  That check earned its place immediately: it caught a leak in another test in
  this same file on the first run.
- **the engine/refresher split** — an engine refuses `refresh`, `start`,
  `status` and `ready`; a refresher refuses `convert`; closing the engine
  releases the refresher whatever order you close in.
- **errors** — a closed or invented handle raises rather than crashing.

## Reference

```
openrate.start(port=None, base_currency=None, sources=None, refresh=None,
               env=None, timeout=20.0) -> str
openrate.base_url() -> str
openrate.stop() -> None
openrate.Client(url=None, timeout=10.0)
    .base_url / .convert(from, to, amount=1) / .rates(base=None) / .meta()
    .healthy() / .close() / context manager

openrate.library_path(path=None) -> str
openrate.load_library(path=None, require_version=None) -> ctypes.CDLL
openrate.abi_version(lib=None) -> str
openrate.open_handles(lib=None) -> int
openrate.Engine(config=None, *, library=None, require_version=None)
    .handle / .closed / .close() / context manager
    .call(method, request=None)
    .convert(from, to, amount=1) / .rates(base=None) / .meta()
    .load(edges=..., built_at=...) / .refresher(config=None)
openrate.Refresher(engine, config=None)
    .status() / .refresh(timeout_ms=None) / .start() / .stop() / .ready(timeout_ms=None)
openrate.OpenRateError, openrate.OpenRateLibraryError, openrate.OpenRateSidecarError
```

Probe the version at startup if you load the library from a path you do not
control: `openrate.load_library(require_version="0.1.2")` turns a stale
`libopenrate` earlier on the load path into a clear error instead of behaviour
that looks like an openrate bug.
