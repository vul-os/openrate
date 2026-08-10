// openrate in this process, over the C ABI.
//
// Two phases, and the first one is the headline:
//
//   1. An Engine alone. It answers, it refuses what it cannot reach, and it
//      provably opens no socket — the ABI has no code path from an engine
//      handle to the network, and this phase demonstrates the refusal rather
//      than asserting the property.
//   2. A Refresher, gated behind `--refresh`. This is the only part that talks
//      to the internet.
//
//   swift run openrate-direct-example                # zero packets
//   swift run openrate-direct-example -- --refresh   # fetch from ECB
//
// Environment:
//   OPENRATE_LIBRARY  path to libopenrate-<goos>-<goarch>.{dylib,so}

import Foundation
import OpenRate

let doRefresh = CommandLine.arguments.contains("--refresh")

func first(_ s: String, _ n: Int) -> String {
    s.count <= n ? s : String(s.prefix(n)) + "…"
}

/// Pulls the interesting scalars out of a convert response.
///
/// Note the nesting: `result` is top level but `rate`, `hops` and `quality`
/// live inside the `"rate"` object. The Go library's `fx.Conversion` is flat.
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
    let path = (rateObj["path"] as? [String])?.joined(separator: "->") ?? "?"
    return "result=\(result) rate=\(rate) hops=\(hops) via \(path) grade=\(grade)"
}

do {
    let path = try LibraryLocator.find()
    print("library:   \(path)")

    // ------------------------------------------------------------------------
    // Phase one: the engine, alone.
    // ------------------------------------------------------------------------
    let eng = try Engine(libraryPath: path, configJSON: #"{"base":"ZAR","quiet":true}"#)
    // The handle is owned by `eng` and released in deinit — at the end of this
    // scope and on every throw below. ARC is the RAII; there is no close() to
    // forget and no defer to write.
    print("abi:       \(eng.abiVersion)")
    print("engine:    handle \(eng.rawHandle)")
    print("handles:   \(try openHandles(libraryPath: path)) open")

    // An engine with no snapshot honestly says it does not know, rather than
    // guessing or going to look.
    do {
        _ = try eng.convert(#"{"from":"USD","to":"ZAR","amount":1}"#)
        print("empty:     UNEXPECTEDLY answered — that would be a bug")
    } catch {
        print("empty:     \(error)")
    }

    // THE property, demonstrated. An engine handle refuses the refresher's
    // methods, and the error names the four it does have. This is what makes
    // "an engine cannot fetch" a fact about the ABI rather than a promise.
    //
    // In Swift there is a second fence in front of it: `refresh()` exists only
    // on Refresher, so the mistake does not compile. Reaching the ABI's refusal
    // at all needs the escape hatch `call(_:_:)`.
    do {
        _ = try eng.call("refresh", "{}")
        print("refuse:    engine ACCEPTED \"refresh\" — that would be a bug")
    } catch {
        print("refuse:    \(error)")
    }

    // The zero-network path: rates you obtained yourself, from a cache, a file,
    // a vendor feed or a fixture.
    let loaded = try eng.load(
        """
        {"built_at":"2026-08-08T16:00:00Z","edges":[
          {"from":"USD","to":"ZAR","rate":18.42,"source":"desk","time":"2026-08-08T16:00:00Z"},
          {"from":"EUR","to":"USD","rate":1.0865,"source":"desk","time":"2026-08-08T16:00:00Z"},
          {"from":"GBP","to":"USD","rate":1.2740,"source":"desk","time":"2026-08-08T16:00:00Z"}]}
        """)
    print("load:      \(loaded)")

    // A direct pair.
    print("USD->ZAR:  \(summarize(try eng.convert(#"{"from":"USD","to":"ZAR","amount":100}"#)))")
    // A triangulated pair: EUR->ZAR exists only as EUR->USD->ZAR. The hop count
    // and the path are part of the answer, not something you reconstruct.
    print("EUR->ZAR:  \(summarize(try eng.convert(#"{"from":"EUR","to":"ZAR","amount":100}"#)))")

    // And it still refuses what it genuinely cannot reach.
    do {
        _ = try eng.convert(#"{"from":"JPY","to":"ZAR","amount":1}"#)
        print("JPY->ZAR:  UNEXPECTEDLY answered")
    } catch {
        print("JPY->ZAR:  \(error)")
    }

    // An unknown base is an ERROR over the ABI, and a 404 carrying the same
    // text over HTTP. It used to be the one place the two surfaces disagreed.
    do {
        _ = try eng.rates(#"{"base":"XXX"}"#)
        print("rates XXX: UNEXPECTEDLY answered")
    } catch {
        print("rates XXX: \(error)   (HTTP answers 404 with the same text)")
    }

    print("rates ZAR: \(try eng.rates(#"{"base":"ZAR"}"#).count) bytes")
    print("meta:      \(first(try eng.meta(), 160))")

    if !doRefresh {
        print("")
        print("no Refresher was constructed, so this process opened no socket.")
        print("run with --refresh for phase two.")
        print("handles:   \(try openHandles(libraryPath: path)) open")
        exit(0)
    }

    // ------------------------------------------------------------------------
    // Phase two: the refresher. This is the part that touches the network.
    // ------------------------------------------------------------------------
    print("")
    // THIS is the line that gives the process an outbound dependency. Before it
    // there was no code path to the network; after it there is.
    let refresher = try eng.refresher(configJSON: #"{"sources":"ecb","fetch_timeout_ms":20000}"#)
    print("refresher: handle \(refresher.rawHandle), no packet sent yet")
    print("status:    \(first(try refresher.status(), 160))")
    print("handles:   \(try openHandles(libraryPath: path)) open")

    // Fetching starts here.
    print("refresh:   \(first(try refresher.refresh(#"{"timeout_ms":25000}"#), 200))")
    print("meta:      \(first(try eng.meta(), 140))")
    print("EUR->USD:  \(summarize(try eng.convert(#"{"from":"EUR","to":"USD","amount":100}"#)))")

    // Both handles deinit at scope exit, refresher first. Closing the engine
    // would also have closed the refresher, so closing in the "wrong" order
    // cannot leak a running loop.
} catch {
    FileHandle.standardError.write(Data("error: \(error)\n".utf8))
    exit(1)
}
