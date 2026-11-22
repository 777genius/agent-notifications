#!/usr/bin/env python3
"""A dirty controller or invalid loader must not replace the public installer."""
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError, URLError

spec = importlib.util.spec_from_file_location('release_loader', Path(__file__).with_name('stage-released-loader.py'))
loader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(loader)


class ReleaseLoaderTest(unittest.TestCase):
    def channel_fixture(self, tags):
        return '# agent-notifications-platform-channels-v1\n' + ''.join(
            f'linux\tamd64\t{tag}\t{"a" * 40}\t{"b" * 40}\trelease/platform-linux-windows\n' for tag in tags)

    def test_published_channels_are_all_verified_once_before_staging(self):
        commit = 'a' * 40
        body = b'#!/usr/bin/env bash\ncontroller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"\nprintf "%s\\n" "$controller"\n'
        channels = self.channel_fixture(['v1.2.0', 'v1.1.0', 'v1.2.0'])
        published = {'draft': False, 'published_at': '2026-11-22T00:00:00Z', 'prerelease': True}
        real_run = subprocess.run
        with tempfile.TemporaryDirectory(prefix='TEST-channel-loader-') as directory:
            target = Path(directory) / 'install.sh'
            target.write_bytes(b'current installer')
            with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo', 'GH_TOKEN': 'TEST-secret'}), \
                    patch.object(loader, 'build_opener') as build, \
                    patch.object(loader.subprocess, 'check_output') as check, \
                    patch.object(loader.subprocess, 'run', wraps=subprocess.run) as run:
                opener = build.return_value
                opener.open.side_effect = [io.BytesIO(json.dumps(published).encode()) for _ in range(2)]
                def git_read(args):
                    if args[-1] == 'HEAD':
                        return commit.encode()
                    if args[-1] == commit + ':release-channels.tsv':
                        return channels.encode()
                    self.assertEqual(args[-1], commit + ':bin/setup.sh')
                    self.assertEqual(opener.open.call_count, 2)
                    return body
                check.side_effect = git_read
                run.side_effect = lambda args, **kw: subprocess.CompletedProcess(args, 0) if args[0] == 'git' else real_run(args, **kw)
                loader.stage(target, verify_published=True)
                calls = opener.open.call_args_list
                self.assertEqual([call.args[0].full_url for call in calls], [
                    'https://api.github.com/repos/owner/repo/releases/tags/v1.1.0',
                    'https://api.github.com/repos/owner/repo/releases/tags/v1.2.0'])
                for call in calls:
                    self.assertEqual(call.args[0].get_header('Authorization'), 'Bearer TEST-secret')
                    self.assertEqual(call.kwargs['timeout'], 30)
                self.assertIsInstance(build.call_args.args[0], loader.NoRedirect)
            self.assertEqual(subprocess.check_output(['bash', str(target)], env={}).decode().strip(), commit)

    def test_release_lookup_failures_preserve_existing_installer(self):
        published = b'{"draft":false,"published_at":"2026-11-22T00:00:00Z"}'
        invalid_releases = [None, [], {}, {'draft': True, 'published_at': 'date'},
                            {'draft': 0, 'published_at': 'date'}, {'draft': False},
                            {'draft': False, 'published_at': None}, {'draft': False, 'published_at': ''},
                            {'draft': False, 'published_at': '  '}, {'draft': False, 'published_at': 1}]
        failures = [(json.dumps(value).encode(), 'Refusing unpublished') for value in invalid_releases]
        failures += [(b'', 'Invalid GitHub release JSON'), (b'{broken', 'Invalid GitHub release JSON'),
                     (HTTPError('https://api.github.com/TEST', 404, 'TEST-secret', {}, None), 'HTTP 404'),
                     (HTTPError('https://api.github.com/TEST', 403, 'TEST-secret', {}, None), 'HTTP 403'),
                     (HTTPError('https://api.github.com/TEST', 302, 'TEST-secret', {}, None), 'HTTP 302'),
                     (URLError('TEST-secret'), 'URLError'), (TimeoutError('TEST-secret'), 'TimeoutError')]
        for failure, message in failures:
            with self.subTest(message=message, failure=type(failure).__name__), \
                    tempfile.TemporaryDirectory(prefix='TEST-channel-loader-') as directory:
                target = Path(directory) / 'install.sh'
                target.write_bytes(b'current installer')
                with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo', 'GH_TOKEN': 'TEST-secret'}), \
                        patch.object(loader, 'build_opener') as build, \
                        patch.object(loader.subprocess, 'check_output', side_effect=[
                            b'a' * 40, self.channel_fixture(['v1.1.0', 'v1.2.0']).encode()]) as check, \
                        patch.object(loader.subprocess, 'run') as run:
                    build.return_value.open.side_effect = [io.BytesIO(published),
                        io.BytesIO(failure) if isinstance(failure, bytes) else failure]
                    with self.assertRaisesRegex(ValueError, message) as caught:
                        loader.stage(target, verify_published=True)
                    self.assertNotIn('TEST-secret', str(caught.exception))
                    self.assertEqual(check.call_count, 2)  # No loader read or write after a failed gate.
                    self.assertEqual(run.call_count, 1)  # Only the controller cleanliness check.
                self.assertEqual(target.read_bytes(), b'current installer')
                self.assertEqual(list(Path(directory).iterdir()), [target])

    def test_missing_channels_or_credentials_refuse_before_api(self):
        valid = self.channel_fixture(['v1.1.0'])
        scenarios = [('', 'owner/repo', 'TEST-secret', 'No platform channel'),
                     ('broken', 'owner/repo', 'TEST-secret', 'Invalid platform channel'),
                     (self.channel_fixture(['']), 'owner/repo', 'TEST-secret', 'Invalid platform channel'),
                     (valid, '', 'TEST-secret', 'GITHUB_REPOSITORY'),
                     (valid, 'owner/repo', '', 'GH_TOKEN')]
        for channels, repository, token, message in scenarios:
            with self.subTest(message=message), \
                    patch.dict(os.environ, {'GITHUB_REPOSITORY': repository, 'GH_TOKEN': token}), \
                    patch.object(loader, 'build_opener') as build:
                with self.assertRaisesRegex(ValueError, message):
                    loader.verify_published_channels(channels)
                build.assert_not_called()

    def test_dirty_controller_refuses_before_api(self):
        with tempfile.TemporaryDirectory(prefix='TEST-channel-loader-') as directory:
            target = Path(directory) / 'install.sh'
            target.write_bytes(b'current installer')
            with patch.object(loader.subprocess, 'check_output', return_value=b'a' * 40), \
                    patch.object(loader.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'git')), \
                    patch.object(loader, 'build_opener') as build:
                with self.assertRaises(subprocess.CalledProcessError):
                    loader.stage(target, verify_published=True)
                build.assert_not_called()
            self.assertEqual(target.read_bytes(), b'current installer')

    def test_release_lookup_does_not_redirect_credentials(self):
        request = loader.Request('https://api.github.com/TEST', headers={'Authorization': 'Bearer TEST-secret'})
        self.assertIsNone(loader.NoRedirect().redirect_request(
            request, None, 302, 'Found', {}, 'https://example.invalid/redirect'))

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
