"""The native oracle must reject low-level completion and misleading error exits."""
import importlib.util
from pathlib import Path
import unittest

FIXTURE = Path(__file__).resolve().parents[1] / "tests/integration/pi_native_fixture.py"
SPEC = importlib.util.spec_from_file_location("pi_fixture", FIXTURE)
F = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(F)


def rows(ends=1, outcome="completed", aborted=False):
    return ([{"type": "factory"}] + [{"type": "agent_end"}] * ends
            + ([{"type": "agent_before_settle", "outcome": outcome}] if outcome else [])
            + [{"type": "agent_settled", "aborted": aborted, "idle": True}])


class NativeOracleTests(unittest.TestCase):
    def test_low_level_ends_are_not_final_completion(self):
        F.verify_case("retry", rows(ends=2), [{}, {}])
        with self.assertRaisesRegex(RuntimeError, "settlement_count"):
            F.verify_case("retry", rows(ends=2) + [{"type": "agent_settled", "aborted": False, "idle": True}], [{}, {}])

    def test_error_requires_error_boundary_even_when_not_aborted(self):
        F.verify_case("error", rows(outcome="error"), [{}])
        with self.assertRaisesRegex(RuntimeError, "boundary_outcome"):
            F.verify_case("error", rows(), [{}])

    def test_abort_needs_no_invented_boundary(self):
        F.verify_case("abort", rows(outcome=None, aborted=True), [{}])
        with self.assertRaisesRegex(RuntimeError, "boundary_outcome"):
            F.verify_case("abort", rows(aborted=True), [{}])

    def test_idle_rpc_ui_does_not_require_an_agent_run(self):
        events = [{"type": "factory"}] + [{"type": name, "idle": True, "mode": "rpc", "hasUI": True}
            for name in ("ui_prompt_start", "ui_prompt_end")]
        F.verify_case("ui", events, [])
        with self.assertRaisesRegex(RuntimeError, "idle_ui_unexpected_run"):
            F.verify_case("ui", events, [{}])


if __name__ == "__main__":
    unittest.main()
