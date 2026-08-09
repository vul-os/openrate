using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Threading;

namespace OpenRate
{
    /// <summary>
    /// openrate <b>in this process</b>, through the C ABI of
    /// <c>libopenrate</c>.
    ///
    /// <para><see cref="Sidecar"/> is the other path: it spawns the
    /// <c>openrate</c> binary and talks HTTP. <b>On .NET the sidecar is the
    /// recommended default</b> — openrate's shared library exists for one
    /// platform and Windows is not it. See README.md.</para>
    ///
    /// <para><b>The exception, and the reason this class exists:</b> an
    /// <see cref="OpenRateEngine"/> provably sends no packets. No sidecar can
    /// offer that, because a sidecar is a server that fetches.</para>
    ///
    /// <h3>Two kinds of handle</h3>
    /// <list type="bullet">
    ///   <item><b>Engine</b> — computes. Creating one starts no thread, opens no
    ///   socket, reads no environment variable and sends no packet.</item>
    ///   <item><b>Refresher</b> — fetches. A separate construction with its own
    ///   handle, and <b>the only object in openrate that can open a socket</b>.
    ///   Building one still does not; fetching begins at <c>refresh</c> or
    ///   <c>start</c>.</item>
    /// </list>
    /// The ABI enforces the split: an engine handle refuses refresher methods.
    ///
    /// <h3>No streaming</h3>
    /// There is no <c>openrate_stream</c>, so there is no
    /// <c>IAsyncEnumerable</c> here. openrate answers from a snapshot it
    /// already holds. llmux, which shares this ABI's shape, does stream.
    /// </summary>
    public static partial class Direct
    {
        // ------------------------------------------------------------ P/Invoke
        //
        // LibraryImport (source-generated, .NET 7+) rather than DllImport.
        //
        // Note what is NOT here: no pointers, no function pointers, no fixed
        // buffers. openrate has no callback — there is deliberately no
        // openrate_stream — so `char**` is expressed as `out IntPtr` and none
        // of the code below is unsafe. llmux's .NET binding writes unsafe code
        // of its own, because llmux_stream takes a function pointer.
        //
        // The project still sets AllowUnsafeBlocks, because LibraryImport
        // requires it unconditionally: the generator emits pointer-using stubs
        // regardless of the declared signature (SYSLIB1062). That is a fact
        // about the generator, not about this file.
        //
        // Every string return is IntPtr, never string. A `string` return
        // compiles, runs, and leaks: the marshaller copies the C string and has
        // no idea the original must go back to openrate_free.

        private const string Lib = "openrate";

        [LibraryImport(Lib, EntryPoint = "openrate_abi_version")]
        private static partial IntPtr AbiVersionNative();

        [LibraryImport(Lib, EntryPoint = "openrate_new", StringMarshalling = StringMarshalling.Utf8)]
        private static partial ulong NewEngineNative(string? configJson, out IntPtr err);

        [LibraryImport(Lib, EntryPoint = "openrate_refresher_new", StringMarshalling = StringMarshalling.Utf8)]
        private static partial ulong NewRefresherNative(ulong engine, string? configJson, out IntPtr err);

        [LibraryImport(Lib, EntryPoint = "openrate_call", StringMarshalling = StringMarshalling.Utf8)]
        private static partial IntPtr CallNative(ulong handle, string method, string? requestJson, out IntPtr err);

        [LibraryImport(Lib, EntryPoint = "openrate_close")]
        internal static partial void CloseNative(ulong handle);

        [LibraryImport(Lib, EntryPoint = "openrate_free")]
        private static partial void FreeNative(IntPtr p);

        [LibraryImport(Lib, EntryPoint = "openrate_open_handles")]
        private static partial ulong OpenHandlesNative();

        // ------------------------------------------------------ library lookup

        private static int _resolverInstalled;
        private static string? _explicitPath;

        /// <summary>Point the runtime at a specific libopenrate.</summary>
        public static void UseLibrary(string path)
        {
            if (!File.Exists(path))
            {
                throw new OpenRateException($"no libopenrate at {path}");
            }
            _explicitPath = path;
            InstallResolver();
        }

        internal static void InstallResolver()
        {
            if (Interlocked.Exchange(ref _resolverInstalled, 1) == 1)
            {
                return;
            }
            NativeLibrary.SetDllImportResolver(
                Assembly.GetExecutingAssembly(),
                (name, assembly, searchPath) =>
                    name == Lib ? NativeLibrary.Load(_explicitPath ?? FindLibrary()) : IntPtr.Zero);
        }

        /// <summary>
        /// Locate libopenrate: <c>$OPENRATE_LIBRARY</c>, then
        /// <c>$OPENRATE_HOME/dist/ffi/</c>, then <c>dist/ffi/</c> walking up
        /// from the current directory.
        ///
        /// <para>Note the file name carries the target:
        /// <c>libopenrate-&lt;goos&gt;-&lt;goarch&gt;.dylib</c>.</para>
        /// </summary>
        public static string FindLibrary()
        {
            string? explicitPath = Environment.GetEnvironmentVariable("OPENRATE_LIBRARY");
            if (!string.IsNullOrEmpty(explicitPath))
            {
                if (!File.Exists(explicitPath))
                {
                    throw new OpenRateException(
                        $"OPENRATE_LIBRARY is set to {explicitPath}, which is not a file");
                }
                return explicitPath!;
            }

            string ext = RuntimeInformation.IsOSPlatform(OSPlatform.OSX) ? "dylib"
                       : RuntimeInformation.IsOSPlatform(OSPlatform.Windows) ? "dll"
                       : "so";
            string file = $"libopenrate-{GoOs()}-{GoArch()}.{ext}";
            var tried = new List<string>();

            string? home = Environment.GetEnvironmentVariable("OPENRATE_HOME");
            if (!string.IsNullOrEmpty(home))
            {
                string p = Path.Combine(home!, "dist", "ffi", file);
                if (File.Exists(p)) { return p; }
                tried.Add(p);
            }

            for (string? at = Directory.GetCurrentDirectory(); at != null; at = Path.GetDirectoryName(at))
            {
                string p = Path.Combine(at, "dist", "ffi", file);
                if (File.Exists(p)) { return p; }
                tried.Add(p);
            }

            throw new OpenRateException(
                $"no {file} found. Tried:{Environment.NewLine}  "
                + string.Join(Environment.NewLine + "  ", tried)
                + Environment.NewLine
                + "Build one with `scripts/build-ffi.sh` in the openrate checkout, or set OPENRATE_LIBRARY."
                + Environment.NewLine
                + "The only library built and executed so far is darwin/arm64. "
                + "THERE IS NO WINDOWS DLL and no linux/arm64 build.");
        }

        private static string GoOs() =>
            RuntimeInformation.IsOSPlatform(OSPlatform.OSX) ? "darwin"
            : RuntimeInformation.IsOSPlatform(OSPlatform.Windows) ? "windows"
            : "linux";

        private static string GoArch() => RuntimeInformation.ProcessArchitecture switch
        {
            Architecture.Arm64 => "arm64",
            Architecture.X64 => "amd64",
            var other => other.ToString().ToLowerInvariant(),
        };

        // --------------------------------------------------------- entry point

        /// <summary>
        /// Create an <b>engine</b>: the object that computes and cannot fetch.
        /// </summary>
        /// <param name="baseCurrency">Default presentation currency (default ZAR).</param>
        /// <param name="quiet">Discard the library's log output.</param>
        /// <param name="libraryPath">An explicit libopenrate, or null to search.</param>
        public static OpenRateEngine OpenEngine(
            string? baseCurrency = null, bool quiet = false, string? libraryPath = null)
        {
            if (libraryPath != null)
            {
                UseLibrary(libraryPath);
            }
            else
            {
                InstallResolver();
            }

            string config = baseCurrency == null
                ? $"{{\"quiet\":{Bool(quiet)}}}"
                : $"{{\"base\":{Json(baseCurrency)},\"quiet\":{Bool(quiet)}}}";

            ulong h = NewEngineNative(config, out IntPtr err);
            if (h == 0)
            {
                throw new OpenRateException("openrate_new: " + TakeError(ref err));
            }
            DrainError(ref err);
            return new OpenRateEngine(new OpenRateSafeHandle(h));
        }

        /// <summary>The version the loaded shared library was built from.</summary>
        public static string AbiVersion()
        {
            InstallResolver();
            IntPtr p = AbiVersionNative();
            if (p == IntPtr.Zero)
            {
                throw new OpenRateException("openrate_abi_version returned NULL");
            }
            // Static, owned by the library. Never freed.
            return Marshal.PtrToStringUTF8(p) ?? throw new OpenRateException("abi version is not UTF-8");
        }

        /// <summary>
        /// Handles currently open inside the library. Diagnostic only, and
        /// exactly what a test suite wants: assert it returns to where it
        /// started and a leak is a failure rather than a slow puzzle.
        /// </summary>
        public static ulong OpenHandles()
        {
            InstallResolver();
            return OpenHandlesNative();
        }

        // -------------------------------------------------------------- shared

        internal static ulong NewRefresher(ulong engine, string? configJson)
        {
            ulong h = NewRefresherNative(engine, configJson, out IntPtr err);
            if (h == 0)
            {
                throw new OpenRateException("openrate_refresher_new: " + TakeError(ref err));
            }
            DrainError(ref err);
            return h;
        }

        internal static string Call(ulong handle, string method, string? requestJson)
        {
            IntPtr result = CallNative(handle, method, requestJson, out IntPtr err);
            if (result == IntPtr.Zero)
            {
                throw new OpenRateException($"openrate_call({method}): " + TakeError(ref err));
            }
            try
            {
                return Marshal.PtrToStringUTF8(result)
                    ?? throw new OpenRateException("openrate_call returned a string that is not UTF-8");
            }
            finally
            {
                // Copied into a managed string; the C allocation goes back to
                // the only allocator that can take it.
                FreeNative(result);
                DrainError(ref err);
            }
        }

        private static string TakeError(ref IntPtr err)
        {
            if (err == IntPtr.Zero)
            {
                return "the library reported a failure but set no message";
            }
            string message = Marshal.PtrToStringUTF8(err) ?? "(error message is not UTF-8)";
            FreeNative(err);
            err = IntPtr.Zero;
            return message;
        }

        /// <summary>
        /// Free the error out-parameter on the SUCCESS path too, so a library
        /// that sets a message alongside a success cannot leak it.
        /// </summary>
        private static void DrainError(ref IntPtr err)
        {
            if (err != IntPtr.Zero)
            {
                FreeNative(err);
                err = IntPtr.Zero;
            }
        }

        internal static string Bool(bool b) => b ? "true" : "false";

        /// <summary>
        /// Quote a string as a JSON scalar. Currency codes are three capitals
        /// in practice, and building JSON by concatenation without escaping is
        /// still how injection bugs get written.
        /// </summary>
        internal static string Json(string s)
        {
            var sb = new System.Text.StringBuilder(s.Length + 2);
            sb.Append('"');
            foreach (char c in s)
            {
                switch (c)
                {
                    case '"': sb.Append("\\\""); break;
                    case '\\': sb.Append("\\\\"); break;
                    case '\n': sb.Append("\\n"); break;
                    case '\r': sb.Append("\\r"); break;
                    case '\t': sb.Append("\\t"); break;
                    default:
                        if (c < ' ') { sb.Append("\\u").Append(((int)c).ToString("x4")); }
                        else { sb.Append(c); }
                        break;
                }
            }
            sb.Append('"');
            return sb.ToString();
        }
    }

    /// <summary>
    /// An openrate handle, as a <see cref="SafeHandle"/> so it is released
    /// deterministically by <c>using</c> and, failing that, by the finaliser
    /// the base class provides — rather than never.
    ///
    /// <para>openrate handles are uint64 registry keys, not pointers.
    /// SafeHandle is still the right vehicle: it is the type the runtime knows
    /// how to keep alive across a P/Invoke and release exactly once. The key
    /// lives in the IntPtr, 64 bits on every platform openrate ships for.</para>
    /// </summary>
    public sealed class OpenRateSafeHandle : SafeHandle
    {
        public OpenRateSafeHandle() : base(IntPtr.Zero, ownsHandle: true) { }

        internal OpenRateSafeHandle(ulong h) : base(IntPtr.Zero, ownsHandle: true)
        {
            SetHandle((IntPtr)h);
        }

        /// <summary>0 is never a valid openrate handle.</summary>
        public override bool IsInvalid => handle == IntPtr.Zero;

        internal ulong Value => (ulong)handle;

        protected override bool ReleaseHandle()
        {
            // openrate_close is idempotent and tolerates unknown handles, so it
            // cannot fail. It also must not throw: a throwing ReleaseHandle is
            // undefined behaviour.
            Direct.CloseNative((ulong)handle);
            return true;
        }
    }

    /// <summary>
    /// The computing half. Cannot fetch: there is no code path from here to a
    /// socket that does not go through <see cref="NewRefresher"/>.
    /// </summary>
    public sealed class OpenRateEngine : IDisposable
    {
        private readonly OpenRateSafeHandle _handle;

        internal OpenRateEngine(OpenRateSafeHandle handle) => _handle = handle;

        /// <summary>Run any engine method: convert, rates, meta, load.</summary>
        public string Call(string method, string? requestJson = null) =>
            Invoke(_handle, method, requestJson);

        /// <summary>
        /// Install rates you obtained yourself. <b>The zero-network path.</b>
        /// </summary>
        public string Load(string edgesJson) => Call("load", edgesJson);

        /// <summary>Convert an amount between two currencies.</summary>
        public string Convert(string from, string to, double amount = 1) =>
            Call("convert",
                $"{{\"from\":{Direct.Json(from)},\"to\":{Direct.Json(to)}," +
                $"\"amount\":{amount.ToString(System.Globalization.CultureInfo.InvariantCulture)}}}");

        /// <summary>The whole book against a base.</summary>
        public string Rates(string? baseCurrency = null) =>
            Call("rates", baseCurrency == null ? null : $"{{\"base\":{Direct.Json(baseCurrency)}}}");

        /// <summary>
        /// Default base, build time, currencies, and the fetch status of every
        /// refresher over this engine — <c>[]</c> for an engine nobody
        /// refreshes, which is the zero-network claim in the library's own words.
        /// </summary>
        public string Meta() => Call("meta");

        /// <summary>
        /// Build a refresher over this engine. <b>The only object that can open
        /// a socket — and constructing it still does not.</b>
        /// </summary>
        public OpenRateRefresher NewRefresher(
            string? sources = null, long? intervalMs = null,
            long? fetchTimeoutMs = null, bool quiet = false)
        {
            bool taken = false;
            try
            {
                _handle.DangerousAddRef(ref taken);
                if (_handle.IsInvalid)
                {
                    throw new OpenRateException("this engine is closed");
                }
                var fields = new List<string>();
                if (sources != null) { fields.Add($"\"sources\":{Direct.Json(sources)}"); }
                if (intervalMs != null) { fields.Add($"\"interval_ms\":{intervalMs}"); }
                if (fetchTimeoutMs != null) { fields.Add($"\"fetch_timeout_ms\":{fetchTimeoutMs}"); }
                fields.Add($"\"quiet\":{Direct.Bool(quiet)}");

                ulong h = Direct.NewRefresher(_handle.Value, "{" + string.Join(",", fields) + "}");
                return new OpenRateRefresher(new OpenRateSafeHandle(h));
            }
            catch (ObjectDisposedException)
            {
                throw new OpenRateException("this engine is closed");
            }
            finally
            {
                if (taken) { _handle.DangerousRelease(); }
            }
        }

        /// <summary>Handles currently open inside the library.</summary>
        public ulong OpenHandles() => Direct.OpenHandles();

        /// <summary>
        /// Release the engine — and with it every refresher built over it,
        /// including a background loop started with <c>start</c>. Idempotent.
        /// </summary>
        public void Dispose() => _handle.Dispose();

        internal static string Invoke(OpenRateSafeHandle handle, string method, string? requestJson)
        {
            ArgumentNullException.ThrowIfNull(method);
            bool taken = false;
            try
            {
                // Pin the handle open for the call, so a concurrent Dispose
                // cannot close it mid-flight.
                handle.DangerousAddRef(ref taken);
                if (handle.IsInvalid)
                {
                    throw new OpenRateException("this handle is closed");
                }
                return Direct.Call(handle.Value, method, requestJson);
            }
            catch (ObjectDisposedException)
            {
                throw new OpenRateException("this handle is closed");
            }
            finally
            {
                if (taken) { handle.DangerousRelease(); }
            }
        }
    }

    /// <summary>
    /// The fetching half. A separate handle with its own lifetime, because in
    /// openrate fetching is a separate capability rather than a flag.
    /// </summary>
    public sealed class OpenRateRefresher : IDisposable
    {
        private readonly OpenRateSafeHandle _handle;

        internal OpenRateRefresher(OpenRateSafeHandle handle) => _handle = handle;

        /// <summary>Run any refresher method: status, refresh, start, stop, ready.</summary>
        public string Call(string method, string? requestJson = null) =>
            OpenRateEngine.Invoke(_handle, method, requestJson);

        /// <summary>Per-source fetch status. Touches no network.</summary>
        public string Status() => Call("status");

        /// <summary>One synchronous fetch of every source. <b>This opens sockets.</b></summary>
        public string Refresh(long? timeoutMs = null) =>
            Call("refresh", timeoutMs == null ? null : $"{{\"timeout_ms\":{timeoutMs}}}");

        /// <summary>Start the background loop — the only thread openrate starts itself.</summary>
        public string Start() => Call("start");

        /// <summary>Stop the background loop and wait for it to exit.</summary>
        public string Stop() => Call("stop");

        /// <summary>
        /// Block until the engine holds at least one currency. Does not fetch:
        /// something must be refreshing, or this waits.
        /// </summary>
        public string Ready(long timeoutMs = 5000) => Call("ready", $"{{\"timeout_ms\":{timeoutMs}}}");

        /// <summary>Release the refresher, stopping its loop. Idempotent.</summary>
        public void Dispose() => _handle.Dispose();
    }
}
