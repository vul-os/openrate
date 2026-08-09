#!/usr/bin/env python3
"""Direct mode: openrate in this interpreter, through the C ABI.

    python3 examples/direct_convert.py            # engine only — sends no packets
    python3 examples/direct_convert.py --fetch    # also builds a refresher

No server, no port, no loopback socket. libopenrate is loaded with ctypes and
the engine lives in this process.

THE HEADLINE IS THE ENGINE, NOT THE REFRESHER, and that ordering is openrate's
whole design. An engine computes. It starts no thread, opens no socket, reads no
environment variable and sends no packet — so the default run below is a
complete, useful conversion program that provably touches nothing. Fetching is a
separate, explicit construction with its own handle, which is why `--fetch` is a
flag and not the default.

Where the library comes from: $OPENRATE_LIBRARY, else the bundled openrate/lib/,
else dist/ffi/ in an enclosing checkout. Build one with `scripts/build-ffi.sh`.
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import openrate  # noqa: E402
from openrate import Engine, OpenRateError  # noqa: E402

# Rates you obtained yourself — a fixings file, a bank statement, your own
# treasury desk. This is the zero-network path, and it has no HTTP counterpart
# because the server is read-only.
BOOK = [
    {"from": "USD", "to": "ZAR", "rate": 18.5, "source": "my-desk"},
    {"from": "EUR", "to": "USD", "rate": 1.09, "source": "my-desk"},
    {"from": "GBP", "to": "USD", "rate": 1.27, "source": "my-desk"},
]


def main(argv: list[str]) -> int:
    print(f"library:      {openrate.library_path()}")
    print(f"abi version:  {openrate.abi_version()}")
    print(f"open handles: {openrate.open_handles()}\n")

    # The context manager is the whole handle-safety story: __exit__ closes on
    # every path, including the exception raised further down. Closing an engine
    # also stops and releases every refresher built over it.
    with Engine({"base": "ZAR", "quiet": True}) as engine:
        print(f"engine:       {engine!r}")

        # --- an engine with no rates says so, rather than guessing ---------
        try:
            engine.convert("USD", "ZAR", 100)
        except OpenRateError as exc:
            print(f"empty engine: {exc}")

        # --- feed it -------------------------------------------------------
        loaded = engine.load(edges=BOOK, built_at="2026-08-09T00:00:00Z")
        print(f"load          {len(loaded['currencies'])} currencies: "
              f"{', '.join(loaded['currencies'])}")

        # --- convert -------------------------------------------------------
        answer = engine.convert("USD", "ZAR", 100)
        rate = answer["rate"]
        print(f"convert       {answer['amount']} {answer['from']} = "
              f"{answer['result']} {answer['to']}")
        print(f"              rate {rate['rate']}, {rate['hops']} hop(s), "
              f"path {' -> '.join(rate['path'])}, sources {rate['sources']}")

        # A pair nobody quoted directly is still answered, through the graph,
        # and the path says so. That auditability is the product.
        crossed = engine.convert("GBP", "EUR", 1)
        print(f"cross         1 GBP = {crossed['result']:.4f} EUR via "
              f"{' -> '.join(crossed['rate']['path'])}")

        # --- the book ------------------------------------------------------
        book = engine.rates("ZAR")
        pairs = ", ".join(f"{ccy} {view['rate']:.4f}" for ccy, view in sorted(book["rates"].items()))
        print(f"rates         1 ZAR = {pairs}")

        # --- metadata ------------------------------------------------------
        meta = engine.meta()
        print(f"meta          default base {meta['default_base']}, "
              f"{len(meta['currencies'])} currencies, sources {meta['sources']}")
        print("              ^ sources is empty: nothing is refreshing this engine")

        # --- errors are exceptions carrying the library's own message ------
        try:
            engine.convert("USD", "XXX", 1)
        except OpenRateError as exc:
            print(f"error         {exc}")

        # --- the engine/refresher split is enforced at the ABI --------------
        # An engine handle REFUSES "refresh". There is no flag that makes an
        # engine fetch; there is no code path.
        try:
            engine.call("refresh", {})
        except OpenRateError as exc:
            print(f"no fetching   {exc}")

        if "--fetch" in argv:
            # --- the other handle kind ---------------------------------------
            # A refresher is a separate construction with its own lifetime.
            # Building it still opens nothing; refresh() is what fetches.
            print("\n--fetch: building a refresher (this one does use the network)")
            with engine.refresher({"sources": "ecb", "quiet": True}) as refresher:
                print(f"refresher:    {refresher!r}")
                print(f"before        {refresher.status()['sources']}")
                try:
                    result = refresher.refresh(timeout_ms=20000)
                except OpenRateError as exc:
                    print(f"refresh       failed: {exc}")
                else:
                    for source in result["sources"]:
                        print(f"refresh       {source['name']}: {source['edges']} edges, "
                              f"last_error={source.get('last_error') or 'none'}")
                    live = engine.convert("EUR", "ZAR", 100)
                    print(f"live          100 EUR = {live['result']:.2f} ZAR via "
                          f"{' -> '.join(live['rate']['path'])}")

    # Out of the with-block: closed. The library's own handle count is the
    # check — a host test suite can assert it closed what it opened.
    print(f"\nafter close:  {engine!r}, open handles {openrate.open_handles()}")
    try:
        engine.meta()
    except OpenRateError as exc:
        print(f"use-after-close: {exc}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except OpenRateError as exc:
        print(f"openrate: {exc}", file=sys.stderr)
        sys.exit(1)
