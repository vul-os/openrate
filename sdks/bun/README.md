# openrate for Bun

Currency conversion with an auditable rate path — every answer carries the hops
it took, the sources behind each leg, how old they are, and a quality grade.

Two modes in one module, [`index.ts`](index.ts), with no runtime dependencies.
The JSON is identical in both; only the transport differs.

| | **Direct** | **Sidecar** |
|---|---|---|
| what runs | `libopenrate` loaded into this process | the `openrate` binary as a child process on `127.0.0.1` |
| exports | `Engine`, `Refresher` | `Sidecar` |
| dependencies | none — `bun:ffi` | none — `Bun.spawn` and `fetch` |
| can it send a packet? | **only if you build a `Refresher`.** An `Engine` cannot | yes — a server refreshes on startup and on its interval |
| blocks the event loop | engine calls: microseconds. `refresh()`: **yes, seconds** | no |
| survives a `fork()` | no | yes |
| extra bytes on disk | a 6.7 MB shared library | the binary you already have |
| platforms | **darwin/arm64 in practice** — see below | wherever the binary builds |

Tested on **Bun 1.3.14, darwin/arm64**.

## Which mode

**Start with the sidecar.** Then read this, because direct mode makes one
argument the sidecar cannot.

Direct mode's real argument is not speed — it is **the engine that provably
fetches nothing**:

```ts
import { Engine } from "./index.ts";

using engine = Engine.open({ base: "ZAR" });
engine.load({ edges: [{ from: "USD", to: "ZAR", rate: 18.5, source: "my-desk" }] });
engine.convert("USD", "ZAR", 100).result;      // 1850
```

That starts no thread, opens no socket, reads no environment variable and sends
no packet. Not by convention: an engine handle **refuses** the `refresh` method,
and fetching needs a second, explicit construction with its own handle. The
example prints the refusal verbatim:

```
refuses     openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
```

That matters more on Bun than on Deno, not less. **`bun:ffi` has no permission
model** — no `--allow-ffi`, no `--allow-net`, nothing to grant — so the runtime
offers no answer at all to "will this process phone home?". openrate's ABI-level
split is the answer.

**Prefer the sidecar when** your process forks (direct mode is not fork-safe),
when several processes should share one refreshing rate book, or when you are
not on darwin/arm64.

There is deliberately **no streaming** here and no `openrate_stream` in the ABI:
openrate answers from a snapshot it already holds, so there is no incremental
operation to stream. (llmux, which shares this ABI shape, does have one. Do not
go looking for openrate's.)

---

## Direct

```
../../scripts/build-ffi.sh --host-only
bun run examples/direct.ts
```

```
bun        1.3.14 on darwin/arm64
library    /Users/pc/code/vulos/openrate/dist/ffi/libopenrate-darwin-arm64.dylib
abi        0.1.2

engine     handle 1, 1 open

empty       openrate: convert USD->ZAR: unknown or unreachable currency pair
load        EUR, USD, ZAR as of 2026-08-09T00:00:00Z
convert     100 USD = 1850 ZAR
            path USD -> ZAR, sources my-desk
convert     100 EUR = 2133.97 ZAR via EUR -> USD -> ZAR
            2 hops, grade C
rates       base USD -> EUR, ZAR
meta        3 currencies, sources []
refuses     openrate: unknown engine method "refresh" (have: convert, rates, meta, load)

refresher   handle 3, 2 handles open
status      [{"name":"ecb","edges":0,"last_ok":"0001-01-01T00:00:00Z"}]
refresh     [{"name":"ecb","edges":29,"last_ok":"2026-08-09T14:36:48.917408Z"}]
            blocked the event loop for 962 ms; timer fired 0x
convert     100 EUR = 115.35 USD
            as of 2026-08-07T00:00:00Z, 225409s old, sources ecb
start       loop running; 30 currencies after 0 non-blocking polls

handles     0 open after both blocks exited
```

Everything above the blank line ran with no network involvement whatsoever.

### Blocking, and what to do about it

`bun:ffi` has no asynchronous call mode — no `nonblocking` option on a symbol
the way Deno has one — so every direct call runs on the thread that made it.

For engine methods that is academic: `convert`, `rates`, `meta` and `load` are
answered from the snapshot in memory in microseconds.

For `refresh()` it is not academic: **962 ms blocked, timer fired 0×**, against
a single fast source. The default source set includes SARB, whose host can take
tens of seconds to connect.

The answer is `start()`, and the example shows it: the refresh loop is a
goroutine **inside the library**, so `start()` returns immediately and the
fetching never touches Bun's event loop. Wait for rates by polling
`engine.meta().currencies.length` from a `Bun.sleep` loop rather than calling
`refresher.ready()`, which does the same wait with the loop frozen.

(A `node:worker_threads` Worker is the obvious other answer, and this SDK does
not ship one — `start()` solves the same problem with no thread at all. **Do not
assume a Worker would work here.** On Node it does not: a thread that has
entered a Go c-shared library never terminates, so the worker answers and then
hangs the process at exit, measured in [`../node/README.md`](../node/README.md).
Bun's `worker_threads` is a different implementation and **has not been measured
against this library**, so whether it has the same defect is unknown. Treat it
as unverified rather than as a supported route.)

### Handles and memory

`Engine` and `Refresher` both implement `Symbol.dispose`, so `using` closes them
on every exit path out of a block, throw included. `close()` is idempotent, as
`openrate_close` is, and **closing an engine closes every refresher built over
it** — which is why the last line reads `0 open` even though the example never
called `refresher.close()` by hand. `openHandles()` is exported for exactly that
assertion.

Every `char*` the library returns — results **and** error messages — goes
through `openrate_free` before the value reaches you, error path included.
`openrate_call` is declared `FFIType.ptr`, not `FFIType.cstring`, precisely so
bun cannot decode the result into a string and drop the pointer we still have to
free. `openrate_abi_version` *is* declared `cstring`, which is correct there and
only there: it returns storage the library owns and must never be freed.

---

## Sidecar

```ts
import { Sidecar } from "./index.ts";

await using side = await Sidecar.start({ base: "ZAR", sources: "ecb", ui: false });
await side.waitForRates();          // start() waited for the LISTENER, not the rates
const c = await side.convert("EUR", "ZAR", 100);
```

`start()` picks a free `127.0.0.1` port, launches the binary with `-addr`
inheriting the environment so paid-source API keys pass through, and polls
`/healthz` until it answers. Binary resolution is `options.binary` →
`OPENRATE_BINARY` → `openrate` on `PATH`. `Sidecar` implements
`Symbol.asyncDispose`, so `await using` kills the child on the way out.

```
go build -o /tmp/openrate ../../cmd/openrate
OPENRATE_BINARY=/tmp/openrate bun run examples/sidecar.ts
```

```
bun        1.3.14 on darwin/arm64
sidecar    http://127.0.0.1:51585
api        http://127.0.0.1:51585/api/v1

rates       30 currencies after the startup refresh
convert     100 EUR = 1871.36 ZAR
            path EUR -> ZAR, 1 hops, sources ecb
            69.0h old, grade C
rates       base USD -> 29 pairs
meta        default base ZAR, sources [{"name":"ecb","edges":29,"last_ok":"2026-08-09T21:01:30.857741Z"}]
error       openrate: HTTP 404 from …/convert?from=XXX&to=ZAR&amount=1: {"error":"unknown or unreachable currency pair"}
```

(openrate's own log lines are interleaved on a real run; the child inherits
stdio.)

### Liveness, then readiness

Two questions, two endpoints, and `Sidecar` keeps them apart:

| | endpoint | answers | this SDK |
|---|---|---|---|
| liveness | `GET /healthz` | the listener is bound | `start()` |
| readiness | `GET /readyz` | the snapshot has rates | `waitForRates()` |

`/healthz` answers the instant the port opens, before the startup refresh has
fetched anything, so **`start()` returning does not mean a conversion will
work.** Skip `waitForRates()` and the first `convert()` gets a 404 reading
`unknown or unreachable currency pair` — which looks exactly like a typo'd
currency code rather than an empty book. Closing that false green is why the
two calls are separate rather than merged.

`waitForRates()` polls `/readyz` every 150 ms and, on timeout, **throws with
what the server last said** instead of returning 0:

```
openrate has no rates after 3s: no rates yet: no source has returned a usable
quote (ecb: Get "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml":
proxyconnect tcp: dial tcp 127.0.0.1:1: connect: connection refused)
```

Everything after the colon comes off the 503 body: its `reason`, then
`name: last_error` for each source that has one. A source that has not failed
carries no `last_error` and is not printed, so the message degrades to the
reason alone rather than to `ecb: undefined`. If the server never answered at
all, the transport error is printed instead.

The interval is fixed and short on purpose. `/readyz` sits outside `/api/`, and
the binary rate-limits only `/api/` paths, so polling readiness cannot spend the
budget the first `convert()` needs. An earlier version of this SDK waited by
polling `/api/v1/meta`, which *is* rate-limited — a probe that could 429 itself
against its own sidecar.

### The child's rate limit

`start()` passes `OPENRATE_RATELIMIT=0`. The child listens on loopback and
serves exactly one client — this process. The 120/min default is anti-scraping
for a public deployment and there is no stranger here to throttle, while a
legitimate batch of conversions would sail past it and take a 429 from our own
sidecar. Pass `env: { OPENRATE_RATELIMIT: "120" }` to put it back.

### If you are behind a proxy

Bun's `fetch` honours `HTTP_PROXY`/`HTTPS_PROXY` **for loopback URLs too**, so
with one of those set this process cannot reach its own sidecar and `start()`
times out waiting for a server that is running fine. Export
`NO_PROXY=127.0.0.1` — the child still uses the proxy for its upstreams, which
is what you wanted.

One deliberate difference between the transports, worth knowing before you port
code between them: **an unknown `base` is an error in the library and a 200 with
an empty book over HTTP.** The library follows the Go API. An unknown *pair* is
an error in both, as the last line shows.

---

## The costs of direct mode

Not footnotes. These are properties of `-buildmode=c-shared`;
[`ffi/README.md`](../../ffi/README.md) is the long version.

1. **The Go runtime lives in your process** — its GC, its scheduler, and its
   signal handlers. Measured, it **replaces** exactly five — `SIGSEGV`,
   `SIGBUS`, `SIGFPE`, `SIGPIPE` and `SIGURG` — chaining to a pre-existing
   handler, and adds `SA_ONSTACK` to three more (`SIGILL`, `SIGXFSZ`,
   `SIGUSR2`). Bun's JavaScriptCore installs its own signal handling, and
   `SIGSEGV` is the overlap that matters. **`SIGPROF` is not touched**, so
   sampling profilers are unaffected.

2. **It is not fork-safe.** After `fork()` without `exec()` the Go runtime in
   the child is broken. `Bun.spawn` always execs, so ordinary Bun code cannot
   hit this. It becomes real under a pre-fork supervisor that loads your app in
   a master and forks workers. **Load the library after the fork, in the worker,
   never in the master.**

3. **The library is ~6.7 MB** on darwin/arm64. Not a constant: two builds of the
   same source here produced 6,682,274 and 6,700,448 bytes.

4. **Platforms — and openrate's matrix is NOT llmux's.** Do not read one as
   covering the other.

   | target | status |
   |---|---|
   | darwin/arm64 | built, smoke-tested, benchmarked. ~6.7 MB. |
   | darwin/amd64 | **built (7,120,680 bytes) but never executed** — the build machine cannot run it. Unverified. |
   | linux/amd64 | not built locally. A CI job exists and has never run. |
   | linux/arm64 | **built nowhere.** (llmux has this one; openrate does not.) |
   | windows/amd64 | **built nowhere. No DLL exists.** |

   Bun runs on Windows. Direct mode does not. The sidecar is the answer there.

5. **Latency is not the reason to embed.** The boundary is a few microseconds
   in-process against tens over loopback — real, and irrelevant next to an HTTP
   fetch from the ECB. The reasons are the provable no-network engine, no second
   process, no port and no loopback surface.

6. **Two Go libraries means two Go runtimes.** If this process also loads
   libllmux you get two independent runtimes with two GCs. It works; it is not
   free.

---

## Layout and checks

```
sdks/bun/
  index.ts              Engine + Refresher (direct) and Sidecar
  examples/direct.ts    engine-only (no packets) then a refresher (packets)
  examples/sidecar.ts   the binary, over loopback
```

```
bun install
bun run check          # tsc --noEmit, reusing the TypeScript pinned by sdks/node
bun run example:direct
bun run example:sidecar
```

`check` deliberately does not add a second `typescript` dependency to this repo;
it borrows the pinned compiler already installed for `sdks/node`, so there is one
version in the tree rather than two that can drift.
