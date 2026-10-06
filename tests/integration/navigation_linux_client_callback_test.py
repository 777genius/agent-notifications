#!/usr/bin/python3
"""Cold Gio callback in an offline TEST guest; one selected vendor-launcher attempt."""
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import socket
import stat
import struct
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path('/var/lib/navigation-client-handoff-TEST')
SEED = Path('/mnt/navigation-handoff-test-seed')
LAUNCHER = Path('/usr/lib/chatgpt/codex-launcher')
EXE = Path('/usr/lib/chatgpt/ChatGPT')
LAUNCHER_SHA = '8f983245c6c07070e2cdc480be50ec239e0f18ee36069126649d0692595c86ad'
EXE_SHA = '207c4fbff7e2fcc1b0789448351ac6eed206206d94c5a0835e5f07c7cd73d6e3'


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for data in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(data)
    return h.hexdigest()


def birth(pid):
    return int(Path('/proc', str(pid), 'stat').read_text().rsplit(')', 1)[1].split()[19])


def identity():
    return dict(pid=os.getpid(), startTicks=birth(os.getpid()), observedBoot=now())


def publish(name, value):
    directory = ROOT / 'session/callback'
    with tempfile.NamedTemporaryFile(mode='w', dir=directory) as stream:
        json.dump(value, stream); stream.flush(); os.fsync(stream.fileno())
        os.link(stream.name, directory / name)  # Atomic exclusive publication; never overwrite an attempt.


def invocation(nonce, token):
    if not isinstance(nonce, str) or not re.fullmatch(r'[0-9a-f]{32}', nonce):
        raise ValueError('invalid_TEST_nonce')
    if not isinstance(token, str) or not token or '\0' in token or len(token.encode()) > 4096:
        raise ValueError('invalid_click_token')
    return [str(LAUNCHER), '--ozone-platform=wayland', 'codex://threads/' + str(uuid.UUID(hex=nonce))]


def private_bus_address(address, path):
    expected = ROOT / 'session/runtime/bus.sock'
    if path != expected or not isinstance(address, str) or not re.fullmatch(
            re.escape('unix:path=' + str(expected) + ',guid=') + r'[0-9a-f]{32}', address):
        raise ValueError('invalid_private_bus_address')


def peer(path, expected_pid, expected_birth):
    fd = os.pidfd_open(expected_pid, 0)
    try:
        if birth(expected_pid) != expected_birth:
            raise RuntimeError('peer_incarnation_changed')
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(2); connection.connect(str(path))
            observed = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        with selectors.DefaultSelector() as selector:
            selector.register(fd, selectors.EVENT_READ)
            if selector.select(0) or observed[:2] != (expected_pid, 1000):
                raise RuntimeError('private_peer_not_live_or_owned')
    finally:
        os.close(fd)


def context():
    if os.getuid() != 1000 or Path('/.dockerenv').exists() or len(sys.argv) != 1:
        raise RuntimeError('offline_TEST_guest_callback_only')
    mounts = [row.split() for row in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in mounts):
        raise RuntimeError('readonly_TEST_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux selected-client handoff TEST only\n':
        raise RuntimeError('TEST_seed_marker_required')
    if sorted(p.name for p in Path('/sys/class/net').iterdir()) != ['lo']:
        raise RuntimeError('offline_guest_no_NIC_required')
    if ROOT.resolve() != ROOT or ROOT.stat().st_uid != 0 or stat.S_IMODE(ROOT.stat().st_mode) != 0o710:
        raise RuntimeError('root_owned_TEST_directory_required')
    spec_path = ROOT / 'callback-spec.json'
    if spec_path.is_symlink() or spec_path.stat().st_uid != 0 or stat.S_IMODE(spec_path.stat().st_mode) != 0o444 or spec_path.stat().st_size > 8192:
        raise RuntimeError('root_owned_callback_spec_required')
    spec = json.loads(spec_path.read_text())
    if set(spec) != {'nonce', 'appID', 'helperSHA256', 'busPID', 'busBirth', 'busSocket', 'busAddress', 'compositorPID', 'compositorBirth', 'waylandDisplay'}:
        raise RuntimeError('unexpected_callback_spec_fields')
    if not re.fullmatch(r'[0-9a-f]{32}', spec['nonce']) or spec['appID'] != 'org.notification.NavigationTest' + spec['nonce']:
        raise RuntimeError('invalid_callback_identity')
    if Path('/proc/self/cgroup').read_text().strip() != '0::/navigation-handoff-TEST-' + spec['nonce']:
        raise RuntimeError('owned_TEST_cgroup_required')
    if Path(__file__).resolve() != SEED / 'client-callback.py' or digest(Path(__file__)) != spec['helperSHA256']:
        raise RuntimeError('callback_source_snapshot_changed')
    work = ROOT / 'session'
    for name in ('home', 'config', 'data', 'cache', 'runtime', 'callback'):
        path = work / name
        if path.resolve() != path or not path.is_dir() or path.stat().st_uid != 1000 or stat.S_IMODE(path.stat().st_mode) != 0o700:
            raise RuntimeError('fresh_private_TEST_directory_required')
    runtime = work / 'runtime'
    if not re.fullmatch(r'wayland-[0-9]+', spec['waylandDisplay']):
        raise RuntimeError('invalid_private_wayland_display')
    bus_path = Path(spec['busSocket'])
    private_bus_address(spec['busAddress'], bus_path)
    if os.environ.get('DBUS_SESSION_BUS_ADDRESS') != spec['busAddress'] or os.environ.get('XDG_RUNTIME_DIR') != str(runtime):
        raise RuntimeError('callback_ambient_session_mismatch')
    peer(bus_path, spec['busPID'], spec['busBirth'])
    peer(runtime / spec['waylandDisplay'], spec['compositorPID'], spec['compositorBirth'])
    env = dict(PATH='/usr/bin:/bin', LANG='C.UTF-8', HOME=str(work / 'home'),
        XDG_CONFIG_HOME=str(work / 'config'), XDG_DATA_HOME=str(work / 'data'),
        XDG_CACHE_HOME=str(work / 'cache'), XDG_RUNTIME_DIR=str(runtime),
        XDG_SESSION_TYPE='wayland', XDG_CURRENT_DESKTOP='sway', WAYLAND_DEBUG='1',
        WAYLAND_DISPLAY=spec['waylandDisplay'], DBUS_SESSION_BUS_ADDRESS=spec['busAddress'])
    return spec, env


def main():
    spec, env = context()
    import gi
    gi.require_version('Gio', '2.0')
    from gi.repository import Gio, GLib
    class Callback(Gio.Application):
        def do_before_emit(self, data):
            self.click_token = None
            if getattr(self, 'platform_seen', False) or data.get_type_string() != 'a{sv}':
                return
            self.platform_seen = True
            token = data.lookup_value('activation-token', None)
            if token is None or token.get_type_string() != 's':
                return
            text = token.unpack()
            invocation(spec['nonce'], text)
            Gio.Application.do_before_emit(self, data)
            publish('platform-data.json', dict(identity(), activationTokenSHA256=hashlib.sha256(text.encode()).hexdigest()))
            self.click_token = text
    app = Callback(application_id=spec['appID'], flags=Gio.ApplicationFlags.IS_SERVICE)
    publish('service-start.json', identity())
    deadline = now() + 15
    def opened(action, parameter):
        token = getattr(app, 'click_token', None); app.click_token = None
        target = parameter.unpack() if parameter is not None else None
        if target != 'notification-navigation-test:' + spec['nonce'] or token is None or now() >= deadline:
            publish('callback-rejected.json', identity()); app.quit(); return
        argv = invocation(spec['nonce'], token)
        for path in (LAUNCHER, EXE):
            metadata = path.stat()
            if path.resolve() != path or not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != 0 or metadata.st_mode & 0o022:
                publish('installation-rejected.json', identity()); app.quit(); return
        if digest(LAUNCHER) != LAUNCHER_SHA or digest(EXE) != EXE_SHA:
            publish('installation-rejected.json', identity()); app.quit(); return
        if now() >= deadline:
            publish('deadline-rejected.json', identity()); app.quit(); return
        # The intent is exclusive BEFORE native launch. Missing result is unknown, never retryable.
        publish('handoff-intent.json', dict(identity(), count=1, uri=argv[-1], launcherSHA256=LAUNCHER_SHA,
            executableSHA256=EXE_SHA, activationTokenSHA256=hashlib.sha256(token.encode()).hexdigest()))
        child_env = dict(env, XDG_ACTIVATION_TOKEN=token, DESKTOP_STARTUP_ID=token)
        directory = ROOT / 'session/callback'
        with (directory / 'client.stdout').open('xb') as out, (directory / 'client.stderr').open('xb') as err:
            if now() >= deadline:
                publish('deadline-rejected.json', identity()); app.quit(); return
            child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                env=child_env, cwd=ROOT / 'session', start_new_session=True)
        publish('handoff-launched.json', dict(identity(), clientPID=child.pid, clientBirth=birth(child.pid),
            activationQualified=False, navigationQualified=False))
        # Guest controller owns the inherited TEST cgroup and must collect the client tree.
        GLib.timeout_add(3000, lambda: (app.quit(), False)[1])
    action = Gio.SimpleAction.new('open', GLib.VariantType.new('s')); action.connect('activate', opened); app.add_action(action)
    app.connect('startup', lambda application: application.hold())
    GLib.timeout_add(15000, lambda: (app.quit(), False)[1])
    return app.run([spec['appID']])


if __name__ == '__main__':
    raise SystemExit(main())
