// Sidecar mode: `openrate serve` as a supervised child process.
//
// The SDK spawns it, waits for it, and stops it, so the user never runs a
// server by hand.
//
// SPDX-License-Identifier: MIT OR Apache-2.0

import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// A failure starting or talking to the sidecar.
public enum SidecarError: Error, CustomStringConvertible {
    case binaryNotFound
    case spawn(String)
    /// The child never started listening.
    case notLive(String)
    /// The child listened but never acquired any rates. Carries the `reason`
    /// and the per-source errors from the last `/readyz` 503, because "timed
    /// out" alone is useless and the server already knows which source failed
    /// and why.
    case notReady(String)
    /// A non-200 response. Carries the code and the body, because the body is
    /// where openrate puts its `{"error": "..."}` object.
    case status(Int, String)
    case transport(String)

    public var description: String {
        switch self {
        case .binaryNotFound:
            return """
                openrate binary not found. Set OPENRATE_BINARY, put `openrate` on PATH, or build \
                it: `go build -o sdks/swift/bin/openrate ./cmd/openrate`
                """
        case .spawn(let m): return "failed to spawn openrate: \(m)"
        case .notLive(let m): return "openrate never started listening: \(m)"
        // Reads as "openrate has no rates after 30s: <reason> (ecb: <error>)".
        case .notReady(let m): return "openrate has no rates \(m)"
        case .status(let c, let b): return "HTTP \(c): \(b)"
        case .transport(let m): return "transport: \(m)"
        }
    }
}

/// A running `openrate serve` owned by this program.
///
/// **`deinit` stops the child.** Swift has no scope-exit `defer` for object
/// lifetime, but ARC gives the same guarantee: drop the last reference — on the
/// happy path or on a `throw` — and the process is terminated and reaped.
public final class Sidecar: @unchecked Sendable {
    /// `http://127.0.0.1:<port>`.
    public let baseURL: String

    private let process: Process
    private let session: URLSession

    /// Spawns the child on a free loopback port and blocks until it is
    /// **listening** — not until it has rates. See ``waitReady(timeout:)``.
    ///
    /// On any failure it terminates whatever it started.
    ///
    /// - Parameters:
    ///   - binary: path to the openrate binary. `nil` resolves
    ///     `$OPENRATE_BINARY`, then `bin/openrate` beside the package, then
    ///     `openrate` on `PATH`.
    ///   - base: default presentation base currency.
    ///   - sources: comma-separated sources for the child to fetch.
    ///   - ui: serve the embedded web console. Off by default — a nice thing to
    ///     give a human and dead weight in a supervised sidecar.
    public init(
        binary: String? = nil,
        base: String = "ZAR",
        sources: String? = nil,
        ui: Bool = false,
        liveTimeout: TimeInterval = 10
    ) throws {
        let bin = try binary ?? Sidecar.resolveBinary()
        let port = try Sidecar.freePort()
        let addr = "127.0.0.1:\(port)"
        baseURL = "http://" + addr

        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 30
        session = URLSession(configuration: cfg)

        process = Process()
        process.executableURL = URL(fileURLWithPath: bin)
        var args = ["-addr", addr, "-base", base, "-ui=\(ui)"]
        if let sources { args += ["-sources", sources] }
        process.arguments = args
        // The child listens on loopback and serves exactly one client: this
        // process. openrate's limiter is anti-scraping for a public deployment
        // and there is no stranger here to throttle — while a legitimate batch
        // of conversions sails past the 120/min default and takes a 429 from
        // our own sidecar.
        var childEnv = ProcessInfo.processInfo.environment
        childEnv["OPENRATE_RATELIMIT"] = "0"
        process.environment = childEnv
        // The child's logs are the operator's, not ours to swallow.
        process.standardOutput = FileHandle.standardError
        process.standardError = FileHandle.standardError

        do {
            try process.run()
        } catch {
            throw SidecarError.spawn(error.localizedDescription)
        }

        do {
            try waitLive(timeout: liveTimeout)
        } catch {
            // deinit would do this too; doing it here keeps the failure path
            // obvious rather than relying on a reader knowing that it would.
            stop()
            throw error
        }
    }

    deinit { stop() }

    /// Terminates and reaps the child. Idempotent.
    public func stop() {
        guard process.isRunning else { return }
        process.terminate()
        process.waitUntilExit()
    }

    /// Polls `/healthz` — **liveness only**. It says nothing about rates.
    private func waitLive(timeout: TimeInterval) throws {
        let deadline = Date().addingTimeInterval(timeout)
        var last = "connection refused"
        while Date() < deadline {
            if !process.isRunning {
                throw SidecarError.notLive("child exited with status \(process.terminationStatus)")
            }
            do {
                _ = try get("/healthz")
                return
            } catch {
                last = "\(error)"
            }
            Thread.sleep(forTimeInterval: 0.05)
        }
        throw SidecarError.notLive(last)
    }

    /// Blocks until `GET /readyz` answers 200, and returns that body.
    ///
    /// This is the HTTP equivalent of the library's `Refresher.ready`, and it is
    /// a different question from `/healthz`, which answers `ok` the instant the
    /// listener binds. Readiness is "the snapshot has currencies in it": until
    /// then `/readyz` is a `503` whose body names every source and why it has
    /// not produced a quote, which is what the `notReady` error carries instead
    /// of a bare timeout.
    ///
    /// # Why a fixed interval is right here
    ///
    /// `openrate serve` ships an anti-scraping rate limiter on by default — 120
    /// requests per minute per IP — but it applies to `/api/` paths only, and
    /// `/readyz` is not one. Polling it cannot spend the budget the first real
    /// conversion needs, so there is nothing for a backoff to protect. This
    /// SDK's earlier readiness check polled `/api/v1/meta`, which *is* under
    /// `/api/`, and needed a 100 ms → 2 s backoff purely to stay under the
    /// limit of the server it was waiting for.
    @discardableResult
    public func waitReady(timeout: TimeInterval = 45) throws -> String {
        let deadline = Date().addingTimeInterval(timeout)
        var last = "/readyz never answered"
        while true {
            do {
                return try get("/readyz")
            } catch SidecarError.status(503, let body) {
                // Not ready, and the body says why.
                last = OpenRate.notReadyReason(in: body) ?? body
            } catch {
                // Not listening yet, or gone again. The transport text is the
                // most specific thing known.
                last = "\(error)"
            }
            if Date() >= deadline { break }
            Thread.sleep(forTimeInterval: 0.15)
        }
        throw SidecarError.notReady("after \(Int(timeout))s: \(last)")
    }

    // MARK: API

    /// `GET /healthz`. Liveness.
    public func healthz() throws -> String { try get("/healthz") }

    /// `GET /api/v1/meta`.
    public func meta() throws -> String { try get("/api/v1/meta") }

    /// `GET /api/v1/convert?from=&to=&amount=`.
    ///
    /// The response nests the provenance under `"rate"` — `rate.rate`,
    /// `rate.hops`, `rate.quality.grade` — where the Go library's
    /// `fx.Conversion` is flat. Same information, different shape.
    public func convert(from: String, to: String, amount: Double) throws -> String {
        try get("/api/v1/convert?from=\(esc(from))&to=\(esc(to))&amount=\(amount)")
    }

    /// `GET /api/v1/rates?base=`.
    ///
    /// **An unknown base answers `200` with an empty book here**, where the
    /// library and the C ABI both return an error. A caller that checks only the
    /// status code will read "no rates" as success.
    public func rates(base: String) throws -> String {
        try get("/api/v1/rates?base=\(esc(base))")
    }

    // MARK: HTTP

    /// `GET path`, returning the body and throwing on any non-200.
    public func get(_ path: String) throws -> String {
        var req = URLRequest(url: URL(string: baseURL + path)!)
        req.httpMethod = "GET"

        var result: Result<String, Error>!
        let sem = DispatchSemaphore(value: 0)
        session.dataTask(with: req) { data, response, error in
            defer { sem.signal() }
            if let error {
                result = .failure(SidecarError.transport(error.localizedDescription))
                return
            }
            let body = String(data: data ?? Data(), encoding: .utf8) ?? ""
            if let http = response as? HTTPURLResponse, http.statusCode != 200 {
                result = .failure(SidecarError.status(http.statusCode, body))
                return
            }
            result = .success(body)
        }.resume()
        // Safe HERE: the completion runs on a URLSession delegate queue, never
        // on the thread being blocked. Do not copy this into code running on
        // Swift's cooperative pool, where blocking a thread can deadlock.
        sem.wait()
        return try result.get()
    }

    private func esc(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? s
    }

    // MARK: Resolution

    static func resolveBinary() throws -> String {
        let env = ProcessInfo.processInfo.environment
        if let explicit = env["OPENRATE_BINARY"], !explicit.isEmpty { return explicit }

        let name = "openrate"
        // #filePath is Sources/OpenRate/Sidecar.swift; the package root is 3 up.
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let bundled = packageRoot.appendingPathComponent("bin").appendingPathComponent(name).path
        if FileManager.default.fileExists(atPath: bundled) { return bundled }

        for dir in (env["PATH"] ?? "").split(separator: ":") {
            let candidate = String(dir) + "/" + name
            if FileManager.default.isExecutableFile(atPath: candidate) { return candidate }
        }
        throw SidecarError.binaryNotFound
    }

    /// Binds port 0, reads the port the kernel picked, and releases it.
    ///
    /// Inherently racy — something else can take the port between the close and
    /// the child's bind. Every "find a free port" helper has this race; the
    /// alternative is passing the listening socket to the child, which openrate
    /// does not support.
    static func freePort() throws -> UInt16 {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { throw SidecarError.spawn("socket() failed") }
        defer { Foundation.close(fd) }
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = 0
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        let bound = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bound == 0 else { throw SidecarError.spawn("bind() failed") }
        var out = sockaddr_in()
        var len = socklen_t(MemoryLayout<sockaddr_in>.size)
        let got = withUnsafeMutablePointer(to: &out) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &len) }
        }
        guard got == 0 else { throw SidecarError.spawn("getsockname() failed") }
        return UInt16(bigEndian: out.sin_port)
    }
}

// MARK: - JSON shape helpers

/// Strips insignificant whitespace from a JSON document, leaving string
/// literals untouched.
///
/// # Why this exists
///
/// **openrate's HTTP API pretty-prints and its C ABI does not.**
/// `GET /api/v1/meta` answers with two-space indentation, so `"currencies": [`
/// with a space, while `openrate_call(h, "meta", …)` returns `"currencies":[`
/// compact.
///
/// That is not a style note. The Rust SDK's first readiness check tested for the
/// compact form and **timed out after 45 seconds against a server that had been
/// serving 30 currencies the whole time** — and its unit test passed, because
/// the test's fixture was hand-written compact JSON rather than a captured
/// response. The tests here use verbatim captures for exactly that reason.
public func compact(_ json: String) -> String {
    var out = ""
    out.reserveCapacity(json.count)
    var inString = false
    var escaped = false
    for c in json {
        if inString {
            out.append(c)
            if escaped {
                escaped = false
            } else if c == "\\" {
                escaped = true
            } else if c == "\"" {
                inString = false
            }
            continue
        }
        if c == "\"" {
            inString = true
            out.append(c)
        } else if !c.isWhitespace {
            out.append(c)
        }
    }
    return out
}

/// Whether a `/api/v1/meta` document reports at least one currency.
/// Tolerant of pretty-printing; see ``compact(_:)``.
///
/// This is **not** how ``Sidecar/waitReady(timeout:)`` decides — that asks
/// `/readyz`, which answers the question directly and says why when the answer
/// is no. Kept because a caller reading `meta()` still wants it.
public func hasCurrencies(_ meta: String) -> Bool {
    let c = compact(meta)
    return c.contains("\"currencies\":[") && !c.contains("\"currencies\":[]")
}

/// Pulls `name: last_error` pairs out of any document carrying openrate's
/// `sources` array — `/readyz` and `/api/v1/meta` both do — for an error
/// message.
///
/// Uses `JSONSerialization` rather than substring scanning, because openrate's
/// `last_error` values are Go `net/http` errors that embed the URL **in
/// quotes** — `Get \"https://…\": dial tcp: i/o timeout` — and a scan to the
/// first `"` returns `Get \`, the useless half. Foundation is already linked
/// here, so unlike the Rust SDK there is no reason to hand-roll it.
///
/// `last_error` is `omitempty`: a source that has not been tried yet has no
/// such key, and contributes nothing rather than `ecb: `.
public func sourceErrors(in document: String) -> String {
    guard
        let data = document.data(using: .utf8),
        let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
        let sources = obj["sources"] as? [[String: Any]]
    else { return "" }
    return
        sources
        .compactMap { s -> String? in
            guard let err = s["last_error"] as? String, !err.isEmpty else { return nil }
            return "\((s["name"] as? String) ?? "?"): \(err)"
        }
        .joined(separator: "; ")
}

/// The readable half of a `/readyz` 503: its `reason`, followed by
/// `name: last_error` for every source that has one.
///
/// `nil` when the body is not that document, so a caller can fall back to
/// printing it raw rather than swallowing something unexpected.
///
/// Note the two shapes this must survive: the 200 goes through openrate's
/// pretty-printing writer and the 503 does not, so the not-ready body is
/// compact and the ready one is indented. Parsing rather than scanning is what
/// makes that a non-issue — the substring check this replaced would have had to
/// know.
public func notReadyReason(in readyBody: String) -> String? {
    guard
        let data = readyBody.data(using: .utf8),
        let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
    else { return nil }
    let reason = obj["reason"] as? String
    let errs = sourceErrors(in: readyBody)
    if errs.isEmpty { return reason }
    guard let reason else { return errs }
    return "\(reason) (\(errs))"
}
