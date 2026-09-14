#!/usr/bin/env python3
"""Real config/setup qualification; only release assets and host CLI are adapters.

CONFIG_E2E_BINARY may name a same-source binary built with ConsumerVersion=9.9.9.
Otherwise build once, offline. CONFIG_E2E_GO and CONFIG_E2E_MODCACHE select tools.
All child environments are constructed from an allowlist; no host agent executes.
"""
import copy
import functools
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import tarfile
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]
TAG = 'v9.9.9'
CANARY = 'synthetic-' + hashlib.sha256(b'config-e2e-private').hexdigest()


def put(path, data, mode=0o600):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    path.write_bytes(data.encode() if isinstance(data, str) else data)
    path.chmod(mode)


def snapshot(path):
    return (path.read_bytes(), path.stat().st_mode, path.stat().st_mtime_ns)


def environment(base):
    env = {'PATH': '/usr/bin:/bin', 'GOMAXPROCS': '2'}
    for key in ('SystemRoot', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT'):
        if key in os.environ:
            env[key] = os.environ[key]
    for key in ('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA',
                'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_DATA_HOME',
                'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_DIRS',
                'XDG_DATA_DIRS', 'CODEX_HOME', 'CLAUDE_HOME', 'CLAUDE_CONFIG_DIR',
                'TMPDIR', 'TMP', 'TEMP'):
        name = 'HOME' if key == 'USERPROFILE' else 'TMPDIR' if key in ('TMP', 'TEMP') else key
        path = base / name
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
        env[key] = str(path)
    return env


class Suite:
    def __init__(self, base, binary):
        self.base, self.binary = base, binary
        self.web = base / 'web'
        self.web.mkdir()
        self.bundle = base / 'source'
        # Same-source runtime files; no repository copy, generated build artifacts,
        # agents, or real profiles. Sound bodies are inert fixture assets.
        for name in ('bin/codex-hook-wrapper.sh', 'bin/codex-hook-wrapper.cmd',
                     'bin/hook-wrapper.sh', 'config/config.json'):
            put(self.bundle / name, (ROOT / name).read_bytes(), 0o755 if name.startswith('bin/') else 0o600)
        put(self.bundle / '.claude-plugin/plugin.json', json.dumps({'name': 'claude-notifications-go', 'version': TAG[1:]}))
        for name in ('success', 'error', 'question', 'warning', 'complete'):
            put(self.bundle / ('sounds/' + name + '.mp3'), b'fixture audio')
        production = (ROOT / 'bin/install.sh').read_text()
        stripped = production.strip()
        if not stripped.endswith('main "$@"'):
            raise AssertionError('production installer entrypoint changed')
        # Production publication through the kernel, with isolated acquisition
        # seams: staged/local assets only, no network, no desktop integrations.
        self.installer = stripped[:-len('main "$@"')] + '''
download_utilities() { :; }
configure_windows_native_hooks() { :; }
create_claude_notifications_app() { :; }
setup_iterm2_venv() { :; }
install_linux_notification_desktop_entry() { :; }
install_gnome_activate_window_extension() { :; }
check_github_availability() { return 0; }
main "$@"
'''
        put(self.bundle / 'bin/install.sh', self.installer, 0o755)
        self.asset = 'claude-notifications-' + platform.system().lower() + '-' + ('arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64')
        dest = self.web / 'download' / TAG
        put(dest / self.asset, binary.read_bytes(), 0o755)
        template = (ROOT / 'config/config.json').read_bytes()
        put(dest / 'config.json', template)
        put(dest / 'checksums.txt', ''.join(hashlib.sha256((dest / n).read_bytes()).hexdigest() + '  ' + n + '\n' for n in (self.asset, 'config.json')))
        with tarfile.open(self.web / (TAG + '.tar.gz'), 'w:gz') as archive:
            archive.add(self.bundle, arcname='bundle')
        put(self.web / 'install.sh', self.installer)
        self.requests = []
        requests = self.requests
        self.asset_gate_enabled = False
        self.asset_gate_entered = threading.Event()
        self.asset_gate_release = threading.Event()
        suite = self
        class Handler(http.server.SimpleHTTPRequestHandler):
            def do_GET(self):
                requests.append(self.path)
                if suite.asset_gate_enabled and self.path.endswith('/' + suite.asset):
                    suite.asset_gate_entered.set()
                    assert suite.asset_gate_release.wait(30), 'asset barrier timeout'
                super().do_GET()
            def log_message(self, *_):
                pass
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(self.web)))
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        self.index = 0

    def fixture(self):
        self.index += 1
        base = self.base / ('case-' + str(self.index))
        env = environment(base)
        clis = base / 'clis'
        clis.mkdir()
        # Any host invocation is recorded. Only plugin metadata operations exist.
        put(clis / 'claude', '''#!/usr/bin/python3
import json, os, pathlib, shutil, sys
args=sys.argv[1:]
with open(os.environ['TRACE'],'a') as f: f.write(json.dumps(args)+'\\n')
assert args and args[0]=='plugin'
if os.environ.get('FAIL_REGISTER'): sys.exit(1)
home=pathlib.Path(os.environ['CLAUDE_CONFIG_DIR'])
market=home/'plugins/marketplaces/claude-notifications-go/.claude-plugin'
market.mkdir(parents=True,exist_ok=True)
(market/'plugin.json').write_text('{"version":"9.9.9"}')
if args[1]=='marketplace': sys.exit()
root=home/'plugins/cache/claude-notifications-go/claude-notifications-go/9.9.9'
shutil.copytree(os.environ['SOURCE'],root,dirs_exist_ok=True)
(home/'plugins/installed_plugins.json').write_text(json.dumps({'plugins':{'claude-notifications-go@claude-notifications-go':[{'installPath':str(root),'version':'9.9.9'}]}}))
''', 0o755)
        for name in ('codex', 'notify-send', 'osascript', 'paplay', 'aplay', 'curl'):
            if name == 'curl':
                # Bootstrap can download only from the fixture HTTP server.
                body = '#!/usr/bin/python3\nimport os,sys\nassert all(not a.startswith(("http:","https:")) or a.startswith(os.environ["LOCAL_URL"]+"/") for a in sys.argv[1:])\nos.execv("/usr/bin/curl",["curl"]+sys.argv[1:])\n'
            else:
                body = '#!/bin/sh\necho invoked >> "$EFFECTS"\nexit 97\n'
            put(clis / name, body, 0o755)
        env.update(PATH=str(clis) + ':/usr/bin:/bin', TRACE=str(base / 'trace'),
                   EFFECTS=str(base / 'effects'), SOURCE=str(self.bundle), LOCAL_URL=self.url,
                   BOOTSTRAP_RELEASE_TAG=TAG, BOOTSTRAP_SOURCE_BASE_URL=self.url,
                   BOOTSTRAP_RELEASES_BASE_URL=self.url, INSTALL_SCRIPT_URL=self.url + '/install.sh')
        return env

    def open_canary_watch(self):
        # Linux inotify reports reads as well as writes to the sibling canary.
        import ctypes
        libc = ctypes.CDLL(None, use_errno=True)
        watch = libc.inotify_init1(os.O_NONBLOCK | os.O_CLOEXEC)
        assert watch >= 0, 'cannot monitor fixture canary'
        mask = 0x1 | 0x2 | 0x4 | 0x20 | 0x400 | 0x800
        assert libc.inotify_add_watch(watch, os.fsencode(self.base / 'outside-fixtures-canary'), mask) >= 0
        return watch

    def close_canary_watch(self, watch):
        try:
            try:
                events = os.read(watch, 65536)
            except BlockingIOError:
                events = b''
            assert not events, 'child accessed outside-fixtures canary'
        finally:
            os.close(watch)

    def run(self, env, args, expected=0, data=None):
        # Observe only the child interval; our own before/after snapshots read it.
        watch = self.open_canary_watch()
        try:
            r = subprocess.run([str(x) for x in args], env=env, input=data, capture_output=True,
                               cwd=env['HOME'], timeout=45, start_new_session=True)
        finally:
            self.close_canary_watch(watch)
        assert CANARY.encode() not in r.stdout + r.stderr, 'canary leaked in diagnostics'
        assert r.returncode == expected, 'exit expected=%s observed=%s\n%s' % (expected, r.returncode, (r.stdout+r.stderr).decode(errors='replace'))
        return r

    def cli(self, env, *args, expected=0, data=None):
        return self.run(env, [self.binary, 'config', *args], expected, data)

    def boot(self, env, product, expected=0):
        return self.run(env, ['/bin/bash', ROOT / 'bin/bootstrap.sh', '--product', product], expected)

    def paths(self, env):
        return (Path(env['HOME']) / '.claude/claude-notifications-go/config.json',
                Path(env['XDG_CONFIG_HOME']) / 'agent-notifications/config.json')

    def events(self, env):
        p = Path(env['TRACE'])
        return p.read_text().splitlines() if p.exists() else []

    def history(self, env, content, version='9.9.9'):
        home = Path(env['CLAUDE_CONFIG_DIR'])
        active = home / 'plugins/cache/claude-notifications-go/claude-notifications-go' / version
        put(active / 'config/config.json', content)
        put(active / 'bin/runtime', b'working-runtime')
        put(active / '.claude-plugin/plugin.json', json.dumps({'version': version}))
        put(active.parent / '99.99.99/config/config.json', b'{"unused":true}')
        put(home / 'plugins/installed_plugins.json', json.dumps({'plugins': {'claude-notifications-go@claude-notifications-go': [{'installPath': str(active), 'version': version}]}}))
        return active

    def fresh(self, product):
        env = self.fixture()
        self.requests.clear()
        self.boot(env, product)
        assert sum(p.endswith('/' + self.asset) for p in self.requests) == 1
        legacy, neutral = self.paths(env)
        assert neutral.exists() and not legacy.exists()
        assert json.loads(self.cli(env, 'path', '--json').stdout)['path'] == str(neutral)
        before = snapshot(neutral)
        self.boot(env, product)
        assert snapshot(neutral) == before, 'repair rewrote config bytes/mode/mtime'
        if product in ('codex', 'both'):
            assert (Path(env['CODEX_HOME']) / 'hooks.json').exists()
        assert not Path(env['EFFECTS']).exists()

    def legacy(self):
        env = self.fixture()
        legacy, neutral = self.paths(env)
        put(legacy, b'{ "volume": 0.25, "future": {"n":9007199254740993} }\n', 0o644)
        os.utime(legacy, ns=(1234567890000000000, 1234567890000000000))
        before = snapshot(legacy)
        self.boot(env, 'both')
        assert snapshot(legacy) == before and not neutral.exists()

    def explicit_and_import_edit(self):
        env = self.fixture()
        target = Path(env['HOME']) / 'private/config.json'
        env['AGENT_NOTIFICATIONS_CONFIG'] = str(target)
        self.cli(env, 'inspect', '--json', expected=1)
        raw = {'notifications': {'desktop': {'volume': 0.5, 'enabled': False, 'future': {'n': 9007199254740993}}, 'webhook': {'enabled': False, 'url': '${WEBHOOK_URL}', 'headers': {'Authorization': CANARY}, 'payloadFields': {'future': '${{git.branch}}'} }}, 'statuses': {'task_complete': {'sound': '${CLAUDE_PLUGIN_ROOT}/sounds/success.mp3', 'future': [None, False, 9007199254740993]}},
               'futureAgent': {'secret': CANARY, 'template': '${{git.branch}}', 'env': '${WEBHOOK_URL}'}}
        source = Path(env['HOME']) / 'import.json'
        put(source, json.dumps(raw))
        before = snapshot(source)
        self.cli(env, 'init', '--from', str(source), '--json')
        assert snapshot(source) == before and json.loads(target.read_bytes()) == raw
        inspection = json.loads(self.cli(env, 'inspect', '--json').stdout)
        edits = {'set': {'/notifications/desktop/volume': 0.2, '/statuses/task_complete/sound': '${CLAUDE_PLUGIN_ROOT}/sounds/error.mp3'}}
        self.cli(env, 'edit', '--stdin', '--expect-revision', inspection['revision'], data=json.dumps(edits).encode())
        expected = copy.deepcopy(raw)
        expected['notifications']['desktop']['volume'] = 0.2
        expected['statuses']['task_complete']['sound'] = edits['set']['/statuses/task_complete/sound']
        assert json.loads(target.read_bytes()) == expected
        after = snapshot(target)
        self.cli(env, 'edit', '--stdin', '--expect-revision', inspection['revision'], data=json.dumps(edits).encode(), expected=1)
        assert snapshot(target) == after
        self.cli(env, 'init', '--from', str(source), '--json')
        assert snapshot(target) == after
        self.boot(env, 'both')
        assert snapshot(target) == after and not any(p.exists() for p in self.paths(env))

    def corrupt(self):
        for content in (b'', b'{invalid', b'null'):
            env = self.fixture()
            legacy, neutral = self.paths(env)
            put(legacy, content)
            put(neutral, b'{}')
            env['PLUGIN_ROOT'] = str(self.bundle)
            before = snapshot(legacy), snapshot(neutral)
            self.cli(env, 'inspect', '--json', expected=1)
            self.cli(env, 'init', '--json', expected=1)
            self.boot(env, 'both', expected=1)
            assert (snapshot(legacy), snapshot(neutral)) == before and not self.events(env)

    def historical(self, kind):
        env = self.fixture()
        template = (ROOT / 'config/config.json').read_bytes()
        active = self.history(env, template if kind == 'exact' else b'{"future":true}', '8.8.8' if kind == 'unknown' else '9.9.9')
        before = snapshot(active / 'config/config.json')
        self.boot(env, 'claude', expected=0 if kind == 'exact' else 1)
        if kind != 'exact':
            assert not self.events(env) and snapshot(active / 'config/config.json') == before
            assert (active / 'bin/runtime').read_bytes() == b'working-runtime'

    def custom_and_overlap(self):
        for overlap in (False, True):
            env = self.fixture()
            if overlap:
                active = self.history(env, b'{}')
                env['AGENT_NOTIFICATIONS_CONFIG'] = str(active / 'config/config.json')
            else:
                put(Path(env['CLAUDE_CONFIG_DIR']) / 'claude-notifications-go/config.json', b'{"future":true}')
            self.boot(env, 'both', expected=1)
            assert not self.events(env) and not self.paths(env)[1].exists()

    def offline(self):
        env = self.fixture()
        self.boot(env, 'both')
        config = self.paths(env)[1]
        hooks = Path(env['CODEX_HOME']) / 'hooks.json'
        before = snapshot(config), snapshot(hooks), self.events(env)
        env['BOOTSTRAP_RELEASES_BASE_URL'] = self.url + '/offline'
        self.boot(env, 'both', expected=1)
        assert (snapshot(config), snapshot(hooks), self.events(env)) == before

    def setup_readonly(self):
        env = self.fixture()
        def tree():
            return {str(p): snapshot(p) if p.is_file() else ('directory', p.stat().st_mode) for p in Path(env['HOME']).parent.rglob('*')}
        before = tree()
        for flag in ('--print', '--dry-run'):
            self.run(env, [self.binary, 'setup-codex', '--plugin-root', self.bundle, flag])
            assert tree() == before
        assert not any(p.exists() for p in self.paths(env))

    def hooks(self):
        for malformed in (False, True):
            env = self.fixture()
            env['PLUGIN_ROOT'] = str(self.bundle)
            legacy, neutral = self.paths(env)
            if malformed:
                put(legacy, b'{invalid')
            before = snapshot(legacy) if malformed else None
            r = self.run(env, [self.binary, 'handle-hook', 'Stop', '--product', 'codex'], data=b'{}')
            assert r.stdout == r.stderr == b''
            assert not neutral.parent.exists()
            assert not Path(env['EFFECTS']).exists()
            if malformed:
                assert snapshot(legacy) == before
            else:
                assert not legacy.parent.exists()

    def partial_retry(self):
        import fcntl
        for standalone in (True, False):
            env = self.fixture()
            lock = Path(env['HOME']) / '.agent-notifications-config.lock'
            with lock.open('w') as held:
                lock.chmod(0o600)
                fcntl.flock(held, fcntl.LOCK_EX)
                if standalone:
                    r = self.run(env, [self.binary, 'setup-codex', '--plugin-root', self.bundle], expected=3)
                else:
                    r = self.boot(env, 'codex', expected=1)
            hooks = Path(env['CODEX_HOME']) / 'hooks.json'
            assert hooks.exists() and not self.paths(env)[1].exists()
            before = snapshot(hooks)
            if standalone:
                command = [self.binary, 'config', 'init', '--json']
            else:
                output = (r.stdout + r.stderr).decode()
                line = next(line for line in output.splitlines() if line.startswith('Config-only retry'))
                command = shlex.split(line.split(': ', 1)[1])
                assert Path(command[0]).read_bytes() == self.binary.read_bytes()
                assert command[1:] == ['config', 'init', '--json']
            self.run(env, command)
            assert self.paths(env)[1].exists() and snapshot(hooks) == before
            assert not self.events(env)

    def registration_failure(self):
        env = self.fixture()
        env['FAIL_REGISTER'] = '1'
        self.boot(env, 'claude', expected=1)
        assert not any(p.exists() for p in self.paths(env))
        env = self.fixture()
        put(Path(env['CODEX_HOME']) / 'hooks.json', b'{invalid')
        self.boot(env, 'codex', expected=1)
        assert not any(p.exists() for p in self.paths(env))

    def concurrent_install_hook_settings(self):
        """A real bootstrap, hook reader, and CAS edit overlap at OS barriers."""
        import fcntl
        import signal
        import time
        env = self.fixture()
        config = self.paths(env)[1]
        initial = json.loads((ROOT / 'config/config.json').read_bytes())
        initial['notifications']['desktop']['enabled'] = False
        initial['notifications']['desktop']['sound'] = False
        initial['notifications']['webhook']['enabled'] = False
        initial['futureConcurrent'] = {'integer': 9007199254740993, 'installed': True}
        source = Path(env['HOME']) / 'concurrent-source.json'
        put(source, json.dumps(initial))
        self.cli(env, 'init', '--from', str(source), '--json')
        inspection = json.loads(self.cli(env, 'inspect', '--json').stdout)
        edits = {'set': {
            '/notifications/desktop/volume': 0.23,
            '/statuses/task_complete/title': 'concurrent edit survived',
        }}

        # The HTTP barrier holds the real installer after it has requested its
        # verified helper. The persistent target lock independently holds the
        # real settings writer while the hook reads the last complete config.
        # No timing sleep decides when any participant may cross a boundary.
        self.asset_gate_entered.clear()
        self.asset_gate_release.clear()
        self.asset_gate_enabled = True
        lock = Path(str(config) + '.lock')
        lock.touch(mode=0o600, exist_ok=True)
        processes = []
        install = settings = hook = None
        canary_watch = self.open_canary_watch()
        def start(args, stdin):
            proc = subprocess.Popen(args, env=env, cwd=env['HOME'], stdin=stdin,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    start_new_session=True)
            processes.append(proc)
            return proc
        def feed(proc, payload):
            proc.stdin.write(payload)
            proc.stdin.close()
            proc.stdin = None
        def await_open_fd(proc, path):
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline and proc.poll() is None:
                try:
                    if any(fd.resolve() == path for fd in Path('/proc/%d/fd' % proc.pid).iterdir()):
                        return
                except OSError:
                    pass
                threading.Event().wait(0.01)
            raise AssertionError('settings writer did not contend on config lock')
        try:
            with lock.open('r+') as held:
                fcntl.flock(held, fcntl.LOCK_EX)
                install = start(['/bin/bash', ROOT / 'bin/bootstrap.sh', '--product', 'both'],
                                subprocess.DEVNULL)
                assert self.asset_gate_entered.wait(15), 'installer did not reach asset barrier'
                settings = start(
                    [self.binary, 'config', 'edit', '--stdin', '--expect-revision', inspection['revision']],
                    subprocess.PIPE)
                hook = start([self.binary, 'handle-hook', 'Stop'], subprocess.PIPE)
                # Supply EOF before observing either command. This proves a live
                # settings process has opened the exclusively-held lock rather
                # than merely waiting for its stdin or its first scheduler slice.
                feed(settings, json.dumps(edits).encode())
                feed(hook, b'{"session_id":"concurrent","transcript_path":"","cwd":""}')
                await_open_fd(settings, lock)
                assert settings.poll() is None, 'settings writer bypassed config lock'
                hook_out = hook.communicate(timeout=30)
                assert hook.returncode == 0, 'hook failed while settings was blocked: %s' % ((hook_out[0] + hook_out[1]).decode(errors='replace'))
                self.asset_gate_release.set()
                fcntl.flock(held, fcntl.LOCK_UN)

            settings_out = settings.communicate(timeout=30)
            install_out = install.communicate(timeout=45)
        finally:
            self.asset_gate_release.set()
            self.asset_gate_enabled = False
            for proc in processes:
                if proc.poll() is None:
                    try:
                        os.killpg(proc.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)
            self.close_canary_watch(canary_watch)
        for name, proc, output in (('settings', settings, settings_out), ('hook', hook, hook_out),
                                   ('installer', install, install_out)):
            assert proc.returncode == 0, '%s failed: %s' % (name, (output[0] + output[1]).decode(errors='replace'))
            assert CANARY.encode() not in output[0] + output[1], '%s leaked canary' % name
        assert b'ConfigInvalid' not in hook_out[0] + hook_out[1], 'hook observed an invalid/partial config'
        final = json.loads(config.read_bytes())
        assert final['futureConcurrent'] == initial['futureConcurrent']
        assert final['notifications']['desktop']['volume'] == 0.23
        assert final['statuses']['task_complete']['title'] == 'concurrent edit survived'
        assert final['notifications']['desktop']['enabled'] is False
        assert final['notifications']['desktop']['sound'] is False
        assert final['notifications']['webhook']['enabled'] is False
        for key, value in initial.items():
            if key not in ('notifications', 'statuses', 'futureConcurrent'):
                assert final[key] == value, 'installation/settings lost root field ' + key
        assert not Path(env['EFFECTS']).exists(), 'hook invoked a desktop/webhook provider'


def main():
    assert platform.system() == 'Linux', 'This suite qualifies native Linux; macOS/Windows require native CI adapters'
    with tempfile.TemporaryDirectory(prefix='real-config-e2e-') as tmp:
        base = Path(tmp)
        # A sibling canary is outside every child fixture environment.
        canary = base / 'outside-fixtures-canary'
        put(canary, CANARY)
        original = snapshot(canary)
        binary = os.environ.get('CONFIG_E2E_BINARY')
        if binary:
            binary = Path(binary).resolve()
        else:
            binary = base / 'helper'
            env = environment(base / 'build')
            go = os.environ.get('CONFIG_E2E_GO') or shutil.which('go')
            assert go, 'Go toolchain required or set CONFIG_E2E_BINARY'
            env.update(GOCACHE=str(base / 'gocache'), GOMODCACHE=os.environ.get('CONFIG_E2E_MODCACHE', str(Path.home() / 'go/pkg/mod')),
                       GOPROXY='off', GOSUMDB='off', GOFLAGS='-p=2', PATH=str(Path(go).parent)+':/usr/bin:/bin')
            subprocess.run([go, 'build', '-p', '2', '-ldflags', '-X github.com/777genius/agent-notifications/internal/config.ConsumerVersion=9.9.9', '-o', str(binary), './cmd/claude-notifications'], cwd=ROOT, env=env, check=True)
        suite = Suite(base, binary)
        failures = []
        cases = [(f'fresh-{p}-and-repair', lambda p=p: suite.fresh(p)) for p in ('claude', 'codex', 'both')]
        cases += [('legacy-exact', suite.legacy), ('explicit-import-CAS', suite.explicit_and_import_edit),
                  ('corrupt-no-fallback', suite.corrupt), ('custom-and-overlap', suite.custom_and_overlap),
                  ('setup-readonly', suite.setup_readonly), ('offline-retains-state', suite.offline), ('registration-failure', suite.registration_failure), ('hooks-no-config-writes', suite.hooks), ('partial-config-only-retry', suite.partial_retry)]
        cases += [('concurrent-install-hook-settings', suite.concurrent_install_hook_settings)]
        cases += [('historical-' + k, lambda k=k: suite.historical(k)) for k in ('personalized', 'unknown', 'exact')]
        try:
            for name, case in cases:
                try:
                    case()
                    assert snapshot(canary) == original, 'outside canary changed'
                    print('PASS', name, flush=True)
                except Exception as error:
                    failures.append(name)
                    print('FAIL', name, str(error).replace(CANARY, '[canary]'), flush=True)
            print('RESULT %d/%d passed; failures=%s' % (len(cases)-len(failures), len(cases), ','.join(failures)), flush=True)
        finally:
            suite.server.shutdown()
            suite.server.server_close()
        return bool(failures)


if __name__ == '__main__':
    raise SystemExit(main())
