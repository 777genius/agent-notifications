#!/usr/bin/env python3
"""Explicitly prepare only the official disposable Mac runner's NC agent."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import stat
import subprocess
import tempfile
import time

REPO = Path(__file__).resolve().parents[1]
LABEL = "com.apple.notificationcenterui.agent"
BUNDLE_ID = "com.apple.notificationcenterui"
PLIST = Path("/System/Library/LaunchAgents/com.apple.notificationcenterui.plist")
APP = Path("/System/Library/CoreServices/NotificationCenter.app")
EXE = APP / "Contents/MacOS/NotificationCenter"


def require(ok, reason):
    if not ok:
        raise ValueError(reason)


def command(argv, timeout=3, observed=None):
    def retain(code, stdout, stderr, timed_out=False):
        if observed is not None:
            output = stdout[:65536]
            error = stderr[:max(0, 65536 - len(output))]
            observed.update(exit_code=code, timed_out=timed_out, stdout_bytes=len(stdout), stderr_bytes=len(stderr),
                            output_bounded=len(stdout) + len(stderr) <= 65536,
                            stdout=output.decode(errors="replace"), stderr=error.decode(errors="replace"))
    try:
        result = subprocess.run([str(x) for x in argv], stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired as error:
        retain(None, error.stdout or b"", error.stderr or b"", True)
        raise
    retain(result.returncode, result.stdout, result.stderr)
    require(len(result.stdout) + len(result.stderr) <= 65536, "host_command_output_limit")
    return result.returncode, result.stdout.decode(errors="replace"), result.stderr.decode(errors="replace")


def observe_host_state(domain, observer):
    """Finish all three fixed read-only observations before parsing any state."""
    snapshot = {}
    for name, argv in (("disabled", ["/bin/launchctl", "print-disabled", domain]),
                       ("service", ["/bin/launchctl", "print", domain + "/" + LABEL]),
                       ("native", [observer, "notification-center-state"])):
        row = snapshot[name] = {"argv": list(map(str, argv)), "timeout_seconds": 3}
        try:
            command(argv, observed=row)
        except Exception as error:
            row.update(error_class=type(error).__name__, error=str(error)[:4096])
    return snapshot


def snapshot_result(snapshot, name):
    row = snapshot[name]
    require(not row.get("error_class") and row.get("timed_out") is False
            and row.get("output_bounded") is True and type(row.get("exit_code")) is int,
            "notification_agent_state_observation_unverified:" + name)
    return row["exit_code"], row["stdout"], row["stderr"]


def admit(env, host, metadata, source, clean):
    require(env.get("GITHUB_REPOSITORY") == "777genius/agent-notifications"
            and env.get("GITHUB_ACTOR") == env.get("GITHUB_TRIGGERING_ACTOR") == "777genius"
            and env.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and env.get("GITHUB_RUN_ATTEMPT") == "1"
            and env.get("PI_MACOS_RUNNER_ENVIRONMENT") == "github-hosted", "explicit_owner_disposable_host_required")
    require(host["os"] == "Darwin" and host["arch"] == "arm64" and host["version"].startswith("15.")
            and env.get("ImageOS") == "macos15", "official_arm_macos15_required")
    version = env.get("ImageVersion", "")
    require(re.fullmatch(r"[0-9]{8}\.[0-9]+\.[0-9]+", version), "official_image_version_required")
    rows = {}
    for group in ("Operating System", "Runner Image"):
        values = [row.get("detail") for row in metadata if isinstance(row, dict) and row.get("group") == group]
        require(len(values) == 1 and isinstance(values[0], str), "official_image_metadata_ambiguous")
        rows[group] = values[0]
    operating = rows.get("Operating System", "").splitlines()
    require(operating == ["macOS", host["version"], host["build"]], "actual_os_image_metadata_mismatch")
    fields = dict(line.split(": ", 1) for line in rows.get("Runner Image", "").splitlines() if ": " in line)
    tag = version.rsplit(".", 1)[0]
    require(fields.get("Image") == "macos-15-arm64" and fields.get("Version") == version
            and fields.get("Included Software") == "https://github.com/actions/runner-images/blob/macos-15-arm64/" + tag + "/images/macos/macos-15-arm64-Readme.md"
            and fields.get("Image Release") == "https://github.com/actions/runner-images/releases/tag/macos-15-arm64%2F" + tag,
            "official_image_metadata_required")
    require(re.fullmatch(r"[a-f0-9]{40}", source) and env.get("SOURCE_SHA") == source and clean,
            "exact_clean_candidate_required")
    return {"source_sha": source, "image_os": env["ImageOS"], "image_version": version,
            "image_metadata": fields, "host": host}


def system_agent(observed=None, evidence_root=None):
    require(PLIST.resolve(strict=True) == PLIST and APP.resolve(strict=True) == APP
            and EXE.resolve(strict=True) == EXE, "immutable_system_agent_paths_required")
    facts = PLIST.stat(follow_symlinks=False)
    require(stat.S_ISREG(facts.st_mode) and facts.st_uid == 0 and not facts.st_mode & 0o022
            and 0 < facts.st_size <= 65536, "immutable_system_agent_plist_required")
    body = PLIST.read_bytes()
    value = plistlib.loads(body)
    program = value.get("Program") or (value.get("ProgramArguments") or [None])[0]
    identity = {"plist_sha256": hashlib.sha256(body).hexdigest(),
                **{key: value.get(key) for key in ("Label", "Program", "BundleProgram", "Disabled", "RunAtLoad", "KeepAlive")},
                "first_program_argument": (value.get("ProgramArguments") or [None])[0]}
    if observed is not None:
        observed.update(path=str(PLIST), device=facts.st_dev, inode=facts.st_ino,
                        owner_uid=facts.st_uid, mode=stat.S_IMODE(facts.st_mode), bytes=len(body), identity=identity)
    if evidence_root is not None:
        with (evidence_root / "notification-center-launchagent.plist").open("xb") as stream:
            stream.write(body)
    require(value.get("Label") == LABEL and program == str(EXE),
            "exact_system_notification_agent_required:actual=" + json.dumps(identity, sort_keys=True)[:4096])
    return {"path": str(PLIST), "sha256": hashlib.sha256(body).hexdigest(),
            "device": facts.st_dev, "inode": facts.st_ino, "label": LABEL, "program": program}


def inspect(domain, snapshot=None):
    code, text, error = snapshot_result(snapshot, "disabled") if snapshot is not None else command(["/bin/launchctl", "print-disabled", domain])
    require(code == 0, "disabled_state_query_failed")
    disabled = re.findall(r'^[ \t]*"' + re.escape(LABEL) + r'"[ \t]*=>[ \t]*([^\r\n]*)$', text, re.MULTILINE)
    require(len(disabled) == 1 and disabled[0].strip(" \t") in ("true", "false", "disabled", "enabled"),
            "notification_agent_disabled_state_ambiguous")
    is_disabled = disabled[0].strip(" \t") in ("true", "disabled")
    code, text, error = snapshot_result(snapshot, "service") if snapshot is not None else command(["/bin/launchctl", "print", domain + "/" + LABEL])
    if code != 0:
        require("Could not find service" in error and LABEL in error, "notification_agent_query_failed")
        return {"disabled": is_disabled, "loaded": False, "running": False}
    states = re.findall(r"^\s*state = ([^\n]+)$", text, re.MULTILINE)
    pids = re.findall(r"^\s*pid = ([0-9]+)\s*$", text, re.MULTILINE)
    require(len(states) == 1 and len(pids) <= 1, "notification_agent_service_state_ambiguous")
    return {"disabled": is_disabled, "loaded": True, "running": states[0] == "running",
            "pid": int(pids[0]) if pids else None}


def action_plan(before):
    if before.get("disabled") is False and before.get("loaded") is True and before.get("running") is True:
        return []
    require(before == {"disabled": True, "loaded": False, "running": False}, "notification_agent_state_not_admitted")
    return ["enable", "bootstrap"]


def validate_running(state, native):
    require(state.get("disabled") is False and state.get("loaded") is True and state.get("running") is True
            and type(state.get("pid")) is int and state["pid"] > 0, "notification_agent_not_running")
    apps = native.get("applications", [])
    require(len(apps) == 1 and apps[0].get("pid") == state["pid"]
            and apps[0].get("bundle_id") == BUNDLE_ID and apps[0].get("bundle_path") == str(APP)
            and apps[0].get("executable_path") == str(EXE) and apps[0].get("finished_launching") is True
            and apps[0].get("process_start_identity"), "system_notification_application_identity_unverified")


def prepare(args):
    report = {"scope": "explicit_disposable_official_macos_host_preparation", "desktop_qualified": False,
              "permission_requests_executed": 0, "actions": [], "status": "unqualified"}
    require(args.report.is_absolute() and not args.report.exists() and not args.report.is_symlink(),
            "fresh_host_preparation_report_required")
    try:
        require(args.prepare_notification_center, "explicit_notification_center_opt_in_required")
        source = command(["git", "-C", REPO, "rev-parse", "HEAD"])[1].strip()
        require(source == args.source_sha, "host_preparation_source_mismatch")
        metadata_path = Path(os.environ["HOME"]) / "imagedata.json"
        require(metadata_path.is_file() and not metadata_path.is_symlink() and metadata_path.stat().st_size <= 65536,
                "actual_official_image_metadata_required")
        host = {"os": platform.system(), "arch": platform.machine(), "version": platform.mac_ver()[0],
                "build": command(["/usr/bin/sw_vers", "-buildVersion"])[1].strip()}
        report["admission"] = admit(os.environ, host, json.loads(metadata_path.read_text()), source,
                                     command(["git", "-C", REPO, "status", "--porcelain", "--untracked-files=all"])[1] == "")
        root = Path(tempfile.mkdtemp(prefix="TEST-pi-macos-host-", dir=os.environ["RUNNER_TEMP"])).resolve()
        root.chmod(0o700)
        report["test_root"] = str(root)
        # Inspect only the fixed system app, independently of any plist Program.
        # Retain this read-only proof even if the plist schema refuses admission.
        code, output, error = command(["/usr/bin/codesign", "--verify", "--strict", "-R",
            '=anchor apple and identifier "com.apple.notificationcenterui"', APP])
        report["system_application_signature"] = {"exit_code": code, "stdout": output, "stderr": error}
        args.report.parent.mkdir(parents=True, exist_ok=True)
        report["system_agent_observed"] = {}
        expected = system_agent(report["system_agent_observed"], args.report.parent)
        report["system_agent"] = expected
        require(code == 0, "apple_system_notification_application_signature_unverified")
        domain = "gui/" + str(os.getuid())
        report["state_before_observed"] = observe_host_state(domain, args.observer)
        code, output, error = snapshot_result(report["state_before_observed"], "native")
        require(code == 0, "notification_agent_native_observer_failed")
        report["native_before"] = json.loads(output)
        before = inspect(domain, report["state_before_observed"])
        report["before"] = before
        actions = action_plan(before)
        if actions:
            require(report["native_before"].get("applications") == [], "unloaded_notification_agent_has_ambiguous_native_process")
        else:
            validate_running(before, report["native_before"])
        # Persist the admitted before-state before either targeted mutation.
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        for operation in actions:
            require(system_agent() == expected, "system_agent_changed_before_preparation")
            argv = ["/bin/launchctl", operation, domain + "/" + LABEL] if operation == "enable" else ["/bin/launchctl", "bootstrap", domain, PLIST]
            code, output, error = command(argv)
            report["actions"].append({"operation": operation, "exit_code": code})
            args.report.write_text(json.dumps(report, indent=2) + "\n")
            require(code == 0, "notification_agent_preparation_failed:" + operation)
        deadline = time.monotonic() + 10
        while True:
            report["state_after_observed"] = observe_host_state(domain, args.observer)
            code, output, error = snapshot_result(report["state_after_observed"], "native")
            require(code == 0, "notification_agent_native_observer_failed")
            report["native_identity"] = json.loads(output)
            report["after"] = inspect(domain, report["state_after_observed"])
            try:
                validate_running(report["after"], report["native_identity"])
                break
            except ValueError:
                require(time.monotonic() < deadline, "notification_agent_readiness_unqualified")
                time.sleep(0.1)
        report.update(status="notification_center_ready", already_ready=not report["actions"])
    except Exception as error:
        report["failure"] = str(error)
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))
    return 0 if report["status"] == "notification_center_ready" else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prepare-notification-center", action="store_true")
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--observer", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    raise SystemExit(prepare(parser.parse_args()))
