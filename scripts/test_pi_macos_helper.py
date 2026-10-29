"""Pure signing-input guards; these tests provide no native qualification."""
import copy
import importlib.util
from pathlib import Path
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


if __name__ == "__main__":
    unittest.main()
