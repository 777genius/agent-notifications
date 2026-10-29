#!/usr/bin/env python3
"""Real child/PTY regressions for macOS Terminal and consent observations."""
import importlib.util
import json
import os
from pathlib import Path
import pty
import select
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("pi_installed_macos", REPO / "scripts/pi-installed-desktop-macos.py")
M = importlib.util.module_from_spec(spec)
spec.loader.exec_module(M)


class TerminalInput(unittest.TestCase):
    def test_actual_async_launcher_keeps_both_ttys_and_collects_child(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-pty-") as temporary:
            root = Path(temporary).resolve()
            cwd = root / "TEST project with spaces"
            cwd.mkdir()
            result = root / "native-tty.json"
            source = ('import json,os,sys; from pathlib import Path; '
                      'Path(sys.argv[1]).write_text(json.dumps({"pid":os.getpid(),'
                      '"stdin_tty":os.isatty(0),"stdout_tty":os.isatty(1),'
                      '"input_tty":os.ttyname(0) if os.isatty(0) else None,'
                      '"output_tty":os.ttyname(1),"cwd":os.getcwd()}))')
            script = M.terminal_launcher(root, [sys.executable, "-c", source, result],
                                         {"PATH": os.environ["PATH"], "HOME": str(root)}, cwd)
            master, slave = pty.openpty()
            try:
                child = subprocess.Popen(["/bin/bash", "--noprofile", "--norc", script],
                                         stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
                self.assertEqual(child.wait(timeout=5), 0)
                observed = json.loads(result.read_text())
                self.assertIs(observed["stdin_tty"], True)
                self.assertIs(observed["stdout_tty"], True)
                self.assertEqual(observed["input_tty"], observed["output_tty"])
                self.assertEqual(observed["cwd"], str(cwd))
                self.assertEqual(observed["pid"], int((root / "process.pid").read_text()))
                self.assertEqual(int((root / "shell.pid").read_text()), child.pid)
                self.assertEqual((root / "process.exit").read_text().strip(), "0")
                with self.assertRaises(ProcessLookupError):
                    os.kill(observed["pid"], 0)
            finally:
                os.close(master)
                os.close(slave)


class ConsentObservation(unittest.TestCase):
    """Real child exit/pipe/timeout observations; these never qualify desktop."""
    def child(self, stdout, stderr=b"", code=0, delay=0):
        source = ('import sys,time; '
                  'sys.stdout.buffer.write(bytes.fromhex(sys.argv[1])); sys.stdout.flush(); '
                  'sys.stderr.buffer.write(bytes.fromhex(sys.argv[2])); sys.stderr.flush(); '
                  'time.sleep(float(sys.argv[4])); sys.exit(int(sys.argv[3]))')
        child = subprocess.Popen([sys.executable, "-c", source, stdout.hex(), stderr.hex(), str(code), str(delay)],
                                 stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        def cleanup():
            if child.poll() is None:
                child.kill()
            child.communicate(timeout=3)
        self.addCleanup(cleanup)
        return child

    def receipt(self, permission, *, code=0, stderr=b""):
        body = json.dumps({"reason": "permission_" + permission, "permission": permission}).encode()
        child = self.child(body, stderr, code)
        self.assertEqual(M.wait_for_consent(child, lambda: [], seconds=3), [])
        stdout, actual_stderr = child.communicate(timeout=3)
        return M.permission_receipt(stdout, actual_stderr, child.returncode)

    def test_early_allowed_requires_fresh_native_allowed_result(self):
        allowed = self.receipt("allowed")
        self.assertEqual(M.verify_permission_grant(allowed, self.receipt("allowed")), "allowed")
        for permission in ("denied", "undetermined"):
            with self.subTest(permission=permission), self.assertRaisesRegex(RuntimeError, "permission_" + permission):
                M.verify_permission_grant(allowed, self.receipt(permission))

    def test_original_request_can_exit_after_former_observer_deadline(self):
        body = json.dumps({"reason": "permission_allowed", "permission": "allowed"}).encode()
        started = time.monotonic()
        child = self.child(body, delay=16)
        # The real child has written its result but remains pending. Observing
        # stdout alone must not finish the request before its natural exit.
        self.assertEqual(select.select([child.stdout], [], [], 3)[0], [child.stdout])
        self.assertIsNone(child.poll())
        self.assertEqual(M.wait_for_consent(child, lambda: []), [])
        out, err = child.communicate(timeout=3)
        self.assertGreaterEqual(time.monotonic() - started, 15)
        self.assertLess(time.monotonic() - started, 30)
        self.assertEqual(child.returncode, 0)
        self.assertEqual(M.observed_permission(M.permission_receipt(out, err, child.returncode)), "allowed")
        with self.assertRaises(ProcessLookupError):
            os.kill(child.pid, 0)

    def test_activation_is_once_bound_to_observed_start_and_never_proves_grant(self):
        child = self.child(b"", delay=20)
        calls, receipt = [], {}
        helper = {"pid": 41, "process_start_identity": "987654:123", "finished_launching": True}

        def observe(*args):
            calls.append(args)
            if args[0] == "notification-consent":
                return {"dialogs": []}
            if args[0] == "helper-processes":
                return {"installed_helpers": [helper]}
            return {"activation_requested": True, "after": {"active": True}}

        observed = M.consent_observer(child, observe, Path("/TEST/owned.app"), receipt)
        self.assertEqual(observed(), [])
        helper.update(pid=42, process_start_identity="different")
        self.assertEqual(observed(), [])
        self.assertEqual([row for row in calls if row[0] == "activate-permission-helper"],
                         [("activate-permission-helper", "/TEST/owned.app", "41", "987654:123")])
        with self.assertRaises((RuntimeError, KeyError)):
            M.observed_permission(receipt["helper_activation"])

    def test_exited_request_ambiguous_or_missing_start_never_activates(self):
        exited = self.child(b"")
        exited.communicate(timeout=3)
        pending = self.child(b"", delay=20)
        states = ((exited, [{"pid": 41, "process_start_identity": "987654:123", "finished_launching": True}]),
                  (pending, [{"pid": 41, "finished_launching": True}]),
                  (pending, [{"pid": 41}, {"pid": 42}]))
        for child, helpers in states:
            calls = []

            def observe(*args):
                calls.append(args)
                return {"dialogs": []} if args[0] == "notification-consent" else {"installed_helpers": helpers}

            self.assertEqual(M.consent_observer(child, observe, Path("/TEST/owned.app"), {})(), [])
            self.assertFalse(any(row[0] == "activate-permission-helper" for row in calls))

    def test_pending_capture_waits_five_seconds_and_runs_once_without_request_retry(self):
        class Request:
            def poll(self):
                return None
        clock, captures, receipt = [0.0], [], {}

        def observe(operation, *args):
            return {"dialogs": []} if operation == "notification-consent" else {"installed_helpers": []}

        def capture():
            captures.append(clock[0])
            return {"status": "captured", "desktop_qualified": False}

        observed = M.consent_observer(Request(), observe, Path("/TEST/owned.app"), receipt, capture, lambda: clock[0])
        for tick in (0.0, 4.999):
            clock[0] = tick
            self.assertEqual(observed(), [])
            self.assertEqual(captures, [])
        for tick in (5.0, 6.0, 120.0):
            clock[0] = tick
            self.assertEqual(observed(), [])
        self.assertEqual(captures, [5.0])
        self.assertFalse(receipt["pending_capture"]["desktop_qualified"])

    def test_completed_request_or_accepted_dialog_never_triggers_pending_capture(self):
        class Request:
            code = None
            def poll(self):
                return self.code
        for code, dialogs in ((0, []), (None, [{"accepted_dialog": True}])):
            clock, captures, request = [0.0], [], Request()
            request.code = code

            def observe(operation, *args):
                return {"dialogs": dialogs} if operation == "notification-consent" else {"installed_helpers": []}

            observed = M.consent_observer(request, observe, Path("/TEST/owned.app"), {},
                                          lambda: captures.append(True), lambda: clock[0])
            clock[0] = 5.0
            self.assertEqual(observed(), dialogs)
            self.assertEqual(captures, [])

    def test_pending_capture_failure_is_inconclusive_and_not_retried(self):
        class Request:
            def poll(self):
                return None
        clock, captures, receipt = [0.0], [], {}

        def observe(operation, *args):
            return {"dialogs": []} if operation == "notification-consent" else {"installed_helpers": []}

        def capture():
            captures.append(True)
            raise RuntimeError("TEST observation unavailable")

        observed = M.consent_observer(Request(), observe, Path("/TEST/owned.app"), receipt, capture, lambda: clock[0])
        clock[0] = 5.0
        self.assertEqual(observed(), [])
        self.assertEqual(observed(), [])
        self.assertEqual(captures, [True])
        self.assertEqual(receipt["pending_capture"]["status"], "inconclusive_pending_capture_failed")
        with self.assertRaises((RuntimeError, KeyError)):
            M.observed_permission(receipt["pending_capture"])

    def test_exit_during_pending_capture_retains_evidence_as_inconclusive(self):
        class Request:
            code = None
            def poll(self):
                return self.code
        request, calls = Request(), []

        def observe(operation, *args, timeout):
            calls.append((operation, timeout))
            if operation == "permission-diagnostics":
                request.code = 0
                return {"scope": "TEST native inventory"}
            return {"installed_helpers": [{"pid": 41, "process_start_identity": "TEST-birth"}]}

        def screenshot(argv, *, timeout):
            self.assertEqual((argv[:2], timeout), (["/usr/sbin/screencapture", "-x"], 3))
            Path(argv[2]).write_bytes(b"TEST screenshot bytes; not native evidence")

        with tempfile.TemporaryDirectory(prefix="TEST-pi-consent-capture-") as temporary:
            with mock.patch.object(M, "command", side_effect=screenshot) as capture:
                receipt = M.capture_pending_consent(request, observe, Path("/TEST/owned.app"), Path(temporary))
            capture.assert_called_once()
            self.assertTrue((Path(temporary) / "pending-notification-consent.png").is_file())
        self.assertEqual(calls, [("helper-processes", 3), ("permission-diagnostics", 4)])
        self.assertTrue(receipt["original_request_running_before"])
        self.assertFalse(receipt["original_request_running_after"])
        self.assertEqual(receipt["status"], "inconclusive_original_request_completed")
        self.assertIn("screenshot_sha256", receipt)
        self.assertIn("native_diagnostics", receipt)
        self.assertFalse(receipt["desktop_qualified"])

    def test_early_denied_undetermined_and_unavailable_never_prove_grant(self):
        for permission in ("denied", "undetermined", "unavailable"):
            receipt = self.receipt(permission, code=1 if permission == "unavailable" else 0)
            self.assertEqual(receipt["permission"], permission)
            with self.subTest(permission=permission), self.assertRaises(RuntimeError):
                M.verify_permission_grant(receipt, self.receipt("allowed"))

    def test_early_failure_retains_diagnostics_and_bounds_output(self):
        child = self.child(b'{"reason":"permission_unavailable","permission":"unavailable"}', b"TEST helper failure\n", 1)
        self.assertEqual(M.wait_for_consent(child, lambda: [], seconds=3), [])
        out, err = child.communicate(timeout=3)
        receipt = M.permission_receipt(out, err, child.returncode)
        self.assertEqual(receipt["exit_code"], 1)
        self.assertEqual(receipt["stderr"], "TEST helper failure\n")
        for invalid in (receipt, M.permission_receipt(b"{}", b"", 0), M.permission_receipt(b"x" * 65537, b"", 0)):
            with self.assertRaises(RuntimeError):
                M.observed_permission(invalid)
        self.assertLessEqual(len(M.permission_receipt(b"x" * 65537, b"y" * 65537, 0)["stdout"]), 32768)

    def test_timeout_keeps_running_child_distinct_and_collects_its_real_output(self):
        child = self.child(b"TEST pending\n", b"TEST waiting\n", delay=20)
        # stderr is flushed after stdout. Wait for actual child readiness before
        # measuring the observer's short timeout, without consuming either pipe.
        self.assertEqual(select.select([child.stderr], [], [], 3)[0], [child.stderr])
        started = time.monotonic()
        with self.assertRaisesRegex(RuntimeError, "consent_dialog_unavailable"):
            M.wait_for_consent(child, lambda: [], seconds=0.1)
        self.assertIsNone(child.poll())
        self.assertLess(time.monotonic() - started, 2)
        child.terminate()
        out, err = child.communicate(timeout=3)
        receipt = M.permission_receipt(out, err, child.returncode)
        self.assertLess(receipt["exit_code"], 0)
        self.assertEqual((receipt["stdout"], receipt["stderr"]), ("TEST pending\n", "TEST waiting\n"))
        with self.assertRaises(RuntimeError):
            M.observed_permission(receipt)


if __name__ == "__main__":
    unittest.main()
