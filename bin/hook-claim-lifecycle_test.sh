#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -eu
src=$(cd "$(dirname "$0")" && pwd)
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
test_env_setup "$root"
python3 - "${1:-$src/hook-wrapper.sh}" "$root" "${2:-all}" <<'PYTHON'
import concurrent.futures
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time

source, root, selected = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
if os.name == 'nt':
    print('SKIP: process-group kill lifecycle fixture requires POSIX')
    sys.exit(0)

def executable(path, text):
    path.write_text(text)
    path.chmod(0o700)

def fixture(name):
    base = root / name
    plugin = base / 'plugin'
    (plugin / 'bin').mkdir(parents=True)
    (plugin / '.claude-plugin').mkdir()
    shutil.copyfile(source, plugin / 'bin/hook-wrapper.sh')
    (plugin / '.claude-plugin/plugin.json').write_text('{"version":"1.42.0"}')
    executable(plugin / 'bin/install.sh', '''#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
printf 'attempt\\n' >> "$FIXTURE/count"
printf '%s\\n' "$$" > "$FIXTURE/installer-pid"
if [ "${BLOCK_INSTALL:-}" = 1 ]; then
    : > "$FIXTURE/install-started"
    while [ ! -e "$FIXTURE/install-release" ]; do sleep 0.01; done
fi
exit 7
''')
    tools = base / 'tools'
    tools.mkdir()
    # Command boundary pauses are externally controlled. No production hooks.
    executable(tools / 'rm', '''#!/bin/sh
pause=0
if [ "${PAUSE_RM:-}" = 1 ]; then
    for arg do case "$arg" in owner|*/active) pause=1;; esac; done
fi
if [ "$pause" = 1 ]; then
    : > "$FIXTURE/rm-entered"
    while [ ! -e "$FIXTURE/rm-release" ]; do sleep 0.01; done
fi
exec "$REAL_RM" "$@"
''')
    executable(tools / 'ln', '''#!/bin/sh
if [ "${PAUSE_LN:-}" = 1 ] || { [ "${PAUSE_WITNESS:-}" = 1 ] && [ "$2" = installer ]; }; then
    : > "$FIXTURE/ln-entered"
    while [ ! -e "$FIXTURE/ln-release" ]; do sleep 0.01; done
fi
exec "$REAL_LN" "$@"
''')
    executable(tools / 'ps', '''#!/bin/sh
[ "${FAIL_BIRTH:-}" != 1 ] || exit 1
exec "$REAL_PS" "$@"
''')
    env = dict(os.environ, FIXTURE=str(base), XDG_CACHE_HOME=str(base / 'cache'),
               CN_PRODUCT='codex', CI='1', PATH=str(tools)+':'+os.environ['PATH'],
               REAL_RM=shutil.which('rm'), REAL_LN=shutil.which('ln'), REAL_PS=shutil.which('ps'))
    os.utime(plugin / 'bin', (946684800, 946684800))
    return base, plugin, env

def start(f, **extra):
    base, plugin, env = f
    return subprocess.Popen(['/bin/sh', str(plugin / 'bin/hook-wrapper.sh'), 'handle-hook', 'Stop'],
                            env=dict(env, **extra), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            start_new_session=True)

def done(p):
    out, err = p.communicate(timeout=15)
    assert p.returncode == 0 and out == err == b'', (p.returncode, out, err)

def invoke(f, **extra):
    done(start(f, **extra))

def wait(path):
    deadline = time.monotonic() + 10
    while not path.exists():
        assert time.monotonic() < deadline, ('missing command boundary', path)
        time.sleep(.005)

def count(f):
    path = f[0] / 'count'
    return len(path.read_text().splitlines()) if path.exists() else 0

def active(f):
    entries = list((f[0] / 'cache/claude-notifications-go/codex').glob('install-backoff-*/active'))
    assert len(entries) == 1, entries
    return entries[0]

def owner(f):
    a = active(f)
    return a.parent / os.readlink(a) if a.is_symlink() else a

def expire(f):
    os.utime(owner(f), (946684800, 946684800))

def stop(p):
    os.killpg(p.pid, signal.SIGKILL)
    p.communicate(timeout=10)

def run(name, function):
    if selected in ('all', name):
        function()
        print('PASS:', name, flush=True)

# Red means a killed cleanup permanently suppresses later installation.
def killed_cleanup():
    f = fixture('killed-cleanup')
    invoke(f)
    expire(f)
    p = start(f, PAUSE_RM='1')
    wait(f[0] / 'rm-entered')
    stop(p)
    expire(f)
    invoke(f)
    assert count(f) == 2, ('cleanup crash wedged retry', count(f))
run('killed-cleanup', killed_cleanup)

# Red means killed empty initialization sticks, or starts unclaimed work.
def killed_initialization():
    f = fixture('killed-initialization')
    p = start(f, PAUSE_LN='1')
    wait(f[0] / 'ln-entered')
    assert count(f) == 0
    stop(p)
    invoke(f)
    assert count(f) == 1
run('killed-initialization', killed_initialization)

# Red means an unrelated recycled live PID suppresses retries indefinitely.
def recycled_pid():
    f = fixture('recycled-pid')
    invoke(f)
    o = owner(f)
    (o / 'failed').unlink()
    if (o / 'owner').exists():
        nonce = (o / 'owner').read_text().splitlines()[-1]
        (o / 'owner').write_text(f'{os.getpid()}\nThu Jan 1 00:00:00 1970\n{nonce}\n')
    else:
        (o / 'pid').write_text(str(os.getpid())+'\n')
    expire(f)
    invoke(f)
    assert count(f) == 2, ('recycled PID blocked retry', count(f))
run('recycled-pid', recycled_pid)

# Red means a long-running installer is stolen after its cooldown expires.
def live_owner():
    for fail_birth in ('0', '1'):
        f = fixture('live-owner-'+fail_birth)
        p = start(f, BLOCK_INSTALL='1', FAIL_BIRTH=fail_birth)
        wait(f[0] / 'install-started')
        expire(f)
        invoke(f, FAIL_BIRTH=fail_birth)
        assert count(f) == 1, ('live installer stolen', fail_birth, count(f))
        (f[0] / 'install-release').touch()
        done(p)
run('live-owner', live_owner)

# Red means a dead wrapper lets a still-live installer lose ownership.
def parent_death():
    for fail_birth in ('0', '1'):
        f = fixture('parent-death-'+fail_birth)
        p = start(f, BLOCK_INSTALL='1', FAIL_BIRTH=fail_birth)
        wait(f[0] / 'install-started')
        child_pid = int((f[0] / 'installer-pid').read_text())
        assert child_pid != p.pid
        os.kill(p.pid, signal.SIGKILL)
        p.communicate(timeout=10)
        os.kill(child_pid, 0)
        try:
            expire(f)
            invoke(f, FAIL_BIRTH=fail_birth)
            assert count(f) == 1, ('surviving installer stolen', count(f))
        finally:
            (f[0] / 'install-release').touch()
            try:
                os.killpg(p.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
run('parent-death', parent_death)


# Red means cleanup of an old inode deletes a newer claim or failure marker.
def competing_reapers():
    f = fixture('competing-reapers')
    invoke(f)
    expire(f)
    first = start(f, PAUSE_RM='1')
    wait(f[0] / 'rm-entered')
    expire(f)
    winner = start(f, BLOCK_INSTALL='1')
    wait(f[0] / 'install-started')
    record = (owner(f) / 'owner').read_bytes()
    marker = owner(f) / 'failed'
    marker.write_text('successor-marker')
    (f[0] / 'rm-release').touch()
    done(first)
    assert (owner(f) / 'owner').read_bytes() == record
    assert marker.read_text() == 'successor-marker'
    assert count(f) == 2, ('old reaper started duplicate install', count(f))
    marker.unlink()
    (f[0] / 'install-release').touch()
    done(winner)
run('competing-reapers', competing_reapers)

# Red means losing empty publication admits duplicate work or deletes winner.
def lost_publication():
    f = fixture('lost-publication')
    first = start(f, PAUSE_LN='1')
    wait(f[0] / 'ln-entered')
    winner = start(f, BLOCK_INSTALL='1')
    wait(f[0] / 'install-started')
    before = (owner(f) / 'owner').read_bytes()
    (f[0] / 'ln-release').touch()
    done(first)
    assert count(f) == 1
    assert (owner(f) / 'owner').read_bytes() == before
    (f[0] / 'install-release').touch()
    done(winner)
run('lost-publication', lost_publication)

# Red means a child publishes after its dead parent was reaped and starts an
# installer without protecting the original inode or loses a new live owner.
def late_witness():
    f = fixture('late-witness')
    first = start(f, PAUSE_WITNESS='1')
    wait(f[0] / 'ln-entered')
    assert count(f) == 0
    os.kill(first.pid, signal.SIGKILL)
    first.communicate(timeout=10)
    expire(f)
    winner = start(f, BLOCK_INSTALL='1')
    try:
        wait(f[0] / 'install-started')
        before = (owner(f) / 'owner').read_bytes()
        (f[0] / 'ln-release').touch()
        time.sleep(.1)
        assert count(f) == 1
        assert (owner(f) / 'owner').read_bytes() == before
    finally:
        (f[0] / 'ln-release').touch()
        (f[0] / 'install-release').touch()
        done(winner)
run('late-witness', late_witness)


# Red means cache failure or unknown ownership starts physical installation,
# or drops dispatch of an otherwise usable retained sender.
def unavailable_claim():
    for kind in ('cache-file', 'unknown-active', 'legacy-link'):
        f = fixture('unavailable-'+kind)
        executable(f[1] / 'bin/claude-notifications', "#!/bin/sh\nif [ \"$1\" = version ]; then echo 1.41.0; else echo delivered >> \"$FIXTURE/delivered\"; fi\n")
        os.utime(f[1] / 'bin', (946684800, 946684800))
        if kind == 'cache-file':
            (f[0] / 'cache').write_text('blocked')
        else:
            key = subprocess.check_output(['cksum'], input=str(f[1] / 'bin').encode()).decode().split()[0]
            directory = f[0] / f'cache/claude-notifications-go/install-backoff-1.42.0-{key}'
            directory.mkdir(parents=True)
            if kind == 'unknown-active':
                (directory / 'active').write_text('foreign')
            else:
                (directory / 'active').symlink_to('attempt.legacy')
        invoke(f, CN_PRODUCT='claude')
        assert count(f) == 0, ('unclaimed installer', kind)
        assert (f[0] / 'delivered').read_text() == 'delivered\n'
run('unavailable-claim', unavailable_claim)


# One real risk boundary, repeated with independent fresh filesystem state.
def stress():
    for i in range(100):
        f = fixture('stress-'+str(i))
        invoke(f)
        expire(f)
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            list(pool.map(lambda _: invoke(f), range(2)))
        assert count(f) == 2, ('duplicate stale takeover', i, count(f))
run('stress', stress)
PYTHON
