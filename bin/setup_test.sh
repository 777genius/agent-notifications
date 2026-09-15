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
PINNED_SETUP_SHA = 'a8fbdc74ab418e6221fae2794d1dc9c3d8fc631d'
PINNED_SETUP_URL = (
    'https://raw.githubusercontent.com/777genius/agent-notifications/'
    + PINNED_SETUP_SHA + '/bin/setup.sh'
)
STORE_PYTHON3_STUB = '''#!/usr/bin/env bash
echo "Python was not found; run without arguments to install from the Microsoft Store, or disable this shortcut from Settings > Apps > Advanced app settings > App execution aliases." >&2
exit 9009
'''
sha = '0123456789abcdef0123456789abcdef01234567'
raw = 'https://raw.githubusercontent.com/777genius/agent-notifications/' + sha + '/bin'
assert 'usable_python3()' in loader and "python3 -I -c 'import json'" in loader
assert '</dev/null' in loader
curl_stub = '''#!/usr/bin/env bash
set -eu
output=""; url=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output="$2"; shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
printf '%s\\n' "$url" >> "$CASE_DIR/requests"
case "$url" in
    ''' + PINNED_SETUP_URL + ''') kind=setup ;;
    https://api.github.com/repos/777genius/agent-notifications/releases/latest) kind=latest ;;
    https://api.github.com/repos/777genius/agent-notifications/commits/v1.43.0) kind=commit ;;
    https://raw.githubusercontent.com/777genius/agent-notifications/*/bin/bootstrap.sh) kind=bootstrap ;;
    *) echo "Unexpected URL: $url" >&2; exit 99 ;;
esac
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
bootstrap_stub = '''#!/usr/bin/env bash
''' + shlex.quote(sys.executable) + ''' -I -c 'import json,os,sys; print(json.dumps({"args":sys.argv[1:],"tag":os.environ["BOOTSTRAP_RELEASE_TAG"],"sha":os.environ["BOOTSTRAP_RELEASE_COMMIT"],"install":os.environ["INSTALL_SCRIPT_URL"]}))' "$@" > "$CASE_DIR/ran.json"
exit "${BOOTSTRAP_STATUS:-0}"
'''

def run_case(name, tag=None, commit=None, fail='', status=0, expected=None, piped=False, documented=False):
    with tempfile.TemporaryDirectory(prefix='setup-test-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        (case / 'latest').write_text(json.dumps({'tag_name': 'v1.43.0'}) if tag is None else tag)
        (case / 'commit').write_text(json.dumps({'sha': sha}) if commit is None else commit)
        (case / 'bootstrap').write_text(bootstrap_stub)
        (case / 'setup').write_text(loader)
        env = dict(os.environ, PATH=str(case / 'bin') + os.pathsep + os.environ['PATH'],
                   CASE_DIR=bash_path(case), TMPDIR=bash_path(case / 'tmp space'),
                   FAIL_DOWNLOAD=fail, BOOTSTRAP_STATUS=str(status),
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        args = ['--product', 'both', 'argument with spaces', '*']
        command = [HOST_BASH, '-s', '--'] if piped else [HOST_BASH, str(root / 'bin/setup.sh')]
        command += args
        if documented:
            command = [HOST_BASH, '-c', public_command.replace(
                '| bash)', '| bash -s -- ' + ' '.join(map(shlex.quote, args)) + ')')]
        result = subprocess.run(command, input=loader if piped else None,
                                text=True, capture_output=True, env=env, timeout=20)
        if expected is None:
            assert result.returncode != 0, (name, result.stdout, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran on failure'
        else:
            assert result.returncode == expected, (name, result.returncode, result.stderr)
            if (case / 'ran.json').exists():
                assert json.loads((case / 'ran.json').read_text()) == {
                    'args': args, 'tag': 'v1.43.0', 'sha': sha, 'install': raw + '/install.sh'
                }, name
            else:
                raise AssertionError(name + ': installer did not run')
            requests = (case / 'requests').read_text().splitlines()
            if documented:
                assert requests.pop(0) == PINNED_SETUP_URL
            assert requests == [
                'https://api.github.com/repos/777genius/agent-notifications/releases/latest',
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
             'tr', 'head', 'cp', 'mv', 'env', 'true', 'false', 'dirname', 'basename',
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
        (case / 'latest').write_text(json.dumps({'tag_name': 'v1.43.0'}))
        (case / 'commit').write_text(json.dumps({'sha': sha}))
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
            assert json.loads((case / 'ran.json').read_text())['tag'] == 'v1.43.0', name
        else:
            assert result.returncode != 0, (name, result.stdout, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran on failure'
            assert 'python3 or node is required' in result.stderr, (name, result.stderr)
        assert not list((case / 'tmp space').iterdir()), name + ': leaked staging directory'
        print('PASS ' + name)

run_case('pinned release and exact argv', expected=0)
run_case('piped one-line entry point', expected=0, piped=True)
run_case('documented one-line command', expected=0, documented=True)
run_case('initial loader download failure', fail='setup', documented=True)
run_case('bootstrap exit status', status=17, expected=17)
for tag in ['v1.43.0-rc1', 'main', 'v01.43.0', 'v1.43.0\r', '../main', 42, None]:
    run_case('reject tag ' + repr(tag), tag=json.dumps({'tag_name': tag}))
for value in ['', 'A' * 40, 'a' * 39, sha + '\r', '../main', 42, None]:
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
run_runtime_case('neither runtime loader e2e', python=False, node=False, expected=1)
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
        (case / 'latest').write_text(json.dumps({'tag_name': 'v1.43.0'}))
        (case / 'commit').write_text(json.dumps({'sha': sha}))
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=bash_path(case / 'bin') + ':' + runtime_path(case, python=True, node=True),
                   CASE_DIR=bash_path(case), TMPDIR=bash_path(case / 'tmp space'),
                   FAIL_DOWNLOAD='', BOOTSTRAP_STATUS='0',
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        result = subprocess.run([HOST_BASH, str(root / 'bin/setup.sh'), '--product', 'codex'],
                                text=True, capture_output=True, env=env, timeout=20)
        assert result.returncode == 0, ('python preferred', result.stderr)
        log = (case / 'runtime.log').read_text()
        assert log.startswith('python3'), log
        assert 'node' not in log, log
        print('PASS python preferred over node')
if host_cmd('node'):
    run_runtime_case('stub python3 falls back to node', python=False, node=True, stub_python=True)
else:
    print('SKIP stub python3 falls back to node: node not available')
run_runtime_case('stub python3 without node', python=False, node=False, stub_python=True, expected=1)
print('All setup loader fixtures passed (no public network or real agent CLIs).')
PY
