# openrate for Deno

Currency conversion with an auditable rate path — every answer carries the hops
it took, the sources behind each leg, how old they are, and a quality grade.

Two modes in one dependency-free module, [`mod.ts`](mod.ts). The JSON is
identical in both; only the transport differs.

| | **Direct** | **Sidecar** |
|---|---|---|
| what runs | `libopenrate` loaded into this isolate's process | the `openrate` binary as a child process on `127.0.0.1` |
| exports | `Engine`, `Refresher` | `Sidecar` |
| flags | `--allow-ffi` | `--allow-run --allow-net --allow-env` |
| dependencies | none — `Deno.dlopen` | none — `Deno.Command` and `fetch` |
| can it send a packet? | **only if you build a `Refresher`.** An `Engine` cannot | yes — a server refreshes on startup and on its interval |
| blocks the event loop | no (engine calls are microseconds; `refresh`/`ready` are `nonblocking`) | no |
| survives a `fork()` | no | yes |
| extra bytes on disk | a 6.7 MB shared library | the binary you already have |
| platforms | **darwin/arm64 in practice** — see below | wherever the binary builds |

Tested on **Deno 2.7.11 (aarch64-apple-darwin)**.

## Which mode

**Start with the sidecar.** Then read this, because direct mode makes one
argument the sidecar cannot.

Direct mode's real argument is not speed — it is **the engine that provably
fetches nothing**:

```ts
import { Engine } from "./mod.ts";

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

**Prefer the sidecar when** your process forks (direct mode is not fork-safe),
when several processes should share one refreshing rate book, or when you are
not on darwin/arm64.

There is deliberately **no streaming** here and no `openrate_stream` in the ABI:
openrate answers from a snapshot it already holds, so there is no incremental
operation to stream. (llmux, which shares this ABI shape, does have one. Do not
go looking for openrate's.)

---

## `--allow-ffi` and what it does not gate

This is the part worth reading twice.

`examples/direct.ts` runs under **`--allow-ffi` and nothing else** — no
`--allow-net`, no `--allow-read`, no `--allow-env` — and it still fetches live
rates from the ECB in part 2. That is not a bug in Deno and not a hole in this
binding: the refresher's sockets are opened by Go, inside the shared library,
below the layer where Deno's permission checks live. **`--allow-ffi` is
effectively "allow anything", and Deno's own docs say so.**

Which is exactly why openrate's engine/refresher split is enforced at the ABI
rather than left as a convention. Deno cannot tell you that this process will
not phone home. `openrate_new` can: an engine has no code path to a socket, and
the refusal above is the library saying so out loud.

`resolveLibrary()` is written for this permission set too. It reads
`OPENRATE_LIBRARY` only if env permission is already granted (checked with
`Deno.permissions.querySync`), and when `Deno.statSync` fails with anything
other than `NotFound` — which under `--allow-ffi` alone means "no read
permission, cannot look" — it returns the checkout path anyway and lets `dlopen`
deliver the verdict, rather than silently skipping a library sitting right
there.

---

## Direct

```
../../scripts/build-ffi.sh --host-only
deno task example:direct
```

```
deno       2.7.11 on darwin/aarch64
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
refresh     [{"name":"ecb","edges":29,"last_ok":"2026-08-09T14:32:11.23489Z"}]
            took 953 ms; the event loop ticked 366x meanwhile
convert     100 EUR = 115.35 USD
            as of 2026-08-07T00:00:00Z, 225131s old, sources ecb
meta        30 currencies after one ECB fetch

handles     0 open after both blocks exited
```

Everything above the blank line ran with no network involvement whatsoever. The
refresher section needs the internet; if it is not there, the example says so
and still exits 0.

### Sync where it is free, async where it is not

Engine methods (`convert`, `rates`, `meta`, `load`) are **synchronous**, because
they are answered from the snapshot in memory in microseconds — a promise would
cost more than the call. `refresh()` and `ready()` are **async**: their symbols
are declared `nonblocking: true`, so Deno runs them on a blocking-task thread.
The `366x` above is a 1 ms timer counting ticks across a 953 ms ECB fetch. The
same call in the Node SDK measures `0`, for reasons documented there.

### Handles and memory

`Engine` and `Refresher` both implement `Symbol.dispose`, so `using` closes them
on every exit path out of a block, throw included. `close()` is idempotent, as
`openrate_close` is, and **closing an engine closes every refresher built over
it** — which is why the last line reads `0 open` even though the example never
called `refresher.close()` by hand. `openHandles()` is exported for exactly that
assertion.

Every `char*` the library returns — results **and** error messages — goes
through `openrate_free` before the value reaches you, error path included.
`openrate_abi_version` is the one exception, and the code says so where it is
read: it returns storage the library owns and must never be freed.

---

## Sidecar

```ts
import { Sidecar } from "./mod.ts";

await using side = await Sidecar.start({ base: "ZAR", sources: "ecb", ui: false });
await side.waitForRates();          // start() waited for the LISTENER, not the rates
const c = await side.convert("EUR", "ZAR", 100);
```

`start()` picks a free `127.0.0.1` port, launches the binary with `-addr`, and
polls `/healthz` until it answers. Binary resolution is `options.binary` →
`OPENRATE_BINARY` → `openrate` on `PATH`. `Sidecar` implements
`Symbol.asyncDispose`, so `await using` kills the child on the way out.

```
go build -o /tmp/openrate ../../cmd/openrate
OPENRATE_BINARY=/tmp/openrate deno task example:sidecar
```

```
deno       2.7.11 on darwin/aarch64
sidecar    http://127.0.0.1:52104
api        http://127.0.0.1:52104/api/v1

rates       30 currencies after the startup refresh
convert     100 EUR = 1871.36 ZAR
            path EUR -> ZAR, 1 hops, sources ecb
            69.0h old, grade C
rates       base USD -> 29 pairs
meta        default base ZAR, sources [{"name":"ecb","edges":29,"last_ok":"2026-08-09T21:01:46.957746Z"}]
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

`start()` passes `OPENRATE_RATELIMIT=0` on top of the inherited environment
(`clearEnv` is false, so the child still sees your API keys). The child listens
on loopback and serves exactly one client — this process. The 120/min default is
anti-scraping for a public deployment and there is no stranger here to throttle,
while a legitimate batch of conversions would sail past it and take a 429 from
our own sidecar. Pass `env: { OPENRATE_RATELIMIT: "120" }` to put it back.

### If you are behind a proxy

Deno's `fetch` honours `HTTP_PROXY`/`HTTPS_PROXY` **for loopback URLs too**, so
with one of those set this process cannot reach its own sidecar and `start()`
times out waiting for a server that is running fine. Export
`NO_PROXY=127.0.0.1` — the child still uses the proxy for its upstreams, which
is what you wanted.

The two transports answer identically, including on the error path. **An
unknown `base` is an error in the library and a `404` over HTTP**, carrying the
same `unknown base currency` text; until 0.1.6 the endpoint answered `200` with
an empty book, which was indistinguishable from a server that had not fetched
yet. An unknown *pair* is an error in both, as the last line shows.

---

## The costs of direct mode

Not footnotes. These are properties of `-buildmode=c-shared`;
[`ffi/README.md`](../../ffi/README.md) is the long version.

1. **The Go runtime lives in your process** — its GC, its scheduler, and its
   signal handlers. Measured, it **replaces** exactly five — `SIGSEGV`,
   `SIGBUS`, `SIGFPE`, `SIGPIPE` and `SIGURG` — chaining to a pre-existing
   handler, and adds `SA_ONSTACK` to three more (`SIGILL`, `SIGXFSZ`,
   `SIGUSR2`). **`SIGPROF` is not touched**, so sampling profilers are
   unaffected.

2. **It is not fork-safe.** After `fork()` without `exec()` the Go runtime in
   the child is broken. Deno has no `fork()` in its API — `Deno.Command` always
   execs — so ordinary Deno code cannot hit this. It becomes real if you embed
   Deno (`deno_core`) in a host that pre-forks, or run under a supervisor that
   forks after loading. **Load the library after the fork, in the worker, never
   in the master.**

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

   Deno runs on Windows. Direct mode does not. The sidecar is the answer there.

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
sdks/deno/
  deno.json               tasks, fmt and lint config
  mod.ts                  Engine + Refresher (direct) and Sidecar
  examples/direct.ts      engine-only (no packets) then a refresher (packets)
  examples/sidecar.ts     the binary, over loopback
```

```
deno task check      # deno check mod.ts and both examples
deno task lint
deno task fmt:check
```
