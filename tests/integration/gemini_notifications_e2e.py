#!/usr/bin/env python3
"""Built AN Gemini driver. Native mode is for the trusted orchestrator ONLY.

--self-test runs pure guards/validators, without children, HTTP, PTY or hooks.
Native mode reuses G0; it never invokes gemini-event or supplies hook stdin.
The driver verifies the actual readonly N2 inspect result alongside native delivery.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import stat
import sys
import threading
import tempfile
import time
import unittest

MARKER = ".an-gemini-TEST"
CONSUMER = "gemini-notifications"
OWN = {"AfterAgent": "agent-notifications-gemini-after-agent",
       "Notification": "agent-notifications-gemini-notification"}
COPY = {"task_complete": "Gemini CLI completed a turn",
        "permission_request": "Gemini CLI requested tool permission"}
HEX = re.compile(r"[0-9a-f]{64}")
UUID = re.compile(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}")
INSPECTED_PARSER_SHA256 = "08a7560511b619073da98a01bb530be43bd5dcc2303a63c0d9b0715591766c57"


class Red(Exception):
    pass


def require(ok, code):
    if not ok:
        raise Red(code)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def physical(value):
    p = Path(value)
    require(p.is_absolute() and p == p.resolve(strict=True), "physical_absolute_path_required")
    require(not any(c in str(p) for c in ('$','`','%', '\n', '\r', '"')), "unsupported_path")
    return p


def test_artifact(value):
    p = physical(value)
    require(any(re.fullmatch(r"TEST(?:[-_].*)?", x) for x in p.parts), "TEST_artifact_required")
    require(any((parent / MARKER).is_file() for parent in (p, *p.parents)), "TEST_artifact_marker_missing")
    repo = Path(__file__).resolve().parents[2]
    require(not (p == repo or repo in p.parents), "repository_artifact_forbidden")
    require(not (p == Path.home().resolve() or Path.home().resolve() in p.parents), "owner_home_artifact_forbidden")
    return p


def bounded_read(path, limit=65536):
    """No following links or opening FIFOs; snapshots stay in bounded memory."""
    physical(str(path.parent))
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    try:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode) and info.st_size <= limit, "bounded_regular_file_required")
        data = bytearray()
        while len(data) <= limit:
            block = os.read(fd, min(4096, limit + 1 - len(data)))
            if not block:
                break
            data.extend(block)
        require(len(data) <= limit, "file_limit")
        return bytes(data)
    finally:
        os.close(fd)


def read_json(path, limit=65536):
    # This is harness-owned evidence/config, not a replacement native parser.
    def unique(pairs):
        value = {}
        for key, item in pairs:
            require(key not in value, "ambiguous_evidence_fields")
            value[key] = item
        return value
    return json.loads(bounded_read(path, limit), object_pairs_hook=unique)


def write_json(path, value):
    require(not path.exists() or not path.is_symlink(), "owned_write_link_forbidden")
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    path.chmod(0o600)


def capture(body):
    """Exact existing generic JSON formatter contract; return fixed classes only."""
    require(isinstance(body, dict) and set(body) == {
        "schema_version", "status", "notification_type", "agent_source", "message",
        "timestamp", "session_id", "source", "title"}, "webhook_field_whitelist")
    status = body["status"]
    require(isinstance(status, str) and status in COPY, "webhook_status")
    require(body["schema_version"] == "1.0" and body["notification_type"] == status
            and body["agent_source"] == "gemini" and body["source"] == "claude-notifications"
            and body["title"] == "Gemini CLI" and body["message"] == COPY[status]
            and body["session_id"] == "", "webhook_fixed_copy")
    require(isinstance(body["timestamp"], str) and re.fullmatch(
        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})", body["timestamp"]), "webhook_timestamp")
    return status


def preserved(settings):
    """Keep foreign groups and every policy/unknown field, excluding just own groups."""
    result = copy.deepcopy(settings)
    hooks = result.get("hooks", {})
    for event, name in OWN.items():
        groups = hooks.get(event, [])
        require(isinstance(groups, list), "settings_event_shape")
        foreign = []
        for group in groups:
            require(isinstance(group, dict) and isinstance(group.get("hooks"), list), "settings_group_shape")
            owned = [h for h in group["hooks"] if h.get("name") == name]
            if owned:
                require(len(owned) == 1 and len(group["hooks"]) == 1, "mixed_or_duplicate_owned_group")
            else:
                foreign.append(group)
        hooks[event] = foreign
    return result


def same_install(before, after):
    require(before == after, "repeat_or_identical_update_changed_installation")


def changed_update(before, after):
    require(before["receipt"] == after["receipt"] and before["policy"] == after["policy"]
            and before["settings_sha256"] == after["settings_sha256"]
            and before["ledger"]["ID"] == after["ledger"]["ID"]
            and before["ledger"]["Consumers"] == after["ledger"]["Consumers"]
            and after["ledger"]["Generation"] == before["ledger"]["Generation"] + 1
            and after["ledger"]["PolicyGeneration"] == before["ledger"]["PolicyGeneration"] + 1,
            "changed_update_binding_or_generation_contract")


def native_rows(g0, lab):
    rows = g0.observations(lab)
    for row in rows:
        fields = {"event", "valid", "neutral", "session_sha256", "timestamp_sha256",
                  "shell", "shell_version", "case", "stop_hook_active", "subtype"}
        require(set(row) <= fields and row["event"] in OWN and row["case"] in g0.CASES,
                "observer_field_whitelist")
        require(row["valid"] is True and row["neutral"] is True, "foreign_observer_invalid")
        require(all(isinstance(row.get(k), str) and HEX.fullmatch(row[k]) for k in
                    ("session_sha256", "timestamp_sha256")), "observer_hash_contract")
        require(row.get("shell") in ("bash", "powershell") and isinstance(row.get("shell_version"), str)
                and re.fullmatch(r"[0-9][0-9A-Za-z.()_-]*", row["shell_version"]), "observer_shell_contract")
        if row["event"] == "AfterAgent":
            require(type(row.get("stop_hook_active")) is bool and "subtype" not in row, "observer_stop_flag")
        else:
            require(row.get("subtype") == "ToolPermission" and "stop_hook_active" not in row, "observer_subtype")
    # neutral refers exclusively to the foreign recorder's stdout.
    return rows


def snapshot(lab, candidate_hash):
    root = lab / "an-control"
    ledger = read_json(root / "ownership.json")
    receipt_path = root / "gemini-receipt.json"
    receipt = read_json(receipt_path, 16384)
    policy = read_json(root / "agent-notifications.json")
    settings_path = lab / "profile/.gemini/settings.json"
    settings = read_json(settings_path, 1024 * 1024)
    require(not (root / "transaction.json").exists(), "pending_transaction")
    require(ledger.get("Owner") == "existing-installer" and ledger.get("ID")
            and type(ledger.get("Generation")) is int and ledger["Generation"] > 0, "ledger_identity")
    consumer = ledger.get("Consumers", {}).get(CONSUMER, {})
    commands = consumer.get("Commands", [])
    binding = receipt.get("binding")
    require(receipt.get("version") == 1 and isinstance(binding, str) and re.fullmatch(r"[0-9a-f]{32}", binding), "receipt_binding")
    require(receipt.get("settingsPath") == str(settings_path) and consumer.get("Registration") == str(receipt_path)
            and consumer.get("RuntimeRoot") == str(lab / "an-runtime"), "receipt_registration")
    require(len(commands) == 6 and commands[1:] == ["gemini-event", "--control-root", str(root), "--binding", binding], "registered_command")
    executable = physical(commands[0])
    require(executable.parent == lab / "an-runtime", "registered_runtime_path")
    files = ledger.get("Files", {})
    require(all(os.path.normcase(name) != os.path.normcase(str(settings_path)) for name in files),
            "whole_settings_ownership_forbidden")
    for path, expected in ((executable, candidate_hash), (receipt_path, sha(bounded_read(receipt_path, 16384)))):
        # Windows preserves path spelling in JSON while Go and Python may
        # canonicalize its casing differently. Accept only one native spelling.
        matches = [identity for name, identity in files.items()
                   if os.path.normcase(name) == os.path.normcase(str(path))]
        require(len(matches) == 1, "owned_asset_path_identity")
        identity = matches[0]
        require(identity.get("Exists") is True and not identity.get("Link"), "owned_asset_kind")
        require(identity.get("SHA256") == expected, "owned_asset_ledger_hash")
        require(sha(bounded_read(path, 64 * 1024 * 1024)) == expected, "owned_asset_disk_hash")
    for event, name in OWN.items():
        groups = [group for group in settings["hooks"].get(event, []) if any(h.get("name") == name for h in group["hooks"])]
        require(len(groups) == 1 and len(groups[0]["hooks"]) == 1, "two_owned_hooks_required")
        hook = groups[0]["hooks"][0]
        require(hook.get("type") == "command" and hook.get("timeout") == 5000, "owned_hook_contract")
        require(event != "Notification" or groups[0].get("matcher") == "ToolPermission", "permission_matcher")
    # UAP owns rendering/full-group digests. Native delivery proves registration;
    # this driver never implements a second quoting or VerifyOwned engine.
    return {"ledger": ledger, "receipt": receipt, "policy": policy,
            "settings_sha256": sha(bounded_read(settings_path, 1024 * 1024))}, settings


def consent(lab, desktop, webhook):
    policy = read_json(lab / "an-control/agent-notifications.json")
    require(policy.get("route", {}).get("geminiNotifications") == {"desktop": desktop, "webhook": webhook}, "channel_consent")
    require(policy.get("enabled") is False and policy.get("AN_TEST_foreign_policy") == {"preserve": True}
            and policy.get("route", {}).get("foreign_TEST") == {"preserve": True}, "foreign_control_policy_changed")


def delivery_counts(rows):
    return {status: sum(row["event"] == ("AfterAgent" if status == "task_complete" else "Notification")
                        and not row.get("stop_hook_active", False) for row in rows) for status in COPY}


def settle(g0, lab, fixture, terminal, expected, seconds=6):
    end = time.monotonic() + seconds
    stable = None
    while time.monotonic() < end:
        terminal.drain()
        require(terminal.exit is None and fixture.error is None, fixture.error or ("native_child_exited" if terminal.exit is not None else "native_or_provider_failure"))
        native_rows(g0, lab)
        with fixture.lock:
            actual = {status: fixture.deliveries.count(status) for status in COPY}
        require(all(actual[k] <= expected[k] for k in COPY), "unexpected_or_duplicate_delivery")
        if actual == expected:
            stable = stable or time.monotonic()
            if time.monotonic() - stable >= 0.5:
                return actual
        else:
            stable = None
        time.sleep(0.05)
    raise Red("production_webhook_missing")


def another_turn(g0, lab, fixture, terminal, case="equal"):
    terminal.case = case
    previous = native_rows(g0, lab)
    fixture.arm(case)
    terminal.write_line("AN_TEST_PLAIN")
    end = time.monotonic() + 25
    while time.monotonic() < end:
        terminal.drain()
        require(terminal.exit is None and fixture.error is None, fixture.error or ("post_remove_native_child_exited" if terminal.exit is not None else "post_remove_native_or_provider_failure"))
        rows = native_rows(g0, lab)
        fresh = [x for x in rows[len(previous):] if x["case"] == case and x["event"] == "AfterAgent"]
        if fresh:
            require({r["session_sha256"] for r in fresh} == {previous[-1]["session_sha256"]}, "live_session_changed")
            old = {(r["session_sha256"], r["timestamp_sha256"]) for r in previous if r["event"] == "AfterAgent"}
            require(all((r["session_sha256"], r["timestamp_sha256"]) not in old for r in fresh), "new_turn_reused_cached_marker")
            return rows
        time.sleep(0.05)
    raise Red("foreign_recorder_inactive_after_remove")


def spool_request(value, owner):
    allowed = {"schemaVersion", "correlationID", "nonce", "bootID", "notAfter", "title", "body", "subtitle", "category", "silent", "action"}
    require(set(value) <= allowed and value.get("schemaVersion") == 1
            and value.get("correlationID") == owner["CorrelationID"] and value.get("nonce") == owner["Nonce"]
            and value.get("bootID") == owner["BootID"] and value.get("notAfter") == owner["NotAfter"], "desktop_request_identity")
    status = next((k for k, text in COPY.items() if text == value.get("body")), None)
    require(status and value.get("title") == "Gemini CLI" and value.get("subtitle", "") == ""
            and value.get("category") == ("info" if status == "task_complete" else "attention")
            and value.get("silent") is True and value.get("action") == "none", "desktop_fixed_copy")
    return status


def spool_receipt(value, owner):
    require(set(value) == {"schemaVersion", "correlationID", "nonce", "notificationID", "status", "reason", "retrySafe"}
            and type(value["schemaVersion"]) is int and value["schemaVersion"] == 1 and value["correlationID"] == owner["CorrelationID"]
            and value["nonce"] == owner["Nonce"] and value["notificationID"] == owner["CorrelationID"]
            and value["retrySafe"] is False, "desktop_receipt_identity")
    require((value["status"], value["reason"]) in {
        ("submitted", "os_accepted"), ("unknown", "timeout"),
        *(("rejected", r) for r in ("malformed_request", "unsupported_version", "unsupported_action", "invalid_file", "expired",
                                    "activation_required", "permission_denied", "unsupported_notifier", "os_rejected"))}, "desktop_receipt_status")
    return value["status"]


def watch_spool(lab, stop, evidence):
    """Observe existing Mac spool, never retain envelopes or interfere with cleanup."""
    root = lab / "an-control/gemini-native-spool"
    seen, owners, requests = set(), {}, {}
    try:
        # Covers two <=240s native watchdogs plus bounded reinstall/cleanup
        # commands. This observer owns no child; native timeouts remain in G0.
        for _ in range(96000):
            if stop.wait(0.01):
                return
            if not root.exists():
                continue
            physical(str(root))
            directories = list(root.iterdir())
            require(len(directories) <= 193, "desktop_spool_limit")
            for directory in directories:
                if not UUID.fullmatch(directory.name):
                    continue  # Permanent lock and native temporary/tombstone entries.
                try:
                    owner = read_json(directory / "owner.json", 1024)
                    require(set(owner) == {"SchemaVersion", "CorrelationID", "Nonce", "BootID", "NotAfter"}
                            and owner["SchemaVersion"] == 1 and owner["CorrelationID"] == directory.name
                            and isinstance(owner["Nonce"], str) and UUID.fullmatch(owner["Nonce"])
                            and isinstance(owner["BootID"], str) and 0 < len(owner["BootID"]) <= 128
                            and type(owner["NotAfter"]) in (float, int) and math.isfinite(owner["NotAfter"]), "desktop_spool_owner")
                    key = (directory.name, owner["Nonce"])
                    owners[key] = owner
                    require(len(owners) <= 64, "desktop_attempt_limit")
                    request_path = directory / (owner["Nonce"] + ".request")
                    if key not in requests and request_path.exists():
                        requests[key] = spool_request(read_json(request_path, 8192), owner)
                    receipt_path = directory / (owner["Nonce"] + ".receipt")
                    if key not in seen and receipt_path.exists():
                        result = spool_receipt(read_json(receipt_path, 8192), owner)
                        seen.add(key)
                        evidence["receipts"].append({"status": result, "fixed_copy_observed": key in requests,
                                                     "class": requests.get(key)})
                except FileNotFoundError:
                    pass  # Actual helper removes requests/receipts promptly.
        evidence["error"] = "spool_watchdog_timeout"
    except Exception as exc:
        evidence["error"] = str(exc) if isinstance(exc, Red) else "desktop_spool_contract_failed"


def ui_contract(path, install):
    path = physical(path)
    require(install in path.parents, "UI_contract_outside_TEST_installation")
    ui = read_json(path, 16384)
    require(set(ui) == {"permission_pattern", "approve", "deny", "cancel", "source_sha256"}
            and isinstance(ui["source_sha256"], str) and HEX.fullmatch(ui["source_sha256"]), "UI_source_contract")
    for key in ("approve", "deny"):
        require(isinstance(ui[key], str) and len(ui[key]) <= 32
                and re.fullmatch(r"(?:[1-9]|\x1b\[[AB])*\r", ui[key]), "UI_isolated_menu_keys_required")
    require(ui["cancel"] in ("\x1b", "\x03"), "UI_cancel_key")
    require(isinstance(ui["permission_pattern"], str) and 0 < len(ui["permission_pattern"]) <= 512, "UI_pattern_limit")
    re.compile(ui["permission_pattern"])
    return ui


def load_g0(value=None):
    sibling = Path(__file__).with_name("gemini_native_e2e.py")
    reference = Path(__file__).resolve().parents[2] / ".research/g0-reference/gemini_native_e2e.py"
    path = physical(value) if value else (sibling if sibling.is_file() else reference)
    require(path.is_file() and path.with_name("gemini_native_pty.cjs").is_file(), "G0_and_sibling_PTY_required")
    spec = importlib.util.spec_from_file_location("gemini_native_g0", path)
    g0 = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(g0)
    return g0, path


def setup(g0, args, lab, env, action, expected=0, binary_source=None):
    require(action in ("install", "update", "remove", "recover"), "uninspected_setup_action")
    argv = [args.binary, "setup-gemini", action, "--config-root", str(lab / "profile/.gemini"),
            "--control-root", str(lab / "an-control"), "--runtime-root", str(lab / "an-runtime")]
    if action in ("install", "update"):
        argv += ["--binary", binary_source or args.binary, "--webhook"]
        if args.desktop:
            argv += ["--desktop"]
            if args.native_app:
                argv += ["--native-app", args.native_app]
    code, out, err = g0.bounded_process(argv, b"", lab / "profile", env, 100)
    if code != expected:
        failure = Red("setup_" + action + "_exit_contract")
        failure.setup_diagnostic = setup_diagnostic(action, expected, code, out, err)
        raise failure
    if expected == 0:
        require(out + err == ("Gemini notifications " + action + " complete\n").encode(), "setup_success_copy_contract")
    # Raw subprocess output, including cleanup conflicts, is never evidence.


def setup_diagnostic(action, expected, code, out, err):
    """Fixed public installer errors only. Unknown output remains lengths/hashes."""
    require(action in ("install", "update", "remove", "recover"), "uninspected_setup_action")
    exact = {
        "managed inode owner mismatch": "managed_inode_owner_mismatch",
        "managed inode requires a private DACL": "managed_inode_private_DACL_required",
        "unsupported managed inode ACL": "managed_inode_ACL_unsupported",
        "managed inode DACL grants foreign access": "managed_inode_DACL_foreign_access",
        "managed paths require a local drive": "managed_local_drive_required",
        "managed path must be absolute and clean": "managed_absolute_clean_path_required",
        "managed Windows links require explicit reparse qualification": "managed_reparse_qualification_required",
        "binary format or architecture does not match Gemini target": "binary_target_mismatch",
        "bounded executable with managed writer protocol required": "binary_writer_protocol_required",
        "native settings unavailable": "native_settings_unavailable",
        "native settings parent changed": "native_settings_parent_changed",
        "native settings must be a regular file": "native_settings_regular_file_required",
        "Gemini hooks conflict: unsupported settings interpolation": "settings_interpolation_unsupported",
        "Gemini hooks conflict: unsupported target shell": "target_shell_unsupported",
        "Gemini hooks conflict: unsupported PowerShell native argument": "PowerShell_argument_unsupported",
        "gemini receipt is missing or changed": "receipt_missing_or_changed",
        "gemini receipt contract is invalid": "receipt_contract_invalid",
    }
    # English Go/Win32 public system messages. PathError may prefix a TEST
    # pathname; match only a complete fixed suffix and emit numeric codes.
    windows_errors = {
        "The system cannot find the file specified.": ("win32_file_not_found", 2),
        "The system cannot find the path specified.": ("win32_path_not_found", 3),
        "Access is denied.": ("win32_access_denied", 5),
        "The handle is invalid.": ("win32_invalid_handle", 6),
        "The process cannot access the file because it is being used by another process.": ("win32_sharing_violation", 32),
        "The process cannot access the file because another process has locked a portion of the file.": ("win32_lock_violation", 33),
        "The parameter is incorrect.": ("win32_invalid_parameter", 87),
        "Cannot create a file when that file already exists.": ("win32_already_exists", 183),
        "A required privilege is not held by the client.": ("win32_privilege_not_held", 1314),
    }
    categories, windows_codes = set(), set()
    for stream in (out, err):
        for line in stream.decode("utf-8", "replace").splitlines():
            if not line.startswith("setup-gemini: "):
                continue
            message = line[len("setup-gemini: "):]
            message = message.removeprefix("gemini consent revoked; cleanup conflict: ")
            if message in exact:
                categories.add(exact[message])
            for public, (label, number) in windows_errors.items():
                if message == public or message.endswith(": " + public):
                    categories.add(label)
                    windows_codes.add(number)
            # These public prefixes have arbitrary path suffixes; never copy them.
            for prefix, label in (("ambiguous managed Windows path component: ", "managed_Windows_path_ambiguous"),
                                  ("managed parent must be a non-reparse directory: ", "managed_parent_reparse_or_not_directory"),
                                  ("concurrent edit: ", "managed_concurrent_edit")):
                if message.startswith(prefix):
                    categories.add(label)
    return {"action": action, "expected_exit_code": expected, "exit_code": code,
            "classifications": sorted(categories) or ["unclassified_setup_error"], "win32_error_codes": sorted(windows_codes),
            "stdout_bytes": len(out), "stdout_sha256": sha(out), "stderr_bytes": len(err), "stderr_sha256": sha(err)}


def inspect(g0, args, lab, env, registered):
    argv = [args.binary, "setup-gemini", "inspect", "--control-root", str(lab / "an-control")]
    code, out, err = g0.bounded_process(argv, b"", lab / "profile", env, 12)
    require(code == 0 and not err, "setup_inspect_exit_contract")
    try:
        value = json.loads(out)
    except (ValueError, UnicodeError):
        raise Red("setup_inspect_result_contract")
    require(value == {"status": "installed" if registered else "absent", "registered": registered,
                      "desktop": bool(registered and args.desktop), "webhook": registered}, "setup_inspect_result_contract")
    return value


def configure(lab, url, desktop):
    config = {"schemaVersion": 2, "notifications": {"desktop": {"enabled": False}, "webhook": {"enabled": False}},
              "agents": {"gemini": {"notifications": {"desktop": {"enabled": desktop, "sound": False, "terminalBell": False},
                  "webhook": {"enabled": True, "preset": "custom", "format": "json", "url": url + "/capture",
                              "headers": {}, "payloadFields": {"AN_TEST_ENRICHMENT": "must-be-removed"},
                              "retry": {"enabled": False}, "rateLimit": {"enabled": False}, "circuitBreaker": {"enabled": False}}},
                  "statuses": {k: {"title": "AN_TEST_TITLE_must-be-removed"} for k in COPY}}}}
    path = lab / "an-control/config.json"
    write_json(path, config)
    # G0 deliberately has no config override env. Use the actual existing AN
    # legacy selector in our synthetic HOME, hard-linked to TEST control config.
    selected = lab / "profile/.claude/claude-notifications-go/config.json"
    selected.parent.mkdir(parents=True, mode=0o700)
    os.link(path, selected)
    require(os.path.samefile(path, selected), "TEST_config_selection_identity")
    return path


def run(args):
    require(args.trusted_orchestrator, "native_execution_requires_trusted_orchestrator")
    require(60 <= args.timeout <= 240, "native_watchdog_bounds")
    g0, g0path = load_g0(args.g0_driver)
    args.binary = str(test_artifact(args.binary))
    require(Path(args.binary).is_file(), "candidate_executable_required")
    if args.update_binary:
        args.update_binary = str(test_artifact(args.update_binary))
        require(Path(args.update_binary).is_file(), "actual_update_candidate_required")
    require(not args.desktop or sys.platform != "darwin" or args.native_app, "Mac_native_app_required")
    if args.native_app:
        args.native_app = str(test_artifact(args.native_app))
        require(sys.platform == "darwin" and args.desktop and Path(args.native_app).is_dir(), "Mac_app_with_desktop_required")
    install = test_artifact(args.cli_install_root)
    executable = str(physical(args.gemini_executable))
    identity = g0.cli_identity(executable, str(install))
    node, shell = physical(args.node_executable), physical(args.hook_shell)
    ui = ui_contract(args.ui_contract, install)
    modules = {}
    for label, value in (("SDK", args.sdk_module_root), ("installer", args.installer_module_root)):
        module = test_artifact(value)
        modules[label + "_public_module_tree_sha256"] = g0.package_digest(module)
        modules[label + "_go_mod_sha256"] = sha(bounded_read(module / "go.mod"))
    artifact_hash = sha(bounded_read(Path(args.binary), 32 * 1024 * 1024))
    update_hash = sha(bounded_read(Path(args.update_binary), 32 * 1024 * 1024)) if args.update_binary else None
    require(not update_hash or update_hash != artifact_hash, "changed_update_requires_distinct_actual_artifact")
    active_hash = artifact_hash
    lab = g0.new_lab(args.lab_root)
    levels = {osname: {"build/contracts": "unverified_external_evidence_required", "native_cli/provider_substitute": "unverified",
                       "OS_API": "unverified", "visual": "unverified"} for osname in ("darwin", "linux", "windows")}
    platform = {"darwin": "darwin", "linux": "linux", "win32": "windows"}[sys.platform]
    levels[platform]["candidate_sha256"] = artifact_hash
    levels[platform]["update_candidate_sha256"] = update_hash
    levels[platform]["CLI_package_tree_sha256"] = identity["installed_package_tree_sha256"]
    evidence = {"task": "an-next-g5", "platforms": levels, "driver": "failed", "CLI": identity,
                "candidate_sha256": artifact_hash, **modules, "G0_sha256": sha(g0path.read_bytes()),
                "PTY_bridge_sha256": sha(g0path.with_name("gemini_native_pty.cjs").read_bytes()),
                "driver_sha256": sha(Path(__file__).read_bytes()), "node_sha256": sha(node.read_bytes()),
                "native_helper_tree_sha256": g0.package_digest(Path(args.native_app)) if args.native_app else None,
                "inspect": "unverified",
                "inspected_setup_parser_sha256": INSPECTED_PARSER_SHA256,
                "production_neutral_stdout": "unverified_requires_separate_contract_evidence",
                "foreign_recorder_neutral_stdout": "unverified", "resume_restart": "unqualified",
                "continuation": "unqualified_no_blocking_hook", "nested": "unqualified",
                "old_loaded_command_gate": "unverified", "damaged_receipt": "unverified"}
    evidence["changed_artifact_update"] = "external_pending_actual_update_candidate" if not update_hash else "unverified"
    evidence["update_candidate_sha256"] = update_hash
    stop, spool = threading.Event(), {"receipts": [], "error": None}
    watcher, terminal, env, damaged_preimage = None, None, None, None
    try:
        env = g0.minimal_env(lab, node, shell, args.system_root)
        settings_path = g0.install_test_hooks(lab, {}, shell, node, args.system_root)
        foreign = read_json(settings_path, 1024 * 1024)
        for event in OWN:
            foreign["hooks"][event][0]["hooks"][0]["name"] = "foreign-an-TEST-" + event
        foreign["AN_TEST_unknown"] = {"preserve": True}
        write_json(settings_path, foreign)
        baseline = preserved(foreign)
        with g0.Fixture(lab, capture_validator=capture) as fixture:
            env["GOOGLE_GEMINI_BASE_URL"] = fixture.url
            (lab / "provider-port").write_text(str(fixture.server.server_port))
            evidence["native_version_probe"] = "running"
            code, out, version_err = g0.bounded_process([str(node), executable, "--version"], b"", lab / "profile", env, 60 if os.name == "nt" else 12)
            evidence["native_version_probe"] = g0.version_probe(code, out, version_err, (lab, install, node, node.parent))
            require(code == 0 and out.strip() == b"0.62.0", "actual_native_version")
            # Known parser errors/help are checked before installation mutations.
            for tail, expected in (([], 2), (["install", "--help"], 2), (["install", "--AN-TEST-unknown"], 2)):
                code, out, err = g0.bounded_process([args.binary, "setup-gemini", *tail], b"", lab / "profile", env, 12)
                require(code == expected, "setup_help_exit_contract")
                text = out + err
                if not tail:
                    require(text == b"usage: setup-gemini install|update|remove|recover|inspect|status|permission-status|request-permission [flags]\n",
                            "setup_usage_contract_changed_requires_parser_reinspection")
                elif tail[-1] == "--help":
                    require(b"Usage of setup-gemini install:\n" in text and all(
                        ("  -" + flag).encode() in text for flag in
                        ("binary", "config-root", "control-root", "runtime-root", "native-app", "desktop", "webhook")), "setup_help_flag_contract")
            require(not (lab / "an-control/ownership.json").exists()
                    and read_json(settings_path, 1024 * 1024) == foreign, "help_error_mutated_installation")
            write_json(lab / "an-control/agent-notifications.json", {
                "schemaVersion": 1, "enabled": False, "AN_TEST_foreign_policy": {"preserve": True},
                "route": {"foreign_TEST": {"preserve": True}}})
            setup(g0, args, lab, env, "install")
            consent(lab, args.desktop, True)
            config_path = configure(lab, fixture.url, args.desktop)  # After real explicit-consent setup.
            first, installed = snapshot(lab, artifact_hash)
            inspect(g0, args, lab, env, True)
            require(snapshot(lab, artifact_hash)[0] == first, "inspect_mutated_installation")
            evidence["inspect"] = "readonly_installed_status_observed"
            evidence["installed_executable_sha256"] = artifact_hash  # Verified ledger and physical executable bytes.
            require(preserved(installed) == baseline, "install_foreign_or_policy_changed")
            setup(g0, args, lab, env, "install")
            repeated, settings = snapshot(lab, artifact_hash)
            same_install(first, repeated)
            require(preserved(settings) == baseline, "repeat_foreign_changed")
            setup(g0, args, lab, env, "update")
            updated, settings = snapshot(lab, artifact_hash)
            same_install(first, updated)  # Same bytes/consent => unchanged generation AND nonce.
            setup(g0, args, lab, env, "recover")
            recovered, settings = snapshot(lab, active_hash)
            same_install(updated, recovered)  # Clean recovery, not injected crash proof.
            evidence["lifecycle"] = {"repeat": "no_op", "identical_update": "no_op", "binding_sha256": sha(first["receipt"]["binding"].encode()),
                                     "installation_sha256": sha(first["ledger"]["ID"].encode()), "generation": first["ledger"]["Generation"],
                                     "recover": "clean_state_no_op", "interrupted_recovery": "unverified"}
            if args.desktop and sys.platform == "darwin":
                watcher = threading.Thread(target=watch_spool, args=(lab, stop, spool), daemon=True)
                watcher.start()
            fixture.arm("plain")
            terminal = g0.Terminal(node, executable, install, lab, env, ui, args.timeout)
            cases, rows = g0.exercise(lab, fixture, terminal, ui, observer=lambda p: native_rows(g0, p))
            before_remove_counts = settle(g0, lab, fixture, terminal, delivery_counts(rows))
            require(snapshot(lab, active_hash)[0] == first, "live_session_installation_mutation")
            if args.update_binary:
                # Keep this same native CLI and its loaded command binding alive
                # across replacement with the separately supplied real artifact.
                setup(g0, args, lab, env, "update", binary_source=args.update_binary)
                changed, _ = snapshot(lab, update_hash)
                changed_update(first, changed)
                evidence["changed_artifact_update"] = "binding_preserved_generation_incremented"
                evidence["installed_executable_sha256"] = update_hash
                evidence["lifecycle"]["updated_generation"] = changed["ledger"]["Generation"]
                first, active_hash = changed, update_hash
                args.binary = args.update_binary
                another_turn(g0, lab, fixture, terminal)
                before_remove_counts["task_complete"] += 1
                settle(g0, lab, fixture, terminal, before_remove_counts)
                evidence["changed_artifact_update"] = "binding_preserved_generation_incremented_live_native_delivery"
            # One further native turn checks per-channel suppression without
            # replaying every UI/tool case. Desktop consent remains independent.
            config = read_json(config_path)
            suppressed = copy.deepcopy(config)
            suppressed["agents"]["gemini"]["statuses"]["task_complete"]["webhook"] = {"enabled": False}
            desktop_before = len(spool["receipts"])
            write_json(config_path, suppressed)  # Hard link keeps the selected inode.
            another_turn(g0, lab, fixture, terminal)
            settle(g0, lab, fixture, terminal, before_remove_counts)
            evidence["webhook_channel_suppression"] = "observed_native_turn_no_new_capture"
            evidence["desktop_channel_independence"] = "unverified"
            if args.desktop and any(r["class"] == "task_complete" and r["fixed_copy_observed"] and r["status"] == "submitted"
                                    for r in spool["receipts"][desktop_before:]):
                evidence["desktop_channel_independence"] = "correlated_desktop_acceptance_with_webhook_suppressed"
            write_json(config_path, config)
            setup(g0, args, lab, env, "remove")  # Gemini stays alive during actual revoke/cleanup.
            consent(lab, False, False)
            inspect(g0, args, lab, env, False)
            evidence["inspect"] = "readonly_installed_and_absent_status_observed"
            remaining = read_json(settings_path, 1024 * 1024)
            require(remaining == baseline, "remove_owned_entries_or_foreign_preservation")
            ledger = read_json(lab / "an-control/ownership.json")
            require(CONSUMER not in ledger.get("Consumers", {}) and not (lab / "an-control/gemini-receipt.json").exists(), "remove_registration_retained")
            rows = another_turn(g0, lab, fixture, terminal)
            settle(g0, lab, fixture, terminal, before_remove_counts)
            # Full native absence window; no hypothetical cached-hook claim.
            end = time.monotonic() + 5
            while time.monotonic() < end:
                settle(g0, lab, fixture, terminal, before_remove_counts, 1)
            terminal.close()
            evidence.update(cases=cases, native_observations=rows, delivery_counts=before_remove_counts,
                            PTY=terminal.identity, own_child_exit=terminal.exit,
                            provider_endpoints=fixture.counts, foreign_recorder_neutral_stdout="observed",
                            normal_remove="revoked_foreign_recorder_live_no_new_webhook_5s")
            terminal = None
            # Reinstall after normal cleanup; a damaged OWNED receipt leaves
            # installed commands/binary live, providing a real stale-command gate.
            setup(g0, args, lab, env, "install")
            second, _ = snapshot(lab, active_hash)
            require(second["receipt"]["binding"] != first["receipt"]["binding"], "reinstall_nonce_reused")
            fixture.arm("plain")
            previous = native_rows(g0, lab)
            terminal = g0.Terminal(node, executable, install, lab, env, ui, args.timeout)
            end = time.monotonic() + 25
            while time.monotonic() < end:
                terminal.drain()
                require(terminal.exit is None and fixture.error is None, "reinstall_native_or_provider_failure")
                now = native_rows(g0, lab)
                if any(r["event"] == "AfterAgent" for r in now[len(previous):]):
                    break
                time.sleep(0.05)
            else:
                raise Red("reinstall_native_completion_missing")
            expected = dict(before_remove_counts)
            expected["task_complete"] += 1
            settle(g0, lab, fixture, terminal, expected)
            damaged = lab / "an-control/gemini-receipt.json"
            require(str(damaged) in second["ledger"]["Files"], "damage_target_not_owned")
            intact = bounded_read(damaged, 16384)
            damaged_bytes = b"{\"AN_TEST_damaged_owned_receipt\":true}\n"
            damaged_preimage = (damaged, intact, damaged_bytes)
            damaged.write_bytes(damaged_bytes)
            setup(g0, args, lab, env, "remove", expected=1)
            consent(lab, False, False)
            revoked = read_json(lab / "an-control/ownership.json")
            require(revoked["ID"] == second["ledger"]["ID"]
                    and revoked["Generation"] == second["ledger"]["Generation"] + 1
                    and revoked["Consumers"][CONSUMER] == second["ledger"]["Consumers"][CONSUMER], "damaged_revoke_ledger_contract")
            require(snapshot_settings_equal(settings_path, second), "conflict_changed_owned_hooks")
            require(physical(second["ledger"]["Consumers"][CONSUMER]["Commands"][0]).is_file(), "conflict_removed_loaded_binary")
            another_turn(g0, lab, fixture, terminal, "recovery")
            settle(g0, lab, fixture, terminal, expected)
            end = time.monotonic() + 5
            while time.monotonic() < end:
                settle(g0, lab, fixture, terminal, expected, 1)
            terminal.close()
            evidence["damaged_receipt"] = "cleanup_conflict_channels_revoked_native_turn_suppressed"
            evidence["old_loaded_command_gate"] = "native_turn_with_retained_installed_groups_and_binary_no_new_webhook_5s"
            evidence["damaged_native_observations"] = native_rows(g0, lab)[len(previous):]
            evidence["damaged_revoked_generation"] = revoked["Generation"]
            evidence["reinstall_binding_sha256"] = sha(second["receipt"]["binding"].encode())
            evidence["final_delivery_counts"] = expected
            evidence["second_session_exit"] = terminal.exit
            evidence["second_session_PTY"] = terminal.identity
            terminal = None
            # Restore only our TEST corruption to its verified ledger preimage,
            # then perform real cleanup; never rewrite a foreign hook/asset.
            damaged.write_bytes(intact)
            damaged_preimage = None
            setup(g0, args, lab, env, "remove")
            consent(lab, False, False)
            require(read_json(settings_path, 1024 * 1024) == baseline, "damaged_scenario_cleanup_foreign_changed")
        evidence["driver"] = "passed_implemented_scenarios_with_external_gates_pending"
        levels[platform]["native_cli/provider_substitute"] = "passed_implemented_scenarios"
    except Exception as exc:
        evidence["classification"] = str(exc) if isinstance(exc, (Red, g0.Red)) else "production_harness_error"
        if hasattr(exc, "setup_diagnostic"):
            evidence["setup_failure"] = exc.setup_diagnostic
        if hasattr(exc, "bridge_diagnostic"):
            evidence["bridge_failure"] = exc.bridge_diagnostic
        if hasattr(exc, "provider_cleanup_classification"):
            evidence["provider_cleanup_classification"] = exc.provider_cleanup_classification
        evidence["exception_type"] = type(exc).__name__
        if "fixture" in locals():
            evidence["provider_endpoints"] = {key: fixture.counts.get(key, 0) for key in
                ("streamGenerateContent", "generateContent", "countTokens")}
        raise
    finally:
        if terminal is not None:
            try:
                terminal.close(graceful=False)
            except Exception as cleanup:
                evidence["shutdown"] = "unconfirmed"
                evidence["driver"] = "failed"
                evidence["cleanup_classification"] = str(cleanup) if isinstance(cleanup, g0.Red) else "bridge_cleanup_error"
            evidence["bridge_failure"] = terminal.failure_facts()
        if env is not None:
            try:
                if damaged_preimage:
                    path, intact, changed = damaged_preimage
                    require(bounded_read(path, 16384) == changed, "TEST_damage_changed_by_another_writer")
                    path.write_bytes(intact)
                if (lab / "an-control/ownership.json").exists():
                    setup(g0, args, lab, env, "remove")
                    consent(lab, False, False)
                evidence["final_cleanup"] = "verified_revoked"
            except Exception as cleanup:
                evidence["final_cleanup"] = "unverified_retained_cleanup_conflict"
                evidence["driver"] = "failed"
                if hasattr(cleanup, "setup_diagnostic"):
                    evidence["setup_cleanup_failure"] = cleanup.setup_diagnostic
        stop.set()
        if watcher:
            watcher.join(4)
            if watcher.is_alive():
                spool["error"] = "spool_shutdown_unconfirmed"
        evidence["desktop"] = spool
        if spool["error"]:
            evidence["driver"] = "failed"
        receipts = spool["receipts"]
        if receipts:
            levels[platform]["OS_API"] = "observed_correlated_receipts"
            if all(r["status"] == "submitted" and r["fixed_copy_observed"] for r in receipts):
                levels[platform]["OS_API"] = "observed_os_acceptance_and_fixed_copy"
            if any(r["status"] != "submitted" for r in receipts):
                levels[platform]["OS_API"] = "observed_delivery_failure"
                evidence["driver"] = "failed"
        if evidence["driver"] == "failed":
            levels[platform]["native_cli/provider_substitute"] = "failed"
        write_json(lab / "production-evidence.json", evidence)
    require(evidence["driver"] != "failed", "production_driver_failed")
    return {"driver": evidence["driver"], "evidence": str(lab / "production-evidence.json"), "inspect": evidence["inspect"],
            "visual": "unverified", "build/contracts": "unverified_external_evidence_required"}


def snapshot_settings_equal(path, state):
    return sha(bounded_read(path, 1024 * 1024)) == state["settings_sha256"]


class PureChecks(unittest.TestCase):
    def test_setup_failure_fixed_diagnostics(self):
        # Observable break: Windows install exit 1 previously lost its cause;
        # the public installer header can contain private path/error suffixes.
        out = b"setup-gemini: managed inode DACL grants foreign access\n"
        err = b"setup-gemini: open C:\\TEST private\\runtime: Access is denied.\n"
        facts = setup_diagnostic("install", 0, 1, out, err)
        self.assertEqual(facts["classifications"], ["managed_inode_DACL_foreign_access", "win32_access_denied"])
        self.assertEqual(facts["win32_error_codes"], [5])
        self.assertEqual(facts["stderr_sha256"], hashlib.sha256(err).hexdigest())
        self.assertNotIn("TEST private", json.dumps(facts))
        unknown = setup_diagnostic("remove", 0, 1, b"", b"setup-gemini: private arbitrary cause\n")
        self.assertEqual(unknown["classifications"], ["unclassified_setup_error"])
        self.assertNotIn("private arbitrary", json.dumps(unknown))
        forged = setup_diagnostic("install", 0, 1, b"provider: Access is denied.\n", b"setup-gemini: Access is denied. more text\n")
        self.assertEqual(forged["classifications"], ["unclassified_setup_error"])

    def test_bridge_failure_projection(self):
        # Observable break: generic G0 startup errors hid the failing stage;
        # fixed bridge facts must survive without arbitrary native error text.
        g0, _ = load_g0()
        facts = g0.bridge_event_facts({"error": "bridge_node_error", "node_error_code": "EINVAL",
                                      "stage": "native_spawn", "native_child_started": False,
                                      "message": "private Windows error"})
        self.assertEqual(facts, {"error": "bridge_node_error", "node_error_code": "EINVAL",
                                 "stage": "native_spawn", "native_child_started": False})
        unknown = g0.bridge_event_facts({"error": "private", "stage": "private", "node_error_code": "PRIVATE"})
        self.assertEqual(unknown, {"error": "bridge_protocol_error"})

    def test_bounded_read_preserves_binary_bytes(self):
        with tempfile.TemporaryDirectory(prefix="TEST-gemini-binary-read-") as root:
            path = Path(root).resolve() / "candidate.bin"
            data = b"MZ\r\nowned\x1aTEST\x00\r\n"
            path.write_bytes(data)
            self.assertEqual(bounded_read(path), data)

    def test_public_version_startup_diagnostic(self):
        # Observable break: Intel --version exit 1 had no ERR_/MODULE code;
        # lengths/hash alone do not distinguish an unavailable runtime global.
        g0, _ = load_g0()
        err = b"ReferenceError: File is not defined\n    at /tmp/TEST profile/cli.js:1:2\n"
        facts = g0.version_probe(1, b"", err)
        self.assertEqual(facts["startup_classification"], "public_runtime_global_missing")
        self.assertEqual(facts["stderr_sha256"], hashlib.sha256(err).hexdigest())
        self.assertNotIn("at /tmp", json.dumps(facts))
        concatenated = b"An unexpected critical error occurred:Error: spawn missing-test-command ENOENT\n    at /tmp/TEST/cli.js:1:2\n"
        startup = g0.version_probe(1, b"", concatenated)
        self.assertEqual(startup["Node_error_codes"], ["ENOENT"])
        self.assertEqual(startup["startup_error_line"], "Error: spawn missing-test-command ENOENT")
        excerpt = b'const banner = "Error: startup failed";\n                      ^\nSyntaxError: Invalid expression\n'
        self.assertEqual(g0.version_probe(1, b"", excerpt)["startup_error_line"], "SyntaxError: Invalid expression")
        private = b"Error: hook provider session text\n"
        self.assertNotIn("startup_error_line", g0.version_probe(1, b"", private))
        paths = b"Error: Cannot load /tmp/TEST profile/cli.js from https://example.invalid/file\n"
        sanitized = g0.version_probe(1, b"", paths, ("/tmp/TEST profile",))
        self.assertNotIn("TEST profile", sanitized.get("startup_error_line", ""))
        self.assertNotIn("example.invalid", sanitized.get("startup_error_line", ""))

    def payload(self, status="task_complete"):
        return {"schema_version": "1.0", "status": status, "notification_type": status, "agent_source": "gemini",
                "message": COPY[status], "timestamp": "2026-10-01T12:00:00Z", "session_id": "", "source": "claude-notifications", "title": "Gemini CLI"}

    def test_fixed_classes_and_privacy(self):
        for status in COPY:
            self.assertEqual(capture(self.payload(status)), status)
        for field in ("prompt", "tool", "message_details", "details", "transcript", "cwd", "project", "raw"):
            body = self.payload()
            body[field] = "private"
            with self.assertRaises(Red):
                capture(body)
        for field in ("message", "title", "session_id", "agent_source", "notification_type", "source", "schema_version"):
            body = self.payload()
            body[field] = "private"
            with self.assertRaises(Red):
                capture(body)

    def test_no_success_on_lifecycle_drift(self):
        state = {"receipt": {"binding": "nonce"}, "ledger": {"Generation": 4}, "settings_sha256": "before"}
        same_install(state, copy.deepcopy(state))
        for key in ("binding", "Generation", "settings_sha256"):
            changed = copy.deepcopy(state)
            target = changed["receipt"] if key == "binding" else changed["ledger"] if key == "Generation" else changed
            target[key] = "changed"
            with self.assertRaises(Red):
                same_install(state, changed)

    def test_foreign_and_policy_preservation(self):
        foreign = {"hooksConfig": {"enabled": False, "disabled": ["foreign"]}, "unknown": [1],
                   "hooks": {"AfterAgent": [{"hooks": [{"name": "foreign", "command": "recorder"}]}], "Notification": []}}
        installed = copy.deepcopy(foreign)
        for event, name in OWN.items():
            installed["hooks"][event].append({"hooks": [{"name": name, "command": "production"}]})
        self.assertEqual(preserved(installed), foreign)
        installed["hooksConfig"]["enabled"] = True
        self.assertNotEqual(preserved(installed), foreign)
        installed["hooks"]["AfterAgent"][-1]["hooks"].append({"name": "foreign"})
        with self.assertRaises(Red):
            preserved(installed)

    def test_changed_update_preserves_binding_and_advances_generation(self):
        before = {"receipt": {"binding": "nonce"}, "policy": {"webhook": True}, "settings_sha256": "same",
                  "ledger": {"ID": "installation", "Consumers": {"gemini": "same"}, "Generation": 4, "PolicyGeneration": 4}}
        after = copy.deepcopy(before)
        after["ledger"].update(Generation=5, PolicyGeneration=5)
        changed_update(before, after)
        for field in ("Generation", "PolicyGeneration", "ID"):
            changed = copy.deepcopy(after)
            changed["ledger"][field] = before["ledger"][field]
            if field == "ID":
                changed["ledger"][field] = "different"
            with self.assertRaises(Red):
                changed_update(before, changed)
        after["receipt"]["binding"] = "new_nonce"
        with self.assertRaises(Red):
            changed_update(before, after)

    def test_receipt_identity_and_visual_separation(self):
        owner = {"CorrelationID": "00000000-0000-4000-8000-000000000001", "Nonce": "00000000-0000-4000-8000-000000000002"}
        value = {"schemaVersion": 1, "correlationID": owner["CorrelationID"], "nonce": owner["Nonce"],
                 "notificationID": owner["CorrelationID"], "status": "submitted", "reason": "os_accepted", "retrySafe": False}
        self.assertEqual(spool_receipt(value, owner), "submitted")
        for field in ("nonce", "correlationID", "notificationID", "reason"):
            changed = dict(value, **{field: "wrong"})
            with self.assertRaises(Red):
                spool_receipt(changed, owner)
        owner.update(BootID="TEST-boot", NotAfter=123.0)
        request = {"schemaVersion": 1, "correlationID": owner["CorrelationID"], "nonce": owner["Nonce"],
                   "bootID": owner["BootID"], "notAfter": owner["NotAfter"], "title": "Gemini CLI", "body": COPY["task_complete"],
                   "category": "info", "silent": True, "action": "none"}
        self.assertEqual(spool_request(request, owner), "task_complete")
        for extra in ({"cwd": "/private"}, {"action": None}, {"action": {"type": "focus"}}, {"subtitle": "private"}, {"body": "private"}):
            with self.assertRaises(Red):
                spool_request(dict(request, **extra), owner)

    def test_ui_and_artifact_guards(self):
        with self.assertRaises(Red):
            test_artifact(str(Path(__file__).resolve()))
        for path in ("relative/TEST/binary", "/tmp/TEST/../binary"):
            with self.assertRaises((Red, FileNotFoundError)):
                test_artifact(path)
        with self.assertRaises(Red):
            setup(None, None, None, None, "inspect")
        with self.assertRaises(Red):
            run(argparse.Namespace(trusted_orchestrator=False))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--trusted-orchestrator", action="store_true", help="external trusted runner attestation; never use inside provider sandbox")
    parser.add_argument("--desktop", action="store_true")
    parser.add_argument("--timeout", type=int, default=240)
    for flag in ("binary", "update-binary", "native-app", "gemini-executable", "node-executable", "cli-install-root", "lab-root", "hook-shell", "system-root",
                 "ui-contract", "g0-driver", "sdk-module-root", "installer-module-root"):
        parser.add_argument("--" + flag)
    args = parser.parse_args()
    if args.self_test:
        result = unittest.TextTestRunner(stream=sys.stderr).run(unittest.defaultTestLoader.loadTestsFromTestCase(PureChecks))
        require(result.wasSuccessful(), "pure_checks_failed")
        return {"pure_checks": "passed", "tests": result.testsRun, "native_execution": "unverified"}
    require(all(getattr(args, x) for x in ("binary", "gemini_executable", "node_executable", "cli_install_root", "lab_root", "hook_shell",
                                          "ui_contract", "sdk_module_root", "installer_module_root")), "explicit_production_inputs_required")
    return run(args)


if __name__ == "__main__":
    try:
        print(json.dumps(main()))
    except Exception as exc:
        print(json.dumps({"red": str(exc) if isinstance(exc, Red) else "production_harness_error"}), file=sys.stderr)
        sys.exit(1)
