#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# Isolated unit/adapter fixtures: no public network, real host CLIs or Go builds.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SANDBOX=$(mktemp -d /tmp/bootstrap-products-XXXXXX)
trap 'rm -rf "$SANDBOX"' EXIT
test_env_setup "$SANDBOX"
# Keep space-containing paths in the bootstrap characterization.
export HOME="$SANDBOX/home space" USERPROFILE="$SANDBOX/home space" CODEX_HOME="$SANDBOX/codex space"
export CLAUDE_CONFIG_DIR="$SANDBOX/claude config" CLAUDE_HOME="$SANDBOX/claude home"
mkdir -p "$HOME" "$CODEX_HOME" "$CLAUDE_CONFIG_DIR" "$CLAUDE_HOME"
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$SANDBOX/functions.sh"
source "$SANDBOX/functions.sh"
for product in claude codex both; do
    PRODUCT=""; select_product --product "$product"; [ "$PRODUCT" = "$product" ]
done
for args in '--product invalid' '--product' '--unknown' '--product claude --product codex'; do
    if ( PRODUCT=""; select_product $args ); then echo "accepted $args"; exit 1; fi
done
for tag in v1.42.0 v1.43.2 v2.0.0; do
    BOOTSTRAP_RELEASE_TAG="$tag" resolve_bootstrap_release
    [ "$BOOTSTRAP_TAG" = "$tag" ]
done
for tag in v1.41.0 v0.99.0 v1.42.0-rc1 v01.42.0 v1.042.0 v1.42.00 v99999999999999999999.0.0 main; do
    if BOOTSTRAP_RELEASE_TAG="$tag" resolve_bootstrap_release; then exit 1; fi
done
# setup_marketplace self-heals a marketplace declared under a retired repo
# name, but leaves an unrelated source conflict alone.
(
    # shellcheck disable=SC2034 # consumed by the sourced setup_marketplace
    MARKETPLACE_SOURCE="new/repo"
    # shellcheck disable=SC2034
    MARKETPLACE_NAME="claude-notifications-go"
    # shellcheck disable=SC2034
    LEGACY_MARKETPLACE_REPOS="old/retired-repo"
    config_preflight() { :; }
    calls="$SANDBOX/marketplace-calls"; declared_repo="old/retired-repo"
    claude() {
        printf '%s\n' "$*" >> "$calls"
        if [ "$1 $2 $3" = "plugin marketplace add" ]; then
            if [ ! -f "$SANDBOX/marketplace-add-called" ]; then
                touch "$SANDBOX/marketplace-add-called"
                echo "Failed to add marketplace: its network source differs from the one declared for it in settings" >&2
                return 1
            fi
            return 0
        elif [ "$1 $2 $3" = "plugin marketplace list" ]; then
            printf '[{"name":"claude-notifications-go","repo":"%s"}]\n' "$declared_repo"
            return 0
        elif [ "$1 $2 $3" = "plugin marketplace remove" ]; then
            return 0
        fi
        return 0
    }
    : > "$calls"; rm -f "$SANDBOX/marketplace-add-called"
    setup_marketplace
    [ "$(grep -c '^plugin marketplace add' "$calls")" = 2 ] || { echo "expected retry add after self-heal"; exit 1; }
    grep -q '^plugin marketplace remove claude-notifications-go$' "$calls" || { echo "expected self-heal remove"; exit 1; }

    declared_repo="someone-else/unrelated-fork"
    : > "$calls"; rm -f "$SANDBOX/marketplace-add-called"
    setup_marketplace
    [ "$(grep -c '^plugin marketplace add' "$calls")" = 1 ] || { echo "unrelated conflict must not retry add"; exit 1; }
    if grep -q '^plugin marketplace remove' "$calls"; then echo "unrelated conflict must not self-heal"; exit 1; fi
)
echo "marketplace self-heal fixtures passed"
# macOS resource refresh outside the cache must also protect explicit config.
(
    PRODUCT=claude
    _CONFIG_STAGE="$SANDBOX/preflight-resource"; mkdir "$_CONFIG_STAGE"
    printf '{"plugins":{}}\n' > "$_CONFIG_STAGE/installed-before.json"
    uname() { printf 'Darwin\n'; }
    capture_preflight() { cat > "$_CONFIG_STAGE/request.json"; printf '{"status":"safe"}\n'; }
    _CONFIG_HELPER=capture_preflight
    config_preflight
    python3 - "$_CONFIG_STAGE/request.json" "$HOME" <<'PYRESOURCE'
import json,pathlib,sys
v=json.load(open(sys.argv[1]))
expected=(pathlib.Path(sys.argv[2])/'.claude'/'claude-notifications-go'/'iterm2-venv').resolve()
actual=[pathlib.Path(entry).resolve() for entry in v['refreshDirs']]
assert expected in actual, f'expected refresh dir {expected!s}; got {[str(path) for path in actual]!r}'
PYRESOURCE
)
# Dispatch tests preserve shared bundle state and isolate CN_PRODUCT.
print_header() { :; }; abort_if_wsl_environment() { :; }
check_prerequisites() { :; }; detect_platform() { :; }
resolve_bootstrap_release() { BOOTSTRAP_TAG=v1.43.2; }
install_claude() { [ "$CN_PRODUCT" = claude ]; PLUGIN_ROOT='bundle space'; echo claude >> "$SANDBOX/calls"; }
install_codex() { [ "${CN_PRODUCT:-}" = sentinel ]; echo codex >> "$SANDBOX/calls"; }
stage_historical_baselines() { :; }; stage_config_helper() { :; }; config_preflight() { :; }; initialize_config() { :; }
export CN_PRODUCT=sentinel
for product in claude codex both; do
    : > "$SANDBOX/calls"
    PRODUCT=""; main --product "$product"
    case "$product" in
        claude) [ "$(cat "$SANDBOX/calls")" = claude ] ;;
        codex) [ "$(cat "$SANDBOX/calls")" = codex ] ;;
        both) [ "$(cat "$SANDBOX/calls")" = "$(printf 'claude\ncodex')" ]; [ "$PLUGIN_ROOT" = 'bundle space' ] ;;
    esac
done
install_codex() { return 1; }
if ( PRODUCT=""; main --product both ); then exit 1; fi
# main installs its own trap; restore test-owned sandbox cleanup.
trap 'rm -rf "$SANDBOX"' EXIT
printf 'bootstrap product unit fixtures passed\n'
# Local HTTP and controlling-PTY integration. Installer/registration are explicit
# fake adapters here; the fetched bootstrap, archive extraction and curl are real.
python3 - "$ROOT" "$SANDBOX" <<'PY'
import functools, http.server, io, json, os, pathlib, select, shlex, shutil, subprocess, sys, tarfile, threading, time
if os.name != "nt":
    import pty
root, sandbox = map(pathlib.Path, sys.argv[1:])
web = sandbox / 'http'; web.mkdir()
(web / 'bootstrap.sh').write_bytes((root / 'bin/bootstrap.sh').read_bytes())
(web / 'latest').write_text('{"tag_name":"v1.42.0"}')
uname_os=subprocess.check_output(['uname','-s'],text=True).strip().lower()
uname_arch=subprocess.check_output(['uname','-m'],text=True).strip().lower()
asset_os='windows' if uname_os.startswith(('mingw','msys','cygwin')) else uname_os
asset_arch='arm64' if uname_arch in ('arm64','aarch64') else 'amd64'
asset_name='claude-notifications-'+asset_os+'-'+asset_arch+('.exe' if asset_os=='windows' else '')
installer = '''#!/bin/bash
set -eu
[ "$1" = --force ]
cp "$INSTALL_STAGED_ASSETS"/claude-notifications-* "$INSTALL_TARGET_DIR/claude-notifications"
chmod +x "$INSTALL_TARGET_DIR/claude-notifications"
cp "$INSTALL_TARGET_DIR/claude-notifications" "$INSTALL_TARGET_DIR/claude-notifications-windows-amd64.exe"
'''
binary = '''#!/usr/bin/env python3
import json, os, pathlib, sys
args=sys.argv[1:]
if os.environ.get('FIXTURE_TRACE'):
    with open(os.environ['FIXTURE_TRACE'],'a') as f: f.write(json.dumps(args)+'\\n')
if args==['--version']:
    print('claude-notifications v1.42.0'); sys.exit()
if args[0]=='setup-codex':
    if os.environ.get('FAIL_REGISTER')=='1': sys.exit(1)
    if '--dry-run' not in args:
        p=pathlib.Path(os.environ['CODEX_HOME'])
        if '--codex-home' in args:
            p=pathlib.Path(args[args.index('--codex-home')+1])
        p.mkdir(parents=True, exist_ok=True)
        (p/'fixture-registration').write_text('registered')
        dest=p/'claude-notifications-go'/'bin'
        dest.mkdir(parents=True, exist_ok=True)
        target=dest/'claude-notifications'
        target.write_bytes(pathlib.Path(sys.argv[0]).read_bytes())
        target.chmod(0o755)
        if os.environ.get('FAIL_SETUP_INIT')=='1': sys.exit(3)
    sys.exit()
if args[0]=='setup-notifications':
    sys.exit()
assert args[0]=='config'
legacy=pathlib.Path(os.environ['HOME'])/'.claude/claude-notifications-go/config.json'
neutral=pathlib.Path(os.environ['XDG_CONFIG_HOME'])/'agent-notifications/config.json'
explicit=os.environ.get('AGENT_NOTIFICATIONS_CONFIG')
p=pathlib.Path(explicit) if explicit else legacy if legacy.exists() else neutral
selected=dict(path=str(p),source='explicit' if explicit else 'legacy' if p==legacy else 'universal',exists=p.exists())
if args[1]=='path': print(json.dumps(selected)); sys.exit()
if args[1]=='preflight-update':
    request=json.load(sys.stdin)
    status='safe'
    if any(p.resolve().is_relative_to(pathlib.Path(d).resolve()) for d in request['refreshDirs']): status='unsafe-target'
    if any(p.resolve()==pathlib.Path(d).resolve() for d in request.get('protectedPaths',[])): status='unsafe-target'
    def customized(c):
        candidate=pathlib.Path(c['path'])
        if not candidate.exists(): return False
        baseline=c.get('baselinePath')
        return not baseline or candidate.read_bytes()!=pathlib.Path(baseline).read_bytes()
    if not explicit and not p.exists() and any(customized(c) for c in request['historicalCandidates']): status='import-required'
    if os.environ.get('FIXTURE_REQUEST'):
        pathlib.Path(os.environ['FIXTURE_REQUEST']).write_text(json.dumps(request))
    print(json.dumps(dict(selected,status=status)))
    sys.exit(0 if status=='safe' else 1)
assert args[1]=='init'
if os.environ.get('FAIL_INIT')=='1': sys.exit(1)
changed=not p.exists()
if changed:
    p.parent.mkdir(parents=True,exist_ok=True); p.write_text('{}')
print(json.dumps(dict(selected,changed=changed)))
'''
for tag in ['v1.42.0', 'v1.43.0']:
    with tarfile.open(web / (tag + '.tar.gz'), 'w:gz') as archive:
        for name, data in {'bin/install.sh': installer, '.claude-plugin/plugin.json': '{"version":"'+tag[1:]+'"}'}.items():
            data = data.encode('utf-8'); entry = tarfile.TarInfo('bundle/' + name); entry.size = len(data); entry.mode = 0o755
            archive.addfile(entry, io.BytesIO(data))
    dest = web / 'download' / tag; dest.mkdir(parents=True)
    payload=binary.replace('v1.42.0', tag).encode('utf-8')
    (dest / 'binary').write_bytes(payload)
    (dest / asset_name).write_bytes(payload)
    import hashlib
    (dest / 'checksums.txt').write_bytes((hashlib.sha256(payload).hexdigest()+'  '+asset_name+'\n').encode('ascii'))
(web/'install.sh').write_bytes(installer.encode('utf-8'))
request_paths=[]
class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        request_paths.append(self.path)
        super().do_GET()
    def log_message(self, *args): pass
server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(web)))
threading.Thread(target=server.serve_forever, daemon=True).start()
base = 'http://127.0.0.1:' + str(server.server_port)
env_keys = (
    'PATH', 'SystemRoot', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT',
    'HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME',
    'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_RUNTIME_DIR',
    'XDG_CONFIG_DIRS', 'XDG_DATA_DIRS', 'CODEX_HOME', 'CLAUDE_HOME',
    'CLAUDE_CONFIG_DIR', 'TMP', 'TEMP', 'TMPDIR',
)
env = {key: os.environ[key] for key in env_keys if key in os.environ}
env.update(INSTALL_SCRIPT_URL=base+'/install.sh', BOOTSTRAP_LATEST_RELEASE_API_URL=base+'/latest', BOOTSTRAP_SOURCE_BASE_URL=base, BOOTSTRAP_RELEASES_BASE_URL=base)
cli = sandbox / 'clis'; cli.mkdir()
(cli / 'codex').write_bytes(b'#!/bin/sh\nexit 99\n'); (cli / 'codex').chmod(0o755)
bash = shutil.which('bash'); assert bash
env['PATH'] = str(cli) + os.pathsep + (os.environ['PATH'] if os.name == 'nt' else '/usr/bin:/bin')
script = str(root / 'bin/bootstrap.sh')
def run(args, expected=0, extra=None):
    result = subprocess.run([bash, script]+args, env=dict(env, **(extra or {})), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True, timeout=20)
    assert (result.returncode == 0) == (expected == 0), result.stdout.decode()
    return result.stdout.decode()
assert 'No controlling TTY' in run([], 1)
assert 'claude CLI not found' in run(['--product', 'both'], 1)
(cli / 'codex').rename(cli / 'absent')
assert 'codex CLI not found' in run(['--product', 'codex'], 1)
(cli / 'absent').rename(cli / 'codex')
run(['--product', 'codex']); run(['--product', 'codex'])
run(['--product', 'codex'], extra={'BOOTSTRAP_RELEASE_TAG':'v1.43.0'})
registration = pathlib.Path(env['CODEX_HOME']) / 'fixture-registration'
before = registration.read_bytes()
# Reject mixed binary/source releases before registration and retain live state.
payload_file = web / 'download/v1.42.0' / asset_name
valid_payload = payload_file.read_bytes()
payload_file.write_bytes(valid_payload.replace(b'v1.42.0', b'v1.41.0'))
run(['--product', 'codex'], 1)
assert registration.read_bytes() == before
payload_file.write_bytes(valid_payload)

run(['--product', 'codex'], 1, {'FAIL_REGISTER':'1'})
run(['--product', 'codex'], 1, {'BOOTSTRAP_SOURCE_BASE_URL':base+'/missing'})
assert registration.read_bytes() == before
# Matching Claude manifest with stale runtime must force a fresh staged binary.
live = sandbox / 'live claude'; (live / 'bin').mkdir(parents=True); (live / '.claude-plugin').mkdir()
(live / '.claude-plugin/plugin.json').write_text('{"version":"1.42.0"}')
(live / 'bin/install.sh').write_bytes(installer.encode('utf-8'))
(live / 'bin/claude-notifications').write_text('stale')
command = 'source '+shlex.quote(str(sandbox/'functions.sh'))+'; PRODUCT=both; PLUGIN_ROOT='+shlex.quote(str(live))+'; BOOTSTRAP_TAG=v1.42.0; install_cleanup_traps; stage_config_helper; config_preflight; install_codex'
r = subprocess.run([bash,'-c',command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
assert r.returncode == 0, r.stdout.decode()
assert (live/'bin/claude-notifications').read_text() == 'stale'
# Menu routing for Claude/both uses explicit adapters; Codex below exercises
# the complete bootstrap HTTP/staging path with fake runtime assets.
dispatch = (root / 'bin/bootstrap.sh').read_text(encoding='utf-8').replace('main "$@"', '')
dispatch += '\ncheck_prerequisites() { :; }\nresolve_bootstrap_release() { :; }\nstage_historical_baselines() { :; }\nstage_config_helper() { :; }\nconfig_preflight() { :; }\ninitialize_config() { :; }\ninstall_claude() { echo CLAUDE_ADAPTER; }\ninstall_codex() { echo CODEX_ADAPTER; }\nmain "$@"\n'
(web / 'dispatch.sh').write_bytes(dispatch.encode('utf-8'))
for choice, success in ([('1',True), ('2',True), ('3',True), ('invalid',False)] if os.name != 'nt' else []):
    entry = '/dispatch.sh' if choice in ['1', '3'] else '/bootstrap.sh'
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(bash,[bash,'-c', 'curl -fsSL '+base+entry+' | bash'],env)
    output = b''; sent = False; deadline = time.monotonic()+20
    while time.monotonic() < deadline:
        if select.select([fd],[],[],0.1)[0]:
            try: chunk = os.read(fd,65536)
            except OSError: break
            if not chunk: break
            output += chunk
            if b'Choice:' in output and not sent:
                os.write(fd,(choice+'\n').encode()); sent=True
    else:
        os.kill(pid,9); raise AssertionError('PTY timeout')
    _, status = os.waitpid(pid,0); os.close(fd)
    assert sent and (os.waitstatus_to_exitcode(status)==0)==success, output.decode()
    if choice in ['1','3']: assert b'CLAUDE_ADAPTER' in output
    if choice == '1': assert b'CODEX_ADAPTER' not in output
    if choice == '3': assert b'CODEX_ADAPTER' in output
assert not list(pathlib.Path(env['TMPDIR']).glob('bootstrap-codex-*'))
assert not list(pathlib.Path(env['TMPDIR']).glob('bootstrap-release-*'))
# Protected flow E2E. Real shell orchestration and local downloads; explicit
# fake config CLI models the coordinated contract, not Go resolver evidence.
trace=sandbox/'trace'; request=sandbox/'request.json'
env.update(FIXTURE_TRACE=str(trace),FIXTURE_REQUEST=str(request))
claude_script='''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['FIXTURE_TRACE'],'a') as f: f.write(json.dumps(['claude']+args)+'\\n')
if os.environ.get('FAIL_CLAUDE')=='1': sys.exit(1)
home=pathlib.Path(os.environ['CLAUDE_CONFIG_DIR'])
market=home/'plugins/marketplaces/claude-notifications-go/.claude-plugin'
market.mkdir(parents=True,exist_ok=True)
(market/'plugin.json').write_text('{"version":"1.42.0"}')
if args[1]=='marketplace': sys.exit()
root=home/'plugins/cache/claude-notifications-go/claude-notifications-go/1.42.0'
(root/'.claude-plugin').mkdir(parents=True,exist_ok=True)
(root/'bin').mkdir(exist_ok=True)
(root/'.claude-plugin/plugin.json').write_text('{"version":"1.42.0"}')
registry=home/'plugins/installed_plugins.json'
registry.write_text(json.dumps({'plugins':{'claude-notifications-go@claude-notifications-go':[{'installPath':str(root),'version':'1.42.0'}]}}))
'''
(cli/'claude').write_bytes(claude_script.encode('utf-8')); (cli/'claude').chmod(0o755)
def events():
    return [json.loads(line) for line in trace.read_text().splitlines()] if trace.exists() else []
def path_ids(values):
    return [pathlib.Path(value).resolve() for value in values]
def native_shell_path(value):
    if os.name != 'nt':
        return value
    cygpath=shutil.which('cygpath')
    assert cygpath, 'native Windows fixture requires cygpath'
    return subprocess.check_output([cygpath,'-w',value],text=True).strip()
def reset_case():
    # Every directory is an explicit child of this fixture, never host state.
    for key in ['HOME','XDG_CONFIG_HOME','CODEX_HOME','CLAUDE_CONFIG_DIR']:
        d=pathlib.Path(env[key]); assert d.is_relative_to(sandbox)
        shutil.rmtree(d); d.mkdir()
    trace.write_text('')
def init_events(): return [e for e in events() if e[:2]==['config','init']]
for product in ['claude','codex','both']:
    reset_case(); request_paths.clear()
    run(['--product',product])
    assert sum(path.endswith('/'+asset_name) for path in request_paths)==1
    neutral=pathlib.Path(env['XDG_CONFIG_HOME'])/'agent-notifications/config.json'
    assert neutral.exists() and len(init_events())==1
    es=events(); init_index=next(i for i,e in enumerate(es) if e[:2]==['config','init'])
    assert any(e[:2]==['claude','plugin'] or e[:1]==['setup-codex'] for e in es[:init_index])
    # Idempotent repair preserves the exact document.
    neutral.write_bytes(b'{ "future": [1, 2], "secret": "canary" }\n')
    before=neutral.read_bytes(); trace.write_text('')
    run(['--product',product]); assert neutral.read_bytes()==before and len(init_events())==1

# All files changed by Claude registration are protected before the mocked
# CLI can mutate any one, whether the explicit target exists or is absent.
for target_name in ['installed_plugins.json','known_marketplaces.json','settings.json']:
    for exists in [False,True]:
        reset_case()
        claude_home=pathlib.Path(env['CLAUDE_CONFIG_DIR'])
        targets={
            'installed_plugins.json':claude_home/'plugins/installed_plugins.json',
            'known_marketplaces.json':claude_home/'plugins/known_marketplaces.json',
            'settings.json':claude_home/'settings.json',
        }
        target=targets[target_name]
        target.parent.mkdir(parents=True,exist_ok=True)
        original=b'{"plugins":{},"canary":true}'
        if exists: target.write_bytes(original)
        for name,path in targets.items():
            if name != target_name:
                path.parent.mkdir(parents=True,exist_ok=True)
                path.write_bytes(b'{"sibling":"preserve"}')
        before={name:(path.read_bytes() if path.exists() else None) for name,path in targets.items()}
        output=run(['--product','claude'],1,{'AGENT_NOTIFICATIONS_CONFIG':str(target)})
        assert not any(e[:1]==['claude'] for e in events())
        after={name:(path.read_bytes() if path.exists() else None) for name,path in targets.items()}
        assert after==before
        protected=json.loads(request.read_text())['protectedPaths']
        assert path_ids(protected)==path_ids([
            pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/installed_plugins.json',
            pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/known_marketplaces.json',
            pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'settings.json',
        ])
reset_case()
legacy=pathlib.Path(env['HOME'])/'.claude/claude-notifications-go/config.json'
legacy.parent.mkdir(parents=True); legacy.write_bytes(b'{ "future": {"x":1} }\n')
before=legacy.read_bytes(); run(['--product','both'])
assert legacy.read_bytes()==before
assert not (pathlib.Path(env['XDG_CONFIG_HOME'])/'agent-notifications/config.json').exists()
# Recorded active root wins over a higher unrelated glob. Baseline absent blocks.
def historical(value=b'{"personalized":true}', version='1.40.0'):
    home=pathlib.Path(env['CLAUDE_CONFIG_DIR'])
    active=home/'plugins/cache/claude-notifications-go/claude-notifications-go'/version
    (active/'config').mkdir(parents=True); (active/'config/config.json').write_bytes(value)
    (active/'bin').mkdir(); (active/'bin/runtime').write_bytes(b'working-runtime')
    other=active.parent/'9.99.99/config'; other.mkdir(parents=True); (other/'config.json').write_bytes(b'{}')
    (home/'plugins/installed_plugins.json').write_text(__import__('json').dumps({'plugins':{'claude-notifications-go@claude-notifications-go':[{'installPath':str(active),'version':version}]}}))
    return active
import json
for value in [b'{"personalized":true}',b'{}']:
    reset_case(); active=historical(value)
    output=run(['--product','claude'],1)
    assert 'import-required' in output and not any(e[:1]==['claude'] for e in events())
    assert (active/'bin/runtime').read_bytes()==b'working-runtime'
    assert path_ids(json.loads(request.read_text())['activeBundleRoots'])==path_ids([active])
# A verified exact-version template permits initialization. Personalized bytes
# against that same template still stop; current template is never substituted.
dest=web/'download/v1.40.0'; dest.mkdir()
(dest/'config.json').write_bytes(b'{}')
(dest/'checksums.txt').write_bytes((hashlib.sha256(b'{}').hexdigest()+'  config.json\n').encode('ascii'))
reset_case(); active=historical(b'{}'); run(['--product','claude'])
assert len(init_events())==1
reset_case(); active=historical(b'{"custom":1}')
run(['--product','claude'],1); assert not any(e[:1]==['claude'] for e in events())
# Alternate Claude home candidate and explicit target overlap.
reset_case()
custom=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'claude-notifications-go/config.json'
custom.parent.mkdir(); custom.write_text('{"custom":true}')
run(['--product','claude'],1); assert not any(e[:1]==['claude'] for e in events())
reset_case(); active=historical()
run(['--product','claude'],1,{'AGENT_NOTIFICATIONS_CONFIG':str(active/'config/config.json')})
assert not any(e[:1]==['claude'] for e in events())
# Codex ignores unrelated registry/baselines, but retains shared history guards.
for product in ['codex', 'claude', 'both']:
    reset_case()
    registry=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/installed_plugins.json'
    registry.parent.mkdir(); registry.write_text('{malformed')
    request_paths.clear()
    run(['--product',product],0 if product=='codex' else 1)
    if product=='codex':
        req=json.loads(request.read_text())
        assert path_ids(req['activeBundleRoots'])==[]
        assert path_ids(req['refreshDirs'])==path_ids([pathlib.Path(env['CODEX_HOME'])/'claude-notifications-go'])
        assert len(init_events())==1
        assert not any(e[:1]==['claude'] for e in events())
        assert not any('v1.40.0' in path for path in request_paths)
    else:
        assert not events()
reset_case()
custom=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'claude-notifications-go/config.json'
custom.parent.mkdir(); custom.write_text('{"custom":true}')
run(['--product','codex'],1)
assert not any(e[:1]==['setup-codex'] for e in events())
reset_case()
dest=pathlib.Path(env['CODEX_HOME'])/'claude-notifications-go'
(dest/'config').mkdir(parents=True); (dest/'config/config.json').write_text('{}')
run(['--product','codex'],1)
assert not any(e[:1]==['setup-codex'] for e in events())
run(['--product','codex'],1,{'AGENT_NOTIFICATIONS_CONFIG':str(dest/'explicit.json')})
assert not any(e[:1]==['setup-codex'] for e in events())
# Reserved exit 3 means committed registration; retain checksum-verified helper.
for product in ['codex','both']:
    reset_case()
    output=run(['--product',product],1,{'FAIL_SETUP_INIT':'1'})
    assert 'Partial setup' in output and 'registration failed' not in output
    assert (pathlib.Path(env['CODEX_HOME'])/'fixture-registration').read_text()=='registered'
    assert not init_events()
    line=next(line for line in output.splitlines() if line.startswith('Config-only retry'))
    shell_command=line.split(': ',1)[1]
    command=shlex.split(shell_command)
    assert pathlib.Path(native_shell_path(command[0])).read_bytes()==payload_file.read_bytes()
    assert command[1:]==['config','init','--json']
    trace.write_text(''); requests_before=list(request_paths)
    r=subprocess.run([bash,'-c',shell_command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
    assert r.returncode==0, r.stdout.decode()
    assert events()==[['config','init','--json']] and request_paths==requests_before
# Fresh registration failure never reaches init.
for product,fail in [('claude',{'FAIL_CLAUDE':'1'}),('codex',{'FAIL_REGISTER':'1'}),('both',{'FAIL_REGISTER':'1'})]:
    reset_case(); run(['--product',product],1,fail); assert not init_events()
# Init failure retains a verified executable for a config-only retry.
reset_case()
output=run(['--product','both'],1,{'FAIL_INIT':'1'})
assert 'Partial setup' in output and 'Config-only retry' in output
line=next(line for line in output.splitlines() if line.startswith('Config-only retry'))
shell_command=line.split(': ',1)[1]; command=shlex.split(shell_command); trace.write_text('')
assert pathlib.Path(native_shell_path(command[0])).read_bytes()==payload_file.read_bytes()
assert command[1:]==['config','init','--json']
r=subprocess.run([bash,'-c',shell_command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
assert r.returncode==0, r.stdout.decode()
assert events()==[['config','init','--json']]
# Offline staging failure cannot touch a working runtime/registration.
trace.write_text(''); active=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/cache/claude-notifications-go/claude-notifications-go/1.42.0'
before=(active/'bin/claude-notifications').read_bytes()
run(['--product','both'],1,{'BOOTSTRAP_RELEASES_BASE_URL':base+'/offline'})
assert (active/'bin/claude-notifications').read_bytes()==before and not events()
# A checksum-valid older helper without config commands fails before touching
# host registration. A hostile Python environment cannot disable verification.
valid_payload=payload_file.read_bytes()
bad_capability=valid_payload.replace(b"assert args[0]=='config'",b"raise SystemExit(2)")
payload_file.write_bytes(bad_capability)
checksums=payload_file.parent/'checksums.txt'; valid_checksums=checksums.read_bytes()
checksums.write_bytes((hashlib.sha256(bad_capability).hexdigest()+'  '+asset_name+'\n').encode('ascii'))
trace.write_text('')
run(['--product','both'],1)
assert not any(e[:1]==['claude'] or e[:1]==['setup-codex'] for e in events())
payload_file.write_bytes(valid_payload+b'\n#tampered')
trace.write_text('')
run(['--product','both'],1,{'PYTHONOPTIMIZE':'2'})
assert not events()
payload_file.write_bytes(valid_payload); checksums.write_bytes(valid_checksums)
print('protected flow fixtures passed (fake config CLI; real Go integration pending)')
server.shutdown(); server.server_close()
print('local HTTP / curl-pipe PTY adapter fixtures passed (fake installer and binary)')
PY
