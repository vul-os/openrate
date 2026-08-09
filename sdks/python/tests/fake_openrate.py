"""A fake `openrate` server, for the sidecar tests.

It honours OPENRATE_ADDR exactly as the real binary does and serves /healthz
plus the three read-only API endpoints from a fixed book. The point is that the
sidecar tests exercise binary resolution, the free-port dance, the health poll,
the singleton and the teardown WITHOUT needing a Go toolchain or a network — so
they run everywhere, every time.

Anything about openrate's own behaviour is tested in Go, against the real
engine. This fixture only has to be shaped like a server.

Run as: OPENRATE_ADDR=127.0.0.1:8080 python3 fake_openrate.py
"""

from __future__ import annotations

import json
import os
import sys
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

BOOK = {"USD": 18.5, "EUR": 20.2, "GBP": 23.5}
DELAY_HEALTH = os.environ.get("FAKE_OPENRATE_NEVER_HEALTHY") == "1"


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
            if DELAY_HEALTH:
                self.send_response(503)
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            body = b"ok"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        if parsed.path == "/api/v1/meta":
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
