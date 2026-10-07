#!/usr/bin/python3
"""Offline TEST guest entry for the shared native pointer effect, never a URI opener."""
import ctypes
import importlib.util
import json
import os
from pathlib import Path
import selectors
import socket
import stat
import struct
import sys

SEED = Path('/mnt/navigation-handoff-test-seed')
ROOT = Path('/var/lib/navigation-client-handoff-TEST')


def main():
    if os.getuid() != 1000 or len(sys.argv) != 1 or Path('/.dockerenv').exists():
        raise RuntimeError('offline_TEST_pointer_entry_only')
    mounts = [row.split() for row in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in mounts):
        raise RuntimeError('readonly_TEST_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux selected-client handoff TEST only\n':
        raise RuntimeError('TEST_seed_marker_required')
    module_spec = importlib.util.spec_from_file_location('owned_TEST_callback', SEED / 'client-callback.py')
    callback = importlib.util.module_from_spec(module_spec)
    module_spec.loader.exec_module(callback)
    session, _ = callback.context()
    spec_path = ROOT / 'pointer-spec.json'
    metadata = spec_path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != 0 or stat.S_IMODE(metadata.st_mode) != 0o444 or metadata.st_size > 8192:
        raise RuntimeError('root_owned_pointer_spec_required')
    spec = json.loads(spec_path.read_text())
    if set(spec) != {'nonce', 'y', 'entrySHA256', 'librarySHA256'} or spec['nonce'] != session['nonce'] or type(spec['y']) is not int or not 0 <= spec['y'] < 720:
        raise RuntimeError('fixed_TEST_pointer_spec_required')
    source, library_path = Path(__file__).resolve(), SEED / 'guest-pointer.so'
    if source != SEED / 'guest-pointer-entry.py' or callback.digest(source) != spec['entrySHA256'] or library_path.resolve() != library_path or callback.digest(library_path) != spec['librarySHA256']:
        raise RuntimeError('pointer_source_or_library_snapshot_changed')
    # Retain the selected compositor incarnation while obtaining this exact connection.
    fd = os.pidfd_open(session['compositorPID'], 0)
    connection = socket.socket(socket.AF_UNIX)
    try:
        if callback.birth(session['compositorPID']) != session['compositorBirth']:
            raise RuntimeError('compositor_incarnation_changed')
        connection.settimeout(2)
        connection.connect(str(ROOT / 'session/runtime' / session['waylandDisplay']))
        peer = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        with selectors.DefaultSelector() as selector:
            selector.register(fd, selectors.EVENT_READ)
            if selector.select(0) or peer[:2] != (session['compositorPID'], 1000):
                raise RuntimeError('connected_compositor_peer_not_owned')
        connection.settimeout(None)
        library = ctypes.CDLL(str(library_path))
        run = library.navigation_guest_pointer_test
        run.argtypes = [ctypes.c_int, ctypes.c_ulong]
        run.restype = ctypes.c_int
        with selectors.DefaultSelector() as selector:
            selector.register(fd, selectors.EVENT_READ)
            if selector.select(0):
                raise RuntimeError('compositor_exited_before_pointer_effect')
        callback.publish('pointer-entry-intent.json', dict(callback.identity(), nonce=spec['nonce'],
            librarySHA256=spec['librarySHA256'], y=spec['y'], retryAllowed=False))
        # The shared native library now owns this connected fd. No environment/default-display lookup.
        return run(connection.detach(), spec['y'])
    finally:
        connection.close()
        os.close(fd)


if __name__ == '__main__':
    raise SystemExit(main())
