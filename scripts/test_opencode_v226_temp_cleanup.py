"""Bounded cleanup retries must not hide persistent or unrelated failures."""
import importlib.util
import contextlib
import io
import json
import tempfile
from unittest.mock import patch
from pathlib import Path
import os
import unittest

spec = importlib.util.spec_from_file_location('source_e2e', Path(__file__).with_name('opencode-v226-source-e2e.py'))
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)


def sharing_error(code):
    error = PermissionError('TEST only')
    error.winerror = code
    return error


class CleanupTest(unittest.TestCase):
    def test_retries_access_denied_only_for_owned_sqlite_shm(self):
        root = os.path.abspath('TEST-owned-root')
        target = os.path.join(root, 'data', 'opencode', 'opencode.db-shm')
        calls = []

        def transient():
            calls.append(1)
            if len(calls) == 1:
                error = PermissionError(13, 'TEST only', target)
                error.winerror = 5
                raise error
            return 'closed'

        self.assertEqual(source.cleanup_with_sharing_retry(transient, True, lambda: 0, lambda _: None, root), 'closed')
        self.assertEqual(len(calls), 2)

        for path, windows in ((os.path.join(root, 'other'), True), (target, False)):
            def denied():
                error = PermissionError(13, 'TEST only', path)
                error.winerror = 5
                raise error
            with self.assertRaises(PermissionError):
                source.cleanup_with_sharing_retry(denied, windows, lambda: 0, lambda _: None, root)

        times = iter((0, 5))
        def persistent():
            error = PermissionError(13, 'TEST only', target)
            error.winerror = 5
            raise error
        with self.assertRaises(PermissionError):
            source.cleanup_with_sharing_retry(persistent, True, lambda: next(times), lambda _: None, root)

    def test_retries_access_denied_only_for_owned_bun_module(self):
        root = os.path.abspath('TEST-owned-root')
        target = os.path.join(root, 'temp', '.bun-3388477846-3cba9b8dc4fa4e45.node')
        calls = []

        def transient():
            calls.append(1)
            if len(calls) == 1:
                error = PermissionError(13, 'TEST only', target)
                error.winerror = 5
                raise error
            return 'closed'

        self.assertEqual(source.cleanup_with_sharing_retry(
            transient, True, lambda: 0, lambda _: None, root), 'closed')
        self.assertEqual(len(calls), 2)

        invalid = (
            os.path.join(root, 'other', '.bun-3388477846-3cba9b8dc4fa4e45.node'),
            os.path.join(root, 'temp', '.bun-3388477846-3cba9b8dc4fa4e45.node.txt'),
            os.path.join(root, 'temp', '.bun-bad-3cba9b8dc4fa4e45.node'),
            os.path.join(root, 'temp', '.bun-3388477846-3cba9b8dc4fa4e45.dll'),
            os.path.join(root, 'temp', '..', 'other', '.bun-3388477846-3cba9b8dc4fa4e45.node'),
        )
        for path in invalid:
            with self.subTest(path=path):
                def denied():
                    error = PermissionError(13, 'TEST only', path)
                    error.winerror = 5
                    raise error
                with self.assertRaises(PermissionError):
                    source.cleanup_with_sharing_retry(
                        denied, True, lambda: 0, lambda _: None, root)

        times = iter((0, 5))
        def persistent():
            error = PermissionError(13, 'TEST only', target)
            error.winerror = 5
            raise error
        with self.assertRaises(PermissionError):
            source.cleanup_with_sharing_retry(
                persistent, True, lambda: next(times), lambda _: None, root)

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


    def test_main_redacts_errors_from_temporary_directory_cleanup(self):
        with tempfile.TemporaryDirectory(prefix='TEST-error-boundary-') as root:
            report = Path(root) / 'safe.json'

            class BrokenCleanup:
                def __enter__(self):
                    return root

                def __exit__(self, _kind, _error, _traceback):
                    raise OSError('PRIVATE_cleanup_path_and_name')

            output = io.StringIO()
            with contextlib.ExitStack() as stack:
                stack.enter_context(patch.object(source, 'OwnedTemporaryDirectory', return_value=BrokenCleanup()))
                stack.enter_context(patch.object(source, 'prepare_windows_private_root'))
                stack.enter_context(patch.object(source, 'run', return_value={'status': 'passed_source_only'}))
                stack.enter_context(patch('sys.argv', ['source-e2e', '--archive', str(Path(root) / 'TEST.zip'),
                                                       '--report', str(report)]))
                stack.enter_context(contextlib.redirect_stdout(output))
                result = source.main()
            self.assertEqual(result, 1)
            evidence = json.loads(report.read_text())
            self.assertEqual(evidence['failureType'], 'OSError')
            self.assertEqual(evidence['failureCode'], 'unexpected')
            self.assertFalse(evidence['nativeObserverQualified'])
            self.assertNotIn('PRIVATE_', report.read_text() + output.getvalue())

    def test_delivery_rejects_private_compaction_payload(self):
        class Webhook:
            def __init__(self):
                import threading
                self.lock = threading.Lock()
                self.bodies = [b'{"notification_type":"task_complete","message":"PRIVATE_leak"}']

        with self.assertRaisesRegex(RuntimeError, 'delivery_payload_invalid'):
            source.wait_for_delivery(Webhook(), 'task_complete', 'Task completed', timeout=.01)


if __name__ == '__main__':
    unittest.main()
