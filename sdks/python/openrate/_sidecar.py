"""Sidecar launcher: spawn the bundled openrate binary on a local port.

Instead of running a server yourself, this starts openrate as a child process on
127.0.0.1 and hands you a base URL. The JSON it serves is the same JSON the C
ABI returns, so a program can move between the two modes without changing its
parser.

    import openrate

    client = openrate.Client()               # spawns the server, or reuses one
    print(client.convert("USD", "ZAR", 100)["result"])

Or, if you already run openrate somewhere:

    client = openrate.Client("https://openrate.example")   # spawns nothing
"""

from __future__ import annotations

import atexit
import json
import os
import platform
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

__all__ = ["Client", "OpenRateSidecarError", "base_url", "start", "stop"]


class OpenRateSidecarError(RuntimeError):
    pass


_lock = threading.Lock()
_proc: subprocess.Popen | None = None
_base: str | None = None


def _binary_path() -> str:
    """$OPENRATE_BINARY, then a bundled bin/openrate, then PATH."""
    env = os.environ.get("OPENRATE_BINARY")
    if env:
        return env
    name = "openrate.exe" if platform.system() == "Windows" else "openrate"
    bundled = Path(__file__).resolve().parent / "bin" / name
    if bundled.exists():
        return str(bundled)
    from shutil import which

    found = which("openrate")
    if found:
        return found
    raise OpenRateSidecarError(
        "openrate binary not found. Set OPENRATE_BINARY, install a platform wheel, "
        "or build it: `go build -o sdks/python/openrate/bin/openrate ./cmd/openrate`"
    )


def _free_port() -> int:
    """Racy by construction — see the same note in every SDK in this repo."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_healthy(base: str, timeout: float) -> None:
    deadline = time.time() + timeout
    last: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(base + "/healthz", timeout=1) as r:
                if r.status == 200:
                    return
        except (urllib.error.URLError, ConnectionError, OSError) as exc:
            last = exc
        time.sleep(0.05)
    raise OpenRateSidecarError(f"openrate did not become healthy within {timeout}s: {last}")


def start(
    port: int | None = None,
    base_currency: str | None = None,
    sources: str | None = None,
    refresh: str | None = None,
    env: dict | None = None,
    timeout: float = 20.0,
) -> str:
    """Start the sidecar (idempotent) and return its base URL.

    ``sources`` is a comma-separated adapter list (``"ecb,coinbase"``); the
    default is openrate's own (ecb, coinbase, luno, sarb). ``refresh`` is a Go
    duration such as ``"1h"``.

    UNLIKE DIRECT MODE, THIS FETCHES. The server refreshes on startup and on its
    interval — that is what a server is for. If you want a process that provably
    sends no packets, that is direct mode with :class:`openrate.Engine` and no
    refresher.
    """
    global _proc, _base
    with _lock:
        if _proc is not None and _proc.poll() is None:
            return _base  # type: ignore[return-value]

        port = port or _free_port()
        addr = f"127.0.0.1:{port}"
        child_env = dict(os.environ)
        child_env["OPENRATE_ADDR"] = addr
        if base_currency:
            child_env["OPENRATE_BASE"] = base_currency
        if sources is not None:
            child_env["OPENRATE_SOURCES"] = sources
        if refresh:
            child_env["OPENRATE_REFRESH"] = refresh
        if env:
            child_env.update(env)

        _proc = subprocess.Popen([_binary_path()], env=child_env)
        _base = f"http://{addr}"
        try:
            _wait_healthy(_base, timeout)
        except Exception:
            _stop_locked()  # _lock is held; the non-reentrant lock would deadlock
            raise
        atexit.register(stop)
        return _base


def base_url() -> str:
    """The running sidecar's base URL, starting it if needed."""
    if _proc is None or _proc.poll() is not None:
        return start()
    return _base  # type: ignore[return-value]


def stop() -> None:
    """Stop the sidecar if this process started one."""
    with _lock:
        _stop_locked()


def _stop_locked() -> None:
    global _proc
    if _proc is not None and _proc.poll() is None:
        _proc.terminate()
        try:
            _proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            _proc.kill()
    _proc = None


class Client:
    """A read-only HTTP client for openrate's JSON API.

    With no argument it starts (or reuses) a managed sidecar. Given a URL it
    spawns nothing and talks to the server you already run — the same client
    either way, because it is the same API.

    ``urllib`` is the whole dependency list.
    """

    def __init__(self, url: str | None = None, timeout: float = 10.0) -> None:
        self._explicit = url.rstrip("/") if url else None
        self.timeout = timeout

    @property
    def base_url(self) -> str:
        return self._explicit if self._explicit else base_url()

    def __enter__(self) -> "Client":
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def close(self) -> None:
        """Stop the managed sidecar, if this client is using one.

        A client pointed at somebody else's server closes nothing, which is the
        only defensible behaviour: it did not start it.
        """
        if self._explicit is None:
            stop()

    def _get(self, path: str, **params: Any) -> Any:
        query = {k: v for k, v in params.items() if v is not None}
        url = self.base_url + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        try:
            with urllib.request.urlopen(url, timeout=self.timeout) as r:
                return json.load(r)
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", "replace").strip()
            raise OpenRateSidecarError(f"HTTP {exc.code} from {path}: {body}") from exc

    def convert(
        self, from_currency: str | None = None, to_currency: str | None = None, amount: float = 1
    ) -> Any:
        """``GET /api/v1/convert`` — the same document ``Engine.convert`` returns."""
        return self._get("/api/v1/convert", **{"from": from_currency, "to": to_currency,
                                               "amount": amount})

    def rates(self, base: str | None = None) -> Any:
        """``GET /api/v1/rates``.

        One deliberate difference from direct mode: an unknown base answers 200
        with an empty book here, where ``Engine.rates`` raises.
        """
        return self._get("/api/v1/rates", base=base)

    def meta(self) -> Any:
        """``GET /api/v1/meta`` — default base, build time, currencies, sources."""
        return self._get("/api/v1/meta")

    def healthy(self) -> bool:
        try:
            with urllib.request.urlopen(self.base_url + "/healthz", timeout=self.timeout) as r:
                return r.status == 200
        except (urllib.error.URLError, OSError):
            return False
