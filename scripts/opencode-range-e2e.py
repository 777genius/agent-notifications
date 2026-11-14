#!/usr/bin/env python3
"""Installed delivery check for any official OpenCode release (V1 or V2).

Starts the unmodified official host with the managed Agent Notifications plugin,
drives genuine turns through a loopback model provider and requires the matching
webhook for completion, permission and provider-failure (plus question on V2).
No release hash is pinned: this is the range check for the family admission.
`--release` takes release tags (v1.19.5) or latest-v1 / latest-v2, which resolve to the
newest published release of that generation, so a new official release is exercised as
soon as it ships. Helpers are shared with opencode-v226-source-e2e.py.
"""
import argparse
import base64
import importlib.util
import json
import os
import platform
import re
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


REPO = 'anomalyco/opencode'
ASSETS = {('linux', 'x86_64'): 'opencode-linux-x64.tar.gz', ('linux', 'aarch64'): 'opencode-linux-arm64.tar.gz',
          ('darwin', 'arm64'): 'opencode-darwin-arm64.zip', ('darwin', 'x86_64'): 'opencode-darwin-x64.zip',
          ('win32', 'AMD64'): 'opencode-windows-x64.zip'}


def github(url, limit):
    headers = {'User-Agent': 'agent-notifications-range-e2e'}
    if os.environ.get('GITHUB_TOKEN') and url.startswith('https://api.github.com/'):
        headers['Authorization'] = 'Bearer ' + os.environ['GITHUB_TOKEN']
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=120) as resp:
        data = resp.read(limit + 1)
    if len(data) > limit:
        raise RuntimeError('release_download_too_large')
    return data


def resolve_release(release):
    """Turn latest-v1 / latest-v2 into the newest published stable tag of that generation."""
    match = re.fullmatch(r'latest-v([12])', release)
    if not match:
        return release
    releases = json.loads(github(f'https://api.github.com/repos/{REPO}/releases?per_page=50', 32 * 1024 * 1024))
    for item in releases:
        if (not item['draft'] and not item['prerelease'] and
                re.fullmatch(r'v' + match.group(1) + r'\.[0-9]+\.[0-9]+', item['tag_name'])):
            return item['tag_name']
    raise RuntimeError('latest_release_unresolved')


def download(tag, target):
    asset = ASSETS.get((sys.platform, platform.machine()))
    if asset is None:
        raise RuntimeError('platform_unsupported')
    # An optional local cache directory saves repeated downloads of the same release.
    cache = os.environ.get('AN_RANGE_CACHE')
    cached = Path(cache) / (tag + '-' + asset) if cache else None
    if cached is not None and cached.is_file():
        shutil.copyfile(cached, target)
        return target
    data = github(f'https://github.com/{REPO}/releases/download/{tag}/{asset}', 250 * 1024 * 1024)
    target.write_bytes(data)
    if cached is not None:
        cached.parent.mkdir(parents=True, exist_ok=True)
        cached.write_bytes(data)
    return target


# Scenario: provider mode, expected webhook type and fixed message, generations that run it.
SCENARIOS = (
    ('completion', 'success', 'task_complete', 'Task completed', ('v1', 'v2')),
    ('permission', 'permission', 'permission_request', 'OpenCode requested permission', ('v1', 'v2')),
    ('question', 'question', 'question', 'OpenCode asked a question', ('v2',)),
    ('failure', 'failure', 'opencode_error', 'An error needs your attention', ('v1', 'v2')),
)


def provider_config(generation, port):
    base = f'http://127.0.0.1:{port}/v1'
    model = {'name': 'TEST model', 'limit': {'context': 32000, 'output': 1024}}
    if generation == 'v2':
        return {'model': 'test/test', 'providers': {'test': {
            'package': '@opencode/ai/providers/openai-compatible',
            'settings': {'baseURL': base, 'apiKey': 'test-only'}, 'models': {'test': model}}},
            'permissions': [{'action': 'shell', 'resource': '*', 'effect': 'ask'}]}
    return {'model': 'test/test', 'provider': {'test': {
        'npm': '@ai-sdk/openai-compatible', 'name': 'TEST',
        'options': {'baseURL': base, 'apiKey': 'test-only'},
        'models': {'test': {**model, 'tool_call': True}}}},
        'permission': {'bash': 'ask'}}


def run(args, root, tag):
    version = tag[1:]
    generation = 'v1' if version.startswith('1.') else 'v2'
    archive = download(tag, root / 'release.archive')
    h = load('opencode-v226-source-e2e')
    native = load('opencode-semantic-e2e')
    binary = root / ('opencode.exe' if sys.platform == 'win32' else 'opencode')
    h.extract_official_binary(archive, binary)
    names = dict(home='home', config='config', project='TEST-project', data='data', cache='cache',
                 state='state', runtime='runtime', temp='temp', bun='bun')
    paths = {k: root / v for k, v in names.items()}
    for directory in paths.values():
        directory.mkdir(mode=0o700)
    subprocess.run(['git', 'init', '-q', str(paths['project'])], check=True, timeout=10)
    (paths['config'] / 'plugins').mkdir(mode=0o700)
    env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': str(paths['home']),
           'LANG': 'C.UTF-8', 'TERM': 'dumb', 'XDG_CONFIG_HOME': str(paths['config']),
           'XDG_DATA_HOME': str(paths['data']), 'XDG_CACHE_HOME': str(paths['cache']),
           'XDG_STATE_HOME': str(paths['state']), 'XDG_RUNTIME_DIR': str(paths['runtime']),
           'BUN_INSTALL_CACHE_DIR': str(paths['bun']), 'TMPDIR': str(paths['temp']),
           'OPENCODE_CONFIG_DIR': str(paths['config']), 'OPENCODE_DISABLE_AUTOUPDATE': 'true',
           'OPENCODE_DISABLE_DEFAULT_PLUGINS': 'true', 'OPENCODE_DISABLE_MODELS_FETCH': 'true',
           'OPENCODE_DISABLE_SHARE': 'true', 'OPENCODE_PASSWORD': 'test-only',
           'OPENCODE_SERVER_PASSWORD': 'test-only'}
    if sys.platform == 'win32':
        env.update({k: os.environ[k] for k in ('SystemRoot', 'WINDIR', 'ComSpec', 'PATHEXT') if k in os.environ})
        env.update({'USERPROFILE': str(paths['home']), 'APPDATA': str(paths['config']),
                    'LOCALAPPDATA': str(paths['data']), 'TEMP': str(paths['temp']), 'TMP': str(paths['temp'])})
    probe = subprocess.run([str(binary), '--version'], cwd=paths['project'], env=env, text=True,
                             capture_output=True, timeout=30)
    if probe.returncode or not re.search(r'(?<![0-9])' + re.escape(version) + r'(?![0-9])', probe.stdout):
        raise RuntimeError('official_version_unexpected')
    provider = native.Endpoint()
    provider.version = version
    threading.Thread(target=provider.serve_forever, daemon=True).start()
    webhook = native.Endpoint(webhook=True)
    threading.Thread(target=webhook.serve_forever, daemon=True).start()
    (paths['project'] / 'opencode.json').write_text(
        json.dumps(provider_config(generation, provider.server_port)), encoding='utf-8')
    h.install_managed_plugin(args.receiver, root, paths, env, paths['config'] / 'plugins', webhook, None)
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    auth = 'Basic ' + base64.b64encode(b'opencode:test-only').decode()

    def request(endpoint, body=None, method=None):
        req = urllib.request.Request(
            f'http://127.0.0.1:{port}' + endpoint, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Content-Type': 'application/json', 'Authorization': auth,
                     'x-opencode-directory': str(paths['project'])})
        with urllib.request.urlopen(req, timeout=35) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else None

    def start_session(title):
        if generation == 'v2':
            return request('/api/session', {'title': title, 'location': {'directory': str(paths['project'])}})['data']['id']
        return request('/session', {'title': title})['id']

    def prompt(sid):
        if generation == 'v2':
            request('/api/session/' + sid + '/prompt', {'text': 'TEST-range-turn'})
        else:
            request('/session/' + sid + '/prompt_async', {'model': {'providerID': 'test', 'modelID': 'test'},
                                                         'parts': [{'type': 'text', 'text': 'TEST-range-turn'}]})

    def ready():
        if host.poll() is not None:
            raise RuntimeError('official_host_exited')
        try:
            return request('/api/location' if generation == 'v2' else '/session') is not None
        except (OSError, urllib.error.URLError):
            return None

    delivered = []
    with (root / 'host.private.log').open('wb') as log:
        host = subprocess.Popen([str(binary), 'serve', '--hostname', '127.0.0.1', '--port', str(port), '--print-logs'],
                                cwd=paths['project'], env=env, stdout=log, stderr=log,
                                start_new_session=(sys.platform != 'win32'),
                                creationflags=(subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == 'win32' else 0))
        try:
            h.wait(ready, 70, 'native_startup_timeout')
            # The plugin activates (runtime profile probe, clock calibration) after the host
            # answers; a loopback model reply is instant, so give activation time to finish.
            time.sleep(args.settle)
            for name, mode, kind, message, generations in SCENARIOS:
                if generation not in generations or args.only and name not in args.only:
                    continue
                with webhook.lock:
                    cursor = len(webhook.bodies)
                provider.mode = mode
                prompt(start_session('TEST-range-' + name))
                h.wait_for_delivery(webhook, kind, message, after=cursor, timeout=40)
                delivered.append(name)
                print('range-stage: ' + name + '-delivered', flush=True)
                provider.mode = 'success'
        except Exception:
            # The plugin's own startup diagnostic is a fixed, content-free line; show only it.
            log.flush()
            for line in (root / 'host.private.log').read_text(errors='replace').splitlines():
                if 'Agent Notifications' in line:
                    print('host-diagnostic: ' + line[:300], flush=True)
            raise
        finally:
            h.stop_official_host_tree(host)
    return {'status': 'passed', 'generation': generation, 'version': version,
            'platform': sys.platform, 'delivered': delivered}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--receiver', type=Path, required=True, help='Exact-source AN executable to install')
    parser.add_argument('--release', nargs='+', required=True, help='Release tags or latest-v1 / latest-v2')
    parser.add_argument('--only', nargs='+', choices=[s[0] for s in SCENARIOS], help='Run only these scenarios')
    parser.add_argument('--settle', type=float, default=3, help='Seconds to wait after the host is ready')
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    h = load('opencode-v226-source-e2e')
    # Resolve every tag first: the API token is only needed here and is dropped before
    # any downloaded host runs. Hosts also get an explicit environment without it.
    tags = {}
    for release in args.release:
        try:
            tags[release] = resolve_release(release)
        except Exception as error:
            tags[release] = error
    os.environ.pop('GITHUB_TOKEN', None)
    results = []
    for release in args.release:
        try:
            if isinstance(tags[release], Exception):
                raise tags[release]
            tag = tags[release]
            with h.OwnedTemporaryDirectory(prefix='TEST-opencode-range-', dir=os.environ.get('RUNNER_TEMP')) as temp:
                root = Path(temp).resolve(strict=True)
                h.prepare_windows_private_root(root)
                result = run(args, root, tag)
        except Exception as error:
            # Never leak native session IDs, paths, model messages or provider errors.
            code = str(error) if isinstance(error, RuntimeError) else type(error).__name__
            result = {'status': 'unqualified', 'release': release, 'failureType': type(error).__name__, 'failureCode': code}
        print(json.dumps(result), flush=True)
        results.append(result)
    args.report.write_text(json.dumps(results), encoding='utf-8')
    return 0 if all(r['status'] == 'passed' for r in results) else 1


if __name__ == '__main__':
    sys.exit(main())
