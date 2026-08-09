package org.vulos.openrate;

import java.lang.foreign.Arena;
import java.lang.foreign.FunctionDescriptor;
import java.lang.foreign.Linker;
import java.lang.foreign.MemorySegment;
import java.lang.foreign.SymbolLookup;
import java.lang.foreign.ValueLayout;
import java.lang.invoke.MethodHandle;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * openrate <b>in this process</b>, through the C ABI of {@code libopenrate}.
 *
 * <p>{@link OpenRate} is the other path: it spawns {@code openrate} as a child
 * process and talks HTTP. <b>On the JVM the sidecar is the recommended
 * default</b> — see {@code README.md} → "The JVM and Go's signal handlers",
 * which is measured rather than described.
 *
 * <h2>Two kinds of handle, and that is the whole design</h2>
 *
 * openrate splits computing from fetching, and it splits them at the ABI rather
 * than by convention:
 *
 * <ul>
 *   <li>An <b>engine</b> ({@link #openEngine}) computes. Creating one starts no
 *       thread, opens no socket, reads no environment variable and sends no
 *       packet. It answers from the snapshot it holds and says "unknown or
 *       unreachable currency pair" until something gives it one.</li>
 *   <li>A <b>refresher</b> ({@link #newRefresher}) fetches. It is a separate
 *       construction with its own handle, and it is <b>the only object in
 *       openrate that can open a socket</b>. A program that never calls
 *       {@code newRefresher} cannot make this library touch the network. That
 *       is not a promise about intent; there is no other code path.</li>
 * </ul>
 *
 * An engine handle <b>refuses</b> refresher methods and vice versa, so the
 * split cannot be blurred by passing the wrong handle.
 *
 * <p>Feed an engine without any network at all with the {@code "load"} method,
 * which installs rates you obtained yourself.
 *
 * <pre>{@code
 * try (OpenRateDirect engine = OpenRateDirect.openEngine(null)) {
 *     engine.call("load", ratesJson);
 *     String converted = engine.call("convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}");
 * }
 * }</pre>
 *
 * <h2>No streaming</h2>
 * There is no {@code openrate_stream} and this class has no streaming method.
 * openrate answers from a snapshot it already holds; there is no incremental
 * operation to stream. llmux, which shares this ABI's shape, does define
 * {@code llmux_stream}. The absence here is deliberate, not missing work.
 *
 * <h2>Requirements</h2>
 * <ul>
 *   <li><b>Java 22+</b> — {@code java.lang.foreign} became permanent in 22.
 *       Tested on OpenJDK 26.0.2, darwin/arm64.</li>
 *   <li>{@code --enable-native-access=ALL-UNNAMED} on the java command line.</li>
 *   <li>A {@code libopenrate} for your platform: <b>darwin/arm64 only, tested.
 *       No Windows DLL exists.</b> See the README.</li>
 * </ul>
 */
public final class OpenRateDirect implements AutoCloseable {

    // ---------------------------------------------------------------- binding

    private static final class Native {
        final MethodHandle newEngine;     // uint64_t (const char*, char**)
        final MethodHandle newRefresher;  // uint64_t (uint64_t, const char*, char**)
        final MethodHandle call;          // char* (uint64_t, const char*, const char*, char**)
        final MethodHandle closeHandle;   // void (uint64_t)
        final MethodHandle free;          // void (char*)
        final MethodHandle abiVersion;    // const char* (void)
        final MethodHandle openHandles;   // uint64_t (void)
        final String version;

        Native(Path library) {
            Linker linker = Linker.nativeLinker();
            SymbolLookup lookup;
            try {
                // Global arena: a library carrying the Go runtime is never
                // unloaded. dlclose on it is not an operation to attempt.
                lookup = SymbolLookup.libraryLookup(library, Arena.global());
            } catch (IllegalArgumentException e) {
                throw new OpenRateException("could not load " + library + ": " + e.getMessage(), e);
            }
            newEngine = down(linker, lookup, "openrate_new",
                    FunctionDescriptor.of(ValueLayout.JAVA_LONG, ValueLayout.ADDRESS, ValueLayout.ADDRESS));
            newRefresher = down(linker, lookup, "openrate_refresher_new",
                    FunctionDescriptor.of(ValueLayout.JAVA_LONG, ValueLayout.JAVA_LONG,
                            ValueLayout.ADDRESS, ValueLayout.ADDRESS));
            call = down(linker, lookup, "openrate_call",
                    FunctionDescriptor.of(ValueLayout.ADDRESS, ValueLayout.JAVA_LONG,
                            ValueLayout.ADDRESS, ValueLayout.ADDRESS, ValueLayout.ADDRESS));
            closeHandle = down(linker, lookup, "openrate_close",
                    FunctionDescriptor.ofVoid(ValueLayout.JAVA_LONG));
            free = down(linker, lookup, "openrate_free",
                    FunctionDescriptor.ofVoid(ValueLayout.ADDRESS));
            abiVersion = down(linker, lookup, "openrate_abi_version",
                    FunctionDescriptor.of(ValueLayout.ADDRESS));
            openHandles = down(linker, lookup, "openrate_open_handles",
                    FunctionDescriptor.of(ValueLayout.JAVA_LONG));
            try {
                MemorySegment v = (MemorySegment) abiVersion.invokeExact();
                // Static, owned by the library. Never free it.
                version = v.reinterpret(Long.MAX_VALUE).getString(0);
            } catch (Throwable t) {
                throw new OpenRateException("openrate_abi_version failed", t);
            }
        }

        private static MethodHandle down(Linker linker, SymbolLookup lookup, String name,
                                         FunctionDescriptor desc) {
            MemorySegment sym = lookup.find(name).orElseThrow(() -> new OpenRateException(
                    "libopenrate does not export " + name
                            + " — the library on the load path is not a libopenrate, or is too old"));
            return linker.downcallHandle(sym, desc);
        }
    }

    private static final Map<Path, Native> LOADED = new ConcurrentHashMap<>();

    private static Native nativeFor(Path library) {
        return LOADED.computeIfAbsent(library.toAbsolutePath().normalize(), Native::new);
    }

    // ------------------------------------------------------------------ state

    /** What a handle is allowed to be asked. */
    private enum Kind { ENGINE, REFRESHER }

    private final Native lib;
    private final Path libraryPath;
    private final Kind kind;
    private volatile long handle;

    private OpenRateDirect(Native lib, Path libraryPath, Kind kind, long handle) {
        this.lib = lib;
        this.libraryPath = libraryPath;
        this.kind = kind;
        this.handle = handle;
    }

    // ---------------------------------------------------------------- opening

    /**
     * Create an <b>engine</b> — the object that computes and never fetches.
     *
     * @param configJson {@code {"base":"ZAR","quiet":false}}, or null for
     *                   defaults
     */
    public static OpenRateDirect openEngine(String configJson) {
        return openEngine(findLibrary(), configJson);
    }

    /** Create an engine from an explicit library path. */
    public static OpenRateDirect openEngine(Path library, String configJson) {
        Native lib = nativeFor(library);
        try (Arena arena = Arena.ofConfined()) {
            MemorySegment cfg = configJson == null ? MemorySegment.NULL : arena.allocateFrom(configJson);
            MemorySegment errBox = arena.allocate(ValueLayout.ADDRESS);
            errBox.set(ValueLayout.ADDRESS, 0, MemorySegment.NULL);
            long h;
            try {
                h = (long) lib.newEngine.invokeExact(cfg, errBox);
            } catch (Throwable t) {
                throw new OpenRateException("openrate_new failed", t);
            }
            if (h == 0) {
                throw new OpenRateException("openrate_new: " + takeError(lib, errBox));
            }
            drainError(lib, errBox);
            return new OpenRateDirect(lib, library, Kind.ENGINE, h);
        }
    }

    /**
     * Create a <b>refresher</b> over this engine — the only object that can
     * open a socket, and building it still does not. Fetching begins at
     * {@code call("refresh", …)} or {@code call("start", …)}.
     *
     * <p>The returned refresher is a separate {@link AutoCloseable} with its own
     * handle. Closing the engine also stops and releases every refresher built
     * over it, so closing in the "wrong" order cannot leak a running loop —
     * but nest the try-with-resources anyway, and let the language say so.
     *
     * @param configJson {@code {"sources":"ecb,coinbase","interval_ms":3600000,
     *                   "fetch_timeout_ms":50000,"quiet":false}}, or null
     */
    public OpenRateDirect newRefresher(String configJson) {
        requireOpen();
        require(kind == Kind.ENGINE, "a refresher cannot be built over another refresher");
        try (Arena arena = Arena.ofConfined()) {
            MemorySegment cfg = configJson == null ? MemorySegment.NULL : arena.allocateFrom(configJson);
            MemorySegment errBox = arena.allocate(ValueLayout.ADDRESS);
            errBox.set(ValueLayout.ADDRESS, 0, MemorySegment.NULL);
            long h;
            try {
                h = (long) lib.newRefresher.invokeExact(handle, cfg, errBox);
            } catch (Throwable t) {
                throw new OpenRateException("openrate_refresher_new failed", t);
            }
            if (h == 0) {
                throw new OpenRateException("openrate_refresher_new: " + takeError(lib, errBox));
            }
            drainError(lib, errBox);
            return new OpenRateDirect(lib, libraryPath, Kind.REFRESHER, h);
        }
    }

    // ---------------------------------------------------------------- calling

    /**
     * Run one method against this handle.
     *
     * <p>Engine: {@code convert}, {@code rates}, {@code meta}, {@code load}.
     * <br>Refresher: {@code status}, {@code refresh}, {@code start},
     * {@code stop}, {@code ready}.
     *
     * <p>{@code requestJson} may be null, which the library reads as {@code {}}.
     *
     * @return the response JSON — the same JSON the HTTP API publishes
     * @throws OpenRateException carrying the library's own message
     */
    public String call(String method, String requestJson) {
        long h = requireOpen();
        try (Arena arena = Arena.ofConfined()) {
            MemorySegment m = arena.allocateFrom(method);
            MemorySegment req = requestJson == null ? MemorySegment.NULL : arena.allocateFrom(requestJson);
            MemorySegment errBox = arena.allocate(ValueLayout.ADDRESS);
            errBox.set(ValueLayout.ADDRESS, 0, MemorySegment.NULL);

            MemorySegment out;
            try {
                out = (MemorySegment) lib.call.invokeExact(h, m, req, errBox);
            } catch (Throwable t) {
                throw new OpenRateException("openrate_call failed", t);
            }
            if (out.equals(MemorySegment.NULL)) {
                throw new OpenRateException("openrate_call(" + method + "): " + takeError(lib, errBox));
            }
            try {
                return out.reinterpret(Long.MAX_VALUE).getString(0);
            } finally {
                // Copied into a String; the C allocation goes back to the only
                // allocator that can take it.
                freeNative(lib, out);
                drainError(lib, errBox);
            }
        }
    }

    /** {@code call(method, null)}. */
    public String call(String method) {
        return call(method, null);
    }

    // ---------------------------------------------------------------- closing

    /**
     * Release this handle. Idempotent, so error paths can close blindly.
     *
     * <p>Closing an engine also stops and releases the refreshers built over
     * it, including any background loop started with {@code "start"}.
     */
    @Override
    public void close() {
        long h;
        synchronized (this) {
            h = handle;
            handle = 0;
        }
        if (h == 0) {
            return;
        }
        try {
            lib.closeHandle.invokeExact(h);
        } catch (Throwable t) {
            throw new OpenRateException("openrate_close failed", t);
        }
    }

    // ------------------------------------------------------------ diagnostics

    /** The openrate version the loaded shared library was built from. */
    public String abiVersion() {
        return lib.version;
    }

    /** The library this handle lives in. */
    public Path libraryPath() {
        return libraryPath;
    }

    /** True if this handle is an engine rather than a refresher. */
    public boolean isEngine() {
        return kind == Kind.ENGINE;
    }

    /**
     * How many handles the library currently has open.
     *
     * <p>Diagnostic only, and exactly what a host test suite wants: assert this
     * is back where it started and a leaked handle is a test failure rather
     * than a slow puzzle. Both examples do.
     */
    public long openHandles() {
        try {
            return (long) lib.openHandles.invokeExact();
        } catch (Throwable t) {
            throw new OpenRateException("openrate_open_handles failed", t);
        }
    }

    // ---------------------------------------------------------------- helpers

    private long requireOpen() {
        long h = handle;
        if (h == 0) {
            throw new OpenRateException("this " + kind.name().toLowerCase(Locale.ROOT) + " is closed");
        }
        return h;
    }

    private static void require(boolean cond, String message) {
        if (!cond) {
            throw new OpenRateException(message);
        }
    }

    private static String takeError(Native lib, MemorySegment errBox) {
        MemorySegment p = errBox.get(ValueLayout.ADDRESS, 0);
        if (p.equals(MemorySegment.NULL)) {
            return "the library reported a failure but set no message";
        }
        String msg = p.reinterpret(Long.MAX_VALUE).getString(0);
        errBox.set(ValueLayout.ADDRESS, 0, MemorySegment.NULL);
        freeNative(lib, p);
        return msg;
    }

    /** Free the error out-parameter on the SUCCESS path too, so it cannot leak. */
    private static void drainError(Native lib, MemorySegment errBox) {
        MemorySegment p = errBox.get(ValueLayout.ADDRESS, 0);
        if (!p.equals(MemorySegment.NULL)) {
            errBox.set(ValueLayout.ADDRESS, 0, MemorySegment.NULL);
            freeNative(lib, p);
        }
    }

    private static void freeNative(Native lib, MemorySegment p) {
        try {
            lib.free.invokeExact(p);
        } catch (Throwable t) {
            throw new OpenRateException("openrate_free failed", t);
        }
    }

    // ----------------------------------------------------------------- lookup

    /**
     * Locate libopenrate, in order:
     * <ol>
     *   <li>{@code $OPENRATE_LIBRARY} — an explicit path</li>
     *   <li>{@code $OPENRATE_HOME/dist/ffi/}</li>
     *   <li>{@code dist/ffi/} walking up from the working directory — the
     *       layout {@code scripts/build-ffi.sh} writes</li>
     * </ol>
     *
     * <p>Note the file name carries the target:
     * {@code libopenrate-<goos>-<goarch>.dylib}.
     *
     * @throws OpenRateException naming every path tried
     */
    public static Path findLibrary() {
        String explicit = System.getenv("OPENRATE_LIBRARY");
        if (explicit != null && !explicit.isEmpty()) {
            Path p = Paths.get(explicit);
            if (!Files.isRegularFile(p)) {
                throw new OpenRateException("OPENRATE_LIBRARY is set to " + p + ", which is not a file");
            }
            return p;
        }

        String file = "libopenrate-" + goos() + "-" + goarch() + "." + extension();
        StringBuilder tried = new StringBuilder();

        String home = System.getenv("OPENRATE_HOME");
        if (home != null && !home.isEmpty()) {
            Path p = Paths.get(home, "dist", "ffi", file);
            if (Files.isRegularFile(p)) {
                return p;
            }
            tried.append("\n  ").append(p);
        }

        for (Path at = Paths.get("").toAbsolutePath(); at != null; at = at.getParent()) {
            Path p = at.resolve("dist").resolve("ffi").resolve(file);
            if (Files.isRegularFile(p)) {
                return p;
            }
            tried.append("\n  ").append(p);
        }

        throw new OpenRateException("no " + file + " found. Tried:" + tried
                + "\nBuild one with `scripts/build-ffi.sh` in the openrate checkout, or set"
                + " OPENRATE_LIBRARY to an existing library."
                + "\nThe only library built and executed so far is darwin/arm64."
                + " There is no Windows DLL and no linux/arm64 build.");
    }

    private static String extension() {
        String os = goos();
        return os.equals("darwin") ? "dylib" : os.equals("windows") ? "dll" : "so";
    }

    private static String goos() {
        String os = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        if (os.contains("mac") || os.contains("darwin")) {
            return "darwin";
        }
        if (os.contains("win")) {
            return "windows";
        }
        return "linux";
    }

    private static String goarch() {
        String arch = System.getProperty("os.arch", "").toLowerCase(Locale.ROOT);
        if (arch.equals("aarch64") || arch.equals("arm64")) {
            return "arm64";
        }
        if (arch.equals("x86_64") || arch.equals("amd64")) {
            return "amd64";
        }
        return arch;
    }
}
