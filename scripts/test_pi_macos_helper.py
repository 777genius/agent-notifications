"""Pure signing-input guards; these tests provide no native qualification."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("pi_macos_helper", Path(__file__).with_name("pi-macos-helper.py"))
H = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(H)
PIN = {"schema_version": 1, "source_sha": "a" * 40, "source_tree": "b" * 40,
       "run_id": 41, "run_attempt": 2, "artifact_id": 42,
       "archive_sha256": "c" * 64, "executable_sha256": "d" * 64, "attestation_sha256": "e" * 64,
       "custody_sha256": "f" * 64, "signing_run_sha256": "1" * 64, "signing_artifacts_sha256": "2" * 64}


def evidence():
    run = {"id": 41, "run_attempt": 2, "head_sha": PIN["source_sha"], "status": "completed",
           "conclusion": "success", "event": "workflow_dispatch", "head_branch": "release/macos-signing",
           "path": H.WORKFLOW, "actor": {"login": "777genius"}, "triggering_actor": {"login": "777genius"}}
    artifacts = {"total_count": 1, "artifacts": [{"id": 42, "name": "ClaudeNotifier-smoke", "expired": False,
                 "workflow_run": {"id": 41, "head_sha": PIN["source_sha"]}}]}
    custody = {"schemaVersion": 1, "sourceSHA": PIN["source_sha"], "workflowSHA": PIN["source_sha"],
               "workflowRef": H.REPOSITORY + "/" + H.WORKFLOW + "@refs/heads/release/macos-signing",
               "runID": "41", "runAttempt": "2", "archiveSHA256": PIN["archive_sha256"],
               "executableSHA256": PIN["executable_sha256"], "attestationSHA256": PIN["attestation_sha256"],
               "bundleID": H.BUNDLE, "signingTeam": H.TEAM, "signatureVerified": True}
    return run, artifacts, custody


class SignedHelperGuards(unittest.TestCase):
    def test_exact_owner_custody_and_candidate_tree_are_accepted(self):
        H.validate_custody(PIN, *evidence(), PIN["archive_sha256"])
        with mock.patch.object(H, "source_tree", return_value=PIN["source_tree"]):
            H.candidate_tree(PIN)

    def test_wrong_run_branch_actor_attempt_or_artifact_never_pass(self):
        for mutate in (lambda values: values[0].update(head_branch="feat/pi"),
                       lambda values: values[0].update(head_sha="f" * 40),
                       lambda values: values[0].update(run_attempt=3),
                       lambda values: values[0].update(conclusion="failure"),
                       lambda values: values[0]["actor"].update(login="someone-else"),
                       lambda values: values[1]["artifacts"][0].update(id=43),
                       lambda values: values[1]["artifacts"][0].update(expired=True),
                       lambda values: values[1]["artifacts"].append(copy.deepcopy(values[1]["artifacts"][0]))):
            values = evidence()
            mutate(values)
            with self.assertRaises(ValueError):
                H.validate_custody(PIN, *values, PIN["archive_sha256"])

    def test_archive_executable_attestation_and_workflow_must_match(self):
        for field in ("sourceSHA", "workflowSHA", "workflowRef", "runID", "runAttempt", "archiveSHA256",
                      "executableSHA256", "attestationSHA256", "bundleID", "signingTeam", "signatureVerified"):
            values = evidence()
            values[2][field] = "wrong"
            with self.subTest(field=field), self.assertRaises(ValueError):
                H.validate_custody(PIN, *values, PIN["archive_sha256"])
        with self.assertRaises(ValueError):
            H.validate_custody(PIN, *evidence(), "f" * 64)

    def test_changed_candidate_tree_and_unverified_receipts_are_refused(self):
        with mock.patch.object(H, "source_tree", return_value="f" * 40), self.assertRaises(ValueError):
            H.candidate_tree(PIN)
        receipt = {"schema_version": 1, "pin": PIN, "signed_source_tree_verified": True,
                   "signature_and_notarization_verified": True}
        H.validate_receipt(receipt, PIN)
        for field in ("signed_source_tree_verified", "signature_and_notarization_verified"):
            for value in (False, 1):
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    H.validate_receipt({**receipt, field: value}, PIN)
        with self.assertRaises(ValueError):
            H.validate_receipt({**receipt, "pin": {**PIN, "source_sha": "f" * 40}}, PIN)


TRANSPORT = (b'error is Error Domain=NSURLErrorDomain Code=-1005 "The network connection was lost." '
             b'NSErrorFailingURLStringKey=https://api.apple-cloudkit.com/database/1/com.apple.gk.ticket-delivery/production/public/records/lookup\n'
             b"CloudKit's response is inconsistent with expections: (null)\nThe validate action failed! Error 68.\n")


class NativeVerificationGuards(unittest.TestCase):
    def verify(self, results, *, fail=None, seconds_per_result=10):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-native-trust-") as name:
            evidence = Path(name) / "verification.jsonl"
            clock = [100.0]
            def run(argv, **kwargs):
                clock[0] += seconds_per_result
                value = results.pop(0)
                if isinstance(value, Exception):
                    raise value
                return subprocess.CompletedProcess(argv, *value)
            with mock.patch.object(H.subprocess, "run", side_effect=run) as commands, \
                    mock.patch.object(H.time, "monotonic", side_effect=lambda: clock[0]):
                if fail:
                    with self.assertRaisesRegex(ValueError, fail):
                        H.verify_native_helper(Path(name) / "ClaudeNotifier.app", evidence)
                else:
                    H.verify_native_helper(Path(name) / "ClaudeNotifier.app", evidence)
            rows = [json.loads(line) for line in evidence.read_text().splitlines()]
            return rows, commands.call_args_list

    def test_only_stapler_transport_retries_and_final_actual_trust_must_pass(self):
        rows, calls = self.verify([(0, b"signature and publisher verified", b""), (68, TRANSPORT, b""),
                                  (0, b"The validate action worked!", b""), (0, b"", b"accepted")])
        self.assertEqual([(r["stage"], r["attempt"], r["retry"]) for r in rows],
                         [("signature", 1, False), ("stapler", 1, True), ("stapler", 2, False), ("assessment", 1, False)])
        self.assertEqual([r["timeout_seconds"] for r in rows], [180, 170, 160, 150])
        self.assertEqual(rows[1]["stdout"], TRANSPORT.decode())
        self.assertEqual(rows[-1]["stderr"], "accepted")
        self.assertNotIn("--notarized", calls[0].args[0])
        self.assertEqual(calls[1].args[0][:3], ["/usr/bin/xcrun", "stapler", "validate"])
        self.assertEqual(calls[1].args[0], calls[2].args[0])
        self.assertEqual(calls[3].args[0][:5], ["/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=2"])

    def test_two_transport_failures_exhaust_attempts_and_never_assess_or_pass(self):
        rows, calls = self.verify([(0, b"valid signature", b""), (68, TRANSPORT, b"first"), (68, TRANSPORT, b"second")],
                                  fail="native_verification_failed:stapler")
        self.assertEqual(len(calls), 3)
        self.assertEqual([row["retry"] for row in rows], [False, True, False])
        self.assertEqual([row["stderr"] for row in rows[1:]], ["first", "second"])

    def test_signature_ticket_assessment_and_unmeasured_transport_failures_never_retry(self):
        for output in (TRANSPORT.replace(b"-1005", b"-1001"), TRANSPORT.replace(b"api.apple-cloudkit.com", b"foreign.invalid"),
                       b"The ticket is invalid.\n" + TRANSPORT, b"The ticket was revoked.\n" + TRANSPORT,
                       b"No ticket found.\n", b"The validate action failed! Error 68.\n"):
            with self.subTest(output=output):
                rows, calls = self.verify([(0, b"valid signature", b""), (68, output, b"")], fail="failed:stapler")
                self.assertEqual(len(calls), 2)
                self.assertFalse(rows[-1]["retry"])
        for stage, results in (("signature", [(1, b"", b"invalid signature")]),
                               ("assessment", [(0, b"", b""), (0, b"valid ticket", b""), (1, TRANSPORT, b"rejected")])):
            rows, calls = self.verify(results, fail="failed:" + stage)
            self.assertEqual(len(calls), 1 if stage == "signature" else 3)
            self.assertFalse(rows[-1]["retry"])

    def test_timeout_is_retained_and_never_becomes_transport_retry(self):
        rows, calls = self.verify([(0, b"valid signature", b""),
            subprocess.TimeoutExpired("stapler", 170, output=TRANSPORT, stderr=b"still running")], fail="timeout:stapler")
        self.assertEqual(len(calls), 2)
        self.assertTrue(rows[-1]["timed_out"])
        self.assertIsNone(rows[-1]["returncode"])
        self.assertEqual(rows[-1]["stdout"], TRANSPORT.decode())
        self.assertFalse(rows[-1]["retry"])

    def test_original_total_deadline_cannot_be_reset_or_overrun_by_success(self):
        rows, calls = self.verify([(0, b"late signature result", b"")],
                                  fail="native_verification_budget_expired", seconds_per_result=180)
        self.assertEqual(len(calls), 1)
        self.assertEqual(rows[0]["timeout_seconds"], 180)
        self.assertFalse(rows[0]["within_budget"])


if __name__ == "__main__":
    unittest.main()
