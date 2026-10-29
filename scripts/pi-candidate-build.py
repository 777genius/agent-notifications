#!/usr/bin/env python3
"""Build an exact clean Pi qualification candidate using normal release recipes.

No release is published. Development workspaces/replacements are rejected. The
supplied macOS helper remains subject to the normal managed writer's signature,
sealed-tree and external-attestation verification during actual installation.
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
import subprocess

REPO = Path(__file__).resolve().parents[1]
PLATFORMS = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}
ARCHES = {"x86_64": "amd64", "AMD64": "amd64", "arm64": "arm64", "aarch64": "arm64", "ARM64": "arm64"}
HELPER_SPEC = importlib.util.spec_from_file_location("pi_macos_helper", Path(__file__).with_name("pi-macos-helper.py"))
H = importlib.util.module_from_spec(HELPER_SPEC)
HELPER_SPEC.loader.exec_module(H)


def require(ok, code):
    if not ok:
        raise RuntimeError(code)


def sha(path):
    result = hashlib.sha256()
    with Path(path).open("rb") as stream:
        while block := stream.read(65536):
            result.update(block)
    return result.hexdigest()


def modules(text):
    decoder, result = json.JSONDecoder(), []
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        result.append(value)
        text = text.lstrip()[end:]
    return result


def pinned_graph(graph):
    require(isinstance(graph, list) and graph and sum(value.get("Main") is True for value in graph) == 1,
            "candidate_module_graph_invalid")
    require(not any(value.get("Replace") or not value.get("Path")
                    or not value.get("Main") and not value.get("Version") for value in graph),
            "candidate_module_replacement_or_unpinned_version_refused")


def linked_source(info, source):
    settings = {}
    dependencies = []
    for line in info.splitlines():
        fields = line.strip().split("\t")
        if fields[0] == "build" and len(fields) == 2 and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            settings[key] = value
        elif fields[0] == "dep":
            dependencies.append(fields)
    require("=>" not in info and settings.get("vcs.revision") == source
            and settings.get("vcs.modified") == "false" and dependencies
            and all(len(row) == 4 and row[2] and row[3].startswith("h1:") for row in dependencies),
            "candidate_linked_source_or_module_identity_invalid")


def inventory(stage):
    result = {}
    for path in sorted(stage.rglob("*")):
        relative = path.relative_to(stage).as_posix()
        if path.is_symlink():
            result[relative] = {"link": os.readlink(path)}
        elif path.is_file():
            result[relative] = {"sha256": sha(path), "bytes": path.stat().st_size}
    return result


def build(args):
    require(not args.output.exists(), "candidate_output_must_be_fresh")
    args.output.mkdir(parents=True, mode=0o700)
    stage = args.output / "stage"
    stage.mkdir(mode=0o700)
    report = {"schema_version": 1, "status": "failed", "platform": args.platform, "binary_architecture": args.arch,
              "host_architecture": platform.machine(), "release_equivalent": False, "desktop_qualified": False}
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="1", GOFLAGS="", GOOS=args.platform, GOARCH=args.arch)
    if args.platform == "windows":
        env["GOFLAGS"] = "-tags=windows_callback_asset"

    def run(argv, timeout=600):
        result = subprocess.run([str(x) for x in argv], cwd=REPO, env=env, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
        require(len(result.stdout) + len(result.stderr) <= 4 * 1024 * 1024, "candidate_build_output_limit")
        require(result.returncode == 0, "candidate_command_failed:" + str(argv[0]) + ":" +
                (result.stdout + result.stderr).decode(errors="replace")[-4000:])
        return result.stdout.decode()

    try:
        require(PLATFORMS.get(platform.system()) == args.platform and ARCHES.get(platform.machine()) == args.arch,
                "candidate_requires_native_release_build_host")
        require(not run(["git", "status", "--porcelain", "--untracked-files=all"]).strip(), "candidate_source_checkout_not_clean")
        source = run(["git", "rev-parse", "HEAD"]).strip()
        require(re.fullmatch(r"[a-f0-9]{40}", source), "candidate_source_sha_invalid")
        report.update(source_sha=source, checkout_clean=True, go_work="off", go_version=run([args.go, "version"]).strip(),
                      go_mod_sha256=sha(REPO / "go.mod"), go_sum_sha256=sha(REPO / "go.sum"))
        graph = modules(run([args.go, "list", "-m", "-json", "all"]))
        pinned_graph(graph)
        report["modules"] = [{key: value[key] for key in ("Path", "Version", "Sum", "Main") if key in value} for value in graph]
        suffix = ".exe" if args.platform == "windows" else ""
        main = "claude-notifications-" + args.platform + "-" + args.arch + suffix
        if args.platform == "windows":
            require(args.arch == "amd64", "windows_release_supports_amd64")
            run(["pwsh", "-NoProfile", "-File", REPO / "native/windows-callback/build.ps1",
                 "-OutputDirectory", args.output / "native-callback", "-EmbedAsset"])
        for package in ("claude-notifications", "sound-preview", "list-devices", "list-sounds"):
            name = package + "-" + args.platform + "-" + args.arch + suffix
            run([args.go, "build", "-trimpath", "-ldflags=-s -w", "-o", stage / name, "./cmd/" + package])
        if args.platform == "windows":
            run([args.go, "build", "-trimpath", "-ldflags=-s -w -H=windowsgui", "-o",
                 stage / ("claude-notifications-windows-amd64-focus.exe"), "./cmd/claude-notifications"])
            run(["pwsh", "-NoProfile", "-File", REPO / "native/windows-callback/verify-embedded.ps1",
                 "-Executable", stage / main, "-Helper", args.output / "native-callback/helper.exe"])
            report["embedded_callback_verified"] = True
        elif args.platform == "darwin":
            require(args.native_assets is not None, "authenticated_macos_helper_inputs_required")
            bundle, attestation = args.native_assets / "ClaudeNotifier.app", args.native_assets / "ClaudeNotifier.app.managed-runtime.json"
            require(bundle.is_dir() and attestation.is_file() and not attestation.is_symlink(), "macos_helper_and_attestation_required")
            helper_input = json.loads((args.native_assets / "helper-input.json").read_text())
            helper_pin = H.load_pin()
            H.candidate_tree(helper_pin)
            H.validate_receipt(helper_input, helper_pin)
            require(sha(bundle / "Contents/MacOS/terminal-notifier-modern") == helper_pin["executable_sha256"]
                    and sha(attestation) == helper_pin["attestation_sha256"], "candidate_macos_helper_pin_mismatch")
            shutil.copytree(bundle, stage / bundle.name, symlinks=True)
            shutil.copy2(attestation, stage / attestation.name)
            report["native_attestation_sha256"] = sha(attestation)
            report["native_helper_input"] = helper_input
        info = run([args.go, "version", "-m", stage / main])
        linked_source(info, source)
        report["go_build_information"] = info
        version = re.search(r'^var ConsumerVersion = "([^"]+)"$', (REPO / "internal/config/runtime.go").read_text(), re.MULTILINE)
        require(version is not None, "candidate_release_version_missing")
        # This is a real executable from the identical clean source and module
        # graph, with a normal linker version identity. Never corrupt bytes or
        # insert a fixture delivery path to manufacture a runtime replacement.
        replacement = args.output / "replacement-stage"
        shutil.copytree(stage, replacement, symlinks=True)
        replacement_version = version[1] + "-TEST-pi-runtime-replacement"
        linker = "-s -w -X github.com/777genius/agent-notifications/internal/config.ConsumerVersion=" + replacement_version
        for package in ("claude-notifications", "sound-preview", "list-devices", "list-sounds"):
            name = package + "-" + args.platform + "-" + args.arch + suffix
            run([args.go, "build", "-trimpath", "-ldflags=" + linker, "-o", replacement / name, "./cmd/" + package])
        if args.platform == "windows":
            run([args.go, "build", "-trimpath", "-ldflags=" + linker + " -H=windowsgui", "-o",
                 replacement / "claude-notifications-windows-amd64-focus.exe", "./cmd/claude-notifications"])
            run(["pwsh", "-NoProfile", "-File", REPO / "native/windows-callback/verify-embedded.ps1",
                 "-Executable", replacement / main, "-Helper", args.output / "native-callback/helper.exe"])
        replacement_info = run([args.go, "version", "-m", replacement / main])
        linked_source(replacement_info, source)
        require(run([replacement / main, "--version"]).strip().endswith(" v" + replacement_version),
                "candidate_replacement_version_not_executable")
        require(sha(stage / main) != sha(replacement / main), "candidate_replacement_bytes_unchanged")
        for label, package_version in (("A", version[1]), ("B", version[1] + "-TEST-pi-update")):
            run([args.go, "run", "./cmd/build-portable-package", "-version", package_version, "-os", args.platform,
                 "-arch", args.arch, "-executable", stage / main, "-output", args.output / ("package-" + label + ".zip"),
                 "-workdir", args.output / ("package-" + label)])
        require(not run(["git", "status", "--porcelain", "--untracked-files=all"]).strip(), "candidate_build_changed_checkout")
        report.update(status="built", release_equivalent=True, candidate_binary=main,
                      candidate_binary_sha256=sha(stage / main), package_sha256=sha(args.output / "package-A.zip"),
                      updated_package_sha256=sha(args.output / "package-B.zip"), stage_inventory=inventory(stage),
                      replacement_runtime_version=replacement_version,
                      replacement_candidate_binary_sha256=sha(replacement / main),
                      replacement_go_build_information=replacement_info, replacement_stage_inventory=inventory(replacement),
                      canonical_assets={name: sha(REPO / name) for name in
                                        ("claude_icon.png", "sounds/task-complete.mp3", "sounds/question.mp3")})
    except Exception as error:
        report["failure"] = str(error)
    (args.output / "build.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "failure": report.get("failure"), "release_equivalent": report["release_equivalent"]}))
    return 0 if report["release_equivalent"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("linux", "darwin", "windows"), required=True)
    parser.add_argument("--arch", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--native-assets", type=Path)
    parser.add_argument("--go", default="go")
    args = parser.parse_args()
    args.output = args.output.resolve()
    if args.native_assets:
        args.native_assets = args.native_assets.resolve(strict=True)
    raise SystemExit(build(args))
