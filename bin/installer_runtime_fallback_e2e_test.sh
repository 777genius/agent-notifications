#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# Isolated python3-or-node installer runtime e2e. No public network, no host agents.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
python3 -I - "$ROOT" <<'PY'
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile

root = Path(sys.argv[1])


def runtime_path(case, python=False, node=False):
    bin_dir = case / 'runtime-bin'
    bin_dir.mkdir()
    names = ['bash', 'sh', 'mktemp', 'rm', 'cat', 'chmod', 'mkdir', 'ln', 'uname',
             'tr', 'head', 'cp', 'mv', 'env', 'true', 'false', 'grep', 'sed', 'awk']
    if python:
        names.append('python3')
    if node:
        names.append('node')
    for name in names:
        src = shutil.which(name)
        if src:
            dest = bin_dir / name
            if not dest.exists():
                dest.symlink_to(src)
    return str(bin_dir)


def pass_name(name):
    print('PASS ' + name, flush=True)


def fail(name, detail):
    raise AssertionError(name + ': ' + detail)


# --- setup.sh loader: python-only, node-only, neither, python-preferred ---
sha = '0123456789abcdef0123456789abcdef01234567'
loader = (root / 'bin/setup.sh').read_text()
curl_stub = r'''#!/usr/bin/env bash
set -eu
output=""; url=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output="$2"; shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
printf '%s\n' "$url" >> "$CASE_DIR/requests"
case "$url" in
    https://api.github.com/repos/777genius/agent-notifications/releases/latest) kind=latest ;;
    https://api.github.com/repos/777genius/agent-notifications/commits/v1.43.0) kind=commit ;;
    https://raw.githubusercontent.com/777genius/agent-notifications/*/bin/bootstrap.sh) kind=bootstrap ;;
    *) echo "Unexpected URL: $url" >&2; exit 99 ;;
esac
if [ -n "$output" ]; then cat "$CASE_DIR/$kind" > "$output"; else cat "$CASE_DIR/$kind"; fi
'''
bootstrap_stub = '''#!/usr/bin/env bash
''' + shlex.quote(sys.executable) + ''' -I -c 'import json,os,sys; print(json.dumps({"args":sys.argv[1:],"tag":os.environ["BOOTSTRAP_RELEASE_TAG"],"sha":os.environ["BOOTSTRAP_RELEASE_COMMIT"],"install":os.environ["INSTALL_SCRIPT_URL"]}))' "$@" > "$CASE_DIR/ran.json"
exit 0
'''


def setup_case(name, python=False, node=False, expected=0, preferred=False):
    with tempfile.TemporaryDirectory(prefix='runtime-e2e-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        (case / 'latest').write_text(json.dumps({'tag_name': 'v1.43.0'}))
        (case / 'commit').write_text(json.dumps({'sha': sha}))
        (case / 'bootstrap').write_text(bootstrap_stub)
        path = str(case / 'bin') + os.pathsep + runtime_path(case, python=python, node=node)
        if preferred:
            (case / 'bin/python3').write_text(
                '#!/usr/bin/env bash\nprintf python3 >> "$CASE_DIR/runtime.log"\nexec '
                + shlex.quote(shutil.which('python3')) + ' "$@"\n')
            (case / 'bin/python3').chmod(0o755)
            (case / 'bin/node').write_text(
                '#!/usr/bin/env bash\nprintf node >> "$CASE_DIR/runtime.log"\nexec '
                + shlex.quote(shutil.which('node')) + ' "$@"\n')
            (case / 'bin/node').chmod(0o755)
        env = dict(os.environ, PATH=path, CASE_DIR=str(case), TMPDIR=str(case / 'tmp space'))
        result = subprocess.run(['bash', str(root / 'bin/setup.sh'), '--product', 'codex'],
                                text=True, capture_output=True, env=env, timeout=20)
        if expected == 0:
            if result.returncode != 0:
                fail(name, result.stderr)
            ran = json.loads((case / 'ran.json').read_text())
            if ran['tag'] != 'v1.43.0' or ran['sha'] != sha:
                fail(name, repr(ran))
            if preferred:
                log = (case / 'runtime.log').read_text()
                if not log.startswith('python3') or 'node' in log:
                    fail(name, log)
        else:
            if result.returncode == 0 or (case / 'ran.json').exists():
                fail(name, 'installer ran without a JSON runtime')
            if 'python3 or node is required' not in result.stderr:
                fail(name, result.stderr)
        if list((case / 'tmp space').iterdir()):
            fail(name, 'leaked staging directory')
        pass_name(name)


if shutil.which('python3'):
    setup_case('setup.sh python-only', python=True)
else:
    print('SKIP setup.sh python-only')
if shutil.which('node'):
    setup_case('setup.sh node-only', node=True)
else:
    print('SKIP setup.sh node-only')
setup_case('setup.sh neither runtime', expected=1)
if shutil.which('python3') and shutil.which('node'):
    setup_case('setup.sh python preferred', python=True, node=True, preferred=True)

# --- bootstrap.sh: node-only commit parse and checksum verify ---
if shutil.which('node'):
    with tempfile.TemporaryDirectory(prefix='bootstrap-node-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        path = runtime_path(case, node=True)
        functions = case / 'functions.sh'
        functions.write_text((root / 'bin/bootstrap.sh').read_text().replace('main "$@"', ''))
        commit = 'a' * 40
        script = r'''
source "$FUNCTIONS"
PATH="$RUNTIME_PATH"
command -v python3 >/dev/null && { echo python3 leaked >&2; exit 1; }
command -v node >/dev/null || { echo node missing >&2; exit 1; }
BOOTSTRAP_RELEASE_TAG=v1.42.0
unset BOOTSTRAP_RELEASE_COMMIT INSTALL_SCRIPT_URL
BOOTSTRAP_RAW_BASE_URL=https://raw.example.invalid/repository
fetch_bootstrap_file() { printf '%s\n' '{"sha":"''' + commit + r'''"}' > "$2"; }
resolve_bootstrap_release
[ "$BOOTSTRAP_COMMIT" = "''' + commit + r'''" ]
payload="$TMPDIR/payload"
mkdir -p "$payload"
printf 'helper-bytes' > "$payload/claude-notifications-linux-amd64"
printf '%s  claude-notifications-linux-amd64\n' "$(
  NODE_OPTIONS= NODE_PATH= node --no-warnings -e 'const fs=require("fs");const c=require("crypto");process.stdout.write(c.createHash("sha256").update(fs.readFileSync(process.argv[1])).digest("hex"))' "$payload/claude-notifications-linux-amd64"
)" > "$payload/checksums.txt"
run_isolated_node - "$payload" claude-notifications-linux-amd64 <<'JSVERIFY'
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const root = process.argv[2], name = process.argv[3];
const expected = fs.readFileSync(path.join(root, 'checksums.txt'), 'utf8').split(/\r?\n/)
  .map((line) => line.split(/\s+/).filter(Boolean))
  .filter((e) => e.length === 2 && e[1].replace(/^\*/, '') === name)
  .map((e) => e[0]);
const actual = crypto.createHash('sha256').update(fs.readFileSync(path.join(root, name))).digest('hex');
if (expected.length !== 1 || actual !== expected[0].toLowerCase()) process.exit(1);
JSVERIFY
printf 'bad  claude-notifications-linux-amd64\n' > "$payload/checksums.txt"
if run_isolated_node - "$payload" claude-notifications-linux-amd64 <<'JSVERIFY'
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const root = process.argv[2], name = process.argv[3];
const expected = fs.readFileSync(path.join(root, 'checksums.txt'), 'utf8').split(/\r?\n/)
  .map((line) => line.split(/\s+/).filter(Boolean))
  .filter((e) => e.length === 2 && e[1].replace(/^\*/, '') === name)
  .map((e) => e[0]);
const actual = crypto.createHash('sha256').update(fs.readFileSync(path.join(root, name))).digest('hex');
if (expected.length !== 1 || actual !== expected[0].toLowerCase()) process.exit(1);
JSVERIFY
then echo 'checksum mismatch accepted' >&2; exit 1; fi
'''
        env = dict(os.environ, PATH=path, FUNCTIONS=str(functions), RUNTIME_PATH=path,
                   TMPDIR=str(case), HOME=str(case / 'home'))
        (case / 'home').mkdir()
        result = subprocess.run(['bash', '-c', script], env=env, text=True, capture_output=True, timeout=20)
        if result.returncode != 0:
            fail('bootstrap node-only commit+checksum', result.stderr + result.stdout)
        pass_name('bootstrap.sh node-only commit parse and checksum verify')
else:
    print('SKIP bootstrap.sh node-only commit parse and checksum verify')

# --- install.sh: node-only config preflight transport ---
if shutil.which('node'):
    with tempfile.TemporaryDirectory(prefix='install-node-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        functions = case / 'functions.sh'
        functions.write_text((root / 'bin/install.sh').read_text().replace('main "$@"', ''))
        helper = case / 'helper'
        helper.write_text('#!' + sys.executable + '''
import json, os, sys
assert sys.argv[1:] == ["config", "preflight-update", "--stdin", "--json"]
request = json.load(sys.stdin)
open(os.environ["TRACE"], "w").write(json.dumps(request))
print(json.dumps({"status": "safe", "diagnostics": []}))
''')
        helper.chmod(0o755)
        target = case / 'outside.json'
        target.write_text('{}')
        path = runtime_path(case, node=True)
        script = '''
source "$FUNCTIONS"
PATH="$RUNTIME_PATH"
command -v python3 >/dev/null && { echo python3 leaked >&2; exit 1; }
detect_platform
INSTALL_CONFIG_HELPER="$HELPER"
AGENT_NOTIFICATIONS_CONFIG="$TARGET"
guard_install_paths "$PWD"
'''
        env = dict(os.environ, PATH=path, FUNCTIONS=str(functions), RUNTIME_PATH=path,
                   HELPER=str(helper), TARGET=str(target), TRACE=str(case / 'trace'),
                   TMPDIR=str(case), HOME=str(case / 'home'), PWD=str(case))
        (case / 'home').mkdir()
        result = subprocess.run(['bash', '-c', script], cwd=str(case), env=env, text=True,
                                capture_output=True, timeout=20)
        if result.returncode != 0:
            fail('install.sh node-only preflight', result.stderr + result.stdout)
        request = json.loads((case / 'trace').read_text())
        if 'refreshDirs' not in request:
            fail('install.sh node-only preflight', repr(request))
        pass_name('install.sh node-only config preflight')
else:
    print('SKIP install.sh node-only config preflight')

print('All installer python/node runtime e2e fixtures passed.')
PY
