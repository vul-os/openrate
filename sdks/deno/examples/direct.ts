// openrate DIRECT mode on Deno — libopenrate inside this process, over the C ABI.
//
//   deno task example:direct
//   # or, spelled out — note what is NOT in this list:
//   deno run --allow-ffi examples/direct.ts
//
// Part 1 sends no packet. Part 2 builds a REFRESHER, which does. Both run under
// `--allow-ffi` alone: no --allow-net, no --allow-read, no --allow-env. That is
// worth staring at, because it is also the catch — the refresher's sockets are
// opened by Go inside the library, below Deno's permission layer. Deno cannot
// gate them. openrate's engine/refresher split is what gates them, and it is
// enforced at the ABI. See README.md.

import { abiVersion, Engine, openHandles, resolveLibrary } from "../mod.ts";

console.log(`deno       ${Deno.version.deno} on ${Deno.build.os}/${Deno.build.arch}`);
console.log(`library    ${resolveLibrary()}`);
console.log(`abi        ${abiVersion()}\n`);

// ===========================================================================
// 1. The engine that provably fetches nothing
// ===========================================================================
{
  // `using` closes the handle on every exit path out of this block, throw
  // included. Without it, an exception between open and close leaks an engine.
  using engine = Engine.open({ base: "ZAR", quiet: true, expectVersion: abiVersion() });
  console.log(`engine     handle ${engine.handle}, ${openHandles()} open\n`);

  // An empty engine is honest about being empty rather than guessing.
  try {
    engine.convert("USD", "ZAR", 100);
    console.log("empty       UNEXPECTED: converted with no rates loaded");
  } catch (e) {
    console.log(`empty       ${e instanceof Error ? e.message : "non-Error thrown"}`);
  }

  const loaded = engine.load({
    edges: [
      { from: "USD", to: "ZAR", rate: 18.5, source: "my-desk", time: "2026-08-09T00:00:00Z" },
      { from: "EUR", to: "USD", rate: 1.1535, source: "my-desk", time: "2026-08-09T00:00:00Z" },
    ],
    built_at: "2026-08-09T00:00:00Z",
  });
  console.log(`load        ${loaded.currencies.join(", ")} as of ${loaded.built_at}`);

  const direct = engine.convert("USD", "ZAR", 100);
  console.log(`convert     100 USD = ${direct.result} ZAR`);
  // Every answer carries its own audit trail. That is the product.
  console.log(`            path ${direct.rate.path.join(" -> ")}, sources ${direct.rate.sources.join(",")}`);

  // Two hops, composed from the two edges above — nobody quoted EUR/ZAR.
  const hops = engine.convert("EUR", "ZAR", 100);
  console.log(`convert     100 EUR = ${hops.result.toFixed(2)} ZAR via ${hops.rate.path.join(" -> ")}`);
  console.log(`            ${hops.rate.hops} hops, grade ${hops.rate.quality.grade}`);

  const book = engine.rates("USD");
  console.log(`rates       base USD -> ${Object.keys(book.rates).sort().join(", ")}`);

  const meta = engine.meta();
  console.log(`meta        ${meta.currencies.length} currencies, sources ${JSON.stringify(meta.sources)}`);

  // The split is enforced at the ABI, not by convention.
  try {
    engine.call("refresh");
    console.log("refuses     UNEXPECTED: an engine accepted refresh");
  } catch (e) {
    console.log(`refuses     ${e instanceof Error ? e.message : "non-Error thrown"}\n`);
  }
}

// ===========================================================================
// 2. A refresher — the only thing here that can open a socket
// ===========================================================================
{
  using engine = Engine.open({ base: "ZAR", quiet: true });
  using refresher = engine.refresher({ sources: "ecb", fetch_timeout_ms: 15_000 });
  console.log(`refresher   handle ${refresher.handle}, ${openHandles()} handles open`);
  console.log(`status      ${JSON.stringify(refresher.status().sources)}`);

  // refresh() is `nonblocking`, so the isolate keeps running while ECB answers.
  // The tick count below is the proof — on Node the same call measures 0.
  let ticks = 0;
  const timer = setInterval(() => ticks++, 1);
  const began = Date.now();
  let fetched = false;
  try {
    const res = await refresher.refresh(15_000);
    fetched = res.sources.some((s) => s.last_ok);
    console.log(`refresh     ${JSON.stringify(res.sources)}`);
  } catch (e) {
    console.log(`refresh     failed: ${e instanceof Error ? e.message : "non-Error thrown"}`);
  }
  clearInterval(timer);
  console.log(`            took ${Date.now() - began} ms; the event loop ticked ${ticks}x meanwhile`);

  if (fetched) {
    const eur = engine.convert("EUR", "USD", 100);
    console.log(`convert     100 EUR = ${eur.result.toFixed(2)} USD`);
    console.log(
      `            as of ${eur.rate.as_of}, ${eur.rate.age_sec.toFixed(0)}s old, sources ${
        eur.rate.sources.join(",")
      }`,
    );
    console.log(`meta        ${engine.meta().currencies.length} currencies after one ECB fetch`);
  } else {
    console.log("convert     skipped — no source answered (offline?). Part 1 above never needed one.");
  }
}

// Closing an engine also closes every refresher built over it, so this is 0
// even though the block above never called refresher.close() by hand.
console.log(`\nhandles     ${openHandles()} open after both blocks exited`);
