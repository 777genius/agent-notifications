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


def is_windows_store_or_wsl_alias(src):
    # GitHub Windows images expose python3.exe/bash.exe as WSL/Store stubs.
    # CreateProcess('bash') also searches System32 before PATH.
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


def place_runtime_cmd(dest, src):
    # Git Bash builtins are not files, and Windows often cannot create native
    # symlinks. Exec wrappers keep restricted PATH tests portable.
    if dest.exists() or not src or not os.path.isfile(src) or is_windows_store_or_wsl_alias(src):
        return
    dest.write_text('#!/bin/sh\nexec {} "$@"\n'.format(shlex.quote(src.replace('\\', '/'))))
    dest.chmod(0o755)


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
        place_runtime_cmd(bin_dir / name, host_cmd(name))
    return str(bin_dir)


def extract_quoted_heredoc(path, marker):
    text = path.read_text(encoding='utf-8')
    token = "<<'" + marker + "'"
    start = text.index('\n', text.index(token)) + 1
    end = text.index('\n' + marker + '\n', start)
    return text[start:end]


def pass_name(name):
    print('PASS ' + name, flush=True)


def fail(name, detail):
    raise AssertionError(name + ': ' + detail)


def describe(result):
    return 'exit=%s stdout=%r stderr=%r' % (result.returncode, result.stdout, result.stderr)


if is_windows_store_or_wsl_alias(r'C:\Windows\System32\python3.exe') is not True \
        or is_windows_store_or_wsl_alias(r'C:\Windows\System32\bash.exe') is not True \
        or is_windows_store_or_wsl_alias(r'C:\Users\x\AppData\Local\Microsoft\WindowsApps\python3.exe') is not True \
        or is_windows_store_or_wsl_alias(r'C:\hostedtoolcache\windows\Python\3.12.10\x64\python.exe') \
        or is_windows_store_or_wsl_alias(r'C:\Program Files\Git\bin\bash.exe'):
    fail('wsl alias detection', 'expected System32/WindowsApps stubs to be skipped')
if host_cmd('python3') != sys.executable and not (
        host_cmd('python3') and os.path.isfile(host_cmd('python3'))):
    fail('host python3', repr(host_cmd('python3')))
HOST_BASH = host_cmd('bash')
if not HOST_BASH:
    fail('host bash', 'Git Bash / bash executable not found')
HOST_NODE = host_cmd('node')
pass_name('skip Windows WSL/Store python3 aliases')


# --- setup.sh loader: python-only, node-only, neither, python-preferred ---
sha = '0123456789abcdef0123456789abcdef01234567'
loader = (root / 'bin/setup.sh').read_text(encoding='utf-8')
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
                + shlex.quote(host_cmd('python3').replace('\\', '/')) + ' "$@"\n')
            (case / 'bin/python3').chmod(0o755)
            (case / 'bin/node').write_text(
                '#!/usr/bin/env bash\nprintf node >> "$CASE_DIR/runtime.log"\nexec '
                + shlex.quote(HOST_NODE.replace('\\', '/')) + ' "$@"\n')
            (case / 'bin/node').chmod(0o755)
        env = dict(os.environ, PATH=path, CASE_DIR=str(case), TMPDIR=str(case / 'tmp space'))
        result = subprocess.run([HOST_BASH, str(root / 'bin/setup.sh'), '--product', 'codex'],
                                text=True, capture_output=True, env=env, timeout=20)
        if expected == 0:
            if result.returncode != 0:
                fail(name, describe(result))
            ran = json.loads((case / 'ran.json').read_text())
            if ran['tag'] != 'v1.43.0' or ran['sha'] != sha:
                fail(name, repr(ran))
            if preferred:
                log = (case / 'runtime.log').read_text()
                if not log.startswith('python3') or 'node' in log:
                    fail(name, log)
        else:
            if result.returncode == 0 or (case / 'ran.json').exists():
                fail(name, 'installer ran without a JSON runtime: ' + describe(result))
            if 'python3 or node is required' not in result.stderr:
                fail(name, describe(result))
        if list((case / 'tmp space').iterdir()):
            fail(name, 'leaked staging directory')
        pass_name(name)


if host_cmd('python3'):
    setup_case('setup.sh python-only', python=True)
else:
    print('SKIP setup.sh python-only')
if HOST_NODE:
    setup_case('setup.sh node-only', node=True)
else:
    print('SKIP setup.sh node-only')
setup_case('setup.sh neither runtime', expected=1)
if host_cmd('python3') and HOST_NODE:
    setup_case('setup.sh python preferred', python=True, node=True, preferred=True)

# --- bootstrap.sh: node-only commit parse and checksum verify ---
if HOST_NODE:
    with tempfile.TemporaryDirectory(prefix='bootstrap-node-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        path = runtime_path(case, node=True)
        functions = case / 'functions.sh'
        functions.write_text((root / 'bin/bootstrap.sh').read_text(encoding='utf-8').replace('main "$@"', ''), encoding='utf-8')
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
        result = subprocess.run([HOST_BASH, '-c', script], env=env, text=True, capture_output=True, timeout=20)
        if result.returncode != 0:
            fail('bootstrap node-only commit+checksum', result.stderr + result.stdout)
        pass_name('bootstrap.sh node-only commit parse and checksum verify')
else:
    print('SKIP bootstrap.sh node-only commit parse and checksum verify')

# --- install.sh: node-only config preflight transport ---
if HOST_NODE:
    with tempfile.TemporaryDirectory(prefix='install-node-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        functions = case / 'functions.sh'
        functions.write_text((root / 'bin/install.sh').read_text(encoding='utf-8').replace('main "$@"', ''), encoding='utf-8')
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
        result = subprocess.run([HOST_BASH, '-c', script], cwd=str(case), env=env, text=True,
                                capture_output=True, timeout=20)
        if result.returncode != 0:
            fail('install.sh node-only preflight', result.stderr + result.stdout)
        request = json.loads((case / 'trace').read_text())
        if 'refreshDirs' not in request:
            fail('install.sh node-only preflight', repr(request))
        pass_name('install.sh node-only config preflight')
else:
    print('SKIP install.sh node-only config preflight')

# Production JSSTAGE must resolve TMPDIR through a symlink ancestor, matching
# Python os.path.realpath, so overlap into a refresh root is rejected.
if HOST_NODE:
    jsstage = extract_quoted_heredoc(root / 'bin/bootstrap.sh', 'JSSTAGE')
    with tempfile.TemporaryDirectory(prefix='jsstage-', dir=os.environ['TMPDIR']) as tmp:
        td = Path(tmp)
        plugin = td / 'plugin'
        plugin.mkdir()
        alias = td / 'alias'
        try:
            alias.symlink_to(plugin, target_is_directory=True)
        except OSError:
            print('SKIP JSSTAGE symlink ancestor overlap: cannot create symlink')
        else:
            scratch = alias / 'scratch'
            env = dict(os.environ)
            result = subprocess.run(
                [HOST_NODE, '-', str(scratch), str(td / 'missing.json'), 'claude-notifications-go',
                 'codex', str(td / 'cache'), str(td / 'market'), str(plugin)],
                input=jsstage, text=True, capture_output=True, env=env, timeout=20)
            if result.returncode == 0 or 'Staging must be outside refreshed bundles' not in result.stderr:
                fail('JSSTAGE symlink ancestor overlap', describe(result))
            pass_name('JSSTAGE rejects TMPDIR under symlink into plugin root')
            child = plugin / 'child'
            child.mkdir()
            outside = td / 'outside'
            outside.mkdir()
            link = outside / 'link'
            link.symlink_to(child, target_is_directory=True)
            via_parent = str(link / '..')
            if os.path.realpath(via_parent) != os.path.realpath(plugin):
                fail('python realpath fixture', via_parent)
            result = subprocess.run(
                [HOST_NODE, '-', via_parent, str(td / 'missing.json'), 'claude-notifications-go',
                 'codex', str(td / 'cache'), str(td / 'market'), str(plugin)],
                input=jsstage, text=True, capture_output=True, env=env, timeout=20)
            if result.returncode == 0 or 'Staging must be outside refreshed bundles' not in result.stderr:
                fail('JSSTAGE symlink .. overlap', describe(result) + ' tmpdir=' + via_parent)
            pass_name('JSSTAGE rejects TMPDIR via symlink then ..')
    helpers, _, _ = jsstage.partition('const argv = process.argv.slice(2);')
    drive_js = (
        "Object.defineProperty(process, 'platform', { value: 'win32' });\n"
        + helpers
        + r'''
const rootRelative = walkReal('D:\\outside', '\\plugin', Object.create(null));
if (rootRelative !== 'D:\\plugin') {
  process.stderr.write('root-relative: ' + rootRelative + '\n');
  process.exit(1);
}
const otherDrive = walkReal('D:\\outside', 'C:\\other\\bundle', Object.create(null));
if (otherDrive !== 'C:\\other\\bundle') {
  process.stderr.write('other-drive: ' + otherDrive + '\n');
  process.exit(1);
}
'''
    )
    result = subprocess.run([HOST_NODE, '-'], input=drive_js, text=True,
                            capture_output=True, timeout=20)
    if result.returncode != 0:
        fail('JSSTAGE Windows root-relative drive', describe(result))
    pass_name('JSSTAGE keeps Windows root-relative symlink targets on the source drive')
else:
    print('SKIP JSSTAGE symlink ancestor overlap: node not available')

# Malformed diagnostics are a protocol failure (status 2), not a final reject.
if HOST_NODE:
    jsinstall = extract_quoted_heredoc(root / 'bin/install.sh', 'JSINSTALL')
    with tempfile.TemporaryDirectory(prefix='jsinstall-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        helper = case / 'helper'
        helper.write_text(
            '#!/bin/sh\nprintf \'{"status":"unsafe-target","diagnostics":["invalid"]}\\n\'\n')
        helper.chmod(0o755)
        result = subprocess.run(
            [HOST_NODE, '-', str(helper), 'linux', str(case)],
            input=jsinstall, text=True, capture_output=True, timeout=20)
        if result.returncode != 2:
            fail('JSINSTALL malformed diagnostics', describe(result))
        pass_name('JSINSTALL malformed diagnostics exits 2')
else:
    print('SKIP JSINSTALL malformed diagnostics: node not available')

# NODE_OPTIONS must not pollute plugin registry parses on node-only installs.
if HOST_NODE:
    with tempfile.TemporaryDirectory(prefix='node-options-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        path = runtime_path(case, node=True)
        functions = case / 'functions.sh'
        functions.write_text((root / 'bin/bootstrap.sh').read_text(encoding='utf-8').replace('main "$@"', ''), encoding='utf-8')
        plugin = case / 'plugin'
        plugin.mkdir()
        installed = case / 'installed.json'
        key = 'claude-notifications-go@claude-notifications-go'
        installed.write_text(json.dumps({
            'plugins': {key: [{'installPath': str(plugin), 'version': '1.42.0'}]}
        }))
        (case / 'preload.js').write_text('process.stdout.write("POLLUTED\\n");\n')
        script = r'''
source "$FUNCTIONS"
PATH="$RUNTIME_PATH"
command -v python3 >/dev/null && { echo python3 leaked >&2; exit 1; }
command -v jq >/dev/null && { echo jq leaked >&2; exit 1; }
export NODE_OPTIONS="--require=./preload.js"
PLUGIN_KEY="claude-notifications-go@claude-notifications-go"
INSTALLED_JSON="$INSTALLED"
ver=$(get_installed_plugin_version)
root=$(get_installed_plugin_root)
printf 'ver=%s root=%s\n' "$ver" "$root"
[ "$ver" = "1.42.0" ] || exit 1
[ "$root" = "$PLUGIN_DIR" ] || exit 1
'''
        env = dict(os.environ, PATH=path, FUNCTIONS=str(functions), RUNTIME_PATH=path,
                   INSTALLED=str(installed), PLUGIN_DIR=str(plugin), TMPDIR=str(case),
                   HOME=str(case / 'home'))
        (case / 'home').mkdir()
        result = subprocess.run([HOST_BASH, '-c', script], cwd=str(case), env=env, text=True,
                                capture_output=True, timeout=20)
        if result.returncode != 0 or 'POLLUTED' in result.stdout:
            fail('isolated node ignores NODE_OPTIONS', describe(result))
        pass_name('get_installed_* ignores NODE_OPTIONS on node-only PATH')
else:
    print('SKIP isolated node NODE_OPTIONS: node not available')

# Generated cache shims are standalone POSIX scripts: they must not call
# bootstrap helpers, and node-only parses must still find installPath.
# Heredocs inside $(...) are invalid: ")" in Python/JS closes the substitution.
shim = extract_quoted_heredoc(root / 'bin/bootstrap.sh', 'SHIMEOF')
if 'run_isolated_node' in shim:
    fail('generated hook-wrapper shim', 'standalone shim calls run_isolated_node')
if 'NODE_OPTIONS= NODE_PATH= node --no-warnings' not in shim:
    fail('generated hook-wrapper shim', 'standalone shim missing inline node isolation')
if '<<' in shim:
    fail('generated hook-wrapper shim', 'standalone shim uses a heredoc inside $()')
if "python3 -I -c '" not in shim or "node --no-warnings -e '" not in shim:
    fail('generated hook-wrapper shim', 'standalone shim missing quoted -c/-e parsers')
pass_name('generated hook-wrapper shim inlines isolated node')

if HOST_NODE:
    with tempfile.TemporaryDirectory(prefix='shim-node-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        claude_home = case / 'claude'
        (claude_home / 'plugins').mkdir(parents=True)
        current = case / 'current-plugin'
        (current / 'bin').mkdir(parents=True)
        wrapper = current / 'bin/hook-wrapper.sh'
        wrapper.write_text('#!/bin/sh\necho ran > "$CLAUDE_PLUGIN_ROOT/ran"\nexit 0\n', encoding='utf-8')
        wrapper.chmod(0o755)
        (claude_home / 'plugins/installed_plugins.json').write_text(json.dumps({
            'plugins': {
                'claude-notifications-go@claude-notifications-go': [
                    {'installPath': str(current), 'version': '1.42.0'}
                ]
            }
        }), encoding='utf-8')
        shim_path = case / 'old' / 'bin' / 'hook-wrapper.sh'
        shim_path.parent.mkdir(parents=True)
        shim_path.write_text(shim, encoding='utf-8')
        shim_path.chmod(0o755)
        syntax = subprocess.run([HOST_BASH, '-n', str(shim_path)], text=True,
                                capture_output=True, timeout=10)
        if syntax.returncode != 0:
            fail('generated shim bash -n', describe(syntax))
        host_sh = host_cmd('sh')
        if host_sh:
            syntax = subprocess.run([host_sh, '-n', str(shim_path)], text=True,
                                    capture_output=True, timeout=10)
            if syntax.returncode != 0:
                fail('generated shim sh -n', describe(syntax))
        pass_name('generated hook-wrapper shim is valid POSIX sh')
        (case / 'preload.js').write_text('process.stdout.write("POLLUTED\\n");\n', encoding='utf-8')
        path = runtime_path(case, node=True)
        ran = current / 'ran'
        script = r'''
PATH="$RUNTIME_PATH"
command -v python3 >/dev/null && { echo python3 leaked >&2; exit 1; }
command -v jq >/dev/null && { echo jq leaked >&2; exit 1; }
command -v node >/dev/null || { echo node missing >&2; exit 1; }
export NODE_OPTIONS="--require=./preload.js"
"$HOST_BASH" "$SHIM" Stop
'''
        env = dict(os.environ, PATH=path, RUNTIME_PATH=path, SHIM=str(shim_path),
                   HOST_BASH=HOST_BASH, CLAUDE_HOME=str(claude_home),
                   CLAUDE_CONFIG_DIR=str(claude_home), HOME=str(case / 'home'),
                   TMPDIR=str(case))
        (case / 'home').mkdir()
        result = subprocess.run([HOST_BASH, '-c', script], cwd=str(case), env=env,
                                text=True, capture_output=True, timeout=20)
        if result.returncode != 0 or 'POLLUTED' in result.stdout:
            fail('generated shim node-only parse', describe(result))
        if not ran.is_file() or ran.read_text(encoding='utf-8').strip() != 'ran':
            fail('generated shim node-only parse',
                 'hook-wrapper was not execed: ' + describe(result))
        pass_name('generated hook-wrapper shim parses installPath on node-only PATH')
else:
    print('SKIP generated hook-wrapper shim node-only parse: node not available')

print('All installer python/node runtime e2e fixtures passed.')
PY
