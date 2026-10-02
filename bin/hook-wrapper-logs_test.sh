#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -eu
src=$(cd "$(dirname "$0")" && pwd)
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
test_env_setup "$root"
python3 - "$src/hook-wrapper.sh" "$root" "$BASH" <<'PY'
import concurrent.futures
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

source, root = Path(sys.argv[1]), Path(sys.argv[2])
shell = sys.argv[3]
env = dict(os.environ, ROOT=root.as_posix(), CN_PRODUCT='claude')
version = '1.42.0'
stub = root / 'stubs'
stub.mkdir()
(stub / 'uname').write_text('#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n', newline='\n')
(stub / 'uname').chmod(0o700)


def fixture(name, installer):
    plugin = root / name
    (plugin / 'bin').mkdir(parents=True)
    (plugin / '.claude-plugin').mkdir()
    shutil.copyfile(source, plugin / 'bin/hook-wrapper.sh')
    (plugin / '.claude-plugin/plugin.json').write_text(json.dumps({'version': version}))
    script = plugin / 'bin/install.sh'
    script.write_text('#!/bin/sh\n# agent-notifications-managed-writer-protocol-v1\n' + installer, newline='\n')
    script.chmod(0o700)
    return plugin


def invoke(plugin, cache, product='claude', extra=None):
    run_env = dict(env, XDG_CACHE_HOME=cache.as_posix(), CN_PRODUCT=product)
    if extra:
        run_env.update(extra)
    # A native Windows Python passes ROOT as D:/...; convert it before putting
    # it in POSIX PATH, where the drive colon would split the stub directory.
    command = '''fixture_root="$ROOT"
if command -v cygpath >/dev/null 2>&1; then fixture_root=$(cygpath -u "$ROOT"); fi
export PATH="$fixture_root/stubs:$PATH"
test "$(uname -s)" = Linux || { echo "fixture uname stub is unavailable" >&2; exit 97; }
unset OS
exec /bin/sh "$1" handle-hook Stop'''
    # Git Bash starts several real child tools for this diagnostics fixture.
    # This watchdog bounds the scenario, not production hook latency.
    result = subprocess.run([shell, '-c', command,
                             'fixture', (plugin / 'bin/hook-wrapper.sh').as_posix()],
                            env=run_env, capture_output=True, timeout=60 if os.name == 'nt' else 15)
    assert result.returncode == 0, result
    return result


def logs(cache, product='claude'):
    directory = cache / 'claude-notifications-go'
    if product == 'codex':
        directory /= 'codex'
    return sorted(p for p in directory.glob('install-*') if p.is_file())


# Lost diagnostics, ANSI leaks, bad JSON escaping or unsafe log modes make this red.
reason = b'progress\n\x1b[31mError: managed fingerprint "changed" at C:\\cache\tSKILL.md\x1b[0m\x01\x1b]0;secret-title\x07\n'
(root / 'reason.txt').write_bytes(reason)
plugin = fixture('failed', 'cat "$ROOT/reason.txt"; exit 7\n')
cache = root / ('cache with spaces' if os.name == 'nt' else 'cache "quoted"\\path\nline\t\x01')
failed = invoke(plugin, cache)
message = json.loads(failed.stdout)['systemMessage']
assert not failed.stderr
assert 'managed fingerprint "changed" at C:\\cache\tSKILL.md' in message, message
assert '\x1b' not in message and 'secret-title' not in message
saved = logs(cache)
assert len(saved) == 1 and saved[0].read_bytes() == reason
assert os.name == 'nt' or saved[0].stat().st_mode & 0o777 == 0o600
assert 'Details: ' + saved[0].as_posix() in message
# Same-root hooks inside the cooldown neither install nor create another log.
repeated = invoke(plugin, cache)
assert repeated.stdout == repeated.stderr == b''
assert logs(cache) == saved
# Expiring the real failed claim permits a new independent attempt/log, while
# the existing failure stamp still suppresses a repeated notification.
claims = list((cache / 'claude-notifications-go').glob('install-backoff-*/active'))
assert len(claims) == 1 and claims[0].is_symlink()
owner = claims[0].parent / os.readlink(claims[0])
assert (owner / 'failed').is_file()
os.utime(owner, (946684800, 946684800))
retried = invoke(plugin, cache)
assert retried.stdout == retried.stderr == b''
assert len(logs(cache)) == 2
assert all(log.read_bytes() == reason for log in logs(cache))
assert all(os.name == 'nt' or log.stat().st_mode & 0o777 == 0o600 for log in logs(cache))

# Cutting off the OSC opener before parsing must not expose its hidden payload.
(root / 'reason.txt').write_bytes(b'Error: real failure\n\x1b]0;' + b'hidden title line\n' * 24 +
                                b'Error: HIDDEN_OSC_PAYLOAD\x07\ncleanup complete\n')
osc_message = json.loads(invoke(plugin, root / 'osc-cache').stdout)['systemMessage']
assert 'Error: real failure' in osc_message and 'HIDDEN_OSC_PAYLOAD' not in osc_message

# DCS/SOS/PM/APC strings end with ST, never BEL. Their hidden payload must
# not replace a real error, even across newlines or repeated ESC bytes.
strings = b'Error: real failure\n'
for introducer in (b'P', b'X', b'^', b'_'):
    strings += b'\x1b' + introducer + b'hidden\x07\nError: HIDDEN_STRING_PAYLOAD\x1b\x1b\\\n'
(root / 'reason.txt').write_bytes(strings)
string_message = json.loads(invoke(plugin, root / 'string-cache').stdout)['systemMessage']
assert 'Error: real failure' in string_message and 'HIDDEN_STRING_PAYLOAD' not in string_message

# An oversized UTF-8 reason must stay bounded and decodable, without splitting a character.
(root / 'reason.txt').write_text('Error: ' + 'я' * 350 + '\n', encoding='utf-8')
long_result = invoke(plugin, root / 'long-cache')
excerpt = json.loads(long_result.stdout)['systemMessage'].split('\n')[1]
assert excerpt.startswith('Error: ') and len(excerpt) <= 300
assert len(excerpt.encode()) <= 300

# Non-UTF-8 filesystem/installer bytes must not invalidate Claude hook JSON.
(root / 'reason.txt').write_bytes(b'Error: malformed \xff\xc0\xaf\xed\xa0\x80\xf4\x90\x80\x80\xe2\x82\n')
invalid_result = invoke(plugin, root / 'invalid-utf8-cache')
invalid_message = json.loads(invalid_result.stdout)['systemMessage']
assert 'Error: malformed ' in invalid_message and '\ufffd' in invalid_message
assert logs(root / 'invalid-utf8-cache')[0].read_bytes() == (root / 'reason.txt').read_bytes()

# Codex must retain the actual failure while leaving both streams empty.
codex = invoke(plugin, root / 'codex-cache', 'codex')
assert codex.stdout == codex.stderr == b''
assert len(logs(root / 'codex-cache', 'codex')) == 1

# Simultaneous same-root hooks elect one installer and keep one complete log.
parallel = fixture('parallel', 'printf "begin:%s\\n" "$$"; sleep 0.1; printf "Error: attempt:%s\\n" "$$"; exit 7\n')
parallel_cache = root / 'parallel-cache'
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    futures = [pool.submit(invoke, parallel, parallel_cache) for _ in range(2)]
    results = [f.result() for f in futures]
assert sum(bool(r.stdout) for r in results) == 1
assert all(not r.stderr for r in results)
attempts = logs(parallel_cache)
assert len(attempts) == 1
first, last = attempts[0].read_text().splitlines()
assert last == 'Error: attempt:' + first.split(':')[1]
assert os.name == 'nt' or attempts[0].stat().st_mode & 0o777 == 0o600

# Separate roots permit simultaneous independent attempts for the same version.
# Their complete private logs remain distinct; notification suppression is atomic.
parallel_other = fixture('parallel-other', 'printf "begin:%s\\n" "$$"; sleep 0.1; printf "Error: attempt:%s\\n" "$$"; exit 7\n')
independent_cache = root / 'independent-cache'
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    futures = [pool.submit(invoke, target, independent_cache) for target in (parallel, parallel_other)]
    results = [f.result() for f in futures]
assert sum(bool(r.stdout) for r in results) == 1
assert all(not r.stderr for r in results)
attempts = logs(independent_cache)
assert len(attempts) == 2 and attempts[0].name != attempts[1].name
pids = []
for log in attempts:
    assert os.name == 'nt' or log.stat().st_mode & 0o777 == 0o600
    first, last = log.read_text().splitlines()
    pid = first.split(':')[1]
    assert last == 'Error: attempt:' + pid
    pids.append(pid)
assert len(set(pids)) == 2

# Success removes only its own log and resets notification suppression after recovery.
script = plugin / 'bin/install.sh'
repair_script = '''#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
cat > "$INSTALL_TARGET_DIR/claude-notifications" <<'BIN'
#!/bin/sh
if [ "$1" = version ]; then echo 1.42.0; fi
BIN
chmod +x "$INSTALL_TARGET_DIR/claude-notifications"
echo repaired
'''
script.write_text(repair_script, newline='\n')
# Hook repair remains suppressed until expiry; explicit manual installation
# bypasses that claim immediately. The next successful hook clears suppression.
still_suppressed = invoke(plugin, cache)
assert still_suppressed.stdout == still_suppressed.stderr == b''
assert not (plugin / 'bin/claude-notifications').exists()
assert len(logs(cache)) == 2
manual = subprocess.run([shell, str(script)],
                        env=dict(env, INSTALL_TARGET_DIR=str(plugin / 'bin'), XDG_CACHE_HOME=str(cache)),
                        capture_output=True, timeout=15)
assert manual.returncode == 0 and manual.stdout == b'repaired\n' and not manual.stderr, manual
repaired = invoke(plugin, cache)
assert repaired.stdout == repaired.stderr == b''
assert len(logs(cache)) == 2
assert not (cache / 'claude-notifications-go/install-failed-1.42.0').exists(), (repaired, (plugin / 'bin/claude-notifications').read_bytes())
assert not claims[0].is_symlink()
(plugin / 'bin/claude-notifications').unlink()
script.write_text('#!/bin/sh\n# agent-notifications-managed-writer-protocol-v1\necho "Error: failed again"; exit 9\n', newline='\n')
assert 'failed again' in json.loads(invoke(plugin, cache).stdout)['systemMessage']
assert len(logs(cache)) == 3
# A later successful hook installation removes its own temporary log and keeps
# every complete failure log. Recovery clears both the claim and failure stamp.
failure_logs = {log: log.read_bytes() for log in logs(cache)}
owner = claims[0].parent / os.readlink(claims[0])
assert (owner / 'failed').is_file()
os.utime(owner, (946684800, 946684800))
script.write_text(repair_script, newline='\n')
recovered = invoke(plugin, cache)
assert recovered.stdout == recovered.stderr == b''
assert {log: log.read_bytes() for log in logs(cache)} == failure_logs
assert not claims[0].is_symlink()
assert not (cache / 'claude-notifications-go/install-failed-1.42.0').exists()

# A broken cache or mktemp must never stop the installer or expose its raw output.
no_log = fixture('no-log', 'echo ran >> "$ROOT/ran"; echo "Error: raw installer output"; exit 8\n')
unusable = root / 'not-a-directory'
unusable.write_text('occupied')
fallback = invoke(no_log, unusable)
assert (root / 'ran').read_text() == 'ran\n'
assert not fallback.stderr
assert 'status 8' in json.loads(fallback.stdout)['systemMessage']
(stub / 'mktemp').write_text('#!/bin/sh\nexit 1\n', newline='\n')
(stub / 'mktemp').chmod(0o700)
fallback = invoke(no_log, root / 'mktemp-failure', 'codex')
assert (root / 'ran').read_text() == 'ran\nran\n'
assert fallback.stdout == fallback.stderr == b''
# A successful mktemp followed by a failed log open must invoke the installer once.
(root / 'log-is-directory').mkdir()
(stub / 'mktemp').write_text('#!/bin/sh\nprintf "%s\\n" "$ROOT/log-is-directory"\n', newline='\n')
fallback = invoke(no_log, root / 'open-failure')
assert (root / 'ran').read_text() == 'ran\nran\nran\n'
assert not fallback.stderr and 'status 8' in json.loads(fallback.stdout)['systemMessage']
(stub / 'mktemp').unlink()
# A usable older Claude binary keeps diagnostics on stderr, including the reason.
binary = no_log / 'bin/claude-notifications'
binary.write_text('#!/bin/sh\nif [ "$1" = version ]; then echo 1.41.0; fi\n', newline='\n')
binary.chmod(0o700)
upgrade = invoke(no_log, root / 'upgrade-cache')
assert not upgrade.stdout and b'Error: raw installer output' in upgrade.stderr
assert b'Details: ' in upgrade.stderr

# Offline forced upgrades return zero while keeping the old version. Its
# explanation is still a failure diagnostic and must retain its full log.
offline = fixture('offline', 'echo "Keeping existing installation (GitHub unreachable)"; exit 0\n')
shutil.copyfile(binary, offline / 'bin/claude-notifications')
(offline / 'bin/claude-notifications').chmod(0o700)
offline_cache = root / 'offline-cache'
offline_result = invoke(offline, offline_cache)
assert not offline_result.stdout and b'GitHub unreachable' in offline_result.stderr
assert b'Details: ' in offline_result.stderr and len(logs(offline_cache)) == 1
print('PASS: private attempt logs, diagnostics, JSON, Codex silence and concurrency')
PY
