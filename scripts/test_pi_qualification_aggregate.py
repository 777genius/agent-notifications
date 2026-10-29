"""Synthetic metadata guard tests only; these are never native OS evidence."""
import importlib.util
from pathlib import Path
import unittest
from unittest import mock

PATH = Path(__file__).with_name("pi-qualification-aggregate.py")
SPEC = importlib.util.spec_from_file_location("pi_aggregate", PATH)
A = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(A)
SOURCE = "a" * 40
HASH = "b" * 64
HELPER_PIN = {"schema_version": 1, "source_sha": "d" * 40, "source_tree": "e" * 40,
              "run_id": 41, "run_attempt": 1, "artifact_id": 42,
              "archive_sha256": HASH, "executable_sha256": HASH, "attestation_sha256": HASH,
              "custody_sha256": HASH, "signing_run_sha256": HASH, "signing_artifacts_sha256": HASH}
HELPER_INPUT = {"schema_version": 1, "pin": HELPER_PIN, "signed_source_tree_verified": True,
                "signature_and_notarization_verified": True}


def metadata(platform="linux"):
    arch = "arm64" if platform == "darwin" else "amd64"
    suffix = ".exe" if platform == "windows" else ""
    names = [name + "-" + platform + "-" + arch + suffix for name in
             ("claude-notifications", "sound-preview", "list-devices", "list-sounds")]
    assets = {name: A.sha(A.REPO / name) for name in ("claude_icon.png", "sounds/task-complete.mp3", "sounds/question.mp3")}
    build = {"schema_version": 1, "platform": platform, "status": "built", "release_equivalent": True,
             "checkout_clean": True, "go_work": "off", "source_sha": SOURCE,
             "modules": [{"Path": "main", "Main": True}, {"Path": "sdk", "Version": "v1.0.0"}],
             "go_build_information": "\tdep\tsdk\tv1.0.0\th1:TEST\n\tbuild\tvcs.revision=" + SOURCE + "\n\tbuild\tvcs.modified=false\n",
             "go_mod_sha256": A.sha(A.REPO / "go.mod"), "go_sum_sha256": A.sha(A.REPO / "go.sum"),
             "binary_architecture": arch, "candidate_binary_sha256": HASH, "package_sha256": HASH,
             "updated_package_sha256": HASH, "canonical_assets": assets,
             "stage_inventory": {name: {"sha256": HASH} for name in names}}
    acquisition = {"schema_version": 1, "platform": platform, "architecture": arch,
                   "version": A.PINS["version"], "source_commit": A.PINS["source_commit"],
                   "archive_sha256": A.PINS["targets"][platform + "/" + arch]["sha256"], "binary_sha256": HASH}
    identity = {"schema_version": 1, "source_sha": SOURCE, "host_os": A.HOSTS[platform],
                "working_tree_dirty": False, "candidate_binary_sha256": HASH, "package_sha256": HASH,
                "pi_binary_sha256": HASH, "unrelated_mcp_policy_enabled": False, "installed_asset_sha256": assets}
    process = {**identity, "status": "passed_installed_pi_process", "updated_package_sha256": HASH,
               "webhook_private_payload_failures": 0,
               "cases": [{"case": name} for name in A.PROCESS_CASES] + [{"case": case, "mode": mode} for case, mode in A.NATIVE_MODES]}
    for row in process["cases"]:
        if row["case"] == "setup-uninstall":
            row.update(pi_executable_supplied=False, foreign_bytes_preserved=True)
        if row["case"] == "fresh-after-remove-no-effect":
            row.update(mode="json", provider_requests=1, webhook=None, child_collected=True,
                       native_counts=dict(A.SUCCESS_COUNTS), native_outcome="completed")
        if row["case"] == "runtime-byte-replacement":
            row.update(old_primary_sha256=HASH, new_primary_sha256="c" * 64, old_provider_requests=1, fresh_provider_requests=1,
                       old_webhook_count=0, fresh_webhook_count=1, old_native_counts=dict(A.SUCCESS_COUNTS), fresh_native_counts=dict(A.SUCCESS_COUNTS))
            row.update({key: True for key in ("package_projection_unchanged", "derived_and_owned_executable_match", "old_child_collected",
                "fresh_child_collected", "foreign_bytes_preserved", "sibling_consumers_preserved", "policy_bytes_preserved",
                "preparation_retired", "test_retainer_retired")})
    build.update(replacement_candidate_binary_sha256="c" * 64, replacement_go_build_information=build["go_build_information"],
                 replacement_stage_inventory={name: {"sha256": "c" * 64, "bytes": 1} for name in names})
    process["replacement_candidate_binary_sha256"] = "c" * 64
    desktop = {**identity, "native_product_observations_passed": True, "owned_children_collected": True,
               "cases": [{"case": name, "checks": {check: True for check in A.CHECKS},
                           "pending_question_at_click": name == "question"} for name in A.CASES]}
    if platform == "linux":
        kernel = {"pid": 123, "ppid": 100, "start_ticks": 456, "executable": "/TEST/runtime/helper"}
        desktop["lifecycle_cases"] = [{"case": action, "status": "passed", "scope": "actual_installed_pi_linux_tui",
            "cancellation_seconds": 0.2, "helper_identities": [dict(kernel), {**kernel, "pid": 124}] if action == "reload" else [dict(kernel)],
            "pi_identity": {**kernel, "pid": 100}, "original_helper_subtree_collected": True,
            "terminal_collected": True, "no_resend": True,
            "natural_timeout_seconds": 10.1, "production_http_timeout_seconds": 10, "no_control_before_timeout": True,
            "admitted_helpers": 2 if action == "reload" else 1, "admitted_webhook_requests": 1,
            "helper_admission_counts": [1, 2, 2] if action == "reload" else [1], "fresh_observer_delivered_once": True,
            "original_webhook_request_ids": ["00000000-0000-4000-8000-000000000001"],
            "fresh_webhook_request_id": "00000000-0000-4000-8000-000000000003", "fresh_webhook_received_after_start": True,
            "excess_observation_dropped": action == "reload", "settled_before_control": 3 if action == "reload" else 1}
            for action in ("reload", "new", "prompt-end", "quit", "timeout")]
        desktop["multiplexer"] = {"case": "tmux-completion", "native_mode": "tui",
            "checks": {check: True for check in A.CHECKS}, "real_mouse_click": True, "banner_observed": True,
            "pane_focus_supported": False, "outer_window_focus_observed": True, "active_pane_before_click": "%1",
            "active_pane_after_click": "%1", "owned_tmux_server_collected": True, "origin_tmux_pane": "%0",
            "tmux_server_identity": kernel, "configured_cue_envelope_match": 0.9}
        for row in desktop["cases"]:
            row["configured_cue_envelope_match"] = None if row["case"] in ("sound-off", "desktop-off") else 0.9
    if platform == "darwin":
        desktop["native_helper_verified"] = True
        build["stage_inventory"]["ClaudeNotifier.app.managed-runtime.json"] = {"sha256": HASH, "bytes": 1}
        build["native_attestation_sha256"] = HASH
        build["native_helper_input"] = HELPER_INPUT
        build["stage_inventory"]["ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"] = {"sha256": HASH, "bytes": 1}
        desktop["helper"] = {"input": HELPER_INPUT, "archive_sha256": HASH,
                             "executable_sha256": HASH, "source_stage_attestation_sha256": HASH}
    if platform == "windows":
        build["embedded_callback_verified"] = True
        build["stage_inventory"]["claude-notifications-windows-amd64-focus.exe"] = {"sha256": HASH, "bytes": 1}
        desktop.update(windows_client=True, render_route_observed=True, host_architecture="AMD64")
        route = {"kind": "render_loopback", "render_endpoint_id": "opaque-render"}
        desktop.update(audio_capture_route=route, prerequisites={"status": "prerequisites_ready"},
                       prerequisite_measurements={"audio_route": route, "known_cue_method": "PlaySoundW", "audio_samples": 96000, "audio_peak": 0.25})
        for row in desktop["cases"]:
            row["audio"] = {"audio_route": dict(route)}
    return build, acquisition, process, desktop


class AggregateGuards(unittest.TestCase):
    def setUp(self):
        self.addCleanup(mock.patch.stopall)
        mock.patch.object(A.B.H, "load_pin", return_value=HELPER_PIN).start()
        mock.patch.object(A.B.H, "candidate_tree").start()

    def test_validated_metadata_contract_on_each_os(self):
        for platform in A.HOSTS:
            self.assertEqual(A.qualify(platform, SOURCE, *metadata(platform))["status"], "qualified")

    def test_linux_real_lifecycle_birth_identities_tmux_and_canonical_audio_are_required(self):
        for change in (lambda desktop: desktop.pop("lifecycle_cases"),
                       lambda desktop: desktop["lifecycle_cases"][0].update(original_helper_subtree_collected=False),
                       lambda desktop: desktop["lifecycle_cases"][0]["helper_identities"][0].update(start_ticks=0),
                       lambda desktop: desktop["lifecycle_cases"][0].update(cancellation_seconds=15),
                       lambda desktop: desktop["lifecycle_cases"][0].update(admitted_helpers=3),
                       lambda desktop: desktop["lifecycle_cases"][0].update(admitted_webhook_requests=2),
                       lambda desktop: desktop["lifecycle_cases"][0].update(helper_admission_counts=[1, 2, 3]),
                       lambda desktop: desktop["lifecycle_cases"][0].update(original_webhook_request_ids=["00000000-0000-4000-8000-000000000001"] * 2),
                       lambda desktop: desktop["lifecycle_cases"][0].update(original_webhook_request_ids=[]),
                       lambda desktop: desktop["lifecycle_cases"][0].update(fresh_webhook_request_id="00000000-0000-4000-8000-000000000001"),
                       lambda desktop: desktop["lifecycle_cases"][0].update(fresh_webhook_received_after_start=False),
                       lambda desktop: desktop["lifecycle_cases"][0].update(excess_observation_dropped=False),
                       lambda desktop: desktop["lifecycle_cases"][1].update(fresh_observer_delivered_once=False),
                       lambda desktop: desktop["lifecycle_cases"][4].update(natural_timeout_seconds=30),
                       lambda desktop: desktop["lifecycle_cases"][4].update(no_control_before_timeout=False),
                       lambda desktop: desktop.pop("multiplexer"),
                       lambda desktop: desktop["multiplexer"].update(outer_window_focus_observed=False),
                       lambda desktop: desktop["multiplexer"].update(pane_focus_supported=True),
                       lambda desktop: desktop["multiplexer"].update(active_pane_after_click="%0"),
                       lambda desktop: desktop["multiplexer"].update(owned_tmux_server_collected=False),
                       lambda desktop: next(row for row in desktop["cases"] if row["case"] == "completion").update(configured_cue_envelope_match=0.1)):
            values = metadata("linux"); change(values[3])
            with self.assertRaises(ValueError):
                A.qualify("linux", SOURCE, *values)

    def test_linux_cancel_can_finish_a_second_previously_admitted_effect(self):
        values = metadata("linux")
        requests = values[3]["lifecycle_cases"][0]["original_webhook_request_ids"]
        requests.append("00000000-0000-4000-8000-000000000002")
        self.assertEqual(A.qualify("linux", SOURCE, *values)["status"], "qualified")
        requests.append("00000000-0000-4000-8000-000000000004")
        with self.assertRaisesRegex(ValueError, "original_webhook_admission_or_identity"):
            A.qualify("linux", SOURCE, *values)

    def test_windows_selected_route_and_original_cue_remain_mandatory(self):
        for change in (lambda desktop: desktop.pop("audio_capture_route"),
                       lambda desktop: desktop["prerequisite_measurements"].update(audio_peak=0),
                       lambda desktop: desktop["prerequisite_measurements"].update(known_cue_method="synthetic_assertion"),
                       lambda desktop: desktop["cases"][0]["audio"].update(audio_route={"kind": "default_microphone"})):
            values = metadata("windows"); change(values[3])
            with self.assertRaises(ValueError):
                A.qualify("windows", SOURCE, *values)

    def test_windows_explicit_pair_requires_verified_same_source_driver_and_route(self):
        def paired():
            values = metadata("windows")
            route = {"kind": "signed_virtual_cable_capture", "driver_instance": "ROOT\\MEDIA\\0000",
                     "render_endpoint_id": "opaque-render", "capture_endpoint_id": "opaque-capture",
                     "render_topology_id": "opaque-render-adapter", "capture_topology_id": "opaque-capture-adapter"}
            desktop = values[3]
            desktop.update(audio_capture_route=dict(route), audio_fixture_verified=True,
                           audio_fixture={"schema_version": 1, "source_sha": SOURCE, "route": dict(route),
                                          "driver": {"instance_id": route["driver_instance"], "image_path": "C:\\TEST\\driver.sys",
                                                     "image_sha256": A.W.DRIVER_SHA256, "inf_path": "C:\\TEST\\driver.inf",
                                                     "catalog_path": "C:\\TEST\\driver.cat"}})
            desktop["prerequisite_measurements"]["audio_route"] = dict(route)
            for row in desktop["cases"]:
                row["audio"]["audio_route"] = dict(route)
            return values
        self.assertEqual(A.qualify("windows", SOURCE, *paired())["status"], "qualified")
        for change in (lambda desktop: desktop.pop("audio_fixture_verified"),
                       lambda desktop: desktop["audio_fixture"].update(source_sha="f" * 40),
                       lambda desktop: desktop["audio_fixture"]["driver"].update(image_sha256="f" * 64),
                       lambda desktop: desktop["audio_fixture"]["route"].update(capture_topology_id="different-opaque-topology")):
            values = paired(); change(values[3])
            with self.assertRaises(ValueError):
                A.qualify("windows", SOURCE, *values)

    def test_status_alone_partial_cases_and_old_source_never_pass(self):
        for mutate in (lambda values: values[3].update(cases=[]),
                       lambda values: values[3].update(source_sha="c" * 40),
                       lambda values: values[2].update(working_tree_dirty=True),
                       lambda values: values[2].update(cases=[])):
            values = metadata()
            mutate(values)
            with self.assertRaises((ValueError, RuntimeError)):
                A.qualify("linux", SOURCE, *values)

    def test_pending_question_and_every_native_check_required(self):
        values = metadata()
        question = next(row for row in values[3]["cases"] if row["case"] == "question")
        question["pending_question_at_click"] = False
        with self.assertRaisesRegex(ValueError, "question"):
            A.qualify("linux", SOURCE, *values)
        for check in A.CHECKS:
            values = metadata()
            values[3]["cases"][0]["checks"][check] = False
            with self.assertRaisesRegex(ValueError, "unobserved"):
                A.qualify("linux", SOURCE, *values)

    def test_windows_no_render_route_or_undisclosed_emulation_refused(self):
        values = metadata("windows")
        values[3]["render_route_observed"] = False
        with self.assertRaisesRegex(ValueError, "windows_client_audio"):
            A.qualify("windows", SOURCE, *values)
        values = metadata("windows")
        values[3]["host_architecture"] = "ARM64"
        with self.assertRaisesRegex(ValueError, "emulation"):
            A.qualify("windows", SOURCE, *values)
        values[3]["execution_mode"] = "x64_emulation"
        A.qualify("windows", SOURCE, *values)

    def test_macos_signed_source_and_actual_helper_bytes_must_match_pin(self):
        for change in (lambda values: values[0].pop("native_helper_input"),
                       lambda values: values[3]["helper"].update(executable_sha256="f" * 64),
                       lambda values: values[3]["helper"].update(archive_sha256="f" * 64),
                       lambda values: values[0]["stage_inventory"]["ClaudeNotifier.app.managed-runtime.json"].update(sha256="f" * 64),
                       lambda values: values[0]["stage_inventory"]["ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"].update(sha256="f" * 64)):
            values = metadata("darwin")
            change(values)
            with self.assertRaises(ValueError):
                A.qualify("darwin", SOURCE, *values)

    def test_missing_pi_hash_and_empty_native_inventory_cannot_agree_as_null(self):
        values = metadata()
        values[1]["binary_sha256"] = None
        values[2].pop("pi_binary_sha256")
        values[3].pop("pi_binary_sha256")
        with self.assertRaisesRegex(ValueError, "pi_binary_identity"):
            A.qualify("linux", SOURCE, *values)
        for platform, name in (("windows", "claude-notifications-windows-amd64-focus.exe"),
                               ("darwin", "ClaudeNotifier.app.managed-runtime.json")):
            values = metadata(platform)
            values[0]["stage_inventory"][name] = {}
            with self.assertRaises(ValueError):
                A.qualify(platform, SOURCE, *values)

    def test_fresh_removal_label_cannot_replace_actual_native_success_and_silence(self):
        for change in ({}, {"webhook": "task_complete"}, {"child_collected": False}, {"provider_requests": 0},
                       {"native_counts": {}}, {"native_outcome": "error"}):
            values = metadata()
            row = next(row for row in values[2]["cases"] if row["case"] == "fresh-after-remove-no-effect")
            if not change:
                row.clear(); row["case"] = "fresh-after-remove-no-effect"
            else:
                row.update(change)
            with self.assertRaisesRegex(ValueError, "fresh_post_removal"):
                A.qualify("linux", SOURCE, *values)
        values = metadata()
        row = next(row for row in values[2]["cases"] if row["case"] == "fresh-after-remove-no-effect")
        row.pop("webhook")
        with self.assertRaisesRegex(ValueError, "fresh_post_removal"):
            A.qualify("linux", SOURCE, *values)

    def test_runtime_change_requires_different_owned_bytes_and_real_old_fresh_events(self):
        for change in ({"new_primary_sha256": HASH}, {"old_webhook_count": 1}, {"fresh_webhook_count": 0},
                       {"old_native_counts": {}}, {"fresh_child_collected": False}, {"sibling_consumers_preserved": False}):
            values = metadata()
            row = next(row for row in values[2]["cases"] if row["case"] == "runtime-byte-replacement")
            row.update(change)
            with self.assertRaisesRegex(ValueError, "runtime_byte_replacement"):
                A.qualify("linux", SOURCE, *values)


if __name__ == "__main__":
    unittest.main()
