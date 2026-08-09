//! Handle-accounting tests. These need EXCLUSIVE use of the process, and
//! getting that took two goes.
//!
//! `openrate_open_handles()` is a **process-global** counter, so a test that
//! asserts on it is meaningless if anything else is holding a handle at the
//! same instant. Two mechanisms are needed and neither is sufficient alone:
//!
//! 1. **Their own file.** Cargo builds each file in `tests/` as a separate
//!    binary, which keeps these away from `tests/direct.rs`. Version one lived
//!    there and failed with `left: 3, right: 2` — a sibling test was holding an
//!    engine.
//! 2. **A mutex within the file.** Cargo still runs the tests inside one binary
//!    in parallel threads. Version two relied on the file split alone and
//!    failed with `left: 0, right: 1`: this test sampled its baseline while the
//!    other one held two handles, then compared against a quiet process.
//!
//! So `EXCLUSIVE` serialises them. The lesson generalises past this file: a
//! test that measures a process-global quantity is not isolated by being in a
//! test binary, only by being the only thing running in it.

use std::path::PathBuf;
use std::sync::Mutex;

use openrate::direct::{open_handles, Engine};

/// Held for the whole of any test that reads `open_handles()`.
static EXCLUSIVE: Mutex<()> = Mutex::new(());

fn library() -> Option<PathBuf> {
    openrate::direct::find_library().ok()
}

/// `openrate_open_handles` exists so a host suite can assert it closed what it
/// opened. Using it that way here is the point.
#[test]
fn drop_closes_every_handle_it_opened() {
    let _guard = EXCLUSIVE.lock().unwrap_or_else(|e| e.into_inner());
    let Some(path) = library() else { return };
    let before = open_handles().expect("open_handles");
    {
        let eng = Engine::open_at(&path, Some(r#"{"quiet":true}"#)).expect("open");
        let _r = eng
            .refresher(Some(r#"{"sources":"ecb","quiet":true}"#))
            .expect("refresher");
        assert_eq!(
            open_handles().expect("open_handles"),
            before + 2,
            "an engine and a refresher are two handles"
        );
    }
    assert_eq!(
        open_handles().expect("open_handles"),
        before,
        "Drop did not release both handles"
    );
}

/// Regression guard for the bug llmux's equivalent binding hit: dropping and
/// re-`dlopen`ing a Go c-shared library per handle hangs. The library is loaded
/// once per process and never unloaded, so this loop is fast.
#[test]
fn many_open_close_cycles_stay_fast() {
    let _guard = EXCLUSIVE.lock().unwrap_or_else(|e| e.into_inner());
    let Some(path) = library() else { return };
    let before = open_handles().expect("open_handles");
    for _ in 0..200 {
        let eng = Engine::open_at(&path, Some(r#"{"quiet":true}"#)).expect("open");
        let _ = eng.meta().expect("meta");
    }
    assert_eq!(
        open_handles().expect("open_handles"),
        before,
        "200 open/close cycles leaked at least one handle"
    );
}
