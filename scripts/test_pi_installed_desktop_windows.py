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
    def observe(self, wrong_after=None, keyboard_only=False, live_delivery=False):
        clock, window = Clock(), {"hwnd": 123}
        stop = CaptureStop(clock)

        def native(command):
            self.assertEqual(command, "foreground")
            wrong = wrong_after is not None and clock.now >= wrong_after
            return {"hwnd": 456 if wrong and not keyboard_only else 123,
                    "keyboard_hwnd": 456 if wrong else 123}

        with patch.object(W.P, "time", SimpleNamespace(monotonic=clock.monotonic, sleep=clock.sleep)):
            result = W.observe_unchanged_focus(native, window, stop, live_delivery=live_delivery)
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

    def test_live_click_monitors_full_focus_window_without_truncating_later_cue(self):
        result, stop = self.observe(live_delivery=True)
        self.assertGreaterEqual(result["elapsed_seconds"], 15)
        self.assertGreater(result["samples"], 250)
        self.assertGreaterEqual(stop.at, 15)
        self.assertLess(stop.at, 15.1)
        with self.assertRaisesRegex(RuntimeError, "changed_foreground_or_keyboard"):
            self.observe(wrong_after=3, live_delivery=True)


class OriginalWebhookDeadlineTests(unittest.TestCase):
    def test_required_original_webhook_uses_only_existing_capture_remainder(self):
        sink, timing = Mock(), {}
        W.await_case_webhook(sink, "task_complete", 30, timing, 0, now=lambda: 28.5)
        sink.wait.assert_called_once_with("task_complete", timeout=1.5)
        self.assertEqual(timing, {"webhook_received_seconds": 28.5})

    def test_expired_original_budget_does_not_start_another_wait(self):
        sink = Mock()
        with self.assertRaisesRegex(RuntimeError, "original_case_webhook_deadline_expired"):
            W.await_case_webhook(sink, "task_complete", 30, {}, 0, now=lambda: 30)
        sink.wait.assert_not_called()


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
            with self.assertRaisesRegex(RuntimeError, "partial_pi_process_cleanup_unverified"):
                owned[0].close()
            self.assertFalse(owned[0].closed)
            self.assertEqual([row["failure"] for row in owned[0].cleanup_stage_failures],
                             ["partial_pi_process_cleanup_unverified", "partial_terminal_cleanup_unverified"])
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


class CaseCleanupTests(unittest.TestCase):
    def test_initial_shell_observation_does_not_launch_or_warm_winrt(self):
        shell = Mock()
        def observed(pointer):
            pointer._obj.value = 6
            return 0
        shell.SHQueryUserNotificationState.side_effect = observed
        with patch("ctypes.WinDLL", create=True, return_value=shell) as library, patch.object(W, "command") as child:
            self.assertEqual(W.shell_notification_state(), {"hresult": 0, "state": 6})
        library.assert_called_once_with("shell32", use_last_error=True)
        child.assert_not_called()

    def test_native_no_banner_exit_retains_mode_status_and_observation(self):
        result = subprocess.CompletedProcess([], 3, b'{"found":false}', b'')
        with patch.object(W.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(RuntimeError, r'native_command_failed:observer.exe:mode=toast-click:exit=3:stderr=:stdout=\{"found":false\}'):
                W.command(["observer.exe", "toast-click", "TEST-Pi-toast-owned", "TEST body", "image.bmp"])

    def test_early_watcher_deadline_collects_only_its_owned_process_and_keeps_refusal_receipt(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-toast-watch-") as directory:
            child = Mock(returncode=-1)
            child.communicate.side_effect = [subprocess.TimeoutExpired("toast-watch", 0.1), (b'{"found":false}', b'')]
            receipt = Path(directory) / "early.json"
            with patch.object(W.time, "monotonic", return_value=20):
                W.collect_toast_watcher(child, receipt, deadline=19)
            child.kill.assert_called_once_with()
            self.assertEqual(child.communicate.call_args_list[0].kwargs, {"timeout": 0.1})
            proof = json.loads(receipt.read_text())
            self.assertTrue(proof["deadline_expired"])
            self.assertFalse(proof["qualification_input"])
            self.assertEqual(proof["observation"], {"found": False})

    def test_primary_observation_survives_cleanup_refusal_and_all_resources_are_attempted(self):
        report = {}
        origin = Mock(side_effect=RuntimeError("owned_process_termination_failed:pid=41:winerror=5"))
        distractor, capture, provider = Mock(), Mock(), Mock()
        with self.assertRaisesRegex(RuntimeError, "^native_product_toast_missing$"):
            try:
                raise RuntimeError("native_product_toast_missing")
            finally:
                W.cleanup_case((("origin", origin), ("distractor", distractor), ("capture", capture), ("provider", provider)),
                               primary_failure="native_product_toast_missing", report=report, case="completion")
        for close in (origin, distractor, capture, provider):
            close.assert_called_once_with()
        self.assertEqual(report["case_cleanup_failures"], [{"case": "completion", "resource": "origin",
                         "failure": "owned_process_termination_failed:pid=41:winerror=5"}])

    def test_cleanup_only_failure_refuses_after_remaining_resources_are_collected(self):
        report = {}
        origin = Mock(side_effect=RuntimeError("cleanup_refused"))
        provider = Mock()
        with self.assertRaisesRegex(RuntimeError, "^native_case_cleanup_failed:cleanup_refused$"):
            W.cleanup_case((("origin", origin), ("provider", provider)), primary_failure=None, report=report, case="completion")
        provider.assert_called_once_with()
        self.assertEqual(report["case_cleanup_failures"][0]["resource"], "origin")

    def test_existing_capture_stop_is_preserved_while_helper_is_collected(self):
        helper = Mock()
        helper.poll.return_value = None
        stop = Mock()
        stop.exists.return_value = True
        W.stop_helper(helper, stop)
        stop.write_text.assert_not_called()
        helper.communicate.assert_called_once_with(timeout=5)


class FailedCaseEvidenceTests(unittest.TestCase):
    def state(self, root):
        case, data = root / "case-completion", root / "data"
        case.mkdir()
        data.mkdir()
        (case / "witness.jsonl").write_text('{"type":"agent_settled","mode":"tui","idle":true}\n')
        provider = Mock(requests=[{"TEST-private-provider-payload": "private"}])
        provider.release.is_set.return_value = True
        sink = Mock(rows=[], failures=[])
        return case, data, provider, sink

    def origin(self):
        identity = {"present": True, "pid": 41, "creation_time": "123456789", "executable": "TEST-pi.exe"}
        native = Mock()
        native.held_process.return_value = identity
        return SimpleNamespace(native=native, process_handle=Mock(value=234), pi_identity=identity,
                               executable="TEST-pi.exe")

    def test_fixed_projection_retains_attempt_facts_and_original_handle_without_private_values(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            private = "TEST-private-prompt-reason-token-cache-key"
            (case / "witness.jsonl").write_text(json.dumps({"type": "agent_settled", "mode": "tui",
                "idle": True, "aborted": False, "outcome": "completed", "reason": private, "prompt": private}) + "\n")
            cache = data / "observations.json"
            cache.write_text(json.dumps({"boot": private, "entries": [
                {"key": private, "until": 7.0, "bits": bit} for bit in (1, 2, 3)]}))
            before = cache.read_bytes()
            sink.rows = [{"status": "task_complete", "received_monotonic": 7.0, "request_id": private, "message": private},
                         {"status": private, "received_monotonic": 8.0},
                         {"status": "question", "received_monotonic": float("inf")}]
            origin, snapshot = self.origin(), Mock(return_value={"screenshot": {"status": "observed"}})
            value = W.case_failure_observation(case, origin, provider, sink, data, snapshot, now=lambda: 10.0)
            self.assertNotIn(private, json.dumps(value))
            self.assertEqual(value["provider"], {"requests": 1, "released": True})
            self.assertEqual(value["witness"], [{"type": "agent_settled", "mode": "tui", "idle": True,
                                                 "aborted": False, "outcome": "completed"}])
            self.assertEqual(value["webhooks"], {"failure_count": 0, "rows": [
                {"status": "task_complete", "received_monotonic": 7.0}]})
            self.assertEqual(value["observation_cache"], {"status": "observed_attempt_bits_only", "entries": 3,
                "desktop_attempt_entries": 2, "webhook_attempt_entries": 2})
            origin.native.held_process.assert_called_once_with(origin.process_handle, "process-identity-handle",
                                                               "TEST-pi.exe", timeout=2)
            origin.process_handle.close.assert_not_called()
            self.assertEqual(value["origin_process"], {"status": "original_retained_identity_observed", "pid": 41,
                                                       "creation_time": "123456789"})
            self.assertFalse(value["qualification_input"])
            self.assertFalse(value["desktop_qualified"])
            self.assertEqual(cache.read_bytes(), before)
            snapshot.assert_called_once_with(16.0)

    def test_file_and_row_bounds_are_typed_unavailable_instead_of_partial_success(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            witness, cache = case / "witness.jsonl", data / "observations.json"
            for witness_bytes in (b" " * 65537, b'{"type":"agent_settled"}\n' * 65, b'{"type":"TEST-private-type"}\n'):
                witness.write_bytes(witness_bytes)
                value = W.case_failure_observation(case, None, provider, sink, data, Mock(return_value={}))
                self.assertEqual(value["witness"]["status"], "unavailable")
            for content in (" " * (48 * 1024 + 1), json.dumps({"entries": [{"bits": 1}] * 257}),
                            json.dumps({"entries": [{"bits": True}]})):
                cache.write_text(content)
                value = W.case_failure_observation(case, None, provider, sink, data, Mock(return_value={}))
                self.assertEqual(value["observation_cache"]["status"], "unavailable")
            sink.rows = [{}] * 33
            provider.requests = [{}] * 65
            value = W.case_failure_observation(case, None, provider, sink, data, Mock(return_value={}))
            self.assertEqual(value["webhooks"]["status"], "unavailable")
            self.assertEqual(value["provider"]["status"], "unavailable")

    def test_links_nonregular_and_outside_cache_never_supply_attempt_evidence(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            cache, foreign = data / "observations.json", root / "foreign.json"
            foreign.write_text('{"entries":[{"bits":3}]}')
            cache.symlink_to(foreign)
            value = W.case_failure_observation(case, None, provider, sink, data, Mock(return_value={}))
            self.assertEqual(value["observation_cache"]["status"], "unavailable")
            cache.unlink()
            cache.mkdir()
            value = W.case_failure_observation(case, None, provider, sink, data, Mock(return_value={}))
            self.assertEqual(value["observation_cache"]["status"], "unavailable")
            value = W.case_failure_observation(case, None, provider, sink, root.parent, Mock(return_value={}))
            self.assertEqual(value["observation_cache"]["status"], "unavailable")

    def test_expired_diagnostic_budget_never_starts_original_process_query_or_renews_deadline(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            clock, origin = Clock(), self.origin()
            def snapshot(deadline):
                self.assertEqual(deadline, 6)
                clock.now = 6
                raise RuntimeError("TEST-private-diagnostic-error")
            value = W.case_failure_observation(case, origin, provider, sink, data, snapshot, now=clock.monotonic)
            self.assertEqual(value["precleanup_ui"], {"status": "unavailable", "error_class": "RuntimeError"})
            self.assertEqual(value["origin_process"], {"status": "unavailable", "error_class": "RuntimeError"})
            self.assertEqual(value["elapsed_seconds"], 6)
            self.assertNotIn("TEST-private-diagnostic-error", json.dumps(value))
            origin.native.held_process.assert_not_called()

    def test_snapshot_refusal_does_not_skip_retained_query_or_mask_primary_cleanup_path(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-failure-evidence-") as temporary:
            root = Path(temporary).resolve()
            case, data, provider, sink = self.state(root)
            origin, primary, calls, report = self.origin(), RuntimeError("actual_primary_observation"), [], {}
            try:
                try:
                    raise primary
                finally:
                    value = W.case_failure_observation(case, origin, provider, sink, data,
                        Mock(side_effect=PermissionError("TEST-private-snapshot-error")))
                    W.cleanup_case((("origin", lambda: calls.append("origin")), ("provider", lambda: calls.append("provider"))),
                                   primary_failure=str(primary), report=report, case="completion")
            except RuntimeError as error:
                self.assertIs(error, primary)
            self.assertEqual(calls, ["origin", "provider"])
            origin.native.held_process.assert_called_once()
            self.assertEqual(value["precleanup_ui"]["error_class"], "PermissionError")
            self.assertNotIn("TEST-private-snapshot-error", json.dumps(value))
            self.assertEqual(value["observation_cache"], {"status": "absent"})


class ProcessTimelineTests(unittest.TestCase):
    def timeline(self):
        return {"observation_only": True, "qualification_input": False, "misses_remain_unknown": True,
                "stop_reason": "stop_requested", "elapsed_ms": 70, "inventory_scans": 3, "missed_candidates": 1,
                "processes": [{"pid": 41, "parent_pid": 0, "birth_filetime": "123456789", "role": "original_pi",
                    "image_leaf": "pi.exe", "first_seen_ms": 0, "last_handle_observed_ms": 60,
                    "exit_signaled_observed_ms": None, "exit_code": None},
                    {"pid": 42, "parent_pid": 41, "birth_filetime": "123456790", "role": "direct_candidate",
                    "image_leaf": "claude-notifications-windows-amd64.exe", "first_seen_ms": 10,
                    "last_handle_observed_ms": 60, "exit_signaled_observed_ms": 40, "exit_code": 0}]}

    def test_start_uses_only_original_handle_and_one_prelaunch_absolute_deadline(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-watch-") as temporary:
            root, helpers = Path(temporary).resolve(strict=True), []
            origin = SimpleNamespace(root=root / "origin", process_handle=Mock(value=234), pi_identity={"creation_time": "123456789"},
                                     executable="TEST-pi.exe", native=Mock(executable="observer.exe"))
            origin.process_handle.kernel.GetTickCount64.return_value = 100000
            child = Mock()
            with patch.object(W.subprocess, "STARTUPINFO", create=True, return_value=SimpleNamespace()) as startup, \
                    patch.object(W.subprocess, "CREATE_NO_WINDOW", create=True, new=0x08000000), \
                    patch.object(W.os, "set_handle_inheritable", create=True) as inherit, \
                    patch.object(W.subprocess, "Popen", return_value=child) as spawn:
                watcher = W.start_process_watcher(origin, "TEST-notifier.exe", root, 30, helpers, now=lambda: 2.5)
            self.assertEqual(helpers, [child])
            self.assertIs(watcher["child"], child)
            self.assertEqual(watcher["deadline"], 30)
            self.assertEqual(watcher["native_deadline"], 127500)
            self.assertEqual(spawn.call_args.args[0], ["observer.exe", "process-watch-owned-handle", "234", "123456789",
                "TEST-pi.exe", "TEST-notifier.exe", str(root / "process-watch-ready.json"), str(root / "process-watch.stop"), "127500",
                str(root / "process-timeline-native.json")])
            self.assertEqual(startup.return_value.lpAttributeList, {"handle_list": [234]})
            self.assertTrue(spawn.call_args.kwargs["close_fds"])
            self.assertEqual(spawn.call_args.kwargs["stdin"], subprocess.DEVNULL)
            self.assertEqual([call.args for call in inherit.call_args_list], [(234, True), (234, False)])
            origin.process_handle.close.assert_not_called()
            (root / "process-watch-ready.json").write_text(json.dumps({"ready": True, "observation_only": True,
                "original_identity_verified": True, "deadline_tick_ms": 127500}))
            W.require_process_watcher_ready(watcher, now=lambda: 3)
            self.assertEqual(watcher["native_deadline"], 127500)

    def test_expired_prelaunch_or_mismatched_ready_never_authorizes_a_new_lifetime(self):
        origin = SimpleNamespace(process_handle=Mock(value=234), pi_identity={"creation_time": "123456789"})
        origin.process_handle.kernel.GetTickCount64.return_value = 100000
        with patch.object(W.subprocess, "Popen") as spawn:
            with self.assertRaisesRegex(RuntimeError, "original_deadline_required"):
                W.start_process_watcher(origin, "TEST-notifier.exe", Path("TEST-root"), 30, [], now=lambda: 30)
        spawn.assert_not_called()
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-watch-") as temporary:
            ready = Path(temporary) / "ready.json"
            watcher = {"ready": ready, "deadline": 30, "native_deadline": 127500}
            for changes in ({"ready": 1}, {"original_identity_verified": False}, {"deadline_tick_ms": 127501}):
                ready.write_text(json.dumps({"ready": True, "observation_only": True,
                    "original_identity_verified": True, "deadline_tick_ms": 127500, **changes}))
                with self.subTest(changes=changes), self.assertRaisesRegex(RuntimeError, "ready_invalid"):
                    W.require_process_watcher_ready(watcher, now=lambda: 3)

    def test_closed_timeline_preserves_observed_bounds_and_never_promotes_misses(self):
        value = self.timeline()
        self.assertEqual(W.process_timeline(json.dumps(value).encode()), value)
        self.assertTrue(value["misses_remain_unknown"])
        self.assertFalse(value["qualification_input"])
        for field, change in (("role", "TEST-private-argv"), ("image_leaf", "C:/TEST-private/path.exe"),
                              ("pid", True), ("exit_code", -1), ("exit_signaled_observed_ms", 100)):
            broken = self.timeline()
            broken["processes"][1][field] = change
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                W.process_timeline(json.dumps(broken).encode())
        for field, change in (("processes", [self.timeline()["processes"][0]] * 65), ("inventory_scans", 1502)):
            broken = {**self.timeline(), field: change}
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                W.process_timeline(json.dumps(broken).encode())

    def test_collection_discards_raw_unknown_output_and_only_stops_owned_observer(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-watch-") as temporary:
            root = Path(temporary)
            private = "TEST-private-argv-stdout-error"
            child = Mock(returncode=0)
            child.poll.side_effect = [None, 0]
            child.communicate.return_value = (b'{"receipt_written":true}\n', b"")
            native_receipt = root / "process-timeline-native.json"
            native_receipt.write_text(json.dumps({**self.timeline(), "private": private}))
            watcher = {"child": child, "stop": root / "stop", "deadline": 30, "native_receipt": native_receipt}
            receipt = root / "receipt.json"
            W.collect_process_watcher(watcher, receipt, now=lambda: 20)
            value = json.loads(receipt.read_text())
            self.assertTrue(value["watcher_collected"])
            self.assertEqual(value["status"], "unavailable")
            self.assertNotIn(private, receipt.read_text())
            self.assertEqual((root / "stop").read_text(), "stop\n")
            child.kill.assert_not_called()
            child.communicate.assert_called_once_with(timeout=10)

    def test_collection_reads_only_closed_bounded_receipt_after_native_join(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-watch-") as temporary:
            root = Path(temporary).resolve()
            native_receipt = root / "process-timeline-native.json"
            native_receipt.write_text(json.dumps(self.timeline()))
            before = native_receipt.read_bytes()
            child = Mock(returncode=0)
            child.poll.return_value = 0
            child.communicate.return_value = (b'{"receipt_written":true}\n', b"")
            watcher = {"child": child, "stop": root / "stop", "deadline": 30, "native_receipt": native_receipt}
            receipt = root / "receipt.json"
            W.collect_process_watcher(watcher, receipt, now=lambda: 20)
            self.assertEqual(json.loads(receipt.read_text())["timeline"], self.timeline())
            self.assertEqual(native_receipt.read_bytes(), before)
            self.assertFalse((root / "stop").exists())
            child.kill.assert_not_called()
            native_receipt.write_bytes(b" " * 65537)
            W.collect_process_watcher(watcher, receipt, now=lambda: 20)
            self.assertEqual(json.loads(receipt.read_text())["status"], "unavailable")

    def test_deadline_collection_kills_reaps_only_watcher_and_keeps_primary_cleanup_path(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-watch-") as temporary:
            root, calls, report = Path(temporary), [], {}
            child = Mock(returncode=-1)
            child.poll.side_effect = [None, -1]
            child.communicate.side_effect = [subprocess.TimeoutExpired("process-watch", 0.001), (b"", b"TEST-private-error")]
            watcher = {"child": child, "stop": root / "stop", "deadline": 30}
            primary = RuntimeError("actual_primary_observation")
            try:
                try:
                    raise primary
                finally:
                    W.cleanup_case((("process_watcher", lambda: W.collect_process_watcher(watcher, root / "receipt.json", now=lambda: 31)),
                                    ("origin", lambda: calls.append("origin")), ("provider", lambda: calls.append("provider"))),
                                   primary_failure=str(primary), report=report, case="completion")
            except RuntimeError as error:
                self.assertIs(error, primary)
            self.assertEqual(calls, ["origin", "provider"])
            child.kill.assert_called_once_with()
            self.assertEqual(child.communicate.call_args_list[0].kwargs, {"timeout": 0.001})
            value = json.loads((root / "receipt.json").read_text())
            self.assertTrue(value["deadline_expired"])
            self.assertTrue(value["watcher_collected"])
            self.assertNotIn("TEST-private-error", json.dumps(value))


class ProcessCleanupTests(unittest.TestCase):
    def terminal(self, root, native):
        terminal = W.Terminal.__new__(W.Terminal)
        terminal.root, terminal.native, terminal.closed = root, native, False
        terminal.executable, terminal.pi_pid = str(root / "pi.exe"), 41
        terminal.pi_identity = {"creation_time": "123456789"}
        terminal.process_cleanup = None
        terminal.process_handle = Mock(value=234)
        terminal.window = {"hwnd": 123, "pid": 456, "title": "TEST-Pi-origin-process"}
        terminal.window_closed, terminal.exit_path = False, root / "process.exit"
        terminal.cleanup_stage_failures = []
        return terminal

    def test_process_and_window_refusals_are_retained_without_masking_first_failure(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-nested-cleanup-") as directory:
            native = Mock(side_effect=[{"hwnd": 123, "pid": 456}, RuntimeError("owned_terminal_identity_changed")])
            native.held_process.side_effect = RuntimeError("owned_process_cleanup_timeout:pid=41:winerror=5")
            terminal = self.terminal(Path(directory), native)
            handle = terminal.process_handle
            with self.assertRaisesRegex(RuntimeError, "^owned_process_cleanup_timeout:pid=41:winerror=5$"):
                terminal.close()
            self.assertEqual(terminal.cleanup_stage_failures, [
                {"stage": "process_collection", "failure": "owned_process_cleanup_timeout:pid=41:winerror=5"},
                {"stage": "window_close", "failure": "owned_terminal_identity_changed"}])
            handle.close.assert_called_once_with()
            self.assertFalse(terminal.closed)

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

    def test_window_retry_preserves_already_collected_original_process(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-process-cleanup-") as directory:
            native = Mock(side_effect=[{"hwnd": 123, "pid": 456}, RuntimeError("owned_terminal_identity_changed")])
            native.held_process.return_value = {"collected": True, "descendants_collected": 0}
            terminal = self.terminal(Path(directory), native)
            handle = terminal.process_handle
            with self.assertRaisesRegex(RuntimeError, "owned_terminal_identity_changed"):
                terminal.close()
            self.assertFalse(terminal.closed)
            self.assertIsNone(terminal.process_handle)
            native.side_effect = None
            native.return_value = {"hwnd": 0}
            terminal.close()
            self.assertTrue(terminal.closed)
            self.assertTrue(terminal.window_closed)
            native.held_process.assert_called_once()
            handle.close.assert_called_once()

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

    @unittest.skipUnless(os.name == "nt" and os.environ.get("PI_WINDOWS_OBSERVER_TEST_PATH"), "Actual native window identity guard only")
    def test_absent_window_close_is_idempotent_but_live_foreign_window_refuses(self):
        import ctypes
        from ctypes import wintypes
        native = W.Native(Path(os.environ["PI_WINDOWS_OBSERVER_TEST_PATH"]))
        self.assertIs(native("close", 0, 0, "TEST-Pi-origin-absent")["already_absent"], True)
        user = ctypes.WinDLL("user32", use_last_error=True)
        user.GetDesktopWindow.restype = wintypes.HWND
        desktop = user.GetDesktopWindow()
        self.assertTrue(desktop)
        with self.assertRaisesRegex(RuntimeError, "owned_terminal_identity_changed"):
            native("close", desktop, 0, "TEST-Pi-origin-foreign")


if __name__ == "__main__":
    unittest.main()
