#!/usr/bin/env python3
"""Qualify downloaded partial prerelease bytes in new disposable profiles only."""
import argparse
import functools
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tempfile
import threading
import zipfile

REPO = '777genius/agent-notifications'


def run(args, **kwargs):
    result = subprocess.run(args, text=True, capture_output=True, timeout=180, **kwargs)
    if result.returncode:
        raise RuntimeError(f'{args[0]} failed ({result.returncode}): {result.stdout[-3000:]}\n{result.stderr[-3000:]}')
    return result.stdout


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def api(path):
    return json.loads(run(['gh', 'api', path]))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    source, report_path = args.source.resolve(), args.report.resolve()
    report_path.parent.mkdir(parents=True, exist_ok=True)
    tag, phase, stable, goos, arch, sha = [os.environ['TEST_' + k] for k in
                                        ('TAG', 'PHASE', 'STABLE', 'OS', 'ARCH', 'SOURCE_SHA')]
    report = dict(status='FAIL', tag=tag, phase=phase, os=goos, arch=arch, source_sha=sha,
                  uap_activation='not_tested', desktop_visual_outcome='not_observed')
    try:
        assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', tag), 'invalid tag'
        assert phase in ('draft', 'public')
        assert (goos, arch) in (('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64'))
        assert run(['git', '-C', str(source), 'rev-parse', 'HEAD']).strip() == sha
        def latest():
            value = api(f'repos/{REPO}/releases/latest')
            assert value['tag_name'] == stable and not value['prerelease'] and not value['draft']
            return value['tag_name']
        report['stable_before'] = latest()
        pages = json.loads(run(['gh', 'api', '--paginate', '--slurp', f'repos/{REPO}/releases']))
        release = next(r for page in pages for r in page if r['tag_name'] == tag)
        assert release['prerelease'] and release['draft'] == (phase == 'draft')
        assert not any('darwin' in a['name'].lower() or 'claudenotifier' in a['name'].lower()
                       for a in release['assets']), 'unexpected macOS asset'
        report['release_id'] = release['id']
        with tempfile.TemporaryDirectory(prefix='TEST-prerelease-package-') as scratch:
            root = Path(scratch).resolve()
            spec = importlib.util.spec_from_file_location('artifact_e2e', source / 'scripts/release-artifact-e2e.py')
            qualifier = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(qualifier)
            qualifier._windows_private_scratch_root(root)
            assets = root / 'assets'
            assets.mkdir()
            binary_name = f'claude-notifications-{goos}-{arch}' + ('.exe' if goos == 'windows' else '')
            zip_name = f'agent-notify-portable-{goos}-{arch}.zip'
            command = ['gh', 'release', 'download', tag, '--repo', REPO, '--dir', str(assets)]
            companions = [binary_name.replace('claude-notifications', prefix, 1)
                          for prefix in ('sound-preview', 'list-devices', 'list-sounds')]
            download_names = [binary_name, zip_name, 'checksums.txt', *companions]
            if goos == 'windows':
                download_names.append(binary_name.removesuffix('.exe') + '-focus.exe')
            for name in download_names:
                command += ['--pattern', name]
            run(command)
            entries = {}
            for line in (assets / 'checksums.txt').read_text().splitlines():
                if not line.strip() or line.startswith('#'):
                    continue
                match = re.fullmatch(r'([0-9a-fA-F]{64})\s+\*?([^\r\n]+)', line)
                assert match, 'invalid checksum record'
                value, name = match.groups()
                assert name not in entries, 'duplicate checksum record'
                entries[name] = value.lower()
            report['asset_sha256'] = {}
            for name in download_names:
                if name == 'checksums.txt':
                    continue
                actual = digest(assets / name)
                assert actual == entries[name], f'checksum mismatch: {name}'
                report['asset_sha256'][name] = actual
            package = root / 'package'
            package.mkdir()
            with zipfile.ZipFile(assets / zip_name) as bundle:
                names = set()
                for info in bundle.infolist():
                    path = PurePosixPath(info.filename)
                    assert not path.is_absolute() and '..' not in path.parts and '\\' not in info.filename
                    assert not re.match(r'^[A-Za-z]:', info.filename)
                    assert info.filename not in names and (info.external_attr >> 16) & 0o170000 != 0o120000
                    assert info.file_size <= 80 << 20
                    names.add(info.filename)
                assert sum(i.file_size for i in bundle.infolist()) <= 160 << 20
                bundle.extractall(package)
            manifest = json.loads((package / 'plugin.json').read_text())
            assert manifest['version'] == tag.removeprefix('v')
            zipped = package / 'bin' / ('claude-notifications.exe' if goos == 'windows' else 'claude-notifications')
            zipped.chmod(0o755)
            assert digest(zipped) == report['asset_sha256'][binary_name]
            env = {k: os.environ[k] for k in ('PATH', 'SystemRoot', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT') if k in os.environ}
            home = root / 'HOME'
            home.mkdir()
            env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM='1')
            for key in ('CODEX_HOME', 'CLAUDE_CONFIG_DIR', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME',
                        'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'TMPDIR', 'TMP', 'TEMP'):
                folder = home / key
                folder.mkdir(mode=0o777 if os.name == 'nt' else 0o700)
                env[key] = str(folder)
            assert run([str(zipped), 'version'], env=env, cwd=home).strip() == 'claude-notifications ' + tag
            relay = root / 'relay'
            relay.mkdir()
            handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(relay))
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), handler)
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            try:
                # Draft assets have no public URLs; serve the downloaded, verified bytes.
                staged = relay / 'download' / tag
                staged.mkdir(parents=True)
                for item in assets.iterdir():
                    (staged / item.name).write_bytes(item.read_bytes())
                base = (f'http://127.0.0.1:{server.server_port}/download/{tag}' if phase == 'draft'
                        else f'https://github.com/{REPO}/releases/download/{tag}')
                target = home / 'bundle'
                target.mkdir()
                env.update(INSTALL_TARGET_DIR=str(target), RELEASE_URL=base, CHECKSUMS_URL=base + '/checksums.txt')
                for attempt in ('fresh', 'repeat'):
                    output = run(['bash', (source / 'bin/install.sh').as_posix()], env=env, cwd=home)
                    (report_path.parent / f'install-{attempt}.log').write_text(output)
                    installed = target / binary_name
                    assert installed.is_file(), 'installed binary missing'
                    assert digest(installed) == report['asset_sha256'][binary_name]
                    for name in companions + ([binary_name.removesuffix('.exe') + '-focus.exe'] if goos == 'windows' else []):
                        assert (target / name).is_file(), f'installed companion missing: {name}'
                        assert digest(target / name) == report['asset_sha256'][name], f'installed companion mismatch: {name}'
                    assert run([str(installed), 'version'], env=env, cwd=home).strip() == 'claude-notifications ' + tag
                    report['installer_' + attempt] = 'PASS'
                output = run([sys.executable, str(source / 'scripts/release-artifact-e2e.py'),
                              '--binary', str(installed), '--version', tag], env=env, cwd=home)
                (report_path.parent / 'webhook.log').write_text(output)
                report['webhook'] = json.loads(output.strip().splitlines()[-1])
                assert report['webhook']['result'] == 'PASS' and report['webhook']['webhook_deliveries'] == 9
            finally:
                server.shutdown()
                server.server_close()
                worker.join()
        report['stable_after'] = latest()
        report['status'] = 'PASS'
    except Exception as error:
        report['error'] = str(error)
        raise
    finally:
        report_path.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
