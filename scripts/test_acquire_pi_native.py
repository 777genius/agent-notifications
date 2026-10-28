import hashlib
import importlib.util
import io
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
import warnings
import zipfile

SPEC = importlib.util.spec_from_file_location("acquire_pi", Path(__file__).with_name("acquire-pi-native.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class AcquisitionTests(unittest.TestCase):
    def test_flat_windows_zip_retains_executable_and_adjacent_resources(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-windows-flat-") as name:
            root = Path(name)
            archive = root / "windows.zip"
            with zipfile.ZipFile(archive, "w") as bundle:
                bundle.writestr("theme/", b"")
                bundle.writestr("pi.exe", b"TEST inert executable bytes")
                bundle.writestr("theme/dark.json", b'{"TEST":"theme"}')
                bundle.writestr("README.md", b"TEST adjacent resource")
            output = root / "output"
            MODULE.extract(archive, output, layout="windows-flat")
            self.assertEqual((output / "pi/pi.exe").read_bytes(), b"TEST inert executable bytes")
            self.assertEqual((output / "pi/theme/dark.json").read_bytes(), b'{"TEST":"theme"}')
            self.assertEqual((output / "pi/README.md").read_bytes(), b"TEST adjacent resource")
            self.assertEqual({p.name for p in output.iterdir()}, {"pi"})

    def test_rooted_tar_contract_stays_explicit(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-rooted-") as name:
            root = Path(name)
            archive = root / "unix.tar.gz"
            with tarfile.open(archive, "w:gz") as bundle:
                item = tarfile.TarInfo("pi/pi")
                item.mode, item.size = 0o755, 4
                bundle.addfile(item, io.BytesIO(b"TEST"))
            MODULE.extract(archive, root / "rooted")
            self.assertEqual((root / "rooted/pi/pi").read_bytes(), b"TEST")
            with self.assertRaisesRegex(ValueError, "requires ZIP"):
                MODULE.extract(archive, root / "flat", layout="windows-flat")
            self.assertFalse((root / "flat").exists())
            flat = root / "flat.zip"
            with zipfile.ZipFile(flat, "w") as bundle:
                bundle.writestr("pi.exe", b"TEST")
            with self.assertRaisesRegex(ValueError, "noncontained"):
                MODULE.extract(flat, root / "not-inferred")
            self.assertFalse((root / "not-inferred").exists())

    def test_flat_zip_rejects_raw_unsafe_names_before_adding_root(self):
        for path in ("../escape", "a/../escape", "/absolute", "//server/share", "C:/escape", "C:escape",
                     "a:b", "a\\..\\escape", "./pi.exe", "a//pi.exe"):
            with self.subTest(path=path), tempfile.TemporaryDirectory(prefix="TEST-pi-flat-reject-") as name:
                root = Path(name)
                archive = root / "unsafe.zip"
                with zipfile.ZipFile(archive, "w") as bundle:
                    bundle.writestr("pi.exe", b"TEST first safe member")
                    bundle.writestr(path, b"TEST unsafe member")
                with self.assertRaises(ValueError):
                    MODULE.extract(archive, root / "output", layout="windows-flat")
                self.assertFalse((root / "output").exists())
                self.assertFalse((root / "escape").exists())

    def test_flat_zip_preserves_duplicate_and_link_refusals(self):
        for link in (False, True):
            with self.subTest(link=link), tempfile.TemporaryDirectory(prefix="TEST-pi-flat-kind-") as name:
                root = Path(name)
                archive = root / "unsafe.zip"
                with zipfile.ZipFile(archive, "w") as bundle, warnings.catch_warnings():
                    warnings.simplefilter("ignore", UserWarning)  # Deliberately duplicate one ZIP member.
                    bundle.writestr("pi.exe", b"TEST")
                    member = zipfile.ZipInfo("link" if link else "pi.exe")
                    if link:
                        member.external_attr = (stat.S_IFLNK | 0o777) << 16
                    bundle.writestr(member, b"../../escape")
                with self.assertRaisesRegex(ValueError, "symlink|duplicate"):
                    MODULE.extract(archive, root / "output", layout="windows-flat")
                self.assertFalse((root / "output").exists())

    def test_tampered_bytes_and_size_cannot_pass_pin(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-acquire-") as name:
            archive = Path(name) / "archive.tar.gz"
            archive.write_bytes(b"fixed archive bytes")
            pin = {"bytes": archive.stat().st_size, "sha256": hashlib.sha256(archive.read_bytes()).hexdigest()}
            MODULE.verified_archive(archive, pin)
            archive.write_bytes(b"other archive bytes")
            with self.assertRaisesRegex(ValueError, "SHA-256"):
                MODULE.verified_archive(archive, pin)
            archive.write_bytes(b"short")
            with self.assertRaisesRegex(ValueError, "size"):
                MODULE.verified_archive(archive, pin)

    def test_noncontained_and_link_members_are_refused_and_output_removed(self):
        for path, kind in (("pi/../../escape", tarfile.REGTYPE), ("pi/link", tarfile.SYMTYPE), ("elsewhere/pi", tarfile.REGTYPE)):
            with self.subTest(path=path), tempfile.TemporaryDirectory(prefix="TEST-pi-extract-") as name:
                root = Path(name)
                archive = root / "bad.tar.gz"
                with tarfile.open(archive, "w:gz") as bundle:
                    item = tarfile.TarInfo(path)
                    item.type, item.size, item.linkname = kind, 1 if kind == tarfile.REGTYPE else 0, "/outside"
                    bundle.addfile(item, io.BytesIO(b"x") if item.size else None)
                with self.assertRaises(ValueError):
                    MODULE.extract(archive, root / "output")
                self.assertFalse((root / "output").exists())
                self.assertFalse((root / "escape").exists())


if __name__ == "__main__":
    unittest.main()
