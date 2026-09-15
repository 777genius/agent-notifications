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
loader = (root / 'bin/setup.sh').read_text()
public_command = next(line for line in (root / 'README.md').read_text().splitlines()
                      if line.startswith('(set -o pipefail; curl '))
sha = '0123456789abcdef0123456789abcdef01234567'
raw = 'https://raw.githubusercontent.com/777genius/agent-notifications/' + sha + '/bin'
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
    https://raw.githubusercontent.com/777genius/agent-notifications/a8fbdc74ab418e6221fae2794d1dc9c3d8fc631d/bin/setup.sh) kind=setup ;;
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
                   CASE_DIR=str(case), TMPDIR=str(case / 'tmp space'),
                   FAIL_DOWNLOAD=fail, BOOTSTRAP_STATUS=str(status),
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        args = ['--product', 'both', 'argument with spaces', '*']
        command = ['bash', '-s', '--'] if piped else ['bash', str(root / 'bin/setup.sh')]
        command += args
        if documented:
            command = ['bash', '-c', public_command.replace(
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
                assert requests.pop(0) == 'https://raw.githubusercontent.com/777genius/agent-notifications/a8fbdc74ab418e6221fae2794d1dc9c3d8fc631d/bin/setup.sh'
            assert requests == [
                'https://api.github.com/repos/777genius/agent-notifications/releases/latest',
                'https://api.github.com/repos/777genius/agent-notifications/commits/v1.43.0',
                raw + '/bootstrap.sh',
            ], name
        assert not list((case / 'tmp space').iterdir()), name + ': leaked staging directory'
        print('PASS ' + name)

def place_runtime_cmd(dest, src):
    if dest.exists() or not src or not os.path.isfile(src):
        return
    dest.write_text('#!/bin/sh\nexec {} "$@"\n'.format(shlex.quote(src.replace('\\', '/'))))
    dest.chmod(0o755)


def runtime_path(case, python=False, node=False):
    bin_dir = case / 'runtime-bin'
    bin_dir.mkdir()
    names = ['bash', 'sh', 'mktemp', 'rm', 'cat', 'chmod', 'mkdir', 'ln', 'uname',
             'tr', 'head', 'cp', 'mv', 'env', 'true', 'false']
    if python:
        names.append('python3')
    if node:
        names.append('node')
    for name in names:
        place_runtime_cmd(bin_dir / name, shutil.which(name))
    return str(case / 'bin') + os.pathsep + str(bin_dir)

def run_runtime_case(name, python=False, node=False, expected=0):
    with tempfile.TemporaryDirectory(prefix='setup-runtime-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        (case / 'latest').write_text(json.dumps({'tag_name': 'v1.43.0'}))
        (case / 'commit').write_text(json.dumps({'sha': sha}))
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=runtime_path(case, python=python, node=node),
                   CASE_DIR=str(case), TMPDIR=str(case / 'tmp space'),
                   FAIL_DOWNLOAD='', BOOTSTRAP_STATUS='0',
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        result = subprocess.run(['bash', str(root / 'bin/setup.sh'), '--product', 'codex'],
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
if shutil.which('python3'):
    run_runtime_case('python-only loader e2e', python=True, node=False)
else:
    print('SKIP python-only loader e2e: python3 not available')
if shutil.which('node'):
    run_runtime_case('node-only loader e2e', python=False, node=True)
else:
    print('SKIP node-only loader e2e: node not available')
run_runtime_case('neither runtime loader e2e', python=False, node=False, expected=1)
if shutil.which('python3') and shutil.which('node'):
    with tempfile.TemporaryDirectory(prefix='setup-pref-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        python_src = shutil.which('python3')
        node_src = shutil.which('node')
        (case / 'bin/python3').write_text(
            '#!/usr/bin/env bash\nprintf python3 >> "$CASE_DIR/runtime.log"\nexec ' + shlex.quote(python_src) + ' "$@"\n')
        (case / 'bin/python3').chmod(0o755)
        (case / 'bin/node').write_text(
            '#!/usr/bin/env bash\nprintf node >> "$CASE_DIR/runtime.log"\nexec ' + shlex.quote(node_src) + ' "$@"\n')
        (case / 'bin/node').chmod(0o755)
        (case / 'latest').write_text(json.dumps({'tag_name': 'v1.43.0'}))
        (case / 'commit').write_text(json.dumps({'sha': sha}))
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=str(case / 'bin') + os.pathsep + runtime_path(case, python=True, node=True),
                   CASE_DIR=str(case), TMPDIR=str(case / 'tmp space'),
                   FAIL_DOWNLOAD='', BOOTSTRAP_STATUS='0',
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        result = subprocess.run(['bash', str(root / 'bin/setup.sh'), '--product', 'codex'],
                                text=True, capture_output=True, env=env, timeout=20)
        assert result.returncode == 0, ('python preferred', result.stderr)
        log = (case / 'runtime.log').read_text()
        assert log.startswith('python3'), log
        assert 'node' not in log, log
        print('PASS python preferred over node')
print('All setup loader fixtures passed (no public network or real agent CLIs).')
PY
