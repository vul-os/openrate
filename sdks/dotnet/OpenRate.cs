using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text.Json;
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
            ///
            /// <para>Readiness is a separate endpoint: <c>GET /readyz</c>
            /// answers 200 once the snapshot holds currencies and 503 with the
            /// per-source failures until then.</para>
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
            // No proxy. HttpClient's default reads HTTP_PROXY from the
            // environment and does not bypass loopback, so on a machine behind
            // a corporate proxy every call to our own child would be posted to
            // that proxy instead. The child is on 127.0.0.1; nothing is in the
            // path but the loopback interface.
            _http = new HttpClient(new HttpClientHandler { UseProxy = false })
            {
                Timeout = TimeSpan.FromSeconds(30),
            };
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
            // The child listens on loopback and serves exactly one client: this
            // process. openrate's limiter is anti-scraping for a public
            // deployment, and there is no stranger here to throttle — while a
            // legitimate batch of conversions sails past the 120/min default and
            // takes a 429 from our own sidecar. Set Options.Env to put it back.
            psi.Environment["OPENRATE_RATELIMIT"] = "0";
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
        /// Poll <c>GET /readyz</c> until it answers 200. See
        /// <see cref="Options.WaitForRates"/> for why 200 from <c>/healthz</c>
        /// is not the same question.
        ///
        /// <para>A 503 carries a JSON body naming every source and why it has
        /// not produced a quote, so the timeout below can say
        /// <c>ecb: connection refused</c> instead of "timed out". That is why
        /// this does not go through <see cref="Get"/>, which is shaped for
        /// bodies you asked for rather than bodies attached to a failure.</para>
        ///
        /// <para>A fixed 150 ms interval is correct: <c>/readyz</c> sits
        /// outside <c>/api/</c>, which is the only prefix openrate's rate
        /// limiter looks at, so polling it cannot spend the budget the first
        /// real conversion needs.</para>
        /// </summary>
        private void WaitForRates(TimeSpan timeout)
        {
            DateTime deadline = DateTime.UtcNow + timeout;
            string last = "/readyz never answered";
            while (true)
            {
                if (_proc.HasExited)
                {
                    throw new OpenRateException("the openrate binary exited while fetching its first rates");
                }
                try
                {
                    using HttpResponseMessage resp =
                        _http.GetAsync(BaseUrl + "/readyz").GetAwaiter().GetResult();
                    string body = resp.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                    if (resp.StatusCode == HttpStatusCode.OK)
                    {
                        return;
                    }
                    last = WhyNotReady(body) ?? $"HTTP {(int)resp.StatusCode}: {body.Trim()}";
                }
                catch (Exception e)
                {
                    // Not listening yet, or listening and then gone. Either way
                    // the transport message is the most specific thing known.
                    last = e.Message;
                }
                if (DateTime.UtcNow >= deadline)
                {
                    break;
                }
                Thread.Sleep(150);
            }
            throw new OpenRateException(
                $"openrate has no rates after {Seconds(timeout)}: {last}. "
                + "Set Options.WaitForRates = false to start anyway.");
        }

        /// <summary>
        /// The human-readable half of a <c>/readyz</c> 503: its <c>reason</c>,
        /// followed by <c>name: last_error</c> for every source that has one.
        /// Returns null if the body is not the document we expect, so the
        /// caller can fall back to printing it raw.
        ///
        /// <para>This parses rather than scans. <c>last_error</c> holds a Go
        /// <c>net/http</c> error with the URL embedded <i>in quotes</i> —
        /// <c>Get \"https://…\": dial tcp …</c> — and a substring scan to the
        /// next quote returns <c>Get \</c>, the useless half. A source that has
        /// not been tried yet has no <c>last_error</c> key at all
        /// (<c>omitempty</c>), which is why nothing here prints a placeholder
        /// for a missing one.</para>
        /// </summary>
        internal static string? WhyNotReady(string readyJson)
        {
            string? reason;
            var errors = new List<string>();
            try
            {
                using JsonDocument doc = JsonDocument.Parse(readyJson);
                JsonElement root = doc.RootElement;
                if (root.ValueKind != JsonValueKind.Object) { return null; }
                reason = root.TryGetProperty("reason", out JsonElement r)
                    && r.ValueKind == JsonValueKind.String
                        ? r.GetString()
                        : null;
                if (root.TryGetProperty("sources", out JsonElement srcs)
                    && srcs.ValueKind == JsonValueKind.Array)
                {
                    foreach (JsonElement s in srcs.EnumerateArray())
                    {
                        if (s.ValueKind != JsonValueKind.Object) { continue; }
                        if (!s.TryGetProperty("last_error", out JsonElement e)
                            || e.ValueKind != JsonValueKind.String
                            || string.IsNullOrEmpty(e.GetString()))
                        {
                            continue;
                        }
                        string name = s.TryGetProperty("name", out JsonElement n)
                            && n.ValueKind == JsonValueKind.String
                                ? n.GetString() ?? "?"
                                : "?";
                        errors.Add($"{name}: {e.GetString()}");
                    }
                }
            }
            catch (JsonException)
            {
                return null;
            }

            if (errors.Count == 0) { return reason; }
            string joined = string.Join("; ", errors);
            return reason == null ? joined : $"{reason} ({joined})";
        }

        private static string Seconds(TimeSpan t) =>
            t.TotalSeconds.ToString("0.#", System.Globalization.CultureInfo.InvariantCulture) + "s";

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
