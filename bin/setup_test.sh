#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
python3 -I - "$ROOT" <<'PY'
import json
import os
import shlex
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

root = Path(sys.argv[1])
loader = (root / 'bin/setup.sh').read_text(encoding='utf-8')
public_command = next(line for line in (root / 'README.md').read_text(encoding='utf-8').splitlines()
                      if line.startswith('(set -o pipefail; curl '))
PINNED_SETUP_SHA = 'a512deb5819c3f8c7c3be8335f713cc8bb734fc3'
PINNED_SETUP_URL = (
    'https://raw.githubusercontent.com/777genius/agent-notifications/'
    + PINNED_SETUP_SHA + '/bin/setup.sh'
)
assert PINNED_SETUP_SHA in public_command
for rel in ('docs/INSTALLATION.md', 'landing/data/install.ts',
            'landing/tests/install.test.ts', 'landing/tests/browser/install.spec.ts'):
    assert PINNED_SETUP_SHA in (root / rel).read_text(encoding='utf-8'), rel
STORE_PYTHON3_STUB = '''#!/usr/bin/env bash
echo "Python was not found; run without arguments to install from the Microsoft Store, or disable this shortcut from Settings > Apps > Advanced app settings > App execution aliases." >&2
exit 9009
'''
sha = '0123456789abcdef0123456789abcdef01234567'
raw = 'https://raw.githubusercontent.com/777genius/agent-notifications/' + sha + '/bin'
assert 'python3' not in loader and 'node' not in loader
assert 'application/vnd.github.sha' in loader and 'url_effective' in loader
curl_stub = '''#!/usr/bin/env bash
set -eu
output=""; url=""; format=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output="$2"; shift 2 ;;
        -w) format="$2"; shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
printf '%s\\n' "$url" >> "$CASE_DIR/requests"
case "$url" in
    ''' + PINNED_SETUP_URL + ''') kind=setup ;;
    https://github.com/777genius/agent-notifications/releases/latest) kind=latest ;;
    https://api.github.com/repos/777genius/agent-notifications/commits/v1.43.0) kind=commit ;;
    https://raw.githubusercontent.com/777genius/agent-notifications/*/bin/bootstrap.sh) kind=bootstrap ;;
    *) echo "Unexpected URL: $url" >&2; exit 99 ;;
esac
if [ "$kind" = latest ] && [ -n "$format" ]; then
    cat "$CASE_DIR/latest_url"
    if [ "${FAIL_DOWNLOAD:-}" = latest ]; then exit 22; fi
    exit 0
fi
# Simulate an initial fetch failing before it produces script bytes.
if [ "$kind" = setup ] && [ "${FAIL_DOWNLOAD:-}" = setup ]; then exit 22; fi
if [ -n "$output" ]; then
    cat "$CASE_DIR/$kind" > "$output"
else
    cat "$CASE_DIR/$kind"
fi
# Return a failure AFTER emitting valid bytes, to catch accidental execution.
if [ "${FAIL_DOWNLOAD:-}" = "$kind" ]; then exit 22; fi
'''
# Record argv in bash. Native Windows python.exe CRT-globs "*" when Git Bash
# execs it, so sys.argv cannot prove setup.sh forwarded the literal argument.
bootstrap_stub = '''#!/usr/bin/env bash
set -eu
: > "$CASE_DIR/argv0"
for a in "$@"; do
    printf '%s\\0' "$a" >> "$CASE_DIR/argv0"
done
''' + shlex.quote(sys.executable.replace('\\', '/')) + ''' -I -c 'import json,os; print(json.dumps({"tag":os.environ["BOOTSTRAP_RELEASE_TAG"],"sha":os.environ["BOOTSTRAP_RELEASE_COMMIT"],"install":os.environ["INSTALL_SCRIPT_URL"]}))' > "$CASE_DIR/ran.json"
exit "${BOOTSTRAP_STATUS:-0}"
'''


def recorded_install(case):
    ran = json.loads((case / 'ran.json').read_text(encoding='utf-8'))
    ran['args'] = [a.decode('utf-8') for a in (case / 'argv0').read_bytes().split(b'\0') if a]
    return ran


def with_noglob(env):
    # Git Bash bash.exe is Win32: its CRT globs a bare "*" argv before the
    # script runs. noglob plus a quoted -c command line keep the literal.
    msys = env.get('MSYS', '')
    if 'noglob' not in msys.split():
        env = dict(env, MSYS=(msys + ' noglob').strip())
    return env


def run_loader(args, env, piped=False, documented=False):
    env = with_noglob(env)
    setup_sh = bash_path(root / 'bin/setup.sh')
    if documented:
        command = [HOST_BASH, '-c', public_command.replace(
            '| bash)', '| bash -s -- ' + ' '.join(map(shlex.quote, args)) + ')')]
        stdin = None
    elif piped:
        command = [HOST_BASH, '-c', 'exec ' + ' '.join(
            shlex.quote(x) for x in [bash_path(HOST_BASH), '-s', '--'] + args)]
        stdin = loader
    else:
        command = [HOST_BASH, '-c', 'exec ' + ' '.join(shlex.quote(x) for x in [setup_sh] + args)]
        stdin = None
    return subprocess.run(command, input=stdin, text=True, capture_output=True, env=env, timeout=20)


def run_case(name, tag=None, commit=None, fail='', status=0, expected=None, piped=False, documented=False):
    with tempfile.TemporaryDirectory(prefix='setup-test-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        if tag is None:
            latest_value = 'v1.43.0'
        else:
            try:
                latest_value = json.loads(tag).get('tag_name')
            except Exception:
                latest_value = tag
        (case / 'latest_url').write_text(
            'https://github.com/777genius/agent-notifications/releases/tag/' + str(latest_value))
        if commit is None:
            commit_value = sha
        else:
            try:
                commit_value = json.loads(commit).get('sha')
            except Exception:
                commit_value = commit
        (case / 'commit').write_text('' if commit_value is None else str(commit_value))
        (case / 'bootstrap').write_text(bootstrap_stub)
        (case / 'setup').write_text(loader)
        env = dict(os.environ, PATH=runtime_path(case, python=True, node=True),
                   CASE_DIR=bash_path(case), TMPDIR=bash_path(case / 'tmp space'),
                   FAIL_DOWNLOAD=fail, BOOTSTRAP_STATUS=str(status),
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        args = ['--product', 'both', 'argument with spaces', '*']
        result = run_loader(args, env, piped=piped, documented=documented)
        if expected is None:
            assert result.returncode != 0, (name, result.stdout, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran on failure'
        else:
            assert result.returncode == expected, (name, result.returncode, result.stderr)
            if (case / 'ran.json').exists():
                got = recorded_install(case)
                assert got == {
                    'args': args, 'tag': 'v1.43.0', 'sha': sha, 'install': raw + '/install.sh'
                }, (name, got)
            else:
                raise AssertionError(name + ': installer did not run')
            requests = (case / 'requests').read_text(encoding='utf-8').splitlines()
            if documented:
                assert requests.pop(0) == PINNED_SETUP_URL
            assert requests == [
                'https://github.com/777genius/agent-notifications/releases/latest',
                'https://api.github.com/repos/777genius/agent-notifications/commits/v1.43.0',
                raw + '/bootstrap.sh',
            ], name
        assert not list((case / 'tmp space').iterdir()), name + ': leaked staging directory'
        print('PASS ' + name)

def is_windows_store_or_wsl_alias(src):
    n = src.replace('\\', '/').lower()
    base = os.path.basename(n)
    if base not in ('python', 'python.exe', 'python3', 'python3.exe',
                    'node', 'node.exe', 'bash', 'bash.exe', 'sh', 'sh.exe',
                    'wsl', 'wsl.exe'):
        return False
    return '/windowsapps/' in n or '/system32/' in n or '/syswow64/' in n


def iter_path_dirs(name):
    seen = []
    for directory in os.environ.get('PATH', '').split(os.pathsep):
        if directory and directory not in seen:
            seen.append(directory)
            yield directory
    if os.name == 'nt' and name in ('bash', 'bash.exe', 'sh', 'sh.exe'):
        for key in ('ProgramFiles', 'ProgramFiles(x86)', 'LOCALAPPDATA'):
            root = os.environ.get(key)
            if not root:
                continue
            for rel in (os.path.join('Git', 'bin'), os.path.join('Programs', 'Git', 'bin')):
                directory = os.path.join(root, rel)
                if directory not in seen and os.path.isdir(directory):
                    seen.append(directory)
                    yield directory


def which_skip_aliases(name):
    names = [name]
    if os.name == 'nt':
        suffixes = [s for s in os.environ.get('PATHEXT', '.COM;.EXE;.BAT;.CMD').split(os.pathsep) if s]
        lower = name.lower()
        if not any(lower.endswith(s.lower()) for s in suffixes):
            names = [name] + [name + s for s in suffixes]
    for directory in iter_path_dirs(name):
        for candidate_name in names:
            candidate = os.path.join(directory, candidate_name)
            if os.path.isfile(candidate) and not is_windows_store_or_wsl_alias(candidate):
                return candidate
    return None


def host_cmd(name):
    # Fixture cleanup needs the system utility, not host PATH wrappers with
    # extra dependencies or process/container scans. Keep runtime lookup intact.
    if name == 'rm' and os.name != 'nt':
        return shutil.which(name, path=os.defpath)
    if name == 'python3' and sys.executable and os.path.isfile(sys.executable) \
            and not is_windows_store_or_wsl_alias(sys.executable):
        return sys.executable
    return which_skip_aliases(name)


def bash_path(p):
    s = str(p).replace('\\', '/')
    if len(s) >= 2 and s[1] == ':':
        s = '/' + s[0].lower() + s[2:]
    return s


HOST_BASH = host_cmd('bash')
assert HOST_BASH, 'Git Bash / bash executable not found'


def place_runtime_cmd(dest, src):
    if dest.exists() or not src or not os.path.isfile(src) or is_windows_store_or_wsl_alias(src):
        return
    dest.write_text('#!/bin/sh\nexec {} "$@"\n'.format(shlex.quote(src.replace('\\', '/'))))
    dest.chmod(0o755)


def runtime_path(case, python=False, node=False):
    bin_dir = case / 'runtime-bin'
    bin_dir.mkdir()
    names = ['bash', 'sh', 'mktemp', 'rm', 'cat', 'chmod', 'mkdir', 'ln', 'uname',
             'tr', 'wc', 'grep', 'head', 'cp', 'mv', 'env', 'true', 'false', 'dirname', 'basename',
             'printf', 'pwd', 'cygpath']
    if python:
        names.append('python3')
    if node:
        names.append('node')
    for name in names:
        place_runtime_cmd(bin_dir / name, host_cmd(name))
    return bash_path(case / 'bin') + ':' + bash_path(bin_dir)

def run_runtime_case(name, python=False, node=False, expected=0, stub_python=False):
    with tempfile.TemporaryDirectory(prefix='setup-runtime-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        if stub_python:
            (case / 'bin/python3').write_text(STORE_PYTHON3_STUB)
            (case / 'bin/python3').chmod(0o755)
        (case / 'latest_url').write_text(
            'https://github.com/777genius/agent-notifications/releases/tag/v1.43.0')
        (case / 'commit').write_text(sha)
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=runtime_path(case, python=python, node=node),
                   CASE_DIR=bash_path(case), TMPDIR=bash_path(case / 'tmp space'),
                   FAIL_DOWNLOAD='', BOOTSTRAP_STATUS='0',
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        result = subprocess.run([HOST_BASH, str(root / 'bin/setup.sh'), '--product', 'codex'],
                                text=True, capture_output=True, env=env, timeout=20)
        if expected == 0:
            assert result.returncode == 0, (name, result.returncode, result.stderr)
            assert (case / 'ran.json').exists(), name + ': installer did not run'
            assert json.loads((case / 'ran.json').read_text(encoding='utf-8'))['tag'] == 'v1.43.0', name
        else:
            assert result.returncode != 0, (name, result.stdout, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran on failure'
        assert not list((case / 'tmp space').iterdir()), name + ': leaked staging directory'
        print('PASS ' + name)

run_case('pinned release and exact argv', expected=0)
run_case('piped one-line entry point', expected=0, piped=True)
run_case('documented one-line command', expected=0, documented=True)
run_case('initial loader download failure', fail='setup', documented=True)
run_case('bootstrap exit status', status=17, expected=17)
for tag in ['v1.43.0-rc1', 'main', 'v01.43.0', 'v1.43.0\r', '../main', 42, None]:
    run_case('reject tag ' + repr(tag), tag=json.dumps({'tag_name': tag}))
for value in ['', 'A' * 40, 'a' * 39, sha + '\r', sha + '\n', '../main', 42, None]:
    run_case('reject commit ' + repr(value), commit=json.dumps({'sha': value}))
run_case('malformed release JSON', tag='{broken')
run_case('non-object release JSON', tag='[]')
run_case('malformed commit JSON', commit='{broken')
for step in ['latest', 'commit', 'bootstrap']:
    run_case('failed download with valid bytes: ' + step, fail=step)
if host_cmd('python3'):
    run_runtime_case('python-only loader e2e', python=True, node=False)
else:
    print('SKIP python-only loader e2e: python3 not available')
if host_cmd('node'):
    run_runtime_case('node-only loader e2e', python=False, node=True)
else:
    print('SKIP node-only loader e2e: node not available')
run_runtime_case('neither runtime loader e2e', python=False, node=False, expected=0)
if host_cmd('python3') and host_cmd('node'):
    with tempfile.TemporaryDirectory(prefix='setup-pref-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        python_src = host_cmd('python3').replace('\\', '/')
        node_src = host_cmd('node').replace('\\', '/')
        (case / 'bin/python3').write_text(
            '#!/usr/bin/env bash\nprintf python3 >> "$CASE_DIR/runtime.log"\nexec ' + shlex.quote(python_src) + ' "$@"\n')
        (case / 'bin/python3').chmod(0o755)
        (case / 'bin/node').write_text(
            '#!/usr/bin/env bash\nprintf node >> "$CASE_DIR/runtime.log"\nexec ' + shlex.quote(node_src) + ' "$@"\n')
        (case / 'bin/node').chmod(0o755)
        (case / 'latest_url').write_text(
            'https://github.com/777genius/agent-notifications/releases/tag/v1.43.0')
        (case / 'commit').write_text(sha)
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=bash_path(case / 'bin') + ':' + runtime_path(case, python=True, node=True),
                   CASE_DIR=bash_path(case), TMPDIR=bash_path(case / 'tmp space'),
                   FAIL_DOWNLOAD='', BOOTSTRAP_STATUS='0',
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        result = subprocess.run([HOST_BASH, str(root / 'bin/setup.sh'), '--product', 'codex'],
                                text=True, capture_output=True, env=env, timeout=20)
        assert result.returncode == 0, ('python preferred', result.stderr)
        assert json.loads((case / 'ran.json').read_text(encoding='utf-8'))['sha'] == sha
        assert not (case / 'runtime.log').exists(), 'loader invoked an optional runtime'
        print('PASS Python/Node presence does not change loader behavior')
if host_cmd('node'):
    run_runtime_case('stub python3 falls back to node', python=False, node=True, stub_python=True)
else:
    print('SKIP stub python3 falls back to node: node not available')
run_runtime_case('stub python3 without node', python=False, node=False, stub_python=True, expected=0)
print('All setup loader fixtures passed (no public network or real agent CLIs).')
PY
