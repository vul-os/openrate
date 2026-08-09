import org.vulos.openrate.OpenRate;

/**
 * openrate SIDECAR — the server as a child process on 127.0.0.1, over HTTP.
 * The recommended default for the JVM.
 *
 * Unlike the direct engine, THIS FETCHES. `openrate serve` starts its refresher
 * on startup, so standing one up means outbound requests to the configured
 * sources (ecb, coinbase, luno, sarb by default). This example therefore needs
 * a network, and says so rather than failing mysteriously without one.
 *
 * Runs on Java 11+. No native library, no --enable-native-access, no platform
 * matrix.
 */
public final class SidecarRates {

    public static void main(String[] args) {
        OpenRate.Options opts = new OpenRate.Options();
        opts.base = "ZAR";
        // Just ECB and Coinbase: both are keyless and public, which keeps this
        // example runnable by anyone rather than by whoever has the API keys.
        opts.sources = "ecb,coinbase";

        System.out.println("starting openrate (this fetches — it needs a network)…");

        // try-with-resources: the child process is stopped on every path out,
        // including a failure mid-example.
        //
        // start() waits for /healthz AND for the currency list to fill. Waiting
        // only for /healthz is the trap: openrate answers 200 the moment it
        // binds, while its first fetch is still in flight, and every conversion
        // in that window returns "unknown or unreachable currency pair" — which
        // reads as a wrong currency code rather than as "not ready yet". This
        // example printed exactly that before OpenRate learned to wait.
        try (OpenRate rates = OpenRate.start(opts)) {
            System.out.println("sidecar: " + rates.baseUrl());

            // 1. What the server knows, and how fresh it is.
            System.out.println("meta: " + oneLine(rates.meta(), 300));

            // 2. A conversion, with full provenance attached to the rate.
            String converted = rates.convert("USD", "ZAR", 100);
            System.out.println("USD->ZAR 100: " + oneLine(converted, 320));
            if (converted.contains("\"error\"")) {
                // A live example that quietly prints an error object and exits
                // 0 is the false green this repo keeps finding.
                System.err.println("FAIL: the sidecar was ready but the conversion still errored");
                System.exit(1);
            }

            // 3. The whole book against a base.
            System.out.println("rates(EUR): " + oneLine(rates.rates("EUR"), 300));

            // 4. The error path. An unparseable amount falls back to 1 rather
            //    than erroring; a non-finite one is a 400. Both are the HTTP
            //    API's documented behaviour, not this SDK's invention.
            System.out.println("bad amount: " + oneLine(rates.get("/api/v1/convert?from=USD&to=ZAR&amount=NaN"), 200));
            System.out.println("unknown currency: " + oneLine(rates.convert("USD", "XYZ", 1), 200));
        }
        System.out.println("sidecar stopped");
        System.out.println("done");
    }

    private static String oneLine(String s, int max) {
        String flat = s.replaceAll("\\s+", " ");
        return flat.length() <= max ? flat : flat.substring(0, max) + "…";
    }
}
