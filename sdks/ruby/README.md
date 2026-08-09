# openrate (Ruby)

Two ways to use openrate from Ruby, both supported:

| mode | what it is | file |
| --- | --- | --- |
| **Sidecar** | the gem spawns the `openrate` binary on a loopback port, waits for `/readyz`, and shuts it down at exit | `lib/openrate.rb` |
| **Direct** | `libopenrate` loaded into the Ruby process with `fiddle` — no child process, no port | `lib/openrate/ffi.rb` |

**Which one to pick.** Ruby's FFI story here is good: `fiddle` is stdlib, and
openrate's ABI has no callback at all — there is no `openrate_stream` — so the
GVL and thread-affinity questions that a streaming API would raise simply do not
arise. The choice comes down to two things:

| your situation | pick |
| --- | --- |
| Unicorn; Puma in **clustered** mode; Passenger; Resque; Spring; anything with `preload_app` — **and you need live rates** | **sidecar** |
| Puma **single** mode, Falcon, Sidekiq, rake tasks, CLI tools, daemons | either |
| **"this process must never open a socket"** is a requirement you have to defend | **direct**, engine-only — see below. The sidecar cannot offer this. |
| deploying to **linux/amd64** | **sidecar**, today — see [platforms](#platforms) |

## Sidecar

```ruby
require "openrate"

Openrate.convert("USD", "ZAR", 100)   # => {"from"=>"USD", "result"=>1842.0, ...}
Openrate.rates("ZAR")                 # the all-pairs snapshot
Openrate.meta                         # sources, freshness, currency list
Openrate.base_url                     # "http://127.0.0.1:<port>"
Openrate.ready?                       # GET /readyz — has it got rates yet?
```

The sidecar starts lazily on first use, is reused (singleton), and is terminated
at process exit. Runnable end to end:

```sh
ruby sdks/ruby/examples/sidecar_convert.rb
```

```
sidecar : http://127.0.0.1:57477
probes  : healthy? true (up), ready? true (has rates)
meta    : base ZAR, 30 currencies from 1 source(s)
source  : ecb        29 edges
convert : 100 EUR = 115.3500 USD (rate 1.153500, 1 hop, ecb)
crossed : 100 EUR = 1871.36 ZAR via EUR→ZAR
rates   : base ZAR, 29 currencies (AUD, BRL, CAD, CHF, CNY, …)
error   : GET /convert: HTTP 404: unknown or unreachable currency pair
stopped : ok
```

A sidecar always has a refresher — `openrate serve` is the program whose job is
to fetch. `Openrate.start(sources: "ecb")` narrows which sources it uses; it
cannot make it fetch nothing.

### Up is not the same as ready

`Openrate.start` returns when the child is **ready**, not when it is listening.
`/healthz` answers 200 the instant the listener binds, with an empty book until
the first fetch lands — wait on that and every conversion comes back
`unknown or unreachable currency pair`, which reads like a bad currency code and
is not. So the gem polls **`/readyz`**, which is 503 until the snapshot has
currencies in it and says why:

```
could not start the sidecar: openrate has no rates after 8.0s: no rates yet: no
source has returned a usable quote (ecb: Get "https://www.ecb.europa.eu/…":
proxyconnect tcp: dial tcp 127.0.0.1:1: connect: connection refused)
```

`timeout:` therefore has to cover the first fetch (default 30s). `Openrate.ready?`
and `Openrate.wait_ready(timeout: 30.0)` ask the same question of a server you
did not start; `Openrate.healthy?` is liveness only.

The poll is a fixed 150 ms with no backoff, which is fine because `/readyz` sits
outside `/api/` and the per-IP limiter never sees it. And readiness means *some*
rates, not *all* sources: with several sources racing, the book flips ready when
the first one lands, so a pair a slower source would have supplied can still be
missing. Name one source if you need to depend on it.

One default worth knowing about: the binary rate-limits the JSON API to **120
requests a minute per IP**, which is anti-scraping for a public deployment and
wrong for a loopback sidecar with exactly one client. There is no stranger here
to throttle, and the budget is small enough that a legitimate batch of
conversions can exhaust it and hand a later real call an HTTP 429.
(Readiness polling is not the reason: only /api/ paths are limited, and neither
/healthz nor /readyz is under /api/.) `Openrate.start` therefore
passes `OPENRATE_RATELIMIT=0`; pass `ratelimit: 120` to put it back.

### Binary resolution

1. `OPENRATE_BINARY` env var
2. bundled `bin/openrate` (`bin/openrate.exe` on Windows)
3. `openrate` on `PATH`

```sh
go build -o sdks/ruby/bin/openrate ./cmd/openrate
```

---

## Direct

```ruby
require "openrate/ffi"

Openrate::Ffi.open(config: { base: "ZAR" }) do |engine|   # closes however the block exits
  engine.call("load", edges: [
    { from: "USD", to: "ZAR", rate: 18.42, source: "my-treasury-desk" }
  ])
  puts engine.convert("USD", "ZAR", 100)["result"]        # => 1842.0
end
```

Requests and responses are the **same JSON the HTTP API publishes**, so moving a
call site between the two modes is a transport change, not a rewrite.

```sh
ruby sdks/ruby/examples/direct_convert.rb                    # zero packets
OPENRATE_NETWORK=1 ruby sdks/ruby/examples/direct_convert.rb # plus a live ECB fetch
```

```
library : /Users/…/openrate/dist/ffi/libopenrate-darwin-arm64.dylib
abi     : 0.1.2
handles : 1 open
empty   : openrate_call(convert): openrate: convert USD->ZAR: unknown or unreachable currency pair
loaded  : EUR, USD, ZAR
convert : 100 USD = 1842.00 ZAR (rate 18.4200, 1 hop)
crossed : 100 EUR = 2001.15 ZAR via EUR→USD→ZAR
rates   : base ZAR, 2 currencies
meta    : default base ZAR, sources 0 (an engine nobody refreshes has none)
refuses : openrate_call(refresh): openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
network : skipped (set OPENRATE_NETWORK=1 to fetch from ECB)
closed  : ok, 0 handles left open by the block above
```

### The engine/refresher split is the headline

This is enforced **at the ABI**, not by convention:

- `Openrate::Ffi.new` calls `openrate_new`, which builds an **engine**. It
  starts no thread, opens no socket, reads no environment variable and sends no
  packet. It answers from the snapshot it holds and says *"unknown or
  unreachable currency pair"* until something gives it one.
- An engine handle **refuses** `"refresh"` — the `refuses:` line above is the
  example proving it, not asserting it. There is no flag and no code path that
  turns an engine into a fetcher.
- Fetching needs `openrate_refresher_new`: a **separate construction with its
  own handle**, `engine.refresher(config: …)` here. Even building that opens
  nothing; sockets start at `refresh` or `start`.
- `"load"` feeds an engine rates you obtained yourself. It has **no HTTP
  counterpart**, because the server is read-only — a capability only direct mode
  has.
- `engine.open_handles` reports the library's live handle count, so a test can
  assert it closed what it opened. `Ffi.open` and `engine.close` are idempotent,
  and closing an engine releases every refresher over it, so one `ensure` covers
  both.

### `fiddle`, not the `ffi` gem

`fiddle` ships with Ruby. Adding `ffi` would put a native-extension gem into the
dependency graph of a gem whose entire pitch is that it is thin, and it would
buy nothing here: openrate's ABI is seven plain functions with no callback, so
`dlopen` plus a calling convention is the whole requirement.

`Fiddle::Function` releases the GVL by default (`need_gvl:` defaults to `false`,
and `ext/fiddle/function.c` then routes the call through
`rb_thread_call_without_gvl`). The SDK keeps that default, so a `refresh`
waiting on a central bank does not stop the rest of your application. There is
no closure anywhere in this binding, so the reacquire-the-GVL question that
`llmux`'s streaming callback raises does not come up.

Library resolution is `OPENRATE_LIBRARY`, then `sdks/ruby/lib/`, then
`dist/ffi/` in a checkout (note the naming: `libopenrate-<goos>-<goarch>.dylib`,
not a per-target directory), then the bare soname.

### The fork problem

After `fork()` without `exec()`, the Go runtime in the child is broken.
`examples/fork_probe.rb` makes it reproducible. On Ruby 4.0.5 (arm64-darwin24)
against openrate 0.1.2:

```
load=before method=convert -> child exited 0
load=before method=rates   -> child exited 0
load=before method=meta    -> child exited 0
load=before method=refresh -> child HUNG (SIGKILLed after 15s)
load=after  method=refresh -> child CRASHED, signal 6 (Go dumped above)
load=after  method=convert -> child exited 0
```

`before` means the library was loaded before the fork, which is what
`preload_app true` and Puma's `preload_app!` do for you. Three findings:

**1. Every engine method survives a fork.** `convert`, `rates` and `meta` are
arithmetic over a snapshot already in memory — no sockets, no timers, nothing
the Go scheduler has to wake. So a forked Unicorn worker doing conversions
genuinely works, and a boot check built on it reports green.

**2. Only the refresher hangs.** It is the one handle that opens a socket, so it
is the one that meets the broken netpoller.

**3. On macOS, loading after the fork does not rescue TLS.** The usual
mitigation — build the `Ffi` in `after_fork`, never at boot — is the fifth line,
and it crashes. Ruby's dump names the cause exactly:

```
objc[42122]: +[NSNumber initialize] may have been in progress in another thread
when fork() was called. We cannot safely call it or ignore it in the fork()
child process. Crashing instead.
```

Underneath, the Go stack is in `crypto/x509/internal/macos.SecTrustEvaluateWithError`
→ `crypto/x509.(*Certificate).systemVerify` → `crypto/tls.(*Conn).verifyServerCertificate`.
That is Apple's Security framework, not openrate and not Go: `SecTrust*` and the
Objective-C runtime are fork-unsafe once the parent has touched them, and only
`exec()` clears it. openrate fetches over HTTPS, so certificate verification is
on the path of every refresh. **Linux, where Go verifies certificates without
calling into the system, has not been tested here** — that is an expectation,
not a measurement, and this file only reports measurements.

Practical rule for Ruby:

| host | engine-only (convert/rates/meta/load) | with a refresher |
| --- | --- | --- |
| Unicorn, clustered Puma, Passenger, Resque, Spring | **fine**, measured | **use the sidecar** |
| Puma single, Falcon, Sidekiq, rake, CLI | fine | fine |

### Platforms

Prebuilt `libopenrate` today:

| target | status |
| --- | --- |
| darwin/arm64 | **built, smoke-tested and benchmarked.** 6,682,274 bytes. |
| darwin/amd64 | built (7,120,680 bytes) but **never executed** — this machine cannot run it. |
| linux/amd64 | **not built locally.** A CI job exists and has never run. |
| linux/arm64 | **built nowhere.** |
| windows/amd64 | **built nowhere.** No `.dll` exists. |

openrate's coverage is **not** llmux's: llmux has a tested linux/arm64 build and
openrate does not. Most Ruby is deployed on linux/amd64, which for openrate has
no build at all today. The `openrate` binary the sidecar spawns has no such gap.

### The rest of the honest list

1. **The Go runtime lives in your process** — its GC, its scheduler, and its
   signal handlers (`SIGSEGV`, `SIGBUS`, `SIGFPE`, `SIGPROF`, …). Ruby installs
   its own and Go chains to a pre-existing handler in most cases; "most" is the
   honest word, and a `SIGPROF`-based sampling profiler (`stackprof` in `:wall`
   mode) is the shape of thing to watch.
2. **Not fork-safe** — see above.
3. **The shared library is 6–7 MB**, per process that loads it.
4. **Prebuilt binaries cover darwin only, and one of the two has never been
   run** — see [Platforms](#platforms).
5. **No rate limiting, no CORS, no auth on the direct boundary.** Those live in
   `openrate serve`'s HTTP shell. In-process you are inside the trust boundary
   and the library does not pretend otherwise.
6. **One deliberate behavioural difference from HTTP:** `rates` with an unknown
   base is an *error* through the C ABI, where `GET /api/v1/rates` answers 200
   with an empty book. The library follows the Go API, not the endpoint.
7. **There is no `openrate_stream`,** deliberately. openrate answers from a
   snapshot it already holds, so there is nothing incremental to deliver.

## Examples

| file | mode | what it shows |
| --- | --- | --- |
| `examples/sidecar_convert.rb` | sidecar | spawn, wait for `/readyz`, convert, cross-rate, snapshot, HTTP error, guaranteed stop |
| `examples/direct_convert.rb` | direct | version probe, empty engine, `load`, convert, cross-rate, `rates`/`meta`, an engine refusing `refresh`, opt-in refresher, handle-count check |
| `examples/fork_probe.rb` | direct | reproduces the fork hazard, shows which methods hide it, and the macOS TLS crash |

Both main examples run with no API key. `direct_convert.rb` needs no network at
all unless you set `OPENRATE_NETWORK=1`; `sidecar_convert.rb` fetches from ECB.
