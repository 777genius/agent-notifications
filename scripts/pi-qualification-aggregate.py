#!/usr/bin/env python3
"""Require one exact clean candidate and real installed Pi desktop on all 3 OSes."""
import argparse
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import re
import subprocess

REPO = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("pi_candidate_build", Path(__file__).with_name("pi-candidate-build.py"))
B = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(B)
WINDOWS_SPEC = importlib.util.spec_from_file_location("pi_windows_audio_fixture", Path(__file__).with_name("pi-windows-audio-fixture.py"))
W = importlib.util.module_from_spec(WINDOWS_SPEC)
WINDOWS_SPEC.loader.exec_module(W)
PINS = json.loads((REPO / "scripts/testdata/pi-native/pins.json").read_text())
HOSTS = {"linux": "Linux", "darwin": "Darwin", "windows": "Windows"}
CASES = {"completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin"}
CHECKS = {"native_tui", "configured_banner_policy", "configured_audio_policy", "configured_click_policy",
          "native_event_counts", "ambient_root_isolated", "owned_terminal_collected"}
PROCESS_CASES = {"normal-quit-drains-admitted-webhook", "hung-quit-original-budget", "webhook-config-disabled",
                 "webhook-consent-disabled", "unchanged-reinstall-restores-derived-metadata",
                 "live-A-update-old-revision-denied", "live-B-remove-current-revision-denied",
                 "setup-update", "setup-repair", "setup-uninstall", "fresh-after-remove-no-effect", "runtime-byte-replacement"}
NATIVE_MODES = {("success", "json"), ("success", "print"), ("retry", "json"), ("followup", "json"),
                ("error", "json"), ("abort", "rpc"), ("ui", "rpc")}
SUCCESS_COUNTS = {"factory": 1, "agent_start": 1, "agent_end": 1, "agent_before_settle": 1,
                  "agent_settled": 1, "ui_prompt_start": 0, "ui_prompt_end": 0}


def require(ok, code):
    if not ok:
        raise ValueError(code)


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read(path):
    require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 2 * 1024 * 1024, "missing_or_invalid_receipt:" + path.name)
    return json.loads(path.read_text())


def qualify(platform, source, build, acquisition, process, desktop):
    require(build.get("schema_version") == 1 and build.get("platform") == platform
            and build.get("status") == "built" and build.get("release_equivalent") is True
            and build.get("checkout_clean") is True and build.get("go_work") == "off", "build_not_clean_release_equivalent")
    require(all(value.get("source_sha") == source for value in (build, process, desktop)), "source_identity_mismatch")
    B.pinned_graph(build.get("modules"))
    B.linked_source(build.get("go_build_information", ""), source)
    require(build.get("go_mod_sha256") == sha(REPO / "go.mod") and build.get("go_sum_sha256") == sha(REPO / "go.sum"), "module_manifest_identity_mismatch")
    arch = build.get("binary_architecture")
    target = platform + "/" + str(arch)
    require(target in PINS["targets"] and acquisition.get("schema_version") == 1
            and acquisition.get("platform") == platform and acquisition.get("architecture") == arch
            and acquisition.get("version") == PINS["version"] and acquisition.get("source_commit") == PINS["source_commit"]
            and acquisition.get("archive_sha256") == PINS["targets"][target]["sha256"], "pi_acquisition_not_immutable_pin")
    require(isinstance(acquisition.get("binary_sha256"), str)
            and re.fullmatch(r"[a-f0-9]{64}", acquisition["binary_sha256"]), "pi_binary_identity_missing")
    main_hash, package_hash = build.get("candidate_binary_sha256"), build.get("package_sha256")
    require(isinstance(main_hash, str) and re.fullmatch(r"[a-f0-9]{64}", main_hash)
            and isinstance(package_hash, str) and re.fullmatch(r"[a-f0-9]{64}", package_hash)
            and isinstance(build.get("updated_package_sha256"), str)
            and re.fullmatch(r"[a-f0-9]{64}", build["updated_package_sha256"]), "candidate_hash_missing")
    suffix = ".exe" if platform == "windows" else ""
    inventory = build.get("stage_inventory", {})
    main_name = "claude-notifications-" + platform + "-" + arch + suffix
    require(inventory.get(main_name, {}).get("sha256") == main_hash, "candidate_main_inventory_mismatch")
    for name in ("sound-preview", "list-devices", "list-sounds"):
        require(re.fullmatch(r"[a-f0-9]{64}", inventory.get(name + "-" + platform + "-" + arch + suffix, {}).get("sha256", "")), "candidate_companion_missing")
    expected_assets = {name: sha(REPO / name) for name in ("claude_icon.png", "sounds/task-complete.mp3", "sounds/question.mp3")}
    require(build.get("canonical_assets") == expected_assets, "canonical_assets_mismatch")
    for value in (process, desktop):
        require(value.get("schema_version") == 1 and value.get("host_os") == HOSTS[platform]
                and value.get("working_tree_dirty") is False and value.get("candidate_binary_sha256") == main_hash
                and value.get("package_sha256") == package_hash and value.get("pi_binary_sha256") == acquisition.get("binary_sha256")
                and value.get("unrelated_mcp_policy_enabled") is False
                and value.get("installed_asset_sha256") == expected_assets, "installed_input_or_policy_identity_mismatch")
    require(process.get("status") == "passed_installed_pi_process"
            and process.get("updated_package_sha256") == build.get("updated_package_sha256")
            and process.get("webhook_private_payload_failures") == 0, "installed_process_not_qualified")
    rows = process.get("cases", [])
    require(PROCESS_CASES <= {row.get("case") for row in rows}
            and NATIVE_MODES <= {(row.get("case"), row.get("mode")) for row in rows}, "installed_process_case_missing")
    removed = [row for row in rows if row.get("case") == "setup-uninstall"]
    require(removed and all(row.get("pi_executable_supplied") is False and row.get("foreign_bytes_preserved") is True for row in removed), "cli_missing_removal_not_observed")
    fresh = [row for row in rows if row.get("case") == "fresh-after-remove-no-effect"]
    require(len(fresh) == 1 and fresh[0].get("mode") == "json" and "webhook" in fresh[0] and fresh[0]["webhook"] is None
            and fresh[0].get("child_collected") is True and fresh[0].get("provider_requests") == 1
            and fresh[0].get("native_outcome") == "completed" and success_counts(fresh[0].get("native_counts")),
            "fresh_post_removal_native_success_or_silence_unobserved")
    replacement = build.get("replacement_candidate_binary_sha256")
    require(isinstance(replacement, str) and re.fullmatch(r"[a-f0-9]{64}", replacement) and replacement != main_hash
            and process.get("replacement_candidate_binary_sha256") == replacement, "runtime_replacement_identity_missing")
    B.linked_source(build.get("replacement_go_build_information", ""), source)
    replacement_inventory = build.get("replacement_stage_inventory", {})
    require(regular_artifact(replacement_inventory.get(main_name, {}))
            and replacement_inventory[main_name]["sha256"] == replacement, "replacement_main_inventory_mismatch")
    for name in ("sound-preview", "list-devices", "list-sounds"):
        require(regular_artifact(replacement_inventory.get(name + "-" + platform + "-" + arch + suffix, {})),
                "replacement_companion_inventory_missing")
    changed = [row for row in rows if row.get("case") == "runtime-byte-replacement"]
    flags = ("package_projection_unchanged", "derived_and_owned_executable_match", "old_child_collected", "fresh_child_collected",
             "foreign_bytes_preserved", "sibling_consumers_preserved", "policy_bytes_preserved", "preparation_retired", "test_retainer_retired")
    require(len(changed) == 1 and changed[0].get("old_primary_sha256") == main_hash and changed[0].get("new_primary_sha256") == replacement
            and all(changed[0].get(flag) is True for flag in flags)
            and changed[0].get("old_provider_requests") == changed[0].get("fresh_provider_requests") == 1
            and changed[0].get("old_webhook_count") == 0 and changed[0].get("fresh_webhook_count") == 1
            and success_counts(changed[0].get("old_native_counts")) and success_counts(changed[0].get("fresh_native_counts")),
            "runtime_byte_replacement_not_observed")
    require(desktop.get("native_product_observations_passed") is True and desktop.get("owned_children_collected") is True,
            "native_desktop_or_collection_not_observed")
    native = desktop.get("cases", [])
    require(len(native) == len(CASES) and {row.get("case") for row in native} == CASES, "native_desktop_case_missing_or_duplicate")
    for row in native:
        require(all(row.get("checks", {}).get(check) is True for check in CHECKS), "native_desktop_check_unobserved:" + str(row.get("case")))
        if row["case"] == "question":
            require(row.get("pending_question_at_click") is True, "native_question_was_not_pending")
    if platform == "linux":
        lifecycle = desktop.get("lifecycle_cases", [])
        require(len(lifecycle) == 5 and {row.get("case") for row in lifecycle} == {"reload", "new", "prompt-end", "quit", "timeout"},
                "linux_actual_pi_cancellation_case_missing")
        for row in lifecycle:
            helpers = row.get("helper_identities", [])
            expected = 2 if row["case"] == "reload" else 1
            require(row.get("status") == "passed" and row.get("scope") == "actual_installed_pi_linux_tui"
                    and all(row.get(key) is True for key in ("original_helper_subtree_collected", "terminal_collected", "no_resend"))
                    and len(helpers) >= expected and all(kernel_identity(helper) for helper in helpers)
                    and len({helper["pid"] for helper in helpers}) == len(helpers)
                    and kernel_identity(row.get("pi_identity")), "linux_actual_pi_cancellation_unobserved")
            require(row.get("admitted_helpers") == expected and row.get("admitted_webhook_requests") == 1
                    and row.get("helper_admission_counts") == ([1, 2, 2] if row["case"] == "reload" else [1]),
                    "linux_pi_helper_admission_bound_unobserved")
            if row["case"] == "timeout":
                duration = row.get("natural_timeout_seconds")
                require(type(duration) in (int, float) and math.isfinite(duration) and 9 <= duration < 13
                        and row.get("production_http_timeout_seconds") == 10 and row.get("no_control_before_timeout") is True,
                        "linux_original_webhook_timeout_unobserved")
            else:
                duration = row.get("cancellation_seconds")
                require(type(duration) in (int, float) and math.isfinite(duration) and 0 <= duration < 3,
                        "linux_original_helper_cancellation_was_not_prompt")
            if row["case"] in ("reload", "new"):
                require(row.get("fresh_observer_delivered_once") is True, "linux_pi_reset_did_not_restore_observer")
            if row["case"] == "reload":
                require(row.get("excess_observation_dropped") is True and row.get("settled_before_control") == 3,
                        "linux_pi_hung_child_stalled_or_queued")
        mux = desktop.get("multiplexer", {})
        require(mux.get("case") == "tmux-completion" and mux.get("native_mode") == "tui"
                and all(mux.get("checks", {}).get(check) is True for check in CHECKS)
                and all(mux.get(key) is True for key in ("real_mouse_click", "banner_observed", "outer_window_focus_observed", "owned_tmux_server_collected"))
                and mux.get("pane_focus_supported") is False
                and re.fullmatch(r"%[0-9]+", mux.get("active_pane_before_click", "")) is not None
                and mux.get("active_pane_before_click") != mux.get("origin_tmux_pane")
                and mux.get("active_pane_after_click") == mux.get("active_pane_before_click")
                and kernel_identity(mux.get("tmux_server_identity"))
                and re.fullmatch(r"%[0-9]+", mux.get("origin_tmux_pane", "")) is not None,
                "linux_real_tmux_window_fallback_or_collection_unobserved")
        for row in [mux, *[row for row in native if row["case"] not in ("sound-off", "desktop-off")]]:
            correlation = row.get("configured_cue_envelope_match")
            require(type(correlation) in (int, float) and math.isfinite(correlation) and correlation >= 0.65,
                    "linux_canonical_cue_not_observed")
    if platform == "darwin":
        pin = B.H.load_pin()
        B.H.candidate_tree(pin)
        B.H.validate_receipt(build.get("native_helper_input"), pin)
        helper = desktop.get("helper", {})
        B.H.validate_receipt(helper.get("input"), pin)
        attestation = inventory.get("ClaudeNotifier.app.managed-runtime.json", {})
        require(desktop.get("native_helper_verified") is True and regular_artifact(attestation)
                and attestation["sha256"] == build.get("native_attestation_sha256") == pin["attestation_sha256"]
                and inventory.get("ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern", {}).get("sha256") == pin["executable_sha256"]
                and helper.get("archive_sha256") == pin["archive_sha256"]
                and helper.get("executable_sha256") == pin["executable_sha256"]
                and helper.get("source_stage_attestation_sha256") == pin["attestation_sha256"],
                "macos_candidate_native_helper_unverified")
    if platform == "windows":
        require(build.get("embedded_callback_verified") is True
                and regular_artifact(inventory.get("claude-notifications-windows-amd64-focus.exe", {}))
                and desktop.get("windows_client") is True and desktop.get("render_route_observed") is True,
                "windows_client_audio_callback_or_focus_unobserved")
        route = desktop.get("audio_capture_route", {})
        require(isinstance(route, dict) and route.get("kind") in {"render_loopback", "signed_virtual_cable_capture"}
                and isinstance(route.get("render_endpoint_id"), str) and route["render_endpoint_id"],
                "windows_actual_audio_route_missing")
        prerequisite = desktop.get("prerequisite_measurements", {})
        peak = prerequisite.get("audio_peak")
        require(desktop.get("prerequisites", {}).get("status") == "prerequisites_ready"
                and prerequisite.get("audio_route") == route and prerequisite.get("known_cue_method") == "PlaySoundW"
                and type(prerequisite.get("audio_samples")) is int and prerequisite["audio_samples"] >= 4800
                and type(peak) in (int, float) and math.isfinite(peak) and peak > 0.01,
                "windows_original_winmm_cue_not_observed_on_selected_route")
        require(all(row.get("audio", {}).get("audio_route") == route for row in native), "windows_audio_route_changed_between_cases")
        if route["kind"] == "signed_virtual_cable_capture":
            fixture = desktop.get("audio_fixture")
            W.validate(fixture, source)
            require(desktop.get("audio_fixture_verified") is True and fixture["route"] == route,
                    "windows_signed_virtual_route_not_verified")
        if desktop.get("host_architecture") in ("ARM64", "arm64", "aarch64") and arch == "amd64":
            require(desktop.get("execution_mode") == "x64_emulation", "windows_emulation_not_disclosed")
    return {"status": "qualified", "candidate_binary_sha256": main_hash, "package_sha256": package_hash,
            "pi_binary_sha256": acquisition["binary_sha256"], "cases": sorted(CASES)}


def regular_artifact(value):
    return (isinstance(value, dict) and not value.get("link") and isinstance(value.get("sha256"), str)
            and re.fullmatch(r"[a-f0-9]{64}", value["sha256"]) is not None
            and type(value.get("bytes")) is int and 0 < value["bytes"] <= 512 * 1024 * 1024)


def kernel_identity(value):
    return isinstance(value, dict) and all(type(value.get(key)) is int and value[key] > 0 for key in ("pid", "start_ticks")) \
        and isinstance(value.get("executable"), str) and value["executable"].startswith("/")


def success_counts(value):
    return isinstance(value, dict) and all(type(value.get(key)) is int and value[key] == count for key, count in SUCCESS_COUNTS.items())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--evidence-root", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    require(re.fullmatch(r"[a-f0-9]{40}", args.source_sha), "exact_source_sha_required")
    actual = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip()
    require(actual == args.source_sha, "aggregate_checkout_source_mismatch")
    result = {"schema_version": 1, "source_sha": args.source_sha, "status": "unqualified", "desktop_qualified": False, "platforms": {}}
    for platform in HOSTS:
        root = args.evidence_root / platform
        try:
            desktop = root / "desktop.json"
            if not desktop.exists():
                desktop = root / "evidence.json"  # Existing macOS artifact name.
            values = [read(root / name) for name in ("build.json", "acquisition.json", "process.json")]
            result["platforms"][platform] = qualify(platform, args.source_sha, *values, read(desktop))
        except (OSError, ValueError, KeyError, TypeError, RuntimeError) as error:
            result["platforms"][platform] = {"status": "unqualified", "reason": str(error)}
    if all(value["status"] == "qualified" for value in result["platforms"].values()):
        result.update(status="qualified", desktop_qualified=True)
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))
    return 0 if result["desktop_qualified"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
