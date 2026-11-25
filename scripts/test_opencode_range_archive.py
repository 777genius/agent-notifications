"""Release archive admission regressions; no network or executable launch."""
import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import MagicMock, patch

spec = importlib.util.spec_from_file_location(
    'source_e2e', Path(__file__).with_name('opencode-v226-source-e2e.py'))
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)


class LinuxArchiveTest(unittest.TestCase):
    def test_regular_executable_is_copied_and_duplicate_rejected(self):
        payload = b'TEST executable bytes'
        with tempfile.TemporaryDirectory() as directory, patch.object(source.sys, 'platform', 'linux'):
            root = Path(directory)
            archive = root / 'release.tar.gz'
            target = root / 'opencode'
            for names in (('opencode',), ('opencode', 'nested/opencode')):
                with tarfile.open(archive, 'w:gz') as packed:
                    for name in names:
                        member = tarfile.TarInfo(name)
                        member.size = len(payload)
                        packed.addfile(member, io.BytesIO(payload))
                if len(names) == 1:
                    self.assertEqual(source.extract_official_binary(archive, target),
                                     hashlib.sha256(payload).hexdigest())
                    self.assertEqual(target.read_bytes(), payload)
                    self.assertEqual(target.stat().st_mode & 0o700, 0o700)
                    target.unlink()
                else:
                    with self.assertRaisesRegex(RuntimeError, 'official_executable_not_unique'):
                        source.extract_official_binary(archive, target)
                    self.assertFalse(target.exists())

    def test_size_bound_before_opening_destination(self):
        # Synthetic member metadata exercises the policy without allocating hundreds of MiB.
        with tempfile.TemporaryDirectory() as directory, patch.object(source.sys, 'platform', 'linux'):
            root = Path(directory)
            for size, accepted in ((0, False), (250 * 1024 * 1024 + 1, True),
                                   (350 * 1024 * 1024, True), (350 * 1024 * 1024 + 1, False)):
                with self.subTest(size=size):
                    target = root / 'opencode'
                    member = tarfile.TarInfo('opencode')
                    member.size = size
                    packed = MagicMock()
                    packed.getmembers.return_value = [member]
                    packed.extractfile.return_value = io.BytesIO(b'TEST')
                    with patch.object(source.tarfile, 'open') as opened:
                        opened.return_value.__enter__.return_value = packed
                        if accepted:
                            source.extract_official_binary(root / 'release.tar.gz', target)
                            packed.extractfile.assert_called_once_with(member)
                            target.unlink()
                        else:
                            with self.assertRaisesRegex(RuntimeError, 'official_executable_not_unique'):
                                source.extract_official_binary(root / 'release.tar.gz', target)
                            packed.extractfile.assert_not_called()
                            self.assertFalse(target.exists())


if __name__ == '__main__':
    unittest.main()
