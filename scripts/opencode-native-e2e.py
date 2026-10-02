#!/usr/bin/env python3
"""Actual installed AN native fixture. Execution belongs to the root after review.
--validate-only is inert; a fixture manifest never grants production eligibility.
"""
import argparse
import base64
import hashlib
import json
import os
import pathlib
import platform
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tarfile
import threading
import time
import urllib.error
import urllib.parse
import zipfile
from http.server import BaseHTTPRequestHandler

REPO = pathlib.Path(__file__).resolve().parents[1]
FIXTURES = REPO / "scripts/testdata/opencode-native-e2e"
sys.path.insert(0, str(FIXTURES))
from provider import OwnedHTTPServer, Provider, request, data, redact as private_redact

VERSIONS = ("1.18.33", "1.18.34", "2.0.21")
PLATFORMS = (("linux", "amd64"), ("linux", "arm64"), ("darwin", "amd64"),
             ("darwin", "arm64"), ("windows", "amd64"))
CELLS = tuple((o, a, v) for o, a in PLATFORMS for v in VERSIONS
              if v != "1.18.34" or (o, a) == ("linux", "amd64"))
COPY = {"completion": ("Task completed", "task_complete"),
        "form": ("OpenCode asked a question", "question"),
        "permission": ("OpenCode requested permission", "permission_request"),
        "error": ("An error needs your attention", "opencode_error")}
GAPS = {
    "production_checkpoint_cancel": "No public pause of product reader/final checkpoint/spawn ordering",
    "duplicate_loader": "No source-qualified public duplicate topology for one registration/fact",
    "reconnect": "No public forcing of exact installed reader end/error and hydration checkpoint",
    "stale_loaded_origin": "Old live loader path staged; its own final spawn/close remains unobservable",
    "lease_revocation": "Sink IO entry can be held; admission lease/claim checkpoint lacks public proof",
    "uncertain_claim_retention": "Disconnected POST is observable; durable claim/duplicate replay is unproved",
    "clock_provenance": "Per-native production clock/source conversion and complete T qualification required",
    "owned_event_child_close": "Fixture leaders reap; product registry inner resourceClosure needs independent proof",
    "compaction_child_locations": "Root/fork/two-location staged; compaction and true-task-child source driver unavailable",
    "nonlinux_external_desktop": "Managed native/path protocol is separate from external desktop submission counts",
}

class Unqualified(RuntimeError):
    pass


def require(ok, code):
    if not ok:
        raise Unqualified(code)


def digest(path):
    h = hashlib.sha256()
    with pathlib.Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def sri(path):
    return "sha512-" + base64.b64encode(hashlib.sha512(pathlib.Path(path).read_bytes()).digest()).decode()


def write_json(path, obj):
    path.write_text(json.dumps(obj, indent=2, sort_keys=True) + "\n")
    path.chmod(0o600)


def run(args, *, cwd, env, timeout=30, input=None):
    result = subprocess.run(args, cwd=cwd, env=env, input=input, capture_output=True,
                            text=True, encoding="utf-8", errors="replace", timeout=timeout)
    require(result.returncode == 0, "candidate_command_failed")
    # Output stays private. Never copy raw native stderr/password/policy into public reports.
    return result.stdout


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def extract_opencode(archive, target, os_name):
    expected = "opencode.exe" if os_name == "windows" else "opencode"
    with (zipfile.ZipFile(archive) if archive.suffix == ".zip" else tarfile.open(archive, "r:gz")) as bundle:
        members = bundle.infolist() if isinstance(bundle, zipfile.ZipFile) else bundle.getmembers()
        matches = [m for m in members if pathlib.PurePosixPath(m.filename if isinstance(bundle, zipfile.ZipFile)
                   else m.name).name == expected]
        if len(matches) != 1:
            raise RuntimeError(f"archive must contain exactly one {expected}")
        member = matches[0]
        require(not isinstance(bundle, tarfile.TarFile) or member.isfile(), 'host_archive_nonregular_member')
        require(not isinstance(bundle, zipfile.ZipFile) or (member.external_attr >> 16) & 0o170000 != 0o120000, 'host_archive_symlink_member')
        with (bundle.open(member) if isinstance(bundle, zipfile.ZipFile) else bundle.extractfile(member)) as source:
            if source is None:
                raise RuntimeError("OpenCode archive member is not a file")
            with target.open("wb") as output:
                while chunk := source.read(1024 * 1024):
                    output.write(chunk)
    if os_name != "windows":
        target.chmod(0o755)


def wait_for(predicate, seconds, label):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        if predicate():
            return
        time.sleep(0.2)
    raise RuntimeError(f"timed out waiting for {label}")


def prepare_sandbox_root(root, os_name):
    """Give every new Windows sandbox child a private inherited DACL."""
    if os_name != "windows":
        return
    repo = pathlib.Path(__file__).resolve().parents[1]
    run(["go", "run", str(repo / "scripts" / "opencode-private-root-windows.go"),
         str(root)], cwd=repo, env=os.environ, timeout=90)



def closed(obj, keys, code):
    require(isinstance(obj, dict) and set(obj) == set(keys), code)
    return obj


def checked_file(base, record):
    closed(record, ('path', 'sha256'), 'artifact_record_schema')
    require(re.fullmatch('[0-9a-f]{64}', str(record['sha256'])) is not None, 'artifact_hash_missing')
    path = base / record['path']
    require(not path.is_symlink() and path.resolve().is_relative_to(base), 'artifact_path_escape')
    require(path.is_file() and digest(path) == record['sha256'], 'artifact_hash_mismatch')
    return path.resolve()


def checked_parent_manifest(path, expected_sha256):
    """Final source/build bindings are external, sealed after the source checkout."""
    path = pathlib.Path(path).absolute()
    root = REPO / '.task-tools/artifacts'
    require(path.resolve().is_relative_to(root) and
            not any(p.is_symlink() for p in (path, *path.parents)), 'external_parent_manifest_required')
    require(re.fullmatch('[0-9a-f]{64}', str(expected_sha256)) is not None and
            path.is_file() and digest(path) == expected_sha256, 'parent_manifest_hash_mismatch')
    return path.resolve()


def verify_source_binding(m, checkout, build, dirty):
    """Parse actual git/Go evidence; custody input never substitutes for it."""
    require(checkout == m['candidateCommit'] == m['buildRevision'], 'exact_checkout_candidate_required')
    require(not dirty, 'clean_candidate_checkout_required')
    require(re.search(r'(?m)^\s*build\s+vcs.revision=' + re.escape(m['buildRevision']) + r'\s*$', build)
            and re.search(r'(?m)^\s*build\s+vcs.modified=false\s*$', build), 'exact_clean_candidate_build_required')


def load_manifest(path, os_name, arch, version, expected_sha256=None):
    input_path = path.absolute()
    path = path.resolve(strict=True)
    require(path.stat().st_size <= 1024 * 1024, 'manifest_size')
    def pairs(items):
        out = {}
        for key, value in items:
            require(key not in out, 'duplicate_manifest_key')
            out[key] = value
        return out
    m = json.loads(path.read_text(), object_pairs_hook=pairs)
    closed(m, ('schema', 'purpose', 'candidateCommit', 'buildRevision', 'assets', 'sdk', 'cells'), 'manifest_schema')
    require(m['schema'] == 1 and m['purpose'] == 'TEST installed AN dual native', 'manifest_purpose')
    require(re.fullmatch('[0-9a-f]{40}', str(m['candidateCommit'])) is not None and
            m['candidateCommit'] == m['buildRevision'], 'candidate_revision_missing')
    checked_parent_manifest(input_path, expected_sha256)
    base = REPO
    assets = closed(m['assets'], ('embedded', 'rebuilt', 'packageLock', 'sdkSource', 'qualificationSource'), 'assets_schema')
    files = {k: checked_file(base, v) for k, v in assets.items()}
    require(files['embedded'] == REPO/'internal/opencodeplugin/dist/agent-notifications.js' and
            files['packageLock'] == REPO/'opencode-plugin/package-lock.json', 'exact_source_asset_paths_required')
    require(files['embedded'].read_bytes() == files['rebuilt'].read_bytes(), 'rebuilt_bundle_differs')
    bundle = files['embedded'].read_text()
    for token in ('EXECUTABLE', 'CONTROL_ROOT', 'ORIGIN'):
        require(bundle.count('"__AGENT_NOTIFICATIONS_' + token + '__"') == 1, 'origin_bound_dual_bundle_required')
    sdk = closed(m['sdk'], ('archive', 'sri', 'lockResolved', 'lockIntegrity'), 'sdk_schema')
    archive = checked_file(base, sdk['archive'])
    require(sri(archive) == sdk['sri'], 'sdk_sri_mismatch')
    with tarfile.open(archive, 'r:gz') as packed:
        metadata = packed.getmember('package/package.json')
        require(metadata.isfile() and metadata.size <= 65536, 'packed_sdk_metadata_invalid')
        with packed.extractfile(metadata) as source:
            package = json.load(source)
    lock = json.loads(files['packageLock'].read_text())['packages']['node_modules/universal-agent-plugins-opencode-events']
    require(package.get('name') == 'universal-agent-plugins-opencode-events' and package.get('version') == lock['version'], 'packed_sdk_lock_identity')
    require(lock['resolved'] == sdk['lockResolved'] and lock.get('integrity') == sdk['lockIntegrity'], 'npm_lock_bytes_mismatch')
    if lock['resolved'].startswith('file:'):
        target = (files['packageLock'].parent / lock['resolved'][5:]).resolve(strict=True)
        require(target == archive and digest(target) == sdk['archive']['sha256'], 'vendored_sdk_bytes_mismatch')
        # Genuine npm file-tar metadata may have no integrity; do not invent one.
        require(lock.get('integrity') in (None, sdk['sri']), 'file_tar_integrity_mismatch')
    else:
        require(lock.get('integrity') == sdk['sri'], 'registry_integrity_mismatch')
    require(isinstance(m['cells'], list) and len(m['cells']) == 11, 'eleven_native_cells_required')
    pins = json.loads((FIXTURES/'host-pins.json').read_text())
    require(pins.get('runtimeQualified') is False, 'fixture_pin_cannot_grant_runtime')
    pinned = {(p['os'],p['arch'],p['version']):p for p in pins['cells']}
    seen = set()
    selected = None
    for c in m['cells']:
        closed(c, ('os', 'arch', 'version', 'hostSourceCommit', 'hostURL', 'archiveName', 'archive',
                   'archiveSRI', 'executableSHA256', 'candidate', 'nativeApp'), 'cell_schema')
        ident = (c['os'], c['arch'], c['version'])
        require(ident in CELLS and ident not in seen, 'unsupported_or_duplicate_cell')
        seen.add(ident)
        pin = pinned[ident]
        require(c['hostURL']==pin['hostURL'] and c['archive']['sha256']==pin['archiveSHA256']
                and c['archiveSRI']==pin['archiveSRI'] and c['executableSHA256']==pin['executableSHA256'], 'reviewed_official_host_bytes_mismatch')
        if pin['hostSourceCommit'] is not None:
            require(c['hostSourceCommit']==pin['hostSourceCommit'], 'reviewed_host_source_mismatch')
        require(re.fullmatch('[0-9a-f]{40}', str(c['hostSourceCommit'])) is not None, 'host_source_missing')
        url = urllib.parse.urlsplit(c['hostURL'])
        require(url.scheme == 'https' and url.hostname in ('github.com', 'registry.npmjs.org') and
                not url.username and not url.password and not url.query and not url.fragment, 'official_archive_url_required')
        if ident == (os_name, arch, version):
            selected = c
    require(seen == set(CELLS) and selected is not None, 'matrix_incomplete')
    host_archive = checked_file(base, selected['archive'])
    require(host_archive.name == selected['archiveName'] and sri(host_archive) == selected['archiveSRI'], 'host_archive_identity')
    candidate = checked_file(base, selected['candidate'])
    return m, selected, files, candidate, host_archive


def fresh_root(artifacts):
    artifacts = artifacts.absolute()
    for p in (artifacts, *artifacts.parents):
        require(not p.is_symlink(), 'evidence_symlink')
    require(artifacts.resolve().is_relative_to(REPO / '.task-tools/artifacts'), 'durable_artifacts_required')
    artifacts.mkdir(mode=0o700, parents=True, exist_ok=True)
    root = artifacts / ('TEST-installed-' + secrets.token_hex(12))
    root.mkdir(mode=0o700)  # exclusive: never reuse an earlier root
    write_json(root / '.owned-test-root.json', {'purpose': 'TEST installed AN dual native', 'nonce': secrets.token_hex(16)})
    return root.resolve()


def network_guard():
    if sys.platform == 'linux':
        require(os.readlink('/proc/self/ns/net') != os.readlink('/proc/1/ns/net'), 'new_private_netns_required')
        require({n for _, n in socket.if_nameindex()} == {'lo'}, 'loopback_only_required')


def environment(root):
    env = {k: os.environ[k] for k in ('PATH', 'SystemRoot', 'WINDIR', 'COMSPEC', 'PATHEXT') if k in os.environ}
    env.update(CI='true', NO_COLOR='1', OPENCODE_DISABLE_AUTOUPDATE='1', OPENCODE_DISABLE_TELEMETRY='1',
               OPENCODE_DISABLE_MODELS_FETCH='1', OPENCODE_DISABLE_PROJECT_CONFIG='1', NPM_CONFIG_OFFLINE='true',
               NPM_CONFIG_FETCH_RETRIES='0', NPM_CONFIG_AUDIT='false', NPM_CONFIG_FUND='false')
    for key, sub in {'HOME': 'home', 'USERPROFILE': 'home', 'XDG_CONFIG_HOME': 'xdg-config',
                     'XDG_DATA_HOME': 'xdg-data', 'XDG_CACHE_HOME': 'xdg-cache', 'XDG_STATE_HOME': 'xdg-state',
                     'XDG_RUNTIME_DIR': 'xdg-run', 'APPDATA': 'appdata', 'LOCALAPPDATA': 'localappdata',
                     'TEMP': 'tmp', 'TMP': 'tmp', 'TMPDIR': 'tmp', 'BUN_INSTALL_CACHE_DIR': 'bun-cache',
                     'OPENCODE_CONFIG_DIR': 'opencode-config'}.items():
        child = root / sub
        child.mkdir(mode=0o700, exist_ok=True)
        env[key] = str(child)
    env.update(AN_TEST_ROOT=str(root), AN_TEST_TRACE=str(root / 'native-private.jsonl'))
    return env


class Owned:
    """Exact handles only. Cleanup is fixture custody, not product IPC close proof."""
    def __init__(self):
        self.processes, self.servers, self.threads, self.logs = [], [], [], []

    def launch(self, argv, root, env, name, stdin=subprocess.DEVNULL, cwd=None):
        log = (root / (name + '-private.log')).open('wb')
        self.logs.append(log)
        proc = subprocess.Popen(argv, cwd=cwd or root, env=env, stdin=stdin,
                                stdout=log, stderr=subprocess.STDOUT)
        self.processes.append(proc)
        return proc

    def serve(self, server):
        self.servers.append(server)
        t = threading.Thread(target=server.serve_forever)
        self.threads.append(t)
        t.start()
        return server

    def stop(self, proc):
        if proc.stdin is not None and not proc.stdin.closed:
            proc.stdin.close()
            try:
                proc.wait(timeout=3)
                return
            except subprocess.TimeoutExpired:
                pass
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=3)
        else:
            proc.wait(timeout=3)

    def close(self):
        for s in self.servers:
            if isinstance(s, Provider):
                s.interrupt_release.set()
            if isinstance(s, Webhook):
                s.release.set()
        failures = []
        for p in reversed(self.processes):
            try:
                self.stop(p)
            except Exception:
                failures.append('owned_process_unclosed')
        for s in self.servers:
            try:
                s.shutdown()
                s.server_close()
            except Exception:
                failures.append('owned_server_unclosed')
        for t in self.threads:
            t.join(timeout=3)
            if t.is_alive():
                failures.append('owned_thread_unclosed')
        for log in self.logs:
            log.close()
        require(not failures and all(p.poll() is not None for p in self.processes), 'owned_cleanup_unproved')


class Webhook(OwnedHTTPServer):
    def __init__(self):
        self.posts, self.lock = [], threading.Lock()
        self.entered, self.release = threading.Event(), threading.Event()
        self.mode = 'success'
        super().__init__(('127.0.0.1', 0), WebhookHandler)

    def count(self):
        with self.lock:
            return len(self.posts)


class WebhookHandler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        n = int(self.headers.get('Content-Length', '0'))
        if self.path != '/webhook' or not 0 < n <= 4096:
            self.send_error(400)
            return
        body = self.rfile.read(n)
        if len(body) != n:
            self.close_connection = True
            return  # A partial request is not an accepted webhook effect.
        with self.server.lock:
            self.server.posts.append((body, dict(self.headers)))
        self.server.entered.set()  # independent external IO entry, not a receipt
        if self.server.mode == 'hold':
            if not self.server.release.wait(10):
                self.close_connection = True
                return
        if self.server.mode == 'disconnect':
            self.close_connection = True  # accepted body; no HTTP success/automatic retry
            self.connection.shutdown(socket.SHUT_RDWR)
            return
        self.send_response(204)
        self.end_headers()


def trace(root):
    path = root / 'native-private.jsonl'
    if not path.exists():
        return []
    require(path.stat().st_size <= 1024 * 1024, 'native_trace_overflow')
    rows = [json.loads(x) for x in path.read_text().splitlines()]
    require(not any(x['kind'] == 'overflow' for x in rows), 'native_capture_truncated')
    return rows


def readiness(proc, base, project, version, headers):
    deadline = time.monotonic() + 35
    v2 = version == '2.0.21'
    while time.monotonic() < deadline:
        require(proc.poll() is None, 'host_exited_before_readiness')
        try:
            info = data(request(base, project, '/api/info' if v2 else '/global/health',
                                v2=v2, timeout=min(1, deadline-time.monotonic()), auth_headers=headers))
            require(isinstance(info, dict) and info.get('version') == version, 'native_version_mismatch')
            require(info.get('pid') == proc.pid if v2 else info.get('healthy') is True, 'native_owned_endpoint_mismatch')
            return
        except urllib.error.HTTPError as e:
            # Exact V2 server-process.ts has starting503; no other HTTP retry.
            require(v2 and e.code == 503, 'native_readiness_http_failure')
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(min(.15, max(0, deadline-time.monotonic())))
    raise Unqualified('native_readiness_budget_exhausted')


def setup(candidate, action, root, env, args, native_app=None):
    common = ['--control-root', str(root / 'control'), '--runtime-root', str(root / 'runtime'),
              '--opencode-config-dir', env['OPENCODE_CONFIG_DIR'], '--home', env['HOME'],
              '--xdg-config-home', env['XDG_CONFIG_HOME']]
    extra = []
    if action in ('install', 'update'):
        extra = ['--binary', str(candidate), '--desktop', '--webhook']
        if args.os == 'darwin':
            require(native_app is not None, 'verified_native_app_required')
            extra += ['--native-app', str(native_app)]
    run([str(candidate), 'setup-opencode', action, *common, *extra], cwd=root, env=env)


def registration(root, plugin, managed, asset):
    ledger = json.loads((root / 'control/ownership.json').read_text())
    r = ledger['Consumers']['opencode-notifications']['OpenCode']
    require(r['OriginBound'] is True, 'registration_not_origin_bound')
    rendered = asset.read_text()
    for key, value in {'EXECUTABLE': str(managed), 'CONTROL_ROOT': str(root / 'control'), 'ORIGIN': r['Origin']}.items():
        rendered = rendered.replace('"__AGENT_NOTIFICATIONS_' + key + '__"', json.dumps(value, ensure_ascii=False))
    require(plugin.read_bytes() == rendered.encode() and digest(plugin) == r['BundleSHA256'], 'installed_render_binding_mismatch')
    return r


def configure(candidate, root, env, endpoint):
    policy = root / 'control/agent-notifications.json'
    inspected = json.loads(run([str(candidate), 'config', 'inspect', '--json'], cwd=root, env=env))
    require(pathlib.Path(inspected['selection']['path']) == policy and inspected['valid'] is True,
            'public_config_cli_does_not_select_managed_policy')
    before = json.loads(policy.read_text())
    edits = {'/notifications/desktop/enabled': True, '/notifications/desktop/sound': True,
             '/notifications/webhook/enabled': True, '/notifications/webhook/url': endpoint,
             '/notifications/webhook/preset': 'custom', '/notifications/webhook/retry/enabled': True, '/notifications/webhook/retry/maxAttempts': 3,
             '/notifications/webhook/format': 'json', '/notifications/webhook/headers/X-Secret': 'PRIVATE_CONFIG_SENTINEL',
             '/notifications/webhook/payloadFields/secret': 'PRIVATE_CONFIG_SENTINEL'}
    for _, status in COPY.values():
        edits['/statuses/' + status + '/enabled'] = True
        edits['/statuses/' + status + '/title'] = 'PRIVATE_CONFIG_SENTINEL'
        edits['/statuses/' + status + '/desktop/enabled'] = True
        edits['/statuses/' + status + '/webhook/enabled'] = True
    run([str(candidate), 'config', 'edit', '--stdin', '--expect-revision', inspected['revision']],
        cwd=root, env=env, input=json.dumps({'set': edits}))
    after = json.loads(policy.read_text())
    require(all(after.get(k) == before.get(k) for k in ('route', 'schemaVersion', 'enabled')), 'config_lost_setup_route')
    return after

def desktop_rows(root):
    file = root / 'desktop-private.jsonl'
    return [json.loads(x) for x in file.read_text().splitlines()] if file.exists() else []


def effects(root, webhook, start, name):
    desktop = desktop_rows(root)[start[0]:]
    posts = webhook.posts[start[1]:]
    require(len(desktop) <= 1 and len(posts) <= 1, 'duplicate_external_effect')
    body, status = COPY['completion' if name == 'retry' else name]
    for row in desktop:
        require(row['valid'] is True and row['body'] == body, 'desktop_copy_silence_actions_mismatch')
    for raw, headers in posts:
        payload = json.loads(raw)
        require(payload.get('notification_type') == status and payload.get('agent_source') == 'opencode', 'webhook_status_mismatch')
        require(payload.get('message') == body and payload.get('title') == 'OpenCode', 'webhook_fixed_copy_mismatch')
        require(set(payload)=={'schema_version','status','notification_type','agent_source','message','timestamp','session_id','source','title'}
                and payload.get('session_id')=='' and b'PRIVATE_' not in raw
                and not any(k.lower()=='x-secret' for k in headers), 'privacy_leak')
    return len(desktop) == 1 and len(posts) == 1


def correlated(value, sid):
    if isinstance(value, dict):
        return value.get('sessionID') == sid or any(correlated(v, sid) for v in value.values())
    return isinstance(value, list) and any(correlated(v, sid) for v in value)


def native_final(events, name, v2, excluded=()):
    if v2:
        terminal = [e for e in events if e.get('type') == ('session.execution.failed' if name == 'error' else 'session.execution.succeeded')]
        steps = [e for e in events if e.get('type') == ('session.step.failed' if name == 'error' else 'session.step.ended') and e.get('data', {}).get('assistantMessageID') and e['data']['assistantMessageID'] not in excluded]
        return any(t.get('data', {}).get('executionID') and t['data']['executionID'] == s.get('data', {}).get('executionID') for t in terminal for s in steps)
    assistants = [e.get('properties', {}).get('info', {}) for e in events if e.get('type') == 'message.updated']
    finals = [m for m in assistants if m.get('role') == 'assistant' and m.get('id') and m.get('parentID') and m.get('time', {}).get('completed') and not m.get('summary') and m['id'] not in excluded]
    if name == 'error':
        return 'session.error' in {e.get('type') for e in events} and any(m.get('error') for m in finals)
    return 'session.idle' in {e.get('type') for e in events} and any(not m.get('error') for m in finals)


def ordinary_projection(history, candidates, session, error, aborted):
    """Validate independently read native message projection, never receive-time stamps."""
    if not isinstance(history,list):
        return False
    for item in history:
        info = item.get('info',item) if isinstance(item,dict) else {}
        times = info.get('time',{})
        completed = times.get('completed')
        if (not isinstance(info.get('id'),str) or not info['id'] or info['id'] not in candidates or info.get('sessionID') != session
                or info.get('role') != 'assistant' or not info.get('parentID') or info.get('summary')
                or not isinstance(completed,(int,float)) or isinstance(completed,bool) or completed<=0):
            continue
        if isinstance(candidates,dict):
            expected = candidates[info['id']]
            if info['parentID'] != expected.get('parentID') or completed != expected.get('completed'):
                continue
        failure = info.get('error')
        if error and isinstance(failure,dict) and failure and failure.get('name') != aborted:
            return True
        if not error and not failure:
            return True
    return False


def final_projection(base, project, enc, sid_hash, native, name, v2, headers, key):
    prefix = '/api/session' if v2 else '/session'
    history = data(request(base,project,prefix+'/'+enc+('/context' if v2 else '/message'),v2=v2,auth_headers=headers))
    if v2:
        terminal_type = 'session.execution.failed' if name=='error' else 'session.execution.succeeded'
        executions = {e.get('data',{}).get('executionID') for e in native if e.get('type')==terminal_type and e.get('data',{}).get('executionID')}
        candidates = {e.get('data',{}).get('assistantMessageID') for e in native
                      if e.get('type')==('session.step.failed' if name=='error' else 'session.step.ended')
                      and e.get('data',{}).get('executionID') in executions}
    else:
        candidates = {}
        for event in native:
            info = event.get('properties',{}).get('info',{})
            if (event.get('type')=='message.updated' and info.get('role')=='assistant'
                    and info.get('sessionID')==sid_hash and info.get('id') and info.get('parentID')
                    and info.get('time',{}).get('completed') and not info.get('summary')):
                candidates[info['id']] = {'parentID':info['parentID'],'completed':info['time']['completed']}
    require(ordinary_projection(private_redact(history,key),candidates,sid_hash,name=='error',
                                private_redact('MessageAbortedError',key)), 'matching_final_ordinary_assistant_projection_missing')


def pending(base, project, enc, name, v2, headers, sid):
    path = ('/api/form' if name == 'form' else f'/api/session/{enc}/permission') if v2 else ('/question' if name == 'form' else '/permission')
    items = data(request(base, project, path, v2=v2, auth_headers=headers))
    require(isinstance(items, list), 'pending_list_shape')
    return path, next((i for i in items if i.get('sessionID') == sid), None)


def native_case(base, project, root, name, v2, headers, provider, webhook, key, report, delivery=True, session_id=None, excluded=()):
    create = {'title': 'TEST ' + name}
    if v2:
        create.update(location={'directory': str(project)}, model={'providerID': 'p0', 'id': 'p0-' + name})
    sid = session_id or data(request(base, project, '/api/session' if v2 else '/session', create, v2=v2, auth_headers=headers))['id']
    enc, sid_hash = urllib.parse.quote(sid, safe=''), private_redact(sid, key)
    start_trace, start_provider = len(trace(root)), len(provider.records)
    start = (len(desktop_rows(root)), webhook.count())
    payload = {'text': 'PRIVATE_NATIVE_TEST ' + name} if v2 else {'model': {'providerID': 'p0', 'modelID': 'p0-' + name}, 'parts': [{'type': 'text', 'text': 'PRIVATE_NATIVE_TEST ' + name}]}
    errors = []
    def submit():
        try:
            request(base, project, f'/api/session/{enc}/prompt' if v2 else f'/session/{enc}/message', payload, v2=v2, timeout=35, auth_headers=headers)
        except Exception as e:
            errors.append(type(e).__name__)
    worker = threading.Thread(target=submit)
    worker.start()
    observed = closed_attention = False
    native = []
    try:
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            native = [r['value'] for r in trace(root)[start_trace:] if r['kind'] in ('native-v1', 'native-v2') and correlated(r['value'], sid_hash)]
            types = {e.get('type') for e in native}
            if name in ('form', 'permission') and not closed_attention:
                path, item = pending(base, project, enc, name, v2, headers, sid)
                required = ('form.created' if v2 else 'question.asked') if name == 'form' else 'permission.asked'
                if item and required in types:
                    rid = urllib.parse.quote(item.get('id', item.get('requestID')), safe='')
                    if v2 and name == 'form':
                        detail = data(request(base, project, f'/api/session/{enc}/form/{rid}', v2=True, auth_headers=headers))
                        require(detail.get('state', {}).get('status') == 'pending', 'form_not_pending')
                    # Keep the real native question/permission pending through both effects.
                    if effects(root, webhook, start, name):
                        observed = True
                        if v2 and name == 'form':
                            request(base, project, f'/api/session/{enc}/form/{rid}', method='DELETE', v2=True, auth_headers=headers)
                        else:
                            closepath = f'/api/session/{enc}/permission/{rid}/reply' if v2 else (f'/question/{rid}/reject' if name == 'form' else f'/permission/{rid}/reply')
                            closebody = {'decision': 'reject'} if v2 else ({} if name == 'form' else {'reply': 'reject'})
                            request(base, project, closepath, closebody, v2=v2, auth_headers=headers)
                        _, remaining = pending(base, project, enc, name, v2, headers, sid)
                        require(remaining is None, 'attention_still_pending_after_close')
                        closed_attention = True
            elif name not in ('form', 'permission') and native_final(native, name, v2, excluded):
                if delivery:
                    observed = effects(root, webhook, start, name)
                else:
                    require((len(desktop_rows(root)), webhook.count()) == start, 'effect_after_remove')
                    observed = True
            close_event = ('form.cancelled' if v2 else 'question.rejected') if name == 'form' else 'permission.replied'
            if observed and not worker.is_alive() and (name not in ('form', 'permission') or close_event in types):
                break
            time.sleep(.15)
        else:
            raise Unqualified('native_fact_or_dual_effect_budget_exhausted')
    finally:
        # On ambiguity, no model/session retry. The parent tears down the host.
        worker.join(timeout=1)
        if worker.is_alive():
            report['unproved']['prompt_cleanup'] = 'Prompt handle remains live until owned host teardown'
        report.setdefault('_promptThreads', []).append(worker)
    require(observed and not worker.is_alive(), 'prompt_unsettled')
    require(len(provider.records) - start_provider == 1 and not provider.gaps, 'provider_calls_or_advertised_tool_gap')
    if delivery:
        effects(root, webhook, start, name)
    projection = data(request(base, project, f'/api/session/{enc}' if v2 else f'/session/{enc}', v2=v2, auth_headers=headers))
    require(not projection.get('parentID'), 'positive_session_is_child')
    if name in ('completion','error'):
        final_projection(base,project,enc,sid_hash,native,name,v2,headers,key)
    if name == 'permission':
        require(not any(private_redact('P0_OWNED_TEST',key) in json.dumps(e) for e in native), 'harmless_shell_executed')
    outcome = {'scenario': name, 'status': 'observed', 'nativeTypes': sorted({e['type'] for e in native}),
               'providerCalls': 1, 'desktopCount': len(desktop_rows(root))-start[0], 'webhookCount': webhook.count()-start[1],
               'nativeCorrelation': sid_hash, 'rootProjectionSHA256': hashlib.sha256(json.dumps(private_redact(projection, key), sort_keys=True).encode()).hexdigest(),
               'settlement': 'unproved_product_final_checkpoint_and_owned_close'}
    report['scenarios'].append(outcome)
    return outcome


def scope_roots(base, projects, root, v2, headers, provider, webhook, key, report):
    """P0 public roots/history/fork routes, with r6's corrected root lineage."""
    prefix = '/api/session' if v2 else '/session'
    def create(project):
        payload = {'title': 'TEST scope root'}
        if v2:
            payload.update(location={'directory': str(project)}, model={'providerID': 'p0', 'id': 'p0-completion'})
        return data(request(base, project, prefix, payload, v2=v2, auth_headers=headers))['id']
    def projection(project, sid):
        enc = urllib.parse.quote(sid, safe='')
        value = data(request(base, project, prefix+'/'+enc, v2=v2, auth_headers=headers))
        directory = value.get('location',{}).get('directory') if v2 else value.get('directory')
        require(value.get('id')==sid and not value.get('parentID') and directory==str(project), 'native_root_location_projection')
        return enc
    ids = [create(p) for p in projects]
    require(len(set(ids))==2, 'independent_native_roots_not_distinct')
    for project, sid in zip(projects,ids):
        projection(project,sid)
        native_case(base,project,root,'completion',v2,headers,provider,webhook,key,report,session_id=sid)
    enc = urllib.parse.quote(ids[0],safe='')
    before = len(trace(root))
    fork_effects = (len(desktop_rows(root)),webhook.count())
    fork = data(request(base,projects[0],prefix+'/'+enc+'/fork',{},v2=v2,auth_headers=headers))['id']
    require(fork not in ids, 'fork_did_not_create_new_root')
    forkenc = projection(projects[0],fork)  # V2 parentID is true ancestry, not fork lineage.
    history = data(request(base,projects[0],prefix+'/'+forkenc+('/context' if v2 else '/message'),v2=v2,auth_headers=headers))
    require(isinstance(history,list) and history, 'fork_copied_history_missing')
    copied = [m.get('info',m).get('id') for m in history]
    require(all(isinstance(mid,str) for mid in copied), 'fork_history_id_missing')
    native_case(base,projects[0],root,'completion',v2,headers,provider,webhook,key,report,
                session_id=fork,excluded=tuple(private_redact(mid,key) for mid in copied))
    require(effects(root,webhook,fork_effects,'completion'), 'fork_history_added_external_effect')
    creations = [r['value'] for r in trace(root)[before:] if r['kind'] in ('native-v1','native-v2')
                 and r['value'].get('type')==('session.forked' if v2 else 'session.created')]
    fork_hash = private_redact(fork,key)
    def matches(event):
        props = event.get('data' if v2 else 'properties',{})
        identity = props.get('sessionID')==fork_hash or event.get('aggregateID')==fork_hash or props.get('info',{}).get('id')==fork_hash
        return identity and (not v2 or props.get('parentID')==private_redact(ids[0],key))
    require(any(matches(e) for e in creations), 'native_fork_lineage_not_correlated')
    report['scenarios'].append({'scenario':'two_location_roots_and_root_fork','status':'observed',
                               'ordinaryTurns':3,'copiedMessagesExcluded':len(copied),'trueTaskChild':'unproved',
                               'settlement':'unproved_product_final_checkpoint_and_owned_close'})


def terminal_scenario(base, project, root, name, v2, provider, key, auth_headers=None):
    """Actual native retry/abort discriminator; no injected callbacks or prompt retry."""
    model='p0-'+name
    create={'title':'P0 terminal '+name}
    if v2: create.update(location={'directory':str(project)},model={'providerID':'p0','id':model})
    outcome={'scenario':name,'status':'unverified','nativeEvents':[]}
    try:
        result=request(base,project,'/api/session' if v2 else '/session',create,v2=v2,auth_headers=auth_headers)
        sid=data(result)['id']
    except Exception as error:
        return {**outcome,'status':'gap','reason':'native_session_create_failed','requestError':type(error).__name__}
    enc=urllib.parse.quote(sid,safe=''); sid_hash=private_redact(sid,key)
    outcome['session']=sid_hash
    start=len(trace(root)); before=len(provider.records)
    prompt_path=f'/api/session/{enc}/prompt' if v2 else f'/session/{enc}/message'
    prompt={'text':'SCENARIO_'+name+' P0 disposable terminal test.'} if v2 else {'model':{'providerID':'p0','modelID':model},'parts':[{'type':'text','text':'SCENARIO_'+name+' P0 disposable terminal test.'}]}
    errors=[]
    def submit():
        try: request(base,project,prompt_path,prompt,v2=v2,timeout=35,auth_headers=auth_headers)
        except Exception as error: errors.append(type(error).__name__)
    thread=threading.Thread(target=submit); provider.prompt_threads.append(thread); thread.start()
    deadline=time.monotonic()+40; interrupted=False; terminal_seen=None
    def belongs(v):
        if isinstance(v,dict): return v.get('sessionID')==sid_hash or any(belongs(x) for x in v.values())
        if isinstance(v,list): return any(belongs(x) for x in v)
        return False
    try:
        while time.monotonic()<deadline:
            native=[r['value'] for r in trace(root)[start:] if r.get('kind') in ('native-v1','native-v2') and belongs(r.get('value'))]
            types=[n.get('type') for n in native]
            outcome['nativeEvents']=sorted(set(t for t in types if t))
            assistants=[n.get('properties',{}).get('info',{}) for n in native if n.get('type')=='message.updated' and n.get('properties',{}).get('info',{}).get('role')=='assistant']
            completed=[a for a in assistants if a.get('id') and a.get('parentID') and a.get('time',{}).get('completed')]
            if name=='interrupt' and not interrupted and provider.interrupt_started.is_set():
                started='session.execution.started' in types if v2 else bool(assistants)
                if started:
                    path=f'/api/session/{enc}/interrupt' if v2 else f'/session/{enc}/abort'
                    response=request(base,project,path,method='POST',v2=v2,auth_headers=auth_headers)
                    body=data(response)
                    if (v2 and (not isinstance(body,dict) or body.get('interrupted') is not True)) or (not v2 and body is not True):
                        outcome.update(status='gap',reason='native_abort_did_not_confirm_active_interruption'); break
                    outcome['interruptResponse']=private_redact(response,key)
                    interrupted=True; provider.interrupt_release.set()
            if name=='retry':
                if v2:
                    retry_positions=[i for i,n in enumerate(native) if n.get('type')=='session.retry.scheduled' and n.get('data',{}).get('assistantMessageID') and n.get('data',{}).get('attempt',0)>0]
                    final_positions=[i for i,n in enumerate(native) if n.get('type')=='session.step.ended' and n.get('data',{}).get('assistantMessageID')]
                    succeeded=[i for i,t in enumerate(types) if t=='session.execution.succeeded']
                    proof=bool(retry_positions and final_positions and succeeded and min(retry_positions)<max(final_positions)<succeeded[-1]) and types.count('session.execution.started')==1 and len(succeeded)==1 and not {'session.execution.failed','session.execution.interrupted'}.intersection(types)
                else:
                    retry_positions=[i for i,n in enumerate(native) if n.get('type')=='session.status' and n.get('properties',{}).get('status',{}).get('type')=='retry']
                    final_positions=[i for i,n in enumerate(native) if n.get('type')=='message.updated' and n.get('properties',{}).get('info',{}).get('time',{}).get('completed') and not n.get('properties',{}).get('info',{}).get('error')]
                    proof=bool(retry_positions and final_positions and min(retry_positions)<max(final_positions) and completed) and 'session.idle' in types and 'session.error' not in types
                outcome['nativeRetryObserved']=bool(retry_positions)
                outcome['nativeRetryEventPositions']=retry_positions
            else:
                if v2:
                    proof=interrupted and types.count('session.execution.started')==1 and types.count('session.execution.interrupted')==1 and 'session.execution.succeeded' not in types and 'session.execution.failed' not in types and any(n.get('type')=='session.execution.interrupted' and n.get('data',{}).get('reason')=='user' for n in native)
                else:
                    aborted=[a for a in completed if a.get('error',{}).get('name')==private_redact('MessageAbortedError',key)]
                    success=[a for a in completed if not a.get('error')]
                    proof=interrupted and bool(aborted) and 'session.idle' in types and not success
                outcome['nativeInterruptionObserved']=bool(proof)
            # A short capture tail checks native events already following the
            # terminal; it is bounded observation, not a source-state barrier.
            if proof:
                if terminal_seen is None: terminal_seen=time.monotonic()
                if not thread.is_alive() and time.monotonic()-terminal_seen>=.35:
                    outcome['status']='native_observed'; break
            else: terminal_seen=None
            time.sleep(.1)
    except Exception as error:
        outcome.update(status='gap',reason='native_terminal_scenario_api_failed',requestError=type(error).__name__)
    finally:
        if name=='interrupt': provider.interrupt_release.set()
    thread.join(timeout=1)
    if outcome['status']=='unverified': outcome.update(status='gap',reason='native_retry_or_interrupt_proof_budget_exhausted')
    outcome['providerRequests']=len(provider.records)-before
    outcome['requestErrors']=errors
    expected=2 if name=='retry' else 1
    if outcome['providerRequests']!=expected: outcome.update(status='gap',reason='native_provider_attempt_count_mismatch')
    if name=='retry' and errors: outcome.update(status='gap',reason='retry_prompt_did_not_settle_successfully')
    if name=='interrupt' and provider.interrupt_hold_expired: outcome.update(status='gap',reason='interrupt_provider_hold_expired')
    if thread.is_alive(): outcome.update(status='gap',reason='prompt_not_settled_within_budget')
    try: outcome['sessionProjection']=private_redact(request(base,project,('/api/session/' if v2 else '/session/')+enc,v2=v2,auth_headers=auth_headers),key)
    except Exception as error: outcome['projectionGap']=type(error).__name__
    return outcome



def native_app(record):
    if record is None:
        return None
    closed(record, ("root", "executable", "sidecar"), "native_app_schema")
    executable = checked_file(REPO, record["executable"])
    checked_file(REPO, record["sidecar"])
    app = (REPO / record["root"]).resolve(strict=True)
    require(app.is_relative_to(REPO) and app.suffix == ".app" and executable.is_relative_to(app), "native_app_custody")
    return app


def qualify(args, report):
    m, c, files, candidate, archive = load_manifest(args.manifest, args.os, args.arch, args.version, args.manifest_sha256)
    require(candidate == args.binary.resolve(strict=True) and archive == args.archive.resolve(strict=True), 'cli_manifest_paths_differ')
    report.update(candidateCommit=m['candidateCommit'], manifestSHA256=digest(args.manifest),
                  candidateSHA256=digest(candidate), embeddedSHA256=digest(files['embedded']), sdkArchiveSHA256=m['sdk']['archive']['sha256'])
    if args.validate_only:
        report['status'] = 'inputs_verified_only'
        return
    actual_os = {'darwin': 'darwin', 'linux': 'linux', 'win32': 'windows'}.get(sys.platform)
    actual_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'AMD64': 'amd64'}.get(platform.machine())
    require((args.os, args.arch) == (actual_os, actual_arch), 'actual_native_platform_mismatch')
    network_guard()
    root = fresh_root(args.artifacts)
    prepare_sandbox_root(root, args.os)
    env, owner = environment(root), Owned()
    key = secrets.token_bytes(32)
    (root / 'trace-key').write_bytes(key)
    report['privateEvidenceRoot'] = root.name
    try:
        build = run(['go', 'version', '-m', str(candidate)], cwd=root, env=env)
        checkout = run(['git', 'rev-parse', 'HEAD'], cwd=REPO, env=env).strip()
        dirty = run(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=REPO, env=env)
        dirty += run(['git', 'ls-files', '--others', '--exclude-standard', '--', '.',
                      ':(exclude).task-tools/artifacts'], cwd=REPO, env=env)
        tracked = run(['git', 'ls-files', '--', str(args.manifest.resolve().relative_to(REPO))], cwd=REPO, env=env)
        require(not tracked, 'external_parent_manifest_required')
        verify_source_binding(m, checkout, build, dirty)
        host = root / ('opencode.exe' if args.os == 'windows' else 'opencode')
        extract_opencode(archive, host, args.os)
        require(digest(host) == c['executableSHA256'], 'host_executable_hash_mismatch')
        version_out = run([str(host), '--version'], cwd=root, env=env).strip()
        require(version_out in (args.version, 'opencode v' + args.version), 'exact_host_version_required')
        projects = [root / 'project-a', root / 'project-b']
        for project in projects:
            project.mkdir(mode=0o700)
            (project / 'README.md').write_text('Fresh private canonical TEST project.\n')
        config_dir = pathlib.Path(env['OPENCODE_CONFIG_DIR'])
        foreign = config_dir / 'opencode.jsonc'
        foreign.write_text('// FOREIGN_COMMENT\n{"theme":"system"}\n')
        skill = config_dir / 'skills/foreign/SKILL.md'
        skill.parent.mkdir(parents=True)
        skill.write_text('---\nname: foreign\ndescription: preserved TEST skill\n---\nForeign body.\n')
        foreign_files = {p: digest(p) for p in (foreign, skill)}
        app = native_app(c['nativeApp'])
        setup(candidate, 'install', root, env, args, app)
        plugin = config_dir / 'plugins/agent-notifications.js'
        managed = root / 'runtime' / ('claude-notifications-' + args.os + '-' + args.arch + ('.exe' if args.os == 'windows' else ''))
        require(digest(managed) == digest(candidate), 'managed_image_mismatch')
        r = registration(root, plugin, managed, files['embedded'])
        report['renderedInstalledSHA256'] = digest(plugin)
        # Seal real installation output before any host launch; random origin is never fabricated.
        write_json(root / 'installation-private.json', {'renderedSHA256': digest(plugin), 'registration': r, 'manifestSHA256': digest(args.manifest)})
        webhook = owner.serve(Webhook())
        policy = configure(candidate, root, env, f'http://127.0.0.1:{webhook.server_port}/webhook')
        report['authoritativePolicySHA256'] = digest(root / 'control/agent-notifications.json')
        provider = owner.serve(Provider(root, key))
        provider.prompt_threads = []
        v2 = args.version == '2.0.21'
        models = {'p0-' + n: {'name': 'Private ' + n, 'limit': {'context': 128000, 'output': 8192}} for n in (*COPY, 'retry', 'interrupt')}
        endpoint = f'http://127.0.0.1:{provider.server_port}/v1'
        if v2:
            config = {'model':'p0/p0-completion','update':'disable','share':'disabled','warming':False,'formatter':False,'lsp':False,'websearch':False,
                      'permissions':[{'action':'*','resource':'*','effect':'deny'},{'action':'question','resource':'*','effect':'allow'},{'action':'shell','resource':'*','effect':'ask'}],
                      'providers':{'p0':{'name':'TEST loopback','package':'@opencode/ai/providers/openai-compatible','env':[], 'settings':{'baseURL':endpoint,'apiKey':'sandbox-only','timeout':10000},'models':models}}}
        else:
            config = {'model':'p0/p0-completion','permission':{'*':'deny','question':'allow','bash':'ask','shell':'ask'},
                      'provider':{'p0':{'npm':'@ai-sdk/openai-compatible','name':'TEST loopback','options':{'baseURL':endpoint,'apiKey':'sandbox-only'},'models':models}}}
        write_json(projects[0] / 'opencode.json', config)
        env['OPENCODE_CONFIG'] = str(projects[0] / 'opencode.json')
        shutil.copyfile(FIXTURES / 'capture.mjs', config_dir / 'plugins/an-test-capture.js')
        write_json(root / 'profile-descriptor.json', {'origin':r['Origin'],'executable':str(managed),'controlRoot':str(root/'control')})
        if args.os == 'linux':
            bus = owner.launch(['dbus-daemon', '--session', '--nofork', '--address=unix:path='+str(root/'xdg-run/bus'), '--print-address=1'], root, env, 'bus')
            buslog = root / 'bus-private.log'
            wait_for(lambda: bus.poll() is None and buslog.stat().st_size > 0, 3, 'private foreground bus')
            address = buslog.read_text().splitlines()[0]
            require(address.startswith('unix:'), 'private_bus_address_shape')
            receiver = checked_file(REPO, json.loads(args.receiver_record.read_text())) if args.receiver_record else None
            require(receiver is not None, 'separately_built_test_receiver_required')
            owner.launch([str(receiver), str(root), address], root, env, 'receiver', stdin=subprocess.PIPE)
            wait_for(lambda: (root/'receiver-control/ready').exists(), 3, 'private receiver')
            env['DBUS_SESSION_BUS_ADDRESS'] = address
        headers = {}
        if v2:
            password = secrets.token_urlsafe(32)
            env['OPENCODE_PASSWORD'] = password
            headers['Authorization'] = 'Basic ' + base64.b64encode(('opencode:' + password).encode()).decode()
            del password
        server_port = port()
        base = f'http://127.0.0.1:{server_port}'
        server = owner.launch([str(host), 'serve', '--hostname', '127.0.0.1', '--port', str(server_port)], root, env, 'host', cwd=projects[0])
        readiness(server, base, projects[0], args.version, headers)
        request(base, projects[0], '/api/plugin' if v2 else '/config', v2=v2, auth_headers=headers)
        wait_for(lambda: any(x['kind']=='profile' for x in trace(root)), 10, 'native parent profile')
        profiles = [x['value'] for x in trace(root) if x['kind']=='profile']
        require(len(profiles)==1 and profiles[0].get('nativePID')==server.pid and profiles[0]['code']==0 and not profiles[0]['bad'] and not profiles[0]['forced'] and profiles[0]['actualClose'], 'profile_query_unclosed')
        receipt = closed(profiles[0]['receipt'], ('protocol','semantic','generation','resourceClosure'), 'profile_receipt_schema')
        require(receipt.get('protocol')==1, 'profile_protocol_mismatch')
        require(receipt.get('semantic')=='eligible' and receipt.get('generation')==('v2' if v2 else 'v1') and receipt.get('resourceClosure')=='reaped_or_not_started', 'production_profile_unqualified')
        # No public native command on this base proves complete production clock
        # selection/source conversion before a model turn. Snapshot alone is E0.
        raise Unqualified('production_clock_selection_pre_model_gate_unobservable')
        # Staged business/lifecycle body: root must close the exact gate above through
        # product integration, never by a fixture boolean, clock/frame or fake grant.
        for name in COPY if args.suite=='full' else ('completion',):
            native_case(base, projects[0], root, name, v2, headers, provider, webhook, key, report)
        if args.suite=='full':
            scope_roots(base,projects,root,v2,headers,provider,webhook,key,report)
            for name in ('retry','interrupt'):
                start=(len(desktop_rows(root)),webhook.count())
                outcome=terminal_scenario(base,projects[0],root,name,v2,provider,key,auth_headers=headers)
                require(outcome['status']=='native_observed','native_terminal_discriminator_gap')
                if name=='retry':
                    wait_for(lambda: effects(root,webhook,start,'retry'),25,'retry final completion')
                else:
                    require((len(desktop_rows(root)),webhook.count())==start,'interrupt_false_attention')
                outcome.update(status='observed',settlement='unproved_product_final_checkpoint_and_owned_close')
                report['scenarios'].append(outcome)
        setup(candidate, 'update', root, env, args, app)
        require(registration(root,plugin,managed,files['embedded'])==r, 'update_rotated_registration')
        setup(candidate, 'recover', root, env, args)
        setup(candidate, 'remove', root, env, args)
        require(not plugin.exists() and not managed.exists() and server.poll() is None, 'remove_custody_or_host_survival')
        native_case(base,projects[0],root,'completion',v2,headers,provider,webhook,key,report,delivery=False)
        setup(candidate,'install',root,env,args,app)
        next_r=registration(root,plugin,managed,files['embedded'])
        require(all(next_r[k]!=r[k] for k in ('Origin','Salt','Namespace')), 'reinstall_did_not_rotate')
        # Old real loader remains loaded; no binary restoration or fabricated wire.
        native_case(base,projects[0],root,'completion',v2,headers,provider,webhook,key,report,delivery=False)
        owner.stop(server)
        write_json(root/'profile-descriptor.json',{'origin':next_r['Origin'],'executable':str(managed),'controlRoot':str(root/'control')})
        server=owner.launch([str(host),'serve','--hostname','127.0.0.1','--port',str(server_port)],root,env,'reinstalled-host',cwd=projects[0])
        readiness(server,base,projects[0],args.version,headers)
        native_case(base,projects[0],root,'completion',v2,headers,provider,webhook,key,report)
        require(all(digest(p)==sha for p,sha in foreign_files.items()), 'foreign_file_changed')
        after=json.loads((root/'control/agent-notifications.json').read_text())
        require(after.get('notifications')==policy.get('notifications'), 'foreign_policy_changed')
    finally:
        threads = report.pop('_promptThreads', [])
        if 'provider' in locals():
            threads += provider.prompt_threads
        cleanup_error = None
        try:
            owner.close()
        except Exception as e:
            cleanup_error = e
        for t in threads:
            t.join(timeout=8)
            if t.is_alive():
                cleanup_error = Unqualified('prompt_thread_unclosed_after_host_reap')
        if cleanup_error:
            raise cleanup_error
        env.pop('OPENCODE_PASSWORD', None)
        report['fixtureLeadersReaped'] = all(p.poll() is not None for p in owner.processes)
        report['productOwnedClose'] = 'unproved'
        if (root/'native-private.jsonl').exists():
            report['privateTraceSHA256'] = digest(root/'native-private.jsonl')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary','archive','manifest','report'):
        parser.add_argument('--'+name,type=pathlib.Path,required=True)
    parser.add_argument('--manifest-sha256', help='SHA256 of externally staged parent manifest')
    parser.add_argument('--os',choices=('linux','darwin','windows'),required=True)
    parser.add_argument('--arch',choices=('amd64','arm64'),required=True)
    parser.add_argument('--version',choices=VERSIONS,required=True)
    parser.add_argument('--suite',choices=('smoke','full'),default='smoke')
    parser.add_argument('--artifacts',type=pathlib.Path,default=REPO/'.task-tools/artifacts/native')
    parser.add_argument('--receiver-record',type=pathlib.Path)
    parser.add_argument('--validate-only',action='store_true')
    args=parser.parse_args()
    require(args.report.absolute().resolve().is_relative_to(REPO/'.task-tools/artifacts'), 'report_outside_artifacts')
    os.umask(0o077)
    report={'schema':2,'purpose':'TEST installed AN dual native','status':'unqualified',
            'os':args.os,'arch':args.arch,'version':args.version,'suite':args.suite,'scenarios':[],
            'unproved':dict(GAPS),'desktopVisualOutcome':'not_observed'}
    code=1
    try:
        qualify(args,report)
        code=0 if report['status']=='inputs_verified_only' else 1
    except Exception as e:
        report['firstFailedPrerequisite']=str(e) if isinstance(e,Unqualified) else type(e).__name__
    finally:
        args.report.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
        write_json(args.report,report)
        print(json.dumps({'status':report['status'],'firstFailedPrerequisite':report.get('firstFailedPrerequisite')}))
    return code


if __name__=='__main__':
    sys.exit(main())
