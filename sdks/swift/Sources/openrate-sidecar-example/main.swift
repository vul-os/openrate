// openrate as a supervised child process, over HTTP.
//
//   go build -o /tmp/openrate ./cmd/openrate
//   OPENRATE_BINARY=/tmp/openrate swift run openrate-sidecar-example
//
// Compare with openrate-direct-example, which is the better default when you
// are on darwin/arm64. The sidecar earns its place when several processes
// should share one refresher, when you want the HTTP shell's rate limiter and
// CORS policy, or when you are on a platform openrate has no prebuilt library
// for — which, for openrate, is most of them. See the README.

import Foundation
import OpenRate

func first(_ s: String, _ n: Int) -> String {
    s.count <= n ? s : String(s.prefix(n)) + "…"
}

func summarize(_ json: String) -> String {
    guard
        let data = json.data(using: .utf8),
        let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
    else { return "(unparseable)" }
    let result = obj["result"] as? Double ?? .nan
    let rateObj = obj["rate"] as? [String: Any] ?? [:]
    let rate = rateObj["rate"] as? Double ?? .nan
    let hops = rateObj["hops"] as? Int ?? -1
    let grade = (rateObj["quality"] as? [String: Any])?["grade"] as? String ?? "?"
    return "result=\(result) rate=\(rate) hops=\(hops) grade=\(grade)"
}

do {
    // Spawns the child on a free loopback port and blocks until it is
    // LISTENING. On failure it terminates what it started, so this `try` cannot
    // leave a server behind.
    let sc = try Sidecar(base: "EUR", sources: "ecb")
    // From here the process is owned by `sc` and stopped in deinit — on the
    // error paths below as much as at the end.
    print("sidecar:   \(sc.baseURL)")

    // ------------------------------------------------------------------ ready
    // Liveness first: already true, and it means almost nothing.
    print("healthz:   \(try sc.healthz())  (liveness only — no rates implied)")

    // Readiness is a different question. /healthz answered before any source
    // had been fetched; waitReady polls /api/v1/meta, with backoff, until the
    // engine actually holds currencies.
    let t = Date()
    let meta = try sc.waitReady(timeout: 45)
    print("ready:     after \(String(format: "%.3fs", Date().timeIntervalSince(t)))")
    // compact() because the HTTP API pretty-prints; see its doc comment.
    print("meta:      \(first(compact(meta), 180))")

    // ---------------------------------------------------------------- convert
    // The same document the C ABI's "convert" method returns, over a different
    // transport. One wire contract.
    print("EUR->USD:  \(summarize(try sc.convert(from: "EUR", to: "USD", amount: 100)))")

    // ------------------------------------------------------------------ rates
    print("rates EUR: \(try sc.rates(base: "EUR").count) bytes")

    // The deliberate difference, demonstrated rather than described. Over the C
    // ABI this same request is an error; here it is a 200 with an empty book,
    // and a client that checks only the status code reads that as success.
    let bogus = compact(try sc.rates(base: "XXX"))
    print(
        "rates XXX: HTTP 200, empty book = \(bogus.contains("\"rates\":{}"))   "
            + "(the C ABI returns \"unknown base currency\")")

    // ------------------------------------------------------------ error path
    do {
        _ = try sc.convert(from: "XXX", to: "YYY", amount: 1)
        print("bogus:     unexpectedly answered")
    } catch {
        // A pair the server cannot reach is a 404 with a JSON error body,
        // surfaced verbatim rather than paraphrased into a status code.
        print("bogus:     \(first("\(error)", 160))")
    }

    sc.stop()
    print("stopped:   child reaped")
} catch {
    FileHandle.standardError.write(Data("error: \(error)\n".utf8))
    exit(1)
}
