"""The independent installed-Pi webhook observer rejects private/false claims."""
import importlib.util
import os
from pathlib import Path
import shutil
import stat
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
    def test_new_TEST_root_is_empty_private_and_refuses_other_prefixes(self):
        with self.assertRaisesRegex(RuntimeError, "TEST_root_prefix"):
            P.new_test_root("ordinary-user-directory-")
        root = P.new_test_root("TEST-pi-root-")
        self.addCleanup(shutil.rmtree, root)
        self.assertTrue(root.is_absolute() and root.is_dir())
        self.assertEqual(list(root.iterdir()), [])
        if os.name != "nt":
            self.assertEqual(stat.S_IMODE(root.stat().st_mode), 0o700)

    @unittest.skipUnless(os.name == "nt", "actual Windows security descriptors required")
    def test_real_windows_root_and_new_child_have_only_private_inherited_grants(self):
        # Observe native descriptors independently of the writer. These are real
        # filesystem inodes; no mocked product permission acceptance is involved.
        import ctypes
        from ctypes import wintypes
        from collections import Counter
        kernel, api = ctypes.WinDLL("kernel32", use_last_error=True), ctypes.WinDLL("advapi32", use_last_error=True)
        pointer = ctypes.c_void_p
        kernel.GetCurrentProcess.restype = wintypes.HANDLE
        kernel.CloseHandle.argtypes = (wintypes.HANDLE,)
        kernel.LocalFree.argtypes = (pointer,)
        kernel.LocalFree.restype = pointer
        api.OpenProcessToken.argtypes = (wintypes.HANDLE, wintypes.DWORD, ctypes.POINTER(wintypes.HANDLE))
        api.GetTokenInformation.argtypes = (wintypes.HANDLE, ctypes.c_int, pointer, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD))
        api.ConvertSidToStringSidW.argtypes = (pointer, ctypes.POINTER(pointer))
        api.GetNamedSecurityInfoW.argtypes = (wintypes.LPWSTR, ctypes.c_int, wintypes.DWORD, ctypes.POINTER(pointer),
                                            pointer, ctypes.POINTER(pointer), pointer, ctypes.POINTER(pointer))
        api.GetNamedSecurityInfoW.restype = wintypes.DWORD
        api.GetSecurityDescriptorControl.argtypes = (pointer, ctypes.POINTER(wintypes.WORD), ctypes.POINTER(wintypes.DWORD))
        api.GetAce.argtypes = (pointer, wintypes.DWORD, ctypes.POINTER(pointer))

        class ACL(ctypes.Structure):
            _fields_ = [("revision", wintypes.BYTE), ("reserved", wintypes.BYTE), ("size", wintypes.WORD),
                        ("count", wintypes.WORD), ("reserved2", wintypes.WORD)]

        class AllowedACE(ctypes.Structure):
            _fields_ = [("type", wintypes.BYTE), ("flags", wintypes.BYTE), ("size", wintypes.WORD), ("mask", wintypes.DWORD)]

        def sid_text(sid):
            text = pointer()
            self.assertTrue(api.ConvertSidToStringSidW(sid, ctypes.byref(text)))
            try:
                return ctypes.wstring_at(text)
            finally:
                kernel.LocalFree(text)

        token = wintypes.HANDLE()
        self.assertTrue(api.OpenProcessToken(kernel.GetCurrentProcess(), 0x0008, ctypes.byref(token)))
        try:
            size = wintypes.DWORD()
            api.GetTokenInformation(token, 1, None, 0, ctypes.byref(size))
            self.assertTrue(0 < size.value < 65536)
            data = ctypes.create_string_buffer(size.value)
            self.assertTrue(api.GetTokenInformation(token, 1, data, size.value, ctypes.byref(size)))
            current = sid_text(ctypes.cast(data, ctypes.POINTER(pointer))[0])
        finally:
            kernel.CloseHandle(token)

        root = P.new_test_root("TEST-pi-acl-")
        self.addCleanup(shutil.rmtree, root)
        child = root / "new-product-descendant"
        child.mkdir()
        for path, inherited in ((root, False), (child, True)):
            descriptor, owner, dacl = pointer(), pointer(), pointer()
            self.assertEqual(api.GetNamedSecurityInfoW(str(path), 1, 5, ctypes.byref(owner), None,
                                                       ctypes.byref(dacl), None, ctypes.byref(descriptor)), 0)
            try:
                if path == root:
                    self.assertEqual(sid_text(owner), current)
                else:
                    self.assertIn(sid_text(owner), (current, "S-1-5-18", "S-1-5-32-544"))
                self.assertTrue(dacl)
                control, revision = wintypes.WORD(), wintypes.DWORD()
                self.assertTrue(api.GetSecurityDescriptorControl(descriptor, ctypes.byref(control), ctypes.byref(revision)))
                if path == root:
                    self.assertTrue(control.value & 0x1000)  # Root inheritance is protected.
                acl = ctypes.cast(dacl, ctypes.POINTER(ACL)).contents
                self.assertEqual(acl.count, 3)
                principals = []
                for index in range(acl.count):
                    address = pointer()
                    self.assertTrue(api.GetAce(dacl, index, ctypes.byref(address)))
                    ace = ctypes.cast(address, ctypes.POINTER(AllowedACE)).contents
                    self.assertEqual((ace.type, ace.mask, ace.flags & 3), (0, 0x1F01FF, 3))
                    self.assertEqual(bool(ace.flags & 0x10), inherited)
                    principals.append(sid_text(address.value + ctypes.sizeof(AllowedACE)))
                self.assertEqual(Counter(principals), Counter((current, "S-1-5-18", "S-1-5-32-544")))
            finally:
                kernel.LocalFree(descriptor)

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
