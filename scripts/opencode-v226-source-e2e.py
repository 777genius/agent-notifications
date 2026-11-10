#!/usr/bin/env python3
"""Observe real OpenCode 2.2.6 V2 source events, without granting notification eligibility.

Run in disposable CI with a pinned official archive. Private logs never enter the report.
"""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request

OFFICIAL_ARCHIVE_SHA256 = "08bb21d6326ce740d91cce7ebe88a6536933f6e1e30353b91a06b01f25da306c"
OFFICIAL_TAG_SHA = "dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06"
TRACE_PLUGIN = r'''import {appendFileSync} from 'node:fs';
const write=value=>appendFileSync(process.env.AN_TEST_TRACE,JSON.stringify(value)+'\n');
export default {id:'test-oc226-source',async setup(ctx){
 write({phase:'loaded',version:ctx.app?.version,
   subscribe:typeof ctx.event?.subscribe==='function',
   sessionGet:typeof ctx.session?.get==='function',
   sessionContext:typeof ctx.session?.context==='function',
   permissionGet:typeof ctx.permission?.get==='function',
   permissionList:typeof ctx.permission?.list==='function',
   rpcRegister:typeof ctx.rpc?.register==='function'});
 const controller=new AbortController();
 void(async()=>{try{for await(const entry of ctx.event.subscribe({signal:controller.signal})){
   if(!entry||typeof entry.type!=='string')continue;
   const e=entry.data??entry.properties??{};
   write({phase:'event',type:entry.type,hasData:typeof e==='object'&&e!==null,
    hasSequence:Number.isSafeInteger(entry.durable?.seq),
    hasTime:typeof e.time==='number'||typeof e.time?.created==='number'||typeof e.info?.time?.created==='number'});
 }}catch{write({phase:'reader_closed'});}})();
 return ()=>{controller.abort();write({phase:'cleanup'});};
}};'''


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def wait(fn, seconds, label):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = fn()
        if value:
            return value
        time.sleep(.08)
    raise RuntimeError(label)


def official_binary(archive, target):
    if not archive.is_file() or archive.stat().st_size > 160 * 1024 * 1024 or digest(archive) != OFFICIAL_ARCHIVE_SHA256:
        raise RuntimeError('official_archive_unverified')
    with tarfile.open(archive, mode='r:gz') as tar:
        files = [m for m in tar.getmembers() if Path(m.name).name == 'opencode' and m.isfile()]
        if len(files) != 1 or not 0 < files[0].size <= 250 * 1024 * 1024:
            raise RuntimeError('official_executable_not_unique')
        with tar.extractfile(files[0]) as source, target.open('xb') as dest:
            while chunk := source.read(1024 * 1024):
                dest.write(chunk)
    target.chmod(0o700)
    return digest(target)


def run(archive, root):
    spec = importlib.util.spec_from_file_location('existing_native_provider',
        Path(__file__).resolve().parent / 'opencode-semantic-e2e.py')
    native = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(native)
    binary = root / 'opencode'
    binary_sha = official_binary(archive, binary)
    print('source-stage: archive-verified', flush=True)
    names = dict(home='home', config='config', project='TEST-project', data='data',
                 cache='cache', state='state', runtime='runtime', temp='temp', bun='bun')
    paths = {k: root / v for k, v in names.items()}
    for directory in paths.values():
        directory.mkdir(mode=0o700)
    subprocess.run(['git', 'init', '-q', str(paths['project'])], check=True, timeout=10)
    plugins = paths['config'] / 'plugins'
    plugins.mkdir(mode=0o700)
    (plugins / 'test-oc226-source.js').write_text(TRACE_PLUGIN, encoding='utf-8')
    trace = root / 'native-events.private.jsonl'
    env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': str(paths['home']),
           'LANG': 'C.UTF-8', 'TERM': 'dumb', 'XDG_CONFIG_HOME': str(paths['config']),
           'XDG_DATA_HOME': str(paths['data']), 'XDG_CACHE_HOME': str(paths['cache']),
           'XDG_STATE_HOME': str(paths['state']), 'XDG_RUNTIME_DIR': str(paths['runtime']),
           'BUN_INSTALL_CACHE_DIR': str(paths['bun']), 'TMPDIR': str(paths['temp']),
           'OPENCODE_CONFIG_DIR': str(paths['config']), 'AN_TEST_TRACE': str(trace),
           'OPENCODE_DISABLE_AUTOUPDATE': 'true',
           'OPENCODE_DISABLE_DEFAULT_PLUGINS': 'true',
           'OPENCODE_DISABLE_MODELS_FETCH': 'true',
           'OPENCODE_DISABLE_PROJECT_CONFIG': 'true',
           'OPENCODE_DISABLE_SHARE': 'true',
           'OPENCODE_PASSWORD': 'test-only', 'OPENCODE_SERVER_PASSWORD': 'test-only'}
    version = subprocess.run([str(binary), '--version'], cwd=paths['project'], env=env,
                             text=True, capture_output=True, timeout=20)
    if version.returncode or not re.fullmatch(r'(?:opencode\s+)?2\.2\.6', version.stdout.strip()):
        raise RuntimeError('official_version_probe_failed')
    print('source-stage: version-verified', flush=True)
    provider = native.Endpoint()
    provider.version = '2.2.6'
    threading.Thread(target=provider.serve_forever, daemon=True).start()
    try:
        settings = {'baseURL': f'http://127.0.0.1:{provider.server_port}/v1', 'apiKey': 'test-only'}
        config = {'model': 'test/test', 'providers': {'test': {
            'package': '@opencode/ai/providers/openai-compatible', 'settings': settings,
            'models': {'test': {'name': 'TEST model', 'limit': {'context': 32000, 'output': 1024}}}}}}
        (paths['project'] / 'opencode.json').write_text(json.dumps(config), encoding='utf-8')
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        with (root / 'host.private.log').open('wb') as log:
            host = subprocess.Popen([str(binary), 'serve', '--hostname', '127.0.0.1',
                                     '--port', str(port), '--print-logs'],
                                    cwd=paths['project'], env=env, stdout=log, stderr=log,
                                    start_new_session=True)
            try:
                def request(endpoint, body=None, method=None):
                    payload = None if body is None else json.dumps(body).encode()
                    headers = {'Content-Type': 'application/json',
                               'x-opencode-directory': str(paths['project']),
                               'Authorization': 'Basic ' + base64.b64encode(b'opencode:test-only').decode()}
                    req = urllib.request.Request(f'http://127.0.0.1:{port}' + endpoint,
                                                 data=payload, method=method, headers=headers)
                    with urllib.request.urlopen(req, timeout=35) as resp:
                        return json.loads(resp.read())

                def rows():
                    if not trace.exists():
                        return []
                    if trace.stat().st_size > 2 * 1024 * 1024:
                        raise RuntimeError('private_trace_limit')
                    return [json.loads(line) for line in trace.read_text().splitlines()]

                def ready():
                    if host.poll() is not None:
                        raise RuntimeError('official_host_exited')
                    try:
                        return request('/api/location')
                    except (urllib.error.URLError, TimeoutError):
                        return None

                wait(ready, 70, 'native_startup_timeout')
                print('source-stage: native-started', flush=True)
                loaded = wait(lambda: next((r for r in rows() if r.get('phase') == 'loaded'), None),
                              25, 'source_autoload_missing')
                print('source-stage: plugin-loaded', flush=True)
                capabilities = ('subscribe', 'sessionGet', 'sessionContext', 'permissionGet',
                                'permissionList', 'rpcRegister')
                if loaded.get('version') != '2.2.6' or any(loaded.get(k) is not True for k in capabilities):
                    raise RuntimeError('host_capabilities_mismatch')
                session = request('/api/session', {'title': 'TEST-source-2.2.6',
                                     'location': {'directory': str(paths['project'])}})
                sid = session['data']['id']
                print('source-stage: session-created', flush=True)
                request('/api/session/' + sid + '/prompt', {'text': 'TEST-native-source-turn'})
                request('/api/experimental/session/' + sid + '/wait', method='POST')
                observed = wait(lambda: {r['type'] for r in rows() if r.get('phase') == 'event'}
                                if any(r.get('phase') == 'event' and r.get('type') == 'session.execution.succeeded'
                                       for r in rows()) else None, 35, 'native_source_events_missing')
                if not provider.bodies or 'session.execution.started' not in observed:
                    raise RuntimeError('real_provider_turn_unproved')
                return {'status': 'passed_source_only', 'version': '2.2.6',
                        'upstreamTagCommit': OFFICIAL_TAG_SHA,
                        'officialArchiveSHA256': OFFICIAL_ARCHIVE_SHA256,
                        'officialBinarySHA256': binary_sha,
                        'realNativeProviderTurn': True, 'sourceSubscriberLoaded': True,
                        'observedEventTypes': sorted(observed),
                        'nativeObserverQualified': False, 'nativeClockQualified': False,
                        'notificationDeliveryQualified': False}
            finally:
                if host.poll() is None:
                    os.killpg(host.pid, signal.SIGTERM)
                    try:
                        host.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(host.pid, signal.SIGKILL)
                        host.wait(timeout=5)
    finally:
        provider.shutdown()
        provider.server_close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='TEST-opencode-v226-', dir=os.environ.get('RUNNER_TEMP')) as temp:
        try:
            result = run(args.archive, Path(temp))
        except Exception as error:
            # Never leak native session IDs, paths, model messages or provider errors.
            safe_errors = {'official_archive_unverified', 'official_executable_not_unique',
                           'official_version_probe_failed', 'official_host_exited',
                           'native_startup_timeout', 'source_autoload_missing',
                           'host_capabilities_mismatch', 'private_trace_limit',
                           'native_source_events_missing', 'real_provider_turn_unproved'}
            reason = str(error) if type(error) is RuntimeError and str(error) in safe_errors else 'unexpected'
            result = {'status': 'unqualified', 'failureType': type(error).__name__, 'failureCode': reason,
                      'nativeObserverQualified': False, 'nativeClockQualified': False,
                      'notificationDeliveryQualified': False}
            args.report.write_text(json.dumps(result) + '\n')
            print(json.dumps(result))
            return 1
    args.report.write_text(json.dumps(result, sort_keys=True) + '\n')
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
