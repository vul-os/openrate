//! Integration tests for the direct (C ABI) path, against the REAL shared
//! library.
//!
//! Every test is gated on `libopenrate` being present: without it they `return`
//! rather than fail, because a checkout that has not run `scripts/build-ffi.sh`
//! is a normal state.
//!
//! Gating creates the classic false green — a suite that skips everything and
//! reports success — so `gate_is_honest_about_skipping` prints which way it
//! went. Run with `--nocapture` to see it.
//!
//! Nothing here touches the network: every test uses an engine, and the one
//! test that constructs a refresher only checks that constructing it is inert.

use std::path::PathBuf;

use openrate::direct::{Engine, Error};

fn library() -> Option<PathBuf> {
    openrate::direct::find_library().ok()
}

const RATES: &str = r#"{"built_at":"2026-08-08T16:00:00Z","edges":[
    {"from":"USD","to":"ZAR","rate":18.42,"source":"t","time":"2026-08-08T16:00:00Z"},
    {"from":"EUR","to":"USD","rate":1.0865,"source":"t","time":"2026-08-08T16:00:00Z"}]}"#;

#[test]
fn gate_is_honest_about_skipping() {
    match library() {
        Some(p) => println!("libopenrate found at {} — direct tests RAN", p.display()),
        None => println!("no libopenrate — direct tests SKIPPED (run scripts/build-ffi.sh)"),
    }
}

#[test]
fn opens_reports_a_version_and_closes() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, None).expect("open");
    let v = eng.abi_version();
    assert!(
        v.chars().next().is_some_and(|c| c.is_ascii_digit()),
        "unexpected version {v:?}"
    );
    assert_ne!(eng.handle(), 0, "0 is never a valid handle");
}

#[test]
fn version_mismatch_is_detected() {
    let Some(_) = library() else { return };
    let err = Engine::open_checked("0.0.0-not-real", None).unwrap_err();
    match err {
        Error::VersionMismatch { loaded, expected } => {
            assert_eq!(expected, "0.0.0-not-real");
            assert!(!loaded.is_empty());
        }
        other => panic!("expected VersionMismatch, got {other:?}"),
    }
}

/// The property the whole product rests on: an engine handle cannot fetch.
/// Not by convention — the dispatch table has no entry for it.
#[test]
fn an_engine_handle_refuses_refresher_methods() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, None).expect("open");
    for method in ["refresh", "start", "stop", "ready", "status"] {
        let err = eng
            .call(method, Some("{}"))
            .unwrap_err_or_panic(&format!("engine accepted {method:?}"));
        let msg = err.to_string();
        assert!(
            msg.contains("unknown engine method"),
            "{method}: unexpected message {msg}"
        );
    }
}

#[test]
fn an_unloaded_engine_says_it_does_not_know_rather_than_looking() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, None).expect("open");
    let err = eng
        .convert(r#"{"from":"USD","to":"ZAR","amount":1}"#)
        .unwrap_err_or_panic("an empty engine answered a conversion");
    assert!(err.to_string().contains("unknown or unreachable"), "{err}");
}

#[test]
fn load_then_convert_including_a_triangulated_pair() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, Some(r#"{"base":"ZAR","quiet":true}"#)).expect("open");
    let loaded = eng.load(RATES).expect("load");
    assert!(loaded.contains("\"ZAR\""), "{loaded}");

    let direct = eng
        .convert(r#"{"from":"USD","to":"ZAR","amount":100}"#)
        .expect("convert");
    assert!(direct.contains("\"hops\":1"), "{direct}");

    // EUR->ZAR exists only as EUR->USD->ZAR.
    let cross = eng
        .convert(r#"{"from":"EUR","to":"ZAR","amount":100}"#)
        .expect("convert");
    assert!(cross.contains("\"hops\":2"), "{cross}");
}

/// This pins the ABI side of a refusal all three surfaces now share. The HTTP
/// API used to answer 200 with an empty book instead; it answers 404 as of
/// 0.1.6, and openrate's own `TestWireParityForAnUnknownBase` asserts the pair.
#[test]
fn an_unknown_base_is_an_error_over_the_abi() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, Some(r#"{"quiet":true}"#)).expect("open");
    eng.load(RATES).expect("load");
    let err = eng
        .rates(Some(r#"{"base":"XXX"}"#))
        .unwrap_err_or_panic("an unknown base was accepted");
    assert!(err.to_string().contains("unknown base"), "{err}");
}

/// Constructing a refresher must not fetch. If it did, this test would take a
/// network round trip; it takes microseconds, and `status` proves no source has
/// run by reporting a zero-valued `last_ok`.
#[test]
fn constructing_a_refresher_sends_nothing() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, Some(r#"{"quiet":true}"#)).expect("open");
    let r = eng
        .refresher(Some(r#"{"sources":"ecb","quiet":true}"#))
        .expect("refresher");
    assert_ne!(
        r.handle(),
        eng.handle(),
        "refresher must get its own handle"
    );
    let status = r.status().expect("status");
    assert!(status.contains("\"name\":\"ecb\""), "{status}");
    assert!(
        status.contains("0001-01-01T00:00:00Z"),
        "a source reported a real last_ok before anything fetched: {status}"
    );
}

#[test]
fn interior_nul_is_rejected_before_it_reaches_c() {
    let Some(path) = library() else { return };
    let eng = Engine::open_at(&path, Some(r#"{"quiet":true}"#)).expect("open");
    let err = eng.convert("{\0}").unwrap_err_or_panic("a NUL reached C");
    assert!(matches!(err, Error::Nul(_)), "got {err:?}");
}

/// `Result::unwrap_err` needs `T: Debug`, and these results carry `String`, so
/// it would work — but the panic message would be the JSON rather than a
/// sentence about what was expected. This says what went wrong instead.
trait UnwrapErrOrPanic<E> {
    fn unwrap_err_or_panic(self, msg: &str) -> E;
}

impl<T, E> UnwrapErrOrPanic<E> for Result<T, E> {
    fn unwrap_err_or_panic(self, msg: &str) -> E {
        match self {
            Ok(_) => panic!("{msg}"),
            Err(e) => e,
        }
    }
}
