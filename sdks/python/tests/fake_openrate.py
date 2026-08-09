"""A fake `openrate` server, for the sidecar tests.

It honours OPENRATE_ADDR exactly as the real binary does and serves /healthz,
/readyz and the three read-only API endpoints from a fixed book. The point is
that the sidecar tests exercise binary resolution, the free-port dance, the
readiness poll, the singleton and the teardown WITHOUT needing a Go toolchain or
a network — so they run everywhere, every time.

Anything about openrate's own behaviour is tested in Go, against the real
engine. This fixture only has to be shaped like a server — including the shapes
that go wrong, which is what the knobs below are for:

    FAKE_OPENRATE_NEVER_READY=1   /readyz always 503, /healthz stays 200 (the
                                  liveness/readiness gap, held open)
    FAKE_OPENRATE_LAST_ERROR=...  the failing source's last_error in that 503.
                                  Empty omits the key entirely, exactly as the
                                  real server's `omitempty` does for a source it
                                  has not tried yet
    FAKE_OPENRATE_NEVER_LISTEN=1  bind nothing at all, so the client sees a
                                  transport error rather than a 503
    FAKE_OPENRATE_META_BROKEN=1   /api/v1/meta 500s. Readiness must not care:
                                  it is no longer the readiness probe

Run as: OPENRATE_ADDR=127.0.0.1:8080 python3 fake_openrate.py
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

BOOK = {"USD": 18.5, "EUR": 20.2, "GBP": 23.5}
NEVER_READY = os.environ.get("FAKE_OPENRATE_NEVER_READY") == "1"
LAST_ERROR = os.environ.get("FAKE_OPENRATE_LAST_ERROR", "")
NEVER_LISTEN = os.environ.get("FAKE_OPENRATE_NEVER_LISTEN") == "1"
META_BROKEN = os.environ.get("FAKE_OPENRATE_META_BROKEN") == "1"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def _json(self, payload: dict, status: int = 200) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler's spelling
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)

        if parsed.path == "/healthz":
            # Liveness, and deliberately not tied to readiness: the real server
            # answers this the instant the listener binds, which is the whole
            # reason /readyz exists.
            body = b"ok"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        if parsed.path == "/readyz":
            if NEVER_READY:
                source: dict = {"name": "fake", "edges": 0}
                if LAST_ERROR:
                    source["last_error"] = LAST_ERROR
                self._json(
                    {
                        "ready": False,
                        "currencies": 0,
                        "built_at": "0001-01-01T00:00:00Z",
                        "reason": "no rates yet: no source has returned a usable quote",
                        "sources": [source],
                    },
                    status=503,
                )
                return
            self._json(
                {
                    "ready": True,
                    "currencies": 1 + len(BOOK),
                    "built_at": "2026-08-09T00:00:00Z",
                    "sources": [{"name": "fake", "edges": len(BOOK)}],
                }
            )
            return

        if parsed.path == "/api/v1/meta":
            if META_BROKEN:
                self._json({"error": "meta is broken on purpose"}, status=500)
                return
            self._json(
                {
                    "default_base": "ZAR",
                    "built_at": "2026-08-09T00:00:00Z",
                    "currencies": ["ZAR", *BOOK],
                    "sources": [{"name": "fake", "edges": len(BOOK), "last_error": ""}],
                }
            )
            return

        if parsed.path == "/api/v1/rates":
            base = (query.get("base", ["ZAR"])[0] or "ZAR").upper()
            if base != "ZAR":
                self._json({"base": base, "built_at": "2026-08-09T00:00:00Z", "rates": {}})
                return
            self._json(
                {
                    "base": "ZAR",
                    "built_at": "2026-08-09T00:00:00Z",
                    "rates": {ccy: {"rate": 1 / r, "hops": 1, "path": ["ZAR", ccy]}
                              for ccy, r in BOOK.items()},
                }
            )
            return

        if parsed.path == "/api/v1/convert":
            frm = (query.get("from", ["ZAR"])[0] or "ZAR").upper()
            to = (query.get("to", ["ZAR"])[0] or "ZAR").upper()
            amount = float(query.get("amount", ["1"])[0] or 1)
            rate = self._rate(frm, to)
            if rate is None:
                self._json({"error": "unknown or unreachable currency pair"}, status=404)
                return
            self._json(
                {
                    "from": frm,
                    "to": to,
                    "amount": amount,
                    "result": amount * rate,
                    "rate": {"rate": rate, "hops": 1, "path": [frm, to], "sources": ["fake"],
                             "age_sec": 0, "quality": {"grade": "A"}},
                }
            )
            return

        self.send_response(404)
        self.send_header("Content-Length", "0")
        self.end_headers()

    @staticmethod
    def _rate(frm: str, to: str) -> float | None:
        if frm == to:
            return 1.0
        if frm == "ZAR" and to in BOOK:
            return 1 / BOOK[to]
        if to == "ZAR" and frm in BOOK:
            return BOOK[frm]
        if frm in BOOK and to in BOOK:
            return BOOK[frm] / BOOK[to]
        return None


def main() -> None:
    if NEVER_LISTEN:
        # A child that runs but never binds. The launcher must report the
        # connection error it kept, not a bare deadline.
        while True:
            time.sleep(3600)

    addr = os.environ.get("OPENRATE_ADDR", "127.0.0.1:0")
    host, _, port = addr.rpartition(":")
    server = ThreadingHTTPServer((host or "127.0.0.1", int(port)), Handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    sys.exit(main())
