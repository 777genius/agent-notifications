#!/usr/bin/env python3
"""Qualify public navigation API on disposable macOS fixtures, not Codex.

No production app, notification, user data or Automation prompt is used.
Each generated receiver expires after 30s. Evidence is retained on failure too.
"""
import argparse
import hashlib
import json
import os
import platform
import plistlib
from pathlib import Path
import shutil
import subprocess
import tempfile
import uuid


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute-test-fixtures', action='store_true', required=True)
    args = parser.parse_args()
    if not args.execute_test_fixtures or platform.system() != 'Darwin':
        parser.error('requires macOS and explicit test-fixture opt-in')
    repo = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix='navigation-psn-test-')).resolve()
    os.chmod(root, 0o700)
    (root / 'fixture.marker').write_text('synthetic PSN routing only\n')
    snapshot = root / 'source'
    snapshot.mkdir(mode=0o700)
    # Compile only captured source bytes; later repository edits cannot change binding.
    for name in ('ProcessSerialBridge.c', 'ProcessSerialBridge.h',
                 'AppleEventEndpointProbe.swift', 'ProcessSerialRoutingProbe.swift'):
        (snapshot / name).write_bytes((repo / 'swift-notifier/Tests/Probes' / name).read_bytes())
    report = dict(schemaVersion=1, macOS=platform.mac_ver()[0], architecture=platform.machine(),
                  sourceSHA256={p.name: digest(p) for p in sorted(snapshot.iterdir())},
                  runnerSHA256=digest(Path(__file__)), commands=[], binaries={},
                  productionClientActivated=False, passed=False,
                  limitations=['No Developer ID or executable-resource qualification',
                               'No actual Codex GURL or visible-chat qualification',
                               'Single observed restart; PID reuse/update unqualified'])

    def run(label, argv, timeout=30):
        try:
            result = subprocess.run([str(a) for a in argv], capture_output=True,
                                    text=True, timeout=timeout)
            stdout, stderr, code = result.stdout, result.stderr, result.returncode
        except subprocess.TimeoutExpired as error:
            stdout, stderr, code = error.stdout or b'', error.stderr or b'', None
            if isinstance(stdout, bytes):
                stdout = stdout.decode(errors='replace')
            if isinstance(stderr, bytes):
                stderr = stderr.decode(errors='replace')
        (root / (label + '.stdout')).write_text(stdout)
        (root / (label + '.stderr')).write_text(stderr)
        report['commands'].append(dict(label=label, exitCode=code,
                                       stdoutSHA256=digest(root / (label + '.stdout')),
                                       stderrSHA256=digest(root / (label + '.stderr'))))
        return code, stdout

    def require_run(label, argv, timeout=30):
        code, stdout = run(label, argv, timeout)
        if code != 0:
            raise RuntimeError(label + '_failed')
        return stdout

    try:
        bridge = root / 'bridge.o'
        require_run('bridge', ['cc', '-c', snapshot / 'ProcessSerialBridge.c', '-o', bridge])
        common = ['swiftc', '-import-objc-header', snapshot / 'ProcessSerialBridge.h']
        endpoint = root / 'endpoint-probe'
        require_run('endpoint-build', [*common, snapshot / 'AppleEventEndpointProbe.swift', bridge, '-o', endpoint])
        report['binaries']['endpointSHA256'] = digest(endpoint)
        report['endpoint'] = json.loads(require_run('endpoint', [endpoint]))
        binary = root / 'routing-probe'
        require_run('routing-build', [*common, snapshot / 'ProcessSerialRoutingProbe.swift', bridge, '-o', binary])
        app = root / 'Receiver.app'
        contents = app / 'Contents'
        (contents / 'MacOS').mkdir(parents=True)
        receiver = contents / 'MacOS/ProcessSerialRoutingProbe'
        shutil.copy2(binary, receiver)
        metadata = dict(CFBundleIdentifier='com.777genius.navigation-psn-test.' + str(uuid.uuid4()),
                        CFBundleExecutable='ProcessSerialRoutingProbe', CFBundlePackageType='APPL',
                        CFBundleName='Navigation PSN TEST', CFBundleVersion='1', LSUIElement=True)
        with (contents / 'Info.plist').open('wb') as stream:
            plistlib.dump(metadata, stream)
        require_run('sign', ['codesign', '--force', '--sign', '-', '--timestamp=none', app])
        shutil.copytree(app, root / 'ReceiverCopy.app')
        report['binaries']['signedReceiverSHA256'] = digest(receiver)
        code, raw = run('routing', [receiver, '--controller', root], timeout=40)
        # Preserve JSON result even when its contract assertion returns nonzero.
        if raw.strip():
            report['routing'] = json.loads(raw)
        report['passed'] = code == 0 and report.get('routing', {}).get('observersAlive') is True
    except (RuntimeError, ValueError, OSError) as error:
        report['error'] = str(error)
    finally:
        for name in ('a', 'b', 'a-restarted'):
            directory = root / name
            if directory.is_dir():
                (directory / 'stop').write_bytes(b'')
        report['snapshotUnchanged'] = all(digest(snapshot / name) == sha
                                          for name, sha in report['sourceSHA256'].items())
        report['passed'] = report['passed'] and report['snapshotUnchanged']
        evidence = root / 'evidence.json'
        evidence.write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(dict(evidence=str(evidence), result=report), indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
