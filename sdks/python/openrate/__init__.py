"""openrate — currency conversion, in your Python process or beside it.

Two modes, one wire contract. The JSON is the same in both; only the transport
differs.

DIRECT — libopenrate loaded into this interpreter with ctypes. An engine
computes and provably fetches nothing:

    from openrate import Engine

    with Engine({"base": "ZAR"}) as engine:
        engine.load(edges=[{"from": "USD", "to": "ZAR", "rate": 18.5, "source": "mine"}])
        engine.convert("USD", "ZAR", 100)["result"]        # 1850.0

    # fetching is a separate, explicit construction:
    with Engine() as engine, engine.refresher({"sources": "ecb"}) as refresher:
        refresher.refresh()                                 # THIS OPENS SOCKETS
        engine.convert("EUR", "ZAR", 100)

SIDECAR — the openrate binary as a child process on 127.0.0.1, or a server you
already run:

    import openrate

    client = openrate.Client()                              # spawns and manages one
    client.convert("USD", "ZAR", 100)["result"]

    other = openrate.Client("https://openrate.example")     # spawns nothing

Direct mode puts the Go runtime inside your interpreter and is NOT fork-safe.
Read README.md — "Which mode" and "The costs" — before choosing it.
"""

from ._direct import (
    Engine,
    OpenRateError,
    OpenRateLibraryError,
    Refresher,
    abi_version,
    library_path,
    load_library,
    open_handles,
)
from ._sidecar import (
    Client,
    OpenRateSidecarError,
    base_url,
    start,
    stop,
)

__all__ = [
    "Client",
    "Engine",
    "OpenRateError",
    "OpenRateLibraryError",
    "OpenRateSidecarError",
    "Refresher",
    "abi_version",
    "base_url",
    "library_path",
    "load_library",
    "open_handles",
    "start",
    "stop",
]

__version__ = "0.1.0"
