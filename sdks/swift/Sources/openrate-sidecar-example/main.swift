// openrate as a supervised child process, over HTTP.
//
//   go build -o /tmp/openrate ./cmd/openrate
//   OPENRATE_BINARY=/tmp/openrate swift run openrate-sidecar-example
//
// Compare with openrate-direct-example, which is the better default when you
// are on darwin/arm64. The sidecar earns its place when several processes
// should share one refresher, or when you are on a platform openrate has no
// prebuilt library for — which, for openrate, is most of them. (Not for the
// HTTP shell's hardening: a sidecar this SDK owns runs with the anti-scraping
// limiter off, because its only client is this process.) See the README.

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

    // Readiness is a different question, and the server answers it directly:
    // /readyz is 503 with a per-source diagnosis until the engine holds
    // currencies, then 200. Everything below this line would otherwise be
    // running against an empty book.
    //
    // The timeout is settable so the failure path is observable without
    // waiting a minute for it:
    //   OPENRATE_READY_TIMEOUT=5 HTTPS_PROXY=http://127.0.0.1:1 ./run.sh sidecar
    let readyTimeout =
        ProcessInfo.processInfo.environment["OPENRATE_READY_TIMEOUT"].flatMap(TimeInterval.init) ?? 45
    let t = Date()
    let readyz = try sc.waitReady(timeout: readyTimeout)
    print("ready:     after \(String(format: "%.3fs", Date().timeIntervalSince(t)))")
    // compact() because the 200 from /readyz is pretty-printed; see its doc.
    print("readyz:    \(first(compact(readyz), 180))")
    print("meta:      \(first(compact(try sc.meta()), 180))")

    // ---------------------------------------------------------------- convert
    // The same document the C ABI's "convert" method returns, over a different
    // transport. One wire contract.
    print("EUR->USD:  \(summarize(try sc.convert(from: "EUR", to: "USD", amount: 100)))")

    // ------------------------------------------------------------------ rates
    print("rates EUR: \(try sc.rates(base: "EUR").count) bytes")

    // The alignment, demonstrated rather than described. This request is an
    // error over the C ABI and a 404 here, and both carry the same sentence —
    // it used to be a 200 with an empty book, which a client checking only the
    // status code read as success.
    do {
        let bogus = compact(try sc.rates(base: "XXX"))
        print("rates XXX: UNEXPECTEDLY answered: \(first(bogus, 80))")
    } catch {
        print(
            "rates XXX: \(error)   "
                + "(the C ABI returns the same \"unknown base currency\")")
    }

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
