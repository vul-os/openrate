using System;
using System.Threading.Tasks;
using OpenRate;

namespace OpenRate.Examples
{
    /// <summary>
    /// openrate SIDECAR — the server as a child process on 127.0.0.1, over HTTP.
    /// The recommended default for .NET.
    ///
    /// No native library, no unsafe code, no platform matrix. It works on
    /// Windows, where the shared library does not exist at all.
    ///
    /// THIS FETCHES: `openrate serve` starts its refresher at startup, so this
    /// example needs a network and says so rather than failing mysteriously.
    ///
    /// Run it with sdks/dotnet/run-examples.sh.
    /// </summary>
    internal static class SidecarRates
    {
        internal static Task<int> RunAsync()
        {
            Console.WriteLine("starting openrate (this fetches — it needs a network)…");

            // `using` stops the child process on every path out, including a
            // failure mid-example.
            //
            // Start() waits for /healthz AND for the currency list to fill.
            // Waiting only for /healthz is the trap: openrate answers 200 the
            // moment it binds, and conversions in that window come back
            // "unknown or unreachable currency pair", which reads as a bad
            // currency code rather than as "not ready yet".
            using var rates = Sidecar.Start(new Sidecar.Options
            {
                Base = "ZAR",
                // Both keyless and public, so this example is runnable by
                // anyone rather than by whoever holds the API keys.
                Sources = "ecb,coinbase",
            });

            Console.WriteLine($"sidecar: {rates.BaseUrl}");
            Console.WriteLine($"meta: {DirectRates.OneLine(rates.Meta(), 280)}");

            string converted = rates.Convert("USD", "ZAR", 100);
            Console.WriteLine($"USD->ZAR 100: {DirectRates.OneLine(converted, 300)}");
            if (converted.Contains("\"error\"", StringComparison.Ordinal))
            {
                // An example that prints an error object and exits 0 is the
                // false green this repo keeps finding.
                Console.Error.WriteLine("FAIL: the sidecar was ready but the conversion still errored");
                return Task.FromResult(1);
            }

            Console.WriteLine($"rates(EUR): {DirectRates.OneLine(rates.Rates("EUR"), 280)}");

            // The error paths are documents with a status, not exceptions.
            Console.WriteLine($"bad amount: {DirectRates.OneLine(rates.Get("/api/v1/convert?from=USD&to=ZAR&amount=NaN"), 160)}");
            Console.WriteLine($"unknown currency: {DirectRates.OneLine(rates.Convert("USD", "XYZ"), 160)}");

            Console.WriteLine("done");
            return Task.FromResult(0);
        }
    }
}
