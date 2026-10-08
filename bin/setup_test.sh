#!/usr/bin/env bash
# The Windows private-root fixture reuses the already prepared offline Go cache.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) TEST_ENV_HANDOFF_GOMODCACHE=1 ;;
esac
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
python3 -I - "$ROOT" <<'PY'
import hashlib
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
                      if line.startswith('curl -fsSL '))
PUBLIC_SETUP_URL = 'https://agent-notifications.com/install.sh'
# The public pin and main commit 9039815 reference the same setup.sh Git blob.
pinned_loader = (root / 'bin/testdata/setup-9039815833ed8d16a11ee4a45de62bb0119c874f.sh').read_text(
    encoding='utf-8')
assert hashlib.sha256(pinned_loader.encode()).hexdigest() == \
    '7b00c0cfbeff547e60d38d4703873975b9dcea5682a8312f908477d0d0e4e4a4'
assert 'python3 or node is required' in pinned_loader
assert PUBLIC_SETUP_URL in public_command
for rel in ('docs/INSTALLATION.md', 'landing/data/install.ts',
            'landing/tests/install.test.ts', 'landing/tests/browser/install.spec.ts'):
    assert PUBLIC_SETUP_URL in (root / rel).read_text(encoding='utf-8'), rel
assert (root / 'landing/public/install.sh').read_text(encoding='utf-8') == loader
STORE_PYTHON3_STUB = '''#!/usr/bin/env bash
echo "Python was not found; run without arguments to install from the Microsoft Store, or disable this shortcut from Settings > Apps > Advanced app settings > App execution aliases." >&2
exit 9009
'''
sha = '0123456789abcdef0123456789abcdef01234567'
raw = 'https://raw.githubusercontent.com/777genius/agent-notifications/' + sha + '/bin'
assert 'python3' not in loader and 'node' not in loader
assert 'application/vnd.github.sha' in loader
curl_stub = '''#!/usr/bin/env bash
set -eu
output=""; url=""; format=""; accept=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output="$2"; shift 2 ;;
        -w) format="$2"; shift 2 ;;
        -H) accept="$2"; shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
printf '%s\\n' "$url" >> "$CASE_DIR/requests"
case "$url" in
    ''' + PUBLIC_SETUP_URL + ''') kind=setup ;;
    https://github.com/777genius/agent-notifications/releases/latest) kind=latest ;;
    https://api.github.com/repos/777genius/agent-notifications/releases/latest) kind=latest_json ;;
    https://api.github.com/repos/777genius/agent-notifications/commits/main) kind=controller ;;
    https://api.github.com/repos/777genius/agent-notifications/commits/v*) kind=commit ;;
    https://raw.githubusercontent.com/777genius/agent-notifications/*/bin/release-channel.sh) kind=channel_module ;;
    https://raw.githubusercontent.com/777genius/agent-notifications/*/release-channels.tsv) kind=latest ;;
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
source="$kind"
if [ "$kind" = latest ]; then source=channels; fi
if [ "$kind" = commit ] && [ "$accept" != 'Accept: application/vnd.github.sha' ]; then
    source=commit_json
fi
if [ -n "$output" ]; then
    cat "$CASE_DIR/$source" > "$output"
else
    cat "$CASE_DIR/$source"
fi
# Return a failure AFTER emitting valid bytes, to catch accidental execution.
if [ "${FAIL_DOWNLOAD:-}" = "$kind" ]; then exit 22; fi
'''
# Record argv in bash. Native Windows python.exe CRT-globs "*" when Git Bash
# execs it, so sys.argv cannot prove setup.sh forwarded the literal argument.
bootstrap_stub = '''#!/usr/bin/env bash
set -eu
if [ "${1:-}" = --selector-capabilities ]; then
    [ "${LEGACY_BOOTSTRAP:-}" != 1 ] || exit 1
    printf 'terminal-selector-v1\n'
    exit 0
fi
if [ "${1:-}" = --capabilities ]; then
    [ "${NO_PRODUCT_CAPABILITY:-}" != 1 ] || exit 1
    printf '%s\\n' bootstrap-products-v1
    exit 0
fi
if [ "${LEGACY_BOOTSTRAP:-}" = 1 ]; then
    case "${1:-}" in
        '') ;;
        --product)
            case "${2:-}" in claude|codex|both|opencode) ;; *) exit 2 ;; esac ;;
        *) exit 2 ;;
    esac
fi
: > "$CASE_DIR/argv0"
for a in "$@"; do
    printf '%s\\0' "$a" >> "$CASE_DIR/argv0"
done
''' + shlex.quote(sys.executable.replace('\\', '/')) + ''' -I -c 'import json,os; print(json.dumps({"tag":os.environ["BOOTSTRAP_RELEASE_TAG"],"sha":os.environ["BOOTSTRAP_RELEASE_COMMIT"],"install":os.environ["INSTALL_SCRIPT_URL"]}))' > "$CASE_DIR/ran.json"
STAGED_BOOTSTRAP="$0" ''' + shlex.quote(sys.executable.replace('\\', '/')) + ''' -I -c 'import json,os; from pathlib import Path; c=Path(os.environ["RECORD_DIR"]); r=json.loads((c/"ran.json").read_text()); r["args"]=[a.decode() for a in (c/"argv0").read_bytes().split(b"\\0") if a]; r["script"]=os.environ["STAGED_BOOTSTRAP"]; print(json.dumps(r))' >> "$CASE_DIR/calls.jsonl"
case "${2:-}" in
    opencode) status="${OPENCODE_STATUS:-${BOOTSTRAP_STATUS:-0}}" ;;
    *) status="${LEGACY_STATUS:-${BOOTSTRAP_STATUS:-0}}" ;;
esac
if [ "$status" -eq 0 ] && [ -n "${BOOTSTRAP_SUMMARY_FILE:-}" ]; then
    selection="${2:-}"
    [ "$selection" != both ] || selection=claude,codex
    for client in ${selection//,/ }; do
        case "$client" in
            claude) label="Claude" ;;
            codex) label=Codex ;;
            opencode) label=OpenCode ;;
            gemini) label="Gemini CLI" ;;
            *) continue ;;
        esac
        echo "  $label - installed; restart required." >> "$BOOTSTRAP_SUMMARY_FILE"
    done
    echo "  Delivery has not been verified." >> "$BOOTSTRAP_SUMMARY_FILE"
fi
exit "$status"
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


def run_loader(args, env, piped=False, documented=False, bash_executable=None):
    env = with_noglob(env)
    runner = bash_executable or HOST_BASH
    setup_sh = bash_path(root / 'bin/setup.sh')
    if documented:
        command = [runner, '-c', public_command.replace(
            '| bash', '| bash -s -- ' + ' '.join(map(shlex.quote, args)))]
        stdin = None
    elif piped:
        command = [runner, '-c', 'exec ' + ' '.join(
            shlex.quote(x) for x in [bash_path(runner), '-s', '--'] + args)]
        stdin = loader
    else:
        # Explicitly select the interpreter for native Bash 3.2 regressions;
        # default fixtures retain the normal env-bash entry point.
        invocation = ([bash_path(runner)] if bash_executable else []) + [setup_sh] + args
        command = [runner, '-c', 'exec ' + ' '.join(map(shlex.quote, invocation))]
        stdin = None
    return subprocess.run(command, input=stdin, text=True, capture_output=True, env=env, timeout=20)


def channel_fixture(case, tag, release_commit=sha):
    (case / 'controller').write_text(sha)
    (case / 'channel_module').write_text((root / 'bin/release-channel.sh').read_text())
    rows = ['# agent-notifications-platform-channels-v1']
    for os_name, arch in [('darwin','amd64'),('darwin','arm64'),('linux','amd64'),('linux','arm64'),('windows','amd64')]:
        ref = 'release/platform-macos' if os_name == 'darwin' else 'release/platform-linux-windows'
        rows.append('\t'.join(map(str, [os_name, arch, tag, release_commit, sha, ref])))
    (case / 'channels').write_text('\n'.join(rows) + '\n')


def channel_requests():
    return ['https://api.github.com/repos/777genius/agent-notifications/commits/main',
            raw + '/release-channel.sh', raw.removesuffix('/bin') + '/release-channels.tsv']


def run_case(name, tag=None, commit=None, fail='', status=0, expected=None, piped=False,
             documented=False, expect_run=True, args=None, calls=None, no_network=False,
             legacy_status=None, opencode_status=None, message=None, help_only=False,
             release=None, release_only=False, bash_executable=None, legacy_bootstrap=False):
    if args is None:
        args = ['--product', 'both', 'argument with spaces', '*']
    if release is None:
        release = 'v1.46.0' if any(a == '--products' or a.startswith('--products=') for a in args) else 'v1.43.0'
    with tempfile.TemporaryDirectory(prefix='setup-test-', dir=os.environ['TMPDIR']) as tmp:
        case = Path(tmp)
        (case / 'bin').mkdir()
        (case / 'tmp space').mkdir()
        (case / 'bin/curl').write_text(curl_stub)
        (case / 'bin/curl').chmod(0o755)
        if tag is None:
            latest_value = release
        else:
            try:
                latest_value = json.loads(tag).get('tag_name')
            except Exception:
                latest_value = tag
        (case / 'latest_url').write_text(
            'https://github.com/777genius/agent-notifications/releases/tag/' + str(latest_value))
        channel_fixture(case, latest_value)
        (case / 'latest_json').write_text(
            json.dumps({'tag_name': 'v1.43.0'}) if tag is None else tag)
        if commit is None:
            commit_value = sha
        else:
            try:
                commit_value = json.loads(commit).get('sha')
            except Exception:
                commit_value = commit
        (case / 'commit').write_text('' if commit_value is None else str(commit_value))
        (case / 'commit_json').write_text(
            json.dumps({'sha': sha}) if commit is None else commit)
        (case / 'bootstrap').write_text(bootstrap_stub)
        (case / 'setup').write_text(loader)
        env = dict(os.environ, PATH=runtime_path(case, python=True, node=True, bash_executable=bash_executable),
                   CASE_DIR=bash_path(case), RECORD_DIR=str(case), TMPDIR=bash_path(case / 'tmp space'),
                   FAIL_DOWNLOAD=fail, BOOTSTRAP_STATUS=str(status),
                   NO_PRODUCT_CAPABILITY='1' if legacy_bootstrap else '0',
                   LEGACY_BOOTSTRAP='1' if legacy_bootstrap else '0',
                   BOOTSTRAP_RELEASE_TAG='untrusted', BOOTSTRAP_RELEASE_COMMIT='untrusted',
                   INSTALL_SCRIPT_URL='https://example.invalid/not-used')
        if legacy_status is not None:
            env['LEGACY_STATUS'] = str(legacy_status)
        if opencode_status is not None:
            env['OPENCODE_STATUS'] = str(opencode_status)
        result = run_loader(args, env, piped=piped, documented=documented, bash_executable=bash_executable)
        if no_network or help_only:
            assert result.returncode == (0 if help_only else 1), (name, result.returncode, result.stderr)
            assert not (case / 'requests').exists(), name + ': parser performed a network request'
            assert not (case / 'ran.json').exists(), name + ': parser ran installer'
            if help_only:
                assert '--products' in result.stdout and '--desktop' in result.stdout, name
        elif release_only:
            assert result.returncode == 1, (name, result.returncode, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran on unsupported release'
            assert (case / 'requests').read_text().splitlines() == channel_requests(), name + ': unsupported release fetched bootstrap'
            assert 'No products were installed.' in result.stderr, (name, result.stderr)
        elif calls is not None:
            assert result.returncode == expected, (name, result.returncode, result.stderr)
            got = [json.loads(line) for line in (case / 'calls.jsonl').read_text().splitlines()]
            assert [r['args'] for r in got] == calls, (name, got)
            assert all({k: r[k] for k in ('tag', 'sha', 'install')} == {
                'tag': release, 'sha': sha, 'install': raw + '/install.sh'
            } for r in got), (name, got)
            assert len({r['script'] for r in got}) == 1, name + ': different staged bootstrap'
            assert (case / 'requests').read_text().splitlines() == channel_requests() + [
                'https://api.github.com/repos/777genius/agent-notifications/commits/' + release,
                raw + '/bootstrap.sh',
            ], name + ': expected one release resolution and bootstrap download'
            if legacy_bootstrap:
                pass  # Older bootstraps own their output and ignore the summary channel.
            elif result.returncode:
                assert 'Installation complete' not in result.stdout, (name, result.stdout)
            else:
                assert result.stdout.count('Installation complete') == 1, (name, result.stdout)
                assert 'Delivery has not been verified.' in result.stdout, (name, result.stdout)
                for call in calls:
                    selected = ['claude', 'codex'] if call[1] == 'both' else call[1].split(',')
                    for client in selected:
                        label = {'claude': 'Claude', 'codex': 'Codex', 'opencode': 'OpenCode', 'gemini': 'Gemini CLI'}[client]
                        assert label + ' - installed; restart required.' in result.stdout, (name, result.stdout)
        elif expected is None:
            assert result.returncode != 0, (name, result.stdout, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran on failure'
        elif not expect_run:
            assert result.returncode == expected, (name, result.returncode, result.stderr)
            assert not (case / 'ran.json').exists(), name + ': installer ran after loader failure'
            assert (case / 'requests').read_text(encoding='utf-8').splitlines() == [PUBLIC_SETUP_URL]
        else:
            assert result.returncode == expected, (name, result.returncode, result.stderr)
            if (case / 'ran.json').exists():
                got = recorded_install(case)
                assert got == {
                    'args': args, 'tag': release, 'sha': sha, 'install': raw + '/install.sh'
                }, (name, got)
            else:
                raise AssertionError(name + ': installer did not run')
            requests = (case / 'requests').read_text(encoding='utf-8').splitlines()
            if documented:
                assert requests.pop(0) == PUBLIC_SETUP_URL
            assert requests == channel_requests() + [
                'https://api.github.com/repos/777genius/agent-notifications/commits/' + release,
                raw + '/bootstrap.sh',
            ], name
        if message:
            assert message in result.stderr, (name, result.stderr)
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


def runtime_path(case, python=False, node=False, bash_executable=None):
    bin_dir = case / 'runtime-bin'
    bin_dir.mkdir()
    names = ['bash', 'sh', 'mktemp', 'rm', 'cat', 'chmod', 'mkdir', 'ln', 'uname',
             'tr', 'wc', 'cmp', 'grep', 'head', 'cp', 'mv', 'env', 'true', 'false', 'dirname', 'basename',
             'printf', 'pwd', 'cygpath', 'awk']
    if python:
        names.append('python3')
    if node:
        names.append('node')
    for name in names:
        source = bash_executable if name == 'bash' and bash_executable else host_cmd(name)
        place_runtime_cmd(bin_dir / name, source)
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
        channel_fixture(case, 'v1.43.0')
        (case / 'commit').write_text(sha)
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=runtime_path(case, python=python, node=node),
                   CASE_DIR=bash_path(case), RECORD_DIR=str(case), TMPDIR=bash_path(case / 'tmp space'),
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


# Regression: pending JSON previously acquired a helper and entered UI; malformed
# Invalid flags and pending product UI stop before acquisition.
for args in [['--json'], ['--product', 'gemini', '--json'],
             ['--products', 'opencode,opencode'], ['--ui=wat'],
             ['--plain', '--ui=rich'], ['--yes'], ['--unknown']]:
    run_case('pure parse refuses ' + repr(args), args=args, no_network=True)
for args in [['--plain'], ['--ui=plain'], ['--product', 'gemini', '--plain']]:
    run_case('terminal feature forwarding ' + repr(args), args=args, expected=0)
run_case('complete legacy JSON remains explicit',
         args=['--product', 'claude', '--json'], expected=0)

run_case('pinned release and exact argv', expected=0)
run_case('piped one-line entry point', expected=0, piped=True)
run_case('documented one-line command', expected=0, documented=True)
run_case('initial loader download failure', fail='setup', expected=0, documented=True, expect_run=False)
run_case('bootstrap exit status', status=17, expected=17)
# v1.46.1 accepts the interactive entry point and old single-product selectors,
# but has neither --capabilities nor --products. Exercise its argv boundary.
for args in [['--product', 'claude'], ['--product', 'codex'],
             ['--product', 'both'], ['--product', 'opencode', '--desktop']]:
    run_case('stable bootstrap passthrough ' + repr(args), args=args,
             release='v1.46.1', legacy_bootstrap=True, expected=0)
# Regression: UI presentation alone must not feature-gate a complete explicit
# request; the public loader must forward it to the older published bootstrap.
run_case('complete explicit UI without terminal feature',
         args=['--product', 'opencode', '--webhook', '--plain'],
         release='v1.46.1', legacy_bootstrap=True, expected=0)
# Regression: an old published no-selector menu could install without the new
# shared final consent. Explicit compatibility above remains intentional.
run_case('stable interactive feature refusal', args=[], release='v1.46.1',
         legacy_bootstrap=True, message='needs terminal selector support')
for args in [['--product', 'gemini', '--desktop'], ['--product=gemini', '--webhook'],
             ['--products', 'claude,gemini', '--webhook']]:
    run_case('stable bootstrap rejects new selector ' + repr(args), args=args,
             release='v1.46.1', legacy_bootstrap=True,
             message='No products were installed.')
for selection, routed in [
    ('claude', [['--product', 'claude']]),
    ('codex', [['--product', 'codex']]),
    ('claude,codex', [['--product', 'both']]),
    ('opencode', [['--product', 'opencode', '--desktop']]),
    ('claude,opencode', [['--product', 'claude'], ['--product', 'opencode', '--desktop']]),
    ('codex,opencode', [['--product', 'codex'], ['--product', 'opencode', '--desktop']]),
    ('opencode,codex,claude', [['--product', 'both'], ['--product', 'opencode', '--desktop']]),
]:
    args = ['--products', selection] + (['--desktop'] if 'opencode' in selection else [])
    run_case('stable selector fallback ' + selection, args=args, calls=routed,
             release='v1.46.1', legacy_bootstrap=True, expected=0)
    if sys.platform == 'darwin':
        run_case('native Bash stable selector fallback ' + selection, args=args, calls=routed,
                 release='v1.46.1', legacy_bootstrap=True, expected=0, bash_executable='/bin/bash')
fallback_args = ['--products=opencode,claude,codex', '--skip-agent-notify', '--webhook']
fallback_calls = [['--product', 'both', '--skip-agent-notify'],
                  ['--product', 'opencode', '--webhook']]
run_case('stable fallback scoped flags', args=fallback_args, calls=fallback_calls,
         release='v1.46.1', legacy_bootstrap=True, expected=0)
run_case('stable fallback stops on first failure', args=fallback_args,
         calls=fallback_calls[:1], release='v1.46.1', legacy_bootstrap=True,
         legacy_status=37, expected=37)
run_case('stable fallback preserves second failure', args=fallback_args,
         calls=fallback_calls, release='v1.46.1', legacy_bootstrap=True,
         opencode_status=42, expected=42)
# One composed bootstrap receives the selected bundle so it can preflight
# every product before mutation. This fixture checks acquisition/argv only;
# bootstrap_opencode_test.sh qualifies real candidate installation separately.
product_sets = []
for selection in ['claude','codex','opencode','gemini','codex,claude',
                  'opencode,claude','opencode,codex','opencode,codex,claude',
                  'claude,codex,opencode,gemini']:
    args = ['--products', selection]
    if any(p in selection.split(',') for p in ('opencode','gemini')):
        args += ['--desktop']
    routed = [args]
    product_sets.append((selection, routed))
    run_case('product set ' + selection, args=args, calls=routed, expected=0)
# Focused native Bash 3.2 coverage protects optional-channel parsing under
# nounset. Check every advertised set and omitted-channel forwarding with
# /bin/bash both as the loader interpreter and as fixture PATH's bash.
if sys.platform == 'darwin':
    native_bash = '/bin/bash'
    native_version = subprocess.run([native_bash, '-c', 'printf "%s" "$BASH_VERSION"'],
                                    text=True, capture_output=True, check=True, timeout=5).stdout
    if native_version.startswith('3.2.'):
        for selection, routed in product_sets:
            args = ['--products', selection]
            if any(p in selection.split(',') for p in ('opencode','gemini')):
                args += ['--desktop']
            run_case('native Bash 3.2 product set ' + selection,
                     args=args, calls=routed, expected=0, bash_executable=native_bash)
        for selection in ['opencode', 'claude,opencode']:
            run_case('native Bash 3.2 default channels ' + selection,
                     args=['--products', selection], calls=[['--products',selection]], expected=0,
                     bash_executable=native_bash)
    else:
        print('SKIP native Bash 3.2 fixtures: /bin/bash is ' + native_version)
for selection in ['opencode','gemini','claude,codex,opencode,gemini']:
    run_case('omitted observer defaults '+selection,
             args=['--products',selection],calls=[['--products',selection]],expected=0,piped=True)
run_case('explicit disabled observer flags survive the loader',
         args=['--products','opencode,gemini','--desktop=false','--webhook=false'],
         calls=[['--products','opencode,gemini','--desktop=false','--webhook=false']],expected=0)
run_case('scoped flags and equals selector',
         args=['--webhook', '--products=opencode,claude,codex,gemini', '--skip-agent-notify', '--desktop'],
         calls=[['--products','opencode,claude,codex,gemini','--skip-agent-notify','--webhook','--desktop']], expected=0, piped=True)
run_case('agent-notify scoped to legacy',
         args=['--products','claude,opencode','--agent-notify','--webhook'],
         calls=[['--products','claude,opencode','--agent-notify','--webhook']], expected=0)
run_case('composed bootstrap failure preserves status',
         args=['--products','claude,opencode','--desktop'],
         calls=[['--products','claude,opencode','--desktop']], legacy_status=37, expected=37)
for args in [
    ['--products'], ['--products', ''], ['--products='],
    ['--products', ',claude'], ['--products', 'claude,'], ['--products', 'claude,,codex'],
    ['--products', 'claude,claude'], ['--products', 'codex,codex'],
    ['--products', 'opencode,opencode', '--desktop'],
    ['--products','gemini,gemini','--webhook'],
    ['--products','gemini,unknown'],
    ['--products','gemini','--desktop','--skip-agent-notify'],
    ['--products', 'both'], ['--products', 'Claude'], ['--products', 'claude, codex'],
    ['--products', '*'], ['--products', 'claude,$(touch marker)'],
    ['--products', 'claude', '--products', 'codex'],
    ['--products=claude', '--products=codex'],
    ['--product', 'claude', '--products', 'codex'],
    ['--products', 'claude', '--product', 'codex'],
    ['--products', 'claude', '--product=codex'],
    ['--products', 'opencode,'],
    ['--products', 'claude', '--desktop'], ['--products', 'codex', '--webhook'],
    ['--products', 'opencode', '--desktop', '--skip-agent-notify'],
    ['--products', 'opencode', '--desktop', '--agent-notify'],
    ['--products', 'claude', '--skip-agent-notify', '--agent-notify'],
    ['--products', 'claude', '--agent-notify', '--agent-notify'],
    ['--products', 'opencode', '--desktop', '--desktop'],
    ['--products', 'opencode', '--webhook', '--webhook'],
    ['--products', 'claude', '--navigation', 'none'],
    ['--products', 'claude', '--codex-home', '/tmp/space path'],
    ['--products', 'claude', '--unknown'], ['--unknown', '--products', 'claude'],
    ['--products', '--desktop'], ['--products', 'claude', 'argument with spaces'],
]:
    run_case('early selector rejection ' + repr(args), args=args, no_network=True)
for args in [['--help', '--products', 'opencode'], ['--products=claude', '-h']]:
    run_case('new mode help ' + repr(args), args=args, help_only=True)
# Complete legacy selectors still forward literal metacharacters unchanged.
# No-selector unknown input belongs to the new pure-parse refusal boundary.
for args in [[], ['--help'], ['--product', 'claude'], ['--product', 'codex'],
             ['--product', 'opencode', '--desktop'],
             ['--product', 'claude', '--unknown', 'space path', '*', '$(touch marker)', '--products-not-a-selector']]:
    run_case('legacy passthrough ' + repr(args), args=args, expected=0)
for release in ['v0.99.0', 'v1.9.0', 'v1.43.0', 'v1.45.99']:
    run_case('reject unsupported OpenCode release ' + release,
             args=['--products', 'claude,opencode', '--desktop'], release=release, release_only=True)
for release in ['v1.46.0', 'v1.100.0', 'v2.0.0']:
    run_case('accept OpenCode release ' + release,
             args=['--products', 'opencode', '--desktop'], release=release,
             calls=[['--products', 'opencode', '--desktop']], expected=0)
run_case('legacy release behavior unchanged', args=['--product', 'opencode', '--desktop'],
         release='v1.43.0', expected=0)
for step in ['latest', 'commit', 'bootstrap']:
    run_case('multi-product failed download: ' + step,
             args=['--products', 'claude,opencode', '--desktop'], fail=step)

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
        channel_fixture(case, 'v1.43.0')
        (case / 'commit').write_text(sha)
        (case / 'bootstrap').write_text(bootstrap_stub)
        env = dict(os.environ, PATH=bash_path(case / 'bin') + ':' + runtime_path(case, python=True, node=True),
                   CASE_DIR=bash_path(case), RECORD_DIR=str(case), TMPDIR=bash_path(case / 'tmp space'),
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

# Reuse the native binary already built by each OS CI job. No host is launched;
# the lifecycle suite creates an isolated disposable profile.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) native_binary="$ROOT/bin/claude-notifications.exe" ;;
    *) native_binary="$ROOT/bin/claude-notifications" ;;
esac
if [ -f "$native_binary" ]; then
    bash "$ROOT/bin/bootstrap_opencode_test.sh" "$native_binary"
else
    echo "SKIP OpenCode bootstrap lifecycle: build the native CLI first."
fi
