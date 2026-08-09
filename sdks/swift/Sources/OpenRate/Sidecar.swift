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
    /// The child listened but never acquired any rates. Carries the per-source
    /// errors from `/api/v1/meta`, because "timed out" alone is useless and
    /// meta already knows which source failed and why.
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
        case .notReady(let m): return "openrate is listening but has no rates: \(m)"
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

    /// Blocks until `/api/v1/meta` reports at least one currency, and returns
    /// that meta document.
    ///
    /// This is the HTTP equivalent of the library's `Refresher.ready`, and it is
    /// a different question from `/healthz`, which answers `ok` the instant the
    /// listener binds.
    ///
    /// # Why this backs off instead of polling on a fixed interval
    ///
    /// `openrate serve` ships an anti-scraping rate limiter on by default:
    /// **120 API requests per minute per IP**. A readiness loop at 200 ms is 300
    /// a minute and gets rate-limited *by the server it is waiting for* —
    /// `HTTP 429 {"error":"rate limited — slow down."}` a few seconds in. The
    /// Rust SDK's first version did exactly that and failed on its first run
    /// against a real binary. The delay here starts at 100 ms and doubles to a
    /// 2 s ceiling: about 25 requests over a 45-second wait.
    @discardableResult
    public func waitReady(timeout: TimeInterval = 45) throws -> String {
        let deadline = Date().addingTimeInterval(timeout)
        var last = ""
        var delay: TimeInterval = 0.1
        while Date() < deadline {
            let meta = try self.meta()
            if OpenRate.hasCurrencies(meta) { return meta }
            last = meta
            Thread.sleep(forTimeInterval: delay)
            delay = min(delay * 2, 2.0)
        }
        let why = OpenRate.sourceErrors(in: last)
        throw SidecarError.notReady(
            why.isEmpty ? "no rates within \(Int(timeout))s" : "no rates within \(Int(timeout))s: \(why)")
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
public func hasCurrencies(_ meta: String) -> Bool {
    let c = compact(meta)
    return c.contains("\"currencies\":[") && !c.contains("\"currencies\":[]")
}

/// Pulls `name: last_error` pairs out of a meta document, for an error message.
///
/// Uses `JSONSerialization` rather than substring scanning, because openrate's
/// `last_error` values are Go `net/http` errors that embed the URL **in
/// quotes** — `Get \"https://…\": dial tcp: i/o timeout` — and a scan to the
/// first `"` returns `Get \`, the useless half. Foundation is already linked
/// here, so unlike the Rust SDK there is no reason to hand-roll it.
public func sourceErrors(in meta: String) -> String {
    guard
        let data = meta.data(using: .utf8),
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
