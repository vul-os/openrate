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


_LOOPBACK = frozenset({"127.0.0.1", "::1", "localhost"})
_no_proxy_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def _urlopen(url: str, timeout: float):
    """urlopen, except that loopback never goes through a proxy.

    $HTTP_PROXY/$HTTPS_PROXY are process-wide and urllib honours them for every
    host, including 127.0.0.1 — so in a proxied environment the managed sidecar
    became unreachable from the process that spawned it, and said so in the
    proxy's words ("connection refused") rather than its own. A child on
    loopback is never something a proxy should carry. A client pointed at a
    remote server keeps the environment's proxy settings, which is what they
    are for.
    """
    host = urllib.parse.urlsplit(url).hostname or ""
    opener = _no_proxy_opener if host in _LOOPBACK else urllib.request
    return opener.open(url, timeout=timeout)


def _not_ready_detail(body: Any) -> str:
    """One actionable line out of a /readyz 503 body.

    The body carries `reason` plus the per-source outcomes, and the sources are
    the half a caller can act on: "no rates yet" is a symptom, "ecb: connection
    refused" is the cause. `last_error` is omitempty, so a source that has not
    been tried yet has no key at all — those are skipped rather than printed as
    `ecb: None`, and if nothing failed the reason stands alone.
    """
    if not isinstance(body, dict):
        return "not ready"
    reason = str(body.get("reason") or "not ready")
    sources = body.get("sources")
    failed = [
        f"{source.get('name') or '?'}: {source['last_error']}"
        for source in (sources if isinstance(sources, list) else [])
        if isinstance(source, dict) and source.get("last_error")
    ]
    return f"{reason} ({'; '.join(failed)})" if failed else reason


def _wait_ready(base: str, timeout: float, interval: float = 0.1) -> None:
    """Poll GET /readyz until the server can actually answer a conversion.

    Not /healthz. /healthz answers the instant the listener binds, before any
    source has been fetched, so a caller that treats it as ready converts
    against an empty book and gets "unknown or unreachable currency pair" for
    every pair — a false green wearing a bad-currency-code costume.

    A fixed 100 ms poll is correct here: /readyz sits outside /api/, so the
    per-IP limiter never sees it and there is no budget to spend.

    On timeout the caller gets the cause, not a bare deadline: whatever the last
    503 said, or — if the server never answered at all — the transport error.
    """
    deadline = time.monotonic() + timeout
    detail: str | None = None  # from the last 503 body
    transport: Exception | None = None
    while True:
        try:
            with _urlopen(base + "/readyz", timeout=2) as r:
                if r.status == 200:
                    # A 200 is not enough. An openrate that predates /readyz has
                    # no such route, so its console catch-all answers 200 with
                    # HTML — and a status-only check reads that as ready and
                    # hands the caller an engine holding zero rates. The 404
                    # branch below only catches binaries whose catch-all is off.
                    # Require the document to say so itself.
                    try:
                        doc = json.loads(r.read().decode("utf-8", "replace"))
                    except ValueError:
                        raise OpenRateSidecarError(
                            f"{base}/readyz answered 200 but not JSON. That is what an "
                            "openrate predating the readiness endpoint looks like: the "
                            "console catch-all answering instead of a route. Upgrade it."
                        ) from None
                    if doc.get("ready") is True:
                        return
                    detail = _not_ready_detail(doc)
                    transport = None
        except urllib.error.HTTPError as exc:
            # urllib hands a non-2xx response back as an exception, and the 503
            # here has a JSON body worth keeping — reading it is the whole point.
            if exc.code == 404:
                raise OpenRateSidecarError(
                    f"{base}/readyz answered 404: this openrate binary predates the "
                    "readiness endpoint. Upgrade it, or point the client at a server "
                    "that has one."
                ) from exc
            try:
                detail = _not_ready_detail(json.loads(exc.read().decode("utf-8", "replace")))
            except (ValueError, OSError):
                detail = f"HTTP {exc.code}"
            transport = None
        except (urllib.error.URLError, OSError) as exc:
            # Not listening yet (or gone). Keep the text: on the unhappy path it
            # is the difference between "the child died" and "the child is slow".
            transport, detail = exc, None
        if time.monotonic() >= deadline:
            break
        time.sleep(interval)

    if detail is not None:
        raise OpenRateSidecarError(f"openrate has no rates after {timeout:g}s: {detail}")
    raise OpenRateSidecarError(
        f"openrate never answered /readyz within {timeout:g}s: {transport}"
    )


def start(
    port: int | None = None,
    base_currency: str | None = None,
    sources: str | None = None,
    refresh: str | None = None,
    env: dict | None = None,
    ratelimit: int = 0,
    timeout: float = 30.0,
) -> str:
    """Start the sidecar (idempotent) and return its base URL.

    ``sources`` is a comma-separated adapter list (``"ecb,coinbase"``); the
    default is openrate's own (ecb, coinbase, luno, sarb). ``refresh`` is a Go
    duration such as ``"1h"``.

    It returns when the child is READY — when ``/readyz`` says a conversion
    would succeed — not merely when it is listening. ``timeout`` therefore has
    to cover the first fetch, and a server that comes up but never gets a rate
    raises with the reason ``/readyz`` gave rather than a bare deadline.

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
        # The child listens on loopback and serves exactly one client: us. The
        # binary's 120/min default is anti-scraping for a public deployment and
        # there is no stranger here to throttle, while an ordinary batch of
        # conversions would sail past it and take a 429 from our own sidecar.
        # Pass ratelimit=120 to put the public default back.
        child_env["OPENRATE_RATELIMIT"] = str(ratelimit)
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
            _wait_ready(_base, timeout)
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
            with _urlopen(url, timeout=self.timeout) as r:
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

        An unknown base is refused, exactly as ``Engine.rates`` refuses it: the
        endpoint answers ``404 {"error": "unknown base currency"}``. An engine
        holding no rates at all still returns an empty book and no error — that
        is "nothing yet", which ``ready()`` is the question for.
        """
        return self._get("/api/v1/rates", base=base)

    def meta(self) -> Any:
        """``GET /api/v1/meta`` — default base, build time, currencies, sources."""
        return self._get("/api/v1/meta")

    def healthy(self) -> bool:
        """``GET /healthz`` — liveness. True the moment the process is listening.

        Do not convert on the strength of this: it is 200 before the first fetch
        lands. :meth:`ready` is the question you actually want answered.
        """
        try:
            with _urlopen(self.base_url + "/healthz", timeout=self.timeout) as r:
                return r.status == 200
        except (urllib.error.URLError, OSError):
            return False

    def ready(self) -> bool:
        """``GET /readyz`` — readiness. True once a conversion would succeed.

        A managed sidecar is already ready by the time :func:`start` returns.
        This is for the other case: a server you were merely pointed at, which
        may be up and still empty.
        """
        try:
            with _urlopen(self.base_url + "/readyz", timeout=self.timeout) as r:
                return r.status == 200
        except (urllib.error.URLError, OSError):
            return False

    def wait_ready(self, timeout: float = 30.0) -> None:
        """Block until ``/readyz`` says ready, or raise with the reason it gave.

        Redundant for a managed sidecar, where :func:`start` has already waited.
        Useful against a server you do not control — and it is the same poll,
        not a second implementation of readiness.
        """
        _wait_ready(self.base_url, timeout)
