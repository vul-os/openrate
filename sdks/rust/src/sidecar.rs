//! Sidecar mode: `openrate serve` as a supervised child process.
//!
//! The SDK spawns it, waits for it, and stops it, so the user never runs a
//! server by hand.
//!
//! # Two things this module makes explicit
//!
//! **Liveness is not readiness.** `/healthz` answers `ok` the instant the
//! listener binds, *before* any source has been fetched. [`Sidecar::start`]
//! waits for that and no more. Readiness — "the engine actually holds rates" —
//! is [`Sidecar::wait_ready`], which polls `/readyz`: `200` when a conversion
//! would succeed, `503` with a `reason` and the per-source outcomes when it
//! would not. A client that treats `/healthz` as readiness will ask for a
//! conversion and be told the pair is unknown.
//!
//! **`Drop` stops the child.** [`Sidecar`] owns the process and kills it when
//! it goes out of scope, including on an early return and on a panic. Rust has
//! no `defer`, and an SDK that leaves a serving openrate behind after a failed
//! request is a bug that only shows up as a port conflict later.

use std::io::Read;
use std::net::{TcpListener, TcpStream};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

use crate::http::{self, HttpError};

/// A failure starting or talking to the sidecar.
#[derive(Debug)]
pub enum Error {
    /// No `openrate` binary was found.
    BinaryNotFound,
    /// Spawning the child failed.
    Spawn(std::io::Error),
    /// The child never started listening.
    NotLive(String),
    /// The child listened but never acquired any rates. Carries the tail of the
    /// message — `after 30s: <reason> (ecb: …)` — built from the last `503` from
    /// `/readyz`, because "timed out" on its own is useless and that 503 already
    /// says which source failed and why.
    NotReady(String),
    /// An HTTP request failed.
    Http(HttpError),
    Io(std::io::Error),
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Error::BinaryNotFound => write!(
                f,
                "openrate binary not found. Set OPENRATE_BINARY, put `openrate` on PATH, or \
                 build it: `go build -o sdks/rust/bin/openrate ./cmd/openrate`"
            ),
            Error::Spawn(e) => write!(f, "failed to spawn openrate: {e}"),
            Error::NotLive(s) => write!(f, "openrate never started listening: {s}"),
            Error::NotReady(s) => write!(f, "openrate has no rates {s}"),
            Error::Http(e) => write!(f, "{e}"),
            Error::Io(e) => write!(f, "io: {e}"),
        }
    }
}

impl std::error::Error for Error {}

impl From<HttpError> for Error {
    fn from(e: HttpError) -> Self {
        Error::Http(e)
    }
}

impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Error::Io(e)
    }
}

type Result<T> = std::result::Result<T, Error>;

/// How to start the child.
pub struct Options {
    /// Path to the binary. `None` means `$OPENRATE_BINARY`, then `bin/openrate`
    /// next to this crate, then `openrate` on `PATH`.
    pub binary: Option<PathBuf>,
    /// Fixed port; `None` picks a free ephemeral one on `127.0.0.1`.
    pub port: Option<u16>,
    /// Default presentation base currency (default `"ZAR"`).
    pub base: String,
    /// Comma-separated sources for the child to fetch (default: openrate's).
    pub sources: Option<String>,
    /// Serve the embedded web console. Off by default: it is a nice thing to
    /// give a human and dead weight in a supervised sidecar.
    pub ui: bool,
    /// How long to wait for the listener (default 10s).
    pub live_timeout: Duration,
}

impl Default for Options {
    fn default() -> Self {
        Options {
            binary: None,
            port: None,
            base: "ZAR".into(),
            sources: None,
            ui: false,
            live_timeout: Duration::from_secs(10),
        }
    }
}

/// A running `openrate serve` owned by this program.
pub struct Sidecar {
    /// `http://127.0.0.1:<port>`.
    pub base_url: String,
    child: Child,
    timeout: Duration,
}

impl Sidecar {
    /// Spawns the child and waits until it is **listening** (not until it has
    /// rates — see [`Sidecar::wait_ready`]).
    ///
    /// On any failure it kills whatever it started, so a caller never has to
    /// clean up after a constructor that returned an error.
    pub fn start(opts: Options) -> Result<Sidecar> {
        let bin = match opts.binary {
            Some(b) => b,
            None => binary_path()?,
        };
        let port = match opts.port {
            Some(p) => p,
            None => free_port()?,
        };
        let addr = format!("127.0.0.1:{port}");

        let mut cmd = Command::new(&bin);
        cmd.arg("-addr")
            .arg(&addr)
            .arg("-base")
            .arg(&opts.base)
            .arg(format!("-ui={}", opts.ui));
        if let Some(s) = &opts.sources {
            cmd.arg("-sources").arg(s);
        }
        // The child listens on loopback and serves exactly one client: this
        // process. openrate's 120/min limiter is anti-scraping for a public
        // deployment and there is no stranger here to throttle — while a
        // legitimate batch of conversions would sail past it and take a 429
        // from our own sidecar.
        cmd.env("OPENRATE_RATELIMIT", "0");
        // The child's logs are the operator's, not ours to swallow.
        cmd.stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());

        let child = cmd.spawn().map_err(Error::Spawn)?;
        let mut sc = Sidecar {
            base_url: format!("http://{addr}"),
            child,
            timeout: Duration::from_secs(30),
        };
        if let Err(e) = sc.wait_live(opts.live_timeout) {
            // Drop would do this too; doing it here keeps the error path
            // obvious rather than relying on a reader knowing that it would.
            sc.kill();
            return Err(e);
        }
        Ok(sc)
    }

    /// Polls `/healthz` — **liveness only**. Says nothing about rates.
    fn wait_live(&mut self, timeout: Duration) -> Result<()> {
        let hostport = self.base_url.trim_start_matches("http://").to_string();
        let deadline = Instant::now() + timeout;
        let mut last = String::from("connection refused");
        while Instant::now() < deadline {
            if let Ok(Some(status)) = self.child.try_wait() {
                return Err(Error::NotLive(format!("child exited: {status}")));
            }
            match probe(&hostport) {
                Ok(true) => return Ok(()),
                Ok(false) => last = "non-200 from /healthz".into(),
                Err(e) => last = e.to_string(),
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        Err(Error::NotLive(last))
    }

    /// Blocks until `GET /readyz` answers `200`, and returns that body.
    ///
    /// This is the HTTP equivalent of the library's `Refresher.ready`, and it
    /// is a different question from `/healthz`. `200` means the engine holds
    /// currencies and a conversion would succeed; `503` means it does not, and
    /// carries a `reason` plus the per-source outcomes. On timeout the error
    /// repeats that reason and names each source that has failed, because the
    /// cause is almost always one named source and the 503 already knows which.
    ///
    /// The body is *not* `/api/v1/meta`: it reports `"currencies"` as a **count**
    /// and has no `default_base`. Call [`Sidecar::meta`] if you want those.
    ///
    /// # Why a fixed interval and no backoff
    ///
    /// `/readyz` sits outside `/api/`, and `guard()` applies openrate's
    /// anti-scraping limiter to `/api/` paths only, so this loop cannot spend
    /// the request budget it is waiting to use. An earlier version polled
    /// `/api/v1/meta` — which *is* limited — and needed exponential backoff to
    /// stay under 120/min; that constraint is gone, so this polls every 150 ms
    /// and notices a source that lands immediately.
    pub fn wait_ready(&self, timeout: Duration) -> Result<String> {
        let deadline = Instant::now() + timeout;
        // What the most recent attempt said. Deliberately unseeded: every path
        // through the loop body sets it before the deadline is checked, so the
        // message a caller sees is always something that actually happened
        // rather than a placeholder.
        let mut last;
        loop {
            match self.readyz() {
                Ok(body) => return Ok(body),
                // The documented not-ready answer: a JSON body we can quote.
                Err(Error::Http(HttpError::Status(503, body))) => last = why_not_ready(&body),
                // Anything else — a refused connection while the listener is
                // still coming up, an unexpected status — is reported as it is.
                Err(e) => last = e.to_string(),
            }
            if Instant::now() >= deadline {
                break;
            }
            std::thread::sleep(Duration::from_millis(150));
        }
        Err(Error::NotReady(format!("after {timeout:?}: {last}")))
    }

    /// `GET /healthz`. Liveness.
    pub fn healthz(&self) -> Result<String> {
        Ok(http::get(
            &format!("{}/healthz", self.base_url),
            self.timeout,
        )?)
    }

    /// `GET /readyz`. Readiness: `200` once a conversion would succeed.
    ///
    /// A not-ready server is an `Err(Http(Status(503, body)))` and **the body is
    /// the interesting part** — it holds `reason` and the per-source
    /// `last_error`. Do not throw it away; [`Sidecar::wait_ready`] is the loop
    /// that reads it for you.
    pub fn readyz(&self) -> Result<String> {
        Ok(http::get(&format!("{}/readyz", self.base_url), self.timeout)?)
    }

    /// `GET /api/v1/meta`.
    pub fn meta(&self) -> Result<String> {
        Ok(http::get(
            &format!("{}/api/v1/meta", self.base_url),
            self.timeout,
        )?)
    }

    /// `GET /api/v1/convert?from=&to=&amount=`.
    ///
    /// The response nests the provenance under `"rate"` — `rate.rate`,
    /// `rate.hops`, `rate.quality.grade` — where the Go library's
    /// `fx.Conversion` is flat. Same information, different shape.
    pub fn convert(&self, from: &str, to: &str, amount: f64) -> Result<String> {
        let url = format!(
            "{}/api/v1/convert?from={}&to={}&amount={}",
            self.base_url,
            urlencode(from),
            urlencode(to),
            amount
        );
        Ok(http::get(&url, self.timeout)?)
    }

    /// `GET /api/v1/rates?base=`.
    ///
    /// **An unknown base answers `200` with an empty book here**, where the
    /// library and the C ABI both return an error. A caller that checks only
    /// the status code will read "no rates" as success.
    pub fn rates(&self, base: &str) -> Result<String> {
        let url = format!("{}/api/v1/rates?base={}", self.base_url, urlencode(base));
        Ok(http::get(&url, self.timeout)?)
    }

    fn kill(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

impl Drop for Sidecar {
    fn drop(&mut self) {
        self.kill();
    }
}

/// Strips insignificant whitespace from a JSON document, leaving string
/// literals untouched.
///
/// # Why this exists
///
/// **openrate's HTTP API pretty-prints and its C ABI does not.** `GET
/// /api/v1/meta` answers with two-space indentation and newlines, so
/// `"currencies": [` — with a space — while `openrate_call(h, "meta", …)`
/// returns `"currencies":[` compact. A substring check written against the
/// compact shape silently never matches the HTTP one.
///
/// That is not hypothetical either. The first version of `wait_ready` tested
/// `meta.contains("\"currencies\":[")` and timed out against a server that was
/// serving 30 currencies the whole time — and its unit test passed, because the
/// test's fixture was hand-written compact JSON rather than a captured
/// response. The tests below now use a **verbatim capture** from the running
/// server for exactly that reason.
///
/// The split even runs through a single endpoint: `/readyz` writes its `200`
/// through the pretty-printing `writeJSON` and its `503` through a plain
/// encoder, so the ready body is indented and the not-ready body is compact.
/// Anything reading either shape by substring must normalise first.
pub fn compact(json: &str) -> String {
    let mut out = String::with_capacity(json.len());
    let mut in_string = false;
    let mut escaped = false;
    for c in json.chars() {
        if in_string {
            out.push(c);
            if escaped {
                escaped = false;
            } else if c == '\\' {
                escaped = true;
            } else if c == '"' {
                in_string = false;
            }
            continue;
        }
        match c {
            '"' => {
                in_string = true;
                out.push(c);
            }
            c if c.is_whitespace() => {}
            c => out.push(c),
        }
    }
    out
}

/// Turns a `503` body from `/readyz` into one line a human can act on.
///
/// The shape is the server's `reason`, then every source that has a
/// `last_error`, in brackets:
///
/// ```text
/// no rates yet: no source has returned a usable quote (ecb: dial tcp …: connection refused)
/// ```
///
/// **The brackets have to be able to disappear.** `last_error` is `omitempty`,
/// so a source that has not been tried yet — the common case in the first
/// second of a poll — carries no such key at all, and a formatter that assumes
/// one prints `(ecb: )`. If nothing has failed yet, the reason stands alone; if
/// the body is not the JSON we expect, it is quoted verbatim rather than
/// reduced to nothing.
fn why_not_ready(body: &str) -> String {
    let reason = json_string_field(body, "reason").unwrap_or_default();
    let sources = extract_source_errors(body);
    match (reason.is_empty(), sources.is_empty()) {
        (false, false) => format!("{reason} ({sources})"),
        (false, true) => reason,
        (true, false) => sources,
        (true, true) => format!("503 from /readyz: {}", compact(body)),
    }
}

/// Reads a top-level `"key": "value"` string out of a JSON document without a
/// JSON dependency. Tolerant of pretty-printing; see [`compact`].
fn json_string_field(json: &str, key: &str) -> Option<String> {
    let json = compact(json);
    let needle = format!("\"{key}\":\"");
    let i = json.find(&needle)?;
    read_json_string(&json[i + needle.len()..])
}

/// Reads a JSON string literal starting just after its opening quote, honouring
/// backslash escapes, and returns the unescaped text.
///
/// The escapes are load-bearing here rather than pedantic: openrate's
/// `last_error` values are Go `http` errors, which embed the URL **in quotes** —
/// `Get \"https://…\": dial tcp: i/o timeout`. Scanning to the first `"` stops
/// after `Get \`, which is precisely the useless half.
fn read_json_string(s: &str) -> Option<String> {
    let mut out = String::new();
    let mut chars = s.chars();
    while let Some(c) = chars.next() {
        match c {
            '"' => return Some(out),
            '\\' => match chars.next()? {
                'n' => out.push('\n'),
                't' => out.push('\t'),
                'r' => out.push('\r'),
                // Enough for an error message. \uXXXX is left as-is rather
                // than half-decoded; openrate does not emit it here.
                other => out.push(other),
            },
            c => out.push(c),
        }
    }
    None
}

/// Pulls `"name":"x", ... "last_error":"y"` pairs out of a `sources` array
/// without a JSON dependency. Best effort, for an error message only.
///
/// `/readyz` and `/api/v1/meta` publish the same `fxsource.Status` objects, so
/// this reads either; the readiness path feeds it the `503` body.
fn extract_source_errors(doc: &str) -> String {
    let meta = compact(doc);
    let mut out = Vec::new();
    for part in meta.split("{\"name\":\"").skip(1) {
        let Some(name) = read_json_string(part) else {
            continue;
        };
        if let Some(i) = part.find("\"last_error\":\"") {
            if let Some(msg) = read_json_string(&part[i + "\"last_error\":\"".len()..]) {
                if !msg.is_empty() {
                    out.push(format!("{name}: {msg}"));
                }
            }
        }
    }
    out.join("; ")
}

fn urlencode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

fn binary_path() -> Result<PathBuf> {
    if let Ok(p) = std::env::var("OPENRATE_BINARY") {
        if !p.is_empty() {
            return Ok(PathBuf::from(p));
        }
    }
    let name = if cfg!(windows) {
        "openrate.exe"
    } else {
        "openrate"
    };
    let bundled = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("bin")
        .join(name);
    if bundled.exists() {
        return Ok(bundled);
    }
    if let Some(path) = std::env::var_os("PATH") {
        for dir in std::env::split_paths(&path) {
            if dir.join(name).is_file() {
                return Ok(PathBuf::from(name));
            }
        }
    }
    Err(Error::BinaryNotFound)
}

fn free_port() -> Result<u16> {
    let l = TcpListener::bind("127.0.0.1:0")?;
    Ok(l.local_addr()?.port())
}

/// One `GET /healthz`, returning whether it answered 200.
fn probe(hostport: &str) -> std::io::Result<bool> {
    use std::io::Write;
    let addr = hostport
        .parse()
        .map_err(|_| std::io::Error::new(std::io::ErrorKind::InvalidInput, "addr"))?;
    let mut s = TcpStream::connect_timeout(&addr, Duration::from_secs(1))?;
    s.set_read_timeout(Some(Duration::from_secs(1)))?;
    s.set_write_timeout(Some(Duration::from_secs(1)))?;
    write!(
        s,
        "GET /healthz HTTP/1.0\r\nHost: {hostport}\r\nConnection: close\r\n\r\n"
    )?;
    let mut buf = [0u8; 256];
    let n = s.read(&mut buf)?;
    let head = String::from_utf8_lossy(&buf[..n]);
    Ok(head.starts_with("HTTP/1.") && head.contains(" 200 "))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// VERBATIM captures from a running `openrate serve`, not hand-written
    /// JSON. The bug these guard against was invisible to a hand-written
    /// fixture: the server pretty-prints, so `"currencies":[` never appears.
    const REAL_READYZ_200: &str = r#"{
  "built_at": "2026-08-09T20:55:57.251721Z",
  "currencies": 30,
  "ready": true,
  "sources": [
    {
      "name": "ecb",
      "edges": 29,
      "last_ok": "2026-08-09T20:55:57.251721Z"
    }
  ]
}"#;

    /// The `503` body, captured with every fetch forced through a dead proxy
    /// (`HTTPS_PROXY=http://127.0.0.1:1`). Note that it is COMPACT where the
    /// `200` above is indented: same endpoint, two encoders.
    const REAL_READYZ_503: &str = r#"{"built_at":"2026-08-09T20:56:05.581908Z","currencies":0,"ready":false,"reason":"no rates yet: no source has returned a usable quote","sources":[{"name":"ecb","edges":0,"last_ok":"0001-01-01T00:00:00Z","last_error":"Get \"https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml\": proxyconnect tcp: dial tcp 127.0.0.1:1: connect: connection refused"}]}"#;

    /// The FIRST `503`, before any source has been tried: same shape, and
    /// `last_error` is simply absent because it is `omitempty`. Every poll
    /// starts here, so this is the body the formatter meets most often.
    const REAL_READYZ_503_UNTRIED: &str = r#"{"built_at":"2026-08-09T20:55:56.679179Z","currencies":0,"ready":false,"reason":"no rates yet: no source has returned a usable quote","sources":[{"name":"ecb","edges":0,"last_ok":"0001-01-01T00:00:00Z"}]}"#;

    #[test]
    fn urlencode_escapes_what_matters() {
        assert_eq!(urlencode("USD"), "USD");
        assert_eq!(urlencode("a b&c"), "a%20b%26c");
    }

    #[test]
    fn compact_strips_layout_but_not_string_contents() {
        assert_eq!(compact("{ \"a\" : [ 1 , 2 ] }"), "{\"a\":[1,2]}");
        // Spaces inside a string literal survive.
        assert_eq!(compact("{ \"a\" : \"x  y\" }"), "{\"a\":\"x  y\"}");
        // So do escaped quotes, which must not end the string early.
        assert_eq!(
            compact(r#"{ "a" : "he said \"hi\" " }"#),
            r#"{"a":"he said \"hi\" "}"#
        );
    }

    /// The readiness verdict is the STATUS CODE, not a field this SDK parses —
    /// but the `200` body still has to survive `compact`, because the example
    /// prints it. Pinned so a future reader does not reintroduce a substring
    /// check against the compact shape.
    #[test]
    fn the_ready_body_is_pretty_printed_and_the_not_ready_body_is_not() {
        assert!(
            !REAL_READYZ_200.contains("\"currencies\":30"),
            "if this ever passes, /readyz stopped pretty-printing its 200 and \
             this test's premise is stale"
        );
        assert!(compact(REAL_READYZ_200).contains("\"currencies\":30"));
        // The 503 arrives compact already, straight off json.NewEncoder.
        assert!(REAL_READYZ_503.contains("\"ready\":false"));
    }

    #[test]
    fn why_not_ready_quotes_the_reason_and_names_the_failing_source() {
        let got = why_not_ready(REAL_READYZ_503);
        assert!(got.starts_with("no rates yet: "), "{got}");
        assert!(got.contains("(ecb: "), "{got}");
        // The whole error, not the fragment before the first escaped quote.
        assert!(got.contains("connection refused"), "{got}");
        assert!(got.contains("ecb.europa.eu"), "{got}");
    }

    /// `last_error` is `omitempty`. A source that has not been tried yet has no
    /// such key, and the message must degrade to the reason alone rather than
    /// printing `(ecb: )`.
    #[test]
    fn why_not_ready_degrades_to_the_reason_when_nothing_has_failed_yet() {
        assert_eq!(
            why_not_ready(REAL_READYZ_503_UNTRIED),
            "no rates yet: no source has returned a usable quote"
        );
    }

    /// Never swallow the body: a 503 this SDK cannot parse is still the only
    /// evidence the caller has.
    #[test]
    fn why_not_ready_falls_back_to_the_raw_body() {
        assert_eq!(
            why_not_ready(r#"{"ready": false}"#),
            r#"503 from /readyz: {"ready":false}"#
        );
    }

    #[test]
    fn extract_source_errors_finds_the_named_failure() {
        let got = extract_source_errors(REAL_READYZ_503);
        assert!(got.starts_with("ecb: "), "{got}");
        assert!(got.contains("connection refused"), "{got}");
        assert!(got.contains("ecb.europa.eu"), "{got}");
    }

    /// The tail this SDK puts in front of the caller, end to end.
    #[test]
    fn the_timeout_message_carries_the_cause_not_just_the_deadline() {
        let e = Error::NotReady(format!(
            "after {:?}: {}",
            Duration::from_secs(30),
            why_not_ready(REAL_READYZ_503)
        ));
        let msg = e.to_string();
        assert!(msg.starts_with("openrate has no rates after 30s: "), "{msg}");
        assert!(msg.contains("ecb: "), "{msg}");
        assert!(msg.contains("connection refused"), "{msg}");
    }

    #[test]
    fn read_json_string_survives_an_embedded_quoted_url() {
        // The exact shape Go's net/http produces, which is what broke the
        // first version of extract_source_errors.
        let raw = r#"Get \"https://x/y\": dial tcp: i/o timeout", "next": 1"#;
        assert_eq!(
            read_json_string(raw).unwrap(),
            r#"Get "https://x/y": dial tcp: i/o timeout"#
        );
    }

    #[test]
    fn extract_source_errors_is_empty_when_all_are_fine() {
        assert_eq!(extract_source_errors(REAL_READYZ_200), "");
        // And when a source simply has not been tried yet: `last_error` is
        // omitempty, so there is no key to read.
        assert_eq!(extract_source_errors(REAL_READYZ_503_UNTRIED), "");
    }
}
