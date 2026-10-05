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


def inside():
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
    env = dict(NAVIGATION_TEST_CONTAINER='1', PATH='/usr/bin:/bin:/opt/portal/libexec', HOME=str(root / 'home'), DISPLAY=':99',
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
        monitor = start('monitor', ['dbus-monitor', '--session', "type='method_call',interface='org.freedesktop.Application'", "interface='org.freedesktop.Notifications'", "interface='org.freedesktop.host.portal.Registry'"])
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
        sender = start('sender', ['/usr/bin/python3', str(helper), '--sender', str(root)])
        sender_code = sender.wait(timeout=15)  # Collected waitpid precedes the native click.
        sender_exited = now()
        report['senderExit'] = dict(pid=sender.pid, exitCode=sender_code, collected=True, exitedBoot=sender_exited)
        dump(root / 'progress.json', report)
        if sender_code != 0 or not (root / 'submitted.json').exists() or not (root / 'registry-registered.json').exists():
            raise RuntimeError('sender_failed_no_retry')
        owner = bus('cold_owner_before_click', 'org.freedesktop.DBus', '/org/freedesktop/DBus', 'org.freedesktop.DBus.GetNameOwner', spec['appID'], allow_failure=True)
        if owner.returncode == 0 or 'NameHasNoOwner' not in owner.stderr.decode() or Path('/proc/' + str(sender.pid)).exists():
            raise RuntimeError('sender_death_not_proven_no_click')
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
        # Exactly one XTest click through the owned GUI window; no action/signal synthesis.
        run('one_native_click', ['xdotool', 'mousemove', '--window', window, str(width // 2), str(height // 2), 'click', '1'])
        wait(lambda: (root / 'receipt.json').exists(), seconds=10)
        receipt = json.loads((root / 'receipt.json').read_text())
        callback = json.loads((root / 'service-start.json').read_text())
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
        effect = json.loads((root / 'effect.json').read_text())
        if effect['nonce'] != spec['nonce'] or effect['pid'] != receipt['pid'] or not effect['targetMatches']:
            raise RuntimeError('effect_identity_mismatch')
        actions = [json.loads(line) for line in (root / 'action-events.jsonl').read_text().splitlines()]
        if len(actions) != 1 or (root / 'duplicate-rejected.json').exists():
            raise RuntimeError('exact_one_effect_not_proven')
        report['callback'] = receipt
        report['callbackExited'] = True
        report['passed'] = True
    except Exception as error:
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
        report['showAttempted'] = (root / 'show-attempt.json').exists()
        report['addReturned'] = (root / 'submitted.json').exists()
        report['artifactSHA256'] = {str(path.relative_to(root)): sha(path) for path in root.rglob('*') if path.is_file()}
        report['helperUnchanged'] = sha(helper) == spec['helperSHA256']
        report['passed'] = report['passed'] and report['helperUnchanged']
        dump(evidence / 'native-evidence.json', report)
    return 0 if report['passed'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inside-test-container', action='store_true')
    parser.add_argument('--build-and-execute-test', action='store_true')
    parser.add_argument('--portal-source-archive', type=Path)
    parser.add_argument('--portal-source-sha256')
    parser.add_argument('--portal-source-provenance', type=Path)
    parser.add_argument('--docker-via-sudo', action='store_true')
    args = parser.parse_args()
    if args.inside_test_container:
        return inside()
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
                  ownerToken=token, image=image, container=container, containerUser=container_user, containerInit=True, commands=[], passed=False)

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
            '-e', 'NAVIGATION_TEST_CONTAINER=1', '-v', str(root) + ':/evidence:rw', image], 60)
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
