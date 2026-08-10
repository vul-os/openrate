// openrate, direct — libopenrate loaded into your Node process over the C ABI
// in ffi/include/openrate.h.
//
//   import { Engine } from "openrate/direct";
//
//   using engine = Engine.open({ base: "ZAR" });
//   engine.load({ edges: [{ from: "USD", to: "ZAR", rate: 18.5, source: "my-desk" }] });
//   engine.convert("USD", "ZAR", 100).result;     // 1850
//
// That program starts no thread, opens no socket, reads no environment variable
// and sends no packet — and not by convention. An ENGINE handle *refuses* the
// "refresh" method; fetching needs a second, explicit construction:
//
//   using engine = Engine.open();
//   using refresher = engine.refresher({ sources: "ecb" });
//   refresher.refresh();                          // THIS OPENS SOCKETS
//
// The split is enforced at the ABI. It is the reason to embed openrate rather
// than run it, and this module keeps it visible instead of papering over it
// with one class that quietly does both.
//
// There is deliberately no openrate_stream: openrate answers from a snapshot it
// already holds, so there is no incremental operation to stream. llmux, which
// shares this ABI shape, does have one.
//
// JSON in, JSON out — the same JSON the HTTP API publishes.

import * as fs from "fs";
import * as path from "path";
import type { KoffiFunc, LibraryHandle } from "koffi";

// ---------------------------------------------------------------------------
// koffi, loaded lazily
// ---------------------------------------------------------------------------
// koffi is an OPTIONAL dependency, so `require("openrate")` — the sidecar path,
// which needs no native code at all — works without it. See README.md for why
// koffi and not a hand-written N-API addon.

/** Anything koffi hands back that is a raw C pointer. Never dereference it in JS. */
type CPtr = unknown;
/** koffi's `_Out_ void**` convention: a one-element array koffi writes into. */
type ErrOut = [CPtr];

interface Koffi {
  load(filename: string): LibraryHandle;
  decode(value: CPtr, type: string, len: number): unknown;
}

let _koffi: Koffi | null = null;
function koffi(): Koffi {
  if (_koffi) return _koffi;
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports -- optional dependency, resolved at first direct-mode use
    _koffi = require("koffi") as Koffi;
  } catch {
    throw new Error(
      'openrate direct mode needs the optional "koffi" dependency: npm install koffi. ' +
        'Or use the sidecar — `require("openrate").Sidecar.start()` — which needs no native FFI.'
    );
  }
  return _koffi;
}

// ---------------------------------------------------------------------------
// Finding libopenrate
// ---------------------------------------------------------------------------

function goArch(): string {
  const map: Record<string, string> = { arm64: "arm64", x64: "amd64" };
  return map[process.arch] ?? process.arch;
}

function goOS(): string {
  const map: Record<string, string> = { darwin: "darwin", linux: "linux", win32: "windows" };
  return map[process.platform] ?? process.platform;
}

function libFileName(): string {
  // scripts/build-ffi.sh names artifacts libopenrate-<goos>-<goarch>.<ext>.
  const ext = process.platform === "darwin" ? "dylib" : process.platform === "win32" ? "dll" : "so";
  return `libopenrate-${goOS()}-${goArch()}.${ext}`;
}

/**
 * Where the shared library will be loaded from.
 *
 * `OPENRATE_LIBRARY` wins. Otherwise a repo checkout's `dist/ffi/` is tried, and
 * failing that the bare name goes to the system loader.
 *
 * openrate ships **no** windows/amd64 or linux/arm64 library, and its
 * darwin/amd64 build has never been executed. See README.md — the matrix is not
 * the same as llmux's.
 */
export function resolveLibrary(explicit?: string): string {
  if (explicit) return explicit;
  if (process.env.OPENRATE_LIBRARY) return process.env.OPENRATE_LIBRARY;
  const candidate = path.join(__dirname, "..", "..", "dist", "ffi", libFileName());
  if (fs.existsSync(candidate)) return candidate;
  return libFileName();
}

// ---------------------------------------------------------------------------
// The six symbols
// ---------------------------------------------------------------------------

interface Binding {
  path: string;
  abi_version: KoffiFunc<() => string>;
  new_: KoffiFunc<(config: string | null, err: ErrOut) => number>;
  refresher_new: KoffiFunc<(engine: number, config: string | null, err: ErrOut) => number>;
  call: KoffiFunc<(h: number, method: string, req: string | null, err: ErrOut) => CPtr>;
  close: KoffiFunc<(h: number) => void>;
  free: KoffiFunc<(p: CPtr) => void>;
  open_handles: KoffiFunc<() => number>;
}

const _bindings = new Map<string, Binding>();

function bind(libPath: string): Binding {
  const cached = _bindings.get(libPath);
  if (cached) return cached;
  const lib = koffi().load(libPath);
  const b: Binding = {
    path: libPath,
    // `const char*`: koffi decodes it and does not free it. Correct here and
    // only here — openrate_abi_version returns storage the library owns.
    abi_version: lib.func("const char *openrate_abi_version()"),
    new_: lib.func("uint64_t openrate_new(const char *config_json, _Out_ void **err)"),
    refresher_new: lib.func(
      "uint64_t openrate_refresher_new(uint64_t engine, const char *config_json, _Out_ void **err)"
    ),
    // Declared `void*`, not `char*`, ON PURPOSE: koffi would decode a `char*`
    // result into a JS string and drop the pointer, leaving nothing to hand
    // openrate_free. That is the leak this line prevents.
    call: lib.func("void *openrate_call(uint64_t h, const char *method, const char *request_json, _Out_ void **err)"),
    close: lib.func("void openrate_close(uint64_t h)"),
    free: lib.func("void openrate_free(void *p)"),
    open_handles: lib.func("uint64_t openrate_open_handles()"),
  };
  _bindings.set(libPath, b);
  return b;
}

/** The openrate version the loaded shared library was built from. */
export function abiVersion(libraryPath?: string): string {
  return bind(resolveLibrary(libraryPath)).abi_version();
}

/**
 * How many handles the library currently holds open. Diagnostic only — for a
 * test suite asserting it closed what it opened.
 */
export function openHandles(libraryPath?: string): number {
  return bind(resolveLibrary(libraryPath)).open_handles();
}

/** Read a C string openrate allocated, then free it. Freeing is not optional. */
function takeString(b: Binding, p: CPtr): string | null {
  if (!p) return null;
  try {
    return koffi().decode(p, "char", -1) as string;
  } finally {
    b.free(p);
  }
}

/** Turn a populated `char** err` into an Error, freeing the message. */
function takeError(b: Binding, err: ErrOut, fallback: string): Error {
  // Error strings are plain UTF-8 text, NOT JSON. Do not parse them.
  const msg = takeString(b, err[0]);
  err[0] = null;
  return new Error(msg ?? fallback);
}

function callRaw(b: Binding, h: number, method: string, request: unknown): unknown {
  const err: ErrOut = [null];
  const body = request == null ? null : typeof request === "string" ? request : JSON.stringify(request);
  const res = b.call(h, method, body, err);
  if (!res) throw takeError(b, err, `openrate_call(${method}) failed`);
  return JSON.parse(takeString(b, res) ?? "null");
}

// ---------------------------------------------------------------------------
// Shapes
// ---------------------------------------------------------------------------

/** One leg of a converted path: where the rate came from and how old it is. */
export interface Leg {
  from: string;
  to: string;
  rate: number;
  source: string;
  age_sec: number;
}

/** The audit trail attached to every answer: hops, sources, freshness, grade. */
export interface RateDetail {
  rate: number;
  hops: number;
  as_of: string;
  age_sec: number;
  path: string[];
  sources: string[];
  quality: Record<string, unknown>;
  legs: Leg[];
  quotes: { source: string; rate: number; age_sec: number }[];
}

export interface ConvertResult {
  from: string;
  to: string;
  amount: number;
  result: number;
  rate: RateDetail;
}

export interface RatesResult {
  base: string;
  built_at: string;
  rates: Record<string, RateDetail>;
}

export interface MetaResult {
  default_base: string;
  built_at: string;
  currencies: string[];
  /** Fetch status of every refresher built over this engine; `[]` if none. */
  sources: { name: string; last_ok?: string; last_error?: string; edges?: number }[];
}

/** A rate you obtained yourself, for {@link Engine.load}. */
export interface Edge {
  from: string;
  to: string;
  rate: number;
  source: string;
  /** Defaults to the document's built_at. */
  time?: string;
}

export interface SourceStatus {
  name: string;
  last_ok?: string;
  last_error?: string;
  edges?: number;
}

export interface EngineOptions {
  /** Default presentation currency (default "ZAR"). */
  base?: string;
  /** Discard the library's log output, which otherwise lands on your stderr. */
  quiet?: boolean;
  /** Override the shared library path (otherwise {@link resolveLibrary}). */
  libraryPath?: string;
  /** Refuse to open unless the loaded library reports this version. */
  expectVersion?: string;
}

export interface RefresherOptions {
  /**
   * Comma-separated adapter names; empty means openrate's defaults
   * (ecb, coinbase, luno, sarb). Resolved purely — an API key in the
   * environment never widens this set.
   */
  sources?: string;
  /** Cadence of the {@link Refresher.start} loop (default 1h). */
  interval_ms?: number;
  /** Bound on one source's fetch (default 50s). */
  fetch_timeout_ms?: number;
  quiet?: boolean;
}

// ---------------------------------------------------------------------------
// Engine
// ---------------------------------------------------------------------------

/**
 * An engine COMPUTES. It answers from the snapshot it holds and says "unknown
 * or unreachable currency pair" until something gives it one.
 *
 * Constructing one starts no thread, opens no socket, reads no environment
 * variable and sends no packet. Feed it with {@link load} for the zero-network
 * path, or build a {@link Refresher} over it — a separate handle with a separate
 * lifetime — to let it fetch.
 *
 * Disposable, so `using engine = Engine.open()` closes the handle on every exit
 * path out of the block, including a throw.
 */
export class Engine implements Disposable {
  private readonly b: Binding;
  private h: number;
  private closed = false;

  private constructor(b: Binding, h: number) {
    this.b = b;
    this.h = h;
  }

  static open(opts: EngineOptions = {}): Engine {
    const libPath = resolveLibrary(opts.libraryPath);
    const b = bind(libPath);
    if (opts.expectVersion) {
      const got = b.abi_version();
      if (got !== opts.expectVersion) {
        throw new Error(
          `libopenrate at ${libPath} reports version ${got}, expected ${opts.expectVersion} — ` +
            "a stale library earlier on the load path is the usual cause"
        );
      }
    }
    const cfg: Record<string, unknown> = {};
    if (opts.base !== undefined) cfg.base = opts.base;
    if (opts.quiet !== undefined) cfg.quiet = opts.quiet;
    const err: ErrOut = [null];
    const h = b.new_(Object.keys(cfg).length ? JSON.stringify(cfg) : null, err);
    if (h === 0) throw takeError(b, err, "openrate_new failed");
    return new Engine(b, h);
  }

  /** The registry key of this engine. Handles are retired, never recycled. */
  get handle(): number {
    return this.h;
  }

  private live(): number {
    if (this.closed) throw new Error("openrate engine is closed");
    return this.h;
  }

  /**
   * Convert an amount. Omitted currencies mean the engine's default base; an
   * omitted amount means 1. Identical to `GET /api/v1/convert`.
   */
  convert(from?: string, to?: string, amount?: number): ConvertResult {
    const req: Record<string, unknown> = {};
    if (from !== undefined) req.from = from;
    if (to !== undefined) req.to = to;
    if (amount !== undefined) req.amount = amount;
    return callRaw(this.b, this.live(), "convert", req) as ConvertResult;
  }

  /**
   * The whole book against one base. `rates[X].rate` reads as
   * "1 base = rate units of X". Identical to `GET /api/v1/rates`, including its
   * error path: an unknown base is an error here and a 404 carrying the
   * same text over HTTP. An engine holding no rates at all returns an empty
   * book and no error on either — that is a readiness question.
   */
  rates(base?: string): RatesResult {
    return callRaw(this.b, this.live(), "rates", base === undefined ? {} : { base }) as RatesResult;
  }

  /** Default base, build time, known currencies, and every refresher's status. */
  meta(): MetaResult {
    return callRaw(this.b, this.live(), "meta", {}) as MetaResult;
  }

  /**
   * Install rates you obtained yourself. The zero-network path; it has no HTTP
   * counterpart, because the server is read-only. `time` defaults to `built_at`,
   * and `built_at` to now.
   */
  load(doc: { edges: Edge[]; built_at?: string }): { built_at: string; currencies: string[] } {
    return callRaw(this.b, this.live(), "load", doc) as { built_at: string; currencies: string[] };
  }

  /**
   * Build a REFRESHER over this engine. Its own handle, its own lifetime, and
   * the only object in openrate that can open a socket — though building it
   * still does not: fetching begins at {@link Refresher.refresh} or
   * {@link Refresher.start}.
   */
  refresher(opts: RefresherOptions = {}): Refresher {
    const err: ErrOut = [null];
    const h = this.b.refresher_new(this.live(), Object.keys(opts).length ? JSON.stringify(opts) : null, err);
    if (h === 0) throw takeError(this.b, err, "openrate_refresher_new failed");
    return new Refresher(this.b, h);
  }

  /**
   * Escape hatch for a method this class does not wrap yet. Same dispatch, same
   * JSON; you lose the typed result.
   */
  call(method: string, request?: unknown): unknown {
    return callRaw(this.b, this.live(), method, request ?? {});
  }

  /**
   * Release the engine. Closing an engine also stops and releases every
   * refresher built over it, so closing in the "wrong" order cannot leak a
   * running loop. Idempotent.
   */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.b.close(this.h);
    this.h = 0;
  }

  [Symbol.dispose](): void {
    this.close();
  }
}

// ---------------------------------------------------------------------------
// Refresher
// ---------------------------------------------------------------------------

/**
 * A refresher FETCHES. It is the only thing in openrate that opens a socket.
 *
 * Disposable. Note that closing the engine closes this too, so the usual
 * `using engine = ...; using refresher = engine.refresher(...)` ordering is safe
 * in either direction.
 */
export class Refresher implements Disposable {
  private readonly b: Binding;
  private h: number;
  private closed = false;

  constructor(b: Binding, h: number) {
    this.b = b;
    this.h = h;
  }

  get handle(): number {
    return this.h;
  }

  private live(): number {
    if (this.closed) throw new Error("openrate refresher is closed");
    return this.h;
  }

  /** What each source last did. Opens nothing. */
  status(): { sources: SourceStatus[] } {
    return callRaw(this.b, this.live(), "status", {}) as { sources: SourceStatus[] };
  }

  /**
   * One synchronous fetch of every source. THIS OPENS SOCKETS, and **it blocks
   * the event loop** for as long as the slowest source takes — seconds, on a
   * bad day, since the SARB host is slow to connect.
   *
   * In a server, call {@link start} instead: the polling loop runs on a Go
   * goroutine inside the library and returns immediately, so nothing in Node
   * waits on the network. See README.md.
   */
  refresh(timeoutMs?: number): { sources: SourceStatus[] } {
    const req = timeoutMs === undefined ? {} : { timeout_ms: timeoutMs };
    return callRaw(this.b, this.live(), "refresh", req) as { sources: SourceStatus[] };
  }

  /**
   * Start the background refresh loop on the configured interval. Returns
   * immediately — the loop is a goroutine inside the library, not a Node timer,
   * and it is the only thread openrate starts on its own.
   */
  start(): { running: boolean } {
    return callRaw(this.b, this.live(), "start", {}) as { running: boolean };
  }

  /** Stop the loop and wait for it to exit. */
  stop(): { running: boolean } {
    return callRaw(this.b, this.live(), "stop", {}) as { running: boolean };
  }

  /**
   * Block until the engine holds at least one currency.
   *
   * It does not fetch: something must be refreshing, or it waits. And it blocks
   * the event loop while it waits, which is why the examples poll
   * `engine.meta().currencies.length` from a Node timer instead — same wait,
   * without freezing the process.
   */
  ready(timeoutMs?: number): { ready: boolean } {
    const req = timeoutMs === undefined ? {} : { timeout_ms: timeoutMs };
    return callRaw(this.b, this.live(), "ready", req) as { ready: boolean };
  }

  /** Release the refresher, stopping its loop. Idempotent. */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.b.close(this.h);
    this.h = 0;
  }

  [Symbol.dispose](): void {
    this.close();
  }
}
