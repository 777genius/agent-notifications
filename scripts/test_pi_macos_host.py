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
    def capture_result(self, on=False):
        before = {"owner_pid": 41, "owner_start": "123:456", "window_id": 7, "bounds": [10, 20, 30, 20],
                  "role": "AXCheckBox", "page": "Notifications", "apple_signature_verified": True,
                  "label": "Allow notifications when mirroring or sharing the display", "enabled": True, "on": on}
        return {"scope": "explicit_disposable_notification_capture_ui", "desktop_qualified": False,
                "status": "notification_capture_ready", "before": before, "after": {**before, "on": True},
                "physical_mouse_click": not on, "topmost_hit_verified": not on, "pointer_at_target": not on}

    def test_capture_requires_same_live_control_on_and_at_most_one_physical_click(self):
        for on in (False, True):
            H.validate_capture_result(self.capture_result(on))
        changes = [lambda value: value.update(status="unqualified"),
                   lambda value: value["after"].update(owner_start="123:457"),
                   lambda value: value["after"].update(on=False),
                   lambda value: value["after"].update(apple_signature_verified=False),
                   lambda value: value["before"].update(enabled=False),
                   lambda value: value["before"].pop("bounds"),
                   lambda value: value.update(physical_mouse_click=False),
                   lambda value: value.update(topmost_hit_verified=False),
                   lambda value: value.update(pointer_at_target=False)]
        for change in changes:
            value = self.capture_result()
            change(value)
            with self.assertRaises(ValueError):
                H.validate_capture_result(value)
        value = self.capture_result(True)
        value["physical_mouse_click"] = True
        with self.assertRaises(ValueError):
            H.validate_capture_result(value)

    def test_failed_capture_keeps_original_native_result_and_after_capture_error(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-macos-capture-") as temporary:
            root = Path(temporary)
            value = self.capture_result()
            value["status"] = "unqualified"
            def command(argv, timeout=3, observed=None):
                if argv[0] == "/usr/sbin/screencapture":
                    if str(argv[-1]).endswith("after.png"):
                        raise RuntimeError("TEST private screenshot error")
                    Path(argv[-1]).write_bytes(b"TEST pixels")
                if observed is not None:
                    observed.update(exit_code=0, timed_out=False, output_bounded=True, stdout=H.json.dumps(value))
                return 0, "", ""
            with patch.object(H, "command", side_effect=command), patch.object(H.time, "sleep"):
                with self.assertRaisesRegex(ValueError, "same_control_on_proof_required"):
                    H.prepare_capture(Path("/TEST/observer"), root)
            receipt = H.json.loads((root / "notification-capture-preparation.json").read_text())
            self.assertEqual(receipt["result"], value)
            self.assertEqual(receipt["after_screenshot_error_class"], "RuntimeError")
            self.assertEqual(receipt["failure_class"], "ValueError")
            self.assertNotIn("private screenshot error", H.json.dumps(receipt))

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

    def test_disabled_refusal_retains_all_exact_readonly_responses_before_parsing(self):
        disabled = b'disabled services = {\n}\n'
        service_error = b'Could not find service "com.apple.notificationcenterui.agent"\n'
        native = b'{"applications":[],"desktop_qualified":false}\n'
        results = [SimpleNamespace(returncode=0, stdout=disabled, stderr=b""),
                   SimpleNamespace(returncode=113, stdout=b"", stderr=service_error),
                   SimpleNamespace(returncode=0, stdout=native, stderr=b"")]
        with patch.object(H.subprocess, "run", side_effect=results) as commands:
            snapshot = H.observe_host_state("gui/501", Path("/TEST/owned-observer"))
        self.assertEqual([call.args[0] for call in commands.call_args_list], [
            ["/bin/launchctl", "print-disabled", "gui/501"],
            ["/bin/launchctl", "print", "gui/501/com.apple.notificationcenterui.agent"],
            ["/TEST/owned-observer", "notification-center-state"]])
        with self.assertRaisesRegex(ValueError, "disabled_state_ambiguous"):
            H.inspect("gui/501", snapshot)
        self.assertEqual(snapshot["disabled"]["stdout"], disabled.decode())
        self.assertEqual(snapshot["service"]["stderr"], service_error.decode())
        self.assertEqual(snapshot["service"]["exit_code"], 113)
        self.assertEqual(snapshot["native"]["stdout"], native.decode())
        self.assertTrue(all(row["output_bounded"] for row in snapshot.values()))

    def test_actual_launchctl_disabled_text_preserves_exact_state_and_action_guards(self):
        domain = "gui/501"
        for value, expected in (("disabled", True), ("true", True), ("enabled", False), ("false", False)):
            with self.subTest(value=value), patch.object(H, "command", side_effect=[
                    (0, '\n\tdisabled services = {\n\t\t"' + H.LABEL + '" => ' + value + '\n\t}\n', ""),
                    (113, "", 'Bad request.\nCould not find service "' + H.LABEL + '" in domain for user gui: 501\n')]):
                state = H.inspect(domain)
            self.assertEqual(state, {"disabled": expected, "loaded": False, "running": False})
            if expected:
                self.assertEqual(H.action_plan(state), ["enable", "bootstrap"])
            else:
                with self.assertRaisesRegex(ValueError, "state_not_admitted"):
                    H.action_plan(state)
        for value in ("unknown", "true unexpected", "disabled unexpected", "", "\ndisabled", "\ntrue", "\r\ndisabled", "\vdisabled",
                      'disabled\n"' + H.LABEL + '" => true',
                      'disabled\n"' + H.LABEL + '" => unknown'):
            with self.subTest(value=value), patch.object(H, "command", return_value=(0, '"' + H.LABEL + '" => ' + value, "")):
                with self.assertRaisesRegex(ValueError, "disabled_state_ambiguous"):
                    H.inspect(domain)

    def test_failed_or_oversized_observation_retains_bounded_output_and_still_reads_other_fixed_states(self):
        for first in (H.subprocess.TimeoutExpired("launchctl", 3, output=b"partial", stderr=b"TEST timeout"),
                      SimpleNamespace(returncode=0, stdout=b"x" * 65537, stderr=b"TEST overflow")):
            with self.subTest(first=type(first).__name__):
                with patch.object(H.subprocess, "run", side_effect=[first,
                        SimpleNamespace(returncode=113, stdout=b"", stderr=b"TEST service unavailable"),
                        SimpleNamespace(returncode=0, stdout=b'{"applications":[]}', stderr=b"")]) as commands:
                    snapshot = H.observe_host_state("gui/501", Path("/TEST/owned-observer"))
                self.assertEqual(commands.call_count, 3)
                self.assertIn("error_class", snapshot["disabled"])
                self.assertLessEqual(len(snapshot["disabled"]["stdout"]) + len(snapshot["disabled"]["stderr"]), 65536)
                self.assertEqual(snapshot["service"]["stderr"], "TEST service unavailable")
                self.assertEqual(snapshot["native"]["exit_code"], 0)
                with self.assertRaisesRegex(ValueError, "state_observation_unverified:disabled"):
                    H.inspect("gui/501", snapshot)


if __name__ == "__main__":
    unittest.main()
