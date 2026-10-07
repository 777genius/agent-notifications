#!/usr/bin/python3
"""Root-only offline TEST seed installer; no package manager or host mounts."""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess

SEED = Path('/mnt/navigation-handoff-test-seed')
FILES = {'client-callback.py', 'client-sender.py', 'guest-pointer-entry.py', 'guest-pointer.so',
         'kernel-observer.py', 'protocol-observer.py', 'client-controller.py',
         'server-observer.so', 'server-observer.py'}


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def install():
    if os.getuid() != 0 or Path('/.dockerenv').exists() or Path(__file__).resolve() != SEED / 'guest-bootstrap.py':
        raise RuntimeError('fixed_offline_guest_bootstrap_required')
    rows = [line.split() for line in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in rows):
        raise RuntimeError('readonly_iso_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux selected-client handoff TEST only\n':
        raise RuntimeError('TEST_seed_marker_required')
    if sorted(p.name for p in Path('/sys/class/net').iterdir()) != ['lo']:
        raise RuntimeError('offline_guest_required')
    manifest = json.loads((SEED / 'manifest.json').read_text())
    if set(manifest) != {'files', 'frontendSHA256', 'backendSHA256'} or set(manifest['files']) != FILES:
        raise RuntimeError('fixed_source_manifest_required')
    for name, digest in manifest['files'].items():
        if not isinstance(digest, str) or not re.fullmatch('[0-9a-f]{64}', digest) or sha(SEED / name) != digest:
            raise RuntimeError('source_manifest_changed')
    catalog = json.loads((SEED / 'runtime-hashes.json').read_text())
    if not isinstance(catalog, dict) or not catalog or len(catalog) > 4096:
        raise RuntimeError('bounded_runtime_catalog_required')
    total = 0
    prepared = []
    for relative, digest in catalog.items():
        parts = relative.split('/')
        if len(parts) < 2 or parts[0] not in ('portal', 'gtk') or any(part in ('', '.', '..') for part in parts) or '\\' in relative:
            raise RuntimeError('fixed_runtime_relative_path_required')
        path = SEED / 'runtime' / relative
        metadata = path.lstat()
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_size > 16 * 1024 * 1024:
            raise RuntimeError('bounded_regular_runtime_file_required')
        total += metadata.st_size
        if total > 64 * 1024 * 1024 or not isinstance(digest, str) or not re.fullmatch('[0-9a-f]{64}', digest) or sha(path) != digest:
            raise RuntimeError('runtime_catalog_bytes_changed')
        prepared.append((relative, path, bool(metadata.st_mode & 0o111)))
    observed = {str(p.relative_to(SEED / 'runtime')) for p in (SEED / 'runtime').rglob('*') if p.is_file()}
    if observed != set(catalog) or any(p.is_symlink() for p in (SEED / 'runtime').rglob('*')):
        raise RuntimeError('complete_unaliased_runtime_catalog_required')
    for name in ('portal', 'gtk'):
        if (Path('/opt') / name).exists() or (Path('/opt') / name).is_symlink():
            raise RuntimeError('fresh_guest_runtime_destination_required')
    if catalog.get('portal/libexec/xdg-desktop-portal') != manifest['frontendSHA256'] or catalog.get('gtk/libexec/xdg-desktop-portal-gtk') != manifest['backendSHA256']:
        raise RuntimeError('qualified_frontend_backend_required')
    # The complete readonly ISO input is checked before any installation write.
    for name in ('portal', 'gtk'): (Path('/opt') / name).mkdir(mode=0o755)
    for relative, source, executable in prepared:
        target = Path('/opt') / relative
        target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        with target.open('xb') as out, source.open('rb') as inp:
            for block in iter(lambda: inp.read(1048576), b''): out.write(block)
        target.chmod(0o555 if executable else 0o444)
        if sha(target) != catalog[relative]: raise RuntimeError('installed_runtime_copy_changed')
    for name in ('portal', 'gtk'):
        root = Path('/opt') / name
        for path in root.rglob('*'):
            if path.is_dir(): path.chmod(0o555)
        root.chmod(0o555)
    return manifest


def main():
    try:
        install()
        # Controller owns the single native attempt and its collected shutdown.
        result = subprocess.run(['/usr/bin/python3', '-I', str(SEED / 'client-controller.py')],
            stdin=subprocess.DEVNULL, timeout=100,
            env={'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'HOME': '/root'})
        return result.returncode
    except Exception as error:
        try:
            result = dict(scope='offline_selected_client_native_handoff_TEST', passed=False,
                failure='bootstrap: ' + type(error).__name__ + ': ' + str(error), retryAllowed=False,
                handoffQualified=False, activationQualified=False, navigationQualified=False)
            data = json.dumps(result, sort_keys=True).encode()
            with Path('/dev/ttyS0').open('w') as serial:
                serial.write('NAVIGATION_TEST_HANDOFF_V1 ' + hashlib.sha256(data).hexdigest() + ' ' + base64.b64encode(data).decode() + '\n')
                serial.flush()
        finally:
            subprocess.run(['/usr/bin/systemctl', 'poweroff'], check=False, timeout=20)
        return 1


if __name__ == '__main__': raise SystemExit(main())
