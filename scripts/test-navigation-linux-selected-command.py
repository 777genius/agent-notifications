#!/usr/bin/python3
"""Candidate discovery tolerates Electron title rewriting without changing proof."""
import importlib.util
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


if __name__ == '__main__': unittest.main()
