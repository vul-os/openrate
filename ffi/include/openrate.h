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
 * work starts and, on failure only, to a malloc'd UTF-8 message. Passing NULL
 * for err is allowed: the return value still reports the failure.
 *
 * ---------------------------------------------------------------------------
 * Handles
 * ---------------------------------------------------------------------------
 * A handle is a uint64 key into a registry inside the library, never a pointer.
 * 0 is never a valid handle. Using a closed or invented handle is a clean error,
 * not a crash in your address space.
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
 * All entry points are safe to call from multiple threads.
 *
 * ---------------------------------------------------------------------------
 * Streaming
 * ---------------------------------------------------------------------------
 * There is deliberately no openrate_stream(). openrate answers from a snapshot
 * it already holds; it has no incremental operation to stream. llmux, which
 * shares this ABI shape, does have one and does define openrate_stream's
 * counterpart. See ffi/README.md.
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
#define OPENRATE_ABI_VERSION "0.1.2"

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
 *              "1 base = rate units of X". An unknown base is an error here,
 *              where the HTTP endpoint answers 200 with an empty book — the one
 *              deliberate difference, and it follows the Go library.
 *
 *   "meta"     {} -> {"default_base","built_at","currencies","sources"}
 *              Identical to GET /api/v1/meta. "sources" carries the fetch
 *              status of every refresher built over this engine, and is [] for
 *              an engine nobody refreshes.
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
 *   "start"    {} -> {"running":true}   background loop on the configured
 *              interval. The only thread this library starts on its own.
 *   "stop"     {} -> {"running":false}  stops it and waits for it to exit.
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
 */
uint64_t openrate_open_handles(void);

#ifdef __cplusplus
}
#endif

#endif /* OPENRATE_H */
