#!/usr/bin/env python3
"""Actual Pi loader against a package projected by the public UAP Engine.

The driver is the UAP repository's testdata/driver host, which calls the public
Prepare/Apply API. This fixture never copies an extension into the active path.
Retained copies are negative loader challenges made from that actual projection.
No Notifications runtime, provider or desktop qualification is granted here.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import uuid

NAMESPACE = "com.universal-agent-plugins.pi"


def require(value, code):
    if not value:
        raise RuntimeError(code)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def write_package(source, revision):
    (source / NAMESPACE).mkdir(parents=True, exist_ok=True)
    (source / "plugin.json").write_text(json.dumps({"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
        "name": "test-pi-native-loader", "version": "1.0." + str(revision),
        "extensions": {NAMESPACE: {"version": 1, "source": NAMESPACE}}}))
    (source / NAMESPACE / "index.ts").write_text('''
import { appendFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
const revision = REVISION;
const record = value => appendFileSync(process.env.TEST_PI_LOADER_RECORDS, JSON.stringify({revision, module: fileURLToPath(import.meta.url), ...value}) + "\\n");
record({type: "top-level"});
export default async function (pi, descriptor) {
  record({type: "factory", descriptor});
  pi.registerCommand("TEST-loader", {description: "TEST common-engine projection", handler: async () => record({type: "command"})});
}
'''.replace("REVISION", str(revision)))


def exercise(args):
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-uap-loader-")).resolve()
    root.chmod(0o700)
    for part in ("home", "profile/extensions", "source", "project", "tmp"):
        (root / part).mkdir(parents=True, exist_ok=True, mode=0o700)
    env = {key: os.environ[key] for key in ("PATH", "SystemRoot", "SYSTEMROOT", "WINDIR", "ComSpec", "PATHEXT") if key in os.environ}
    env.update(HOME=str(root / "home"), USERPROFILE=str(root / "home"), PI_CODING_AGENT_DIR=str(root / "profile"),
               TMPDIR=str(root / "tmp"), TMP=str(root / "tmp"), TEMP=str(root / "tmp"), PI_OFFLINE="1",
               XDG_CONFIG_HOME=str(root / "home/.config"), TEST_PI_LOADER_RECORDS=str(root / "records.jsonl"))
    report = {"schema_version": 1, "scope": "actual_uap_engine_pi_loader", "status": "failed",
              "pi_sha256": sha(args.pi_executable), "driver_sha256": sha(args.driver),
              "fixture_sha256": sha(__file__), "product_notifications_qualified": False, "phases": []}

    def run(argv, request=None):
        result = subprocess.run([str(value) for value in argv], input=request, cwd=root / "project", env=env,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        require(result.returncode == 0, "native_process_failed:" + result.stderr.decode(errors="replace")[-2000:])
        require(len(result.stdout) + len(result.stderr) <= 1024 * 1024, "native_output_limit")
        return result.stdout

    installation = "TEST-pi-loader-" + uuid.uuid4().hex
    source = root / "source"

    def install(operation):
        result_path = root / (operation + "-" + uuid.uuid4().hex + ".json")
        run([args.driver, root / "state", root / "profile", source, operation, installation, result_path])
        value = json.loads(result_path.read_text())
        require(value["InstallationID"] == installation, "installation_identity_changed")
        report["phases"].append({"operation": operation, "outcome": value["Outcome"], "mutated": value["Mutated"], "no_change": value["NoChange"]})
        return value

    def pi(label, active, revision, extra=(), expected=True):
        records = root / "records.jsonl"
        records.unlink(missing_ok=True)
        request = {"type": "prompt", "id": "TEST-loader", "message": "/TEST-loader"} if expected else {"type": "get_commands", "id": "TEST-loader"}
        output = run([args.pi_executable, "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools", "--mode", "rpc", *extra],
                     (json.dumps(request) + "\n").encode())
        rows = [json.loads(line) for line in records.read_text().splitlines()] if records.exists() else []
        own = [row for row in rows if row.get("type") != "foreign"]
        require(sum(row.get("type") == "foreign" for row in rows) == 1, "foreign_extension_not_loaded")
        if expected:
            require([row["type"] for row in own] == ["top-level", "factory", "command"], "installed_factory_not_exactly_once")
            require(all(row["revision"] == revision and Path(row["module"]).parent.parent == active for row in own), "wrong_projection_source_loaded")
            descriptor = own[1]["descriptor"]
            require(descriptor["active_path"] == str(active) and descriptor["package_root"] == str(active), "wrong_projection_descriptor")
            responses = [json.loads(line) for line in output.splitlines()]
            require(any(row.get("id") == "TEST-loader" and row.get("success") is True for row in responses), "installed_command_not_handled")
        else:
            require(not own and b'"name":"TEST-loader"' not in output, "inactive_source_executed")
        report["phases"].append({"operation": label, "source_executions": len(own), "foreign_loaded": True})

    try:
        report["pi_version"] = run([args.pi_executable, "--offline", "--version"]).decode().strip()
        require(report["pi_version"] == "1.4.0", "wrong_pi_version")
        foreign = root / "profile/extensions/TEST-foreign.ts"
        foreign.write_text('import { appendFileSync } from "node:fs"; export default function() { appendFileSync(process.env.TEST_PI_LOADER_RECORDS, JSON.stringify({type:"foreign"})+"\\n"); }\n')
        foreign_hash = sha(foreign)
        write_package(source, 0)
        result = install("install")
        active = Path(result["Binding"]["TargetPath"])
        artifact = result["Delivery"]["PhysicalArtifactID"]
        require(active == root / "profile/extensions" / artifact and not active.name.startswith("."), "active_projection_not_visible_native_child")
        require(re.fullmatch(r"test-pi-native-loader-[0-9a-f]{12}", artifact), "physical_artifact_identity_not_standard")
        report["active_leaf"] = artifact
        pi("automatic-installed", active, 0)
        install("install")
        for name in ("retained-TEST-copy", ".agentplugins-backup-TEST"):
            shutil.copytree(active, active.parent / name)
        pi("retained-copy-no-source-effects", active, 0)
        pi("explicit-hidden-no-source-effects", active, 0, ("-e", str(active.parent / ".agentplugins-backup-TEST/index.ts")))
        displaced = root / "wrong-physical-root"
        active.rename(displaced)
        try:
            active.symlink_to(displaced, target_is_directory=True)
            pi("misbound-no-source-effects", active, 0, expected=False)
        finally:
            if active.is_symlink():
                active.unlink()
            displaced.rename(active)
        write_package(source, 1)
        install("update")
        pi("updated-native-projection", active, 1)
        active.rename(root / "missing-owned-projection")
        install("repair")
        pi("repaired-native-projection", active, 1)
        install("remove")
        pi("removed-native-projection", active, 1, expected=False)
        require(sha(foreign) == foreign_hash, "foreign_extension_changed")
        report["status"] = "passed_uap_native_loader"
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as error:
        report["failure"] = str(error)
    finally:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "product_notifications_qualified": False}))
    return 0 if report["status"] == "passed_uap_native_loader" else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--driver", type=Path, required=True)
    parser.add_argument("--pi-executable", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    args.driver = args.driver.resolve(strict=True)
    args.pi_executable = args.pi_executable.resolve(strict=True)
    raise SystemExit(exercise(args))


if __name__ == "__main__":
    main()
