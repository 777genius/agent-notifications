#!/usr/bin/env python3
"""Parent-only disposable native API prequalification; never a TimePolicy grant."""
import argparse
import base64
import ctypes
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import secrets
import shutil
import signal
import socket
import subprocess
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

VERSIONS = {'1.18.33': ('51ef4be1d3c122f18fefb510dca8d778571f4f18', '1.3.14'),
            '2.0.21': ('8a8bd622a3d7dc29ccf30ec17f84e363ed95ed72', '1.4.2')}
PINS = {'anomalyco/opencode': {v[0] for v in VERSIONS.values()},
        'oven-sh/bun': {'0d9b296af33f2b851fcbf4df3e9ec89751734ba4', '744846f844374847c902b5e7fd59b4342a51ef99'},
        'oven-sh/WebKit': {'5488984d20e0dbfe4be2c3ba8fb18eb81a5e0e8b', '2e2aa2290fac856d6f451ceacb58f7f5b44dd057'},
        'Effect-TS/effect': {'2600f62f4532026928454dcea8d1c48557b3f942'},
        'apple-oss-distributions/Libc': {'71bbe350ab79eef58113991d817ccc6165061a64'},
        'apple-oss-distributions/xnu': {'f6217f891ac0bb64f3d375211650a4c1ff8ca1ea'},
        'torvalds/linux': {'e8f897f4afef0031fe618a8e94127a0934896aba'}}
FLAGS = {k: '1' for k in ('OPENCODE_DISABLE_AUTOUPDATE', 'OPENCODE_DISABLE_DEFAULT_PLUGINS',
                         'OPENCODE_DISABLE_MODELS_FETCH', 'OPENCODE_DISABLE_PROJECT_CONFIG')}
HEX = re.compile(r'^[0-9a-f]{64}$')
UUID = re.compile(r'^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$')
MAX = 9223372036854775807
HERE = Path(__file__).resolve().parent
REPO = HERE.parent
INPUTS = HERE / 'testdata/opencode-clock-qualification/closed-inputs.json'


def need(value, reason):
    if not value:
        raise RuntimeError(reason)


def require_workflow_commit(commit, report):
    expected = os.environ.get('CLOCK_SOURCE_COMMIT', '')
    observed_valid = re.fullmatch(r'[0-9a-f]{40}', commit) is not None
    expected_valid = re.fullmatch(r'[0-9a-f]{40}', expected) is not None
    report.update(observedCommit=commit if observed_valid else None,
                  expectedCommit=expected if expected_valid else None)
    need(observed_valid and expected_valid and commit == expected, 'exact_workflow_ref_required')


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def write_json(path, value):
    with Path(path).open('x', encoding='utf-8') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')
    Path(path).chmod(0o600)


def unique(pairs):
    result = {}
    for k, v in pairs:
        need(k not in result, 'duplicate_json_key')
        result[k] = v
    return result


def load(path, limit=65536):
    need(not Path(path).is_symlink() and Path(path).stat().st_size <= limit, 'private_json_limit_or_symlink')
    return json.loads(Path(path).read_bytes(), object_pairs_hook=unique)


def command(argv, cwd, env=None, timeout=60):
    # Preparation commands only; stdout never copied into public failure messages.
    if argv[0] == 'git': argv = [argv[0], '-c', 'safe.directory=' + str(REPO), *argv[1:]]
    proc = subprocess.run(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    need(proc.returncode == 0 and len(proc.stdout) <= 4 * 1024 * 1024, 'preparation_command_failed')
    return proc.stdout


def windows_bootstrap():
    # kernel32 is already loaded by the OS/Python. Verify its actual module origin.
    from ctypes import wintypes as w
    k = ctypes.WinDLL('kernel32.dll', use_last_error=True)
    k.GetModuleFileNameW.argtypes = [w.HMODULE, w.LPWSTR, w.DWORD]
    k.GetSystemDirectoryW.argtypes = [w.LPWSTR, w.UINT]
    buffer = ctypes.create_unicode_buffer(32768)
    n = k.GetSystemDirectoryW(buffer, len(buffer))
    need(0 < n < len(buffer), 'os_system_directory_failed')
    directory = Path(buffer.value)
    n = k.GetModuleFileNameW(k._handle, buffer, len(buffer))
    need(0 < n < len(buffer) and Path(buffer.value).resolve() == (directory / 'kernel32.dll').resolve(), 'loaded_kernel32_origin_mismatch')
    k.GetCurrentProcess.restype = w.HANDLE
    return k, { 'directory': str(directory), 'kernel32': str(directory / 'kernel32.dll'),
                'ntdll': str(directory / 'ntdll.dll'), 'kernel32Sha256': sha(directory / 'kernel32.dll'),
                'ntdllSha256': sha(directory / 'ntdll.dll') }


def private_root(base):
    base = Path(base).absolute()
    need(base.is_dir(), 'existing_temp_base_required')
    for p in (base, *base.parents):
        need(not p.is_symlink(), 'test_symlink_ancestry_rejected')
    need(not base.resolve().is_relative_to(REPO), 'test_execution_outside_checkout_required')
    root = Path(tempfile.mkdtemp(prefix='TEST-opencode-clock-', dir=base))
    root.chmod(0o700)
    if os.name == 'nt':
        from ctypes import wintypes as w
        k, system = windows_bootstrap()
        adv = ctypes.WinDLL('advapi32.dll', use_last_error=True)
        adv.OpenProcessToken.argtypes = [w.HANDLE, w.DWORD, ctypes.POINTER(w.HANDLE)]
        adv.GetTokenInformation.argtypes = [w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD, ctypes.POINTER(w.DWORD)]
        adv.ConvertSidToStringSidW.argtypes = [ctypes.c_void_p, ctypes.POINTER(w.LPWSTR)]
        k.CloseHandle.argtypes = [w.HANDLE]; k.LocalFree.argtypes = [ctypes.c_void_p]
        token, length, text_sid = w.HANDLE(), w.DWORD(), w.LPWSTR()
        need(adv.OpenProcessToken(k.GetCurrentProcess(), 8, ctypes.byref(token)), 'private_acl_token_failed')
        try:
            adv.GetTokenInformation(token, 1, None, 0, ctypes.byref(length))
            need(0 < length.value <= 4096, 'private_acl_token_size')
            data = ctypes.create_string_buffer(length.value)
            need(adv.GetTokenInformation(token, 1, data, length.value, ctypes.byref(length)), 'private_acl_identity_failed')
            sid = ctypes.cast(data, ctypes.POINTER(ctypes.c_void_p))[0]
            need(adv.ConvertSidToStringSidW(sid, ctypes.byref(text_sid)), 'private_acl_sid_failed')
            command([str(Path(system['directory']) / 'icacls.exe'), str(root), '/inheritance:r', '/grant:r',
                     '*' + text_sid.value + ':(OI)(CI)F'], root, timeout=10)
        finally:
            if text_sid: k.LocalFree(ctypes.cast(text_sid, ctypes.c_void_p))
            k.CloseHandle(token)
    else:
        need(root.stat().st_mode & 0o777 == 0o700 and root.stat().st_uid == os.getuid(), 'private_root_permissions')
    return root


def environment(root):
    env = {'CI': 'true', 'NO_COLOR': '1', 'NPM_CONFIG_OFFLINE': 'true',
           'NPM_CONFIG_FETCH_RETRIES': '0', **FLAGS, 'CLOCK_TEST_ROOT': str(root)}
    paths = {'HOME': 'home', 'USERPROFILE': 'home', 'XDG_CONFIG_HOME': 'config', 'XDG_DATA_HOME': 'data',
             'XDG_CACHE_HOME': 'cache', 'XDG_STATE_HOME': 'state', 'XDG_RUNTIME_DIR': 'run',
             'TMPDIR': 'tmp', 'TEMP': 'tmp', 'TMP': 'tmp', 'BUN_INSTALL_CACHE_DIR': 'bun-cache',
             'OPENCODE_CONFIG_DIR': 'opencode-config'}
    for key, sub in paths.items():
        p = root / sub
        p.mkdir(mode=0o700, exist_ok=True)
        env[key] = str(p)
    if os.name == 'nt':
        _, system = windows_bootstrap()
        env['SystemRoot'] = str(Path(system['directory']).parent)  # OS-authenticated, never inherited.
    return env


def source_manifest(path, root):
    value = load(path)
    # Dispatch may supply the same closed primary data. Candidate fixture hashes are
    # computed externally/from checkout; never embed self-referential script hashes.
    need(value == load(INPUTS) and set(value) == {'schema', 'inputHashes', 'versions', 'sources', 'artifacts'} and
         value['schema'] == 2, 'fixed_closed_inputs_required')
    need(len(value['sources']) == 21 and len(value['artifacts']) == 10, 'complete_primary_inputs_required')
    closure = root / 'source-closure'; closure.mkdir(mode=0o700)
    seen = set()
    for i, s in enumerate(value['sources']):
        need(set(s) == {'repo', 'ref', 'path', 'sha256', 'bytes'} and s['repo'] in PINS and s['ref'] in PINS[s['repo']] and
             HEX.fullmatch(s['sha256']) and 0 < s['bytes'] <= 4 * 1024 * 1024 and
             re.fullmatch(r'[A-Za-z0-9_./-]+', s['path']) and not PurePosixPath(s['path']).is_absolute() and
             '..' not in PurePosixPath(s['path']).parts, 'fixed_primary_source_required')
        identity = (s['repo'], s['ref'], s['path'])
        need(identity not in seen, 'duplicate_source_identity'); seen.add(identity)
        raw = command(['gh', 'api', f"repos/{s['repo']}/contents/{s['path']}?ref={s['ref']}",
                       '-H', 'Accept: application/vnd.github.raw+json'], root, timeout=30)
        need(len(raw) == s['bytes'] and hashlib.sha256(raw).hexdigest() == s['sha256'], 'primary_source_hash_mismatch')
        (closure / str(i)).write_bytes(raw)
    for version, (commit, bun) in VERSIONS.items():
        need(value['versions'][version]['commit'] == commit and value['versions'][version]['bun'] == bun, 'fixed_source_version_required')
    return sha(path), {version: {role: [value['sources'][i]['sha256'] for i in indexes]
                                for role, indexes in v['bindings'].items()}
                       for version, v in value['versions'].items()}, value['artifacts']


def native_header(exe, os_name, arch):
    with exe.open('rb') as stream: header = stream.read(4096)
    if os_name == 'linux':
        valid = (header[:6] == b'\x7fELF\x02\x01' and
                 int.from_bytes(header[18:20], 'little') == (62 if arch == 'amd64' else 183))
    elif os_name == 'darwin':
        valid = (header[:4] == b'\xcf\xfa\xed\xfe' and
                 int.from_bytes(header[4:8], 'little') == (0x1000007 if arch == 'amd64' else 0x100000c))
    else:
        offset = int.from_bytes(header[60:64], 'little')
        valid = (arch == 'amd64' and header[:2] == b'MZ' and 64 <= offset <= len(header) - 26 and
                 header[offset:offset+4] == b'PE\0\0' and header[offset+4:offset+6] == b'\x64\x86' and
                 header[offset+24:offset+26] == b'\x0b\x02')
    need(valid, 'actual_native_architecture_header_required')


def official_image(root, version, os_name, arch, report, identities):
    matches = [a for a in identities if (a['version'], a['os'], a['arch']) ==
               (version, os_name, 'x64' if arch == 'amd64' else 'arm64')]
    need(len(matches) == 1, 'one_frozen_primary_image_required'); item = matches[0]
    name = f"{'@opencode/cli' if version == '2.0.21' else 'opencode'}-{os_name}-{item['arch']}"
    url = f"https://registry.npmjs.org/{name}/-/{name.split('/')[-1]}-{version}.tgz"
    need(item['name'] == name and item['archive'] == url, 'exact_primary_package_url_required')
    # Source tags exist for both generations. No nonexistent V2 release dependency.
    obj = json.loads(command(['gh', 'api', f'repos/anomalyco/opencode/git/ref/tags/v{version}'], root))['object']
    for _ in range(4):
        need(re.fullmatch(r'[0-9a-f]{40}', obj['sha']), 'canonical_source_ref_required')
        if obj['type'] == 'commit': break
        need(obj['type'] == 'tag', 'source_commit_object_required')
        obj = json.loads(command(['gh', 'api', f"repos/anomalyco/opencode/git/tags/{obj['sha']}"], root))['object']
    need(obj['type'] == 'commit' and obj['sha'] == VERSIONS[version][0], 'fixed_image_source_commit_mismatch')
    archive = root / 'package.tgz'
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    end = time.monotonic() + 120
    with opener.open(item['archive'], timeout=15) as response, archive.open('xb') as out:
        need(response.status == 200, 'primary_archive_http_status')
        total = 0
        while block := response.read(1024 * 1024):
            total += len(block)
            need(total <= 512 * 1024 * 1024 and time.monotonic() < end, 'primary_archive_download_bound')
            out.write(block)
    with archive.open('rb') as stream:
        sri = 'sha512-' + base64.b64encode(hashlib.file_digest(stream, 'sha512').digest()).decode()
    # SRI is checked BEFORE tar parsing/extraction; never npm install or fallback.
    need(sri == item['sri'] and sha(archive) == item['archiveSHA256'], 'primary_archive_sri_sha_mismatch')
    exe = root / ('opencode.exe' if os_name == 'windows' else 'opencode')
    with tarfile.open(archive, 'r:gz') as tar:
        members = []; total = count = 0
        for member in tar:
            count += 1; total += member.size
            path = PurePosixPath(member.name)
            need(count <= 128 and total <= 768 * 1024 * 1024 and time.monotonic() < end and
                 not path.is_absolute() and '..' not in path.parts and '\\' not in member.name and
                 (member.isfile() or member.isdir()), 'bounded_regular_primary_tar_required')
            if path.name == exe.name: members.append(member)
        need(len(members) == 1 and members[0].name == 'package/bin/' + exe.name and members[0].isfile() and
             0 < members[0].size <= 512 * 1024 * 1024, 'unique_regular_primary_bin_required')
        with tar.extractfile(members[0]) as source, exe.open('xb') as out:
            remaining = members[0].size
            while remaining:
                block = source.read(min(1024 * 1024, remaining))
                need(block and time.monotonic() < end, 'bounded_primary_extract_required')
                out.write(block); remaining -= len(block)
    need(sha(exe) == item['binarySHA256'], 'frozen_primary_binary_sha_mismatch')
    native_header(exe, os_name, arch); exe.chmod(0o700)
    report.update(package=name, sri=sri, archiveSha256=item['archiveSHA256'], imageSha256=sha(exe),
                  imageSourceCommit=obj['sha'], nativeArchitectureHeaderVerified=True)
    return exe


def linux_network(root):
    # Existing private-netns contract, entered only AFTER all fetch/build preparation.
    before = os.readlink('/proc/self/ns/net')
    libc = ctypes.CDLL(None, use_errno=True); libc.unshare.argtypes = [ctypes.c_int]
    need(libc.unshare(0x40000000) == 0, 'fresh_private_netns_unavailable')
    need(os.readlink('/proc/self/ns/net') != before and
         os.readlink('/proc/self/ns/net') != os.readlink('/proc/1/ns/net') and
         {name for _, name in socket.if_nameindex()} == {'lo'}, 'fresh_loopback_only_netns_required')
    command(['/usr/sbin/ip', 'link', 'set', 'lo', 'up'], root, env={'PATH': '/usr/sbin:/usr/bin:/bin'}, timeout=5)


class Owned:
    """Keep handles until actual native reap AND EOF; timeout/kill is never inner proof."""
    def __init__(self):
        self.live = {}; self.started = self.closed = self.highwater = 0; self.helper_hashes = []

    def launch(self, argv, root, env, helper=False):
        need(len(self.live) < 4 and (not helper or sum(x['helper'] for x in self.live.values()) < 3), 'owned_slot_limit')
        p = subprocess.Popen(argv, cwd=root / 'project', env=env, stdin=subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=os.name != 'nt',
                             creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
        state = {'p': p, 'helper': helper, 'buffers': [bytearray(), bytearray()], 'threads': [], 'overflow': False, 'pipeError': False}
        self.live[p.pid] = state; self.highwater = max(self.highwater, len(self.live))
        if helper: self.started += 1
        cap = 4096 if helper else 512 * 1024
        for index, stream in enumerate((p.stdout, p.stderr)):
            def drain(stream=stream, index=index):
                try:
                    with (root / f"{'helper' if helper else 'host'}-{p.pid}-{index}.private").open('xb') as raw:
                        for block in iter(lambda: stream.read(512), b''):
                            room = max(0, cap - len(state['buffers'][index]))
                            state['buffers'][index].extend(block[:room]); raw.write(block[:room])
                            if len(block) > room: state['overflow'] = True
                except Exception: state['pipeError'] = True
            t = threading.Thread(target=drain, daemon=True); state['threads'].append(t); t.start()
        return state

    def stop(self, s, deadline=None, terminate=True):
        p = s['p']
        # Natural helper reap/EOF shares its original absolute deadline. Cleanup has
        # a separate bounded budget and can never qualify bytes from a timed-out call.
        end = deadline if deadline is not None else time.monotonic() + 5
        def remaining():
            left = end - time.monotonic()
            need(left > 0, 'owned_close_deadline'); return left
        if terminate and p.poll() is None:
            if os.name == 'nt': p.terminate()
            else:
                need(os.getpgid(p.pid) == p.pid, 'owned_process_group_changed')
                os.killpg(p.pid, signal.SIGTERM)
            try: p.wait(timeout=min(2, remaining()))
            except subprocess.TimeoutExpired:
                if os.name == 'nt': p.kill()
                else: os.killpg(p.pid, signal.SIGKILL)
        p.wait(timeout=remaining())
        for t in s['threads']: t.join(timeout=remaining())
        need(not any(t.is_alive() for t in s['threads']), 'owned_pipe_eof_missing')
        p.stdout.close(); p.stderr.close()
        remaining()  # Unproved/late closure retains ownership until cleanup reobserves it.
        self.live.pop(p.pid)
        if s['helper']: self.closed += 1

    def helper(self, exe, root, env, deadline, expected_sha):
        end = min(deadline, time.monotonic() + .224)  # helper224ms != whole operation2s
        need(sha(exe) == expected_sha and time.monotonic() < end, 'copied_helper_sha_or_deadline')
        s = self.launch([str(exe), 'opencode-clock', '--protocol', '1'], root, env, True)
        try:
            self.stop(s, end, terminate=False)
            need(s['p'].returncode == 0 and not s['overflow'] and not s['pipeError'] and not s['buffers'][1], 'actual_helper_close_output_failure')
            raw = bytes(s['buffers'][0]); decode_helper(raw)
            self.helper_hashes.append(hashlib.sha256(raw).hexdigest())
            need(time.monotonic() < end, 'helper_original_deadline')
            return raw
        finally:
            if s['p'].pid in self.live: self.stop(s)  # failure cleanup, never output proof


def decode_helper(raw):
    need(0 < len(raw) <= 1024, 'go_renderer_limit')  # current Go renderer is stricter than transport4096.
    value = json.loads(raw, object_pairs_hook=unique)
    keys = ['protocol', 'boot', 'clockDomain', 'clockKind', 'monoLoNs', 'monoHiNs', 'wallUnixNs', 'uncertaintyNs']
    need(list(value) == keys and type(value['protocol']) is int and value['protocol'] == 1 and
         (json.dumps(value, separators=(',', ':'), ensure_ascii=True) + '\n').encode() == raw, 'canonical_closed_helper_required')
    need(UUID.fullmatch(value['boot']) and value['boot'] != '00000000-0000-0000-0000-000000000000', 'canonical_boot_required')
    kind, domain = value['clockKind'], value['clockDomain']
    need((kind == 'linux-boottime' and re.fullmatch(r'linux-time:[1-9][0-9]*:[1-9][0-9]*', domain) and
          all(int(x) <= 18446744073709551615 for x in domain.split(':')[1:])) or
         (kind, domain) in [('darwin-monotonic-raw', 'darwin-kernel'), ('windows-interrupt-precise', 'windows-kernel')], 'closed_native_domain_required')
    ns = []
    for key in keys[4:]:
        need(isinstance(value[key], str) and re.fullmatch(r'(0|[1-9][0-9]{0,18})', value[key]) and int(value[key]) <= MAX, 'canonical_int64_required')
        ns.append(int(value[key]))
    lo, hi, _, error = ns
    need(0 <= hi - lo <= 100000000 and error == hi - lo + 3000000 and error <= 103000000, 'original_go_bracket_required')
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args): raise RuntimeError('native_redirect_rejected')


def get(base, path, headers, timeout):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with opener.open(urllib.request.Request(base + path, headers=headers), timeout=timeout) as response:
        raw = response.read(1024 * 1024 + 1)
        need(len(raw) <= 1024 * 1024, 'native_http_limit')
        v = json.loads(raw)
        return v.get('data', v) if isinstance(v, dict) else v


def rows(root):
    p = root / 'loader-private.jsonl'
    if not p.exists(): return []
    need(p.stat().st_size <= 32768 and not p.is_symlink(), 'loader_trace_limit')
    result = [json.loads(line, object_pairs_hook=unique) for line in p.read_bytes().splitlines()]
    need(len(result) <= 2, 'loader_trace_count')
    return result


def live_image(proc, target):
    need(proc.poll() is None, 'native_host_not_live')
    if sys_platform() == 'linux':
        image = Path(f'/proc/{proc.pid}/exe')
        need(os.readlink(image) == str(target) and image.stat().st_ino == target.stat().st_ino and
             image.stat().st_dev == target.stat().st_dev and sha(image) == sha(target), 'kernel_live_image_mismatch')
    elif sys_platform() == 'darwin':
        lib = ctypes.CDLL('/usr/lib/libproc.dylib'); b = ctypes.create_string_buffer(4096)
        lib.proc_pidpath.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
        need(lib.proc_pidpath(proc.pid, b, len(b)) > 0 and Path(os.fsdecode(b.value)).resolve() == target, 'kernel_live_image_mismatch')
    else:
        from ctypes import wintypes as w
        k, _ = windows_bootstrap(); k.QueryFullProcessImageNameW.argtypes = [w.HANDLE, w.DWORD, w.LPWSTR, ctypes.POINTER(w.DWORD)]
        b = ctypes.create_unicode_buffer(32768); n = w.DWORD(len(b))
        need(k.QueryFullProcessImageNameW(w.HANDLE(int(proc._handle)), 0, b, ctypes.byref(n)) and Path(b.value).resolve() == target, 'kernel_live_image_mismatch')
    need(proc.poll() is None, 'native_host_exited_during_identity')


def sys_platform():
    return {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}.get(platform.system())


def run_version(root, version, args, helper, helper_sha, manifest_sha, ownership, report, exe):
    (root / 'project').mkdir(mode=0o700)
    env = environment(root)
    copied_helper = root / ('helper.exe' if args.os == 'windows' else 'helper')
    shutil.copyfile(helper, copied_helper); copied_helper.chmod(0o700)
    plugin_dir = root / 'opencode-config' / 'plugins'; plugin_dir.mkdir(mode=0o700)
    module = plugin_dir / 'clock-qualification.js'
    shutil.copyfile(HERE / 'opencode-clock-native-qualification.mjs', module); module.chmod(0o600)
    v2 = version == '2.0.21'
    metadata = {'executable': str(exe), 'imageSha256': sha(exe), 'parentPID': os.getpid(),
                'platform': 'win32' if args.os == 'windows' else args.os, 'arch': 'x64' if args.arch == 'amd64' else 'arm64',
                'moduleSha256': sha(module), 'bun': VERSIONS[version][1], 'branch': 'v2' if v2 else 'v1',
                'sourceManifestSha256': manifest_sha}
    if args.os == 'windows': metadata['windows'] = windows_bootstrap()[1]
    write_json(root / 'metadata.json', metadata)
    config = ({'update': 'disable', 'share': 'disabled', 'warming': False, 'formatter': False, 'lsp': False,
               'websearch': False, 'permissions': [{'action': '*', 'resource': '*', 'effect': 'deny'}], 'providers': {}}
              if v2 else {'permission': {'*': 'deny'}, 'provider': {}})
    write_json(root / 'project' / 'opencode.json', config); env['OPENCODE_CONFIG'] = str(root / 'project' / 'opencode.json')
    password = secrets.token_urlsafe(32)
    env['OPENCODE_PASSWORD' if v2 else 'OPENCODE_SERVER_PASSWORD'] = password
    headers = {'Authorization': 'Basic ' + base64.b64encode(('opencode:' + password).encode()).decode()}
    helper_env = {k: v for k, v in env.items() if k in {'HOME', 'USERPROFILE', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME',
                  'XDG_CACHE_HOME', 'XDG_STATE_HOME', 'TMPDIR', 'TEMP', 'TMP', 'SystemRoot'}}
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
    host = ownership.launch([str(exe), 'serve', '--port', str(port), '--hostname', '127.0.0.1'], root, env)
    try:
        url = f'http://127.0.0.1:{port}'; deadline = time.monotonic() + 35
        while time.monotonic() < deadline:
            need(host['p'].poll() is None and not host['overflow'] and not host['pipeError'], 'host_exited_or_log_overflow')
            try:
                info = get(url, '/api/info' if v2 else '/global/health', headers, min(1, deadline - time.monotonic()))
                need(isinstance(info, dict) and info.get('version') == version and
                     (info.get('pid') == host['p'].pid if v2 else info.get('healthy') is True), 'native_health_identity_mismatch')
                break
            except urllib.error.HTTPError: raise
            except (OSError, urllib.error.URLError): time.sleep(.05)
        else: raise RuntimeError('native_readiness_deadline')
        # These read-only public location ports are input-proved by the Linux P0 source fixture.
        trigger = '/api/plugin?' + urllib.parse.urlencode({'location[directory]': str(root / 'project')}) if v2 else '/config?' + urllib.parse.urlencode({'directory': str(root / 'project')})
        get(url, trigger, headers, 8)
        deadline = time.monotonic() + 5
        while not (root / 'clock-ready').exists() and time.monotonic() < deadline:
            need(host['p'].poll() is None and not any(r.get('kind') == 'clock_result' for r in rows(root)), 'native_ffi_preparation_gap')
            time.sleep(.01)
        need((root / 'clock-ready').is_file() and (root / 'clock-ready').read_bytes() == b'ready\n', 'native_preparation_deadline')
        loader = rows(root)
        need(len(loader) == 1 and loader[0]['kind'] == 'loader' and loader[0]['pid'] == host['p'].pid and
             loader[0]['ppid'] == os.getpid() and loader[0]['execPath'] == str(exe) and
             loader[0]['imageSha256'] == report['imageSha256'] and loader[0]['moduleSha256'] == sha(module) and
             loader[0]['sourceManifestSha256'] == manifest_sha and loader[0]['bunVersion'] == VERSIONS[version][1] and
             loader[0]['publicContextVerified'] is True and loader[0]['platform'] == metadata['platform'] and
             loader[0]['arch'] == metadata['arch'] and loader[0]['branch'] == metadata['branch'], 'public_loader_identity_mismatch')
        live_image(host['p'], exe)
        report['liveImageAndPublicLoaderVerified'] = True
        before_calls = ownership.started; before_closes = ownership.closed
        deadline = time.monotonic() + 2
        (root / 'clock-start').write_bytes(b'start\n')
        for i in range(3):
            request = root / f'clock-request-{i}'; response = root / f'clock-response-{i}.json'
            while not request.exists() and time.monotonic() < deadline:
                need(host['p'].poll() is None and not any(r.get('kind') == 'clock_result' for r in rows(root)), 'clock_fixture_failed')
                time.sleep(.002)
            need(time.monotonic() < deadline and request.is_file() and not request.is_symlink() and
                 request.read_bytes() == f'{i}\n'.encode() and not response.exists(), 'fresh_helper_request_deadline')
            raw = ownership.helper(copied_helper, root, helper_env, deadline, helper_sha)
            need(time.monotonic() < deadline, 'qualification_operation_deadline')
            scratch = root / f'response-{i}.tmp'
            with scratch.open('xb') as out: out.write(raw)
            scratch.chmod(0o600); os.replace(scratch, response)  # ONLY genuine bytes AFTER actual close + EOF.
        while len(rows(root)) < 2 and time.monotonic() < deadline: time.sleep(.002)
        result_rows = rows(root)
        need(time.monotonic() < deadline, 'qualification_operation_deadline')
        need(len(result_rows) == 2, 'bounded_clock_result_required'); result = result_rows[1]
        need(result.get('kind') == 'clock_result' and result.get('pid') == host['p'].pid and
             result.get('status') == 'api_prequalification_passed' and result.get('roundCount') == 3 and
             result.get('productionClockModuleBound') is False and result.get('timePolicyQualified') is False and
             result.get('sourceWallBoundQualified') is False, 'native_api_prequalification_gap')
        mandatory = {'canonicalHelper', 'goDomainBootKind', 'integerRoundtrip', 'pairWidth', 'nativeBracket', 'causalOverlap', 'wallContinuity', 'monotonicNonregression'}
        mandatory |= {'procfs', 'nsfs', 'bigintFs', 'bootGrammar', 'currentThreadDomain', 'currentCallingThread'} if args.os == 'linux' else {'machTimebaseAndSysctl'} if args.os == 'darwin' else {'system32ApiSet', 'ntBootExact32', 'voidUnsigned100ns', 'goPreciseWallContained'}
        need(all(result.get('checks', {}).get(k) is True for k in mandatory) and
             ownership.started - before_calls == ownership.closed - before_closes == 3 and
             sha(exe) == report['imageSha256'] and sha(copied_helper) == helper_sha and sha(module) == metadata['moduleSha256'] and not host['overflow'] and not host['pipeError'], 'native_checks_or_actual_close_incomplete')
        bounds = result.get('aggregate', {})
        need(set(bounds) == {'maxPairWidthNs', 'maxOuterWidthNs', 'maxGoWidthNs', 'maxDatePreciseDistanceNs', 'preciseComparisons'}, 'closed_safe_bounds_required')
        for k in set(bounds) - {'preciseComparisons'}:
            need(isinstance(bounds[k], str) and re.fullmatch(r'(0|[1-9][0-9]{0,18})', bounds[k]) and int(bounds[k]) <= MAX, 'safe_numeric_bound_required')
        need(int(bounds['maxPairWidthNs']) <= (110000000 if args.os == 'linux' else 100000000) and
             int(bounds['maxGoWidthNs']) <= 100000000 and int(bounds['maxOuterWidthNs']) <= 2000000000 and
             type(bounds['preciseComparisons']) is int and (bounds['preciseComparisons'] > 0 if args.os == 'windows' else bounds['preciseComparisons'] == 0), 'observed_bound_limits')
        if args.os == 'windows':
            need(all(type(result['checks'].get(k)) is bool for k in ('dateInsidePreciseInterval', 'dateWithinTwoMsOfPrecise')), 'actual_date_precise_predicates_required')
        live_image(host['p'], exe)
        need(time.monotonic() < deadline, 'qualification_operation_deadline')
        # Closed safe projection; raw loader/image paths, UUIDs and all clocks remain private.
        safe_result = dict(status='api_prequalification_passed', bun=loader[0]['bunVersion'], roundCount=3,
                      checks={k: result['checks'][k] for k in sorted(mandatory)}, bounds=bounds,
                      datePrecisePredicates={k: result['checks'].get(k) for k in ('dateInsidePreciseInterval', 'dateWithinTwoMsOfPrecise')})
    finally:
        try:
            ownership.stop(host)
            need(not host['overflow'] and not host['pipeError'], 'host_final_pipe_failure')
        finally: headers.clear(); env.clear(); helper_env.clear()
        report['traceSha256'] = sha(root / 'loader-private.jsonl') if (root / 'loader-private.jsonl').exists() else None
    # Includes final identity/hash validation, evidence projection and actual host reap.
    need(time.monotonic() < deadline, 'qualification_operation_deadline')
    report.update(safe_result)
    need(time.monotonic() < deadline, 'qualification_operation_deadline')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--os', choices=('linux', 'darwin', 'windows'))
    parser.add_argument('--arch', choices=('amd64', 'arm64'))
    parser.add_argument('--source-manifest', type=Path, default=INPUTS)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--temp-base', type=Path, default=Path(tempfile.gettempdir()))
    parser.add_argument('--prepare-check', action='store_true', help='private-directory/environment lifecycle only; no host/helper/build/fetch')
    args = parser.parse_args(); os.umask(0o077)
    if args.prepare_check:
        root = private_root(args.temp_base)
        try:
            env = environment(root)
            need(env['HOME'] != os.environ.get('HOME') and 'PATH' not in env and not any(k in env for k in ('GH_TOKEN', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'BUN_OPTIONS', 'LD_PRELOAD')), 'ambient_environment_leak')
            need(all(Path(env[k]).is_relative_to(root) for k in ('HOME', 'XDG_CONFIG_HOME', 'TMPDIR')), 'private_directory_escape')
        finally: shutil.rmtree(root)
        print(json.dumps({'preparationOnly': True, 'privateLifecycle': 'passed', 'nativeExecution': False})); return 0
    need(args.os and args.arch and args.source_manifest and args.report, 'run_arguments_required')
    need(args.os == sys_platform() and args.arch == {'x86_64': 'amd64', 'AMD64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine()) and
         (args.os, args.arch) in [('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'), ('darwin', 'arm64'), ('windows', 'amd64')], 'actual_native_cell_required')
    root = private_root(args.temp_base); ownership = Owned()
    report = {'schema': 1, 'purpose': 'native_clock_api_prequalification', 'status': 'preparation_gap',
              'platform': args.os, 'arch': args.arch, 'productionClockModuleBound': False, 'timePolicyQualified': False,
              'sourceWallBoundQualified': False, 'candidateRQualified': False, 'candidateTQualified': False,
              'modelCalls': 0, 'businessCalls': 0, 'sessionCreates': 0, 'suspendExperiments': 0,
              'osIdentitySha256': hashlib.sha256(json.dumps([platform.system(), platform.release(), platform.version(), platform.machine()]).encode()).hexdigest(),
              'harnessSha256': sha(__file__), 'moduleSha256': sha(HERE / 'opencode-clock-native-qualification.mjs'), 'versions': []}
    try:
        manifest_sha, bindings, identities = source_manifest(args.source_manifest, root)
        report.update(sourceManifestSha256=manifest_sha, sourceImplementationHashes=bindings,
                      primaryInputHashes=load(args.source_manifest)['inputHashes'])
        commit = command(['git', 'rev-parse', 'HEAD'], REPO).decode().strip()
        require_workflow_commit(commit, report)
        checkpoint = '6602b0674b4c1041ae971008f60b84fedd178b74'
        command(['git', 'merge-base', '--is-ancestor', checkpoint, commit], REPO)
        report['reviewedBaseCheckpoint'] = checkpoint
        need(not command(['git', 'status', '--porcelain', '--untracked-files=no'], REPO).strip(), 'clean_candidate_checkout_required')
        helper = root / ('candidate.exe' if args.os == 'windows' else 'candidate')
        build_env = {**os.environ, 'CGO_ENABLED': '1', 'GIT_CONFIG_COUNT': '1',
                     'GIT_CONFIG_KEY_0': 'safe.directory', 'GIT_CONFIG_VALUE_0': str(REPO)}
        command(['go', 'build', '-trimpath', '-buildvcs=true', '-o', str(helper), './cmd/claude-notifications'], REPO, env=build_env, timeout=300)
        build_info = command(['go', 'version', '-m', str(helper)], root)
        need(('vcs.revision=' + commit).encode() in build_info and b'vcs.modified=false' in build_info and b'go1.26' in build_info, 'candidate_vcs_build_binding_required')
        (root / 'build-info.private').write_bytes(build_info)
        source_paths = sorted(set(REPO.glob('internal/opencodeevent/*clock*.go')) | set(REPO.glob('internal/agentnotify/journal/clock*.go')) |
                              set(REPO.glob('cmd/claude-notifications/*clock*.go')) | {REPO / 'go.mod', REPO / 'go.sum'})
        helper_sha = sha(helper)
        go_version = build_info.decode().splitlines()[0].split()[-1]
        need(re.fullmatch(r'go1\.26(?:\.[0-9]+)?', go_version), 'fixed_go_version_required')
        report.update(commit=commit, helperSha256=helper_sha, buildInfoSha256=hashlib.sha256(build_info).hexdigest(),
                      goVersion=go_version,
                      buildEnvironment={'CGO_ENABLED': '1'},
                      sourceHashes={str(p.relative_to(REPO)): sha(p) for p in source_paths}, buildCommand=['go', 'build', '-trimpath', '-buildvcs=true', './cmd/claude-notifications'])
        prepared = []
        for version in VERSIONS:
            cell_root = root / ('TEST-' + version); cell_root.mkdir(mode=0o700)
            item = {'version': version, 'status': 'qualification_gap'}; report['versions'].append(item)
            exe = official_image(cell_root, version, args.os, args.arch, item, identities)
            prepared.append((version, cell_root, item, exe))
        if args.os == 'linux': linux_network(root)
        report['isolation'] = 'fresh_loopback_only_netns' if args.os == 'linux' else 'private_files_env_loopback_no_network_namespace'
        for version, cell_root, item, exe in prepared:
            try: run_version(cell_root, version, args, helper, helper_sha, manifest_sha, ownership, item, exe)
            except Exception as error:
                item['status'] = 'qualification_gap'
                item['failureReason'] = str(error) if isinstance(error, RuntimeError) and re.fullmatch(r'[a-z0-9_]{1,80}', str(error)) else 'native_exception'
                need(not ownership.live, 'failed_version_owned_handle_pending')
        report['status'] = 'api_prequalification_passed' if all(v['status'] == 'api_prequalification_passed' for v in report['versions']) else 'qualification_gap'
    except Exception as error:
        report['status'] = 'qualification_gap'
        report['failureReason'] = str(error) if isinstance(error, RuntimeError) and re.fullmatch(r'[a-z0-9_]{1,80}', str(error)) else 'preparation_or_native_exception'
    finally:
        for s in list(ownership.live.values()):
            try: ownership.stop(s)
            except Exception: report['status'] = 'qualification_gap'
        report.update(helperStarts=ownership.started, helperActualCloses=ownership.closed, helperOutputHashes=ownership.helper_hashes,
                      ownershipHighwater=ownership.highwater, allOwnedHandlesClosed=not ownership.live)
        if ownership.live: report['status'] = 'qualification_gap'
        write_json(args.report, report)  # Only this closed, sanitized evidence is uploadable.
    return 0 if report['status'] == 'api_prequalification_passed' else 1


if __name__ == '__main__':
    raise SystemExit(main())
