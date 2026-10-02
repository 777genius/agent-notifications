"""Pure helper preflight deadline regressions.

Load the actual fixture or an explicitly supplied TEST copy. No Popen, helper, FFI,
OpenCode, build, native clock, session or model operation is performed.
The actual helper method runs only to its natural-wait boundary. SHA reads
a real private file; fake elapsed time makes the boundary deterministic.
"""
import hashlib
import importlib.util
from pathlib import Path
import sys
import tempfile
import types
import unittest

fixture_path = (Path(sys.argv.pop(1)).resolve()
                if len(sys.argv) > 1 and not sys.argv[1].startswith('-')
                else Path(__file__).resolve().parents[2] / 'opencode-clock-native-qualification.py')
spec = importlib.util.spec_from_file_location("clock_fixture_under_test", fixture_path)
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class WaitBoundaryReached(Exception):
    pass


class HelperDeadlineTests(unittest.TestCase):
    def setUp(self):
        self.private = tempfile.TemporaryDirectory(prefix="TEST-clock-helper-deadline-")
        self.image = Path(self.private.name) / "helper"
        self.image.write_bytes(b"private independently known helper bytes\n")
        self.expected_sha = hashlib.sha256(self.image.read_bytes()).hexdigest()
        self.now = 0.0
        self.hash_duration = 0.0
        self.hash_calls = 0
        self.launches = 0
        self.launch_started = None
        self.wait_deadline = None
        self.real_time, self.real_sha = fixture.time, fixture.sha

        def real_hash_with_elapsed_time(path):
            self.hash_calls += 1
            result = self.real_sha(path)
            self.now += self.hash_duration
            return result

        fixture.time = types.SimpleNamespace(monotonic=lambda: self.now)
        fixture.sha = real_hash_with_elapsed_time
        owner = fixture.Owned()

        def launch(*args):
            self.launches += 1
            self.launch_started = self.now
            return {"p": types.SimpleNamespace(pid=-1)}

        def stop(state, deadline=None, terminate=True):
            self.assertFalse(terminate)
            self.wait_deadline = deadline
            raise WaitBoundaryReached()

        owner.launch = launch
        owner.stop = stop
        self.owner = owner

    def tearDown(self):
        fixture.time, fixture.sha = self.real_time, self.real_sha
        self.private.cleanup()

    def call(self, deadline=2.0, expected=None):
        self.owner.helper(self.image, Path(self.private.name), {}, deadline,
                          self.expected_sha if expected is None else expected)

    def test_hash_preparation_does_not_consume_command_window(self):
        self.hash_duration = 0.300  # Preparation is longer than the command cap.
        with self.assertRaises(WaitBoundaryReached):
            self.call()
        self.assertEqual(self.hash_calls, 1)
        self.assertEqual(self.launches, 1)
        self.assertAlmostEqual(self.launch_started, 0.300)
        self.assertAlmostEqual(self.wait_deadline - self.launch_started, 0.224)

    def test_hash_consumes_original_operation_budget(self):
        self.hash_duration = 2.001
        with self.assertRaisesRegex(RuntimeError, "^qualification_operation_deadline$"):
            self.call()
        self.assertEqual(self.hash_calls, 1)
        self.assertEqual(self.launches, 0)

    def test_original_deadline_still_shortens_command_window(self):
        self.hash_duration = 0.300
        with self.assertRaises(WaitBoundaryReached):
            self.call(deadline=0.400)
        self.assertAlmostEqual(self.wait_deadline, 0.400)
        self.assertLess(self.wait_deadline - self.launch_started, 0.224)

    def test_expired_operation_does_not_even_hash(self):
        self.now = 2.0
        with self.assertRaisesRegex(RuntimeError, "^qualification_operation_deadline$"):
            self.call()
        self.assertEqual(self.hash_calls, 0)
        self.assertEqual(self.launches, 0)

    def test_hash_mismatch_is_distinct_and_never_launches(self):
        with self.assertRaisesRegex(RuntimeError, "^copied_helper_sha_mismatch$"):
            self.call(expected="0" * 64)
        self.assertEqual(self.hash_calls, 1)
        self.assertEqual(self.launches, 0)


if __name__ == "__main__":
    unittest.main()
