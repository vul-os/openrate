import org.vulos.openrate.OpenRateDirect;
import org.vulos.openrate.OpenRateException;

import java.nio.file.Path;

/**
 * openrate DIRECT — the engine inside this JVM, through libopenrate's C ABI.
 *
 * The headline is the part most rate libraries cannot offer: an engine that
 * PROVABLY sends no packets. openrate_new starts no thread, opens no socket and
 * reads no environment variable, and the only object that can fetch —
 * a refresher — is a separate explicit construction with its own handle. This
 * example spends most of its time in that zero-network mode, feeding the engine
 * rates through "load" and converting against them.
 *
 * Then, because a refresher IS the other half of the design, it builds one and
 * shows that it still has not fetched until asked. Whether it then fetches for
 * real depends on OPENRATE_ALLOW_NETWORK; by default this example touches
 * nothing.
 *
 * Run with sdks/java/run-examples.sh. Requires Java 22+ and
 * --enable-native-access=ALL-UNNAMED.
 *
 * READ sdks/java/README.md FIRST. On the JVM the sidecar is the recommended
 * default, for reasons that are measured there.
 */
public final class DirectRates {

    public static void main(String[] args) {
        Path library = OpenRateDirect.findLibrary();
        System.out.println("library: " + library);

        boolean allowNetwork = "1".equals(System.getenv("OPENRATE_ALLOW_NETWORK"));

        // try-with-resources on the engine. Closing an engine also releases
        // every refresher built over it, so this one statement is the whole
        // cleanup story even for the nested handle below.
        try (OpenRateDirect engine = OpenRateDirect.openEngine(library, "{\"base\":\"ZAR\",\"quiet\":true}")) {
            System.out.println("abi version: " + engine.abiVersion());
            System.out.println("open handles after creating the engine: " + engine.openHandles());

            // 1. An engine with nothing in it says so, rather than guessing.
            try {
                engine.call("convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}");
                System.out.println("FAIL: an empty engine should not have converted anything");
            } catch (OpenRateException e) {
                System.out.println("empty engine: " + e.getMessage());
            }

            // 2. "load" — the zero-network path. These are rates you obtained
            //    yourself; openrate does not care where from.
            String load = "{"
                    + "\"built_at\":\"2026-08-09T00:00:00Z\","
                    + "\"edges\":["
                    + "{\"from\":\"USD\",\"to\":\"ZAR\",\"rate\":18.5,\"source\":\"mine\"},"
                    + "{\"from\":\"EUR\",\"to\":\"USD\",\"rate\":1.09,\"source\":\"mine\"}"
                    + "]}";
            System.out.println("load: " + oneLine(engine.call("load", load), 200));

            // 3. Convert directly, and across a hop the graph works out itself.
            System.out.println("USD->ZAR: "
                    + oneLine(engine.call("convert", "{\"from\":\"USD\",\"to\":\"ZAR\",\"amount\":100}"), 240));
            System.out.println("EUR->ZAR (2 hops): "
                    + oneLine(engine.call("convert", "{\"from\":\"EUR\",\"to\":\"ZAR\",\"amount\":1}"), 240));

            // 4. meta reports no sources, because nothing is refreshing this
            //    engine. That empty list is the zero-network claim, in the
            //    library's own words.
            System.out.println("meta: " + oneLine(engine.call("meta"), 200));

            // 5. A refresher is a SEPARATE handle. Building it opens no socket.
            try (OpenRateDirect refresher =
                         engine.newRefresher("{\"sources\":\"ecb\",\"quiet\":true}")) {
                System.out.println("open handles with a refresher: " + refresher.openHandles());
                System.out.println("refresher status before any fetch: "
                        + oneLine(refresher.call("status"), 200));

                // The ABI enforces the split: an engine refuses refresher work.
                try {
                    engine.call("refresh", "{}");
                    System.out.println("FAIL: an engine should refuse \"refresh\"");
                } catch (OpenRateException e) {
                    System.out.println("engine refuses \"refresh\": " + e.getMessage());
                }

                if (allowNetwork) {
                    System.out.println("OPENRATE_ALLOW_NETWORK=1 — fetching from ECB for real…");
                    System.out.println("refresh: " + oneLine(refresher.call("refresh", "{\"timeout_ms\":30000}"), 300));
                    System.out.println("USD->EUR from live data: "
                            + oneLine(engine.call("convert", "{\"from\":\"USD\",\"to\":\"EUR\",\"amount\":1}"), 200));
                } else {
                    System.out.println("OPENRATE_ALLOW_NETWORK is unset, so nothing was fetched.");
                    System.out.println("This process has opened no socket on openrate's behalf.");
                }
            }
            System.out.println("open handles after the refresher closed: " + engine.openHandles());
        }

        // 6. Every handle closed. This is the assertion that makes the
        //    try-with-resources above evidence rather than decoration.
        try (OpenRateDirect probe = OpenRateDirect.openEngine(library, "{\"quiet\":true}")) {
            long open = probe.openHandles();
            probe.close();
            long after = probe.openHandles();
            System.out.println("handle accounting: " + open + " open, " + after + " after close");
            if (after != open - 1) {
                System.out.println("FAIL: closing a handle should have decremented the count");
            }

            // 7. Use after close is a clean error, not a crash. Handles are
            //    retired rather than recycled, so a stale one can never reach
            //    somebody else's object.
            try {
                probe.call("meta");
                System.out.println("FAIL: a closed engine should not have answered");
            } catch (OpenRateException e) {
                System.out.println("after close: " + e.getMessage());
            }
        }

        System.out.println("done");
    }

    private static String oneLine(String s, int max) {
        String flat = s.replaceAll("\\s+", " ");
        return flat.length() <= max ? flat : flat.substring(0, max) + "…";
    }
}
