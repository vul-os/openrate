@file:JvmName("OpenRateDirectKt")

package org.vulos.openrate.kotlin

import org.vulos.openrate.OpenRateDirect
import java.nio.file.Path

/**
 * Kotlin over [org.vulos.openrate.OpenRateDirect] — openrate running **in this
 * JVM** through libopenrate's C ABI.
 *
 * A thin, idiomatic layer rather than a second binding: the FFM calls, the
 * memory rules and the handle lifecycle stay in the Java class, because two
 * bindings to one C ABI is two places for a use-after-free.
 *
 * **On the JVM the sidecar is the recommended default** — see `README.md`. The
 * exception is the reason this class exists at all: an [OpenRateEngine]
 * provably sends no packets, and no sidecar can offer that.
 *
 * ```kotlin
 * OpenRate.engine(base = "ZAR").use { engine ->
 *     engine.load(myRatesJson)
 *     println(engine.convert("USD", "ZAR", 100.0))
 * }
 * ```
 */
public class OpenRateEngine internal constructor(
    private val delegate: OpenRateDirect,
) : AutoCloseable {

    /** The openrate version the loaded shared library was built from. */
    public val abiVersion: String get() = delegate.abiVersion()

    /** The library this engine is running out of. */
    public val libraryPath: Path get() = delegate.libraryPath()

    /**
     * Handles currently open inside the library — engines and refreshers
     * together. Diagnostic only, and exactly what a test wants: assert it comes
     * back to where it started and a leak is a failure rather than a puzzle.
     */
    public val openHandles: Long get() = delegate.openHandles()

    /** Run any engine method: `convert`, `rates`, `meta`, `load`. */
    public fun call(method: String, requestJson: String? = null): String =
        delegate.call(method, requestJson)

    /**
     * Install rates you obtained yourself. **The zero-network path** — this is
     * how an engine gets data without a refresher existing anywhere.
     */
    public fun load(edgesJson: String): String = call("load", edgesJson)

    /** Convert an amount. Omitted currencies mean the engine's default base. */
    public fun convert(from: String, to: String, amount: Double = 1.0): String =
        call("convert", """{"from":${from.json()},"to":${to.json()},"amount":$amount}""")

    /** The whole book against a base. `rates[X].rate` reads "1 base = rate X". */
    public fun rates(base: String? = null): String =
        call("rates", if (base == null) null else """{"base":${base.json()}}""")

    /**
     * Default base, build time, currency list, and the fetch status of every
     * refresher over this engine — `[]` for an engine nobody refreshes, which
     * is the zero-network claim in the library's own words.
     */
    public fun meta(): String = call("meta")

    /**
     * Build a [OpenRateRefresher] over this engine.
     *
     * **This is the only object in openrate that can open a socket, and
     * constructing it still does not.** Fetching begins at
     * [OpenRateRefresher.refresh] or [OpenRateRefresher.start].
     */
    public fun newRefresher(
        sources: String? = null,
        intervalMs: Long? = null,
        fetchTimeoutMs: Long? = null,
        quiet: Boolean = false,
    ): OpenRateRefresher {
        val fields = buildList {
            if (sources != null) add(""""sources":${sources.json()}""")
            if (intervalMs != null) add(""""interval_ms":$intervalMs""")
            if (fetchTimeoutMs != null) add(""""fetch_timeout_ms":$fetchTimeoutMs""")
            add(""""quiet":$quiet""")
        }
        return OpenRateRefresher(delegate.newRefresher(fields.joinToString(",", "{", "}")))
    }

    /**
     * Build a refresher, run [block] with it, and close it — leaving the engine
     * open. The shape that makes "fetch once, then go offline" one expression.
     */
    public fun <R> withRefresher(
        sources: String? = null,
        quiet: Boolean = true,
        block: (OpenRateRefresher) -> R,
    ): R = newRefresher(sources = sources, quiet = quiet).use(block)

    /**
     * Release the engine — and, with it, every refresher built over it,
     * including a background loop started with [OpenRateRefresher.start].
     * Idempotent.
     */
    override fun close(): Unit = delegate.close()
}

/**
 * The fetching half. A separate handle with its own lifetime, because in
 * openrate fetching is a separate capability rather than a flag.
 */
public class OpenRateRefresher internal constructor(
    private val delegate: OpenRateDirect,
) : AutoCloseable {

    /** Per-source fetch status. Answers without touching the network. */
    public fun status(): String = delegate.call("status")

    /** One synchronous fetch of every source. **This opens sockets.** */
    public fun refresh(timeoutMs: Long? = null): String =
        delegate.call("refresh", if (timeoutMs == null) null else """{"timeout_ms":$timeoutMs}""")

    /** Start the background loop — the only thread this library starts itself. */
    public fun start(): String = delegate.call("start")

    /** Stop the background loop and wait for it to exit. */
    public fun stop(): String = delegate.call("stop")

    /**
     * Block until the engine holds at least one currency. It does **not**
     * fetch: something must be refreshing, or this waits.
     */
    public fun ready(timeoutMs: Long = 5_000): String =
        delegate.call("ready", """{"timeout_ms":$timeoutMs}""")

    /** Release the refresher, stopping its loop. Idempotent. */
    override fun close(): Unit = delegate.close()
}

/** Entry points for the direct path. */
public object OpenRate {

    /**
     * Create an **engine**: the object that computes and cannot fetch.
     *
     * Creating one starts no thread, opens no socket, reads no environment
     * variable and sends no packet.
     */
    public fun engine(
        base: String? = null,
        quiet: Boolean = false,
        library: Path? = null,
    ): OpenRateEngine {
        val fields = buildList {
            if (base != null) add(""""base":${base.json()}""")
            add(""""quiet":$quiet""")
        }
        val config = fields.joinToString(",", "{", "}")
        return OpenRateEngine(
            if (library == null) OpenRateDirect.openEngine(config)
            else OpenRateDirect.openEngine(library, config),
        )
    }

    /** Where the direct path would load its library from, without loading it. */
    public fun findLibrary(): Path = OpenRateDirect.findLibrary()
}

/**
 * Quote a string as a JSON scalar.
 *
 * Currency codes are `[A-Z]{3,}` in practice, but building JSON by
 * concatenation without escaping is how injection bugs are written, and a
 * five-line escaper is cheaper than a JSON dependency in a library whose whole
 * point is not having one.
 */
private fun String.json(): String {
    val sb = StringBuilder(length + 2)
    sb.append('"')
    for (c in this) {
        when {
            c == '"' -> sb.append("\\\"")
            c == '\\' -> sb.append("\\\\")
            c == '\n' -> sb.append("\\n")
            c == '\r' -> sb.append("\\r")
            c == '\t' -> sb.append("\\t")
            c < ' ' -> sb.append("\\u%04x".format(c.code))
            else -> sb.append(c)
        }
    }
    sb.append('"')
    return sb.toString()
}
