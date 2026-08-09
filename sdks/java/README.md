# openrate (Java)

Two ways to run openrate from Java, both supported:

| | class | what it is | recommended? |
|---|---|---|---|
| **Sidecar** | `org.vulos.openrate.OpenRate` | spawns `openrate` on `127.0.0.1`, talks HTTP | **yes — the default for the JVM** |
| **Direct** | `org.vulos.openrate.OpenRateDirect` | loads `libopenrate` and runs the engine *inside this JVM* | when you need the zero-network guarantee, and after reading [the signal-handler section](#the-jvm-and-gos-signal-handlers) |

**On the JVM the sidecar is the recommended default.** The direct path works —
both examples below run green and the measurements are in this file — but it
requires a change to the *java launch command* that a library cannot make on its
own, and openrate's shared library exists for exactly one platform.

**There is one thing only the direct path can give you, and it is not speed:**
an engine that provably sends no packets. If that is what you are buying, read
[Direct](#direct--orgvulosopenrateopenratedirect) rather than this table.

```sh
sdks/java/run-examples.sh            # both
sdks/java/run-examples.sh direct     # offline by default
sdks/java/run-examples.sh sidecar    # needs a network
```

---

## Sidecar — `org.vulos.openrate.OpenRate`

```java
OpenRate.Options opts = new OpenRate.Options();
opts.base = "ZAR";
opts.sources = "ecb,coinbase";

try (OpenRate rates = OpenRate.start(opts)) {
    String converted = rates.convert("USD", "ZAR", 100);
    String book      = rates.rates("EUR");
    String meta      = rates.meta();
}
```

**Requires Java 11.** No native library, no FFM, no platform matrix. Runs on
Windows, where the direct path does not exist at all.

Worked example: [`examples/SidecarRates.java`](examples/SidecarRates.java).

### `start()` waits for rates, not just for a listening socket

This is the one non-obvious thing in the sidecar wrapper, and it exists because
the example caught it:

**`/healthz` is a liveness probe.** openrate answers it `200` the moment it
binds, while its first source fetch is still in flight. Every conversion in that
window comes back:

```json
{"error":"unknown or unreachable currency pair"}
```

which reads as *you passed a bad currency code*, not as *the server is not ready
yet*. The first version of the example did exactly this and printed error
objects for every call while still exiting 0.

So `OpenRate.start()` waits for `/healthz` **and then** for `/readyz`, which is
the server's own readiness probe: `200` once the snapshot holds currencies, and
until then `503` with a body that says why.

```json
{"ready":false,"currencies":0,
 "reason":"no rates yet: no source has returned a usable quote",
 "sources":[{"name":"ecb","edges":0,
             "last_error":"… dial tcp 127.0.0.1:1: connect: connection refused"}]}
```

That body is why the wait is worth having: a startup that never succeeds fails
with the cause rather than with the elapsed time.

```
openrate has no rates after 60s: no rates yet: no source has returned a usable
quote (ecb: … dial tcp 127.0.0.1:1: connect: connection refused)
```

Turn the wait off with `Options.waitForRates = false` if you want to start
against a cold server on purpose.

Earlier versions polled `/api/v1/meta` until its currency list was non-empty,
because openrate had no readiness endpoint. That workaround had a sting:
`/api/v1/meta` is under `/api/`, the only prefix the server rate-limits, so
polling it spent the budget the first real call needed. `/readyz` and
`/healthz` both sit outside `/api/` and are never limited, which is why the
poll here is a flat 200 ms rather than a backoff.

### The child runs with its rate limiter off

`Options.ratelimit` defaults to `0`, passed to the child as
`OPENRATE_RATELIMIT=0`. The child listens on loopback and serves exactly one
client: your process. The limiter is anti-scraping for a public deployment and
there is no stranger here to throttle — while a legitimate batch of conversions
would sail straight past the `120`/min default and take a `429` from your own
sidecar. Set `Options.ratelimit = 120` to put the binary's default back.

### The sidecar fetches, and that is not configurable

`openrate serve` starts its refresher at startup. Standing one up means outbound
requests to the configured sources. If "no packets unless I ask for them" is the
property you need, the direct path's engine is the answer, and it is a real
guarantee rather than a setting.

### Binary resolution

1. `OPENRATE_BINARY`
2. a sibling `bin/openrate` next to the classes, or under `$OPENRATE_HOME`
3. `openrate` on `PATH`

```sh
go build -o sdks/java/bin/openrate ./cmd/openrate
```

---

## Direct — `org.vulos.openrate.OpenRateDirect`

openrate inside your JVM, through the C ABI in
[`ffi/README.md`](../../ffi/README.md) and
[`ffi/include/openrate.h`](../../ffi/include/openrate.h).

### The engine/refresher split is the design, and it is enforced at the ABI

```java
try (OpenRateDirect engine = OpenRateDirect.openEngine("{\"base\":\"ZAR\"}")) {

    // Zero network. Not "we try not to" — there is no code path.
    engine.call("load", myRatesJson);
    String converted = engine.call("convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}");

    // Fetching is a SEPARATE object with its own handle.
    try (OpenRateDirect refresher = engine.newRefresher("{\"sources\":\"ecb\"}")) {
        refresher.call("refresh", "{\"timeout_ms\":30000}");   // this opens sockets
    }
}
```

- `openEngine` starts no thread, opens no socket, reads no environment variable
  and sends no packet.
- `newRefresher` is the only thing that can open a socket — **and building one
  still does not**. Fetching begins at `"refresh"` or `"start"`.
- An engine handle **refuses** `"refresh"`. From the example's real output:

  ```
  engine refuses "refresh": openrate_call(refresh): openrate: unknown engine method
      "refresh" (have: convert, rates, meta, load)
  ```

- `"meta"` on an unrefreshed engine reports `"sources": []` — the zero-network
  claim in the library's own words.

A program that never calls `newRefresher` cannot make this library touch the
network, and you can hold it to that rather than trust it.

| handle | methods |
|---|---|
| engine | `convert`, `rates`, `meta`, `load` |
| refresher | `status`, `refresh`, `start`, `stop`, `ready` |

### There is no streaming

No `openrate_stream`, and no streaming method on this class. openrate answers
from a snapshot it already holds, so there is no incremental operation to
stream. llmux, which shares this ABI's shape, does define `llmux_stream`. The
omission is deliberate and is stated rather than left to be noticed.

### Requirements

- **Java 22+** — `java.lang.foreign` became permanent in Java 22. **Tested on
  OpenJDK 26.0.2 (Homebrew), darwin/arm64.**
- **`--enable-native-access=ALL-UNNAMED`** on the java command line.
- A `libopenrate` for your platform. See [Platforms](#platforms) — the list is
  short.

### Why FFM and not JNA

FFM is in the JDK. JNA is a dependency that ships its own native stub per
platform, so adopting it to load a native library means shipping *two* native
artifacts to solve the problem of shipping one.

**JNA is the documented fallback for Java 11–21**, where FFM is absent or
preview. It is **not implemented here and not tested**. The shape is
`Pointer openrate_call(long, String, String, PointerByReference)` via
`Native.load("openrate", …)`, and the trap is mapping the return as `String`:
JNA copies it and you can no longer hand the original to `openrate_free`. Given
that Java 21 is a widely deployed LTS, **the honest recommendation for 11–21 is
the sidecar**, which is fully supported there and needs no native code.

### Memory and handles

- results are copied into a `java.lang.String` and then freed with
  `openrate_free` in a `finally`;
- error strings are read, freed, and turned into `OpenRateException`;
- the `char** err` out-parameter is drained **on the success path too**;
- handles are closed by `close()`, which is idempotent — use
  try-with-resources, as the example does. Closing an engine also stops and
  releases every refresher built over it, so a background `"start"` loop cannot
  outlive its engine.

And the accounting is checkable rather than claimed: `openHandles()` exposes
`openrate_open_handles()`, and the example asserts the count returns to where it
started. From its real output:

```
open handles after creating the engine: 1
open handles with a refresher: 2
open handles after the refresher closed: 1
handle accounting: 1 open, 0 after close
after close: this engine is closed
```

Use after close is a clean error because handle numbers are **retired, not
recycled** — a stale handle can never reach somebody else's object.

---

## The JVM and Go's signal handlers

openrate is Go, so loading `libopenrate` puts the Go runtime — GC, scheduler,
signal handlers — into your JVM. This is measured, not described:

```sh
sdks/java/signal-probe.sh              # what changed
sdks/java/signal-probe.sh --checkjni   # HotSpot's own audit of it
sdks/java/signal-probe.sh --jsig       # again, with libjsig preloaded
```

On **OpenJDK 26.0.2, darwin/arm64, openrate 0.1.2**:

```
signal    before                after                 verdict
--------------------------------------------------------------------------
SIGILL    0x106977cc0 f=0x42    0x106977cc0 f=0x43    flags changed (Go added SA_ONSTACK)
SIGFPE    0x106977cc0 f=0x42    0x12815c7f0 f=0x43    HANDLER REPLACED by the Go runtime
SIGBUS    0x106977cc0 f=0x42    0x12815c7f0 f=0x43    HANDLER REPLACED by the Go runtime
SIGSEGV   0x106977cc0 f=0x43    0x12815c7f0 f=0x43    HANDLER REPLACED by the Go runtime
SIGPIPE   0x106977cc0 f=0x42    0x12815c7f0 f=0x43    HANDLER REPLACED by the Go runtime
SIGURG    SIG_DFL f=0x0         0x12815c7f0 f=0x43    HANDLER REPLACED by the Go runtime
SIGXFSZ   0x106977cc0 f=0x42    0x106977cc0 f=0x43    flags changed (Go added SA_ONSTACK)
SIGPROF   SIG_DFL f=0x0         SIG_DFL f=0x0         unchanged
SIGUSR2   0x106977940 f=0x42    0x106977940 f=0x43    flags changed (Go added SA_ONSTACK)

5 handler(s) replaced, 3 left in place with altered flags
```

Five things worth stating plainly:

1. **`SIGSEGV` is replaced.** HotSpot elides null checks in compiled code and
   recovers them from `SIGSEGV`, and grows stacks through guard-page faults.
   This is the JVM's most load-bearing signal, and a Go shared library takes it.
2. **`SIGPROF` is not touched.** This is the one everyone expects to break. Go
   installs only *synchronous* signals plus `SIGPIPE` and `SIGURG` when built
   `-buildmode=c-shared`, and `SIGPROF` is neither. Profiling is unaffected —
   do not write defensive code for a problem that is not there.
3. **Go mutates handlers it does not replace**, adding `SA_ONSTACK` to `SIGILL`,
   `SIGXFSZ` and `SIGUSR2`. `SIGUSR2` is HotSpot's `SR_handler`, the thread
   suspend/resume mechanism safepoints depend on.
4. **HotSpot notices.** Under `-Xcheck:jni` the VM audits its own handlers and
   prints `Warning: SIGSEGV handler modified!` (and four more), ending with
   `Consider using jsig library.`
5. **Both runtimes keep working.** Go chains to the handler it displaced. After
   loading the library: 2,000,000 implicit null checks recovered as
   `NullPointerException`, `StackOverflowError` raised and caught, and a Go
   nil-dereference *inside* a Go c-shared library called from a Java thread
   still recovered as a Go panic.

The numbers above are **identical to llmux's**, which is the point: this is a
property of `-buildmode=c-shared`, not of either product.

### `libjsig` fixes it, and the fix is not something a library can apply

The JDK ships `libjsig`, which interposes `sigaction` so a library's handlers
are chained behind the JVM's rather than over them:

```sh
DYLD_INSERT_LIBRARIES=$JAVA_HOME/lib/libjsig.dylib java …   # macOS
LD_PRELOAD=$JAVA_HOME/lib/libjsig.so java …                 # Linux
```

With it preloaded, `-Xcheck:jni` reports **no modified handlers at all**.

One measurement caveat: `libjsig` interposes the probe's own `sigaction()` calls
too, so under `--jsig` the address column still shows Go's handler. That column
is not evidence there; HotSpot's audit is, and it is silent.

**But it is a flag on the java launch command.** A library cannot add one to a
process that already started, so a direct-path dependency is not a drop-in — it
is a dependency plus an operations change. That is the argument for the sidecar,
and it is an argument about deployment rather than about correctness.

### Untested, and stated as such

- **Linux.** Everything above is darwin/arm64. `signal-probe.sh` knows the Linux
  signal numbers and will run; nobody has run it.
- **JVMTI agents and async-profiler**, which may install handlers *after*
  `libopenrate` loads and capture Go's as the chain target.
- **JDKs other than 26.**

---

## The costs of the direct path

Full detail in [`ffi/README.md`](../../ffi/README.md).

1. **The Go runtime lives in your process.** See above; on the JVM this is the
   big one.
2. **Not fork-safe.** After `fork()` without `exec()` the Go runtime in the
   child is broken. The JVM is a mild case — it does not pre-fork, and
   `ProcessBuilder`/`Runtime.exec` are safe because they `posix_spawn` or
   `fork`+`exec`. The victims are a JNI/FFM call to bare `fork(2)`, and any
   supervisor that forks the JVM after the library is loaded.
3. **The library is ~6.7 MB** on darwin/arm64 — 6,682,274 bytes for the build
   in `dist/` at the time of writing and 6,700,448 for a later rebuild of the
   same source. The figure moves a little with build paths, so treat it as
   "about 6.7 MB" rather than a constant. Smaller than llmux's 12–17 MB, and
   still not nothing.
4. **One platform.** See below.
5. **Latency is not the reason to embed.** The reason is the engine's
   zero-network guarantee, plus no second process and no port. Not microseconds.

## Platforms

The **shared library** (direct path only) — openrate's coverage is **narrower
than llmux's**, and they are not interchangeable:

| target | status |
|---|---|
| darwin/arm64 | **built, smoke-tested and benchmarked.** ~6.7 MB. Everything on this page was run on it. |
| darwin/amd64 | **built (7,120,680 bytes) but NEVER EXECUTED.** Do not read this row as tested. |
| linux/amd64 | **not built locally.** A CI job exists and has never run. |
| **linux/arm64** | **built nowhere** — unlike llmux, which has a tested one. |
| **windows/amd64** | **built nowhere. No DLL exists.** |

**There is no Windows DLL and no linux/arm64 build**, so this file gives no
install instructions for either. `OpenRateDirect.findLibrary()` says so in its
error message rather than throwing a bare loader error.

The **sidecar** has no such matrix — it is an ordinary Go binary.

## Toolchain this was built and run on

- OpenJDK **26.0.2** (Homebrew), darwin/arm64
- Maven **3.9.16** (for `pom.xml`; `run-examples.sh` uses plain `javac`)
- Go **1.25.12**, openrate **0.1.2**

## Layout

```
sdks/java/
  src/main/java/org/vulos/openrate/OpenRate.java           sidecar (Java 11+)
  src/main/java/org/vulos/openrate/OpenRateDirect.java     direct, FFM (Java 22+)
  src/main/java/org/vulos/openrate/OpenRateException.java
  examples/SidecarRates.java     runnable — live rates, needs a network
  examples/DirectRates.java      runnable — offline unless OPENRATE_ALLOW_NETWORK=1
  tools/SignalHandlerProbe.java  the evidence for this README
  run-examples.sh
  signal-probe.sh
```
