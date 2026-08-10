# openrate (PHP)

Two ways to use openrate from PHP, both supported:

| mode | what it is | file |
| --- | --- | --- |
| **Sidecar** *(recommended for PHP)* | the SDK spawns the `openrate` binary on a loopback port, waits for `/readyz`, and shuts it down at exit | `src/Openrate.php` |
| **Direct** | `libopenrate` loaded into the PHP process through ext-ffi — no child process, no port | `src/Ffi.php` |

**For PHP the sidecar is the right default, and this is not a hedge.** The
measurements are in [The fork problem](#the-fork-problem) below; the short
version is that php-fpm forks its workers and the Go runtime inside
`libopenrate` does not survive `fork()`. Naming the mode that fits is part of
the job, not a shortfall.

The one thing direct mode does that the sidecar cannot: an **engine** with no
refresher provably never opens a socket. If "this process must not reach the
network" is a requirement you have to defend, read
[The engine/refresher split](#the-enginerefresher-split-is-the-headline).

## Sidecar

```php
use Openrate\Openrate;

$r = Openrate::convert('USD', 'ZAR', 100);
echo $r['result'];                      // 1842.0

$rates = Openrate::rates('ZAR');        // the all-pairs snapshot
$meta  = Openrate::meta();              // sources, freshness, currency list
$base  = Openrate::baseUrl();           // http://127.0.0.1:<port>
Openrate::ready();                      // GET /readyz — has it got rates yet?
```

The sidecar starts lazily on first use, is reused (singleton), and is terminated
at process shutdown. Runnable end to end:

```sh
php sdks/php/examples/sidecar_convert.php
```

```
sidecar : http://127.0.0.1:59834
probes  : healthy yes (up), ready yes (has rates)
meta    : base ZAR, 30 currencies from 1 source(s)
source  : ecb        29 edges
convert : 100 EUR = 115.3500 USD (rate 1.153500, 1 hop, ecb)
crossed : 100 EUR = 1871.36 ZAR via EUR→ZAR
rates   : base ZAR, 29 currencies (AUD, BRL, CAD, CHF, CNY, …)
error   : GET /convert?from=XXX&to=YYY&amount=1: HTTP 404: unknown or unreachable currency pair
stopped : ok
```

A sidecar always has a refresher — `openrate serve` is the program whose job is
to fetch. `Openrate::start(['sources' => 'ecb'])` narrows which sources it uses;
it cannot make it fetch nothing.

### Up is not the same as ready

`Openrate::start()` returns when the child is **ready**, not when it is
listening. `/healthz` answers 200 the instant the listener binds, with an empty
book until the first fetch lands — wait on that and every conversion comes back
`unknown or unreachable currency pair`, which reads like a bad currency code and
is not. So the SDK polls **`/readyz`**, which is 503 until the snapshot has
currencies in it and says why:

```
could not start the sidecar: openrate has no rates after 8s: no rates yet: no
source has returned a usable quote (ecb: Get "https://www.ecb.europa.eu/…":
proxyconnect tcp: dial tcp 127.0.0.1:1: connect: connection refused)
```

`'timeout'` therefore has to cover the first fetch (default 30s).
`Openrate::ready()` and `Openrate::waitReadyFor(30.0)` ask the same question of a
server you did not start; `Openrate::healthy()` is liveness only.

The poll is a fixed 150 ms with no backoff, which is fine because `/readyz` sits
outside `/api/` and the limiter never sees it. And readiness means *some*
rates, not *all* sources: with several sources racing, the book flips ready when
the first one lands, so a pair a slower source would have supplied can still be
missing. Name one source if you need to depend on it.

One default worth knowing about: the binary rate-limits the JSON API to **120
requests a minute per client network prefix**, which is anti-scraping for a public deployment and
wrong for a loopback sidecar with exactly one client. There is no stranger here
to throttle, and the budget is small enough that a legitimate batch of
conversions can exhaust it and hand a later real call an HTTP 429.
(Readiness polling is not the reason: only /api/ paths are limited, and neither
/healthz nor /readyz is under /api/.) `Openrate::start()` therefore
passes `OPENRATE_RATELIMIT=0`; pass `['ratelimit' => 120]` to put it back.

### Binary resolution

1. `OPENRATE_BINARY` env var
2. bundled `bin/openrate` (`bin/openrate.exe` on Windows)
3. `openrate` on `PATH`

For local development, build it into the package's `bin/`:

```sh
go build -o sdks/php/bin/openrate ./cmd/openrate
```

---

## Direct

```php
use Openrate\Ffi;

$engine = new Ffi(['base' => 'ZAR', 'quiet' => true]);
try {
    $engine->call('load', ['edges' => [
        ['from' => 'USD', 'to' => 'ZAR', 'rate' => 18.42, 'source' => 'my-treasury-desk'],
    ]]);
    $r = $engine->convert('USD', 'ZAR', 100);
    echo $r['result'];                  // 1842
} finally {
    $engine->close();                   // PHP has no `using`; try/finally is the idiom
}
```

`Ffi::with($config, fn ($engine) => …)` wraps the same try/finally in a closure.
Requests and responses are the **same JSON the HTTP API publishes**, so moving a
call site between the two modes is a transport change, not a rewrite.

```sh
php sdks/php/examples/direct_convert.php                    # zero packets
OPENRATE_NETWORK=1 php sdks/php/examples/direct_convert.php # plus a live ECB fetch
```

```
library : /Users/…/openrate/dist/ffi/libopenrate-darwin-arm64.dylib
abi     : 0.1.2
handles : 1 open
empty   : openrate_call(convert): openrate: convert USD->ZAR: unknown or unreachable currency pair
loaded  : EUR, USD, ZAR
convert : 100 USD = 1842 ZAR (rate 18.4200, 1 hop)
crossed : 100 EUR = 2001.15 ZAR via EUR→USD→ZAR
rates   : base ZAR, 2 currencies
meta    : default base ZAR, sources 0 (an engine nobody refreshes has none)
refuses : openrate_call(refresh): openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
network : skipped (set OPENRATE_NETWORK=1 to fetch from ECB)
closed  : ok, 0 handles left open
```

### The engine/refresher split is the headline

This is enforced **at the ABI**, not by convention:

- `openrate_new()` builds an **engine**. It starts no thread, opens no socket,
  reads no environment variable and sends no packet. It answers from the
  snapshot it holds and says *"unknown or unreachable currency pair"* until
  something gives it one.
- An engine handle **refuses** `"refresh"` — the line above proves it, in the
  example's own output. There is no flag, no env var and no code path that turns
  an engine into a fetcher.
- Fetching needs `openrate_refresher_new()`: a **separate construction, with its
  own handle**. `$engine->refresher([...])` in this SDK. Building even that
  still opens nothing; sockets start at `refresh` or `start`.
- Feed an engine without a refresher using `"load"`, which takes rates you
  obtained yourself. It has no HTTP counterpart, because the server is
  read-only — so this is a capability only direct mode has.

Closing an engine also stops and releases every refresher built over it, so
closing in the "wrong" order cannot leak a running loop. One `finally` is enough.

### There is no `openrate_stream`

Deliberately. openrate answers from a snapshot it already holds, so there is no
incremental result to deliver and a callback entry point would be a promise with
nothing behind it. llmux, which shares this ABI shape, *does* define
`llmux_stream`, because token-by-token chat is its main event. Practically: the
PHP FFI callback machinery — and everything you would have to know about it —
does not come into openrate at all.

### You must enable the FFI extension

`ext-ffi` ships with PHP 7.4+ but is gated by the `ffi.enable` php.ini
directive, whose **default value is `preload`**:

| `ffi.enable` | CLI (`php script.php`) | php-fpm / mod_php / any other SAPI |
| --- | --- | --- |
| `preload` *(default)* | FFI works — CLI is exempt | `FFI\Exception: FFI API is restricted by "ffi.enable" configuration directive` |
| `false` | blocked everywhere | blocked everywhere |
| `true` | works | works — and PHP can now dlopen and call **any** native library |

Observed on PHP 8.5.9 (Homebrew, darwin/arm64), using the built-in server as a
stand-in for a non-CLI SAPI:

```
$ php -d ffi.enable=0 -r 'FFI::cdef("int puts(const char*);", null);'
FFI BLOCKED: FFI\Exception: FFI API is restricted by "ffi.enable" configuration directive

$ php -S 127.0.0.1:19182 ffigate.php          # ffi.enable=preload, the default
FFI BLOCKED: FFI\Exception: FFI API is restricted by "ffi.enable" configuration directive

$ php -d ffi.enable=1 -S 127.0.0.1:19183 ffigate.php
FFI OK
```

So a web deployment needs `ffi.enable=1` in `php.ini` — a global, unrestricted
native-code capability for every script the interpreter runs. Check what you
have:

```sh
php -r 'var_dump(extension_loaded("FFI"), ini_get("ffi.enable"));'
```

Also required: a `libopenrate` for your platform. Resolution order is
`OPENRATE_LIBRARY`, then `sdks/php/lib/`, then `dist/ffi/` in a checkout (note
the naming: `libopenrate-<goos>-<goarch>.dylib`, not a per-target directory),
then the bare soname. Build one with `scripts/build-ffi.sh`.

### The fork problem

After `fork()` without `exec()`, the Go runtime in the child is broken: its
threads did not come across. `examples/fork_probe.php` makes it reproducible. On
PHP 8.5.9 / darwin/arm64 against openrate 0.1.2:

```
load=before method=convert -> child exited 0
load=before method=rates   -> child exited 0
load=before method=meta    -> child exited 0
load=before method=refresh -> child HUNG (SIGKILLed after 15s)
load=after  method=refresh -> child CRASHED, signal 3 (Go dumped above)
load=after  method=convert -> child exited 0
```

`before` means the library was loaded before `pcntl_fork()`, which is what
php-fpm's master does for you. Three findings, and the second two are the
interesting ones:

**1. Every engine method survives a fork.** `convert`, `rates` and `meta` are
arithmetic over a snapshot already in memory — no sockets, no timers, nothing
the Go scheduler has to wake. So a forked worker doing conversions genuinely
does work, and a smoke test built on it reports green.

**2. Only the refresher hangs.** It is the one handle that opens a socket, so it
is the one that meets the broken netpoller. "It worked in my forked worker" is
not evidence of anything unless what you tried was a refresh.

**3. On macOS, loading after the fork does not rescue TLS.** The usual
mitigation — dlopen in the worker, never in the master — is the last line above,
and it **segfaults** rather than working:

```
SIGSEGV: segmentation violation
crypto/x509/internal/macos.SecTrustEvaluateWithError(...)
	crypto/x509/internal/macos/security.go:112
crypto/x509.(*Certificate).systemVerify(...)
	crypto/x509/root_darwin.go:62
crypto/tls.(*Conn).verifyServerCertificate(...)
```

That is Apple's Security framework, not openrate and not Go: `SecTrust*` is
fork-unsafe once the parent process has touched CoreFoundation, and only
`exec()` clears it. Since openrate fetches over HTTPS, certificate verification
is on the path of every refresh. **Linux, where Go verifies certificates without
calling into the system, has not been tested here** and probably behaves like
the "load after fork" mitigation is supposed to — but that is an expectation,
not a measurement, and this file only reports measurements.

Concrete PHP victims: **php-fpm** (any `pm` mode — workers are always forked
from the master), **mod_php** under Apache `prefork`/`event`, **`pcntl_fork()`**
in your own code, and Swoole / RoadRunner / FrankenPHP worker pools that fork.
`exec()`-based process managers are fine, because `exec` replaces the image.

### When direct mode *is* right for PHP

- Long-lived CLI processes: workers, queue consumers, cron jobs, `artisan`
  commands, one-shot scripts. No fork, no SAPI restriction.
- **An engine with no refresher, in any process, forked or not.** All four
  engine methods survive a fork, and the network is not merely unused but
  unreachable. If your requirement is "convert money in-process, never call
  out", that is exactly this and the sidecar cannot offer it.
- It is **not** about latency. Measured for the sibling ABI (llmux, same shape):
  the boundary is ~4 µs in-process against ~46 µs over loopback. Real work
  dwarfs both.

### Platforms

Prebuilt `libopenrate` today:

| target | status |
| --- | --- |
| darwin/arm64 | **built, smoke-tested and benchmarked.** ~6.7 MB (it varies slightly per build). |
| darwin/amd64 | built (7,120,680 bytes) but **never executed** — this machine cannot run it. |
| linux/amd64 | **not built locally.** A CI job exists and has never run. |
| linux/arm64 | **built nowhere.** |
| windows/amd64 | **built nowhere.** No `.dll` exists. |

Note that openrate's coverage is **not** llmux's: llmux has a tested
linux/arm64 build and openrate does not. Most PHP is deployed on linux/amd64,
which for openrate has no build at all today. The `openrate` binary the sidecar
spawns has no such gap.

### The rest of the honest list

1. **The Go runtime lives in your process** — its GC, its scheduler, and its
   signal handlers. Measured, it **replaces** `SIGSEGV`, `SIGBUS`, `SIGFPE`,
   `SIGPIPE` and `SIGURG`, chaining to what was there, and adds `SA_ONSTACK` to
   `SIGILL`, `SIGXFSZ` and `SIGUSR2`. Any extension with its own opinions about
   *those* — Xdebug, an APM agent's crash handler — is sharing them now.
   **`SIGPROF` is not touched**, so `SIGPROF`-driven PHP profilers are not
   affected.
2. **Not fork-safe** — see above.
3. **The shared library is 6–7 MB**, per worker that loads it.
4. **Prebuilt binaries cover darwin only, and one of those two has never been
   run** — see [Platforms](#platforms).
5. **No rate limiting, no CORS, no auth on the direct boundary.** Those live in
   `openrate serve`'s HTTP shell. In-process you are inside the trust boundary
   and the library does not pretend otherwise.
6. **One deliberate behavioural difference from HTTP:** `rates` with an unknown
   base is an *error* through the C ABI, where `GET /api/v1/rates` answers 200
   with an empty book. The library follows the Go API, not the endpoint.

### PHP-specific FFI behaviour worth knowing

Observable on PHP 8.5.9, and all of it shaped `src/Ffi.php`:

- A `const char*` **return** (`openrate_abi_version`) arrives as a PHP `string`.
  A non-const `char*` return (`openrate_call`) arrives as `FFI\CData` — or as
  PHP `null` when the C function returned `NULL`. The failure test is therefore
  `$res === null`, not `FFI::isNull($res)`, which throws a `TypeError` on null.
- openrate writes `*err` **on failure only**, so `Ffi` allocates a fresh
  zero-initialised `char*` slot per call. Reusing one leaves a dangling pointer
  from the previous error.
- Everything the library returns — results **and** error messages — is released
  with `openrate_free` and nothing else. `Ffi` does that on the error path too,
  before it throws.
- `FFI::new()` called statically is deprecated in PHP 8.5; use
  `$ffi->new('char*')`.
- PHP's FFI parser does not run the C preprocessor, so `ffi/include/openrate.h`
  cannot be handed to `FFI::cdef` directly. The declarations are transcribed
  into `Ffi::CDEF`; if the header changes, that constant changes with it.
- An empty PHP array encodes as a JSON **array**, and openrate wants an object.
  Pass `'{}'` (or `(object) []`) for a request with no fields.

## Examples

| file | mode | what it shows |
| --- | --- | --- |
| `examples/sidecar_convert.php` | sidecar | spawn, wait for `/readyz`, convert, cross-rate, snapshot, HTTP error, guaranteed stop |
| `examples/direct_convert.php` | direct | version probe, empty engine, `load`, convert, cross-rate, `rates`/`meta`, an engine refusing `refresh`, opt-in refresher, `finally { close() }` |
| `examples/fork_probe.php` | direct | reproduces the fork hazard, shows which methods hide it, and the macOS TLS crash |

Both main examples run with no API key. `direct_convert.php` needs no network at
all unless you set `OPENRATE_NETWORK=1`; `sidecar_convert.php` fetches from ECB.
