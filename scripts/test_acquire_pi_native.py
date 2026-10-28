import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("acquire_pi", Path(__file__).with_name("acquire-pi-native.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class AcquisitionTests(unittest.TestCase):
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
