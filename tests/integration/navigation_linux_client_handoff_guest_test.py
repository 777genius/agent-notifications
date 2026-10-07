#!/usr/bin/python3
"""One offline guest: native notification click -> cold callback -> selected client."""
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import selectors
import signal
import socket
import stat
import struct
import subprocess
import time
import uuid

SEED = Path('/mnt/navigation-handoff-test-seed')
ROOT = Path('/var/lib/navigation-client-handoff-TEST')


def now():
    return time.clock_gettime(time.CLOCK_BOOTTIME)


def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for part in iter(lambda: stream.read(1048576), b''):
            h.update(part)
    return h.hexdigest()


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def parse_guest_cpu(raw):
    if not 0 < len(raw) <= 32768:
        raise RuntimeError('bounded_guest_CPUinfo_required')
    processors = []
    for block in raw.decode('ascii').strip().split('\n\n'):
        fields = {}
        for row in block.splitlines():
            key, separator, value = row.partition(':')
            if separator and key.strip() in ('processor', 'model name', 'flags'):
                key = key.strip()
                if key in fields: raise RuntimeError('duplicate_guest_CPU_field')
                fields[key] = value.strip()
        if set(fields) != {'processor', 'model name', 'flags'}:
            raise RuntimeError('complete_guest_CPU_observation_required')
        identifier, model, flags = fields['processor'], fields['model name'], fields['flags'].split()
        if not identifier.isdigit() or not 0 < len(model) <= 512 or any(ord(c) < 32 or ord(c) > 126 for c in model) or not 0 < len(flags) <= 256 or len(flags) != len(set(flags)) or any(re.fullmatch('[a-z0-9_]{1,64}', flag) is None for flag in flags):
            raise RuntimeError('bounded_guest_CPU_fields_required')
        if 'vmx' in flags or 'svm' in flags:
            raise RuntimeError('nested_virtualization_flags_must_be_off')
        processors.append(dict(processor=int(identifier), modelName=model, flags=flags))
    if [p['processor'] for p in processors] != [0, 1]:
        raise RuntimeError('exact_two_guest_processors_required')
    return dict(source='/proc/cpuinfo', rawBytes=len(raw), rawSHA256=hashlib.sha256(raw).hexdigest(), processors=processors)

def observe_guest_cpu():
    begin = now()
    with Path('/proc/cpuinfo').open('rb') as stream: raw = stream.read(32769)
    observation = parse_guest_cpu(raw)
    observation.update(beginBoot=begin, endBoot=now(), beforeSelectedTreeRead=True)
    return observation


def client_activation_surfaces(trace, token):
    # Client and system libwayland can use different object-ID delimiters.
    pattern = (r'xdg_activation_v1(?P<separator>[@#])\d+\.activate\("' +
               re.escape(token) + r'", wl_surface(?P=separator)(?P<surface>\d+)\)')
    return [match.group('surface') for match in re.finditer(pattern, trace)]


def completion_data(report, shipping):
    # This runs after cleanup, before any success publication. The full private
    # report may exceed the serial budget, but it must then explicitly fail.
    def qualify():
        for qualified, observed in (('handoffQualified', 'handoffObserved'), ('tokenForwardingQualified', 'tokenEqualityObserved'), ('focusQualified', 'serverFocusObserved')):
            report[qualified] = report['passed'] and report.get(observed, False)
        report['activationQualified'] = report['passed'] and report.get('serverSurfaceFocusJoinObserved', False)
    qualify()
    data = json.dumps(report, sort_keys=True).encode()
    if not shipping or len(data) <= 90 * 1024: return data
    report['passed'] = False
    report['completionFailure'] = 'complete_shipping_frame_exceeds_bound_no_retry'
    qualify()
    diagnostics = json.dumps(report, sort_keys=True).encode()
    failure = dict(scope='offline_shipping_go_native_handoff_TEST', passed=False,
        failure=report['completionFailure'], retryAllowed=False, shipping=shipping,
        handoffQualified=False, tokenForwardingQualified=False, focusQualified=False,
        activationQualified=False, navigationQualified=False, humanRenderQualified=False,
        notificationAttempted=bool(report.get('notificationAttempted')),
        clickAttempted=bool(report.get('clickAttempted')),
        cleanupPassed=bool(report.get('cleanupPassed')),
        cgroupEmptyObserved=bool(report.get('cgroupEmptyObserved')),
        privateDiagnosticsBytes=len(diagnostics),
        privateDiagnosticsSHA256=hashlib.sha256(diagnostics).hexdigest())
    return json.dumps(failure, sort_keys=True).encode()


def main():
    if os.getuid() != 0 or Path('/.dockerenv').exists() or ROOT.exists():
        raise RuntimeError('fresh_offline_TEST_guest_root_only')
    mounts = [row.split() for row in Path('/proc/mounts').read_text().splitlines()]
    if not any(row[1:3] == [str(SEED), 'iso9660'] and 'ro' in row[3].split(',') for row in mounts):
        raise RuntimeError('readonly_TEST_seed_required')
    if (SEED / 'navigation.marker').read_text() != 'Linux selected-client handoff TEST only\n' or sorted(p.name for p in Path('/sys/class/net').iterdir()) != ['lo']:
        raise RuntimeError('offline_TEST_marker_and_no_NIC_required')
    manifest = json.loads((SEED / 'manifest.json').read_text())
    files = {'client-callback.py', 'client-sender.py', 'guest-pointer-entry.py', 'guest-pointer.so',
             'kernel-observer.py', 'protocol-observer.py', 'client-controller.py',
             'server-observer.so', 'server-observer.py'}
    if set(manifest) != {'files', 'frontendSHA256', 'backendSHA256'} | ({'shipping'} if 'shipping' in manifest else set()) or set(manifest['files']) != files:
        raise RuntimeError('fixed_TEST_seed_manifest_required')
    for name, digest in manifest['files'].items():
        if not re.fullmatch('[0-9a-f]{64}', digest) or sha(SEED / name) != digest:
            raise RuntimeError('TEST_source_snapshot_changed')
    if Path(__file__).resolve() != SEED / 'client-controller.py':
        raise RuntimeError('fixed_TEST_controller_entry_required')
    shipping = load('TEST_assets', SEED / 'guest-bootstrap.py').shipping_inputs(SEED, manifest)
    kernel = load('TEST_kernel', SEED / 'kernel-observer.py')
    protocol = load('TEST_protocol', SEED / 'protocol-observer.py')
    callback = load('TEST_callback', SEED / 'client-callback.py')
    server_decoder = load('TEST_server_decoder', SEED / 'server-observer.py')
    nonce = uuid.uuid4().hex
    group = Path('/sys/fs/cgroup') / ('navigation-handoff-TEST-' + nonce)
    work = ROOT / 'session'
    children, streams, handles = [], [], {}
    report = dict(scope='offline_selected_client_native_handoff_TEST', nonce=nonce, passed=False,
        retryAllowed=False, notificationAttempted=False, clickAttempted=False,
        handoffQualified=False, activationQualified=False, navigationQualified=False,
        humanRenderQualified=False, trackedProcesses=[], sourceSHA256=manifest['files'])
    if shipping: report.update(scope='offline_shipping_go_native_handoff_TEST', shipping=shipping)
    deadline = now() + (150 if shipping else 65)
    mako_label = 'mako'
    group_created = False
    env = dict(PATH='/usr/bin:/bin', LANG='C.UTF-8', HOME=str(work / 'home'),
        XDG_CONFIG_HOME=str(work / 'config'), XDG_DATA_HOME=str(work / 'data'),
        XDG_CACHE_HOME=str(work / 'cache'), XDG_RUNTIME_DIR=str(work / 'runtime'),
        XDG_DATA_DIRS='/opt/portal/share:/opt/gtk/share:/usr/share', XDG_SESSION_TYPE='wayland',
        XDG_CURRENT_DESKTOP='TEST', WLR_BACKENDS='headless', WLR_RENDERER='pixman',
        WLR_HEADLESS_OUTPUTS='1', WLR_LIBINPUT_NO_DEVICES='1', GDK_BACKEND='wayland')

    def persist():
        (ROOT / 'progress.json').write_text(json.dumps(report, indent=2) + '\n')

    def read(path):
        with path.open('rb') as stream:
            data = stream.read(4 * 1024 * 1024 + 1)
        if len(data) > 4 * 1024 * 1024:
            raise RuntimeError('private_trace_exceeds_bound')
        return data.decode()

    def wait(predicate, seconds=5, *, phase='observation'):
        report['observationPhase'] = phase
        end = min(deadline, now() + seconds)
        while True:
            if now() >= end: raise RuntimeError('bounded_observation_expired_no_retry')
            ready = predicate()
            if now() >= end: raise RuntimeError('expired_observation_cannot_admit_effect')
            if ready: return
            time.sleep(0.025)

    def drop():
        (group / 'cgroup.procs').write_text(str(os.getpid()))
        os.setgroups([]); os.setgid(1000); os.setuid(1000); os.umask(0o077)

    def retain(pid, require_uid1000=True):
        before = kernel.snapshot(pid)
        fd = os.pidfd_open(pid, 0)
        try:
            after = kernel.snapshot(pid)
            if before['startTicks'] != after['startTicks']:
                raise RuntimeError('owned_incarnation_changed')
            if require_uid1000 and after['status']['Uid'].split() != ['1000'] * 4:
                raise RuntimeError('selected_peer_uid_not_1000')
            if Path('/proc', str(pid), 'cgroup').read_text().strip() != '0::/' + group.name:
                raise RuntimeError('process_outside_owned_TEST_cgroup')
            with selectors.DefaultSelector() as selector:
                selector.register(fd, selectors.EVENT_READ)
                if selector.select(0): raise RuntimeError('owned_process_already_exited')
            if pid in handles:
                raise RuntimeError('refuse_to_replace_retained_incarnation')
            handles[pid] = (fd, after)
            return after
        except Exception:
            os.close(fd); raise

    def alive(pid):
        fd, saved = handles[pid]
        with selectors.DefaultSelector() as selector:
            selector.register(fd, selectors.EVENT_READ)
            if selector.select(0): raise RuntimeError('required_owned_process_exited')
        if kernel.snapshot(pid)['startTicks'] != saved['startTicks']:
            raise RuntimeError('owned_incarnation_changed')

    def capture_group(cleanup=False):
        values = (group / 'cgroup.procs').read_text().split()
        if len(values) > 128: raise RuntimeError('owned_process_observation_bound_exceeded')
        observation_errors = []
        for value in values:
            pid = int(value)
            if pid in handles: continue
            try: retain(pid, require_uid1000=False)
            except (FileNotFoundError, ProcessLookupError): pass
            except Exception as error:
                if not cleanup: raise
                # A failed observation must not prevent collecting other owned descendants.
                observation_errors.append(dict(pid=pid, reason=str(error)))
        if observation_errors:
            report['cleanupObservationErrors'] = observation_errors
            raise RuntimeError('owned_group_observation_failed')

    def selected_peer(pid, phase):
        # Group retention is cleanup authority, never selected-peer admission.
        if pid not in handles: retain(pid, require_uid1000=False)
        alive(pid)
        observed = kernel.snapshot(pid)
        alive(pid)
        uids = observed['status']['Uid'].split()
        membership = Path('/proc', str(pid), 'cgroup').read_text().strip()
        alive(pid)
        if uids != ['1000'] * 4 or membership != '0::/' + group.name:
            report['selectedPeerAdmissionFailure'] = dict(phase=phase, pid=pid,
                retainedBirth=handles[pid][1]['startTicks'], observedBirth=observed['startTicks'],
                uidFields=uids, cgroup=membership)
            raise RuntimeError('selected_peer_uid_or_cgroup_unproved')
        return observed

    def start(label, argv, pipe=False, input_pipe=False, extra=None, passed_fds=()):
        if passed_fds and label != 'sway': raise RuntimeError('observer_fd_only_for_TEST_sway')
        out, err = (ROOT / (label + '.stdout')).open('xb'), (ROOT / (label + '.stderr')).open('xb')
        streams.extend([out, err])
        if now() >= deadline: raise RuntimeError('deadline_before_owned_process_start')
        child = subprocess.Popen(argv, cwd=work, env=dict(env, **(extra or {})), preexec_fn=drop,
            stdin=subprocess.PIPE if input_pipe else subprocess.DEVNULL,
            stdout=subprocess.PIPE if pipe else out, stderr=err, bufsize=0 if pipe else -1,
            start_new_session=True, pass_fds=passed_fds)
        children.append(child); retain(child.pid)
        return child

    def run(argv):
        remaining = deadline - now()
        if remaining <= 0: raise RuntimeError('deadline_before_command')
        value = subprocess.run(argv, cwd=work, env=env, preexec_fn=drop,
            capture_output=True, timeout=min(5, remaining))
        if now() >= deadline: raise RuntimeError('private_bus_reply_after_deadline')
        if value.returncode: raise RuntimeError('private_bus_command_failed')
        return value.stdout.decode().strip()

    def bus(method, name):
        return run(['/usr/bin/gdbus', 'call', '--session', '--dest', 'org.freedesktop.DBus',
            '--object-path', '/org/freedesktop/DBus', '--method', 'org.freedesktop.DBus.' + method, name])

    def owner(name, child):
        def ready():
            alive(child.pid)
            return bus('NameHasOwner', name) == '(true,)'
        wait(ready)
        if bus('GetConnectionUnixProcessID', name) != '(uint32 ' + str(child.pid) + ',)':
            raise RuntimeError('private_bus_owner_PID_mismatch')
        value = bus('GetNameOwner', name)
        match = re.fullmatch(r"\('(:[0-9]+\.[0-9]+)',\)", value)
        if not match: raise RuntimeError('private_bus_unique_owner_missing')
        return match[1]

    def root_spec(path, data):
        with path.open('x') as stream:
            json.dump(data, stream); stream.flush(); os.fsync(stream.fileno())
        path.chmod(0o444)

    def server_records():
        alive(sway.pid)
        data = read(ROOT / 'server-protocol.jsonl')
        if not data or not data.endswith('\n'): return None
        def closed_pairs(pairs):
            value = {}
            for key, item in pairs:
                if key in value: raise RuntimeError('duplicate_server_record_field')
                value[key] = item
            return value
        rows = [json.loads(line, object_pairs_hook=closed_pairs) for line in data.splitlines()]
        if not 0 < len(rows) <= 512 or rows[0] != dict(kind='ready', compositorPID=sway.pid):
            raise RuntimeError('owned_server_observer_readiness_unproved')
        if any(row.get('kind') == 'fault' for row in rows): raise RuntimeError('server_observer_fault')
        alive(sway.pid)
        return rows

    def bound_server_surface(pid, token_hash):
        alive(sway.pid); alive(pid)
        rows = server_records()
        if rows is None: return None
        matching_request = False
        for row in rows:
            if row.get('kind') != 'activate': continue
            token = row.get('tokenHex')
            if not isinstance(token, str) or not re.fullmatch('[0-9a-f]{2,8192}', token) or len(token) % 2:
                raise RuntimeError('server_activation_token_encoding_invalid')
            if hashlib.sha256(bytes.fromhex(token)).hexdigest() == token_hash: matching_request = True
        if not matching_request: return None
        value = server_decoder.selected_server_surface(rows, sway.pid, pid,
            int(handles[pid][1]['startTicks']), token_hash)
        fd = value['observerPidfd']
        path = Path('/proc', str(sway.pid), 'fd', str(fd))
        if os.readlink(path) != 'anon_inode:[pidfd]': raise RuntimeError('observer_handle_not_kernel_pidfd')
        info = Path('/proc', str(sway.pid), 'fdinfo', str(fd)).read_text()
        observed = re.findall(r'(?m)^Pid:\s+([0-9]+)$', info)
        if observed != [str(pid)]: raise RuntimeError('actual_server_connection_pidfd_unbound')
        alive(sway.pid); alive(pid)
        current = kernel.snapshot(pid)
        if int(current['startTicks']) != value['birth'] or current['executable'] != str(kernel.EXE):
            raise RuntimeError('observed_server_peer_incarnation_changed')
        value['kernelBound'] = True
        return value

    def server_focus(pid):
        # Query the retained compositor over this peer-verified connection, not swaymsg's lookup.
        alive(sway.pid)
        paths = list((work / 'runtime').glob('sway-ipc.*.sock'))
        if len(paths) != 1 or paths[0].resolve() != paths[0] or not stat.S_ISSOCK(paths[0].stat().st_mode):
            raise RuntimeError('unique_private_sway_IPC_required')
        end = min(deadline, now() + 3)
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(max(0.01, end - now())); connection.connect(str(paths[0]))
            peer = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if peer[:2] != (sway.pid, 1000): raise RuntimeError('actual_sway_IPC_peer_unbound')
            alive(sway.pid)
            connection.sendall(struct.pack('<6sII', b'i3-ipc', 0, 4))
            def receive(count):
                result = b''
                while len(result) < count:
                    if now() >= end: raise RuntimeError('bounded_sway_IPC_reply_expired')
                    connection.settimeout(end - now())
                    part = connection.recv(count - len(result))
                    if not part: raise RuntimeError('sway_IPC_reply_incomplete')
                    result += part
                return result
            magic, length, kind = struct.unpack('<6sII', receive(14))
            if magic != b'i3-ipc' or kind != 4 or not 0 < length <= 4 * 1024 * 1024:
                raise RuntimeError('sway_IPC_tree_header_invalid')
            tree = json.loads(receive(length))
            alive(sway.pid)
        pending, focused, count = [tree], [], 0
        while pending:
            node = pending.pop(); count += 1
            if count > 1024 or not isinstance(node, dict): raise RuntimeError('sway_tree_bound_or_type_invalid')
            if node.get('focused') is True and type(node.get('pid')) is int:
                focused.append(node)
            for key in ('nodes', 'floating_nodes'):
                values = node.get(key, [])
                if not isinstance(values, list): raise RuntimeError('sway_child_nodes_invalid')
                pending.extend(values)
        if len(focused) != 1 or focused[0]['pid'] != pid: return False
        if focused[0].get('type') != 'con' or focused[0].get('shell') != 'xdg_shell' or focused[0].get('window') is not None or type(focused[0].get('id')) is not int or focused[0]['id'] <= 0:
            raise RuntimeError('focused_node_not_native_xdg_toplevel_view')
        alive(pid)
        current = kernel.snapshot(pid)
        if current['executable'] != str(kernel.EXE): raise RuntimeError('focused_client_kernel_identity_changed')
        report['serverFocus'] = dict(peerPID=peer[0], peerUID=peer[1], clientPID=pid,
            clientBirth=current['startTicks'], containerID=focused[0].get('id'),
            shell=focused[0]['shell'],
            evidenceClass='peer_verified_compositor_tree_and_selected_kernel_incarnation')
        return True

    def messages():
        return [part for part in re.split(r'(?m)(?=^(?:method call|method return|signal|error) time=)',
            read(ROOT / 'monitor.stdout')) if part.strip()]

    def trace():
        return read(ROOT / (mako_label + '.stderr'))

    def owned_fixture(path, data, mode=0o600):
        with path.open('xb') as stream: stream.write(data)
        os.chown(path, 1000, 1000); path.chmod(mode)

    def shipping_setup():
        nonlocal mako, mako_owner, mako_label
        managed, stage = work / 'managed', work / 'shipping-stage'
        for directory in (managed, managed / 'bin', stage):
            directory.mkdir(mode=0o700); os.chown(directory, 1000, 1000)
        staged = stage / 'claude-notifications-linux-amd64'
        owned_fixture(staged, (SEED / 'shipping-client').read_bytes(), 0o700)
        cli = managed / 'bin/claude-notifications-linux-amd64'
        control = Path(env['XDG_CONFIG_HOME']) / 'agent-notifications'
        global_config = work / 'global.json'
        env['AGENT_NOTIFICATIONS_CONFIG'] = str(global_config)
        defaults = work / 'defaults.json'
        owned_fixture(defaults, b'{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":true}}}')
        commands = []; report['setupCommands'] = commands

        def command(label, argv, allowed=(0,)):
            remaining = deadline - now()
            if remaining <= 0: raise RuntimeError('shipping_setup_budget_expired')
            result = subprocess.run([str(cli if cli.exists() else staged)] + argv,
                cwd=work, env=env, preexec_fn=drop, capture_output=True, timeout=min(45, remaining))
            if len(result.stdout) + len(result.stderr) > 65536: raise RuntimeError('shipping_CLI_output_bound')
            commands.append(dict(label=label, argv=argv, exitCode=result.returncode,
                stdout=result.stdout.decode(), stderr=result.stderr.decode(), collectedBoot=now()))
            if result.returncode not in allowed: raise RuntimeError('shipping_normal_CLI_failed_' + label)
            return result.stdout.decode()

        # The real installer holds O_RDONLY directory descriptors for ancestors.
        # Observe admission as its actual UID before invoking the unchanged CLI.
        admission_code = """import json,os,stat,sys
from pathlib import Path
result=dict(uid=os.getuid(),gid=os.getgid(),groups=os.getgroups(),ancestors=[],passed=False)
paths=set()
for argument in sys.argv[1:]:
 p=Path(argument)
 paths.update([p,*p.parents])
for p in sorted(paths,key=lambda p:(len(p.parts),str(p))):
 try:fd=os.open(p,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
 except OSError as error:
  result['failure']=dict(path=str(p),errno=error.errno,error=str(error));break
 try:
  s=os.fstat(fd)
  result['ancestors'].append(dict(path=str(p),uid=s.st_uid,gid=s.st_gid,mode=stat.S_IMODE(s.st_mode),device=s.st_dev,inode=s.st_ino))
 finally:os.close(fd)
else:result['passed']=True
print(json.dumps(result,sort_keys=True),flush=True)
raise SystemExit(0 if result['passed'] else 1)
"""
        remaining = deadline - now()
        if remaining <= 0: raise RuntimeError('shipping_ancestor_admission_budget_expired')
        admission = subprocess.run(['/usr/bin/python3', '-I', '-c', admission_code,
            str(stage), str(managed / 'bin'), str(control.parent)],
            cwd=work, env=env, preexec_fn=drop, capture_output=True, timeout=min(3, remaining))
        if len(admission.stdout) + len(admission.stderr) > 8192: raise RuntimeError('shipping_ancestor_admission_output_bound')
        report['managedAncestorAdmission'] = dict(collected=True, exitCode=admission.returncode,
            collectedBoot=now(), stdout=admission.stdout.decode(), stderr=admission.stderr.decode())
        observation = json.loads(admission.stdout)
        if admission.returncode or observation.get('passed') is not True or observation.get('uid') != 1000 or observation.get('gid') != 1000 or observation.get('groups') != []:
            raise RuntimeError('shipping_real_UID_directory_admission_failed')
        command('managed-install', ['internal-install-runtime', '--stage', str(stage), '--entry', staged.name,
            '--target', str(managed / 'bin'), '--control-root', str(control), '--consumer', 'claude-hooks'])
        def generation(label):
            value = json.loads(command('status-' + label, ['setup-notifications', 'status', '--global-config', str(global_config), '--json'], (0, 1)))
            if type(value.get('generation')) is not int or value['generation'] <= 0: raise RuntimeError('actual_setup_generation_required')
            return str(value['generation'])
        command('prepare', ['setup-notifications', 'prepare', '--global-config', str(global_config),
            '--legacy-config', str(work / 'absent-legacy.json'), '--defaults-config', str(defaults),
            '--expected-generation', generation('prepare'), '--json'])
        def bind(label):
            command('callback-' + label, ['setup-linux-callback', '--installation-root', '/usr/lib/chatgpt'])
            command('enable-' + label, ['setup-notifications', 'enable', '--global-config', str(global_config),
                '--navigation', 'desktop_thread', '--allow-unknown-caller', 'false', '--allow-caller-asserted', 'true',
                '--expected-generation', generation(label), '--json'])
            binding = json.loads(read(control / 'agent-notifications.json'))['route']['linuxCallbackSnapshot']
            path = Path(binding['snapshotPath']); snapshot = json.loads(read(path))
            if sha(path) != binding['sha256'] or snapshot['ReaderSHA256'] != shipping['files']['shipping-client'] or snapshot['ManifestSHA256'] != shipping['files']['shipping-vendor-manifest.json']:
                raise RuntimeError('actual_immutable_shipping_snapshot_unbound')
            saved = {str(p): sha(p) for p in (path, Path(snapshot['Reader']),
                Path(snapshot['DataRoot']) / 'applications' / (snapshot['ApplicationID'] + '.desktop'),
                Path(snapshot['DataRoot']) / 'dbus-1/services' / (snapshot['ApplicationID'] + '.service'))}
            return dict(binding=binding, snapshot=snapshot, retained=saved)

        def submit(label, binding):
            thread = str(uuid.UUID(hex=nonce)) if label == 'A' else str(uuid.uuid4())
            title = 'Navigation TEST ' + nonce + ' ' + label
            context_file = work / ('context-' + label + '.json')
            owned_fixture(context_file, json.dumps(dict(provider='codex', session=thread, locality='local', interface='desktop')).encode())
            report['notificationAttempted'] = True; persist()
            sender = start('sender-' + label, [str(cli), 'notify', '--context-file', str(context_file)], input_pipe=True)
            payload = dict(title=title, body='Shipping Go TEST', category='info', request_id=nonce + '-' + label, navigation='required')
            sender.stdin.write(json.dumps(payload).encode()); sender.stdin.close()
            code = sender.wait(timeout=min(20, deadline - now())); exited = now()
            receipt = json.loads(read(ROOT / ('sender-' + label + '.stdout')))
            if code or receipt['status'] != 'submitted' or receipt['navigation']['Precision'] != 'chat_id' or receipt['navigation']['Scope'] != 'selected_linux_installation':
                raise RuntimeError('shipping_actual_admission_receipt_unproved')
            snapshot = binding['snapshot']; records = list(Path(snapshot['Records']).glob('*.json'))
            if len(records) != 1: raise RuntimeError('sole_owned_shipping_record_required')
            record = json.loads(read(records[0]))
            binding['retained'][str(records[0])] = sha(records[0])
            if record['ThreadID'] != thread or record['SnapshotSHA256'] != binding['binding']['sha256'] or record['InstallationID'] != snapshot['InstallationID'] or record['Owners']['GTK'] != gtk_owner or record['Owners']['Frontend'] != frontend_owner or record['Owners']['Notifications'] != mako_owner:
                raise RuntimeError('shipping_record_actual_binding_mismatch')
            app = snapshot['ApplicationID']
            if bus('NameHasOwner', app) != '(false,)': raise RuntimeError('shipping_callback_not_cold')
            additions = [m for m in messages() if 'interface=org.freedesktop.portal.Notification; member=AddNotification\n' in m and '\n   string "' + record['Key'] + '"\n' in m]
            if len(additions) != 1 or 'destination=' + frontend_owner + ' ' not in additions[0].splitlines()[0]: raise RuntimeError('shipping_actual_PortalAdd_unproved')
            add_sender = re.search(r' sender=(:[0-9]+\.[0-9]+) ', additions[0].splitlines()[0])
            registrations = [m for m in messages() if 'interface=org.freedesktop.host.portal.Registry; member=Register\n' in m and '\n   string "' + app + '"\n' in m]
            if not add_sender or len(registrations) != 1 or 'sender=' + add_sender[1] + ' ' not in registrations[0].splitlines()[0] or 'destination=' + frontend_owner + ' ' not in registrations[0].splitlines()[0]: raise RuntimeError('shipping_Register_Add_connection_unbound')
            report['submission' + label] = dict(pid=sender.pid, collected=True, exitCode=code, exitedBoot=exited,
                payload=payload, receipt=receipt, context=json.loads(read(context_file)), record=record, portalAdd=additions[0], registryRegister=registrations[0], **binding)
            return dict(app=app, title=title, thread=thread, key=record['Key'], exited=exited, snapshot=snapshot, binding=binding['binding'])

        a = bind('A'); selected = submit('A', a)
        old_selected = selected
        if shipping['scenario'] == 'restart_b':
            old_owner = mako_owner
            signal.pidfd_send_signal(handles[mako.pid][0], signal.SIGTERM); mako.wait(timeout=3)
            mako_label = 'mako-B'
            mako = start(mako_label, ['/usr/bin/mako', '--config', str(mako_config)], extra={'WAYLAND_DEBUG': '1'})
            mako_owner = owner('org.freedesktop.Notifications', mako)
            if mako_owner == old_owner: raise RuntimeError('notification_owner_restart_unproved')
            report['ownerRestart'] = dict(old=old_owner, current=mako_owner)
            report['providerOwners'] = dict(mako=mako_owner, gtk=gtk_owner, frontend=frontend_owner)
        b = bind('B')
        if shipping['scenario'] == 'restart_b': selected = submit('B', b)
        if a['binding'] == b['binding'] or any(sha(Path(path)) != digest for path, digest in a['retained'].items()):
            raise RuntimeError('retained_A_changed_after_setup_B')
        report['retainedA'] = a; report['currentB'] = b; report['normalSetupObserved'] = True
        if cli.is_symlink() or sha(cli) != shipping['files']['shipping-client']: raise RuntimeError('owned_TEST_producer_identity_changed')
        # Exact disposable producer state only. Immutable callback custody and
        # installation ledger/ownership markers remain intact. No directory rm.
        mutable = [global_config, control / 'agent-notifications.json'] + [control / 'state/journal' / name for name in ('namespace', 'journal.json', 'lock')]
        removed = []; report['removedProducerState'] = removed
        for path in mutable + [cli, staged]:
            before = path.lstat()
            if not path.is_relative_to(work) or path.resolve() != path or not stat.S_ISREG(before.st_mode) or before.st_uid != 1000 or before.st_nlink != 1 or before.st_mode & 0o077:
                raise RuntimeError('exact_owned_TEST_mutable_file_required')
            if path in mutable and before.st_size > 65536: raise RuntimeError('complete_removed_state_evidence_bound')
            digest = sha(path)
            item = dict(path=str(path), uid=before.st_uid, mode=stat.S_IMODE(before.st_mode), device=before.st_dev, inode=before.st_ino, sha256=digest)
            if path in mutable: item['bytesBase64'] = base64.b64encode(path.read_bytes()).decode()
            current = path.lstat()
            identity = lambda st: (st.st_dev, st.st_ino, st.st_mode, st.st_uid, st.st_nlink, st.st_size, st.st_mtime_ns, st.st_ctime_ns)
            if identity(current) != identity(before) or sha(path) != digest: raise RuntimeError('TEST_mutable_file_changed_before_removal')
            path.unlink(); item.update(absent=not path.exists() and not path.is_symlink(), removedBoot=now())
            if not item['absent']: raise RuntimeError('exact_TEST_removal_unproved')
            removed.append(item)
        if any(sha(Path(path)) != digest for held in (a, b) for path, digest in held['retained'].items()):
            raise RuntimeError('immutable_callback_custody_changed_during_TEST_removal')
        report['producerRemoved'] = True; report['mutableProducerStateRemoved'] = True
        if shipping['scenario'] == 'restart_b':
            # Same UID does not authenticate the wire sender. This control cannot
            # isolate owner-fence rejection, and does not substitute for B click.
            path = '/' + old_selected['app'].replace('.', '/')
            forged = subprocess.run(['/usr/bin/gdbus', 'call', '--session', '--dest', old_selected['app'],
                '--object-path', path, '--method', 'org.freedesktop.Application.ActivateAction', 'open',
                "[<'" + old_selected['key'] + "'>]", "{'activation-token': <'TEST-forged-token'>}"],
                cwd=work, env=env, preexec_fn=drop, capture_output=True, timeout=min(5, deadline - now()))
            calls = [m for m in messages() if 'interface=org.freedesktop.Application; member=ActivateAction\n' in m and 'destination=' + old_selected['app'] + ' ' in m.splitlines()[0]]
            if forged.returncode != 1 or len(calls) != 1 or 'sender=' + gtk_owner + ' ' in calls[0].splitlines()[0] or protocol.activate_arguments(calls[0]) != ('open', old_selected['key'], {'activation-token': 'TEST-forged-token'}):
                raise RuntimeError('actual_borrowed_UID_sender_rejection_unproved')
            for value in (group / 'cgroup.procs').read_text().split():
                try:
                    if kernel.snapshot(int(value))['executable'] == str(kernel.EXE): raise RuntimeError('forged_sender_launched_vendor')
                except (FileNotFoundError, ProcessLookupError): pass
            if any(row.get('kind') == 'activate' for row in server_records()): raise RuntimeError('forged_sender_activation_effect')
            report['forgedASenderControl'] = dict(scope='actual_wire_sender_auth_only', uid=1000, call=calls[0], collectedExitCode=forged.returncode,
                stdout=forged.stdout.decode(), stderr=forged.stderr.decode(), vendorEffectObserved=False, ownerFenceQualified=False)
        if bus('NameHasOwner', selected['app']) != '(false,)': raise RuntimeError('selected_Go_reader_not_cold_before_click')
        # A late click is substantive evidence that its submission deadline expired.
        wait(lambda: now() - selected['exited'] > 16, 20)
        report['lateClickObserved'] = True
        report['senderExit'] = dict(collected=True, exitCode=0, exitedBoot=selected['exited'])
        report['plannedNativeEffects'] = dict(notifications=2 if shipping['scenario'] == 'restart_b' else 1, genuineClicks=1, selectedVendorHandoffs=1, retryAllowed=False)
        return selected

    def observe_shipping(selected):
        if bus('NameHasOwner', selected['app']) != '(true,)': return None
        match = re.fullmatch(r'\(uint32 (\d+),\)', bus('GetConnectionUnixProcessID', selected['app']))
        if not match: raise RuntimeError('cold_Go_callback_owner_PID_unproved')
        pid = int(match[1]); reader = selected_peer(pid, 'cold-Go-reader')
        expected = [selected['snapshot']['Reader'], 'internal-linux-callback', '--snapshot', selected['binding']['snapshotPath'], '--sha256', selected['binding']['sha256']]
        if reader['command'] != [s.encode() for s in expected] or sha(Path('/proc', str(pid), 'exe')) != shipping['files']['shipping-client'] or int(reader['startTicks']) / os.sysconf('SC_CLK_TCK') < selected['exited']:
            raise RuntimeError('actual_cold_retained_Go_reader_unbound')
        uri = 'codex://threads/' + selected['thread']
        found = []
        for value in (group / 'cgroup.procs').read_text().split():
            try:
                client = kernel.snapshot(int(value))
                if client['executable'] == str(kernel.EXE) and client['command'] == [bytes(kernel.EXE), b'--ozone-platform=wayland', uri.encode()]: found.append(client)
            except (FileNotFoundError, ProcessLookupError): pass
        if not found: return None
        if len(found) != 1: raise RuntimeError('sole_selected_exact_URI_process_unproved')
        client_pid = found[0]['pid']; selected_peer(client_pid, 'selected-launch')
        with Path('/proc', str(client_pid), 'environ').open('rb') as stream: environment = stream.read(65537)
        if len(environment) > 65536 or not environment.endswith(b'\0'): raise RuntimeError('bounded_selected_environment_required')
        entries = [value.split(b'=', 1) for value in environment.split(b'\0')[:-1]]
        if any(len(pair) != 2 for pair in entries) or len({pair[0] for pair in entries}) != len(entries): raise RuntimeError('unambiguous_selected_environment_required')
        report['selectedEnvironment'] = {key.decode(): value.decode() for key, value in entries}
        alive(client_pid)
        report['coldGoReader'] = kernel.public_snapshot(reader); report['coldGoReaderObserved'] = True
        report['exactURI'] = uri; report['exactURIObserved'] = True
        return found[0]['pid']

    try:
        if shipping:
            report['cpuProfileRequested'] = 'host,vmx=off,svm=off'
            report['guestCPUObservation'] = observe_guest_cpu()
        ROOT.mkdir(mode=0o700); os.chown(ROOT, 0, 1000)
        # Shipping directory anchors need group read as well as traversal. The
        # root stays root-owned, group non-writable, and inaccessible to others.
        ROOT.chmod(0o750 if shipping else 0o710)
        # One explicitly frozen offline TEST assembly; no automatic attempt/retry.
        work.mkdir(mode=0o700); os.chown(work, 1000, 1000)
        group.mkdir(mode=0o700); group_created = True
        if not (group / 'cgroup.kill').is_file(): raise RuntimeError('owned_cgroup_kill_required')
        report['ownedCgroup'] = str(group)
        for name in ('home', 'config', 'data', 'cache', 'runtime', 'callback'):
            path = work / name; path.mkdir(mode=0o700); os.chown(path, 1000, 1000)
        if shipping: report['firstSelectedVendorReadBeginBoot'] = now()
        if sha(kernel.EXE) != kernel.EXE_SHA or sha(kernel.LAUNCHER) != kernel.LAUNCHER_SHA or sha(Path('/etc/apparmor.d/chatgpt')) != kernel.PROFILE_SHA:
            raise RuntimeError('selected_vendor_installation_changed')
        if Path('/etc/apparmor.d/local/chatgpt').exists() or not any(row.split(' (', 1)[0] == 'chatgpt' for row in Path('/sys/kernel/security/apparmor/profiles').read_text().splitlines()):
            raise RuntimeError('shipped_vendor_profile_required')
        frontend_path = Path('/opt/portal/libexec/xdg-desktop-portal')
        backend_path = Path('/opt/gtk/libexec/xdg-desktop-portal-gtk')
        if sha(frontend_path) != manifest['frontendSHA256'] or sha(backend_path) != manifest['backendSHA256']:
            raise RuntimeError('qualified_portal_runtime_changed')
        app_id = 'org.notification.NavigationTest' + nonce
        for folder, suffix, text in ([] if shipping else [('applications', '.desktop', '[Desktop Entry]\nType=Application\nName=Navigation TEST\nDBusActivatable=true\nExec=/usr/bin/python3 ' + str(SEED / 'client-callback.py') + '\n'),
                ('dbus-1/services', '.service', '[D-BUS Service]\nName=' + app_id + '\nExec=/usr/bin/python3 ' + str(SEED / 'client-callback.py') + '\n')]):
            directory = work / 'data' / folder; directory.mkdir(parents=True); os.chown(directory, 1000, 1000)
            path = directory / (app_id + suffix); path.write_text(text); path.chmod(0o444)
        portal_config = work / 'config/xdg-desktop-portal'; portal_config.mkdir(); os.chown(portal_config, 1000, 1000)
        (portal_config / 'portals.conf').write_text('[preferred]\ndefault=gtk\norg.freedesktop.impl.portal.Notification=gtk\n')
        sway_config = work / 'sway.conf'; sway_config.write_text('xwayland disable\nseat seat0 fallback true\noutput * resolution 1280x720\nfocus_follows_mouse no\n')
        mako_config = work / 'mako.conf'; mako_config.write_text('anchor=top-center\nwidth=600\nheight=120\nmargin=0\ndefault-timeout=0\nmax-visible=1\nfont=DejaVu Sans 12\non-button-left=invoke-default-action\n')
        observer_fd = os.open(ROOT / 'server-protocol.jsonl', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_APPEND | os.O_CLOEXEC, 0o600)
        streams.append(os.fdopen(observer_fd, 'wb', buffering=0))
        sway = start('sway', ['/usr/bin/sway', '-c', str(sway_config), '-d'],
            extra={'LD_PRELOAD': str(SEED / 'server-observer.so'),
                'NAVIGATION_TEST_SERVER_PROTOCOL_FD': str(observer_fd)}, passed_fds=(observer_fd,))
        wait(lambda: server_records() is not None)
        sockets = []
        def display_ready():
            nonlocal sockets
            alive(sway.pid)
            sockets = [p for p in (work / 'runtime').glob('wayland-*') if stat.S_ISSOCK(p.stat().st_mode)]
            return len(sockets) == 1
        wait(display_ready)
        env['WAYLAND_DISPLAY'] = sockets[0].name
        callback.peer(sockets[0], sway.pid, int(handles[sway.pid][1]['startTicks']))
        daemon = start('bus', ['/usr/bin/dbus-daemon', '--session', '--nofork', '--print-address=1',
            '--address=unix:path=' + str(work / 'runtime/bus.sock')], True)
        with selectors.DefaultSelector() as selector:
            selector.register(daemon.stdout, selectors.EVENT_READ)
            if not selector.select(5): raise RuntimeError('private_bus_address_expired')
            address = os.read(daemon.stdout.fileno(), 4096).decode().strip()
        callback.private_bus_address(address, work / 'runtime/bus.sock')
        callback.peer(work / 'runtime/bus.sock', daemon.pid, int(handles[daemon.pid][1]['startTicks']))
        env['DBUS_SESSION_BUS_ADDRESS'] = address
        root_spec(ROOT / 'callback-spec.json', dict(nonce=nonce, appID=app_id,
            helperSHA256=manifest['files']['client-callback.py'], busPID=daemon.pid,
            busBirth=int(handles[daemon.pid][1]['startTicks']), busSocket=str(work / 'runtime/bus.sock'),
            busAddress=address, compositorPID=sway.pid, compositorBirth=int(handles[sway.pid][1]['startTicks']),
            waylandDisplay=sockets[0].name))
        # D-Bus activation inherits the daemon's environment, so seed session address explicitly.
        run(['/usr/bin/dbus-update-activation-environment', 'DBUS_SESSION_BUS_ADDRESS=' + address,
            'XDG_RUNTIME_DIR=' + env['XDG_RUNTIME_DIR'], 'XDG_DATA_HOME=' + env['XDG_DATA_HOME'],
            'XDG_CONFIG_HOME=' + env['XDG_CONFIG_HOME']])
        monitor = start('monitor', ['/usr/bin/dbus-monitor', '--session', "interface='org.freedesktop.Notifications'",
            "interface='org.freedesktop.Application'", "type='method_return'"] + (["interface='org.freedesktop.portal.Notification'", "interface='org.freedesktop.host.portal.Registry'"] if shipping else []))
        mako = start('mako', ['/usr/bin/mako', '--config', str(mako_config)], extra={'WAYLAND_DEBUG': '1'})
        mako_owner = owner('org.freedesktop.Notifications', mako)
        gtk = start('gtk', [str(backend_path)])
        gtk_owner = owner('org.freedesktop.impl.portal.desktop.gtk', gtk)
        frontend = start('frontend', [str(frontend_path), '--verbose'])
        frontend_owner = owner('org.freedesktop.portal.Desktop', frontend)
        report['providerOwners'] = dict(mako=mako_owner, gtk=gtk_owner, frontend=frontend_owner)
        selected = None
        title = 'Navigation TEST ' + nonce
        if shipping:
            selected = shipping_setup()
            app_id, title, exited = selected['app'], selected['title'], selected['exited']
        else:
            report['notificationAttempted'] = True; persist()
            sender = start('sender', ['/usr/bin/python3', str(SEED / 'client-sender.py')])
            code = sender.wait(timeout=min(12, deadline - now())); exited = now()
            report['senderExit'] = dict(pid=sender.pid, collected=True, exitCode=code, exitedBoot=exited)
            if code or bus('NameHasOwner', app_id) != '(false,)' or (work / 'callback/service-start.json').exists():
                raise RuntimeError('sender_exit_or_cold_callback_absence_unproved')
        notify = [m for m in messages() if m.startswith('method call ') and 'interface=org.freedesktop.Notifications; member=Notify\n' in m and 'sender=' + gtk_owner + ' ' in m.splitlines()[0] and 'string "' + title + '"' in m]
        if len(notify) != 1: raise RuntimeError('sole_owned_native_Notify_unproved')
        serial = re.search(r' serial=(\d+) ', notify[0].splitlines()[0])
        replies = [m for m in messages() if m.startswith('method return ') and 'sender=' + mako_owner + ' ' in m.splitlines()[0] and 'destination=' + gtk_owner + ' ' in m.splitlines()[0] and re.search(r'\breply_serial=' + serial[1] + r'(?:\s|$)', m.splitlines()[0])]
        if len(replies) != 1: raise RuntimeError('native_notification_reply_unbound')
        native_id = re.fullmatch(r'\s+uint32 ([0-9]+)\s*', '\n'.join(replies[0].splitlines()[1:]))
        if not native_id or int(native_id[1]) <= 0: raise RuntimeError('native_notification_ID_invalid')
        report['nativeNotificationID'] = int(native_id[1])
        # Test-only native-banner correlation; shipping binds its opaque key,
        # never this observed frontend ID (AddNotification returns no such ID).
        layers = []
        def surface_ready():
            nonlocal layers
            layers = re.findall(r'get_layer_surface\(new id zwlr_layer_surface_v1@(\d+), wl_surface@(\d+), [^\n]*"notifications"\)', trace())
            return len(layers) == 1 and re.search(r'zwlr_layer_surface_v1@' + layers[0][0] + r'\.configure\(', trace()) is not None
        wait(surface_ready, phase='native_surface_ready')
        surface = layers[0][1]
        configured = re.findall(r'zwlr_layer_surface_v1@' + layers[0][0] + r'\.configure\(\d+, (\d+), (\d+)\)', trace())
        if not configured or not (0 < int(configured[-1][0]) <= 1280 and 0 < int(configured[-1][1]) <= 720):
            raise RuntimeError('native_surface_dimensions_unproved')
        y = int(configured[-1][1]) // 2
        root_spec(ROOT / 'pointer-spec.json', dict(nonce=nonce, y=y, shipping=bool(shipping),
            entrySHA256=manifest['files']['guest-pointer-entry.py'], librarySHA256=manifest['files']['guest-pointer.so']))
        pointer = start('pointer', ['/usr/bin/python3', str(SEED / 'guest-pointer-entry.py')], True, True)
        entry_pattern = r'wl_pointer@(\d+)\.enter\(\d+, wl_surface@' + surface + r','
        def pointer_entered():
            alive(pointer.pid)
            return re.search(entry_pattern, trace()) is not None
        wait(pointer_entered, phase='pointer_enter')
        enters = list(re.finditer(entry_pattern, trace()))
        if len(enters) != 1: raise RuntimeError('ambiguous_pointer_enter')
        for child in (sway, daemon, mako, gtk, frontend, pointer): alive(child.pid)
        pre_click_characters = len(trace())
        report['clickAttempted'] = True; report['clickAttemptedBoot'] = now(); persist()
        if now() >= deadline: raise RuntimeError('deadline_before_sole_native_CLICK')
        pointer.stdin.write(b'CLICK\n'); pointer.stdin.flush()
        if shipping:
            client_pid = None
            def shipping_started():
                nonlocal client_pid
                client_pid = observe_shipping(selected)
                return client_pid is not None
            wait(shipping_started, 12)
            launched = dict(clientBirth=int(kernel.snapshot(client_pid)['startTicks']))
        else:
            wait(lambda: (work / 'callback/handoff-launched.json').exists(), 12)
            capture_group()
            started = json.loads((work / 'callback/service-start.json').read_text())
            launched = json.loads((work / 'callback/handoff-launched.json').read_text())
            hz = os.sysconf('SC_CLK_TCK')
            if hz <= 0 or started['pid'] != launched['pid'] or started['startTicks'] != launched['startTicks'] or started['startTicks'] / hz < exited:
                raise RuntimeError('cold_callback_kernel_birth_unproved')
            selected_peer(started['pid'], 'callback_admission')
            if int(handles[started['pid']][1]['startTicks']) != started['startTicks'] or bus('GetConnectionUnixProcessID', app_id) != '(uint32 ' + str(started['pid']) + ',)':
                raise RuntimeError('cold_callback_owner_unbound')
            client_pid = launched['clientPID']
        client = selected_peer(client_pid, 'client_admission')
        if int(client['startTicks']) != launched['clientBirth'] or client['executable'] != str(kernel.EXE) or sha(Path('/proc', str(client_pid), 'exe')) != kernel.EXE_SHA:
            raise RuntimeError('selected_live_client_identity_unproved')
        if client['appArmor'].split(' (', 1)[0] != 'chatgpt':
            raise RuntimeError('selected_main_vendor_profile_not_attached')
        report['clientProcess'] = kernel.public_snapshot(client)
        report['handoffObserved'] = True
        chain = protocol.native_click_chain(trace(), pre_click_characters, messages(), mako_owner,
            gtk_owner, app_id, nonce, int(native_id[1]), surface, enters[0][1], selected['key'] if shipping else None)
        if not chain['forwarded']: raise RuntimeError('native_platform_token_not_forwarded')
        token_hash = chain['tokenSHA256']
        report['protocolOffsets'] = chain['offsets']
        if shipping:
            environment = report.pop('selectedEnvironment')
            if environment.get('XDG_ACTIVATION_TOKEN') != chain['token'] or environment.get('DESKTOP_STARTUP_ID') != chain['token']:
                raise RuntimeError('actual_selected_process_token_unbound')
            if any(name.startswith('LD_') or name in ('NODE_OPTIONS', 'ELECTRON_RUN_AS_NODE', 'BASH_ENV', 'ENV') for name in environment):
                raise RuntimeError('selected_client_injection_environment')
            report['selectedTokenEnvironment'] = dict(names=sorted(environment), activationTokenSHA256=hashlib.sha256(environment['XDG_ACTIVATION_TOKEN'].encode()).hexdigest(), startupTokenSHA256=hashlib.sha256(environment['DESKTOP_STARTUP_ID'].encode()).hexdigest())
        else:
            observation = json.loads((work / 'callback/platform-data.json').read_text())
            intent = json.loads((work / 'callback/handoff-intent.json').read_text())
            if observation['activationTokenSHA256'] != token_hash or intent['activationTokenSHA256'] != token_hash or intent['count'] != 1:
                raise RuntimeError('callback_and_handoff_token_unbound')
        pointer.stdin.write(b'DONE\n'); pointer.stdin.flush(); pointer.stdin.close()
        if pointer.wait(timeout=3): raise RuntimeError('pointer_press_release_collection_failed')
        pointer_bytes = pointer.stdout.read(4096)
        (ROOT / 'pointer.stdout').write_bytes(pointer_bytes)
        if b'CLICKED\n' not in pointer_bytes: raise RuntimeError('pointer_click_ack_missing')
        report['compositorTokenSHA256'] = token_hash
        report['tokenEqualityObserved'] = True
        # Request evidence is separate from server focus; neither implies chat selection.
        requests = None
        if not shipping:
            client_trace = work / 'callback/client.stderr'
            wait(lambda: bool(client_activation_surfaces(read(client_trace), chain['token'])), 12)
            report['clientActivationRequestObserved'] = True
            requests = client_activation_surfaces(read(client_trace), chain['token'])
            if len(requests) != 1: raise RuntimeError('single_client_activation_surface_unproved')
            report['clientActivationSurfaceID'] = requests[0]
        joined = None
        def joined_focus():
            nonlocal joined
            before = bound_server_surface(client_pid, token_hash)
            if before is None or not server_focus(client_pid): return False
            after = bound_server_surface(client_pid, token_hash)
            if after is None or before != after:
                raise RuntimeError('selected_server_surface_changed_during_focus_observation')
            if requests is not None and after['surface'] != int(requests[0]): raise RuntimeError('client_and_server_activation_surface_mismatch')
            joined = after
            return True
        wait(joined_focus, 12)
        report['serverSurface'] = joined
        if shipping:
            report['clientActivationRequestObserved'] = True
            report['clientActivationSurfaceID'] = str(joined['surface'])
            raw_trace = trace(); last_line = raw_trace.find('\n', chain['offsets'][-1])
            if last_line < 0: raise RuntimeError('complete_raw_token_event_required')
            report['rawProtocolEvidence'] = dict(nativeNotify=notify[0], nativeReply=replies[0],
                pointerSetup=[line for line in raw_trace.splitlines() if '.get_pointer(new id wl_pointer@' + enters[0][1] + ')' in line],
                clickTrace=raw_trace[chain['offsets'][0]:last_line + 1],
                addressedCalls=[m for m in messages() if 'destination=' + app_id + ' ' in m.splitlines()[0]],
                nativeSignals=[m for m in messages() if m.startswith('signal ') and 'sender=' + mako_owner + ' ' in m.splitlines()[0] and ('member=ActivationToken\n' in m or 'member=ActionInvoked\n' in m)], serverRecords=server_records())
        report['serverFocusObserved'] = True
        report['activationRequestAndFocusObserved'] = True
        report['serverSurfaceFocusJoinObserved'] = True
        # Native request + live unique toplevel + focus, not counterfactual token causation.
        report['activationEvidenceClass'] = 'peer_bound_selected_connection_unique_live_toplevel_and_compositor_focus'
        for child in (gtk, frontend, mako, monitor):
            signal.pidfd_send_signal(handles[child.pid][0], signal.SIGTERM)
            remaining = deadline - now()
            if remaining <= 0: raise RuntimeError('qualification_deadline_before_final_provider_collection')
            child.wait(timeout=min(2, remaining))
        final_calls = [m for m in messages() if m.startswith('method call ') and 'interface=org.freedesktop.Application; member=ActivateAction\n' in m and 'destination=' + app_id + ' ' in m.splitlines()[0]]
        if len(final_calls) != 1 or final_calls[0].splitlines()[0] != chain['activateSender']:
            raise RuntimeError('additional_addressed_native_action')
        buttons = re.findall(r'wl_pointer@' + enters[0][1] + r'\.button\(\d+, \d+, 272, ([01])\)', trace())
        if buttons != ['1', '0']: raise RuntimeError('sole_native_press_release_unproved')
        if any((work / 'callback' / name).exists() for name in ('callback-rejected.json', 'installation-rejected.json', 'deadline-rejected.json')):
            raise RuntimeError('callback_rejection_observed')
        if shipping: load('TEST_final_assets', SEED / 'guest-bootstrap.py').shipping_inputs(SEED, manifest)
        if any(sha(SEED / name) != digest for name, digest in manifest['files'].items()):
            raise RuntimeError('TEST_seed_source_changed')
        if now() >= deadline: raise RuntimeError('qualification_deadline_before_final_surface')
        alive(sway.pid); alive(client_pid)
        final_surface = bound_server_surface(client_pid, token_hash)
        if final_surface != joined: raise RuntimeError('selected_server_surface_changed_before_collection')
        if now() >= deadline: raise RuntimeError('qualification_deadline_before_PASS')
        if shipping and any(Path(item['path']).exists() or Path(item['path']).is_symlink() for item in report['removedProducerState']): raise RuntimeError('removed_TEST_producer_state_reappeared')
        if shipping and len(json.dumps(report).encode()) > 90 * 1024: raise RuntimeError('complete_shipping_evidence_frame_bound')
        report['passed'] = True
    except Exception as error:
        report['failure'] = type(error).__name__ + ': ' + str(error)
        if not report['clickAttempted'] and report.get('observationPhase') in ('native_surface_ready', 'pointer_enter'):
            diagnostics = dict(phase=report['observationPhase'], traces={})
            report['preClickFailure'] = diagnostics
            for name in ('pointer.stderr', mako_label + '.stderr'):
                try:
                    fd = os.open(ROOT / name, os.O_RDONLY | os.O_NOFOLLOW)
                    with os.fdopen(fd, 'rb') as stream:
                        before = os.fstat(stream.fileno())
                        if not stat.S_ISREG(before.st_mode) or before.st_uid != 0:
                            raise RuntimeError('owned_TEST_trace_required')
                        offset = max(0, before.st_size - 4096)
                        stream.seek(offset); captured = stream.read(4096)
                        after = os.fstat(stream.fileno())
                    diagnostics['traces'][name] = dict(observedFileBytes=before.st_size,
                        capturedOffset=offset, capturedBytes=len(captured),
                        capturedSHA256=hashlib.sha256(captured).hexdigest(),
                        capturedBase64=base64.b64encode(captured).decode(),
                        wholeFileCaptured=offset == 0 and len(captured) == before.st_size,
                        sampledSizeUnchanged=before.st_size == after.st_size)
                except Exception as trace_error:
                    diagnostics['traces'][name] = dict(readFailure=type(trace_error).__name__)
    finally:
        failures = []
        if group_created:
            try: capture_group(cleanup=True)
            except Exception as error: failures.append('group_observation: ' + str(error))
            try:
                if str(os.getpid()) in (group / 'cgroup.procs').read_text().split(): raise RuntimeError('supervisor_in_owned_cgroup')
                (group / 'cgroup.kill').write_text('1')
            except Exception as error: failures.append('cgroup_kill: ' + str(error))
        end = now() + 5
        for pid, (fd, saved) in handles.items():
            try:
                try: signal.pidfd_send_signal(fd, signal.SIGKILL)
                except ProcessLookupError: pass
                with selectors.DefaultSelector() as selector:
                    selector.register(fd, selectors.EVENT_READ)
                    exited = bool(selector.select(max(0, end - now())))
                report['trackedProcesses'].append(dict(pid=pid, startTicks=saved['startTicks'], kernelExitObserved=exited))
                if not exited: failures.append('kernel_exit_unknown')
            except Exception as error: failures.append('pidfd_collection: ' + str(error))
            finally:
                try: os.close(fd)
                except OSError as error: failures.append('pidfd_close: ' + str(error))
        for child in children:
            try:
                if child.pid not in handles and child.poll() is None: child.kill()
                child.wait(timeout=max(0.01, end - now()))
                if child.stdin and not child.stdin.closed: child.stdin.close()
                if child.stdout: child.stdout.close()
            except Exception as error: failures.append('direct_child_collection: ' + str(error))
        if group_created:
            try:
                empty_deadline = now() + 3
                while 'populated 1' in (group / 'cgroup.events').read_text():
                    if now() >= empty_deadline: raise RuntimeError('owned_cgroup_collection_expired')
                    time.sleep(0.025)
                report['cgroupEmptyObserved'] = True; group.rmdir()
            except Exception as error: failures.append('cgroup_collection: ' + str(error))
        for stream in streams:
            try: stream.close()
            except Exception as error: failures.append('stream_close: ' + str(error))
        report['cleanupErrors'] = failures; report['cleanupPassed'] = not failures
        report['passed'] = report['passed'] and report['cleanupPassed']
        try:
            data = completion_data(report, shipping)
            try:
                if ROOT.exists(): (ROOT / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
            except Exception as error:
                report['passed'] = False; report['publicationError'] = str(error)
                data = completion_data(report, shipping)
            with Path('/dev/ttyS0').open('w') as serial:
                serial.write(('NAVIGATION_TEST_SHIPPING_GO_V1 ' if shipping else 'NAVIGATION_TEST_HANDOFF_V1 ') + hashlib.sha256(data).hexdigest() + ' ' + base64.b64encode(data).decode() + '\n'); serial.flush()
        finally:
            # Serialization/publication failures still attempt bounded guest shutdown.
            subprocess.run(['/usr/bin/systemctl', 'poweroff'], check=True, timeout=20)
    return 0 if report['passed'] else 1


def test_post_cleanup_completion_contract():
    # Cleanup growth used to leave a PASS artifact while preventing any frame.
    shipping = dict(scenario='retained_a', sourceSHA='a' * 40, files={})
    report = dict(passed=True, handoffObserved=True, tokenEqualityObserved=True,
        serverFocusObserved=True, serverSurfaceFocusJoinObserved=True,
        notificationAttempted=True, clickAttempted=True, cleanupPassed=True,
        cgroupEmptyObserved=True, trackedProcesses=[])
    assert json.loads(completion_data(report, shipping))['passed'] is True
    report['trackedProcesses'] = ['inert TEST cleanup evidence'] * 4000
    frame = completion_data(report, shipping)
    assert len(frame) <= 90 * 1024
    decoded = json.loads(frame)
    assert report['passed'] is False and decoded['passed'] is False
    assert decoded['clickAttempted'] is True and decoded['retryAllowed'] is False
    assert decoded['privateDiagnosticsSHA256'] == hashlib.sha256(json.dumps(report, sort_keys=True).encode()).hexdigest()
    assert not any(report[key] or decoded[key] for key in ('handoffQualified', 'tokenForwardingQualified', 'focusQualified', 'activationQualified'))


if __name__ == '__main__':
    raise SystemExit(main())
