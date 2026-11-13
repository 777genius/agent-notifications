import importlib.util
import io
import json
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
            manifest = json.dumps({'files': ['index.js']}).encode()
            (source.parent / 'package.json').write_bytes(manifest)
            (source.parent / 'README.md').write_bytes(b'documentation')
            (source.parent / 'unpublished.test.js').write_bytes(b'not packed')
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
                        metadata = tarfile.TarInfo('package/package.json')
                        metadata.size = len(manifest)
                        tar.addfile(metadata, io.BytesIO(manifest))
                        readme = tarfile.TarInfo('package/README.md')
                        readme.size = len(b'documentation')
                        tar.addfile(readme, io.BytesIO(b'documentation'))
                        member = tarfile.TarInfo(name)
                        member.size = len(data)
                        tar.addfile(member, io.BytesIO(data))
                    if expected:
                        self.assertTrue(semantic.sdk_contents_match_source(archive, root, revision))
                    else:
                        with self.assertRaises(ValueError):
                            semantic.sdk_contents_match_source(archive, root, revision)
            with tarfile.open(archive, 'w:gz') as tar:
                for name, data in [('package.json', manifest), ('index.js', b'original')]:
                    member = tarfile.TarInfo('package/' + name)
                    member.size = len(data)
                    tar.addfile(member, io.BytesIO(data))
            with self.assertRaisesRegex(ValueError, 'member set'):
                semantic.sdk_contents_match_source(archive, root, revision)


    def test_rejects_subset_of_tracked_sdk_files(self):
        with tempfile.TemporaryDirectory(prefix='TEST-sdk-subset-') as tmp:
            root = Path(tmp)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            package = root / 'sdk/opencode-js'
            package.mkdir(parents=True)
            (package / 'index.js').write_bytes(b'original')
            (package / 'missing.js').write_bytes(b'other')
            manifest = json.dumps({'files': ['index.js', 'missing.js']}).encode()
            (package / 'package.json').write_bytes(manifest)
            subprocess.run(['git', '-C', str(root), 'add', 'sdk/opencode-js'], check=True)
            subprocess.run(['git', '-C', str(root), '-c', 'core.hooksPath=/dev/null',
                            '-c', 'user.name=test', '-c', 'user.email=test@example.invalid',
                            'commit', '-qm', 'TEST full source'], check=True)
            revision = subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip()
            archive = root / 'TEST-partial.tgz'
            with tarfile.open(archive, 'w:gz') as tar:
                metadata = tarfile.TarInfo('package/package.json')
                metadata.size = len(manifest)
                tar.addfile(metadata, io.BytesIO(manifest))
                member = tarfile.TarInfo('package/index.js')
                member.size = len(b'original')
                tar.addfile(member, io.BytesIO(b'original'))
            with self.assertRaisesRegex(ValueError, 'member set'):
                semantic.sdk_contents_match_source(archive, root, revision)


    def test_requires_executable_files_and_refuses_symlink_source(self):
        with tempfile.TemporaryDirectory(prefix='TEST-sdk-modes-') as tmp:
            root = Path(tmp)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            package = root / 'sdk/opencode-js'
            package.mkdir(parents=True)
            (package / 'index.js').write_bytes(b'original')
            (package / 'tool.sh').write_bytes(b'#!/bin/sh\nexit 0\n')
            manifest = json.dumps({'files': ['index.js', 'tool.sh']}).encode()
            (package / 'package.json').write_bytes(manifest)
            subprocess.run(['git', '-C', str(root), 'add', 'sdk/opencode-js'], check=True)
            # Force the Git executable bit even when the Windows worktree lacks it.
            subprocess.run(['git', '-C', str(root), 'update-index', '--chmod=+x',
                            'sdk/opencode-js/tool.sh'], check=True)
            identity = ['-c', 'core.hooksPath=/dev/null', '-c', 'user.name=test',
                        '-c', 'user.email=test@example.invalid']
            subprocess.run(['git', '-C', str(root), *identity, 'commit', '-qm',
                            'TEST regular executable source'], check=True)
            revision = subprocess.check_output(
                ['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip()
            archive = root / 'TEST-modes.tgz'
            with tarfile.open(archive, 'w:gz') as tar:
                metadata = tarfile.TarInfo('package/package.json')
                metadata.size = len(manifest)
                tar.addfile(metadata, io.BytesIO(manifest))
                member = tarfile.TarInfo('package/index.js')
                member.size = len(b'original')
                tar.addfile(member, io.BytesIO(b'original'))
            with self.assertRaisesRegex(ValueError, 'member set'):
                semantic.sdk_contents_match_source(archive, root, revision)
            with tarfile.open(archive, 'w:gz') as tar:
                for name, content in [('package.json', manifest), ('index.js', b'original'),
                                      ('tool.sh', b'#!/bin/sh\nexit 0\n')]:
                    member = tarfile.TarInfo('package/' + name)
                    member.size = len(content)
                    member.mode = 0o755 if name == 'tool.sh' else 0o644
                    tar.addfile(member, io.BytesIO(content))
            self.assertTrue(semantic.sdk_contents_match_source(archive, root, revision))

            # Byte-identical package content is insufficient if a tracked
            # executable loses its executable bit during packaging.
            with tarfile.open(archive, 'w:gz') as tar:
                for name, content in [('package.json', manifest), ('index.js', b'original'),
                                      ('tool.sh', b'#!/bin/sh\nexit 0\n')]:
                    member = tarfile.TarInfo('package/' + name)
                    member.size = len(content)
                    member.mode = 0o644
                    tar.addfile(member, io.BytesIO(content))
            with self.assertRaisesRegex(ValueError, 'executable mode'):
                semantic.sdk_contents_match_source(archive, root, revision)

            # Insert a symlink in the Git tree without needing OS symlink rights.
            link_sha = subprocess.check_output(
                ['git', '-C', str(root), 'hash-object', '-w', '--stdin'],
                input=b'index.js').decode().strip()
            subprocess.run(['git', '-C', str(root), 'update-index', '--add',
                            '--cacheinfo', f'120000,{link_sha},sdk/opencode-js/link.js'],
                           check=True)
            subprocess.run(['git', '-C', str(root), *identity, 'commit', '-qm',
                            'TEST non-regular SDK source'], check=True)
            symlink_revision = subprocess.check_output(
                ['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip()
            with self.assertRaisesRegex(ValueError, 'non-regular entry'):
                semantic.sdk_contents_match_source(archive, root, symlink_revision)


if __name__ == '__main__':
    unittest.main()
