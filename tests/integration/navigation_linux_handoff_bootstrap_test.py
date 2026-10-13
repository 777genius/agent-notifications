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


def shipping_inputs(root, manifest):
    """Readonly CI asset contract, also called before host/container launch.

    Each role pins four files from its own exact candidate artifact, never
    handwritten build metadata. The vendor catalog must be that source's embed.
    """
    shipping = manifest.get('shipping')
    if shipping is None: return None
    update = shipping.get('update')
    transition = shipping.get('scenario') == 'retained_a_update_rollback'
    if set(shipping) != {'scenario', 'sourceSHA', 'files'} | ({'update'} if transition else set()) or shipping['scenario'] not in ('restart_b', 'retained_a', 'retained_a_update_rollback'):
        raise RuntimeError('explicit_shipping_asset_contract_required')
    if transition and (not isinstance(update, dict) or set(update) != {'sourceSHA', 'files'} or shipping['sourceSHA'] != 'a0ee25796a5312b829a55e24303e5578d848cd46' or update['sourceSHA'] != '3005338942c791ce9d8ec40c542c2fcc2e17da28'):
        raise RuntimeError('explicit_old_and_update_source_roles_required')
    for prefix, item in [('', shipping)] + ([('update-', update)] if transition else []):
        limits = {'shipping-client': 32 << 20, 'shipping-build-info.txt': 64 << 10,
                  'shipping-vendor-manifest.json': 8 << 20, 'shipping-inputs.json': 64 << 10}
        if not re.fullmatch('[0-9a-f]{40}', item['sourceSHA']) or set(item['files']) != {prefix + name for name in limits}:
            raise RuntimeError('explicit_shipping_asset_contract_required')
        for name, limit in limits.items():
            path = root / (prefix + name); info = path.lstat()
            if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= limit or not re.fullmatch('[0-9a-f]{64}', item['files'][prefix + name]) or sha(path) != item['files'][prefix + name]:
                raise RuntimeError('bounded_digest_bound_shipping_asset_required')
        metadata = json.loads((root / (prefix + 'shipping-inputs.json')).read_text())
        expected = dict(version=1, sourceSHA=item['sourceSHA'], packageSHA256='637c3c94bc50f8ee33a15e2e28ec7f92a787f0943e700efe111bc0bf0d4813b4',
            binarySHA256=item['files'][prefix + 'shipping-client'], catalogSHA256=item['files'][prefix + 'shipping-vendor-manifest.json'],
            buildInfoSHA256=item['files'][prefix + 'shipping-build-info.txt'], catalogAvailable=True)
        if metadata != expected: raise RuntimeError('shipping_source_catalog_identity_mismatch')
        info = (root / (prefix + 'shipping-build-info.txt')).read_text()
        for setting in ('vcs.revision=' + item['sourceSHA'], 'vcs.modified=false', 'GOOS=linux', 'GOARCH=amd64'):
            if len(re.findall(r'(?m)^\s+build\s+' + re.escape(setting) + r'\s*$', info)) != 1:
                raise RuntimeError('exact_unmodified_linux_go_build_required')
        with (root / (prefix + 'shipping-client')).open('rb') as stream:
            if stream.read(4) != b'\x7fELF': raise RuntimeError('selected_shipping_ELF_required')
        catalog = json.loads((root / (prefix + 'shipping-vendor-manifest.json')).read_text())
        if catalog.get('version') != 1 or catalog.get('packageSHA256') != expected['packageSHA256'] or catalog.get('root') != '/usr/lib/chatgpt' or catalog.get('launcher') != '/usr/lib/chatgpt/codex-launcher' or catalog.get('executable') != '/usr/lib/chatgpt/ChatGPT' or not isinstance(catalog.get('entries'), list) or not 0 < len(catalog['entries']) <= 20000:
            raise RuntimeError('authentic_nonempty_vendor_catalog_required')
    if transition and (shipping['files']['shipping-client'] == update['files']['update-shipping-client'] or shipping['files']['shipping-vendor-manifest.json'] != update['files']['update-shipping-vendor-manifest.json']):
        raise RuntimeError('distinct_compatible_update_artifact_required')
    return shipping


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
    if set(manifest) != {'files', 'frontendSHA256', 'backendSHA256'} | ({'shipping'} if 'shipping' in manifest else set()) or set(manifest['files']) != FILES:
        raise RuntimeError('fixed_source_manifest_required')
    shipping_inputs(SEED, manifest)
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
            stdin=subprocess.DEVNULL, timeout=180 if 'shipping' in json.loads((SEED / 'manifest.json').read_text()) else 100,
            env={'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'HOME': '/root'})
        return result.returncode
    except Exception as error:
        try:
            result = dict(scope='offline_selected_client_native_handoff_TEST', passed=False,
                failure='bootstrap: ' + type(error).__name__ + ': ' + str(error), retryAllowed=False,
                handoffQualified=False, activationQualified=False, navigationQualified=False)
            shipping = 'shipping' in json.loads((SEED / 'manifest.json').read_text())
            if shipping: result['scope'] = 'offline_shipping_go_native_handoff_TEST'
            data = json.dumps(result, sort_keys=True).encode()
            with Path('/dev/ttyS0').open('w') as serial:
                serial.write(('NAVIGATION_TEST_SHIPPING_GO_V1 ' if shipping else 'NAVIGATION_TEST_HANDOFF_V1 ') + hashlib.sha256(data).hexdigest() + ' ' + base64.b64encode(data).decode() + '\n')
                serial.flush()
        finally:
            subprocess.run(['/usr/bin/systemctl', 'poweroff'], check=False, timeout=20)
        return 1


def test_shipping_inputs_contract():
    # Inert format fixtures, never Go/vendor executables. These regressions must
    # reject even when the operator's file hashes have been consistently updated.
    import tempfile
    with tempfile.TemporaryDirectory(prefix='navigation-shipping-assets-TEST-') as directory:
        root = Path(directory); source = 'a' * 40
        (root / 'shipping-client').write_bytes(b'\x7fELF inert TEST data, never execute')
        catalog = dict(version=1, packageSHA256='637c3c94bc50f8ee33a15e2e28ec7f92a787f0943e700efe111bc0bf0d4813b4', root='/usr/lib/chatgpt', launcher='/usr/lib/chatgpt/codex-launcher', executable='/usr/lib/chatgpt/ChatGPT', entries=[dict(path='/usr/lib/chatgpt', type='directory')])
        (root / 'shipping-build-info.txt').write_text('\n'.join('\tbuild\t' + value for value in ('vcs.revision=' + source, 'vcs.modified=false', 'GOOS=linux', 'GOARCH=amd64')) + '\n')
        def pins():
            (root / 'shipping-vendor-manifest.json').write_text(json.dumps(catalog))
            names = ('shipping-client', 'shipping-build-info.txt', 'shipping-vendor-manifest.json')
            hashes = {name: sha(root / name) for name in names}
            metadata = dict(version=1, sourceSHA=source, packageSHA256=catalog['packageSHA256'], catalogAvailable=True,
                binarySHA256=hashes['shipping-client'], catalogSHA256=hashes['shipping-vendor-manifest.json'], buildInfoSHA256=hashes['shipping-build-info.txt'])
            (root / 'shipping-inputs.json').write_text(json.dumps(metadata))
            hashes['shipping-inputs.json'] = sha(root / 'shipping-inputs.json')
            return dict(shipping=dict(scenario='restart_b', sourceSHA=source, files=hashes))
        manifest = pins()
        assert shipping_inputs(root, {}) is None  # Prototype remains independent.
        assert shipping_inputs(root, manifest)['scenario'] == 'restart_b'
        catalog['entries'] = []
        try: shipping_inputs(root, pins())
        except RuntimeError: pass
        else: raise AssertionError('placeholder catalog admitted')
        catalog['entries'] = [dict(path='/usr/lib/chatgpt', type='directory')]
        info = root / 'shipping-build-info.txt'
        info.write_text(info.read_text().replace('vcs.modified=false', 'vcs.modified=true'))
        try: shipping_inputs(root, pins())
        except RuntimeError: pass
        else: raise AssertionError('modified Go source admitted')
        info.write_text(info.read_text().replace('vcs.modified=true', 'vcs.modified=false'))
        manifest = pins(); (root / 'shipping-client').write_bytes(b'changed TEST asset')
        try: shipping_inputs(root, manifest)
        except RuntimeError: pass
        else: raise AssertionError('changed ELF asset admitted')

        # Red when a pair can reuse one ELF or consistently swap old/update
        # source roles. The real managed-transition assertions remain native.
        def pair(old_source='a0ee25796a5312b829a55e24303e5578d848cd46', new_source='3005338942c791ce9d8ec40c542c2fcc2e17da28', same_binary=False):
            specs = []
            for prefix, revision in (('', old_source), ('update-', new_source)):
                binary = b'\x7fELF OLD TEST never execute' if not prefix or same_binary else b'\x7fELF UPDATE TEST never execute'
                (root / (prefix + 'shipping-client')).write_bytes(binary)
                (root / (prefix + 'shipping-build-info.txt')).write_text('\n'.join('\tbuild\t' + value for value in ('vcs.revision=' + revision, 'vcs.modified=false', 'GOOS=linux', 'GOARCH=amd64')) + '\n')
                (root / (prefix + 'shipping-vendor-manifest.json')).write_text(json.dumps(catalog))
                hashes = {prefix + name: sha(root / (prefix + name)) for name in ('shipping-client', 'shipping-build-info.txt', 'shipping-vendor-manifest.json')}
                metadata = dict(version=1, sourceSHA=revision, packageSHA256=catalog['packageSHA256'], catalogAvailable=True,
                    binarySHA256=hashes[prefix + 'shipping-client'], catalogSHA256=hashes[prefix + 'shipping-vendor-manifest.json'], buildInfoSHA256=hashes[prefix + 'shipping-build-info.txt'])
                (root / (prefix + 'shipping-inputs.json')).write_text(json.dumps(metadata)); hashes[prefix + 'shipping-inputs.json'] = sha(root / (prefix + 'shipping-inputs.json'))
                specs.append(dict(sourceSHA=revision, files=hashes))
            return dict(shipping=dict(scenario='retained_a_update_rollback', **specs[0], update=specs[1]))
        assert shipping_inputs(root, pair())['update']['sourceSHA'] == '3005338942c791ce9d8ec40c542c2fcc2e17da28'
        for make in (lambda: pair(same_binary=True), lambda: pair(old_source='3005338942c791ce9d8ec40c542c2fcc2e17da28', new_source='a0ee25796a5312b829a55e24303e5578d848cd46')):
            invalid = make()
            try: shipping_inputs(root, invalid)
            except RuntimeError: pass
            else: raise AssertionError('non-distinct or wrong-role update admitted')
        invalid = pair(); del invalid['shipping']['update']
        try: shipping_inputs(root, invalid)
        except RuntimeError: pass
        else: raise AssertionError('update without bound candidate admitted')


if __name__ == '__main__': raise SystemExit(main())
