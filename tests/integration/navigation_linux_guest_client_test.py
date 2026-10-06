#!/usr/bin/python3
"""One offline disposable-guest client sandbox preflight, without URI or notification."""
import base64
import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
import socket
import stat
import struct
import subprocess
import time
import uuid
from urllib.parse import unquote

SEED = Path('/mnt/navigation-native-test-seed')
ROOT = Path('/var/lib/navigation-native-client-TEST')
EXE = Path('/usr/lib/chatgpt/ChatGPT')
EXE_SHA = '207c4fbff7e2fcc1b0789448351ac6eed206206d94c5a0835e5f07c7cd73d6e3'
LAUNCHER = Path('/usr/lib/chatgpt/codex-launcher')
LAUNCHER_SHA = '8f983245c6c07070e2cdc480be50ec239e0f18ee36069126649d0692595c86ad'
PROFILE_SHA = '05be1a8336a80236f4b56798ea7a75accb55f51438afa1e7cb5e94bfa326b1b7'


def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1048576), b''):
            h.update(chunk)
    return h.hexdigest()


def snapshot(pid):
    p = Path('/proc') / str(pid)
    fields = (p / 'stat').read_text().rsplit(')', 1)[1].split()
    status = {}
    for row in (p / 'status').read_text().splitlines():
        key, value = row.split(':', 1)
        if key in ('Uid', 'Gid', 'Seccomp', 'Seccomp_filters', 'NoNewPrivs', 'CapEff', 'NSpid'):
            status[key] = value.strip()
    executable_stat = (p / 'exe').stat()
    return dict(pid=pid, parent=int(fields[1]), startTicks=fields[19],
        executableDevice=executable_stat.st_dev, executableInode=str(executable_stat.st_ino),
        executable=os.readlink(p / 'exe'), command=(p / 'cmdline').read_bytes().split(b'\0')[:-1],
        appArmor=(p / 'attr/current').read_text().strip(), status=status,
        namespaces={key: os.readlink(p / 'ns' / key) for key in ('user', 'pid', 'mnt', 'net')},
        uidMap=(p / 'uid_map').read_text(), gidMap=(p / 'gid_map').read_text())


def public_snapshot(item):
    return dict(item, command=[part.decode('utf-8', errors='replace') for part in item['command']])


def main():
    if os.getuid() != 0 or Path('/.dockerenv').exists():
        raise RuntimeError('guest_root_only')
    mounts = [row.split() for row in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in mounts):
        raise RuntimeError('readonly_native_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux offline client preflight TEST only\n':
        raise RuntimeError('native_TEST_marker_required')
    if sorted(p.name for p in Path('/sys/class/net').iterdir()) != ['lo']:
        raise RuntimeError('offline_guest_no_NIC_required')
    if ROOT.exists(): raise RuntimeError('fresh_native_TEST_root_required')
    work = ROOT / 'session'
    report = dict(scope='offline_selected_client_renderer_preflight', passed=False,
        notificationAttempted=False, navigationQualified=False, focusQualified=False,
        clientSandboxQualified=False, observedRendererRestrictionsQualified=False,
        renderedClientQualified=False, clientLaunchAttempted=False, retryAllowed=False,
        sourceSHA256=sha(Path(__file__)), trackedProcesses=[])
    children, handles = [], {}
    client_descendants = set()
    group = Path('/sys/fs/cgroup') / ('navigation-native-TEST-' + uuid.uuid4().hex)
    group_created = False

    def persist():
        (ROOT / 'progress.json').write_text(json.dumps(report, indent=2) + '\n')

    def drop():
        (group / 'cgroup.procs').write_text(str(os.getpid()))
        os.setgroups([]); os.setgid(1000); os.setuid(1000)
        os.umask(0o077)

    env = {'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'HOME': str(work / 'home'),
        'XDG_CONFIG_HOME': str(work / 'config'), 'XDG_DATA_HOME': str(work / 'data'),
        'XDG_CACHE_HOME': str(work / 'cache'), 'XDG_RUNTIME_DIR': str(work / 'runtime'),
        'WLR_BACKENDS': 'headless', 'WLR_LIBINPUT_NO_DEVICES': '1', 'WLR_RENDERER': 'pixman',
        'XDG_SESSION_TYPE': 'wayland', 'XDG_CURRENT_DESKTOP': 'sway'}

    def retain(pid, required_parent=None):
        before = snapshot(pid)
        if required_parent is not None and before['parent'] != required_parent:
            raise RuntimeError('process_parent_changed')
        fd = os.pidfd_open(pid, 0)
        try:
            after = snapshot(pid)
            if before['startTicks'] != after['startTicks']:
                raise RuntimeError('process_incarnation_changed')
            if after['status']['Uid'].split() != ['1000'] * 4:
                raise RuntimeError('unexpected_TEST_process_uid')
            handles[pid] = (fd, after)
        except Exception:
            os.close(fd); raise
        return after

    def start(label, argv, pipe=False):
        out = (ROOT / (label + '.stdout')).open('xb')
        err = (ROOT / (label + '.stderr')).open('xb')
        try:
            child = subprocess.Popen(argv, stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE if pipe else out, stderr=err, env=env,
                cwd=work, preexec_fn=drop, start_new_session=True)
        finally:
            out.close(); err.close()
        children.append(child)
        retain(child.pid)
        return child

    def capture_descendants(main_pid):
        # Read-only /proc discovery; ancestry must lead to an owned live incarnation.
        pending = {main_pid} | client_descendants
        for _ in range(8):
            added = set()
            for p in Path('/proc').iterdir():
                if not p.name.isdecimal() or int(p.name) in handles:
                    continue
                try:
                    fields = (p / 'stat').read_text().rsplit(')', 1)[1].split()
                    parent = int(fields[1])
                    if parent not in pending or parent not in handles:
                        continue
                    parent_fd, saved = handles[parent]
                    with selectors.DefaultSelector() as selector:
                        selector.register(parent_fd, selectors.EVENT_READ)
                        if selector.select(0): continue
                    if snapshot(parent)['startTicks'] != saved['startTicks']:
                        continue
                    retain(int(p.name), parent); added.add(int(p.name))
                except (FileNotFoundError, ProcessLookupError):
                    continue
            if not added: break
            pending |= added
            client_descendants.update(added)

    try:
        ROOT.mkdir(mode=0o700, exist_ok=False)
        os.chown(ROOT, 0, 1000); os.chmod(ROOT, 0o710)
        work.mkdir(mode=0o700); os.chown(work, 1000, 1000)
        if not Path('/sys/fs/cgroup/cgroup.controllers').is_file():
            raise RuntimeError('guest_cgroup_v2_required')
        group.mkdir(mode=0o700); group_created = True
        if not (group / 'cgroup.kill').is_file():
            raise RuntimeError('guest_exact_cgroup_kill_required')
        report['ownedCgroup'] = str(group)
        for key in ('HOME', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME', 'XDG_RUNTIME_DIR'):
            path = Path(env[key]); path.mkdir(mode=0o700); os.chown(path, 1000, 1000)
        if sha(EXE) != EXE_SHA or sha(LAUNCHER) != LAUNCHER_SHA or sha(Path('/etc/apparmor.d/chatgpt')) != PROFILE_SHA:
            raise RuntimeError('selected_installation_bytes_mismatch')
        if Path('/etc/apparmor.d/local/chatgpt').exists():
            raise RuntimeError('unexpected_vendor_profile_override')
        profiles = Path('/sys/kernel/security/apparmor/profiles').read_text()
        if not any(row.split(' (', 1)[0] == 'chatgpt' for row in profiles.splitlines()):
            raise RuntimeError('vendor_profile_not_loaded')
        report['guestBaseline'] = public_snapshot(snapshot(1))
        report['selectedExecutableSHA256'] = sha(EXE)
        report['vendorProfileSHA256'] = PROFILE_SHA
        config = work / 'sway.conf'
        config.write_text('xwayland disable\nseat seat0 fallback true\noutput * resolution 1280x720\n')
        os.chown(config, 1000, 1000)
        sway = start('sway', ['/usr/bin/sway', '-c', str(config), '-d'])
        deadline = time.monotonic() + 12
        sockets = []
        while time.monotonic() < deadline:
            sockets = [p for p in Path(env['XDG_RUNTIME_DIR']).glob('wayland-*') if stat.S_ISSOCK(p.stat().st_mode)]
            if sockets: break
            if sway.poll() is not None: raise RuntimeError('compositor_exited')
            time.sleep(0.05)
        if len(sockets) != 1: raise RuntimeError('unique_compositor_socket_required')
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(max(0.01, deadline - time.monotonic()))
            connection.connect(str(sockets[0]))
            peer = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if peer[:2] != (sway.pid, 1000): raise RuntimeError('owned_compositor_peer_required')
        env['WAYLAND_DISPLAY'] = sockets[0].name
        bus = start('bus', ['/usr/bin/dbus-daemon', '--session', '--nofork', '--print-address=1'], True)
        with selectors.DefaultSelector() as selector:
            selector.register(bus.stdout, selectors.EVENT_READ)
            if not selector.select(5): raise RuntimeError('private_bus_readiness_expired')
            address = os.read(bus.stdout.fileno(), 4096).decode().strip()
        if not address.startswith('unix:path=') or '\n' in address:
            raise RuntimeError('private_bus_address_required')
        bus_path = unquote(address.split(',', 1)[0][len('unix:path='):])
        if not Path(bus_path).is_absolute(): raise RuntimeError('absolute_private_bus_socket_required')
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(2); connection.connect(bus_path)
            peer = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if peer[:2] != (bus.pid, 1000) or bus.poll() is not None:
            raise RuntimeError('owned_private_bus_peer_required')
        report['privateBusPeer'] = dict(pid=peer[0], uid=peer[1])
        env['DBUS_SESSION_BUS_ADDRESS'] = address
        report['clientLaunchAttempted'] = True; persist()
        client = start('client', [str(LAUNCHER), '--ozone-platform=wayland'])
        deadline = time.monotonic() + 30
        renderers = []
        while time.monotonic() < deadline:
            if client.poll() is not None: raise RuntimeError('selected_client_exited')
            capture_descendants(client.pid)
            main_state = snapshot(client.pid)
            renderers = []
            if main_state['executable'] == str(EXE):
                for pid, (_, saved) in list(handles.items()):
                    try:
                        current = snapshot(pid)
                        if current['startTicks'] == saved['startTicks'] and b'--type=renderer' in current['command']:
                            renderers.append(current)
                    except (FileNotFoundError, ProcessLookupError): pass
                if renderers: break
            time.sleep(0.1)
        if not renderers: raise RuntimeError('actual_renderer_evidence_absent')
        if main_state['appArmor'].split(' (', 1)[0] != 'chatgpt':
            raise RuntimeError('main_vendor_profile_not_attached')
        time.sleep(0.1)
        repeated = []
        for item in renderers:
            fresh = snapshot(item['pid'])
            if any(fresh[key] != item[key] for key in ('startTicks', 'executable', 'command', 'namespaces', 'uidMap', 'status')):
                raise RuntimeError('renderer_snapshot_not_consistent')
            repeated.append(fresh)
        report['rendererRepeatedSnapshots'] = [public_snapshot(item) for item in repeated]
        for process in (sway, bus, client):
            fd, saved = handles[process.pid]
            with selectors.DefaultSelector() as selector:
                selector.register(fd, selectors.EVENT_READ)
                if selector.select(0) or process.poll() is not None:
                    raise RuntimeError('required_owned_process_exited')
            if snapshot(process.pid)['startTicks'] != saved['startTicks']:
                raise RuntimeError('required_owned_incarnation_changed')
        final_main = snapshot(client.pid)
        if final_main['executable'] != str(EXE) or final_main['appArmor'].split(' (', 1)[0] != 'chatgpt':
            raise RuntimeError('final_selected_main_identity_mismatch')
        if sha(EXE) != EXE_SHA or sha(Path('/proc') / str(client.pid) / 'exe') != EXE_SHA:
            raise RuntimeError('selected_live_executable_changed')
        report['mainProcess'] = public_snapshot(main_state)
        report['renderers'] = [public_snapshot(item) for item in renderers]
        for item in renderers:
            if item['executable'] != str(EXE) or item['status'].get('Seccomp') != '2' or any(
                    item[key] != final_main[key] for key in ('executableDevice', 'executableInode')):
                raise RuntimeError('renderer_executable_or_seccomp_mismatch')
            if item['status'].get('NoNewPrivs') != '1' or int(item['status'].get('CapEff', '1'), 16) != 0:
                raise RuntimeError('renderer_privilege_restriction_absent')
            if int(item['status'].get('Seccomp_filters', '0')) <= int(main_state['status'].get('Seccomp_filters', '0')):
                raise RuntimeError('renderer_additional_seccomp_filters_absent')
            if item['namespaces']['pid'] == main_state['namespaces']['pid'] or all(
                    item['namespaces'][key] == main_state['namespaces'][key] for key in ('user', 'mnt')):
                raise RuntimeError('renderer_namespace_isolation_absent')
        report['observedRendererRestrictionsQualified'] = True
        report['passed'] = True
    except Exception as error:
        report['failure'] = type(error).__name__ + ': ' + str(error)
    finally:
        failures = []
        if group_created:
            try:
                if str(os.getpid()) in (group / 'cgroup.procs').read_text().split():
                    raise RuntimeError('supervisor_inside_TEST_cgroup')
                (group / 'cgroup.kill').write_text('1')
                report['exactCgroupKillReturned'] = True
            except Exception as error: failures.append('cgroup_kill: ' + str(error))
        deadline = time.monotonic() + 5
        for pid, (fd, saved) in handles.items():
            try:
                try: signal.pidfd_send_signal(fd, signal.SIGKILL)
                except ProcessLookupError: pass
                with selectors.DefaultSelector() as selector:
                    selector.register(fd, selectors.EVENT_READ)
                    exited = bool(selector.select(max(0, deadline - time.monotonic())))
                report['trackedProcesses'].append(dict(pid=pid, startTicks=saved['startTicks'], kernelExitObserved=exited))
                if not exited: failures.append('kernel_exit_unknown')
            except Exception as error: failures.append('pidfd_cleanup: ' + str(error))
            finally:
                try: os.close(fd)
                except OSError as error: failures.append('pidfd_close: ' + str(error))
        for child in children:
            try:
                if child.pid not in handles and child.poll() is None:
                    # Unreaped direct children cannot have their PID reused.
                    child.kill()
                child.wait(timeout=max(0.01, deadline - time.monotonic()))
                if child.stdout: child.stdout.close()
            except Exception as error: failures.append('direct_child_collection: ' + str(error))
        if group_created:
            try:
                while time.monotonic() < deadline and 'populated 1' in (group / 'cgroup.events').read_text():
                    time.sleep(0.025)
                report['cgroupEmptyObserved'] = 'populated 0' in (group / 'cgroup.events').read_text()
                if not report['cgroupEmptyObserved']: failures.append('owned_cgroup_process_scope_unknown')
                else: group.rmdir()
            except Exception as error: failures.append('cgroup_collection: ' + str(error))
        try: subprocess.run(['/usr/bin/sync'], check=True, timeout=20)
        except Exception as error: failures.append('sync: ' + str(error))
        report['cleanupErrors'] = failures
        report['cleanupPassed'] = not failures
        report['passed'] = report['passed'] and report['cleanupPassed']
        try:
            data = json.dumps(report, sort_keys=True).encode()
            (ROOT / 'result.json').write_bytes(data)
        except Exception as error:
            report['passed'] = False; report['publicationError'] = str(error)
        try:
            # A filesystem publication failure must still produce a failed frame.
            data = json.dumps(report, sort_keys=True).encode()
            frame = 'NAVIGATION_TEST_CLIENT_V1 ' + hashlib.sha256(data).hexdigest() + ' ' + base64.b64encode(data).decode() + '\n'
            with Path('/dev/ttyS0').open('w') as serial:
                serial.write(frame); serial.flush()
        finally:
            # Independent of report/serial publication, bounded outer QMP proves shutdown.
            subprocess.run(['/usr/bin/systemctl', 'poweroff'], check=True, timeout=20)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
