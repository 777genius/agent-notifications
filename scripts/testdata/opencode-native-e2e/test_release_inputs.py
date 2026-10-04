"""Custody regressions only: no builds, native hosts, providers or notifiers."""
import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location('release_inputs', REPO / 'scripts/release-opencode-inputs.py')
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class ReleaseInputsTests(unittest.TestCase):
    def test_reviewed_sdk_index_retains_actual_original_bytes(self):
        archive = REPO / 'opencode-plugin/vendor/universal-agent-plugins-opencode-events-0.3.0.tgz'
        with tempfile.TemporaryDirectory(prefix='TEST-release-inputs-') as directory:
            output = Path(directory).resolve()
            release.reviewed_sdk_index(archive, output)
            body = (output / 'sdk-index.js').read_bytes()
            self.assertEqual(len(body), 311)
            self.assertEqual(hashlib.sha256(body).hexdigest(),
                             '8e0c343aa9ea29bfce3e4d53c0d979ffc447af96c36b10c5253e3a766a6454a5')
            with tarfile.open(archive) as packed:
                self.assertEqual(body, packed.extractfile('package/index.js').read())

    def test_corrupted_raw_sdk_index_rejected_before_output(self):
        with tempfile.TemporaryDirectory(prefix='TEST-release-inputs-') as directory:
            root = Path(directory).resolve()
            archive = root / 'changed.tar.gz'
            with tarfile.open(archive, 'w:gz') as packed:
                member = tarfile.TarInfo('package/index.js')
                member.size = 311
                packed.addfile(member, io.BytesIO(b'x' * 311))
            output = root / 'output'
            output.mkdir()
            with self.assertRaisesRegex(release.r.Unqualified, 'reviewed_sdk_index_differs'):
                release.reviewed_sdk_index(archive, output)
            self.assertEqual(list(output.iterdir()), [])

    def test_qualification_custody_reads_actual_current_go_source(self):
        archive = REPO / 'opencode-plugin/vendor/universal-agent-plugins-opencode-events-0.3.0.tgz'
        with tempfile.TemporaryDirectory(prefix='TEST-release-inputs-') as directory:
            records = release.custody_sources(archive, Path(directory).resolve())
            source = release.r.checked_file(REPO, records['qualificationSource'])
            self.assertEqual(source, REPO / 'internal/opencodecodec/qualification_rows.go')
            self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(),
                             'e879efac04bad423a2e1adbc1b7712b5e9f21d245e5feb66db974a312c08d925')

    def test_archive_is_repeatable_and_contains_only_original_bytes(self):
        with tempfile.TemporaryDirectory(prefix='TEST-release-inputs-') as directory:
            root = Path(directory)
            source = root / 'source'
            source.mkdir()
            (source / 'manifest.json').write_bytes(b'{"custody":true}\n')
            first, second = root / 'first.tar.gz', root / 'second.tar.gz'
            self.assertEqual(release.seal_archive(source, first), release.seal_archive(source, second))
            with tarfile.open(first) as archive:
                self.assertEqual(archive.getnames(), ['manifest.json'])
                self.assertEqual(archive.extractfile('manifest.json').read(), b'{"custody":true}\n')
                self.assertEqual(archive.getmember('manifest.json').mtime, 0)

    def test_even_dangling_symlink_cannot_enter_custody(self):
        with tempfile.TemporaryDirectory(prefix='TEST-release-inputs-') as directory:
            root = Path(directory)
            source = root / 'source'
            source.mkdir()
            (source / 'manifest.json').write_text('{}')
            (source / 'missing').symlink_to(root / 'absent')
            with self.assertRaisesRegex(release.r.Unqualified, 'parent_member_symlink'):
                release.seal_archive(source, root / 'inputs.tar.gz')

    def test_archive_digest_failure_precedes_extraction(self):
        with tempfile.TemporaryDirectory(prefix='TEST-release-inputs-') as directory:
            root = Path(directory).resolve()
            source = root / 'source'
            source.mkdir()
            (source / 'manifest.json').write_text('{}')
            archive = root / 'inputs.tar.gz'
            release.seal_archive(source, archive)
            target = root / 'must-not-exist'
            with self.assertRaisesRegex(release.r.Unqualified, 'parent_archive_hash_mismatch'):
                release.inputs.stage_parent_archive(archive, '0' * 64, target)
            self.assertFalse(target.exists())


if __name__ == '__main__':
    unittest.main()
