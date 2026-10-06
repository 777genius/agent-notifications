#!/usr/bin/python3
"""Archive boundary regressions; never launch a runtime or native notification."""
import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('stage', Path(__file__).with_name('navigation-linux-handoff-runtime-stage.py'))
stage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stage)
PAYLOAD = b'qualified TEST executable bytes\n'
PAYLOAD_SHA = hashlib.sha256(PAYLOAD).hexdigest()


def fixture(extra=()):
    wire = io.BytesIO()
    with tarfile.open(fileobj=wire, mode='w') as archive:
        root = tarfile.TarInfo('.')
        root.type = tarfile.DIRTYPE
        archive.addfile(root)
        executable = tarfile.TarInfo('./bin/TEST')
        executable.mode = 0o755
        executable.size = len(PAYLOAD)
        archive.addfile(executable, io.BytesIO(PAYLOAD))
        for name, kind in extra:
            member = tarfile.TarInfo(name)
            member.type = kind
            member.linkname = '/tmp/TEST-outside'
            archive.addfile(member, io.BytesIO(b''))
    return wire.getvalue()


def parse(data, digest=None, executable_sha=PAYLOAD_SHA):
    return stage.validated_tree(data, digest or hashlib.sha256(data).hexdigest(), 'bin/TEST', executable_sha)


class RuntimeStageTest(unittest.TestCase):
    def test_regular_tree_preserves_exact_executable_bytes(self):
        tree = parse(fixture([('./share', tarfile.DIRTYPE)]))
        self.assertEqual(tree['bin/TEST'], (PAYLOAD, True))
        self.assertEqual(tree['share'], (None, False))

    def test_rejects_links_devices_and_path_aliases(self):
        for name, kind in [('./link', tarfile.SYMTYPE), ('./hard', tarfile.LNKTYPE),
                           ('./device', tarfile.CHRTYPE), ('./pipe', tarfile.FIFOTYPE),
                           ('../outside', tarfile.REGTYPE), ('/absolute', tarfile.REGTYPE),
                           ('./a/../b', tarfile.REGTYPE), ('./a//b', tarfile.REGTYPE),
                           ('./a\\b', tarfile.REGTYPE), ('././a', tarfile.REGTYPE)]:
            with self.subTest(name=name, kind=kind), self.assertRaises(RuntimeError):
                parse(fixture([(name, kind)]))

    def test_rejects_duplicates_and_file_ancestors(self):
        for extra in [[('bin/TEST', tarfile.REGTYPE)], [('./bin/TEST/child', tarfile.REGTYPE)],
                      [('./empty', tarfile.DIRTYPE), ('empty', tarfile.DIRTYPE)]]:
            with self.subTest(extra=extra), self.assertRaises(RuntimeError):
                parse(fixture(extra))

    def test_rejects_wrong_archive_or_executable_identity(self):
        with self.assertRaises(RuntimeError): parse(fixture(), digest='0' * 64)
        with self.assertRaises(RuntimeError): parse(fixture(), executable_sha='0' * 64)

    def test_authentication_failure_emits_no_destination(self):
        with tempfile.TemporaryDirectory(prefix='navigation-runtime-stage-TEST-') as temp:
            root = Path(temp)
            (root / 'portal.tar').write_bytes(fixture())
            with self.assertRaises(RuntimeError): stage.stage_runtime_archives(root, root / 'output')
            self.assertFalse((root / 'output').exists())


if __name__ == '__main__': unittest.main()
