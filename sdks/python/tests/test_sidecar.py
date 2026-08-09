"""Tests for the openrate Python sidecar launcher.

Run from sdks/python:  python3 -m unittest discover -s tests

These drive `tests/fake_openrate.py`, a stdlib HTTP server that honours
OPENRATE_ADDR and serves /healthz plus the read-only API, so the whole suite
runs with no Go toolchain and no network. An integration test at the bottom
drives the real binary when OPENRATE_BINARY points at one, and skips otherwise.

What is under test is the launcher contract, not openrate: binary resolution,
the free-port dance, the health poll and its timeout, the lazy singleton, and
that the child is gone afterwards.
"""

from __future__ import annotations

import os
import socket
import stat
import subprocess
import sys
import tempfile
import textwrap
import time
import unittest
import unittest.mock
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import openrate  # noqa: E402
from openrate import Client, OpenRateSidecarError, _sidecar  # noqa: E402

FAKE = Path(__file__).with_name("fake_openrate.py")


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _make_fake_binary(tmpdir: str, env: dict[str, str] | None = None) -> str:
    """An executable shell wrapper that runs the fake fixture, so the launcher
    resolves and spawns a real file exactly as it would the real binary."""
    exports = "\n".join(f'export {k}="{v}"' for k, v in (env or {}).items())
    wrapper = Path(tmpdir) / "openrate"
    wrapper.write_text(
        textwrap.dedent(
            f"""\
            #!/bin/sh
            {exports}
            exec "{sys.executable}" "{FAKE}"
            """
        )
    )
    wrapper.chmod(wrapper.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    return str(wrapper)


def _reset_singleton() -> None:
    _sidecar._stop_locked()
    _sidecar._base = None


class SidecarTestBase(unittest.TestCase):
    def setUp(self) -> None:
        _reset_singleton()
        self._saved_env = dict(os.environ)
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.addCleanup(self._restore_env)
        self.addCleanup(_reset_singleton)

    def _restore_env(self) -> None:
        os.environ.clear()
        os.environ.update(self._saved_env)

    def use_fake_binary(self, env: dict[str, str] | None = None) -> str:
        path = _make_fake_binary(self._tmp.name, env)
        os.environ["OPENRATE_BINARY"] = path
        return path


class BinaryResolution(SidecarTestBase):
    def test_env_override_wins(self):
        path = self.use_fake_binary()
        self.assertEqual(_sidecar._binary_path(), path)

    def test_path_lookup(self):
        path = self.use_fake_binary()
        del os.environ["OPENRATE_BINARY"]
        os.environ["PATH"] = str(Path(path).parent)
        with unittest.mock.patch.object(_sidecar, "__file__", "/nowhere/_sidecar.py"):
            self.assertEqual(_sidecar._binary_path(), path)

    def test_a_missing_binary_says_what_to_do(self):
        os.environ.pop("OPENRATE_BINARY", None)
        os.environ["PATH"] = self._tmp.name  # empty
        with unittest.mock.patch.object(_sidecar, "__file__", "/nowhere/_sidecar.py"):
            with self.assertRaises(OpenRateSidecarError) as caught:
                _sidecar._binary_path()
        message = str(caught.exception)
        self.assertIn("OPENRATE_BINARY", message)
        self.assertIn("go build", message)


class StartAndStop(SidecarTestBase):
    def test_start_returns_a_healthy_base_url(self):
        self.use_fake_binary()
        base = openrate.start()
        self.assertTrue(base.startswith("http://127.0.0.1:"))
        self.assertTrue(Client().healthy())

    def test_start_is_a_lazy_singleton(self):
        self.use_fake_binary()
        first = openrate.start()
        pid = _sidecar._proc.pid
        self.assertEqual(openrate.start(), first)
        self.assertEqual(openrate.base_url(), first)
        self.assertEqual(_sidecar._proc.pid, pid, "start() spawned a second process")

    def test_an_explicit_port_is_honoured(self):
        self.use_fake_binary()
        port = _free_port()
        self.assertEqual(openrate.start(port=port), f"http://127.0.0.1:{port}")

    def test_stop_terminates_the_child_and_frees_the_port(self):
        self.use_fake_binary()
        base = openrate.start()
        proc = _sidecar._proc
        openrate.stop()
        self.assertIsNone(_sidecar._proc)
        self.assertIsNotNone(proc.poll(), "the child is still running")

        # The port is usable again, which is the observable half of "stopped".
        port = int(base.rsplit(":", 1)[1])
        deadline = time.time() + 5
        while time.time() < deadline:
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                try:
                    s.bind(("127.0.0.1", port))
                    return
                except OSError:
                    time.sleep(0.05)
        self.fail(f"port {port} was never released")

    def test_stop_is_idempotent(self):
        self.use_fake_binary()
        openrate.start()
        openrate.stop()
        openrate.stop()

    def test_a_never_healthy_server_times_out_and_leaves_no_child(self):
        self.use_fake_binary({"FAKE_OPENRATE_NEVER_HEALTHY": "1"})
        with self.assertRaises(OpenRateSidecarError) as caught:
            openrate.start(timeout=1.0)
        self.assertIn("did not become healthy", str(caught.exception))
        self.assertIsNone(_sidecar._proc, "the failed start left a child behind")

    def test_configuration_reaches_the_child(self):
        """OPENRATE_SOURCES and friends are passed as environment, the same way
        the real binary reads them."""
        self.use_fake_binary()
        with unittest.mock.patch.object(subprocess, "Popen", wraps=subprocess.Popen) as popen:
            openrate.start(base_currency="USD", sources="ecb", refresh="15m")
        env = popen.call_args.kwargs["env"]
        self.assertEqual(env["OPENRATE_BASE"], "USD")
        self.assertEqual(env["OPENRATE_SOURCES"], "ecb")
        self.assertEqual(env["OPENRATE_REFRESH"], "15m")


class ClientAgainstTheFake(SidecarTestBase):
    def setUp(self) -> None:
        super().setUp()
        self.use_fake_binary()
        self.client = Client()

    def test_convert(self):
        answer = self.client.convert("USD", "ZAR", 100)
        self.assertEqual(answer["result"], 1850)
        self.assertEqual(answer["rate"]["path"], ["USD", "ZAR"])

    def test_rates_and_meta(self):
        self.assertIn("USD", self.client.rates("ZAR")["rates"])
        self.assertEqual(self.client.meta()["default_base"], "ZAR")

    def test_an_unknown_pair_raises_with_the_body(self):
        with self.assertRaises(OpenRateSidecarError) as caught:
            self.client.convert("USD", "XXX", 1)
        self.assertIn("404", str(caught.exception))
        self.assertIn("unknown or unreachable", str(caught.exception))

    def test_context_manager_stops_the_managed_sidecar(self):
        with Client() as client:
            self.assertTrue(client.healthy())
        self.assertIsNone(_sidecar._proc)


class ClientAgainstSomebodyElsesServer(SidecarTestBase):
    def test_an_explicit_url_spawns_nothing_and_closes_nothing(self):
        self.use_fake_binary()
        base = openrate.start()          # a server we did start
        proc = _sidecar._proc

        with Client(base) as client:     # a client told to use it explicitly
            self.assertEqual(client.base_url, base)
            self.assertEqual(client.convert("USD", "ZAR", 2)["result"], 37)

        # Closing that client must not kill a server it did not start.
        self.assertIs(_sidecar._proc, proc)
        self.assertIsNone(proc.poll())


@unittest.skipUnless(
    os.environ.get("OPENRATE_BINARY_REAL"),
    "set OPENRATE_BINARY_REAL=/path/to/openrate to run the integration test",
)
class RealBinary(unittest.TestCase):
    """Drives the actual server. Opt-in: it needs a built binary, and openrate
    fetches on startup, so it needs the network too."""

    def setUp(self) -> None:
        _reset_singleton()
        self.addCleanup(_reset_singleton)
        os.environ["OPENRATE_BINARY"] = os.environ["OPENRATE_BINARY_REAL"]

    def test_health_and_meta(self):
        with Client() as client:
            self.assertTrue(client.healthy())
            meta = client.meta()
        self.assertIn("default_base", meta)


if __name__ == "__main__":
    unittest.main()
