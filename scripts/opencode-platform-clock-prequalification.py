#!/usr/bin/env python3
"""Parent CI only: bounded actual product module observations; all grants FALSE.

Worker validation may import pure parsers but MUST NOT use --execute-ci or
--isolated-cell. All generated manifests/binaries are outside the checkout.
"""
import argparse
import _thread
import base64
import ctypes
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import queue
import re
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import threading
import time

BASE = 'c7bf79d36b2d6a44403500c8b9b4fc57056ec8e9'
API_INPUT_COMMIT = '25000e2e11340b930615751293073a67af1e5801'
CLOSED_CANONICAL = '73f9af516e9ebb4b1c4fe6cbefea27229906a24f49de3c3eebea70f9dabe3ddf'
CURRENT_COMMIT = 'aec0b9a6d8898f68f923aaf08b7306d931fd9d76'
CURRENT_PACKAGE = '9816a52873e44e6b6d9d47a419834e0faf77e5747d0e7000ed86e643bfb25fb1'
VERSIONS = {'1.18.33': ('51ef4be1d3c122f18fefb510dca8d778571f4f18', '1.3.14'),
            '2.0.21': ('8a8bd622a3d7dc29ccf30ec17f84e363ed95ed72', '1.4.2'),
            '1.18.34': (CURRENT_COMMIT, '1.3.14')}
MODULES = tuple('opencode-plugin/' + x + '.mjs' for x in (
    'platform-clock', 'linux-clock', 'darwin-clock', 'windows-clock',
    'native-clock-contract', 'native-clock-image', 'protocol'))
OWNED = ('scripts/opencode-platform-clock-prequalification.py',
         'scripts/opencode-platform-clock-prequalification.mjs',
         '.github/workflows/opencode-platform-clock-prequalification.yml',
         'scripts/testdata/opencode-platform-clock-prequalification/README.md',
         'scripts/testdata/opencode-platform-clock-prequalification/test_fixture.py')
CELLS = {('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'), ('darwin', 'arm64'), ('windows', 'amd64')}
BUDGETS = {'preparationMs': 2000, 'jsMs': 25000, 'goMs': 20000, 'helperMs': 224,
           'sampleCap': 512, 'chunkSize': 32, 'rounds': 3, 'frameBytes': 16384, 'jobSeconds': 900}
QUALIFICATIONS = {k: False for k in ('TimePolicyQualified', 'runtimeEligibilityGranted', 'candidateRQualified',
    'candidateTQualified', 'sourceWallBoundQualified', 'suspendQualified', 'finalSpanQualified', 'registryQualified')}
HERE = Path(__file__).resolve().parent
REPO = HERE.parent
HEX = re.compile(r'[a-f0-9]{64}')
MAX = 9223372036854775807


def need(value, reason):
    if not value:
        raise RuntimeError(reason)


def canonical(value):
    return json.dumps(value, separators=(',', ':'), sort_keys=True, ensure_ascii=True).encode()


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def unique(pairs):
    result = {}
    for key, value in pairs:
        need(key not in result, 'duplicate_json_key')
        result[key] = value
    return result


def parse(raw, cap=1048576):
    need(isinstance(raw, bytes) and 0 < len(raw) <= cap, 'json_byte_limit')
    return json.loads(raw, object_pairs_hook=unique,
                      parse_constant=lambda _: need(False, 'nonfinite_json'))


def load(path, cap=1048576):
    p = Path(path)
    need(not p.is_symlink() and p.is_file() and p.stat().st_size <= cap, 'json_file_limit')
    return parse(p.read_bytes(), cap)


def write_json(path, value):
    with Path(path).open('xb') as out:
        out.write(canonical(value) + b'\n')
    Path(path).chmod(0o600)


def remaining(end):
    left = end - time.monotonic()
    need(left > 0, 'absolute_deadline')
    return left


def commit_match(observed, expected):
    need(isinstance(observed, str) and isinstance(expected, str) and
         re.fullmatch('[a-f0-9]{40}', observed) and re.fullmatch('[a-f0-9]{40}', expected) and
         observed == expected, 'exact_source_commit')
    return observed


def native_header(raw, os_name, arch):
    need((os_name, arch) in CELLS and isinstance(raw, bytes) and len(raw) >= 64, 'native_cell_header')
    if os_name == 'linux':
        ok = raw[:6] == b'\x7fELF\x02\x01' and int.from_bytes(raw[18:20], 'little') == (62 if arch == 'amd64' else 183)
    elif os_name == 'darwin':
        ok = raw[:4] == b'\xcf\xfa\xed\xfe' and int.from_bytes(raw[4:8], 'little') == (0x1000007 if arch == 'amd64' else 0x100000c)
    else:
        at = int.from_bytes(raw[60:64], 'little')
        ok = (arch == 'amd64' and raw[:2] == b'MZ' and 64 <= at <= len(raw) - 26 and
              raw[at:at+6] == b'PE\0\0\x64\x86' and raw[at+24:at+26] == b'\x0b\x02')
    need(ok, 'actual_image_architecture')


def frame(raw, os_name):
    v = parse(raw, 1024)
    keys = ['protocol', 'boot', 'clockDomain', 'clockKind', 'monoLoNs', 'monoHiNs', 'wallUnixNs', 'uncertaintyNs']
    need(isinstance(v, dict) and list(v) == keys and type(v['protocol']) is int and v['protocol'] == 1 and
         (json.dumps(v, separators=(',', ':'), ensure_ascii=True) + '\n').encode() == raw, 'canonical_go_frame')
    need(isinstance(v['boot'], str) and re.fullmatch('[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}', v['boot']) and
         v['boot'] != '00000000-0000-0000-0000-000000000000', 'canonical_boot')
    kind, domain = v['clockKind'], v['clockDomain']
    if os_name == 'linux':
        need(kind == 'linux-boottime' and isinstance(domain, str) and
             re.fullmatch('linux-time:[1-9][0-9]{0,19}:[1-9][0-9]{0,19}', domain) and
             all(int(x) <= 18446744073709551615 for x in domain.split(':')[1:]), 'actual_linux_domain')
    else:
        need((os_name, kind, domain) in [('darwin', 'darwin-monotonic-raw', 'darwin-kernel'),
             ('windows', 'windows-interrupt-precise', 'windows-kernel')], 'actual_native_domain')
    numbers = []
    for k in keys[4:]:
        need(isinstance(v[k], str) and re.fullmatch('(0|[1-9][0-9]{0,18})', v[k]) and int(v[k]) <= MAX, 'int64_frame')
        numbers.append(int(v[k]))
    lo, hi, wall, error = numbers
    need(lo > 0 and wall > 0 and 0 <= hi - lo <= 100000000 and error == hi - lo + 3000000, 'original_go_bracket')
    return v


def archive_name(name):
    need(isinstance(name, str) and 0 < len(name) <= 240 and '\\' not in name and '\0' not in name and
         not name.startswith('/') and all(p not in ('', '.', '..') for p in name.rstrip('/').split('/')) and
         ':' not in name, 'strict_archive_path')
    return PurePosixPath(name)


def bound_source(blob, actual, os_name):
    if blob == actual:
        return 'git_blob_bytes'
    need(os_name == 'windows' and b'\0' not in blob and actual == blob.replace(b'\r\n', b'\n').replace(b'\n', b'\r\n'),
         'checkout_byte_provenance')
    return 'windows_git_crlf_checkout_bytes'


def current_primary(v, pinned_binary):
    keys = {'version', 'commit', 'bun', 'os', 'arch', 'archive', 'archiveSHA256', 'binarySHA256', 'sri'}
    need(isinstance(v, dict) and set(v) == keys and
         (v['version'], v['commit'], v['bun'], v['os'], v['arch']) ==
         ('1.18.34', CURRENT_COMMIT, '1.3.14', 'linux', 'x64') and
         v['archive'] == 'https://registry.npmjs.org/opencode-linux-x64/-/opencode-linux-x64-1.18.34.tgz' and
         HEX.fullmatch(v['archiveSHA256']) and v['binarySHA256'] == pinned_binary and
         re.fullmatch(r'sha512-[A-Za-z0-9+/]{86}==', v['sri']), 'separately_staged_current_primary_required')
    return v


def closed_inputs(value):
    need(digest(canonical(value)) == CLOSED_CANONICAL and value['schema'] == 2 and
         len(value['sources']) == 21 and len(value['artifacts']) == 10, 'immutable_reviewed_inputs')
    return value


class Owned:
    """Actual wait AND both pipe EOFs retain ownership; kills never prove success."""
    def __init__(self, job_end):
        self.end = job_end
        self.live = {}
        self.held_helper = None
        self.cleanup_end = None
        self.starts = self.closes = self.helpers = self.helper_closes = self.highwater = 0

    def start(self, argv, cwd, env, label, cap=65536, interactive=False):
        remaining(self.end)
        need(len(self.live) < 4 and (label != 'helper' or not any(s['label'] == 'helper' for s in self.live.values())), 'owned_weight_limit')
        started_at = time.monotonic()
        p = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.PIPE if interactive else subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=os.name != 'nt',
                             creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
        state = {'p': p, 'label': label, 'startedAt': started_at, 'buffers': [bytearray(), bytearray()],
                 'threads': [], 'overflow': False, 'pipeError': False, 'lines': queue.Queue(maxsize=16)}
        self.live[p.pid] = state
        self.starts += 1
        self.highwater = max(self.highwater, len(self.live))
        if label == 'helper': self.helpers += 1
        for index, stream in enumerate((p.stdout, p.stderr)):
            def drain(index=index, stream=stream):
                pending = bytearray()
                try:
                    while True:
                        block = os.read(stream.fileno(), 512)
                        if not block: break
                        room = cap - len(state['buffers'][index])
                        if len(block) > room:
                            state['overflow'] = True
                            continue
                        state['buffers'][index].extend(block)
                        if interactive and index == 0:
                            pending.extend(block)
                            need(len(pending) <= BUDGETS['frameBytes'], 'child_frame_limit')
                            while b'\n' in pending:
                                at = pending.index(10)
                                raw = bytes(pending[:at+1]); del pending[:at+1]
                                state['lines'].put_nowait(raw)
                    if pending: state['pipeError'] = True
                except Exception:
                    state['pipeError'] = True
            thread = threading.Thread(target=drain, daemon=True)
            state['threads'].append(thread); thread.start()
        return state

    def message(self, s, kind, end):
        need(not s['overflow'] and not s['pipeError'], 'child_stream_bound')
        try: raw = s['lines'].get(timeout=min(remaining(end), remaining(self.end)))
        except queue.Empty: raise RuntimeError('child_frame_deadline')
        value = parse(raw, BUDGETS['frameBytes'])
        if value.get('kind') == 'failure':
            reason = value.get('reason', '')
            need(isinstance(reason, str) and re.fullmatch('[a-z0-9_]{1,80}', reason) and
                 all(value.get(k) is False for k in QUALIFICATIONS), 'closed_failure_diagnostic')
            error = RuntimeError(reason)
            if 'nativeComparisonWidths' in value:
                widths = value['nativeComparisonWidths']
                need(reason == 'actual_translation_counter_span' and isinstance(widths, dict) and
                     set(widths) == {'round', 'outerWidthNs', 'goWidthNs'} and
                     all(type(x) is int for x in widths.values()) and 0 <= widths['round'] < 3 and
                     224000000 < widths['outerWidthNs'] <= 2000000000 and
                     0 <= widths['goWidthNs'] <= 100000000, 'closed_comparison_width_diagnostic')
                error.nativeComparisonWidths = widths
            raise error
        need(not s['overflow'] and not s['pipeError'] and value.get('kind') == kind, 'child_frame_order_or_failure')
        return value

    def send(self, s, value, end):
        remaining(end); remaining(self.end)
        raw = canonical(value) + b'\n'
        need(len(raw) <= BUDGETS['frameBytes'] and s['p'].poll() is None, 'owned_parent_frame')
        s['p'].stdin.write(raw); s['p'].stdin.flush()
        remaining(end)

    def kill(self, s):
        p = s['p']
        if p.poll() is None and not s.get('killRequested', False):
            if os.name == 'nt': p.kill()
            else:
                need(os.getpgid(p.pid) == p.pid, 'owned_group_identity')
                os.killpg(p.pid, signal.SIGKILL)
            s['killRequested'] = True  # request only; wait/EOF must still prove closure

    def close(self, s, end, natural=True):
        p = s['p']
        if not natural: self.kill(s)
        p.wait(timeout=remaining(end))
        for t in s['threads']: t.join(timeout=remaining(end))
        need(not any(t.is_alive() for t in s['threads']), 'actual_pipe_eof_required')
        for stream in (p.stdin, p.stdout, p.stderr):
            if stream is not None: stream.close()
        remaining(end)
        self.live.pop(p.pid)
        self.closes += 1
        if s['label'] == 'helper': self.helper_closes += 1
        if natural: need(p.returncode == 0 and not s['overflow'] and not s['pipeError'], 'actual_natural_close')

    def admit_helper(self, helper, expected_sha):
        remaining(self.end)
        need(self.held_helper is None and not helper.is_symlink(), 'single_held_helper_image')
        fd = os.open(helper, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_CLOEXEC', 0))
        self.held_helper = (fd, helper, expected_sha, None, None)  # Register before any fallible stat.
        self.held_helper = (fd, helper, expected_sha, os.fstat(fd), helper.lstat())
        self.verify_helper(helper, expected_sha)
        need(sha(helper) == expected_sha, 'actual_helper_hash')
        self.verify_helper(helper, expected_sha)
        remaining(self.end)

    def verify_helper(self, helper, expected_sha):
        need(self.held_helper is not None, 'held_helper_image_required')
        fd, path, pinned_sha, fd_original, path_original = self.held_helper
        keys = ('st_dev', 'st_ino', 'st_mode', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
        # Windows3.12 path .exe mode/ctime differs from fstat representation.
        # Bind device/inode, then compare each full snapshot to its own original.
        need(helper == path and expected_sha == pinned_sha and
             (fd_original.st_dev, fd_original.st_ino) == (path_original.st_dev, path_original.st_ino) and
             all(stat.S_ISREG(s.st_mode) and all(getattr(s, k) == getattr(original, k) for k in keys)
                 for s, original in ((os.fstat(fd), fd_original), (helper.lstat(), path_original))),
             'held_helper_image_changed')

    def start_helper(self, helper, expected_sha, cwd, env, operation_end):
        # Actual module cases hold the preflight SHA-verified file; cheap exact
        # fd/path identity checks preserve custody inside the native224ms span.
        remaining(operation_end); remaining(self.end)
        if self.held_helper is not None:
            self.verify_helper(helper, expected_sha)
        else:  # Direct/pure callers retain the original immediate SHA contract.
            actual_sha = sha(helper)
            remaining(operation_end); remaining(self.end)
            need(actual_sha == expected_sha, 'actual_helper_hash')
        launch_start = time.monotonic()
        end = min(operation_end, self.end, launch_start + BUDGETS['goMs'] / 1000)
        s = self.start([str(helper), 'opencode-clock', '--protocol', '1'], cwd, env, 'helper', 1024)
        if self.held_helper is not None: self.verify_helper(helper, expected_sha)
        return s, end

    def cleanup(self):
        # One nonauthorizing 5s ceiling, shared by all owned waits/pipe joins
        # and repeated finally paths. Kill every live child before waiting any.
        if self.cleanup_end is None: self.cleanup_end = time.monotonic() + 5
        states = list(self.live.values())
        for s in states:
            try: self.kill(s)
            except Exception: pass
        for s in states:
            try: self.close(s, self.cleanup_end, natural=False)
            except Exception: pass
        if self.held_helper is not None:
            try:
                os.close(self.held_helper[0]); self.held_helper = None
            except OSError: pass  # Keep failed closure visible in the ownership summary.

    def command(self, argv, cwd, env, seconds=30, cap=4194304):
        s = self.start(argv, cwd, env, 'preparation', cap)
        end = min(self.end, s['startedAt'] + seconds)
        try:
            self.close(s, end)
            return bytes(s['buffers'][0])
        finally:
            if s['p'].pid in self.live:
                self.cleanup()

    def summary(self):
        return {'ownedStarts': self.starts, 'ownedActualCloses': self.closes,
                'helperStarts': self.helpers, 'helperActualCloses': self.helper_closes,
                'maxOwnedWeight': self.highwater, 'allOwnedHandlesClosed': not self.live and self.held_helper is None}


def canonical_path(path, role='path'):
    p = Path(path).absolute()
    for depth, at in enumerate((p, *p.parents)):
        symlink = at.is_symlink()
        junction = not symlink and hasattr(at, 'is_junction') and at.is_junction()
        if symlink or junction:
            error = RuntimeError('symlink_ancestry')
            error.path_failure = {'role': role, 'ancestorDepth': depth, 'kind': 'symlink' if symlink else 'junction'}
            raise error
    need(p.resolve() == p, 'canonical_native_path')
    return p


def private_root(base):
    base = canonical_path(base)
    need(base.is_dir() and not base.is_relative_to(REPO), 'external_temp_base')
    if os.name == 'nt':
        # Short literal system-drive path. No Git Bash /c aliases, long-name
        # reinterpretation or short-name recanonicalization of source files.
        need(re.fullmatch(r'C:\\[^\\ :]{1,24}', str(base)), 'windows_short_temp_base')
    root = Path(tempfile.mkdtemp(prefix='TEST-pclock-', dir=base)); root.chmod(0o700)
    if os.name == 'nt': secure_windows(root)
    else: need(stat.S_IMODE(root.stat().st_mode) == 0o700 and root.stat().st_uid == os.getuid(), 'owned_private_root')
    return canonical_path(root)


def secure_windows(root):
    # ACL bootstrap only, not a clock/API substitute. Current-process token is
    # kernel authenticated; fixed protected OS directory is a signed assumption.
    from ctypes import wintypes as w
    k = ctypes.WinDLL(r'C:\Windows\System32\kernel32.dll', use_last_error=True)
    a = ctypes.WinDLL(r'C:\Windows\System32\advapi32.dll', use_last_error=True)
    k.GetCurrentProcess.restype = w.HANDLE
    k.CloseHandle.argtypes = [w.HANDLE]; k.LocalFree.argtypes = [ctypes.c_void_p]
    a.OpenProcessToken.argtypes = [w.HANDLE, w.DWORD, ctypes.POINTER(w.HANDLE)]
    a.GetTokenInformation.argtypes = [w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD, ctypes.POINTER(w.DWORD)]
    a.ConvertSidToStringSidW.argtypes = [ctypes.c_void_p, ctypes.POINTER(w.LPWSTR)]
    token, length, sid_text = w.HANDLE(), w.DWORD(), w.LPWSTR()
    need(a.OpenProcessToken(k.GetCurrentProcess(), 8, ctypes.byref(token)), 'private_token')
    try:
        a.GetTokenInformation(token, 1, None, 0, ctypes.byref(length))
        need(0 < length.value <= 4096, 'private_token_bound')
        data = ctypes.create_string_buffer(length.value)
        need(a.GetTokenInformation(token, 1, data, length.value, ctypes.byref(length)), 'private_token_identity')
        sid = ctypes.cast(data, ctypes.POINTER(ctypes.c_void_p))[0]
        need(a.ConvertSidToStringSidW(sid, ctypes.byref(sid_text)), 'private_sid')
        owned = Owned(time.monotonic() + 10)
        owned.command([r'C:\Windows\System32\icacls.exe', str(root), '/inheritance:r', '/grant:r',
                       '*' + sid_text.value + ':(OI)(CI)F'], root, {'SystemRoot': r'C:\Windows'}, 10)
    finally:
        if sid_text: k.LocalFree(ctypes.cast(sid_text, ctypes.c_void_p))
        k.CloseHandle(token)


def environment(root, tooling=False):
    env = {'CI': 'true', 'NO_COLOR': '1', 'BUN_BE_BUN': '1', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
           'OPENCODE_DISABLE_AUTOUPDATE': '1', 'OPENCODE_DISABLE_DEFAULT_PLUGINS': '1',
           'OPENCODE_DISABLE_MODELS_FETCH': '1', 'OPENCODE_DISABLE_PROJECT_CONFIG': '1'}
    for k, leaf in {'HOME': 'home', 'USERPROFILE': 'home', 'XDG_CONFIG_HOME': 'config', 'XDG_DATA_HOME': 'data',
                    'XDG_CACHE_HOME': 'cache', 'XDG_STATE_HOME': 'state', 'XDG_RUNTIME_DIR': 'run',
                    'TMPDIR': 'tmp', 'TEMP': 'tmp', 'TMP': 'tmp', 'OPENCODE_CONFIG_DIR': 'opencode-config'}.items():
        p = root / leaf; p.mkdir(mode=0o700, exist_ok=True); env[k] = str(p)
    if os.name == 'nt': env['SystemRoot'] = r'C:\Windows'
    if tooling:
        env.update(PATH=os.environ['PATH'], GOENV='off', GOWORK='off', GOTOOLCHAIN='local',
                   GOPATH=str(root / 'gopath'), GOCACHE=str(root / 'gocache'), CGO_ENABLED='1',
                   GIT_CONFIG_COUNT='1', GIT_CONFIG_KEY_0='safe.directory', GIT_CONFIG_VALUE_0=str(REPO),
                   GIT_TERMINAL_PROMPT='0', GCM_INTERACTIVE='never')
    return env


def git(owned, env, *args, cap=4194304):
    return owned.command(['git', '-c', 'safe.directory=' + str(REPO), *args], REPO, env, 30, cap)


def checkout(owned, env, expected):
    observed = git(owned, env, 'rev-parse', '--verify', 'HEAD').decode().strip()
    commit_match(observed, expected)
    git(owned, env, 'merge-base', '--is-ancestor', BASE, observed)
    need(not git(owned, env, 'status', '--porcelain', '--untracked-files=no').strip(), 'clean_tracked_checkout')
    return observed


def git_leaf(owned, env, path, commit, os_name):
    p = canonical_path(REPO / path)
    blob = git(owned, env, 'show', commit + ':' + path, cap=16777216)
    actual = p.read_bytes()
    return {'path': path, 'gitBlobSHA256': digest(blob), 'actualSHA256': digest(actual),
            'actualBytes': len(actual), 'provenance': bound_source(blob, actual, os_name)}


def source_closure(owned, env, commit, os_name, root):
    # All native ports, protocol/dispatch AND all platform E0 source originals.
    # Additional Go dependency originals (including SDK if linked) come from
    # actual go list, never selected by a pretend semantic/Merkle ID.
    paths = set(MODULES) | set(OWNED)
    paths |= {'go.mod', 'go.sum', 'cmd/claude-notifications/main.go',
              'opencode-plugin/clock-cells.mjs', 'opencode-plugin/clock-qualification-data.mjs'}
    for directory in ('internal/opencodeevent', 'internal/agentnotify/journal', 'cmd/claude-notifications'):
        paths |= {p.relative_to(REPO).as_posix() for p in (REPO / directory).glob('*clock*.go') if not p.name.endswith('_test.go')}
    leaves = [git_leaf(owned, env, p, commit, os_name) for p in sorted(paths)]
    write_json(root / 'checkout-source-closure.json', leaves)
    return leaves


def json_stream(raw):
    text = raw.decode('utf8'); at = 0; out = []
    decoder = json.JSONDecoder(object_pairs_hook=unique)
    while at < len(text):
        while at < len(text) and text[at].isspace(): at += 1
        if at == len(text): break
        value, at = decoder.raw_decode(text, at); out.append(value)
        need(len(out) <= 2048, 'go_dependency_count')
    return out


def build_helper(owned, env, root, commit, os_name, arch):
    if os_name == 'windows':
        # setup-go may expose its unchanged toolchain through a cache junction.
        # Bind the physical toolchain before collecting original source leaves.
        root_raw = owned.command(['go', 'env', 'GOROOT'], root, env)
        go_root = canonical_path(Path(root_raw.decode('utf8').strip()).resolve(strict=True), 'native_go_toolchain')
        go_exe = canonical_path(go_root / 'bin' / 'go.exe', 'native_go_toolchain')
        selected_go = shutil.which('go', path=env['PATH'])
        need(selected_go and go_exe.is_file() and go_exe.samefile(selected_go), 'same_native_go_toolchain')
        env = dict(env, GOROOT=str(go_root), PATH=str(go_root / 'bin') + os.pathsep + env['PATH'])
    version_raw = owned.command(['go', 'version'], root, env)
    need(re.fullmatch(rb'go version go1\.27\.1 ' + os_name.encode() + b'/' + arch.encode() + rb'\r?\n', version_raw), 'existing_pinned_native_go')
    binary = root / ('helper.exe' if os_name == 'windows' else 'helper')
    command = ['go', 'build', '-trimpath', '-buildvcs=true', '-o', str(binary), './cmd/claude-notifications']
    owned.command(command, REPO, env, 300)
    info = owned.command(['go', 'version', '-m', str(binary)], root, env)
    info_text = info.decode('utf8')
    settings = dict(re.findall(r'^\s*build\s+([^=\s]+)=(.*)$', info_text, re.M))
    need(settings.get('vcs.revision') == commit and settings.get('vcs.modified') == 'false' and
         settings.get('GOOS') == os_name and settings.get('GOARCH') == arch and settings.get('CGO_ENABLED') == '1', 'exact_vcs_native_build')
    with binary.open('rb') as stream: native_header(stream.read(4096), os_name, arch)
    info_path = root / 'build-info.private'; info_path.write_bytes(info); info_path.chmod(0o600)
    dependencies = json_stream(owned.command(['go', 'list', '-deps', '-json', './cmd/claude-notifications'],
                                           REPO, env, 120, 16777216))
    owned.command(['go', 'mod', 'verify'], REPO, env, 60)
    leaves, modules = [], {}
    for package in dependencies:
        need(not package.get('Error') and not package.get('Incomplete'), 'complete_go_dependency_closure')
        directory = canonical_path(package['Dir'], 'go_dependency_directory')
        module = package.get('Module')
        if module:
            need(not module.get('Replace'), 'unreviewed_go_module_replace')
            identity = module['Path'] + '@' + module.get('Version', 'checkout')
            if identity not in modules:
                modules[identity] = {'path': module['Path'], 'version': module.get('Version'),
                                     'sum': module.get('Sum'), 'goModSHA256': sha(module['GoMod'])}
        for category in ('GoFiles', 'CgoFiles', 'CFiles', 'CXXFiles', 'MFiles', 'HFiles', 'FFiles', 'SFiles', 'SysoFiles', 'EmbedFiles'):
            for name in package.get(category, []):
                p = canonical_path(directory / name, 'go_dependency_leaf')
                need(p.is_relative_to(directory) and p.is_file(), 'original_go_leaf_containment')
                record = {'package': package['ImportPath'], 'category': category, 'leaf': name,
                          'actualSHA256': sha(p), 'actualBytes': p.stat().st_size,
                          'origin': identity if module else 'native_go_toolchain_stdlib'}
                if p.is_relative_to(REPO):
                    record.update(git_leaf(owned, env, p.relative_to(REPO).as_posix(), commit, os_name))
                leaves.append(record)
    need(any(x.get('path') == 'cmd/claude-notifications/main.go' for x in leaves) and
         any(x.get('path') == 'cmd/claude-notifications/opencode_clock.go' for x in leaves) and
         any(x.get('path') == 'internal/opencodeevent/clockprotocol.go' for x in leaves), 'actual_go_main_e0_closure')
    closure = {'leaves': leaves, 'modules': modules, 'buildInfoSHA256': digest(info), 'binarySHA256': sha(binary),
               'goVersion': version_raw.decode().strip(), 'sourceCommit': commit,
               'buildCommand': ['go', 'build', '-trimpath', '-buildvcs=true', '-o', '<owned TEST binary>', './cmd/claude-notifications'],
               'CGO_ENABLED': '1', 'vcsModified': False, 'compilerSHA256': sha(shutil.which('go', path=env['PATH']))}
    write_json(root / 'go-build-closure.json', closure)
    return binary, closure


def github(owned, env, endpoint, raw=False):
    # GH token exists ONLY in this explicit fetch environment, never in a build,
    # source fixture, helper, privilege launcher, or private artifact.
    fetch_env = {**env, 'GH_TOKEN': os.environ.get('GH_TOKEN', '')}
    need(fetch_env['GH_TOKEN'], 'ci_gh_token_required')
    argv = ['gh', 'api', endpoint]
    if raw: argv += ['-H', 'Accept: application/vnd.github.raw+json']
    return owned.command(argv, REPO, fetch_env, 30)


def origin_tag(owned, env, version):
    obj = parse(github(owned, env, 'repos/anomalyco/opencode/git/ref/tags/v' + version))['object']
    for _ in range(4):
        need(re.fullmatch('[a-f0-9]{40}', obj['sha']), 'canonical_origin_tag')
        if obj['type'] == 'commit': break
        need(obj['type'] == 'tag', 'origin_tag_type')
        obj = parse(github(owned, env, 'repos/anomalyco/opencode/git/tags/' + obj['sha']))['object']
    need(obj['type'] == 'commit' and obj['sha'] == VERSIONS[version][0], 'exact_origin_tag_commit')
    return obj['sha']


def primary_sources(owned, env, root, value):
    directory = root / 'primary-sources'; directory.mkdir(mode=0o700)
    for index, item in enumerate(value['sources']):
        raw = github(owned, env, 'repos/{repo}/contents/{path}?ref={ref}'.format(**item), True)
        need(len(raw) == item['bytes'] and digest(raw) == item['sha256'], 'reviewed_primary_bytes')
        p = directory / str(index); p.write_bytes(raw); p.chmod(0o600)
    return {'canonicalClosedInputsSHA256': digest(canonical(value)), 'inputHashes': value['inputHashes'],
            'sources': value['sources'], 'versions': value['versions'], 'artifacts': value['artifacts']}


def fetch_archive(owned, env, root, item):
    archive = root / 'official.tgz'
    # ONLY the immutable official npm URL. All GitHub calls use gh above.
    need(re.fullmatch(r'https://registry\.npmjs\.org/(?:@opencode/cli-|opencode-)[a-z0-9-]+/-/[a-z0-9.-]+\.tgz', item['archive']), 'official_registry_archive_url')
    owned.command(['curl', '--fail', '--silent', '--show-error', '--proto', '=https', '--tlsv1.2',
                   '--connect-timeout', '15', '--max-time', '120', '--max-filesize', '536870912',
                   '--output', str(archive), item['archive']], root, env, 125)
    need(0 < archive.stat().st_size <= 536870912 and sha(archive) == item['archiveSHA256'], 'official_archive_hash_before_parse')
    with archive.open('rb') as stream:
        sri = 'sha512-' + base64.b64encode(hashlib.file_digest(stream, 'sha512').digest()).decode()
    need(sri == item['sri'], 'official_archive_sri_before_parse')
    return archive


def extract_image(archive, root, item, os_name, arch, end):
    exe_name = 'opencode.exe' if os_name == 'windows' else 'opencode'
    exe = root / exe_name
    with tarfile.open(archive, 'r:gz') as tar:
        selected = None; count = total = 0; seen = set()
        for member in tar:
            remaining(end); count += 1; total += member.size
            archive_name(member.name)
            need(member.name not in seen and count <= 128 and 0 <= member.size <= 536870912 and
                 total <= 805306368 and (member.isfile() or member.isdir()), 'strict_regular_archive')
            seen.add(member.name)
            if PurePosixPath(member.name).name == exe_name:
                need(selected is None and member.name == 'package/bin/' + exe_name and member.isfile() and member.size > 0,
                     'exact_unique_archive_binary')
                selected = member
        need(selected is not None, 'official_binary_member')
        with tar.extractfile(selected) as inp, exe.open('xb') as out:
            left = selected.size
            while left:
                remaining(end); block = inp.read(min(left, 65536))
                need(block, 'exact_archive_binary_length'); out.write(block); left -= len(block)
    need(sha(exe) == item['binarySHA256'], 'official_binary_hash')
    with exe.open('rb') as stream: native_header(stream.read(4096), os_name, arch)
    exe.chmod(0o700)
    return exe


def kernel_image(p, exe, os_name):
    need(p.poll() is None, 'live_image_required')
    if os_name == 'linux':
        observed = Path('/proc') / str(p.pid) / 'exe'
        need(os.readlink(observed) == str(exe) and observed.stat().st_ino == exe.stat().st_ino and
             observed.stat().st_dev == exe.stat().st_dev and sha(observed) == sha(exe), 'kernel_executing_image')
    elif os_name == 'darwin':
        lib = ctypes.CDLL('/usr/lib/libproc.dylib')
        lib.proc_pidpath.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
        buffer = ctypes.create_string_buffer(4096)
        need(lib.proc_pidpath(p.pid, buffer, len(buffer)) > 0 and
             Path(os.fsdecode(buffer.value)).resolve() == exe, 'kernel_executing_image')
    else:
        from ctypes import wintypes as w
        k = ctypes.WinDLL(r'C:\Windows\System32\kernel32.dll')
        k.QueryFullProcessImageNameW.argtypes = [w.HANDLE, w.DWORD, w.LPWSTR, ctypes.POINTER(w.DWORD)]
        buffer = ctypes.create_unicode_buffer(32768); length = w.DWORD(len(buffer))
        need(k.QueryFullProcessImageNameW(w.HANDLE(int(p._handle)), 0, buffer, ctypes.byref(length)) and
             Path(buffer.value).resolve() == exe, 'kernel_executing_image')
    need(p.poll() is None, 'live_image_identity_lifetime')


def native_resources(p, os_name):
    need(p.poll() is None, 'live_resource_snapshot')
    if os_name == 'linux':
        # Fixed kernel FD directory, not user's files/configuration.
        return len(list((Path('/proc') / str(p.pid) / 'fd').iterdir()))
    if os_name == 'darwin':
        lib = ctypes.CDLL('/usr/lib/libproc.dylib')
        lib.proc_pidinfo.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_uint64, ctypes.c_void_p, ctypes.c_int]
        lib.proc_pidinfo.restype = ctypes.c_int
        buffer = ctypes.create_string_buffer(32768)
        count = lib.proc_pidinfo(p.pid, 1, 0, buffer, len(buffer))  # PROC_PIDLISTFDS, 8-byte records.
        need(0 < count < len(buffer) and count % 8 == 0, 'native_fd_snapshot_bound')
        return count // 8
    from ctypes import wintypes as w
    k = ctypes.WinDLL(r'C:\Windows\System32\kernel32.dll')
    k.GetProcessHandleCount.argtypes = [w.HANDLE, ctypes.POINTER(w.DWORD)]
    count = w.DWORD()
    need(k.GetProcessHandleCount(w.HANDLE(int(p._handle)), ctypes.byref(count)) and count.value <= 4096,
         'native_handle_snapshot_bound')
    return count.value


def windows_loader_files():
    # Authenticate bootstrap/held disk files, NOT a duplicate clock reader.
    # Product DLL/API-set use is conditional on the explicitly protected mapping
    # assumption; these disk hashes alone never grant mapping authentication.
    from ctypes import wintypes as w
    directory = Path(r'C:\Windows\System32')
    k = ctypes.WinDLL(str(directory / 'kernel32.dll'))
    k.GetSystemDirectoryW.argtypes = [w.LPWSTR, w.UINT]
    k.GetSystemDirectoryW.restype = w.UINT
    k.GetModuleFileNameW.argtypes = [w.HMODULE, w.LPWSTR, w.DWORD]
    k.GetModuleFileNameW.restype = w.DWORD
    buffer = ctypes.create_unicode_buffer(32768)
    n = k.GetSystemDirectoryW(buffer, len(buffer))
    need(0 < n < len(buffer) and buffer.value.lower() == str(directory).lower(), 'protected_system_directory')
    n = k.GetModuleFileNameW(w.HMODULE(k._handle), buffer, len(buffer))
    need(0 < n < len(buffer) and buffer.value.lower() == str(directory / 'kernel32.dll').lower(), 'actual_bootstrap_loader_origin')
    for path in (Path('C:\\'), directory.parent, directory): canonical_path(path)
    result = []
    for name in ('kernel32.dll', 'ntdll.dll'):
        path = canonical_path(directory / name); s = path.stat()
        need(path.is_file() and 0 < s.st_size <= 536870912, 'protected_loader_file_bound')
        result.append({'path': str(path), 'sha256': sha(path), 'device': s.st_dev, 'inode': s.st_ino,
                       'size': s.st_size, 'mtimeNs': s.st_mtime_ns, 'ctimeNs': s.st_ctime_ns})
    return result


def isolate_linux():
    need(os.geteuid() == 0, 'owned_test_root_privilege')
    before = os.readlink('/proc/self/ns/net')
    libc = ctypes.CDLL(None, use_errno=True); libc.unshare.argtypes = [ctypes.c_int]
    need(libc.unshare(0x40000000) == 0 and os.readlink('/proc/self/ns/net') != before and
         os.readlink('/proc/self/ns/net') != os.readlink('/proc/1/ns/net'), 'owned_fresh_netns')
    import socket
    need({name for _, name in socket.if_nameindex()} == {'lo'}, 'owned_loopback_only_namespace')
    # No sockets are used: the only communication is owned stdin/stdout pipes.


def require_sampler_nonincrease(os_name, loader_before, after, import_before=None):
    # Windows runtime/module imports precede the sole sampler creation.
    before = import_before if os_name == 'windows' else loader_before
    need(type(before) is int and 0 <= before <= 0xffffffff, 'actual_sampler_resource_baseline')
    need(after <= before, 'actual_module_resource_leak')


def settle_sampler_resources(before, initial, operation_end, read_resources, observation):
    # TEST temporal boundary only; no new budget, baseline or sampler read.
    require_sampler_nonincrease('windows', before, before, before)
    need(type(initial) is int and 0 <= initial <= 0xffffffff, 'native_handle_snapshot_bound')
    started = time.monotonic()
    observation.update(criterion='live_count_at_or_below_post_import_before_original_operation_end',
                       baseline=before, immediate=initial, immediateNonincrease=initial <= before,
                       final=initial, readsAfterImmediate=0, settlingElapsedMs=0,
                       remainingOperationMs=round((operation_end - started) * 1000, 3), settled=False)
    remaining(operation_end)
    after = initial
    while after > before:
        time.sleep(min(0.01, remaining(operation_end)))
        remaining(operation_end)
        after = read_resources()  # Actual live GetProcessHandleCount; failures propagate.
        need(type(after) is int and 0 <= after <= 0xffffffff, 'native_handle_snapshot_bound')
        observed_at = time.monotonic()
        observation.update(final=after, readsAfterImmediate=observation['readsAfterImmediate'] + 1,
                           settlingElapsedMs=round((observed_at - started) * 1000, 3),
                           remainingOperationMs=round((operation_end - observed_at) * 1000, 3))
        remaining(operation_end)  # A low count arriving at/after expiry cannot qualify.
    observation['settled'] = True
    return after


def require_repeated_sampler_nonincrease(baseline, observed, expected):
    # One external fixed baseline, checked before advancing either instance.
    need(type(expected) is int and expected in (1, 2) and len(observed) == expected, 'actual_repeated_disposal_count')
    for count in observed:
        require_sampler_nonincrease('windows', baseline, count, baseline)


def run_case(root, metadata, os_name, arch, job_end):
    case_started = time.monotonic(); operation_started = None; round_number = None
    stage = 'initial_custody'; disposed_observed = False
    def observation():
        now = time.monotonic()
        return {'stage': stage, 'caseElapsedMs': round((now - case_started) * 1000, 3),
                'operationElapsedMs': None if operation_started is None else round((now - operation_started) * 1000, 3),
                'helperRound': round_number, 'disposedFrameObserved': disposed_observed}
    own = Owned(job_end)
    env = environment(root)
    metadata_path = root / 'metadata.json'
    need(load(metadata_path) == metadata, 'external_case_metadata')
    exe = canonical_path(root / ('opencode.exe' if os_name == 'windows' else 'opencode'))
    helper = canonical_path(root / ('helper.exe' if os_name == 'windows' else 'helper'))
    fixture = canonical_path(root / 'fixture.mjs')
    safe = {'version': metadata['version'], 'bun': metadata['bun'], 'status': 'unqualified',
            'imageSHA256': metadata['imageSha256'], 'archiveSHA256': metadata['archiveSha256'],
            'sri': metadata['sri'], 'originCommit': metadata['originCommit'], 'sourceCommit': metadata['sourceCommit'],
            'fixtureSHA256': metadata['fixtureSha256'], 'moduleLeaves': metadata['moduleLeaves'],
            'helperSHA256': metadata['helperSha256'], 'actualModuleBound': False,
            'budgets': BUDGETS, **QUALIFICATIONS}
    host = None
    try:
        loader_files = windows_loader_files() if os_name == 'windows' else []
        if loader_files: write_json(root / 'os-loader-files.private.json', loader_files)
        own.admit_helper(helper, metadata['helperSha256'])
        need(sha(exe) == metadata['imageSha256'] and
             sha(fixture) == metadata['fixtureSha256'], 'owned_actual_file_hashes')
        host = own.start([str(exe), str(fixture), '--private-fixture', str(root)], root, env, 'module', interactive=True)
        js_end = min(job_end, host['startedAt'] + BUDGETS['jsMs'] / 1000)
        loader = own.message(host, 'loader', js_end)
        need(loader['pid'] == host['p'].pid and loader['ppid'] == os.getpid() and
             loader['executable'] == str(exe) and loader['fixture'] == str(fixture) and
             loader['loader'] == 'stock_BUN_BE_BUN_private_source_import' and
             all(loader[k] == metadata[k] for k in ('platform', 'arch', 'bun', 'imageSha256', 'fixtureSha256', 'sourceCommit')) and
             loader['modulePaths'] == {x['path']: str(root / x['path']) for x in metadata['moduleLeaves']}, 'actual_private_loader')
        write_json(root / 'loader.private.json', loader)
        kernel_image(host['p'], exe, os_name)
        resources_before = native_resources(host['p'], os_name)
        resource_before_at = time.monotonic() if os_name == 'windows' else None
        operation_started = time.monotonic()
        operation_end = min(js_end, job_end, operation_started + 2)
        measured_resource_counts = []
        own.send(host, {'kind': 'begin'}, operation_end)
        if os_name == 'windows':
            stage = 'postwarmup_resource_observation'
            ready = own.message(host, 'source_ready', operation_end)
            windows_leaf = next(x for x in metadata['moduleLeaves'] if x['path'] == 'opencode-plugin/windows-clock.mjs')
            need(set(ready) == {'kind', 'pid', 'sourceCommit', 'windowsModuleSHA256', 'samplerCreated', 'samples', 'warmup'} and
                 ready['pid'] == host['p'].pid and ready['sourceCommit'] == metadata['sourceCommit'] and
                 ready['windowsModuleSHA256'] == windows_leaf['actualSHA256'] and
                 ready['samplerCreated'] is True and type(ready['samples']) is int and ready['samples'] == 1 and
                 ready['warmup'] == {'samples': 1, 'disposeCalls': 2, 'sampleAfterDisposeRefused': True} and
                 type(ready['warmup']['samples']) is int and type(ready['warmup']['disposeCalls']) is int and
                 ready['warmup']['sampleAfterDisposeRefused'] is True,
                 'actual_postwarmup_source_baseline')
            warmed_resources = native_resources(host['p'], os_name)
            sampler_resource_before_at = time.monotonic()
            safe['nativeWarmupResourceObservation'] = {
                'measurementKind': 'GetProcessHandleCount', 'before': resources_before, 'after': warmed_resources,
                'delta': warmed_resources - resources_before,
                'baselineStage': 'loader_received_before_begin', 'stage': 'actual_postwarmup_baseline',
                'beforeCaseElapsedMs': round((resource_before_at - case_started) * 1000, 3),
                'beforeHostElapsedMs': round((resource_before_at - host['startedAt']) * 1000, 3),
                'samplerCreated': True, 'warmup': ready['warmup'], 'samples': 1,
                'operationElapsedMs': round((time.monotonic() - operation_started) * 1000, 3)}
            own.send(host, {'kind': 'sampler_begin'}, operation_end)
            safe['nativeResourceCheckpoints'] = []
        def resource_checkpoint(checkpoint, checkpoint_round, expected_samples):
            if os_name != 'windows': return
            ready = own.message(host, 'resource_checkpoint', operation_end)
            need(set(ready) == {'kind', 'pid', 'stage', 'round', 'samples'} and
                 ready['pid'] == host['p'].pid and ready['stage'] == checkpoint and
                 ready['round'] == checkpoint_round and
                 (ready['round'] is None or type(ready['round']) is int) and
                 type(ready['samples']) is int and ready['samples'] == expected_samples,
                 'actual_resource_checkpoint')
            count = native_resources(host['p'], os_name)
            if checkpoint == 'instance_disposed':
                # Aggregate count is diagnostic: live overlapping B still owns its image.
                measured_resource_counts.append(count)
            safe['nativeResourceCheckpoints'].append({'stage': checkpoint, 'round': checkpoint_round,
                'samples': expected_samples, 'count': count,
                'operationElapsedMs': round((time.monotonic() - operation_started) * 1000, 3)})
            own.send(host, {'kind': 'resource_continue', 'stage': checkpoint, 'round': checkpoint_round}, operation_end)
        if os_name == 'windows': resource_checkpoint('sampler_created', None, 1)
        helper_receipts = []
        helper_lifecycle = []
        for round_number in range(3):
            stage = 'helper_round'
            request = own.message(host, 'helper_request', operation_end)
            need(request['round'] == round_number, 'single_planned_round')
            s, end = own.start_helper(helper, metadata['helperSha256'], root, env, operation_end)
            try:
                own.close(s, end)
                own.verify_helper(helper, metadata['helperSha256'])
                need(not s['buffers'][1], 'helper_stderr_refused')
                raw = bytes(s['buffers'][0]); frame(raw, os_name)
                st = helper.stat()
                helper_lifecycle.append({'pid': s['p'].pid, 'argv': [str(helper), 'opencode-clock', '--protocol', '1'],
                    'helperSHA256': metadata['helperSha256'], 'fileIdentity': {'device': st.st_dev, 'inode': st.st_ino,
                    'bytes': st.st_size, 'mtimeNs': st.st_mtime_ns, 'ctimeNs': st.st_ctime_ns},
                    'startedAtMonotonic': s['startedAt'], 'closedAtMonotonic': time.monotonic(),
                    'returnCode': s['p'].returncode, 'naturalWaitAndBothPipeEOF': True,
                    'goFrameSHA256': digest(raw)})
                path = root / ('helper-' + str(round_number) + '.private')
                path.write_bytes(raw); path.chmod(0o600)
                helper_receipts.append(digest(raw))
                stage = 'helper_deadline_guard'
                remaining(end)
                stage = 'helper_response'
                own.send(host, {'kind': 'helper_response', 'round': round_number,
                                'raw': base64.b64encode(raw).decode()}, operation_end)
            finally:
                if s['p'].pid in own.live: own.cleanup()
            if os_name == 'windows':
                stage = 'helper_compared_resource_observation'
                resource_checkpoint('helper_compared', round_number, (round_number + 1) * (BUDGETS['chunkSize'] + 4) + (1 if round_number == 0 else 2))
                if round_number == 0:
                    resource_checkpoint('instance_disposed', 0, 38)
                    resource_checkpoint('sampler_created', 1, 38)
        stage = 'disposed'
        result = own.message(host, 'disposed', operation_end)
        disposed_observed = True
        validate_result(result, os_name)
        write_json(root / 'helper-lifecycle.private.json', helper_lifecycle)
        stage = 'resource_recheck'
        resources_after = native_resources(host['p'], os_name)
        if os_name == 'windows':
            resource_after_at = time.monotonic()
            # Non-authorizing numeric diagnostics from the two existing native reads.
            safe['nativeResourceObservation'] = {
                'measurementKind': 'GetProcessHandleCount', 'version': metadata['version'],
                'before': warmed_resources, 'after': resources_after, 'delta': resources_after - warmed_resources,
                'baselineStage': 'actual_postwarmup_before_measured_instances', 'stage': stage,
                'beforeCaseElapsedMs': round((sampler_resource_before_at - case_started) * 1000, 3),
                'afterCaseElapsedMs': round((resource_after_at - case_started) * 1000, 3),
                'beforeHostElapsedMs': round((sampler_resource_before_at - host['startedAt']) * 1000, 3),
                'afterHostElapsedMs': round((resource_after_at - host['startedAt']) * 1000, 3),
                'afterOperationElapsedMs': round((resource_after_at - operation_started) * 1000, 3),
                'disposedFrameObserved': disposed_observed, 'helperRound': round_number,
                'helperStarts': own.helpers, 'helperActualCloses': own.helper_closes}
        if os_name == 'windows':
            safe['nativeWarmupResourceObservation']['postDispose'] = resources_after
            safe['nativeWarmupResourceObservation']['samplerPhaseDelta'] = resources_after - warmed_resources
            safe['nativeResourceCheckpoints'].append({'stage': 'disposed', 'round': None,
                'samples': result['samples'], 'count': resources_after,
                'operationElapsedMs': round((time.monotonic() - operation_started) * 1000, 3)})
        if os_name == 'windows':
            measured_resource_counts.append(resources_after)
            safe['ownedClockResourceCriterion'] = {'criterion': 'identity_bound_descriptor_close_overlap_abort',
                'baseline': warmed_resources, 'aggregateCheckpointCounts': measured_resource_counts,
                'aggregateCountsDiagnosticOnly': True, 'processWideGrowthCauseVerified': False,
                'ownedClockLifetime': result['ownedClockLifetime']}
        else:
            require_sampler_nonincrease(os_name, resources_before, resources_after)
        remaining(operation_end)
        kernel_image(host['p'], exe, os_name)
        remaining(operation_end)
        stage = 'finish'
        own.send(host, {'kind': 'finish'}, operation_end)
        host['p'].stdin.close()
        stage = 'host_close'
        own.close(host, operation_end)
        # Product preparation ends at genuine host/helper closure. Retained
        # custody hashes use the original host/job ceiling, not a new grant.
        stage = 'final_custody_hashes'
        remaining(js_end)
        need(sha(exe) == metadata['imageSha256'] and sha(helper) == metadata['helperSha256'] and
             sha(fixture) == metadata['fixtureSha256'] and
             all(sha(root / x['path']) == x['actualSHA256'] for x in metadata['moduleLeaves']), 'final_actual_source_image_hashes')
        if loader_files:
            need(windows_loader_files() == loader_files, 'actual_os_loader_file_lifetime')
        stage = 'final_custody_guard'
        remaining(js_end)
        safe.update({k: result[k] for k in ('status', 'actualModuleBound', 'rounds', 'samples', 'comparisons',
                     'datePredicates', 'disposeCalls', 'sampleAfterDisposeRefused', 'operationElapsedMs', 'jsElapsedMs', 'checks')})
        if os_name == 'windows':
            safe.update({k: result[k] for k in ('warmupSamples', 'measuredSamples', 'measuredInstances', 'ownedClockLifetime')})
        safe.update(kernelExecutingImageVerified=True, nativeResourceNonincrease=os_name != 'windows',
                    protectedLoaderFileSHA256=[x['sha256'] for x in loader_files],
                    actualHelperOutputReceiptSHA256=helper_receipts,
                    rawPrivateReceiptSHA256=sha(root / 'samples.private.json'),
                    isolation='owned_loopback_only_netns' if os_name == 'linux' else 'private_files_minimal_env_no_netns')
    except Exception as error:
        safe['status'] = 'unqualified'
        safe['failureReason'] = failure_reason(error)
        safe['failureObservation'] = observation()
        if hasattr(error, 'nativeComparisonWidths'): safe['nativeComparisonWidths'] = error.nativeComparisonWidths
        if hasattr(error, 'path_failure'): safe['pathFailure'] = error.path_failure
    finally:
        own.cleanup()
        safe.update(own.summary())
        if own.live or own.held_helper is not None or safe['helperStarts'] != safe['helperActualCloses']: safe['status'] = 'unqualified'
        if safe['status'] == 'module_prequalification_observed' and not (
                safe['helperStarts'] == safe['helperActualCloses'] == 3 and
                safe['ownedStarts'] == safe['ownedActualCloses'] == 4 and safe['maxOwnedWeight'] <= 2):
            safe.update(status='unqualified', failureReason='actual_lifecycle_counts')
        stage = 'final_stream_custody'
        if host:
            for index, data in enumerate(host['buffers']):
                path = root / ('module-stream-' + str(index) + '.private'); path.write_bytes(data); path.chmod(0o600)
        if safe['status'] == 'module_prequalification_observed' and time.monotonic() >= js_end:
            safe.update(status='unqualified', failureReason='original_js_deadline')
            safe['failureObservation'] = observation()
    return safe


def validate_result(v, os_name):
    keys = {'kind', 'status', 'actualModuleBound', 'rounds', 'samples', 'comparisons', 'datePredicates',
            'disposeCalls', 'sampleAfterDisposeRefused', 'operationElapsedMs', 'jsElapsedMs', 'checks', *QUALIFICATIONS}
    if os_name == 'windows':
        keys.update(('warmupSamples', 'measuredSamples', 'measuredInstances', 'ownedClockLifetime'))
        need(all(type(v.get(k)) is int for k in
                 ('warmupSamples', 'measuredSamples', 'measuredInstances', 'disposeCalls')) and
             v.get('warmupSamples') == 1 and v.get('measuredSamples') == 109 and
             v.get('measuredInstances') == 2, 'actual_repeated_module_lifecycle')
        lifetime = v.get('ownedClockLifetime')
        need(isinstance(lifetime, dict) and set(lifetime) == {'descriptorOpens', 'descriptorCloses',
             'firstInitCancelRejected', 'overlapDisposalPreservedB', 'abortSticky'} and
             type(lifetime['descriptorOpens']) is int and lifetime['descriptorOpens'] == 4 and
             type(lifetime['descriptorCloses']) is int and lifetime['descriptorCloses'] == 4 and
             all(lifetime[k] is True for k in ('firstInitCancelRejected', 'overlapDisposalPreservedB', 'abortSticky')),
             'actual_owned_descriptor_lifetime')
    need(set(v) == keys and v['status'] == 'module_prequalification_observed' and v['actualModuleBound'] is True and
         v['rounds'] == 3 and type(v['samples']) is int and v['samples'] == (110 if os_name == 'windows' else 102) and
         v['disposeCalls'] == (4 if os_name == 'windows' else 2) and v['sampleAfterDisposeRefused'] is True, 'closed_actual_module_result')
    need(all(v[k] is False for k in QUALIFICATIONS), 'qualification_grant_refused')
    need(type(v['operationElapsedMs']) in (int, float) and 0 <= v['operationElapsedMs'] < 2000 and
         type(v['jsElapsedMs']) in (int, float) and 0 <= v['jsElapsedMs'] < 25000, 'original_result_budgets')
    checks = {'actualTupleParity', 'causalCounterContainment', 'causalWallContainment', 'actualWallCounterTypes',
              'currentImageHeldFile', 'sampleNonregression', 'samplerBounds', 'stickyDisposal'}
    need(set(v['checks']) == checks and all(v['checks'][k] is True for k in checks), 'actual_module_predicates')
    need(len(v['comparisons']) == 3, 'single_actual_round_count')
    for comparison in v['comparisons']:
        need(set(comparison) == {'outerWidthNs', 'goWidthNs'} and
             all(isinstance(n, str) and re.fullmatch('(0|[1-9][0-9]{0,18})', n) for n in comparison.values()) and
             int(comparison['outerWidthNs']) <= BUDGETS['helperMs'] * 1000000 and int(comparison['goWidthNs']) <= 100000000, 'fixed_parity_bounds')
    need(len(v['datePredicates']) == (3 if os_name == 'windows' else 0), 'separate_windows_date_observations')
    for observation in v['datePredicates']:
        need(set(observation) == {'dateInsideNativeInterval', 'datePreciseDistanceNs'} and
             type(observation['dateInsideNativeInterval']) is bool and
             isinstance(observation['datePreciseDistanceNs'], str) and
             re.fullmatch('(0|[1-9][0-9]{0,18})', observation['datePreciseDistanceNs']) and
             int(observation['datePreciseDistanceNs']) <= MAX and
             observation['dateInsideNativeInterval'] == (observation['datePreciseDistanceNs'] == '0'),
             'date_observation_types')


def failure_reason(error):
    text = str(error)
    return text if isinstance(error, RuntimeError) and re.fullmatch('[a-z0-9_]{1,80}', text) else 'bounded_prequalification_exception'


def stage_case(owned, env, parent_root, version, item, leaves, helper, helper_sha, commit, os_name, arch, origin):
    root = parent_root / ('TEST-' + version); root.mkdir(mode=0o700)
    for directory in ('opencode-plugin',): (root / directory).mkdir(mode=0o700)
    for path in MODULES: shutil.copyfile(REPO / path, root / path); (root / path).chmod(0o600)
    shutil.copyfile(HERE / 'opencode-platform-clock-prequalification.mjs', root / 'fixture.mjs')
    copied_helper = root / helper.name; shutil.copyfile(helper, copied_helper); copied_helper.chmod(0o700)
    archive = fetch_archive(owned, env, root, item)
    extract_image(archive, root, item, os_name, arch, min(owned.end, time.monotonic() + 120))
    case_env = environment(root)
    metadata = {'version': version, 'sourceCommit': commit, 'originCommit': origin,
                'platform': 'win32' if os_name == 'windows' else os_name,
                'arch': 'x64' if arch == 'amd64' else 'arm64', 'bun': VERSIONS[version][1],
                'imageSha256': item['binarySHA256'], 'archiveSha256': item['archiveSHA256'], 'sri': item['sri'],
                'fixtureSha256': sha(root / 'fixture.mjs'), 'helperSha256': helper_sha,
                'moduleLeaves': [x for x in leaves if x['path'] in MODULES],
                'environmentKeys': sorted(case_env), 'jobDeadline': owned.end, 'os': os_name, 'goarch': arch}
    write_json(root / 'metadata.json', metadata)
    return root, metadata


def parent_main(args):
    parent_started = time.monotonic(); stage = 'initial_custody'
    job_end = parent_started + BUDGETS['jobSeconds']
    # Deadline-only watchdog; never a sampling clock or a timing fallback.
    # Cooperative checks additionally refuse every late successful observation.
    watchdog = threading.Timer(BUDGETS['jobSeconds'], _thread.interrupt_main)
    watchdog.daemon = True; watchdog.start()
    owned = Owned(job_end); root = None
    report = {'schema': 1, 'purpose': 'actual_product_platform_clock_module_prequalification',
              'status': 'unqualified', 'qualificationLevel': 'module_prequalification_only',
              'executionOwner': 'parent_ci', 'nativeWorkerExecution': False,
              'platform': args.os, 'arch': args.arch, 'reviewedBase': BASE, 'budgets': BUDGETS,
              'cases': [], 'plannedVersions': ['1.18.33', '2.0.21'] + (['1.18.34'] if (args.os, args.arch) == ('linux', 'amd64') else []),
              'modelCalls': 0, 'providerCalls': 0, 'sessionCreates': 0, 'installerCalls': 0,
              'suspendExperiments': 0, 'qualificationRowsFilled': 0,
              'operationalAssumptions': {'trustedGlobalsAndBuiltins': True, 'immutableProtectedMappings': True,
                'darwinProtectedLibSystemAndDyldLP64LittleEndianStableTimebase': True,
                'windowsProtectedDefaultSystem32Win64APISet': True,
                'linuxTrustedProcMountAndCallingThreadTimeNamespace': True,
                'sampleObservationsAreNotFutureWallRateSuspendOrComparisonBounds': True}, **QUALIFICATIONS}
    try:
        need((args.os, args.arch) in CELLS, 'planned_native_cell')
        if args.v2_helper_admission_canary:
            need((args.os, args.arch) in [('windows', 'amd64'), ('darwin', 'amd64')], 'closed_v2_helper_canary')
            report.update(plannedVersions=['2.0.21'], purpose='v2_helper_admission_fixture_canary', diagnosticOnly=True)
        actual_os = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}.get(platform.system())
        actual_arch = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}.get(platform.machine())
        need((actual_os, actual_arch) == (args.os, args.arch), 'actual_host_no_uname_reinterpretation')
        expected = os.environ.get('SOURCE_COMMIT', '')
        commit_match(expected, expected)  # Fail before any download/build on malformed purpose-specific input.
        root = private_root(args.temp_base); env = environment(root, tooling=True)
        commit = checkout(owned, env, expected)
        report.update(expectedSourceCommit=expected, actualGitHEAD=commit, cleanTrackedCheckout=True)
        leaves = source_closure(owned, env, commit, args.os, root)
        report.update(checkoutLeaves=leaves, checkoutClosureSHA256=digest(canonical(leaves)))
        raw_inputs = (args.closed_inputs.read_bytes() if args.closed_inputs else github(owned, env,
            'repos/777genius/agent-notifications/contents/scripts/testdata/opencode-clock-qualification/closed-inputs.json?ref=' + API_INPUT_COMMIT, True))
        value = closed_inputs(parse(raw_inputs))
        write_json(root / 'reviewed-closed-inputs.json', value)
        report['closedPrimaryInputs'] = primary_sources(owned, env, root, value)
        stage = 'go_build_source_closure'
        helper, build = build_helper(owned, env, root, commit, args.os, args.arch)
        stage = 'cases'
        report['goBuildClosure'] = build
        report['goBuildClosureSHA256'] = digest(canonical(build))
        checkout(owned, env, expected)
        staged = []
        for version in report['plannedVersions']:
            case = {'version': version, 'status': 'unqualified', **QUALIFICATIONS}; report['cases'].append(case)
            try:
                origin = origin_tag(owned, env, version)
                if version == '1.18.34':
                    pins = (REPO / 'opencode-plugin/clock-cells.mjs').read_text()
                    pin = re.search(r"\['1\.18\.34', '([a-f0-9]{64})'\]", pins)
                    need(pin, 'existing_current_linux_image_pin')
                    staged_current = args.current_primary.read_bytes() if args.current_primary else os.environ.get('PLATFORM_CLOCK_CURRENT_PRIMARY_JSON', '').encode()
                    need(staged_current, 'separately_staged_current_primary_required')
                    item = current_primary(parse(staged_current), pin[1])
                    write_json(root / 'current-primary.json', item)
                    package_raw = github(owned, env, 'repos/anomalyco/opencode/contents/packages/opencode/package.json?ref=' + origin, True)
                    need(digest(canonical(parse(package_raw))) == CURRENT_PACKAGE, 'actual_current_package_source')
                    (root / 'current-package.private.json').write_bytes(package_raw)
                    # Bind actual current build/flags/entry leaves separately; never
                    # substitute floor-source bodies for the current origin tag.
                    current_sources = []
                    for path in ('packages/opencode/script/build.ts', 'packages/opencode/src/effect/runtime-flags.ts', 'packages/opencode/src/plugin/index.ts'):
                        raw = github(owned, env, 'repos/anomalyco/opencode/contents/' + path + '?ref=' + origin, True)
                        current_sources.append({'repo': 'anomalyco/opencode', 'ref': origin, 'path': path,
                                                'sha256': digest(raw), 'bytes': len(raw)})
                        (root / ('current-' + str(len(current_sources)) + '.private')).write_bytes(raw)
                    case.update(currentPrimarySHA256=digest(canonical(item)), currentPackageSourceSHA256=digest(package_raw),
                                currentOriginSources=current_sources)
                else:
                    matches = [x for x in value['artifacts'] if (x['version'], x['os'], x['arch']) ==
                               (version, args.os, 'x64' if args.arch == 'amd64' else 'arm64')]
                    need(len(matches) == 1, 'one_closed_official_image'); item = matches[0]
                case_root, metadata = stage_case(owned, env, root, version, item, leaves, helper, build['binarySHA256'], commit, args.os, args.arch, origin)
                staged.append((case, case_root, metadata))
            except Exception as error:
                case['failureReason'] = failure_reason(error)
        for case, case_root, metadata in staged:
            try:
                if args.os == 'linux':
                    # Privilege is confined to one owned TEST subprocess AFTER all
                    # source checkout, fetching and build, with closed HOME/env.
                    clean = environment(case_root)
                    command = ['sudo', '-n', '/usr/bin/env', '-i', *[k + '=' + v for k, v in sorted(clean.items())],
                               str(Path(sys.executable).resolve()), str(Path(__file__).resolve()), '--isolated-cell', str(case_root)]
                    out = owned.command(command, case_root, clean, 35, 65536)
                    result = parse(out, 65536)
                else:
                    result = run_case(case_root, metadata, args.os, args.arch, job_end)
                case.update(result)
            except Exception as error:
                case.update(status='unqualified', failureReason=failure_reason(error))
        checkout(owned, env, expected)
        need(all(sha(REPO / x['path']) == x['actualSHA256'] for x in leaves), 'final_checkout_source_closure')
        need(len(report['cases']) == len(report['plannedVersions']) and
             all(x['status'] == 'module_prequalification_observed' for x in report['cases']), 'all_planned_actual_cases_required')
        remaining(job_end)
        report['status'] = 'module_prequalification_observed'
    except Exception as error:
        report['failureReason'] = failure_reason(error)
        report['failureObservation'] = {'stage': stage, 'parentElapsedMs': round((time.monotonic() - parent_started) * 1000, 3)}
        if hasattr(error, 'path_failure'): report['pathFailure'] = error.path_failure
    except KeyboardInterrupt:
        report.update(status='unqualified', failureReason='absolute_deadline' if time.monotonic() >= job_end else 'parent_interrupted')
    finally:
        watchdog.cancel()
        owned.cleanup(); report['parentOwnership'] = owned.summary()
        report['helperStarts'] = sum(c.get('helperStarts', 0) for c in report['cases'])
        report['helperActualCloses'] = sum(c.get('helperActualCloses', 0) for c in report['cases'])
        report['maxOwnedWeightIncludingSupervisor'] = max(
            [owned.highwater] + [c.get('maxOwnedWeight', 0) + (1 if args.os == 'linux' else 0) for c in report['cases']])
        report['allOwnedHandlesClosed'] = not owned.live and all(c.get('allOwnedHandlesClosed') is True for c in report['cases'])
        if report['status'] == 'module_prequalification_observed' and not (
                report['helperStarts'] == report['helperActualCloses'] == 3 * len(report['plannedVersions']) and
                report['allOwnedHandlesClosed'] and report['maxOwnedWeightIncludingSupervisor'] <= 4):
            report.update(status='unqualified', failureReason='cell_lifecycle_counts')
        if owned.live: report['status'] = 'unqualified'
        if time.monotonic() >= job_end:
            report.update(status='unqualified', failureReason='absolute_deadline')
        # No raw stdout, UUID, domain IDs, ticks, PIDs, paths or native log dump.
        write_json(args.report, report)
        if time.monotonic() >= job_end and report['status'] != 'unqualified':
            report.update(status='unqualified', failureReason='absolute_deadline')
            late = args.report.with_name(args.report.name + '.owned-late')
            write_json(late, report); os.replace(late, args.report)
    return 0 if report['status'] == 'module_prequalification_observed' else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute-ci', action='store_true')
    parser.add_argument('--v2-helper-admission-canary', action='store_true')
    parser.add_argument('--isolated-cell', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--os', choices=('linux', 'darwin', 'windows'))
    parser.add_argument('--arch', choices=('amd64', 'arm64'))
    parser.add_argument('--temp-base', type=Path)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--closed-inputs', type=Path)
    parser.add_argument('--current-primary', type=Path)
    args = parser.parse_args(); os.umask(0o077)
    if args.isolated_cell:
        root = canonical_path(args.isolated_cell)
        need(root.name.startswith('TEST-') and root.parent.name.startswith('TEST-pclock-'), 'owned_isolated_cell_path')
        metadata = load(root / 'metadata.json')
        need(metadata['os'] == 'linux' and (metadata['os'], metadata['goarch']) in CELLS and
             set(os.environ) == set(environment(root)), 'owned_isolated_minimal_environment')
        commit_match(metadata['sourceCommit'], metadata['sourceCommit'])
        isolate_linux()
        result = run_case(root, metadata, 'linux', metadata['goarch'], metadata['jobDeadline'])
        sys.stdout.buffer.write(canonical(result) + b'\n')
        return 0  # Parent interprets explicit status, never exit-code-as-proof.
    need(args.execute_ci and args.os and args.arch and args.temp_base and args.report, 'explicit_parent_ci_execution_required')
    need(os.environ.get('GITHUB_ACTIONS') == 'true', 'parent_github_actions_execution_only')
    return parent_main(args)


if __name__ == '__main__':
    raise SystemExit(main())
