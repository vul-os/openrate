/*
 * openrate.h — the C ABI of libopenrate.
 *
 * This is the hand-written, stable header. cgo also emits one next to the
 * shared library it builds (libopenrate.h); that one is generated, carries the
 * Go type prologue, and changes shape with the toolchain. Bind against THIS
 * file.
 *
 * ---------------------------------------------------------------------------
 * The shape
 * ---------------------------------------------------------------------------
 * openrate and llmux expose the same ABI shape, so a reader who learns one
 * knows the other: integer handles, JSON in and JSON out, one dispatch function
 * taking a method name, one free function for everything the library returns.
 *
 * The JSON is the JSON openrate's HTTP API already publishes. That is the point
 * — the wire contract is reused, not reinvented, and a binding written against
 * the sidecar works against the shared library with the same parser.
 *
 * ---------------------------------------------------------------------------
 * Memory
 * ---------------------------------------------------------------------------
 * Anything this library hands back — call results AND error strings — is
 * released with openrate_free() and nothing else. It was not allocated by your
 * allocator. openrate_free(NULL) is safe.
 *
 * Every fallible entry point takes a `char** err`. It is set to NULL before the
 * work starts and, on failure only, to a malloc'd message. The message is PLAIN
 * UTF-8 TEXT, not JSON — do not try to parse it. Passing NULL for err is
 * allowed: the return value still reports the failure.
 *
 * ---------------------------------------------------------------------------
 * Handles
 * ---------------------------------------------------------------------------
 * A handle is a uint64 key into a registry inside the library, never a pointer.
 * 0 is never a valid handle, and a closed handle's number is RETIRED rather than
 * recycled. That is what makes use-after-close readable: a stale handle can only
 * ever produce "handle N is not open", never silent access to whatever object
 * happened to be created next. Using a closed or invented handle is a clean
 * error, not a crash in your address space.
 *
 * There are two kinds, and the difference is openrate's whole design:
 *
 *   ENGINE     computes. openrate_new() starts no thread, opens no socket,
 *              reads no environment variable and sends no packet. An engine
 *              answers from the snapshot it holds, and says "unknown or
 *              unreachable currency pair" until something gives it one.
 *
 *   REFRESHER  fetches. It is a SEPARATE construction over an engine, with its
 *              own handle and its own lifetime. A host that never calls
 *              openrate_refresher_new() cannot make this library touch the
 *              network. That is not a convention; there is no other code path.
 *
 * Feed an engine without a refresher using the "load" method, which takes rates
 * you obtained yourself.
 *
 * ---------------------------------------------------------------------------
 * Threads, forks, and the Go runtime
 * ---------------------------------------------------------------------------
 * Loading this library loads the Go runtime into your process: its GC, its
 * scheduler, its signal handlers. It is NOT fork-safe — after fork() without
 * exec(), the runtime in the child is broken. See ffi/README.md before you use
 * this from a process that forks (Python multiprocessing, uWSGI, Unicorn) or
 * that installs its own SIGSEGV/SIGPROF handling (the JVM).
 *
 * All entry points are safe to call from multiple threads, INCLUDING against
 * the same handle, and including openrate_close() concurrently with a call on
 * the handle it is closing. A race between the two always resolves one of two
 * ways and never a third: either the call runs to completion and the close
 * WAITS for it before tearing the object down, or the close wins and the call
 * returns the ordinary "handle N is not open" error. There is no interleaving
 * in which work outlives the handle that authorized it:
 *
 *   - "start" racing openrate_close() can never leave a refresh loop running,
 *     whichever order the two land in.
 *   - "refresh" racing openrate_close() can never put a request on the wire
 *     after openrate_close() has returned. A refresh already in flight is
 *     cancelled and then WAITED FOR, so a fetch that does not stop promptly
 *     delays the close rather than outliving it.
 *   - "ready" is likewise cancelled by a close, and returns rather than
 *     blocking out its timeout against a handle that no longer exists.
 *
 * What is NOT safe is using a handle you have already closed and then acting on
 * the error: that is well-defined (you get "not open") but it means some other
 * thread owns the lifetime, which is a design to fix rather than a race to win.
 *
 * ---------------------------------------------------------------------------
 * Streaming
 * ---------------------------------------------------------------------------
 * There is deliberately no openrate_stream(). openrate answers from a snapshot
 * it already holds; there is no incremental operation to stream, so an empty
 * callback entry point added for symmetry would be a promise with nothing behind
 * it. llmux, which shares this ABI shape, DOES define llmux_stream, because chat
 * streaming is its main event. The omission is stated in ffi/README.md rather
 * than left as a gap a reader has to notice.
 */

#ifndef OPENRATE_H
#define OPENRATE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/*
 * The version openrate_abi_version() returns at runtime, compiled into your
 * program at build time. Compare the two after loading to detect a stale
 * library on the load path:
 *
 *   if (strcmp(openrate_abi_version(), OPENRATE_ABI_VERSION) != 0) { ... }
 *
 * KEEP IN SYNC WITH: /VERSION and ffi/abi/version.go. Tests fail if they drift.
 */
#define OPENRATE_ABI_VERSION "0.1.5"

/*
 * Construct an ENGINE. Returns a handle, or 0 with *err set.
 *
 * config_json may be NULL. Fields:
 *   {"base": "ZAR", "quiet": false}
 *
 *   base   default presentation currency (default "ZAR")
 *   quiet  discard the library's log output, which otherwise lands on your
 *          process's stderr
 */
uint64_t openrate_new(const char *config_json, char **err);

/*
 * Construct a REFRESHER over an engine. Returns its own handle, or 0 with *err
 * set. This is the only object in openrate that can open a socket, and building
 * it still does not: fetching begins at openrate_call(h, "refresh", ...) or
 * openrate_call(h, "start", ...).
 *
 * config_json may be NULL. Fields:
 *   {"sources": "ecb,coinbase", "interval_ms": 3600000,
 *    "fetch_timeout_ms": 50000, "quiet": false}
 *
 *   sources           comma-separated adapter names; empty means openrate's
 *                     defaults (ecb, coinbase, luno, sarb). Resolved purely:
 *                     an API key in the environment never widens this set.
 *   interval_ms       cadence of the "start" loop (default 1h)
 *   fetch_timeout_ms  bound on one source's fetch (default 50s)
 *
 * Both millisecond fields are limited to 9223372036854 (about 292 years), which
 * is everything a duration can hold. A larger value is REFUSED with *err set,
 * rather than accepted: the multiply into nanoseconds wraps, and it does not
 * wrap to something you would notice — interval_ms 288230376151711745 comes out
 * as exactly 1ms, turning the most conservative cadence you can ask for into a
 * loop that hammers every source a thousand times a second. Leave a field 0 for
 * openrate's default. (timeout_ms on a per-call request is CLAMPED to the same
 * ceiling instead of refused, since a 292-year deadline is indistinguishable
 * from the one you asked for.)
 *
 * If the engine is closed by another thread while this call is in flight, this
 * returns 0 with *err set to that engine's "handle N is not open". No refresher
 * is created in that case — there is no outcome where you are handed a handle
 * whose engine is already gone.
 */
uint64_t openrate_refresher_new(uint64_t engine, const char *config_json, char **err);

/*
 * Run one method against a handle. Returns a malloc'd UTF-8 JSON document, or
 * NULL with *err set. Free the result with openrate_free().
 *
 * request_json may be NULL, which means "{}".
 *
 * ENGINE methods
 * --------------
 *   "convert"  {"from":"USD","to":"ZAR","amount":100}
 *              -> {"from","to","amount","result","rate":{...}}
 *              Identical to GET /api/v1/convert. Omitted currencies mean the
 *              engine's default base; an omitted amount means 1.
 *
 *   "rates"    {"base":"ZAR"}
 *              -> {"base","built_at","rates":{"USD":{...},...}}
 *              Identical to GET /api/v1/rates. rates[X].rate reads as
 *              "1 base = rate units of X". A base the snapshot has never heard
 *              of is an error, exactly as it is over HTTP (404) and in the Go
 *              library (ErrUnknownBase). An engine holding NO rates at all
 *              still returns an empty book and no error: that is "nothing yet",
 *              a readiness question, not a bad request.
 *
 *   "meta"     {} -> {"default_base","built_at","currencies","sources"}
 *              Identical to GET /api/v1/meta. "sources" carries the fetch
 *              status of every OPEN refresher built over this engine, and is []
 *              for an engine nobody refreshes. A refresher you close drops out
 *              of it: its last fetch status can never change again, and it is
 *              reported under a handle you can no longer address.
 *
 *   "load"     {"edges":[{"from":"USD","to":"ZAR","rate":18.5,
 *                         "source":"mine","time":"2026-08-09T00:00:00Z"}],
 *               "built_at":"2026-08-09T00:00:00Z"}
 *              -> {"built_at","currencies"}
 *              The zero-network path: install rates you obtained yourself. Has
 *              no HTTP counterpart, because the server is read-only. "time"
 *              defaults to built_at, and "built_at" to now.
 *
 * REFRESHER methods
 * -----------------
 *   "status"   {} -> {"sources":[{"name","last_ok","last_error","edges"},...]}
 *   "refresh"  {"timeout_ms":30000} -> {"sources":[...]}
 *              One synchronous fetch of every source. THIS OPENS SOCKETS.
 *              On a CLOSED handle it is "handle N is not open", and a close
 *              that lands while it is running waits for it — see "Threads".
 *   "start"    {} -> {"running":true}   background loop on the configured
 *              interval. The only thread this library starts on its own.
 *              Starting an already-running refresher is an error, not a second
 *              loop. Starting one whose handle has been CLOSED is "handle N is
 *              not open" — stop is reversible, close is not, and a loop started
 *              after close would have no reachable stop.
 *   "stop"     {} -> {"running":false}  stops it and waits for it to exit.
 *              Reversible: "start" afterwards is legal and starts a fresh loop.
 *   "ready"    {"timeout_ms":5000} -> {"ready":true}
 *              Blocks until the engine holds at least one currency. It does not
 *              fetch: something must be refreshing, or it waits.
 *
 * timeout_ms of 0 or absent means no deadline of the caller's own.
 */
char *openrate_call(uint64_t h, const char *method, const char *request_json, char **err);

/*
 * Release a handle. Closing an engine also stops and releases every refresher
 * built over it, so closing in the "wrong" order cannot leak a running loop.
 * Closing an unknown or already-closed handle is a no-op.
 *
 * This is TERMINAL, and it waits: when it returns, any background loop on the
 * handle has been cancelled AND has exited, and any blocking call in flight on
 * it ("refresh", "ready") has been cancelled AND has returned — so a host that
 * closes everything and then unloads the library has no thread of ours left in
 * its address space. A fetch that ignores cancellation therefore delays this
 * call; that is the guarantee working, not a hang.
 * It is also safe against a concurrent call on the same handle — see "Threads"
 * above for exactly which two outcomes are possible.
 */
void openrate_close(uint64_t h);

/* Release anything this library returned: results and error strings alike. */
void openrate_free(char *p);

/*
 * The compiled-in version of the loaded library. The returned pointer is owned
 * by the library, lives as long as it is loaded, and must NOT be passed to
 * openrate_free().
 */
const char *openrate_abi_version(void);

/*
 * How many handles are currently open. Diagnostic only — for a host test suite
 * asserting it closed what it opened.
 *
 * It is meaningful even when handles are being opened and closed concurrently:
 * there is no window in which a refresher exists but no close can reach it, so
 * a host that closes every handle it was given always sees this return to what
 * it was. A number that will not come down is a leak in the library, not a race
 * in your test.
 */
uint64_t openrate_open_handles(void);

#ifdef __cplusplus
}
#endif

#endif /* OPENRATE_H */
