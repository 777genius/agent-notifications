#!/usr/bin/env python3
"""Failure-boundary checks; only the owned Bash exit observer starts children."""
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import shutil
import subprocess
import tempfile
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

    def test_transient_wrong_focus_cannot_be_hidden_by_later_recovery(self):
        clock = [0.0]
        def observe():
            return "wrong" if 0.1 <= clock[0] < 0.2 else "distractor"
        with self.assertRaises(D.Failed):
            D.unchanged_focus(observe, "distractor", seconds=1,
                              now=lambda: clock[0],
                              pause=lambda value: clock.__setitem__(0, clock[0] + value))


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
        obj.owner, obj.calls, obj.actions, obj.error = ":1.7", [], [], None
        return obj

    def arguments(self, strict=True):
        return ["claude-notifications" if strict else "agent-notifications", 0, "", "Gemini CLI",
                D.G5.COPY["task_complete"], ["default", "Focus Terminal"] if strict else [], {}, 30000]

    def message(self, kind, sender, signature, arguments, serial=0, reply=0, destination=None):
        if kind == 1 and destination is None:
            destination = "org.freedesktop.Notifications"
        return SimpleNamespace(get_type=lambda: kind, get_sender=lambda: sender,
            get_signature=lambda: signature, get_args_list=lambda: arguments,
            get_serial=lambda: serial, get_reply_serial=lambda: reply,
            get_destination=lambda: destination, get_path=lambda: "/org/freedesktop/Notifications",
            get_interface=lambda: "org.freedesktop.Notifications",
            get_member=lambda: "ActionInvoked" if kind == 4 else "Notify")

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


if __name__ == "__main__":
    unittest.main()
