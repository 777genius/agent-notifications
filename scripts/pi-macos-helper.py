#!/usr/bin/env python3
"""Acquire only Pi's pinned, owner-signed and notarized macOS helper input."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import time
import zipfile

REPO = Path(__file__).resolve().parents[1]
PIN = REPO / "scripts/testdata/pi-native/macos-helper.json"
INPUTS = PIN.parent / "macos-helper"
BUNDLE = "com.777genius.agent-notifications"
TEAM = "86399583GS"
WORKFLOW = ".github/workflows/notifier-signing-smoke.yml"
REPOSITORY = "777genius/agent-notifications"


def require(ok, reason):
    if not ok:
        raise ValueError(reason)


def sha(path):
    require(path.is_file() and not path.is_symlink() and 0 < path.stat().st_size <= 64 * 1024 * 1024,
            "macos_helper_regular_bounded_input_required")
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_pin():
    pin = json.loads(PIN.read_text())
    validate_pin(pin)
    return pin


def validate_pin(pin):
    require(isinstance(pin, dict) and set(pin) == {
        "schema_version", "source_sha", "source_tree", "run_id", "run_attempt", "artifact_id",
        "archive_sha256", "executable_sha256", "attestation_sha256", "custody_sha256",
        "signing_run_sha256", "signing_artifacts_sha256"}
        and type(pin["schema_version"]) is int and pin["schema_version"] == 1,
        "macos_helper_pin_schema_invalid")
    for field in ("source_sha", "source_tree", "archive_sha256", "executable_sha256", "attestation_sha256",
                  "custody_sha256", "signing_run_sha256", "signing_artifacts_sha256"):
        require(isinstance(pin[field], str) and re.fullmatch(r"[a-f0-9]{" + ("40" if field in ("source_sha", "source_tree") else "64") + r"}", pin[field]),
                "macos_helper_pin_identity_invalid")
    require(all(type(pin[field]) is int and pin[field] > 0 for field in ("run_id", "run_attempt", "artifact_id")),
            "macos_helper_pin_run_invalid")


def validate_custody(pin, run, artifacts, custody, archive_hash):
    validate_pin(pin)
    require(run.get("id") == pin["run_id"] and run.get("run_attempt") == pin["run_attempt"]
            and run.get("head_sha") == pin["source_sha"] and run.get("status") == "completed"
            and run.get("conclusion") == "success" and run.get("event") == "workflow_dispatch"
            and run.get("head_branch") == "release/macos-signing" and run.get("path") == WORKFLOW
            and run.get("actor", {}).get("login") == "777genius"
            and run.get("triggering_actor", {}).get("login") == "777genius",
            "macos_helper_owner_signing_run_mismatch")
    rows = artifacts.get("artifacts", [])
    require(artifacts.get("total_count") == len(rows) == 1 and rows[0].get("id") == pin["artifact_id"]
            and rows[0].get("name") == "ClaudeNotifier-smoke" and rows[0].get("expired") is False
            and rows[0].get("workflow_run", {}).get("id") == pin["run_id"]
            and rows[0].get("workflow_run", {}).get("head_sha") == pin["source_sha"],
            "macos_helper_exact_signing_artifact_mismatch")
    require(custody.get("schemaVersion") == 1 and custody.get("sourceSHA") == pin["source_sha"]
            and custody.get("workflowSHA") == pin["source_sha"]
            and custody.get("workflowRef") == REPOSITORY + "/" + WORKFLOW + "@refs/heads/release/macos-signing"
            and custody.get("runID") == str(pin["run_id"]) and custody.get("runAttempt") == str(pin["run_attempt"])
            and custody.get("archiveSHA256") == archive_hash == pin["archive_sha256"]
            and custody.get("executableSHA256") == pin["executable_sha256"]
            and custody.get("attestationSHA256") == pin["attestation_sha256"]
            and custody.get("bundleID") == BUNDLE and custody.get("signingTeam") == TEAM
            and custody.get("signatureVerified") is True,
            "macos_helper_signed_custody_mismatch")


def validate_receipt(receipt, pin):
    validate_pin(pin)
    require(isinstance(receipt, dict) and type(receipt.get("schema_version")) is int
            and receipt.get("signed_source_tree_verified") is True
            and receipt.get("signature_and_notarization_verified") is True
            and receipt == {"schema_version": 1, "pin": pin, "signed_source_tree_verified": True,
                       "signature_and_notarization_verified": True}, "macos_helper_acquisition_receipt_mismatch")


def source_tree(ref):
    return subprocess.check_output(["git", "rev-parse", ref + ":swift-notifier"], cwd=REPO, text=True).strip()


def candidate_tree(pin):
    require(source_tree("HEAD") == pin["source_tree"], "macos_helper_candidate_swift_source_mismatch")


def stapler_transport_failure(code, output):
    """Only the measured Apple ticket-lookup connection loss is retryable."""
    return (code == 68
            and re.search(r"Error Domain=NSURLErrorDomain Code=-1005\b", output) is not None
            and "The network connection was lost." in output
            and "https://api.apple-cloudkit.com/database/1/com.apple.gk.ticket-delivery/production/public/records/lookup" in output
            and re.search(r"^The validate action failed! Error 68\.$", output, re.MULTILINE) is not None
            and re.search(r"\b(?:invalid|rejected|revoked|untrusted)\b|\b(?:not trusted|not notarized|no ticket|not found)\b",
                          output, re.IGNORECASE) is None)


def verify_native_helper(app, evidence):
    """Keep the original trust checks and one shared 180-second total budget."""
    deadline = time.monotonic() + 180
    evidence.parent.mkdir(parents=True, exist_ok=True)
    # Retain each result before another command, even when verification fails.
    # An existing file or symlink cannot replace this fresh evidence stream.
    with evidence.open("x", encoding="utf-8") as stream:
        for stage, argv in (
                ("signature", ["bash", REPO / "swift-notifier/scripts/verify-signing.sh", app]),
                ("stapler", ["/usr/bin/xcrun", "stapler", "validate", app]),
                ("assessment", ["/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=2", app])):
            for attempt in range(1, (2 if stage == "stapler" else 1) + 1):
                remaining = deadline - time.monotonic()
                require(remaining > 0, "macos_helper_native_verification_budget_expired")
                timed_out = False
                try:
                    value = subprocess.run([str(arg) for arg in argv], cwd=REPO, stdin=subprocess.DEVNULL,
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=remaining)
                    code, stdout, stderr = value.returncode, value.stdout, value.stderr
                except subprocess.TimeoutExpired as error:
                    code, stdout, stderr, timed_out = None, error.stdout or b"", error.stderr or b"", True
                within_budget = time.monotonic() < deadline
                limit = 2 * 1024 * 1024
                oversized = len(stdout) + len(stderr) > limit
                stdout, stderr = stdout[:limit], stderr[:max(0, limit - len(stdout))]
                output = (stdout + stderr).decode(errors="replace")
                retry = (stage == "stapler" and attempt == 1 and not timed_out and not oversized and within_budget
                         and stapler_transport_failure(code, output))
                stream.write(json.dumps({"stage": stage, "attempt": attempt, "argv": list(map(str, argv)),
                    "timeout_seconds": remaining, "returncode": code, "timed_out": timed_out,
                    "within_budget": within_budget,
                    "output_truncated": oversized, "stdout": stdout.decode(errors="replace"),
                    "stderr": stderr.decode(errors="replace"), "retry": retry}) + "\n")
                stream.flush()
                require(not oversized, "macos_helper_acquisition_output_limit")
                require(not timed_out, "macos_helper_native_verification_timeout:" + stage)
                require(within_budget, "macos_helper_native_verification_budget_expired")
                if code == 0:
                    break
                require(retry, "macos_helper_native_verification_failed:" + stage + ":" + output[-8192:])


def acquire(output, report):
    pin = load_pin()
    candidate_tree(pin)
    require(not output.exists(), "macos_helper_output_must_be_fresh")
    output.mkdir(parents=True, mode=0o700)

    def command(argv):
        value = subprocess.run([str(arg) for arg in argv], cwd=REPO, stdin=subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180)
        require(len(value.stdout) + len(value.stderr) <= 2 * 1024 * 1024, "macos_helper_acquisition_output_limit")
        if value.returncode != 0:
            diagnostics = (value.stdout + value.stderr).decode(errors="replace")[-8192:]
            raise ValueError("macos_helper_acquisition_command_failed:" + str(argv[0]) + ":" + diagnostics)
        return value.stdout

    # These exact producer records and bytes were retained while the successful
    # signing run was live. CI does not depend on expiring Actions artifacts.
    for filename, field in (("signing-run.json", "signing_run_sha256"),
                            ("signing-artifacts.json", "signing_artifacts_sha256"),
                            ("ClaudeNotifier-smoke.custody.json", "custody_sha256"),
                            ("ClaudeNotifier-smoke.app.zip", "archive_sha256")):
        require(sha(INPUTS / filename) == pin[field], "macos_helper_retained_input_mismatch:" + filename)
        shutil.copyfile(INPUTS / filename, output / filename)
    run = json.loads((output / "signing-run.json").read_text())
    artifacts = json.loads((output / "signing-artifacts.json").read_text())
    archive = output / "ClaudeNotifier-smoke.app.zip"
    custody_path = output / "ClaudeNotifier-smoke.custody.json"
    require(custody_path.stat().st_size <= 65536, "macos_helper_custody_limit")
    custody = json.loads(custody_path.read_text())
    validate_custody(pin, run, artifacts, custody, sha(archive))
    command(["git", "fetch", "--no-tags", "--depth=1", "origin", pin["source_sha"]])
    require(source_tree(pin["source_sha"]) == pin["source_tree"], "macos_helper_signed_swift_source_mismatch")
    with zipfile.ZipFile(archive) as packaged:
        names, total = set(), 0
        for member in packaged.infolist():
            name = PurePosixPath(member.filename)
            total += member.file_size
            require(member.filename and not name.is_absolute() and ".." not in name.parts
                    and member.filename == name.as_posix() + ("/" if member.is_dir() else "")
                    and member.orig_filename == member.filename
                    and "\\" not in member.filename and ":" not in member.filename
                    and member.filename.casefold() not in names and not member.flag_bits & 1
                    and (member.external_attr >> 16) & 0o170000 in (0, 0o100000, 0o040000)
                    and member.file_size <= 64 * 1024 * 1024 and total <= 64 * 1024 * 1024,
                    "macos_helper_archive_unsafe")
            names.add(member.filename.casefold())
        for name, expected in (("ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern", pin["executable_sha256"]),
                               ("ClaudeNotifier.app.managed-runtime.json", pin["attestation_sha256"])):
            require(hashlib.sha256(packaged.read(name)).hexdigest() == expected, "macos_helper_signed_leaf_mismatch")
    native = output / "native"
    native.mkdir(mode=0o700)
    command(["/usr/bin/ditto", "-x", "-k", archive, native])
    verify_native_helper(native / "ClaudeNotifier.app", report.parent / "helper-native-verification.jsonl")
    require(sha(native / "ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern") == pin["executable_sha256"]
            and sha(native / "ClaudeNotifier.app.managed-runtime.json") == pin["attestation_sha256"],
            "macos_helper_extracted_leaf_mismatch")
    receipt = {"schema_version": 1, "pin": pin, "signed_source_tree_verified": True,
               "signature_and_notarization_verified": True}
    (native / "helper-input.json").write_text(json.dumps(receipt, indent=2) + "\n")
    report.parent.mkdir(parents=True, exist_ok=True)
    report.write_text(json.dumps(receipt, indent=2) + "\n")
    (report.parent / "helper-signing-run.json").write_text(json.dumps(run, indent=2) + "\n")
    (report.parent / "helper-signing-custody.json").write_text(json.dumps(custody, indent=2) + "\n")
    print(json.dumps({"source_sha": pin["source_sha"], "source_tree": pin["source_tree"],
                      "run_id": pin["run_id"], "signature_and_notarization_verified": True}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    acquire(args.output.resolve(), args.report.resolve())
