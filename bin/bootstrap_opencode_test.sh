#!/bin/bash
# Real installer boundary: a native Go binary must persist an owned runtime and
# global plugin in a disposable profile, with independent webhook consent.
# The Windows private-root fixture reuses the already prepared offline Go cache.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) TEST_ENV_HANDOFF_GOMODCACHE=1 ;;
esac
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
[ "$#" -eq 1 ] || { echo "Usage: bash bin/bootstrap_opencode_test.sh /absolute/native/test-binary" >&2; exit 2; }
TEST_BINARY="$1"
[ -f "$TEST_BINARY" ] || exit 2
# CI qualifies release-mode bytes: unstripped Windows debug symbols can exceed
# the product's existing 32 MiB executable bound. Keep the actual size visible.
source_size=$(wc -c < "$TEST_BINARY" | tr -d '[:space:]')
printf 'OpenCode fixture native source: %s bytes\n' "$source_size"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SANDBOX=$(mktemp -d /tmp/bootstrap-opencode-XXXXXX)
trap 'rm -rf "$SANDBOX"' EXIT
# Give all Windows sandbox children the established private inherited DACL
# before creating HOME/config/plugin paths. chmod alone does not create it.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        (cd "$ROOT" && GOTMPDIR="$(cygpath -m "$TMPDIR")" go run scripts/opencode-private-root-windows.go "$(cygpath -m "$SANDBOX")") ;;
esac
test_env_setup "$SANDBOX"
export OPENCODE_CONFIG_DIR="$SANDBOX/opencode profile"
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        # OpenCode's strict placement contract requires filepath.Clean(path) ==
        # path. Native Windows environment paths therefore need backslashes.
        export USERPROFILE="$(cygpath -w "$USERPROFILE")" APPDATA="$(cygpath -w "$APPDATA")"
        export LOCALAPPDATA="$(cygpath -w "$LOCALAPPDATA")" OPENCODE_CONFIG_DIR="$(cygpath -w "$OPENCODE_CONFIG_DIR")"
        export XDG_CONFIG_HOME="$(cygpath -w "$XDG_CONFIG_HOME")"
        ;;
esac
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$SANDBOX/functions.sh"
source "$SANDBOX/functions.sh"
_CONFIG_HELPER="$TEST_BINARY"
PRODUCT=opencode
OPENCODE_ARGS=(--webhook)
install_opencode
control=$(bootstrap_control_root)
read -r os arch < <(bootstrap_release_os_arch)
name="claude-notifications-$os-$arch"
[ "$os" != windows ] || name="$name.exe"
installed="$control/runtime/$name"
[ -f "$installed" ]
[ -f "$OPENCODE_CONFIG_DIR/plugins/agent-notifications.js" ]
"$installed" config inspect --json > "$SANDBOX/inspect.json"
# A repeated public install must reuse the recorded runtime and preserve config.
cp "$control/ownership.json" "$SANDBOX/ownership-before.json"
config=$("$installed" config path)
case "$os" in windows) config=$(cygpath -u "$config") ;; esac
cp "$config" "$SANDBOX/config-before.json"
install_opencode
cmp "$config" "$SANDBOX/config-before.json"
python3 - "$control" "$OPENCODE_CONFIG_DIR" "$installed" <<'PY'
import json, pathlib, sys
control, profile, binary = map(pathlib.Path,sys.argv[1:])
ledger=json.loads((control/'ownership.json').read_text())
assert len(ledger['Consumers']) == 1, ledger['Consumers']
policy=json.loads((control/'agent-notifications.json').read_text())
assert policy['route']['openCodeNotifications'] == {'desktop':False,'webhook':True}
assert policy.get('enabled',False) is False
assert binary.read_bytes(), 'runtime disappeared with installer staging'
assert (profile/'plugins/agent-notifications.js').read_bytes(), 'global plugin missing'
PY
# Real selected bundle through the actual bootstrap and actual candidate binary.
# Missing Gemini route/capability, a command stub, or separate consent would leave
# this assertion red. Host PATH shims answer --version only in TEST profiles;
# neither native agent nor provider is launched.
python3 -I - "$ROOT" "$SANDBOX" "$TEST_BINARY" "$control" "$installed" <<'PYBUNDLE'
import hashlib, json, os, pathlib, shlex, shutil, subprocess, sys
root, lab, binary, control, installed = map(pathlib.Path, sys.argv[1:])
if os.name == 'nt':
    print('SKIP mixed candidate bootstrap fixture: Windows native path qualification is separate')
    sys.exit()
commands = lab/'bundle-fixture-bin'
commands.mkdir()
profile = lab/'Gemini TEST profile'
(profile/'.gemini').mkdir(parents=True)
settings = profile/'.gemini/settings.json'
settings.write_text('{ // foreign TEST comment\n "foreign":{"keep":true}\n}\n')
name = binary.name
# Match the release asset name selected by the genuine bootstrap, irrespective
# of the candidate build filename supplied by CI.
os_name = 'darwin' if sys.platform == 'darwin' else 'linux'
arch = subprocess.check_output(['uname','-m'],text=True).strip()
arch = 'amd64' if arch in ('x86_64','amd64') else 'arm64'
name = 'claude-notifications-'+os_name+'-'+arch
assets = lab/'bundle-fixture-assets'
assets.mkdir()
shutil.copyfile(binary,assets/name)
(assets/'checksums.txt').write_text(hashlib.sha256(binary.read_bytes()).hexdigest()+'  '+name+'\n')
version = subprocess.check_output([str(binary),'--version'],text=True).strip().split()[-1]
assert version.startswith('v'), version
(commands/'curl').write_text('#!/bin/bash\nset -eu\nout=""; url=""\nwhile [ "$#" -gt 0 ]; do\n case "$1" in -o) out=$2; shift 2 ;; -*) shift ;; *) url=$1; shift ;; esac\ndone\ncase "$url" in\n */checksums.txt) cp '+shlex.quote(str(assets/'checksums.txt'))+' "$out" ;;\n */'+name+') cp '+shlex.quote(str(assets/name))+' "$out" ;;\n */install.sh) cp '+shlex.quote(str(root/'bin/install.sh'))+' "$out" ;;\n *) echo "Unexpected fixture download: $url" >&2; exit 99 ;;\nesac\n')
(commands/'opencode').write_text('#!/bin/bash\n[ "$#" -eq 1 ] && [ "$1" = --version ] || exit 99\nprintf "1.18.33\\n"\n')
(commands/'gemini').write_text('#!/bin/bash\nset -eu\n[ "$#" -eq 1 ] && [ "$1" = --version ] || exit 99\ncase "$PWD" in */bootstrap-gemini-TEST-*/project) ;; *) echo "Gemini queried outside TEST project" >&2; exit 99 ;; esac\n[ "$HOME" = "$GEMINI_CLI_HOME" ] && [ -f "$GEMINI_CLI_SYSTEM_SETTINGS_PATH" ] || exit 99\nprintf "0.62.0\\n"\n')
for command in commands.iterdir(): command.chmod(0o755)
env = dict(os.environ, PATH=str(commands)+os.pathsep+os.environ['PATH'],
           GEMINI_CLI_HOME=str(profile), BOOTSTRAP_RELEASE_TAG=version,
           BOOTSTRAP_RELEASE_COMMIT='0123456789abcdef0123456789abcdef01234567',
           BOOTSTRAP_RELEASES_BASE_URL='https://candidate-fixture.invalid/releases',
           INSTALL_SCRIPT_URL='https://candidate-fixture.invalid/bin/install.sh')
project = lab/'bundle TEST project'
project.mkdir()
# Exercise no-args through a controlling TTY and the genuine public UI.
# Cancel/empty may acquire temporary verified bytes, but must leave persistent
# native settings, policy, registrations and binaries byte-for-byte unchanged.
import errno, pty, select, signal, time

def persistent_state():
    state = {}
    for directory in (control, profile):
        for file in directory.rglob('*'):
            if file.is_file(): state[str(file)] = hashlib.sha256(file.read_bytes()).hexdigest()
    return state

def run_tty(answer):
    before = persistent_state()
    pid, terminal = pty.fork()
    if pid == 0:
        os.chdir(project)
        os.execvpe('bash',['bash',str(root/'bin/bootstrap.sh')],env)
    transcript = b''
    sent = False
    deadline = time.monotonic()+30
    status = None
    try:
        while time.monotonic() < deadline:
            readable,_,_ = select.select([terminal],[],[],0.1)
            if readable:
                try: chunk = os.read(terminal,65536)
                except OSError as err:
                    if err.errno != errno.EIO: raise
                    chunk = b''
                transcript += chunk
                if b'comma-separated' in transcript and not sent:
                    os.write(terminal,answer)
                    sent = True
            finished, code = os.waitpid(pid,os.WNOHANG)
            if finished:
                status = os.waitstatus_to_exitcode(code)
                break
        assert status == 0 and sent, transcript.decode(errors='replace')
        assert persistent_state() == before, 'cancel/empty mutated persistent product roots'
        for label in (b'Claude Code',b'Codex',b'OpenCode',b'Gemini CLI'):
            assert label in transcript, transcript.decode(errors='replace')
    finally:
        if status is None:
            os.kill(pid,signal.SIGKILL)
            os.waitpid(pid,0)
        os.close(terminal)

run_tty(b'cancel\n')
run_tty(b'\n')
for attempt in range(2):
    result = subprocess.run(['bash',str(root/'bin/bootstrap.sh'),'--products','opencode,gemini','--webhook'],
                            cwd=project,env=env,text=True,capture_output=True,timeout=60)
    assert result.returncode == 0, result.stdout+'\n'+result.stderr
    ledger = json.loads((control/'ownership.json').read_text())
    assert set(ledger['Consumers']) == {'opencode-notifications','gemini-notifications'}, ledger['Consumers']
    policy = json.loads((control/'agent-notifications.json').read_text())
    for observer in ('openCodeNotifications','geminiNotifications'):
        assert policy['route'][observer] == {'desktop':False,'webhook':True}, policy
    assert str(settings) not in ledger['Files'], 'native settings became whole-file ownership'
    assert 'foreign TEST comment' in settings.read_text(), 'native foreign comment lost'
    assert len(set(c['Commands'][0] for c in ledger['Consumers'].values())) == 1, 'observer binary fork'
# Execute the actual command printed by the candidate bootstrap after changing
# the default config environment. The other profile must stay byte-identical.
removal = next(line.removeprefix('Remove: ') for line in result.stdout.splitlines()
               if line.startswith('Remove: ') and 'setup-gemini' in line)
assert shlex.split(removal) == [str(installed),'setup-gemini','remove','--control-root',str(control)], removal
other_config = lab/'different default TEST config'
other_control = other_config/'agent-notifications'
other_control.mkdir(parents=True)
(other_control/'agent-notifications.json').write_text('{"TEST":"untouched different default"}\n')
def other_state():
    return {str(path.relative_to(other_config)):path.read_bytes()
            for path in other_config.rglob('*') if path.is_file()}
other_before = other_state()
different_default = dict(env, XDG_CONFIG_HOME=str(other_config))
result = subprocess.run(['bash','-c',removal],cwd=project,env=different_default,text=True,capture_output=True,timeout=30)
assert result.returncode == 0, result.stdout+'\n'+result.stderr
assert other_state() == other_before, 'printed Gemini removal changed the other default root'
assert settings.exists() and 'foreign TEST comment' in settings.read_text()
assert installed.exists(), 'Gemini removal damaged OpenCode sibling runtime'
assert set(json.loads((control/'ownership.json').read_text())['Consumers']) == {'opencode-notifications'}
policy = json.loads((control/'agent-notifications.json').read_text())
assert policy['route']['geminiNotifications'] == {'desktop':False,'webhook':False}, policy
assert policy['route']['openCodeNotifications'] == {'desktop':False,'webhook':True}, policy
print('PASS actual candidate mixed OpenCode/Gemini bootstrap install/repeat/remove')
PYBUNDLE

# Native Windows qualification uses the standalone executable, never its .bat
# launcher. Keep the old environment off the printed remover's invocation.
if [ "$os" = windows ]; then
    gemini_profile="$SANDBOX/Gemini TEST profile"
    mkdir -p "$gemini_profile/.gemini"
    printf '{"foreign":{"TEST":"keep"}}\n' > "$gemini_profile/.gemini/settings.json"
    export GEMINI_CLI_HOME="$(cygpath -w "$gemini_profile")"
    install_gemini > "$SANDBOX/gemini-output"
    printed=$(sed -n 's/^Remove: //p' "$SANDBOX/gemini-output")
    other_default="$SANDBOX/different default TEST config"
    mkdir -p "$other_default"
    printf 'untouched default\n' > "$other_default/canary"
    APPDATA="$(cygpath -w "$other_default")" XDG_CONFIG_HOME="$(cygpath -w "$other_default")" bash -c "$printed"
    python3 - "$control" "$other_default" "$gemini_profile" "$installed" <<'PYWINDOWS'
import json, pathlib, sys
control, other, profile, binary = map(pathlib.Path,sys.argv[1:])
assert sorted(p.relative_to(other).as_posix() for p in other.rglob('*')) == ['canary']
assert (other/'canary').read_text() == 'untouched default\n'
ledger = json.loads((control/'ownership.json').read_text())
assert set(ledger['Consumers']) == {'opencode-notifications'}, ledger
policy = json.loads((control/'agent-notifications.json').read_text())
assert policy['route']['geminiNotifications'] == {'desktop':False,'webhook':False}, policy
assert policy['route']['openCodeNotifications'] == {'desktop':False,'webhook':True}, policy
assert json.loads((profile/'.gemini/settings.json').read_text())['foreign'] == {'TEST':'keep'}
assert binary.exists(), 'Gemini removal damaged OpenCode sibling runtime'
PYWINDOWS
fi

# Execute the same command printed by public setup. On Windows its temporary
# remover must preserve failure status and avoid deleting a foreign plugin.
remove_command=$(opencode_remove_command "$installed")
plugin="$OPENCODE_CONFIG_DIR/plugins/agent-notifications.js"
cp "$plugin" "$SANDBOX/owned-plugin.js"
printf 'foreign plugin edit\n' > "$plugin"
if bash -c "$remove_command"; then
    echo "removal accepted an edited plugin" >&2
    exit 1
fi
[ -f "$installed" ]
printf 'foreign plugin edit\n' > "$SANDBOX/foreign-plugin.js"
cmp "$plugin" "$SANDBOX/foreign-plugin.js"
cp "$SANDBOX/owned-plugin.js" "$plugin"
bash -c "$remove_command"
python3 - "$control" <<'PYREMOVE'
import json, pathlib, sys
root=pathlib.Path(sys.argv[1])
ledger=json.loads((root/'ownership.json').read_text())
assert not ledger['Consumers'], ledger['Consumers']
policy=json.loads((root/'agent-notifications.json').read_text())
assert policy['route']['openCodeNotifications'] == {'desktop':False,'webhook':False}
PYREMOVE
[ ! -e "$installed" ]
[ ! -e "$OPENCODE_CONFIG_DIR/plugins/agent-notifications.js" ]
echo "OpenCode bootstrap real native lifecycle passed in disposable profile"
