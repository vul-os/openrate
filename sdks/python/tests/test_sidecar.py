"""Tests for the openrate Python sidecar launcher.

Run from sdks/python:  python3 -m unittest discover -s tests

These drive `tests/fake_openrate.py`, a stdlib HTTP server that honours
OPENRATE_ADDR and serves /healthz, /readyz and the read-only API, so the whole
suite runs with no Go toolchain and no network. An integration test at the
bottom drives the real binary when OPENRATE_BINARY points at one, and skips
otherwise.

What is under test is the launcher contract, not openrate: binary resolution,
the free-port dance, the readiness poll and what its timeout says, the lazy
singleton, and that the child is gone afterwards.
"""

from __future__ import annotations

import os
import shlex
import socket
import stat
import subprocess
import sys
import tempfile
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
    resolves and spawns a real file exactly as it would the real binary.

    Assembled line by line rather than with a dedented block: a second `export`
    line lands at column 0 inside the template, which makes the common indent
    empty, which leaves `#!/bin/sh` indented and the wrapper unexecutable —
    "Exec format error", from a helper that looked fine with one variable.
    """
    lines = ["#!/bin/sh"]
    lines += [f"export {k}={shlex.quote(v)}" for k, v in (env or {}).items()]
    lines.append(f"exec {shlex.quote(sys.executable)} {shlex.quote(str(FAKE))}")
    wrapper = Path(tmpdir) / "openrate"
    wrapper.write_text("\n".join(lines) + "\n")
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
    def test_start_returns_a_ready_base_url(self):
        self.use_fake_binary()
        base = openrate.start()
        self.assertTrue(base.startswith("http://127.0.0.1:"))
        self.assertTrue(Client().healthy())
        self.assertTrue(Client().ready(), "start() returned before /readyz said ready")

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

    def test_the_rate_limiter_is_off_for_a_loopback_child(self):
        """Not because of readiness polling — /readyz is outside /api/ and the
        limiter never sees it — but because the only client is us, and a batch
        of conversions would take a 429 from our own sidecar."""
        self.use_fake_binary()
        with unittest.mock.patch.object(subprocess, "Popen", wraps=subprocess.Popen) as popen:
            openrate.start()
        self.assertEqual(popen.call_args.kwargs["env"]["OPENRATE_RATELIMIT"], "0")

        _reset_singleton()
        with unittest.mock.patch.object(subprocess, "Popen", wraps=subprocess.Popen) as popen:
            openrate.start(ratelimit=120)
        self.assertEqual(popen.call_args.kwargs["env"]["OPENRATE_RATELIMIT"], "120")


class Readiness(SidecarTestBase):
    """The endpoint that replaced a false green.

    /healthz answers the moment the listener binds, so a launcher that waited on
    it handed back a base URL whose every conversion was "unknown or unreachable
    currency pair". These pin the replacement: start() waits for /readyz, and
    when readiness never comes the error names the source that caused it.
    """

    def test_liveness_and_readiness_disagree_and_the_launcher_follows_readiness(self):
        """The exact window the old launcher shipped through.

        The child is spawned by hand here because a failed start() tears its
        child down, and the point is to observe both probes on the SAME live
        server: /healthz says yes, /readyz says no, and the wait believes the
        second one.
        """
        port = _free_port()
        base = f"http://127.0.0.1:{port}"
        env = dict(os.environ, OPENRATE_ADDR=f"127.0.0.1:{port}",
                   FAKE_OPENRATE_NEVER_READY="1")
        child = subprocess.Popen([sys.executable, str(FAKE)], env=env)
        self.addCleanup(child.wait)
        self.addCleanup(child.terminate)

        client = Client(base)
        deadline = time.time() + 5
        while not client.healthy() and time.time() < deadline:
            time.sleep(0.05)
        self.assertTrue(client.healthy(), "the fake never came up; the rest proves nothing")
        self.assertFalse(client.ready())

        with self.assertRaises(OpenRateSidecarError):
            _sidecar._wait_ready(base, timeout=0.5)

    def test_a_never_ready_server_times_out_and_leaves_no_child(self):
        self.use_fake_binary({"FAKE_OPENRATE_NEVER_READY": "1"})
        with self.assertRaises(OpenRateSidecarError) as caught:
            openrate.start(timeout=1.0)
        self.assertIn("no rates", str(caught.exception))
        self.assertIsNone(_sidecar._proc, "the failed start left a child behind")

    def test_the_timeout_carries_the_source_error_that_caused_it(self):
        self.use_fake_binary({
            "FAKE_OPENRATE_NEVER_READY": "1",
            "FAKE_OPENRATE_LAST_ERROR": "dial tcp 127.0.0.1:1: connect: connection refused",
        })
        with self.assertRaises(OpenRateSidecarError) as caught:
            openrate.start(timeout=1.0)
        message = str(caught.exception)
        self.assertIn("no source has returned a usable quote", message, "the reason is missing")
        self.assertIn("fake: dial tcp 127.0.0.1:1: connect: connection refused", message,
                      "a caller who can only see 'not ready' has to guess which source broke")

    def test_a_source_with_no_last_error_yet_degrades_to_the_reason_alone(self):
        """last_error is omitempty: a source not yet tried has no key at all,
        and printing `fake: None` would be worse than printing nothing."""
        self.use_fake_binary({"FAKE_OPENRATE_NEVER_READY": "1"})  # no LAST_ERROR
        with self.assertRaises(OpenRateSidecarError) as caught:
            openrate.start(timeout=1.0)
        message = str(caught.exception)
        self.assertIn("no source has returned a usable quote", message)
        for junk in ("fake:", "None", "null", "(", ")"):
            self.assertNotIn(junk, message, f"empty source detail leaked {junk!r}")

    def test_a_child_that_never_listens_reports_the_transport_error(self):
        self.use_fake_binary({"FAKE_OPENRATE_NEVER_LISTEN": "1"})
        with self.assertRaises(OpenRateSidecarError) as caught:
            openrate.start(timeout=1.0)
        message = str(caught.exception)
        self.assertIn("/readyz", message)
        self.assertIn("refused", message, "the connection error itself is the useful part")

    def test_readiness_does_not_poll_the_rate_limited_meta_endpoint(self):
        """/api/v1/meta was the old workaround, and it IS rate-limited. Break it
        and startup must not notice — if this fails, readiness is back on meta."""
        self.use_fake_binary({"FAKE_OPENRATE_META_BROKEN": "1"})
        openrate.start(timeout=5.0)
        self.assertTrue(Client().ready())

    def test_wait_ready_against_a_server_we_did_not_start(self):
        self.use_fake_binary()
        base = openrate.start()
        Client(base).wait_ready(timeout=5.0)   # returns, does not raise


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
        # sources="ecb", not the default four, and the reason is a real limit of
        # what readiness can promise: /readyz is "the snapshot has currencies in
        # it", so with several sources racing it flips as soon as the FIRST one
        # lands. A book that is ready can still be missing the pair a later
        # source would have supplied. With one source, ready means that source.
        openrate.start(sources="ecb")
        with Client() as client:
            self.assertTrue(client.healthy())
            # start() already waited for this, so it is a check on the real
            # /readyz contract rather than on our patience.
            self.assertTrue(client.ready())
            meta = client.meta()
            self.assertTrue(client.convert("EUR", "USD", 1)["result"] > 0,
                            "ready but unable to convert — the false green, again")
        self.assertIn("default_base", meta)


if __name__ == "__main__":
    unittest.main()
