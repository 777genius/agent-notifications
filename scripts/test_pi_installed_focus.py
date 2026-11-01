"""Synthetic negative-click oracle guards; never native desktop evidence."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


M = module("pi_macos_focus_guard", "pi-installed-desktop-macos.py")
L = module("pi_linux_focus_guard", "pi-installed-desktop-linux.py")


class Clock:
    now = 0.0

    def monotonic(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class NegativeClickFocusTests(unittest.TestCase):
    def observe(self, platform, change=None, at=3):
        clock, stopped = Clock(), []

        def current():
            wrong = change is not None and clock.now >= at
            return {"window": "wrong" if wrong and change == "window" else "decoy",
                    "tty": "/dev/ttys999" if wrong and change == "tty" else "/dev/ttys123",
                    "keyboard_pid": 999 if wrong and change == "keyboard" else 123}

        def stop_capture():
            stopped.append(clock.now)

        adapter = M if platform == "macos" else L
        fake_time = SimpleNamespace(monotonic=clock.monotonic, sleep=clock.sleep)
        with patch.object(adapter, "time", fake_time), patch.object(adapter.P, "time", fake_time):
            if platform == "macos":
                terminal = SimpleNamespace(window="decoy", tty="/dev/ttys123", front=current,
                                           is_front=lambda: current()["window"] == "decoy" and current()["tty"] == "/dev/ttys123")
                result = adapter.observe_unchanged_focus(terminal, stop_capture)
            else:
                result = adapter.observe_unchanged_focus(lambda: current()["window"], lambda: current()["keyboard_pid"],
                                                         "decoy", 123, stop_capture)
        return result, stopped

    def test_macos_rejects_delayed_wrong_window(self):
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe("macos", "window")

    def test_macos_rejects_delayed_selected_tty_change(self):
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe("macos", "tty")

    def test_linux_rejects_delayed_wrong_window(self):
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe("linux", "window")

    def test_linux_rejects_delayed_keyboard_change(self):
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe("linux", "keyboard")

    def test_full_window_preserves_each_bounded_audio_drain(self):
        for platform, drain in (("macos", 3), ("linux", 0.4)):
            with self.subTest(platform=platform):
                result, stopped = self.observe(platform)
                self.assertGreaterEqual(result["elapsed_seconds"], 15)
                self.assertGreater(result["samples"], 1)
                self.assertEqual(len(stopped), 1)
                self.assertGreaterEqual(stopped[0], drain)
                self.assertLess(stopped[0], drain + 0.1)


if __name__ == "__main__":
    unittest.main()
