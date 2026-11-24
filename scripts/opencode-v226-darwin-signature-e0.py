#!/usr/bin/env python3
"""Observe the official 2.2.6 Darwin signature, without granting admission."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

IMAGE = '90e4d03597b03d2051b65b4d793467764e55bacd8e22bf4824479c7233ea0e15'
REQUIREMENT = '=anchor apple generic and identifier "opencode" and certificate leaf[subject.OU] = "5NZ4Q7NXJ4"'
WRONG_TEAM = '=anchor apple generic and identifier "opencode" and certificate leaf[subject.OU] = "0000000000"'


def verify(image, requirement):
    return subprocess.run(['/usr/bin/codesign', '--verify', '--strict', '-R', requirement, str(image)],
                          stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                          env={'PATH': '/usr/bin:/bin', 'LC_ALL': 'C'}, timeout=60, check=False)


def main():
    if sys.platform != 'darwin' or len(sys.argv) != 2:
        raise ValueError('invalid_invocation')
    image = Path(sys.argv[1]).resolve(strict=True)
    with image.open('rb') as source:
        if hashlib.file_digest(source, 'sha256').hexdigest() != IMAGE:
            raise ValueError('official_image_unverified')
    if verify(image, REQUIREMENT).returncode != 0 or verify(image, WRONG_TEAM).returncode != 3:
        raise ValueError('signer_or_negative_unverified')
    with tempfile.TemporaryDirectory(prefix='TEST-opencode-signature-') as directory:
        modified = Path(directory) / 'modified-opencode'
        shutil.copyfile(image, modified)
        with modified.open('r+b') as stream:
            stream.seek(16384)
            original = stream.read(1)
            if len(original) != 1:
                raise ValueError('image_too_short')
            stream.seek(16384)
            stream.write(bytes([original[0] ^ 1]))
        rejected = verify(modified, REQUIREMENT)
        if rejected.returncode != 1 or b'code or signature have been modified' not in rejected.stderr:
            raise ValueError('modified_image_rejection_unverified')
    print(json.dumps({'status': 'signature_observed_only', 'officialTeam': '5NZ4Q7NXJ4',
                      'officialIdentifier': 'opencode', 'wrongTeamRefused': True,
                      'modifiedBytesRefused': True, 'nativeObserverQualified': False,
                      'nativeClockQualified': False, 'notificationDeliveryQualified': False}))


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, subprocess.SubprocessError):
        print(json.dumps({'status': 'unqualified', 'nativeObserverQualified': False,
                          'nativeClockQualified': False, 'notificationDeliveryQualified': False}))
        sys.exit(1)
