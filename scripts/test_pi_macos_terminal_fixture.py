#!/usr/bin/env python3
"""Real child/PTY regressions for macOS Terminal and consent observations."""
import importlib.util
import json
import os
from pathlib import Path
import plistlib
import pty
import select
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock
import zipfile

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("pi_installed_macos", REPO / "scripts/pi-installed-desktop-macos.py")
M = importlib.util.module_from_spec(spec)
spec.loader.exec_module(M)


class ProductBannerObservation(unittest.TestCase):
    def test_polls_share_one_absolute_deadline(self):
        clock, timeouts = [100.0], []

        def observe(operation, body, *, timeout):
            self.assertEqual((operation, body), ("banner", "Pi completed a task"))
            timeouts.append(timeout)
            clock[0] += min(10, timeout)
            return {"matches": []}

        def pause(seconds):
            clock[0] += seconds

        with mock.patch.object(M.time, "monotonic", side_effect=lambda: clock[0]), \
                mock.patch.object(M.time, "sleep", side_effect=pause):
            with self.assertRaisesRegex(RuntimeError, "actual_product_banner_not_observed"):
                M.wait_for_product_banner(observe, "Pi completed a task", 125)
        self.assertEqual(len(timeouts), 3)
        self.assertEqual(timeouts[0], 20)
        self.assertAlmostEqual(timeouts[-1], 4.9)
        self.assertEqual(clock[0], 125)

    def test_only_observations_before_original_deadline_qualify(self):
        for finished, allowed in ((124.9, True), (125.0, False), (126.0, False)):
            with self.subTest(finished=finished):
                clock = [100.0]

                def observe(*args, **kwargs):
                    clock[0] = finished
                    return {"matches": [{"observed": True}]}

                with mock.patch.object(M.time, "monotonic", side_effect=lambda: clock[0]):
                    if allowed:
                        self.assertEqual(M.wait_for_product_banner(observe, "Pi completed a task", 125),
                                         [{"observed": True}])
                    else:
                        with self.assertRaisesRegex(RuntimeError, "actual_product_banner_not_observed"):
                            M.wait_for_product_banner(observe, "Pi completed a task", 125)


class PartialCaseEvidence(unittest.TestCase):
    def capture(self, root, observe):
        provider = mock.Mock(requests=[{"prompt": "TEST-secret"}])
        sink = mock.Mock(rows=[{"status": "question"},
            {"status": "task_complete", "request_id": "TEST-secret", "payload": "TEST-secret"}],
            failures=["previous failure", "TEST-secret"])
        return M.partial_case_failure(root, provider, sink, (1, 1), "task_complete", observe,
                                      Path("/TEST/ClaudeNotifier.app"), 41)

    def test_runtime_reuses_original_bounded_observations_without_more_reads_or_probes(self):
        provider = mock.Mock(requests=[{"prompt": "TEST-secret"}])
        sink = mock.Mock(rows=[{"status": "task_complete", "request_id": "TEST-secret"}], failures=[])
        observed = {"witness": [{"type": "factory", "pid": 41},
                                 {"type": "agent_before_settle", "outcome": "completed"}],
                    "installed_helper_processes": {"installed_helpers": [{"active": False,
                        "finished_launching": True, "process_start_identity": "123:456"}]}}
        observe = mock.Mock(side_effect=AssertionError("duplicate native probe"))
        with mock.patch.object(Path, "open", side_effect=AssertionError("duplicate witness read")):
            receipt = M.partial_case_failure(Path("/TEST/case-completion"), provider, sink, (0, 0),
                "task_complete", observe, Path("/TEST/ClaudeNotifier.app"), 41, observations=observed)
        observe.assert_not_called()
        self.assertTrue(receipt["witness"]["factory_matches_origin"])
        self.assertEqual(receipt["witness"]["boundary_outcome_counts"]["completed"], 1)
        self.assertEqual(receipt["native_helpers"]["count"], 1)
        self.assertEqual(receipt["sink_expected_status_count"], 1)
        self.assertNotIn("TEST-secret", json.dumps(receipt))

    def test_fixed_counts_exclude_unknown_witness_and_sink_content(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            rows = [{"type": "factory", "pid": 41}, {"type": "agent_before_settle", "outcome": "completed"},
                    {"type": "agent_settled", "mode": "tui", "hasUI": True, "idle": True, "aborted": False},
                    {"type": "TEST-secret", "mode": "TEST-secret", "outcome": "TEST-secret", "body": "TEST-secret"}]
            (root / "witness.jsonl").write_text("\n".join(map(json.dumps, rows)))
            observe = mock.Mock(return_value={"installed_helpers": [{"active": False, "finished_launching": True,
                "process_start_identity": "123:456", "extra": "TEST-secret"}]})
            receipt = self.capture(root, observe)
        self.assertEqual(receipt["provider_request_count"], 1)
        self.assertEqual((receipt["sink_row_count"], receipt["sink_rows_sampled"], receipt["sink_failure_count"]), (1, 1, 1))
        self.assertEqual((receipt["sink_expected_status_count"], receipt["sink_request_id_present_count"]), (1, 1))
        witness = receipt["witness"]
        self.assertTrue(witness["factory_matches_origin"])
        self.assertEqual((witness["event_counts"]["agent_settled"], witness["unknown_event_count"]), (1, 1))
        self.assertEqual(witness["boundary_outcome_counts"], {"completed": 1, "aborted": 0, "error": 0})
        self.assertEqual(receipt["native_helpers"], {"count": 1, "active_count": 0,
                         "finished_launching_count": 1, "kernel_birth_observed_count": 1})
        self.assertNotIn("TEST-secret", json.dumps(receipt))
        self.assertFalse(receipt["desktop_qualified"])
        observe.assert_called_once_with("helper-processes", "/TEST/ClaudeNotifier.app", timeout=3)

    def test_witness_limits_are_checked_before_json_parsing(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for body in (b"x" * 65537, b"{}\n" * 129):
                with self.subTest(size=len(body)):
                    (root / "witness.jsonl").write_bytes(body)
                    with mock.patch.object(M.json, "loads", side_effect=AssertionError("must not parse")) as parse:
                        receipt = self.capture(root, mock.Mock(return_value={"installed_helpers": []}))
                    parse.assert_not_called()
                    self.assertEqual(receipt["witness_error_class"], "RuntimeError")
                    self.assertEqual(receipt["native_helpers"]["count"], 0)

    def test_missing_and_malformed_witness_and_native_errors_remain_bounded(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            receipt = self.capture(root, mock.Mock(return_value={"installed_helpers": []}))
            self.assertFalse(receipt["witness"]["file_present"])
            self.assertEqual(receipt["witness"]["row_count"], 0)
            (root / "witness.jsonl").write_text("TEST-secret invalid JSON")
            receipt = self.capture(root, mock.Mock(side_effect=RuntimeError("TEST-secret native failure")))
        self.assertEqual(receipt["witness_error_class"], "JSONDecodeError")
        self.assertEqual(receipt["native_helpers_error_class"], "RuntimeError")
        self.assertNotIn("TEST-secret", json.dumps(receipt))


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


class TerminalOwnership(unittest.TestCase):
    def identity(self):
        return {"status": "observed", "pid": 41, "parent_pid": 40,
                "process_start_identity": "123:456", "executable_path": str(Path("/bin/sleep").resolve())}

    def launch(self, root, observer, owned):
        (root / "process.pid").write_text("41\n")
        (root / "shell.pid").write_text("40\n")
        return M.Terminal(root, ["/bin/sleep", "20"], {}, observer, root, owned=owned)

    def test_failed_child_discovery_retains_owner_and_exact_window_cleanup_without_claiming_collection(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-owner-") as temporary:
            root, owned = Path(temporary), []

            def launch(_):
                self.assertEqual(len(owned), 1)
                return "7|/dev/ttysTEST"

            with mock.patch.object(M, "applescript", side_effect=launch), \
                    mock.patch.object(M, "wait_for", side_effect=RuntimeError("native_pi_pid_missing")):
                with self.assertRaisesRegex(RuntimeError, "native_pi_pid_missing"):
                    M.Terminal(root, ["/bin/sleep", "20"], {}, mock.Mock(), root, owned=owned)
            owner = owned[0]
            self.assertFalse(owner.collected())
            with mock.patch.object(M, "applescript", return_value="closed") as cleanup:
                with self.assertRaisesRegex(RuntimeError, "partial_terminal_cleanup_unverified"):
                    owner.close()
            self.assertIn("window id 7", cleanup.call_args.args[0])
            self.assertIn('"/dev/ttysTEST"', cleanup.call_args.args[0])
            self.assertTrue(owner.window_closed)
            self.assertFalse(owner.closed)
            self.assertFalse(owner.collected())

    def test_failed_launch_response_keeps_unresolved_owner_uncollected(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-owner-") as temporary:
            owned = []
            with mock.patch.object(M, "applescript", side_effect=RuntimeError("TEST response unavailable")) as native:
                with self.assertRaisesRegex(RuntimeError, "TEST response unavailable"):
                    M.Terminal(Path(temporary), ["/bin/sleep", "20"], {}, mock.Mock(), owned=owned)
                with self.assertRaisesRegex(RuntimeError, "partial_terminal_cleanup_unverified"):
                    owned[0].close()
            self.assertEqual(native.call_count, 1)
            self.assertFalse(owned[0].collected())

    def test_initial_birth_path_and_launcher_parent_must_be_verified_before_cleanup_can_signal(self):
        changes = ({"status": "absent"}, {"parent_pid": 42}, {"executable_path": "/TEST/foreign"},
                   {"process_start_identity": ""})
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-owner-") as temporary:
                owned = []
                observer = mock.Mock(return_value={**self.identity(), **change})
                with mock.patch.object(M, "applescript", side_effect=["7|/dev/ttysTEST", "closed"]):
                    with self.assertRaisesRegex(RuntimeError, "owned_terminal_process_identity_unverified"):
                        self.launch(Path(temporary), observer, owned)
                    with self.assertRaisesRegex(RuntimeError, "partial_terminal_cleanup_unverified"):
                        owned[0].close()
                self.assertFalse(any(call.args[0] == "signal-owned-terminal-process" for call in observer.call_args_list))
                self.assertFalse(owned[0].collected())

    def test_reused_pid_is_collected_without_signalling_replacement(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-owner-") as temporary:
            root, owned = Path(temporary), []
            observer = mock.Mock(side_effect=[self.identity(), {"status": "replaced", "pid": 41}])
            with mock.patch.object(M, "applescript", side_effect=["7|/dev/ttysTEST", "closed"]):
                terminal = self.launch(root, observer, owned)
                terminal.exit_path.write_text("0\n")
                terminal.close()
            self.assertTrue(terminal.closed)
            self.assertTrue(terminal.collected())
            self.assertEqual([call.args for call in observer.call_args_list],
                             [("terminal-process-identity", "41"), ("terminal-process-identity", "41", "123:456")])

    def test_signal_identity_failure_closes_only_owned_window_and_never_escalates(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-owner-") as temporary:
            owned = []
            observer = mock.Mock(side_effect=[self.identity(), RuntimeError("owned_terminal_process_identity_mismatch")])
            with mock.patch.object(M, "applescript", side_effect=["7|/dev/ttysTEST", "closed"]):
                terminal = self.launch(Path(temporary), observer, owned)
                with self.assertRaisesRegex(RuntimeError, "owned_terminal_process_identity_mismatch"):
                    terminal.close()
            self.assertTrue(terminal.window_closed)
            self.assertFalse(terminal.closed)
            signals = [call.args for call in observer.call_args_list if call.args[0] == "signal-owned-terminal-process"]
            self.assertEqual(signals, [("signal-owned-terminal-process", "41", "123:456", self.identity()["executable_path"], "TERM")])
            self.assertFalse(terminal.collected())


class TerminalAutomation(unittest.TestCase):
    def actors(self, child):
        return {"admitted": True, "requestPID": child.pid, "requestStart": "TEST-original-birth",
                "pythonPID": os.getpid(), "requesterPID": 123, "requesterStart": "TEST-requester-birth",
                "requesterPath": "/TEST/hosted-compute-agent", "requesterUID": os.getuid(), "chain": []}

    def waiting_child(self, kwargs, release=None):
        code = ('import sys,time; from pathlib import Path; '
                'release=Path(sys.argv[1]); '
                'deadline=time.monotonic()+5; '
                '\nwhile not release.exists() and time.monotonic()<deadline: time.sleep(.01)\n'
                'print("7|/dev/ttysTEST")') if release else 'import time; time.sleep(5)'
        return subprocess.Popen([sys.executable, "-c", code, str(release or "")], **kwargs)

    def test_one_original_launch_survives_exact_consent_and_returns_original_window_tty(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-automation-") as temporary:
            release, children, calls, receipt = Path(temporary) / "release", [], [], {}
            clock = [0.0]

            def spawn(argv, **kwargs):
                self.assertEqual(argv, ["/usr/bin/osascript", "-e", "TEST original do-script"])
                self.assertTrue(kwargs["start_new_session"])
                children.append(self.waiting_child(kwargs, release))
                return children[-1]

            def observe(operation, *args, **kwargs):
                calls.append(operation)
                child = children[0]
                if operation == "terminal-automation-actors":
                    self.assertEqual(args, (str(child.pid), str(os.getpid())))
                    return self.actors(child)
                context = json.loads(args[0])
                self.assertEqual(context["deadlineMonotonic"], 20)
                self.assertEqual(context["requestPID"], child.pid)
                if operation == "terminal-automation-consent":
                    clock[0] = 2
                    return {"dialogs": [{"TEST-original-OS-dialog": True}]}
                self.assertEqual(operation, "allow-terminal-automation")
                self.assertTrue(context["officialHostAdmitted"])
                release.touch()
                return {**self.actors(child), "physical_allow_click": True, "topmost_allow_item_verified": True,
                        "original_request_verified": True, "denial_button_observed": True}

            admit = mock.Mock(return_value={"TEST-official-host": True})
            value = M.terminal_applescript("TEST original do-script", observe, receipt, admit,
                                           now=lambda: clock[0], spawn=spawn)
            self.assertEqual(value, "7|/dev/ttysTEST")
            self.assertEqual(len(children), 1)
            self.assertEqual(calls.count("allow-terminal-automation"), 1)
            admit.assert_called_once()
            self.assertTrue(receipt["owned_child_collected"])
            self.assertEqual(receipt["original_deadline_monotonic"], 20)
            self.assertEqual(receipt["exit_code"], 0)
            with self.assertRaises(ProcessLookupError):
                os.kill(children[0].pid, 0)

    def test_completed_original_launch_never_requests_automation_or_relaunches(self):
        children, receipt = [], {}
        def spawn(argv, **kwargs):
            child = subprocess.Popen([sys.executable, "-c", 'print("8|/dev/ttysDONE")'], **kwargs)
            child.wait(timeout=3)
            children.append(child)
            return child
        observer, admit = mock.Mock(), mock.Mock()
        self.assertEqual(M.terminal_applescript("TEST original", observer, receipt, admit, spawn=spawn), "8|/dev/ttysDONE")
        self.assertEqual(len(children), 1)
        observer.assert_not_called()
        admit.assert_not_called()
        self.assertTrue(receipt["owned_child_collected"])

    def test_original_exit_during_readonly_consent_collects_only_its_stdout_within_deadline(self):
        for outcome in ("identity_disappeared", "stale_dialog", "expired_identity_disappeared"):
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-exit-") as temporary:
                release, children, calls, receipt, clock = Path(temporary) / "release", [], [], {}, [0.0]
                def spawn(argv, **kwargs):
                    children.append(self.waiting_child(kwargs, release))
                    return children[-1]
                def observe(operation, *args, **kwargs):
                    calls.append(operation)
                    if operation == "terminal-automation-actors":
                        return self.actors(children[0])
                    self.assertEqual(operation, "terminal-automation-consent")
                    self.assertEqual(json.loads(args[0])["deadlineMonotonic"], 20)
                    release.touch()
                    children[0].wait(timeout=3)
                    if outcome == "stale_dialog":
                        return {"dialogs": [{"TEST-ended-request-dialog": True}]}
                    if outcome.startswith("expired"):
                        clock[0] = 21
                    raise RuntimeError("TEST terminal_process_identity_unverified")
                admit = mock.Mock()
                if outcome.startswith("expired"):
                    with self.assertRaisesRegex(RuntimeError, "original_terminal_applescript_deadline_expired"):
                        M.terminal_applescript("TEST original", observe, receipt, admit, now=lambda: clock[0], spawn=spawn)
                else:
                    self.assertEqual(M.terminal_applescript("TEST original", observe, receipt, admit,
                                                           now=lambda: clock[0], spawn=spawn), "7|/dev/ttysTEST")
                    self.assertEqual(receipt["exit_code"], 0)
                self.assertEqual(len(children), 1)
                self.assertEqual(calls, ["terminal-automation-actors", "terminal-automation-consent"])
                self.assertFalse(receipt["physical_allow_click"])
                self.assertTrue(receipt["owned_child_collected"])
                admit.assert_not_called()
                if outcome == "stale_dialog":
                    self.assertTrue(receipt["completed_during_consent_observation"])
                else:
                    self.assertIn("terminal_process_identity_unverified", receipt["completed_request_observation_error"])

    def test_pending_original_readonly_consent_error_still_fails_and_collects_child(self):
        children, calls, receipt = [], [], {}
        def spawn(argv, **kwargs):
            children.append(self.waiting_child(kwargs))
            return children[-1]
        def observe(operation, *args, **kwargs):
            calls.append(operation)
            if operation == "terminal-automation-actors":
                return self.actors(children[0])
            self.assertEqual(operation, "terminal-automation-consent")
            self.assertIsNone(children[0].poll())
            raise RuntimeError("TEST pending original observation failed")
        admit = mock.Mock()
        with self.assertRaisesRegex(RuntimeError, "TEST pending original observation failed"):
            M.terminal_applescript("TEST original", observe, receipt, admit, spawn=spawn)
        self.assertEqual(len(children), 1)
        self.assertEqual(calls, ["terminal-automation-actors", "terminal-automation-consent"])
        self.assertTrue(receipt["owned_child_collected"])
        self.assertFalse(receipt["physical_allow_click"])
        admit.assert_not_called()

    def test_observer_handoff_cannot_extend_original_twenty_second_deadline(self):
        children, receipt, clock = [], {}, [0.0]
        def spawn(argv, **kwargs):
            children.append(self.waiting_child(kwargs))
            return children[-1]
        def observe(operation, *args, **kwargs):
            self.assertEqual(operation, "terminal-automation-actors")
            clock[0] = 21
            return self.actors(children[0])
        admit = mock.Mock()
        with self.assertRaisesRegex(RuntimeError, "original_terminal_applescript_deadline_expired"):
            M.terminal_applescript("TEST original", observe, receipt, admit, now=lambda: clock[0], spawn=spawn)
        self.assertEqual(len(children), 1)
        self.assertTrue(receipt["owned_child_collected"])
        admit.assert_not_called()

    def test_natural_exit_during_consent_observation_collects_only_the_original_actual_result(self):
        for exit_code in (0, 1):
            with self.subTest(exit_code=exit_code), tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-exit-") as temporary:
                release, children, calls, receipt = Path(temporary) / "release", [], [], {}
                def spawn(argv, **kwargs):
                    code = ('import sys,time; from pathlib import Path; release=Path(sys.argv[1]); '
                            'deadline=time.monotonic()+5\n'
                            'while not release.exists() and time.monotonic()<deadline: time.sleep(.01)\n'
                            'print("7|/dev/ttysTEST"); sys.exit(int(sys.argv[2]))')
                    children.append(subprocess.Popen([sys.executable, "-c", code, str(release), str(exit_code)], **kwargs))
                    return children[-1]
                def observe(operation, *args, **kwargs):
                    calls.append(operation)
                    if operation == "terminal-automation-actors":
                        return self.actors(children[0])
                    self.assertEqual(operation, "terminal-automation-consent")
                    release.touch()
                    children[0].wait(timeout=3)
                    raise RuntimeError("TEST original exited during native observation")
                admit = mock.Mock()
                if exit_code == 0:
                    self.assertEqual(M.terminal_applescript("TEST original", observe, receipt, admit, spawn=spawn),
                                     "7|/dev/ttysTEST")
                else:
                    with self.assertRaisesRegex(RuntimeError, "original_terminal_applescript_failed"):
                        M.terminal_applescript("TEST original", observe, receipt, admit, spawn=spawn)
                self.assertEqual(len(children), 1)
                self.assertEqual(calls, ["terminal-automation-actors", "terminal-automation-consent"])
                self.assertEqual(receipt["exit_code"], exit_code)
                self.assertTrue(receipt["owned_child_collected"])
                self.assertFalse(receipt["physical_allow_click"])
                admit.assert_not_called()

    def test_consent_observer_error_while_original_is_pending_remains_primary(self):
        children, receipt = [], {}
        def spawn(argv, **kwargs):
            children.append(self.waiting_child(kwargs))
            return children[-1]
        def observe(operation, *args, **kwargs):
            if operation == "terminal-automation-actors":
                return self.actors(children[0])
            self.assertEqual(operation, "terminal-automation-consent")
            self.assertIsNone(children[0].poll())
            raise RuntimeError("TEST live original observer failed")
        admit = mock.Mock()
        with self.assertRaisesRegex(RuntimeError, "TEST live original observer failed"):
            M.terminal_applescript("TEST original", observe, receipt, admit, spawn=spawn)
        self.assertEqual(len(children), 1)
        self.assertTrue(receipt["owned_child_collected"])
        self.assertFalse(receipt["physical_allow_click"])
        admit.assert_not_called()

    def test_observer_primary_error_is_preserved_while_original_child_is_collected(self):
        children, receipt = [], {}
        def spawn(argv, **kwargs):
            children.append(self.waiting_child(kwargs))
            return children[-1]
        observer = mock.Mock(side_effect=RuntimeError("TEST original observer failure"))
        with self.assertRaisesRegex(RuntimeError, "TEST original observer failure"):
            M.terminal_applescript("TEST original", observer, receipt, mock.Mock(), spawn=spawn)
        self.assertEqual(len(children), 1)
        self.assertTrue(receipt["owned_child_collected"])
        observer.assert_called_once()

    def test_late_completed_child_cannot_return_window_tty_after_observer_handoff(self):
        clock, polls, receipt = [0.0], [0], {}
        child = mock.Mock(pid=424242, returncode=0)
        def poll():
            polls[0] += 1
            return None if polls[0] == 1 else 0
        child.poll.side_effect = poll
        child.communicate.return_value = (b"7|/dev/ttysLATE\n", b"")
        def observe(*args, **kwargs):
            clock[0] = 21
            return {"admitted": False}
        with mock.patch.object(M.os, "killpg") as kill:
            with self.assertRaisesRegex(RuntimeError, "original_terminal_applescript_deadline_expired"):
                M.terminal_applescript("TEST original", observe, receipt, mock.Mock(), now=lambda: clock[0],
                                      spawn=mock.Mock(return_value=child))
            kill.assert_not_called()
        self.assertTrue(receipt["owned_child_collected"])
        self.assertEqual(receipt["failure_class"], "RuntimeError")

    def test_cleanup_error_is_retained_without_overwriting_original_failure(self):
        receipt = {}
        child = mock.Mock(pid=424242, returncode=None)
        child.poll.return_value = None
        def collect(*args, **kwargs):
            child.poll.return_value = 0
            return b"", b""
        child.communicate.side_effect = collect
        observer = mock.Mock(side_effect=RuntimeError("TEST primary observer failure"))
        with mock.patch.object(M.os, "killpg", side_effect=PermissionError("TEST cleanup failure")):
            with self.assertRaisesRegex(RuntimeError, "TEST primary observer failure"):
                M.terminal_applescript("TEST original", observer, receipt, mock.Mock(), spawn=mock.Mock(return_value=child))
        self.assertEqual(receipt["failure"], "TEST primary observer failure")
        self.assertTrue(receipt["owned_child_collected"])
        self.assertIn("PermissionError:TEST cleanup failure", receipt["cleanup_failures"])


class FailedCaseEvidence(unittest.TestCase):
    def state(self, root):
        case = root / "case-completion"
        data = root / "data"
        case.mkdir()
        data.mkdir()
        provider = mock.Mock(requests=[{"path": "/v1/chat/completions", "stream": True, "model": "TEST-local"}])
        provider.release.is_set.return_value = True
        sink = mock.Mock(rows=[], failures=[])
        return case, data, provider, sink

    def test_fixed_projection_retains_actual_event_and_attempt_facts_without_private_values(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            private = "TEST-private-prompt-reason-token-cache-key"
            rows = [{"type": "factory", "pid": os.getpid(), "prompt": private},
                    {"type": "agent_settled", "mode": "tui", "idle": True, "aborted": False,
                     "outcome": "completed", "reason": private}]
            (case / "witness.jsonl").write_text("\n".join(map(json.dumps, rows)))
            (data / "observations.json").write_text(json.dumps({"boot": private,
                "entries": [{"key": private, "until": 7.0, "bits": 3}]}))
            sink.rows = [{"status": "task_complete", "received_monotonic": 7.0, "request_id": private, "message": private}]
            observe = mock.Mock(return_value={"TEST-fixed-observation": True})
            screenshot = mock.Mock(return_value={"artifact": "case-completion/failure-before-cleanup.png"})
            value = M.case_failure_observation(case, None, provider, sink, data, root / "ClaudeNotifier.app",
                                                "task_complete", observe, screenshot)
            self.assertNotIn(private, json.dumps(value))
            self.assertEqual(value["witness"][1], {"type": "agent_settled", "mode": "tui", "idle": True,
                                                   "aborted": False, "outcome": "completed"})
            self.assertEqual(value["webhooks"], [{"status": "task_complete", "received_monotonic": 7.0}])
            self.assertEqual(value["observation_cache"], {"status": "observed_attempt_bits_only", "entries": 1,
                "desktop_attempt_entries": 1, "webhook_attempt_entries": 1})
            self.assertEqual(value["provider_requests"], 1)
            self.assertFalse(value["desktop_qualified"])
            self.assertEqual([call.args[0] for call in observe.call_args_list], ["helper-processes", "foreground", "banner"])
            self.assertTrue(all(0 < call.kwargs["timeout"] <= 2 for call in observe.call_args_list))
            screenshot.assert_called_once()

    def test_untrusted_files_and_expired_diagnostic_budget_never_expand_observation(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            private = "TEST-private-diagnostic-error"
            (case / "witness.jsonl").write_bytes(b" " * 65537)
            foreign = root / "foreign.json"
            foreign.write_text(json.dumps({"entries": []}))
            (data / "observations.json").symlink_to(foreign)
            clock = [0.0]
            def screenshot(seconds):
                self.assertLessEqual(seconds, 2)
                clock[0] = 6
                raise RuntimeError(private)
            observe = mock.Mock()
            value = M.case_failure_observation(case, None, provider, sink, data, root / "ClaudeNotifier.app",
                                                "task_complete", observe, screenshot, now=lambda: clock[0])
            self.assertEqual(value["witness"]["status"], "unavailable")
            self.assertEqual(value["observation_cache"]["status"], "unavailable")
            self.assertEqual(value["screenshot"]["error_class"], "RuntimeError")
            self.assertEqual(value["elapsed_seconds"], 6)
            self.assertNotIn(private, json.dumps(value))
            observe.assert_not_called()
            outside = root.parent / "TEST-outside-binding"
            value = M.case_failure_observation(case, None, provider, sink, outside, root / "ClaudeNotifier.app",
                                                "task_complete", mock.Mock(), mock.Mock(return_value={}))
            self.assertEqual(value["observation_cache"]["status"], "unavailable")

    def test_cleanup_attempts_all_owners_and_retains_the_primary_observation_error(self):
        calls, report = [], {}
        primary = RuntimeError("TEST actual banner observation failure")
        def failed_close():
            calls.append("origin")
            raise PermissionError("TEST cleanup error")
        actions = [("origin", failed_close), ("provider", lambda: calls.append("provider")),
                   ("evidence", lambda: calls.append("evidence"))]
        try:
            try:
                raise primary
            finally:
                M.cleanup_case(actions, primary, report, "completion")
        except RuntimeError as error:
            self.assertIs(error, primary)
        self.assertEqual(calls, ["origin", "provider", "evidence"])
        self.assertEqual(report["case_cleanup_failures"],
                         [{"case": "completion", "role": "origin", "error_class": "PermissionError"}])
        with self.assertRaisesRegex(RuntimeError, "owned_native_case_cleanup_failed"):
            M.cleanup_case([("origin", failed_close)], None, {}, "completion")


class NativeContextEvidence(unittest.TestCase):
    correlation = "11111111-1111-4111-8111-111111111111"
    nonce = "22222222-2222-4222-8222-222222222222"

    def transport(self, root):
        temporary = root / "case-completion/tmp"
        temporary.mkdir(parents=True, mode=0o700)
        parent = temporary / "agent-notifications-context-123456"
        parent.mkdir(mode=0o700)
        directory = parent / self.correlation
        directory.mkdir(mode=0o700)
        return temporary, directory

    def publish(self, directory, phase, value):
        path = directory / (self.nonce + "." + phase)
        path.write_text(json.dumps(value))
        path.chmod(0o600)
        return path

    def test_original_transport_metadata_is_read_without_request_access_or_file_changes(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-installed-macos-") as temporary:
            root = Path(temporary).resolve()
            owned, directory = self.transport(root)
            process = {"pid": os.getpid(), "startSeconds": 123, "startMicroseconds": 456}
            started = self.publish(directory, "started", {"schemaVersion": 1, "correlationID": self.correlation,
                "nonce": self.nonce, "process": process})
            receipt = self.publish(directory, "receipt", {"schemaVersion": 1, "correlationID": self.correlation,
                "nonce": self.nonce, "notificationID": self.correlation, "retrySafe": False,
                "status": "rejected", "reason": "permission_denied"})
            request = self.publish(directory, "request", {"arguments": ["TEST-private-prompt-token"], "bootID": "TEST-private-boot"})
            before = {p.name: (p.read_bytes(), p.stat().st_ino, p.stat().st_mode, p.stat().st_nlink)
                      for p in (started, receipt, request)}
            reads, original_read = [], M.NativeContextObservation.read
            def read(instance, fd, name, limit):
                reads.append(name)
                return original_read(instance, fd, name, limit)
            with mock.patch.object(M.NativeContextObservation, "read", read):
                watcher = M.NativeContextObservation(owned)
                try:
                    M.wait_for(lambda: len(watcher.receipt["rows"]) == 2, "TEST-original-metadata-not-observed", seconds=2)
                finally:
                    observed = watcher.close()
            self.assertEqual(set(reads), {started.name, receipt.name})
            self.assertEqual(before, {p.name: (p.read_bytes(), p.stat().st_ino, p.stat().st_mode, p.stat().st_nlink)
                                      for p in (started, receipt, request)})
            self.assertEqual(set(p.name for p in directory.iterdir()), set(before))
            output = json.dumps(observed)
            for private in (self.correlation, self.nonce, "TEST-private", str(root), "arguments", "bootID"):
                self.assertNotIn(private, output)
            self.assertEqual(observed["rows"][0]["reported_process"], process)
            self.assertFalse(observed["rows"][0]["process_identity_verified"])
            self.assertEqual(observed["rows"][1]["reason"], "permission_denied")
            self.assertTrue(observed["rows"][1]["matching_started_observed"])
            self.assertTrue(observed["owned_thread_collected"])
            self.assertTrue(observed["absence_is_unknown"])
            self.assertFalse(observed["qualification_input"])
            self.assertFalse(watcher.thread.is_alive())

    def test_symlink_and_inventory_cap_are_refusals_without_following_or_mutating_files(self):
        for scenario in ("symlink", "inventory_cap", "oversized", "unknown_reason", "missing_real_fields", "retry_safe"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory(prefix="TEST-pi-installed-macos-") as temporary:
                root = Path(temporary).resolve()
                owned, directory = self.transport(root)
                private = "TEST-private-unsupported-reason"
                if scenario == "symlink":
                    target = root / "foreign"
                    target.write_text(private)
                    (directory / (self.nonce + ".receipt")).symlink_to(target)
                elif scenario == "inventory_cap":
                    for number in range(65):
                        (owned / ("TEST-extra-" + str(number))).touch()
                elif scenario == "oversized":
                    self.publish(directory, "receipt", "x" * 16385)
                else:
                    value = {"schemaVersion": 1, "correlationID": self.correlation, "nonce": self.nonce,
                             "notificationID": self.correlation, "retrySafe": scenario == "retry_safe",
                             "status": "rejected", "reason": private if scenario == "unknown_reason" else "permission_denied"}
                    if scenario == "missing_real_fields":
                        del value["notificationID"]
                        del value["retrySafe"]
                    self.publish(directory, "receipt", value)
                watcher = M.NativeContextObservation(owned)
                try:
                    M.wait_for(lambda: "unavailable_class" in watcher.receipt, "TEST-unsafe-metadata-not-refused", seconds=2)
                finally:
                    value = watcher.close()
                self.assertEqual(value["rows"], [])
                self.assertTrue(value["owned_thread_collected"])
                self.assertNotIn(private, json.dumps(value))
                if scenario == "inventory_cap":
                    self.assertTrue(value["capped"])
                if scenario == "symlink":
                    self.assertEqual(target.read_text(), private)
                    self.assertTrue((directory / (self.nonce + ".receipt")).is_symlink())

    def test_real_hardlink_publication_is_ignored_until_sealed_without_stopping_the_watcher(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-installed-macos-") as temporary:
            root = Path(temporary).resolve()
            owned, directory = self.transport(root)
            watcher = M.NativeContextObservation(owned)
            try:
                for phase, value in (
                    ("started", {"schemaVersion": 1, "correlationID": self.correlation, "nonce": self.nonce,
                                 "process": {"pid": os.getpid(), "startSeconds": 123, "startMicroseconds": 456}}),
                    ("receipt", {"schemaVersion": 1, "correlationID": self.correlation, "nonce": self.nonce,
                                 "notificationID": self.correlation, "retrySafe": False,
                                 "status": "submitted", "reason": "os_accepted"})):
                    unsealed = directory / ("TEST-" + phase + ".tmp")
                    unsealed.write_text(json.dumps(value))
                    unsealed.chmod(0o600)
                    final = directory / (self.nonce + "." + phase)
                    count = watcher.receipt.get("unsealed_publications", 0)
                    os.link(unsealed, final)
                    M.wait_for(lambda: watcher.receipt.get("unsealed_publications", 0) > count,
                               "TEST-hardlink-transition-not-seen", seconds=2)
                    self.assertEqual(final.stat().st_nlink, 2)
                    self.assertNotIn("unavailable_class", watcher.receipt)
                    self.assertFalse(any(row["phase"] == phase for row in watcher.receipt["rows"]))
                    unsealed.unlink()  # Only the actual producer seals publication.
                    M.wait_for(lambda: any(row["phase"] == phase for row in watcher.receipt["rows"]),
                               "TEST-sealed-publication-not-seen", seconds=2)
                    self.assertEqual(final.stat().st_nlink, 1)
                    self.assertEqual(json.loads(final.read_text()), value)
            finally:
                observed = watcher.close()
            self.assertEqual([row["phase"] for row in observed["rows"]], ["started", "receipt"])
            self.assertEqual(observed["rows"][1]["status"], "submitted")
            self.assertTrue(observed["owned_thread_collected"])

    def test_watch_lifetime_and_unavailable_root_never_claim_missing_delivery(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-installed-macos-") as temporary:
            root = Path(temporary).resolve()
            owned, _ = self.transport(root)
            clock = [0.0]
            watcher = M.NativeContextObservation(owned, now=lambda: clock[0])
            clock[0] = 16
            M.wait_for(lambda: not watcher.thread.is_alive(), "TEST-native-watch-not-expired", seconds=2)
            value = watcher.close()
            self.assertEqual(value["rows"], [])
            self.assertEqual(value["elapsed_seconds"], 16)
            self.assertTrue(value["absence_is_unknown"])
            self.assertTrue(value["owned_thread_collected"])
            self.assertFalse(value["qualification_input"])
            rejected = M.NativeContextObservation(root)
            value = rejected.close()
            self.assertIsNone(rejected.thread)
            self.assertEqual(value["unavailable_class"], "RuntimeError")
            self.assertTrue(value["owned_thread_collected"])


class GlobalSettingsInspection(unittest.TestCase):
    def settings(self):
        return {"scope": "readonly_global_notification_settings", "diagnostic_only": True, "qualification_input": False,
            "desktop_qualified": False, "maximum_seconds": 6, "owner_pid": os.getpid(), "owner_uid": os.getuid(),
            "owner_start_identity": "123:456", "owner_bundle": "com.apple.systempreferences",
            "owner_path": "/System/Applications/System Settings.app", "apple_signature_verified": True, "settings_left_open": True,
            "settings_present_before": [], "opened_new_settings_instance": True, "notifications_page_ready": True, "notifications_navigation_performed": False,
            "observed_notifications_navigation_rows": 0, "sidebar_scroll_performed": False,
            "observed_notifications_sidebar_count": 0, "elapsed_seconds": 0.1,
            "initial_tree": {"nodes": [{"index": 0, "depth": 0, "role": "AXCheckBox", "fixed_labels": ["mirroring_or_sharing"],
                "navigation_press_action": True, "inert_navigation_descendant": False, "value_enum": "off",
                "children_query_status": 0, "children_array_verified": True, "action_query_status": 0,
                "action_array_verified": True, "subtree_complete": True,
                "public_action_bits": {"press": True, "increment": False, "decrement": False, "confirm": False, "pick": False, "show_menu": False},
                "unclassified_action_count": 0}], "traversal_capped": False, "cap_reasons": [],
                "maximum_nodes": 256, "maximum_depth": 18, "maximum_children": 64, "ax_timeout_status": 0, "tree_scope": "app_windows"}}

    def prepared(self, *, already_on=False):
        return {"scope": "fresh_owned_public_notification_presentation_preparation", "qualification_input": False,
            "desktop_qualified": False, "status": "prepared", "maximum_seconds": 6, "elapsed_seconds": 0.1,
            "settings_instance_was_new": True, "owned_window_observed": True, "owned_window_disappeared": True,
            "close_click_performed": True, "settings_process_collected": False, "settings_process_running_after_cleanup": True,
            "sharing_click_performed": not already_on, "policy_on_verified": True,
            "policy_before": {"mirroring_or_sharing": "on" if already_on else "off", "display_sleeping": "off", "screen_locked": "on"},
            "policy_after": {"mirroring_or_sharing": "on", "display_sleeping": "off", "screen_locked": "on"},
            "sharing_checkbox_bounds": [813, 382, 26, 15], "inspection_before_cleanup": self.settings()}

    def test_prepared_policy_requires_only_on_transition_and_real_owned_window_cleanup(self):
        for already_on in (False, True):
            value = self.prepared(already_on=already_on)
            self.assertIs(M.presentation_preparation_projection(value), value)
        for scenario in ("other_setting_changed", "window_left_open", "false_process_collection", "reused_settings",
                         "already_on_clicked", "primary_failure", "page_not_ready", "private_field"):
            value = self.prepared()
            if scenario == "other_setting_changed":
                value["policy_after"]["screen_locked"] = "off"
            elif scenario == "window_left_open":
                value["owned_window_disappeared"] = False
            elif scenario == "false_process_collection":
                value["settings_process_collected"] = True
            elif scenario == "reused_settings":
                value["settings_instance_was_new"] = False
            elif scenario == "already_on_clicked":
                value["policy_before"]["mirroring_or_sharing"] = "on"
            elif scenario == "primary_failure":
                value["primary_failure_class"] = "probe_refusal"
            elif scenario == "page_not_ready":
                value["inspection_before_cleanup"]["notifications_page_ready"] = False
            else:
                value["raw_private_ui_text"] = "TEST-private-user-text"
            with self.subTest(scenario=scenario), self.assertRaises(RuntimeError) as caught:
                M.presentation_preparation_projection(value)
            self.assertNotIn("TEST-private-user-text", str(caught.exception))

    def test_refused_host_admission_starts_no_capture_or_settings_operation(self):
        with tempfile.TemporaryDirectory() as temporary:
            execute, spawn = mock.Mock(), mock.Mock()
            with mock.patch.object(M, "presentation_preparation_admission", side_effect=RuntimeError("TEST-private-refusal")):
                value = M.inspect_global_notification_settings(Path("/TEST-observer"), Path(temporary),
                    prepare_presentation=True, execute=execute, spawn=spawn)
        execute.assert_not_called()
        spawn.assert_not_called()
        self.assertEqual(value["status"], "unavailable")
        self.assertFalse(value["capture_started"])
        self.assertNotIn("TEST-private-refusal", json.dumps(value))

    def test_early_inspection_refusal_retains_primary_and_cleanup_facts(self):
        value = self.prepared()
        value.update(status="refused", sharing_click_performed=False, owned_window_disappeared=False,
                     primary_failure_class="probe_refusal", primary_refusal="settings_sidebar_hit_unavailable",
                     cleanup_failure_class="probe_refusal", cleanup_refusal="owned_settings_close_button_changed")
        for key in ("policy_before", "policy_after", "policy_on_verified", "sharing_checkbox_bounds"):
            del value[key]
        # The original snapshot precedes row discovery and has a legitimate
        # observed-zero counter; it must not erase either later refusal.
        self.assertEqual(value["inspection_before_cleanup"]["observed_notifications_navigation_rows"], 0)
        projected = M.presentation_preparation_projection(value)
        self.assertIs(projected, value)
        self.assertEqual(projected["primary_refusal"], "settings_sidebar_hit_unavailable")
        self.assertEqual(projected["cleanup_refusal"], "owned_settings_close_button_changed")

    def measurements(self, frames):
        return {"audio_samples": 14400, "audio_peak": 0, "screen_frames": frames, "audio_nonzero_samples": 0,
                "audio_rms": 0, "sample_rate": 48000, "channels": 1, "format": "s16le"}

    def test_projection_refuses_private_labels_unknown_values_and_foreign_owners(self):
        self.assertEqual(M.global_settings_projection(self.settings()), self.settings())
        for field in ("private_label", "private_value", "foreign_owner", "page_ready_not_boolean", "extra_text"):
            value = self.settings()
            if field == "private_label":
                value["initial_tree"]["nodes"][0]["fixed_labels"] = ["TEST-private-user-text"]
            elif field == "private_value":
                value["initial_tree"]["nodes"][0]["value_enum"] = "TEST-private-user-text"
            elif field == "foreign_owner":
                value["owner_path"] = "/TEST/foreign.app"
            elif field == "page_ready_not_boolean":
                value["notifications_page_ready"] = "true"
            else:
                value["initial_tree"]["nodes"][0]["raw_description"] = "TEST-private-user-text"
            with self.subTest(field=field), self.assertRaises(RuntimeError) as caught:
                M.global_settings_projection(value)
            self.assertNotIn("TEST-private-user-text", str(caught.exception))

    def test_scroll_and_uncertain_query_facts_remain_closed_and_diagnostic_only(self):
        value = self.settings()
        value.update(sidebar_scroll_performed=True, observed_notifications_sidebar_count=1)
        value["sidebar_reveal_tree"] = json.loads(json.dumps(value["initial_tree"]))
        tree = value["sidebar_reveal_tree"]
        tree["tree_scope"] = "retained_sidebar"
        tree["traversal_capped"] = True
        tree["cap_reasons"] = ["children_unavailable"]
        tree["nodes"][0].update(children_query_status=-25205, children_array_verified=False, subtree_complete=False)
        self.assertIs(M.global_settings_projection(value), value)
        self.assertFalse(value["qualification_input"])
        self.assertFalse(value["desktop_qualified"])
        for scenario in ("ambiguous_scroll", "private_reason", "contradictory_query", "hidden_cap", "private_action", "settable_without_query"):
            candidate = json.loads(json.dumps(value))
            if scenario == "ambiguous_scroll":
                candidate["observed_notifications_sidebar_count"] = 2
            elif scenario == "private_reason":
                candidate["sidebar_reveal_tree"]["cap_reasons"] = ["TEST-private-user-text"]
            elif scenario == "contradictory_query":
                candidate["sidebar_reveal_tree"]["nodes"][0]["children_array_verified"] = True
            elif scenario == "hidden_cap":
                candidate["sidebar_reveal_tree"]["cap_reasons"] = []
            elif scenario == "private_action":
                candidate["sidebar_reveal_tree"]["nodes"][0]["public_action_bits"]["TEST-private-user-text"] = True
            else:
                candidate["sidebar_reveal_tree"]["nodes"][0]["value_settable"] = False
            with self.subTest(scenario=scenario), self.assertRaises(RuntimeError) as caught:
                M.global_settings_projection(candidate)
            self.assertNotIn("TEST-private-user-text", str(caught.exception))

    def test_real_owned_capture_is_collected_after_inspection_or_primary_failure(self):
        for preparing, failed, refused in ((False, False, False), (False, True, False),
                                          (True, False, False), (True, True, False), (True, False, True)):
            with self.subTest(preparing=preparing, failed=failed, refused=refused), tempfile.TemporaryDirectory(prefix="TEST-settings-observer-") as temporary:
                children, calls = [], []
                root = Path(temporary)
                baseline, final = self.measurements(2), self.measurements(4)
                def spawn(argv, **kwargs):
                    captured = Path(argv[2])
                    code = ('import json,sys,time; from pathlib import Path; root=Path(sys.argv[1]); '
                            '(root/"capture-ready.json").write_text(sys.argv[2]); deadline=time.monotonic()+5\n'
                            'while not (root/"capture-stop").exists() and time.monotonic()<deadline: time.sleep(.01)\n'
                            'print(sys.argv[3])')
                    child = subprocess.Popen([sys.executable, "-c", code, str(captured), json.dumps(baseline),
                                              json.dumps({"baseline": baseline, "measurements": final})], **kwargs)
                    children.append(child)
                    return child
                def execute(argv, **kwargs):
                    calls.append(argv)
                    self.assertIsNone(children[0].poll())
                    if argv[1] in ("inspect-global-notification-settings", "prepare-global-notification-presentation"):
                        self.assertLessEqual(kwargs["timeout"], 6)
                        if failed:
                            raise RuntimeError("TEST-private-native-failure")
                        if preparing:
                            self.assertEqual(kwargs["env"]["PI_MACOS_PRESENTATION_ADMITTED"], "1")
                            prepared = self.prepared()
                            if refused:
                                prepared.update(status="refused", primary_failure_class="probe_refusal", cleanup_failure_class="native_exception",
                                                owned_window_disappeared=False)
                            return json.dumps(prepared)
                        return json.dumps(self.settings())
                    self.assertEqual(argv[0], "/usr/sbin/screencapture")
                    Path(argv[2]).write_bytes(b"TEST screenshot only; never native qualification")
                    return ""
                clock = mock.Mock(implementation="mach_absolute_time()", monotonic=True, adjustable=False)
                with mock.patch.object(M.time, "get_clock_info", return_value=clock), \
                     mock.patch.object(M, "presentation_preparation_admission", return_value={"source_sha": "a" * 40}) as admission:
                    result = M.inspect_global_notification_settings(root / "TEST-observer", root / "evidence",
                        prepare_presentation=preparing, execute=execute, spawn=spawn)
                self.assertEqual(admission.call_count, int(preparing))
                self.assertEqual(result["status"], "unavailable" if failed or refused else "prepared" if preparing else "inspected")
                self.assertTrue(result["owned_capture_collected"])
                self.assertTrue(result["capture_frames_advanced"])
                self.assertFalse(result["qualification_input"])
                self.assertNotIn("TEST-private-native-failure", json.dumps(result))
                self.assertEqual(len(children), 1)
                with self.assertRaises(ProcessLookupError):
                    os.kill(children[0].pid, 0)
                if failed or refused:
                    self.assertEqual(result["failure_class"], "RuntimeError")
                    self.assertEqual(len(calls), 1)
                    if refused:
                        self.assertEqual(result["preparation"]["primary_failure_class"], "probe_refusal")
                        self.assertEqual(result["preparation"]["cleanup_failure_class"], "native_exception")
                else:
                    self.assertTrue(result["capture_active_after_inspection"])
                    self.assertTrue(result["capture_active_after_screenshot"])
                    if preparing:
                        self.assertTrue(result["preparation"]["owned_window_disappeared"])
                        self.assertFalse(result["preparation"]["settings_process_collected"])
                    else:
                        self.assertTrue(result["settings"]["settings_left_open"])


class PermissionUIIdentity(unittest.TestCase):
    def test_display_aliases_come_from_pinned_archive_and_identical_installed_metadata(self):
        archive = M.H.INPUTS / "ClaudeNotifier-smoke.app.zip"
        pin = M.H.load_pin()
        with zipfile.ZipFile(archive) as packaged:
            body = packaged.read("ClaudeNotifier.app/Contents/Info.plist")
        with tempfile.TemporaryDirectory(prefix="TEST-pi-permission-identity-") as temporary:
            app = Path(temporary) / "owned.app"
            info = app / "Contents/Info.plist"
            info.parent.mkdir(parents=True)
            info.write_bytes(body)
            identity = M.permission_ui_identity(archive, app, pin)
            self.assertEqual(identity["display_names"], ["Agent Notifications", "ClaudeNotifier"])
            self.assertEqual(identity["bundle_id"], M.H.BUNDLE)
            self.assertEqual(identity["installed_info_plist_sha256"], M.P.sha(info))
            with self.assertRaisesRegex(RuntimeError, "archive_identity_mismatch"):
                M.permission_ui_identity(archive, app, {**pin, "archive_sha256": "0" * 64})
            metadata = plistlib.loads(body)
            metadata["CFBundleDisplayName"] = "TEST foreign"
            info.write_bytes(plistlib.dumps(metadata))
            with self.assertRaisesRegex(RuntimeError, "installed_metadata_mismatch"):
                M.permission_ui_identity(archive, app, pin)


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

    def actor_receipt(self, child, candidate):
        return {"request_process_identity": {"status": "observed", "pid": child.pid, "parent_pid": os.getpid(),
                "executable_path": str(candidate), "process_start_identity": "123456:789"},
                "request_helper_identity": {"pid": 41, "finished_launching": True, "process_start_identity": "987654:123"},
                "permission_card_identity": {"candidate_name": "ClaudeNotifier", "os_dialog_pid": 81,
                "os_owner_start_identity": "555555:111", "bounds": [664, 40, 344, 73]}}

    def menu_step(self):
        window = {"window_id": 28, "owner_pid": 81, "layer": 101, "bounds": [918, 108, 97, 54], "positive_alpha_verified": True}
        return {"physical_options_click": True, "original_request_verified": True,
                "action_target_bounds": [918, 81, 80, 22], "popup_windows_before_options": [],
                "popup_windows_after_options": [window], "opened_menu_window": {**window,
                "options_bounds": [918, 81, 80, 22], "new_after_original_options_click": True}}

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

    def test_collapsed_candidate_card_keeps_original_helper_and_public_request_for_one_click(self):
        child = self.child(b"", delay=20)
        candidate, app = Path(sys.executable).resolve(), Path("/TEST/owned.app")
        helper = {"pid": 41, "process_start_identity": "987654:123", "finished_launching": True}
        identity = {"status": "observed", "pid": child.pid, "parent_pid": os.getpid(),
                    "executable_path": str(candidate), "process_start_identity": "123456:789"}
        card = self.actor_receipt(child, candidate)["permission_card_identity"]
        calls, receipt = [], {}

        def observe(operation, *args, **kwargs):
            calls.append((operation, args, kwargs))
            if operation == "terminal-process-identity":
                return identity
            if operation == "notification-consent":
                return {"dialogs": [card], "permission_cards": [card]}
            if operation == "helper-processes":
                return {"installed_helpers": [helper]}
            if operation == "allow-notification-consent":
                return {"physical_hover": True, "physical_options_click": True, "physical_allow_click": True,
                        "denial_button_observed": True, "original_request_verified": True, "pointer_at_target": True,
                        "installed_helper_pid": 41, "installed_helper_start_identity": "987654:123",
                        "original_request_pid": child.pid, "original_request_start_identity": "123456:789"}
            self.fail("unexpected native operation " + operation)

        receipt["request_process_identity"] = M.consent_request_identity(child, observe, candidate)
        self.assertEqual(M.consent_observer(child, observe, app, receipt)(), [card])
        helper.update(pid=42, process_start_identity="replacement")
        click = M.allow_consent(child, observe, app, candidate, receipt, 15, now=lambda: 10)
        actions = [row for row in calls if row[0] == "allow-notification-consent"]
        self.assertEqual(len(actions), 1)
        context = json.loads(actions[0][1][0])
        self.assertEqual(context, {"app": str(app), "helperPID": 41, "helperStart": "987654:123",
                         "requestPID": child.pid, "requestStart": "123456:789", "requestExecutable": str(candidate),
                         "deadlineMonotonic": 15, "clock": "mach_absolute_time", "ownerPID": 81,
                         "ownerStart": "555555:111", "candidateName": "ClaudeNotifier",
                         "cardBounds": [664, 40, 344, 73]})
        self.assertEqual(actions[0][2], {"timeout": 5})
        self.assertFalse(any(row[0] == "activate-permission-helper" for row in calls))
        self.assertIs(receipt["physical_consent_flow"], click)
        with self.assertRaises((RuntimeError, KeyError)):
            M.observed_permission(click)

    def test_completed_original_request_expired_budget_or_unverified_actors_never_click(self):
        child = self.child(b"", delay=20)
        candidate = Path(sys.executable).resolve()
        identity = {"status": "observed", "pid": child.pid, "parent_pid": os.getpid(),
                    "executable_path": str(candidate), "process_start_identity": "123456:789"}
        receipt = {"request_process_identity": identity,
                   "request_helper_identity": {"pid": 41, "finished_launching": True, "process_start_identity": "987654:123"}}
        observer = mock.Mock()
        with self.assertRaisesRegex(RuntimeError, "no_longer_pending"):
            M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate, receipt, 10, now=lambda: 10)
        for change in ({"pid": child.pid + 1}, {"parent_pid": os.getpid() + 1},
                       {"executable_path": "/TEST/foreign"}, {"process_start_identity": ""}):
            changed = {**receipt, "request_process_identity": {**identity, **change}}
            with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, "actors_unverified"):
                M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate, changed, 15, now=lambda: 10)
        for helper in ({}, {"pid": 41, "finished_launching": True},
                       {"pid": 41, "finished_launching": False, "process_start_identity": "987654:123"}):
            with self.subTest(helper=helper), self.assertRaisesRegex(RuntimeError, "actors_unverified"):
                M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate,
                                {**receipt, "request_helper_identity": helper}, 15, now=lambda: 10)
        child.terminate()
        child.communicate(timeout=3)
        with self.assertRaisesRegex(RuntimeError, "no_longer_pending"):
            M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate, receipt, 15, now=lambda: 10)
        observer.assert_not_called()

    def test_native_click_requires_exact_actor_and_visible_denial_receipt(self):
        child = self.child(b"", delay=20)
        candidate = Path(sys.executable).resolve()
        receipt = self.actor_receipt(child, candidate)
        click = {"physical_allow_click": True, "denial_button_observed": True, "original_request_verified": True,
                 "pointer_at_target": True,
                 "installed_helper_pid": 41, "installed_helper_start_identity": "987654:123",
                 "original_request_pid": child.pid, "original_request_start_identity": "123456:789"}
        for change in ({"physical_allow_click": False}, {"denial_button_observed": False}, {"pointer_at_target": False},
                       {"original_request_verified": False}, {"installed_helper_pid": 42},
                       {"installed_helper_start_identity": "replacement"}, {"original_request_pid": child.pid + 1},
                       {"original_request_start_identity": "replacement"}):
            native = mock.Mock(return_value={**click, **change})
            with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, "physical_click_unverified"):
                M.allow_consent(child, native, Path("/TEST/owned.app"), candidate, receipt, 15, now=lambda: 10)
            native.assert_called_once()

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

    def test_permission_card_hover_and_observed_options_are_once_bound_to_original_request(self):
        child = self.child(b"", delay=20)
        candidate = Path(sys.executable).resolve()
        receipt = self.actor_receipt(child, candidate)
        card = receipt["permission_card_identity"]
        phase, calls, captures = [0], [], []

        def observe(operation, *args, **kwargs):
            calls.append(operation)
            if operation in ("hover-notification-consent", "open-notification-consent-options", "observe-notification-consent-menu"):
                context = json.loads(args[0])
                self.assertEqual(context["requestPID"], child.pid)
                self.assertEqual(context["helperPID"], 41)
                self.assertEqual(context["ownerPID"], 81)
                self.assertEqual(context["deadlineMonotonic"], 15)
                self.assertEqual(context["clock"], "mach_absolute_time")
                if operation == "observe-notification-consent-menu":
                    self.assertEqual(context["menuWindowID"], 28)
                    return {"original_request_verified": True, "installed_helper_pid": 41,
                            "installed_helper_start_identity": "987654:123", "original_request_pid": child.pid,
                            "original_request_start_identity": "123456:789", "dialogs": [{"observed_allow": True}],
                            "permission_cards": [card], "observed_menu": {"window_id": 28, "bounds": [918, 108, 97, 54],
                            "options_bounds": [918, 81, 80, 22], "denial_button_observed": True}}
                phase[0] = 1 if operation == "hover-notification-consent" else 2
                return {**(self.menu_step() if phase[0] == 2 else {}),
                        "physical_pointer_hover": phase[0] == 1, "physical_options_click": phase[0] == 2,
                        "pointer_at_target": True, "original_request_verified": True,
                        "installed_helper_pid": 41, "installed_helper_start_identity": "987654:123",
                        "original_request_pid": child.pid, "original_request_start_identity": "123456:789"}
            self.assertEqual(operation, "notification-consent")
            return {"dialogs": [{"observed_allow": True}] if phase[0] == 2 else [],
                    "permission_cards": [{**card, "options_bounds": [1, 2, 3, 4]}] if phase[0] else [card]}

        observed = M.consent_observer(child, observe, Path("/TEST/owned.app"), receipt, now=lambda: 10,
                                      ui_capture=lambda phase: captures.append(phase) or {"desktop_qualified": False},
                                      candidate=candidate, deadline=15)
        self.assertEqual(observed(), [])
        self.assertEqual(observed(), [])
        self.assertEqual(observed(), [{"observed_allow": True}])
        self.assertEqual(observed(), [{"observed_allow": True}])
        self.assertEqual(calls.count("hover-notification-consent"), 1)
        self.assertEqual(calls.count("open-notification-consent-options"), 1)
        self.assertEqual(captures, ["hover", "options"])
        self.assertNotIn("allow-notification-consent", calls)
        with self.assertRaises((KeyError, RuntimeError)):
            M.observed_permission(receipt)

    def test_menu_context_rejects_existing_ambient_or_mismatched_popup(self):
        child = self.child(b"", delay=20)
        candidate = Path(sys.executable).resolve()
        receipt = self.actor_receipt(child, candidate)
        step = self.menu_step()
        receipt["permission_card_options"] = step
        expected = {"menuWindowID": 28, "menuBounds": [918, 108, 97, 54], "optionsBounds": [918, 81, 80, 22]}
        self.assertEqual(M.consent_menu_context(receipt), expected)
        for change in ({"physical_options_click": False}, {"original_request_verified": False},
                       {"popup_windows_before_options": [{"window_id": 28}]},
                       {"popup_windows_before_options": [{"invalid_bounds": True}]},
                       {"opened_menu_window": {**step["opened_menu_window"], "owner_pid": 82}},
                       {"opened_menu_window": {**step["opened_menu_window"], "positive_alpha_verified": False}},
                       {"opened_menu_window": {**step["opened_menu_window"], "bounds": [918, 108, 401, 54]}},
                       {"opened_menu_window": {**step["opened_menu_window"], "new_after_original_options_click": False}},
                       {"popup_windows_after_options": [{"invalid_inventory": True}]},
                       {"popup_windows_after_options": [{**step["popup_windows_after_options"][0], "window_id": 29}]},
                       {"popup_windows_after_options": step["popup_windows_after_options"] * 2}):
            observer = mock.Mock()
            with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, "original_options_menu"):
                M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate,
                                {**receipt, "permission_card_options": {**step, **change}}, 15, now=lambda: 10,
                                operation="observe-notification-consent-menu")
            observer.assert_not_called()

    def test_menu_allow_requires_same_popup_and_topmost_item_after_original_options(self):
        child = self.child(b"", delay=20)
        candidate = Path(sys.executable).resolve()
        receipt = self.actor_receipt(child, candidate)
        receipt["permission_card_options"] = self.menu_step()
        click = {"physical_allow_click": True, "pointer_at_target": True, "denial_button_observed": True,
                 "original_request_verified": True, "installed_helper_pid": 41,
                 "installed_helper_start_identity": "987654:123", "original_request_pid": child.pid,
                 "original_request_start_identity": "123456:789", "topmost_allow_item_verified": True,
                 "observed_menu": {"window_id": 28, "bounds": [918, 108, 97, 54],
                 "options_bounds": [918, 81, 80, 22], "denial_button_observed": True}}
        for change in ({"topmost_allow_item_verified": False},
                       {"observed_menu": {**click["observed_menu"], "window_id": 29}},
                       {"observed_menu": {**click["observed_menu"], "bounds": [1, 2, 97, 54]}},
                       {"observed_menu": {**click["observed_menu"], "denial_button_observed": False}}):
            observer = mock.Mock(return_value={**click, **change})
            with self.subTest(change=change), self.assertRaisesRegex(RuntimeError, "menu_identity_or_topmost"):
                M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate, receipt, 15, now=lambda: 10)
            observer.assert_called_once()
        observer = mock.Mock(return_value=click)
        self.assertIs(M.allow_consent(child, observer, Path("/TEST/owned.app"), candidate, receipt, 15, now=lambda: 10), click)
        context = json.loads(observer.call_args.args[1])
        self.assertEqual(context["menuWindowID"], 28)
        self.assertEqual(context["deadlineMonotonic"], 15)
        with self.assertRaises((KeyError, RuntimeError)):
            M.observed_permission(click)

    def test_completed_request_or_ambiguous_permission_cards_never_hover(self):
        for code, cards in ((0, [{}]), (None, [{}, {}])):
            request = mock.Mock(poll=mock.Mock(return_value=code))
            observe = mock.Mock(return_value={"dialogs": [], "permission_cards": cards})
            observed = M.consent_observer(request, observe, Path("/TEST/owned.app"), {})
            self.assertEqual(observed(), [])
            self.assertFalse(any(call.args[0] in ("hover-notification-consent", "open-notification-consent-options",
                                                 "allow-notification-consent") for call in observe.call_args_list))

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

    def test_permission_log_enforces_byte_limit_and_collects_real_child(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-permission-log-") as temporary:
            log = M.PermissionLog(Path(temporary), argv=[sys.executable, "-c",
                "import sys,time;sys.stdout.buffer.write(b'x'*4096);sys.stdout.flush();time.sleep(10)"],
                seconds=2, limit=64)
            deadline = time.monotonic() + 3
            while log.thread.is_alive() and time.monotonic() < deadline:
                time.sleep(0.01)
            receipt = log.close()
            self.assertTrue(receipt["owned_child_collected"])
            self.assertTrue(receipt["output_limit_reached"])
            self.assertEqual(receipt["bytes"], 64)
            self.assertEqual(log.path.read_bytes(), b"x" * 64)
            self.assertIsNotNone(log.child.poll())
            self.assertFalse(receipt["desktop_qualified"])

    def test_permission_log_early_close_collects_a_silent_child(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-permission-log-") as temporary:
            log = M.PermissionLog(Path(temporary), argv=[sys.executable, "-c", "import time;time.sleep(10)"],
                                  seconds=20, limit=64)
            started = time.monotonic()
            receipt = log.close()
            self.assertTrue(receipt["owned_child_collected"])
            self.assertLess(time.monotonic() - started, 6)
            self.assertEqual(receipt["bytes"], 0)
            self.assertFalse(receipt["output_limit_reached"])

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
