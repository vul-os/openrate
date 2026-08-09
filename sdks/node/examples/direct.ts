// openrate DIRECT mode on Node — libopenrate inside this process, over the C ABI.
//
//   npm run build && node examples/direct.ts
//
// Part 1 sends no packet at all, and that is the headline: an ENGINE cannot
// fetch. Part 2 builds a REFRESHER — a separate handle, a separate lifetime —
// and that one does open sockets. If this machine is offline, part 2 says so
// and the example still exits 0; part 1 never needed the network.

import { abiVersion, Engine, openHandles, resolveLibrary } from "../direct.js";

console.log(`node       ${process.version} on ${process.platform}/${process.arch}`);
console.log(`library    ${resolveLibrary()}`);
console.log(`abi        ${abiVersion()}\n`);

// ===========================================================================
// 1. The engine that provably fetches nothing
// ===========================================================================
{
  // `using` closes the handle on every exit path out of this block, throw
  // included. Without it, an exception between open and close leaks an engine.
  using engine = Engine.open({ base: "ZAR", quiet: true, expectVersion: abiVersion() });
  console.log(`engine     handle ${String(engine.handle)}, ${String(openHandles())} open\n`);

  // An empty engine is honest about being empty rather than guessing.
  try {
    engine.convert("USD", "ZAR", 100);
    console.log("empty       UNEXPECTED: converted with no rates loaded");
  } catch (e) {
    console.log(`empty       ${e instanceof Error ? e.message : "non-Error thrown"}`);
  }

  // The zero-network path: rates you obtained yourself.
  const loaded = engine.load({
    edges: [
      { from: "USD", to: "ZAR", rate: 18.5, source: "my-desk", time: "2026-08-09T00:00:00Z" },
      { from: "EUR", to: "USD", rate: 1.1535, source: "my-desk", time: "2026-08-09T00:00:00Z" },
    ],
    built_at: "2026-08-09T00:00:00Z",
  });
  console.log(`load        ${loaded.currencies.join(", ")} as of ${loaded.built_at}`);

  const direct = engine.convert("USD", "ZAR", 100);
  console.log(`convert     100 USD = ${String(direct.result)} ZAR`);
  // Every answer carries its own audit trail: the hops it took, the sources
  // behind each leg, and how old they are. That is the product.
  console.log(`            path ${direct.rate.path.join(" -> ")}, sources ${direct.rate.sources.join(",")}`);

  // Two hops, composed from the two edges above — nobody quoted EUR/ZAR.
  const hops = engine.convert("EUR", "ZAR", 100);
  console.log(`convert     100 EUR = ${hops.result.toFixed(2)} ZAR via ${hops.rate.path.join(" -> ")}`);
  console.log(`            ${String(hops.rate.hops)} hops, grade ${String(hops.rate.quality.grade)}`);

  const book = engine.rates("USD");
  console.log(`rates       base USD -> ${Object.keys(book.rates).sort().join(", ")}`);

  const meta = engine.meta();
  console.log(`meta        ${String(meta.currencies.length)} currencies, sources ${JSON.stringify(meta.sources)}`);

  // The split is enforced at the ABI, not by convention: an engine handle
  // refuses the refresher's methods outright.
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
  console.log(`refresher   handle ${String(refresher.handle)}, ${String(openHandles())} handles open`);

  // Before any fetch: named, but nothing has happened.
  console.log(`status      ${JSON.stringify(refresher.status().sources)}`);

  // refresh() BLOCKS the event loop for the whole fetch. In a server, call
  // start() instead — the loop runs on a goroutine inside the library and
  // returns immediately — and poll engine.meta() from a Node timer.
  let ticks = 0;
  const timer = setInterval(() => ticks++, 1);
  const began = Date.now();
  let fetched = false;
  try {
    const res = refresher.refresh(15_000);
    fetched = res.sources.some((s) => s.last_ok);
    console.log(`refresh     ${JSON.stringify(res.sources)}`);
  } catch (e) {
    console.log(`refresh     failed: ${e instanceof Error ? e.message : "non-Error thrown"}`);
  }
  clearInterval(timer);
  console.log(`            blocked the event loop for ${String(Date.now() - began)} ms; timer fired ${String(ticks)}x`);

  if (fetched) {
    const eur = engine.convert("EUR", "USD", 100);
    console.log(`convert     100 EUR = ${eur.result.toFixed(2)} USD`);
    console.log(`            as of ${eur.rate.as_of}, ${eur.rate.age_sec.toFixed(0)}s old, sources ${eur.rate.sources.join(",")}`);
    console.log(`meta        ${String(engine.meta().currencies.length)} currencies after one ECB fetch`);
  } else {
    console.log("convert     skipped — no source answered (offline?). Part 1 above never needed one.");
  }
}

// Closing an engine also closes every refresher built over it, so this is 0
// even though the block above never called refresher.close() by hand.
console.log(`\nhandles     ${String(openHandles())} open after both blocks exited`);
