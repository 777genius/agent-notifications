"""Pure helper preflight and host readiness regressions.

Load the actual fixture or an explicitly supplied TEST copy. No Popen, helper, FFI,
OpenCode, build, native clock, session or model operation is performed.
The actual helper method runs only to its natural-wait boundary. Final custody
runs actual SHA checks over private files; controlled elapsed time determines deadlines.
"""
import hashlib
import io
import importlib.util
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest import mock

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

    def call(self, deadline=2.0, expected=None, diagnostics=None):
        options = {} if diagnostics is None else {"diagnostics": diagnostics}
        self.owner.helper(self.image, Path(self.private.name), {}, deadline,
                          self.expected_sha if expected is None else expected, **options)

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


    def test_expired_admission_reports_round_without_hash(self):
        self.now = 2.0
        diagnostic = {'failureRound': 1}
        with self.assertRaisesRegex(RuntimeError, '^qualification_operation_deadline$'):
            self.call(diagnostics=diagnostic)
        self.assertEqual(diagnostic, {'failurePhase': 'helper_admission', 'failureRound': 1})
        self.assertEqual(self.hash_calls, 0)
        self.assertEqual(self.launches, 0)

    def test_hash_deadline_reports_hash_phase_without_launch(self):
        self.hash_duration = 2.001
        diagnostic = {'failureRound': 2}
        with self.assertRaisesRegex(RuntimeError, '^qualification_operation_deadline$'):
            self.call(diagnostics=diagnostic)
        self.assertEqual(diagnostic, {'failurePhase': 'helper_sha', 'failureRound': 2})
        self.assertEqual(self.hash_calls, 1)
        self.assertEqual(self.launches, 0)


class SafeJSDiagnosticsTests(unittest.TestCase):
    def test_completed_rounds_remain_visible_for_late_parent_failure(self):
        report = {'failurePhase': 'result_wait', 'failureRound': None}
        fixture.copy_js_diagnostics([{'kind': 'loader'}, {'kind': 'clock_result', 'pid': 42,
            'diagnosticStage': 'complete', 'roundCount': 3, 'privateRaw': 'not exported'}], 42, report)
        self.assertEqual(report, {'failurePhase': 'result_wait', 'failureRound': None,
                                 'jsStage': 'complete', 'jsCompletedRounds': 3})

    def test_other_pid_does_not_supply_diagnostics(self):
        report = {}
        fixture.copy_js_diagnostics([{}, {'kind': 'clock_result', 'pid': 99,
            'diagnosticStage': 'complete', 'roundCount': 3}], 42, report)
        self.assertEqual(report, {})

    def test_private_or_malformed_fields_cannot_enter_safe_report(self):
        for stage, rounds in [('/private/runtime/path', 3), ('complete', True), ('complete', 4)]:
            with self.subTest(stage=stage, rounds=rounds):
                report = {}
                with self.assertRaisesRegex(RuntimeError, '^closed_js_diagnostic_required$'):
                    fixture.copy_js_diagnostics([{}, {'kind': 'clock_result', 'pid': 42,
                        'diagnosticStage': stage, 'roundCount': rounds}], 42, report)
                self.assertEqual(report, {})


class PublicEntryReached(Exception):
    pass


class HostReadinessTests(unittest.TestCase):
    """Run the actual run_version through readiness, with no process/socket/native IO."""
    def setUp(self):
        self.private = tempfile.TemporaryDirectory(prefix="TEST-clock-host-ready-")
        self.root = Path(self.private.name)
        self.now = 0.0
        self.calls, self.errors, self.sleeps = [], [], []
        self.report = {}
        self.host = {"p": types.SimpleNamespace(pid=42, poll=lambda: None),
                     "overflow": False, "pipeError": False}
        self.owner = types.SimpleNamespace(launch=lambda *args: self.host, stop=lambda *args, **kwargs: None)

    def tearDown(self):
        self.private.cleanup()

    def sleep(self, delay):
        self.sleeps.append(delay)
        self.now += delay

    def check(self, outcomes, *, version="2.0.21", first_delay=0):
        responses = iter(outcomes)
        def get(base, path, headers, timeout):
            self.calls.append((path, timeout))
            if path != "/api/info" and path != "/global/health":
                raise PublicEntryReached()
            if len(self.calls) == 1:
                self.now += first_delay
            value = next(responses)
            if isinstance(value, int):
                error = fixture.urllib.error.HTTPError(base + path, value, "TEST status", {}, io.BytesIO(b""))
                self.errors.append(error)
                raise error
            return value
        def environment(root):
            (root / "opencode-config").mkdir()
            return {}
        def copyfile(source, target):
            Path(target).write_bytes(b"TEST inert source bytes\n")
        socket = mock.MagicMock()
        socket.__enter__.return_value.getsockname.return_value = ("127.0.0.1", 12345)
        clock = types.SimpleNamespace(monotonic=lambda: self.now, sleep=self.sleep)
        with mock.patch.object(fixture, "time", clock), mock.patch.object(fixture, "get", get), \
             mock.patch.object(fixture, "environment", environment), mock.patch.object(fixture, "sha", return_value="0" * 64), \
             mock.patch.object(fixture.shutil, "copyfile", copyfile), mock.patch.object(fixture.socket, "socket", return_value=socket):
            fixture.run_version(self.root, version, types.SimpleNamespace(os="darwin", arch="amd64"),
                                self.root / "TEST-helper", "0" * 64, "0" * 64, self.owner,
                                self.report, self.root / "TEST-host")

    def test_v2_starting_503_then_exact_ready_reaches_only_public_entry(self):
        with self.assertRaises(PublicEntryReached):
            self.check([503, {"version": "2.0.21", "pid": 42}])
        self.assertEqual([path for path, _ in self.calls[:2]], ["/api/info", "/api/info"])
        self.assertEqual(len(self.calls), 3)
        self.assertEqual(self.sleeps, [1])
        self.assertTrue(self.errors[0].fp.closed)
        self.assertEqual(self.report["failurePhase"], "public_entry")

    def test_non503_http_and_v1_503_are_immediate_refusals(self):
        for status, version in [(401, "2.0.21"), (403, "2.0.21"), (500, "2.0.21"), (503, "1.18.33")]:
            with self.subTest(status=status, version=version):
                with self.assertRaises(fixture.urllib.error.HTTPError) as caught:
                    self.check([status], version=version)
                self.assertEqual(caught.exception.code, status)
                self.assertEqual(self.sleeps, [])
                self.assertEqual(self.report["failurePhase"], "host_ready")
                caught.exception.close()
                # Each subcase owns a fresh isolated root; no earlier fixture state.
                self.tearDown(); self.setUp()

    def test_503_does_not_reset_original_35_second_deadline(self):
        with self.assertRaisesRegex(RuntimeError, "^native_readiness_deadline$"):
            self.check([503] * 35, first_delay=.95)
        self.assertEqual(len(self.calls), 35)
        self.assertAlmostEqual(self.now, 35)
        self.assertAlmostEqual(self.sleeps[-1], .05)
        self.assertTrue(all(0 < delay <= 1 for delay in self.sleeps))
        self.assertTrue(all(error.fp.closed for error in self.errors))

    def test_ready_success_still_rejects_wrong_version_or_pid(self):
        for info in [{"version": "2.0.20", "pid": 42}, {"version": "2.0.21", "pid": 43}]:
            with self.subTest(info=info):
                with self.assertRaisesRegex(RuntimeError, "^native_health_identity_mismatch$"):
                    self.check([info])
                self.assertEqual(len(self.calls), 1)
                self.assertEqual(self.report["failurePhase"], "host_ready")
                self.tearDown(); self.setUp()

    def test_exited_owned_host_refuses_before_http(self):
        self.host["p"].poll = lambda: 1
        with self.assertRaisesRegex(RuntimeError, "^host_exited_or_log_overflow$"):
            self.check([])
        self.assertEqual(self.calls, [])
        self.assertEqual(self.sleeps, [])


class FinalCustodyDeadlineTests(unittest.TestCase):
    """Actual custody hashes over independent bytes, with only elapsed time controlled."""
    def check(self, before, hash_duration):
        with tempfile.TemporaryDirectory(prefix='TEST-final-custody-') as directory:
            image = Path(directory) / 'image'
            image.write_bytes(b'independent TEST custody bytes\n')
            expected = hashlib.sha256(image.read_bytes()).hexdigest()
            now, real_sha, reads = [before], fixture.sha, []
            def timed_hash(path):
                reads.append(path)
                value = real_sha(path)
                now[0] += hash_duration
                return value
            with mock.patch.object(fixture, 'time', types.SimpleNamespace(monotonic=lambda: now[0])), \
                 mock.patch.object(fixture, 'sha', timed_hash):
                try:
                    end = fixture.final_custody_hashes(((image, expected),), 2.0)
                finally:
                    self.reads, self.now = reads, now[0]
            return end

    def test_late_native_result_refuses_before_custody_hash(self):
        with self.assertRaisesRegex(RuntimeError, '^qualification_operation_deadline$'):
            self.check(2.001, 0)
        self.assertEqual(self.reads, [])

    def test_hash_crosses_native_deadline_but_cannot_cross_custody_deadline(self):
        self.assertAlmostEqual(self.check(1.9, .7), 6.9)
        self.assertAlmostEqual(self.now, 2.6)
        self.assertEqual(len(self.reads), 1)
        with self.assertRaisesRegex(RuntimeError, '^qualification_custody_deadline$'):
            self.check(1.9, 5.001)
        self.assertEqual(len(self.reads), 1)


if __name__ == "__main__":
    unittest.main()
