//! openrate in this process, over the C ABI.
//!
//! Two phases, and the first one is the headline:
//!
//!   1. An Engine alone. It answers, it refuses what it cannot reach, and it
//!      **provably opens no socket** — the ABI has no code path from an engine
//!      handle to the network, and this phase demonstrates the refusal rather
//!      than asserting the property.
//!   2. A Refresher, gated behind `--refresh`. This is the only part that
//!      talks to the internet.
//!
//! ```text
//! cargo run --example direct                # zero packets
//! cargo run --example direct -- --refresh   # fetch from ECB, then answer
//! ```
//!
//! Environment:
//!   OPENRATE_LIBRARY  path to libopenrate-<goos>-<goarch>.{dylib,so}

use std::process::ExitCode;

use openrate::direct::{Engine, Error};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("error: {e}");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), Error> {
    let do_refresh = std::env::args().any(|a| a == "--refresh");

    let path = openrate::direct::find_library()?;
    println!("library:   {}", path.display());

    // ------------------------------------------------------------------------
    // Phase one: the engine, alone.
    // ------------------------------------------------------------------------
    let eng = Engine::open(Some(r#"{"base":"ZAR","quiet":true}"#))?;
    // The handle is owned by `eng` and released by Drop — at the end of this
    // function, on every `?` below, and on a panic. There is no close() to
    // forget, which is the reason to wrap a C handle in a Rust type at all.
    println!("abi:       {}", eng.abi_version());
    println!("engine:    handle {}", eng.handle());
    println!("handles:   {} open", openrate::direct::open_handles()?);

    // An engine with no snapshot honestly says it does not know, rather than
    // guessing or going to look.
    match eng.convert(r#"{"from":"USD","to":"ZAR","amount":1}"#) {
        Ok(_) => println!("empty:     UNEXPECTEDLY answered — that would be a bug"),
        Err(e) => println!("empty:     {e}"),
    }

    // THE property, demonstrated. An engine handle refuses the refresher's
    // methods, and the error names the four it does have. This is what makes
    // "an engine cannot fetch" a fact about the ABI rather than a promise in a
    // README.
    match eng.call("refresh", Some("{}")) {
        Ok(_) => println!("refuse:    engine ACCEPTED \"refresh\" — that would be a bug"),
        Err(e) => println!("refuse:    {e}"),
    }

    // The zero-network path: rates you obtained yourself, from a cache, a file,
    // a vendor feed or a fixture. This is what lets openrate live in a process
    // that is not allowed to make outbound calls at all.
    let loaded = eng.load(
        r#"{"built_at":"2026-08-08T16:00:00Z","edges":[
            {"from":"USD","to":"ZAR","rate":18.42,"source":"desk","time":"2026-08-08T16:00:00Z"},
            {"from":"EUR","to":"USD","rate":1.0865,"source":"desk","time":"2026-08-08T16:00:00Z"},
            {"from":"GBP","to":"USD","rate":1.2740,"source":"desk","time":"2026-08-08T16:00:00Z"}]}"#,
    )?;
    println!("load:      {loaded}");

    // A direct pair.
    let usd_zar = eng.convert(r#"{"from":"USD","to":"ZAR","amount":100}"#)?;
    println!("USD->ZAR:  {}", summarize(&usd_zar));

    // A triangulated pair: EUR->ZAR exists only as EUR->USD->ZAR. The hop count
    // and the path are part of the answer, not something you reconstruct.
    let eur_zar = eng.convert(r#"{"from":"EUR","to":"ZAR","amount":100}"#)?;
    println!("EUR->ZAR:  {}", summarize(&eur_zar));

    // And it still refuses what it genuinely cannot reach.
    match eng.convert(r#"{"from":"JPY","to":"ZAR","amount":1}"#) {
        Ok(_) => println!("JPY->ZAR:  UNEXPECTEDLY answered"),
        Err(e) => println!("JPY->ZAR:  {e}"),
    }

    // An unknown base is an ERROR over the ABI, where GET /api/v1/rates answers
    // 200 with an empty book. The one deliberate difference between the two
    // surfaces; the ABI follows the Go library.
    match eng.rates(Some(r#"{"base":"XXX"}"#)) {
        Ok(r) => println!("rates XXX: UNEXPECTEDLY answered: {}", first(&r, 80)),
        Err(e) => println!("rates XXX: {e}   (HTTP would answer 200 with an empty book)"),
    }

    let zar = eng.rates(Some(r#"{"base":"ZAR"}"#))?;
    println!("rates ZAR: {} bytes", zar.len());
    println!("meta:      {}", first(&eng.meta()?, 200));

    if !do_refresh {
        println!();
        println!("no Refresher was constructed, so this process opened no socket.");
        println!("run with --refresh for phase two.");
        println!("handles:   {} open", openrate::direct::open_handles()?);
        return Ok(());
    }

    // ------------------------------------------------------------------------
    // Phase two: the refresher. This is the part that touches the network.
    // ------------------------------------------------------------------------
    println!();
    // THIS is the line that gives the process an outbound dependency. Before
    // it there was no code path to the network; after it there is.
    let refresher = eng.refresher(Some(r#"{"sources":"ecb","fetch_timeout_ms":20000}"#))?;
    println!(
        "refresher: handle {}, no packet sent yet",
        refresher.handle()
    );
    println!("status:    {}", first(&refresher.status()?, 200));
    println!("handles:   {} open", openrate::direct::open_handles()?);

    // Fetching starts here.
    let after = refresher.refresh(Some(r#"{"timeout_ms":25000}"#))?;
    println!("refresh:   {}", first(&after, 300));
    println!("meta:      {}", first(&eng.meta()?, 160));

    let eur_usd = eng.convert(r#"{"from":"EUR","to":"USD","amount":100}"#)?;
    println!("EUR->USD:  {}", summarize(&eur_usd));

    // Both handles drop here, refresher first. Closing the engine would also
    // have closed the refresher — openrate_close on an engine stops and
    // releases every refresher over it — so closing in the "wrong" order
    // cannot leak a running loop.
    Ok(())
}

/// Pulls the interesting scalars out of a convert response without a JSON
/// dependency. Fine for an example; use serde_json in anything real.
///
/// Note the nesting: `result` is top level but `rate`, `hops` and `path` live
/// inside the `"rate"` object. The Go library's fx.Conversion is flat.
fn summarize(json: &str) -> String {
    let result = num(json, "\"result\":");
    let rate = num(json, "\"rate\":{\"rate\":");
    let hops = num(json, "\"hops\":");
    let grade = str_field(json, "\"grade\":\"");
    format!("result={result} rate={rate} hops={hops} grade={grade}")
}

fn num(json: &str, key: &str) -> String {
    let Some(i) = json.find(key) else {
        return "?".into();
    };
    json[i + key.len()..]
        .chars()
        .take_while(|c| c.is_ascii_digit() || *c == '.' || *c == '-' || *c == 'e' || *c == '+')
        .collect::<String>()
}

fn str_field(json: &str, key: &str) -> String {
    let Some(i) = json.find(key) else {
        return "?".into();
    };
    json[i + key.len()..]
        .chars()
        .take_while(|c| *c != '"')
        .collect()
}

fn first(s: &str, max: usize) -> String {
    if s.chars().count() <= max {
        return s.to_string();
    }
    let t: String = s.chars().take(max).collect();
    format!("{t}…")
}
