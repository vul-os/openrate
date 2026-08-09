import org.vulos.openrate.OpenRateException
import org.vulos.openrate.kotlin.OpenRateSidecar
import java.time.Duration
import kotlin.system.exitProcess

/**
 * openrate SIDECAR from Kotlin — the server as a child process on 127.0.0.1.
 * The recommended default for the JVM.
 *
 * THIS FETCHES. `openrate serve` starts its refresher at startup, so this
 * example needs a network and says so rather than failing mysteriously.
 *
 * Runs on Java 11+. No native library, no --enable-native-access.
 */
fun main() {
    println("starting openrate (this fetches — it needs a network)…")

    // OPENRATE_READY_TIMEOUT_SECONDS shortens the readiness wait so the failure
    // path can be demonstrated without sitting out the full minute — see the
    // run-examples.sh header.
    val readyTimeout = System.getenv("OPENRATE_READY_TIMEOUT_SECONDS")
        ?.takeIf { it.isNotEmpty() }
        ?.let { Duration.ofSeconds(it.toLong()) }
        ?: Duration.ofSeconds(60)

    // use {} stops the child process on every path out, including a failure
    // mid-example.
    //
    // The constructor waits for /healthz AND THEN for /readyz. Waiting only for
    // /healthz is the trap: that is liveness, answered 200 the moment the
    // listener binds, and conversions in that window return "unknown or
    // unreachable currency pair", which reads as a bad currency code rather
    // than as "not ready yet". /readyz answers the question actually being
    // asked, and its 503 names the source that is failing — so the catch below
    // prints a cause rather than a stopwatch reading.
    try {
        OpenRateSidecar(base = "ZAR", sources = "ecb,coinbase", ratesTimeout = readyTimeout).use { rates ->
            println("sidecar: ${rates.baseUrl}")
            println("meta: ${rates.meta().oneLine(280)}")

            val converted = rates.convert("USD", "ZAR", 100.0)
            println("USD->ZAR 100: ${converted.oneLine(300)}")
            if ("\"error\"" in converted) {
                // An example that prints an error object and exits 0 is the
                // false green this repo keeps finding.
                System.err.println("FAIL: the sidecar was ready but the conversion still errored")
                exitProcess(1)
            }

            println("rates(EUR): ${rates.rates("EUR").oneLine(280)}")

            // The error paths are status codes and error documents, not exceptions.
            println("bad amount: ${rates.get("/api/v1/convert?from=USD&to=ZAR&amount=NaN").oneLine(160)}")
            println("unknown currency: ${rates.convert("USD", "XYZ").oneLine(160)}")
        }
    } catch (e: OpenRateException) {
        // The message, not a stack trace. On a readiness timeout it carries
        // /readyz's own reason and the per-source error underneath it, e.g.
        //   openrate has no rates after 8s: no rates yet: no source has
        //   returned a usable quote (ecb: … connection refused)
        System.err.println("FAIL: ${e.message}")
        exitProcess(1)
    }
    println("sidecar stopped")
    println("done")
}

private fun String.oneLine(max: Int): String {
    val flat = replace(Regex("\\s+"), " ")
    return if (flat.length <= max) flat else flat.take(max) + "…"
}
