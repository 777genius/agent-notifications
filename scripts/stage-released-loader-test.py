#!/usr/bin/env python3
"""A release mismatch or failed download must not replace the public loader."""
import base64
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("release_loader", Path(__file__).with_name("stage-released-loader.py"))
loader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(loader)


class ReleaseLoaderTest(unittest.TestCase):
    def test_stable_release_uses_exact_commit_and_keeps_release_bytes(self):
        commit = "a" * 40
        body = b"#!/usr/bin/env bash\nprintf 'released loader\\n'\n"
        responses = {
            f"repos/{loader.REPOSITORY}/releases/latest": {"tag_name": "v1.46.1"},
            f"repos/{loader.REPOSITORY}/commits/v1.46.1": {"sha": commit},
            f"repos/{loader.REPOSITORY}/contents/bin/setup.sh?ref={commit}": {
                "type": "file", "encoding": "base64", "content": base64.encodebytes(body).decode(),
            },
        }
        calls = []

        def gh(args):
            self.assertEqual(args[:2], ["gh", "api"])
            calls.append(args[2])
            return json.dumps(responses[args[2]]).encode()

        with tempfile.TemporaryDirectory(prefix="TEST-release-loader-") as directory:
            target = Path(directory) / "install.sh"
            target.write_bytes(b"unreleased candidate")
            with patch.object(loader.subprocess, "check_output", side_effect=gh):
                loader.stage(target)
            self.assertEqual(target.read_bytes(), body)
            self.assertEqual(calls, list(responses))
            self.assertEqual(list(Path(directory).iterdir()), [target])

    def test_invalid_release_or_failed_fetch_preserves_existing_loader(self):
        failures = [
            [{"tag_name": "v1.47.0-rc.1"}],
            [{"tag_name": "v1.46.1"}, {"sha": "main"}],
            [{"tag_name": "v1.46.1"}, {"sha": "a" * 40}, subprocess.CalledProcessError(1, "gh")],
        ]
        for responses in failures:
            with self.subTest(responses=responses), tempfile.TemporaryDirectory(prefix="TEST-release-loader-") as directory:
                target = Path(directory) / "install.sh"
                target.write_bytes(b"current public loader")
                encoded = [json.dumps(value).encode() if isinstance(value, dict) else value for value in responses]
                with patch.object(loader.subprocess, "check_output", side_effect=encoded):
                    with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                        loader.stage(target)
                self.assertEqual(target.read_bytes(), b"current public loader")
                self.assertEqual(list(Path(directory).iterdir()), [target])


if __name__ == "__main__":
    unittest.main()
