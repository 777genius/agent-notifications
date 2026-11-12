"""Reject false prerequisite success independently of native desktop availability."""
import importlib.util
import json
import sys
from pathlib import Path
import tempfile
import unittest
import wave

SPEC = importlib.util.spec_from_file_location("pi_preflight", Path(__file__).with_name("pi-native-preflight.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class PrerequisiteEvidenceTests(unittest.TestCase):
    def test_every_required_observation_is_mandatory(self):
        for target in ("linux", "darwin", "windows"):
            facts = dict.fromkeys(MODULE.COMMON, True)
            if target == "windows":
                facts.update(dict.fromkeys(("windows_client", "shell", "uia"), True))
            self.assertEqual(MODULE.classify(target, target, facts)["status"], "prerequisites_ready")
            for name in facts:
                if name == "native_host":
                    continue
                for invalid in (False, None, "true", 1):
                    with self.subTest(target=target, observation=name, invalid=invalid):
                        changed = dict(facts, **{name: invalid})
                        self.assertEqual(MODULE.classify(target, target, changed)["status"], "unqualified")

    def test_cross_platform_and_partial_receipts_never_pass(self):
        facts = dict.fromkeys(MODULE.COMMON, True)
        self.assertEqual(MODULE.classify("linux", "darwin", facts)["status"], "unqualified")
        self.assertEqual(MODULE.classify("linux", "linux", {})["status"], "unqualified")

    def test_ready_prerequisites_never_grant_product_qualification(self):
        result = MODULE.classify("linux", "linux", dict.fromkeys(MODULE.COMMON, True))
        self.assertEqual(result["status"], "prerequisites_ready")
        self.assertIs(result["desktop_qualified"], False)
        self.assertEqual(result["scope"], "synthetic_native_observers_only")

    def test_visible_xterm_window_requires_one_owned_pid_match(self):
        # Production caller selects --pid <launched xterm PID> first.
        for output, code, expected in (
            ("12345\\n", 0, "12345"), (" 12345\\n", 0, "12345"),
            ("", 1, None), ("12345\\n", 1, None),
            ("12345\\n67890\\n", 0, None),
            ("0\\n", 0, None), ("not-a-window\\n", 0, None),
        ):
            with self.subTest(output=output, code=code):
                self.assertEqual(MODULE.owned_visible_xterm_window(output.replace("\\n", "\n"), code), expected)

    def test_title_fallback_requires_unique_mapped_window_and_exact_held_pid(self):
        cases = (
            ("12345\\n", 0, "123\\n", 0, 123, "12345"),
            ("12345\\n", 0, "999\\n", 0, 123, None),
            ("12345\\n", 0, "123\\n", 1, 123, None),
            ("12345\\n67890\\n", 0, "123\\n", 0, 123, None),
            ("12345\\n", 1, "123\\n", 0, 123, None),
            ("12345\\n", 0, "garbage\\n", 0, 123, None),
            ("12345\\n", 0, "123\\n456\\n", 0, 123, None),
        )
        for titled, status, owner, owner_status, launched, expected in cases:
            with self.subTest(titled=titled, owner=owner, status=status, owner_status=owner_status):
                self.assertEqual(
                    MODULE.verified_title_window(titled.replace("\\n", "\n"), status,
                                                 owner.replace("\\n", "\n"), owner_status, launched),
                    expected)

    def test_xterm_relaunch_requires_fresh_owned_window_proof(self):
        class Lab:
            def __init__(self):
                self.report, self.stopped = {}, []
            def stop(self, child):
                self.stopped.append(child)

        lab, starts = Lab(), []
        def launch():
            child = object()
            starts.append(child)
            return child
        def observe(child):
            if child is starts[0]:
                raise MODULE.Unqualified("xterm_window_not_visible")
            return "12345"

        self.assertEqual(MODULE.mapped_xterm_with_one_relaunch(lab, launch, observe), "12345")
        self.assertEqual(len(starts), 2)
        self.assertEqual(lab.stopped, [starts[0]])
        self.assertIs(lab.report["xterm_mapping_relaunch"], True)

    def test_xterm_relaunch_fails_if_both_fixtures_unmapped(self):
        class Lab:
            def __init__(self):
                self.report, self.stopped = {}, []
            def stop(self, child):
                self.stopped.append(child)

        lab, starts = Lab(), []
        def launch():
            child = object()
            starts.append(child)
            return child
        def observe(_child):
            raise MODULE.Unqualified("xterm_window_not_visible")

        with self.assertRaisesRegex(MODULE.Unqualified, "^xterm_window_not_visible$"):
            MODULE.mapped_xterm_with_one_relaunch(lab, launch, observe)
        self.assertEqual(len(starts), 2)
        self.assertEqual(lab.stopped, [starts[0]])

    def test_xterm_relaunch_never_retries_other_errors(self):
        class Lab:
            def __init__(self):
                self.report, self.stopped = {}, []
            def stop(self, child):
                self.stopped.append(child)

        for failure in ("xterm_fixture_exited", "fixture_command_failed"):
            lab, starts = Lab(), []
            def launch():
                starts.append(object())
                return starts[-1]
            def observe(_child):
                raise MODULE.Unqualified(failure)
            with self.subTest(failure=failure):
                with self.assertRaisesRegex(MODULE.Unqualified, "^" + failure + "$"):
                    MODULE.mapped_xterm_with_one_relaunch(lab, launch, observe)
                self.assertEqual(len(starts), 1)
                self.assertEqual(lab.stopped, [])
                self.assertNotIn("xterm_mapping_relaunch", lab.report)

    def test_failed_fixture_command_never_exports_raw_process_output(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-private-report-") as name:
            report = {}
            lab = MODULE.Lab(Path(name), report)
            sentinel = "TEST_PRIVATE_FIXTURE_OUTPUT"
            with self.assertRaisesRegex(MODULE.Unqualified, "fixture_command_failed"):
                lab.run([sys.executable, "-c",
                         "import sys; sys.stdout.write('TEST_PRIVATE_FIXTURE_OUTPUT'); "
                         "sys.stderr.write('TEST_PRIVATE_FIXTURE_OUTPUT'); sys.exit(23)"])
            failure = report["command_failure"]
            self.assertEqual(failure["exit"], 23)
            self.assertEqual(failure["stdout_bytes"], len(sentinel))
            self.assertEqual(failure["stderr_bytes"], len(sentinel))
            self.assertNotIn(sentinel, json.dumps(report))
            self.assertNotIn("stdout", failure)
            self.assertNotIn("stderr", failure)

    def test_cue_has_real_nonzero_pcm_without_external_assets(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-cue-") as name:
            path = Path(name) / "cue.wav"
            MODULE.cue(path)
            with wave.open(str(path), "rb") as cue:
                self.assertEqual((cue.getnchannels(), cue.getsampwidth(), cue.getframerate(), cue.getnframes()), (1, 2, 48000, 24000))
                self.assertNotEqual(set(cue.readframes(24000)), {0})


if __name__ == "__main__":
    unittest.main()
