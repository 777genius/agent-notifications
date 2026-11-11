import importlib.util
import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


spec = importlib.util.spec_from_file_location('semantic_e2e', Path(__file__).with_name('opencode-semantic-e2e.py'))
semantic = importlib.util.module_from_spec(spec)
spec.loader.exec_module(semantic)


class SDKSourceTest(unittest.TestCase):
    def test_contents_and_unsafe_archive_entries(self):
        with tempfile.TemporaryDirectory(prefix='TEST-sdk-source-') as tmp:
            root = Path(tmp)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            source = root / 'sdk/opencode-js/index.js'
            source.parent.mkdir(parents=True)
            source.write_bytes(b'original')
            subprocess.run(['git', '-C', str(root), 'add', '.'], check=True)
            subprocess.run(['git', '-C', str(root), '-c', 'core.hooksPath=/dev/null',
                            '-c', 'user.name=iliya', '-c', 'user.email=iliyazelenkog@gmail.com',
                            'commit', '-qm', 'TEST source'], check=True)
            revision = subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip()
            archive = root / 'TEST-sdk.tgz'
            for name, data, expected in [('package/index.js', b'original', True),
                                          ('package/index.js', b'changed', False),
                                          ('package/../escape', b'original', False)]:
                with self.subTest(name=name, expected=expected):
                    with tarfile.open(archive, 'w:gz') as tar:
                        member = tarfile.TarInfo(name)
                        member.size = len(data)
                        tar.addfile(member, io.BytesIO(data))
                    if expected:
                        self.assertTrue(semantic.sdk_contents_match_source(archive, root, revision))
                    else:
                        with self.assertRaises(ValueError):
                            semantic.sdk_contents_match_source(archive, root, revision)


if __name__ == '__main__':
    unittest.main()
