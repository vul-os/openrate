// openrate SIDECAR mode on Bun — the `openrate` binary in a child process,
// HTTP over loopback.
//
//   bun run examples/sidecar.ts
//
// No FFI, no native code, no Go runtime in this process, no fork hazard, and it
// works on every platform the binary builds for — which is more platforms than
// the shared library covers.
//
// It needs the binary — set OPENRATE_BINARY, or build one:
//   go build -o /tmp/openrate ../../cmd/openrate
//
// A server refreshes on startup, so this example uses the network. If the
// machine is offline it reports WHICH source failed and how, then exits 0
// rather than pretending. To watch that path without unplugging anything:
//   NO_PROXY=127.0.0.1 HTTPS_PROXY=http://127.0.0.1:1 \
//     OPENRATE_READY_TIMEOUT_MS=3000 bun run examples/sidecar.ts
//
// NO_PROXY is not decoration: Bun's fetch honours HTTP(S)_PROXY for loopback
// URLs too, so without it this process cannot reach its own sidecar and the
// run fails at start(), before readiness is ever tested.

import { Sidecar } from "../index.ts";

interface Convert {
  result: number;
  rate: { path: string[]; sources: string[]; hops: number; age_sec: number; quality: { grade: string } };
}

console.log(`bun        ${Bun.version} on ${process.platform}/${process.arch}`);

// `await using` kills the child on the way out of this block, throw included.
await using side = await Sidecar.start({ base: "ZAR", sources: "ecb", ui: false });
console.log(`sidecar    ${side.baseURL}`);
console.log(`api        ${side.apiBaseURL}\n`);

// waitForRates polls /readyz on a timer — nothing blocks while it waits, which
// is the difference from direct mode's refresher.ready(). start() only waited
// for the listener; converting before this line would ask an empty book and get
// "unknown or unreachable currency pair" for every pair.
const readyTimeoutMs = Number(process.env.OPENRATE_READY_TIMEOUT_MS ?? 20_000);
let currencies = 0;
try {
  currencies = await side.waitForRates(readyTimeoutMs);
} catch (e) {
  // The message carries the 503's reason and each source's last error, so this
  // says WHICH source failed and how — not just "timed out".
  console.log(`rates       ${e instanceof Error ? e.message : "non-Error thrown"}`);
  console.log("            openrate says so rather than inventing a number; that is the design.");
  // And the exit code says it too. An example that prints a failure and exits 0
  // is the same false green as converting against an empty book: whatever runs
  // this in CI sees success.
  process.exitCode = 1;
}
if (currencies > 0) {
  console.log(`rates       ${currencies} currencies after the startup refresh`);

  const c = (await side.convert("EUR", "ZAR", 100)) as Convert;
  console.log(`convert     100 EUR = ${c.result.toFixed(2)} ZAR`);
  // The same audit trail direct mode returns — identical JSON, different
  // transport. That is the point of reusing the wire contract.
  console.log(`            path ${c.rate.path.join(" -> ")}, ${c.rate.hops} hops, sources ${c.rate.sources.join(",")}`);
  console.log(`            ${(c.rate.age_sec / 3600).toFixed(1)}h old, grade ${c.rate.quality.grade}`);

  const book = (await side.rates("USD")) as { base: string; rates: Record<string, unknown> };
  console.log(`rates       base ${book.base} -> ${Object.keys(book.rates).length} pairs`);
}

const meta = (await side.meta()) as { default_base: string; sources: unknown[] };
console.log(`meta        default base ${meta.default_base}, sources ${JSON.stringify(meta.sources)}`);

// The error path, and it is now the same one in both transports: an unknown
// BASE is a 404 here and an error in the library, as an unknown PAIR always was.
try {
  await side.convert("XXX", "ZAR", 1);
  console.log("error       UNEXPECTED: an unknown currency converted");
} catch (e) {
  console.log(`error       ${e instanceof Error ? e.message : "non-Error thrown"}`);
}
