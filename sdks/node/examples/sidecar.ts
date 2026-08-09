// openrate SIDECAR mode on Node — the `openrate` binary in a child process,
// HTTP over loopback.
//
//   npm run build && node examples/sidecar.ts
//
// No FFI, no native dependency, no Go runtime in this process, and no fork
// hazard. It needs the binary — set OPENRATE_BINARY, or build one:
//   go build -o /tmp/openrate ../../cmd/openrate
//
// A server refreshes on startup, so this example DOES use the network. If the
// machine is offline it reports WHICH source failed and how, then exits 0
// rather than pretending. To watch that path without unplugging anything:
//   HTTPS_PROXY=http://127.0.0.1:1 OPENRATE_READY_TIMEOUT_MS=3000 node examples/sidecar.ts

import { Sidecar } from "../index.js";

interface Convert {
  from: string;
  to: string;
  amount: number;
  result: number;
  rate: { path: string[]; sources: string[]; hops: number; age_sec: number; quality: { grade: string } };
}

console.log(`node       ${process.version} on ${process.platform}/${process.arch}`);

// -ui=false leaves only the JSON API. -sources ecb keeps the example to one
// upstream so a slow source cannot stretch it out.
const side = await Sidecar.start({ base: "ZAR", sources: "ecb", ui: false });
try {
  console.log(`sidecar    ${side.baseURL}`);
  console.log(`api        ${side.apiBaseURL}\n`);

  // waitForRates polls /readyz on a Node timer — the event loop is never
  // blocked, which is the whole difference from direct mode's refresher.ready().
  // start() only waited for the listener; converting before this line would ask
  // an empty book and get "unknown or unreachable currency pair" for every pair.
  const readyTimeoutMs = Number(process.env.OPENRATE_READY_TIMEOUT_MS ?? 20_000);
  let currencies = 0;
  try {
    currencies = await side.waitForRates(readyTimeoutMs);
  } catch (e) {
    // The message carries the 503's reason and each source's last error, so
    // this says WHICH source failed and how — not just "timed out".
    console.log(`rates       ${e instanceof Error ? e.message : "non-Error thrown"}`);
    console.log("            openrate says so rather than inventing a number; that is the design.");
    // And the exit code says it too. An example that prints a failure and exits
    // 0 is the same false green as converting against an empty book: whatever
    // runs this in CI sees success.
    process.exitCode = 1;
  }
  if (currencies > 0) {
    console.log(`rates       ${String(currencies)} currencies after the startup refresh`);

    const c = (await side.convert("EUR", "ZAR", 100)) as Convert;
    console.log(`convert     100 EUR = ${c.result.toFixed(2)} ZAR`);
    // The same audit trail direct mode returns — identical JSON, different
    // transport. That is the point of reusing the wire contract.
    console.log(`            path ${c.rate.path.join(" -> ")}, ${String(c.rate.hops)} hops, sources ${c.rate.sources.join(",")}`);
    console.log(`            ${(c.rate.age_sec / 3600).toFixed(1)}h old, grade ${c.rate.quality.grade}`);

    const book = (await side.rates("USD")) as { base: string; rates: Record<string, unknown> };
    console.log(`rates       base ${book.base} -> ${String(Object.keys(book.rates).length)} pairs`);
  }

  const meta = (await side.meta()) as { default_base: string; sources: { name: string; edges?: number }[] };
  console.log(`meta        default base ${meta.default_base}, sources ${JSON.stringify(meta.sources)}`);

  // The error path. Unlike the library, the HTTP endpoint answers 200 with an
  // empty book for an unknown base — but an unknown *pair* is still an error.
  try {
    await side.convert("XXX", "ZAR", 1);
    console.log("error       UNEXPECTED: an unknown currency converted");
  } catch (e) {
    console.log(`error       ${e instanceof Error ? e.message : "non-Error thrown"}`);
  }
} finally {
  side.stop();
}
