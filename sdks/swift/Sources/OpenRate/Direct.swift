// Direct mode: openrate running inside this process, over the C ABI.
//
// Swift's C interop is one of its genuine strengths, and this file uses the
// plainest form of it: dlopen/dlsym plus @convention(c) function types. No
// module map, no bridging header, no unsafeFlags in Package.swift — which
// matters, because a target with unsafeFlags cannot be depended on by another
// package.
//
// # The engine/refresher split is the whole point
//
//   Engine     computes.  openrate_new() starts no thread, opens no socket,
//                         reads no environment variable and sends no packet.
//                         Methods: convert, rates, meta, load.
//
//   Refresher  fetches.   openrate_refresher_new() is a SEPARATE call with its
//                         own handle and its own lifetime, and it is the only
//                         thing here that can open a socket. Even constructing
//                         it sends nothing.
//                         Methods: status, refresh, start, stop, ready.
//
// The split is enforced at the ABI: an Engine handle REFUSES "refresh". Swift
// puts a second fence in front of that one — `refresh()` exists only on
// `Refresher`, so the mistake does not compile.
//
// # No streaming
//
// There is deliberately no openrate_stream and no AsyncSequence here. openrate
// answers from a snapshot it already holds, so there is no incremental
// operation to stream. llmux, which shares this ABI shape, does define
// llmux_stream, because chat streaming is its main event.
//
// SPDX-License-Identifier: MIT OR Apache-2.0

#if canImport(Darwin)
import Darwin
#else
import Glibc
#endif
import Foundation

// MARK: - Errors

/// Everything that can go wrong on the direct path.
public enum OpenRateError: Error, CustomStringConvertible {
    /// No `libopenrate` could be found. Carries the paths that were tried.
    case libraryNotFound([String])
    /// `dlopen` failed. Carries the platform's message.
    case load(String)
    /// A symbol was missing — usually a library that is not openrate.
    case missingSymbol(String)
    /// The loaded library reports a different version than expected.
    case versionMismatch(loaded: String, expected: String)
    /// openrate itself failed. The string is the library's own message, which
    /// is plain UTF-8 text and deliberately **not** JSON — do not parse it.
    case openrate(String)

    public var description: String {
        switch self {
        case .libraryNotFound(let tried):
            return """
                libopenrate not found. Set OPENRATE_LIBRARY to its path, or build it with \
                `scripts/build-ffi.sh`. Tried: \(tried.joined(separator: ", "))
                """
        case .load(let msg):
            return "loading libopenrate: \(msg)"
        case .missingSymbol(let name):
            return "libopenrate is missing the symbol \(name) — is this really libopenrate?"
        case .versionMismatch(let loaded, let expected):
            return """
                libopenrate reports version \(loaded), expected \(expected) — a stale library is \
                earlier on your load path
                """
        case .openrate(let msg):
            return msg
        }
    }
}

// MARK: - The ABI

typealias AbiVersionFn = @convention(c) () -> UnsafePointer<CChar>?
typealias NewFn = @convention(c) (
    UnsafePointer<CChar>?, UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>?
) -> UInt64
typealias RefresherNewFn = @convention(c) (
    UInt64, UnsafePointer<CChar>?, UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>?
) -> UInt64
typealias CloseFn = @convention(c) (UInt64) -> Void
typealias CallFn = @convention(c) (
    UInt64, UnsafePointer<CChar>?, UnsafePointer<CChar>?,
    UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>?
) -> UnsafeMutablePointer<CChar>?
typealias FreeFn = @convention(c) (UnsafeMutablePointer<CChar>?) -> Void
typealias OpenHandlesFn = @convention(c) () -> UInt64

/// The loaded library and its seven resolved symbols.
///
/// # Why this is never unloaded
///
/// There is no `dlclose` anywhere in this file, and that is deliberate.
/// `libopenrate` is a Go `c-shared` object: loading it starts the Go runtime
/// and its threads, and Go has no way to shut that down, so unloading would
/// unmap code those threads are still executing.
///
/// The Rust binding for llmux's equivalent library was written the other way
/// first — unloading with each handle — and a 200-cycle open/close loop hung
/// and had to be killed. `Library.shared(at:)` caches one instance per path for
/// the life of the process.
final class Library: @unchecked Sendable {
    let abiVersion: AbiVersionFn
    let new: NewFn
    let refresherNew: RefresherNewFn
    let close: CloseFn
    let call: CallFn
    let free: FreeFn
    let openHandles: OpenHandlesFn

    private static let lock = NSLock()
    private static var loaded: [String: Library] = [:]

    static func shared(at path: String) throws -> Library {
        lock.lock()
        defer { lock.unlock() }
        if let existing = loaded[path] { return existing }
        let lib = try Library(path: path)
        loaded[path] = lib
        return lib
    }

    private init(path: String) throws {
        guard let handle = dlopen(path, RTLD_NOW | RTLD_LOCAL) else {
            let reason = dlerror().map { String(cString: $0) } ?? "unknown error"
            throw OpenRateError.load("\(path): \(reason)")
        }
        // Deliberately not stored: nothing may ever dlclose it. See above.
        func sym<T>(_ name: String, _ type: T.Type) throws -> T {
            guard let p = dlsym(handle, name) else { throw OpenRateError.missingSymbol(name) }
            return unsafeBitCast(p, to: type)
        }
        abiVersion = try sym("openrate_abi_version", AbiVersionFn.self)
        new = try sym("openrate_new", NewFn.self)
        refresherNew = try sym("openrate_refresher_new", RefresherNewFn.self)
        close = try sym("openrate_close", CloseFn.self)
        call = try sym("openrate_call", CallFn.self)
        free = try sym("openrate_free", FreeFn.self)
        openHandles = try sym("openrate_open_handles", OpenHandlesFn.self)
    }

    var version: String {
        // openrate_abi_version returns a pointer OWNED BY THE LIBRARY. It must
        // not be freed — the one exception to the ownership rule, and freeing
        // it would be heap corruption that only shows up later.
        guard let p = abiVersion() else { return "" }
        return String(cString: p)
    }

    /// Takes ownership of a `char*` error message, copies it, and **frees it**.
    /// Letting the message reach a Swift `Error` without this is the classic
    /// C-ABI binding leak: error strings are malloc'd exactly like results.
    func takeError(_ err: UnsafeMutablePointer<CChar>?) -> OpenRateError {
        guard let err else { return .openrate("openrate failed without a message") }
        let msg = String(cString: err)
        free(err)
        return .openrate(msg)
    }

    /// One `openrate_call`, with both strings freed correctly on both paths.
    func invoke(_ handle: UInt64, _ method: String, _ requestJSON: String?) throws -> String {
        var err: UnsafeMutablePointer<CChar>?
        let out: UnsafeMutablePointer<CChar>? = method.withCString { m in
            withOptionalCString(requestJSON) { req in
                call(handle, m, req, &err)
            }
        }
        guard let out else { throw takeError(err) }
        let s = String(cString: out)
        free(out)
        return s
    }
}

// MARK: - Locating the library

public enum LibraryLocator {
    /// The platform's file name, in the `libopenrate-<goos>-<goarch>.<ext>`
    /// shape `scripts/build-ffi.sh` produces.
    ///
    /// Note this differs from llmux's layout (`<goos>_<goarch>/libllmux.<ext>`).
    /// Two products, two build scripts, two conventions — do not assume one.
    public static var fileName: String {
        #if os(macOS)
        let goos = "darwin", ext = "dylib"
        #elseif os(Windows)
        let goos = "windows", ext = "dll"
        #else
        let goos = "linux", ext = "so"
        #endif
        #if arch(arm64)
        let goarch = "arm64"
        #else
        let goarch = "amd64"
        #endif
        return "libopenrate-\(goos)-\(goarch).\(ext)"
    }

    /// Finds `libopenrate`, in this order:
    ///
    /// 1. `$OPENRATE_LIBRARY`, if set — an explicit path always wins.
    /// 2. `dist/ffi/`, walking up from the working directory.
    /// 3. The bare file name, handed to the platform loader.
    public static func find() throws -> String {
        var tried: [String] = []

        if let explicit = ProcessInfo.processInfo.environment["OPENRATE_LIBRARY"],
            !explicit.isEmpty
        {
            if FileManager.default.fileExists(atPath: explicit) { return explicit }
            tried.append(explicit)
        }

        let relative = "dist/ffi/\(fileName)"
        var dir = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
        while true {
            let candidate = dir.appendingPathComponent(relative).path
            if FileManager.default.fileExists(atPath: candidate) { return candidate }
            tried.append(candidate)
            let parent = dir.deletingLastPathComponent()
            if parent.path == dir.path { break }
            dir = parent
        }

        if dlopen(fileName, RTLD_NOW | RTLD_LOCAL) != nil {
            return fileName  // never dlclose: see Library
        }
        tried.append(fileName)
        throw OpenRateError.libraryNotFound(tried)
    }
}

/// How many handles the loaded library currently has open.
///
/// Diagnostic only, and exactly what a host test suite wants in order to assert
/// it closed what it opened.
public func openHandles(libraryPath: String? = nil) throws -> UInt64 {
    let lib = try Library.shared(at: libraryPath ?? LibraryLocator.find())
    return lib.openHandles()
}

// MARK: - Engine

/// An openrate **engine**: computes, never fetches.
///
/// Constructing one starts no thread, opens no socket, reads no environment
/// variable and sends no packet. It answers from the snapshot it holds, and
/// until something gives it one it honestly says it does not know.
///
/// **Closing is `deinit`.** ARC is the RAII: the handle is released when the
/// last reference goes away, on a `throw` as much as on the happy path.
public final class Engine: @unchecked Sendable {
    let lib: Library
    let handle: UInt64

    /// Loads the library (see ``LibraryLocator/find()``) and creates an engine.
    ///
    /// `configJSON` may be nil. Fields: `{"base": "ZAR", "quiet": false}`.
    public convenience init(configJSON: String? = nil) throws {
        try self.init(libraryPath: try LibraryLocator.find(), configJSON: configJSON)
    }

    /// As above, against a library at a known path.
    public init(libraryPath: String, configJSON: String? = nil) throws {
        let lib = try Library.shared(at: libraryPath)
        self.lib = lib

        var err: UnsafeMutablePointer<CChar>?
        let h: UInt64 = withOptionalCString(configJSON) { cfg in
            lib.new(cfg, &err)
        }
        guard h != 0 else { throw lib.takeError(err) }
        self.handle = h
    }

    /// As above, refusing to continue if the loaded library is not the expected
    /// version. Worth doing at startup: a shared library resolves off a load
    /// path you may not control.
    public convenience init(expectedVersion: String, configJSON: String? = nil) throws {
        let path = try LibraryLocator.find()
        let lib = try Library.shared(at: path)
        let loaded = lib.version
        guard loaded == expectedVersion else {
            throw OpenRateError.versionMismatch(loaded: loaded, expected: expectedVersion)
        }
        try self.init(libraryPath: path, configJSON: configJSON)
    }

    deinit {
        // Idempotent, and closing an engine also stops and releases every
        // refresher built over it, so closing in the "wrong" order cannot leak
        // a running loop.
        lib.close(handle)
    }

    /// The version the loaded library was built from, e.g. `"0.1.2"`.
    public var abiVersion: String { lib.version }

    /// The raw registry key. Handles are never reused, so a stale number can
    /// only ever produce "handle N is not open".
    public var rawHandle: UInt64 { handle }

    /// Run any engine method: `"convert"`, `"rates"`, `"meta"` or `"load"`.
    ///
    /// Prefer the named methods; this exists for forward compatibility, since
    /// the ABI takes a method string precisely so the header stays stable as
    /// openrate grows methods.
    public func call(_ method: String, _ requestJSON: String? = nil) throws -> String {
        try lib.invoke(handle, method, requestJSON)
    }

    /// `{"from":"USD","to":"ZAR","amount":100}` → the conversion, with its
    /// provenance nested under `"rate"`.
    public func convert(_ requestJSON: String) throws -> String {
        try call("convert", requestJSON)
    }

    /// `{"base":"ZAR"}` → every known currency against that base.
    ///
    /// An unknown base is an **error** here, where `GET /api/v1/rates` answers
    /// `200` with an empty book. That is the one deliberate difference between
    /// the two surfaces, and the ABI follows the Go library.
    public func rates(_ requestJSON: String? = nil) throws -> String {
        try call("rates", requestJSON)
    }

    /// `{}` → default base, build time, currency list, and the fetch status of
    /// every refresher over this engine (`[]` if nobody refreshes it).
    public func meta() throws -> String {
        try call("meta", nil)
    }

    /// The zero-network path: install rates you obtained yourself.
    public func load(_ requestJSON: String) throws -> String {
        try call("load", requestJSON)
    }

    /// Build a ``Refresher`` over this engine.
    ///
    /// **This is the call that gives your process an outbound dependency.**
    /// Constructing it still opens nothing — fetching starts at
    /// ``Refresher/refresh(_:)`` or ``Refresher/start()`` — but before this line
    /// there is no code path from your program to the network, and after it
    /// there is.
    public func refresher(configJSON: String? = nil) throws -> Refresher {
        var err: UnsafeMutablePointer<CChar>?
        let h: UInt64 = withOptionalCString(configJSON) { cfg in
            lib.refresherNew(handle, cfg, &err)
        }
        guard h != 0 else { throw lib.takeError(err) }
        return Refresher(lib: lib, handle: h, engine: self)
    }
}

// MARK: - Refresher

/// An openrate **refresher**: the only object here that can open a socket.
///
/// It has its own handle and its own lifetime, and it keeps a strong reference
/// to its engine — so the engine cannot be closed underneath it, whatever order
/// the caller drops things in.
public final class Refresher: @unchecked Sendable {
    private let lib: Library
    private let handle: UInt64
    /// Strong on purpose: closing an engine closes its refreshers.
    private let engine: Engine

    init(lib: Library, handle: UInt64, engine: Engine) {
        self.lib = lib
        self.handle = handle
        self.engine = engine
    }

    deinit {
        // Stops the background loop if one is running, then releases.
        lib.close(handle)
    }

    public var rawHandle: UInt64 { handle }

    /// Run any refresher method: `"status"`, `"refresh"`, `"start"`, `"stop"`
    /// or `"ready"`.
    public func call(_ method: String, _ requestJSON: String? = nil) throws -> String {
        try lib.invoke(handle, method, requestJSON)
    }

    /// `{}` → per-source last_ok / last_error / edges. Opens nothing.
    public func status() throws -> String { try call("status", nil) }

    /// One synchronous fetch of every source. **This opens sockets.**
    ///
    /// `{"timeout_ms":30000}`; `0` or absent means no deadline of your own.
    public func refresh(_ requestJSON: String? = nil) throws -> String {
        try call("refresh", requestJSON)
    }

    /// Start the background loop on the configured interval. The only thread
    /// this library starts on its own.
    public func start() throws -> String { try call("start", nil) }

    /// Stop the background loop and wait for it to exit.
    public func stop() throws -> String { try call("stop", nil) }

    /// Block until the engine holds at least one currency.
    ///
    /// It does **not** fetch: something must be refreshing, or it waits out its
    /// timeout. `{"timeout_ms":5000}`.
    public func ready(_ requestJSON: String? = nil) throws -> String {
        try call("ready", requestJSON)
    }
}

/// Runs `body` with a C string for `s`, or with `nil` when `s` is `nil`.
///
/// `String.withCString` has no optional form, and the naive workaround —
/// `s?.withCString { … } ?? body(nil)` — does not type-check when `body` throws
/// or returns a non-Void value.
@inline(__always)
func withOptionalCString<R>(_ s: String?, _ body: (UnsafePointer<CChar>?) throws -> R) rethrows -> R
{
    guard let s else { return try body(nil) }
    return try s.withCString { try body($0) }
}
