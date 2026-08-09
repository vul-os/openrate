// Tests for the direct (C ABI) path and the sidecar's JSON-shape helpers.
//
// # Why swift-testing and not XCTest
//
// **XCTest ships with Xcode, not with the Command Line Tools.** On a machine
// with only the CLT installed — which is where this SDK was written and run —
// `import XCTest` fails with "no such module 'XCTest'" and `swift test` cannot
// build at all. swift-testing (`import Testing`) is part of the Swift 6
// toolchain, so it works with the CLT alone. Worth knowing before you write a
// suite that only runs on machines with a 10 GB IDE.
//
// # Gating
//
// Tests needing a real `libopenrate` return rather than fail without one,
// because a checkout that has not run `scripts/build-ffi.sh` is a normal state.
// Gating creates the classic false green, so `gateIsHonestAboutSkipping` prints
// which way it went.
//
// Nothing here touches the network: every test uses an engine, and the one that
// constructs a refresher only checks that constructing it is inert.
//
// # Why the whole suite is .serialized
//
// `openrate_open_handles()` is a PROCESS-GLOBAL counter, so a test asserting on
// it is meaningless if anything else holds a handle at the same instant — and
// most tests here open an engine. Two attempts got this wrong before this one:
//
//   1. Putting only the handle tests in a nested `@Suite(.serialized)`. That
//      trait serialises tests WITHIN its suite; it does nothing about the free
//      `@Test` functions in the same file, which kept running alongside. The
//      failure was `before + 2` seeing an extra handle from a sibling.
//   2. Reaching for the Rust fix — a separate test binary. **That does not
//      transfer.** SwiftPM links every `testTarget` into ONE
//      `<Package>PackageTests.xctest` bundle and runs it in one process, so a
//      second test target shares the counter just as much.
//
// Serialising the file is the fix. It costs nothing: the suite runs in ~0.02s.

import Foundation
import Testing



@testable import OpenRate

private func library() -> String? {
    try? LibraryLocator.find()
}

private let ratesJSON = """
    {"built_at":"2026-08-08T16:00:00Z","edges":[
      {"from":"USD","to":"ZAR","rate":18.42,"source":"t","time":"2026-08-08T16:00:00Z"},
      {"from":"EUR","to":"USD","rate":1.0865,"source":"t","time":"2026-08-08T16:00:00Z"}]}
    """

/// Every test in this file, in one suite, run ONE AT A TIME.
///
/// The nesting is the mechanism: swift-testing groups tests by lexical
/// containment, so a free `@Test` function is not in this suite no matter what
/// traits the suite carries. An empty `@Suite(.serialized) struct` declared
/// beside the tests — which is what attempt three looked like — serialises
/// nothing at all and reports a green build while the tests still race.
@Suite(.serialized)
struct OpenRateDirect {
    // MARK: - Gate

    @Test func gateIsHonestAboutSkipping() {
        if let p = library() {
            print("libopenrate found at \(p) — direct tests RAN")
        } else {
            print("no libopenrate — direct tests SKIPPED (run scripts/build-ffi.sh)")
        }
    }

    @Test func fileNameMatchesTheBuildScriptConvention() {
        let name = LibraryLocator.fileName
        #expect(name.hasPrefix("libopenrate-"), "\(name)")
        // Not llmux's `<goos>_<goarch>/libllmux.<ext>` shape.
        #expect(!name.contains("_"), "\(name)")
        #if os(macOS)
        #expect(name.hasSuffix(".dylib"), "\(name)")
        #endif
    }

    @Test func missingLibraryIsALoadErrorNotACrash() {
        do {
            _ = try Engine(libraryPath: "/nonexistent/libopenrate.dylib")
            Issue.record("expected a throw")
        } catch let e as OpenRateError {
            guard case .load = e else {
                Issue.record("expected .load, got \(e)")
                return
            }
            #expect(e.description.contains("/nonexistent/libopenrate.dylib"))
        } catch {
            Issue.record("unexpected error \(error)")
        }
    }

    @Test func libraryNotFoundNamesTheEnvVarAndThePaths() {
        let e = OpenRateError.libraryNotFound(["/a/libopenrate-linux-arm64.so"])
        #expect(e.description.contains("OPENRATE_LIBRARY"))
        #expect(e.description.contains("/a/libopenrate-linux-arm64.so"))
    }

    // MARK: - Engine

    @Test func opensReportsAVersionAndCloses() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path)
        #expect(eng.abiVersion.first?.isNumber == true, "unexpected version \(eng.abiVersion)")
        #expect(eng.rawHandle != 0, "0 is never a valid handle")
    }

    @Test func versionMismatchIsDetected() throws {
        guard library() != nil else { return }
        do {
            _ = try Engine(expectedVersion: "0.0.0-not-real")
            Issue.record("a bogus expected version was accepted")
        } catch let OpenRateError.versionMismatch(loaded, expected) {
            #expect(expected == "0.0.0-not-real")
            #expect(!loaded.isEmpty)
        }
    }

    /// The property the whole product rests on: an engine handle cannot fetch.
    /// Not by convention — the dispatch table has no entry for it.
    @Test func anEngineHandleRefusesRefresherMethods() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path)
        for method in ["refresh", "start", "stop", "ready", "status"] {
            do {
                _ = try eng.call(method, "{}")
                Issue.record("engine accepted \(method)")
            } catch {
                #expect("\(error)".contains("unknown engine method"), "\(method): \(error)")
            }
        }
    }

    @Test func anUnloadedEngineSaysItDoesNotKnowRatherThanLooking() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path)
        do {
            _ = try eng.convert(#"{"from":"USD","to":"ZAR","amount":1}"#)
            Issue.record("an empty engine answered a conversion")
        } catch {
            #expect("\(error)".contains("unknown or unreachable"), "\(error)")
        }
    }

    @Test func loadThenConvertIncludingATriangulatedPair() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path, configJSON: #"{"base":"ZAR","quiet":true}"#)
        #expect(try eng.load(ratesJSON).contains("\"ZAR\""))
        #expect(try eng.convert(#"{"from":"USD","to":"ZAR","amount":100}"#).contains("\"hops\":1"))
        // EUR->ZAR exists only as EUR->USD->ZAR.
        #expect(try eng.convert(#"{"from":"EUR","to":"ZAR","amount":100}"#).contains("\"hops\":2"))
    }

    /// The ABI and the HTTP API deliberately disagree here. This pins the ABI side
    /// so a future change to either surface cannot drift silently.
    @Test func anUnknownBaseIsAnErrorOverTheABI() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path, configJSON: #"{"quiet":true}"#)
        _ = try eng.load(ratesJSON)
        do {
            _ = try eng.rates(#"{"base":"XXX"}"#)
            Issue.record("an unknown base was accepted")
        } catch {
            #expect("\(error)".contains("unknown base"), "\(error)")
        }
    }

    /// The C ABI does NOT pretty-print, unlike the HTTP API. Pinned, because the
    /// sidecar's readiness check depends on knowing which surface produces which.
    @Test func theCABIReturnsCompactJSON() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path, configJSON: #"{"quiet":true}"#)
        _ = try eng.load(ratesJSON)
        let meta = try eng.meta()
        #expect(meta.contains("\"currencies\":["), "the C ABI started pretty-printing: \(meta)")
        #expect(!meta.contains("\n"), "the C ABI started pretty-printing: \(meta)")
    }

    // MARK: - Refresher

    /// Constructing a refresher must not fetch. If it did, this test would take a
    /// network round trip; `status` proves no source has run by reporting a
    /// zero-valued `last_ok`.
    @Test func constructingARefresherSendsNothing() throws {
        guard let path = library() else { return }
        let eng = try Engine(libraryPath: path, configJSON: #"{"quiet":true}"#)
        let r = try eng.refresher(configJSON: #"{"sources":"ecb","quiet":true}"#)
        #expect(r.rawHandle != eng.rawHandle, "refresher must get its own handle")
        let status = try r.status()
        #expect(status.contains("\"name\":\"ecb\""), "\(status)")
        #expect(
            status.contains("0001-01-01T00:00:00Z"),
            "a source reported a real last_ok before anything fetched: \(status)")
    }

    /// `openrate_open_handles` exists so a host suite can assert it closed what it
    /// opened. Using it that way is the point — and it is why the WHOLE suite above
    /// is `.serialized`; see the file header.
    @Test func dropClosesEveryHandleItOpened() throws {
        guard let path = library() else { return }
        let before = try openHandles(libraryPath: path)
        do {
            let eng = try Engine(libraryPath: path, configJSON: #"{"quiet":true}"#)
            // `let refresher =`, NOT `_ =`. Assigning to `_` in Swift releases
            // the value IMMEDIATELY — deinit runs, the handle closes, and the
            // count below is before+1 instead of before+2. This test failed
            // exactly that way before the binding was named.
            let refresher = try eng.refresher(configJSON: #"{"sources":"ecb","quiet":true}"#)
            #expect(
                try openHandles(libraryPath: path) == before + 2,
                "an engine and a refresher are two handles")
            // ARC can end a lifetime at last use, which is before the end of
            // scope; keep both alive across the assertion explicitly.
            withExtendedLifetime((eng, refresher)) {}
        }
        #expect(try openHandles(libraryPath: path) == before, "deinit did not release both handles")
    }

    /// Regression guard for the bug llmux's Rust binding hit: unloading and
    /// re-`dlopen`ing a Go c-shared library per handle hangs. Loaded once per
    /// process, so this loop is fast.
    @Test func manyOpenCloseCyclesStayFast() throws {
        guard let path = library() else { return }
        let before = try openHandles(libraryPath: path)
        for _ in 0..<200 {
            let eng = try Engine(libraryPath: path, configJSON: #"{"quiet":true}"#)
            _ = try eng.meta()
        }
        #expect(
            try openHandles(libraryPath: path) == before,
            "200 open/close cycles leaked at least one handle")
    }

    // MARK: - JSON shape helpers

    /// VERBATIM captures from a running `openrate serve`, not hand-written JSON.
    /// The bug these guard against was invisible to a hand-written fixture: the
    /// server pretty-prints, so `"currencies":[` never appears.
    private let realMetaReady = """
        {
          "built_at": "2026-08-09T13:50:58.698005Z",
          "currencies": [
            "AUD",
            "USD",
            "ZAR"
          ],
          "default_base": "EUR",
          "sources": [
            {
              "name": "ecb",
              "edges": 29,
              "last_ok": "2026-08-09T13:50:58.698005Z"
            }
          ]
        }
        """

    private let realMetaEmpty = """
        {
          "built_at": "0001-01-01T00:00:00Z",
          "currencies": [],
          "default_base": "EUR",
          "sources": [
            {
              "name": "ecb",
              "edges": 0,
              "last_ok": "0001-01-01T00:00:00Z",
              "last_error": "Get \\"https://www.ecb.europa.eu/x\\": dial tcp: i/o timeout"
            }
          ]
        }
        """

    @Test func compactStripsLayoutButNotStringContents() {
        #expect(compact("{ \"a\" : [ 1 , 2 ] }") == "{\"a\":[1,2]}")
        // Spaces inside a string literal survive.
        #expect(compact("{ \"a\" : \"x  y\" }") == "{\"a\":\"x  y\"}")
        // So do escaped quotes, which must not end the string early.
        #expect(compact(#"{ "a" : "he said \"hi\" " }"#) == #"{"a":"he said \"hi\" "}"#)
    }

    @Test func hasCurrenciesReadsTheRealPrettyPrintedResponses() {
        #expect(
            hasCurrencies(realMetaReady),
            "the readiness check missed a server that IS serving rates")
        #expect(!hasCurrencies(realMetaEmpty))
    }

    @Test func hasCurrenciesAlsoReadsTheCompactCABIShape() {
        #expect(hasCurrencies(#"{"currencies":["EUR","USD"]}"#))
        #expect(!hasCurrencies(#"{"currencies":[]}"#))
    }

    /// The bug, pinned. This is the check a first implementation reaches for; it
    /// must NOT be what the code relies on.
    @Test func theNaiveSubstringCheckIsTheOneThatFails() {
        #expect(
            !realMetaReady.contains("\"currencies\":["),
            "if this passes, openrate stopped pretty-printing — hasCurrencies works either way")
    }

    @Test func sourceErrorsSurvivesAnEmbeddedQuotedURL() {
        let got = sourceErrors(in: realMetaEmpty)
        #expect(got.hasPrefix("ecb: "), "\(got)")
        // The whole message, not the fragment before the first escaped quote.
        #expect(got.contains("i/o timeout"), "\(got)")
        #expect(got.contains("ecb.europa.eu"), "\(got)")
    }

    @Test func sourceErrorsIsEmptyWhenAllAreFine() {
        #expect(sourceErrors(in: realMetaReady).isEmpty)
    }

    @Test func freePortIsInRange() throws {
        #expect(try Sidecar.freePort() > 0)
    }
}
