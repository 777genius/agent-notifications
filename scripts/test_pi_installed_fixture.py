"""The independent installed-Pi webhook observer rejects private/false claims."""
import importlib.util
from pathlib import Path
import unittest

PATH = Path(__file__).resolve().parents[1] / "tests/integration/pi_installed_fixture.py"
SPEC = importlib.util.spec_from_file_location("pi_installed_fixture", PATH)
P = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(P)


def body(status="task_complete"):
    return {"schema_version": "1.0", "status": status, "notification_type": status,
            "agent_source": "pi", "message": P.COPY[status], "timestamp": "2026-10-28T10:00:00Z",
            "session_id": "", "source": "claude-notifications", "title": "Pi"}


class ObserverTests(unittest.TestCase):
    def test_all_fixed_pi_outcomes(self):
        for status in P.COPY:
            self.assertEqual(P.capture(body(status)), status)

    def test_private_native_and_configured_copy_is_refused(self):
        for key, value in (("session_id", "TEST-private-session"), ("message", "TEST-private-provider-error"),
                           ("title", "TEST-private-configured-title")):
            value_body = body()
            value_body[key] = value
            with self.assertRaisesRegex(RuntimeError, "private_copy"):
                P.capture(value_body)

    def test_additive_private_fields_and_other_agent_cannot_pass(self):
        value_body = body()
        value_body["cwd"] = "/TEST-private-project"
        with self.assertRaisesRegex(RuntimeError, "field_whitelist"):
            P.capture(value_body)
        value_body = body()
        value_body["agent_source"] = "claude"
        with self.assertRaisesRegex(RuntimeError, "private_copy"):
            P.capture(value_body)


if __name__ == "__main__":
    unittest.main()
