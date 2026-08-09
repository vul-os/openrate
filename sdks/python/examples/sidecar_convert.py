#!/usr/bin/env python3
"""Sidecar mode: openrate as a child process on 127.0.0.1, spoken to over HTTP.

    python3 examples/sidecar_convert.py                       # managed child
    python3 examples/sidecar_convert.py https://openrate.io    # somebody else's

You do not run a server. `openrate.Client()` finds the binary
($OPENRATE_BINARY, the bundled openrate/bin/openrate, or `openrate` on PATH),
picks a free loopback port, launches it, waits for /readyz, and terminates it at
exit. Given a URL instead, it spawns nothing and talks to the server you already
run — the same client either way, because it is the same API.

UNLIKE DIRECT MODE, THIS FETCHES. A server refreshes on startup and on its
interval; that is what a server is for. If you want a process that provably
sends no packets, that is `examples/direct_convert.py` — an engine with no
refresher.

This example needs network access for the managed case, because a freshly
started openrate has an empty book until its first fetch lands. Waiting for
/readyz rather than /healthz is what makes that a named failure ("ecb:
connection refused") instead of every conversion coming back as an unknown
currency pair.

    OPENRATE_READY_TIMEOUT   seconds to wait for the first rate (default 30)
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import openrate  # noqa: E402
from openrate import Client, OpenRateSidecarError  # noqa: E402

SOURCES = "ecb"  # one fast source keeps the example quick; the default is four
READY_TIMEOUT = float(os.environ.get("OPENRATE_READY_TIMEOUT", "30"))


def main(argv: list[str]) -> int:
    url = argv[0] if argv else None

    # try/finally, not just atexit: an exception below must not leave a server
    # process behind for the rest of this interpreter's life. `with` does the
    # same thing, and closes nothing when the client points at somebody else's
    # server — it did not start it.
    if url is None:
        # start() returns when /readyz says a conversion will succeed, so there
        # is no separate "wait for the book" step here and no window in which
        # this example could convert against an empty one. If no rate arrives it
        # raises, naming the source that failed.
        openrate.start(sources=SOURCES, base_currency="ZAR", timeout=READY_TIMEOUT)

    with Client(url) as client:
        print(f"base url:     {client.base_url}")
        print(f"managed:      {'no — pointing at an existing server' if url else 'yes'}")
        print(f"healthy:      {client.healthy()}   (liveness: the process is up)")
        print(f"ready:        {client.ready()}   (readiness: it has rates)\n")

        if url is not None:
            # Somebody else's server can be up and still empty; we did not wait
            # for it at startup because we did not start it.
            client.wait_ready(READY_TIMEOUT)

        meta = client.meta()
        print(f"meta          default base {meta['default_base']}, "
              f"{len(meta['currencies'])} currencies")
        for source in meta["sources"]:
            print(f"              source {source['name']}: {source['edges']} edges, "
                  f"last_error={source.get('last_error') or 'none'}")

        answer = client.convert("USD", "ZAR", 100)
        rate = answer["rate"]
        print(f"convert       {answer['amount']} {answer['from']} = "
              f"{answer['result']:.2f} {answer['to']}")
        print(f"              rate {rate['rate']:.4f}, {rate['hops']} hop(s), "
              f"path {' -> '.join(rate['path'])}, sources {rate['sources']}")
        print(f"              quality {rate['quality']['grade']}, "
              f"{rate['age_sec']:.0f}s old")

        book = client.rates("ZAR")
        sample = sorted(book["rates"])[:6]
        print(f"rates         {len(book['rates'])} currencies against ZAR; "
              f"first few: {', '.join(sample)}")

        # The one deliberate difference between the two modes: an unknown base
        # answers 200 with an empty book here, where Engine.rates() raises.
        empty = client.rates("XXX")
        print(f"unknown base  HTTP 200 with {len(empty['rates'])} rates "
              f"(direct mode raises instead — the one deliberate difference)")

        # An unknown pair is a 404 with a JSON body, where direct mode raises
        # OpenRateError carrying plain text. Same failure, two shapes.
        try:
            client.convert("USD", "XXX", 1)
        except OpenRateSidecarError as exc:
            print(f"error         {exc}")

    print("\nsidecar stopped" if url is None else "\nleft the remote server alone")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except OpenRateSidecarError as exc:
        print(f"openrate: {exc}", file=sys.stderr)
        openrate.stop()
        sys.exit(1)
