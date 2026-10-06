#!/usr/bin/python3
"""One native portal submission in the same offline TEST guest as the cold callback."""
import hashlib
import importlib.util
import os
from pathlib import Path
import sys

SEED = Path('/mnt/navigation-handoff-test-seed')


def main():
    # The import target is a read-only TEST seed, never an operator-supplied path.
    if os.getuid() != 1000 or len(sys.argv) != 1 or Path('/.dockerenv').exists():
        raise RuntimeError('offline_TEST_sender_only')
    mounts = [row.split() for row in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in mounts):
        raise RuntimeError('readonly_TEST_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux selected-client handoff TEST only\n':
        raise RuntimeError('TEST_seed_marker_required')
    source = Path(__file__).resolve()
    if source != SEED / 'client-sender.py' or hashlib.sha256(source.read_bytes()).hexdigest() != (SEED / 'sender.sha256').read_text().strip():
        raise RuntimeError('sender_source_snapshot_changed')
    module_spec = importlib.util.spec_from_file_location('owned_TEST_callback', SEED / 'client-callback.py')
    callback = importlib.util.module_from_spec(module_spec)
    module_spec.loader.exec_module(callback)
    spec, env = callback.context()
    # Gio must use exactly the session whose live peer context() checked.
    if os.environ.get('XDG_DATA_HOME') != env['XDG_DATA_HOME'] or os.environ.get('XDG_CONFIG_HOME') != env['XDG_CONFIG_HOME']:
        raise RuntimeError('sender_ambient_private_registry_mismatch')
    import gi
    gi.require_version('Gio', '2.0')
    from gi.repository import Gio, GLib
    callback.publish('sender-start.json', callback.identity())
    app = Gio.Application(application_id=spec['appID'], flags=Gio.ApplicationFlags.FLAGS_NONE)
    if not app.register(None) or app.get_is_remote():
        raise RuntimeError('sender_not_unique')
    connection = app.get_dbus_connection()
    connection.call_sync('org.freedesktop.portal.Desktop', '/org/freedesktop/portal/desktop',
        'org.freedesktop.host.portal.Registry', 'Register', GLib.Variant('(sa{sv})', (spec['appID'], {})),
        GLib.VariantType.new('()'), Gio.DBusCallFlags.NONE, 5000, None)
    callback.publish('sender-registered.json', dict(callback.identity(), appID=spec['appID']))
    notification = {
        'title': GLib.Variant('s', 'Navigation TEST ' + spec['nonce']),
        'body': GLib.Variant('s', 'Owned cold callback client handoff TEST'),
        'default-action': GLib.Variant('s', 'app.open'),
        'default-action-target': GLib.Variant('s', 'notification-navigation-test:' + spec['nonce'])}
    # Exclusive intent precedes the sole AddNotification. Timeout is unknown, not retryable.
    callback.publish('sender-show-intent.json', dict(callback.identity(), count=1, retryAllowed=False))
    connection.call_sync('org.freedesktop.portal.Desktop', '/org/freedesktop/portal/desktop',
        'org.freedesktop.portal.Notification', 'AddNotification', GLib.Variant('(sa{sv})', (spec['nonce'], notification)),
        GLib.VariantType.new('()'), Gio.DBusCallFlags.NONE, 5000, None)
    # A typed synchronous reply proves the request was sent; no unbounded flush.
    callback.publish('sender-submitted.json', dict(callback.identity(), addReturned=True, nativeClickQualified=False))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
