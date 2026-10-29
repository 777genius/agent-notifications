#!/usr/bin/env python3
"""Immutable release custody must fail closed before running an installer."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('upgrade', Path(__file__).with_name('stable-upgrade-e2e.py'))
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


class AssetRedirects(unittest.TestCase):
    def test_authorization_only_survives_same_origin(self):
        original = upgrade.urllib.request.Request(
            'https://api.github.com/releases/assets/123',
            headers={'Authorization': 'Bearer TEST-synthetic-token', 'Accept': 'application/octet-stream'})
        handler = upgrade.AssetRedirectHandler()
        for target, authorized in (
                ('https://api.github.com/another-asset', True),
                ('https://release-assets.githubusercontent.com/asset', False),
                ('http://api.github.com/asset', False),
                ('https://api.github.com:8443/asset', False)):
            with self.subTest(target=target):
                redirected = handler.redirect_request(original, None, 302, 'Found', {}, target)
                self.assertEqual(redirected.get_header('Authorization'),
                                 'Bearer TEST-synthetic-token' if authorized else None)
                self.assertEqual(redirected.get_header('Accept'), 'application/octet-stream')
                self.assertEqual(original.get_header('Authorization'), 'Bearer TEST-synthetic-token')
                self.assertEqual(original.full_url, 'https://api.github.com/releases/assets/123')
                self.assertEqual(original.get_header('Accept'), 'application/octet-stream')

    def test_fetch_uses_safe_opener(self):
        with patch.object(upgrade.urllib.request, 'build_opener') as build_opener, \
                patch.object(upgrade.urllib.request, 'urlopen', side_effect=AssertionError('unsafe opener')), \
                patch.dict(upgrade.os.environ, {'GH_TOKEN': 'TEST-synthetic-token'}):
            opener = build_opener.return_value
            opener.open.return_value.__enter__.return_value.read.return_value = b'TEST asset'
            self.assertEqual(upgrade.fetch(upgrade.API + '/releases/assets/123', binary=True), b'TEST asset')
            build_opener.assert_called_once()
            self.assertIsInstance(build_opener.call_args.args[0], upgrade.AssetRedirectHandler)
            opener.open.assert_called_once()
            request = opener.open.call_args.args[0]
            self.assertEqual(request.full_url, upgrade.API + '/releases/assets/123')
            self.assertEqual(request.get_header('Authorization'), 'Bearer TEST-synthetic-token')
            self.assertEqual(request.get_header('Accept'), 'application/octet-stream')
            self.assertEqual(opener.open.call_args.kwargs, {'timeout': 90})


class ReleaseCustody(unittest.TestCase):
    def test_one_latest_lookup_then_only_immutable_assets_and_local_hashes(self):
        with tempfile.TemporaryDirectory(prefix='TEST-upgrade-pin-') as scratch:
            root = Path(scratch) / 'pin'
            names = ('claude-notifications-linux-amd64', 'agent-notify-portable-linux-amd64.zip')
            payloads = {name: ('TEST ' + name).encode() for name in names}
            payloads['checksums.txt'] = ''.join(hashlib.sha256(data).hexdigest() + '  ' + name + '\n'
                                                for name, data in payloads.items()).encode()
            assets = [{'id': index, 'name': name, 'digest': 'sha256:' + hashlib.sha256(data).hexdigest()}
                      for index, (name, data) in enumerate(payloads.items(), 10)]
            release = {'id': 123, 'tag_name': 'v1.2.3', 'draft': False, 'prerelease': False,
                       'published_at': '2026-10-01T00:00:00Z', 'assets': assets}
            calls = []

            def fetch(url, binary=False):
                calls.append(url)
                if url == upgrade.API + '/releases/latest':
                    self.assertFalse(binary)
                    return json.dumps(release).encode()
                self.assertTrue(binary)
                asset, = [a for a in assets if url == upgrade.API + '/releases/assets/' + str(a['id'])]
                return payloads[asset['name']]

            args = type('Args', (), {'output': root, 'stable_tag': None})()
            with patch.object(upgrade, 'fetch', fetch):
                upgrade.prepare(args)
            self.assertEqual(calls.count(upgrade.API + '/releases/latest'), 1)
            self.assertEqual(len(calls), 4)
            # Execution must neither consult Latest nor repair changed bytes by
            # downloading again. A stale/tampered prepared artifact is a hard error.
            with patch.object(upgrade, 'fetch', side_effect=AssertionError('execution attempted network')):
                self.assertEqual(upgrade.verify_pin(root / 'stable-pin.json')['release_id'], 123)
                (root / names[0]).write_bytes(b'tampered')
                with self.assertRaisesRegex(AssertionError, 'pinned input changed'):
                    upgrade.verify_pin(root / 'stable-pin.json')

    def test_current_draft_is_not_previous_published_stable(self):
        with tempfile.TemporaryDirectory(prefix='TEST-upgrade-draft-') as scratch:
            args = type('Args', (), {'output': Path(scratch) / 'pin', 'stable_tag': None})()
            release = {'draft': True, 'prerelease': False, 'published_at': None}
            with patch.object(upgrade, 'fetch', return_value=json.dumps(release).encode()):
                with self.assertRaises(AssertionError):
                    upgrade.prepare(args)


if __name__ == '__main__':
    unittest.main()
