# openrate (Kotlin)

Idiomatic Kotlin over the [Java SDK](../java): `use {}`, named arguments,
default parameters, and typed helpers instead of hand-built JSON.

| | class | what it is | recommended? |
|---|---|---|---|
| **Sidecar** | `OpenRateSidecar` | spawns `openrate` on `127.0.0.1`, talks HTTP | **yes — the default for the JVM** |
| **Direct** | `OpenRate.engine()` → `OpenRateEngine` | loads `libopenrate` into this JVM | when you need the zero-network guarantee |

**This is a wrapper, not a reimplementation.** The FFM calls, the memory rules
and the handle lifecycle live in `sdks/java`; two bindings to one C ABI is two
places for a use-after-free.

```sh
sdks/kotlin/run-examples.sh            # both
sdks/kotlin/run-examples.sh direct     # offline by default
sdks/kotlin/run-examples.sh sidecar    # needs a network
```

---

## Sidecar — the recommended default

```kotlin
OpenRateSidecar(base = "ZAR", sources = "ecb,coinbase").use { rates ->
    println(rates.convert("USD", "ZAR", 100.0))
    println(rates.rates("EUR"))
}
```

`use {}` stops the child process on every path out. Runs on **Java 11+** with
no native library, no `--enable-native-access`, and no platform matrix —
including on Windows, where the direct path does not exist.

**It fetches.** `openrate serve` starts its refresher at startup, so standing
one up means outbound requests. There is no offline mode for the server.

The constructor waits for `/healthz` **and** for the currency list to fill.
`/healthz` is liveness only: openrate answers 200 the moment it binds, while its
first fetch is in flight, and conversions in that window return
`{"error":"unknown or unreachable currency pair"}` — which reads as a bad
currency code rather than "not ready". Pass `waitForRates = false` to start
against a cold server deliberately.

Worked example: [`examples/SidecarRates.kt`](examples/SidecarRates.kt).

## Direct — the engine that provably sends no packets

This is the reason the direct path exists here, and it is not latency.

```kotlin
OpenRate.engine(base = "ZAR").use { engine ->
    engine.load(myRatesJson)                        // rates you obtained yourself
    println(engine.convert("USD", "ZAR", 100.0))    // no socket has been opened

    engine.withRefresher(sources = "ecb") { refresher ->
        refresher.refresh(timeoutMs = 30_000)        // THIS opens sockets
    }
}
```

- `OpenRate.engine()` starts no thread, opens no socket, reads no environment
  variable and sends no packet.
- `withRefresher` builds the fetching half, runs the block, and closes it —
  leaving the engine open. Constructing a refresher **still** opens nothing;
  fetching begins at `refresh()` or `start()`.
- The split is enforced at the ABI, not by convention. From the example's real
  output:

  ```
  engine refuses "refresh": openrate_call(refresh): openrate: unknown engine method
      "refresh" (have: convert, rates, meta, load)
  ```

- `engine.openHandles` surfaces `openrate_open_handles()`, so a test can assert
  it closed what it opened rather than assume `use {}` did its job:

  ```
  open handles after creating the engine: 1
  open handles with a refresher: 2
  open handles after the refresher closed: 1
  handle accounting: 1 open, 0 after close
  ```

Requires **Java 22+** (the underlying binding is `java.lang.foreign`) and
`--enable-native-access=ALL-UNNAMED`. Worked example:
[`examples/DirectRates.kt`](examples/DirectRates.kt).

### No streaming, and therefore no coroutines dependency

There is no `openrate_stream` — openrate answers from a snapshot it already
holds, so there is no incremental operation to stream — and there is no `Flow`
here.

llmux's Kotlin SDK does depend on `kotlinx-coroutines-core`, because chat
streaming genuinely needs `Flow`. **This SDK depends on nothing but
`kotlin-stdlib`**, because putting `suspend` in front of four synchronous calls
would buy a jar in everybody's build in exchange for a keyword. If you are
calling from a coroutine, wrap the call in `Dispatchers.IO` at the call site —
one line, no dependency.

That is the whole justification, and it is here because this repo's standard is
that a dependency has to be argued for.

---

## The JVM and Go's signal handlers

Kotlin runs on the JVM, so this applies unchanged. It is measured, not asserted:
the diff, HotSpot's `-Xcheck:jni` audit, the `libjsig` result and the verdict
are in [`sdks/java/README.md`](../java/README.md#the-jvm-and-gos-signal-handlers),
and the probe that produces them is `sdks/java/signal-probe.sh`.

Short version: loading `libopenrate` replaces the JVM's `SIGSEGV`, `SIGBUS`,
`SIGFPE`, `SIGPIPE` and `SIGURG` handlers and adds `SA_ONSTACK` to three more.
`SIGPROF` is untouched, so profiling is unaffected. Both runtimes keep working
because Go chains. HotSpot still reports its handlers as modified and tells you
to preload `libjsig`, which fixes it — but that is a flag on the **java launch
command**, which a library cannot add to a process that already started.

The numbers are identical to llmux's, because this is a property of
`-buildmode=c-shared` rather than of either product.

## The other costs of the direct path

Full detail in [`ffi/README.md`](../../ffi/README.md), Java-specific treatment
in [`sdks/java/README.md`](../java/README.md#the-costs-of-the-direct-path).

1. **The Go runtime is in your process** — see above.
2. **Not fork-safe.** The JVM is a mild case: no pre-forking, and
   `ProcessBuilder` is safe because it `posix_spawn`s or `fork`+`exec`s. The
   victims are a JNI/FFM call to bare `fork(2)`, and a supervisor that forks the
   JVM after the library loads.
3. **~6.7 MB of shared library** on darwin/arm64.
4. **One platform.** See below.
5. **Latency is not the reason.** The zero-network engine is.

## Platforms (direct path only)

openrate's coverage is **narrower than llmux's** — do not read one matrix for
both:

| target | status |
|---|---|
| darwin/arm64 | **built, smoke-tested, benchmarked.** Everything here ran on it. |
| darwin/amd64 | **built but NEVER EXECUTED.** |
| linux/amd64 | not built locally; a CI job exists and has never run. |
| **linux/arm64** | **built nowhere** (llmux has a tested one; openrate does not). |
| **windows/amd64** | **built nowhere. No DLL exists.** |

On Windows, use the sidecar — an ordinary Go binary.

## Build

There is **no `build.gradle.kts`**, on purpose. Nothing in this repo runs
Gradle, so a build file would be an unexecuted claim about how the module
builds, and a check nobody runs is worse than no check. `run-examples.sh` drives
`kotlinc` directly and is run for real.

## Toolchain this was built and run on

- OpenJDK **26.0.2** (Homebrew), darwin/arm64
- Kotlin **2.4.10** (`kotlinc-jvm`), `-jvm-target 22`
- Go **1.25.12**, openrate **0.1.2**

`-jvm-target 22` is a floor, not a preference: `org.vulos.openrate.OpenRateDirect`
is a Java 22 class file and kotlinc must be able to read it.

## Layout

```
sdks/kotlin/
  src/main/kotlin/org/vulos/openrate/kotlin/Direct.kt    OpenRate.engine(), OpenRateEngine, OpenRateRefresher
  src/main/kotlin/org/vulos/openrate/kotlin/Sidecar.kt   OpenRateSidecar
  examples/DirectRates.kt      runnable — offline unless OPENRATE_ALLOW_NETWORK=1
  examples/SidecarRates.kt     runnable — live rates, needs a network
  run-examples.sh
```
