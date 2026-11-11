"""Bounded cleanup retries must not hide persistent or unrelated failures."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('source_e2e', Path(__file__).with_name('opencode-v226-source-e2e.py'))
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)


def sharing_error(code):
    error = PermissionError('TEST only')
    error.winerror = code
    return error


class CleanupTest(unittest.TestCase):
    def test_retries_windows_sharing_violation_until_cleanup(self):
        calls, delays = [], []

        def cleanup():
            calls.append(1)
            if len(calls) < 3:
                raise sharing_error(32)
            return 'closed'

        self.assertEqual(source.cleanup_with_sharing_retry(cleanup, True, lambda: 0, delays.append), 'closed')
        self.assertEqual((len(calls), delays), (3, [.1, .1]))

    def test_does_not_retry_other_errors_or_non_windows(self):
        for windows, code in ((True, 5), (False, 32)):
            calls = []

            def cleanup():
                calls.append(1)
                raise sharing_error(code)

            with self.assertRaises(PermissionError):
                source.cleanup_with_sharing_retry(cleanup, windows, lambda: 0, lambda _: None)
            self.assertEqual(len(calls), 1)

    def test_persistent_sharing_violation_fails_after_deadline(self):
        times = iter((0, 1, 5))
        calls = []

        def cleanup():
            calls.append(1)
            raise sharing_error(32)

        with self.assertRaises(PermissionError):
            source.cleanup_with_sharing_retry(cleanup, True, lambda: next(times), lambda _: None)
        self.assertEqual(len(calls), 2)


if __name__ == '__main__':
    unittest.main()
