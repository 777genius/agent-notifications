#!/usr/bin/env python3
"""One disposable native resource-envelope experiment; no production activation."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import tempfile
import uuid


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute-test-fixtures', action='store_true', required=True)
    args = parser.parse_args()
    if not args.execute_test_fixtures or platform.system() != 'Darwin':
        parser.error('macOS and explicit TEST fixture opt-in required')
    repo = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix='navigation-psn-test-')).resolve()
    os.chmod(root, 0o700)
    (root / 'fixture.marker').write_text('synthetic PSN routing only\n')
    snapshot = root / 'source'
    snapshot.mkdir(mode=0o700)
    for name in ('ProcessSerialBridge.h', 'ProcessSerialBridge.c',
                 'ProcessSerialRoutingProbe.swift', 'ResourceEnvelopeValidationProbe.swift'):
        (snapshot / name).write_bytes((repo / 'swift-notifier/Tests/Probes' / name).read_bytes())
    report = dict(schemaVersion=1, macOS=platform.mac_ver()[0], architecture=platform.machine(),
                  sourceSHA256={p.name: sha(p) for p in snapshot.iterdir()},
                  runnerSHA256=sha(Path(__file__)), commands=[], binarySHA256={}, passed=False,
                  productionClientActivated=False, fastPathQualified=False,
                  passedScope='resource_controls_and_cleanup',
                  limitations=['Ad-hoc fixture only; no Developer ID/Codex qualification',
                               'One architecture; no universal-binary inactive-slice control',
                               'Executable mutation is observational; kernel can kill its child',
                               'No proof of secure guest-to-filesystem binding or concurrent-update safety'])

    def run(label, argv, timeout=30):
        try:
            value = subprocess.run([str(a) for a in argv], capture_output=True, text=True, timeout=timeout)
            code, stdout, stderr = value.returncode, value.stdout, value.stderr
        except subprocess.TimeoutExpired as error:
            code, stdout, stderr = None, error.stdout or b'', error.stderr or b''
            stdout = stdout.decode(errors='replace') if isinstance(stdout, bytes) else stdout
            stderr = stderr.decode(errors='replace') if isinstance(stderr, bytes) else stderr
        for suffix, text in [('stdout', stdout), ('stderr', stderr)]:
            (root / (label + '.' + suffix)).write_text(text)
        report['commands'].append(dict(label=label, argv=[str(a) for a in argv], exitCode=code,
                                       stdoutSHA256=sha(root / (label + '.stdout')),
                                       stderrSHA256=sha(root / (label + '.stderr'))))
        if code != 0 and label != 'experiment':
            raise RuntimeError(label + '_failed')
        return code, stdout

    try:
        bridge, receiver, probe = root / 'bridge.o', root / 'receiver', root / 'envelope-probe'
        run('bridge', ['cc', '-c', snapshot / 'ProcessSerialBridge.c', '-o', bridge])
        run('receiver-build', ['swiftc', '-import-objc-header', snapshot / 'ProcessSerialBridge.h',
                               snapshot / 'ProcessSerialRoutingProbe.swift', bridge, '-o', receiver])
        run('probe-build', ['swiftc', snapshot / 'ResourceEnvelopeValidationProbe.swift', '-o', probe])
        report['binarySHA256']['probe'] = sha(probe)
        for phase in ('untouched', 'resource', 'envelope', 'executable'):
            app = root / (phase + '.app')
            contents = app / 'Contents'
            (contents / 'MacOS').mkdir(parents=True)
            (contents / 'Resources').mkdir()
            binary = contents / 'MacOS/ProcessSerialRoutingProbe'
            shutil.copy2(receiver, binary)
            metadata = dict(CFBundleIdentifier='com.777genius.navigation-psn-test.' + str(uuid.uuid4()),
                            CFBundleExecutable='ProcessSerialRoutingProbe', CFBundlePackageType='APPL',
                            CFBundleName='Envelope TEST', CFBundleVersion='1', LSUIElement=True)
            with (contents / 'Info.plist').open('wb') as stream:
                plistlib.dump(metadata, stream)
            (contents / 'Resources/validation-resource.txt').write_text('signed TEST resource\n')
            run(phase + '-sign', ['codesign', '--force', '--sign', '-', '--timestamp=none', app])
            report['binarySHA256'][phase] = sha(binary)
        code, raw = run('experiment', [probe, root], timeout=55)
        if raw.strip():
            report['experiment'] = json.loads(raw)
        report['passed'] = code == 0 and report.get('experiment', {}).get('passed') is True
    except (OSError, RuntimeError, ValueError) as error:
        report['error'] = str(error)
    finally:
        for phase in ('untouched', 'resource', 'envelope', 'executable'):
            directory = root / ('envelope-child-' + phase)
            if directory.is_dir():
                (directory / 'stop').write_bytes(b'')
        report['snapshotUnchanged'] = all(sha(snapshot / name) == digest for name, digest in report['sourceSHA256'].items())
        report['passed'] = report['passed'] and report['snapshotUnchanged']
        evidence = root / 'evidence.json'
        evidence.write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(dict(evidence=str(evidence), result=report), indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
