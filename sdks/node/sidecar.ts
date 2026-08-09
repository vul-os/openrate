// openrate, sidecar — the `openrate` binary in a child process, HTTP over
// loopback, started and supervised for you.
//
//   import { Sidecar } from "openrate";
//
//   const side = await Sidecar.start({ sources: "ecb" });
//   const usd = await side.convert("USD", "ZAR", 100);
//   side.stop();
//
// No native code, no Go runtime in your process, no fork hazard, and it works
// on every platform the binary builds for — which is more platforms than the
// shared library covers. See README.md.
//
// Unlike direct mode there is no "can it send a packet" question: a server
// refreshes on startup and on its interval. That is what a server is for.

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
  /** Extra environment for the child. */
  env?: Record<string, string>;
  /** Health-check timeout in milliseconds (default 10000). */
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

/**
 * A running `openrate` server, owned by this process.
 *
 * `start()` picks a free loopback port, launches the binary with `-addr`,
 * inheriting the environment so paid-source API keys pass through, and polls
 * `/healthz` until it answers.
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
      env: { ...process.env, ...opts.env },
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
        const res = await fetch(base + "/healthz");
        await res.text();
        if (res.status === 200) return new Sidecar(child, base);
      } catch {
        // not listening yet
      }
      if (Date.now() > deadline) {
        child.kill();
        throw new Error(`openrate did not become healthy on ${addr} in time`);
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
   * Wait until the server has rates. A server refreshes on startup, so this is
   * a poll of /api/v1/meta rather than anything the API exposes — and it is a
   * Node timer, so nothing blocks while it waits.
   *
   * Returns the currency count, which is 0 if the deadline passed with none.
   */
  async waitForRates(timeoutMs = 20_000): Promise<number> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const meta = (await this.meta()) as { currencies?: string[] };
      const n = meta.currencies?.length ?? 0;
      if (n > 0) return n;
      if (Date.now() > deadline) return 0;
      await new Promise((r) => setTimeout(r, 250));
    }
  }

  /** Kill the child. Idempotent. */
  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    this.child.kill();
  }
}
