#!/usr/bin/env python3
"""Real common setup -> installed Pi observer -> loopback webhooks/lifecycle.

No product extension/locator/policy is hand-written, and pi-event is never called
by the fixture. Native desktop acceptance is a separate mandatory three-OS gate.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import platform
import shutil
import subprocess
import time

REPO = Path(__file__).resolve().parents[1]


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, REPO / "tests/integration" / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


F = load("pi_native_fixture", "pi_native_fixture.py")
P = load("pi_installed_fixture", "pi_installed_fixture.py")
R = load("pi_runtime_replacement", "pi_runtime_replacement.py")


# CLI results may include recovery commands, profiles and provider errors. Only
# these closed product codes enter portable evidence; arbitrary text is omitted.
_COMMAND_CODES = {
    "action": {"install", "update", "repair", "uninstall", "inspect"},
    "outcome": {"completed", "unchanged", "incomplete", "invalid", "conflict", "cancelled",
                "installed", "removed", "absent", "activation_failed", "ready"},
    "reason": {"cursor_composition_required", "cursor_identity_unavailable", "local_composition_required",
               "global_config_parent_invalid", "activation_incomplete", "portable_preflight_failed",
               "portable_remove_failed", "invalid_arguments", "pending_setup_required", "concurrent_change",
               "state_migration_required", "state_requires_inspection", "live_profile_conflict",
               "package_acquisition_failed", "recorded_package_unavailable", "recovery_required",
               "owned_profile_unavailable", "binding_not_installed", "activation_failed"},
    "client": {"pi", "cursor", "claude", "codex", "copilot-vscode"},
    "failureStage": {"preflight", "materialization", "activation", "verification", "deactivation", "removal"},
}


def _command_failure(result):
    """Project a failed CLI response to bounded, non-private product codes."""
    diagnostic = {"exit_code": result.returncode, "stderr_present": bool(result.stderr)}
    try:
        value = json.loads(result.stdout)
    except (ValueError, UnicodeDecodeError):
        return diagnostic
    if not isinstance(value, dict):
        return diagnostic
    diagnostic["json_stdout"] = True

    def codes(source, keys):
        safe = {}
        for key in keys:
            entry = source.get(key)
            if isinstance(entry, str) and entry in _COMMAND_CODES[key]:
                safe[key] = entry
            elif entry is not None:
                safe[key + "_redacted"] = True
        return safe

    diagnostic.update(codes(value, ("action", "outcome", "reason")))
    targets = value.get("targets")
    if isinstance(targets, list):
        diagnostic["targets"] = [codes(row, ("client", "outcome", "reason", "failureStage"))
                                 for row in targets[:8] if isinstance(row, dict)]
    return diagnostic


def command(executable, args, root, env):
    result = subprocess.run([str(executable), *map(str, args)], stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=root / "project", env=env, timeout=180)
    P.require(len(result.stdout) + len(result.stderr) <= 4 * 1024 * 1024, "product_command_output_limit")
    if result.returncode != 0:
        name = str(args[0]) if str(args[0]) in ("setup-notifications", "internal-install-runtime") else "unknown"
        raise RuntimeError("product_command_failed:" + name + ":" + json.dumps(_command_failure(result), sort_keys=True))
    return result.stdout


def run(args):
    root = P.new_test_root("TEST-pi-installed-")
    sink, first_provider = P.Webhooks(), F.Provider("success")
    env = F.prepare(root, first_provider, "success")
    env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
    profile = root / "agent"
    report = {"schema_version": 1, "scope": "actual_common_setup_installed_pi_process", "status": "failed",
              "test_root": str(root),
              "source_sha": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip(),
              "script_sha256": P.sha(__file__),
              "fixture_sha256": {name: P.sha(REPO / "tests/integration" / name)
                                  for name in ("pi_native_fixture.py", "pi_installed_fixture.py")},
              "working_tree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO)),
              "host_os": platform.system(), "host_architecture": platform.machine(),
              "candidate_binary_sha256": P.sha(args.candidate), "package_sha256": P.sha(args.package),
              "replacement_candidate_binary_sha256": P.sha(args.replacement_runtime_stage / args.candidate.name),
              "updated_package_sha256": P.sha(args.updated_package), "pi_binary_sha256": P.sha(args.pi_executable),
              "desktop_qualified": False, "release_equivalent_qualified": False, "cases": []}
    held = []

    def setup(action, package=None, extra=(), executable=None):
        foreign = [profile / "settings.json", profile / "models.json", profile / "extensions/TEST-witness.ts"]
        before = {str(path): P.sha(path) for path in foreign if path.is_file()}
        selected_pi = args.pi_executable
        if action == "uninstall":
            P.require(shutil.which("pi", path=env["PATH"]) is None, "cli_missing_removal_requires_no_pi_on_path")
            selected_pi = None
        value = P.setup(executable or args.candidate, action, root, package or args.package, selected_pi, env, extra)
        P.require(all(path.is_file() and P.sha(path) == before[str(path)] for path in foreign if str(path) in before),
                  "common_pi_setup_changed_foreign_settings_or_extension")
        report["cases"].append({"case": "setup-" + action, "outcome": value["outcome"],
                                "foreign_bytes_preserved": True, "pi_executable_supplied": selected_pi is not None})
        return value

    def native(case, mode="json", expected="task_complete"):
        case_root = root / ("case-" + str(len(report["cases"])) + "-" + case + "-" + mode)
        provider = F.Provider(case)
        process = None
        try:
            native_env = F.prepare(case_root, provider, case, agent_dir=profile)
            native_env["CLAUDE_PLUGIN_ROOT"] = str(root / "foreign-TEST-root")
            process = F.PiProcess(args.pi_executable, case_root, native_env, mode)
            if mode == "rpc":
                process.send({"id": "TEST-prompt", "type": "prompt", "message": "/TEST-dialog" if case == "ui" else "TEST say done"})
                aborted = False
                while True:
                    row = process.next()
                    if row.get("type") == "extension_ui_request" and row.get("method") == "input":
                        sink.wait("question")
                        before = F.witness(case_root)
                        P.require(sum(x["type"] == "ui_prompt_start" for x in before) == 1
                                  and not any(x["type"] == "ui_prompt_end" for x in before), "installed_question_not_pending")
                        process.send({"type": "extension_ui_response", "id": row["id"], "value": "TEST accepted"})
                    if case == "ui" and row.get("type") == "response" and row.get("id") == "TEST-prompt":
                        break
                    if case == "abort" and not aborted and row.get("type") == "message_update":
                        aborted = True
                        process.send({"id": "TEST-abort", "type": "abort"})
                    if row.get("type") == "agent_settled":
                        break
                process.child.stdin.close()
            code = process.wait()
            P.require(code == 0 and process.stderr_bytes == 0, "installed_pi_exit_or_loader_diagnostic")
            witness = F.witness(case_root)
            counts = F.verify_case(case, witness, provider.requests)
            outcomes = [value["outcome"] for value in witness if value["type"] == "agent_before_settle"]
            if case not in ("abort", "ui") and expected:
                sink.wait(expected)
            sink.quiet()
            row = {"case": case, "mode": mode, "native_counts": counts,
                   "native_outcome": outcomes[-1] if outcomes else None,
                   "provider_requests": len(provider.requests), "webhook": expected, "child_collected": True}
            report["cases"].append(row)
            return row
        finally:
            if process:
                process.close()
            provider.close()

    def drain(hung):
        case_root = root / ("case-hung-quit" if hung else "case-held-quit")
        destination, provider = P.Webhooks(hold_response=True), F.Provider("success")
        process = None
        started = time.monotonic()
        try:
            P.configure(root / "global/config.json", args.assets_root / "config/config.json", destination, desktop=False)
            native_env = F.prepare(case_root, provider, "success", agent_dir=profile)
            process = F.PiProcess(args.pi_executable, case_root, native_env, "json")
            destination.wait("task_complete")
            time.sleep(0.3)
            P.require(process.child.poll() is None, "installed_pi_quit_dropped_inflight_webhook")
            if not hung:
                destination.release.set()
            P.require(process.wait(timeout=20) == 0, "installed_pi_quit_failed")
            elapsed = time.monotonic() - started
            P.require(elapsed < 19, "installed_pi_quit_exceeded_original_budget")
            destination.quiet()
            counts = F.verify_case("success", F.witness(case_root), provider.requests)
            report["cases"].append({"case": "hung-quit-original-budget" if hung else "normal-quit-drains-admitted-webhook",
                                    "native_counts": counts, "held_request_count": len(destination.rows),
                                    "elapsed_seconds": round(elapsed, 3), "child_collected": True})
        finally:
            if process:
                process.close()
            provider.close()
            destination.close()
            P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink, desktop=False)

    try:
        P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink, desktop=False)
        (root / "foreign-TEST-root").mkdir()
        command(args.candidate, ["internal-install-runtime", "--stage", args.runtime_stage, "--target", root / "runtime/bin",
                                "--entry", args.candidate.name, "--control-root", root / "control",
                                "--consumer", "pi-runtime-preparation"], root, env)
        # Deliberately no --pi-agent-dir: use actual environment-only discovery.
        setup("install", extra=("--hooks", "false", "--agent-notify", "true", "--pi-desktop", "false", "--pi-webhook", "true"))
        policy = json.loads((root / "control/agent-notifications.json").read_text(encoding="utf-8"))
        P.require(policy.get("enabled") is False, "pi_setup_enabled_unrelated_mcp_policy")
        report["unrelated_mcp_policy_enabled"] = False
        initial = P.installed(root / "control", profile)
        P.require(P.sha(initial["runtime"]["executable"]) == P.sha(args.candidate), "installed_pi_candidate_bytes_differ")
        report["installed_asset_sha256"] = P.asset_receipts(root / "control", root / "runtime", args.assets_root)
        report["active_leaf"] = initial["active"].name
        report["env_only_profile_discovery"] = True
        setup("install")
        P.require(P.installed(root / "control", profile)["runtime"] == initial["runtime"], "installed_pi_reinstall_revision_changed")
        derived = Path(initial["binding"]["dataRoot"]) / "pi-runtime.json"
        original_derived = derived.read_bytes()
        derived.unlink()
        setup("install")
        P.require(derived.read_bytes() == original_derived and P.installed(root / "control", profile)["runtime"] == initial["runtime"],
                  "unchanged_reinstall_did_not_restore_exact_derived_metadata")
        report["cases"].append({"case": "unchanged-reinstall-restores-derived-metadata", "projection_unchanged": True})
        for case, mode, expected in (("success", "json", "task_complete"), ("success", "print", "task_complete"),
                                     ("retry", "json", "task_complete"), ("followup", "json", "task_complete"),
                                     ("error", "json", "api_error"), ("abort", "rpc", None), ("ui", "rpc", "question")):
            native(case, mode, expected)
        drain(False)
        drain(True)
        # Effective configuration and saved installer consent independently
        # restrict the same real observer. Both negatives wait collected quit.
        config_path = root / "global/config.json"
        disabled = json.loads(config_path.read_text(encoding="utf-8"))
        disabled["notifications"]["webhook"]["enabled"] = False
        config_path.write_text(json.dumps(disabled) + "\n", encoding="utf-8")
        native("success", expected=None)
        report["cases"][-1]["case"] = "webhook-config-disabled"
        P.configure(config_path, args.assets_root / "config/config.json", sink, desktop=False)
        setup("install", extra=("--pi-desktop", "false", "--pi-webhook", "false"))
        native("success", expected=None)
        report["cases"][-1]["case"] = "webhook-consent-disabled"
        setup("install", extra=("--pi-desktop", "false", "--pi-webhook", "true"))
        replacement_case, retire_cursor = R.exercise(root, profile, args, env, sink, fixture=F, product=P,
                                                     command=command, native=native)
        report["cases"].append(replacement_case)
        # The same live native process captures A before the real package update.
        live_root = root / "live-A"
        live_provider = F.Provider("success")
        live_env = F.prepare(live_root, live_provider, "success", agent_dir=profile)
        live = F.PiProcess(args.pi_executable, live_root, live_env, "rpc")
        held.append((live, live_provider))
        live.send({"id": "TEST-ready", "type": "get_commands"})
        while live.next().get("id") != "TEST-ready":
            pass
        setup("update", args.updated_package)
        updated = P.installed(root / "control", profile)
        P.require(updated["runtime"]["loaded_revision"] != initial["runtime"]["loaded_revision"], "installed_pi_update_did_not_change_revision")
        live.send({"id": "TEST-old", "type": "prompt", "message": "TEST say done"})
        while live.next().get("type") != "agent_settled":
            pass
        F.verify_case("success", F.witness(live_root), live_provider.requests)
        live.child.stdin.close()
        P.require(live.wait() == 0, "installed_pi_old_process_not_collected")
        live_provider.close()
        held.remove((live, live_provider))
        sink.quiet(1)
        report["cases"].append({"case": "live-A-update-old-revision-denied", "native_settled": True, "webhook": None, "child_collected": True})
        native("success")
        # Missing owned package repair uses only the normal wizard.
        shutil.rmtree(updated["active"])
        setup("repair", args.updated_package)
        repaired = P.installed(root / "control", profile)
        P.require(repaired["active"].is_dir(), "installed_pi_repair_missing")
        native("success")
        live_root = root / "live-B"
        live_provider = F.Provider("success")
        live_env = F.prepare(live_root, live_provider, "success", agent_dir=profile)
        live = F.PiProcess(args.pi_executable, live_root, live_env, "rpc")
        held.append((live, live_provider))
        live.send({"id": "TEST-current", "type": "prompt", "message": "TEST say done"})
        while live.next().get("type") != "agent_settled":
            pass
        sink.wait("task_complete")
        setup("uninstall", args.updated_package)
        P.require(not repaired["active"].exists(), "installed_pi_extension_survived_removal")
        live.send({"id": "TEST-removed", "type": "prompt", "message": "TEST say done"})
        while live.next().get("type") != "agent_settled":
            pass
        live.child.stdin.close()
        P.require(live.wait() == 0, "installed_pi_removed_process_not_collected")
        live_provider.close()
        held.remove((live, live_provider))
        sink.quiet(1)
        report["cases"].append({"case": "live-B-remove-current-revision-denied", "native_settled": True, "webhook": None, "child_collected": True})
        native("success", expected=None)
        report["cases"][-1]["case"] = "fresh-after-remove-no-effect"
        P.require((profile / "extensions/TEST-witness.ts").is_file(), "installed_pi_foreign_extension_removed")
        retire_cursor()
        report["status"] = "passed_installed_pi_process"
    except Exception as error:
        report["failure"] = str(error)
    finally:
        for process, provider in reversed(held):
            process.close()
            provider.close()
        first_provider.close()
        sink.close()
        report["webhook_status_counts"] = {status: sum(row["status"] == status for row in sink.rows) for status in P.COPY}
        report["webhook_private_payload_failures"] = len(sink.failures)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["status"] == "passed_installed_pi_process" else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "runtime-stage", "replacement-runtime-stage", "package", "updated-package", "assets-root", "pi-executable", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    for name in ("candidate", "runtime_stage", "replacement_runtime_stage", "package", "updated_package", "assets_root", "pi_executable"):
        setattr(args, name, getattr(args, name).resolve(strict=True))
    raise SystemExit(run(args))
