# openrate for Node

Currency conversion with an auditable rate path — every answer carries the hops
it took, the sources behind each leg, how old they are, and a quality grade.

Two modes. The JSON is identical in both; only the transport differs.

| | **Sidecar** | **Direct** |
|---|---|---|
| what runs | the `openrate` binary as a child process on `127.0.0.1` | `libopenrate` loaded into this Node process |
| module | `require("openrate")` | `require("openrate/direct")` |
| dependencies | none | `koffi` (optional peer) |
| can it send a packet? | yes — a server refreshes on startup and on its interval | **only if you build a `Refresher`.** An `Engine` cannot |
| blocks the event loop | no | **yes, on every call** |
| survives a `fork()` | yes | no |
| extra bytes on disk | the binary you already have | a 6.7 MB shared library |
| platforms | wherever the binary builds | **darwin/arm64 in practice** — see below |

## Which mode

**Start with the sidecar.** Then read the next paragraph, because direct mode
has one argument the sidecar cannot make.

Direct mode's real argument is not speed — it is **the engine that provably
fetches nothing**:

```ts
import { Engine } from "openrate/direct";

using engine = Engine.open({ base: "ZAR" });
engine.load({ edges: [{ from: "USD", to: "ZAR", rate: 18.5, source: "my-desk" }] });
engine.convert("USD", "ZAR", 100).result;      // 1850
```

That program starts no thread, opens no socket, reads no environment variable
and sends no packet. Not "does not by default": an engine handle **refuses** the
`refresh` method, and fetching requires a second, explicit construction with its
own handle.

```ts
using engine = Engine.open();
using refresher = engine.refresher({ sources: "ecb" });
refresher.refresh();                            // THIS OPENS SOCKETS
```

The example in [`examples/direct.ts`](examples/direct.ts) prints the refusal
verbatim:

```
refuses     openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
```

That split is enforced at the ABI, not by convention. If you are shipping into
an audited environment, an airgapped one, or a process that must not surprise
anyone with outbound traffic, direct mode is the mode that can be *proved*
rather than promised.

**Prefer the sidecar when:**

- **Your Node process serves requests.** Every direct call is synchronous and
  blocks the event loop. Engine methods answer from memory in microseconds, so
  that is academic for `convert`/`rates`/`meta`/`load`. `refresher.refresh()` is
  not: measured below at **795 ms blocked, timer fired 0×**, against one fast
  source. Use `refresher.start()` instead — the loop is a goroutine inside the
  library and returns immediately — or use the sidecar.
- **You want one shared, refreshing rate book** across several processes. That
  is what a server is; running four fetchers in four workers to hold four copies
  of the same book is worse in every dimension.
- **You are not on darwin/arm64.** See the platform section.

There is deliberately **no streaming here**, and no `openrate_stream` in the
ABI: openrate answers from a snapshot it already holds, so there is no
incremental operation to stream. (llmux, which shares this ABI shape, does have
one — `llmux_stream`. Do not go looking for openrate's.)

---

## Direct

```
npm install
npm install koffi            # optional peer, direct mode only
../../scripts/build-ffi.sh
npm run build
node examples/direct.ts
```

```
node       v24.12.0 on darwin/arm64
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
refresh     [{"name":"ecb","edges":29,"last_ok":"2026-08-09T14:16:41.075359Z"}]
            blocked the event loop for 795 ms; timer fired 0x
convert     100 EUR = 115.35 USD
            as of 2026-08-07T00:00:00Z, 224201s old, sources ecb
meta        30 currencies after one ECB fetch

handles     0 open after both blocks exited
```

Everything up to the blank line ran with the network unplugged as far as the
library is concerned. The `refresher` section needs the internet; if it is not
there, the example says so and still exits 0.

### Handles and memory

`Engine` and `Refresher` both implement `Symbol.dispose`, so `using` closes them
on every exit path out of the block, throw included. `close()` is idempotent, as
`openrate_close` is, and **closing an engine also closes every refresher built
over it** — which is why the last line above reads `0 open` even though the
example never called `refresher.close()` by hand. `openHandles()` is exported
for exactly that assertion.

Every `char*` the library returns — results **and** error messages — goes
through `openrate_free` before the value reaches you, on the error path
included. `openrate_call` is declared as returning `void*`, not `char*`,
precisely so koffi cannot decode it into a string and discard the pointer we
still have to free. `openrate_abi_version` *is* declared `const char*`, which is
correct there and only there: it returns storage the library owns and that must
never be freed.

### Why koffi

Node has no FFI in its standard library. koffi is declared an **optional peer
dependency**, so the sidecar path installs with no native code at all.

- **`node-ffi-napi`** — effectively unmaintained; its last release predates
  several Node majors. Not viable.
- **A hand-written N-API addon** — the honest alternative. Rejected on cost: it
  means node-gyp and a C toolchain at install time, or `prebuildify` artifacts
  for every platform × Node-ABI pair, for a seven-function ABI. It would buy a
  build pipeline and, for openrate, nothing else — there is no callback here to
  need `napi_threadsafe_function`.
- **`koffi`** — MIT, actively released (3.1.4 at the time of writing), ships
  prebuilt binaries so `npm install` needs no compiler, ~1.9 MB installed, and
  its declarative C prototypes make this binding a transcription of
  `openrate.h` rather than a reimplementation of it.

The cost is real and worth naming: koffi is native code in your process, and a
bug in it is a segfault, not an exception.

### Why every call is synchronous

`bun:ffi` has a worker option and Deno has `nonblocking: true`; Node has
neither, and the two ways to fake it both break. Measured on darwin/arm64,
Node v24.12.0, koffi 3.1.4:

| approach | worked? | process exited? |
|---|---|---|
| main thread, synchronous | yes | yes |
| `worker_threads` worker | yes | **no** — the worker's `exit` event never fires |
| koffi `.async` (libuv threadpool) | yes | **no** — hangs after the last statement |

Minimal reproduction, with no openrate logic in it at all:

```js
import { Worker, isMainThread, parentPort } from "node:worker_threads";
if (!isMainThread) {
  const koffi = (await import("koffi")).default;
  const lib = koffi.load("…/libopenrate-darwin-arm64.dylib");
  parentPort.postMessage(lib.func("const char *openrate_abi_version()")());
} else {
  const w = new Worker(new URL(import.meta.url));
  console.log("worker said:", await new Promise((r) => w.on("message", r)));
  console.log("worker exited with", await new Promise((r) => w.on("exit", r))); // never prints
}
```

It prints `worker said: 0.1.2` and hangs forever. The control — the same code
against `/usr/lib/libSystem.B.dylib` and `atoi` — prints `worker exited with 0`.
It reproduces against libopenrate and libllmux and not against a C library, so
it is a Go-runtime × non-main-thread interaction rather than a koffi or openrate
bug: a thread that has entered the Go runtime cannot be joined, and Node joins
its worker threads on the way out.

For openrate this costs almost nothing, because the engine methods that matter
answer from memory. It costs something for `refresh()`, and the answer there is
`start()`: the refresh loop is a goroutine **inside the library**, so it returns
immediately and the fetching never touches Node's event loop. Poll
`engine.meta().currencies.length` from a `setInterval` if you need to know when
rates have arrived — `refresher.ready()` does the same wait with the loop frozen.

---

## Sidecar

```ts
import { Sidecar } from "openrate";

const side = await Sidecar.start({ base: "ZAR", sources: "ecb", ui: false });
await side.waitForRates();
const c = await side.convert("EUR", "ZAR", 100);
side.stop();
```

`start()` picks a free `127.0.0.1` port, launches the binary with `-addr`
inheriting the environment so paid-source API keys pass through, and polls
`/healthz` until it answers. Binary resolution is
`options.binary` → `OPENRATE_BINARY` → `openrate` on `PATH`.

```
go build -o /tmp/openrate ../../cmd/openrate
OPENRATE_BINARY=/tmp/openrate node examples/sidecar.ts
```

```
node       v24.12.0 on darwin/arm64
sidecar    http://127.0.0.1:60806
api        http://127.0.0.1:60806/api/v1

rates       30 currencies after the startup refresh
convert     100 EUR = 1871.36 ZAR
            path EUR -> ZAR, 1 hops, sources ecb
            62.3h old, grade C
rates       base USD -> 29 pairs
meta        default base ZAR, sources [{"name":"ecb","edges":29,"last_ok":"2026-08-09T14:17:50.169175Z"}]
error       openrate: HTTP 404 from …/convert?from=XXX&to=ZAR&amount=1: {"error":"unknown or unreachable currency pair"}
```

(openrate's own log lines are interleaved with that on a real run; the child
inherits stdio.)

One deliberate difference between the two transports, worth knowing before you
port code between them: **an unknown `base` is an error in the library and a 200
with an empty book over HTTP.** The library follows the Go API. An unknown
*pair* is an error in both.

---

## The costs of direct mode

Not footnotes. These are properties of `-buildmode=c-shared`;
[`ffi/README.md`](../../ffi/README.md) is the long version.

1. **The Go runtime lives in your process** — its GC, its scheduler, and its
   handlers for `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPROF` and others. Go chains
   to a pre-existing handler in most cases; "most" is the honest word.

2. **It is not fork-safe.** After `fork()` without `exec()` the Go runtime in
   the child is broken. Node's own `child_process` and `cluster` always `exec`,
   so ordinary Node code is not at risk. The victims are native modules and
   supervisors that fork the interpreter itself: `posix.fork()` from an addon,
   or any pre-fork wrapper that loads your app in a master and forks workers.
   **Load the library after the fork, in the worker, never in the master.**

3. **The library is 6.7 MB.** Measured: 6,682,274 bytes on darwin/arm64.

4. **Platforms — and openrate's matrix is NOT llmux's.** Do not read one as
   covering the other.

   | target | status |
   |---|---|
   | darwin/arm64 | built, smoke-tested, benchmarked. 6,682,274 bytes. |
   | darwin/amd64 | **built (7,120,680 bytes) but never executed** — the build machine cannot run it. Treat it as unverified. |
   | linux/amd64 | not built locally. A CI job exists and has never run. |
   | linux/arm64 | **built nowhere.** (llmux has this one; openrate does not.) |
   | windows/amd64 | **built nowhere. No DLL exists.** |

   Node is heavily used on Windows, so say it plainly: **direct mode is not
   available on Windows.** The sidecar is, and it is the answer there.

5. **Latency is not the reason to embed.** The boundary is a few microseconds
   in-process against tens of microseconds over loopback — real, and irrelevant
   next to an HTTP fetch from the ECB. The reasons are the provable no-network
   engine, no second process, no port and no loopback surface.

6. **Two Go libraries means two Go runtimes.** If this process also loads
   libllmux you get two independent runtimes with two GCs. It works; it is not
   free.

---

## Layout and checks

```
sdks/node/
  index.ts              the sidecar client + a re-export of direct mode
  sidecar.ts            spawn, health poll, convert/rates/meta helpers
  direct.ts             the C ABI binding: Engine and Refresher
  examples/direct.ts    engine-only (offline) then a refresher (online)
  examples/sidecar.ts   the binary, over loopback
```

```
npm run build                # tsc -> index.js/.d.ts, direct.js/.d.ts, sidecar.js/.d.ts
npm run typecheck            # tsc over the library sources
npm run typecheck:examples   # tsc over examples/ (ESM, its own tsconfig)
npm run example:direct
npm run example:sidecar
```
