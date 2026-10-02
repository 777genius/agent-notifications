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
    result = subprocess.run(args, text=True, encoding="utf-8", capture_output=True, timeout=180, **kwargs)
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
            for line in (assets / 'checksums.txt').read_text(encoding='utf-8').splitlines():
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
            manifest = json.loads((package / 'plugin.json').read_text(encoding='utf-8'))
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
                consumer = root / 'TEST-consumer'
                target = consumer / 'bin'
                target.mkdir(parents=True)
                for relative in ('bin/hook-wrapper.sh', 'bin/codex-hook-wrapper.sh',
                                 'bin/install.sh', '.claude-plugin/plugin.json'):
                    copied = consumer / relative
                    copied.parent.mkdir(parents=True, exist_ok=True)
                    copied.write_bytes((source / relative).read_bytes())
                    copied.chmod(0o755 if relative.endswith('.sh') else 0o600)
                env.update(PLUGIN_ROOT=str(consumer), CLAUDE_PLUGIN_ROOT=str(consumer))
                env.update(INSTALL_TARGET_DIR=str(target), RELEASE_URL=base, CHECKSUMS_URL=base + '/checksums.txt')
                for attempt in ('fresh', 'repeat'):
                    output = run([os.environ.get('TEST_BASH', 'bash'), (consumer / 'bin/install.sh').as_posix()], env=env, cwd=home)
                    (report_path.parent / f'install-{attempt}.log').write_text(output, encoding="utf-8")
                    installed = target / binary_name
                    assert installed.is_file(), 'installed binary missing'
                    assert digest(installed) == report['asset_sha256'][binary_name]
                    for name in companions + ([binary_name.removesuffix('.exe') + '-focus.exe'] if goos == 'windows' else []):
                        assert (target / name).is_file(), f'installed companion missing: {name}'
                        assert digest(target / name) == report['asset_sha256'][name], f'installed companion mismatch: {name}'
                    assert run([str(installed), 'version'], env=env, cwd=home).strip() == 'claude-notifications ' + tag
                    report['installer_' + attempt] = 'PASS'
                report['installed_runtime_version'] = 'claude-notifications ' + tag
                # Red if a real hook silently drops/misroutes a payload or replaces
                # the opted-in candidate with the latest stable runtime.
                hook_env = {k: v for k, v in env.items() if k not in
                            ('RELEASE_URL', 'CHECKSUMS_URL', 'INSTALL_TARGET_DIR')}
                bash = os.environ.get('TEST_BASH', 'bash')
                if goos == 'windows':
                    assert Path(bash).as_posix() == 'C:/Program Files/Git/bin/bash.exe'
                config = Path(run([str(installed), 'config', 'path'], env=hook_env, cwd=home).strip())
                run([str(installed), 'config', 'init'], env=hook_env, cwd=home)
                deliveries = []

                class Sink(http.server.BaseHTTPRequestHandler):
                    def log_message(self, *_):
                        pass

                    def do_POST(self):
                        body = self.rfile.read(int(self.headers['Content-Length'])).decode('utf-8')
                        deliveries.append((self.path, json.loads(body)))
                        self.send_response(200)
                        self.end_headers()
                        self.wfile.write(b'ok')

                sink = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Sink)
                sink_worker = threading.Thread(target=sink.serve_forever, daemon=True)
                sink_worker.start()
                try:
                    config.write_text(json.dumps({
                        'schemaVersion': 2,
                        'notifications': {
                            'desktop': {'enabled': False, 'sound': False, 'terminalBell': False},
                            'webhook': {'enabled': True, 'preset': 'slack',
                                        'url': f'http://127.0.0.1:{sink.server_port}/unexpected-shared'},
                        },
                        'agents': {product: {'notifications': {'webhook': {
                            'url': f'http://127.0.0.1:{sink.server_port}/{product}'}}}
                            for product in ('claude', 'codex')},
                        'future': {'preserved': 9007199254740993},
                    }), encoding='utf-8')
                    before = config.read_bytes()
                    run([str(installed), 'config', 'init'], env=hook_env, cwd=home)
                    assert config.read_bytes() == before
                    for attempt in ('fresh', 'repeat'):
                        for product in ('claude', 'codex'):
                            cwd = root / f'TEST-hook-{attempt}-{product}'
                            cwd.mkdir()
                            marker = f'package-{phase}-{goos}-{arch}-{attempt}-{product}'
                            event = 'Stop' if product == 'codex' else 'Notification'
                            payload = dict(hook_event_name=event, session_id=marker, cwd=str(cwd))
                            if product == 'codex':
                                payload.update(turn_id=marker, last_assistant_message=marker,
                                               stop_hook_active=False)
                            else:
                                payload.update(notification_type='permission_prompt', message=marker)
                            wrapper = target / ('codex-hook-wrapper.sh' if product == 'codex'
                                                else 'hook-wrapper.sh')
                            command = [bash, wrapper.as_posix(), 'handle-hook', event]
                            if product == 'codex':
                                command += ['--product', 'codex']
                            count = len(deliveries)
                            output = run(command, input=json.dumps(payload), env=hook_env, cwd=cwd)
                            assert len(deliveries) == count + 1, (marker, output, deliveries)
                            path, body = deliveries[-1]
                            assert path == '/' + product and marker in body['attachments'][0]['text'], (marker, path, body)
                            assert sum(marker in json.dumps(item) for _, item in deliveries) == 1
                            assert config.read_bytes() == before, 'hook rewrote custom config'
                            assert digest(installed) == report['asset_sha256'][binary_name], 'hook changed runtime'
                            version = run([str(installed), 'version'], env=hook_env, cwd=cwd).strip()
                            report['installed_runtime_version'] = version
                            assert version == 'claude-notifications ' + tag, version
                            selected_version = run([bash, wrapper.as_posix(), 'version'],
                                                   env=hook_env, cwd=cwd).strip()
                            assert selected_version == 'claude-notifications ' + tag, selected_version
                        report['hook_chain_' + attempt] = 'PASS'
                    assert len(deliveries) == 4
                    report['hook_chain'] = dict(result='PASS', webhook_deliveries=4,
                                                installed_runtime_version=version)
                finally:
                    sink.shutdown()
                    sink.server_close()
                    sink_worker.join()
                output = run([sys.executable, str(source / 'scripts/release-artifact-e2e.py'),
                              '--binary', str(installed), '--version', tag], env=env, cwd=home)
                (report_path.parent / 'webhook.log').write_text(output, encoding="utf-8")
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
        report_path.write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')


if __name__ == '__main__':
    main()
