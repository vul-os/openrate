//! openrate as a supervised child process, over HTTP.
//!
//! ```text
//! go build -o /tmp/openrate ./cmd/openrate
//! OPENRATE_BINARY=/tmp/openrate cargo run --example sidecar
//! ```
//!
//! Compare with `examples/direct.rs`, which is the better default for a Rust
//! program. The sidecar earns its place when several processes should share one
//! refresher, when you want the HTTP shell's rate limiter and CORS policy, or
//! when you are on a platform openrate has no prebuilt library for — which, for
//! openrate, is most of them. See the crate README.

use std::process::ExitCode;
use std::time::{Duration, Instant};

use openrate::sidecar::{Options, Sidecar};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("error: {e}");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    // Spawns the child on a free loopback port and blocks until it is
    // LISTENING. On failure it kills what it started, so this `?` cannot leave
    // a server behind.
    let sc = Sidecar::start(Options {
        base: "EUR".into(),
        sources: Some("ecb".into()),
        ..Default::default()
    })?;
    // From here the process is owned by `sc` and stopped by Drop — on the
    // early returns below and on a panic, not only at the end. Rust has no
    // `defer`, so this is where the equivalent guarantee comes from.
    println!("sidecar:   {}", sc.base_url);

    // ---------------------------------------------------------------- ready
    // Liveness first: this is already true, and it means almost nothing.
    println!(
        "healthz:   {:?}  (liveness only — no rates implied)",
        sc.healthz()?
    );

    // Readiness is a different question. /healthz answered `ok` before any
    // source had been fetched; wait_ready polls /api/v1/meta until the engine
    // actually holds currencies.
    let t = Instant::now();
    let meta = sc.wait_ready(Duration::from_secs(45))?;
    println!("ready:     after {:?}", t.elapsed());
    println!(
        "meta:      {}",
        first(&openrate::sidecar::compact(&meta), 200)
    );

    // -------------------------------------------------------------- convert
    // The same document the C ABI's "convert" method returns, over a different
    // transport. One wire contract.
    let conv = sc.convert("EUR", "USD", 100.0)?;
    println!("EUR->USD:  {}", summarize(&conv));

    // ---------------------------------------------------------------- rates
    let rates = sc.rates("EUR")?;
    println!("rates EUR: {} bytes", rates.len());

    // The deliberate difference, demonstrated rather than described. Over the
    // C ABI this same request is an error; here it is a 200 with an empty book,
    // and a client that checks only the status code reads that as success.
    let bogus = sc.rates("XXX")?;
    // NOTE the `compact` call. openrate's HTTP API PRETTY-PRINTS (`"rates": {}`)
    // while its C ABI does not (`"rates":{}`), so a substring check written
    // against one shape silently never matches the other. This example's
    // readiness check hit exactly that and timed out against a server that was
    // serving 30 currencies the whole time.
    let empty = openrate::sidecar::compact(&bogus).contains("\"rates\":{}");
    println!(
        "rates XXX: HTTP 200, empty book = {empty}   (the C ABI returns \"unknown base currency\")"
    );

    // ----------------------------------------------------------- error path
    // A pair the server cannot reach is a 404 with a JSON error body, and the
    // HTTP client surfaces that body verbatim rather than paraphrasing a code.
    match sc.convert("XXX", "YYY", 1.0) {
        Ok(v) => println!("bogus:     unexpectedly answered: {}", first(&v, 120)),
        Err(e) => println!("bogus:     {}", first(&e.to_string(), 160)),
    }

    println!("stopping:  Drop kills and reaps the child");
    Ok(())
}

fn summarize(json: &str) -> String {
    // Compact first: the HTTP API pretty-prints, so `"result": 115.35` has a
    // space the C ABI's output does not. Same nesting caveat as the direct
    // example too — `result` is top level, but `rate`, `hops` and `grade` live
    // inside the `"rate"` object.
    let json = &openrate::sidecar::compact(json);
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
