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
        /**
         * Per-IP API requests/minute for the child; {@code 0} disables the
         * limiter, and that is the default here.
         *
         * <p>The child listens on loopback and serves exactly one client: this
         * process. The limiter is anti-scraping for a public deployment and
         * there is no stranger here to throttle — while a legitimate batch of
         * conversions would sail past the 120/min default and take a 429 from
         * our own sidecar. Set it to 120 to put the binary's default back.
         */
        public Integer ratelimit;
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
         * exactly like a wrong currency code.
         *
         * <p>{@code /readyz} is the readiness probe that settles it: 200 once
         * the snapshot holds currencies, 503 with a JSON body naming every
         * source and its last error until then.
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
     * Spawn {@code openrate}, wait for {@code /healthz} to say it is listening
     * and then for {@code /readyz} to say it holds rates.
     *
     * <p>The two waits are different questions and the second one is the one
     * that matters to a caller: see {@link Options#waitForRates}.
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
        // See Options.ratelimit: a loopback child with one client has no
        // stranger to throttle, and the 120/min default would 429 a legitimate
        // batch of our own conversions. Set before Options.env, so a caller who
        // puts OPENRATE_RATELIMIT there by hand still overrides it.
        environment.put("OPENRATE_RATELIMIT",
                Integer.toString(opts.ratelimit != null ? opts.ratelimit : 0));
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
     * Poll {@code GET /readyz} until the server says it can serve a conversion.
     *
     * <p>See {@link Options#waitForRates}. 200 is ready; 503 is not ready yet
     * and carries a JSON body with the reason and the per-source outcomes, so
     * a timeout here reports the cause rather than the elapsed time. Anything
     * else — a refused connection, a socket dropped mid-flight — is kept as
     * text and polled through, because the child is allowed to still be coming
     * up.
     *
     * <p>A fixed 200 ms interval is deliberate. {@code /readyz} sits outside
     * {@code /api/}, which is the only prefix {@code guard()} rate-limits, so
     * polling it cannot spend the budget the first real call needs. This
     * replaces polling {@code /api/v1/meta} for a non-empty currency list,
     * which could and did.
     */
    private void waitForRates(Duration timeout) {
        long deadline = System.nanoTime() + timeout.toNanos();
        HttpRequest req = HttpRequest.newBuilder(URI.create(baseUrl + "/readyz"))
                .timeout(Duration.ofSeconds(5))
                .GET()
                .build();
        String last = "no rates yet";
        while (System.nanoTime() < deadline) {
            if (!proc.isAlive()) {
                throw new OpenRateException("the openrate binary exited while fetching its first rates");
            }
            try {
                // BodyHandlers.ofString, not discarding: the 503 IS the answer,
                // and throwing its body away leaves nothing but a bare timeout.
                HttpResponse<String> res = http.send(req, HttpResponse.BodyHandlers.ofString());
                if (res.statusCode() == 200) {
                    return;
                }
                if (res.statusCode() == 503) {
                    last = notReadyReason(res.body());
                } else if (res.statusCode() == 404) {
                    // Not a transient state: this binary predates /readyz.
                    // Failing now beats polling a route that will never exist.
                    throw new OpenRateException(
                            "this openrate binary has no /readyz — it predates the readiness "
                                    + "endpoint. Rebuild it (`go build -o bin/openrate ./cmd/openrate`), "
                                    + "or set waitForRates = false and accept converting "
                                    + "against a possibly empty book.");
                } else {
                    last = "unexpected status " + res.statusCode() + " from /readyz";
                }
            } catch (OpenRateException e) {
                throw e; // A verdict, not a transport hiccup — do not poll on.
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (Exception e) {
                last = String.valueOf(e.getMessage());
            }
            try {
                Thread.sleep(200);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        throw new OpenRateException(
                "openrate has no rates after " + human(timeout) + ": " + last
                        + ". Set waitForRates = false to start anyway.");
    }

    /**
     * The human cause carried by a {@code /readyz} 503: its {@code reason},
     * plus {@code name: last_error} for every source that has one.
     *
     * <p>{@code last_error} is {@code omitempty} on the server, so a source
     * that has not been tried yet has no such key at all — it is skipped
     * rather than reported as {@code ecb: null}. When no source has failed
     * yet the reason stands alone.
     */
    static String notReadyReason(String readyJson) {
        if (readyJson == null) {
            return "not ready";
        }
        int open = readyJson.indexOf('{');
        if (open < 0) {
            return readyJson.trim().isEmpty() ? "not ready" : oneLine(readyJson);
        }
        int lo = open + 1;
        int hi = readyJson.length();

        String reason = stringAt(readyJson, valueSpan(readyJson, lo, hi, "reason"));
        StringBuilder failures = new StringBuilder();
        int[] sources = valueSpan(readyJson, lo, hi, "sources");
        if (sources != null && readyJson.charAt(sources[0]) == '[') {
            int i = sources[0] + 1;
            while (i < sources[1] - 1) {
                if (readyJson.charAt(i) != '{') {
                    i++;
                    continue;
                }
                int end = valueEnd(readyJson, i);
                String name = stringAt(readyJson, valueSpan(readyJson, i + 1, end - 1, "name"));
                String err = stringAt(readyJson, valueSpan(readyJson, i + 1, end - 1, "last_error"));
                if (name != null && err != null && !err.isEmpty()) {
                    failures.append(failures.length() == 0 ? "" : "; ").append(name).append(": ").append(err);
                }
                i = end;
            }
        }
        if (reason == null || reason.isEmpty()) {
            reason = "not ready";
        }
        return failures.length() == 0 ? reason : reason + " (" + failures + ")";
    }

    // ------------------------------------------------------------------ JSON
    //
    // Enough JSON to read /readyz, and no more. This SDK has no JSON dependency
    // on purpose — every document it returns is handed to the caller as a
    // string, for the parser they already have — but a readiness message is the
    // one thing the SDK has to read for itself, and a substring hunt for
    // "last_error" would happily match one quoted inside another source's error
    // text. So: a scanner that knows where strings end, skips whole values, and
    // matches keys only at the depth it was asked about.

    /** [start,end) of the value of {@code key} among the members in [lo,hi), or null. */
    private static int[] valueSpan(String json, int lo, int hi, String key) {
        int i = lo;
        while (i < hi) {
            if (json.charAt(i) != '"') {
                i++;
                continue;
            }
            int nameEnd = stringEnd(json, i);
            int colon = skipWs(json, nameEnd, hi);
            if (colon >= hi || json.charAt(colon) != ':') {
                i = nameEnd;
                continue;
            }
            int start = skipWs(json, colon + 1, hi);
            int end = valueEnd(json, start);
            if (key.equals(unescape(json, i + 1, nameEnd - 1))) {
                return new int[] {start, end};
            }
            i = end; // Skip the whole value: nested keys are not ours.
        }
        return null;
    }

    /** The span's contents as a string, or null when it is absent or not a string. */
    private static String stringAt(String json, int[] span) {
        if (span == null || span[0] >= span[1] || json.charAt(span[0]) != '"') {
            return null;
        }
        return unescape(json, span[0] + 1, span[1] - 1);
    }

    /** Index just past the value that starts at {@code start}. */
    private static int valueEnd(String json, int start) {
        char c = json.charAt(start);
        if (c == '"') {
            return stringEnd(json, start);
        }
        if (c == '{' || c == '[') {
            int depth = 0;
            for (int i = start; i < json.length(); i++) {
                char d = json.charAt(i);
                if (d == '"') {
                    i = stringEnd(json, i) - 1;
                } else if (d == '{' || d == '[') {
                    depth++;
                } else if (d == '}' || d == ']') {
                    if (--depth == 0) {
                        return i + 1;
                    }
                }
            }
            return json.length();
        }
        int i = start;
        while (i < json.length() && ",}] \t\r\n".indexOf(json.charAt(i)) < 0) {
            i++;
        }
        return i;
    }

    /** Index just past the closing quote of the string opening at {@code start}. */
    private static int stringEnd(String json, int start) {
        for (int i = start + 1; i < json.length(); i++) {
            char c = json.charAt(i);
            if (c == '\\') {
                i++;
            } else if (c == '"') {
                return i + 1;
            }
        }
        return json.length();
    }

    private static int skipWs(String json, int i, int hi) {
        while (i < hi && Character.isWhitespace(json.charAt(i))) {
            i++;
        }
        return i;
    }

    /** The characters between two quotes, with JSON escapes resolved. */
    private static String unescape(String json, int from, int to) {
        StringBuilder out = new StringBuilder(to - from);
        for (int i = from; i < to; i++) {
            char c = json.charAt(i);
            if (c != '\\' || i + 1 >= to) {
                out.append(c);
                continue;
            }
            char e = json.charAt(++i);
            switch (e) {
                case 'n': out.append('\n'); break;
                case 't': out.append('\t'); break;
                case 'r': out.append('\r'); break;
                case 'b': out.append('\b'); break;
                case 'f': out.append('\f'); break;
                case 'u':
                    if (i + 4 < to) {
                        out.append((char) Integer.parseInt(json.substring(i + 1, i + 5), 16));
                        i += 4;
                    }
                    break;
                default: out.append(e); break; // " \ / and anything malformed
            }
        }
        return out.toString();
    }

    /** A server error is one line in an exception message, however it arrived. */
    private static String oneLine(String s) {
        return s.replaceAll("\\s+", " ").trim();
    }

    /** {@code PT30S} is not what a person wants to read in a timeout message. */
    private static String human(Duration d) {
        long ms = d.toMillis();
        return ms % 1000 == 0 ? (ms / 1000) + "s" : ms + "ms";
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
