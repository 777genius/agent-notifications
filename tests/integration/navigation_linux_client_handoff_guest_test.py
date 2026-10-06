#!/usr/bin/python3
"""One offline guest: native notification click -> cold callback -> selected client."""
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import selectors
import signal
import socket
import stat
import struct
import subprocess
import time
import uuid

SEED = Path('/mnt/navigation-handoff-test-seed')
ROOT = Path('/var/lib/navigation-client-handoff-TEST')


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for part in iter(lambda: stream.read(1048576), b''):
            h.update(part)
    return h.hexdigest()


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    if os.getuid() != 0 or Path('/.dockerenv').exists() or ROOT.exists():
        raise RuntimeError('fresh_offline_TEST_guest_root_only')
    mounts = [row.split() for row in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in mounts):
        raise RuntimeError('readonly_TEST_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux selected-client handoff TEST only\n' or sorted(p.name for p in Path('/sys/class/net').iterdir()) != ['lo']:
        raise RuntimeError('offline_TEST_marker_and_no_NIC_required')
    manifest = json.loads((SEED / 'manifest.json').read_text())
    files = {'client-callback.py', 'client-sender.py', 'guest-pointer-entry.py', 'guest-pointer.so',
             'kernel-observer.py', 'protocol-observer.py', 'client-controller.py'}
    if set(manifest) != {'files', 'frontendSHA256', 'backendSHA256'} or set(manifest['files']) != files:
        raise RuntimeError('fixed_TEST_seed_manifest_required')
    for name, digest in manifest['files'].items():
        if not re.fullmatch('[0-9a-f]{64}', digest) or sha(SEED / name) != digest:
            raise RuntimeError('TEST_source_snapshot_changed')
    if Path(__file__).resolve() != SEED / 'client-controller.py':
        raise RuntimeError('fixed_TEST_controller_entry_required')
    kernel = load('TEST_kernel', SEED / 'kernel-observer.py')
    protocol = load('TEST_protocol', SEED / 'protocol-observer.py')
    callback = load('TEST_callback', SEED / 'client-callback.py')
    nonce = uuid.uuid4().hex
    group = Path('/sys/fs/cgroup') / ('navigation-handoff-TEST-' + nonce)
    work = ROOT / 'session'
    children, streams, handles = [], [], {}
    report = dict(scope='offline_selected_client_native_handoff_TEST', nonce=nonce, passed=False,
        retryAllowed=False, notificationAttempted=False, clickAttempted=False,
        handoffQualified=False, activationQualified=False, navigationQualified=False,
        humanRenderQualified=False, trackedProcesses=[], sourceSHA256=manifest['files'])
    deadline = now() + 65
    group_created = False
    env = dict(PATH='/usr/bin:/bin', LANG='C.UTF-8', HOME=str(work / 'home'),
        XDG_CONFIG_HOME=str(work / 'config'), XDG_DATA_HOME=str(work / 'data'),
        XDG_CACHE_HOME=str(work / 'cache'), XDG_RUNTIME_DIR=str(work / 'runtime'),
        XDG_DATA_DIRS='/opt/portal/share:/usr/share', XDG_SESSION_TYPE='wayland',
        XDG_CURRENT_DESKTOP='TEST', WLR_BACKENDS='headless', WLR_RENDERER='pixman',
        WLR_HEADLESS_OUTPUTS='1', WLR_LIBINPUT_NO_DEVICES='1', GDK_BACKEND='wayland')

    def persist():
        (ROOT / 'progress.json').write_text(json.dumps(report, indent=2) + '\n')

    def read(path):
        with path.open('rb') as stream:
            data = stream.read(4 * 1024 * 1024 + 1)
        if len(data) > 4 * 1024 * 1024:
            raise RuntimeError('private_trace_exceeds_bound')
        return data.decode()

    def wait(predicate, seconds=5):
        end = min(deadline, now() + seconds)
        while True:
            if now() >= end: raise RuntimeError('bounded_observation_expired_no_retry')
            ready = predicate()
            if now() >= end: raise RuntimeError('expired_observation_cannot_admit_effect')
            if ready: return
            time.sleep(0.025)

    def drop():
        (group / 'cgroup.procs').write_text(str(os.getpid()))
        os.setgroups([]); os.setgid(1000); os.setuid(1000); os.umask(0o077)

    def retain(pid):
        before = kernel.snapshot(pid)
        fd = os.pidfd_open(pid, 0)
        try:
            after = kernel.snapshot(pid)
            if before['startTicks'] != after['startTicks'] or after['status']['Uid'].split() != ['1000'] * 4:
                raise RuntimeError('owned_incarnation_or_uid_changed')
            if Path('/proc', str(pid), 'cgroup').read_text().strip() != '0::/' + group.name:
                raise RuntimeError('process_outside_owned_TEST_cgroup')
            with selectors.DefaultSelector() as selector:
                selector.register(fd, selectors.EVENT_READ)
                if selector.select(0): raise RuntimeError('owned_process_already_exited')
            if pid in handles:
                raise RuntimeError('refuse_to_replace_retained_incarnation')
            handles[pid] = (fd, after)
            return after
        except Exception:
            os.close(fd); raise

    def alive(pid):
        fd, saved = handles[pid]
        with selectors.DefaultSelector() as selector:
            selector.register(fd, selectors.EVENT_READ)
            if selector.select(0): raise RuntimeError('required_owned_process_exited')
        if kernel.snapshot(pid)['startTicks'] != saved['startTicks']:
            raise RuntimeError('owned_incarnation_changed')

    def capture_group():
        values = (group / 'cgroup.procs').read_text().split()
        if len(values) > 128: raise RuntimeError('owned_process_observation_bound_exceeded')
        for value in values:
            pid = int(value)
            if pid in handles: continue
            try: retain(pid)
            except (FileNotFoundError, ProcessLookupError): pass

    def start(label, argv, pipe=False, input_pipe=False, extra=None):
        out, err = (ROOT / (label + '.stdout')).open('xb'), (ROOT / (label + '.stderr')).open('xb')
        streams.extend([out, err])
        if now() >= deadline: raise RuntimeError('deadline_before_owned_process_start')
        child = subprocess.Popen(argv, cwd=work, env=dict(env, **(extra or {})), preexec_fn=drop,
            stdin=subprocess.PIPE if input_pipe else subprocess.DEVNULL,
            stdout=subprocess.PIPE if pipe else out, stderr=err, bufsize=0 if pipe else -1,
            start_new_session=True)
        children.append(child); retain(child.pid)
        return child

    def run(argv):
        remaining = deadline - now()
        if remaining <= 0: raise RuntimeError('deadline_before_command')
        value = subprocess.run(argv, cwd=work, env=env, preexec_fn=drop,
            capture_output=True, timeout=min(5, remaining))
        if now() >= deadline: raise RuntimeError('private_bus_reply_after_deadline')
        if value.returncode: raise RuntimeError('private_bus_command_failed')
        return value.stdout.decode().strip()

    def bus(method, name):
        return run(['/usr/bin/gdbus', 'call', '--session', '--dest', 'org.freedesktop.DBus',
            '--object-path', '/org/freedesktop/DBus', '--method', 'org.freedesktop.DBus.' + method, name])

    def owner(name, child):
        def ready():
            alive(child.pid)
            return bus('NameHasOwner', name) == '(true,)'
        wait(ready)
        if bus('GetConnectionUnixProcessID', name) != '(uint32 ' + str(child.pid) + ',)':
            raise RuntimeError('private_bus_owner_PID_mismatch')
        value = bus('GetNameOwner', name)
        match = re.fullmatch(r"\('(:[0-9]+\.[0-9]+)',\)", value)
        if not match: raise RuntimeError('private_bus_unique_owner_missing')
        return match[1]

    def root_spec(path, data):
        with path.open('x') as stream:
            json.dump(data, stream); stream.flush(); os.fsync(stream.fileno())
        path.chmod(0o444)

    def server_focus(pid):
        # Query the retained compositor over this peer-verified connection, not swaymsg's lookup.
        alive(sway.pid)
        paths = list((work / 'runtime').glob('sway-ipc.*.sock'))
        if len(paths) != 1 or paths[0].resolve() != paths[0] or not stat.S_ISSOCK(paths[0].stat().st_mode):
            raise RuntimeError('unique_private_sway_IPC_required')
        end = min(deadline, now() + 3)
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(max(0.01, end - now())); connection.connect(str(paths[0]))
            peer = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if peer[:2] != (sway.pid, 1000): raise RuntimeError('actual_sway_IPC_peer_unbound')
            alive(sway.pid)
            connection.sendall(struct.pack('<6sII', b'i3-ipc', 0, 4))
            def receive(count):
                result = b''
                while len(result) < count:
                    if now() >= end: raise RuntimeError('bounded_sway_IPC_reply_expired')
                    connection.settimeout(end - now())
                    part = connection.recv(count - len(result))
                    if not part: raise RuntimeError('sway_IPC_reply_incomplete')
                    result += part
                return result
            magic, length, kind = struct.unpack('<6sII', receive(14))
            if magic != b'i3-ipc' or kind != 4 or not 0 < length <= 4 * 1024 * 1024:
                raise RuntimeError('sway_IPC_tree_header_invalid')
            tree = json.loads(receive(length))
            alive(sway.pid)
        pending, focused, count = [tree], [], 0
        while pending:
            node = pending.pop(); count += 1
            if count > 1024 or not isinstance(node, dict): raise RuntimeError('sway_tree_bound_or_type_invalid')
            if node.get('focused') is True and type(node.get('pid')) is int:
                focused.append(node)
            for key in ('nodes', 'floating_nodes'):
                values = node.get(key, [])
                if not isinstance(values, list): raise RuntimeError('sway_child_nodes_invalid')
                pending.extend(values)
        if len(focused) != 1 or focused[0]['pid'] != pid: return False
        alive(pid)
        current = kernel.snapshot(pid)
        if current['executable'] != str(kernel.EXE): raise RuntimeError('focused_client_kernel_identity_changed')
        report['serverFocus'] = dict(peerPID=peer[0], peerUID=peer[1], clientPID=pid,
            clientBirth=current['startTicks'], containerID=focused[0].get('id'),
            evidenceClass='peer_verified_compositor_tree_and_selected_kernel_incarnation')
        return True

    def messages():
        return [part for part in re.split(r'(?m)(?=^(?:method call|method return|signal|error) time=)',
            read(ROOT / 'monitor.stdout')) if part.strip()]

    def trace():
        return read(ROOT / 'mako.stderr')

    try:
        ROOT.mkdir(mode=0o700); os.chown(ROOT, 0, 1000); ROOT.chmod(0o710)
        # Source checkpoint only: refuse before creating any native child or notification.
        # Remove this refusal only with the reviewed server connection/toplevel join.
        raise RuntimeError('native_attempt_not_ready_server_surface_join_missing')
        work.mkdir(mode=0o700); os.chown(work, 1000, 1000)
        group.mkdir(mode=0o700); group_created = True
        if not (group / 'cgroup.kill').is_file(): raise RuntimeError('owned_cgroup_kill_required')
        report['ownedCgroup'] = str(group)
        for name in ('home', 'config', 'data', 'cache', 'runtime', 'callback'):
            path = work / name; path.mkdir(mode=0o700); os.chown(path, 1000, 1000)
        if sha(kernel.EXE) != kernel.EXE_SHA or sha(kernel.LAUNCHER) != kernel.LAUNCHER_SHA or sha(Path('/etc/apparmor.d/chatgpt')) != kernel.PROFILE_SHA:
            raise RuntimeError('selected_vendor_installation_changed')
        if Path('/etc/apparmor.d/local/chatgpt').exists() or not any(row.split(' (', 1)[0] == 'chatgpt' for row in Path('/sys/kernel/security/apparmor/profiles').read_text().splitlines()):
            raise RuntimeError('shipped_vendor_profile_required')
        frontend_path = Path('/opt/portal/libexec/xdg-desktop-portal')
        backend_path = Path('/opt/gtk/libexec/xdg-desktop-portal-gtk')
        if sha(frontend_path) != manifest['frontendSHA256'] or sha(backend_path) != manifest['backendSHA256']:
            raise RuntimeError('qualified_portal_runtime_changed')
        app_id = 'org.notification.NavigationTest' + nonce
        for folder, suffix, text in [('applications', '.desktop', '[Desktop Entry]\nType=Application\nName=Navigation TEST\nDBusActivatable=true\nExec=/usr/bin/python3 ' + str(SEED / 'client-callback.py') + '\n'),
                ('dbus-1/services', '.service', '[D-BUS Service]\nName=' + app_id + '\nExec=/usr/bin/python3 ' + str(SEED / 'client-callback.py') + '\n')]:
            directory = work / 'data' / folder; directory.mkdir(parents=True); os.chown(directory, 1000, 1000)
            path = directory / (app_id + suffix); path.write_text(text); path.chmod(0o444)
        portal_config = work / 'config/xdg-desktop-portal'; portal_config.mkdir(); os.chown(portal_config, 1000, 1000)
        (portal_config / 'portals.conf').write_text('[preferred]\ndefault=gtk\norg.freedesktop.impl.portal.Notification=gtk\n')
        sway_config = work / 'sway.conf'; sway_config.write_text('xwayland disable\nseat seat0 fallback true\noutput * resolution 1280x720\nfocus_follows_mouse no\n')
        mako_config = work / 'mako.conf'; mako_config.write_text('anchor=top-center\nwidth=600\nheight=120\nmargin=0\ndefault-timeout=0\nmax-visible=1\nfont=DejaVu Sans 12\non-button-left=invoke-default-action\n')
        sway = start('sway', ['/usr/bin/sway', '-c', str(sway_config), '-d'])
        sockets = []
        def display_ready():
            nonlocal sockets
            alive(sway.pid)
            sockets = [p for p in (work / 'runtime').glob('wayland-*') if stat.S_ISSOCK(p.stat().st_mode)]
            return len(sockets) == 1
        wait(display_ready)
        env['WAYLAND_DISPLAY'] = sockets[0].name
        callback.peer(sockets[0], sway.pid, int(handles[sway.pid][1]['startTicks']))
        daemon = start('bus', ['/usr/bin/dbus-daemon', '--session', '--nofork', '--print-address=1',
            '--address=unix:path=' + str(work / 'runtime/bus.sock')], True)
        with selectors.DefaultSelector() as selector:
            selector.register(daemon.stdout, selectors.EVENT_READ)
            if not selector.select(5): raise RuntimeError('private_bus_address_expired')
            address = os.read(daemon.stdout.fileno(), 4096).decode().strip()
        callback.private_bus_address(address, work / 'runtime/bus.sock')
        callback.peer(work / 'runtime/bus.sock', daemon.pid, int(handles[daemon.pid][1]['startTicks']))
        env['DBUS_SESSION_BUS_ADDRESS'] = address
        root_spec(ROOT / 'callback-spec.json', dict(nonce=nonce, appID=app_id,
            helperSHA256=manifest['files']['client-callback.py'], busPID=daemon.pid,
            busBirth=int(handles[daemon.pid][1]['startTicks']), busSocket=str(work / 'runtime/bus.sock'),
            busAddress=address, compositorPID=sway.pid, compositorBirth=int(handles[sway.pid][1]['startTicks']),
            waylandDisplay=sockets[0].name))
        # D-Bus activation inherits the daemon's environment, so seed session address explicitly.
        run(['/usr/bin/dbus-update-activation-environment', 'DBUS_SESSION_BUS_ADDRESS=' + address,
            'XDG_RUNTIME_DIR=' + env['XDG_RUNTIME_DIR'], 'XDG_DATA_HOME=' + env['XDG_DATA_HOME'],
            'XDG_CONFIG_HOME=' + env['XDG_CONFIG_HOME']])
        monitor = start('monitor', ['/usr/bin/dbus-monitor', '--session', "interface='org.freedesktop.Notifications'",
            "interface='org.freedesktop.Application'", "type='method_return'"])
        mako = start('mako', ['/usr/bin/mako', '--config', str(mako_config)], extra={'WAYLAND_DEBUG': '1'})
        mako_owner = owner('org.freedesktop.Notifications', mako)
        gtk = start('gtk', [str(backend_path)])
        gtk_owner = owner('org.freedesktop.impl.portal.desktop.gtk', gtk)
        frontend = start('frontend', [str(frontend_path), '--verbose'])
        frontend_owner = owner('org.freedesktop.portal.Desktop', frontend)
        report['providerOwners'] = dict(mako=mako_owner, gtk=gtk_owner, frontend=frontend_owner)
        report['notificationAttempted'] = True; persist()
        sender = start('sender', ['/usr/bin/python3', str(SEED / 'client-sender.py')])
        code = sender.wait(timeout=min(12, deadline - now())); exited = now()
        report['senderExit'] = dict(pid=sender.pid, collected=True, exitCode=code, exitedBoot=exited)
        if code or bus('NameHasOwner', app_id) != '(false,)' or (work / 'callback/service-start.json').exists():
            raise RuntimeError('sender_exit_or_cold_callback_absence_unproved')
        notify = [m for m in messages() if m.startswith('method call ') and 'interface=org.freedesktop.Notifications; member=Notify\n' in m and 'sender=' + gtk_owner + ' ' in m.splitlines()[0] and 'string "Navigation TEST ' + nonce + '"' in m]
        if len(notify) != 1: raise RuntimeError('sole_owned_native_Notify_unproved')
        serial = re.search(r' serial=(\d+) ', notify[0].splitlines()[0])
        replies = [m for m in messages() if m.startswith('method return ') and 'sender=' + mako_owner + ' ' in m.splitlines()[0] and 'destination=' + gtk_owner + ' ' in m.splitlines()[0] and re.search(r'\breply_serial=' + serial[1] + r'(?:\s|$)', m.splitlines()[0])]
        if len(replies) != 1: raise RuntimeError('native_notification_reply_unbound')
        native_id = re.fullmatch(r'\s+uint32 ([0-9]+)\s*', '\n'.join(replies[0].splitlines()[1:]))
        if not native_id or int(native_id[1]) <= 0: raise RuntimeError('native_notification_ID_invalid')
        report['nativeNotificationID'] = int(native_id[1])
        layers = []
        def surface_ready():
            nonlocal layers
            layers = re.findall(r'get_layer_surface\(new id zwlr_layer_surface_v1@(\d+), wl_surface@(\d+), [^\n]*"notifications"\)', trace())
            return len(layers) == 1 and re.search(r'zwlr_layer_surface_v1@' + layers[0][0] + r'\.configure\(', trace()) is not None
        wait(surface_ready)
        surface = layers[0][1]
        configured = re.findall(r'zwlr_layer_surface_v1@' + layers[0][0] + r'\.configure\(\d+, (\d+), (\d+)\)', trace())
        if not configured or not (0 < int(configured[-1][0]) <= 1280 and 0 < int(configured[-1][1]) <= 720):
            raise RuntimeError('native_surface_dimensions_unproved')
        y = int(configured[-1][1]) // 2
        root_spec(ROOT / 'pointer-spec.json', dict(nonce=nonce, y=y,
            entrySHA256=manifest['files']['guest-pointer-entry.py'], librarySHA256=manifest['files']['guest-pointer.so']))
        pointer = start('pointer', ['/usr/bin/python3', str(SEED / 'guest-pointer-entry.py')], True, True)
        entry_pattern = r'wl_pointer@(\d+)\.enter\(\d+, wl_surface@' + surface + r','
        wait(lambda: re.search(entry_pattern, trace()) is not None)
        enters = list(re.finditer(entry_pattern, trace()))
        if len(enters) != 1: raise RuntimeError('ambiguous_pointer_enter')
        for child in (sway, daemon, mako, gtk, frontend, pointer): alive(child.pid)
        pre_click_characters = len(trace())
        report['clickAttempted'] = True; report['clickAttemptedBoot'] = now(); persist()
        if now() >= deadline: raise RuntimeError('deadline_before_sole_native_CLICK')
        pointer.stdin.write(b'CLICK\n'); pointer.stdin.flush()
        wait(lambda: (work / 'callback/handoff-launched.json').exists(), 12)
        capture_group()
        started = json.loads((work / 'callback/service-start.json').read_text())
        launched = json.loads((work / 'callback/handoff-launched.json').read_text())
        hz = os.sysconf('SC_CLK_TCK')
        if hz <= 0 or started['pid'] != launched['pid'] or started['startTicks'] != launched['startTicks'] or started['startTicks'] / hz < exited:
            raise RuntimeError('cold_callback_kernel_birth_unproved')
        if started['pid'] not in handles: retain(started['pid'])
        alive(started['pid'])
        if int(handles[started['pid']][1]['startTicks']) != started['startTicks'] or bus('GetConnectionUnixProcessID', app_id) != '(uint32 ' + str(started['pid']) + ',)':
            raise RuntimeError('cold_callback_owner_unbound')
        client_pid = launched['clientPID']
        if client_pid not in handles: retain(client_pid)
        alive(client_pid)
        client = kernel.snapshot(client_pid)
        if int(client['startTicks']) != launched['clientBirth'] or client['executable'] != str(kernel.EXE) or sha(Path('/proc', str(client_pid), 'exe')) != kernel.EXE_SHA:
            raise RuntimeError('selected_live_client_identity_unproved')
        if client['appArmor'].split(' (', 1)[0] != 'chatgpt':
            raise RuntimeError('selected_main_vendor_profile_not_attached')
        report['clientProcess'] = kernel.public_snapshot(client)
        report['handoffObserved'] = True
        chain = protocol.native_click_chain(trace(), pre_click_characters, messages(), mako_owner,
            gtk_owner, app_id, nonce, int(native_id[1]), surface, enters[0][1])
        if not chain['forwarded']: raise RuntimeError('native_platform_token_not_forwarded')
        token_hash = chain['tokenSHA256']
        report['protocolOffsets'] = chain['offsets']
        observation = json.loads((work / 'callback/platform-data.json').read_text())
        intent = json.loads((work / 'callback/handoff-intent.json').read_text())
        if observation['activationTokenSHA256'] != token_hash or intent['activationTokenSHA256'] != token_hash or intent['count'] != 1:
            raise RuntimeError('callback_and_handoff_token_unbound')
        pointer.stdin.write(b'DONE\n'); pointer.stdin.flush(); pointer.stdin.close()
        if pointer.wait(timeout=3): raise RuntimeError('pointer_press_release_collection_failed')
        pointer_bytes = pointer.stdout.read(4096)
        (ROOT / 'pointer.stdout').write_bytes(pointer_bytes)
        if b'CLICKED\n' not in pointer_bytes: raise RuntimeError('pointer_click_ack_missing')
        report['compositorTokenSHA256'] = token_hash
        report['tokenEqualityObserved'] = True
        # Request evidence is separate from server focus; neither implies chat selection.
        client_trace = work / 'callback/client.stderr'
        wait(lambda: re.search(r'xdg_activation_v1@\d+\.activate\("' + re.escape(chain['token']) + r'", wl_surface@\d+\)', read(client_trace)) is not None, 12)
        report['clientActivationRequestObserved'] = True
        requests = re.findall(r'xdg_activation_v1@\d+\.activate\(\"' + re.escape(chain['token']) + r'\", wl_surface@(\d+)\)', read(client_trace))
        if len(requests) != 1: raise RuntimeError('single_client_activation_surface_unproved')
        report['clientActivationSurfaceID'] = requests[0]
        wait(lambda: server_focus(client_pid), 12)
        report['serverFocusObserved'] = True
        report['activationRequestAndFocusObserved'] = True
        # A server-side connection/surface join is still required before activation qualification.
        # This proves an observed native request and server focus, not counterfactual causation.
        report['activationEvidenceClass'] = 'selected_client_token_request_and_server_focus'
        for child in (gtk, frontend, mako, monitor):
            signal.pidfd_send_signal(handles[child.pid][0], signal.SIGTERM)
            child.wait(timeout=2)
        final_calls = [m for m in messages() if m.startswith('method call ') and 'interface=org.freedesktop.Application; member=ActivateAction\n' in m and 'destination=' + app_id + ' ' in m.splitlines()[0]]
        if len(final_calls) != 1 or final_calls[0].splitlines()[0] != chain['activateSender']:
            raise RuntimeError('additional_addressed_native_action')
        buttons = re.findall(r'wl_pointer@' + enters[0][1] + r'\.button\(\d+, \d+, 272, ([01])\)', trace())
        if buttons != ['1', '0']: raise RuntimeError('sole_native_press_release_unproved')
        if any((work / 'callback' / name).exists() for name in ('callback-rejected.json', 'installation-rejected.json', 'deadline-rejected.json')):
            raise RuntimeError('callback_rejection_observed')
        if any(sha(SEED / name) != digest for name, digest in manifest['files'].items()):
            raise RuntimeError('TEST_seed_source_changed')
        raise RuntimeError('server_activation_surface_join_not_implemented')
    except Exception as error:
        report['failure'] = type(error).__name__ + ': ' + str(error)
    finally:
        failures = []
        if group_created:
            try: capture_group()
            except Exception as error: failures.append('group_observation: ' + str(error))
            try:
                if str(os.getpid()) in (group / 'cgroup.procs').read_text().split(): raise RuntimeError('supervisor_in_owned_cgroup')
                (group / 'cgroup.kill').write_text('1')
            except Exception as error: failures.append('cgroup_kill: ' + str(error))
        end = now() + 5
        for pid, (fd, saved) in handles.items():
            try:
                try: signal.pidfd_send_signal(fd, signal.SIGKILL)
                except ProcessLookupError: pass
                with selectors.DefaultSelector() as selector:
                    selector.register(fd, selectors.EVENT_READ)
                    exited = bool(selector.select(max(0, end - now())))
                report['trackedProcesses'].append(dict(pid=pid, startTicks=saved['startTicks'], kernelExitObserved=exited))
                if not exited: failures.append('kernel_exit_unknown')
            except Exception as error: failures.append('pidfd_collection: ' + str(error))
            finally:
                try: os.close(fd)
                except OSError as error: failures.append('pidfd_close: ' + str(error))
        for child in children:
            try:
                if child.pid not in handles and child.poll() is None: child.kill()
                child.wait(timeout=max(0.01, end - now()))
                if child.stdin and not child.stdin.closed: child.stdin.close()
                if child.stdout: child.stdout.close()
            except Exception as error: failures.append('direct_child_collection: ' + str(error))
        if group_created:
            try:
                empty_deadline = now() + 3
                while 'populated 1' in (group / 'cgroup.events').read_text():
                    if now() >= empty_deadline: raise RuntimeError('owned_cgroup_collection_expired')
                    time.sleep(0.025)
                report['cgroupEmptyObserved'] = True; group.rmdir()
            except Exception as error: failures.append('cgroup_collection: ' + str(error))
        for stream in streams:
            try: stream.close()
            except Exception as error: failures.append('stream_close: ' + str(error))
        report['cleanupErrors'] = failures; report['cleanupPassed'] = not failures
        report['passed'] = report['passed'] and report['cleanupPassed']
        def qualify():
            for qualified, observed in (('handoffQualified', 'handoffObserved'), ('tokenForwardingQualified', 'tokenEqualityObserved'), ('focusQualified', 'serverFocusObserved')):
                report[qualified] = report['passed'] and report.get(observed, False)
            report['activationQualified'] = False  # Requires the actual server connection/surface join.
        try:
            qualify()
            try:
                if ROOT.exists(): (ROOT / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
            except Exception as error:
                report['passed'] = False; report['publicationError'] = str(error); qualify()
            data = json.dumps(report, sort_keys=True).encode()
            with Path('/dev/ttyS0').open('w') as serial:
                serial.write('NAVIGATION_TEST_HANDOFF_V1 ' + hashlib.sha256(data).hexdigest() + ' ' + base64.b64encode(data).decode() + '\n'); serial.flush()
        finally:
            # Serialization/publication failures still attempt bounded guest shutdown.
            subprocess.run(['/usr/bin/systemctl', 'poweroff'], check=True, timeout=20)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
