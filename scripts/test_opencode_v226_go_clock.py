#!/usr/bin/env python3
"""Pure cross-platform Go T0 receipt checks; not native qualification evidence."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    'source_e2e', Path(__file__).with_name('opencode-v226-source-e2e.py'))
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)

BOOT = '11111111-2222-3333-4444-555555555555'
SAMPLE = dict(boot=BOOT, domain='linux-time:4:4026531834',
              kind='linux-boottime', loNS='1000000000',
              hiNS='1001000000', wallUnixNS='1794000000000000000',
              uncertaintyNS='4000000')


class GoClockT0Tests(unittest.TestCase):
    def test_real_platform_clock_kinds_only(self):
        for platform, kind, domain in (
                ('linux', 'linux-boottime', 'linux-time:4:4026531834'),
                ('darwin', 'darwin-monotonic-raw', 'darwin-kernel'),
                ('win32', 'windows-interrupt-precise', 'windows-kernel')):
            with self.subTest(platform=platform):
                value = dict(SAMPLE, kind=kind, domain=domain)
                self.assertTrue(source.valid_native_go_t0_sample(value, platform))
                for other in ('linux', 'darwin', 'win32'):
                    if other != platform:
                        self.assertFalse(source.valid_native_go_t0_sample(value, other))

    def test_malformed_and_unbound_samples_fail_closed(self):
        changes = (
            {'boot': '00000000-0000-0000-0000-000000000000'},
            {'kind': 'windows-qpc'},
            {'domain': 'windows-kernel'},
            {'domain': 'linux-time:0:4026531834'},
            {'domain': 'linux-time:18446744073709551616:4026531834'},
            {'loNS': '01000000000'},
            {'loNS': '-1'},
            {'hiNS': '999999999'},
            {'hiNS': '1200000000'},
            {'wallUnixNS': '0'},
            {'uncertaintyNS': '3999999'},
            {'uncertaintyNS': '9223372036854775808'},
        )
        for change in changes:
            with self.subTest(change=change):
                self.assertFalse(source.valid_native_go_t0_sample(dict(SAMPLE, **change), 'linux'))
        self.assertFalse(source.valid_native_go_t0_sample(None, 'linux'))
        self.assertFalse(source.valid_native_go_t0_sample(dict(SAMPLE, injected=True), 'linux'))
        self.assertFalse(source.valid_native_go_t0_sample(SAMPLE, 'unknown'))
        # There is no sample for unknown or earlier OpenCode images.
        self.assertFalse(source.valid_native_go_t0_sample({}, 'win32'))

    def test_windows_specific_frame_rejections(self):
        valid = dict(SAMPLE, domain='windows-kernel', kind='windows-interrupt-precise')
        self.assertTrue(source.valid_native_go_t0_sample(valid, 'win32'))
        for change in ({'domain': 'darwin-kernel'}, {'kind': 'darwin-monotonic-raw'},
                       {'uncertaintyNS': '5000000'}, {'hiNS': '1500000000'}):
            with self.subTest(change=change):
                self.assertFalse(source.valid_native_go_t0_sample(dict(valid, **change), 'win32'))


if __name__ == '__main__':
    unittest.main()
