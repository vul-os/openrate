/**
 * openrate for Deno — both modes in one dependency-free module.
 *
 * DIRECT (in-process, the C ABI in `ffi/include/openrate.h`):
 *
 * ```ts
 * import { Engine } from "./mod.ts";
 *
 * using engine = Engine.open({ base: "ZAR" });
 * engine.load({ edges: [{ from: "USD", to: "ZAR", rate: 18.5, source: "my-desk" }] });
 * engine.convert("USD", "ZAR", 100).result;   // 1850
 * ```
 *
 * That program starts no thread, opens no socket, reads no environment variable
 * and sends no packet — and not by convention. An ENGINE handle *refuses* the
 * "refresh" method; fetching needs a second, explicit construction with its own
 * handle:
 *
 * ```ts
 * using engine = Engine.open();
 * using refresher = engine.refresher({ sources: "ecb" });
 * await refresher.refresh();                  // THIS OPENS SOCKETS
 * ```
 *
 * Deno makes that split visible in a second way no other runtime here can: the
 * direct mode needs `--allow-ffi` and nothing else, so a reader of your run
 * command can see there is no `--allow-net` anywhere. The refresher opens
 * sockets from inside the library, below Deno's permission layer, which is
 * exactly why the ABI-level split matters — see README.md.
 *
 * SIDECAR (out-of-process, the `openrate` binary over loopback):
 *
 * ```ts
 * await using side = await Sidecar.start({ sources: "ecb" });
 * await side.convert("EUR", "ZAR", 100);
 * ```
 *
 * There is deliberately no `openrate_stream`: openrate answers from a snapshot
 * it already holds, so there is no incremental operation to stream. (llmux,
 * which shares this ABI shape, does have one.)
 *
 * JSON in, JSON out — the same JSON the HTTP API publishes.
 *
 * @module
 */

// ---------------------------------------------------------------------------
// Finding libopenrate
// ---------------------------------------------------------------------------

function goArch(): string {
  return Deno.build.arch === "aarch64" ? "arm64" : "amd64";
}

function libFileName(): string {
  // scripts/build-ffi.sh names artifacts libopenrate-<goos>-<goarch>.<ext>.
  const ext = Deno.build.os === "darwin" ? "dylib" : Deno.build.os === "windows" ? "dll" : "so";
  return `libopenrate-${Deno.build.os}-${goArch()}.${ext}`;
}

/**
 * Where the shared library will be loaded from: `OPENRATE_LIBRARY`, else a repo
 * checkout's `dist/ffi/`, else the bare name for the system loader.
 *
 * The env lookup is skipped unless env permission is already granted, so
 * `--allow-ffi` on its own neither prompts nor throws.
 *
 * openrate ships **no** windows/amd64 and **no** linux/arm64 library, and its
 * darwin/amd64 build has never been executed. The matrix is not llmux's — see
 * README.md.
 */
export function resolveLibrary(explicit?: string): string {
  if (explicit) return explicit;
  const fromEnv =
    Deno.permissions.querySync({ name: "env", variable: "OPENRATE_LIBRARY" }).state === "granted"
      ? Deno.env.get("OPENRATE_LIBRARY")
      : undefined;
  if (fromEnv) return fromEnv;
  const candidate = new URL(`../../dist/ffi/${libFileName()}`, import.meta.url);
  try {
    Deno.statSync(candidate);
    return candidate.pathname;
  } catch (e) {
    // NotFound means there is no checkout build here, so fall back to the bare
    // name. Anything else — in practice NotCapable, because statSync needs
    // --allow-read and the direct mode deliberately does not ask for it — means
    // we simply cannot look. Prefer the checkout path in that case and let
    // dlopen deliver the verdict, rather than silently skipping a library that
    // is sitting right there.
    if (e instanceof Deno.errors.NotFound) return libFileName();
    return candidate.pathname;
  }
}

// ---------------------------------------------------------------------------
// The ABI
// ---------------------------------------------------------------------------

// openrate_call appears twice: once blocking, once `nonblocking`. Engine
// methods answer from memory in microseconds and are fine on this thread;
// "refresh" and "ready" can take seconds and are not. Deno keys symbols by the
// JS name and takes the C name from `name`.
const SYMBOLS = {
  openrate_abi_version: { parameters: [], result: "pointer" },
  openrate_new: { parameters: ["buffer", "buffer"], result: "u64" },
  openrate_refresher_new: { parameters: ["u64", "buffer", "buffer"], result: "u64" },
  openrate_call: { parameters: ["u64", "buffer", "buffer", "buffer"], result: "pointer" },
  openrate_call_async: {
    name: "openrate_call",
    parameters: ["u64", "buffer", "buffer", "buffer"],
    result: "pointer",
    nonblocking: true,
  },
  openrate_close: { parameters: ["u64"], result: "void" },
  openrate_free: { parameters: ["pointer"], result: "void" },
  openrate_open_handles: { parameters: [], result: "u64" },
} as const;

type Lib = Deno.DynamicLibrary<typeof SYMBOLS>;

const _libs = new Map<string, Lib>();

function load(libPath: string): Lib {
  const cached = _libs.get(libPath);
  if (cached) return cached;
  const lib = Deno.dlopen(libPath, SYMBOLS);
  _libs.set(libPath, lib);
  return lib;
}

const encoder = new TextEncoder();

/**
 * A NUL-terminated UTF-8 copy of `s`, for a `const char*` parameter.
 *
 * Backed by a plain ArrayBuffer rather than `encoder.encode`'s
 * `ArrayBufferLike`: Deno's FFI refuses a buffer that might be shared, and it is
 * right to — the library reads it from another thread on a nonblocking call.
 */
function cstr(s: string): Uint8Array<ArrayBuffer> {
  const buf = new Uint8Array(new ArrayBuffer(s.length * 3 + 1));
  const { written } = encoder.encodeInto(s, buf);
  buf[written] = 0;
  return buf.subarray(0, written + 1);
}

/** A `char** err` slot: eight bytes for the library to write a pointer into. */
function errSlot(): BigUint64Array<ArrayBuffer> {
  return new BigUint64Array(new ArrayBuffer(8));
}

/** Read a C string openrate allocated, then free it. Freeing is not optional. */
function takeString(lib: Lib, p: Deno.PointerValue): string | null {
  if (p === null) return null;
  try {
    return Deno.UnsafePointerView.getCString(p);
  } finally {
    lib.symbols.openrate_free(p);
  }
}

/** Turn a populated `char** err` into an Error, freeing the message. */
function takeError(lib: Lib, slot: BigUint64Array<ArrayBuffer>, fallback: string): Error {
  // Error strings are plain UTF-8 text, NOT JSON. Do not parse them.
  const msg = takeString(lib, Deno.UnsafePointer.create(slot[0] ?? 0n));
  slot[0] = 0n;
  return new Error(msg ?? fallback);
}

/** The openrate version the loaded shared library was built from. */
export function abiVersion(libraryPath?: string): string {
  // Storage the library owns. Do NOT free it.
  const p = load(resolveLibrary(libraryPath)).symbols.openrate_abi_version();
  return p === null ? "" : Deno.UnsafePointerView.getCString(p);
}

/**
 * How many handles the library currently holds open. Diagnostic only — for a
 * test asserting it closed what it opened.
 */
export function openHandles(libraryPath?: string): number {
  return Number(load(resolveLibrary(libraryPath)).symbols.openrate_open_handles());
}

function body(request: unknown): Uint8Array<ArrayBuffer> | null {
  if (request == null) return null;
  return cstr(typeof request === "string" ? request : JSON.stringify(request));
}

function callSyncRaw(lib: Lib, h: bigint, method: string, request: unknown): unknown {
  const err = errSlot();
  const res = lib.symbols.openrate_call(h, cstr(method), body(request), err);
  if (res === null) throw takeError(lib, err, `openrate_call(${method}) failed`);
  return JSON.parse(takeString(lib, res) ?? "null");
}

async function callAsyncRaw(lib: Lib, h: bigint, method: string, request: unknown): Promise<unknown> {
  const err = errSlot();
  const res = await lib.symbols.openrate_call_async(h, cstr(method), body(request), err);
  if (res === null) throw takeError(lib, err, `openrate_call(${method}) failed`);
  return JSON.parse(takeString(lib, res) ?? "null");
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
  sources: SourceStatus[];
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
 * Every method here is synchronous, and that is a considered choice rather than
 * a limitation: they are answered from memory in microseconds, so a promise
 * would cost more than the call. The one thing that can take real time —
 * fetching — lives on {@link Refresher}, which has async variants.
 *
 * Disposable: `using engine = Engine.open()`.
 */
export class Engine implements Disposable {
  #lib: Lib;
  #h: bigint;
  #closed = false;

  private constructor(lib: Lib, h: bigint) {
    this.#lib = lib;
    this.#h = h;
  }

  static open(opts: EngineOptions = {}): Engine {
    const libPath = resolveLibrary(opts.libraryPath);
    const lib = load(libPath);
    if (opts.expectVersion) {
      const got = abiVersion(libPath);
      if (got !== opts.expectVersion) {
        throw new Error(
          `libopenrate at ${libPath} reports version ${got}, expected ${opts.expectVersion} — ` +
            "a stale library earlier on the load path is the usual cause",
        );
      }
    }
    const cfg: Record<string, unknown> = {};
    if (opts.base !== undefined) cfg.base = opts.base;
    if (opts.quiet !== undefined) cfg.quiet = opts.quiet;
    const err = errSlot();
    const h = lib.symbols.openrate_new(Object.keys(cfg).length ? cstr(JSON.stringify(cfg)) : null, err);
    if (h === 0n) throw takeError(lib, err, "openrate_new failed");
    return new Engine(lib, h);
  }

  /** The registry key of this engine. Handles are retired, never recycled. */
  get handle(): bigint {
    return this.#h;
  }

  #live(): bigint {
    if (this.#closed) throw new Error("openrate engine is closed");
    return this.#h;
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
    return callSyncRaw(this.#lib, this.#live(), "convert", req) as ConvertResult;
  }

  /**
   * The whole book against one base. `rates[X].rate` reads as
   * "1 base = rate units of X". Identical to `GET /api/v1/rates`, with one
   * deliberate difference: an unknown base is an error here, where the HTTP
   * endpoint answers 200 with an empty book. The library follows the Go API.
   */
  rates(base?: string): RatesResult {
    return callSyncRaw(this.#lib, this.#live(), "rates", base === undefined ? {} : { base }) as RatesResult;
  }

  /** Default base, build time, known currencies, and every refresher's status. */
  meta(): MetaResult {
    return callSyncRaw(this.#lib, this.#live(), "meta", {}) as MetaResult;
  }

  /**
   * Install rates you obtained yourself. The zero-network path; it has no HTTP
   * counterpart, because the server is read-only. `time` defaults to `built_at`,
   * and `built_at` to now.
   */
  load(doc: { edges: Edge[]; built_at?: string }): { built_at: string; currencies: string[] } {
    return callSyncRaw(this.#lib, this.#live(), "load", doc) as { built_at: string; currencies: string[] };
  }

  /**
   * Build a REFRESHER over this engine: its own handle, its own lifetime, and
   * the only object in openrate that can open a socket — though building it
   * still does not.
   */
  refresher(opts: RefresherOptions = {}): Refresher {
    const err = errSlot();
    const h = this.#lib.symbols.openrate_refresher_new(
      this.#live(),
      Object.keys(opts).length ? cstr(JSON.stringify(opts)) : null,
      err,
    );
    if (h === 0n) throw takeError(this.#lib, err, "openrate_refresher_new failed");
    return new Refresher(this.#lib, h);
  }

  /** Escape hatch for a method this class does not wrap. Same dispatch, same JSON. */
  call(method: string, request?: unknown): unknown {
    return callSyncRaw(this.#lib, this.#live(), method, request ?? {});
  }

  /**
   * Release the engine. Closing an engine also stops and releases every
   * refresher built over it, so closing in the "wrong" order cannot leak a
   * running loop. Idempotent.
   */
  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#lib.symbols.openrate_close(this.#h);
    this.#h = 0n;
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
 * {@link refresh} and {@link ready} are async because they can take seconds:
 * their symbols are declared `nonblocking`, so Deno runs them on a blocking-task
 * thread and the isolate keeps going. {@link status}, {@link start} and
 * {@link stop} return immediately and are synchronous.
 *
 * Disposable. Closing the engine closes this too, so either order is safe.
 */
export class Refresher implements Disposable {
  #lib: Lib;
  #h: bigint;
  #closed = false;

  constructor(lib: Lib, h: bigint) {
    this.#lib = lib;
    this.#h = h;
  }

  get handle(): bigint {
    return this.#h;
  }

  #live(): bigint {
    if (this.#closed) throw new Error("openrate refresher is closed");
    return this.#h;
  }

  /** What each source last did. Opens nothing, returns immediately. */
  status(): { sources: SourceStatus[] } {
    return callSyncRaw(this.#lib, this.#live(), "status", {}) as { sources: SourceStatus[] };
  }

  /**
   * One fetch of every source. THIS OPENS SOCKETS — from inside the library, so
   * Deno's `--allow-net` does not gate it.
   *
   * Off the event loop: the symbol is `nonblocking`, so the isolate keeps
   * serving while the sources answer.
   */
  async refresh(timeoutMs?: number): Promise<{ sources: SourceStatus[] }> {
    const req = timeoutMs === undefined ? {} : { timeout_ms: timeoutMs };
    return await callAsyncRaw(this.#lib, this.#live(), "refresh", req) as { sources: SourceStatus[] };
  }

  /**
   * Start the background refresh loop on the configured interval. Returns
   * immediately — the loop is a goroutine inside the library, not a Deno timer,
   * and it is the only thread openrate starts on its own.
   */
  start(): { running: boolean } {
    return callSyncRaw(this.#lib, this.#live(), "start", {}) as { running: boolean };
  }

  /** Stop the loop and wait for it to exit. */
  stop(): { running: boolean } {
    return callSyncRaw(this.#lib, this.#live(), "stop", {}) as { running: boolean };
  }

  /**
   * Resolve once the engine holds at least one currency. It does not fetch:
   * something must be refreshing, or it waits. `nonblocking`, so waiting costs
   * the isolate nothing.
   */
  async ready(timeoutMs?: number): Promise<{ ready: boolean }> {
    const req = timeoutMs === undefined ? {} : { timeout_ms: timeoutMs };
    return await callAsyncRaw(this.#lib, this.#live(), "ready", req) as { ready: boolean };
  }

  /** Release the refresher, stopping its loop. Idempotent. */
  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#lib.symbols.openrate_close(this.#h);
    this.#h = 0n;
  }

  [Symbol.dispose](): void {
    this.close();
  }
}

// ---------------------------------------------------------------------------
// Sidecar
// ---------------------------------------------------------------------------

export interface SidecarOptions {
  /** Fixed port; default is an ephemeral free port on 127.0.0.1. */
  port?: number;
  /** Default presentation base currency (`-base`, default "ZAR"). */
  base?: string;
  /** Comma-separated FX sources (`-sources`); empty means openrate's defaults. */
  sources?: string;
  /** Serve the embedded web console at `/` (`-ui`, default false here). */
  ui?: boolean;
  /** Extra arguments appended verbatim. */
  args?: string[];
  /** Extra environment for the child. */
  env?: Record<string, string>;
  /** Health-check timeout in milliseconds (default 10000). */
  timeoutMs?: number;
  /** Binary to run; default OPENRATE_BINARY, else "openrate" on PATH. */
  binary?: string;
}

async function freePort(): Promise<number> {
  const l = Deno.listen({ hostname: "127.0.0.1", port: 0 });
  const { port } = l.addr as Deno.NetAddr;
  l.close();
  await Promise.resolve();
  return port;
}

async function getJSON(url: string): Promise<unknown> {
  const res = await fetch(url);
  const text = await res.text();
  if (!res.ok) throw new Error(`openrate: HTTP ${res.status} from ${url}: ${text.trim().slice(0, 200)}`);
  return JSON.parse(text);
}

/**
 * The `openrate` binary in a child process, started and supervised for you.
 *
 * Async-disposable, so `await using side = await Sidecar.start()` kills the
 * child on the way out of the block.
 */
export class Sidecar implements AsyncDisposable {
  readonly baseURL: string;
  readonly apiBaseURL: string;
  #child: Deno.ChildProcess;
  #stopped = false;

  private constructor(child: Deno.ChildProcess, baseURL: string) {
    this.#child = child;
    this.baseURL = baseURL;
    this.apiBaseURL = baseURL + "/api/v1";
  }

  static async start(opts: SidecarOptions = {}): Promise<Sidecar> {
    const port = opts.port ?? (await freePort());
    const addr = `127.0.0.1:${port}`;
    const binary = opts.binary ?? Deno.env.get("OPENRATE_BINARY") ?? "openrate";

    const args = ["-addr", addr, `-ui=${opts.ui ? "true" : "false"}`];
    if (opts.base) args.push("-base", opts.base);
    if (opts.sources) args.push("-sources", opts.sources);
    if (opts.args) args.push(...opts.args);

    const child = new Deno.Command(binary, {
      args,
      env: opts.env ?? {},
      stdout: "inherit",
      stderr: "inherit",
    }).spawn();

    const base = `http://${addr}`;
    const deadline = Date.now() + (opts.timeoutMs ?? 10_000);
    for (;;) {
      try {
        const res = await fetch(base + "/healthz");
        await res.text();
        if (res.status === 200) return new Sidecar(child, base);
      } catch {
        // not listening yet
      }
      if (Date.now() > deadline) {
        try {
          child.kill();
        } catch { /* already gone */ }
        throw new Error(`openrate did not become healthy on ${addr} in time`);
      }
      await new Promise((r) => setTimeout(r, 50));
    }
  }

  /** `GET /api/v1/convert`. */
  convert(from: string, to: string, amount = 1): Promise<unknown> {
    const q = new URLSearchParams({ from, to, amount: String(amount) });
    return getJSON(`${this.apiBaseURL}/convert?${q.toString()}`);
  }

  /** `GET /api/v1/rates`. */
  rates(base?: string): Promise<unknown> {
    const q = base ? `?${new URLSearchParams({ base }).toString()}` : "";
    return getJSON(`${this.apiBaseURL}/rates${q}`);
  }

  /** `GET /api/v1/meta`. */
  meta(): Promise<unknown> {
    return getJSON(`${this.apiBaseURL}/meta`);
  }

  /**
   * Wait until the server has rates. A server refreshes on startup, so this is
   * a poll of /api/v1/meta rather than anything the API exposes. Returns the
   * currency count, which is 0 if the deadline passed with none.
   */
  async waitForRates(timeoutMs = 20_000): Promise<number> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const meta = await this.meta() as { currencies?: string[] };
      const n = meta.currencies?.length ?? 0;
      if (n > 0) return n;
      if (Date.now() > deadline) return 0;
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  /** Kill the child and wait for it. Idempotent. */
  async stop(): Promise<void> {
    if (this.#stopped) return;
    this.#stopped = true;
    try {
      this.#child.kill();
    } catch { /* already exited */ }
    await this.#child.status;
  }

  async [Symbol.asyncDispose](): Promise<void> {
    await this.stop();
  }
}
