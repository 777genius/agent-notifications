#!/usr/bin/env python3
"""Thin trusted GitHub runner wrapper; --self-test is pure and provider-safe.

No native execution in a provider sandbox. G0 owns profile/environment/PTY/HTTP
and child cleanup; G5 owns production lifecycle/delivery assertions. Only their
bounded evidence manifests, public pin metadata and build identities are saved.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
MARKER = ".an-gemini-TEST"
SDK = "github.com/777genius/plugin-kit-ai/sdk"
INSTALLER = "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins"


class Red(Exception):
    pass


def require(ok, code):
    if not ok:
        raise Red(code)


def sha(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        while block := stream.read(65536):
            value.update(block)
    return value.hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def inside(path, root):
    return path == root or root in path.parents


def outside(path, roots):
    require(not any(inside(path, root) for root in roots), "TEST_path_inside_repository_cwd_or_home")
    require(not any((ancestor / ".git").exists() for ancestor in path.parents), "TEST_repository_ancestor")


def ui_contract(raw):
    ui = json.loads(raw)
    require(set(ui) == {"permission_pattern", "approve", "deny", "cancel", "source_sha256"}, "UI_fields")
    require(isinstance(ui["source_sha256"], str) and re.fullmatch(r"[0-9a-f]{64}", ui["source_sha256"]), "UI_source_sha256")
    for key in ("approve", "deny"):
        require(isinstance(ui[key], str) and 0 < len(ui[key]) <= 32 and
                re.fullmatch(r"(?:[1-9]|\x1b\[[AB])*\r", ui[key]), "UI_navigation_keys")
    require(ui["approve"] != ui["deny"] and ui["cancel"] in ("\x1b", "\x03"), "UI_distinct_choices")
    require(isinstance(ui["permission_pattern"], str) and 0 < len(ui["permission_pattern"]) <= 512, "UI_pattern")
    re.compile(ui["permission_pattern"])
    return ui


def json_stream(raw):
    decoder, values, text = json.JSONDecoder(), [], raw.decode("utf-8")
    while text.strip():
        item, end = decoder.raw_decode(text.lstrip())
        values.append(item)
        text = text.lstrip()[end:]
    return values


def module_graph(values):
    modules = {}
    for item in values:
        require(not item.get("Replace"), "Go_module_replacement_forbidden")
        if item.get("Main"):
            continue
        require(item.get("Version", "").startswith("v") and item.get("Sum", "").startswith("h1:") and
                item.get("GoModSum", "").startswith("h1:"), "Go_module_public_pin_missing")
        require(item["Path"] not in modules, "Go_duplicate_module")
        modules[item["Path"]] = item
    require(all(name in modules and modules[name].get("Dir") for name in (SDK, INSTALLER)), "Go_required_module_directory_missing")
    return modules


def graph_pins(raw):
    values = []
    for line in raw.decode("utf-8").splitlines():
        if not line.strip():
            continue
        fields = line.split()
        require(len(fields) == 4, "Go_graph_pin_fields")
        values.append(dict(zip(("Path", "Version", "Sum", "GoModSum"), fields)))
    return values


def build_identity(raw, modules, commit, osname, arch):
    # Actual `go version -m`, not go.mod intent, binds linked modules and VCS.
    dependencies, settings = {}, {}
    for line in raw.decode("utf-8").splitlines()[1:]:
        fields = line.strip().split()
        require(not fields or fields[0] != "=>", "binary_module_replacement_forbidden")
        if fields and fields[0] == "dep":
            require(len(fields) == 4, "binary_dependency_identity_missing")
            dependencies[fields[1]] = (fields[2], fields[3])
        elif fields and fields[0] == "build":
            key, value = fields[1].split("=", 1)
            settings[key] = value
    for name, (version, checksum) in dependencies.items():
        require(name in modules and (modules[name]["Version"], modules[name]["Sum"]) == (version, checksum), "binary_graph_pin_mismatch")
    require(all(name in dependencies for name in (SDK, INSTALLER)), "binary_required_modules_not_linked")
    require(all(settings.get(key) == value for key, value in
                {"CGO_ENABLED": "1", "GOOS": osname, "GOARCH": arch, "vcs.revision": commit, "vcs.modified": "false"}.items()), "binary_build_or_commit_mismatch")
    return {"dependencies": dependencies, "settings": settings}


def tool(name):
    value = shutil.which(name)
    require(value is not None, "required_tool_missing")
    return Path(value).resolve(strict=True)


def npm_cli(node):
    npm = tool("npm")
    # Execute npm's physical JS entry through the physical Node, including on
    # Windows; never ask cmd.exe to interpret an npm.cmd command string.
    candidates = (npm, node.parent / "node_modules/npm/bin/npm-cli.js",
                  node.parent.parent / "lib/node_modules/npm/bin/npm-cli.js")
    for path in candidates:
        if path.name == "npm-cli.js" and path.is_file():
            return path.resolve(strict=True)
    raise Red("physical_npm_JS_entry_missing")


def source(value, default):
    path = Path(value) if value else REPO / "tests/integration" / default
    require(path.is_file(), "integration_input_missing")
    return path.resolve(strict=True)


def run(args):
    require(args.trusted_github_runner and os.environ.get("GITHUB_ACTIONS") == "true" and
            os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted", "trusted_GitHub_runner_required")
    temp = Path(os.environ["RUNNER_TEMP"]).resolve(strict=True)
    forbidden = (REPO, Path.cwd().resolve(), Path.home().resolve())
    # Hosted Linux/Mac RUNNER_TEMP can be underneath the runner's HOME. It is
    # suitable for upload manifests, but never for a native lab or installation.
    outside(temp, forbidden[:2])
    evidence_dir = temp / "gemini-native-ci-evidence"
    require(not evidence_dir.exists(), "evidence_directory_must_be_fresh")
    evidence_dir.mkdir(mode=0o700)
    evidence = {"status": "failed", "platform": args.platform, "arch": args.arch, "commit": args.commit,
                "native_provider": "deterministic_real_loopback_HTTP_substitute", "Google_model_service": "unqualified",
                "desktop": "unverified_headless_webhook_only", "visual": "unverified", "steps": {}}
    root = None
    try:
        require(re.fullmatch(r"[0-9a-f]{40}", args.commit or ""), "exact_commit_required")
        host = {"Linux": "linux", "Windows": "windows", "Darwin": "darwin"}.get(platform.system())
        machine = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
        require((host, machine) == (args.platform, args.arch), "physical_runner_platform_mismatch")
        g0path = source(args.g0_driver, "gemini_native_e2e.py")
        bridge = g0path.with_name("gemini_native_pty.cjs")
        require(bridge.is_file(), "G0_bridge_missing")
        g5path = source(args.g5_driver, "gemini_notifications_e2e.py")
        uipath = source(args.ui_contract, "gemini-ui-contract.json")
        require(uipath.stat().st_size <= 16384, "UI_size")
        raw_ui = uipath.read_bytes()
        ui = ui_contract(raw_ui)
        evidence["inputs"] = {"G0_sha256": sha(g0path), "PTY_bridge_sha256": sha(bridge), "G5_sha256": sha(g5path),
                              "CI_sha256": sha(Path(__file__)), "UI_contract_sha256": sha(uipath), "UI_source_sha256": ui["source_sha256"]}
        spec = importlib.util.spec_from_file_location("gemini_ci_g0", g0path)
        g0 = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(g0)
        node, go, git = tool("node"), tool("go"), tool("git")
        shell = Path(args.hook_shell).resolve(strict=True)
        system_root = str(Path(args.system_root).resolve(strict=True)) if args.system_root else None
        npm = npm_cli(node)
        python = Path(sys.executable).resolve(strict=True)
        lab_parent = Path("/tmp").resolve(strict=True) if os.name != "nt" else temp
        outside(lab_parent, forbidden)
        root = Path(tempfile.mkdtemp(prefix="TEST-gemini-ci-", dir=lab_parent)).resolve(strict=True)
        outside(root, forbidden)
        (root / MARKER).write_text("owned Gemini CI TEST artifacts\n", encoding="utf-8")
        bootstrap = g0.new_lab(str(root / "TEST-bootstrap"))
        env = g0.minimal_env(bootstrap, node, shell, system_root)
        require(python == g0.physical(str(python)), "physical_python_required")
        build_env = dict(env)
        build_env.update(CGO_ENABLED="1", GOWORK="off", GOFLAGS="-mod=readonly", GOENV="off", GOTOOLCHAIN="local",
                         GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org", GOPRIVATE="", GONOSUMDB="",
                         GOMODCACHE=str(root / "gomodcache"), GOPATH=str(root / "gopath"), GOCACHE=str(root / "gocache"),
                         npm_config_cache=str(root / "npm-cache"), npm_config_update_notifier="false",
                         npm_config_registry="https://registry.npmjs.org")
        for name in ("userconfig", "globalconfig"):
            path = root / ("npm-" + name)
            path.write_text("", encoding="utf-8")
            build_env["npm_config_" + name] = str(path)
        bins = [str(go.parent), str(git.parent), str(node.parent)]
        if os.name == "nt":
            cc = tool("gcc")
            build_env["CC"] = str(cc)
            # npm's Windows lifecycle scripts use cmd's /d /s /c protocol.
            # Native hooks still use the separate unmodified G0 pwsh environment.
            build_env["ComSpec"] = str(g0.physical(str(Path(system_root) / "System32/cmd.exe")))
            bins.append(str(cc.parent))
        build_env["PATH"] = os.pathsep.join(dict.fromkeys(bins + env["PATH"].split(os.pathsep)))
        if args.platform == "darwin":
            build_env["MACOSX_DEPLOYMENT_TARGET"] = "12.0"
        evidence["tools"] = {name: {"physical_path": str(path), "sha256": sha(path)} for name, path in
                             (("python", python), ("node", node), ("hook_shell", shell), ("go", go), ("npm_cli", npm))}

        def command(label, argv, cwd=REPO, child_env=build_env, timeout=180):
            evidence["steps"][label] = "running"
            write_json(evidence_dir / "ci-evidence.json", evidence)
            code, out, _ = g0.bounded_process([str(x) for x in argv], b"", cwd, child_env, timeout)
            evidence["steps"][label] = {"exit_code": code}
            require(code == 0, "command_failed_" + label)
            return out  # Never persist stdout/stderr, except explicit public Go metadata.

        require(command("checkout_identity", [git, "rev-parse", "HEAD"]).decode().strip() == args.commit, "checkout_commit_mismatch")
        require(not (REPO / "go.work").exists(), "go_work_forbidden")
        mod = json.loads(command("go_mod_metadata", [go, "mod", "edit", "-json"]))
        require(not mod.get("Replace"), "Go_mod_replace_forbidden")
        command("download_public_pins", [go, "mod", "download", "all"])
        # Keep the full graph inside G0's bounded stream cap. Obtain directory
        # records with the actual -json interface only for the two staged trees.
        pins = graph_pins(command("module_graph", [go, "list", "-m", "-f",
                          "{{if not .Main}}{{.Path}} {{.Version}} {{.Sum}} {{.GoModSum}}{{end}}", "all"]))
        selected = json_stream(command("module_directories", [go, "list", "-m", "-json", SDK, INSTALLER]))
        records = {item["Path"]: item for item in pins}
        for item in selected:
            require(item["Path"] in records and all(records[item["Path"]][key] == item[key]
                    for key in ("Version", "Sum", "GoModSum")), "Go_directory_pin_mismatch")
            records[item["Path"]] = item
        modules = module_graph(list(records.values()))
        write_json(evidence_dir / "go-modules.json", list(modules.values()))
        command("verify_public_pins", [go, "mod", "verify"])
        staged = {}
        for label, name in (("sdk", SDK), ("agentplugins", INSTALLER)):
            origin = Path(modules[name]["Dir"]).resolve(strict=True)
            destination = root / "modules" / label
            shutil.copytree(origin, destination)
            original_hash = g0.package_digest(origin)
            require(g0.package_digest(destination) == original_hash, "staged_module_tree_mismatch")
            staged[label] = destination
            evidence.setdefault("staged_modules", {})[label] = {"path": name, "version": modules[name]["Version"],
                "sum": modules[name]["Sum"], "go_mod_sum": modules[name]["GoModSum"], "origin": str(origin),
                "physical_path": str(destination), "tree_sha256": original_hash}
        binary = root / ("claude-notifications.exe" if os.name == "nt" else "claude-notifications")
        command("build", [go, "build", "-trimpath", "-buildvcs=true", "-ldflags=-s -w", "-o", binary,
                          "./cmd/claude-notifications"], timeout=300)
        metadata = command("actual_binary_metadata", [go, "version", "-m", binary])
        (evidence_dir / "go-version-m.txt").write_bytes(metadata)
        evidence["binary"] = {"physical_path": str(binary), "sha256": sha(binary),
                              **build_identity(metadata, modules, args.commit, args.platform, args.arch)}
        (evidence_dir / "go-version-m.txt").write_bytes(metadata)
        extension = ".exe" if os.name == "nt" else ""
        shutil.copyfile(binary, evidence_dir / ("claude-notifications-" + args.platform + "-" + args.arch + extension))
        # A second real link from the same source qualifies replacement without
        # inventing a version or mutating executable bytes. Both are stripped for
        # the installer's established 32 MiB bound; object cache is shared.
        updated_binary = root / ("claude-notifications-update" + extension)
        command("build_update", [go, "build", "-trimpath", "-buildvcs=true", "-ldflags=-s -w -buildid=gemini-native-update", "-o", updated_binary,
                                 "./cmd/claude-notifications"], timeout=300)
        update_metadata = command("actual_update_metadata", [go, "version", "-m", updated_binary])
        evidence["update_binary"] = {"physical_path": str(updated_binary), "sha256": sha(updated_binary),
                                    **build_identity(update_metadata, modules, args.commit, args.platform, args.arch)}
        require(sha(binary) != sha(updated_binary), "actual_update_artifacts_identical")
        shutil.copyfile(updated_binary, evidence_dir / ("claude-notifications-update-" + args.platform + "-" + args.arch + extension))
        (evidence_dir / "go-version-m-update.txt").write_bytes(update_metadata)
        install = root / "cli"
        install.mkdir(mode=0o700)
        (install / MARKER).write_text("fresh exact TEST CLI installation\n", encoding="utf-8")
        write_json(install / "package.json", {"name": "an-gemini-test", "private": True,
                                             "dependencies": {"@google/gemini-cli": "0.62.0"}})
        command("npm_exact_CLI", [node, npm, "install", "--prefix", install, "--include=optional", "--no-audit", "--no-fund"], cwd=install)
        shutil.copyfile(install / "package-lock.json", evidence_dir / "npm-package-lock.json")
        package = install / "node_modules/@google/gemini-cli/package.json"
        info = json.loads(package.read_bytes())
        entry = info["bin"] if isinstance(info["bin"], str) else info["bin"]["gemini"]
        executable = (package.parent / entry).resolve(strict=True)
        evidence["CLI"] = {"physical_path": str(executable), **g0.cli_identity(str(executable), str(install))}
        installed_ui = install / "ui-contract.json"
        installed_ui.write_bytes(raw_ui)  # Exact trusted public metadata; no reconstructed menu.
        require(sha(installed_ui) == sha(uipath), "UI_copy_mismatch")
        common = ["--gemini-executable", executable, "--node-executable", node, "--cli-install-root", install,
                  "--hook-shell", shell, "--ui-contract", installed_ui, "--timeout", "240"]
        if system_root:
            common += ["--system-root", system_root]
        # The parent also receives G0's minimum environment. Both drivers create
        # distinct new profiles and replace it again for their native children.
        for label, driver, extra, manifest, limit in (
            ("G0", g0path, [], "evidence.json", 280),
            ("G5", g5path, ["--trusted-orchestrator", "--binary", binary, "--update-binary", updated_binary, "--g0-driver", g0path,
                           "--sdk-module-root", staged["sdk"], "--installer-module-root", staged["agentplugins"]],
             "production-evidence.json", 650)):
            lab = root / ("TEST-" + label)
            try:
                command(label, [python, "-B", driver, *common, "--lab-root", lab, *extra], child_env=env, timeout=limit)
                facts = json.loads((lab / manifest).read_bytes())
                require(facts.get("native_execution") == "passed_scenarios" if label == "G0" else
                        facts.get("driver", "").startswith("passed_"), "driver_success_manifest_missing")
                evidence[label] = "passed_implemented_scenarios"
            except Exception:
                evidence[label] = "failed"
            finally:
                # These two trusted driver manifests contain fixed facts/hashes,
                # never native payloads, provider bodies or terminal transcripts.
                if (lab / manifest).is_file():
                    require((lab / manifest).stat().st_size <= 1024 * 1024, "driver_evidence_size")
                    shutil.copyfile(lab / manifest, evidence_dir / (label + "-evidence.json"))
                    driver_facts = json.loads((lab / manifest).read_bytes())
                    # Fixed classifications and version metadata only, never provider or terminal text.
                    print(json.dumps({"native_driver": label, **{key: driver_facts[key] for key in
                        ("classification", "exception_type", "native_version_probe", "setup_failure", "setup_cleanup_failure",
                         "bridge_failure", "cleanup_classification", "native_execution", "driver")
                        if key in driver_facts}}), flush=True)
        require(all(evidence.get(label) == "passed_implemented_scenarios" for label in ("G0", "G5")), "native_qualification_failed")
        evidence["status"] = "passed_implemented_scenarios_with_external_gates_pending"
    except Exception as exc:
        evidence["classification"] = str(exc) if isinstance(exc, Red) else "CI_harness_error"
    finally:
        evidence["TEST_root"] = str(root) if root else None
        write_json(evidence_dir / "ci-evidence.json", evidence)
    return evidence["status"].startswith("passed_")


class PureChecks(unittest.TestCase):
    def test_UI_rejects_typed_commands(self):
        ui = {"permission_pattern": "public menu", "approve": "1\r", "deny": "2\r", "cancel": "\x1b", "source_sha256": "a" * 64}
        self.assertEqual(ui_contract(json.dumps(ui)), ui)
        for change in ({"approve": "shell-command\r"}, {"deny": "1\r"}, {"cancel": "y"}, {"source_sha256": "unknown"}, {"raw_log": "private"}):
            with self.assertRaises(Red):
                ui_contract(json.dumps(dict(ui, **change)))

    def test_linked_build_pins_and_commit(self):
        # Public metadata-shaped values exercise actual parsing and decisions;
        # no subprocess mock or fake native/installer passing gate.
        items = [{"Path": name, "Version": "v1.2.0", "Sum": "h1:pin", "GoModSum": "h1:mod", "Dir": "/public/module"} for name in (SDK, INSTALLER)]
        raw = ("binary: go1.25.8\n" + "".join("\tdep\t" + name + "\tv1.2.0\th1:pin\n" for name in (SDK, INSTALLER)) +
               "\tbuild\tCGO_ENABLED=1\n\tbuild\tGOOS=linux\n\tbuild\tGOARCH=amd64\n\tbuild\tvcs.revision=" + "a" * 40 + "\n\tbuild\tvcs.modified=false\n").encode()
        modules = module_graph(json_stream("\n".join(json.dumps(x) for x in items).encode()))
        self.assertIn(SDK, build_identity(raw, modules, "a" * 40, "linux", "amd64")["dependencies"])
        for bad in (raw.replace(b"h1:pin", b"h1:wrong"), raw.replace(b"CGO_ENABLED=1", b"CGO_ENABLED=0"), raw.replace(b"modified=false", b"modified=true")):
            with self.assertRaises(Red):
                build_identity(bad, modules, "a" * 40, "linux", "amd64")
        with self.assertRaises(Red):
            module_graph([dict(items[0], Replace={"Dir": "/local"}), items[1]])
        with self.assertRaises(Red):
            build_identity(raw, modules, "b" * 40, "linux", "amd64")

    def test_runner_attestation_precedes_all_side_effects(self):
        with self.assertRaises(Red):
            run(argparse.Namespace(trusted_github_runner=False))

    def test_graph_and_home_boundaries(self):
        self.assertEqual(graph_pins(b"\npublic/module v1.0.0 h1:pin h1:mod\n")[0]["Version"], "v1.0.0")
        with self.assertRaises(Red):
            graph_pins(b"public/module v1.0.0\n")
        # A hosted runner temp directory under HOME must be rejected for labs.
        home = Path.home().resolve()
        with self.assertRaises(Red):
            outside(home / "work/_temp/TEST-lab", (REPO, home))
        with self.assertRaises(Red):
            outside(REPO / "TEST-lab", (REPO, home))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--trusted-github-runner", action="store_true")
    for name in ("platform", "arch", "commit", "hook-shell", "system-root", "g0-driver", "g5-driver", "ui-contract"):
        parser.add_argument("--" + name)
    args = parser.parse_args()
    if args.self_test:
        result = unittest.TextTestRunner().run(unittest.defaultTestLoader.loadTestsFromTestCase(PureChecks))
        return 0 if result.wasSuccessful() else 1
    require(args.hook_shell, "explicit_hook_shell_required")
    return 0 if run(args) else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(json.dumps({"red": str(exc) if isinstance(exc, Red) else "CI_harness_error"}), file=sys.stderr)
        sys.exit(1)
