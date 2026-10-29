"""Policy/deadline guards; synthetic observations never qualify a native host."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import Mock, call

PATH = Path(__file__).with_name("pi-windows-notification-readiness.py")
SPEC = importlib.util.spec_from_file_location("pi_windows_notification_readiness", PATH)
W = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(W)


class Clock:
    def __init__(self):
        self.now, self.sleeps = 0, []

    def read(self):
        return self.now

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds


class ReadinessTests(unittest.TestCase):
    def wait(self, states, *, seconds=30, clock=None):
        clock, proof = clock or Clock(), {}
        values = iter(states)
        W.wait_for_readiness(proof, lambda: next(values), seconds=seconds, clock=clock.read,
                             sleep=clock.sleep, emit=lambda *args, **kwargs: None)
        return proof, clock

    def test_actual_accepting_state_needs_no_wait(self):
        proof, clock = self.wait([{"hresult": 0, "state": 5}])
        self.assertEqual(proof["status"], "ready")
        self.assertEqual(proof["sample_count"], 1)
        self.assertEqual(clock.sleeps, [])

    def test_only_quiet_time_can_wait_then_accept_real_transition(self):
        proof, clock = self.wait([{"hresult": 0, "state": value} for value in (6, 6, 5)])
        self.assertEqual(proof["elapsed_seconds"], 30)
        self.assertEqual((proof["last_hresult"], proof["last_state"]), (0, 5))
        self.assertEqual(clock.sleeps, [15, 15])
        self.assertIn("started_at", proof)
        self.assertIn("finished_at", proof)

    def test_unknown_busy_locked_or_error_state_never_waits_or_succeeds(self):
        for value in (0, 1, 2, 3, 4, 7, 8, -1):
            with self.subTest(state=value), self.assertRaisesRegex(RuntimeError, "state_refused"):
                self.wait([{"hresult": 0, "state": value}])
        for code in (-2147467259, 1):
            with self.subTest(hresult=code), self.assertRaisesRegex(RuntimeError, "hresult_refused"):
                self.wait([{"hresult": code, "state": 5}])

    def test_quiet_timeout_keeps_exact_final_observation_and_deadline(self):
        proof, clock = {}, Clock()
        with self.assertRaisesRegex(RuntimeError, "readiness_timeout"):
            W.wait_for_readiness(proof, lambda: {"hresult": 0, "state": 6}, seconds=30,
                                 clock=clock.read, sleep=clock.sleep, emit=lambda *args, **kwargs: None)
        self.assertEqual(proof["status"], "failed")
        self.assertEqual(proof["elapsed_seconds"], 30)
        self.assertEqual(proof["sample_count"], 3)
        self.assertEqual((proof["last_hresult"], proof["last_state"]), (0, 6))

    def test_late_accepting_result_cannot_extend_original_budget(self):
        proof, clock = {}, Clock()
        def late():
            clock.now = 31
            return {"hresult": 0, "state": 5}
        with self.assertRaisesRegex(RuntimeError, "readiness_timeout"):
            W.wait_for_readiness(proof, late, seconds=30, clock=clock.read)
        self.assertEqual(proof["status"], "failed")
        self.assertEqual(proof["last_state"], 5)

    def test_stalled_clock_still_has_finite_sample_bound(self):
        proof = {}
        with self.assertRaisesRegex(RuntimeError, "sample_bound"):
            W.wait_for_readiness(proof, lambda: {"hresult": 0, "state": 6}, seconds=30,
                                 clock=lambda: 0, sleep=lambda _: None, emit=lambda *args, **kwargs: None)
        self.assertEqual(proof["sample_count"], 4)

    def test_execution_request_is_restored_after_ready_and_failed_wait(self):
        for state in (5, 2):
            proof, request, clock = {}, Mock(), Clock()
            try:
                W.wait_with_execution_request(proof, lambda: {"hresult": 0, "state": state}, request,
                                              clock=clock.read, sleep=clock.sleep)
            except RuntimeError:
                self.assertEqual(state, 2)
            request.acquire.assert_called_once_with(proof)
            request.release.assert_called_once_with(proof)
            self.assertEqual(proof["status"], "ready" if state == 5 else "failed")

    def test_native_execution_adapter_restores_exact_previous_state_on_same_thread(self):
        proof = {}
        request = W.ExecutionRequest.__new__(W.ExecutionRequest)
        request.kernel = Mock()
        request.kernel.GetCurrentThreadId.return_value = 17
        request.kernel.SetThreadExecutionState.side_effect = [0x80000002, 0x80000003]
        request.acquire(proof)
        request.release(proof)
        self.assertEqual(request.kernel.SetThreadExecutionState.call_args_list,
                         [call(0x80000003), call(0x80000002)])
        self.assertTrue(proof["execution_request"]["released"])
        request.kernel.GetCurrentThreadId.return_value = 18
        with self.assertRaisesRegex(RuntimeError, "execution_thread_changed"):
            request.release(proof)
        self.assertEqual(request.kernel.SetThreadExecutionState.call_count, 2)

    def test_restore_failure_cannot_replace_original_native_state_refusal(self):
        proof, request, clock = {}, Mock(), Clock()
        request.release.side_effect = RuntimeError("restore_failed")
        with self.assertRaisesRegex(RuntimeError, "state_refused"):
            W.wait_with_execution_request(proof, lambda: {"hresult": 0, "state": 2}, request,
                                          clock=clock.read, sleep=clock.sleep)
        self.assertEqual(proof["execution_cleanup_failure"], "restore_failed")
        self.assertEqual(proof["failure"], "notification_readiness_state_refused")

    def test_restore_failure_after_ready_refuses_and_failed_acquire_is_not_released(self):
        proof, request, clock = {}, Mock(), Clock()
        request.release.side_effect = RuntimeError("restore_failed")
        with self.assertRaisesRegex(RuntimeError, "restore_failed"):
            W.wait_with_execution_request(proof, lambda: {"hresult": 0, "state": 5}, request,
                                          clock=clock.read, sleep=clock.sleep)
        self.assertEqual(proof["status"], "failed")
        request = Mock()
        request.acquire.side_effect = RuntimeError("acquire_failed")
        with self.assertRaisesRegex(RuntimeError, "acquire_failed"):
            W.wait_with_execution_request({}, lambda: {"hresult": 0, "state": 5}, request)
        request.release.assert_not_called()


if __name__ == "__main__":
    unittest.main()
