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
//! is [`Sidecar::wait_ready`], which polls `/api/v1/meta` for a non-empty
//! currency list. A client that treats `/healthz` as readiness will ask for a
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
    /// The child listened but never acquired any rates. Carries the per-source
    /// errors from `/api/v1/meta`, because "timed out" on its own is useless
    /// and meta already knows which source failed and why.
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
            Error::NotReady(s) => write!(f, "openrate is listening but has no rates: {s}"),
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

    /// Blocks until `/api/v1/meta` reports at least one currency, and returns
    /// that meta document.
    ///
    /// This is the HTTP equivalent of the library's `Refresher.ready`, and it
    /// is a different question from `/healthz`. On timeout the error carries
    /// the per-source failures, because the cause is almost always one named
    /// source and meta already knows which.
    ///
    /// # Why this backs off instead of polling on a fixed interval
    ///
    /// `openrate serve` ships an anti-scraping rate limiter on by default:
    /// **120 API requests per minute per IP**. A readiness loop polling every
    /// 200 ms issues 300 requests a minute and is therefore rate-limited *by
    /// the server it is waiting for*, which surfaces as
    /// `HTTP 429 {"error":"rate limited — slow down."}` a few seconds in. The
    /// first version of this function did exactly that and failed on its first
    /// run against a real binary.
    ///
    /// So the delay starts at 100 ms and doubles to a 2 s ceiling: about 25
    /// requests over a 45-second wait, comfortably inside the budget, and still
    /// fast to notice a source that lands immediately.
    pub fn wait_ready(&self, timeout: Duration) -> Result<String> {
        let deadline = Instant::now() + timeout;
        let mut last = String::new();
        let mut delay = Duration::from_millis(100);
        let max_delay = Duration::from_secs(2);
        while Instant::now() < deadline {
            let meta = self.meta()?;
            if has_currencies(&meta) {
                return Ok(meta);
            }
            last = meta;
            std::thread::sleep(delay);
            delay = (delay * 2).min(max_delay);
        }
        let why = extract_source_errors(&last);
        Err(Error::NotReady(if why.is_empty() {
            format!("no rates within {timeout:?}")
        } else {
            format!("no rates within {timeout:?}: {why}")
        }))
    }

    /// `GET /healthz`. Liveness.
    pub fn healthz(&self) -> Result<String> {
        Ok(http::get(
            &format!("{}/healthz", self.base_url),
            self.timeout,
        )?)
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

/// Whether a `/api/v1/meta` document reports at least one currency.
///
/// Tolerant of pretty-printing; see [`compact`].
pub fn has_currencies(meta: &str) -> bool {
    let c = compact(meta);
    c.contains("\"currencies\":[") && !c.contains("\"currencies\":[]")
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

/// Pulls `"name":"x", ... "last_error":"y"` pairs out of a meta document
/// without a JSON dependency. Best effort, for an error message only.
fn extract_source_errors(meta: &str) -> String {
    let meta = compact(meta);
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
    const REAL_META_READY: &str = r#"{
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
}"#;

    const REAL_META_EMPTY: &str = r#"{
  "built_at": "0001-01-01T00:00:00Z",
  "currencies": [],
  "default_base": "EUR",
  "sources": [
    {
      "name": "ecb",
      "edges": 0,
      "last_ok": "0001-01-01T00:00:00Z",
      "last_error": "Get \"https://www.ecb.europa.eu/\": dial tcp: i/o timeout"
    }
  ]
}"#;

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

    #[test]
    fn has_currencies_reads_the_real_pretty_printed_responses() {
        assert!(
            has_currencies(REAL_META_READY),
            "the readiness check missed a server that IS serving rates"
        );
        assert!(!has_currencies(REAL_META_EMPTY));
    }

    #[test]
    fn has_currencies_also_reads_the_compact_c_abi_shape() {
        // The C ABI does not pretty-print, so both shapes must work.
        assert!(has_currencies(r#"{"currencies":["EUR","USD"]}"#));
        assert!(!has_currencies(r#"{"currencies":[]}"#));
    }

    /// The bug, pinned. This is the check the first version used; it must NOT
    /// be what the code relies on.
    #[test]
    fn the_naive_substring_check_is_the_one_that_fails() {
        assert!(
            !REAL_META_READY.contains("\"currencies\":["),
            "if this ever passes, openrate stopped pretty-printing and this \
             test's premise is stale — but has_currencies still works either way"
        );
    }

    #[test]
    fn extract_source_errors_finds_the_named_failure_in_pretty_json() {
        let got = extract_source_errors(REAL_META_EMPTY);
        assert!(got.starts_with("ecb: "), "{got}");
        // The whole message, not the fragment before the first escaped quote.
        assert!(got.contains("i/o timeout"), "{got}");
        assert!(got.contains("ecb.europa.eu"), "{got}");
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
        assert_eq!(extract_source_errors(REAL_META_READY), "");
    }
}
