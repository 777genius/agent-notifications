#!/bin/bash
# Frozen Pi product routing only. Actual installation and native Pi delivery are
# qualified by the product integration fixture, not by these argv adapters.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
# A parent fixture may expose shell-script PATH adapters. Python on Windows
# needs the genuine executable of this running Bash, not a rediscovered wrapper.
TEST_PI_BASH=$BASH
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SANDBOX=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-pi-TEST-XXXXXX")
trap 'rm -rf "$SANDBOX"' EXIT
test_env_setup "$SANDBOX"
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$SANDBOX/functions.sh"
export TEST_PI_FUNCTIONS="$SANDBOX/functions.sh" TEST_PI_HELPER="$SANDBOX/helper"
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        [ ! -f "$TEST_PI_BASH.exe" ] || TEST_PI_BASH="$TEST_PI_BASH.exe"
        TEST_PI_BASH=$(cygpath -w "$TEST_PI_BASH") ;;
esac
export TEST_PI_BASH
cat > "$TEST_PI_HELPER" <<'PI_HELPER'
#!/bin/bash
set -euo pipefail
trace=$(mktemp "$TEST_PI_CASE/call-XXXXXX")
printf '%s\0' "$@" > "$trace"
case "$1 ${2:-}" in
    'setup-products capabilities')
        printf 'setup-products-v1 claude codex opencode gemini qoder%s\n' "${TEST_PI_CAP_SUFFIX- pi}" ;;
    'setup-products features') printf 'terminal-selector-v1\n' ;;
    'setup-products select') printf 'pi\n' ;;
    'setup-products channels') printf 'desktop\n' ;;
    'setup-products prepare'|'setup-products confirm')
        operation=$2
        while [ "$#" -gt 0 ]; do
            if [ "$1" = --intent-file ]; then
                if [[ "$2" = *selector-pi-runtime-intent.json ]] && [ "${TEST_PI_CANCEL:-false}" = true ]; then exit 0; fi
                printf 'TEST immutable checkpoint' > "$2"
                break
            fi
            shift
        done
        if [ "$operation" = confirm ]; then printf 'approved\n'; else printf 'prepared\n'; fi ;;
    'setup-products intent-args')
        if [[ "$4" = *selector-pi-runtime-intent.json ]] && [ -n "${TEST_PI_REFRESH_ATOMS:-}" ]; then
            cat "$TEST_PI_REFRESH_ATOMS"
        else cat "$TEST_PI_ATOMS"; fi ;;
    'config path')
        while IFS= read -r -d '' key && IFS= read -r -d '' value; do
            if [ "$key" = global-config ]; then printf '%s\n' "$value"; exit 0; fi
        done < "$TEST_PI_ATOMS"
        exit 97 ;;
    'config installer')
        [ "$#" -eq 4 ] && [ "$3" = runtime-root ] && [ -f "$TEST_PI_CASE/runtime-installed" ] || exit 1
        printf '%s\n' "$TEST_PI_RUNTIME_ROOT" ;;
    'internal-install-runtime --stage')
        # Inert writer boundary: the genuine shell decides whether to call it.
        # Canonical asset bytes/ownership are covered by the native Go test.
        printf '%s\0' "$@" >> "$TEST_PI_CASE/runtime-writes"
        printf '\0' >> "$TEST_PI_CASE/runtime-writes"
        [ "${TEST_PI_RUNTIME_EXIT:-0}" -eq 0 ] || exit "$TEST_PI_RUNTIME_EXIT"
        shift
        target= entry=
        while [ "$#" -gt 0 ]; do
            case "$1" in
                --target) target=$2; shift 2 ;;
                --entry) entry=$2; shift 2 ;;
                *) shift ;;
            esac
        done
        [ -n "$target" ] && [ -n "$entry" ] || exit 97
        mkdir -p "$target"
        cp "$TEST_PI_HELPER" "$target/$entry"
        touch "$TEST_PI_CASE/runtime-installed" ;;
    'setup-products preflight') exit "${TEST_PI_PREFLIGHT_EXIT:-0}" ;;
    'setup-notifications --help') printf '%s\n' '--policy-only --preserve-enabled' ;;
    'setup-notifications wizard')
        if [ "$4" = inspect ]; then printf '{"targets":[]}\n'; else exit "${TEST_PI_WIZARD_EXIT:-0}"; fi ;;
    *) exit 97 ;;
esac
PI_HELPER
chmod +x "$TEST_PI_HELPER"
cat > "$SANDBOX/runner" <<'PI_MAIN'
#!/bin/bash
source "$TEST_PI_FUNCTIONS"
abort_if_wsl_environment() { :; }; detect_platform() { :; }; require_installer_runtime() { :; }
check_prerequisites() { :; }; print_header() { :; }; report_progress() { :; }; bootstrap_promotion() { :; }
resolve_bootstrap_release() { BOOTSTRAP_TAG=v2.0.0; }
stage_config_helper() { _CONFIG_STAGE="$TEST_PI_CASE/stage"; mkdir "$_CONFIG_STAGE"; _CONFIG_HELPER=$TEST_PI_HELPER; }
run_setup_stage() { shift; "$@"; }
acquire_wizard_portable_asset() { WIZARD_PACKAGE_ROOT="$TEST_PI_CASE/TEST package with spaces.zip"; }
prepare_managed_runtime() {
    [ "$#" -eq 2 ] && [ "$1" = --consumer ] && [ "$2" = pi-runtime-preparation ] || return 98
    printf 'runtime\n' >> "$TEST_PI_CASE/stages"
    _MANAGED_RUNTIME_PREPARED=${TEST_PI_PREPARED:-false}
}
install_legacy_products() { printf 'legacy\n' >> "$TEST_PI_CASE/stages"; }
install_gemini() { printf 'gemini\n' >> "$TEST_PI_CASE/stages"; }
prepare_cursor_runtime() { printf 'cursor-runtime\n' >> "$TEST_PI_CASE/stages"; _CURSOR_RUNTIME_PREPARED=false; }
stage_historical_baselines() { :; }; config_preflight() { :; }
configure_agent_policy() { printf 'unexpected-policy\n' >> "$TEST_PI_CASE/stages"; return 99; }
bootstrap_abs_command() { printf 'unexpected-discovery\n' >> "$TEST_PI_CASE/stages"; return 99; }
main "$@"
PI_MAIN
cat > "$SANDBOX/runtime-runner" <<'PI_RUNTIME'
#!/bin/bash
set -euo pipefail
source "$TEST_PI_FUNCTIONS"
bootstrap_release_os_arch() { printf 'linux amd64\n'; }
_SELECTOR_INTENT="$TEST_PI_CASE/intent.json"
_FROZEN_CONTROL="$TEST_PI_CASE/control"
_FROZEN_RUNTIME="$TEST_PI_RUNTIME_ROOT"
_FROZEN_CURSOR_SEED=${TEST_PI_CURSOR_SEED:-false}
_CONFIG_STAGE="$TEST_PI_CASE/stage"
_CONFIG_HELPER=$TEST_PI_HELPER
mkdir "$_CONFIG_STAGE"
# Exercise the real Cursor then Pi preparation against one shared binary.
prepare_cursor_runtime
[ "$_CURSOR_RUNTIME_PREPARED" = true ]
prepare_cursor_runtime
[ "$_CURSOR_RUNTIME_PREPARED" = false ]
export TEST_PI_RUNTIME_EXIT=${TEST_PI_FAIL_ON_PI:-0}
if prepare_managed_runtime "$@"; then status=0; else status=$?; fi
printf '%s\n' "$_MANAGED_RUNTIME_PREPARED" > "$TEST_PI_CASE/prepared"
[ "$status" -eq 0 ] || exit "$status"
if [ "${TEST_PI_RUNTIME_REPEAT:-false}" = true ]; then
    prepare_managed_runtime "$@"
    printf '%s\n' "$_MANAGED_RUNTIME_PREPARED" >> "$TEST_PI_CASE/prepared"
fi
PI_RUNTIME
python3 -I - "$SANDBOX" <<'PY_PI'
import os, pathlib, subprocess, sys
lab = pathlib.Path(sys.argv[1])
bash = os.environ['TEST_PI_BASH']
profile = str(lab / "TEST Pi profile ' $literal")
vendor = str(lab / 'TEST Pi executable with spaces')
control = str(lab / 'TEST control' / 'agent-notifications')
runtime = str(lab / 'TEST runtime')
policy = str(lab / 'TEST shared config.json')
base = [('home', str(lab / 'TEST home')), ('control-root', control), ('runtime-root', runtime),
        ('global-config', policy), ('pi-agent-dir', profile), ('pi-executable', vendor),
        ('pi-desktop', 'true'), ('pi-webhook', 'false'), ('pi-channel-preimage', 'absent')]
def encode(rows): return b''.join(k.encode()+b'\0'+v.encode()+b'\0' for k,v in rows)
def run(args, changes=None, rows=None, raw=None, refresh=None, tty=False):
    case = lab / ('case-'+str(len(list(lab.glob('case-*')))))
    case.mkdir()
    atoms = case/'atoms'; atoms.write_bytes(raw if raw is not None else encode(rows or base))
    env = dict(os.environ, TEST_PI_CASE=str(case), TEST_PI_ATOMS=str(atoms), **(changes or {}))
    if refresh is not None:
        refreshed=case/'refresh-atoms'; refreshed.write_bytes(encode(refresh))
        env['TEST_PI_REFRESH_ATOMS']=str(refreshed)
    options = {}
    if tty:
        import fcntl, termios
        master, slave = os.openpty()
        def terminal():
            os.setsid(); fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        options = dict(stdin=slave, preexec_fn=terminal)
    proc = subprocess.Popen([bash, str(lab/'runner')]+args, env=env, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, **options)
    if tty: os.close(slave)
    try: output, _ = proc.communicate(timeout=20)
    finally:
        if proc.poll() is None: proc.kill(); proc.communicate()
        if tty: os.close(master)
    calls = [p.read_bytes().split(b'\0')[:-1] for p in sorted(case.glob('call-*'))]
    stages = (case/'stages').read_text().splitlines() if (case/'stages').exists() else []
    return proc.returncode, calls, stages, output.decode(errors='replace')
def matching(calls, prefix): return [c for c in calls if c[:len(prefix)] == [s.encode() for s in prefix]]
def value(args, key): return args[args.index(key.encode())+1]
for selector in (['--product','pi'], ['--products','pi']):
    for desktop, webhook in ((True,False),(False,True),(True,True),(False,False)):
        rows=[(k, str(desktop).lower() if k=='pi-desktop' else str(webhook).lower() if k=='pi-webhook' else v) for k,v in base]
        code,calls,stages,out=run(selector+['--pi-agent-dir',profile,'--pi-executable',vendor,
                                         '--desktop='+str(desktop).lower(),'--webhook='+str(webhook).lower()], rows=rows)
        assert code==0, out
        prepare=matching(calls,['setup-products','prepare'])[0]
        assert value(prepare,'--pi-agent-dir')==profile.encode() and value(prepare,'--pi-executable')==vendor.encode()
        install=matching(calls,['setup-notifications','wizard','--action','install'])[0]
        inspect=matching(calls,['setup-notifications','wizard','--action','inspect'])[0]
        for args in (install,inspect):
            for key,expected in (('--agents','pi'),('--pi-agent-dir',profile),('--pi-executable',vendor),
                                 ('--control-root',control),('--runtime-root',runtime),('--global-config',policy)):
                assert value(args,key)==expected.encode(), (key,args)
            assert not any(k.encode() in args for k in ('--helper','--mcp-config','--claude-config','--codex-home','--plugin-root','--client-executable'))
        assert value(install,'--hooks')==b'false' and value(install,'--agent-notify')==b'true'
        for key,want in (('pi-desktop',desktop),('pi-webhook',webhook)):
            assert ('--'+key+'='+str(want).lower()).encode() in install
        assert b'--pi-channel-preimage=absent' in install and stages==['runtime'], (stages,out)
# A fresh managed runtime advances generation; route through a new Pi-only checkpoint.
code,calls,stages,out=run(['--product','pi'], {'TEST_PI_PREPARED':'true'})
assert code==0 and len(matching(calls,['setup-products','prepare']))==2, out
install=matching(calls,['setup-notifications','wizard','--action','install'])[0]
assert value(install,'--bootstrap-intent-file').endswith(b'selector-pi-runtime-intent.json'), install
# Existing selected stages keep their order; Pi then receives its own projection.
for product in ('claude','gemini'):
    extra=([('claude-config',str(lab/'TEST Claude profile')),('claude-mcp-config',str(lab/'TEST Claude MCP.json')),
            ('claude-executable',str(lab/'TEST Claude executable'))] if product=='claude' else
           [('gemini-config-root',str(lab/'TEST Gemini profile')),('gemini-executable',str(lab/'TEST Gemini executable')),
            ('gemini-desktop','true'),('gemini-webhook','false'),('gemini-channel-preimage','absent')])
    code,calls,stages,out=run(['--products',product+',pi','--pi-agent-dir',profile,'--pi-executable',vendor],
                             rows=base+extra, refresh=base)
    assert code==0 and stages==[('legacy' if product=='claude' else product),'runtime'], (stages,out)
    install=matching(calls,['setup-notifications','wizard','--action','install'])[0]
    assert value(install,'--agents')==b'pi' and value(install,'--bootstrap-intent-file').endswith(b'selector-pi-runtime-intent.json'), install
# Cursor finishes against the parent before Pi refreshes its own confirmation.
cursor_rows=[('scope-root',str(lab/'TEST Cursor profile')),('client-executable',str(lab/'TEST Cursor executable')),
             ('cursor-desktop','true'),('cursor-webhook','false'),('cursor-channel-preimage','absent'),
             ('cursor-seed-enabled','true')]
code,calls,stages,out=run(['--products','cursor,pi','--desktop'],
                         {'TEST_PI_CAP_SUFFIX':' cursor pi'}, rows=base+cursor_rows, refresh=base)
assert code==0 and stages==['cursor-runtime','runtime'], (stages,out)
installs=matching(calls,['setup-notifications','wizard','--action','install'])
by_agent={value(args,'--agents'):args for args in installs}
assert len(installs)==2 and set(by_agent)=={b'cursor',b'pi'}, (installs,out)
assert value(by_agent[b'cursor'],'--bootstrap-intent-file').endswith(b'selector-intent.json'), by_agent[b'cursor']
assert value(by_agent[b'pi'],'--bootstrap-intent-file').endswith(b'selector-pi-runtime-intent.json'), by_agent[b'pi']
# Preserve observed Pi channel/preimage authority during refresh, including updates.
digest='a'*64
updated=[(k, digest if k=='pi-channel-preimage' else v) for k,v in base]
code,calls,_,out=run(['--product','pi'], rows=updated)
assert code==0 and ('--pi-channel-preimage='+digest).encode() in matching(calls,['setup-notifications','wizard','--action','install'])[0], out
drift=[(k, 'true' if k=='pi-webhook' else v) for k,v in base]
code,calls,_,out=run(['--product','pi'], {'TEST_PI_PREPARED':'true'}, refresh=drift)
assert code!=0 and not matching(calls,['setup-notifications','wizard']), out
# Unknown, duplicate, relative, malformed or missing frozen facts stop before runtime preparation.
valid=encode(base)
for raw in (valid+b'pi-agent-dir\0'+profile.encode()+b'\0', valid+b'invented-key\0/TEST\0',
            valid.replace(vendor.encode(),b'relative'), valid.replace(b'pi-channel-preimage\0absent\0',b''),
            valid.replace(b'pi-desktop\0true\0',b'pi-desktop\0maybe\0'), valid[:-1]):
    code,calls,stages,out=run(['--product','pi'], raw=raw)
    assert code!=0 and not stages and not matching(calls,['setup-notifications','wizard']), out
for args in (['--product','pi','--agent-notify'], ['--product','pi','--pi-agent-dir','relative'],
             ['--product','claude','--pi-agent-dir',profile], ['--products','pi,pi']):
    code,calls,stages,out=run(args)
    assert code!=0 and not calls and not stages, out
code,calls,stages,out=run(['--product','pi'], {'TEST_PI_CAP_SUFFIX':''})
assert code!=0 and not stages and not matching(calls,['setup-notifications','wizard']), out
for changes in ({'TEST_PI_PREFLIGHT_EXIT':'73'}, {'TEST_PI_WIZARD_EXIT':'77'}):
    code,calls,_,out=run(['--product','pi'], changes)
    assert code in (73,77) and not matching(calls,['setup-notifications','wizard','--action','inspect']), out
if os.name!='nt':
    code,calls,stages,out=run([], {'TEST_PI_PREPARED':'true','TEST_PI_CANCEL':'true'}, tty=True)
    assert code==0 and stages==['runtime'] and not matching(calls,['setup-notifications','wizard']), out
print('PASS Pi frozen bootstrap routing, channel preservation, refreshed intent and refusal boundaries (composition only)')

def runtime_request(args, **changes):
    case = lab / ('runtime-'+str(len(list(lab.glob('runtime-*')))))
    case.mkdir()
    env = dict(os.environ, TEST_PI_CASE=str(case), TEST_PI_RUNTIME_ROOT=str(case/'runtime'), **changes)
    proc = subprocess.run([bash, str(lab/'runtime-runner'), *args], env=env,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
    trace = case/'runtime-writes'
    writes = [row.split(b'\0') for row in trace.read_bytes().split(b'\0\0') if row] if trace.exists() else []
    prepared = (case/'prepared').read_text().splitlines() if (case/'prepared').exists() else []
    return proc.returncode, writes, prepared, proc.stdout

pi_preparation=['--consumer','pi-runtime-preparation']
for seeded in ('false','true'):
    for repeated in ('false','true'):
        code,writes,prepared,out=runtime_request(pi_preparation, TEST_PI_CURSOR_SEED=seeded,
                                                TEST_PI_RUNTIME_REPEAT=repeated)
        assert code==0 and len(writes)==(3 if repeated=='true' else 2), (code,writes,prepared,out)
        assert (b'--seed-enabled-if-absent' in writes[0]) == (seeded=='true'), writes
        assert all(value(call,'--consumer')==b'pi-runtime-preparation' for call in writes[1:]), writes
        assert prepared==(['true','true'] if repeated=='true' else ['true']), prepared
# An unsuccessful Pi writer must not be reported as a prepared runtime.
code,writes,prepared,out=runtime_request(pi_preparation, TEST_PI_FAIL_ON_PI='73')
assert code!=0 and len(writes)==2 and prepared==['false'], (code,writes,prepared,out)
# The finite private call contract is checked before the matching-byte shortcut.
for args in (['--consumer','foreign-consumer'], pi_preparation+['--seed-enabled-if-absent'],
             ['--seed-enabled-if-absent','extra'], ['--consumer=pi-runtime-preparation']):
    code,writes,prepared,out=runtime_request(args)
    assert code!=0 and len(writes)==1 and prepared==['false'], (args,code,writes,prepared,out)
print('PASS genuine shared-runtime Cursor reuse, Pi writer admission/revalidation and refusal boundaries (composition only)')
PY_PI
