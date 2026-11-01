"""Reject false prerequisite success independently of native desktop availability."""
import importlib.util
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

    def test_cue_has_real_nonzero_pcm_without_external_assets(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-cue-") as name:
            path = Path(name) / "cue.wav"
            MODULE.cue(path)
            with wave.open(str(path), "rb") as cue:
                self.assertEqual((cue.getnchannels(), cue.getsampwidth(), cue.getframerate(), cue.getnframes()), (1, 2, 48000, 24000))
                self.assertNotEqual(set(cue.readframes(24000)), {0})


if __name__ == "__main__":
    unittest.main()
