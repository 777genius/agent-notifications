#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -eu
src=$(cd "$(dirname "$0")" && pwd)
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
test_env_setup "$root"
python3 - "$src/hook-wrapper.sh" "$root" <<'PY'
import concurrent.futures
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

source, root = Path(sys.argv[1]), Path(sys.argv[2])
env = dict(os.environ, ROOT=root.as_posix(), CN_PRODUCT='claude')
version = '1.42.0'
stub = root / 'stubs'
stub.mkdir()
(stub / 'uname').write_text('#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
(stub / 'uname').chmod(0o700)


def fixture(name, installer):
    plugin = root / name
    (plugin / 'bin').mkdir(parents=True)
    (plugin / '.claude-plugin').mkdir()
    shutil.copyfile(source, plugin / 'bin/hook-wrapper.sh')
    (plugin / '.claude-plugin/plugin.json').write_text(json.dumps({'version': version}))
    script = plugin / 'bin/install.sh'
    script.write_text('#!/bin/sh\n# agent-notifications-managed-writer-protocol-v1\n' + installer)
    script.chmod(0o700)
    return plugin


def invoke(plugin, cache, product='claude', extra=None):
    run_env = dict(env, XDG_CACHE_HOME=cache.as_posix(), CN_PRODUCT=product)
    if extra:
        run_env.update(extra)
    result = subprocess.run(['bash', '-c', 'export PATH="$ROOT/stubs:$PATH"; unset OS; exec sh "$1" handle-hook Stop',
                             'fixture', (plugin / 'bin/hook-wrapper.sh').as_posix()],
                            env=run_env, capture_output=True, timeout=15)
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
# Repeated failures keep their logs but emit only one notification.
repeated = invoke(plugin, cache)
assert repeated.stdout == repeated.stderr == b''
assert len(logs(cache)) == 2

# An oversized UTF-8 reason must stay bounded and decodable, without splitting a character.
(root / 'reason.txt').write_text('Error: ' + 'я' * 350 + '\n', encoding='utf-8')
long_result = invoke(plugin, root / 'long-cache')
excerpt = json.loads(long_result.stdout)['systemMessage'].split('\n')[1]
assert excerpt.startswith('Error: ') and len(excerpt) <= 300
assert len(excerpt.encode()) <= 300

# Codex must retain the actual failure while leaving both streams empty.
codex = invoke(plugin, root / 'codex-cache', 'codex')
assert codex.stdout == codex.stderr == b''
assert len(logs(root / 'codex-cache', 'codex')) == 1

# Concurrent attempts must have independent complete logs and one atomic notification.
parallel = fixture('parallel', 'printf "begin:%s\\n" "$$"; sleep 0.1; printf "Error: attempt:%s\\n" "$$"; exit 7\n')
parallel_cache = root / 'parallel-cache'
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    futures = [pool.submit(invoke, parallel, parallel_cache) for _ in range(2)]
    results = [f.result() for f in futures]
assert sum(bool(r.stdout) for r in results) == 1
assert all(not r.stderr for r in results)
attempts = logs(parallel_cache)
assert len(attempts) == 2 and attempts[0].name != attempts[1].name
pids = []
for log in attempts:
    first, last = log.read_text().splitlines()
    pid = first.split(':')[1]
    assert last == 'Error: attempt:' + pid
    pids.append(pid)
assert len(set(pids)) == 2

# Success removes only its own log and resets notification suppression after recovery.
script = plugin / 'bin/install.sh'
script.write_text('''#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
cat > "$INSTALL_TARGET_DIR/claude-notifications" <<'BIN'
#!/bin/sh
if [ "$1" = version ]; then echo 1.42.0; fi
BIN
chmod +x "$INSTALL_TARGET_DIR/claude-notifications"
echo repaired
''')
repaired = invoke(plugin, cache)
assert repaired.stdout == repaired.stderr == b''
assert len(logs(cache)) == 2
assert not (cache / 'claude-notifications-go/install-failed-1.42.0').exists()
(plugin / 'bin/claude-notifications').unlink()
script.write_text('#!/bin/sh\n# agent-notifications-managed-writer-protocol-v1\necho "Error: failed again"; exit 9\n')
assert 'failed again' in json.loads(invoke(plugin, cache).stdout)['systemMessage']

# A broken cache or mktemp must never stop the installer or expose its raw output.
no_log = fixture('no-log', 'echo ran >> "$ROOT/ran"; echo "Error: raw installer output"; exit 8\n')
unusable = root / 'not-a-directory'
unusable.write_text('occupied')
fallback = invoke(no_log, unusable)
assert (root / 'ran').read_text() == 'ran\n'
assert not fallback.stderr
assert 'status 8' in json.loads(fallback.stdout)['systemMessage']
(stub / 'mktemp').write_text('#!/bin/sh\nexit 1\n')
(stub / 'mktemp').chmod(0o700)
fallback = invoke(no_log, root / 'mktemp-failure', 'codex')
assert (root / 'ran').read_text() == 'ran\nran\n'
assert fallback.stdout == fallback.stderr == b''
# A successful mktemp followed by a failed log open must invoke the installer once.
(root / 'log-is-directory').mkdir()
(stub / 'mktemp').write_text('#!/bin/sh\nprintf "%s\\n" "$ROOT/log-is-directory"\n')
fallback = invoke(no_log, root / 'open-failure')
assert (root / 'ran').read_text() == 'ran\nran\nran\n'
assert not fallback.stderr and 'status 8' in json.loads(fallback.stdout)['systemMessage']
(stub / 'mktemp').unlink()
# A usable older Claude binary keeps diagnostics on stderr, including the reason.
binary = no_log / 'bin/claude-notifications'
binary.write_text('#!/bin/sh\nif [ "$1" = version ]; then echo 1.41.0; fi\n')
binary.chmod(0o700)
upgrade = invoke(no_log, root / 'upgrade-cache')
assert not upgrade.stdout and b'Error: raw installer output' in upgrade.stderr
assert b'Details: ' in upgrade.stderr
print('PASS: private attempt logs, diagnostics, JSON, Codex silence and concurrency')
PY
