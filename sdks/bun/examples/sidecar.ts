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
// machine is offline it says so and exits 0 rather than pretending.

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

// waitForRates polls /api/v1/meta on a timer — nothing blocks while it waits,
// which is the difference from direct mode's refresher.ready().
const currencies = await side.waitForRates(20_000);
if (currencies === 0) {
  console.log("rates       none arrived within 20s — the sidecar could not reach its sources (offline?)");
  console.log("            openrate says so rather than inventing a number; that is the design.");
} else {
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

// The error path. Unlike the library, the HTTP endpoint answers 200 with an
// empty book for an unknown BASE — but an unknown PAIR is an error in both.
try {
  await side.convert("XXX", "ZAR", 1);
  console.log("error       UNEXPECTED: an unknown currency converted");
} catch (e) {
  console.log(`error       ${e instanceof Error ? e.message : "non-Error thrown"}`);
}
