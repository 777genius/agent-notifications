#!/usr/bin/python3
"""Owned TEST container: prove KVM access with a paused VM, without guest boot."""
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import struct
import subprocess
import time


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    root = Path('/evidence')
    if os.environ.get('NAVIGATION_KVM_TEST') != '1' or os.getuid() != 1000 or not Path('/.dockerenv').exists() or root.stat().st_mode & 0o777 != 0o700 or (root / 'container.marker').read_text() != 'Linux selected client KVM TEST only\n':
        raise RuntimeError('explicit_owned_restricted_KVM_TEST_required')
    if not stat.S_ISCHR(Path('/dev/kvm').stat().st_mode):
        raise RuntimeError('KVM_device_missing')
    report = dict(scope='paused_TEST_KVM_access_only', passed=False,
        guestBootAttempted=False, notificationAttempted=False, clientLaunchAttempted=False,
        clientSandboxQualified=False, navigationQualified=False, retryAllowed=False,
        sourceSHA256=sha(Path(__file__)), qemuSHA256=sha(Path('/usr/bin/qemu-system-x86_64')),
        qmpMessages=[])
    endpoint = root / 'qmp.sock'
    if endpoint.exists():
        raise RuntimeError('fresh_QMP_endpoint_required')
    child = None
    out, err = (root / 'qemu.stdout').open('xb'), (root / 'qemu.stderr').open('xb')

    def persist():
        (root / 'progress.json').write_text(json.dumps(report, indent=2))

    try:
        report['launchIntentBoot'] = now(); persist()
        child = subprocess.Popen(['/usr/bin/qemu-system-x86_64', '-machine', 'q35,accel=kvm',
            '-S', '-m', '256M', '-smp', '1', '-nodefaults', '-nographic', '-display', 'none',
            '-serial', 'none', '-monitor', 'none', '-nic', 'none', '-qmp',
            'unix:' + str(endpoint) + ',server=on,wait=off'], stdin=subprocess.DEVNULL,
            stdout=out, stderr=err, env={'PATH': '/usr/bin:/bin', 'HOME': str(root), 'LANG': 'C.UTF-8'})
        report['qemuPID'] = child.pid
        deadline = now() + 10
        while not endpoint.exists():
            if child.poll() is not None:
                raise RuntimeError('QEMU_exited_before_QMP')
            if now() >= deadline:
                raise RuntimeError('QMP_readiness_deadline')
            time.sleep(0.025)
        if not stat.S_ISSOCK(endpoint.stat().st_mode):
            raise RuntimeError('QMP_endpoint_not_socket')
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(2); connection.connect(str(endpoint))
            pid, uid, gid = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if pid != child.pid or uid != os.getuid() or child.poll() is not None:
                raise RuntimeError('owned_QMP_peer_mismatch')
            report['qmpPeer'] = dict(pid=pid, uid=uid, gid=gid)
            qmp_deadline = now() + 12
            buffer = b''

            def receive():
                nonlocal buffer
                while b'\n' not in buffer:
                    remaining = qmp_deadline - now()
                    if remaining <= 0:
                        raise RuntimeError('QMP_session_deadline')
                    connection.settimeout(min(2, remaining))
                    chunk = connection.recv(4096)
                    if not chunk:
                        raise RuntimeError('QMP_frame_incomplete')
                    buffer += chunk
                    (root / 'qmp-unconsumed.bin').write_bytes(buffer)
                    if len(buffer) > 16384 and b'\n' not in buffer:
                        raise RuntimeError('bounded_QMP_frame_invalid')
                if now() >= qmp_deadline:
                    raise RuntimeError('QMP_session_deadline')
                line = buffer.split(b'\n', 1)[0]
                if len(line) + 1 > 16384:
                    raise RuntimeError('bounded_QMP_frame_invalid')
                _, buffer = buffer.split(b'\n', 1)
                (root / 'qmp-unconsumed.bin').write_bytes(buffer)
                with (root / 'qmp-wire.jsonl').open('ab') as raw:
                    raw.write(line + b'\n')
                message = json.loads(line)
                if not isinstance(message, dict):
                    raise RuntimeError('QMP_object_required')
                report['qmpMessages'].append(message); persist()
                return message

            greeting = receive()
            if 'QMP' not in greeting:
                raise RuntimeError('QMP_greeting_missing')

            def request(command, ident):
                remaining = qmp_deadline - now()
                if remaining <= 0:
                    raise RuntimeError('QMP_session_deadline')
                connection.settimeout(min(2, remaining))
                connection.sendall((json.dumps({'execute': command, 'id': ident}) + '\n').encode())
                for _ in range(8):
                    message = receive()
                    if 'event' in message:
                        continue
                    if message.get('id') != ident or 'error' in message or 'return' not in message:
                        raise RuntimeError('QMP_addressed_reply_invalid')
                    return message['return']
                raise RuntimeError('QMP_reply_budget_exceeded')

            request('qmp_capabilities', 'capabilities')
            kvm = request('query-kvm', 'kvm')
            status = request('query-status', 'status')
            report['kvm'] = kvm; report['vmStatus'] = status
            if not isinstance(kvm, dict) or set(kvm) != {'enabled', 'present'} or kvm['enabled'] is not True or kvm['present'] is not True or not isinstance(status, dict) or status.get('running') is not False or status.get('status') not in ('prelaunch', 'paused'):
                raise RuntimeError('accelerated_paused_VM_unproved')
            request('quit', 'quit')
        child.wait(timeout=3)
        if child.returncode != 0:
            raise RuntimeError('QEMU_exit_failed')
        report['passed'] = True
    except Exception as error:
        report['failure'] = str(error)
    finally:
        if child is not None:
            if child.poll() is None:
                child.terminate()
                try: child.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    child.kill()
                    try: child.wait(timeout=2)
                    except subprocess.TimeoutExpired: report['ownedChildStopUnknown'] = True
            report['childCollected'] = child.returncode is not None
            report['childExitCode'] = child.returncode
            report['passed'] = report['passed'] and report['childCollected']
        out.close(); err.close()
        for name in ('apt-policy.txt', 'packages.txt', 'qemu-version.txt', 'qemu-img-version.txt'):
            (root / name).write_bytes((Path('/fixture-build') / name).read_bytes())
        # The outer controller keeps writing its own record after this probe exits.
        # Hash only this probe's closed outputs and immutable explicit inputs.
        owned_artifacts = ('probe.py', 'container.marker', 'qemu.stdout', 'qemu.stderr',
            'qmp-wire.jsonl', 'qmp-unconsumed.bin', 'apt-policy.txt', 'packages.txt',
            'qemu-version.txt', 'qemu-img-version.txt')
        report['artifactSHA256'] = {name: sha(root / name) for name in owned_artifacts if (root / name).is_file()}
        (root / 'native-evidence.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
