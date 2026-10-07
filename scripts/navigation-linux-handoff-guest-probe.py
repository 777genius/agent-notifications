#!/usr/bin/python3
"""Owned TEST container: assemble the frozen handoff seed and boot one offline guest."""
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import socket
import stat
import struct
import subprocess
import time

BASE_SHA = '6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2'
PROVISIONED_SHA = '839d348d47bb6f370012da63479e6d9b7493e9ce5a5fa717d4f713b449327a4e'
STAGE_SHA = '9c3863168abf95fe1dee37a91a96760332a0c07cacaebaa246c8b22b4c07c89b'
FILES = {'client-callback.py', 'client-sender.py', 'guest-pointer-entry.py', 'guest-pointer.so',
         'kernel-observer.py', 'protocol-observer.py', 'client-controller.py',
         'server-observer.so', 'server-observer.py'}
ROOT = Path('/evidence')


def sha(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def decode_guest_frame(serial_bytes, shipping=False):
    """One bounded complete hash-checked frame, even after a serial getty prompt."""
    marker = b'NAVIGATION_TEST_SHIPPING_GO_V1 ' if shipping else b'NAVIGATION_TEST_HANDOFF_V1 '
    if len(serial_bytes) > 16 * 1024 * 1024 or serial_bytes.count(marker) != 1:
        raise RuntimeError('unique_bounded_guest_completion_required')
    start = serial_bytes.index(marker)
    frame, terminator, _ = serial_bytes[start:].partition(b'\n')
    frame = frame.rstrip(b'\r')
    if not terminator or len(frame) > 128 * 1024:
        raise RuntimeError('complete_bounded_guest_frame_required')
    _, claimed, encoded = frame.split(b' ', 2)
    payload = base64.b64decode(encoded, validate=True)
    if hashlib.sha256(payload).hexdigest().encode() != claimed:
        raise RuntimeError('guest_completion_hash_mismatch')
    return payload



def qualified_guest_result(guest, sources, shipping=None):
    """A complete native chain and collected cleanup; never an exact-chat claim."""
    scope = 'offline_shipping_go_native_handoff_TEST' if shipping else 'offline_selected_client_native_handoff_TEST'
    if not isinstance(guest, dict) or guest.get('scope') != scope:
        return False
    if shipping:
        cpu = guest.get('guestCPUObservation', {})
        processors = cpu.get('processors', [])
        if guest.get('cpuProfileRequested') != 'host,vmx=off,svm=off' or cpu.get('source') != '/proc/cpuinfo' or cpu.get('beforeSelectedTreeRead') is not True or type(cpu.get('rawBytes')) is not int or not 0 < cpu['rawBytes'] <= 32768 or len(cpu.get('rawSHA256', '')) != 64 or [p.get('processor') for p in processors] != [0, 1]:
            return False
        for processor in processors:
            flags = processor.get('flags', [])
            if not isinstance(processor.get('modelName'), str) or not 0 < len(processor['modelName']) <= 512 or not isinstance(flags, list) or not 0 < len(flags) <= 256 or any(not isinstance(flag, str) or len(flag) > 64 for flag in flags) or 'vmx' in flags or 'svm' in flags:
                return False
        times = (cpu.get('beginBoot'), cpu.get('endBoot'), guest.get('firstSelectedVendorReadBeginBoot'))
        if any(type(value) not in (int, float) for value in times) or not 0 <= times[0] <= times[1] <= times[2]:
            return False
    if shipping and (guest.get('shipping') != shipping or any(guest.get(key) is not True for key in ('normalSetupObserved', 'producerRemoved', 'mutableProducerStateRemoved', 'lateClickObserved', 'coldGoReaderObserved', 'exactURIObserved'))):
        return False
    if guest.get('sourceSHA256') != sources:
        return False
    required = ('passed', 'notificationAttempted', 'clickAttempted', 'handoffQualified',
                'activationQualified', 'cleanupPassed', 'serverSurfaceFocusJoinObserved',
                'tokenForwardingQualified', 'focusQualified', 'cgroupEmptyObserved')
    if any(guest.get(key) is not True for key in required): return False
    if guest.get('navigationQualified') is not False or guest.get('humanRenderQualified') is not False:
        return False
    if guest.get('activationEvidenceClass') != 'peer_bound_selected_connection_unique_live_toplevel_and_compositor_focus':
        return False
    return guest.get('retryAllowed') is False


def main():
    if os.environ.get('NAVIGATION_GUEST_HANDOFF_TEST') != '1' or os.getuid() != 1000 or not Path('/.dockerenv').exists():
        raise RuntimeError('explicit_owned_nonroot_container_required')
    if ROOT.stat().st_mode & 0o777 != 0o700 or (ROOT / 'container.marker').read_text() != 'Linux offline guest handoff TEST only\n':
        raise RuntimeError('owned_private_root_required')
    # Explicit selected input bytes, no host paths, profiles, sockets or credentials.
    if sha(ROOT / 'base.img') != BASE_SHA or sha(ROOT / 'provisioned.qcow2') != PROVISIONED_SHA:
        raise RuntimeError('authenticated_inputs_mismatch')
    if any((ROOT / name).stat().st_mode & 0o222 for name in ('base.img', 'provisioned.qcow2')):
        raise RuntimeError('read_only_backing_chain_required')
    if sorted(path.name for path in Path('/sys/class/net').iterdir()) != ['lo']:
        raise RuntimeError('offline_outer_container_required')
    manifest = json.loads((ROOT / 'manifest.json').read_text())
    if set(manifest) != {'files', 'frontendSHA256', 'backendSHA256'} | ({'shipping'} if 'shipping' in manifest else set()) or set(manifest['files']) != FILES:
        raise RuntimeError('complete_handoff_manifest_required')
    for filename, digest in manifest['files'].items():
        if not isinstance(digest, str) or len(digest) != 64 or sha(ROOT / filename) != digest:
            raise RuntimeError('accepted_guest_source_required_before_boot')
    if 'shipping' in manifest:
        spec = importlib.util.spec_from_file_location('TEST_shipping_assets', ROOT / 'guest-bootstrap.py')
        assets = importlib.util.module_from_spec(spec); spec.loader.exec_module(assets)
        assets.shipping_inputs(ROOT, manifest)
    if sha(ROOT / 'runtime-stage.py') != STAGE_SHA:
        raise RuntimeError('accepted_runtime_stager_required')
    stage_spec = importlib.util.spec_from_file_location('TEST_runtime_stage', ROOT / 'runtime-stage.py')
    stage = importlib.util.module_from_spec(stage_spec); stage_spec.loader.exec_module(stage)
    report = dict(scope='offline_selected_client_native_handoff_TEST', passed=False,
        guestBootAttempted=False, guestClientLaunchOutcome='not_started', notificationAttempted=False,
        clickAttempted=False, nativeEffectOutcome='not_started', handoffQualified=False, activationQualified=False,
        clientSandboxQualified=False, navigationQualified=False, retryAllowed=False,
        sourceSHA256=sha(Path(__file__)), guestSourceSHA256=manifest['files'],
        bootstrapSHA256=sha(ROOT / 'guest-bootstrap.py'), manifestSHA256=sha(ROOT / 'manifest.json'),
        runtimeStageSHA256=STAGE_SHA)
    if 'shipping' in manifest:
        report.update(scope='offline_shipping_go_native_handoff_TEST', shipping=manifest['shipping'], cpuProfileRequested='host,vmx=off,svm=off')
    child = None
    connection = None

    def persist():
        (ROOT / 'native-progress.json').write_text(json.dumps(report, indent=2) + '\n')

    def command(label, argv, timeout=30):
        with (ROOT / (label + '.stdout')).open('xb') as out, (ROOT / (label + '.stderr')).open('xb') as err:
            subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                check=True, timeout=timeout, env={'PATH': '/usr/bin:/bin', 'HOME': str(ROOT), 'LANG': 'C.UTF-8'})

    try:
        overlay, seed = ROOT / 'native-overlay.qcow2', ROOT / 'native-seed.iso'
        endpoint = ROOT / 'qmp.sock'
        if overlay.exists() or seed.exists() or (ROOT / 'serial.log').exists() or endpoint.exists():
            raise RuntimeError('fresh_guest_intent_required')
        command('create_overlay', ['qemu-img', 'create', '-f', 'qcow2', '-F', 'qcow2',
            '-b', str(ROOT / 'provisioned.qcow2'), str(overlay)])
        command('overlay_info', ['qemu-img', 'info', '--output=json', str(overlay)])
        info = json.loads((ROOT / 'overlay_info.stdout').read_text())
        if info.get('format') != 'qcow2' or info.get('full-backing-filename') != str(ROOT / 'provisioned.qcow2') or info.get('backing-filename-format') != 'qcow2':
            raise RuntimeError('explicit_backing_chain_mismatch')
        command('backing_chain', ['qemu-img', 'info', '--backing-chain', '--output=json', str(overlay)])
        chain = json.loads((ROOT / 'backing_chain.stdout').read_text())
        if len(chain) != 3 or [item.get('filename') for item in chain] != [str(overlay), str(ROOT / 'provisioned.qcow2'), str(ROOT / 'base.img')]:
            raise RuntimeError('qualified_full_backing_chain_required')
        seed_dir = ROOT / 'native-seed'
        seed_dir.mkdir(mode=0o700)
        # Hard links stay inside the single owned fixture; no shared filesystem in guest.
        for filename in FILES | {'guest-bootstrap.py', 'manifest.json'}:
            os.link(ROOT / filename, seed_dir / filename)
        for filename in manifest.get('shipping', {}).get('files', {}):
            os.link(ROOT / filename, seed_dir / filename)
        catalog = stage.stage_runtime_archives(ROOT, seed_dir / 'runtime')
        (seed_dir / 'runtime-hashes.json').write_text(json.dumps(catalog, sort_keys=True) + '\n')
        (seed_dir / 'navigation.marker').write_text('Linux selected-client handoff TEST only\n')
        (seed_dir / 'meta-data').write_text('instance-id: navigation-native-' + os.urandom(16).hex() + '\nlocal-hostname: navigation-test-guest\n')
        config = {'ssh_pwauth': False, 'disable_root': True,
            'users': [],
            'runcmd': [['mkdir', '-p', '/mnt/navigation-handoff-test-seed'],
                       ['mount', '-o', 'ro', '/dev/sr0', '/mnt/navigation-handoff-test-seed'],
                       ['/usr/bin/python3', '-I', '/mnt/navigation-handoff-test-seed/guest-bootstrap.py']]}
        (seed_dir / 'user-data').write_text('#cloud-config\n' + json.dumps(config) + '\n')
        (seed_dir / 'network-config').write_text(json.dumps({'version': 2, 'ethernets': {}}) + '\n')
        command('create_seed', ['genisoimage', '-quiet', '-output', str(seed), '-volid', 'cidata',
            '-joliet', '-rock', str(seed_dir)], 90)
        report['seedSHA256'] = sha(seed)
        report['launchIntentBoot'] = time.clock_gettime(time.CLOCK_BOOTTIME); persist()
        with (ROOT / 'qemu.stdout').open('xb') as out, (ROOT / 'qemu.stderr').open('xb') as err:
            child = subprocess.Popen(['/usr/bin/qemu-system-x86_64', '-machine', 'q35,accel=kvm',
                '-cpu', 'host,vmx=off,svm=off',
                '-S', '-m', '4096M', '-smp', '2', '-nographic', '-display', 'none', '-monitor', 'none',
                '-qmp', 'unix:' + str(endpoint) + ',server=on,wait=off',
                '-serial', 'file:' + str(ROOT / 'serial.log'), '-no-reboot',
                '-drive', 'file=' + str(overlay) + ',format=qcow2,if=virtio',
                '-drive', 'file=' + str(seed) + ',format=raw,media=cdrom,readonly=on',
                '-nic', 'none'], stdin=subprocess.DEVNULL,
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
                remaining = session_deadline - time.clock_gettime(time.CLOCK_BOOTTIME)
                if remaining <= 0:
                    raise RuntimeError('QMP_deadline_before_send')
                connection.settimeout(min(0.5, remaining))
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
            status = request('query-status', 'paused-status')
            if not isinstance(status, dict) or status.get('running') is not False or status.get('status') != 'prelaunch':
                raise RuntimeError('actual_fresh_paused_guest_required')
            report['pausedStatus'] = status
            report['guestBootAttempted'] = True; report['guestClientLaunchOutcome'] = 'unknown'
            report['notificationAttempted'] = None; report['clickAttempted'] = None
            report['nativeEffectOutcome'] = 'unknown'; persist()
            request('cont', 'guest-boot')
            deadline = time.clock_gettime(time.CLOCK_BOOTTIME) + 300
            session_deadline = deadline
            while True:
                if time.clock_gettime(time.CLOCK_BOOTTIME) >= deadline:
                    raise RuntimeError('guest_native_deadline')
                # Bound this fixture's disk growth without touching other jobs.
                capacity = os.statvfs(ROOT)
                if overlay.stat().st_size > 1024**3 or capacity.f_bavail * capacity.f_frsize < 512 * 1024**2:
                    raise RuntimeError('owned_overlay_or_free_disk_budget')
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
        payload = decode_guest_frame(serial_bytes, 'shipping' in manifest)
        guest = json.loads(payload)
        report['guestResult'] = guest
        (ROOT / 'guest-result.json').write_bytes(payload)
        if isinstance(guest, dict):
            for key in ('notificationAttempted', 'clickAttempted'):
                value = guest.get(key)
                report[key] = value if isinstance(value, bool) else None
        if not qualified_guest_result(guest, manifest['files'], manifest.get('shipping')):
            raise RuntimeError('guest_native_contract_failed')
        report['guestClientLaunchOutcome'] = 'observed_once'
        report['notificationAttempted'] = True
        report['clickAttempted'] = True
        report['nativeEffectOutcome'] = 'observed_once'
        report['handoffQualified'] = True
        report['activationQualified'] = True
        report['passed'] = True
    except Exception as error:
        report['failure'] = type(error).__name__ + ': ' + str(error)
    finally:
        errors = []
        report['passedBeforeCleanup'] = report['passed']
        try: persist()
        except Exception as error: errors.append('progress: ' + str(error))
        if connection is not None:
            try: connection.close()
            except Exception as error: errors.append('QMP_close: ' + str(error))
        if child is not None:
            try:
                if child.poll() is None:
                    child.terminate()
                    try: child.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        child.kill(); child.wait(timeout=3)
                report['childCollected'] = child.returncode is not None
                report['childExitCode'] = child.returncode
                report['passed'] = report['passed'] and report['childCollected'] and child.returncode == 0
            except Exception as error:
                errors.append('QEMU_collection: ' + str(error)); report['childCollected'] = False
        checks = [('base.img', BASE_SHA, 'base'),
                  ('provisioned.qcow2', PROVISIONED_SHA, 'provisioned'),
                  ('manifest.json', report['manifestSHA256'], 'manifest'),
                  ('guest-bootstrap.py', report['bootstrapSHA256'], 'bootstrap'),
                  ('runtime-stage.py', STAGE_SHA, 'runtimeStage'),
                  ('portal.tar', stage.QUALIFIED['portal'][0], 'portalArchive'),
                  ('gtk.tar', stage.QUALIFIED['gtk'][0], 'gtkArchive'),
                  ('native-seed.iso', report.get('seedSHA256'), 'seed')]
        checks += [(name, digest, name) for name, digest in manifest['files'].items()]
        checks += [(name, digest, name) for name, digest in manifest.get('shipping', {}).get('files', {}).items()]
        for name, expected, label in checks:
            try:
                actual = sha(ROOT / name)
                report[label + 'SHA256After'] = actual
                report[label + 'Unchanged'] = actual == expected and expected is not None
                if not report[label + 'Unchanged']: errors.append(label + '_changed')
            except Exception as error:
                report[label + 'Unchanged'] = False; errors.append(label + ': ' + str(error))
        try:
            report['sourceSHA256After'] = sha(Path(__file__))
            report['sourceUnchanged'] = report['sourceSHA256After'] == report['sourceSHA256']
            if not report['sourceUnchanged']: errors.append('source_changed')
        except Exception as error:
            report['sourceUnchanged'] = False; errors.append('source: ' + str(error))
        report['cleanupErrors'] = errors
        report['passed'] = report['passed'] and not errors
        report['handoffQualified'] = report['passed']
        report['activationQualified'] = report['passed']
        (ROOT / 'native-evidence.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
