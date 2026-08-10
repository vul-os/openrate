//! Direct mode: openrate running **inside this process**, over the C ABI.
//!
//! This module `dlopen`s `libopenrate` and binds the seven functions in
//! [`ffi/include/openrate.h`]. There is no server, no port and no socket.
//!
//! # The engine/refresher split is the whole point
//!
//! openrate is two objects, and which one you construct decides whether your
//! process can talk to the internet at all:
//!
//! ```text
//! Engine     computes.  openrate_new() starts no thread, opens no socket,
//!                       reads no environment variable and sends no packet.
//!                       Methods: convert, rates, meta, load.
//!
//! Refresher  fetches.   openrate_refresher_new() is a SEPARATE construction
//!                       over an engine, with its own handle and lifetime, and
//!                       it is the only thing here that can open a socket.
//!                       Methods: status, refresh, start, stop, ready.
//! ```
//!
//! That split is enforced at the ABI, not by convention: an [`Engine`] handle
//! **refuses** `"refresh"` with an error naming the four methods it does have.
//! A program that never calls [`Engine::refresher`] cannot acquire an outbound
//! dependency by accident, and the type system here says so too — [`Refresher`]
//! is the only type with a [`Refresher::refresh`] method on it.
//!
//! Feed an engine without a refresher with [`Engine::load`]: rates from a
//! cache, a file, a vendor feed, a fixture.
//!
//! ```no_run
//! let eng = openrate::direct::Engine::open(None)?;
//! eng.load(r#"{"edges":[{"from":"USD","to":"ZAR","rate":18.42,"source":"mine"}]}"#)?;
//! let json = eng.convert(r#"{"from":"USD","to":"ZAR","amount":100}"#)?;
//! # Ok::<(), openrate::direct::Error>(())
//! ```
//!
//! # No streaming
//!
//! There is deliberately no `openrate_stream` and no iterator here. openrate
//! answers from a snapshot it already holds, so there is no incremental
//! operation to stream. llmux, which shares this ABI shape, does define
//! `llmux_stream`, because chat streaming is its main event.
//!
//! # Read this before you choose it
//!
//! Loading this library loads **the Go runtime** into your process: its garbage
//! collector, its scheduler, and its signal handlers. It is **not fork-safe**.
//! It is a ~6.7 MB artifact, and it is prebuilt for **fewer** platforms than
//! llmux's — see the crate README, and do not assume one matrix covers both
//! products.

use std::ffi::{c_char, CStr, CString};
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/// Everything that can go wrong on the direct path.
#[derive(Debug)]
pub enum Error {
    /// No `libopenrate` could be found. Carries the paths that were tried.
    LibraryNotFound(Vec<PathBuf>),
    /// `dlopen` failed, or a symbol was missing.
    Load(libloading::Error),
    /// The loaded library reports a different version than expected.
    VersionMismatch { loaded: String, expected: String },
    /// openrate itself failed. The string is the library's own message, which
    /// is plain UTF-8 text and deliberately **not** JSON — do not parse it.
    OpenRate(String),
    /// A Rust string could not be passed to C because it contains a NUL.
    Nul(std::ffi::NulError),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::LibraryNotFound(tried) => {
                write!(
                    f,
                    "libopenrate not found. Set OPENRATE_LIBRARY to its path, or build it with \
                     `scripts/build-ffi.sh`. Tried: "
                )?;
                for (i, p) in tried.iter().enumerate() {
                    if i > 0 {
                        write!(f, ", ")?;
                    }
                    write!(f, "{}", p.display())?;
                }
                Ok(())
            }
            Error::Load(e) => write!(f, "loading libopenrate: {e}"),
            Error::VersionMismatch { loaded, expected } => write!(
                f,
                "libopenrate reports version {loaded}, expected {expected} — a stale library is \
                 earlier on your load path"
            ),
            Error::OpenRate(m) => write!(f, "{m}"),
            Error::Nul(e) => write!(f, "argument contains an interior NUL: {e}"),
        }
    }
}

impl std::error::Error for Error {}

impl From<libloading::Error> for Error {
    fn from(e: libloading::Error) -> Self {
        Error::Load(e)
    }
}

impl From<std::ffi::NulError> for Error {
    fn from(e: std::ffi::NulError) -> Self {
        Error::Nul(e)
    }
}

impl Error {
    /// Takes ownership of a `char*` error message from the library, copies it
    /// into a Rust `String`, and **frees it**. Letting the message reach an
    /// `Error` without this step is the classic C-ABI binding leak: error
    /// strings are malloc'd exactly like results are.
    ///
    /// # Safety
    /// `err` must be either null or a pointer the library returned.
    unsafe fn from_c(api: &Api, err: *mut c_char) -> Error {
        if err.is_null() {
            return Error::OpenRate("openrate failed without a message".into());
        }
        let msg = CStr::from_ptr(err).to_string_lossy().into_owned();
        (api.free)(err);
        Error::OpenRate(msg)
    }
}

/// Shorthand for this module's results.
pub type Result<T> = std::result::Result<T, Error>;

// ---------------------------------------------------------------------------
// The ABI
// ---------------------------------------------------------------------------

type AbiVersionFn = unsafe extern "C" fn() -> *const c_char;
type NewFn = unsafe extern "C" fn(*const c_char, *mut *mut c_char) -> u64;
type RefresherNewFn = unsafe extern "C" fn(u64, *const c_char, *mut *mut c_char) -> u64;
type CloseFn = unsafe extern "C" fn(u64);
type CallFn =
    unsafe extern "C" fn(u64, *const c_char, *const c_char, *mut *mut c_char) -> *mut c_char;
type FreeFn = unsafe extern "C" fn(*mut c_char);
type OpenHandlesFn = unsafe extern "C" fn() -> u64;

/// The loaded library and its seven resolved symbols.
///
/// # Why this is never dropped
///
/// `Api` has no `Drop`, is only handed out as `&'static`, and the
/// [`libloading::Library`] inside it is **leaked on purpose**.
///
/// `libopenrate` is a Go `c-shared` object. Loading it starts the Go runtime
/// and its threads, and those threads run for the life of the process — Go has
/// no "shut the runtime down" entry point. `dlclose` would unmap the code those
/// threads are executing. The equivalent binding for llmux learned this the
/// hard way: a loop that opened and closed 200 handles, each with its own
/// `dlopen`/`dlclose`, hung and had to be killed. One load per path per
/// process, cached, never unloaded.
struct Api {
    abi_version: AbiVersionFn,
    new: NewFn,
    refresher_new: RefresherNewFn,
    close: CloseFn,
    call: CallFn,
    free: FreeFn,
    open_handles: OpenHandlesFn,
    /// Kept so the mapping outlives the pointers above. Never dropped.
    _lib: libloading::Library,
}

// SAFETY: C function pointers plus a libloading::Library, which is Send+Sync.
// The header documents every entry point as safe from multiple threads.
unsafe impl Send for Api {}
unsafe impl Sync for Api {}

static LOADED: Mutex<Vec<(PathBuf, &'static Api)>> = Mutex::new(Vec::new());

impl Api {
    fn shared(path: &Path) -> Result<&'static Api> {
        let mut loaded = LOADED.lock().unwrap_or_else(|e| e.into_inner());
        if let Some((_, api)) = loaded.iter().find(|(p, _)| p == path) {
            return Ok(api);
        }
        // SAFETY: loading arbitrary code from `path` is inherently unsafe; the
        // caller chose the path. Initialisers run here, once.
        let api = unsafe { Api::load(path)? };
        let api: &'static Api = Box::leak(Box::new(api));
        loaded.push((path.to_path_buf(), api));
        Ok(api)
    }

    unsafe fn load(path: &Path) -> Result<Api> {
        let lib = libloading::Library::new(path)?;
        let abi_version = *lib.get::<AbiVersionFn>(b"openrate_abi_version\0")?;
        let new = *lib.get::<NewFn>(b"openrate_new\0")?;
        let refresher_new = *lib.get::<RefresherNewFn>(b"openrate_refresher_new\0")?;
        let close = *lib.get::<CloseFn>(b"openrate_close\0")?;
        let call = *lib.get::<CallFn>(b"openrate_call\0")?;
        let free = *lib.get::<FreeFn>(b"openrate_free\0")?;
        let open_handles = *lib.get::<OpenHandlesFn>(b"openrate_open_handles\0")?;
        Ok(Api {
            abi_version,
            new,
            refresher_new,
            close,
            call,
            free,
            open_handles,
            _lib: lib,
        })
    }

    fn version(&self) -> String {
        // SAFETY: openrate_abi_version returns a pointer owned by the library.
        // It must NOT be freed — the one exception to the ownership rule.
        unsafe {
            CStr::from_ptr((self.abi_version)())
                .to_string_lossy()
                .into_owned()
        }
    }

    /// One `openrate_call`, with both strings freed correctly on both paths.
    fn call(&self, handle: u64, method: &str, request_json: Option<&str>) -> Result<String> {
        let m = CString::new(method)?;
        let req = request_json.map(CString::new).transpose()?;
        let req_ptr = req.as_ref().map_or(std::ptr::null(), |c| c.as_ptr());

        let mut err: *mut c_char = std::ptr::null_mut();
        // SAFETY: the handle is open (the owning type holds it), and both
        // strings outlive the call.
        let out = unsafe { (self.call)(handle, m.as_ptr(), req_ptr, &mut err) };
        if out.is_null() {
            return Err(unsafe { Error::from_c(self, err) });
        }
        // Copy, then free through openrate_free. The library allocated `out`
        // with Go's allocator; it must never go through Rust's.
        let s = unsafe { CStr::from_ptr(out).to_string_lossy().into_owned() };
        unsafe { (self.free)(out) };
        Ok(s)
    }
}

// ---------------------------------------------------------------------------
// Locating the library
// ---------------------------------------------------------------------------

/// The platform's file name for the shared library, in the
/// `libopenrate-<goos>-<goarch>.<ext>` shape `scripts/build-ffi.sh` produces.
///
/// Note this differs from llmux's layout (`<goos>_<goarch>/libllmux.<ext>`).
/// Two products, two build scripts, two conventions — do not assume one.
pub fn library_file_name() -> String {
    let goos = if cfg!(target_os = "macos") {
        "darwin"
    } else if cfg!(target_os = "windows") {
        "windows"
    } else {
        "linux"
    };
    let goarch = if cfg!(target_arch = "aarch64") {
        "arm64"
    } else {
        "amd64"
    };
    let ext = if cfg!(target_os = "macos") {
        "dylib"
    } else if cfg!(target_os = "windows") {
        "dll"
    } else {
        "so"
    };
    format!("libopenrate-{goos}-{goarch}.{ext}")
}

/// Finds `libopenrate`, in this order:
///
/// 1. `$OPENRATE_LIBRARY`, if set — an explicit path always wins.
/// 2. `dist/ffi/libopenrate-<goos>-<goarch>.<ext>`, walking up from the crate.
/// 3. The bare file name, handed to the platform loader.
pub fn find_library() -> Result<PathBuf> {
    let name = library_file_name();
    let mut tried = Vec::new();

    if let Ok(p) = std::env::var("OPENRATE_LIBRARY") {
        if !p.is_empty() {
            let p = PathBuf::from(p);
            if p.exists() {
                return Ok(p);
            }
            tried.push(p);
        }
    }

    let rel = format!("dist/ffi/{name}");
    let mut dir: Option<&Path> = Some(Path::new(env!("CARGO_MANIFEST_DIR")));
    while let Some(d) = dir {
        let candidate = d.join(&rel);
        if candidate.exists() {
            return Ok(candidate);
        }
        tried.push(candidate);
        dir = d.parent();
    }

    let bare = PathBuf::from(&name);
    // SAFETY: probing whether the loader can resolve the bare name. This runs
    // the library's initialisers, which is what `open` is about to do anyway.
    if unsafe { libloading::Library::new(&bare) }.is_ok() {
        return Ok(bare);
    }
    tried.push(bare);
    Err(Error::LibraryNotFound(tried))
}

/// How many handles the loaded library currently has open.
///
/// Diagnostic only, and exactly what a host test suite wants in order to assert
/// it closed what it opened. Returns `Ok(0)` before anything is loaded.
pub fn open_handles() -> Result<u64> {
    let api = Api::shared(&find_library()?)?;
    // SAFETY: no arguments, no ownership.
    Ok(unsafe { (api.open_handles)() })
}

// ---------------------------------------------------------------------------
// Engine
// ---------------------------------------------------------------------------

/// A handle plus the library it belongs to. Split out so a [`Refresher`] can
/// hold an owning `Arc` of its engine: the engine cannot be closed out from
/// under a refresher built over it.
struct Handle {
    api: &'static Api,
    id: u64,
}

impl Drop for Handle {
    fn drop(&mut self) {
        // SAFETY: `id` came from openrate_new / openrate_refresher_new and is
        // closed exactly once. openrate_close is documented idempotent and,
        // for an engine, also stops and releases every refresher over it.
        unsafe { (self.api.close)(self.id) }
    }
}

/// An openrate **engine**: computes, never fetches.
///
/// Constructing one starts no thread, opens no socket, reads no environment
/// variable and sends no packet. It answers from the snapshot it holds, and
/// until something gives it one it honestly says it does not know.
///
/// Closing is [`Drop`] — the handle is released when the `Engine` goes out of
/// scope, on early returns and panics as much as on the happy path.
pub struct Engine {
    inner: Arc<Handle>,
}

impl fmt::Debug for Engine {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Engine")
            .field("handle", &self.inner.id)
            .finish()
    }
}

impl Engine {
    /// Loads the library (see [`find_library`]) and creates an engine.
    ///
    /// `config_json` may be `None`. Fields: `{"base": "ZAR", "quiet": false}`.
    pub fn open(config_json: Option<&str>) -> Result<Engine> {
        Self::open_at(&find_library()?, config_json)
    }

    /// [`Engine::open`] against a library at a known path.
    pub fn open_at(path: &Path, config_json: Option<&str>) -> Result<Engine> {
        Self::from_api(Api::shared(path)?, config_json)
    }

    /// [`Engine::open`] with a version check first.
    ///
    /// Worth doing at startup: a shared library resolves off a load path you
    /// may not control, and a stale `libopenrate` earlier on it is otherwise
    /// called silently.
    pub fn open_checked(expected_version: &str, config_json: Option<&str>) -> Result<Engine> {
        let api = Api::shared(&find_library()?)?;
        let loaded = api.version();
        if loaded != expected_version {
            return Err(Error::VersionMismatch {
                loaded,
                expected: expected_version.to_string(),
            });
        }
        Self::from_api(api, config_json)
    }

    fn from_api(api: &'static Api, config_json: Option<&str>) -> Result<Engine> {
        let cfg = config_json.map(CString::new).transpose()?;
        let cfg_ptr = cfg.as_ref().map_or(std::ptr::null(), |c| c.as_ptr());

        let mut err: *mut c_char = std::ptr::null_mut();
        // SAFETY: cfg_ptr is null or a valid NUL-terminated string outliving
        // the call; err is a valid out-parameter.
        let id = unsafe { (api.new)(cfg_ptr, &mut err) };
        if id == 0 {
            return Err(unsafe { Error::from_c(api, err) });
        }
        Ok(Engine {
            inner: Arc::new(Handle { api, id }),
        })
    }

    /// The version the loaded library was built from, e.g. `"0.1.2"`.
    pub fn abi_version(&self) -> String {
        self.inner.api.version()
    }

    /// The raw registry key, for a host that wants to log or assert on it.
    /// Handles are never reused, so a stale number can only ever produce
    /// "handle N is not open" and never silent access to another object.
    pub fn handle(&self) -> u64 {
        self.inner.id
    }

    /// Run any engine method: `"convert"`, `"rates"`, `"meta"` or `"load"`.
    ///
    /// Prefer the named methods below; this is here for forward compatibility,
    /// since the ABI takes a method string precisely so the header stays stable
    /// as openrate grows methods.
    pub fn call(&self, method: &str, request_json: Option<&str>) -> Result<String> {
        self.inner.api.call(self.inner.id, method, request_json)
    }

    /// `{"from":"USD","to":"ZAR","amount":100}` → the conversion, with full
    /// provenance nested under `"rate"`.
    pub fn convert(&self, request_json: &str) -> Result<String> {
        self.call("convert", Some(request_json))
    }

    /// `{"base":"ZAR"}` → every known currency against that base.
    ///
    /// An unknown base is an **error** here, and a `404` carrying the same
    /// `unknown base currency` text over HTTP. An engine holding no rates at
    /// all returns an empty book and no error on either surface — that is a
    /// readiness question, not a bad request.
    pub fn rates(&self, request_json: Option<&str>) -> Result<String> {
        self.call("rates", request_json)
    }

    /// `{}` → default base, build time, currency list, and the fetch status of
    /// every refresher built over this engine (`[]` if nobody refreshes it).
    pub fn meta(&self) -> Result<String> {
        self.call("meta", None)
    }

    /// The zero-network path: install rates you obtained yourself.
    ///
    /// `{"edges":[{"from","to","rate","source","time"}], "built_at": "..."}`.
    /// `time` defaults to `built_at`, and `built_at` to now.
    pub fn load(&self, request_json: &str) -> Result<String> {
        self.call("load", Some(request_json))
    }

    /// Build a [`Refresher`] over this engine.
    ///
    /// **This is the line that gives your process an outbound dependency.**
    /// Constructing it still opens nothing — fetching starts at
    /// [`Refresher::refresh`] or [`Refresher::start`] — but from here on there
    /// is a code path from this program to the network, and before it there
    /// was not.
    ///
    /// `config_json` may be `None`. Fields:
    /// `{"sources":"ecb,coinbase", "interval_ms":3600000,
    ///   "fetch_timeout_ms":50000, "quiet":false}`. Sources are resolved
    /// purely: an API key in the environment never widens the set.
    pub fn refresher(&self, config_json: Option<&str>) -> Result<Refresher> {
        let api = self.inner.api;
        let cfg = config_json.map(CString::new).transpose()?;
        let cfg_ptr = cfg.as_ref().map_or(std::ptr::null(), |c| c.as_ptr());

        let mut err: *mut c_char = std::ptr::null_mut();
        // SAFETY: the engine handle is open (we own it) and cfg_ptr is null or
        // a valid string outliving the call.
        let id = unsafe { (api.refresher_new)(self.inner.id, cfg_ptr, &mut err) };
        if id == 0 {
            return Err(unsafe { Error::from_c(api, err) });
        }
        Ok(Refresher {
            inner: Handle { api, id },
            // Keep the engine alive for as long as the refresher exists.
            // Closing an engine also closes its refreshers, so without this an
            // engine dropped first would leave this handle already retired.
            _engine: Arc::clone(&self.inner),
        })
    }
}

// ---------------------------------------------------------------------------
// Refresher
// ---------------------------------------------------------------------------

/// An openrate **refresher**: the only object here that can open a socket.
///
/// It has its own handle and its own lifetime, and it holds its engine alive.
/// Closing is [`Drop`]; dropping a refresher that is running its background
/// loop stops the loop.
pub struct Refresher {
    inner: Handle,
    _engine: Arc<Handle>,
}

impl fmt::Debug for Refresher {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Refresher")
            .field("handle", &self.inner.id)
            .field("engine", &self._engine.id)
            .finish()
    }
}

impl Refresher {
    /// The raw registry key.
    pub fn handle(&self) -> u64 {
        self.inner.id
    }

    /// Run any refresher method: `"status"`, `"refresh"`, `"start"`, `"stop"`
    /// or `"ready"`.
    pub fn call(&self, method: &str, request_json: Option<&str>) -> Result<String> {
        self.inner.api.call(self.inner.id, method, request_json)
    }

    /// `{}` → `{"sources":[{"name","last_ok","last_error","edges"},...]}`.
    /// Opens nothing.
    pub fn status(&self) -> Result<String> {
        self.call("status", None)
    }

    /// One synchronous fetch of every source. **This opens sockets.**
    ///
    /// `{"timeout_ms":30000}`; `0` or absent means no deadline of your own.
    pub fn refresh(&self, request_json: Option<&str>) -> Result<String> {
        self.call("refresh", request_json)
    }

    /// Start the background loop on the configured interval. The only thread
    /// this library starts on its own.
    pub fn start(&self) -> Result<String> {
        self.call("start", None)
    }

    /// Stop the background loop and wait for it to exit.
    pub fn stop(&self) -> Result<String> {
        self.call("stop", None)
    }

    /// Block until the engine holds at least one currency.
    ///
    /// It does **not** fetch: something must be refreshing, or it waits out its
    /// timeout. `{"timeout_ms":5000}`.
    pub fn ready(&self, request_json: Option<&str>) -> Result<String> {
        self.call("ready", request_json)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn library_file_name_matches_the_build_script_convention() {
        let name = library_file_name();
        assert!(name.starts_with("libopenrate-"), "{name}");
        // Not llmux's `<goos>_<goarch>/libllmux.<ext>` shape.
        assert!(!name.contains('_'), "{name}");
        if cfg!(target_os = "macos") {
            assert!(name.ends_with(".dylib"), "{name}");
        }
    }

    #[test]
    fn missing_library_is_a_load_error_not_a_panic() {
        let err = Engine::open_at(Path::new("/nonexistent/libopenrate.dylib"), None).unwrap_err();
        assert!(matches!(err, Error::Load(_)), "got {err:?}");
    }

    #[test]
    fn error_display_names_the_env_var_and_the_paths() {
        let e = Error::LibraryNotFound(vec![PathBuf::from("/a/libopenrate-linux-arm64.so")]);
        let s = e.to_string();
        assert!(s.contains("OPENRATE_LIBRARY"), "{s}");
        assert!(s.contains("/a/libopenrate-linux-arm64.so"), "{s}");
    }
}
