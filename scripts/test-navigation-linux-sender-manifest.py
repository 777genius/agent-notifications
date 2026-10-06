#!/usr/bin/python3
"""Sender authenticates the canonical seed wire; no separate sidecar or SDK."""
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).resolve().parents[1] / 'tests/integration/navigation_linux_client_sender_test.py'
spec = importlib.util.spec_from_file_location('TEST_sender', path)
sender = importlib.util.module_from_spec(spec); spec.loader.exec_module(sender)
MANIFEST = (b'{"files":{"client-sender.py":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},'
            b'"frontendSHA256":"TEST frontend","backendSHA256":"TEST backend"}')


class SenderManifestTest(unittest.TestCase):
    def test_canonical_manifest_authenticates_actual_source_bytes(self):
        self.assertIsNone(sender.validate_source_snapshot(b'abc', MANIFEST))
        with self.assertRaises(RuntimeError): sender.validate_source_snapshot(b'changed', MANIFEST)

    def test_missing_or_ambiguous_source_authority_rejected(self):
        for manifest in (MANIFEST.replace(b'client-sender.py', b'other.py'),
                         MANIFEST.replace(b'"files":{', b'"files":{},"files":{'),
                         b'{"files":{}}', b' ' * 8193):
            with self.subTest(manifest=manifest[:80]), self.assertRaises(RuntimeError):
                sender.validate_source_snapshot(b'abc', manifest)


if __name__ == '__main__': unittest.main()
