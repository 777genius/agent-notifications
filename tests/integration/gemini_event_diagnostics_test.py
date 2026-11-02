"""Pure synthetic SDK stderr projection; no native CLI or provider execution."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("gemini_native_diagnostic_fixture", Path(__file__).with_name("gemini_native_e2e.py"))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


LINE = "gemini.event.result phase=registration class=unavailable status=none reason=none desktop=none webhook=none"


class DiagnosticsTests(unittest.TestCase):
    def test_closed_labels_and_unknown_text(self):
        expected = [{"phase": "registration", "classification": "unavailable", "status": "none",
                     "reason": "none", "desktop": "none", "webhook": "none"}]
        self.assertEqual(fixture.event_diagnostics("PRIVATE path /secret\n" + LINE + "\n"), expected)
        for value in (LINE + " PRIVATE", LINE.replace("registration", "/private/secret"),
                      LINE.replace("unavailable", "PRIVATE-error"), LINE + "\n" + LINE,
                      "x" * 65537):
            with self.subTest(value=value[:80]):
                self.assertEqual(fixture.event_diagnostics(value), [])

    def test_owned_sdk_case_join_excludes_foreign_diagnostics(self):
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-diag-") as directory:
            root = Path(directory).resolve()
            private = root / "sdk-private"
            private.mkdir(mode=0o700)
            incoming = {"hook_event_name": "Notification", "cwd": str(root / "profile"),
                        "session_id": "PRIVATE-session", "timestamp": "2026-11-02T15:00:00Z"}
            row = {"event": "Notification", "valid": True, "neutral": True, "case": "approve",
                   "session_sha256": fixture.digest(incoming["session_id"].encode()),
                   "timestamp_sha256": fixture.digest(incoming["timestamp"].encode()),
                   "payload_sha256": fixture.digest(json.dumps(incoming).encode())}
            (root / "events.jsonl").write_text(json.dumps(row) + "\n")
            owned = {"attributes": {"event.name": "gemini_cli.hook_call", "hook_type": "command",
                     "hook_name": "agent-notifications-gemini-notification", "hook_event_name": "Notification",
                     "hook_input": json.dumps(incoming), "exit_code": 0, "duration_ms": 50, "success": True,
                     "stderr": "PRIVATE raw stderr\n" + LINE + "\n"}}
            foreign = json.loads(json.dumps(owned))
            foreign["attributes"]["hook_name"] = "foreign-an-TEST-Notification"
            telemetry = private / "telemetry.json"
            telemetry.write_text(json.dumps(foreign) + "\n" + json.dumps(owned) + "\n")
            telemetry.chmod(0o600)
            capture = fixture.capture_sdk_hook_outcomes(root)
            self.assertTrue(capture["qualified"])
            public_text = (root / "sdk-hook-outcomes.json").read_text()
            public = json.loads(public_text)
            self.assertNotIn("PRIVATE", public_text)
            self.assertNotIn(str(root), public_text)
            self.assertEqual(public["owned_calls"], 1)
            self.assertEqual(len(public["outcomes"]), 1)
            outcome = public["outcomes"][0]
            self.assertEqual((outcome["role"], outcome["case"]), ("Notification", "approve"))
            self.assertEqual(outcome["event_diagnostics"][0]["phase"], "registration")
            # Exercise the actual operator's closed upload boundary too.
            path = Path(__file__).resolve().parents[2] / "scripts/gemini-native-ci.py"
            spec = importlib.util.spec_from_file_location("gemini_ci_diagnostic_test", path)
            ci = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(ci)
            self.assertTrue(ci.closed_sdk_projection(public, fixture.CASES))
            outcome["event_diagnostics"][0]["reason"] = "PRIVATE-error"
            self.assertFalse(ci.closed_sdk_projection(public, fixture.CASES))


if __name__ == "__main__":
    unittest.main()
