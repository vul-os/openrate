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

            try
            {
                return Task.FromResult(Run());
            }
            catch (OpenRateException e)
            {
                // Startup failed. The point of /readyz is that this message
                // names the source that failed and what it said, rather than
                // being a bare timeout — so print it, not a stack trace.
                Console.Error.WriteLine($"FAIL: {e.Message}");
                return Task.FromResult(1);
            }
        }

        private static int Run()
        {
            // `using` stops the child process on every path out, including a
            // failure mid-example.
            //
            // Start() waits for /healthz AND then for /readyz. Waiting only for
            // /healthz is the trap: openrate answers it 200 the moment it
            // binds, and conversions in that window come back "unknown or
            // unreachable currency pair", which reads as a bad currency code
            // rather than as "not ready yet". /readyz answers 200 only once the
            // snapshot actually holds currencies.
            using var rates = Sidecar.Start(new Sidecar.Options
            {
                Base = "ZAR",
                // Both keyless and public, so this example is runnable by
                // anyone rather than by whoever holds the API keys.
                Sources = "ecb,coinbase",
                // Settable from outside so the failure path is observable
                // without waiting a minute for it:
                //   OPENRATE_READY_TIMEOUT=5 HTTPS_PROXY=http://127.0.0.1:1 …
                RatesTimeout = ReadyTimeout(),
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
                return 1;
            }

            Console.WriteLine($"rates(EUR): {DirectRates.OneLine(rates.Rates("EUR"), 280)}");

            // The error paths are documents with a status, not exceptions.
            Console.WriteLine($"bad amount: {DirectRates.OneLine(rates.Get("/api/v1/convert?from=USD&to=ZAR&amount=NaN"), 160)}");
            Console.WriteLine($"unknown currency: {DirectRates.OneLine(rates.Convert("USD", "XYZ"), 160)}");

            Console.WriteLine("done");
            return 0;
        }

        /// <summary>
        /// $OPENRATE_READY_TIMEOUT in seconds, or null for the SDK's default.
        /// </summary>
        private static TimeSpan? ReadyTimeout()
        {
            string? s = Environment.GetEnvironmentVariable("OPENRATE_READY_TIMEOUT");
            if (string.IsNullOrEmpty(s)) { return null; }
            return double.TryParse(s, System.Globalization.NumberStyles.Float,
                System.Globalization.CultureInfo.InvariantCulture, out double secs)
                ? TimeSpan.FromSeconds(secs)
                : null;
        }
    }
}
