"""Regression guards for the Windows observation oracle, not native evidence."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
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

        with patch.object(W.P, "time", SimpleNamespace(monotonic=clock.monotonic, sleep=clock.sleep)):
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


class LauncherDiagnosticsTests(unittest.TestCase):
    @unittest.skipUnless(os.name == "nt" and shutil.which("pwsh.exe"), "Actual Windows PowerShell guard only")
    def test_missing_real_terminal_records_failure_without_starting_pi(self):
        with tempfile.TemporaryDirectory(prefix="TEST Pi launcher ") as directory:
            root = Path(directory)
            launcher, spec, state = root / "launch.ps1", root / "launch.json", root / "launch-state.json"
            launcher.write_text(W.POWERSHELL_LAUNCHER)
            spec.write_text(json.dumps({"state_path": str(state), "observer": str(root / "missing-observer.exe"),
                                        "failure_image": str(root / "failure.bmp"), "executable": None,
                                        "environment": {}, "arguments": [], "cwd": str(root),
                                        "exit_path": str(root / "process.exit"), "witness_path": str(root / "witness.jsonl")}))
            env = {key: value for key, value in os.environ.items() if key not in ("WT_SESSION", "WT_PROFILE_ID")}
            result = subprocess.run([shutil.which("pwsh.exe"), "-NoLogo", "-NoProfile", "-File", str(launcher), "-Spec", str(spec)],
                                    env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            observed = json.loads(state.read_text(encoding="utf-8-sig"))
            self.assertEqual(observed["phase"], "launcher_failed")
            self.assertIs(observed["wt_session_present"], False)
            self.assertIn("Real Windows Terminal session unavailable", observed["error"])
            self.assertNotIn("child_exit_code", observed)
            self.assertFalse((root / "process.exit").exists())


if __name__ == "__main__":
    unittest.main()
