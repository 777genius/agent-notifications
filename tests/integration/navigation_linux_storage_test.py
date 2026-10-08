"""Inert storage contract only. Explicit job-owned scratch + separate TEST filesystem required."""
import hashlib, importlib.util, os, pathlib, re, stat, sys, unittest, uuid

# Importing this test never creates a directory, loads the controller or starts an actor.
ROOT = SOURCE = None
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('TEST_storage', pathlib.Path(__file__).parents[2] / 'scripts/navigation-linux-test-storage.py')
storage = importlib.util.module_from_spec(spec); spec.loader.exec_module(storage)


class StorageTest(unittest.TestCase):
    def setUp(self):
        if ROOT is None or SOURCE is None: self.skipTest('explicit owned scratch/source TEST mount required')
        self.scratch = storage.Scratch(ROOT)
        info = SOURCE.lstat()
        self.assertTrue(stat.S_ISDIR(info.st_mode) and info.st_uid == 1000 and stat.S_IMODE(info.st_mode) == 0o700)
        self.assertEqual(SOURCE.resolve(strict=True), SOURCE)
        self.assertTrue(SOURCE.is_relative_to(ROOT) and re.fullmatch(r'TEST-source-filesystem-[0-9a-f]{32}', SOURCE.name))
        rows = [line.split() for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines()]
        mounts = [row for row in rows if row[4] == str(SOURCE)]; self.assertEqual(len(mounts), 1)
        self.assertEqual(mounts[0][mounts[0].index('-') + 1], 'tmpfs')
        space = os.statvfs(SOURCE); self.assertLessEqual(space.f_blocks * space.f_frsize, 64 * 1024**2)
        self.assertNotEqual(info.st_dev, os.fstat(self.scratch.fd).st_dev)  # Real kernel cross-filesystem boundary.
        self.source = SOURCE / ('TEST-input-' + uuid.uuid4().hex); self.dest = 'TEST-copy-' + uuid.uuid4().hex
        self.created = []
        with self.source.open('xb') as stream:
            stream.write(b'A'); stream.seek(3 * 1048576 - 1); stream.write(b'B')
        self.source.chmod(0o444); self.created.append(self.source)
        self.size = self.source.stat().st_size; self.hash = hashlib.sha256(self.source.read_bytes()).hexdigest()

    def tearDown(self):
        for path in reversed(self.created): path.unlink()
        self.scratch.close()

    def test_cross_device_sparse_copy_preserves_original(self):
        before = storage.identity(self.source.stat()); proof = self.scratch.copy(self.source, self.dest, self.hash, self.size)
        target = ROOT / self.dest; self.created.append(target)
        self.assertEqual(before, storage.identity(self.source.stat()))
        self.assertEqual(proof['sourceBefore'], proof['sourceAfter']); self.assertEqual(proof['destinationSHA256'], self.hash)
        self.assertNotEqual(self.source.stat().st_dev, target.stat().st_dev)
        self.assertEqual(target.stat().st_nlink, 1); self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o444)
        self.assertLess(target.stat().st_blocks * 512, self.size)
        self.assertEqual(hashlib.sha256(target.read_bytes()).hexdigest(), self.hash)

    def test_redirect_existing_destination_and_changed_input_rejected(self):
        link = ROOT / self.dest; link.symlink_to(self.source); self.created.append(link)
        with self.assertRaises(RuntimeError): self.scratch.copy(link, 'TEST-redirect-copy', self.hash, self.size)
        with self.assertRaises(FileExistsError): self.scratch.copy(self.source, self.dest, self.hash, self.size)
        self.assertEqual(hashlib.sha256(self.source.read_bytes()).hexdigest(), self.hash)
        link.unlink(); self.created.remove(link)
        self.source.chmod(0o600)
        with self.source.open('r+b') as stream: stream.write(b'C')
        self.source.chmod(0o444)
        with self.assertRaises(RuntimeError): self.scratch.copy(self.source, self.dest, self.hash, self.size)
        self.assertFalse((ROOT / self.dest).exists())

    def test_qualified_archive_mode_preserved(self):
        self.source.chmod(0o664); before = storage.identity(self.source.stat())
        with self.assertRaises(RuntimeError): self.scratch.copy(self.source, self.dest, self.hash, self.size)
        proof = self.scratch.copy(self.source, self.dest, self.hash, self.size, readonly_required=False)
        self.created.append(ROOT / self.dest)
        self.assertEqual(before, storage.identity(self.source.stat())); self.assertEqual(proof['sourceAfter']['mode'], 0o664)
        self.assertEqual(proof['destinationIdentity']['mode'], 0o444)

    def test_wrong_parent_and_kernel_device_rejected(self):
        with self.assertRaisesRegex(RuntimeError, "TEST_scratch_device_required"): storage.require_scratch_device(SOURCE.stat())
        candidate = SOURCE / ('navigation-handoff-stage-TEST-' + uuid.uuid4().hex); candidate.mkdir(mode=0o700)
        try:
            with self.assertRaises(RuntimeError): storage.Scratch(candidate)
        finally: candidate.rmdir()


if __name__ == '__main__':
    if len(sys.argv) != 3: raise RuntimeError('explicit_verified_TEST_scratch_and_source_filesystem_required')
    ROOT, SOURCE = (pathlib.Path(arg).absolute() for arg in sys.argv[1:])
    unittest.main(argv=[sys.argv[0]])
