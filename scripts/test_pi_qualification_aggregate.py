"""Synthetic metadata guard tests only; these are never native OS evidence."""
import importlib.util
from pathlib import Path
import unittest

PATH = Path(__file__).with_name("pi-qualification-aggregate.py")
SPEC = importlib.util.spec_from_file_location("pi_aggregate", PATH)
A = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(A)
SOURCE = "a" * 40
HASH = "b" * 64


def metadata(platform="linux"):
    arch = "arm64" if platform == "darwin" else "amd64"
    suffix = ".exe" if platform == "windows" else ""
    names = [name + "-" + platform + "-" + arch + suffix for name in
             ("claude-notifications", "sound-preview", "list-devices", "list-sounds")]
    assets = {name: A.sha(A.REPO / name) for name in ("claude_icon.png", "sounds/task-complete.mp3", "sounds/question.mp3")}
    build = {"schema_version": 1, "platform": platform, "status": "built", "release_equivalent": True,
             "checkout_clean": True, "go_work": "off", "source_sha": SOURCE,
             "modules": [{"Path": "main", "Main": True}, {"Path": "sdk", "Version": "v1.0.0"}],
             "go_build_information": "\tdep\tsdk\tv1.0.0\th1:TEST\n\tbuild\tvcs.revision=" + SOURCE + "\n\tbuild\tvcs.modified=false\n",
             "go_mod_sha256": A.sha(A.REPO / "go.mod"), "go_sum_sha256": A.sha(A.REPO / "go.sum"),
             "binary_architecture": arch, "candidate_binary_sha256": HASH, "package_sha256": HASH,
             "updated_package_sha256": HASH, "canonical_assets": assets,
             "stage_inventory": {name: {"sha256": HASH} for name in names}}
    acquisition = {"schema_version": 1, "platform": platform, "architecture": arch,
                   "version": A.PINS["version"], "source_commit": A.PINS["source_commit"],
                   "archive_sha256": A.PINS["targets"][platform + "/" + arch]["sha256"], "binary_sha256": HASH}
    identity = {"schema_version": 1, "source_sha": SOURCE, "host_os": A.HOSTS[platform],
                "working_tree_dirty": False, "candidate_binary_sha256": HASH, "package_sha256": HASH,
                "pi_binary_sha256": HASH, "unrelated_mcp_policy_enabled": False, "installed_asset_sha256": assets}
    process = {**identity, "status": "passed_installed_pi_process", "updated_package_sha256": HASH,
               "webhook_private_payload_failures": 0,
               "cases": [{"case": name} for name in A.PROCESS_CASES] + [{"case": case, "mode": mode} for case, mode in A.NATIVE_MODES]}
    for row in process["cases"]:
        if row["case"] == "setup-uninstall":
            row.update(pi_executable_supplied=False, foreign_bytes_preserved=True)
    desktop = {**identity, "native_product_observations_passed": True, "owned_children_collected": True,
               "cases": [{"case": name, "checks": {check: True for check in A.CHECKS},
                           "pending_question_at_click": name == "question"} for name in A.CASES]}
    if platform == "darwin":
        desktop["native_helper_verified"] = True
        build["stage_inventory"]["ClaudeNotifier.app.managed-runtime.json"] = {"sha256": HASH}
    if platform == "windows":
        build["embedded_callback_verified"] = True
        build["stage_inventory"]["claude-notifications-windows-amd64-focus.exe"] = {"sha256": HASH}
        desktop.update(windows_client=True, render_route_observed=True, host_architecture="AMD64")
    return build, acquisition, process, desktop


class AggregateGuards(unittest.TestCase):
    def test_validated_metadata_contract_on_each_os(self):
        for platform in A.HOSTS:
            self.assertEqual(A.qualify(platform, SOURCE, *metadata(platform))["status"], "qualified")

    def test_status_alone_partial_cases_and_old_source_never_pass(self):
        for mutate in (lambda values: values[3].update(cases=[]),
                       lambda values: values[3].update(source_sha="c" * 40),
                       lambda values: values[2].update(working_tree_dirty=True),
                       lambda values: values[2].update(cases=[])):
            values = metadata()
            mutate(values)
            with self.assertRaises((ValueError, RuntimeError)):
                A.qualify("linux", SOURCE, *values)

    def test_pending_question_and_every_native_check_required(self):
        values = metadata()
        question = next(row for row in values[3]["cases"] if row["case"] == "question")
        question["pending_question_at_click"] = False
        with self.assertRaisesRegex(ValueError, "question"):
            A.qualify("linux", SOURCE, *values)
        for check in A.CHECKS:
            values = metadata()
            values[3]["cases"][0]["checks"][check] = False
            with self.assertRaisesRegex(ValueError, "unobserved"):
                A.qualify("linux", SOURCE, *values)

    def test_windows_no_render_route_or_undisclosed_emulation_refused(self):
        values = metadata("windows")
        values[3]["render_route_observed"] = False
        with self.assertRaisesRegex(ValueError, "windows_client_audio"):
            A.qualify("windows", SOURCE, *values)
        values = metadata("windows")
        values[3]["host_architecture"] = "ARM64"
        with self.assertRaisesRegex(ValueError, "emulation"):
            A.qualify("windows", SOURCE, *values)
        values[3]["execution_mode"] = "x64_emulation"
        A.qualify("windows", SOURCE, *values)


if __name__ == "__main__":
    unittest.main()
