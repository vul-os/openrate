using System;
using System.Text.RegularExpressions;
using System.Threading.Tasks;
using OpenRate;

namespace OpenRate.Examples
{
    /// <summary>
    /// openrate DIRECT — the engine inside this process, through libopenrate.
    ///
    /// The headline is the guarantee no sidecar can offer: an engine that
    /// provably sends no packets. This example spends most of its time there,
    /// feeding the engine rates with "load" and converting against them, and
    /// only then builds a refresher to show that fetching is a separate,
    /// explicit capability with its own handle.
    ///
    /// Offline by default. Set OPENRATE_ALLOW_NETWORK=1 to also fetch from ECB.
    ///
    /// Run it with sdks/dotnet/run-examples.sh.
    /// </summary>
    internal static class DirectRates
    {
        internal static Task<int> RunAsync()
        {
            string library = Direct.FindLibrary();
            Console.WriteLine($"library: {library}");

            bool allowNetwork = Environment.GetEnvironmentVariable("OPENRATE_ALLOW_NETWORK") == "1";

            // `using` disposes the SafeHandle on every path out — and disposing
            // an engine also releases every refresher built over it, so this is
            // the whole cleanup story even for the nested handle below.
            using (var engine = Direct.OpenEngine(baseCurrency: "ZAR", quiet: true, libraryPath: library))
            {
                Console.WriteLine($"abi version: {Direct.AbiVersion()}");
                Console.WriteLine($"open handles after creating the engine: {engine.OpenHandles()}");

                // 1. An engine with nothing in it says so rather than guessing.
                try
                {
                    engine.Convert("USD", "ZAR", 100);
                    Console.WriteLine("FAIL: an empty engine should not have converted anything");
                }
                catch (OpenRateException e)
                {
                    Console.WriteLine($"empty engine: {e.Message}");
                }

                // 2. "load" — the zero-network path. Rates you obtained yourself.
                const string edges = """
                    {"built_at":"2026-08-09T00:00:00Z","edges":[
                      {"from":"USD","to":"ZAR","rate":18.5,"source":"mine"},
                      {"from":"EUR","to":"USD","rate":1.09,"source":"mine"}
                    ]}
                    """;
                Console.WriteLine($"load: {OneLine(engine.Load(edges), 200)}");

                // 3. Direct, and across a hop the graph works out itself.
                Console.WriteLine($"USD->ZAR: {OneLine(engine.Convert("USD", "ZAR", 100), 220)}");
                Console.WriteLine($"EUR->ZAR (2 hops): {OneLine(engine.Convert("EUR", "ZAR"), 220)}");

                // 4. meta reports no sources: nothing is refreshing this engine.
                Console.WriteLine($"meta: {OneLine(engine.Meta(), 200)}");

                // 5. A refresher is a SEPARATE handle. Building it opens nothing.
                using (var refresher = engine.NewRefresher(sources: "ecb", quiet: true))
                {
                    Console.WriteLine($"open handles with a refresher: {engine.OpenHandles()}");
                    Console.WriteLine($"status before any fetch: {OneLine(refresher.Status(), 200)}");

                    // The ABI enforces the split rather than trusting the caller.
                    try
                    {
                        engine.Call("refresh", "{}");
                        Console.WriteLine("FAIL: an engine should refuse \"refresh\"");
                    }
                    catch (OpenRateException e)
                    {
                        Console.WriteLine($"engine refuses \"refresh\": {e.Message}");
                    }

                    if (allowNetwork)
                    {
                        Console.WriteLine("OPENRATE_ALLOW_NETWORK=1 — fetching from ECB for real…");
                        Console.WriteLine($"refresh: {OneLine(refresher.Refresh(30000), 240)}");
                        Console.WriteLine($"USD->EUR live: {OneLine(engine.Convert("USD", "EUR"), 200)}");
                    }
                    else
                    {
                        Console.WriteLine("OPENRATE_ALLOW_NETWORK is unset, so nothing was fetched.");
                        Console.WriteLine("This process has opened no socket on openrate's behalf.");
                    }
                }
                Console.WriteLine($"open handles after the refresher closed: {engine.OpenHandles()}");
            }

            // 6. Handle accounting, so the `using` above is evidence and not habit.
            using (var probe = Direct.OpenEngine(quiet: true, libraryPath: library))
            {
                ulong open = probe.OpenHandles();
                probe.Dispose();
                probe.Dispose(); // idempotent
                ulong after = probe.OpenHandles();
                Console.WriteLine($"handle accounting: {open} open, {after} after dispose");
                if (after != open - 1)
                {
                    Console.WriteLine("FAIL: disposing a handle should have decremented the count");
                }

                // 7. Use after dispose is a clean error, not a crash. Handle
                //    numbers are retired rather than recycled, so a stale one
                //    can never reach somebody else's object.
                try
                {
                    probe.Meta();
                    Console.WriteLine("FAIL: a disposed engine should not have answered");
                }
                catch (OpenRateException e)
                {
                    Console.WriteLine($"after dispose: {e.Message}");
                }
            }

            Console.WriteLine("done");
            return Task.FromResult(0);
        }

        internal static string OneLine(string s, int max)
        {
            string flat = Regex.Replace(s, @"\s+", " ");
            return flat.Length <= max ? flat : flat.Substring(0, max) + "…";
        }
    }
}
