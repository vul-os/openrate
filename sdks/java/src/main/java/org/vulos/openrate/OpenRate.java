package org.vulos.openrate;

import java.io.File;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Duration;
import java.util.Map;

/**
 * openrate as a <b>child process</b> on {@code 127.0.0.1}, spoken to over HTTP.
 *
 * <p><b>This is the recommended default on the JVM.</b> It needs no native
 * library, no {@code --enable-native-access}, and it works on every platform
 * openrate builds for — including Windows, where the shared library does not
 * exist. See {@code README.md}.
 *
 * <pre>{@code
 * try (OpenRate rates = OpenRate.start(new OpenRate.Options())) {
 *     String zar = rates.convert("USD", "ZAR", 100);
 * }
 * }</pre>
 *
 * <p>Unlike the direct path, <b>the sidecar fetches</b>: {@code openrate serve}
 * starts its refresher on startup, so standing one up means outbound requests
 * to the configured sources. That is the server's job and it is not
 * configurable away here. If "no packets unless I ask" is the property you
 * want, that is the direct path's engine, and it is genuinely zero-network.
 *
 * <p>Requires <b>Java 11</b>. Nothing here is newer than {@code java.net.http}.
 */
public final class OpenRate implements AutoCloseable {

    public static final String VERSION = "0.1.2";

    /** Options for {@link #start(Options)}. */
    public static final class Options {
        /** Fixed port; defaults to an ephemeral free port. */
        public Integer port;
        /** Default presentation base currency (server default: ZAR). */
        public String base;
        /** Comma-separated FX sources; empty means openrate's defaults. */
        public String sources;
        /** Serve the embedded web console. Off here: an API client does not need it. */
        public boolean ui = false;
        /** Extra environment for the child process. */
        public Map<String, String> env;
        /** How long to wait for /healthz (default 30s: the first fetch can be slow). */
        public Duration timeout;
        /**
         * Also wait until the server actually holds rates, not merely until it
         * is listening. Default true, and it is the difference between a
         * working client and a mystery.
         *
         * <p>{@code /healthz} is a LIVENESS probe: openrate answers 200 the
         * moment it binds, while its first fetch is still in flight, and every
         * conversion in that window comes back
         * {@code {"error":"unknown or unreachable currency pair"}}. That looks
         * exactly like a wrong currency code. Waiting for a non-empty currency
         * list is the readiness check the HTTP API does not expose as its own
         * endpoint.
         */
        public boolean waitForRates = true;
        /** How long to wait for the first rates (default 60s). */
        public Duration ratesTimeout;
    }

    private final Process proc;
    private final String baseUrl;
    private final HttpClient http;
    private final Thread shutdownHook;

    private OpenRate(Process proc, String baseUrl) {
        this.proc = proc;
        this.baseUrl = baseUrl;
        this.http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();
        this.shutdownHook = new Thread(this::terminate);
        Runtime.getRuntime().addShutdownHook(shutdownHook);
    }

    /**
     * Spawn {@code openrate} and wait for {@code /healthz}.
     *
     * <p>Unlike llmux's Java SDK, this is <b>not a process-wide singleton</b>:
     * you get an instance you own and close. Several engines with different
     * bases or source sets is a normal thing to want from a rates service, and
     * a static singleton would make it impossible.
     */
    public static OpenRate start(Options opts) {
        if (opts == null) {
            opts = new Options();
        }
        int port = opts.port != null ? opts.port : freePort();
        String addr = "127.0.0.1:" + port;

        ProcessBuilder pb = new ProcessBuilder(binaryPath(), "-addr", addr,
                "-ui=" + Boolean.toString(opts.ui));
        pb.inheritIO();
        Map<String, String> environment = pb.environment();
        if (opts.base != null) {
            environment.put("OPENRATE_BASE", opts.base);
        }
        if (opts.sources != null) {
            environment.put("OPENRATE_SOURCES", opts.sources);
        }
        if (opts.env != null) {
            environment.putAll(opts.env);
        }

        Process proc;
        try {
            proc = pb.start();
        } catch (IOException e) {
            throw new OpenRateException("failed to spawn the openrate binary", e);
        }

        String base = "http://" + addr;
        OpenRate instance = new OpenRate(proc, base);
        try {
            instance.waitHealthy(opts.timeout != null ? opts.timeout : Duration.ofSeconds(30));
            if (opts.waitForRates) {
                instance.waitForRates(
                        opts.ratesTimeout != null ? opts.ratesTimeout : Duration.ofSeconds(60));
            }
        } catch (RuntimeException e) {
            // Do not leave a child process behind because startup failed.
            instance.close();
            throw e;
        }
        return instance;
    }

    /** {@code http://127.0.0.1:<port>}. */
    public String baseUrl() {
        return baseUrl;
    }

    /** {@code GET /api/v1/meta} — sources, freshness, and the currency list. */
    public String meta() {
        return get("/api/v1/meta");
    }

    /** {@code GET /api/v1/rates?base=…}. */
    public String rates(String base) {
        return get("/api/v1/rates?base=" + enc(base));
    }

    /** {@code GET /api/v1/convert?from=…&to=…&amount=…}. */
    public String convert(String from, String to, double amount) {
        return get("/api/v1/convert?from=" + enc(from) + "&to=" + enc(to) + "&amount=" + amount);
    }

    /** One GET against the running server; the body, whatever the status. */
    public String get(String path) {
        HttpRequest req = HttpRequest.newBuilder(URI.create(baseUrl + path))
                .timeout(Duration.ofSeconds(30))
                .GET()
                .build();
        try {
            return http.send(req, HttpResponse.BodyHandlers.ofString()).body();
        } catch (IOException e) {
            throw new OpenRateException("GET " + path + " failed", e);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new OpenRateException("GET " + path + " interrupted", e);
        }
    }

    /** Stop the child process. Idempotent — use try-with-resources. */
    @Override
    public void close() {
        terminate();
        try {
            Runtime.getRuntime().removeShutdownHook(shutdownHook);
        } catch (IllegalStateException ignored) {
            // Already shutting down; the hook is running or has run.
        }
    }

    private void terminate() {
        if (proc.isAlive()) {
            proc.destroy();
            try {
                if (!proc.waitFor(5, java.util.concurrent.TimeUnit.SECONDS)) {
                    proc.destroyForcibly();
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                proc.destroyForcibly();
            }
        }
    }

    private void waitHealthy(Duration timeout) {
        HttpRequest req = HttpRequest.newBuilder(URI.create(baseUrl + "/healthz"))
                .timeout(Duration.ofSeconds(2))
                .GET()
                .build();
        long deadline = System.nanoTime() + timeout.toNanos();
        String last = "connection refused";
        while (System.nanoTime() < deadline) {
            if (!proc.isAlive()) {
                throw new OpenRateException(
                        "the openrate binary exited before becoming healthy (status "
                                + proc.exitValue() + ")");
            }
            try {
                HttpResponse<Void> res = http.send(req, HttpResponse.BodyHandlers.discarding());
                if (res.statusCode() == 200) {
                    return;
                }
                last = "status " + res.statusCode();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (Exception e) {
                last = String.valueOf(e.getMessage());
            }
            try {
                Thread.sleep(100);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        throw new OpenRateException("openrate did not become healthy within " + timeout + ": " + last);
    }

    /**
     * Poll {@code /api/v1/meta} until the currency list is non-empty.
     *
     * <p>See {@link Options#waitForRates}. This is a readiness check built on a
     * liveness endpoint plus an observation, because openrate publishes no
     * readiness endpoint of its own — the direct path's refresher does, as the
     * {@code "ready"} method.
     */
    private void waitForRates(Duration timeout) {
        long deadline = System.nanoTime() + timeout.toNanos();
        String last = "no rates yet";
        while (System.nanoTime() < deadline) {
            if (!proc.isAlive()) {
                throw new OpenRateException("the openrate binary exited while fetching its first rates");
            }
            try {
                String meta = get("/api/v1/meta");
                if (hasCurrencies(meta)) {
                    return;
                }
                last = "the currency list is still empty";
            } catch (RuntimeException e) {
                last = String.valueOf(e.getMessage());
            }
            try {
                Thread.sleep(250);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        throw new OpenRateException(
                "openrate is listening but holds no rates after " + timeout + " (" + last + ")."
                        + " Every source fetch failed — the usual cause is no outbound network."
                        + " Set Options.waitForRates = false to start anyway.");
    }

    /**
     * True if the meta document's {@code currencies} array has anything in it.
     *
     * <p>Deliberately not a JSON parse: this SDK has no JSON dependency, and
     * "is this array empty" needs one about as much as it needs a compiler.
     */
    static boolean hasCurrencies(String metaJson) {
        int at = metaJson.indexOf("\"currencies\"");
        if (at < 0) {
            return false;
        }
        int open = metaJson.indexOf('[', at);
        if (open < 0) {
            return false;
        }
        for (int i = open + 1; i < metaJson.length(); i++) {
            char c = metaJson.charAt(i);
            if (c == ']') {
                return false;
            }
            if (!Character.isWhitespace(c)) {
                return true;
            }
        }
        return false;
    }

    // ---------------------------------------------------------------- lookup

    /**
     * Resolve the binary: {@code $OPENRATE_BINARY}, then a sibling
     * {@code bin/openrate} next to the classes or under
     * {@code $OPENRATE_HOME}, then {@code openrate} on {@code PATH}.
     */
    static String binaryPath() {
        String env = System.getenv("OPENRATE_BINARY");
        if (env != null && !env.isEmpty()) {
            return env;
        }
        boolean windows = System.getProperty("os.name", "").toLowerCase().contains("win");
        String name = windows ? "openrate.exe" : "openrate";

        Path bundled = bundledDir().resolve("bin").resolve(name);
        if (Files.isRegularFile(bundled)) {
            return bundled.toString();
        }
        String found = which(name);
        if (found != null) {
            return found;
        }
        throw new OpenRateException(
                "openrate binary not found. Set OPENRATE_BINARY, or build it: "
                        + "`go build -o sdks/java/bin/openrate ./cmd/openrate`");
    }

    private static Path bundledDir() {
        String home = System.getenv("OPENRATE_HOME");
        if (home != null && !home.isEmpty()) {
            return Paths.get(home);
        }
        try {
            Path self = Paths.get(
                    OpenRate.class.getProtectionDomain().getCodeSource().getLocation().toURI());
            Path dir = Files.isDirectory(self) ? self : self.getParent();
            return dir != null ? dir : Paths.get(".");
        } catch (Exception e) {
            return Paths.get(".");
        }
    }

    private static String which(String cmd) {
        String path = System.getenv("PATH");
        if (path == null) {
            return null;
        }
        for (String dir : path.split(File.pathSeparator)) {
            Path candidate = Paths.get(dir, cmd);
            if (Files.isRegularFile(candidate) && Files.isExecutable(candidate)) {
                return candidate.toString();
            }
        }
        return null;
    }

    private static int freePort() {
        try (ServerSocket s = new ServerSocket()) {
            s.bind(new InetSocketAddress("127.0.0.1", 0));
            return s.getLocalPort();
        } catch (IOException e) {
            throw new OpenRateException("could not allocate a free port", e);
        }
    }

    private static String enc(String s) {
        try {
            return URLEncoder.encode(s, StandardCharsets.UTF_8.name());
        } catch (Exception e) {
            throw new OpenRateException("could not encode " + s, e);
        }
    }
}
