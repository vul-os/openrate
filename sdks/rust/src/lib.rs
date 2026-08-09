//! openrate — currency conversion with provenance, for Rust, in two modes.
//!
//! # Direct: openrate inside your process
//!
//! [`direct::Engine`] `dlopen`s `libopenrate` and calls it over the C ABI. No
//! server, no port, no socket. Handles close on [`Drop`] and errors are
//! `Result`.
//!
//! The headline is not speed, it is the **engine/refresher split**. An
//! [`direct::Engine`] computes and provably cannot fetch: constructing one
//! starts no thread, opens no socket, reads no environment variable and sends
//! no packet, and the ABI itself refuses `"refresh"` on an engine handle.
//! Fetching requires a separate [`direct::Refresher`] with its own handle,
//! built by an explicit call. A program that never makes that call has no code
//! path to the network.
//!
//! ```no_run
//! let eng = openrate::direct::Engine::open(None)?;
//! eng.load(r#"{"edges":[{"from":"USD","to":"ZAR","rate":18.42,"source":"mine"}]}"#)?;
//! println!("{}", eng.convert(r#"{"from":"USD","to":"ZAR","amount":100}"#)?);
//! # Ok::<(), openrate::direct::Error>(())
//! ```
//!
//! # Sidecar: openrate as a child process
//!
//! [`sidecar::Sidecar`] spawns and supervises `openrate serve` and talks HTTP
//! to it, so the user never runs a server by hand.
//!
//! ```no_run
//! let sc = openrate::sidecar::Sidecar::start(Default::default())?;
//! sc.wait_ready(std::time::Duration::from_secs(45))?;
//! println!("{}", sc.convert("EUR", "USD", 100.0)?);
//! # Ok::<(), Box<dyn std::error::Error>>(())
//! ```
//!
//! # Which one
//!
//! **Direct, unless you have a specific reason not to.** Rust does not pre-fork
//! and does not embed a competing runtime, so the costs that push other
//! languages toward the sidecar largely do not apply — and moving to HTTP
//! trades away the property that makes openrate worth embedding, since an
//! engine that provably cannot reach the network becomes an HTTP client that
//! provably can.
//!
//! Choose the sidecar when several processes should share **one** refresher
//! (four processes each refreshing is four times the load on ECB and SARB, from
//! four IPs, on four unsynchronised cadences), when you want the HTTP shell's
//! rate limiter and CORS policy, when you want openrate restartable
//! independently of your program, or when you are on a platform with no
//! prebuilt shared library — which for openrate is most of them. See the crate
//! README for the exact matrix; it is **narrower than llmux's**.
//!
//! # No streaming
//!
//! There is no `openrate_stream` and no iterator API. openrate answers from a
//! snapshot it already holds, so there is no incremental operation to stream.

pub mod direct;
pub mod http;
pub mod sidecar;
