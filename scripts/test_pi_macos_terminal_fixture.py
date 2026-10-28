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

    def test_activation_is_once_bound_to_observed_start_and_never_proves_grant(self):
        child = self.child(b"", delay=20)
        calls, receipt = [], {}
        helper = {"pid": 41, "process_start_identity": "987654", "finished_launching": True}

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
                         [("activate-permission-helper", "/TEST/owned.app", "41", "987654")])
        with self.assertRaises((RuntimeError, KeyError)):
            M.observed_permission(receipt["helper_activation"])

    def test_exited_request_ambiguous_or_missing_start_never_activates(self):
        exited = self.child(b"")
        exited.communicate(timeout=3)
        pending = self.child(b"", delay=20)
        states = ((exited, [{"pid": 41, "process_start_identity": "987654", "finished_launching": True}]),
                  (pending, [{"pid": 41, "finished_launching": True}]),
                  (pending, [{"pid": 41}, {"pid": 42}]))
        for child, helpers in states:
            calls = []

            def observe(*args):
                calls.append(args)
                return {"dialogs": []} if args[0] == "notification-consent" else {"installed_helpers": helpers}

            self.assertEqual(M.consent_observer(child, observe, Path("/TEST/owned.app"), {})(), [])
            self.assertFalse(any(row[0] == "activate-permission-helper" for row in calls))

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
