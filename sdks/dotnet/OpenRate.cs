using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Threading;

namespace OpenRate
{
    /// <summary>
    /// openrate as a <b>child process</b> on 127.0.0.1, spoken to over HTTP.
    ///
    /// <para><b>The recommended default for .NET.</b> No native library, no
    /// unsafe code, no platform matrix — it works on Windows, where the shared
    /// library does not exist.</para>
    ///
    /// <code>
    /// using var rates = Sidecar.Start(new Sidecar.Options { Base = "ZAR" });
    /// string converted = rates.Convert("USD", "ZAR", 100);
    /// </code>
    ///
    /// <para><b>This fetches.</b> <c>openrate serve</c> starts its refresher at
    /// startup, so standing one up means outbound requests. If "no packets
    /// unless I ask" is the property you want, that is
    /// <see cref="OpenRateEngine"/> on the direct path, and it is a guarantee
    /// rather than a setting.</para>
    ///
    /// <para>Unlike llmux's .NET SDK this is <b>not a process-wide
    /// singleton</b>: you get an instance you own and dispose. Several servers
    /// with different bases or source sets is a normal thing to want from a
    /// rates service, and a static singleton would forbid it.</para>
    /// </summary>
    public sealed class Sidecar : IDisposable
    {
        public const string Version = "0.1.2";

        public sealed class Options
        {
            /// <summary>Fixed port; defaults to an ephemeral free port.</summary>
            public int? Port { get; set; }
            /// <summary>Default presentation base currency (server default: ZAR).</summary>
            public string? Base { get; set; }
            /// <summary>Comma-separated FX sources; empty means openrate's defaults.</summary>
            public string? Sources { get; set; }
            /// <summary>Serve the embedded web console. Off: an API client does not need it.</summary>
            public bool Ui { get; set; }
            /// <summary>Extra environment for the child process.</summary>
            public IDictionary<string, string>? Env { get; set; }
            /// <summary>How long to wait for /healthz (default 30s).</summary>
            public TimeSpan? Timeout { get; set; }
            /// <summary>
            /// Also wait until the server actually holds rates, not merely until
            /// it is listening. Default true, and it is the difference between a
            /// working client and a mystery.
            ///
            /// <para><c>/healthz</c> is a LIVENESS probe: openrate answers 200
            /// the moment it binds, while its first fetch is still in flight,
            /// and every conversion in that window returns
            /// <c>{"error":"unknown or unreachable currency pair"}</c> — which
            /// reads as a wrong currency code, not as "not ready yet".</para>
            /// </summary>
            public bool WaitForRates { get; set; } = true;
            /// <summary>How long to wait for the first rates (default 60s).</summary>
            public TimeSpan? RatesTimeout { get; set; }
        }

        private readonly Process _proc;
        private readonly HttpClient _http;
        private readonly EventHandler _exitHandler;
        private int _disposed;

        /// <summary>http://127.0.0.1:&lt;port&gt;</summary>
        public string BaseUrl { get; }

        private Sidecar(Process proc, string baseUrl)
        {
            _proc = proc;
            BaseUrl = baseUrl;
            _http = new HttpClient { Timeout = TimeSpan.FromSeconds(30) };
            _exitHandler = (_, _) => Terminate();
            AppDomain.CurrentDomain.ProcessExit += _exitHandler;
        }

        /// <summary>Spawn openrate and wait for it to be ready.</summary>
        public static Sidecar Start(Options? opts = null)
        {
            opts ??= new Options();
            int port = opts.Port ?? FreePort();
            string addr = $"127.0.0.1:{port}";

            var psi = new ProcessStartInfo
            {
                FileName = BinaryPath(),
                UseShellExecute = false,
            };
            psi.ArgumentList.Add("-addr");
            psi.ArgumentList.Add(addr);
            psi.ArgumentList.Add("-ui=" + (opts.Ui ? "true" : "false"));
            if (opts.Base != null) { psi.Environment["OPENRATE_BASE"] = opts.Base; }
            if (opts.Sources != null) { psi.Environment["OPENRATE_SOURCES"] = opts.Sources; }
            if (opts.Env != null)
            {
                foreach (var kv in opts.Env) { psi.Environment[kv.Key] = kv.Value; }
            }

            Process proc;
            try
            {
                proc = Process.Start(psi)
                    ?? throw new OpenRateException("failed to spawn the openrate binary");
            }
            catch (Exception e) when (e is not OpenRateException)
            {
                throw new OpenRateException("failed to spawn the openrate binary", e);
            }

            var sidecar = new Sidecar(proc, $"http://{addr}");
            try
            {
                sidecar.WaitHealthy(opts.Timeout ?? TimeSpan.FromSeconds(30));
                if (opts.WaitForRates)
                {
                    sidecar.WaitForRates(opts.RatesTimeout ?? TimeSpan.FromSeconds(60));
                }
            }
            catch
            {
                // Never leave a child process behind because startup failed.
                sidecar.Dispose();
                throw;
            }
            return sidecar;
        }

        /// <summary>GET /api/v1/meta.</summary>
        public string Meta() => Get("/api/v1/meta");

        /// <summary>GET /api/v1/rates?base=…</summary>
        public string Rates(string baseCurrency) =>
            Get("/api/v1/rates?base=" + Uri.EscapeDataString(baseCurrency));

        /// <summary>GET /api/v1/convert?from=…&amp;to=…&amp;amount=…</summary>
        public string Convert(string from, string to, double amount = 1) =>
            Get($"/api/v1/convert?from={Uri.EscapeDataString(from)}"
                + $"&to={Uri.EscapeDataString(to)}"
                + $"&amount={amount.ToString(System.Globalization.CultureInfo.InvariantCulture)}");

        /// <summary>One GET against the running server; the body, whatever the status.</summary>
        public string Get(string path)
        {
            try
            {
                return _http.GetStringAsync(BaseUrl + path).GetAwaiter().GetResult();
            }
            catch (HttpRequestException e)
            {
                // openrate answers 4xx with a JSON error document, which is a
                // result rather than an exception. Hand it back.
                if (e.StatusCode != null)
                {
                    using var resp = _http.GetAsync(BaseUrl + path).GetAwaiter().GetResult();
                    return resp.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                }
                throw new OpenRateException($"GET {path} failed", e);
            }
        }

        /// <summary>Stop the child process. Idempotent — use <c>using</c>.</summary>
        public void Dispose()
        {
            if (Interlocked.Exchange(ref _disposed, 1) == 1)
            {
                return;
            }
            Terminate();
            AppDomain.CurrentDomain.ProcessExit -= _exitHandler;
            _http.Dispose();
        }

        private void Terminate()
        {
            try
            {
                if (!_proc.HasExited)
                {
                    _proc.Kill(entireProcessTree: true);
                    _proc.WaitForExit(5000);
                }
            }
            catch
            {
                // Best effort: the process may already be gone.
            }
        }

        private void WaitHealthy(TimeSpan timeout)
        {
            DateTime deadline = DateTime.UtcNow + timeout;
            string last = "connection refused";
            while (DateTime.UtcNow < deadline)
            {
                if (_proc.HasExited)
                {
                    throw new OpenRateException(
                        $"the openrate binary exited before becoming healthy (status {_proc.ExitCode})");
                }
                try
                {
                    using HttpResponseMessage resp =
                        _http.GetAsync(BaseUrl + "/healthz").GetAwaiter().GetResult();
                    if (resp.StatusCode == HttpStatusCode.OK)
                    {
                        return;
                    }
                    last = $"status {(int)resp.StatusCode}";
                }
                catch (Exception e)
                {
                    last = e.Message;
                }
                Thread.Sleep(100);
            }
            throw new OpenRateException($"openrate did not become healthy within {timeout}: {last}");
        }

        /// <summary>
        /// Poll /api/v1/meta until the currency list is non-empty. See
        /// <see cref="Options.WaitForRates"/>: this is a readiness check built
        /// on a liveness endpoint, because openrate publishes no readiness
        /// endpoint of its own. The direct path's refresher does — its
        /// <c>ready</c> method.
        /// </summary>
        private void WaitForRates(TimeSpan timeout)
        {
            DateTime deadline = DateTime.UtcNow + timeout;
            string last = "no rates yet";
            while (DateTime.UtcNow < deadline)
            {
                if (_proc.HasExited)
                {
                    throw new OpenRateException("the openrate binary exited while fetching its first rates");
                }
                try
                {
                    if (HasCurrencies(Get("/api/v1/meta")))
                    {
                        return;
                    }
                    last = "the currency list is still empty";
                }
                catch (Exception e)
                {
                    last = e.Message;
                }
                Thread.Sleep(250);
            }
            throw new OpenRateException(
                $"openrate is listening but holds no rates after {timeout} ({last}). "
                + "Every source fetch failed — the usual cause is no outbound network. "
                + "Set Options.WaitForRates = false to start anyway.");
        }

        /// <summary>
        /// True if the meta document's <c>currencies</c> array has anything in
        /// it. Deliberately not a JSON parse: this SDK has no JSON dependency,
        /// and "is this array empty" does not need one.
        /// </summary>
        internal static bool HasCurrencies(string metaJson)
        {
            int at = metaJson.IndexOf("\"currencies\"", StringComparison.Ordinal);
            if (at < 0) { return false; }
            int open = metaJson.IndexOf('[', at);
            if (open < 0) { return false; }
            for (int i = open + 1; i < metaJson.Length; i++)
            {
                char c = metaJson[i];
                if (c == ']') { return false; }
                if (!char.IsWhiteSpace(c)) { return true; }
            }
            return false;
        }

        // ---------------------------------------------------------------- lookup

        private static string BinaryPath()
        {
            string? env = Environment.GetEnvironmentVariable("OPENRATE_BINARY");
            if (!string.IsNullOrEmpty(env)) { return env!; }

            bool windows = RuntimeInformation.IsOSPlatform(OSPlatform.Windows);
            string name = windows ? "openrate.exe" : "openrate";
            string bundled = Path.Combine(AppContext.BaseDirectory, "bin", name);
            if (File.Exists(bundled)) { return bundled; }

            string? found = Which(name);
            if (found != null) { return found; }

            throw new OpenRateException(
                "openrate binary not found. Set OPENRATE_BINARY, or build it: "
                + "`go build -o sdks/dotnet/bin/openrate ./cmd/openrate`");
        }

        private static string? Which(string cmd)
        {
            string? path = Environment.GetEnvironmentVariable("PATH");
            if (path == null) { return null; }
            foreach (string dir in path.Split(Path.PathSeparator))
            {
                string candidate = Path.Combine(dir, cmd);
                if (File.Exists(candidate)) { return candidate; }
            }
            return null;
        }

        private static int FreePort()
        {
            var listener = new TcpListener(IPAddress.Loopback, 0);
            listener.Start();
            int port = ((IPEndPoint)listener.LocalEndpoint).Port;
            listener.Stop();
            return port;
        }
    }

    /// <summary>Thrown when openrate cannot be located, started, or made to answer.</summary>
    public sealed class OpenRateException : Exception
    {
        public OpenRateException(string message) : base(message) { }
        public OpenRateException(string message, Exception inner) : base(message, inner) { }
    }
}
