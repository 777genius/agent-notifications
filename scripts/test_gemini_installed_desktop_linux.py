#!/usr/bin/env python3
"""Failure-boundary checks with owned exit/observer workers; no GUI required."""
import importlib.util
from contextlib import ExitStack, redirect_stdout
import io
import json
import os
from pathlib import Path
from types import SimpleNamespace
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name("gemini-installed-desktop-linux.py")
spec = importlib.util.spec_from_file_location("gemini_installed_desktop_tested", SCRIPT)
D = importlib.util.module_from_spec(spec)
spec.loader.exec_module(D)


class ReceiptTests(unittest.TestCase):
    def packet(self):
        return {"attributes": {
            "event.name": "gemini_cli.hook_call",
            "hook_name": "agent-notifications-gemini-after-agent",
            "hook_type": "command", "hook_event_name": "AfterAgent",
            "success": True, "exit_code": 0,
            "hook_input": json.dumps({"cwd": "/TEST/profile", "hook_event_name": "AfterAgent",
                                     "stop_hook_active": False, "session_id": "PRIVATE_SESSION",
                                     "timestamp": "PRIVATE_TIMESTAMP", "prompt": "PRIVATE_PROMPT"})}}

    def project(self, packets):
        return D.sdk_projection(("\n".join(json.dumps(row) for row in packets)).encode(),
                                Path("/TEST/profile"))

    def test_sdk_projection_is_content_free_and_does_not_claim_flush(self):
        result = self.project([{"attributes": {"event.name": "other"}}, self.packet()])
        self.assertEqual(result["owned_AfterAgent_successes"], 1)
        self.assertFalse(result["SDK_flush_complete"])
        self.assertEqual(result["absence_means"], "unknown")
        self.assertNotIn("PRIVATE", json.dumps(result))

    def test_foreign_failed_or_duplicate_hook_cannot_pass(self):
        for field, value in (("hook_name", "an-TEST-AfterAgent"), ("success", False),
                             ("exit_code", 1), ("hook_event_name", "Notification")):
            packet = self.packet()
            packet["attributes"][field] = value
            with self.subTest(field=field), self.assertRaises(D.Failed):
                self.project([packet])
        with self.assertRaises(D.Failed):
            self.project([self.packet(), self.packet()])
        with self.assertRaises(D.Failed):
            D.sdk_projection(b'{"attributes":', Path("/TEST/profile"))

    def test_native_cwd_mismatch_is_rejected(self):
        with self.assertRaises(D.Failed):
            D.sdk_projection(json.dumps(self.packet()).encode(), Path("/TEST/foreign"))


class FocusTests(unittest.TestCase):
    def test_negative_observation_requires_full_window(self):
        clock = [0.0]
        expected = {"window": "1", "keyboard_pid": 2}
        result = D.unchanged_focus(lambda: dict(expected), expected, seconds=15,
                                  now=lambda: clock[0],
                                  pause=lambda value: clock.__setitem__(0, clock[0] + value))
        self.assertGreaterEqual(result["seconds"], 15)
        self.assertGreater(result["samples"], 250)
        self.assertTrue(result["unchanged_at_observed_samples"])
        self.assertEqual(result["observation_method"], "sampled_polling")
        self.assertFalse(result["continuous_transition_proof"])
        self.assertEqual(result["requested_sleep_max_seconds"], 0.05)

    def test_transient_wrong_focus_cannot_be_hidden_by_later_recovery(self):
        clock = [0.0]
        def observe():
            return "wrong" if 0.1 <= clock[0] < 0.2 else "distractor"
        with self.assertRaises(D.Failed):
            D.unchanged_focus(observe, "distractor", seconds=1,
                              now=lambda: clock[0],
                              pause=lambda value: clock.__setitem__(0, clock[0] + value))


    def test_positive_receipt_rejects_focus_changed_after_successful_wait(self):
        expected = {"window": "1", "keyboard_pid": 2}
        for changed in ({"window": "3", "keyboard_pid": 2},
                        {"window": "1", "keyboard_pid": 4}):
            reads = iter([dict(expected), changed])
            with self.subTest(changed=changed):
                self.assertTrue(D.N.wait_for(lambda: next(reads) == expected, seconds=1))
                with self.assertRaisesRegex(D.Failed, "click_focus_receipt_mismatch"):
                    D.validated_focus_sample(lambda: next(reads), expected)

    def test_negative_receipt_rejects_change_after_full_observation_window(self):
        clock, completed = [0.0], [False]
        expected = {"window": "1", "keyboard_pid": 2}
        def observe():
            if completed[0]:
                return {"window": "3", "keyboard_pid": 4}
            completed[0] = clock[0] >= 15
            return dict(expected)
        result = D.unchanged_focus(observe, expected, seconds=15,
                                  now=lambda: clock[0],
                                  pause=lambda value: clock.__setitem__(0, clock[0] + value))
        self.assertGreaterEqual(result["seconds"], 15)
        with self.assertRaisesRegex(D.Failed, "click_focus_receipt_mismatch"):
            D.validated_focus_sample(observe, expected)

    def test_negative_window_starts_after_native_delivery(self):
        clock, proof_at, observations, order = [0.0], [], [], []
        expected = {"window": "1", "keyboard_pid": 2}
        def delivered(row):
            order.append("receipt")
            clock[0] += 7
            proof_at.append(clock[0])
            return {"native_click_delivery": "observed"}
        def focus():
            observations.append(clock[0])
            return dict(expected)
        desktop = SimpleNamespace(click=lambda button: order.append(("click", button)), focus=focus,
                                  observer=SimpleNamespace(after_click=delivered))
        unchanged = D.unchanged_focus
        def monitor(observe, wanted):
            return unchanged(observe, wanted, now=lambda: clock[0],
                             pause=lambda value: clock.__setitem__(0, clock[0] + value))
        with patch.object(D, "unchanged_focus", side_effect=monitor):
            native, period, final = D.observe_native_click(
                desktop, {"default_action": False}, expected, False)
        self.assertEqual(order, [("click", 2), "receipt"])
        self.assertEqual(native["mouse_button"], 2)
        self.assertEqual(native["input_semantics"], "informational_user_dismissal")
        self.assertGreaterEqual(period["seconds"], 15)
        self.assertGreaterEqual(observations[0], proof_at[0])
        self.assertGreaterEqual(clock[0] - proof_at[0], 15)
        self.assertEqual(final, expected)

    def test_late_native_action_with_wrong_focus_cannot_follow_a_passed_receipt(self):
        expected = {"window": "1", "keyboard_pid": 2}
        current, clock = [dict(expected)], [0.0]
        def delivered(row):
            current[0] = {"window": "3", "keyboard_pid": 4}
            return {"native_click_delivery": "observed"}
        desktop = SimpleNamespace(click=lambda button: None, focus=lambda: current[0],
                                  observer=SimpleNamespace(after_click=delivered))
        unchanged = D.unchanged_focus
        def monitor(observe, wanted):
            return unchanged(observe, wanted, now=lambda: clock[0],
                             pause=lambda value: clock.__setitem__(0, clock[0] + value))
        with patch.object(D, "unchanged_focus", side_effect=monitor):
            with self.assertRaisesRegex(D.Failed, "negative_click_changed_focus"):
                D.observe_native_click(desktop, {"default_action": True}, expected, False)


class ManagedCleanupTests(unittest.TestCase):
    def installation(self):
        obj = object.__new__(D.Installation)
        obj.root, obj.executable = Path("/TEST"), Path("/TEST/candidate")
        obj.env, obj.selected = {}, Path("/TEST/config.json")
        obj.options = SimpleNamespace(binary=str(obj.executable), desktop=True, native_app=None)
        obj.candidate_hash = "candidate"
        obj.registered = obj.install_attempted = True
        obj.cleanup_attempts = []
        return obj

    def test_partial_initialization_does_not_skip_supported_remove(self):
        obj = self.installation()
        self.assertFalse(hasattr(obj, "settings_hash"))
        after = {"generation": 11, "explicitIntent": False, "runtimeEligible": False}
        policy = {"enabled": False, "route": {"geminiNotifications": {"desktop": True, "webhook": True}}}
        with patch.object(obj, "status", side_effect=[{"generation": 7}, after]), \
             patch.object(D, "json_command", return_value=dict(after, reason="disabled")), \
             patch.object(D.G5, "snapshot", return_value=({"policy": policy, "settings_sha256": "settings"}, {})), \
             patch.object(D.G5, "setup") as remove, patch.object(D.G5, "inspect") as inspect:
            with self.assertRaisesRegex(D.Failed, "managed_cleanup_failed"):
                obj.remove()
        remove.assert_called_once_with(D.G0, obj.options, obj.root, obj.env, "remove")
        inspect.assert_called_once_with(D.G0, obj.options, obj.root, obj.env, False)
        self.assertEqual(obj.cleanup_attempts, [
            {"stage": "disable", "status": "failed", "error": "unexpected_AttributeError"},
            {"stage": "remove", "status": "passed"},
            {"stage": "inspect_absent", "status": "passed"}])
        self.assertFalse(obj.registered or obj.install_attempted)

    def test_failed_install_transaction_still_requires_cleanup(self):
        obj = self.installation()
        obj.registered = obj.install_attempted = False
        with patch.object(D.G5, "setup", side_effect=D.Failed("partial_install_failure")):
            with self.assertRaisesRegex(D.Failed, "partial_install_failure"):
                obj.install()
        self.assertFalse(obj.registered)
        self.assertTrue(obj.install_attempted)
        with patch.object(obj, "change", side_effect=D.Failed("policy_unavailable")), \
             patch.object(D.G5, "setup") as remove, patch.object(D.G5, "inspect") as inspect:
            with self.assertRaisesRegex(D.Failed, "managed_cleanup_failed"):
                obj.remove()
        remove.assert_called_once_with(D.G0, obj.options, obj.root, obj.env, "remove")
        inspect.assert_called_once_with(D.G0, obj.options, obj.root, obj.env, False)
        self.assertFalse(obj.registered or obj.install_attempted)

    def test_remove_and_inspection_failures_are_independent_and_fatal(self):
        for remove_error, inspect_error in (
                (D.Failed("remove_transaction_failed"), None),
                (None, D.Failed("absence_not_verified")),
                (OSError("PRIVATE subprocess output"), D.Failed("absence_not_verified"))):
            obj = self.installation()
            with self.subTest(remove=type(remove_error).__name__, inspect=type(inspect_error).__name__), \
                 patch.object(obj, "change", return_value={"enabled": False}) as disable, \
                 patch.object(D.G5, "setup", side_effect=remove_error) as remove, \
                 patch.object(D.G5, "inspect", side_effect=inspect_error) as inspect:
                with self.assertRaisesRegex(D.Failed, "managed_cleanup_failed"):
                    obj.remove()
            disable.assert_called_once_with(False)
            remove.assert_called_once_with(D.G0, obj.options, obj.root, obj.env, "remove")
            inspect.assert_called_once_with(D.G0, obj.options, obj.root, obj.env, False)
            self.assertEqual([row["stage"] for row in obj.cleanup_attempts],
                             ["disable", "remove", "inspect_absent"])
            self.assertEqual([row["status"] for row in obj.cleanup_attempts],
                             ["passed", "failed" if remove_error else "passed",
                              "failed" if inspect_error else "passed"])
            self.assertEqual(obj.registered, inspect_error is not None)
            self.assertEqual(obj.install_attempted, inspect_error is not None)
            self.assertNotIn("PRIVATE", json.dumps(obj.cleanup_attempts))

    def test_success_requires_all_cleanup_stages(self):
        obj = self.installation()
        with patch.object(obj, "change", return_value={"enabled": False}), \
             patch.object(D.G5, "setup"), patch.object(D.G5, "inspect"):
            obj.remove()
        self.assertEqual([row["status"] for row in obj.cleanup_attempts], ["passed"] * 3)
        self.assertFalse(obj.registered or obj.install_attempted)


class RunFailureTests(unittest.TestCase):
    def test_run_removes_registered_installation_and_writes_failed_receipt(self):
        # Native setup and operator processes are mocked, never qualified here.
        # The real run/install/remove orchestration must still collect an owned
        # child and write its own failed report after a post-install exception.
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-run-failure-") as temporary:
            root = Path(temporary).resolve()
            paths = {name: root / name for name in ("candidate", "gemini", "node", "bash")}
            for path in paths.values():
                path.write_bytes(b"TEST unit-only executable identity\n")
            args = SimpleNamespace(candidate=paths["candidate"], gemini_executable=paths["gemini"],
                                   node_executable=paths["node"], hook_shell=paths["bash"],
                                   cli_install_root=root, gemini_version="0.62.0",
                                   source_sha="a" * 40, report=root / "actual-run-report.json")
            order, labs, sentinels = [], [], []
            provider = SimpleNamespace(url="http://127.0.0.1:18791", release=threading.Event(),
                                       server=object(), thread=SimpleNamespace(is_alive=lambda: False))
            provider.__enter__ = lambda: provider
            provider.__exit__ = lambda *unused: order.append("provider_closed")

            def desktop_setup(lab, terminal_lifetime):
                self.assertEqual(terminal_lifetime, 600)
                labs.append(lab)
                lab.env.update(DISPLAY=":TEST", DBUS_SESSION_BUS_ADDRESS="TEST-unit-bus",
                               PULSE_SERVER="TEST-unit-audio")
                names = ("Xvfb", "openbox", "dunst", "xterm", "xdotool", "dbus-daemon", "ffmpeg")
                lab.report["tools"] = {name: {"unit_fixture": True} for name in names}
                lab.report["dunst_click_bindings"] = {"1": "do_action", "2": "close_current"}
                sentinel = D.N.Lab.start(lab, [sys.executable, "-I", "-c",
                                              "import time; time.sleep(60)"])
                sentinels.append(sentinel)
                self.addCleanup(lab.close)
                return {}, None, ["1", "2"]

            def setup(*arguments):
                order.append("setup_" + arguments[-1])

            def inspect(*arguments):
                order.append("inspect_" + str(arguments[-1]))

            def change(installation, enable):
                self.assertTrue(installation.registered)
                order.append("policy_" + str(enable))
                return {"before": 7, "after": 8, "enabled": enable}

            def fail_after_install(lab, argv):
                self.assertEqual(argv, [paths["candidate"], "--daemon"])
                self.assertEqual(order, ["setup_install", "inspect_True", "policy_True"])
                order.append("post_install_failure")
                raise D.Failed("TEST_post_install_failure")

            snapshot = {"settings_sha256": "TEST-settings",
                        "ledger": {"Consumers": {D.G5.CONSUMER: {"Commands": [str(paths["candidate"])]}}}}
            settings = {"hooks": {event: [{"hooks": [{"name": name}]}]
                                   for event, name in D.G5.OWN.items()}}
            patches = (
                patch.object(D, "source_identity", return_value={}),
                patch.object(D.G0, "cli_identity", return_value={"version": "0.62.0"}),
                patch.object(D, "fixture_settings"),
                patch.object(D.ctypes, "CDLL", return_value=SimpleNamespace(prctl=lambda *unused: 0)),
                patch.object(D.N, "linux_desktop", side_effect=desktop_setup),
                patch.object(D.IsolatedWindowOwner, "probe"),
                patch.object(D, "HeldProvider", return_value=provider),
                patch.object(D, "run_command", return_value=(0, b"0.62.0\n", b"")),
                patch.object(D, "json_command", side_effect=lambda argv, cwd, env, **options:
                             {"path": env["AGENT_NOTIFICATIONS_CONFIG"]}),
                patch.object(D.G5, "setup", side_effect=setup),
                patch.object(D.G5, "snapshot", return_value=(snapshot, settings)),
                patch.object(D.G5, "inspect", side_effect=inspect),
                patch.object(D.Installation, "change", new=change),
                patch.object(D.GeminiDesktopLab, "start", new=fail_after_install),
            )
            with ExitStack() as stack, redirect_stdout(io.StringIO()):
                for replacement in patches:
                    stack.enter_context(replacement)
                self.assertEqual(D.run(args), 1)

            report = json.loads(args.report.read_text())
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["failure"], "TEST_post_install_failure")
            self.assertFalse(report["native_product_observations_passed"])
            self.assertEqual(report["managed_cleanup"], "disabled_and_removed")
            self.assertEqual(report["managed_cleanup_attempts"], [
                {"stage": "disable", "status": "passed"},
                {"stage": "remove", "status": "passed"},
                {"stage": "inspect_absent", "status": "passed"}])
            self.assertEqual(order, ["setup_install", "inspect_True", "policy_True", "post_install_failure",
                                     "policy_False", "setup_remove", "inspect_False", "provider_closed"])
            self.assertTrue(provider.release.is_set())
            self.assertTrue(report["provider_thread_collected"])
            self.assertTrue(report["owned_direct_children_collected"])
            self.assertTrue(report["private_fixture_removed"])
            self.assertNotIn("cleanup_failures", report)
            self.assertEqual(len(sentinels), 1)
            self.assertIsNotNone(sentinels[0].poll())
            self.assertFalse(labs[0].root.parent.exists())


class ExitObserverTests(unittest.TestCase):
    def observe(self, receipt, code):
        bash = shutil.which("bash")
        self.assertIsNotNone(bash, "The native Linux fixture requires Bash")
        return subprocess.run(
            [bash, "-c", D.CLI_EXIT_OBSERVER, "TEST-gemini-exit-observer", str(receipt),
             bash, "-c", "exit " + str(code)],
            env={"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"}, cwd=receipt.parent,
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            timeout=5, check=False)

    def test_real_child_exit_and_spaced_path_are_preserved(self):
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-exit-") as temporary:
            for code in (0, 7):
                receipt = Path(temporary) / ("exit with spaces " + str(code) + ".txt")
                result = self.observe(receipt, code)
                self.assertEqual(result.returncode, code)
                self.assertEqual(receipt.read_bytes(), (str(code) + "\n").encode())
                self.assertEqual(result.stdout + result.stderr, b"")

    def test_existing_exit_receipt_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-exit-") as temporary:
            receipt = Path(temporary) / "prior receipt.txt"
            receipt.write_bytes(b"prior evidence\n")
            result = self.observe(receipt, 0)
            self.assertEqual(result.returncode, 125)
            self.assertEqual(receipt.read_bytes(), b"prior evidence\n")


class ProvenanceTests(unittest.TestCase):
    def build(self, revision="a" * 40, modified="false"):
        fields = {"vcs": "git", "vcs.revision": revision, "vcs.modified": modified,
                  "GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "1"}
        return "\n".join("\tbuild\t" + key + "=" + value for key, value in fields.items())

    def test_candidate_identity_is_observed_not_assigned(self):
        self.assertEqual(D.vcs_fields(self.build(), "a" * 40, "amd64")["vcs.revision"], "a" * 40)
        for output in (self.build("b" * 40), self.build(modified="true"), ""):
            with self.assertRaises(D.Failed):
                D.vcs_fields(output, "a" * 40, "amd64")

    def test_generation_does_not_accept_boolean_or_zero(self):
        for value in (True, False, 0, -1, "5", None):
            with self.subTest(value=value), self.assertRaises(D.Failed):
                D.generation({"generation": value})

    def test_enable_rereads_generation_and_preserves_gemini_hooks(self):
        obj = object.__new__(D.Installation)
        obj.executable, obj.root = Path("/TEST/runtime/notify"), Path("/TEST")
        obj.env, obj.selected = {}, Path("/TEST/config.json")
        obj.candidate_hash, obj.settings_hash = "candidate", "settings"
        after = {"generation": 11, "explicitIntent": True, "runtimeEligible": True}
        policy = {"enabled": True, "route": {"geminiNotifications": {"desktop": True, "webhook": True}}}
        with patch.object(obj, "status", side_effect=[{"generation": 7}, after]), \
             patch.object(D, "json_command", return_value=dict(after, reason="enabled")) as command, \
             patch.object(D.G5, "snapshot", return_value=({"policy": policy, "settings_sha256": "settings"}, {})):
            observed = obj.change(True)
        self.assertEqual(observed, {"before": 7, "after": 11, "enabled": True})
        argv = command.call_args.args[0]
        self.assertEqual(argv[argv.index("--expected-generation") + 1], "7")
        self.assertEqual(argv[argv.index("--navigation") + 1], "none")
        self.assertEqual(argv[argv.index("--allow-unknown-caller") + 1], "false")
        self.assertEqual(argv[argv.index("--allow-caller-asserted") + 1], "false")


class NativeNotifyTests(unittest.TestCase):
    def observer(self):
        obj = object.__new__(D.NotifyObserver)
        obj.dbus = SimpleNamespace(lowlevel=SimpleNamespace(MESSAGE_TYPE_METHOD_CALL=1,
            MESSAGE_TYPE_METHOD_RETURN=2, MESSAGE_TYPE_SIGNAL=4, HANDLER_RESULT_HANDLED=0))
        obj.context = SimpleNamespace(pending=lambda: False)
        obj.connections = [SimpleNamespace(get_is_connected=lambda: True)]
        obj.owner, obj.calls, obj.actions, obj.closed, obj.error = ":1.7", [], [], [], None
        obj.dunst = SimpleNamespace(poll=lambda: None)
        obj.bus = SimpleNamespace(GetNameOwner=lambda *a, **k: obj.owner)
        return obj

    def arguments(self, strict=True):
        return ["claude-notifications" if strict else "agent-notifications", 0, "", "Gemini CLI",
                D.G5.COPY["task_complete"], ["default", "Focus Terminal"] if strict else [], {}, 30000]

    def message(self, kind, sender, signature, arguments, serial=0, reply=0, destination=None,
                member=None):
        if kind == 1 and destination is None:
            destination = "org.freedesktop.Notifications"
        return SimpleNamespace(get_type=lambda: kind, get_sender=lambda: sender,
            get_signature=lambda: signature, get_args_list=lambda: arguments,
            get_serial=lambda: serial, get_reply_serial=lambda: reply,
            get_destination=lambda: destination, get_path=lambda: "/org/freedesktop/Notifications",
            get_interface=lambda: "org.freedesktop.Notifications",
            get_member=lambda: member or ("ActionInvoked" if kind == 4 else "Notify"))

    def test_informational_fallback_cannot_satisfy_revocation_precondition(self):
        obj = self.observer()
        row = D.notify_call_projection("susssasa{sv}i", self.arguments(False), ":1.10", 3)
        row["id"] = 8
        obj.calls.append(row)
        with self.assertRaisesRegex(D.Failed, "native_banner_action_precondition_failed"):
            obj.before_click(0, True)
        wrong = self.arguments(False)
        wrong[5] = ["default", "Focus Terminal"]
        with self.assertRaises(D.Failed):
            D.notify_call_projection("susssasa{sv}i", wrong, ":1.10", 3)

    def test_notify_reply_join_uses_sender_and_serial(self):
        obj = self.observer()
        for sender in (":1.10", ":1.11"):
            result = obj.message(None, self.message(1, sender, "susssasa{sv}i", self.arguments(), serial=3))
            self.assertIs(result, obj.dbus.lowlevel.HANDLER_RESULT_HANDLED)
        obj.message(None, self.message(2, ":1.99", "u", [8], reply=3, destination=":1.10"))
        self.assertEqual([row["id"] for row in obj.calls], [None, None])
        obj.message(None, self.message(2, obj.owner, "u", [8], reply=3, destination=":1.10"))
        self.assertEqual([row["id"] for row in obj.calls], [8, None])
        obj.message(None, self.message(2, obj.owner, "u", [9], reply=3, destination=":1.11"))
        self.assertEqual([row["id"] for row in obj.calls], [8, 9])
        self.assertIsNone(obj.error)

    def test_action_requires_held_dunst_owner_and_original_destination(self):
        obj = self.observer()
        row = D.notify_call_projection("susssasa{sv}i", self.arguments(), ":1.10", 3)
        row["id"] = 8
        obj.calls.append(row)
        obj.message(None, self.message(4, ":1.99", "us", [8, "default"], destination=":1.10"))
        self.assertEqual(obj.actions, [])
        obj.message(None, self.message(4, obj.owner, "us", [8, "default"], destination=":1.11"))
        with self.assertRaisesRegex(D.Failed, "real_dunst_action_destination_mismatch"):
            obj.after_click(row)
        obj.actions.clear()
        result = obj.message(None, self.message(4, obj.owner, "us", [8, "default"], destination=":1.10"))
        self.assertIs(result, obj.dbus.lowlevel.HANDLER_RESULT_HANDLED)
        self.assertTrue(obj.after_click(row)["authentic_dunst_action_observed"])

    def test_malformed_monitored_call_is_consumed_and_fails_closed(self):
        obj = self.observer()
        result = obj.message(None, self.message(1, ":1.10", "s", ["PRIVATE"], serial=3))
        self.assertIs(result, obj.dbus.lowlevel.HANDLER_RESULT_HANDLED)
        self.assertEqual(obj.error, "native_notify_signature")
        with self.assertRaises(D.Failed):
            obj.pump()

    def informational(self):
        obj = self.observer()
        row = D.notify_call_projection("susssasa{sv}i", self.arguments(False), ":1.10", 3)
        row["id"] = 8
        obj.calls.append(row)
        return obj, row

    def close_message(self, obj, identifier=8, reason=2, destination=":1.10", sender=None):
        return self.message(4, sender or obj.owner, "uu", [identifier, reason],
                            destination=destination, member="NotificationClosed")

    @staticmethod
    def immediate_wait(predicate, **options):
        result = predicate()
        if not result:
            raise D.N.Unqualified(options.get("failure", "not_observed"))
        return result

    def test_informational_click_requires_owned_user_dismissal(self):
        obj, row = self.informational()
        obj.before_click(0, False)
        obj.message(None, self.close_message(obj))
        result = obj.after_click(row)
        self.assertTrue(result["informational_user_dismissal_observed"])
        self.assertEqual(result["native_click_delivery"], "NotificationClosed_reason_2")
        self.assertEqual(result["native_user_dismissal"], {"notification_id": 8, "reason": 2})
        self.assertFalse(result["authentic_dunst_action_observed"])

    def test_expiry_api_closure_wrong_destination_or_duplicate_cannot_prove_click(self):
        for reason, destination, duplicate in ((1, ":1.10", False), (3, ":1.10", False),
                                                (4, ":1.10", False), (2, ":1.11", False),
                                                (2, None, False), (2, ":1.10", True)):
            obj, row = self.informational()
            with self.subTest(reason=reason, destination=destination, duplicate=duplicate):
                obj.message(None, self.close_message(obj, reason=reason, destination=destination))
                if duplicate:
                    obj.message(None, self.close_message(obj))
                with self.assertRaisesRegex(D.Failed, "informational_click_not_owned_user_dismissal"):
                    obj.after_click(row)
                self.assertFalse(row["finished"])

    def test_missing_foreign_or_other_id_close_cannot_prove_click(self):
        for sender, identifier in ((":1.99", 8), (":1.7", 9), (None, None)):
            obj, row = self.informational()
            with self.subTest(sender=sender, identifier=identifier):
                if identifier:
                    obj.message(None, self.close_message(obj, sender=sender, identifier=identifier))
                with patch.object(D.N, "wait_for", side_effect=self.immediate_wait):
                    with self.assertRaisesRegex(D.N.Unqualified, "informational_click_delivery_not_observed"):
                        obj.after_click(row)
                self.assertFalse(row["finished"])

    def test_prior_close_or_unexpected_action_cannot_pass_informational_case(self):
        obj, row = self.informational()
        obj.message(None, self.close_message(obj))
        with self.assertRaisesRegex(D.Failed, "native_banner_closed_before_x11_click"):
            obj.before_click(0, False)
        obj.message(None, self.message(4, obj.owner, "us", [8, "default"], destination=":1.10"))
        with self.assertRaisesRegex(D.Failed, "informational_banner_emitted_action"):
            obj.after_click(row)


class XResWorkerTests(unittest.TestCase):
    # Unit-only fault injection. Native runs always use the pinned repo helper.
    HELPER = r'''
import os
from pathlib import Path
import time

trace = Path(__file__).with_suffix(".trace")
def record(phase):
    with trace.open("a") as stream:
        stream.write(str(os.getpid()) + " " + phase + "\n")

class XResWindowOwner:
    def __init__(self, mode):
        self.mode, self.version = mode, [1, 2]
        record("open")
        if mode == "fatal_open":
            os._exit(71)
    def pid(self, window):
        record("query")
        if self.mode == "fatal_query":
            os._exit(72)
        if self.mode == "exception":
            raise RuntimeError("PRIVATE worker failure")
        if self.mode == "timeout":
            time.sleep(60)
        return 7
    def close(self):
        record("close")
        if self.mode == "fatal_close":
            os._exit(73)
'''

    def fixture(self, mode):
        temporary = tempfile.TemporaryDirectory(prefix="TEST-gemini-xres-worker-")
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        lab = D.N.Lab(root, {})
        self.addCleanup(lab.close)
        helper = root / "TEST-xres-helper.py"
        helper.write_text(self.HELPER)
        substitute = patch.object(D, "XRES_OWNER_HELPER", helper)
        substitute.start()
        self.addCleanup(substitute.stop)
        lab.env["DISPLAY"] = mode
        return lab, D.IsolatedWindowOwner(lab), root

    def assert_workers_reaped(self, root):
        records = (root / "TEST-xres-helper.trace").read_text().splitlines()
        self.assertTrue(records)
        for pid in {int(row.split()[0]) for row in records}:
            self.assertNotEqual(pid, os.getpid())
            with self.assertRaises(ChildProcessError):
                os.waitpid(pid, os.WNOHANG)
        return [row.split()[1] for row in records]

    def test_real_worker_closes_before_success_and_is_reaped(self):
        lab, owner, root = self.fixture("success")
        owner.probe()
        self.assertEqual(owner.pid("83"), 7)
        self.assertEqual(owner.version, [1, 2])
        self.assertFalse(owner.report["parent_x11_connection"])
        self.assertEqual([row["status"] for row in owner.report["queries"]], ["passed", "passed"])
        self.assertTrue(all(row["connection_closed"] is True for row in owner.report["queries"]))
        self.assertEqual(self.assert_workers_reaped(root), ["open", "close", "open", "query", "close"])
        lab.close()

    def test_fatal_exit_and_exception_preserve_parent_cleanup_and_receipt(self):
        for mode, exit_code in (("fatal_open", 71), ("fatal_query", 72),
                                ("fatal_close", 73), ("exception", 1)):
            with self.subTest(mode=mode):
                lab, owner, root = self.fixture(mode)
                sentinel = lab.start([sys.executable, "-I", "-c", "import time; time.sleep(60)"])
                try:
                    with self.assertRaisesRegex(D.Failed, "native_window_owner_worker_failed"):
                        owner.pid("83")
                finally:
                    lab.close()
                self.assertIsNotNone(sentinel.poll())
                self.assertTrue(lab.report["owned_children_collected"])
                self.assertEqual(owner.report["queries"][-1]["worker_exit"], exit_code)
                self.assertEqual(owner.report["queries"][-1]["status"], "failed")
                self.assertNotIn("connection_closed", owner.report["queries"][-1])
                self.assertNotIn("PRIVATE", json.dumps(owner.report))
                D.write_json(root / "parent-receipt.json",
                             {"status": "failed", "owner": owner.report, "cleanup_reached": True})
                self.assertTrue(json.loads((root / "parent-receipt.json").read_text())["cleanup_reached"])
                self.assert_workers_reaped(root)

    def test_timed_out_worker_is_collected_before_parent_cleanup(self):
        lab, owner, root = self.fixture("timeout")
        original_run = D.run_command
        def shorter_test_bound(argv, cwd, env, timeout):
            self.assertEqual(timeout, 5)
            return original_run(argv, cwd, env, timeout=1)
        sentinel = lab.start([sys.executable, "-I", "-c", "import time; time.sleep(60)"])
        try:
            with patch.object(D, "run_command", side_effect=shorter_test_bound):
                with self.assertRaisesRegex(D.Failed, "native_window_owner_worker_timeout"):
                    owner.pid("83")
        finally:
            lab.close()
        self.assertIsNotNone(sentinel.poll())
        self.assertTrue(lab.report["owned_children_collected"])
        self.assertEqual(owner.report["queries"][-1]["failure"], "native_window_owner_worker_timeout")
        self.assertNotIn("connection_closed", owner.report["queries"][-1])
        self.assert_workers_reaped(root)

    def test_closed_reply_rejects_missing_identity_and_false_completion(self):
        valid = {"schema_version": 1, "xres_version": [1, 2], "window": 83, "pid": 7,
                 "worker_pid": os.getpid() + 1, "parent_pid": os.getpid(),
                 "owned_unmapped_window_self_check": True, "connection_closed": True}
        self.assertEqual(D.xres_worker_result(json.dumps(valid), 83), valid)
        for field, value in (("schema_version", True), ("xres_version", [1, 1]),
                             ("xres_version", [True, 2]), ("window", 84), ("pid", True),
                             ("pid", 0), ("worker_pid", os.getpid()),
                             ("parent_pid", os.getpid() + 1),
                             ("owned_unmapped_window_self_check", False),
                             ("connection_closed", False), ("extra", "PRIVATE")):
            wrong = dict(valid)
            wrong[field] = value
            with self.subTest(field=field, value=value), self.assertRaises(D.Failed):
                D.xres_worker_result(json.dumps(wrong), 83)
        raw = json.dumps(valid)
        for malformed in ("[]", "{}", raw + raw, '{"schema_version":1,' + raw[1:], " " * 4097):
            with self.subTest(malformed=malformed[:30]), self.assertRaises(D.Failed):
                D.xres_worker_result(malformed, 83)


class BannerOwnershipTests(unittest.TestCase):
    def desktop(self, owner_pid):
        obj = object.__new__(D.Desktop)
        obj.banner_server = SimpleNamespace(pid=7, poll=lambda: None)
        obj.banner_server_identity = {"pid": 7, "birth": 41}
        obj.window_owner = SimpleNamespace(pid=lambda window: owner_pid)
        obj.displayed = lambda: 1
        obj.window_pid = lambda window: self.fail("Banner must not require _NET_WM_PID")
        calls = []
        def command(*argv):
            calls.append(argv)
            return "WINDOW=83\nX=1100\nY=70\n" if argv[0] == "getmouselocation" else ""
        obj.x = command
        return obj, calls

    def test_banner_without_ewmh_pid_uses_server_owner(self):
        for button in (1, 2):
            obj, calls = self.desktop(7)
            with self.subTest(button=button), patch.object(D, "still_present", return_value=True):
                obj.click(button)
            self.assertEqual(calls[-1], ("click", str(button)))

    def test_unknown_foreign_or_expired_server_owner_prevents_click(self):
        for owner_pid, alive in ((9, True), (0, True), (7, False)):
            obj, calls = self.desktop(owner_pid)
            with self.subTest(owner_pid=owner_pid, alive=alive), \
                 patch.object(D, "still_present", return_value=alive):
                with self.assertRaisesRegex(D.Failed, "click_pointer_not_over_owned_banner"):
                    obj.click()
            self.assertNotIn(("click", "1"), calls)

    def test_owned_server_bindings_are_verified_without_forcing_action_close(self):
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-dunst-") as temporary:
            lab = D.GeminiDesktopLab(Path(temporary), {})
            config = lab.root / "dunstrc"
            content = "[global]\nmouse_left_click = do_action\nmouse_middle_click = close_current\n"
            config.write_text(content)
            def started(argv):
                self.assertEqual(config.read_text(), content)
                return "owned_child"
            with patch.object(D.N.Lab, "start", side_effect=started):
                self.assertEqual(lab.start(["/usr/bin/dunst", "-config", config]), "owned_child")
            self.assertEqual(lab.report["dunst_click_bindings"], {"1": "do_action", "2": "close_current"})

    def test_other_config_and_unexpected_binding_are_not_rewritten(self):
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-dunst-") as temporary:
            lab = D.GeminiDesktopLab(Path(temporary), {})
            for name, content in (("other.conf", "mouse_left_click = do_action\n"),
                                  ("dunstrc", "mouse_left_click = context\n"),
                                  ("dunstrc", "mouse_left_click = do_action\n")):
                config = lab.root / name
                config.write_text(content)
                with self.subTest(name=name), patch.object(D.N.Lab, "start") as start:
                    with self.assertRaises(D.Failed):
                        lab.start(["/usr/bin/dunst", "-config", config])
                self.assertEqual(config.read_text(), content)
                start.assert_not_called()

    def test_commented_or_other_section_bindings_cannot_satisfy_global_contract(self):
        fixtures = (
            "[global]\n# mouse_left_click = do_action\nmouse_left_click = context\n"
            "mouse_middle_click = close_current\n",
            "[global]\nmouse_left_click = do_action\n# mouse_middle_click = close_current\n"
            "mouse_middle_click = do_action\n",
            "[global]\nmouse_left_click = context\n[rule]\n"
            "mouse_left_click = do_action\nmouse_middle_click = close_current\n",
            "[DEFAULT]\nmouse_left_click = do_action\nmouse_middle_click = close_current\n[global]\n",
            "[global]\nmouse_left_click: do_action\nmouse_middle_click = close_current\n",
        )
        for content in fixtures:
            with self.subTest(content=content), self.assertRaisesRegex(
                    D.Failed, "owned_dunst_binding_mismatch"):
                D.dunst_click_bindings(content)

    def test_duplicate_active_global_bindings_are_rejected(self):
        for duplicate in ("mouse_left_click = context\n", "mouse_middle_click = do_action\n"):
            content = "[global]\nmouse_left_click = do_action\nmouse_middle_click = close_current\n"
            with self.subTest(duplicate=duplicate), self.assertRaisesRegex(
                    D.Failed, "owned_dunst_binding_mismatch"):
                D.dunst_click_bindings(content + duplicate)


if __name__ == "__main__":
    unittest.main()
