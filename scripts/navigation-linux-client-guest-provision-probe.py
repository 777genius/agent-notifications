#!/usr/bin/python3
"""Owned Docker fixture: boot one disposable guest to install TEST prerequisites."""
import base64
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import struct
import subprocess
import time

BASE_SHA = '6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2'
PACKAGE_SHA = '637c3c94bc50f8ee33a15e2e28ec7f92a787f0943e700efe111bc0bf0d4813b4'
ROOT = Path('/evidence')


def sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def main():
    if os.environ.get('NAVIGATION_GUEST_PROVISION_TEST') != '1' or os.getuid() != 1000 or not Path('/.dockerenv').exists():
        raise RuntimeError('explicit_owned_nonroot_container_required')
    if ROOT.stat().st_mode & 0o777 != 0o700 or (ROOT / 'container.marker').read_text() != 'Linux guest provisioning TEST only\n':
        raise RuntimeError('owned_private_root_required')
    # Explicit selected input bytes, no host paths, profiles, sockets or credentials.
    if sha(ROOT / 'base.img') != BASE_SHA or sha(ROOT / 'chatgpt.deb') != PACKAGE_SHA:
        raise RuntimeError('authenticated_inputs_mismatch')
    if (ROOT / 'base.img').stat().st_mode & 0o222:
        raise RuntimeError('read_only_base_required')
    report = dict(scope='disposable_guest_provisioning_only', passed=False,
        guestBootAttempted=False, clientLaunchAttempted=False, notificationAttempted=False,
        clientSandboxQualified=False, navigationQualified=False, retryAllowed=False,
        sourceSHA256=sha(Path(__file__)), guestSourceSHA256=sha(ROOT / 'guest-provision.py'))
    child = None
    connection = None

    def persist():
        (ROOT / 'provision-progress.json').write_text(json.dumps(report, indent=2) + '\n')

    def command(label, argv, timeout=30):
        with (ROOT / (label + '.stdout')).open('xb') as out, (ROOT / (label + '.stderr')).open('xb') as err:
            subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                check=True, timeout=timeout, env={'PATH': '/usr/bin:/bin', 'HOME': str(ROOT), 'LANG': 'C.UTF-8'})

    try:
        overlay, seed = ROOT / 'overlay.qcow2', ROOT / 'seed.iso'
        endpoint = ROOT / 'qmp.sock'
        if overlay.exists() or seed.exists() or (ROOT / 'serial.log').exists() or endpoint.exists():
            raise RuntimeError('fresh_guest_intent_required')
        command('create_overlay', ['qemu-img', 'create', '-f', 'qcow2', '-F', 'qcow2',
            '-b', str(ROOT / 'base.img'), str(overlay), '20G'])
        command('overlay_info', ['qemu-img', 'info', '--output=json', str(overlay)])
        info = json.loads((ROOT / 'overlay_info.stdout').read_text())
        if info.get('format') != 'qcow2' or info.get('full-backing-filename') != str(ROOT / 'base.img') or info.get('backing-filename-format') != 'qcow2':
            raise RuntimeError('explicit_backing_chain_mismatch')
        seed_dir = ROOT / 'seed'
        seed_dir.mkdir(mode=0o700)
        # Hard links stay inside the single owned fixture; no shared filesystem in guest.
        os.link(ROOT / 'chatgpt.deb', seed_dir / 'chatgpt.deb')
        os.link(ROOT / 'guest-provision.py', seed_dir / 'guest-provision.py')
        (seed_dir / 'navigation.marker').write_text('Linux client provisioning TEST only\n')
        (seed_dir / 'meta-data').write_text('instance-id: navigation-provision-' + os.urandom(16).hex() + '\nlocal-hostname: navigation-test-guest\n')
        config = {'ssh_pwauth': False, 'disable_root': True,
            'users': [{'name': 'navigationtest', 'uid': 1000, 'lock_passwd': True, 'shell': '/bin/bash'}],
            'runcmd': [['mkdir', '-p', '/mnt/navigation-test-seed'],
                       ['mount', '-o', 'ro', '/dev/sr0', '/mnt/navigation-test-seed'],
                       ['/usr/bin/python3', '-I', '/mnt/navigation-test-seed/guest-provision.py']]}
        (seed_dir / 'user-data').write_text('#cloud-config\n' + json.dumps(config) + '\n')
        command('create_seed', ['genisoimage', '-quiet', '-output', str(seed), '-volid', 'cidata',
            '-joliet', '-rock', str(seed_dir)], 90)
        report['seedSHA256'] = sha(seed)
        report['launchIntentBoot'] = time.clock_gettime(time.CLOCK_BOOTTIME); persist()
        with (ROOT / 'qemu.stdout').open('xb') as out, (ROOT / 'qemu.stderr').open('xb') as err:
            child = subprocess.Popen(['/usr/bin/qemu-system-x86_64', '-machine', 'q35,accel=kvm',
                '-S', '-m', '4096M', '-smp', '2', '-nographic', '-display', 'none', '-monitor', 'none',
                '-qmp', 'unix:' + str(endpoint) + ',server=on,wait=off',
                '-serial', 'file:' + str(ROOT / 'serial.log'), '-no-reboot',
                '-drive', 'file=' + str(overlay) + ',format=qcow2,if=virtio',
                '-drive', 'file=' + str(seed) + ',format=raw,media=cdrom,readonly=on',
                '-nic', 'user,model=virtio-net-pci'], stdin=subprocess.DEVNULL,
                stdout=out, stderr=err, env={'PATH': '/usr/bin:/bin', 'HOME': str(ROOT), 'LANG': 'C.UTF-8'})
            report['qemuPID'] = child.pid; persist()
            readiness_deadline = time.clock_gettime(time.CLOCK_BOOTTIME) + 10
            while not endpoint.exists():
                if child.poll() is not None or time.clock_gettime(time.CLOCK_BOOTTIME) >= readiness_deadline:
                    raise RuntimeError('owned_QMP_readiness_failed')
                time.sleep(0.025)
            if not stat.S_ISSOCK(endpoint.stat().st_mode):
                raise RuntimeError('QMP_socket_required')
            connection = socket.socket(socket.AF_UNIX)
            connection.settimeout(2); connection.connect(str(endpoint))
            peer = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if peer[0] != child.pid or peer[1] != os.getuid() or child.poll() is not None:
                raise RuntimeError('owned_QMP_peer_required')
            report['qmpPeer'] = dict(pid=peer[0], uid=peer[1], gid=peer[2])
            buffer = b''
            messages = []
            session_deadline = time.clock_gettime(time.CLOCK_BOOTTIME) + 12

            def receive(retry_timeouts=False):
                nonlocal buffer
                while b'\n' not in buffer:
                    remaining = session_deadline - time.clock_gettime(time.CLOCK_BOOTTIME)
                    if remaining <= 0:
                        raise RuntimeError('QMP_session_deadline')
                    connection.settimeout(min(0.5, remaining))
                    try:
                        chunk = connection.recv(4096)
                    except socket.timeout:
                        if retry_timeouts:
                            continue
                        raise
                    if not chunk:
                        if buffer:
                            raise RuntimeError('QMP_stream_tail_incomplete')
                        return None
                    buffer += chunk
                    (ROOT / 'qmp-unconsumed.bin').write_bytes(buffer)
                    if len(buffer) > 16384 and b'\n' not in buffer:
                        raise RuntimeError('QMP_frame_budget')
                line, buffer = buffer.split(b'\n', 1)
                if len(line) > 16384 or time.clock_gettime(time.CLOCK_BOOTTIME) >= session_deadline:
                    raise RuntimeError('QMP_frame_or_time_budget')
                (ROOT / 'qmp-unconsumed.bin').write_bytes(buffer)
                with (ROOT / 'qmp-wire.jsonl').open('ab') as wire:
                    wire.write(line + b'\n')
                item = json.loads(line)
                if not isinstance(item, dict) or len(messages) >= 128:
                    raise RuntimeError('QMP_message_budget')
                messages.append(item)
                return item

            def request(command, ident):
                connection.sendall((json.dumps({'execute': command, 'id': ident}) + '\n').encode())
                for _ in range(8):
                    item = receive(retry_timeouts=True)
                    if item is None:
                        raise RuntimeError('QMP_response_missing')
                    if 'event' in item:
                        continue
                    if item.get('id') != ident or 'error' in item or 'return' not in item:
                        raise RuntimeError('QMP_addressed_response_required')
                    return item['return']
                raise RuntimeError('QMP_response_budget')

            greeting = receive(retry_timeouts=True)
            if not isinstance(greeting, dict) or 'QMP' not in greeting:
                raise RuntimeError('QMP_greeting_required')
            request('qmp_capabilities', 'caps')
            kvm = request('query-kvm', 'kvm')
            if not isinstance(kvm, dict) or kvm.get('enabled') is not True or kvm.get('present') is not True:
                raise RuntimeError('actual_KVM_required')
            report['kvm'] = kvm
            report['guestBootAttempted'] = True; persist()
            request('cont', 'guest-boot')
            deadline = time.clock_gettime(time.CLOCK_BOOTTIME) + 1800
            session_deadline = deadline
            while True:
                if time.clock_gettime(time.CLOCK_BOOTTIME) >= deadline:
                    raise RuntimeError('guest_provisioning_deadline')
                if (ROOT / 'serial.log').exists() and (ROOT / 'serial.log').stat().st_size > 16 * 1024 * 1024:
                    raise RuntimeError('serial_evidence_budget_exceeded')
                try:
                    item = receive()
                    if item is None:
                        break
                except socket.timeout:
                    if child.poll() is not None:
                        raise RuntimeError('QMP_shutdown_stream_incomplete')
            report['qmpMessages'] = messages
            if child.wait(timeout=2) != 0:
                raise RuntimeError('QEMU_collected_exit_failed')
            shutdown = [item for item in messages if item.get('event') == 'SHUTDOWN']
            if len(shutdown) != 1 or not isinstance(shutdown[0].get('data'), dict) or shutdown[0]['data'].get('guest') is not True or shutdown[0]['data'].get('reason') != 'guest-shutdown':
                raise RuntimeError('actual_guest_poweroff_required')
            report['guestPoweroffObserved'] = True
        if (ROOT / 'serial.log').stat().st_size > 16 * 1024 * 1024:
            raise RuntimeError('final_serial_evidence_budget_exceeded')
        with (ROOT / 'serial.log').open('rb') as serial:
            serial_bytes = serial.read(16 * 1024 * 1024 + 1)
        if len(serial_bytes) > 16 * 1024 * 1024:
            raise RuntimeError('serial_read_budget_exceeded')
        frames = [line for line in serial_bytes.splitlines()
                  if line.startswith(b'NAVIGATION_TEST_PROVISION_V1 ')]
        if len(frames) != 1 or len(frames[0]) > 128 * 1024:
            raise RuntimeError('unique_bounded_guest_completion_required')
        _, claimed, encoded = frames[0].split(b' ', 2)
        payload = base64.b64decode(encoded, validate=True)
        if hashlib.sha256(payload).hexdigest().encode() != claimed:
            raise RuntimeError('guest_completion_hash_mismatch')
        guest = json.loads(payload)
        report['guestResult'] = guest
        if not isinstance(guest, dict) or guest.get('passed') is not True or guest.get('scope') != report['scope'] or guest.get('sourceSHA256') != report['guestSourceSHA256'] or guest.get('clientLaunchAttempted') is not False or guest.get('notificationAttempted') is not False or guest.get('guestSyncSucceeded') is not True:
            raise RuntimeError('guest_provisioning_contract_failed')
        (ROOT / 'guest-result.json').write_bytes(payload)
        report['passed'] = True
    except Exception as error:
        report['failure'] = type(error).__name__ + ': ' + str(error)
    finally:
        if connection is not None:
            connection.close()
        if child is not None:
            if child.poll() is None:
                child.terminate()
                try: child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
                    try: child.wait(timeout=3)
                    except subprocess.TimeoutExpired: report['ownedChildStopUnknown'] = True
            report['childCollected'] = child.returncode is not None
            report['childExitCode'] = child.returncode
            report['passed'] = report['passed'] and report['childCollected'] and child.returncode == 0
        report['baseSHA256After'] = sha(ROOT / 'base.img')
        report['baseUnchanged'] = report['baseSHA256After'] == BASE_SHA
        report['sourceSHA256After'] = sha(Path(__file__))
        report['guestSourceSHA256After'] = sha(ROOT / 'guest-provision.py')
        report['packageSHA256After'] = sha(ROOT / 'chatgpt.deb')
        report['sourceUnchanged'] = report['sourceSHA256After'] == report['sourceSHA256']
        report['guestSourceUnchanged'] = report['guestSourceSHA256After'] == report['guestSourceSHA256']
        report['packageUnchanged'] = report['packageSHA256After'] == PACKAGE_SHA
        report['seedUnchanged'] = seed.exists() and sha(seed) == report.get('seedSHA256')
        report['passed'] = report['passed'] and all(report[key] for key in
            ('baseUnchanged', 'sourceUnchanged', 'guestSourceUnchanged', 'packageUnchanged', 'seedUnchanged'))
        (ROOT / 'provision-evidence.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
