"""Regression guards for the Windows observation oracle, not native evidence."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


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
    def test_window_discovery_failure_keeps_started_terminal_owned_for_cleanup(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-owner-") as directory:
            root, owned = Path(directory), []
            native = Mock(executable=root / "observer.exe")
            title = "TEST-Pi-origin-owned"
            with patch.object(W, "command") as launched, patch.object(W, "wait_for", side_effect=RuntimeError("discovery_failed")):
                with self.assertRaisesRegex(RuntimeError, "discovery_failed"):
                    W.Terminal("wt.exe", "pwsh.exe", root / "origin", title, None, [], {}, root, native, owned=owned)
            launched.assert_called_once()
            self.assertEqual(len(owned), 1)
            terminal = owned[0]
            self.assertFalse(terminal.closed)
            native.side_effect = [{"hwnd": 123, "pid": 456}, {"ok": True}]
            with patch.object(W, "wait_for"):
                terminal.close()
            native.assert_any_call("close", 123, 456, title)
            self.assertTrue(terminal.closed)

    def test_partial_launch_without_observed_window_cannot_claim_cleanup(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-pending-") as directory:
            root, owned = Path(directory), []
            native = Mock(executable=root / "observer.exe", return_value={"hwnd": 0, "pid": 0})
            with patch.object(W, "command"), patch.object(W, "wait_for", side_effect=RuntimeError("discovery_failed")):
                with self.assertRaisesRegex(RuntimeError, "discovery_failed"):
                    W.Terminal("wt.exe", "pwsh.exe", root / "origin", "TEST-Pi-origin-pending", root / "pi.exe", [], {}, root, native, owned=owned)
            self.assertEqual(len(owned), 1)
            with self.assertRaisesRegex(RuntimeError, "partial_terminal_cleanup_unverified"):
                owned[0].close()
            self.assertFalse(owned[0].closed)
            native.assert_called_once_with("window", "TEST-Pi-origin-pending")

    @unittest.skipUnless(os.name == "nt" and shutil.which("pwsh.exe"), "Actual Windows PowerShell guard only")
    def test_case_aliases_parse_before_missing_real_terminal_refuses_pi(self):
        with tempfile.TemporaryDirectory(prefix="TEST Pi launcher ") as directory:
            root = Path(directory)
            terminal_root = root / "origin"
            native = Mock(executable=root / "missing-observer.exe", return_value={"hwnd": 123, "pid": 456})
            # Exercise the actual launch-spec producer with aliases returned by
            # Windows os.environ; this never starts a Terminal or product.
            with patch.object(W, "command"), patch.object(W, "wait_for", return_value=123):
                W.Terminal("wt.exe", "pwsh.exe", terminal_root, "TEST-Pi-origin-json", None, [],
                           {"SystemRoot": "C:\\Windows", "SYSTEMROOT": "C:\\Windows"}, root, native, owned=[])
            launcher, spec, state = terminal_root / "launch.ps1", terminal_root / "launch.json", terminal_root / "launch-state.json"
            env = {key: value for key, value in os.environ.items() if key not in ("WT_SESSION", "WT_PROFILE_ID")}
            result = subprocess.run([shutil.which("pwsh.exe"), "-NoLogo", "-NoProfile", "-File", str(launcher), "-Spec", str(spec)],
                                    env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            observed = json.loads(state.read_text(encoding="utf-8-sig"))
            self.assertEqual(observed["phase"], "launcher_failed")
            self.assertIs(observed["wt_session_present"], False)
            self.assertIn("Real Windows Terminal session unavailable", observed["error"])
            self.assertNotIn("child_exit_code", observed)
            self.assertFalse((terminal_root / "process.exit").exists())


class ProcessCleanupTests(unittest.TestCase):
    def terminal(self, root, native):
        terminal = W.Terminal.__new__(W.Terminal)
        terminal.root, terminal.native, terminal.closed = root, native, False
        terminal.executable, terminal.pi_pid = str(root / "pi.exe"), 41
        terminal.pi_identity = {"creation_time": "123456789"}
        terminal.process_handle = Mock(value=234)
        terminal.window = {"hwnd": 123, "pid": 456, "title": "TEST-Pi-origin-process"}
        terminal.window_closed, terminal.exit_path = False, root / "process.exit"
        return terminal

    def test_retained_original_handle_collects_after_exit_without_pid_lookup_or_taskkill(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-cleanup-") as directory:
            root = Path(directory)
            native = Mock(return_value={"hwnd": 0})
            native.held_process.return_value = {"collected": True, "descendants_collected": 1}
            terminal = self.terminal(root, native)
            handle = terminal.process_handle
            with patch.object(W, "command") as external:
                terminal.close()
            external.assert_not_called()
            native.held_process.assert_called_once_with(handle, "collect-owned-handle", "123456789", str(root / "pi.exe"))
            native.assert_called_once_with("window", "TEST-Pi-origin-process")
            handle.close.assert_called_once()
            self.assertTrue(terminal.closed)

    def test_unknown_identity_never_falls_back_to_numeric_pid_and_remains_uncollected(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-cleanup-") as directory:
            root = Path(directory)
            native = Mock(side_effect=[{"hwnd": 123, "pid": 456}, {"ok": True}])
            native.held_process.side_effect = RuntimeError("owned_process_token_unavailable")
            terminal = self.terminal(root, native)
            with patch.object(W, "command") as external, patch.object(W, "wait_for"):
                with self.assertRaisesRegex(RuntimeError, "owned_process_token_unavailable"):
                    terminal.close()
            external.assert_not_called()
            native.assert_any_call("close", 123, 456, "TEST-Pi-origin-process")
            self.assertTrue(terminal.window_closed)
            self.assertFalse(terminal.closed)

    def test_unverified_startup_closes_only_owned_window_without_claiming_child_collection(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-cleanup-") as directory:
            native = Mock(return_value={"hwnd": 0})
            terminal = self.terminal(Path(directory), native)
            terminal.pi_identity = None
            with patch.object(W, "command") as external:
                with self.assertRaisesRegex(RuntimeError, "partial_pi_process_cleanup_unverified"):
                    terminal.close()
            external.assert_not_called()
            native.assert_called_once_with("window", "TEST-Pi-origin-process")
            self.assertTrue(terminal.window_closed)
            self.assertFalse(terminal.closed)

    def test_unverified_native_birth_or_image_cannot_become_cleanup_authority(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-cleanup-") as directory:
            root = Path(directory)
            for change in ({"present": False}, {"creation_time": ""}, {"creation_time": "0"},
                           {"pid": 42}, {"executable": str(root / "foreign.exe")}):
                native = Mock(return_value={"present": True, "pid": 41, "creation_time": "123456789",
                                            "executable": str(root / "pi.exe"), **change})
                native.held_process.return_value = native.return_value
                terminal = self.terminal(root, native)
                terminal.pi_identity = None
                handle = Mock(value=234)
                with patch.object(W, "ProcessHandle", return_value=handle):
                    with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, "owned_pi_process_identity_unverified"):
                        terminal.bind_process(41)
                self.assertIsNone(terminal.pi_identity)
                native.held_process.assert_called_once_with(handle, "process-identity-handle", str(root / "pi.exe"))

    @unittest.skipUnless(os.name == "nt" and os.environ.get("PI_WINDOWS_OBSERVER_TEST_PATH"), "Actual inherited native process handle guard only")
    def test_actual_inherited_handle_identity_and_wrong_birth_refusal(self):
        native = W.Native(Path(os.environ["PI_WINDOWS_OBSERVER_TEST_PATH"]))
        handle = W.ProcessHandle(os.getpid())
        image = str(Path(sys.executable).resolve(strict=True))
        try:
            identity = native.held_process(handle, "process-identity-handle", image)
            self.assertEqual(identity["pid"], os.getpid())
            with self.assertRaisesRegex(RuntimeError, "owned_process_birth_changed"):
                native.held_process(handle, "collect-owned-handle", str(int(identity["creation_time"]) + 1), image)
        finally:
            handle.close()


if __name__ == "__main__":
    unittest.main()
