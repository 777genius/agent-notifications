#!/usr/bin/env python3
"""Credential-free actual Pi HTTP/events. This does not qualify Notifications."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import platform
import subprocess
import tempfile

FIXTURE = Path(__file__).resolve().parents[1] / "tests/integration/pi_native_fixture.py"
SPEC = importlib.util.spec_from_file_location("pi_fixture", FIXTURE)
F = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(F)


def run(args):
    report = {"schema_version": 1, "status": "failed", "scope": "actual_pi_local_provider_contract",
              "source_sha": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=FIXTURE.parents[2], text=True).strip(),
              "fixture_sha256": hashlib.sha256(FIXTURE.read_bytes()).hexdigest(),
              "script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "host_os": platform.system(), "host_architecture": platform.machine(),
              "product_notifications_qualified": False,
              "pi_binary_sha256": hashlib.sha256(args.pi_executable.read_bytes()).hexdigest(), "cases": []}
    try:
        for case in ("success", "retry", "followup", "error", "abort", "ui", "print-success"):
            root = Path(tempfile.mkdtemp(prefix="TEST-pi-contract-" + case + "-")).resolve()
            root.chmod(0o700)
            scenario = "success" if case == "print-success" else case
            provider = F.Provider(scenario)
            process = None
            try:
                env = F.prepare(root, provider, scenario)
                if "pi_version" not in report:
                    version = subprocess.run([args.pi_executable, "--offline", "--version"], cwd=root / "project", env=env,
                                             capture_output=True, timeout=20, check=True)
                    report["pi_version"] = version.stdout.decode().strip()
                    F.require(report["pi_version"] == "1.4.0" and not version.stderr, "wrong_native_pi_version")
                mode = "rpc" if case in ("ui", "abort") else "print" if case == "print-success" else "json"
                process = F.PiProcess(args.pi_executable, root, env, mode)
                if mode == "rpc":
                    process.send({"id": "TEST-prompt", "type": "prompt", "message": "/TEST-dialog" if case == "ui" else "TEST say done"})
                    aborted = False
                    while True:
                        row = process.next()
                        if row.get("type") == "extension_ui_request" and row.get("method") == "input":
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
                F.require(code == 0 and process.stderr_bytes == 0, "unexpected_pi_exit_or_diagnostic")
                counts = F.verify_case(scenario, F.witness(root), provider.requests)
                report["cases"].append({"case": case, "mode": mode, "exit_code": code,
                    "provider_requests": len(provider.requests), "native_counts": counts, "child_collected": True})
            finally:
                if process:
                    process.close()
                provider.close()
        report["status"] = "passed_actual_pi_contracts"
    except Exception as error:
        report["failure"] = str(error)
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "failure": report.get("failure"), "product_notifications_qualified": False}))
    return 0 if report["status"] == "passed_actual_pi_contracts" else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pi-executable", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    args.pi_executable = args.pi_executable.resolve(strict=True)
    raise SystemExit(run(args))
