# openrate (.NET)

Two ways to run openrate from C#, both supported:

| | type | what it is | recommended? |
|---|---|---|---|
| **Sidecar** | `OpenRate.Sidecar` | spawns `openrate` on `127.0.0.1`, talks HTTP | **yes — the default for .NET** |
| **Direct** | `OpenRate.Direct` → `OpenRateEngine` | loads `libopenrate` into this process | when you need the zero-network guarantee and are not shipping to Windows |

**For .NET the sidecar is the recommended default, and the deciding reason is
platform coverage.** openrate's shared library has been built and executed on
**exactly one target** — darwin/arm64 — and there is no Windows DLL. .NET has a
very large Windows install base; a direct-mode dependency would simply not load
for a large fraction of the people who took it.

**The one thing only the direct path can give you, and it is not speed:** an
engine that provably sends no packets.

```sh
sdks/dotnet/run-examples.sh            # both
sdks/dotnet/run-examples.sh direct     # offline by default
sdks/dotnet/run-examples.sh sidecar    # needs a network
```

---

## Sidecar — `OpenRate.Sidecar`

```csharp
using OpenRate;

using var rates = Sidecar.Start(new Sidecar.Options
{
    Base = "ZAR",
    Sources = "ecb,coinbase",
});

string converted = rates.Convert("USD", "ZAR", 100);
string book      = rates.Rates("EUR");
string meta      = rates.Meta();
```

`using` stops the child process on every path out. No native library, no unsafe
code, no platform matrix — it works on Windows.

Unlike llmux's .NET SDK this is **not a process-wide singleton**: you get an
instance you own and dispose. Several servers with different bases or source
sets is a normal thing to want from a rates service, and a static singleton
would forbid it.

Worked example: [`examples/SidecarRates.cs`](examples/SidecarRates.cs).

### `Start()` waits for rates, not just for a listening socket

The one non-obvious thing in the wrapper, and it is here because the example
caught it.

**`/healthz` is a liveness probe.** openrate answers it `200` the moment it
binds, while its first source fetch is still in flight. Every conversion in that
window comes back:

```json
{"error":"unknown or unreachable currency pair"}
```

which reads as *you passed a bad currency code*, not as *the server is not ready
yet*. The first version of this example printed exactly that for every call and
still exited 0.

Readiness is its own endpoint. `Sidecar.Start()` waits for `/healthz`, then
polls **`GET /readyz`** until it answers `200` — which it does once the snapshot
actually holds currencies. `Options.WaitForRates = false` turns that second wait
off; `Options.RatesTimeout` bounds it.

Until then `/readyz` is a `503` carrying the diagnosis, so a timeout here names
the source and quotes what it said rather than shrugging:

```
openrate has no rates after 5s: no rates yet: no source has returned a usable
quote (ecb: Get "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml":
proxyconnect tcp: dial tcp 127.0.0.1:1: connect: connection refused)
```

The poll runs on a fixed 150 ms interval. It can afford to: openrate's
anti-scraping limiter applies to `/api/` paths only, and `/readyz` is not one,
so waiting cannot spend the budget the first conversion needs. (An earlier
version of this SDK polled `/api/v1/meta`, which *is* under `/api/`.)

### The sidecar runs with the rate limiter off

`Start()` passes `OPENRATE_RATELIMIT=0` to the child. It listens on loopback and
serves exactly one client — this process. The limiter is anti-scraping for a
public deployment and there is no stranger here to throttle, while a legitimate
batch of conversions would sail past the 120/min default and take a `429` from
your own sidecar. Put it back with
`Options.Env = new Dictionary<string, string> { ["OPENRATE_RATELIMIT"] = "120" }`.

### The sidecar fetches, and that is not configurable

`openrate serve` starts its refresher at startup. Standing one up means outbound
requests. If "no packets unless I ask" is the property you need, that is the
direct path's engine, and it is a guarantee rather than a setting.

### Binary resolution

1. `OPENRATE_BINARY`
2. `bin/openrate` next to the assembly (`bin/openrate.exe` on Windows)
3. `openrate` on `PATH`

```sh
go build -o sdks/dotnet/bin/openrate ./cmd/openrate
```

---

## Direct — `OpenRate.Direct`

openrate inside your process, through the C ABI in
[`ffi/README.md`](../../ffi/README.md) and
[`ffi/include/openrate.h`](../../ffi/include/openrate.h).

### The engine/refresher split is enforced at the ABI

```csharp
using var engine = Direct.OpenEngine(baseCurrency: "ZAR");

// Zero network. Not "we try not to" — there is no code path.
engine.Load(myRatesJson);
string converted = engine.Convert("USD", "ZAR", 100);

// Fetching is a SEPARATE object with its own handle.
using (var refresher = engine.NewRefresher(sources: "ecb"))
{
    refresher.Refresh(timeoutMs: 30_000);    // this opens sockets
}
```

- `OpenEngine` starts no thread, opens no socket, reads no environment variable
  and sends no packet.
- `NewRefresher` is the only thing that can open a socket — **and constructing
  one still does not**.
- An engine **refuses** refresher methods. From the example's real output:

  ```
  engine refuses "refresh": openrate_call(refresh): openrate: unknown engine method
      "refresh" (have: convert, rates, meta, load)
  ```

- `Meta()` on an unrefreshed engine reports `"sources": []` — the zero-network
  claim in the library's own words.

| handle | methods |
|---|---|
| `OpenRateEngine` | `Convert`, `Rates`, `Meta`, `Load` |
| `OpenRateRefresher` | `Status`, `Refresh`, `Start`, `Stop`, `Ready` |

### There is no streaming, and therefore no `IAsyncEnumerable`

No `openrate_stream`. openrate answers from a snapshot it already holds, so
there is no incremental operation to stream. llmux's .NET SDK, which binds the
same ABI shape, does expose `IAsyncEnumerable<string>` for `llmux_stream`. The
absence here is deliberate and stated rather than left to be noticed.

### `LibraryImport`, `out IntPtr`, and one thing this file got wrong

The binding uses **`LibraryImport`** (source-generated, .NET 7+) rather than
`DllImport`: compile-time stubs, NativeAOT-compatible, and every string across
the boundary declared rather than guessed.

**Every function returning a string returns `IntPtr`, never `string`.** A
`string` return compiles, runs, and leaks: the marshaller copies the C string
and has no idea the original must go back to `openrate_free`. Results are copied
with `Marshal.PtrToStringUTF8` and freed in a `finally`; error strings are freed
after becoming exceptions; and the `char** err` out-parameter is drained **on
the success path too**, so a message set alongside a success cannot leak.

Because openrate has no callback, `char**` is expressed as `out IntPtr` and
**nothing in `OpenRateDirect.cs` uses a pointer, a function pointer or a fixed
buffer**. llmux's .NET binding does, because `llmux_stream` takes a function
pointer.

The correction: the project still sets `AllowUnsafeBlocks`. An earlier draft of
this file claimed it did not need to, on the reasoning above. That is wrong —
**`LibraryImport` requires it unconditionally**, because the generator emits
pointer-using stubs regardless of the declared signature, and says so as
`SYSLIB1062`. The compiler caught it; the claim is recorded here because a
plausible-sounding one that nobody built is exactly the kind that survives
review.

### `SafeHandle`

Engines and refreshers are both `OpenRateSafeHandle`, so:

- `using` closes them deterministically, including on the exception path;
- if you forget, the base class's finaliser closes them rather than never;
- `DangerousAddRef`/`DangerousRelease` around each call means a concurrent
  `Dispose` cannot close a handle mid-flight;
- double `Dispose` is safe, and calling after `Dispose` is a clean
  `OpenRateException`, not a crash.

And it is checkable rather than claimed. `OpenHandles()` exposes
`openrate_open_handles()`; from the example's real output:

```
open handles after creating the engine: 1
open handles with a refresher: 2
open handles after the refresher closed: 1
handle accounting: 1 open, 0 after dispose
after dispose: this handle is closed
```

Use after dispose is a clean error because handle numbers are **retired, not
recycled** — a stale handle can never reach somebody else's object.

---

## The costs of the direct path

Full detail in [`ffi/README.md`](../../ffi/README.md).

1. **The Go runtime lives in your process** — GC, scheduler, signal handlers. It
   replaces `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPIPE` and `SIGURG`, chaining to
   whatever was installed, and leaves `SIGPROF` alone. **This was measured in
   detail against HotSpot — see
   [`sdks/java/README.md`](../java/README.md#the-jvm-and-gos-signal-handlers) —
   and NOT against CoreCLR.** The Java findings are suggestive, not
   transferable: CoreCLR uses `SIGSEGV` too (null checks, write barriers) and
   `SIGRTMIN`-range signals for GC suspension on Linux, and its chaining is
   different code. The .NET examples here ran clean, repeatedly, on
   darwin/arm64. That is evidence, not proof. If you adopt the direct path,
   test it under load on your platform.
2. **Not fork-safe.** After `fork()` without `exec()` the Go runtime in the
   child is broken. .NET gets off lightly: no fork-based worker model, and
   `Process.Start` uses `fork`+`exec`. The victims are a P/Invoke to bare
   `fork(2)` and a supervisor that forks the process after the library loads.
3. **~6.7 MB of shared library** on darwin/arm64. Two builds of the same source
   here produced 6,682,274 and 6,700,448 bytes, so treat it as "about 6.7 MB"
   rather than a constant.
4. **One executed platform, and it is not Windows.** See below.
5. **Latency is not the reason to embed.** The zero-network engine is.

## Platforms (direct path only)

openrate's coverage is **narrower than llmux's** — do not read one matrix for
both products:

| target | status |
|---|---|
| darwin/arm64 | **built, smoke-tested, benchmarked.** Everything here ran on it. |
| darwin/amd64 | **built but NEVER EXECUTED.** Not a tested target. |
| linux/amd64 | not built locally; a CI job exists and has never run. |
| **linux/arm64** | **built nowhere** (llmux has a tested one; openrate does not). |
| **windows/amd64** | **built nowhere. No DLL exists.** |

**There is no Windows DLL**, so this file gives no Windows install instructions
for the direct path — there is nothing to install. `Direct.FindLibrary()` says
so in its error message rather than throwing a bare `DllNotFoundException`.

The **sidecar** has no such matrix — it is an ordinary Go binary.

## Toolchain this was built and run on

- .NET SDK **10.0.302**, targeting **net8.0**
- Go **1.25.12**, openrate **0.1.2**
- darwin/arm64

The examples project sets `RollForward=LatestMajor` so a net8.0 build starts on
a machine that only has the .NET 10 runtime, which is the case here.

## Layout

```
sdks/dotnet/
  OpenRate.cs                 sidecar (no unsafe, no native)
  OpenRateDirect.cs           direct: LibraryImport + SafeHandle, engine/refresher
  OpenRate.csproj
  examples/DirectRates.cs     runnable — offline unless OPENRATE_ALLOW_NETWORK=1
  examples/SidecarRates.cs    runnable — live rates, needs a network
  examples/Program.cs         picks one
  examples/Examples.csproj
  run-examples.sh
```
