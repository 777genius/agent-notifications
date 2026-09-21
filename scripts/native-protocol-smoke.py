#!/usr/bin/env python3
"""No-notification subprocess qualification of a newly built standalone helper.

Never pass an installed/unknown notifier. A caller-supplied exact build hash is
required, and .app executables are refused. No LaunchServices marker is sent,
so valid structured requests stop before AppKit/UN initialization.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--expected-sha256", required=True)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    if any(part.endswith(".app") for part in binary.parts):
        parser.error("use the standalone test build, not an app installation")
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    if digest != args.expected_sha256:
        parser.error("test binary fingerprint mismatch")
    checks = []

    def invoke(arguments):
        return subprocess.run([str(binary), *arguments], capture_output=True,
                              timeout=3, check=False)

    cap = invoke(["--capabilities-json"])
    assert cap.returncode == 0 and cap.stderr == b""
    capabilities = json.loads(cap.stdout)
    assert capabilities["schemaVersion"] == 1
    assert capabilities["actionKinds"] == ["none"]
    assert capabilities["receiptSupport"] is True
    checks.append("standalone capabilities without permission or app launch")

    help_result = invoke(["--help"])
    assert help_result.returncode == 0 and b"Usage:" in help_result.stdout
    for field in ["-title", "-message", "-subtitle"]:
        for literal in ["--help", "--capabilities-json", "--send-json"]:
            arguments = ["-title", "title", "-message", "body", field, literal]
            result = invoke(arguments)
            assert result.returncode != 0 and result.stdout == b""
            assert b"LaunchServices" in result.stderr
    checks.append("option-looking legacy text never chooses help or protocol mode")

    with tempfile.TemporaryDirectory(prefix="native-protocol-smoke-") as scratch:
        root = Path(scratch).resolve(strict=True)

        def request(changes):
            correlation, nonce = str(uuid.uuid4()), str(uuid.uuid4())
            directory = root / correlation
            directory.mkdir(mode=0o700)
            request_file = directory / (nonce + ".request")
            receipt_file = directory / (nonce + ".receipt")
            payload = dict(schemaVersion=1, correlationID=correlation, nonce=nonce,
                           bootID="test-boot", notAfter=10, title="--help",
                           body="literal", category="info", action="none", silent=True)
            payload.update(changes)
            request_file.write_text(json.dumps(payload))
            request_file.chmod(0o600)
            return request_file, receipt_file, correlation, nonce

        def submit(request_file, receipt_file):
            return invoke(["--send-json", "--request-file", str(request_file),
                           "--receipt-file", str(receipt_file)])

        for changes, reason in [({"schemaVersion": 999}, "unsupported_version"),
                                ({"action": "execute"}, "unsupported_action"),
                                ({"body": "\0"}, "malformed_request"),
                                ({}, "unsupported_notifier"),
                                ({"title": "👩‍💻", "body": "می\u200cروم"}, "unsupported_notifier")]:
            request_file, receipt_file, correlation, nonce = request(changes)
            result = submit(request_file, receipt_file)
            assert result.returncode == 0 and result.stdout == result.stderr == b""
            raw = receipt_file.read_bytes()
            receipt = json.loads(raw)
            assert receipt["correlationID"] == correlation and receipt["nonce"] == nonce
            assert receipt["notificationID"] == correlation
            assert receipt["status"] == "rejected" and receipt["reason"] == reason
            assert receipt["retrySafe"] is False
            assert not request_file.exists()
            assert submit(request_file, receipt_file).returncode != 0
            assert receipt_file.read_bytes() == raw
            checks.append("correlated " + reason + ", duplicate launch cannot overwrite")

        request_file, receipt_file, _, _ = request({})
        original = request_file.read_bytes()
        target = request_file.with_name("target")
        request_file.rename(target)
        request_file.symlink_to(target)
        assert submit(request_file, receipt_file).returncode == 0
        assert json.loads(receipt_file.read_bytes())["reason"] == "invalid_file"
        assert target.read_bytes() == original
        checks.append("symlink payload rejected without altering target")

    print(json.dumps({"binarySHA256": digest, "status": "passed", "checks": checks,
                      "notificationsSent": 0, "scope": "standalone native boundary"}, indent=2))


if __name__ == "__main__":
    main()
