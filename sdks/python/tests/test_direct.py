"""Tests for direct mode — libopenrate loaded into this interpreter with ctypes.

Run from sdks/python:  python3 -m unittest discover -s tests

The whole file skips when no shared library is resolvable, which is the honest
outcome on a platform nobody has built one for. Set OPENRATE_LIBRARY to force a
particular one.

None of this tests openrate's arithmetic — Go does that, against the real
engine. It tests the four things a ctypes binding gets wrong:

  1. Ownership. openrate_call returns malloc'd memory that only openrate_free
     may release; a c_char_p restype would silently leak every result.
  2. Handles on the error path. openrate_open_handles() makes this checkable
     rather than argued about, so every test here asserts the count returns to
     where it started.
  3. The engine/refresher split. It is enforced at the ABI, and the binding must
     not paper over it.
  4. Errors. A closed or invented handle must raise, not crash.

NO NETWORK. Every test uses the "load" method, which is the zero-network path.
One refresher IS constructed, because constructing one is precisely the thing
that still opens nothing — but nothing here calls refresh().
"""

from __future__ import annotations

import ctypes
import sys
import unittest
import unittest.mock
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import openrate  # noqa: E402
from openrate import Engine, OpenRateError, OpenRateLibraryError, Refresher  # noqa: E402
from openrate import _direct  # noqa: E402

BOOK = [
    {"from": "USD", "to": "ZAR", "rate": 18.5, "source": "test"},
    {"from": "EUR", "to": "USD", "rate": 1.09, "source": "test"},
]


def _library_or_skip() -> str:
    try:
        return openrate.library_path()
    except OpenRateLibraryError as exc:
        raise unittest.SkipTest(f"no libopenrate on this machine: {exc}") from exc


class DirectTestBase(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.library = _library_or_skip()
        cls.lib = openrate.load_library(cls.library)

    def setUp(self) -> None:
        # Every test must leave the registry as it found it. This is the leak
        # check, and it is the library's own count, not the binding's opinion.
        #
        # Registered as a cleanup rather than in tearDown, and registered FIRST:
        # cleanups run last-in-first-out and after tearDown, so this way it runs
        # after every engine this test registered for closing. In tearDown it
        # would fire before them and fail on tests that are perfectly correct.
        self._handles_before = openrate.open_handles(self.lib)
        self.addCleanup(self._assert_no_leak)

    def _assert_no_leak(self) -> None:
        self.assertEqual(
            openrate.open_handles(self.lib),
            self._handles_before,
            "the test leaked a handle",
        )

    def engine(self, config=None) -> Engine:
        eng = Engine(config or {"base": "ZAR", "quiet": True}, library=self.lib)
        self.addCleanup(eng.close)
        return eng

    def loaded_engine(self) -> Engine:
        eng = self.engine()
        eng.load(edges=BOOK, built_at="2026-08-09T00:00:00Z")
        return eng


class LibraryResolution(unittest.TestCase):
    def test_missing_library_names_where_it_looked(self):
        with unittest.mock.patch.dict("os.environ", {"OPENRATE_LIBRARY": ""}, clear=False):
            with unittest.mock.patch.object(_direct, "_candidates", return_value=[]):
                with unittest.mock.patch.object(
                    _direct.ctypes, "CDLL", side_effect=OSError("nope")
                ):
                    with self.assertRaises(OpenRateLibraryError) as caught:
                        openrate.library_path()
        message = str(caught.exception)
        self.assertIn("OPENRATE_LIBRARY", message)
        self.assertIn("Looked at:", message)

    def test_explicit_path_wins(self):
        self.assertEqual(openrate.library_path("/nowhere/libopenrate.dylib"),
                         "/nowhere/libopenrate.dylib")


class Version(DirectTestBase):
    def test_abi_version_is_the_repo_version(self):
        version_file = Path(__file__).resolve().parents[3] / "VERSION"
        got = openrate.abi_version(self.lib)
        if version_file.exists():
            self.assertEqual(got, version_file.read_text().strip())
        else:  # installed package, no checkout
            self.assertRegex(got, r"^\d+\.\d+\.\d+")

    def test_require_version_rejects_a_stale_library(self):
        with self.assertRaises(OpenRateLibraryError) as caught:
            openrate.load_library(self.library, require_version="0.0.0-not-a-real-version")
        self.assertIn("stale library", str(caught.exception))


class Lifecycle(DirectTestBase):
    def test_context_manager_closes(self):
        with Engine(library=self.lib) as engine:
            self.assertFalse(engine.closed)
        self.assertTrue(engine.closed)

    def test_context_manager_closes_on_the_error_path(self):
        """The reason the context manager exists at all."""
        with self.assertRaises(ZeroDivisionError):
            with Engine(library=self.lib) as engine:
                1 / 0
        self.assertTrue(engine.closed)

    def test_close_is_idempotent(self):
        engine = Engine(library=self.lib)
        engine.close()
        engine.close()
        self.assertTrue(engine.closed)

    def test_use_after_close_is_a_clean_error(self):
        engine = Engine(library=self.lib)
        engine.close()
        with self.assertRaises(OpenRateError):
            engine.meta()

    def test_an_invented_handle_is_an_error_not_a_segfault(self):
        engine = self.engine()
        real = engine.handle
        engine._handle = 999999  # a handle that was never issued
        try:
            with self.assertRaises(OpenRateError) as caught:
                engine.meta()
            self.assertIn("999999", str(caught.exception))
        finally:
            # Put the real handle back, or the cleanup would close the invented
            # one and leak this engine — which is exactly what tearDown caught
            # the first time this test was written.
            engine._handle = real

    def test_closing_the_engine_releases_its_refreshers(self):
        """The header promises it, so the binding must not need the right order."""
        engine = Engine(library=self.lib)
        refresher = engine.refresher({"sources": "ecb", "quiet": True})
        self.assertEqual(openrate.open_handles(self.lib), self._handles_before + 2)
        engine.close()  # NOT refresher.close() first
        self.assertEqual(openrate.open_handles(self.lib), self._handles_before)
        refresher.close()  # must be a harmless no-op, not a double free
        self.assertEqual(openrate.open_handles(self.lib), self._handles_before)


class EngineMethods(DirectTestBase):
    def test_an_empty_engine_knows_nothing(self):
        """The claim on the tin: an engine with no rates says so, and does not
        invent one."""
        with self.engine() as engine:
            self.assertEqual(engine.meta()["currencies"], [])
            with self.assertRaises(OpenRateError) as caught:
                engine.convert("USD", "ZAR", 100)
        self.assertIn("unknown or unreachable", str(caught.exception))

    def test_load_then_convert(self):
        with self.loaded_engine() as engine:
            answer = engine.convert("USD", "ZAR", 100)
        self.assertEqual(answer["result"], 1850)
        self.assertEqual(answer["rate"]["path"], ["USD", "ZAR"])

    def test_a_crossed_pair_reports_its_path(self):
        with self.loaded_engine() as engine:
            answer = engine.convert("EUR", "ZAR", 1)
        self.assertEqual(answer["rate"]["path"], ["EUR", "USD", "ZAR"])
        self.assertEqual(answer["rate"]["hops"], 2)

    def test_rates_and_meta(self):
        with self.loaded_engine() as engine:
            book = engine.rates("ZAR")
            meta = engine.meta()
        self.assertEqual(book["base"], "ZAR")
        self.assertIn("USD", book["rates"])
        self.assertEqual(meta["default_base"], "ZAR")
        self.assertEqual(meta["sources"], [], "an engine nobody refreshes has no sources")

    def test_an_unknown_base_is_an_error_here(self):
        """The HTTP endpoint refuses the same input with a 404 since 0.1.6; it
        used to answer 200 with an empty book. Direct mode follows the Go
        library and always has."""
        with self.loaded_engine() as engine:
            with self.assertRaises(OpenRateError):
                engine.rates("XXX")

    def test_an_engine_refuses_to_fetch(self):
        """The engine/refresher split is enforced at the ABI, not by convention."""
        with self.engine() as engine:
            for method in ("refresh", "start", "status", "ready"):
                with self.assertRaises(OpenRateError, msg=method) as caught:
                    engine.call(method, {})
                self.assertIn("unknown engine method", str(caught.exception))

    def test_unknown_method(self):
        with self.engine() as engine:
            with self.assertRaises(OpenRateError):
                engine.call("no-such-method", {})

    def test_results_are_freed_not_leaked(self):
        """The ownership rule, asserted rather than trusted.

        POINTER(c_char) + an explicit openrate_free is the only correct shape; a
        c_char_p restype loses the pointer and leaks. This counts the frees.
        """
        freed: list[int] = []
        real_free = self.lib.openrate_free

        def counting_free(ptr):
            if ptr:
                freed.append(ctypes.cast(ptr, ctypes.c_void_p).value or 0)
            return real_free(ptr)

        with unittest.mock.patch.object(self.lib, "openrate_free", counting_free):
            engine = Engine(library=self.lib)
            try:
                engine.load(edges=BOOK)
                engine.meta()
                with self.assertRaises(OpenRateError):
                    engine.call("no-such-method", {})  # the error string is freed too
            finally:
                engine.close()

        self.assertEqual(len(freed), 3, f"freed {len(freed)} allocations, expected 3")
        self.assertEqual(len(set(freed)), len(freed), "the same pointer was freed twice")


class RefresherConstruction(DirectTestBase):
    """Constructing a refresher opens nothing. Nothing here calls refresh()."""

    def test_a_refresher_has_its_own_handle(self):
        with self.engine() as engine:
            with engine.refresher({"sources": "ecb", "quiet": True}) as refresher:
                self.assertIsInstance(refresher, Refresher)
                self.assertNotEqual(refresher.handle, engine.handle)

    def test_status_before_any_fetch(self):
        with self.engine() as engine, engine.refresher({"sources": "ecb", "quiet": True}) as r:
            status = r.status()
        self.assertEqual([s["name"] for s in status["sources"]], ["ecb"])
        self.assertEqual(status["sources"][0]["edges"], 0)

    def test_a_refresher_shows_up_in_the_engines_meta(self):
        with self.engine() as engine, engine.refresher({"sources": "ecb", "quiet": True}):
            names = [s["name"] for s in engine.meta()["sources"]]
        self.assertEqual(names, ["ecb"])

    def test_a_refresher_refuses_engine_methods(self):
        with self.engine() as engine, engine.refresher({"sources": "ecb", "quiet": True}) as r:
            with self.assertRaises(OpenRateError):
                r.call("convert", {"from": "USD", "to": "ZAR"})

    def test_cannot_build_one_over_a_closed_engine(self):
        engine = Engine(library=self.lib)
        engine.close()
        with self.assertRaises(OpenRateError):
            engine.refresher()


if __name__ == "__main__":
    unittest.main()
