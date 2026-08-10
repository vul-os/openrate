//! openrate as a supervised child process, over HTTP.
//!
//! ```text
//! go build -o /tmp/openrate ./cmd/openrate
//! OPENRATE_BINARY=/tmp/openrate cargo run --example sidecar
//! OPENRATE_BINARY=/tmp/openrate cargo run --example sidecar -- 5   # 5s readiness budget
//! ```
//!
//! The optional argument is how long to wait for rates. It is there so the
//! failure path can be seen without waiting 45 seconds for it:
//!
//! ```text
//! HTTPS_PROXY=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 \
//!   OPENRATE_BINARY=/tmp/openrate cargo run --example sidecar -- 5
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
    // How long to wait for rates. Short values are for demonstrating the
    // failure path; 45s is the honest budget for a cold ECB fetch.
    let ready_timeout = match std::env::args().nth(1) {
        Some(s) => Duration::from_secs(s.parse()?),
        None => Duration::from_secs(45),
    };

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
    // source had been fetched; wait_ready polls /readyz, which is a 503 with a
    // reason until the engine actually holds rates and a 200 once it does. If
    // this times out the error names the source that failed and why — run it
    // behind a dead proxy to see that.
    let t = Instant::now();
    let ready = sc.wait_ready(ready_timeout)?;
    println!("ready:     after {:?}", t.elapsed());
    println!(
        "readyz:    {}",
        first(&openrate::sidecar::compact(&ready), 200)
    );

    // /readyz counts currencies and does not name the presentation base, so
    // meta is still the place to ask for that. It is an API call now, not a
    // readiness probe.
    let meta = sc.meta()?;
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

    // The alignment, demonstrated rather than described. This request is an
    // error over the C ABI and a 404 here, and both carry the same sentence —
    // it used to be a 200 with an empty book, which a client checking only the
    // status code read as success.
    match sc.rates("XXX") {
        Ok(v) => println!(
            "rates XXX: UNEXPECTEDLY answered: {}",
            first(&openrate::sidecar::compact(&v), 80)
        ),
        Err(e) => println!(
            "rates XXX: {}   (the C ABI returns the same \"unknown base currency\")",
            first(&e.to_string(), 160)
        ),
    }

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
