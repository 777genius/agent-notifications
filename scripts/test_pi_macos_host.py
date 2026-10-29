"""Pure official-host admission guards; never prepare a host or grant consent."""
import copy
import importlib.util
from pathlib import Path
import unittest
import tempfile
from types import SimpleNamespace
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("pi_macos_host", Path(__file__).with_name("pi-macos-host.py"))
H = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(H)
SOURCE = "a" * 40


def facts(version="20260907.0337.1"):
    env = {"GITHUB_REPOSITORY": "777genius/agent-notifications", "GITHUB_ACTOR": "777genius",
           "GITHUB_TRIGGERING_ACTOR": "777genius", "GITHUB_EVENT_NAME": "workflow_dispatch",
           "GITHUB_RUN_ATTEMPT": "1", "PI_MACOS_RUNNER_ENVIRONMENT": "github-hosted",
           "ImageOS": "macos15", "ImageVersion": version, "SOURCE_SHA": SOURCE}
    host = {"os": "Darwin", "arch": "arm64", "version": "15.7.9", "build": "24G830"}
    tag = version.rsplit(".", 1)[0]
    metadata = [{"group": "Operating System", "detail": "macOS\n15.7.9\n24G830"},
                {"group": "Runner Image", "detail": "Image: macos-15-arm64\nVersion: " + version
                 + "\nIncluded Software: https://github.com/actions/runner-images/blob/macos-15-arm64/" + tag
                 + "/images/macos/macos-15-arm64-Readme.md\nImage Release: https://github.com/actions/runner-images/releases/tag/macos-15-arm64%2F" + tag}]
    return env, host, metadata, SOURCE, True


class OfficialHostGuards(unittest.TestCase):
    def test_rejected_existing_or_symlink_report_preserves_foreign_bytes(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-macos-host-report-") as temporary:
            root = Path(temporary)
            existing = root / "existing.json"
            existing.write_bytes(b"TEST foreign sentinel")
            link = root / "report.json"
            link.symlink_to(existing)
            for report in (existing, link):
                with self.assertRaisesRegex(ValueError, "fresh_host_preparation_report_required"):
                    H.prepare(SimpleNamespace(report=report, prepare_notification_center=True))
                self.assertEqual(existing.read_bytes(), b"TEST foreign sentinel")
                self.assertTrue(link.is_symlink())

    def test_official_family_admitted_without_freezing_weekly_image_version(self):
        for version in ("20260907.0337.1", "20261029.123.2"):
            self.assertEqual(H.admit(*facts(version))["image_version"], version)

    def test_foreign_owner_event_host_arch_source_or_metadata_never_admitted(self):
        changes = [lambda values, field=field: values[0].update({field: "wrong"}) for field in
                   ("GITHUB_REPOSITORY", "GITHUB_ACTOR", "GITHUB_TRIGGERING_ACTOR", "GITHUB_EVENT_NAME",
                    "GITHUB_RUN_ATTEMPT", "PI_MACOS_RUNNER_ENVIRONMENT", "ImageOS", "ImageVersion", "SOURCE_SHA")]
        changes += [lambda values: values[1].update(arch="x86_64"),
                    lambda values: values[1].update(version="26.6.2"),
                    lambda values: values[2].append(copy.deepcopy(values[2][1])),
                    lambda values: values[2][1].update(detail="Image: namespace-profile-macos-15")]
        for change in changes:
            values = facts()
            change(values)
            with self.assertRaises(ValueError):
                H.admit(*values)
        with self.assertRaises(ValueError):
            H.admit(*facts()[:-1], False)

    def test_only_confirmed_disabled_unloaded_agent_gets_two_exact_actions(self):
        self.assertEqual(H.action_plan({"disabled": True, "loaded": False, "running": False}), ["enable", "bootstrap"])
        self.assertEqual(H.action_plan({"disabled": False, "loaded": True, "running": True, "pid": 41}), [])
        for state in ({}, {"disabled": True, "loaded": True, "running": False},
                      {"disabled": False, "loaded": False, "running": False}):
            with self.assertRaises(ValueError):
                H.action_plan(state)

    def test_readiness_requires_actual_running_system_application_identity(self):
        state = {"disabled": False, "loaded": True, "running": True, "pid": 41}
        app = {"pid": 41, "bundle_id": H.BUNDLE_ID, "bundle_path": str(H.APP), "executable_path": str(H.EXE),
               "finished_launching": True, "process_start_identity": "123:456"}
        H.validate_running(state, {"applications": [app]})
        for change in ({"pid": 42}, {"bundle_path": "/TEST/foreign.app"}, {"executable_path": "/TEST/foreign"},
                       {"bundle_id": "foreign"}, {"bundle_id": H.LABEL},
                       {"finished_launching": False}, {"process_start_identity": ""}):
            with self.assertRaises(ValueError):
                H.validate_running(state, {"applications": [{**app, **change}]})

    def test_launchd_service_identity_is_distinct_from_application_bundle_identity(self):
        domain = "gui/501"
        with patch.object(H, "command", side_effect=[
                (0, '"com.apple.notificationcenterui.agent" => true', ""),
                (113, "", 'Could not find service "com.apple.notificationcenterui.agent"')]) as observed:
            self.assertEqual(H.inspect(domain), {"disabled": True, "loaded": False, "running": False})
            self.assertEqual(observed.call_args_list[1].args[0],
                             ["/bin/launchctl", "print", domain + "/com.apple.notificationcenterui.agent"])
        with patch.object(H, "command", return_value=(0, '"com.apple.notificationcenterui" => true', "")):
            with self.assertRaisesRegex(ValueError, "disabled_state_ambiguous"):
                H.inspect(domain)

    def test_ambiguous_disabled_state_retains_both_readonly_queries_before_refusal(self):
        observed = {}
        disabled = 'disabled services = {\n}\n'
        service_error = 'Could not find service "com.apple.notificationcenterui.agent"'
        with patch.object(H, "command", side_effect=[(0, disabled, ""), (113, "", service_error)]) as queried:
            with self.assertRaisesRegex(ValueError, "notification_agent_disabled_state_ambiguous"):
                H.inspect("gui/501", observed)
        self.assertEqual(observed, {"domain": "gui/501",
                                   "disabled_query": {"exit_code": 0, "stdout": disabled, "stderr": ""},
                                   "service_query": {"exit_code": 113, "stdout": "", "stderr": service_error}})
        self.assertEqual([call.args[0][1] for call in queried.call_args_list], ["print-disabled", "print"])


if __name__ == "__main__":
    unittest.main()
