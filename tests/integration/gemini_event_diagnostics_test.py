"""Semantic diagnostic hints, not byte equality or delivery authority; pure tests."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import tempfile
import time
import unittest
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location("gemini_native_diagnostic_fixture", Path(__file__).with_name("gemini_native_e2e.py"))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
LINE = "gemini.event.result phase=registration class=unavailable status=none reason=none desktop=none webhook=none\n"


class DiagnosticsTests(unittest.TestCase):
    def frame(self, body):
        value = json.loads(body)
        return json.dumps({"version": 1, "event": value["hook_event_name"],
                           "session_sha256": fixture.digest(value["session_id"].encode()),
                           "timestamp_sha256": fixture.digest(value["timestamp"].encode()), "result": LINE}).encode()

    def test_closed_frame_and_unknown_text(self):
        raw = self.frame('{"hook_event_name":"AfterAgent","session_id":"PRIVATE-session","timestamp":"PRIVATE-time"}')
        self.assertEqual(fixture.diagnostic_frame(raw)["diagnostic"]["phase"], "registration")
        for value in (raw + b"x", b"x" * 513, raw.replace(b'"version": 1', b'"version": true'),
                      raw.replace(b'"version": 1', b'"version": 1, "version": 1'),
                      raw.replace(b'registration', b'/private/secret'),
                      raw.replace(b'"version": 1', b'"version": 1, "path": "PRIVATE"')):
            with self.subTest(value=value[:80]):
                self.assertIsNone(fixture.diagnostic_frame(value))

    def test_namespace_requires_private_existing_control(self):
        if os.name == "nt": self.skipTest("Unix diagnostic channel only")
        with tempfile.TemporaryDirectory(prefix="TEST-gd-", dir="/tmp") as directory:
            root = Path(directory).resolve()
            (root / fixture.MARKER).write_text("owned disposable Gemini native TEST\n")
            (root / "an-control").mkdir(mode=0o755)
            (root / "an-control").chmod(0o755)
            with self.assertRaises(fixture.Red): fixture.EventDiagnosticReceiver(root)
            self.assertFalse((root / "an-control/.gd.sock").exists())

    def test_foreign_passed_descriptors_are_not_a_hint_channel(self):
        if os.name == "nt": self.skipTest("Unix diagnostic channel only")
        import array
        with tempfile.TemporaryDirectory(prefix="TEST-gd-", dir="/tmp") as directory:
            root = Path(directory).resolve()
            (root / fixture.MARKER).write_text("owned disposable Gemini native TEST\n")
            (root / "an-control").mkdir(mode=0o700)
            receiver = fixture.EventDiagnosticReceiver(root)
            sender = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
            fd = os.open(os.devnull, os.O_RDONLY)
            try:
                frame = self.frame('{"hook_event_name":"AfterAgent","session_id":"PRIVATE","timestamp":"PRIVATE"}')
                sender.sendmsg([frame], [(socket.SOL_SOCKET, socket.SCM_RIGHTS, array.array("i", [fd]))], 0, str(receiver.path))
                receiver.wait_count(1, time.monotonic() + 1)
            finally:
                sender.close()
                os.close(fd)
                snapshot = receiver.stop()
            self.assertTrue(snapshot["invalid"])
            self.assertEqual(snapshot["frames"], [])

    def test_passive_probes_share_deadline_and_retain_once(self):
        for mode in ("flush", "deadline", "invalid"):
            with self.subTest(mode=mode):
                clock, calls = [10.0], []
                receiver = SimpleNamespace(stop=lambda: calls.append(("stop", clock[0])))
                def pause(seconds):
                    self.assertGreater(seconds, 0)
                    self.assertLessEqual(seconds, 0.025)
                    clock[0] += seconds
                def observe(lab, capture, *, retain):
                    self.assertIs(capture, receiver)
                    calls.append(("retained" if retain else "probe", clock[0]))
                    complete = mode == "flush" and clock[0] >= 10.04
                    return {"diagnostic_complete": complete,
                            "diagnostic_retryable": mode != "invalid"}
                result = fixture.await_initial_event_diagnostics(None, receiver, 10.06,
                    now=lambda: clock[0], pause=pause, observe=observe)
                self.assertLessEqual(clock[0], 10.06)
                self.assertEqual([kind for kind, _ in calls].count("retained"), 1)
                self.assertEqual([kind for kind, _ in calls][-2:], ["stop", "retained"])
                self.assertTrue(all(kind == "probe" for kind, _ in calls[:-2]))
                self.assertEqual(result["diagnostic_complete"], mode == "flush")
                if mode == "invalid": self.assertEqual(clock[0], 10.0)
                if mode == "deadline": self.assertEqual(clock[0], 10.06)

    def test_actual_datagram_semantic_join_not_raw_byte_equality(self):
        if os.name == "nt": self.skipTest("Unix diagnostic channel only")
        for fault in (None, "changed-body", "different-role", "different-session", "different-timestamp", "duplicate", "unjoined", "malformed", "absent"):
            with self.subTest(fault=fault), tempfile.TemporaryDirectory(prefix="TEST-gd-", dir="/tmp") as directory:
                root = Path(directory).resolve()
                (root / fixture.MARKER).write_text("owned disposable Gemini native TEST\n")
                (root / "an-control").mkdir(mode=0o700)
                private = root / "sdk-private"
                private.mkdir(mode=0o700)
                rows, telemetry, bodies = [], [], []
                cases = (("AfterAgent", "plain"), ("AfterAgent", "equal"), ("Notification", "approve"),
                         ("AfterAgent", "approve"), ("Notification", "deny"), ("Notification", "cancel"),
                         ("AfterAgent", "recovery"))
                for index, (role, case) in enumerate(cases):
                    incoming = {"hook_event_name": role, "cwd": str(root / "profile"), "session_id": "PRIVATE-session",
                                "timestamp": "2026-11-02T15:00:0" + str(index) + "Z", "private_body": "stdin"}
                    body = json.dumps(incoming, separators=(",", ":"))
                    bodies.append(body)
                    rows.append({"event": role, "valid": True, "neutral": True, "case": case,
                                 "session_sha256": fixture.digest(incoming["session_id"].encode()),
                                 "timestamp_sha256": fixture.digest(incoming["timestamp"].encode()),
                                 "payload_sha256": fixture.digest(body.encode())})
                    attr = {"event.name": "gemini_cli.hook_call", "hook_type": "command",
                            "hook_name": "agent-notifications-gemini-" + ("notification" if role == "Notification" else "after-agent"),
                            "hook_event_name": role, "hook_input": json.dumps(dict(incoming, private_body="changed" if fault == "changed-body" else "stdin"), indent=2), "exit_code": 0, "duration_ms": 50, "success": True,
                            "stderr": "PRIVATE stderr is not a diagnostic channel\n" + LINE}
                    telemetry.append({"attributes": attr})
                foreign = json.loads(json.dumps(telemetry[0]))
                foreign["attributes"]["hook_name"] = "foreign-an-TEST-AfterAgent"
                telemetry.append(foreign)
                (root / "events.jsonl").write_text("\n".join(map(json.dumps, rows)) + "\n")
                path = private / "telemetry.json"
                path.write_text("\n".join(map(json.dumps, telemetry)) + "\n")
                path.chmod(0o600)
                receiver = fixture.EventDiagnosticReceiver(root)
                try:
                    if fault != "absent":
                        sender = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
                        try:
                            for index, body in enumerate(bodies):
                                value = json.loads(body)
                                if index == 0:
                                    if fault == "different-role": value["hook_event_name"] = "Notification"
                                    elif fault == "different-session": value["session_id"] = "different"
                                    elif fault == "different-timestamp": value["timestamp"] = "different"
                                sender.sendto(self.frame(json.dumps(value)), str(receiver.path))
                            if fault == "duplicate": sender.sendto(self.frame(bodies[0]), str(receiver.path))
                            if fault == "unjoined": sender.sendto(self.frame('{"hook_event_name":"AfterAgent","session_id":"PRIVATE-foreign","timestamp":"PRIVATE-time"}'), str(receiver.path))
                            if fault == "malformed": sender.sendto(b"PRIVATE malformed", str(receiver.path))
                            receiver.wait_count(8 if fault in ("duplicate", "unjoined") else 7, time.monotonic() + 1)
                        finally:
                            sender.close()
                finally:
                    receiver.stop()
                self.assertFalse(os.path.lexists(receiver.path))
                probe = fixture.capture_sdk_hook_outcomes(root, receiver, retain=False)
                self.assertFalse((root / "sdk-hook-outcomes.json").exists())
                self.assertEqual(list(private.glob("pre-teardown-*.json")), [])
                capture = fixture.capture_sdk_hook_outcomes(root, receiver)
                self.assertEqual(probe["diagnostic_complete"], capture["diagnostic_complete"])
                self.assertEqual(len(list(private.glob("pre-teardown-*.json"))), 1)
                public_text = (root / "sdk-hook-outcomes.json").read_text()
                public = json.loads(public_text)
                self.assertTrue(capture["qualified"])
                self.assertEqual(capture["diagnostic_complete"], fault in (None, "changed-body"))
                self.assertNotEqual(telemetry[0]["attributes"]["hook_input"].encode(), bodies[0].encode())
                self.assertNotIn("PRIVATE", public_text)
                self.assertNotIn(str(root), public_text)
                self.assertNotIn(fixture.digest(b"PRIVATE-session"), public_text)
                self.assertNotIn(fixture.digest(b"2026-11-02T15:00:00Z"), public_text)
                self.assertNotIn(".gd.sock", public_text)
                self.assertNotIn(fixture.digest(bodies[0].encode()), public_text)
                self.assertEqual(public["owned_calls"], 7)
                for outcome in public["outcomes"]:
                    self.assertEqual(len(outcome["event_diagnostics"]), 1 if fault in (None, "changed-body") else 0)
                path = Path(__file__).resolve().parents[2] / "scripts/gemini-native-ci.py"
                ci_spec = importlib.util.spec_from_file_location("gemini_ci_diagnostic_test", path)
                ci = importlib.util.module_from_spec(ci_spec)
                ci_spec.loader.exec_module(ci)
                self.assertTrue(ci.closed_sdk_projection(public, fixture.CASES))
                public["event_diagnostic_capture"]["session_sha256"] = fixture.digest(b"PRIVATE-session")
                self.assertFalse(ci.closed_sdk_projection(public, fixture.CASES))


if __name__ == "__main__":
    unittest.main()
