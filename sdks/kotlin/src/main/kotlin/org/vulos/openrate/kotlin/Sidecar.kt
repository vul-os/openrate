@file:JvmName("OpenRateSidecarKt")

package org.vulos.openrate.kotlin

import org.vulos.openrate.OpenRate as JavaOpenRate
import java.time.Duration

/**
 * Kotlin over [org.vulos.openrate.OpenRate] — openrate as a child process on
 * `127.0.0.1`, spoken to over HTTP.
 *
 * **The recommended default on the JVM.** No native library, no
 * `--enable-native-access`, no platform matrix — it runs on Windows, where the
 * shared library does not exist.
 *
 * ```kotlin
 * OpenRateSidecar(base = "ZAR", sources = "ecb,coinbase").use { rates ->
 *     println(rates.convert("USD", "ZAR", 100.0))
 * }
 * ```
 *
 * **This fetches.** `openrate serve` starts its refresher at startup, so
 * standing one up means outbound requests. If you want "no packets unless I
 * ask", that is [OpenRateEngine] on the direct path, and it is a guarantee
 * rather than a setting.
 *
 * ### No coroutines here, on purpose
 *
 * llmux's Kotlin SDK depends on kotlinx-coroutines because chat streaming needs
 * `Flow`. **openrate has no streaming** — there is deliberately no
 * `openrate_stream`, because it answers from a snapshot it already holds — so
 * every operation is one request that returns one document. Adding a coroutines
 * dependency to put `suspend` in front of four synchronous calls would be a
 * dependency bought with nothing. Call these from `Dispatchers.IO` if you are
 * in a coroutine; that is one line at the call site instead of a jar in
 * everyone's build.
 */
public class OpenRateSidecar(
    base: String? = null,
    sources: String? = null,
    fixedPort: Int? = null,
    ui: Boolean = false,
    extraEnv: Map<String, String>? = null,
    /**
     * Per-IP API requests/minute for the child; `0` disables the limiter, and
     * that is the default.
     *
     * The child listens on loopback and serves exactly one client: this
     * process. The limiter is anti-scraping for a public deployment and there
     * is no stranger here to throttle — while a legitimate batch of
     * conversions would sail past the 120/min default and take a 429 from our
     * own sidecar. Pass `120` to put the binary's default back.
     */
    ratelimit: Int = 0,
    healthTimeout: Duration = Duration.ofSeconds(30),
    /**
     * Wait for the server to actually hold rates, not merely to be listening.
     *
     * `/healthz` is a liveness probe: openrate answers 200 the moment it binds,
     * while its first fetch is in flight, and conversions in that window come
     * back "unknown or unreachable currency pair" — which reads as a bad
     * currency code rather than as "not ready yet".
     *
     * `/readyz` is the readiness probe that settles it: 200 once the snapshot
     * holds currencies, and until then 503 with the reason and every source's
     * last error, so a wait that times out says what failed instead of only
     * how long it took.
     */
    waitForRates: Boolean = true,
    ratesTimeout: Duration = Duration.ofSeconds(60),
) : AutoCloseable {

    private val delegate: JavaOpenRate

    init {
        val opts = JavaOpenRate.Options()
        opts.base = base
        opts.sources = sources
        opts.port = fixedPort
        opts.ui = ui
        opts.env = extraEnv
        opts.ratelimit = ratelimit
        opts.timeout = healthTimeout
        opts.waitForRates = waitForRates
        opts.ratesTimeout = ratesTimeout
        delegate = JavaOpenRate.start(opts)
    }

    /** `http://127.0.0.1:<port>`. */
    public val baseUrl: String get() = delegate.baseUrl()

    /** `GET /api/v1/meta`. */
    public fun meta(): String = delegate.meta()

    /** `GET /api/v1/rates?base=…`. */
    public fun rates(base: String): String = delegate.rates(base)

    /** `GET /api/v1/convert?from=…&to=…&amount=…`. */
    public fun convert(from: String, to: String, amount: Double = 1.0): String =
        delegate.convert(from, to, amount)

    /** One raw GET, for anything this wrapper does not name. */
    public fun get(path: String): String = delegate.get(path)

    /** Stop the child process. Idempotent; `use {}` calls it on every path out. */
    override fun close(): Unit = delegate.close()
}
