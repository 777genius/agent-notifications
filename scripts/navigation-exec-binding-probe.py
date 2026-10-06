#!/usr/bin/env python3
"""One reviewed disposable same-PID exec TEST; no production application effects."""
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
    if platform.system() != 'Darwin' or not args.execute_test_fixtures:
        parser.error('macOS and explicit TEST opt-in required')
    repo = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix='navigation-exec-test-')).resolve()
    os.chmod(root, 0o700)
    (root / 'fixture.marker').write_text('same PID exec TEST only\n')
    source = root / 'source'
    source.mkdir(mode=0o700)
    for name in ('ProcessSerialBridge.h', 'ProcessSerialBridge.c', 'ProcessSerialExecProbe.swift'):
        (source / name).write_bytes((repo / 'swift-notifier/Tests/Probes' / name).read_bytes())
    (source / 'ContinuousClock.swift').write_bytes((repo / 'swift-notifier/Sources/terminal-notifier-modern/Protocol/ContinuousClock.swift').read_bytes())
    for name in ('child.stdout', 'child.stderr'):
        (root / name).write_bytes(b'')
    report = dict(schemaVersion=1, macOS=platform.mac_ver()[0], architecture=platform.machine(),
                  sourceSHA256={p.name: sha(p) for p in source.iterdir()}, runnerSHA256=sha(Path(__file__)),
                  commands=[], binarySHA256={}, passed=False, productionClientActivated=False,
                  limitations=['Ad-hoc same-PID exec fixture only; no Developer ID/Codex qualification',
                               'Public checks are observations, not an atomic production binding',
                               'Bridge suppresses consent prompts; no retry or fallback',
                               'No visible-application acknowledgement; receiver records exact TEST event'])

    def run(label, argv, timeout=30):
        try:
            value = subprocess.run([str(a) for a in argv], capture_output=True, timeout=timeout)
            code, stdout, stderr = value.returncode, value.stdout, value.stderr
        except subprocess.TimeoutExpired as error:
            code, stdout, stderr = None, error.stdout or b'', error.stderr or b''
        for suffix, content in [('stdout', stdout), ('stderr', stderr)]:
            (root / (label + '.' + suffix)).write_bytes(content)
        report['commands'].append(dict(label=label, argv=[str(a).replace(str(root), '$TEST_ROOT') for a in argv],
                                       exitCode=code, stdoutSHA256=sha(root / (label + '.stdout')),
                                       stderrSHA256=sha(root / (label + '.stderr'))))
        if code != 0 and label != 'experiment':
            raise RuntimeError(label + '_failed')
        return code, stdout

    try:
        bridge, binary = root / 'bridge.o', root / 'probe'
        run('bridge-build', ['cc', '-c', source / 'ProcessSerialBridge.c', '-o', bridge])
        run('probe-build', ['swiftc', '-import-objc-header', source / 'ProcessSerialBridge.h',
                            source / 'ProcessSerialExecProbe.swift', source / 'ContinuousClock.swift', bridge, '-o', binary])
        report['binarySHA256']['controller'] = sha(binary)
        for phase in ('A', 'B'):
            app = root / (phase + '.app')
            (app / 'Contents/MacOS').mkdir(parents=True)
            executable = app / 'Contents/MacOS/Receiver'
            shutil.copy2(binary, executable)
            metadata = dict(CFBundleIdentifier='com.777genius.navigation-psn-test.exec-' + phase.lower() + '.' + str(uuid.uuid4()),
                            CFBundleExecutable='Receiver', CFBundlePackageType='APPL', LSUIElement=True,
                            CFBundleName='Exec TEST ' + phase, CFBundleVersion='1')
            with (app / 'Contents/Info.plist').open('wb') as stream:
                plistlib.dump(metadata, stream)
            run(phase + '-sign', ['codesign', '--force', '--sign', '-', '--timestamp=none', app])
            report['binarySHA256'][phase] = sha(executable)
        code, raw = run('experiment', [binary, '--controller', root], timeout=40)
        if raw.strip():
            report['experiment'] = json.loads(raw)
        report['passed'] = code == 0 and report.get('experiment', {}).get('passed') is True
        if code is None:
            report['failure'] = 'controller_timeout_effect_unknown_no_retry'
    except (OSError, ValueError, RuntimeError) as error:
        report['failure'] = str(error)
    finally:
        (root / 'stop').write_bytes(b'')  # Only this root's own self-expiring receiver observes it.
        report['snapshotUnchanged'] = all(sha(source / name) == digest for name, digest in report['sourceSHA256'].items())
        report['passed'] = report['passed'] and report['snapshotUnchanged']
        report['childOutputSHA256'] = {name: sha(root / name) for name in ('child.stdout', 'child.stderr')}
        evidence = root / 'evidence.json'
        evidence.write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(dict(evidence=str(evidence), result=report), indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
