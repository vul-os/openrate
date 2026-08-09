// openrate, sidecar — the `openrate` binary in a child process, HTTP over
// loopback, started and supervised for you.
//
//   import { Sidecar } from "openrate";
//
//   const side = await Sidecar.start({ sources: "ecb" });
//   await side.waitForRates();
//   const usd = await side.convert("USD", "ZAR", 100);
//   side.stop();
//
// No native code, no Go runtime in your process, no fork hazard, and it works
// on every platform the binary builds for — which is more platforms than the
// shared library covers. See README.md.
//
// Unlike direct mode there is no "can it send a packet" question: a server
// refreshes on startup and on its interval. That is what a server is for.
//
// The waitForRates() line is not optional politeness. start() waits for the
// LISTENER; the startup refresh has not finished when it returns, and a convert
// against an empty book is a 404 that reads like a bad currency code.

import { spawn, type ChildProcess } from "child_process";
import * as net from "net";

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
  /** Extra environment for the child; overrides what {@link Sidecar.start} sets. */
  env?: Record<string, string>;
  /**
   * How long to wait for the child to start LISTENING, in milliseconds
   * (default 10000). Waiting for rates is a separate step with its own
   * deadline — see {@link Sidecar.waitForRates}.
   */
  timeoutMs?: number;
  /** Binary to run; default OPENRATE_BINARY, else "openrate" on PATH. */
  binary?: string;
}

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      // A loopback host:port, so address() is always an AddressInfo here.
      const { port } = srv.address() as net.AddressInfo;
      srv.close(() => {
        resolve(port);
      });
    });
  });
}

async function getJSON(url: string): Promise<unknown> {
  const res = await fetch(url);
  const text = await res.text();
  if (!res.ok) throw new Error(`openrate: HTTP ${String(res.status)} from ${url}: ${text.trim().slice(0, 200)}`);
  return JSON.parse(text);
}

/** The parts of a `/readyz` 503 body this client acts on. */
interface ReadyBody {
  currencies?: number;
  reason?: string;
  sources?: { name?: string; last_error?: string }[];
}

/**
 * Turn a `/readyz` 503 body into one line a human can act on.
 *
 * `last_error` is `omitempty` on the wire, so a source that has simply not been
 * tried yet has no such key: those are dropped rather than printed as
 * `ecb: undefined`, and a body where nobody has failed yet degrades to the
 * server's `reason` alone.
 */
function describeNotReady(text: string): string {
  let body: ReadyBody;
  try {
    body = JSON.parse(text) as ReadyBody;
  } catch {
    // A 503 from something that is not openrate — a proxy, say. Show it raw.
    return text.trim().slice(0, 200) || "the server answered 503 with no body";
  }
  const reason = body.reason ?? "not ready";
  const causes = (body.sources ?? []).flatMap((s) => (s.last_error ? [`${s.name ?? "?"}: ${s.last_error}`] : []));
  return causes.length > 0 ? `${reason} (${causes.join("; ")})` : reason;
}

const seconds = (ms: number): string => `${String(Math.round(ms / 100) / 10)}s`;

/**
 * A running `openrate` server, owned by this process.
 *
 * `start()` picks a free loopback port, launches the binary with `-addr`,
 * inheriting the environment so paid-source API keys pass through, and polls
 * `/healthz` until it answers. That is LIVENESS. The server is not useful
 * until `waitForRates()` says so.
 */
export class Sidecar {
  readonly baseURL: string;
  readonly apiBaseURL: string;
  private child: ChildProcess;
  private stopped = false;

  private constructor(child: ChildProcess, baseURL: string) {
    this.child = child;
    this.baseURL = baseURL;
    this.apiBaseURL = baseURL + "/api/v1";
  }

  static async start(opts: SidecarOptions = {}): Promise<Sidecar> {
    const port = opts.port ?? (await freePort());
    const addr = `127.0.0.1:${String(port)}`;
    const binary = opts.binary ?? process.env.OPENRATE_BINARY ?? "openrate";

    const args = ["-addr", addr, `-ui=${opts.ui ? "true" : "false"}`];
    if (opts.base) args.push("-base", opts.base);
    if (opts.sources) args.push("-sources", opts.sources);
    if (opts.args) args.push(...opts.args);

    const child = spawn(binary, args, {
      // OPENRATE_RATELIMIT=0: the child listens on loopback and serves exactly
      // one client — this process. The 120/min default is anti-scraping for a
      // public deployment and there is no stranger here to throttle, while a
      // legitimate batch of conversions would sail past it and take a 429 from
      // our own sidecar. Pass env: { OPENRATE_RATELIMIT: "120" } to restore it.
      env: { ...process.env, OPENRATE_RATELIMIT: "0", ...opts.env },
      stdio: ["ignore", "inherit", "inherit"],
    });
    // Capture spawn failures (ENOENT for a missing binary) so they surface as a
    // rejected start() rather than an uncaught 'error' event.
    let spawnError: Error | null = null;
    child.on("error", (e) => {
      spawnError = e;
    });
    const currentSpawnError = (): Error | null => spawnError;

    const base = `http://${addr}`;
    const deadline = Date.now() + (opts.timeoutMs ?? 10_000);
    for (;;) {
      const failed = currentSpawnError();
      if (failed) throw failed;
      try {
        // /healthz is LIVENESS: it answers the instant the listener binds, with
        // no rates fetched yet. Readiness is waitForRates(), and start() does
        // not do it for you — a long first refresh should not look like a slow
        // spawn, and a caller with its own deadline gets to set it.
        const res = await fetch(base + "/healthz");
        await res.text();
        if (res.status === 200) return new Sidecar(child, base);
      } catch {
        // not listening yet
      }
      if (Date.now() > deadline) {
        child.kill();
        throw new Error(`openrate did not start listening on ${addr} within ${seconds(opts.timeoutMs ?? 10_000)}`);
      }
      await new Promise((r) => setTimeout(r, 50));
    }
  }

  /** `GET /api/v1/convert`. */
  async convert(from: string, to: string, amount = 1): Promise<unknown> {
    const q = new URLSearchParams({ from, to, amount: String(amount) });
    return getJSON(`${this.apiBaseURL}/convert?${q.toString()}`);
  }

  /** `GET /api/v1/rates`. */
  async rates(base?: string): Promise<unknown> {
    const q = base ? `?${new URLSearchParams({ base }).toString()}` : "";
    return getJSON(`${this.apiBaseURL}/rates${q}`);
  }

  /** `GET /api/v1/meta`. */
  async meta(): Promise<unknown> {
    return getJSON(`${this.apiBaseURL}/meta`);
  }

  /**
   * Wait until the server can actually answer a conversion, and return how many
   * currencies it holds. This is READINESS: `GET /readyz`, which is 200 once the
   * startup refresh has put rates in the snapshot and 503 with the per-source
   * outcomes until then. It is a Node timer, so nothing blocks while it waits.
   *
   * A fixed 150 ms interval is right here: `/readyz` sits outside `/api/`, so
   * `guard()`'s rate limiter never sees it and polling cannot spend the budget
   * this call is waiting to hand to `convert()`.
   *
   * On timeout it THROWS, carrying the last thing the server said — the 503's
   * `reason` plus every source that has an error, or the transport failure if
   * the server never answered at all:
   *
   *     openrate has no rates after 20s: no rates yet: no source has returned a
   *     usable quote (ecb: dial tcp 127.0.0.1:1: connect: connection refused)
   *
   * The alternative, a bare timeout or a returned 0, makes the caller guess
   * between "offline", "wrong sources" and "just slow".
   */
  async waitForRates(timeoutMs = 20_000): Promise<number> {
    const deadline = Date.now() + timeoutMs;
    let cause = "the server never answered /readyz";
    for (;;) {
      try {
        // Not getJSON(): it discards the body of a non-2xx response, and on a
        // 503 that body is the entire diagnosis.
        const res = await fetch(`${this.baseURL}/readyz`);
        const text = await res.text();
        if (res.status === 200) return (JSON.parse(text) as ReadyBody).currencies ?? 0;
        cause = describeNotReady(text);
      } catch (e) {
        // Not listening yet, or gone. Either way it is the current cause.
        cause = e instanceof Error ? e.message : String(e);
      }
      if (Date.now() > deadline) {
        throw new Error(`openrate has no rates after ${seconds(timeoutMs)}: ${cause}`);
      }
      await new Promise((r) => setTimeout(r, 150));
    }
  }

  /** Kill the child. Idempotent. */
  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    this.child.kill();
  }
}
