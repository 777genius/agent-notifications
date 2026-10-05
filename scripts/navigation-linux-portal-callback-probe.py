#!/usr/bin/env python3
"""TEST-only portal sender-death experiment, isolated in an owned Docker container."""
import argparse
import ctypes as ct
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time
import uuid

COMMIT = '1d20fadc304f6601452b5db65ed91197dba77041'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def dump(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


class XResWindowOwner:
    # Public libXres 1.2 API, verified from X.Org libXres-1.2.3 XRes.h/XRes.c.
    # Archive SHA256 d2de8f5401d6c86a8992791654547eb8def585dfdc0c08cc16e24ef6aeeb69dc.
    class Spec(ct.Structure):
        _fields_ = [('client', ct.c_ulong), ('mask', ct.c_uint)]

    def __init__(self, display_name):
        class Value(ct.Structure):
            _fields_ = [('spec', self.Spec), ('length', ct.c_long), ('value', ct.c_void_p)]
        self.Value = Value
        self.abi = dict(long=ct.sizeof(ct.c_long), xid=ct.sizeof(ct.c_ulong), spec=ct.sizeof(self.Spec),
                        value=ct.sizeof(Value), maskOffset=self.Spec.mask.offset,
                        lengthOffset=Value.length.offset, valueOffset=Value.value.offset)
        if self.abi != dict(long=8, xid=8, spec=16, value=32, maskOffset=8, lengthOffset=16, valueOffset=24):
            raise RuntimeError('unsupported_XRes_LP64_ABI_before_show')
        self.x = ct.CDLL('libX11.so.6'); self.res = ct.CDLL('libXRes.so.1')
        signatures = [(self.x.XOpenDisplay, [ct.c_char_p], ct.c_void_p),
            (self.x.XCloseDisplay, [ct.c_void_p], ct.c_int),
            (self.x.XDefaultRootWindow, [ct.c_void_p], ct.c_ulong),
            (self.x.XCreateSimpleWindow, [ct.c_void_p, ct.c_ulong, ct.c_int, ct.c_int, ct.c_uint, ct.c_uint, ct.c_uint, ct.c_ulong, ct.c_ulong], ct.c_ulong),
            (self.x.XDestroyWindow, [ct.c_void_p, ct.c_ulong], ct.c_int),
            (self.res.XResQueryExtension, [ct.c_void_p, ct.POINTER(ct.c_int), ct.POINTER(ct.c_int)], ct.c_int),
            (self.res.XResQueryVersion, [ct.c_void_p, ct.POINTER(ct.c_int), ct.POINTER(ct.c_int)], ct.c_int),
            (self.res.XResQueryClientIds, [ct.c_void_p, ct.c_long, ct.POINTER(self.Spec), ct.POINTER(ct.c_long), ct.POINTER(ct.POINTER(Value))], ct.c_int),
            (self.res.XResGetClientIdType, [ct.POINTER(Value)], ct.c_int),
            (self.res.XResGetClientPid, [ct.POINTER(Value)], ct.c_int),
            (self.res.XResClientIdsDestroy, [ct.c_long, ct.POINTER(Value)], None)]
        for function, args, result in signatures:
            function.argtypes, function.restype = args, result
        self.display = self.x.XOpenDisplay(display_name.encode())
        if not self.display:
            raise RuntimeError('private_XRes_display_unavailable_before_show')
        probe = 0
        try:
            major, minor = ct.c_int(), ct.c_int()
            event, error = ct.c_int(), ct.c_int()
            if not self.res.XResQueryExtension(self.display, ct.byref(event), ct.byref(error)) or not self.res.XResQueryVersion(self.display, ct.byref(major), ct.byref(minor)) or (major.value, minor.value) < (1, 2):
                raise RuntimeError('XRes_1_2_unavailable_before_show')
            self.version = [major.value, minor.value]
            # An unmapped, owned 1x1 preflight resource verifies server-derived PID support.
            probe = self.x.XCreateSimpleWindow(self.display, self.x.XDefaultRootWindow(self.display), 0, 0, 1, 1, 0, 0, 0)
            if not probe or self.pid(probe) != os.getpid():
                raise RuntimeError('XRes_local_client_PID_unavailable_before_show')
        except Exception:
            if probe: self.x.XDestroyWindow(self.display, probe)
            self.close(); raise
        self.x.XDestroyWindow(self.display, probe)

    def pid(self, window):
        spec, count, values = self.Spec(int(window), 2), ct.c_long(), ct.POINTER(self.Value)()
        try:
            # LOCAL_CLIENT_PID mask=2; QueryClientIds returns X11 Success=0, unlike QueryVersion.
            status = self.res.XResQueryClientIds(self.display, 1, ct.byref(spec), ct.byref(count), ct.byref(values))
            if status != 0 or count.value != 1 or not values:
                raise RuntimeError('XRes_client_ID_result_invalid')
            value = values.contents
            if value.spec.mask != 2 or value.length != 4 or not value.value or self.res.XResGetClientIdType(values) != 1:
                raise RuntimeError('XRes_PID_type_or_length_invalid')
            pid = self.res.XResGetClientPid(values)
            if pid <= 0: raise RuntimeError('XRes_PID_invalid')
            return pid
        finally:
            if values: self.res.XResClientIdsDestroy(count, values)

    def close(self):
        if self.display:
            self.x.XCloseDisplay(self.display); self.display = None


def inside(restart=False, recovery=False):
    if os.environ.get('NAVIGATION_TEST_CONTAINER') != '1' or not Path('/.dockerenv').exists():
        raise RuntimeError('owned_container_required')
    evidence = Path('/evidence')
    if not (evidence / 'container.marker').read_text().startswith('Linux portal container TEST '):
        raise RuntimeError('owned_mount_required')
    root = evidence / 'runtime'
    root.mkdir(mode=0o700)
    (root / 'fixture.marker').write_text('Linux portal TEST only\n')
    for name in ('home', 'data', 'config', 'cache', 'run'):
        (root / name).mkdir(mode=0o700)
    env = dict(NAVIGATION_TEST_CONTAINER='1', NAVIGATION_RESTART_TEST='1' if restart else '0', PATH='/usr/bin:/bin:/opt/portal/libexec', HOME=str(root / 'home'), DISPLAY=':99',
               XDG_RUNTIME_DIR=str(root / 'run'), XDG_DATA_HOME=str(root / 'data'),
               XDG_CONFIG_HOME=str(root / 'config'), XDG_CACHE_HOME=str(root / 'cache'),
               XDG_DATA_DIRS='/opt/portal/share:/usr/share', XDG_CURRENT_DESKTOP='TEST', LANG='C.UTF-8')
    helper = root / 'helper.py'
    shutil.copyfile('/fixture/navigation_linux_portal_test_app.py', helper)
    nonce = uuid.uuid4().hex
    spec = dict(appID='org.notification.NavigationTest' + nonce, nonce=nonce,
                title='Navigation TEST ' + nonce, helperSHA256=sha(helper))
    dump(root / 'spec.json', spec)
    appdir, servicedir = root / 'data/applications', root / 'data/dbus-1/services'
    appdir.mkdir(parents=True); servicedir.mkdir(parents=True)
    execute = '/usr/bin/python3 ' + str(helper) + ' --service ' + str(root)
    desktop = appdir / (spec['appID'] + '.desktop')
    desktop.write_text('[Desktop Entry]\nType=Application\nName=Navigation TEST\nDBusActivatable=true\nExec=' + execute + '\n')
    service = servicedir / (spec['appID'] + '.service')
    service.write_text('[D-BUS Service]\nName=' + spec['appID'] + '\nExec=' + execute + '\n')
    fixtures, immutable = {}, {}
    if restart:
        # Independent A/B files remain readable; never replace A's spec with B's identity.
        (root / 'fixtures').mkdir(mode=0o700)
        for label in ('A', 'B'):
            fixture = root / 'fixtures' / label; fixture.mkdir(mode=0o700)
            (fixture / 'fixture.marker').write_text('Linux portal TEST only\n')
            token = uuid.uuid4().hex
            item = dict(appID='org.notification.NavigationTest' + token, nonce=token,
                        title='Navigation TEST ' + token, helperSHA256=sha(helper))
            dump(fixture / 'spec.json', item)
            command = '/usr/bin/python3 ' + str(helper) + ' --service ' + str(fixture)
            files = [fixture / 'spec.json', appdir / (item['appID'] + '.desktop'), servicedir / (item['appID'] + '.service')]
            files[1].write_text('[Desktop Entry]\nType=Application\nName=Navigation TEST\nDBusActivatable=true\nExec=' + command + '\n')
            files[2].write_text('[D-BUS Service]\nName=' + item['appID'] + '\nExec=' + command + '\n')
            for file in files:
                file.chmod(0o400); immutable[str(file.relative_to(root))] = sha(file)
            fixtures[label] = (fixture, item)
    portalconfig = root / 'config/xdg-desktop-portal'
    portalconfig.mkdir()
    (portalconfig / 'portals.conf').write_text('[preferred]\ndefault=gtk\norg.freedesktop.impl.portal.Notification=gtk\n')
    dunstconfig = root / 'dunstrc'
    dunstconfig.write_text('[global]\norigin=top-center\nwidth=600\nheight=120\nfont=DejaVu Sans 12\nformat="%s\\n%b"\nnotification_limit=1\nmouse_left_click=do_action, close_current\n[urgency_normal]\ntimeout=0\n')
    report = dict(schemaVersion=1, passed=False, productionClientActivated=False, humanRenderQualified=False,
                  appID=spec['appID'], commands=[], processes=[], sourceSHA256={'helper': sha(helper),
                  'runner': sha(Path(__file__)), 'desktop': sha(desktop), 'service': sha(service)},
                  limitations=['Owned Xvfb/XTest click only; no human rendering acknowledgement',
                               'Backend/dunst restart, Wayland and actual client routes unqualified'])
    report['scenario'] = 'daemon_restart_recovery_control' if recovery else ('daemon_restart_owner_invalidation' if restart else 'sender_death')
    report['restartOwnerBindingQualified'] = False
    report['immutableFixtureSHA256'] = immutable
    if restart:
        report['fixtures'] = {label: dict(path=str(fixture.relative_to(root)), appID=item['appID'], nonce=item['nonce']) for label, (fixture, item) in fixtures.items()}
        report.pop('appID')
        report['limitations'][1] = 'GTK/frontend restart, Wayland and actual client routes unqualified; owned dunst restart TEST only'
    children, streams = [], []
    window_owner = None

    def run(label, argv, timeout=5, allow_failure=False):
        try:
            value = subprocess.run(argv, env=env, capture_output=True, timeout=timeout)
        except subprocess.TimeoutExpired as error:
            value = subprocess.CompletedProcess(argv, None, error.stdout or b'', error.stderr or b'')
        (root / (label + '.stdout')).write_bytes(value.stdout)
        (root / (label + '.stderr')).write_bytes(value.stderr)
        report['commands'].append(dict(label=label, argv=argv, exitCode=value.returncode,
                                       stdoutSHA256=sha(root / (label + '.stdout')),
                                       stderrSHA256=sha(root / (label + '.stderr'))))
        dump(root / 'progress.json', report)
        if value.returncode is None or (value.returncode and not allow_failure):
            raise RuntimeError(label + '_failed')
        return value

    def start(label, argv, pipe=False):
        output, errors = (root / (label + '.stdout')).open('wb'), (root / (label + '.stderr')).open('wb')
        streams.extend([output, errors])
        process = subprocess.Popen(argv, env=env, stdout=subprocess.PIPE if pipe else output, stderr=errors)
        children.append((label, process))
        report['processes'].append(dict(label=label, argv=argv, pid=process.pid))
        dump(root / 'progress.json', report)
        return process

    def wait(predicate, seconds=5):
        end = now() + seconds
        while not predicate():
            if now() >= end:
                raise RuntimeError('bounded_wait_failed')
            time.sleep(0.05)

    def bus(label, dest, path, method, *args, allow_failure=False, timeout=5):
        return run(label, ['gdbus', 'call', '--session', '--dest', dest, '--object-path', path,
                           '--method', method, *args], allow_failure=allow_failure, timeout=timeout)

    def wait_owned_name(label, name, process):
        end, attempt = now() + 5, 0
        while now() < end:
            if process.poll() is not None:
                raise RuntimeError(label + '_exited_before_name_ready')
            attempt += 1
            # Driver lookup never autoactivates the queried well-known service name.
            owner = bus(label + '_owner_ready_' + str(attempt), 'org.freedesktop.DBus',
                        '/org/freedesktop/DBus', 'org.freedesktop.DBus.GetConnectionUnixProcessID',
                        name, allow_failure=True, timeout=max(0.05, min(1, end - now())))
            if owner.returncode == 0:
                match = re.fullmatch(r'\(uint32 ([0-9]+),\)\s*', owner.stdout.decode())
                if not match or int(match[1]) != process.pid or process.poll() is not None:
                    raise RuntimeError(label + '_unexpected_owner_before_show')
                return
            if 'org.freedesktop.DBus.Error.NameHasNoOwner' not in owner.stderr.decode():
                raise RuntimeError(label + '_owner_lookup_failed_before_show')
            time.sleep(min(0.05, max(0, end - now())))
        raise RuntimeError(label + '_name_readiness_timeout_before_show')

    def unique_owner(label, name, process):
        wait_owned_name(label, name, process)
        value = bus(label + '_unique', 'org.freedesktop.DBus', '/org/freedesktop/DBus', 'org.freedesktop.DBus.GetNameOwner', name)
        match = re.fullmatch(r"\('(:[0-9]+\.[0-9]+)',\)\s*", value.stdout.decode())
        if not match or process.poll() is not None:
            raise RuntimeError(label + '_unique_owner_not_proven')
        return match[1]

    def start_ticks(process):
        if process.poll() is not None:
            raise RuntimeError('owned_process_exited')
        return int(Path('/proc/' + str(process.pid) + '/stat').read_text().rsplit(')', 1)[1].split()[19])

    def messages():
        text = (root / 'monitor.stdout').read_text(errors='strict')
        return [part for part in re.split(r'(?m)(?=^(?:method call|method return|signal|error) time=)', text) if part.strip()]

    def notify_reply(label, item, owner, gtk_owner):
        found = []
        def lookup():
            nonlocal found
            calls = [part for part in messages() if part.startswith('method call ') and
                     'interface=org.freedesktop.Notifications; member=Notify\n' in part and
                     'sender=' + gtk_owner + ' ' in part.splitlines()[0] and
                     'string "' + item['title'] + '"' in part and 'string "' + item['appID'] + '"' in part]
            if len(calls) > 1:
                raise RuntimeError('duplicate_native_Notify_no_retry')
            if not calls: return False
            serial = re.search(r' serial=([0-9]+) ', calls[0].splitlines()[0])
            if not serial: raise RuntimeError('Notify_serial_missing')
            replies = [part for part in messages() if part.startswith('method return ') and
                       'sender=' + owner + ' ' in part.splitlines()[0] and
                       'destination=' + gtk_owner + ' ' in part.splitlines()[0] and
                       re.search(r'\breply_serial=' + serial[1] + r'(?:\s|$)', part.splitlines()[0])]
            if len(replies) > 1: raise RuntimeError('ambiguous_Notify_reply')
            if not replies: return False
            value = re.fullmatch(r'\s+uint32 ([0-9]+)\s*', '\n'.join(replies[0].splitlines()[1:]))
            if not value or int(value[1]) <= 0: raise RuntimeError('Notify_ID_reply_invalid')
            found = dict(id=int(value[1]), requestSerial=int(serial[1]), requestSender=gtk_owner, replySender=owner,
                         requestSHA256=hashlib.sha256(calls[0].encode()).hexdigest(), replySHA256=hashlib.sha256(replies[0].encode()).hexdigest())
            return True
        wait(lookup)
        report[label + 'NativeNotification'] = found
        dump(root / 'progress.json', report)
        return found['id']

    def send_fixture(label, fixture, item):
        process = start(label, ['/usr/bin/python3', str(helper), '--sender', str(fixture)])
        code = process.wait(timeout=15); exited = now()
        report[label + 'Exit'] = dict(pid=process.pid, exitCode=code, collected=True, exitedBoot=exited)
        dump(root / 'progress.json', report)
        if code != 0 or not (fixture / 'submitted.json').exists() or not (fixture / 'registry-registered.json').exists():
            raise RuntimeError(label + '_failed_no_retry')
        value = bus(label + '_cold_owner', 'org.freedesktop.DBus', '/org/freedesktop/DBus', 'org.freedesktop.DBus.GetNameOwner', item['appID'], allow_failure=True)
        if value.returncode == 0 or 'NameHasNoOwner' not in value.stderr.decode() or Path('/proc/' + str(process.pid)).exists():
            raise RuntimeError(label + '_death_not_proven')
        return process, exited

    def restart_window(label, owned):
        value = run(label + '_window', ['xdotool', 'search', '--sync', '--onlyvisible', '--class', '^Dunst$']).stdout.decode().split()
        if len(value) != 1 or not value[0].isdigit() or window_owner.pid(value[0]) != owned.pid or owned.poll() is not None:
            raise RuntimeError(label + '_window_owner_not_proven')
        run(label + '_screenshot', ['import', '-window', value[0], str(root / (label + '.png'))])
        report[label + 'Window'] = dict(windowID=value[0], serverDerivedPID=owned.pid, screenshotSHA256=sha(root / (label + '.png')))
        return value[0]

    def restart_scenario(dunst, backend, frontend):
        first_owner = unique_owner('daemon_A', 'org.freedesktop.Notifications', dunst)
        gtk_owner = unique_owner('gtk_restart', 'org.freedesktop.impl.portal.desktop.gtk', backend)
        retained = {label: dict(pid=process.pid, startTicks=start_ticks(process)) for label, process in [('gtk', backend), ('frontend', frontend)]}
        a_root, a_spec = fixtures['A']; b_root, b_spec = fixtures['B']
        send_fixture('senderA', a_root, a_spec)
        first_id = notify_reply('A', a_spec, first_owner, gtk_owner)
        restart_window('A', dunst)
        # Crash only this exact live Popen child; graceful close would erase GTK's old map.
        if unique_owner('daemon_A_precrash', 'org.freedesktop.Notifications', dunst) != first_owner:
            raise RuntimeError('daemon_A_owner_changed_before_crash')
        dunst.kill(); code = dunst.wait(timeout=3)
        report['daemonACrash'] = dict(pid=dunst.pid, collected=True, exitCode=code, exitedBoot=now())
        if code != -9 or Path('/proc/' + str(dunst.pid)).exists(): raise RuntimeError('own_crash_not_proven')
        second = start('dunst_B', ['dunst', '-config', str(dunstconfig)])
        second_owner = unique_owner('daemon_B', 'org.freedesktop.Notifications', second)
        if second_owner == first_owner: raise RuntimeError('daemon_owner_change_not_proven')
        report['daemonOwners'] = dict(before=first_owner, after=second_owner, beforePID=dunst.pid, afterPID=second.pid)
        sender_b, exited_b = send_fixture('senderB', b_root, b_spec)
        second_id = notify_reply('B', b_spec, second_owner, gtk_owner)
        report['numericIDReused'] = second_id == first_id
        if not recovery and second_id != first_id: raise RuntimeError('inconclusive_numeric_ID_not_reused')
        window = restart_window('B', second)
        for label, process in [('gtk', backend), ('frontend', frontend)]:
            if retained[label] != dict(pid=process.pid, startTicks=start_ticks(process)):
                raise RuntimeError('backend_or_frontend_restarted')
        if unique_owner('gtk_retained', 'org.freedesktop.impl.portal.desktop.gtk', backend) != gtk_owner:
            raise RuntimeError('gtk_owner_changed')
        report['retainedProcesses'] = retained
        if recovery:
            report['outcome'] = 'restart_recovery_pending_B_click'
            return second, b_root, b_spec, sender_b, exited_b, False
        if window_owner.pid(window) != second.pid or unique_owner('daemon_B_preremove', 'org.freedesktop.Notifications', second) != second_owner:
            raise RuntimeError('daemon_B_identity_changed_before_remove')
        remover = start('removeA', ['/usr/bin/python3', str(helper), '--remove', str(a_root)])
        remove_code = remover.wait(timeout=10)
        report['removeAExit'] = dict(pid=remover.pid, exitCode=remove_code, collected=True)
        if remove_code != 0 or not (a_root / 'removed.json').exists(): raise RuntimeError('remove_A_failed_no_retry')
        wait(lambda: any(part.startswith('method call ') and
             'interface=org.freedesktop.portal.Notification; member=RemoveNotification\n' in part and
             re.fullmatch(r'\s+string "' + a_spec['nonce'] + r'"\s*', '\n'.join(part.splitlines()[1:])) for part in messages()))
        removal = [part for part in messages() if part.startswith('method call ') and
                   'interface=org.freedesktop.portal.Notification; member=RemoveNotification\n' in part and
                   re.fullmatch(r'\s+string "' + a_spec['nonce'] + r'"\s*', '\n'.join(part.splitlines()[1:]))]
        if len(removal) != 1: raise RuntimeError('official_remove_A_not_unique')
        remove_peer = re.search(r' sender=(:[0-9]+\.[0-9]+) ', removal[0].splitlines()[0])
        registration = [part for part in messages() if remove_peer and part.startswith('method call ') and
                        'sender=' + remove_peer[1] + ' ' in part.splitlines()[0] and
                        'interface=org.freedesktop.host.portal.Registry; member=Register\n' in part and
                        'string "' + a_spec['appID'] + '"' in part]
        if len(registration) != 1: raise RuntimeError('remove_A_registered_peer_not_proven')
        report['officialRemoveA'] = dict(peer=remove_peer[1], appID=a_spec['appID'], nonce=a_spec['nonce'])
        close, close_serial = [], None
        def close_request():
            nonlocal close, close_serial
            close = [part for part in messages() if part.startswith('method call ') and
                     'interface=org.freedesktop.Notifications; member=CloseNotification\n' in part]
            if len(close) > 1: raise RuntimeError('duplicate_CloseNotification')
            if not close: return False
            header = close[0].splitlines()[0]
            if 'sender=' + gtk_owner + ' ' not in header or not ('destination=' + second_owner + ' ' in header or 'destination=org.freedesktop.Notifications ' in header) or not re.fullmatch(r'\s+uint32 ' + str(second_id) + r'\s*', '\n'.join(close[0].splitlines()[1:])):
                raise RuntimeError('observed_CloseNotification_identity_or_ID_mismatch')
            serial = re.search(r' serial=([0-9]+) ', close[0].splitlines()[0])
            if not serial: raise RuntimeError('CloseNotification_serial_missing')
            close_serial = int(serial[1])
            return True

        def optional_close_replies():
            replies = [part for part in messages() if close_serial is not None and part.startswith('method return ') and
                       'sender=' + second_owner + ' ' in part.splitlines()[0] and
                       'destination=' + gtk_owner + ' ' in part.splitlines()[0] and
                       re.search(r'\breply_serial=' + str(close_serial) + r'(?:\s|$)', part.splitlines()[0])]
            if len(replies) > 1 or any('\n'.join(part.splitlines()[1:]).strip() for part in replies):
                raise RuntimeError('CloseNotification_optional_reply_invalid')
            return replies
        close_observation_started = now()
        try:
            wait(close_request, seconds=2)
        except RuntimeError as error:
            # Correct suppression can produce no native Close. GTK's callback=NULL
            # uses NO_REPLY_EXPECTED; actual close effects are observed separately.
            if str(error) != 'bounded_wait_failed' or close:
                raise
        report['nativeCloseA'] = dict(observed=bool(close), newOwner=second_owner, numericID=second_id,
                                     closeRequestSerial=close_serial,
                                     requestSHA256=hashlib.sha256(close[0].encode()).hexdigest() if close else None,
                                     observationStartedBoot=close_observation_started, observationEndedBoot=now(),
                                     replyExpectedFromSource=False,
                                     replyPolicyEvidence='GTK1.15.1 call_close callback=NULL; GLib2.80 public call uses NO_REPLY_EXPECTED',
                                     absenceScope='bounded_monitor_observation_only; future_effects_unqualified')
        observation_started, end, attempt = now(), now() + 2, 0
        while True:
            attempt += 1
            visible = run('B_after_remove_window_' + str(attempt), ['xdotool', 'search', '--onlyvisible', '--class', '^Dunst$'], allow_failure=True, timeout=1)
            closed = [part for part in messages() if part.startswith('signal ') and 'sender=' + second_owner + ' ' in part.splitlines()[0] and
                      'destination=' + gtk_owner + ' ' in part.splitlines()[0] and
                      'interface=org.freedesktop.Notifications; member=NotificationClosed\n' in part and
                      re.fullmatch(r'\s+uint32 ' + str(second_id) + r'\s+uint32 3\s*', '\n'.join(part.splitlines()[1:]))]
            if second.poll() is not None: raise RuntimeError('daemon_B_exited_during_remove')
            if (visible.returncode == 1 and not visible.stdout.strip() and len(closed) == 1) or now() >= end: break
            if visible.returncode not in (0, 1): raise RuntimeError('B_window_query_unknown')
            time.sleep(min(0.05, max(0, end - now())))
        replies = optional_close_replies()
        report['nativeCloseA'].update(optionalReplyCount=len(replies), optionalReplySHA256=hashlib.sha256(replies[0].encode()).hexdigest() if replies else None)
        report['removeObservation'] = dict(startedBoot=observation_started, endedBoot=now(), closedSignals=len(closed),
                                          closedSignalSHA256=hashlib.sha256(closed[0].encode()).hexdigest() if len(closed) == 1 else None,
                                          closedSignalDestination=gtk_owner if len(closed) == 1 else None, visibleWindows=visible.stdout.decode().split())
        for label, process in [('gtk', backend), ('frontend', frontend)]:
            if retained[label] != dict(pid=process.pid, startTicks=start_ticks(process)):
                raise RuntimeError('backend_or_frontend_changed_after_remove')
        if unique_owner('daemon_B_postremove', 'org.freedesktop.Notifications', second) != second_owner:
            raise RuntimeError('daemon_B_owner_changed_after_remove')
        if visible.returncode == 1 and not visible.stdout.strip() and len(closed) == 1 and close:
            if any((fixture / name).exists() for fixture, _ in fixtures.values() for name in ('service-start.json', 'effect.json', 'receipt.json', 'action-events.jsonl')):
                raise RuntimeError('unexpected_callback_during_remove')
            report.update(outcome='incorrect_close_B_counterexample', passedScope='owned_restart_counterexample', passed=True)
            return second, b_root, b_spec, sender_b, exited_b, True
        if visible.returncode != 0 or visible.stdout.decode().split() != [window] or closed:
            raise RuntimeError('B_visibility_after_remove_inconclusive')
        if close and not replies:
            report['outcome'] = 'observed_Close_B_survived_effect_unknown'
            raise RuntimeError('observed_Close_without_reply_or_close_effect_no_click')
        report['outcome'] = 'B_survived_remove_A_pending_click'
        return second, b_root, b_spec, sender_b, exited_b, False

    try:
        for file in Path('/fixture-build').glob('*version.txt'):
            report[file.stem] = file.read_text().strip()
        report['aptCandidates'] = Path('/fixture-build/apt-candidates.txt').read_text()
        shutil.copyfile('/fixture-build/packages.txt', root / 'packages.txt')
        if '1.22.1' not in report.get('portal-version', '') or report.get('meson-version') != '1.12.1':
            raise RuntimeError('build_version_mismatch_before_show')
        display = start('xvfb', ['Xvfb', ':99', '-screen', '0', '1024x768x24', '-nolisten', 'tcp'])
        wait(lambda: Path('/tmp/.X11-unix/X99').exists())
        if 'XTEST' not in run('x_extensions', ['xdpyinfo', '-queryExtensions']).stdout.decode():
            raise RuntimeError('XTEST_unavailable_before_show')
        window_owner = XResWindowOwner(env['DISPLAY'])
        report['XResPreflight'] = dict(version=window_owner.version, abi=window_owner.abi, ownPIDVerified=os.getpid())
        daemon = start('bus', ['dbus-daemon', '--session', '--nofork', '--print-address=1'], pipe=True)
        address = daemon.stdout.readline().decode().strip()
        if not address.startswith('unix:'):
            raise RuntimeError('private_bus_unavailable_before_show')
        env['DBUS_SESSION_BUS_ADDRESS'] = address
        monitor = start('monitor', ['dbus-monitor', '--session', "type='method_call',interface='org.freedesktop.Application'", "interface='org.freedesktop.Notifications'", "interface='org.freedesktop.host.portal.Registry'"] + (["type='method_return'", "type='method_call',interface='org.freedesktop.portal.Notification'"] if restart else []))
        dunst = start('dunst', ['dunst', '-config', str(dunstconfig)])
        wait_owned_name('dunst', 'org.freedesktop.Notifications', dunst)
        backend = start('gtk_backend', ['/usr/libexec/xdg-desktop-portal-gtk'])
        wait_owned_name('gtk_backend', 'org.freedesktop.impl.portal.desktop.gtk', backend)
        frontend = start('frontend', ['/opt/portal/libexec/xdg-desktop-portal', '--verbose'])
        wait_owned_name('frontend', 'org.freedesktop.portal.Desktop', frontend)
        # Public frontend introspection only; never call the internal backend directly.
        registry = run('registry_introspection', ['gdbus', 'introspect', '--session', '--dest',
            'org.freedesktop.portal.Desktop', '--object-path', '/org/freedesktop/portal/desktop'])
        if 'org.freedesktop.host.portal.Registry' not in registry.stdout.decode() or 'Register' not in registry.stdout.decode():
            raise RuntimeError('Registry_unavailable_before_show')
        capabilities = bus('notification_capabilities', 'org.freedesktop.Notifications', '/org/freedesktop/Notifications', 'org.freedesktop.Notifications.GetCapabilities')
        if "'actions'" not in capabilities.stdout.decode():
            raise RuntimeError('native_actions_unavailable_before_show')
        for label, name, owned in [('frontend', 'org.freedesktop.portal.Desktop', frontend),
                                    ('backend', 'org.freedesktop.impl.portal.desktop.gtk', backend),
                                    ('notification_server', 'org.freedesktop.Notifications', dunst)]:
            actual = bus(label + '_bus_owner', 'org.freedesktop.DBus', '/org/freedesktop/DBus',
                         'org.freedesktop.DBus.GetConnectionUnixProcessID', name)
            match = re.search(r'uint32 ([0-9]+)', actual.stdout.decode())
            if not match or int(match[1]) != owned.pid or owned.poll() is not None:
                raise RuntimeError(label + '_not_owned_live_before_show')
        if any(process.poll() is not None for _, process in children):
            raise RuntimeError('owned_background_exited_before_show')
        callback_root, closed = root, False
        if restart:
            dunst, callback_root, spec, sender, sender_exited, closed = restart_scenario(dunst, backend, frontend)
        else:
            sender, sender_exited = send_fixture('sender', root, spec)
        if not closed:
            window = run('owned_window', ['xdotool', 'search', '--sync', '--onlyvisible', '--class', '^Dunst$'], timeout=5).stdout.decode().split()
            if len(window) != 1 or not window[0].isdigit():
                raise RuntimeError('owned_notification_window_not_unique_no_retry')
            window = window[0]
            properties = run('window_properties', ['xprop', '-id', window, '_NET_WM_PID', 'WM_CLASS'])
            # Class is discovery only; identity comes from the private X server, not WM properties.
            owner_pid = window_owner.pid(window)
            report['windowOwnership'] = dict(windowID=window, serverDerivedPID=owner_pid, method='XRes_LOCAL_CLIENT_PID')
            dump(root / 'progress.json', report)
            if owner_pid != dunst.pid or dunst.poll() is not None:
                raise RuntimeError('window_owner_not_proven_no_click')
            geometry = run('window_geometry', ['xdotool', 'getwindowgeometry', '--shell', window]).stdout.decode()
            dimensions = dict(re.findall(r'^(WIDTH|HEIGHT)=([0-9]+)$', geometry, re.MULTILINE))
            width, height = int(dimensions.get('WIDTH', 0)), int(dimensions.get('HEIGHT', 0))
            if not (20 <= width <= 1024 and 20 <= height <= 768):
                raise RuntimeError('owned_window_geometry_invalid_no_click')
            run('screenshot', ['import', '-window', window, str(root / 'pre-click.png')])
            report['nativeClick'] = dict(windowID=window, ownerPID=dunst.pid, afterSenderExit=True, screenshotSHA256=sha(root / 'pre-click.png'))
            dump(root / 'progress.json', report)
            if window_owner.pid(window) != dunst.pid or dunst.poll() is not None:
                raise RuntimeError('window_owner_changed_before_click')
            if restart and not recovery and not report['nativeCloseA']['observed'] and any(part.startswith('method call ') and 'interface=org.freedesktop.Notifications; member=CloseNotification\n' in part for part in messages()):
                raise RuntimeError('late_CloseNotification_after_absence_observation_no_click')
            # Exactly one XTest click through the owned GUI window; no action/signal synthesis.
            run('one_native_click', ['xdotool', 'mousemove', '--window', window, str(width // 2), str(height // 2), 'click', '1'])
            wait(lambda: (callback_root / 'receipt.json').exists(), seconds=10)
            receipt = json.loads((callback_root / 'receipt.json').read_text())
            callback = json.loads((callback_root / 'service-start.json').read_text())
            live_pid = bus('callback_owner_pid', 'org.freedesktop.DBus', '/org/freedesktop/DBus', 'org.freedesktop.DBus.GetConnectionUnixProcessID', spec['appID'])
            owner_pid = re.search(r'uint32 ([0-9]+)', live_pid.stdout.decode())
            current_ticks = int(Path('/proc/' + str(receipt['pid']) + '/stat').read_text().rsplit(')', 1)[1].split()[19])
            if not owner_pid or int(owner_pid[1]) != receipt['pid'] or receipt['pid'] == sender.pid or callback['entryBoot'] <= sender_exited or callback['pid'] != receipt['pid'] or callback['startTicks'] != current_ticks or receipt['startTicks'] != current_ticks:
                raise RuntimeError('cold_service_identity_not_proven')
            # /proc starttime is a kernel BOOTTIME birth timestamp, quantized to clock ticks.
            # Its lower bound must follow collected waitpid; overlapping ticks are inconclusive.
            hz = os.sysconf('SC_CLK_TCK')
            if hz <= 0:
                raise RuntimeError('kernel_tick_frequency_invalid')
            birth_lower = current_ticks / hz
            report['kernelBirthProof'] = dict(startTicks=current_ticks, clockTicksPerSecond=hz,
                lowerBoundBoot=birth_lower, upperBoundBoot=(current_ticks + 1) / hz,
                senderExitedBoot=sender_exited, afterCollectedSenderExit=birth_lower >= sender_exited,
                clockScope='kernel_proc_starttime_and_CLOCK_BOOTTIME; code_entry_is_not_birth')
            dump(root / 'progress.json', report)
            if birth_lower < sender_exited:
                raise RuntimeError('kernel_birth_not_proven_after_sender_exit_tick_ambiguous')
            if receipt['helperSHA256'] != spec['helperSHA256'] or not receipt['targetMatches'] or receipt['effectCount'] != 1 or receipt['enteredBoot'] - callback['entryBoot'] >= 15:
                raise RuntimeError('callback_contract_rejected')
            report['callback'] = receipt
            try:
                wait(lambda: not Path('/proc/' + str(receipt['pid'])).exists(), seconds=6)
            except RuntimeError:
                observation = dict(pid=receipt['pid'], expectedStartTicks=current_ticks, observedBoot=now())
                try:
                    fields = Path('/proc/' + str(receipt['pid']) + '/stat').read_text().rsplit(')', 1)[1].split()
                    observation['observedStartTicks'] = int(fields[19])
                    observation['identityMatches'] = observation['observedStartTicks'] == current_ticks
                    if observation['identityMatches']:
                        observation.update(state=fields[0], parentPID=int(fields[1]))
                except (OSError, ValueError, IndexError) as error:
                    observation['readFailure'] = type(error).__name__
                report['callbackExitObservation'] = observation
                dump(root / 'progress.json', report)
                raise
            effect = json.loads((callback_root / 'effect.json').read_text())
            if effect['nonce'] != spec['nonce'] or effect['pid'] != receipt['pid'] or not effect['targetMatches']:
                raise RuntimeError('effect_identity_mismatch')
            actions = [json.loads(line) for line in (callback_root / 'action-events.jsonl').read_text().splitlines()]
            if len(actions) != 1 or (callback_root / 'duplicate-rejected.json').exists():
                raise RuntimeError('exact_one_effect_not_proven')
            report['callback'] = receipt
            report['callbackExited'] = True
            report['passed'] = True
        if restart and not closed:
            if any((fixtures['A'][0] / name).exists() for name in ('service-start.json', 'effect.json', 'receipt.json', 'action-events.jsonl')):
                raise RuntimeError('A_callback_after_restart')
            report['outcome'] = 'restart_recovery_one_B_callback' if recovery else 'B_survived_remove_A_and_one_B_callback'
            report['passedScope'] = 'owned_restart_recovery_control' if recovery else 'owned_restart_and_recovery_control'
            report['restartRecoveryControlPassed'] = True
        if restart:
            notify_count = sum(part.startswith('method call ') and 'interface=org.freedesktop.Notifications; member=Notify\n' in part for part in messages())
            remove_count = sum(part.startswith('method call ') and 'interface=org.freedesktop.portal.Notification; member=RemoveNotification\n' in part for part in messages())
            report['counts'] = dict(nativeNotify=notify_count, officialRemove=remove_count,
                                   nativeClick=0 if closed else 1, expectedNotify=2, expectedRemove=0 if recovery else 1)
            if notify_count != 2 or remove_count != (0 if recovery else 1):
                raise RuntimeError('restart_transport_count_mismatch')
    except Exception as error:
        report['passed'] = False
        report['failure'] = str(error)
        report['retryAllowed'] = False
    finally:
        if window_owner is not None:
            window_owner.close()
        for label, process in reversed(children):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait(timeout=2)
            report['processes'][next(i for i, p in enumerate(report['processes']) if p['pid'] == process.pid)]['reapedExitCode'] = process.returncode
        for stream in streams:
            stream.close()
        report['showAttempted'] = any(path.exists() for path in ([fixture / 'show-attempt.json' for fixture, _ in fixtures.values()] if restart else [root / 'show-attempt.json']))
        report['addReturned'] = any(path.exists() for path in ([fixture / 'submitted.json' for fixture, _ in fixtures.values()] if restart else [root / 'submitted.json']))
        report['artifactSHA256'] = {str(path.relative_to(root)): sha(path) for path in root.rglob('*') if path.is_file()}
        report['helperUnchanged'] = sha(helper) == spec['helperSHA256']
        report['immutableFixturesUnchanged'] = all(sha(root / name) == digest for name, digest in immutable.items())
        report['passed'] = report['passed'] and report['helperUnchanged'] and report['immutableFixturesUnchanged']
        dump(evidence / 'native-evidence.json', report)
    return 0 if report['passed'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside-test-container', action='store_true')
    parser.add_argument('--build-and-execute-test', action='store_true')
    restart_modes = parser.add_mutually_exclusive_group()
    restart_modes.add_argument('--restart-owner-invalidation-test', action='store_true')
    restart_modes.add_argument('--restart-recovery-control-test', action='store_true')
    parser.add_argument('--portal-source-archive', type=Path)
    parser.add_argument('--portal-source-sha256')
    parser.add_argument('--portal-source-provenance', type=Path)
    parser.add_argument('--docker-via-sudo', action='store_true')
    args = parser.parse_args()
    if args.inside_test_container:
        return inside(restart=os.environ.get('NAVIGATION_RESTART_TEST') == '1', recovery=os.environ.get('NAVIGATION_RESTART_RECOVERY') == '1')
    if not args.build_and_execute_test or not args.portal_source_archive or not args.portal_source_provenance or not re.fullmatch(r'[0-9a-f]{64}', args.portal_source_sha256 or ''):
        parser.error('explicit TEST opt-in and primary-verified portal1.22.1 archive/checksum required')
    if sha(args.portal_source_archive) != args.portal_source_sha256:
        parser.error('portal source checksum mismatch')
    provenance = json.loads(args.portal_source_provenance.read_text())
    if provenance.get('commit') != COMMIT or provenance.get('archiveSHA256') != args.portal_source_sha256 or provenance.get('version') != '1.22.1' or not str(provenance.get('ghCommand', '')).startswith('gh '):
        parser.error('primary gh source provenance/checksum binding required')
    repo = Path(__file__).resolve().parents[1]
    root = Path(tempfile.mkdtemp(prefix='navigation-linux-portal-test-')).resolve()
    os.chmod(root, 0o700)
    context = root / 'context'; context.mkdir(mode=0o700)
    names = {'navigation-linux-portal-callback-probe.py': Path(__file__),
             'navigation_linux_portal_test_app.py': repo / 'tests/integration/navigation_linux_portal_test_app.py',
             'Dockerfile': repo / 'tests/integration/navigation-linux-portal.Dockerfile'}
    for name, path in names.items():
        shutil.copyfile(path, context / name)
    shutil.copyfile(args.portal_source_archive, context / 'portal-source.tar.xz')
    shutil.copyfile(args.portal_source_provenance, context / 'portal-source-provenance.json')
    token = uuid.uuid4().hex
    (root / 'container.marker').write_text('Linux portal container TEST ' + token + '\n')
    docker = ['sudo', '-n', 'docker'] if args.docker_via_sudo else ['docker']
    image, container = 'navigation-portal-test:' + token, 'navigation-portal-test-' + token
    container_user = str(os.getuid()) + ':' + str(os.getgid())
    report = dict(sourceSHA256={name: sha(context / name) for name in names}, portalSourceSHA256=args.portal_source_sha256,
                  portalSourceDeclaredCommit=COMMIT, sourceProvenance=provenance,
                  ownerToken=token, image=image, container=container, containerUser=container_user, containerInit=True,
                  scenario='daemon_restart_recovery_control' if args.restart_recovery_control_test else ('daemon_restart_owner_invalidation' if args.restart_owner_invalidation_test else 'sender_death'), commands=[], passed=False)

    def command(label, argv, timeout):
        try:
            value = subprocess.run(argv, capture_output=True, timeout=timeout)
        except subprocess.TimeoutExpired as error:
            value = subprocess.CompletedProcess(argv, None, error.stdout or b'', error.stderr or b'')
        (root / (label + '.stdout')).write_bytes(value.stdout); (root / (label + '.stderr')).write_bytes(value.stderr)
        report['commands'].append(dict(label=label, argv=argv, exitCode=value.returncode,
            stdoutSHA256=sha(root / (label + '.stdout')), stderrSHA256=sha(root / (label + '.stderr'))))
        dump(root / 'build-progress.json', report)
        return value

    try:
        build = command('build', docker + ['build', '--label', 'navigation.test=true', '--label', 'navigation.owner=' + token,
            '--build-arg', 'PORTAL_SOURCE_SHA256=' + args.portal_source_sha256, '--build-arg', 'PORTAL_SOURCE_COMMIT=' + COMMIT,
            '-t', image, str(context)], 1800)
        if build.returncode != 0:
            raise RuntimeError('image_build_failed_before_show')
        report['imageID'] = command('image_identity', docker + ['image', 'inspect', image, '--format', '{{.Id}}'], 10).stdout.decode().strip()
        result = command('run', docker + ['run', '--init', '--name', container, '--user', container_user, '--label', 'navigation.test=true', '--label', 'navigation.owner=' + token,
            '--network=none', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--pids-limit=128', '--memory=2g', '--cpus=2',
            '-e', 'NAVIGATION_TEST_CONTAINER=1', '-e', 'NAVIGATION_RESTART_TEST=' + ('1' if args.restart_owner_invalidation_test or args.restart_recovery_control_test else '0'),
            '-e', 'NAVIGATION_RESTART_RECOVERY=' + ('1' if args.restart_recovery_control_test else '0'), '-v', str(root) + ':/evidence:rw', image], 60)
        report['passed'] = result.returncode == 0 and json.loads((root / 'native-evidence.json').read_text()).get('passed') is True
    except Exception as error:
        report['failure'] = str(error); report['retryAllowed'] = False
    finally:
        cleanup = dict(verified=False)
        def absent(value, exact):
            diagnostic = value.stderr.decode(errors='replace').strip()
            pattern = r'Error(?: response from daemon)?: No such (?:object|container): ' + re.escape(exact)
            return value.returncode == 1 and not value.stdout.strip() and re.fullmatch(pattern, diagnostic) is not None

        try:
            proof = command('container_owner', docker + ['container', 'inspect', '--format',
                '{{.Id}} {{index .Config.Labels "navigation.owner"}}', container], 10)
            if absent(proof, container):
                cleanup.update(verified=True, status='exact_name_already_absent')
            elif proof.returncode != 0:
                cleanup.update(status='inspect_unknown', cleanupUnknown=True)
            else:
                match = re.fullmatch(r'([0-9a-f]{64}) ' + re.escape(token), proof.stdout.decode().strip())
                if not match:
                    cleanup.update(status='owner_identity_mismatch', cleanupFailed=True)
                else:
                    owned_id = match[1]
                    cleanup['ownedContainerID'] = owned_id
                    removed = command('own_container_remove', docker + ['rm', '-f', owned_id], 10)
                    if removed.returncode != 0:
                        cleanup.update(status='remove_failed_or_unknown', cleanupFailed=True)
                    else:
                        by_id = command('container_absent_by_id', docker + ['container', 'inspect', '--format', '{{.Id}}', owned_id], 10)
                        by_name = command('container_absent_by_name', docker + ['container', 'inspect', '--format', '{{.Id}}', container], 10)
                        if absent(by_id, owned_id) and absent(by_name, container):
                            cleanup.update(verified=True, status='owned_id_removed_and_exact_name_absent')
                        else:
                            cleanup.update(status='fresh_absence_not_proven', cleanupUnknown=True)
        except Exception as error:
            cleanup.update(status='cleanup_exception', cleanupUnknown=True, error=str(error))
        report['containerCleanup'] = cleanup
        report['passed'] = report['passed'] and cleanup['verified']
        report['snapshotUnchanged'] = all(sha(context / name) == digest for name, digest in report['sourceSHA256'].items())
        report['passed'] = report['passed'] and report['snapshotUnchanged']
        dump(root / 'evidence.json', report)
        print(json.dumps({'evidence': str(root / 'evidence.json'), 'passed': report['passed']}))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
