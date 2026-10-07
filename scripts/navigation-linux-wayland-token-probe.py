#!/usr/bin/python3
"""Owned TEST container only: native pointer click and cold token forwarding."""
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import socket
import stat
import struct
import subprocess
import time
import uuid


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def activate_arguments(message):
    quoted = r'("(?:\\.|[^"\\])*")'
    match = re.fullmatch(r'\s*string ' + quoted + r'\s+array \[\s*variant\s+string ' + quoted + r'\s*\]\s+array \[(.*)\]\s*',
        '\n'.join(message.splitlines()[1:]), re.DOTALL)
    if not match: raise RuntimeError('ActivateAction_typed_arguments_invalid')
    entries, remaining = {}, match[3]
    while remaining.strip():
        entry = re.match(r'\s*dict entry\(\s*string ' + quoted + r'\s+variant\s+string ' + quoted + r'\s*\)\s*', remaining)
        if not entry: raise RuntimeError('ActivateAction_platform_data_invalid')
        key, value = json.loads(entry[1]), json.loads(entry[2])
        if key in entries: raise RuntimeError('duplicate_platform_key')
        entries[key] = value; remaining = remaining[entry.end():]
    return json.loads(match[1]), json.loads(match[2]), entries


def native_click_chain(full_trace, pre_click_characters, events, mako_owner, gtk_owner, app_id, nonce, native_id, surface, entered_pointer):
    entry_pattern = r'wl_pointer@(\d+)\.enter\(\d+, wl_surface@' + surface + r','
    token_requests = re.findall(r'xdg_activation_token_v1@(\d+)\.set_serial\((\d+), wl_seat@(\d+)\)', full_trace)
    if len(token_requests) != 1: raise RuntimeError('single_click_token_request_unproved')
    token_object, click_serial, seat = token_requests[0]
    new_trace = full_trace[pre_click_characters:]
    buttons = re.findall(r'wl_pointer@(\d+)\.button\(' + click_serial + r', \d+, 272, 1\)', new_trace)
    if buttons != [entered_pointer] or 'wl_seat@' + seat + '.get_pointer(new id wl_pointer@' + entered_pointer + ')' not in full_trace: raise RuntimeError('click_serial_surface_token_unbound')
    done = re.findall(r'xdg_activation_token_v1@' + token_object + r'\.done\("([^"\n]+)"\)', new_trace)
    if len(done) != 1 or len(done[0].encode()) > 4096: raise RuntimeError('compositor_token_missing')
    ordered_patterns = [entry_pattern, r'wl_pointer@' + entered_pointer + r'\.button\(' + click_serial + r', \d+, 272, 1\)',
        r'get_activation_token\(new id xdg_activation_token_v1@' + token_object + r'\)',
        r'xdg_activation_token_v1@' + token_object + r'\.set_serial\(' + click_serial + r', wl_seat@' + seat + r'\)',
        r'xdg_activation_token_v1@' + token_object + r'\.set_surface\(wl_surface@' + surface + r'\)',
        r'xdg_activation_token_v1@' + token_object + r'\.commit\(\)',
        r'xdg_activation_token_v1@' + token_object + r'\.done\("' + re.escape(done[0]) + r'"\)']
    cursor = 0; offsets = []
    for pattern in ordered_patterns:
        event = re.search(pattern, full_trace[cursor:])
        if not event: raise RuntimeError('native_protocol_order_unproved')
        offsets.append(cursor + event.start()); cursor += event.end()
    interval = full_trace[offsets[0]:cursor]
    if re.search(r'wl_pointer@' + entered_pointer + r'\.leave\(', interval) or 'wl_surface@' + surface + '.destroy()' in interval:
        raise RuntimeError('surface_pointer_binding_lost_before_token')

    token_hash = hashlib.sha256(done[0].encode()).hexdigest()
    signals = [part for part in events if part.startswith('signal ') and 'sender=' + mako_owner + ' ' in part.splitlines()[0] and 'path=/org/freedesktop/Notifications;' in part.splitlines()[0] and '\n   uint32 ' + str(native_id) + '\n' in part]
    tokens = [part for part in signals if 'interface=org.freedesktop.Notifications; member=ActivationToken\n' in part and '\n   string "' + done[0] + '"\n' in part]
    actions = [part for part in signals if 'interface=org.freedesktop.Notifications; member=ActionInvoked\n' in part and '\n   string "default"\n' in part]
    activates = [part for part in events if part.startswith('method call ') and 'interface=org.freedesktop.Application; member=ActivateAction\n' in part and 'destination=' + app_id + ' ' in part.splitlines()[0]]
    if len(tokens) != 1 or len(actions) != 1 or len(activates) != 1 or not events.index(tokens[0]) < events.index(actions[0]) < events.index(activates[0]): raise RuntimeError('native_token_action_chain_unproved')
    expected_path = '/' + app_id.replace('.', '/').replace('-', '_')
    if 'sender=' + gtk_owner + ' ' not in activates[0].splitlines()[0] or 'path=' + expected_path + ';' not in activates[0].splitlines()[0]:
        raise RuntimeError('ActivateAction_sender_path_unbound')
    action_name, target, platform = activate_arguments(activates[0])
    if action_name != 'open' or target != 'notification-navigation-test:' + nonce: raise RuntimeError('ActivateAction_target_mismatch')
    if set(platform) - {'activation-token', 'desktop-startup-id'}: raise RuntimeError('unexpected_platform_key')
    forwarded = platform == {'activation-token': done[0], 'desktop-startup-id': done[0]}
    return dict(token=done[0], tokenSHA256=token_hash, forwarded=forwarded, platform=platform, offsets=offsets, activateSender=activates[0].splitlines()[0])


def main():
    evidence = Path('/evidence')
    if os.getuid() == 0 or os.environ.get('NAVIGATION_TEST_CONTAINER') != '1' or os.environ.get('NAVIGATION_WAYLAND_TEST') != '1' or not Path('/.dockerenv').exists() or not (evidence / 'container.marker').read_text().startswith('Linux portal container TEST '):
        raise RuntimeError('explicit_owned_wayland_TEST_required')
    root = evidence / 'runtime'; root.mkdir(mode=0o700)
    (root / 'fixture.marker').write_text('Linux portal TEST only\n')
    for name in ('home', 'data', 'config', 'cache', 'run'):
        (root / name).mkdir(mode=0o700)
    negative = os.environ.get('NAVIGATION_GTK_TOKEN_NEGATIVE') == '1'
    env = dict(PATH='/usr/bin:/bin', HOME=str(root / 'home'), XDG_RUNTIME_DIR=str(root / 'run'),
        XDG_DATA_HOME=str(root / 'data'), XDG_CONFIG_HOME=str(root / 'config'), XDG_CACHE_HOME=str(root / 'cache'),
        XDG_DATA_DIRS='/opt/portal/share:/usr/share', XDG_CURRENT_DESKTOP='TEST', LANG='C.UTF-8',
        NAVIGATION_TEST_CONTAINER='1', NAVIGATION_WAYLAND_TEST='1', WLR_BACKENDS='headless',
        WLR_RENDERER='pixman', WLR_HEADLESS_OUTPUTS='1', WLR_LIBINPUT_NO_DEVICES='1', GDK_BACKEND='wayland')
    # Constructed environment intentionally excludes inherited WAYLAND_SOCKET/display/bus.
    helper = root / 'helper.py'; shutil.copyfile('/fixture/navigation_linux_portal_test_app.py', helper)
    nonce = uuid.uuid4().hex
    spec = dict(appID='org.notification.NavigationTest' + nonce, nonce=nonce,
        title='Navigation TEST ' + nonce, helperSHA256=sha(helper))
    (root / 'spec.json').write_text(json.dumps(spec))
    execute = '/usr/bin/python3 ' + str(helper) + ' --service ' + str(root)
    for directory, suffix, content in [('applications', '.desktop', '[Desktop Entry]\nType=Application\nName=Navigation TEST\nDBusActivatable=true\nExec=' + execute + '\n'),
        ('dbus-1/services', '.service', '[D-BUS Service]\nName=' + spec['appID'] + '\nExec=' + execute + '\n')]:
        folder = root / 'data' / directory; folder.mkdir(parents=True)
        (folder / (spec['appID'] + suffix)).write_text(content)
    config = root / 'config/xdg-desktop-portal'; config.mkdir()
    (config / 'portals.conf').write_text('[preferred]\ndefault=gtk\norg.freedesktop.impl.portal.Notification=gtk\n')
    sway_config = root / 'sway.conf'
    sway_config.write_text('xwayland disable\nseat seat0 fallback true\noutput * resolution 1280x720\nfocus_follows_mouse no\n')
    mako_config = root / 'mako.conf'
    mako_config.write_text('anchor=top-center\nwidth=600\nheight=120\nmargin=0\ndefault-timeout=0\nmax-visible=1\nfont=DejaVu Sans 12\non-button-left=invoke-default-action\n')
    immutable = {str(path.relative_to(root)): sha(path) for path in [root / 'spec.json', sway_config, mako_config] + list((root / 'data').rglob('*')) if path.is_file()}
    for name in immutable: (root / name).chmod(0o400)
    report = dict(passed=False, tokenForwardingQualified=False, focusQualified=False,
        selectedClientQualified=False, navigationQualified=False, humanRenderQualified=False,
        surfaceEvidenceClass='owned_process_protocol_trace', serverDerivedSurfacePIDQualified=False,
        negativeControl=negative, processes=[], commands=[], retryAllowed=False,
        helperSHA256=sha(helper), runnerSHA256=sha(Path(__file__)), immutableSHA256=immutable)
    children, streams = [], []; deadline = now() + 50

    def persist():
        (root / 'progress.json').write_text(json.dumps(report, indent=2))

    def wait(predicate, seconds=5):
        end = min(deadline, now() + seconds)
        while not predicate():
            if now() >= end: raise RuntimeError('bounded_wait_failed')
            time.sleep(0.025)

    def start(label, argv, pipe=False, extra=None):
        out, err = (root / (label + '.stdout')).open('wb'), (root / (label + '.stderr')).open('wb')
        streams.extend([out, err])
        process = subprocess.Popen(argv, env=dict(env, **(extra or {})), stdout=subprocess.PIPE if pipe else out,
            stderr=err, stdin=subprocess.PIPE if label == 'pointer' else subprocess.DEVNULL, bufsize=0 if pipe else -1)
        children.append((label, process)); report['processes'].append(dict(label=label, pid=process.pid, argv=argv))
        persist(); return process

    def run(label, argv, allow_failure=False):
        value = subprocess.run(argv, env=env, capture_output=True, timeout=max(0.1, min(5, deadline - now())))
        (root / (label + '.stdout')).write_bytes(value.stdout); (root / (label + '.stderr')).write_bytes(value.stderr)
        report['commands'].append(dict(label=label, exitCode=value.returncode)); persist()
        if value.returncode and not allow_failure: raise RuntimeError(label + '_failed')
        return value

    def bus(label, method, name):
        return run(label, ['gdbus', 'call', '--session', '--dest', 'org.freedesktop.DBus', '--object-path',
            '/org/freedesktop/DBus', '--method', 'org.freedesktop.DBus.' + method, name], True)

    def owned(label, name, child):
        def ready():
            if child.poll() is not None: raise RuntimeError(label + '_exited')
            value = bus(label + '_pid', 'GetConnectionUnixProcessID', name)
            if value.returncode:
                if b'org.freedesktop.DBus.Error.NameHasNoOwner' not in value.stderr: raise RuntimeError(label + '_lookup_failed')
                return False
            if value.stdout.decode().strip() != '(uint32 ' + str(child.pid) + ',)': raise RuntimeError(label + '_PID_mismatch')
            return True
        wait(ready)
        owner = bus(label + '_unique', 'GetNameOwner', name)
        match = re.fullmatch(r"\('(:[0-9]+\.[0-9]+)',\)\s*", owner.stdout.decode())
        if not match: raise RuntimeError(label + '_owner_missing')
        return match[1]

    def messages():
        return [part for part in re.split(r'(?m)(?=^(?:method call|method return|signal|error) time=)',
            (root / 'monitor.stdout').read_text()) if part.strip()]

    def trace():
        return (root / 'mako.stderr').read_text()

    try:
        sway = start('sway', ['sway', '-c', str(sway_config), '-d'])
        sockets = []
        def display_ready():
            nonlocal sockets
            if sway.poll() is not None: raise RuntimeError('compositor_exited')
            sockets = [p for p in (root / 'run').glob('wayland-*') if stat.S_ISSOCK(p.stat().st_mode)]
            return len(sockets) == 1
        wait(display_ready)
        with socket.socket(socket.AF_UNIX) as connection:
            connection.connect(str(sockets[0]))
            pid, uid, gid = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        if pid != sway.pid or uid != os.getuid() or sway.poll() is not None: raise RuntimeError('private_compositor_peer_mismatch')
        report['compositorPeer'] = dict(pid=pid, uid=uid, gid=gid, executableSHA256=sha(Path('/usr/bin/sway')))
        env['WAYLAND_DISPLAY'] = sockets[0].name
        daemon = start('bus', ['dbus-daemon', '--session', '--nofork', '--print-address=1'], True)
        with selectors.DefaultSelector() as selector:
            selector.register(daemon.stdout, selectors.EVENT_READ)
            if not selector.select(3): raise RuntimeError('private_bus_address_timeout')
            address = daemon.stdout.readline(4096).decode().strip()
        if not address.startswith('unix:'): raise RuntimeError('private_bus_address_invalid')
        (root / 'bus.stdout').write_text(address + '\n'); env['DBUS_SESSION_BUS_ADDRESS'] = address
        monitor = start('monitor', ['dbus-monitor', '--session', "interface='org.freedesktop.Notifications'",
            "interface='org.freedesktop.Application'", "type='method_return'"])
        mako = start('mako', ['mako', '--config', str(mako_config)], extra={'WAYLAND_DEBUG': '1'})
        mako_owner = owned('mako', 'org.freedesktop.Notifications', mako)
        report['notificationOwner'] = dict(unique=mako_owner, pid=mako.pid,
            startTicks=int(Path('/proc/' + str(mako.pid) + '/stat').read_text().rsplit(')', 1)[1].split()[19]), executableSHA256=sha(Path('/usr/bin/mako')))
        gtk = start('gtk', ['/usr/libexec/xdg-desktop-portal-gtk' if negative else '/opt/gtk/libexec/xdg-desktop-portal-gtk'])
        gtk_owner = owned('gtk', 'org.freedesktop.impl.portal.desktop.gtk', gtk)
        frontend = start('frontend', ['/opt/portal/libexec/xdg-desktop-portal', '--verbose'])
        frontend_owner = owned('frontend', 'org.freedesktop.portal.Desktop', frontend)
        report['runtimeExecutables'] = {label: dict(pid=p.pid,
            startTicks=int(Path('/proc/' + str(p.pid) + '/stat').read_text().rsplit(')', 1)[1].split()[19]),
            executableSHA256=sha(Path('/proc/' + str(p.pid) + '/exe'))) for label, p in [('gtk', gtk), ('frontend', frontend)]}
        report['pointerExecutableSHA256'] = sha(Path('/fixture/navigation-wayland-pointer-test'))
        sender = start('sender', ['/usr/bin/python3', str(helper), '--sender', str(root)])
        code = sender.wait(timeout=10); exited = now()
        report['senderExit'] = dict(pid=sender.pid, exitCode=code, collected=True, exitedBoot=exited)
        if code or Path('/proc/' + str(sender.pid)).exists() or not (root / 'submitted.json').exists(): raise RuntimeError('sender_exit_unproved')
        absent = bus('application_absent', 'NameHasOwner', spec['appID'])
        if absent.returncode or absent.stdout.decode().strip() != '(false,)': raise RuntimeError('callback_already_running')
        notify = [part for part in messages() if part.startswith('method call ') and 'interface=org.freedesktop.Notifications; member=Notify\n' in part and 'sender=' + gtk_owner + ' ' in part.splitlines()[0] and 'string "' + spec['title'] + '"' in part]
        if len(notify) != 1: raise RuntimeError('single_native_Notify_unproved')
        serial = re.search(r' serial=(\d+) ', notify[0].splitlines()[0])
        replies = [part for part in messages() if part.startswith('method return ') and 'sender=' + mako_owner + ' ' in part.splitlines()[0] and 'destination=' + gtk_owner + ' ' in part.splitlines()[0] and re.search(r'\breply_serial=' + serial[1] + r'(?:\s|$)', part.splitlines()[0])]
        if len(replies) != 1: raise RuntimeError('native_notification_ID_unproved')
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
        if 'new id wl_surface@' + surface + ')' not in trace() or 'wl_surface@' + surface + '.commit()' not in trace(): raise RuntimeError('owned_surface_lifecycle_incomplete')
        configured = re.findall(r'zwlr_layer_surface_v1@' + layers[0][0] + r'\.configure\(\d+, (\d+), (\d+)\)', trace())
        if not configured or not (0 < int(configured[-1][0]) <= 1280 and 0 < int(configured[-1][1]) <= 720):
            raise RuntimeError('surface_dimensions_unproved')
        y = int(configured[-1][1]) // 2
        report['pointerCoordinates'] = dict(x=640, y=y, outputWidth=1280, outputHeight=720,
            configuredWidth=int(configured[-1][0]), configuredHeight=int(configured[-1][1]), evidenceClass='owned_protocol_configure')
        pointer = start('pointer', ['/fixture/navigation-wayland-pointer-test', str(y)], True)
        entry_pattern = r'wl_pointer@(\d+)\.enter\(\d+, wl_surface@' + surface + r','
        wait(lambda: re.search(entry_pattern, trace()) is not None)
        entered = list(re.finditer(entry_pattern, trace()))
        if len(entered) != 1: raise RuntimeError('ambiguous_pointer_enter')
        entered_pointer = entered[0][1]
        report['preClickTraceBytes'] = (root / 'mako.stderr').stat().st_size
        report['preClickTraceCharacters'] = len(trace())
        if any(p.poll() is not None for label, p in children if label != 'sender'): raise RuntimeError('background_exited_before_click')
        report['clickAttemptedBoot'] = now(); persist()
        pointer.stdin.write(b'CLICK\n'); pointer.stdin.flush()
        pointer_output = []
        with selectors.DefaultSelector() as selector:
            selector.register(pointer.stdout, selectors.EVENT_READ)
            end = min(deadline, now() + 5)
            while b'CLICKED\n' not in pointer_output:
                if now() >= end or not selector.select(max(0.01, end - now())): raise RuntimeError('click_outcome_unknown_no_retry')
                line = pointer.stdout.readline(128)
                if not line: raise RuntimeError('pointer_exited_before_click_ack')
                pointer_output.append(line)
        (root / 'pointer.stdout').write_bytes(b''.join(pointer_output))
        wait(lambda: (root / 'receipt.json').exists())
        callback = json.loads((root / 'service-start.json').read_text()); receipt = json.loads((root / 'receipt.json').read_text())
        ticks = int(Path('/proc/' + str(receipt['pid']) + '/stat').read_text().rsplit(')', 1)[1].split()[19])
        hz = os.sysconf('SC_CLK_TCK')
        if hz <= 0 or ticks / hz < exited or callback['pid'] != receipt['pid'] or callback['startTicks'] != ticks or receipt['startTicks'] != ticks or not receipt['targetMatches'] or receipt['effectCount'] != 1: raise RuntimeError('cold_callback_contract_unproved')
        callback_pid = bus('callback_owner_PID', 'GetConnectionUnixProcessID', spec['appID'])
        callback_owner = bus('callback_unique_owner', 'GetNameOwner', spec['appID'])
        if callback_pid.returncode or callback_pid.stdout.decode().strip() != '(uint32 ' + str(receipt['pid']) + ',)' or callback_owner.returncode:
            raise RuntimeError('cold_callback_bus_owner_unproved')
        report['callbackBusOwner'] = callback_owner.stdout.decode().strip()
        report['callback'] = receipt; report['kernelBirthLowerBoundBoot'] = ticks / hz
        chain = native_click_chain(trace(), report['preClickTraceCharacters'], messages(), mako_owner, gtk_owner,
            spec['appID'], nonce, int(native_id[1]), surface, entered_pointer)
        report['protocolOffsets'] = chain['offsets']
        pointer.stdin.write(b'DONE\n'); pointer.stdin.flush(); pointer.stdin.close()
        if pointer.wait(timeout=3): raise RuntimeError('pointer_cleanup_or_release_unknown')
        with (root / 'pointer.stdout').open('ab') as output: output.write(pointer.stdout.read())
        token_hash, platform, forwarded = chain['tokenSHA256'], chain['platform'], chain['forwarded']
        report.update(compositorTokenSHA256=token_hash, tokenForwarded=forwarded,
            tokenEqualityObserved=forwarded and receipt.get('activationTokenSHA256') == token_hash,
            activateSender=chain['activateSender'], frontendOwner=frontend_owner)
        if negative:
            if platform or receipt.get('activationTokenSHA256') is not None: raise RuntimeError('old_GTK_expected_omission_not_observed')
            report['outcome'] = 'old_GTK_drops_native_click_token'
        elif not report['tokenEqualityObserved']: raise RuntimeError('exact_token_forwarding_missing')
        else: report['outcome'] = 'native_click_token_reaches_cold_TEST_callback'
        wait(lambda: not Path('/proc/' + str(receipt['pid'])).exists(), 6)
        # Quiesce the only owned producers, then collect the observer's final bytes.
        for process in (gtk, frontend, mako, monitor):
            process.terminate(); process.wait(timeout=2)
        final_calls = [part for part in messages() if part.startswith('method call ') and 'interface=org.freedesktop.Application; member=ActivateAction\n' in part and 'destination=' + spec['appID'] + ' ' in part.splitlines()[0]]
        if len(final_calls) != 1 or final_calls[0].splitlines()[0] != chain['activateSender']: raise RuntimeError('additional_addressed_ActivateAction')
        final_buttons = re.findall(r'wl_pointer@' + entered_pointer + r'\.button\(\d+, \d+, 272, ([01])\)', trace())
        if final_buttons != ['1', '0']: raise RuntimeError('single_native_press_release_unproved')
        events = (root / 'action-events.jsonl').read_text().splitlines()
        if len(events) != 1 or (root / 'duplicate-rejected.json').exists() or (root / 'callback-rejected.json').exists(): raise RuntimeError('exact_one_effect_unproved')
        report['passed'] = True
    except Exception as error:
        report.update(passed=False, failure=str(error))
    finally:
        for label, child in reversed(children):
            if child.poll() is None:
                child.terminate()
                try: child.wait(timeout=2)
                except subprocess.TimeoutExpired: child.kill(); child.wait(timeout=2)
            report['processes'][next(i for i, p in enumerate(report['processes']) if p['pid'] == child.pid)]['reapedExitCode'] = child.returncode
            if label == 'pointer':
                # Retain the actual motion/click acknowledgement even on pre-click failure.
                with (root / 'pointer.stdout').open('ab') as output: output.write(child.stdout.read())
        for stream in streams: stream.close()
        report['immutableUnchanged'] = all(sha(root / name) == digest for name, digest in immutable.items())
        report['helperUnchanged'] = sha(helper) == spec['helperSHA256']
        report['showAttempted'] = (root / 'show-attempt.json').exists()
        report['addReturned'] = (root / 'submitted.json').exists()
        report['passed'] = report['passed'] and report['immutableUnchanged'] and report['helperUnchanged']
        report['tokenForwardingQualified'] = report['passed'] and report.get('tokenEqualityObserved', False)
        for name in ('wayland-packages.txt', 'packages.txt', 'portal-version.txt', 'gtk-portal-build-binding.txt'):
            shutil.copyfile('/fixture-build/' + name, root / name)
        report['artifactSHA256'] = {str(p.relative_to(root)): sha(p) for p in root.rglob('*') if p.is_file()}
        (evidence / 'native-evidence.json').write_text(json.dumps(report, indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
