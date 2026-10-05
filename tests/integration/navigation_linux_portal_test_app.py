#!/usr/bin/python3
"""Disposable Gio TEST sender/service. No real application or URI execution."""
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import time
import gi

gi.require_version('Gio', '2.0')
from gi.repository import Gio, GLib


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def identity():
    return dict(pid=os.getpid(), startTicks=int(Path('/proc/self/stat').read_text().rsplit(')', 1)[1].split()[19]),
                entryBoot=now(), helperSHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())


def save(root, name, value):
    with (root / name).open('x') as stream:
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ('--sender', '--service'):
        raise RuntimeError('explicit_TEST_mode_required')
    root = Path(sys.argv[2]).resolve()
    if root != Path('/evidence/runtime') or os.environ.get('NAVIGATION_TEST_CONTAINER') != '1' or not Path('/.dockerenv').exists() or root.stat().st_mode & 0o777 != 0o700 or (root / 'fixture.marker').read_text() != 'Linux portal TEST only\n':
        raise RuntimeError('owned_TEST_root_required')
    spec = json.loads((root / 'spec.json').read_text())
    if not re.fullmatch(r'org\.notification\.NavigationTest[0-9a-f]{32}', spec['appID']) or not re.fullmatch(r'[0-9a-f]{32}', spec['nonce']):
        raise RuntimeError('invalid_TEST_identity')
    expected = 'notification-navigation-test:' + spec['nonce']
    if spec['helperSHA256'] != identity()['helperSHA256']:
        raise RuntimeError('helper_snapshot_changed')
    app = Gio.Application(application_id=spec['appID'], flags=Gio.ApplicationFlags.IS_SERVICE if sys.argv[1] == '--service' else Gio.ApplicationFlags.FLAGS_NONE)
    if sys.argv[1] == '--sender':
        save(root, 'sender-start.json', identity())
        if not app.register(None) or app.get_is_remote():
            raise RuntimeError('sender_not_unique')
        connection = app.get_dbus_connection()
        # Registry must precede all portal methods on this sender's connection.
        connection.call_sync('org.freedesktop.portal.Desktop', '/org/freedesktop/portal/desktop',
            'org.freedesktop.host.portal.Registry', 'Register', GLib.Variant('(sa{sv})', (spec['appID'], {})),
            GLib.VariantType.new('()'), Gio.DBusCallFlags.NONE, 5000, None)
        save(root, 'registry-registered.json', dict(identity(), appID=spec['appID']))
        notification = {'title': GLib.Variant('s', spec['title']), 'body': GLib.Variant('s', 'Owned sender-death TEST'),
                        'default-action': GLib.Variant('s', 'app.open'), 'default-action-target': GLib.Variant('s', expected)}
        save(root, 'show-attempt.json', dict(identity(), count=1))
        connection.call_sync('org.freedesktop.portal.Desktop', '/org/freedesktop/portal/desktop',
            'org.freedesktop.portal.Notification', 'AddNotification', GLib.Variant('(sa{sv})', (spec['nonce'], notification)),
            GLib.VariantType.new('()'), Gio.DBusCallFlags.NONE, 5000, None)
        save(root, 'submitted.json', dict(identity(), addReturned=True))
        connection.flush_sync(None)
        return 0
    started = identity()
    save(root, 'service-start.json', started)
    budget = now() + 15

    def opened(action, parameter):
        actual = parameter.unpack() if parameter is not None else None
        event = dict(identity(), enteredBoot=now(), targetMatches=actual == expected)
        with (root / 'action-events.jsonl').open('a') as stream:
            stream.write(json.dumps(event) + '\n'); stream.flush(); os.fsync(stream.fileno())
        if not isinstance(actual, str) or len(actual.encode()) > 128 or actual != expected or now() >= budget:
            save(root, 'callback-rejected.json', event); app.quit(); return
        try:
            save(root, 'effect.json', dict(event, nonce=spec['nonce']))  # exclusive, exactly one TEST effect
            save(root, 'receipt.json', dict(event, effectCount=1))
        except FileExistsError:
            save(root, 'duplicate-rejected.json', event)
        GLib.timeout_add(3000, lambda: (app.quit(), False)[1])

    action = Gio.SimpleAction.new('open', GLib.VariantType.new('s'))
    action.connect('activate', opened); app.add_action(action)
    app.connect('startup', lambda application: application.hold())
    GLib.timeout_add(15000, lambda: (app.quit(), False)[1])
    return app.run([spec['appID']])


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print(json.dumps({'phase': 'helper_failed', 'error': str(error)}), file=sys.stderr, flush=True)
        raise
