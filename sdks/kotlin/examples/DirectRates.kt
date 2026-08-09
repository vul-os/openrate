import org.vulos.openrate.OpenRateException
import org.vulos.openrate.kotlin.OpenRate

/**
 * openrate DIRECT from Kotlin — the engine inside this JVM, through libopenrate.
 *
 * The headline is the guarantee no sidecar can offer: an engine that provably
 * sends no packets. This example spends most of its time in that mode, feeding
 * the engine rates through `load` and converting against them, and only then
 * builds a refresher to show that fetching is a separate, explicit capability.
 *
 * Offline by default. Set OPENRATE_ALLOW_NETWORK=1 to have it also fetch from
 * ECB for real.
 *
 * Requires Java 22+ and --enable-native-access=ALL-UNNAMED.
 * Run with sdks/kotlin/run-examples.sh.
 */
fun main() {
    val library = OpenRate.findLibrary()
    println("library: $library")

    val allowNetwork = System.getenv("OPENRATE_ALLOW_NETWORK") == "1"

    // use {} closes the engine on every path out — and closing an engine also
    // releases every refresher built over it, so this is the whole cleanup
    // story even for the nested handle below.
    OpenRate.engine(base = "ZAR", quiet = true, library = library).use { engine ->
        println("abi version: ${engine.abiVersion}")
        println("open handles after creating the engine: ${engine.openHandles}")

        // 1. An engine with nothing in it says so rather than guessing.
        try {
            engine.convert("USD", "ZAR", 100.0)
            println("FAIL: an empty engine should not have converted anything")
        } catch (e: OpenRateException) {
            println("empty engine: ${e.message}")
        }

        // 2. load — the zero-network path. Rates you obtained yourself.
        val edges = """
            {"built_at":"2026-08-09T00:00:00Z","edges":[
              {"from":"USD","to":"ZAR","rate":18.5,"source":"mine"},
              {"from":"EUR","to":"USD","rate":1.09,"source":"mine"}
            ]}
        """.trimIndent()
        println("load: ${engine.load(edges).oneLine(200)}")

        // 3. Direct, and across a hop the graph works out itself.
        println("USD->ZAR: ${engine.convert("USD", "ZAR", 100.0).oneLine(220)}")
        println("EUR->ZAR (2 hops): ${engine.convert("EUR", "ZAR").oneLine(220)}")

        // 4. meta reports no sources: nothing is refreshing this engine.
        println("meta: ${engine.meta().oneLine(200)}")

        // 5. withRefresher builds the fetching half, runs the block, and closes
        //    it — leaving the engine open. Constructing it opens no socket.
        engine.withRefresher(sources = "ecb") { refresher ->
            println("open handles with a refresher: ${engine.openHandles}")
            println("status before any fetch: ${refresher.status().oneLine(200)}")

            // The ABI enforces the split rather than trusting the caller.
            try {
                engine.call("refresh", "{}")
                println("FAIL: an engine should refuse \"refresh\"")
            } catch (e: OpenRateException) {
                println("engine refuses \"refresh\": ${e.message}")
            }

            if (allowNetwork) {
                println("OPENRATE_ALLOW_NETWORK=1 — fetching from ECB for real…")
                println("refresh: ${refresher.refresh(timeoutMs = 30_000).oneLine(240)}")
                println("USD->EUR live: ${engine.convert("USD", "EUR").oneLine(200)}")
            } else {
                println("OPENRATE_ALLOW_NETWORK is unset, so nothing was fetched.")
                println("This process has opened no socket on openrate's behalf.")
            }
        }
        println("open handles after the refresher closed: ${engine.openHandles}")
    }

    // 6. Handle accounting, so the use {} above is evidence rather than habit.
    OpenRate.engine(quiet = true, library = library).use { probe ->
        val open = probe.openHandles
        probe.close()
        val after = probe.openHandles
        println("handle accounting: $open open, $after after close")
        if (after != open - 1) {
            println("FAIL: closing a handle should have decremented the count")
        }
        try {
            probe.meta()
            println("FAIL: a closed engine should not have answered")
        } catch (e: OpenRateException) {
            println("after close: ${e.message}")
        }
    }

    println("done")
}

private fun String.oneLine(max: Int): String {
    val flat = replace(Regex("\\s+"), " ")
    return if (flat.length <= max) flat else flat.take(max) + "…"
}
