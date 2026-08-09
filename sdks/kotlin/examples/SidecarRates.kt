import org.vulos.openrate.kotlin.OpenRateSidecar
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

    // use {} stops the child process on every path out, including a failure
    // mid-example.
    //
    // The constructor waits for /healthz AND for the currency list to fill.
    // Waiting only for /healthz is the trap: openrate answers 200 the moment it
    // binds, and conversions in that window return "unknown or unreachable
    // currency pair", which reads as a bad currency code.
    OpenRateSidecar(base = "ZAR", sources = "ecb,coinbase").use { rates ->
        println("sidecar: ${rates.baseUrl}")
        println("meta: ${rates.meta().oneLine(280)}")

        val converted = rates.convert("USD", "ZAR", 100.0)
        println("USD->ZAR 100: ${converted.oneLine(300)}")
        if ("\"error\"" in converted) {
            // An example that prints an error object and exits 0 is the false
            // green this repo keeps finding.
            System.err.println("FAIL: the sidecar was ready but the conversion still errored")
            exitProcess(1)
        }

        println("rates(EUR): ${rates.rates("EUR").oneLine(280)}")

        // The error paths are status codes and error documents, not exceptions.
        println("bad amount: ${rates.get("/api/v1/convert?from=USD&to=ZAR&amount=NaN").oneLine(160)}")
        println("unknown currency: ${rates.convert("USD", "XYZ").oneLine(160)}")
    }
    println("sidecar stopped")
    println("done")
}

private fun String.oneLine(max: Int): String {
    val flat = replace(Regex("\\s+"), " ")
    return if (flat.length <= max) flat else flat.take(max) + "…"
}
