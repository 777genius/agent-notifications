#!/usr/bin/env python3
"""A dirty controller or invalid loader must not replace the public installer."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release_loader', Path(__file__).with_name('stage-released-loader.py'))
loader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(loader)


class ReleaseLoaderTest(unittest.TestCase):
    def test_published_loader_pins_controller_snapshot(self):
        commit = 'a' * 40
        body = b'#!/usr/bin/env bash\ncontroller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"\nprintf "%s\\n" "$controller"\n'
        real_run = subprocess.run
        with tempfile.TemporaryDirectory(prefix='TEST-channel-loader-') as directory:
            target = Path(directory) / 'install.sh'
            with patch.object(loader.subprocess, 'check_output', side_effect=[commit.encode(), body]), \
                    patch.object(loader.subprocess, 'run', wraps=subprocess.run) as run:
                # Only git's cleanliness check is fixture-controlled; Bash syntax
                # and the staged artifact are real executions in a TEST directory.
                run.side_effect = lambda args, **kw: subprocess.CompletedProcess(args, 0) if args[0] == 'git' else real_run(args, **kw)
                loader.stage(target)
            result = subprocess.check_output(['bash', str(target)], env={})
            self.assertEqual(result.decode().strip(), commit)
            self.assertEqual(list(Path(directory).iterdir()), [target])

    def test_failure_preserves_existing_public_installer(self):
        for sha, body, dirty in [('main', b'', False), ('a'*40, b'broken', False), ('a'*40, b'', True)]:
            with self.subTest(sha=sha, dirty=dirty), tempfile.TemporaryDirectory(prefix='TEST-channel-loader-') as directory:
                target = Path(directory) / 'install.sh'
                target.write_bytes(b'current installer')
                with patch.object(loader.subprocess, 'check_output', side_effect=[sha.encode(), body]), \
                        patch.object(loader.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'git') if dirty else None):
                    with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                        loader.stage(target)
                self.assertEqual(target.read_bytes(), b'current installer')
                self.assertEqual(list(Path(directory).iterdir()), [target])


if __name__ == '__main__':
    unittest.main()
