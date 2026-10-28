"""Regression guards for the Windows observation oracle, not native evidence."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch


PATH = Path(__file__).with_name("pi-installed-desktop-windows.py")
SPEC = importlib.util.spec_from_file_location("pi_windows_desktop", PATH)
W = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(W)


class Clock:
    now = 0.0

    def monotonic(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class CaptureStop:
    def __init__(self, clock):
        self.clock, self.at = clock, None

    def exists(self):
        return self.at is not None

    def write_text(self, value):
        assert value == "stop\n" and self.at is None
        self.at = self.clock.now


class PostClickOracleTests(unittest.TestCase):
    def observe(self, wrong_after=None, keyboard_only=False):
        clock, window = Clock(), {"hwnd": 123}
        stop = CaptureStop(clock)

        def native(command):
            self.assertEqual(command, "foreground")
            wrong = wrong_after is not None and clock.now >= wrong_after
            return {"hwnd": 456 if wrong and not keyboard_only else 123,
                    "keyboard_hwnd": 456 if wrong else 123}

        with patch.object(W, "time", SimpleNamespace(monotonic=clock.monotonic, sleep=clock.sleep)):
            result = W.observe_unchanged_focus(native, window, stop)
        return result, stop

    def test_delayed_wrong_focus_is_not_admitted_by_initial_decoy(self):
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe(wrong_after=3)

    def test_delayed_keyboard_focus_change_also_fails(self):
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe(wrong_after=3, keyboard_only=True)

    def test_full_budget_is_observed_without_extending_audio_capture(self):
        result, stop = self.observe()
        self.assertGreaterEqual(result["elapsed_seconds"], 15)
        self.assertGreater(result["samples"], 1)
        self.assertEqual(result["foreground_hwnd"], 123)
        self.assertEqual(result["keyboard_hwnd"], 123)
        self.assertGreaterEqual(stop.at, 0.5)
        self.assertLess(stop.at, 0.6)


if __name__ == "__main__":
    unittest.main()
