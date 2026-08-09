# openrate (Elixir)

Use openrate **locally** from Elixir. The package starts the `openrate` binary
on a loopback port via an Erlang `Port` (managed by a singleton GenServer,
`Openrate.Sidecar`) and answers questions against it.

```elixir
{:ok, r}     = Openrate.convert("USD", "ZAR", 100)   # r["result"] => 1842.0
{:ok, rates} = Openrate.rates("ZAR")                 # the all-pairs snapshot
{:ok, meta}  = Openrate.meta()                       # sources, freshness, currencies
{:ok, base}  = Openrate.base_url()                   # "http://127.0.0.1:<port>"
```

The sidecar starts lazily on first use, is reused (one GenServer process), and
is terminated when the GenServer stops — including BEAM shutdown, which closes
the Port and reaps the child.

> Why a `Port` and not `System.cmd/3`: `System.cmd/3` blocks until the process
> exits, which is wrong for a long-lived sidecar. A `Port` gives a non-blocking,
> supervised handle with exit notifications and automatic teardown.

**No dependencies.** Not even `:inets` — `:httpc` reaches for `:ssl` and
`:public_key` defaults before it will answer a plain-http request, so
`Openrate.HTTP` is a thirty-line `:gen_tcp` GET instead. Every openrate API call
is a GET against loopback; point a real HTTP client at `Openrate.base_url/0` if
you want pooling or TLS.

Two runnable examples:

```sh
cd sdks/elixir
mix run examples/sidecar_convert.exs   # convert, cross-rate, snapshot, error, crash isolation
mix run examples/sidecar_rates.exs     # 100 concurrent conversions, cancellation, timeouts
```

```
sidecar : http://127.0.0.1:54766
meta    : base ZAR, 30 currencies from 1 source(s)
source  : ecb        29 edges
convert : 100 EUR = 115.35 USD (rate 1.1535, 1 hop, ecb)
crossed : 100 EUR = 1871.36 ZAR via EUR→ZAR
rates   : base ZAR, 29 currencies (AUD, BRL, CAD, CHF, CNY, …)
error   : HTTP 404 unknown or unreachable currency pair
isolate : a worker died (:killed); the VM and the sidecar are fine
          openrate still answering after that
stopped : ok
```

```
fanout  : 100/100 conversions across 100 processes in 26 ms
cancel  : killed a busy caller mid-flight; the VM did not notice
timeout : a 100 ms deadline was enforced: true
```

## Binary resolution

1. `OPENRATE_BINARY` env var
2. bundled `priv/bin/openrate` (`priv/bin/openrate.exe` on Windows)
3. `openrate` on `PATH`

```sh
go build -o sdks/elixir/priv/bin/openrate ./cmd/openrate
```

## Options

`Openrate.start/1` takes `:port`, `:base` (default presentation currency),
`:sources` (comma-separated: `ecb`, `coinbase`, `luno`, `sarb`, …), `:refresh`
(a Go duration, e.g. `"1h"`), `:ui` (default `false`), `:ratelimit`, `:env`,
`:timeout`.

`:ratelimit` **defaults to 0 here, and the binary's own default is 120** API
requests a minute per IP. That limit is anti-scraping for a public deployment
and wrong for a loopback sidecar with exactly one client: it is small enough
that the SDK's own startup health polling could exhaust it and hand your first
real call an HTTP 429, which is how this was found. Pass `ratelimit: 120` to put
it back — `examples/sidecar_rates.exs` then reports about 58/100 on its
hundred-request fan-out. Either way it is a property of `openrate serve`, not of
the library; an in-process engine has no equivalent, in either direction.

---

## There is no direct (in-process) mode for Elixir, and that is the recommendation

openrate ships a C ABI (`ffi/include/openrate.h`) so hosts can run the engine
inside their own process. Every other SDK binds it. **Elixir does not,
deliberately.** This is the reasoning, not an apology — if it stops holding, the
ABI is right there.

### There is no FFI in OTP; the choice is between four things

Erlang/OTP has no `ctypes`, no `fiddle`, no `FFI::cdef`. To call
`openrate_call()` from Elixir the options are:

| mechanism | what it costs | verdict |
| --- | --- | --- |
| **NIF** | C you write, compile and ship per platform; runs on a scheduler thread; a crash takes the whole VM | the only true in-process option, and the one this rejects |
| **Linked-in port driver** (`:erl_ddll`) | same C, same address space, same blast radius, an older API | strictly worse than a NIF |
| **Port to a C program that dlopens libopenrate** | safe, but it is a second OS process — the exact thing in-process mode exists to avoid — and now you maintain a C program too | worse than the sidecar in every dimension |
| **Managed sidecar** (`Openrate.Sidecar`) | a second OS process, supervised by a Port, reaped by the VM | **what this package does** |

The third row is the one worth sitting with. The argument for the C ABI is *no
second process, no port, no loopback surface*. Every safe way to reach it from
the BEAM reintroduces a second process. So the real choice is "a NIF" or "the
sidecar", and the NIF has to earn it.

### Why a NIF does not earn it

**1. A crash stops being local.** "Let it crash" works because a process dying
is contained. A segfault in a NIF is not a process dying; it is the VM dying,
taking every unrelated supervision tree with it. `examples/sidecar_convert.exs`
kills a worker and prints that openrate is still answering. That is not a
property a NIF can offer.

**2. A refresh cannot be preempted, killed, or timed out.** The BEAM's scheduler
is preemptive by reduction counting; native code inside a NIF is not.
`openrate_call(refresher, "refresh")` waits on central-bank endpoints — the SARB
host is slow enough that the library's own default fetch timeout is 50 seconds.
Inside a NIF that is a scheduler thread nothing can reach: `Process.exit(pid,
:kill)` and `Task.await/2` timeouts do not apply to it.
`examples/sidecar_rates.exs` demonstrates both working over the sidecar.

**3. The scheduler budget is ~1 ms, and a fetch is four orders of magnitude over
it.** ERTS expects a NIF to return within about a millisecond. A refresh has to
be a dirty NIF, and dirty schedulers are a small fixed pool — on this machine
(OTP 29, 8 cores) `erlang:system_info/1` reports 8 normal schedulers, 8
dirty-CPU, and **10 dirty-IO**. A dirty-IO NIF holds one of those ten for the
whole fetch, in a runtime whose defining property is that you can have a million
processes waiting on a million things. The fan-out above did 100 concurrent
conversions in 26 ms with no pool to exhaust.

**4. Two runtimes in one address space.** The BEAM has a preemptive scheduler
and per-process GC; the Go runtime has its own scheduler, a global GC, and
handlers for `SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPROF` and `SIGURG` (which Go
sends to its own threads constantly for asynchronous preemption). ERTS installs
its own, including for crash dumps. Both are well behaved alone; together they
are a support burden nobody signed up for, plus a 6.4 MB library mapped into
the VM.

**5. Nobody has to write, build, sign and ship C for five platforms.** A NIF
needs a compiler on the user's machine or a precompiled artifact per target, and
today `libopenrate` exists for **darwin/arm64** only in any tested form — see
[Platforms](#platforms). The `openrate` binary the sidecar spawns has no such
gap.

**6. There is no latency argument.** Measured for the sibling ABI (llmux, same
shape): the boundary is ~4 µs in-process against ~46 µs over loopback. A
currency conversion is a graph lookup and a multiplication; the fan-out above
averaged well under a millisecond each including HTTP. Nobody should trade the
BEAM's isolation for 40 µs.

**7. There is no `openrate_stream`, so the usual counter-argument is absent
too.** For llmux, in-process buys you a token callback with no SSE framing in
between; that is at least an argument worth weighing. openrate answers from a
snapshot it already holds and has no streaming half at all, so the C ABI's
advantage over a loopback GET is smaller here than anywhere.

### The one thing the sidecar genuinely cannot do

Direct mode has a capability the sidecar does not: **an engine with no refresher
provably never opens a socket.** `openrate_new()` builds an engine that starts
no thread, reads no environment variable and sends no packet, and the engine
handle *refuses* `"refresh"` — enforced at the ABI, not by convention. Feed it
with `"load"` and you have currency conversion in a process that cannot reach
the network.

`openrate serve` is the opposite: it is the program whose job is to fetch. If
"this must not call out" is a requirement you have to defend, that argues for
in-process — and on the BEAM the honest way to get it is **not** a NIF but:

- **Write that component in Go.** `github.com/vul-os/openrate` is a library; a
  Go service imports `openrate.NewEngine` directly with no C ABI in the middle,
  and can simply never construct a `Refresher`.
- **Or keep the sidecar and constrain it at the boundary** — run it with
  `-sources` you trust, on a host with egress rules, and let the OS enforce what
  the process model cannot.

If you build a NIF anyway, do it in your own application rather than here, use
`ERL_NIF_DIRTY_JOB_IO_BOUND` for anything touching a refresher, never load the
library in a process that will `fork()`, and read `ffi/README.md` first.

## Platforms

Prebuilt `libopenrate` today, for the record — this is what a NIF would have to
ship against:

| target | status |
| --- | --- |
| darwin/arm64 | **built, smoke-tested and benchmarked.** 6,682,274 bytes. |
| darwin/amd64 | built (7,120,680 bytes) but **never executed**. |
| linux/amd64 | **not built locally.** A CI job exists and has never run. |
| linux/arm64 | **built nowhere.** |
| windows/amd64 | **built nowhere.** No `.dll` exists. |

Note this is **not** llmux's matrix: llmux has a tested linux/arm64 build and
openrate does not. Most BEAM deployments are linux/amd64, where openrate has no
build at all today. The `openrate` binary has no such gap.

## Honest notes that apply to the sidecar too

1. **It is a second OS process** with its own memory and lifecycle. The `Port`
   reaps it when the GenServer stops or the VM shuts down; a `kill -9` of the VM
   leaves that to the OS.
2. **It listens on a loopback port.** `Openrate.Sidecar` picks a free one, but it
   is a socket on the box that anything running as any user can reach.
3. **It fetches.** A sidecar always has a refresher; that is what the binary is.
   See [the one thing the sidecar cannot do](#the-one-thing-the-sidecar-genuinely-cannot-do).
4. **Rate limiting, CORS and the read-only contract live in the HTTP shell** —
   they are properties of the mode you are using, not something you gave up.
