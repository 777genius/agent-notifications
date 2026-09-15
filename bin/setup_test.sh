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
python3 -I -c 'import json,os,sys; print(json.dumps({"args":sys.argv[1:],"tag":os.environ["BOOTSTRAP_RELEASE_TAG"],"sha":os.environ["BOOTSTRAP_RELEASE_COMMIT"],"install":os.environ["INSTALL_SCRIPT_URL"]}))' "$@" > "$CASE_DIR/ran.json"
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
print('All setup loader fixtures passed (no public network or real agent CLIs).')
PY
