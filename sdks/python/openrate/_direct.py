"""Direct mode: openrate running IN THIS PROCESS, through the C ABI.

The other half of this package is :mod:`openrate._sidecar`, which spawns the
``openrate`` binary and talks HTTP to it. This half loads ``libopenrate`` with
:mod:`ctypes` and calls it in-process — no second process, no port, no loopback
socket. The JSON is identical either way: it is the JSON the HTTP API publishes.

    from openrate import Engine

    with Engine() as engine:
        engine.load(edges=[{"from": "USD", "to": "ZAR", "rate": 18.5, "source": "mine"}])
        print(engine.convert("USD", "ZAR", 100)["result"])   # 1850.0

``ctypes`` is the whole dependency list; this module imports nothing outside the
standard library.

TWO KINDS OF HANDLE, AND THE DIFFERENCE IS THE POINT. An :class:`Engine`
computes: constructing one starts no thread, opens no socket, reads no
environment variable and sends no packet, and it answers "unknown or unreachable
currency pair" until something gives it rates. A :class:`Refresher` fetches, is
a separate construction with its own handle, and is the only object here that
can touch the network. A program that never builds one cannot make this library
send a packet — that is not a convention, there is no other code path. Feed an
engine without one using :meth:`Engine.load`.

There is no ``openrate_stream``: openrate answers from a snapshot it already
holds, so there is no incremental operation to stream. llmux, which shares this
ABI shape, does have one. The omission is deliberate, not a gap.

READ ``sdks/python/README.md`` BEFORE CHOOSING THIS. It loads the Go runtime
into your interpreter, and it is not fork-safe — which in Python means
``multiprocessing`` with the ``fork`` start method, uWSGI, and Gunicorn's sync
workers.
"""

from __future__ import annotations

import ctypes
import json
import os
import platform
import threading
from pathlib import Path
from typing import Any, Iterable, Mapping, Sequence

__all__ = [
    "Engine",
    "OpenRateError",
    "OpenRateLibraryError",
    "Refresher",
    "abi_version",
    "library_path",
    "load_library",
    "open_handles",
]


class OpenRateError(RuntimeError):
    """The library refused a call. The message is its own, verbatim."""


class OpenRateLibraryError(OpenRateError):
    """The shared library could not be found, loaded, or is the wrong build."""


# --------------------------------------------------------------------------
# Finding the library
# --------------------------------------------------------------------------


def _go_platform() -> tuple[str, str]:
    goos = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}.get(
        platform.system(), platform.system().lower()
    )
    machine = platform.machine().lower()
    goarch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(
        machine, machine
    )
    return goos, goarch


def _lib_filenames() -> list[str]:
    """Both spellings: build-ffi.sh's per-target name, and the plain one."""
    goos, goarch = _go_platform()
    ext = {"darwin": "dylib", "windows": "dll"}.get(goos, "so")
    prefix = "" if goos == "windows" else "lib"
    return [f"{prefix}openrate-{goos}-{goarch}.{ext}", f"{prefix}openrate.{ext}"]


def _candidates() -> list[Path]:
    out: list[Path] = []
    here = Path(__file__).resolve()
    for name in _lib_filenames():
        # 1. Bundled in the wheel next to this module.
        out.append(here.parent / "lib" / name)
        # 2. A checkout: scripts/build-ffi.sh writes dist/ffi/.
        for parent in here.parents:
            out.append(parent / "dist" / "ffi" / name)
            if (parent / ".git").exists():
                break
    return out


def library_path(path: str | os.PathLike[str] | None = None) -> str:
    """Resolve the shared library path, or raise a message that says where it looked.

    Order: the ``path`` argument, then ``$OPENRATE_LIBRARY``, then the bundled
    ``openrate/lib/``, then ``dist/ffi/`` in an enclosing checkout, then the
    platform loader's own search path.
    """
    if path:
        return str(path)
    env = os.environ.get("OPENRATE_LIBRARY")
    if env:
        return env

    tried: list[str] = []
    for candidate in _candidates():
        if candidate.exists():
            return str(candidate)
        tried.append(str(candidate))

    for name in _lib_filenames():
        try:
            ctypes.CDLL(name)
            return name
        except OSError:
            tried.append(f"{name} (via the platform loader's search path)")

    goos, goarch = _go_platform()
    note = ""
    if (goos, goarch) != ("darwin", "arm64"):
        note = (
            f"\n\nOn openrate, only darwin/arm64 has been built AND executed. "
            "darwin/amd64 is built but has never been run; linux/amd64 has a CI job that "
            "has never run; linux/arm64 and windows/amd64 are built nowhere. Use the "
            "sidecar (`import openrate; openrate.start()`) — it is a supported answer, "
            "not a fallback."
        )
    raise OpenRateLibraryError(
        "libopenrate not found. Set OPENRATE_LIBRARY=/path/to/"
        + _lib_filenames()[0]
        + ", or build it with `scripts/build-ffi.sh` from an openrate checkout.\nLooked at:\n  "
        + "\n  ".join(tried)
        + note
    )


# --------------------------------------------------------------------------
# Binding
# --------------------------------------------------------------------------

# openrate_call hands back malloc'd memory that only openrate_free may release.
# ctypes' c_char_p restype would copy the bytes and DISCARD THE POINTER, leaking
# every result — so results come back as POINTER(c_char) and _take() copies then
# frees. This is the single most important detail in the file.
_CharPtr = ctypes.POINTER(ctypes.c_char)
_ErrPtr = ctypes.POINTER(ctypes.c_char_p)

_loaded: dict[str, ctypes.CDLL] = {}
_loaded_lock = threading.Lock()


def load_library(
    path: str | os.PathLike[str] | None = None, require_version: str | None = None
) -> ctypes.CDLL:
    """Load (once per path) and bind ``libopenrate``.

    ``require_version`` compares :func:`abi_version` against what your bindings
    were written for. Worth doing at startup: a shared library is resolved off a
    load path you may not control.
    """
    resolved = library_path(path)
    with _loaded_lock:
        lib = _loaded.get(resolved)
        if lib is None:
            try:
                lib = ctypes.CDLL(resolved)
            except OSError as exc:  # pragma: no cover - platform specific
                raise OpenRateLibraryError(f"could not load {resolved}: {exc}") from exc
            _bind(lib)
            _loaded[resolved] = lib

    if require_version is not None:
        got = abi_version(lib)
        if got != require_version:
            raise OpenRateLibraryError(
                f"{resolved} reports openrate {got}, these bindings want {require_version} "
                "— a stale library is on the load path"
            )
    return lib


def _bind(lib: ctypes.CDLL) -> None:
    lib.openrate_abi_version.restype = ctypes.c_char_p  # static; never freed
    lib.openrate_abi_version.argtypes = []

    lib.openrate_new.restype = ctypes.c_uint64
    lib.openrate_new.argtypes = [ctypes.c_char_p, _ErrPtr]

    lib.openrate_refresher_new.restype = ctypes.c_uint64
    lib.openrate_refresher_new.argtypes = [ctypes.c_uint64, ctypes.c_char_p, _ErrPtr]

    lib.openrate_call.restype = _CharPtr
    lib.openrate_call.argtypes = [ctypes.c_uint64, ctypes.c_char_p, ctypes.c_char_p, _ErrPtr]

    lib.openrate_close.restype = None
    lib.openrate_close.argtypes = [ctypes.c_uint64]

    lib.openrate_free.restype = None
    lib.openrate_free.argtypes = [_CharPtr]

    lib.openrate_open_handles.restype = ctypes.c_uint64
    lib.openrate_open_handles.argtypes = []


def abi_version(lib: ctypes.CDLL | None = None) -> str:
    """The openrate version the loaded library was built from."""
    lib = lib or load_library()
    return lib.openrate_abi_version().decode("utf-8")


def open_handles(lib: ctypes.CDLL | None = None) -> int:
    """How many handles the library currently holds. Diagnostic only.

    Useful in a host's own test suite: assert it closed what it opened.
    """
    lib = lib or load_library()
    return int(lib.openrate_open_handles())


def _take(lib: ctypes.CDLL, ptr: Any) -> str | None:
    """Copy a library-owned C string into Python and free the original."""
    if not ptr:
        return None
    try:
        return ctypes.cast(ptr, ctypes.c_char_p).value.decode("utf-8")  # type: ignore[union-attr]
    finally:
        lib.openrate_free(ptr)


def _take_err(lib: ctypes.CDLL, err: ctypes.c_char_p) -> str | None:
    if not err.value:
        return None
    message = err.value.decode("utf-8", "replace")
    lib.openrate_free(ctypes.cast(err, _CharPtr))
    err.value = None
    return message


def _encode(body: Any) -> bytes | None:
    if body is None:
        return None
    if isinstance(body, bytes):
        return body
    if isinstance(body, str):
        return body.encode("utf-8")
    return json.dumps(body).encode("utf-8")


# --------------------------------------------------------------------------
# Handles
# --------------------------------------------------------------------------


class _Handle:
    """Shared lifecycle for the two handle kinds."""

    _kind = "handle"

    def __init__(self, lib: ctypes.CDLL, handle: int) -> None:
        self._lib = lib
        self._handle = int(handle)
        self._closed = False

    @property
    def handle(self) -> int:
        """The raw uint64 registry key, for code that calls the ABI directly."""
        return self._handle

    @property
    def closed(self) -> bool:
        return self._closed

    def close(self) -> None:
        """Release the handle. Idempotent, like ``openrate_close`` itself."""
        if not self._closed:
            self._closed = True
            self._lib.openrate_close(ctypes.c_uint64(self._handle))

    def __enter__(self):
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def __del__(self) -> None:  # best effort; the context manager is the contract
        try:
            self.close()
        except Exception:  # pragma: no cover - interpreter teardown
            pass

    def __repr__(self) -> str:
        return f"<openrate.{type(self).__name__} handle={self._handle} " \
               f"{'closed' if self._closed else 'open'}>"

    def call(self, method: str, request: Any = None) -> Any:
        """One call against this handle. Returns the parsed JSON response.

        Raises :class:`OpenRateError` with the library's message on failure. The
        message is plain text, not JSON, so it is not parsed.
        """
        if self._closed:
            raise OpenRateError(f"{self._kind} {self._handle} is closed")
        err = ctypes.c_char_p()
        ptr = self._lib.openrate_call(
            ctypes.c_uint64(self._handle),
            method.encode("utf-8"),
            _encode(request),
            ctypes.byref(err),
        )
        result = _take(self._lib, ptr)
        message = _take_err(self._lib, err)
        if result is None:
            raise OpenRateError(message or f"openrate_call {method} failed without a message")
        return json.loads(result)


class Engine(_Handle):
    """A conversion engine. Computes; never fetches.

    Constructing one starts no thread, opens no socket, reads no environment
    variable and sends no packet. It answers from the snapshot it holds and says
    "unknown or unreachable currency pair" until something gives it one — either
    :meth:`load`, or a :class:`Refresher` built over it.

        with Engine({"base": "ZAR"}) as engine:
            engine.load(edges=[...])
            engine.convert("USD", "ZAR", 100)

    Use it as a context manager. ``__exit__`` closes the handle on every path,
    including an exception — and closing an engine also stops and releases every
    refresher built over it, so closing in the "wrong" order cannot leak a
    running loop.
    """

    _kind = "engine"

    def __init__(
        self,
        config: Mapping[str, Any] | str | bytes | None = None,
        *,
        library: str | os.PathLike[str] | ctypes.CDLL | None = None,
        require_version: str | None = None,
    ) -> None:
        lib = (
            library
            if isinstance(library, ctypes.CDLL)
            else load_library(library, require_version=require_version)
        )
        err = ctypes.c_char_p()
        handle = lib.openrate_new(_encode(config), ctypes.byref(err))
        message = _take_err(lib, err)
        if handle == 0:
            raise OpenRateError(message or "openrate_new failed without a message")
        super().__init__(lib, handle)

    # -- methods -----------------------------------------------------------

    def convert(
        self, from_currency: str | None = None, to_currency: str | None = None, amount: float = 1
    ) -> Any:
        """Convert an amount. Identical to ``GET /api/v1/convert``.

        An omitted currency means the engine's default base.
        """
        request: dict[str, Any] = {"amount": amount}
        if from_currency is not None:
            request["from"] = from_currency
        if to_currency is not None:
            request["to"] = to_currency
        return self.call("convert", request)

    def rates(self, base: str | None = None) -> Any:
        """The whole book against ``base``. Identical to ``GET /api/v1/rates``.

        ``rates[X].rate`` reads as "1 base = rate units of X". An unknown base
        is an error here, and a ``404`` carrying the same text over HTTP. An
        engine holding no rates at all returns an empty book and no error on
        either surface: that is a readiness question, not a bad request.
        """
        return self.call("rates", {"base": base} if base else {})

    def meta(self) -> Any:
        """Default base, build time, currency list, and every refresher's status.

        Identical to ``GET /api/v1/meta``. ``sources`` is ``[]`` for an engine
        nobody refreshes.
        """
        return self.call("meta", {})

    def load(
        self,
        edges: Iterable[Mapping[str, Any]] | None = None,
        built_at: str | None = None,
        **kwargs: Any,
    ) -> Any:
        """Install rates you obtained yourself. The zero-network path.

        Each edge is ``{"from", "to", "rate"}`` plus optional ``"source"`` and
        ``"time"``; ``time`` defaults to ``built_at``, and ``built_at`` to now.
        Has no HTTP counterpart, because the server is read-only.
        """
        request: dict[str, Any] = dict(kwargs)
        if edges is not None:
            request["edges"] = list(edges)
        if built_at is not None:
            request["built_at"] = built_at
        return self.call("load", request)

    def refresher(self, config: Mapping[str, Any] | str | bytes | None = None) -> "Refresher":
        """Build a :class:`Refresher` over this engine.

        This is the only object in openrate that can open a socket, and building
        it still does not: fetching begins at :meth:`Refresher.refresh` or
        :meth:`Refresher.start`.
        """
        return Refresher(self, config)


class Refresher(_Handle):
    """Fetches rates into an engine. The only thing here that touches the network.

    A separate construction with its own handle and its own lifetime, because
    that is what makes "this program sends no packets" checkable rather than
    promised.

        with Engine() as engine, engine.refresher({"sources": "ecb"}) as refresher:
            refresher.refresh()          # THIS OPENS SOCKETS
            engine.convert("EUR", "ZAR", 100)
    """

    _kind = "refresher"

    def __init__(
        self, engine: Engine, config: Mapping[str, Any] | str | bytes | None = None
    ) -> None:
        if engine.closed:
            raise OpenRateError("cannot build a refresher over a closed engine")
        err = ctypes.c_char_p()
        handle = engine._lib.openrate_refresher_new(
            ctypes.c_uint64(engine.handle), _encode(config), ctypes.byref(err)
        )
        message = _take_err(engine._lib, err)
        if handle == 0:
            raise OpenRateError(message or "openrate_refresher_new failed without a message")
        super().__init__(engine._lib, handle)

    def status(self) -> Any:
        """Per-source fetch status. Opens nothing."""
        return self.call("status", {})

    def refresh(self, timeout_ms: int | None = None) -> Any:
        """One synchronous fetch of every configured source. **This opens sockets.**"""
        return self.call("refresh", {"timeout_ms": timeout_ms} if timeout_ms else {})

    def start(self) -> Any:
        """Start the background loop on the configured interval.

        The only thread this library starts on its own.
        """
        return self.call("start", {})

    def stop(self) -> Any:
        """Stop the background loop and wait for it to exit."""
        return self.call("stop", {})

    def ready(self, timeout_ms: int | None = None) -> Any:
        """Block until the engine holds at least one currency.

        It does not fetch: something must be refreshing, or it waits.
        """
        return self.call("ready", {"timeout_ms": timeout_ms} if timeout_ms else {})
