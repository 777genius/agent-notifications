#!/usr/bin/env python3
"""Installed-binary native semantics, using only disposable Git projects and loopback providers."""
import argparse
import base64
import concurrent.futures
from contextlib import ExitStack
import hashlib
import json
import os
import pathlib
import re
import secrets
import shutil
import signal
import socket
import subprocess
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PINS = {
    '1.18.33': '0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427',
    '2.0.0': '5bba9cb9e7676b8349b9f685dae9911dc0e9da51a1f5b3e5aa8805e08243c407',
    '2.0.21': 'f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7',
}
COPY = {
    'task_complete': 'Task completed',
    'question': 'OpenCode asked a question',
    'permission_request': 'OpenCode requested permission',
    'opencode_error': 'An error needs your attention',
}
TRACE_PLUGIN = r'''import {appendFileSync} from 'node:fs';
const record=x=>appendFileSync(process.env.TEST_TRACE,JSON.stringify(x)+'\n');
const note=(e,v,observer)=>{const p=e.data??e.properties??{},f=p.form,i=p.info;
 record({type:e.type,version:v,observer,sessionID:p.sessionID??p.sessionId??i?.id??f?.sessionID,
  parentID:p.parentID??i?.parentID,inboxID:p.inboxID,assistantMessageID:p.assistantMessageID,
  finish:p.finish,location:e.location,seq:e.durable?.seq,id:p.id??p.requestID??f?.id,
  source:p.source?{type:p.source.type,messageID:p.source.messageID,id:p.source.id}:undefined});};
export default {id:'test-native-trace',async server(){record({phase:'loaded',version:'v1'});
 return {event:async({event})=>note(event,'v1')};},async setup(ctx){
 const registration=await ctx.session.hook('http.request',input=>{
  const headers=new Headers(input.request.headers);headers.set('x-test-kind',input.kind);
  headers.set('x-test-session',input.sessionID);input.request=new Request(input.request,{headers});
  record({phase:'http',kind:input.kind,sessionID:input.sessionID});});
 const ac=new AbortController();record({phase:'loaded',version:ctx.app.version,location:ctx.location});
 void(async()=>{try{for await(const e of ctx.event.subscribe({signal:ac.signal}))note(e,ctx.app.version,ctx.location.directory);}catch{}})();
 return async()=>{ac.abort();await registration.dispose();record({phase:'cleanup',version:ctx.app.version});};}};'''


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def wait(predicate, label, seconds=30):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(.05)
    raise TimeoutError(label)


class Endpoint(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, webhook=False):
        super().__init__(('127.0.0.1', 0), Handler)
        self.webhook, self.bodies, self.lock = webhook, [], threading.Lock()
        self.mode, self.version, self.attempts = 'success', '2.0.21', 0
        self.entered, self.release = threading.Event(), threading.Event()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        size = int(self.headers.get('Content-Length', '0'))
        if not 0 <= size <= 2 * 1024 * 1024:
            self.send_error(413)
            return
        body = self.rfile.read(size)
        with self.server.lock:
            self.server.bodies.append(body)
        if self.server.webhook:
            self.send_response(204)
            self.end_headers()
            return
        if self.path != '/v1/chat/completions':
            self.send_error(404)
            return
        request = json.loads(body)
        primary = self.headers.get('x-test-kind', 'primary') == 'primary'
        text = json.dumps(request.get('messages', []))
        is_child = 'TEST_CHILD_ONLY' in text and 'TEST_CHILD_PROBE' not in text
        mode = 'success' if is_child else self.server.mode
        if primary:
            with self.server.lock:
                self.server.attempts += 1
                attempt = self.server.attempts
        else:
            attempt = 0
        if mode == 'failure' or (mode in ('retry', 'overflow') and primary and attempt == 1):
            code = 429 if mode == 'retry' else 400
            error = {'error': {'message': 'maximum context length exceeded' if mode == 'overflow' else 'TEST_PRIVATE_PROVIDER_ERROR',
                               'type': 'invalid_request_error', 'code': 'context_length_exceeded' if mode == 'overflow' else 'test_error'}}
            data = json.dumps(error).encode()
            self.send_response(code)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.send_header('Retry-After', '0')
            self.end_headers()
            self.wfile.write(data)
            return
        tools = {t['function']['name'] for t in request.get('tools', [])}
        after_tool = any(m.get('role') == 'tool' for m in request.get('messages', []))
        call = None
        if primary and not after_tool and mode in ('question', 'dismiss'):
            call = ('question', {'questions': [{'question': 'TEST_PRIVATE_QUESTION', 'header': 'Test',
                     'options': [{'label': 'Yes', 'description': 'TEST_PRIVATE_OPTION'}]}]})
        if primary and not after_tool and mode in ('permission', 'reject', 'tool'):
            call = ('bash' if self.server.version.startswith('1.') else 'shell',
                    {'command': 'printf TEST_PRIVATE_TOOL_OUTPUT', 'description': 'Test safe sandbox command'})
        if primary and not after_tool and mode == 'child':
            call = ('task' if self.server.version.startswith('1.') else 'subagent',
                    {'description': 'Test child session', 'prompt': 'TEST_CHILD_ONLY sandbox response',
                     'subagent_type': 'general', 'agent': 'general'})
        if call and call[0] not in tools:
            self.server.missing_tool = {'mode': mode, 'requested': call[0], 'available': sorted(tools)}
            call = None
        answer = '## Goal\nTEST_PRIVATE_SUMMARY\n## Work State\nTest ongoing.\n## Next Move\nContinue the sandbox task.' if not primary else 'TEST_PRIVATE_ANSWER'
        if not request.get('stream'):
            response = {'id': 'chatcmpl-test', 'object': 'chat.completion', 'created': int(time.time()),
                'model': 'test', 'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': answer},
                                            'finish_reason': 'stop'}],
                'usage': {'prompt_tokens': 10, 'completion_tokens': 6, 'total_tokens': 16}}
            data = json.dumps(response).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        deltas = [({'role': 'assistant'}, None)]
        if call:
            deltas.append(({'tool_calls': [{'index': 0, 'id': 'call_test', 'type': 'function',
                            'function': {'name': call[0], 'arguments': json.dumps(call[1])}}]}, None))
            deltas.append(({}, 'tool_calls'))
        else:
            deltas.extend([({'content': answer}, None), ({}, 'stop')])
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        try:
            for index, (delta, finish) in enumerate(deltas):
                chunk = {'id': 'chatcmpl-test', 'object': 'chat.completion.chunk', 'created': int(time.time()),
                         'model': 'test', 'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}]}
                self.wfile.write(('data: ' + json.dumps(chunk) + '\n\n').encode())
                self.wfile.flush()
                if index == 1 and primary and mode in ('queue', 'steer', 'cancel'):
                    self.server.entered.set()
                    if not self.server.release.wait(20):
                        return
            self.wfile.write(b'data: [DONE]\n\n')
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass  # Expected only when the native interrupted request disconnects.


def stop_owned(process):
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)


def qualify(args):
    with ExitStack() as cleanup:
        return qualify_owned(args, cleanup)


def qualify_owned(args, cleanup):
    binary, opencode = args.binary.resolve(strict=True), args.opencode.resolve(strict=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    source_sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
    build_info = subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)
    if not re.search(r'(?m)^\s*build\s+vcs\.revision=' + source_sha + r'\s*$', build_info) or not re.search(
            r'(?m)^\s*build\s+vcs\.modified=false\s*$', build_info):
        raise ValueError('candidate must have clean VCS metadata for this exact checkout')
    lock = repo / 'opencode-plugin/package-lock.json'
    observer_pin = json.loads(lock.read_text())['packages']['node_modules/universal-agent-plugins-opencode-events']
    tarball = args.sdk_tarball.resolve(strict=True)
    integrity = 'sha512-' + base64.b64encode(hashlib.sha512(tarball.read_bytes()).digest()).decode()
    if integrity != observer_pin['integrity'] or not re.fullmatch('[0-9a-f]{40}', args.sdk_source_sha):
        raise ValueError('reviewed SDK candidate differs from the product lock integrity')
    provenance = {'schema_version': 1, 'candidate_sha': source_sha, 'sdk_source_sha': args.sdk_source_sha,
        'sdk_tarball_sha256': digest(tarball), 'uap_observer_pin': observer_pin,
        'lock_sha256': digest(lock), 'bundled_js_source_sha256': digest(repo / 'internal/opencodeplugin/dist/agent-notifications.js'),
        'native_os': 'linux', 'native_arch': 'amd64', 'product_build_vcs': 'clean_exact_checkout'}
    if digest(opencode) != PINS[args.version]:
        raise ValueError('native Linux amd64 OpenCode binary differs from acquisition pin')
    if os.uname().machine != 'x86_64' or os.uname().sysname != 'Linux':
        raise ValueError('semantic checkpoint requires native Linux amd64')
    root = args.sandbox.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    project = root / 'TEST-project'
    project.mkdir()
    subprocess.run(['git', 'init', '-q', str(project)], check=True)
    home, config_dir = root / 'home', root / 'opencode-config'
    config_dir.mkdir(parents=True)
    (config_dir / 'opencode.json').write_text(json.dumps({'$schema': 'https://opencode.ai/config.json'}))
    env = {k: os.environ[k] for k in ('PATH', 'LD_LIBRARY_PATH', 'SSL_CERT_FILE') if k in os.environ}
    for name, part in {'HOME': 'home', 'XDG_CONFIG_HOME': 'home/.config', 'XDG_DATA_HOME': 'data',
                       'XDG_CACHE_HOME': 'cache', 'XDG_STATE_HOME': 'state', 'XDG_RUNTIME_DIR': 'runtime-dir',
                       'TMPDIR': 'tmp', 'BUN_INSTALL_CACHE_DIR': 'bun-cache'}.items():
        path = root / part
        path.mkdir(parents=True, exist_ok=True)
        env[name] = str(path)
    trace = root / 'trace.jsonl'
    env.update({'OPENCODE_CONFIG_DIR': str(config_dir), 'OPENCODE_DISABLE_AUTOUPDATE': 'true',
                'OPENCODE_DISABLE_MODELS_FETCH': 'true', 'OPENCODE_DISABLE_SHARE': 'true',
                'OPENCODE_PASSWORD': 'test-only', 'OPENCODE_SERVER_PASSWORD': 'test-only',
                'TEST_TRACE': str(trace), 'AGENT_NOTIFICATIONS_CONTROL_ROOT': str(root / 'control'),
                'AGENT_NOTIFICATIONS_CONFIG': str(root / 'notifications.json'), 'LANG': 'C.UTF-8', 'TERM': 'dumb'})
    provider, webhook = Endpoint(), Endpoint(webhook=True)
    provider.version = args.version
    for endpoint in (provider, webhook):
        threading.Thread(target=endpoint.serve_forever, daemon=True).start()
        cleanup.callback(endpoint.server_close)
        cleanup.callback(endpoint.shutdown)
    auxiliaries = []
    desktop_capture = root / 'desktop.jsonl'
    if args.desktop:
        bus = subprocess.Popen(['dbus-daemon', '--session', '--nofork', '--print-address=1',
            '--address=unix:abstract=TEST-opencode-' + secrets.token_hex(8)], env=env,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, start_new_session=True)
        auxiliaries.append(bus)
        cleanup.callback(stop_owned, bus)
        env['DBUS_SESSION_BUS_ADDRESS'] = bus.stdout.readline().strip()
        if not env['DBUS_SESSION_BUS_ADDRESS']:
            raise RuntimeError('private D-Bus failed to start')
        ready_file = root / 'desktop-ready'
        capture = subprocess.Popen(['/usr/bin/python3', str(repo / 'scripts/opencode-dbus-capture.py'),
            '--address', env['DBUS_SESSION_BUS_ADDRESS'], '--outputJSONL', str(desktop_capture),
            '--readyFile', str(ready_file)], env=env, start_new_session=True)
        auxiliaries.append(capture)
        cleanup.callback(stop_owned, capture)
        wait(ready_file.exists, 'private notification capture')
    (root / 'notifications.json').write_text(json.dumps({'notifications': {'desktop': {'enabled': args.desktop},
        'webhook': {'enabled': True, 'preset': 'custom', 'format': 'json',
                    'url': f'http://127.0.0.1:{webhook.server_port}/webhook'}}}))
    model = {'name': 'Test scripted model', 'limit': {'context': 32000, 'output': 1024}}
    v1 = args.version.startswith('1.')
    if v1:
        env.pop('OPENCODE_PASSWORD')
        env.pop('OPENCODE_SERVER_PASSWORD')
        env.pop('XDG_RUNTIME_DIR')
        env.pop('LD_LIBRARY_PATH', None)
        env.pop('SSL_CERT_FILE', None)
        env['XDG_DATA_HOME'] = str(home / '.local/share')
        env['XDG_STATE_HOME'] = str(home / '.local/state')
    settings = {'baseURL': f'http://127.0.0.1:{provider.server_port}/v1', 'apiKey': 'test-only'}
    configuration = {'model': 'test/test', 'provider' if v1 else 'providers': {'test': {
        'npm' if v1 else 'package': '@ai-sdk/openai-compatible' if v1 else '@opencode/ai/providers/openai-compatible',
        'options' if v1 else 'settings': settings, 'models': {'test': model}}}}
    if v1:
        configuration['permission'] = {'bash': 'ask'}
    (project / 'opencode.json').write_text(json.dumps(configuration))
    common = ['--control-root', str(root / 'control'), '--runtime-root', str(root / 'runtime'),
              '--opencode-config-dir', str(config_dir), '--home', str(home), '--xdg-config-home', env['XDG_CONFIG_HOME']]
    def command(argv):
        result = subprocess.run(argv, cwd=project, env=env, capture_output=True, text=True, timeout=60)
        if result.returncode:
            raise RuntimeError(f'{pathlib.Path(argv[0]).name} {argv[1]} failed: {result.returncode}')
        return result.stdout
    if v1:
        # V1 installs its authoring SDK before activating file plugins. Seed
        # the exact host peer with npm, with lifecycle scripts disabled.
        for directory in (config_dir, home / '.config/opencode'):
            command(['npm', 'install', '--prefix', str(directory), '--save-exact', '--ignore-scripts', '--no-audit', '--no-fund',
                     '--cache', str(root / 'npm-cache'), '@opencode-ai/plugin@1.18.33'])
    command([str(binary), 'setup-opencode', 'install', *common, '--binary', str(binary), '--webhook',
             *(['--desktop'] if args.desktop else [])])
    installed = config_dir / 'plugins/agent-notifications.js'
    installed_digest = digest(installed)
    tracing = TRACE_PLUGIN
    if v1:
        tracing = r'''import {appendFileSync} from 'node:fs';
const record=x=>appendFileSync(process.env.TEST_TRACE,JSON.stringify(x)+'\n');
export default async function(){record({phase:'loaded',version:'v1'});return{event:async({event})=>{
 const p=event.properties??{},i=p.info;
 record({type:event.type,version:'v1',sessionID:p.sessionID??i?.sessionID??i?.id,parentID:i?.parentID,
  id:p.id??p.requestID,finish:i?.finish,status:p.status?.type});}};}'''
    (config_dir / 'plugins/test-native-trace.js').write_text(tracing)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    log = (root / 'server.log').open('w')
    process = subprocess.Popen([str(opencode), 'serve', '--hostname', '127.0.0.1', '--port', str(port), '--print-logs'],
        cwd=project, env=env, stdout=log, stderr=log, start_new_session=True)
    cleanup.callback(stop_owned, process)
    def req(path, body=None, method=None, directory=project):
        data = None if body is None else json.dumps(body).encode()
        headers = {'Content-Type': 'application/json'}
        if not v1:
            headers['x-opencode-directory'] = str(directory)
        if not v1:
            headers['Authorization'] = 'Basic ' + base64.b64encode(b'opencode:test-only').decode()
        request = urllib.request.Request(f'http://127.0.0.1:{port}' + path, data=data, method=method, headers=headers)
        if v1:
            # V1's native client uses fetch. Keep that transport for its control
            # API as well, including persistent HTTP rather than urllib close.
            script = "const [url,method,headers,body]=process.argv.slice(1);const r=await fetch(url,{method,headers:JSON.parse(headers),...(body?{body}:{}),signal:AbortSignal.timeout(55000)});const t=await r.text();if(!r.ok)throw Error('native HTTP '+r.status);process.stdout.write(t);"
            output = subprocess.run(['node', '--input-type=module', '-e', script, request.full_url,
                request.get_method(), json.dumps(headers), data.decode() if data else ''],
                cwd=project, env=env, capture_output=True, text=True, timeout=60)
            if output.returncode:
                raise RuntimeError('V1 fetch control request failed: ' + output.stderr[-1000:])
            return json.loads(output.stdout) if output.stdout else None
        with urllib.request.urlopen(request, timeout=55) as response:
            raw = response.read()
            return json.loads(raw) if raw else None
    def rows():
        return [json.loads(x) for x in trace.read_text().splitlines()] if trace.exists() else []
    cases = []
    removed = False
    try:
        def ready():
            if process.poll() is not None:
                raise RuntimeError('native server exited before readiness')
            try:
                if v1:
                    with socket.create_connection(('127.0.0.1', port), timeout=.3):
                        return True
                return req('/api/location')
            except (urllib.error.URLError, OSError):
                return False
        wait(ready, 'native readiness', 60)
        if v1:
            # Exercise the same SDK/event-stream initialization as the native
            # CLI. Direct controls before that can stall in V1 server startup.
            command([str(opencode), 'run', '--attach', f'http://127.0.0.1:{port}',
                     '--model', 'test/test', '--format', 'json', 'TEST-native-startup'])
            wait(lambda: len(webhook.bodies) == 1, 'native CLI startup completion')
            cases.append({'case': 'native-cli-startup', 'alerts': ['task_complete']})
        prefix = '/session/' if v1 else '/api/session/'
        for case in args.cases.split(','):
            child_flow = case.startswith('child-')
            removed_flow = case.startswith('removed-')
            mode = case.split('-', 1)[1] if child_flow or removed_flow else case
            if case == 'reload':
                if args.version != '2.0.21':
                    raise ValueError('native plugin reload belongs to the current V2 API')
                warm_session = req('/api/session', {'title': 'TEST-before-reload', 'location': {'directory': str(project)}})['data']['id']
                previous = len(webhook.bodies)
                req(prefix + warm_session + '/prompt', {'text': 'TEST-before-reload'})
                req('/api/experimental/session/' + warm_session + '/wait', method='POST')
                wait(lambda: len(webhook.bodies) == previous + 1, 'pre-reload native completion')
                old_trace = len(rows())
                req('/api/location/reload', {})
                wait(lambda: any(x.get('phase') == 'cleanup' for x in rows()[old_trace:]), 'native plugin disposal')
                wait(lambda: any(x.get('phase') == 'loaded' for x in rows()[old_trace:]), 'native plugin reload')
                mode = 'success'
            if removed_flow and not removed:
                command([str(binary), 'setup-opencode', 'remove', *common])
                if installed.exists():
                    raise AssertionError('remove retained installed plugin')
                # The old in-memory bundle must be denied even if its old
                # executable path is executable again, not just spawn-failed.
                managed = root / 'runtime/claude-notifications-linux-amd64'
                shutil.copyfile(binary, managed)
                managed.chmod(0o755)
                removed = True
            if removed and not removed_flow:
                raise ValueError('revocation cases must come last')
            target = project
            if mode == 'locations':
                if v1:
                    raise ValueError('global multi-location subscription case belongs to V2')
                target = root / 'TEST-project-B'
                target.mkdir()
                subprocess.run(['git', 'init', '-q', str(target)], check=True)
                (target / 'opencode.json').write_text(json.dumps(configuration))
                req('/api/location', directory=target)
            provider.mode, provider.attempts = mode, 0
            provider.entered.clear()
            provider.release.clear()
            body = {'title': 'TEST-' + mode}
            if not v1:
                body.update({'location': {'directory': str(target)}, 'permissions': [
                    {'action': '*', 'resource': '*', 'effect': 'allow'},
                    {'action': 'shell' if mode in ('permission', 'reject') else 'TEST-never', 'resource': '*', 'effect': 'ask'}]})
            created = req('/session' if v1 else '/api/session', body)
            sid = created['id'] if v1 else created['data']['id']
            parent_sid = sid
            if child_flow:
                # Public V2 create does not accept parentID. Create ancestry
                # through the actual subagent tool, then drive that child.
                provider.mode = 'child'
                seed_before = len(webhook.bodies)
                seed_prompt = {'parts': [{'type': 'text', 'text': 'TEST-create-child'}]} if v1 else {'text': 'TEST-create-child'}
                req(prefix + parent_sid + ('/message' if v1 else '/prompt'), seed_prompt)
                wait(lambda: len(webhook.bodies) == seed_before + 1, 'native parent seed completion')
                child = wait(lambda: next((x for x in rows() if x.get('parentID') == parent_sid), None), 'native child creation')
                sid = child['sessionID']
                if v1:
                    req(prefix + sid, {'permission': [
                        {'permission': '*', 'pattern': '*', 'action': 'allow'},
                        {'permission': 'bash', 'pattern': '*', 'action': 'ask'}]}, method='PATCH')
                if not v1:
                    wait_prefix = prefix if args.version == '2.0.0' else '/api/experimental/session/'
                    req(wait_prefix + parent_sid + '/wait', method='POST')
                provider.mode, provider.attempts = mode, 0
            if mode == 'overflow':
                # A new empty session has nothing recoverable to compact.
                provider.mode = 'success'
                warm = {'parts': [{'type': 'text', 'text': 'TEST-warm-context'}]} if v1 else {'text': 'TEST-warm-context'}
                previous = len(webhook.bodies)
                req(prefix + sid + ('/message' if v1 else '/prompt'), warm)
                wait(lambda: len(webhook.bodies) == previous + 1, 'native context warmup')
                provider.mode, provider.attempts = mode, 0
            before = len(webhook.bodies)
            trace_start = len(rows())
            marker = ('TEST_CHILD_PROBE ' if child_flow else '') + 'PRIVATE_PROMPT_' + secrets.token_hex(12)
            prompt = {'parts': [{'type': 'text', 'text': marker}]} if v1 else {'text': marker}
            with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
                initial = pool.submit(req, prefix + sid + ('/message' if v1 else '/prompt'), prompt)
                if mode in ('queue', 'steer', 'cancel'):
                    wait(provider.entered.is_set, 'held primary provider')
                    if mode == 'cancel':
                        ack = req(prefix + sid + ('/abort' if v1 else '/interrupt'), {})
                        if not v1 and ack.get('interrupted') is not True:
                            raise AssertionError('native interruption was not accepted')
                    else:
                        second_prompt = {'parts': [{'type': 'text', 'text': marker + '-second'}]} if v1 else {'text': marker + '-second', 'delivery': mode}
                        second = pool.submit(req, prefix + sid + ('/message' if v1 else '/prompt'), second_prompt)
                        wait(lambda: len({x.get('inboxID') for x in rows() if x.get('sessionID') == sid and
                             x.get('observer') == str(target) and x.get('type') == 'session.inbox.enqueued'}) >= 2,
                             'overlapping inbox admission') if not v1 else time.sleep(.2)
                    provider.release.set()
                    if mode != 'cancel':
                        second.result(timeout=55)
                elif mode in ('question', 'dismiss', 'permission', 'reject'):
                    if mode in ('question', 'dismiss'):
                        pending = wait(lambda: req('/question' if v1 else prefix + sid + '/form').get('data', []) if not v1 else req('/question'),
                                       'native question pending')
                        request_id = pending[0]['id']
                        expected = 'question'
                        if not child_flow and not removed_flow:
                            wait(lambda: len(webhook.bodies) > before, 'installed question alert')
                        if mode == 'dismiss':
                            old_v2 = args.version == '2.0.0'
                            req('/question/' + request_id + '/reject' if v1 else prefix + sid + '/form/' + request_id + ('/cancel' if old_v2 else ''),
                                {} if v1 or old_v2 else None, method='POST' if v1 or old_v2 else 'DELETE')
                        else:
                            req('/question/' + request_id + '/reply' if v1 else prefix + sid + '/form/' + request_id + '/reply',
                                {'answers': [['Yes']]} if v1 else {'answer': {'q0': 'Yes'}})
                    else:
                        pending = wait(lambda: req('/permission') if v1 else req(prefix + sid + '/permission')['data'], 'native permission pending')
                        request_id = pending[0]['id']
                        expected = 'permission_request'
                        if not child_flow and not removed_flow:
                            wait(lambda: len(webhook.bodies) > before, 'installed permission alert')
                        req('/permission/' + request_id + '/reply' if v1 else prefix + sid + '/permission/' + request_id + '/reply',
                            {'reply' if v1 or args.version == '2.0.0' else 'decision': 'reject' if mode == 'reject' else 'once'})
                elif mode == 'tool' and v1:
                    pending = wait(lambda: req('/permission'), 'native tool permission')
                    req('/permission/' + pending[0]['id'] + '/reply', {'reply': 'once'})
                initial.result(timeout=55)
            if not v1:
                wait(lambda: any(x.get('sessionID') == sid and x.get('type') in
                     ('session.execution.succeeded', 'session.execution.failed', 'session.execution.interrupted')
                     for x in rows()[trace_start:]), 'fresh native terminal')
                wait_prefix = prefix if args.version == '2.0.0' else '/api/experimental/session/'
                req(wait_prefix + sid + '/wait', method='POST')
            expected_types = [] if child_flow or removed_flow else ['opencode_error'] if mode == 'failure' else ([] if mode == 'cancel' else
                ['question'] if mode == 'dismiss' else ['permission_request'] if mode == 'reject' else
                ['question', 'task_complete'] if mode == 'question' else
                ['permission_request', 'task_complete'] if mode in ('permission', 'tool') and v1 or mode == 'permission' else ['task_complete'])
            wait(lambda: len(webhook.bodies) >= before + len(expected_types), 'expected installed alerts')
            # A verifier may take two bounded lookups, followed by a 25s IPC
            # attempt. Observe that whole window before claiming a negative.
            seconds = 30 if child_flow or removed_flow or mode in ('queue', 'steer', 'cancel', 'retry', 'child', 'overflow', 'dismiss', 'reject', 'locations') else 1
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                if len(webhook.bodies) != before + len(expected_types):
                    raise AssertionError(f'{mode}: unexpected late or duplicate alert')
                time.sleep(.1)
            actual = [json.loads(x)['notification_type'] for x in webhook.bodies[before:]]
            if actual != expected_types:
                raise AssertionError(f'{mode}: expected {expected_types}, observed {actual}')
            if getattr(provider, 'missing_tool', None):
                raise AssertionError(str(provider.missing_tool))
            native = [x for x in rows()[trace_start:] if x.get('sessionID') == sid and
                      (v1 or x.get('observer') == str(target))]
            if mode == 'retry' and (provider.attempts < 2 or (not v1 and not any(
                    x.get('type') == 'session.retry.scheduled' for x in native))):
                raise AssertionError('retry scenario did not retry native provider')
            if mode == 'overflow' and not any(x.get('type') == ('session.compacted' if v1 else 'session.compaction.ended') for x in native):
                raise AssertionError('overflow scenario did not perform native compaction')
            if mode == 'child' and not any(x.get('parentID') == sid for x in rows()):
                raise AssertionError('subagent tool did not create a native child session')
            if child_flow and not any(x.get('parentID') == parent_sid and x.get('sessionID') == sid for x in rows()):
                raise AssertionError('child scenario lacks native ancestry event')
            if mode == 'locations':
                readers = {x.get('observer') for x in rows()[trace_start:] if x.get('sessionID') == sid and
                           x.get('type') == 'session.execution.succeeded'}
                if readers != {str(project), str(target)}:
                    raise AssertionError('case did not activate two actual global event readers')
            if mode in ('queue', 'steer') and not v1:
                admissions = {x.get('inboxID'): x.get('seq') for x in native if x.get('type') == 'session.inbox.enqueued'}
                terminals = [x.get('seq') for x in native if x.get('type') == 'session.execution.succeeded']
                if len(admissions) != 2 or len(terminals) != 1 or max(admissions.values()) >= terminals[0]:
                    raise AssertionError('native overlap did not coalesce two durable inputs into one busy period')
            cases.append({'case': case, 'sessionID': sid, 'alerts': actual, 'primary_requests': provider.attempts,
                          'native_event_types': sorted({x['type'] for x in native if 'type' in x})})
            args.report.write_text(json.dumps({**provenance, 'status': 'running', 'version': args.version, 'cases': cases}, indent=2) + '\n')
            print(json.dumps(cases[-1]), flush=True)
        if not removed:
            command([str(binary), 'setup-opencode', 'remove', *common])
        if installed.exists():
            raise AssertionError('remove retained installed plugin')
        for body in webhook.bodies:
            payload = json.loads(body)
            if payload.get('agent_source') != 'opencode' or b'PRIVATE' in body or b'ses_' in body or b'msg_' in body:
                raise AssertionError('generic webhook privacy boundary failed')
            if payload.get('message') != COPY[payload['notification_type']]:
                raise AssertionError('webhook text differs from fixed generic copy')
        if args.desktop:
            captured = [json.loads(x) for x in desktop_capture.read_text().splitlines()]
            expected = [COPY[json.loads(body)['notification_type']] for body in webhook.bodies]
            if [x['body'] for x in captured] != expected or any(x['summary'] != 'OpenCode' or x['actions'] or
                    x['hints'].get('suppress-sound') is not True for x in captured):
                raise AssertionError('native desktop generic/silent/no-navigation contract failed')
            provenance.update({'desktop_boundary': 'private_dbus_notify', 'desktop_notify_count': len(captured),
                               'desktop_silent': True, 'desktop_actions_empty': True})
        return {**provenance, 'status': 'pass', 'version': args.version, 'candidate_binary_sha256': digest(binary),
                'opencode_binary_sha256': digest(opencode), 'installed_plugin_sha256': installed_digest,
                'cases': cases, 'sandbox': str(root), 'trace': str(trace), 'desktop_visual_outcome': 'not_observed'}
    finally:
        provider.release.set()
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
        log.close()
        for endpoint in (provider, webhook):
            endpoint.shutdown()
            endpoint.server_close()
        for auxiliary in reversed(auxiliaries):
            if auxiliary.poll() is None:
                auxiliary.terminate()
                try:
                    auxiliary.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    auxiliary.kill()
                    auxiliary.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=pathlib.Path, required=True)
    parser.add_argument('--opencode', type=pathlib.Path, required=True)
    parser.add_argument('--version', choices=PINS, required=True)
    parser.add_argument('--sandbox', type=pathlib.Path, required=True)
    parser.add_argument('--report', type=pathlib.Path, required=True)
    parser.add_argument('--sdk-tarball', type=pathlib.Path, required=True)
    parser.add_argument('--sdk-source-sha', required=True)
    parser.add_argument('--desktop', action='store_true', help='capture real Notify calls on a private Linux D-Bus')
    parser.add_argument('--cases', default='success,question,permission,failure,queue,steer,cancel,retry,tool,child,overflow')
    args = parser.parse_args()
    report = {'status': 'fail', 'version': args.version}
    try:
        report = qualify(args)
    except Exception as error:
        if args.report.exists():
            report = json.loads(args.report.read_text())
        report['status'] = 'fail'
        report['failure'] = type(error).__name__ + ': ' + str(error)[:1000]
        raise
    finally:
        args.report.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
