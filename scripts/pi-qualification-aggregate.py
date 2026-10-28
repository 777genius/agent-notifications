#!/usr/bin/env python3
"""Require one exact clean candidate and real installed Pi desktop on all 3 OSes."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess

REPO = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("pi_candidate_build", Path(__file__).with_name("pi-candidate-build.py"))
B = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(B)
PINS = json.loads((REPO / "scripts/testdata/pi-native/pins.json").read_text())
HOSTS = {"linux": "Linux", "darwin": "Darwin", "windows": "Windows"}
CASES = {"completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin"}
CHECKS = {"native_tui", "configured_banner_policy", "configured_audio_policy", "configured_click_policy",
          "native_event_counts", "ambient_root_isolated", "owned_terminal_collected"}
PROCESS_CASES = {"normal-quit-drains-admitted-webhook", "hung-quit-original-budget", "webhook-config-disabled",
                 "webhook-consent-disabled", "unchanged-reinstall-restores-derived-metadata",
                 "live-A-update-old-revision-denied", "live-B-remove-current-revision-denied",
                 "setup-update", "setup-repair", "setup-uninstall"}
NATIVE_MODES = {("success", "json"), ("success", "print"), ("retry", "json"), ("followup", "json"),
                ("error", "json"), ("abort", "rpc"), ("ui", "rpc")}


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
    main_hash, package_hash = build.get("candidate_binary_sha256"), build.get("package_sha256")
    require(isinstance(main_hash, str) and re.fullmatch(r"[a-f0-9]{64}", main_hash)
            and isinstance(package_hash, str) and re.fullmatch(r"[a-f0-9]{64}", package_hash), "candidate_hash_missing")
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
    require(desktop.get("native_product_observations_passed") is True and desktop.get("owned_children_collected") is True,
            "native_desktop_or_collection_not_observed")
    native = desktop.get("cases", [])
    require(len(native) == len(CASES) and {row.get("case") for row in native} == CASES, "native_desktop_case_missing_or_duplicate")
    for row in native:
        require(all(row.get("checks", {}).get(check) is True for check in CHECKS), "native_desktop_check_unobserved:" + str(row.get("case")))
        if row["case"] == "question":
            require(row.get("pending_question_at_click") is True, "native_question_was_not_pending")
    if platform == "darwin":
        require(desktop.get("native_helper_verified") is True
                and "ClaudeNotifier.app.managed-runtime.json" in inventory, "macos_candidate_native_helper_unverified")
    if platform == "windows":
        require(build.get("embedded_callback_verified") is True
                and "claude-notifications-windows-amd64-focus.exe" in inventory
                and desktop.get("windows_client") is True and desktop.get("render_route_observed") is True,
                "windows_client_audio_callback_or_focus_unobserved")
        if desktop.get("host_architecture") in ("ARM64", "arm64", "aarch64") and arch == "amd64":
            require(desktop.get("execution_mode") == "x64_emulation", "windows_emulation_not_disclosed")
    return {"status": "qualified", "candidate_binary_sha256": main_hash, "package_sha256": package_hash,
            "pi_binary_sha256": acquisition["binary_sha256"], "cases": sorted(CASES)}


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
