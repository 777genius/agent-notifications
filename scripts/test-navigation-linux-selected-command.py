#!/usr/bin/python3
"""Candidate discovery tolerates Electron title rewriting without changing proof."""
import importlib.util
import hashlib
import json
from pathlib import Path
import unittest
import sys
sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location('handoff', Path(__file__).resolve().parents[1] / 'tests/integration/navigation_linux_client_handoff_guest_test.py')
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


class SelectedCommandTest(unittest.TestCase):
    def test_exact_argv_and_observed_title_are_distinct_discovery_hints(self):
        exe = b'/usr/lib/chatgpt/ChatGPT'
        uri = b'codex://threads/00000000-0000-4000-8000-000000000000'
        argv = [exe, b'--ozone-platform=wayland', uri]
        title = [exe + b' --ozone-platform=wayland ' + uri]
        self.assertEqual(len(title[0]), 102)
        self.assertNotEqual(title, argv)  # Old exact-array observer rejects Electron's title.
        self.assertEqual(handoff.selected_command_hint(argv, exe, uri), 'argv')
        self.assertEqual(handoff.selected_command_hint(title, exe, uri), 'process_title')
        for candidate in (argv + [b'--extra'], title + [b''], [title[0] + b' extra'],
                          [title[0].replace(b' --ozone', b'  --ozone')],
                          [exe + b'-other', argv[1], uri], [exe, argv[1], uri + b'-other']):
            with self.subTest(shape=len(candidate)):
                self.assertIsNone(handoff.selected_command_hint(candidate, exe, uri))
        self.assertIsNone(handoff.selected_command_hint(title, exe, uri + b'-other'))
        self.assertIsNone(handoff.selected_command_hint(title, exe + b'-other', uri))


class SelectedEnvironmentTest(unittest.TestCase):
    def measurement(self, sample):
        summary = handoff.selected_environment_measurement(sample, 1327, '15693', 156.99, 157.0)
        data = json.dumps(summary, sort_keys=True).encode()
        self.assertLessEqual(len(data), 1024)
        self.assertLessEqual(len(json.dumps({'selectedEnvironmentSample': summary}).encode()), 8192)
        self.assertNotIn(b'UNLISTED_SENTINEL', data)
        self.assertNotIn(b'PRIVATE_VALUE_SENTINEL', data)
        return summary

    def test_structure_capture_preserves_strict_rejections_and_privacy(self):
        accepted = b'PATH=/usr/bin:/bin\0XDG_ACTIVATION_TOKEN=TEST-token\0DESKTOP_STARTUP_ID=TEST-token\0UNLISTED_SENTINEL=PRIVATE_VALUE_SENTINEL\0'
        self.assertEqual(handoff.parse_selected_environment(accepted)['PATH'], '/usr/bin:/bin')
        summary = self.measurement(accepted)
        self.assertEqual((summary['emptyRecords'], summary['malformedRecords'], summary['duplicateNameExcess']), (0, 0, 0))
        self.assertEqual(summary['fixedOccurrences']['XDG_ACTIVATION_TOKEN'], 1)
        self.assertEqual(summary['fixedOccurrences']['DISPLAY'], 0)  # Diagnostic only, not required admission.
        cases = [
            (b'XDG_ACTIVATION_TOKEN=a\0XDG_ACTIVATION_TOKEN=b\0', 0, 0, 0, 1),
            (b'UNLISTED_SENTINEL=a\0UNLISTED_SENTINEL=b\0', 0, 0, 0, 1),
            (b'broken-record\0', 0, 0, 1, 0),
            (b'A=x\0\0B=y\0', 1, 0, 1, 0),
            (b'A=x\0\0', 1, 1, 1, 0),
            (b'A=x', 0, 0, 0, 0),
            (b'A=' + b'x' * 65534 + b'\0', 0, 0, 0, 0),
            (b'A=\xff\0', 0, 0, 0, 0),
        ]
        for sample, empty, trailing, malformed, duplicate in cases:
            with self.subTest(bytes=len(sample)):
                observed = self.measurement(sample)
                self.assertEqual((observed['emptyRecords'], observed['trailingEmptyRecords'], observed['malformedRecords'], observed['duplicateNameExcess']), (empty, trailing, malformed, duplicate))
                self.assertEqual(observed['sampleBytes'], len(sample))
                self.assertEqual(observed['sampleSHA256'], hashlib.sha256(sample).hexdigest())
                with self.assertRaises((RuntimeError, UnicodeDecodeError)):
                    handoff.parse_selected_environment(sample)
        self.assertEqual(handoff.parse_selected_environment(b'=value\0'), {'': 'value'})
        unavailable = self.measurement(None)
        self.assertEqual(unavailable['outcome'], 'read_unavailable')
        self.assertNotIn('sampleBytes', unavailable)
        overflow = self.measurement(cases[-2][0])
        self.assertEqual((overflow['sampleBound'], overflow['digestScope']), ('overflow_prefix', 'captured_sample'))


if __name__ == '__main__': unittest.main()
