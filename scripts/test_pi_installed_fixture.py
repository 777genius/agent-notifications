"""The independent installed-Pi webhook observer rejects private/false claims."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
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
    def test_canonical_config_and_native_witness_use_utf8_in_a_real_child(self):
        code = r'''
import importlib.util
import json
import locale
from pathlib import Path
import sys
from types import SimpleNamespace

assert sys.flags.utf8_mode == 0
if sys.platform != "win32":
    locale.setlocale(locale.LC_CTYPE, "C")
def load(path):
    spec = importlib.util.spec_from_file_location(path.stem, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module
fixture = Path(sys.argv[1])
product, native = load(fixture), load(fixture.with_name("pi_native_fixture.py"))
root = Path(sys.argv[3])
product.configure(root / "config.json", Path(sys.argv[2]), SimpleNamespace(url="http://127.0.0.1/TEST"), desktop=False)
cfg = json.loads((root / "config.json").read_bytes().decode("utf-8"))
assert cfg["statuses"]["review_complete"]["title"] == "\U0001f50d Review"
assert cfg["schemaVersion"] == 2 and cfg["notifications"]["desktop"]["enabled"] is False
row = {"type": "TEST-reader-only", "value": "TEST caf\u00e9 \U0001f50d"}
(root / "witness.jsonl").write_bytes((json.dumps(row, ensure_ascii=False) + "\n").encode("utf-8"))
assert native.witness(root) == [row]
'''
        env = dict(os.environ, LC_ALL="C", LANG="C", PYTHONUTF8="0", PYTHONCOERCECLOCALE="0")
        with tempfile.TemporaryDirectory(prefix="TEST-pi-utf8-") as root:
            result = subprocess.run([sys.executable, "-B", "-c", code, str(PATH),
                                     str(PATH.parents[2] / "config/config.json"), root],
                                    env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    text=True, encoding="utf-8", timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_malformed_utf8_defaults_are_rejected(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-invalid-utf8-") as root:
            defaults = Path(root) / "invalid.json"
            defaults.write_bytes(b'{"invalid": "\xff"}')
            with self.assertRaises(UnicodeDecodeError):
                P.configure(Path(root) / "output.json", defaults, None)

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
