# openrate from C++

`openrate.hpp` is a header-only, C++17 RAII wrapper over the openrate C ABI. No
dependencies beyond the standard library and
[`ffi/include/openrate.h`](../../ffi/include/openrate.h). Copy the header into
your project, or add both directories to your include path.

```cpp
#include "openrate.hpp"

openrate::Engine engine(R"({"base":"ZAR"})");        // throws on failure
engine.load(R"({"edges":[{"from":"USD","to":"ZAR","rate":18.5}]})");
std::string answer = engine.convert("USD", "ZAR", 100);
                                                     // ~Engine closes the handle
```

## Two types, because there are two kinds of handle

That split is openrate's whole design, and the wrapper promotes it from a
runtime error into a compile-time one.

| type | what it does |
|---|---|
| `openrate::Engine` | **computes.** Constructing one starts no thread, opens no socket, reads no environment variable and sends no packet. Feed it with `load()` |
| `openrate::Refresher` | **fetches.** A separate construction over an engine, with its own handle. Building it still opens nothing — `refresh()` and `start()` are what fetch |

`Engine` has no method that can reach the network, so **a function taking
`const Engine&` cannot fetch, and the compiler enforces it** — no review comment
required. The ABI enforces the same thing underneath: an engine handle refuses
the `refresh` method. `direct_convert.cpp` shows both halves, the type-system
one by omission and the library one by going around the wrapper with
`try_call("refresh", ...)`.

Closing an engine also releases every refresher built over it, so closing in the
"wrong" order cannot leak a running loop. `direct_convert.cpp` does exactly that
and prints the handle count before and after.

**There is no `stream()`.** openrate answers from a snapshot it already holds,
so there is no incremental operation to stream, and no `openrate_stream` in the
C ABI either. llmux, which shares this ABI shape, has one. The omission is
deliberate, not a gap.

## What the wrapper is actually for

The C ABI hands out two kinds of resource: `uint64_t` handles, and malloc'd
`char*` strings that only `openrate_free` may release. Both are easy to leak on
an error path, and in C++ **every** path is an error path, because any line can
throw. So:

- **Every `char*` the library returns is owned by `openrate::OwnedString` the
  instant it comes back** — results and error messages alike, including the
  message that is about to become an exception. There is no window in which it
  is a raw pointer.
- **Handles are owned by move-only types that close in `noexcept` destructors.**
  Double-close and close-during-unwinding are both fine.
- `openrate::open_handles()` is the library's own count. Assert on it in your
  own tests; `direct_convert.cpp` prints it at the start and the end.

## Errors: exceptions or expected-style, your choice

Both are present and neither is a second implementation — the throwing calls are
one line each on top of the non-throwing ones.

```cpp
std::string out = engine.convert("USD", "ZAR", 100);          // throws openrate::Error

openrate::StringResult r = engine.try_convert("USD", "ZAR", 100);   // never throws
if (!r.ok()) std::cerr << r.error();          // plain UTF-8 text, not JSON
else         use(r.value());                  // or r.take() to move it out
```

`Engine::try_open()` and `Refresher::try_open()` are the non-throwing
constructors. Define `OPENRATE_NO_EXCEPTIONS` (or build with `-fno-exceptions`,
which defines it for you) to compile only the `try_` layer.

`Result<T>` holds its value in a `std::optional`, and that is load-bearing
rather than stylistic: with a plain member, `Result<Engine>::failure(...)` would
default-construct an `Engine` — opening a real engine on the failure path, from
inside the code reporting that an engine could not be opened.

## The two examples

| file | mode | what it shows |
|---|---|---|
| `direct_convert.cpp` | direct | version probe, an empty engine refusing to guess, `load`, `convert`, a crossed pair reporting its path, an engine refusing `refresh`, both error styles, a handle closed during stack unwinding, closing engine-before-refresher |
| `sidecar_convert.cpp` | sidecar | `Socket` and `Sidecar` classes with destructors, spawn on a free loopback port, `/healthz` poll, waiting for the first fetch to land, convert, the book, HTTP errors |

```bash
make                                      # build both
./direct_convert                          # no network at all
./direct_convert --fetch                  # also builds a refresher (network)
OPENRATE_BINARY=../../dist/openrate ./sidecar_convert
./sidecar_convert 8080                    # a server you already run
make run
```

Build the library first: `scripts/build-ffi.sh --host-only` from the repo root
writes `dist/ffi/`. Override with `make OPENRATE_LIB_DIR=/path/to/dir`.

Compare `direct_convert.cpp` with
[`../c/direct_convert.c`](../c/direct_convert.c): the same calls in the same
order producing the same output, with the C version's single `goto done` cleanup
label replaced by destructors. That comparison is the best short argument for
the wrapper.

**`sidecar_convert.cpp` never includes `openrate.hpp`, and must not.** It forks,
and a process that has loaded the Go runtime cannot fork safely. Keep them as
two binaries.

Real output from `direct_convert`, with no network:

```
abi version:  0.1.2 (built against 0.1.2)
open handles: 0
engine:       handle 1

empty engine  openrate: convert USD->ZAR: unknown or unreachable currency pair
load          {"built_at":"2026-08-09T00:00:00Z","currencies":["EUR","GBP","USD","ZAR"]}
convert       100 USD = 1850 ZAR
              rate 18.5, 1 hop(s), path USD -> ZAR
cross         1 GBP = 1.165137614678899 EUR via GBP -> USD -> EUR
no fetching   openrate: unknown engine method "refresh" (have: convert, rates, meta, load)
unwind        handle 2 was closed by ~Engine during unwinding
order         closed the engine first: 3 -> 1 handles (the refresher went with it)

after close   open handles 0
```

## Building

```
c++ -std=c++17 -I<repo>/ffi/include -o direct_convert direct_convert.cpp \
    <libdir>/libopenrate-<goos>-<goarch>.<ext> -Wl,-rpath,<libdir>
```

`build-ffi.sh` names the artifact per target, so there is no plain `-lopenrate`
to link — the file is passed by path.

**macOS wart.** `go build -buildmode=c-shared` gives the dylib a bare install
name with no `@rpath/` prefix, so `-rpath` is never consulted and the program
dies at startup with `Library not loaded: libopenrate-darwin-arm64.dylib`. The
Makefile fixes it in the executable with `install_name_tool -change`; a packager
should instead fix the library with
`install_name_tool -id @rpath/<name> <name>`.

## Which mode to use from C++

C++ has no FFI friction here, and direct mode is the only way to get **the
engine that cannot fetch**. Prefer it, unless:

- **Your process forks.** The Go runtime does not survive `fork()` without
  `exec()`.
- **Your process handles its own signals**, or you build with sanitizers, or you
  ship a crash reporter. Go installs handlers for `SIGSEGV`, `SIGBUS`, `SIGFPE`
  and `SIGPROF`, and chains to a pre-existing handler in most cases. "Most" is
  the honest word.
- **Several processes should share one refreshing book.** Four workers each
  fetching their own copy is worse in every dimension.
- **You are not on darwin/arm64.** Read the next section before assuming.

## Platform reality for openrate

| target | status |
|---|---|
| darwin/arm64 | built, smoke-tested and benchmarked. 6,682,274 bytes |
| darwin/amd64 | **built (7,120,680 bytes) but NEVER EXECUTED** — the build machine cannot run it |
| linux/amd64 | **not built locally.** A CI job exists and has never run |
| linux/arm64 | **built nowhere.** (llmux has this target; openrate does not) |
| windows/amd64 | **built nowhere.** No mingw-w64 available. No DLL exists |

One row has been executed. That is not a support matrix, and this page will not
present it as one.

## Latency

Measured on darwin/arm64 over 30,000 iterations: **3.7 µs in-process against
33.5 µs over loopback HTTP** — about 9×, ~30 µs saved per call. The HTTP side was
deliberately flattered (keep-alive on, no TLS), so that is a floor. Embed for the
engine that cannot fetch, and for no second process, no port and no loopback
surface.

## Reference

```cpp
namespace openrate {

std::string   abi_version();            // static string; never freed
bool          abi_version_matches();    // against the header's OPENRATE_ABI_VERSION
std::uint64_t open_handles();           // diagnostic

class OwnedString {                     // move-only; frees with openrate_free
    const char* c_str() const noexcept;
    std::string str() const;
    char** err_slot() noexcept;         // for a trailing char** err
};

template <class T> class Result {       // value held in a std::optional,
    bool ok() const;                    // so a failure constructs no T
    const std::string& error() const;
    const T& value() const&;   T take();
};
using StringResult = Result<std::string>;

class Error : public std::runtime_error {};   // carries the library's message

class Engine {                          // move-only; closes on destruction
    static Result<Engine> try_open(const char* config_json = nullptr);
    explicit Engine(const char* config_json = nullptr);        // throws
    std::uint64_t handle() const noexcept;  bool is_open() const noexcept;
    void close() noexcept;               // idempotent

    std::string convert(std::string_view from, std::string_view to, double amount = 1) const;
    std::string rates(std::string_view base = {}) const;
    std::string meta() const;
    std::string load(const char* request_json) const;
    // ... and try_convert / try_rates / try_meta / try_load / try_call

    Result<Refresher> try_refresher(const char* config_json = nullptr);
    Refresher         refresher(const char* config_json = nullptr);
};

class Refresher {                       // move-only; closes on destruction
    static Result<Refresher> try_open(const Engine&, const char* config_json = nullptr);
    explicit Refresher(const Engine&, const char* config_json = nullptr);   // throws
    std::string status() const;
    std::string refresh(int timeout_ms = 0) const;   // THIS OPENS SOCKETS
    std::string start() const;   std::string stop() const;
    std::string ready(int timeout_ms = 0) const;
    // ... and the try_ equivalents
};

}
```

The JSON is not parsed for you, and that is deliberate: openrate speaks ordinary
JSON, so use whatever your project already uses — nlohmann/json, simdjson,
RapidJSON. The examples use a short string scanner for printing only, and the
comment where its limits bite (openrate's convert response nests `"from"`,
`"to"` and `"rate"` inside the rate path's legs) is left in as the argument for
linking a real one.
