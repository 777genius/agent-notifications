"""TEST-only SDK method. Original tracer qualification remains historical HOLD."""
import argparse
import ast
import hashlib
import importlib.util
import inspect
import json
import pathlib
import struct
import sys
import time

SOURCE = 'a7a5ccdcf0b302b5ca668bd08e7bd3a542fd8adb'
FIXTURE_SHA = 'b99478b2b5b23cef65cf58f31d19ff382cfe4ddb4fcb185caa8856255310e113'
METHOD = 'public-sdk-private-dbus-v1'


def load_fixture(path):
    path = pathlib.Path(path).resolve(strict=True)
    assert hashlib.sha256(path.read_bytes()).hexdigest() == FIXTURE_SHA
    spec = importlib.util.spec_from_file_location('original_cursor_case', path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def adapt(m):
    # Keep product provenance exact; this changes no installed command or authority.
    m.SOURCE_SHA = SOURCE
    # Relocate only the exact product revision in the authenticated public bridge.
    assert m.READBACK_SOURCE_SHA256 == '77030250348f8af0ee99878627b59c39cc7d658974c11adca67f7d98a4d4c8fc'
    assert hashlib.sha256(m.READBACK_SOURCE_BYTES).hexdigest() == m.READBACK_SOURCE_SHA256
    old_revision = b'const productRevision = "9f014e72af510783d58d00ad3d2db455a2f8117b"'
    assert m.READBACK_SOURCE_BYTES.count(old_revision) == 1
    m.READBACK_SOURCE_BYTES = m.READBACK_SOURCE_BYTES.replace(old_revision, ('const productRevision = "'+SOURCE+'"').encode())
    m.READBACK_SOURCE_SHA256 = '364fc0ee014fc21106c16eddd6bf4eb4ad9acf66adc11449a5b6288bfbf159a7'
    assert hashlib.sha256(m.READBACK_SOURCE_BYTES).hexdigest() == m.READBACK_SOURCE_SHA256
    # Bounded SDK runtime copies grew beyond the old 25,264,137-byte fixture.
    # The same per-file limit governs RLIMIT_FSIZE and full object readbacks;
    # the original aggregate writable-case limit remains 1 GiB.
    assert m.TEST_RUNTIME_FILE_LIMIT == 25264137
    m.TEST_RUNTIME_FILE_LIMIT = 64 << 20
    inputs = inspect.getsource(m.execution_inputs)
    anchor = "    assert b'=>\\t' not in build_info, 'local module replacement forbidden'"
    assert inputs.count(anchor) == 1
    pins = "    assert b'github.com/777genius/plugin-kit-ai/install/integrationctl\\tv0.1.1-0.20261011092622-35e444fdf875' in build_info\n    assert b'github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins\\tv0.0.0-20261011092622-35e444fdf875' in build_info\n"
    exec(compile(inputs.replace(anchor, pins+anchor), '<SDK-current-input-pins>', 'exec'), m.__dict__)
    original_contract = inspect.getsource(m.require_native_observation_contract)
    original_contract = original_contract.replace("'nsenter','setpriv'}", "'nsenter','setpriv','newgidmap','ip'}")
    original_contract = original_contract.replace("    raise IncompleteInstalledContract('R1360 TEST increment: full Linux proof/exact review/carrier qualification pending; native ineligible')\n", '')
    original_contract = original_contract.replace("capabilities & (1<<19) and capabilities & (1<<21)", "capabilities & (1<<21)")
    original_contract = original_contract.replace("existing SYS_PTRACE/SYS_ADMIN required", "existing SYS_ADMIN required")
    original_contract = original_contract.replace("    assert pathlib.Path('/proc/sys/kernel/yama/ptrace_scope').read_text().strip() in ('0','1','2'), 'existing Yama attach permission'\n", '')
    exec(compile(original_contract, '<SDK-package-qualification>', 'exec'), m.__dict__)

    class SDKSession(m.OwnedNotificationsSession):
        def message(self, message):
            h = message['header']
            ownership = message['type'] == 4 and h.get(2) == 'org.freedesktop.DBus' and h.get(3) == 'NameOwnerChanged'
            with self.records_changed:
                if ownership:
                    assert h.get(7) == 'org.freedesktop.DBus' and h.get(1) == '/org/freedesktop/DBus'
                    assert message['signature'] == 'sss' and len(message['body']) == 3
                    name, old, new = message['body']
                    if name.startswith(':'):
                        assert m.re.fullmatch(r':\d+\.\d+', name)
                        if old == '' and new == name: self.active_names.add(name)
                        elif old == name and new == '': self.active_names.discard(name)
                        else: raise AssertionError('unique bus name replacement')
                    if name == 'org.freedesktop.Notifications' and self.exchange is not None:
                        assert self.closing or old == '' and new == self.owner
                    if old == name and new == '' and self.exchange is not None:
                        assert not any(key[0] == name for key in self.exchange.pending)
                self.messages.append(message)
                assert len(self.messages) <= 4096 and len(json.dumps(self.messages)) <= 16*m.LIMIT
                if self.exchange is not None:
                    sender = h.get(7)
                    # The isolated owned bus, exact owner and real return pair are
                    # transport authority. No short-lived PID credential RPC race.
                    candidate = message['type'] == 1 and sender in self.active_names and sender not in self.control_names
                    relevant = h.get(3) in ('GetCapabilities', 'Notify') or h.get(3) == 'NameHasOwner' and message['body'] == ['org.freedesktop.Notifications']
                    if candidate and relevant: self.exchange.call(message, sender)
                    if message['type'] in (2, 3) and (h.get(6), h.get(5)) in self.exchange.pending:
                        self.exchange.reply(message)
                self.records_changed.notify_all()

        def checkpoint(self, helper=None, enabled=False):
            self.assert_continuous()
            guid, call = self.rpc('org.freedesktop.DBus.GetId', [], 's')
            assert guid == self.guid
            self.observed_roundtrip(call)
            self.exchange.finish()
            assert len(self.exchange.receipts) == 1, 'one actual enabled Notify; no duplicate/disabled Notify'
            (self.root/'evidence/notifications-actual.json').write_text(json.dumps({
                'observationMethod': METHOD, 'busGUID': self.guid, 'owner': self.owner,
                'receipts': self.exchange.receipts, 'messages': self.messages,
                'credentials': {}, 'helperPIDQualification': 'NOT_RUN', 'GUIVisibility': 'NOT_RUN'}, indent=2)+'\n')

    class SDKOuter(m.OuterObservation):
        def fresh_local(self, local, thread=False):
            paths = (pathlib.Path('/proc')/str(self.controller['pid'])/'task').iterdir() if thread else pathlib.Path('/proc').iterdir()
            matches = []
            for path in paths:
                if not path.name.isdecimal(): continue
                try:
                    if m.os.readlink(path/'ns/pid') != self.controller['ns']['pid']: continue
                    raw = (path/'stat').read_text()
                    if int(raw[raw.rindex(')')+2:].split()[19]) != local['birth']: continue
                    status = dict(row.split(':', 1) for row in (path/'status').read_text().splitlines() if ':' in row)
                    if int(status['NSpid'].split()[-1]) != local['tid' if thread else 'pid']: continue
                    facts = m.trace_task_identity(int(path.name)) if thread else m.proc_identity(int(path.name))
                except FileNotFoundError: continue
                if facts['birth'] == local['birth'] and facts['nspid'][-1] == local['tid' if thread else 'pid'] and facts['ns'] == self.controller['ns']:
                    matches.append(facts)
            assert len(matches) == 1, 'one fresh local PID/birth/namespace identity'
            facts = matches[0]
            assert facts['uids'] == facts['gids'] == [1000]*4 and facts['groups'] == [] and facts['tracer'] == 0
            if thread: assert facts['tgid'] == self.controller['pid']
            return facts

        def root_facts(self, local, held_nets=None):
            assert m.proc_identity(self.controller['pid'],self.controller)['ns'] == self.controller['ns']
            matches = []
            # Newest candidates first; kernel PID namespace/NSpid uniqueness remains checked.
            paths = sorted((p for p in pathlib.Path('/proc').iterdir() if p.name.isdecimal()), key=lambda p:int(p.name), reverse=True)
            for path in paths:
                try:
                    if m.os.readlink(path/'ns/pid') != self.controller['ns']['pid']: continue
                    status = dict(line.split(':',1) for line in (path/'status').read_text().splitlines() if ':' in line)
                    if int(status['NSpid'].split()[-1]) != local['stat'][0]: continue
                    fields = (path/'stat').read_text().rsplit(')',1)[1].split()
                    birth = int(fields[19])
                    if fields[0] == 'Z': continue
                    assert birth == local['stat'][5], 'sandbox PID reused'
                    if held_nets is not None:
                        fd = m.os.open(path/'ns/net',m.os.O_RDONLY|m.os.O_CLOEXEC)
                        self.session.owned_fds.add(fd); held_nets[int(path.name)] = fd
                        assert m.os.readlink('/proc/self/fd/'+str(fd)) == local['netNamespace'], 'early namespace changed'
                    facts = m.proc_identity(int(path.name))
                    if held_nets is not None:
                        assert m.os.readlink('/proc/self/fd/'+str(fd)) == facts['ns']['net'], 'early namespace identity changed'
                    matches.append(facts)
                except FileNotFoundError: continue
            assert m.proc_identity(self.controller['pid'],self.controller)['ns'] == self.controller['ns']
            assert len(matches) <= 1, 'sandbox mapping ambiguity'
            return matches[0] if matches else None

        def sandbox_role(self, facts):
            argv = facts['argv']; root = pathlib.Path(self.inputs['root'])
            expected = str(pathlib.Path(self.inputs['artifact'])/'cursorsandbox')
            assert facts['exe'] == expected and len(argv) == 6 and argv[:2] == [expected,'--policy']
            assert argv[3:] == ['--preflight-only','--','/bin/true'], 'only vendor preflight'
            policy = pathlib.Path(argv[2]); assert policy.parent == root/'home/.cursor/sandbox-policies'
            assert m.re.fullmatch(r'sandbox-policy-[0-9a-f]{16}',policy.name)
            m.physical(policy); st = policy.stat()
            assert st.st_uid == st.st_gid == 1000 and m.stat.S_ISREG(st.st_mode) and st.st_size <= 65536
            assert facts['uids'] == facts['gids'] == [1000]*4 and facts['groups'] == []
            assert facts['cwd'] == str(root/'workspace')
            return {'path':str(policy),'sha256':m.digest(policy),'bytes':st.st_size}

        def sandbox_readback(self, snapshot):
            assert snapshot['namespace'] == self.controller['ns']['pid'] and snapshot['netNamespace'] == self.controller['ns']['net']
            assert self.active_phase and not self.closed and self.clock.work is not None
            rows = {row['stat'][0]:row for row in snapshot['rows']}; output = {}
            # Persist actual request before qualification, including refusals/unknown exits.
            received = pathlib.Path(self.inputs['root'])/'evidence'/self.active_phase/'sandbox-root-candidates.jsonl'
            raw = (json.dumps(snapshot)+'\n').encode()
            assert len(raw)+(received.stat().st_size if received.exists() else 0) <= 16*m.LIMIT
            with received.open('ab') as stream: stream.write(raw)
            for local in rows.values():
                argv = local.get('argv',[])
                sandbox = argv and argv[0] == str(pathlib.Path(self.inputs['artifact'])/'cursorsandbox')
                mapper = argv and argv[0] == '/usr/bin/newgidmap'
                if not (sandbox and local.get('netNamespace') != snapshot['netNamespace'] or mapper): continue
                held_nets = {}
                try:
                    facts = self.root_facts(local,held_nets)
                    if facts is None:
                        assert local.get('netNamespace',snapshot['netNamespace']) == snapshot['netNamespace'], 'observed foreign namespace exited without containment proof'
                        output[str(local['stat'][0])] = {'exited':True,'tuple':[local['stat'][0],local['stat'][5]]}; continue
                    assert facts['ns']['pid'] == self.controller['ns']['pid'] and facts['tracer'] == 0
                    assert facts['pgid'] == facts['sid'] == self.native_leader['pid'] and facts['birth'] >= self.native_leader['birth']
                    assert facts['argv'] == argv and facts['nspid'][-1] == local['stat'][0]
                    assert facts['cwd'] == str(pathlib.Path(self.inputs['root'])/'workspace')
                    chain = facts; ancestors = []; actual_parent = None
                    while chain['pid'] != self.native_leader['pid']:
                        ancestors.append(chain)
                        assert len(ancestors) <= 65
                        parent_pid = chain['ppid']
                        if parent_pid == self.native_leader['pid']:
                            chain = m.proc_identity(parent_pid,self.native_leader)
                        else:
                            parent_local = chain['nspid'][-1]
                            parent_row = rows[parent_local]
                            parent_row = rows[parent_row['stat'][1]]
                            chain = self.root_facts(parent_row)
                            assert chain is not None, 'preflight ancestor exited before qualification'
                            assert chain['pid'] == parent_pid, 'preflight ancestry changed'
                        if actual_parent is None: actual_parent = chain['nspid'][-1]
                        assert chain['ns']['pid'] == self.controller['ns']['pid'] and chain['pgid'] == chain['sid'] == self.native_leader['pid']
                    assert chain['birth'] == self.native_leader['birth']
                    target = facts
                    if mapper:
                        tool = self.inputs['tools']['newgidmap']; path = pathlib.Path(tool['path']); st = path.stat()
                        assert facts['exe'] == str(path) == '/usr/bin/newgidmap' and st.st_uid == st.st_gid == 0
                        assert m.stat.S_ISREG(st.st_mode) and st.st_mode & m.stat.S_ISUID and m.digest(path) == tool['sha256']
                        assert len(argv) == 5 and argv[2:] == ['0','1000','1'] and facts['argv'] == argv
                        assert facts['uids'] in ([1000]*4,[1000,0,0,0]) and facts['gids'] == [1000]*4 and facts['groups'] == []
                        assert facts['ns']['net'] == self.controller['ns']['net']
                        target_row = rows[int(argv[1])]; target = self.root_facts(target_row,held_nets)
                        assert target is not None and any(a['pid'] == target['pid'] and a['birth'] == target['birth'] for a in ancestors)
                    for ancestor in ancestors[1 if mapper else 0:]: self.sandbox_role(ancestor)
                    policy = self.sandbox_role(target)
                    assert target['ns']['net'] not in (self.controller['ns']['net'],m.os.readlink('/proc/self/ns/net'))
                    assert target['uidMap'].split() == ['0','1000','1']
                    map_deadline = time.monotonic()+self.clock.wait(.2)
                    while target['gidMap'].split() == [] and time.monotonic() < map_deadline:
                        time.sleep(min(.005,max(0,map_deadline-time.monotonic())))
                        target = m.proc_identity(target['pid'],target)
                    assert target['gidMap'].split() == ['0','1000','1'], 'incomplete or foreign preflight GID map'
                    fd = held_nets[target['pid']]  # retained before complete identity/ancestry/map reads
                    try:
                        held = m.os.fstat(fd); assert m.os.readlink('/proc/self/fd/'+str(fd)) == target['ns']['net']
                        records = []
                        for args in (['address','show'],['route','show'],['-6','route','show']):
                            command = [self.inputs['tools']['nsenter']['path'],'--net=/proc/self/fd/'+str(fd),self.inputs['tools']['ip']['path'],'-j',*args]
                            child = m.subprocess.Popen(command,stdout=m.subprocess.PIPE,stderr=m.subprocess.PIPE,env={'PATH':'/usr/bin:/bin','LANG':'C'},pass_fds=(fd,))
                            # Provisional Popen custody only; no executable/bus admission.
                            record = {'name':'nsenter','child':child,'identity':None,'admitted':False}
                            self.session.control_processes.append(record)
                            assert len(self.session.control_processes) <= 128
                            try: out,err = child.communicate(timeout=self.clock.wait(1))
                            except BaseException:
                                began = time.monotonic()
                                try:
                                    child.kill()
                                    child.communicate(timeout=max(.001,self.clock.cleanup_deadline()-time.monotonic()))
                                finally:
                                    for pipe in (child.stdin,child.stdout,child.stderr):
                                        if pipe is not None: pipe.close()
                                    self.clock.charge_cleanup(began)
                                raise
                            finally:
                                if child.poll() is not None:
                                    for pipe in (child.stdin,child.stdout,child.stderr):
                                        if pipe is not None: pipe.close()
                            assert child.returncode == 0 and not err and len(out) <= 65536
                            records.append({'argv':command,'pid':child.pid,'exit':0,'joined':True,'result':m.strict_json(out)})
                        assert len(records[0]['result']) == 1 and records[0]['result'][0]['ifname'] == 'lo'
                        assert records[1]['result'] == records[2]['result'] == [], 'nested namespace external routes'
                        assert m.os.fstat(fd) == held and m.os.readlink('/proc/'+str(target['pid'])+'/ns/net') == target['ns']['net']
                        after = m.proc_identity(target['pid'],target); assert after == target, 'nested authority changed'
                        current = m.proc_identity(facts['pid'],facts)
                        if not mapper: facts = target
                        assert current == facts, 'map/helper authority changed'
                        proof = {'role':'newgidmap' if mapper else 'sandbox-preflight','tuple':[local['stat'][0],facts['birth']],
                                 'localIdentity':[actual_parent,self.native_leader['nspid'][-1],self.native_leader['nspid'][-1],facts['birth']],
                                 'netNamespace':facts['ns']['net'],'target':target,'identity':facts,'policy':policy,'records':records,'fdClosed':True}
                        output[str(local['stat'][0])] = proof
                    finally:
                        m.os.close(fd); self.session.owned_fds.remove(fd); held_nets.pop(target['pid'])
                except FileNotFoundError:
                    assert local.get('netNamespace',snapshot['netNamespace']) == snapshot['netNamespace'], 'observed foreign namespace lost before containment proof'
                    assert self.root_facts(local) is None, 'live candidate metadata failure'
                    output[str(local['stat'][0])] = {'exited':True,'tuple':[local['stat'][0],local['stat'][5]]}
                finally:
                    for early_fd in held_nets.values():
                        m.os.close(early_fd); self.session.owned_fds.remove(early_fd)
            evidence = pathlib.Path(self.inputs['root'])/'evidence'/self.active_phase/'sandbox-root-readbacks.jsonl'
            raw = (json.dumps(output)+'\n').encode(); assert len(raw)+(evidence.stat().st_size if evidence.exists() else 0) <= 16*m.LIMIT
            with evidence.open('ab') as stream: stream.write(raw)
            return output

        def request(self):
            message = m.control_receive(self.peer, self.clock)
            assert message['sequence'] == self.sequence+1
            self.sequence += 1
            if self.nonce is None: self.nonce = message['nonce']
            assert message['nonce'] == self.nonce and m.re.fullmatch(r'[0-9a-f]{64}', self.nonce)
            self.clock.cleanup_used = max(self.clock.cleanup_used, message['cleanupUsed'])
            assert 0 <= self.clock.cleanup_used < 8
            reply = {'sequence': self.sequence, 'nonce': self.nonce, 'ok': True}
            action = message['action']
            expected = 'enabled-native' if not self.phases else 'disabled-restart'
            if action == 'prepare':
                assert self.controller is None and self.session is None and self.trace is None
                self.controller = self.controller_identity(message['controller'])
                self.empty_native()
                self.session = SDKSession.__new__(SDKSession)
                self.session.__init__(self.controller, self.inputs['root'], self.clock, self.inputs['tools'], self.inputs['displayNumber'])
                self.empty_native()
                reply.update(environment={k: self.session.env[k] for k in ('DBUS_SESSION_BUS_ADDRESS', 'DISPLAY', 'XAUTHORITY')}, controller=self.controller)
            elif action == 'begin-phase':
                assert len(self.phases) < 2 and message['phase'] == expected and not self.closed
                self.empty_native(); self.session.assert_continuous()
                self.active_phase = expected
            elif action == 'sandbox-readback':
                reply['sandbox'] = self.sandbox_readback(message['snapshot'])
            elif action == 'controller-thread':
                assert message['phase'] == self.active_phase == expected and self.clock.work is not None and not self.closed
                local = message['thread']
                assert set(local) == {'tid', 'birth'} and type(local['tid']) is int and type(local['birth']) is int
                self.fresh_local(local, True)
                reply['controllerThread'] = dict(local, phase=expected)
            elif action == 'leader':
                assert message['phase'] == self.active_phase == expected and len(self.phases) < 2
                if self.clock.work is None: self.clock.work = min(message['nativeStart']+90, self.clock.outer-8)
                self.clock.wait(5)
                self.native_leader = self.fresh_local(message['localLeader'])
                reply['leader'] = self.native_leader
                self.session.assert_continuous()
            elif action == 'phase':
                assert message['phase'] == self.active_phase == expected and len(self.phases) < 2
                self.empty_native(); self.session.checkpoint(enabled=expected == 'enabled-native')
                facts = message['sdkFacts']
                assert set(facts) == {'conversation', 'generation', 'status', 'loopCountKnown', 'loopCount'}
                assert facts['status'] == 'completed' and facts['loopCountKnown'] is True and facts['loopCount'] == 0
                assert message['sdkAttemptBitsPresent'] == (expected == 'enabled-native')
                if self.phases:
                    assert all(facts[k] != self.phases[0]['facts'][k] for k in ('conversation', 'generation'))
                row = {'phase': expected, 'facts': facts, 'observationMethod': METHOD, 'sdkAttemptBitsPresent': message['sdkAttemptBitsPresent']}
                self.phases.append(row)
                reply.update(row)
            elif action == 'checkpoint':
                assert self.phases
                self.empty_native(); self.session.checkpoint()
            elif action == 'close':
                assert len(self.phases) == 2
                self.empty_native(); self.session.checkpoint(); self.session.close()
                self.closed = True
                reply['cleanupUsed'] = self.clock.cleanup_used
            else: raise AssertionError('unknown outer control action')
            m.control_send(self.peer, reply, self.clock)

    def sdk_effects(root, run, installed, observation, phase, leader, clock, cleanup_signal, cleanup_roles):
        primary, selector, binding, generation = installed
        ledger = m.strict_json((pathlib.Path(binding['controlRoot'])/'ownership.json').read_bytes())
        selected = [key for key, value in ledger['Consumers'].items() if value['Registration'] == selector.read_text()]
        assert len(selected) == 1
        public_phase = 'enabled' if phase == 'enabled-native' else 'updated'
        public = m.strict_json((root/'evidence'/(public_phase+'-authoritative-readback.json')).read_bytes())['consumerBinding']
        assert public['Generation'] == ledger['Generation']
        assert public['InstallationID'] == binding['installationID'] and public['BindingID'] == selected[0]
        fields = [public['InstallationID'], public['BindingID'], public['ProfileIdentity'], str(ledger['Generation']), public['Product'], run['conversation'], run['generation'], 'stop', 'completed', 'true', '0']
        marker = hashlib.sha256(b''.join(struct.pack('>Q', len(s.encode()))+s.encode() for s in fields)).hexdigest()
        cache = pathlib.Path(binding['dataRoot'])/'observations.json'
        entries = m.strict_json(cache.read_bytes())['entries']
        matching = [row for row in entries if row['key'] == marker]
        enabled = phase == 'enabled-native'
        assert len(matching) == (1 if enabled else 0)
        if enabled: assert matching[0]['bits'] == 3, 'actual persisted desktop+webhook attempt bits'
        facts = dict(conversation=run['conversation'], generation=run['generation'], status='completed', loopCountKnown=True, loopCount=0)
        reply = observation.exchange('phase', phase=phase, localLeader=leader, cleanupUsed=clock.cleanup_used, cleanupSignal=cleanup_signal, cleanupRoles=cleanup_roles, sdkFacts=facts, sdkAttemptBitsPresent=enabled)
        assert reply['facts'] == facts and reply['observationMethod'] == METHOD
        # Explicit reconstruction, never labeled captured or exact genuine stdin.
        payload = json.dumps({'conversation_id': run['conversation'], 'generation_id': run['generation'], 'hook_event_name': 'stop', 'status': 'completed', 'loop_count': 0, 'cursor_version': '2026.09.28-64d2043'}, separators=(',', ':')).encode()
        record = {'observationMethod': METHOD, 'facts': facts, 'cacheMarker': marker, 'sdkAttemptBitsPresent': enabled, 'stdinCapture': 'NOT_RUN', 'duplicatePayload': 'RECONSTRUCTED', 'cacheSHA256': m.digest(cache)}
        (root/'evidence'/phase/'sdk-observation.json').write_text(json.dumps(record, indent=2)+'\n')
        return dict(record, nativeBytes=payload, facts=facts)

    def track_events(data):
        assert len(data) <= 65536, 'TEST telemetry body bound'
        body = m.strict_json(data); assert set(body) == {'events'} and type(body['events']) is list and 1 <= len(body['events']) <= 200
        for event in body['events']:
            assert type(event) is dict and set(event) == {'eventName','eventData','timestamp'}
            assert type(event['eventName']) is str and 0 < len(event['eventName'].encode()) <= 256
            assert type(event['timestamp']) is str and m.re.fullmatch(r'-?(0|[1-9][0-9]{0,18})',event['timestamp']) and -(1<<63) <= int(event['timestamp']) < 1<<63
            assert type(event['eventData']) is dict and len(event['eventData']) <= 128
            for key,value in event['eventData'].items():
                assert len(key.encode()) <= 256 and type(value) is dict and len(value) == 1
                kind,item = next(iter(value.items()))
                assert kind in ('stringValue','doubleValue','boolValue','intValue')
                assert (kind == 'stringValue' and type(item) is str or kind == 'boolValue' and type(item) is bool or kind == 'doubleValue' and type(item) in (int,float) and item == item and abs(item) != float('inf') or kind == 'intValue' and type(item) is str and m.re.fullmatch(r'-?(0|[1-9][0-9]{0,18})',item) and -(1<<63) <= int(item) < 1<<63)
        return len(body['events'])
    m.sdk_track_events = track_events

    # Restart inventories retain the same exact vendor socket policy as after.
    native = inspect.getsource(m.native_run)
    before = "    native_before = {'home':bounded_tree(root/'home'),'profile':bounded_tree(root/'home/.cursor'),'cache':bounded_tree(root/'cache')}"
    assert native.count(before) == 1
    native = native.replace(before, "    native_before = {'home':bounded_tree(root/'home',vendor_root=root),'profile':bounded_tree(root/'home/.cursor',vendor_root=root),'cache':bounded_tree(root/'cache')}")
    old_refusal = "                capture({'unsupportedRPC':self.path})\n                self.answer(501,b'{\"code\":\"unimplemented\",\"message\":\"TEST finite allowlist\"}','application/json')\n                assert self.path in REFUSALS, 'unknown server effects'"
    new_refusal = """                telemetry = self.path == '/aiserver.v1.AnalyticsService/TrackEvents'
                assert telemetry or self.path in REFUSALS, 'unknown server effects'
                if telemetry:
                    assert self.headers.get('Content-Type','').split(';',1)[0] == 'application/json'
                    capture({'TESTTelemetryCollected':self.path,'eventCount':sdk_track_events(data),'bodySHA256':hashlib.sha256(data).hexdigest()})
                else: capture({'unsupportedRPC':self.path})
                try:
                    if telemetry: self.answer(200,b'{}','application/json')
                    else: self.answer(501,b'{"code":"unimplemented","message":"TEST finite allowlist"}','application/json')
                except BrokenPipeError:
                    capture({'advisoryResponseAborted':self.path,'bodySHA256':hashlib.sha256(data).hexdigest(),'bodyBytes':len(data),'responseWriteCompleted':False,'status':200 if telemetry else 501,'syntheticAuthMatches':True})"""
    assert native.count(old_refusal) == 1
    native = native.replace(old_refusal,new_refusal)
    exec(compile(native,'<SDK-native-before-inventory>','exec'),m.__dict__)

    original_snapshot = m.process_snapshot
    def root_snapshot(procs, namespace):
        snapshot = original_snapshot(procs, namespace)
        candidates = any(row.get('netNamespace',snapshot['netNamespace']) != snapshot['netNamespace'] or row.get('argv',[None])[0:1] == ['/usr/bin/newgidmap'] for row in snapshot['rows'])
        if not candidates: return snapshot
        reply = m.SDK_CONTROLLER.exchange('sandbox-readback', snapshot=snapshot)['sandbox']
        for row in snapshot['rows']:
            proof = reply.get(str(row['stat'][0]))
            if proof is None: continue
            assert proof['tuple'] == [row['stat'][0],row['stat'][5]]
            if proof.get('exited'):
                assert row.get('netNamespace',snapshot['netNamespace']) == snapshot['netNamespace'], 'foreign namespace unqualified exit'
                row['sdkExited'] = True; continue
            facts = proof['identity']
            assert facts['nspid'][-1] == row['stat'][0] and facts['argv'] == row['argv']
            row.update(exe=facts['exe'],cwd=facts['cwd'],namespace=facts['ns']['pid'],netNamespace=facts['ns']['net'],uid=list(map(str,facts['uids'])),freshIdentity=proof['localIdentity'],sdkRootProof=proof)
            row.pop('readFailure',None); row.pop('refusalStage',None)
        return snapshot
    def qualified(row, role=None):
        proof = row.get('sdkRootProof',{})
        return proof.get('role') in ((role,) if role else ('sandbox-preflight','newgidmap')) and proof['tuple'] == [row['stat'][0],row['stat'][5]] and proof['netNamespace'] == row['netNamespace'] and proof['fdClosed'] is True
    m.sdk_qualified = qualified
    m.process_snapshot = root_snapshot
    for name in ('complete_process_facts','active_descendants','active_namespace'):
        source = inspect.getsource(getattr(m,name))
        source = source.replace("row['uid'] == ['1000']*4", "(row['uid'] == ['1000']*4 or sdk_qualified(row,'newgidmap'))")
        source = source.replace("row['netNamespace'] == snapshot['netNamespace']", "(row['netNamespace'] == snapshot['netNamespace'] or sdk_qualified(row,'sandbox-preflight'))")
        source = source.replace("all(r['netNamespace'] == network for r in snapshot['rows'])", "all(r['netNamespace'] == network or sdk_qualified(r,'sandbox-preflight') for r in snapshot['rows'])")
        if name == 'active_descendants':
            source = source.replace('    active_resource_bounds(snapshot)', "    active_resource_bounds(snapshot)\n    assert all(not row.get('sdkExited') or row.get('netNamespace',snapshot['netNamespace']) == snapshot['netNamespace'] for row in snapshot['rows']), 'foreign namespace unqualified exit'\n    if any(row.get('sdkExited') for row in snapshot['rows']): raise ProcessObservationRace('root bookended exact tuple exit')",1)
        if name == 'complete_process_facts':
            source = source.replace("        p = row['stat']", "        p = row['stat']\n        if row.get('sdkExited'):\n            assert row.get('netNamespace',snapshot['netNamespace']) == snapshot['netNamespace'], 'foreign namespace unqualified exit'\n            race = ProcessObservationRace('root observed exact owned tuple exit'); continue",1)
        exec(compile(source,'<SDK-sandbox-'+name+'>','exec'),m.__dict__)

    m.OwnedNotificationsSession = SDKSession
    m.OuterObservation = SDKOuter
    m.validate_installed_effects = sdk_effects
    def owned_empty_lock(old,new):
        assert old == new and new and new['mode'] == m.stat.S_IFREG|0o600
        assert new['uid'] == new['gid'] == 1000 and new['nlink'] == 1 and new['byteLength'] == 0 and new['sha256'] == hashlib.sha256(b'').hexdigest()
    m.sdk_owned_empty_lock = owned_empty_lock
    def mutation_metadata(root):
        path = root/'home/.config/uap/state/mutation.lock'; m.physical(path)
        fd = m.os.open(path,m.os.O_RDONLY|m.os.O_NOFOLLOW|m.os.O_CLOEXEC)
        try:
            st = m.os.fstat(fd); raw = m.os.read(fd,4097)
            assert m.stat.S_ISREG(st.st_mode) and st.st_uid == st.st_gid == 1000 and st.st_nlink == 1 and st.st_size == len(raw) <= 4096
            final = m.os.fstat(fd)
            assert all(getattr(st,k) == getattr(final,k) for k in ('st_mode','st_uid','st_gid','st_dev','st_ino','st_nlink','st_size','st_mtime_ns','st_ctime_ns'))
        finally: m.os.close(fd)
        obj = m.strict_json(raw)
        assert set(obj) == {'pid','acquired_at'} and type(obj['pid']) is int and obj['pid'] > 1
        assert type(obj['acquired_at']) is str and m.re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z',obj['acquired_at'])
        import datetime
        stamp = datetime.datetime.fromisoformat(obj['acquired_at'].replace('Z','+00:00'))
        fraction = obj['acquired_at'].removesuffix('Z').split('.',1)[1] if '.' in obj['acquired_at'] else ''
        return {'sha256':hashlib.sha256(raw).hexdigest(),'byteLength':len(raw),'metadata':obj,'stamp':(stamp.replace(microsecond=0),int(fraction.ljust(9,'0') or '0'))}
    m.sdk_mutation_metadata = mutation_metadata
    removal = inspect.getsource(m.classify_removal)
    current = "pathlib.Path(AUTHORITY_BASELINE[str(root)]['fixed']['Selector'])"
    assert removal.count(current) == 1
    removal = removal.replace(current,"pathlib.Path(selected['selected_delivery']['cursor']['selector'])")
    anchor = '    result = []\n    for area in ('
    extra = "    cursor = selected['selected_delivery']['cursor']\n    managed_selector = pathlib.Path(cursor['selector'])\n    assert managed_selector.parent == pathlib.Path(binding['dataRoot']) and cursor['planned_receipt']['selector'] == str(managed_selector)\n    keys = [k for k,v in before_ledger['Consumers'].items() if strict_json(v['Registration']) == binding]\n    assert len(keys) == 1 and keys[0] == 'portable:'+before['data'][managed_selector.name]['sha256']\n    registration = before_ledger['Consumers'][keys[0]]['Registration'].encode()\n    assert managed_selector.name == 'agent-notify-'+hashlib.sha256(registration).hexdigest()+'.json' and before['data'][managed_selector.name]['byteLength'] == len(registration)\n    live = json.dumps({'cursor':binding['scopeRoot']},separators=(',',':')).encode()\n    assert selected['native_profile_root'] == selected['profile_authority']['canonical_root'] == binding['scopeRoot']\n    for name,raw in ((managed_selector.name,registration),('live-profiles.json',live)):\n        facts = before['data'][name]\n        assert name not in foreign.get('data',{}) and str((pathlib.Path(binding['dataRoot'])/name).relative_to(root/'home')) not in foreign['home']\n        assert facts['mode'] == stat.S_IFREG|0o600 and facts['uid'] == facts['gid'] == 1000 and facts['nlink'] == 1 and facts['device'] == before['data']['.']['device']\n        assert facts['sha256'] == hashlib.sha256(raw).hexdigest() and facts['byteLength'] == len(raw)\n"
    assert removal.count(anchor) == 1; removal = removal.replace(anchor,extra+anchor)
    anchor = "            if disposition is None and area == 'data':\n                assert old is not None"
    extra = "            if disposition is None and area == 'data' and path == 'live-profiles.json':\n                assert old is not None and new is None, 'selected managed live profile remainder'\n                disposition = 'owned selected managed live-profile projection removed'\n"
    assert removal.count(anchor) == 1; removal = removal.replace(anchor,extra+anchor)

    anchor = "            if disposition is None and area == 'profile' and path == 'hooks.json':"
    extra = "            if disposition is None and area == 'profile' and path == 'hooks.json.agentplugins.lock' and path not in foreign['profile']:\n                sdk_owned_empty_lock(old,new)\n                assert new['device'] == before['profile']['.']['device']\n                disposition = 'owned retained SDK35e nativeconfig exact hooks writer lock'\n"
    assert removal.count(anchor) == 1; removal = removal.replace(anchor,extra+anchor)
    anchor = '    home_result = classify_home_removal('
    extra = "    policy_path = pathlib.Path(binding['controlRoot'])/'policy-generation.json'\n    policy = strict_json(policy_path.read_bytes())\n    assert policy == {'Generation':after_ledger['PolicyGeneration'],'Enabled':after_ledger['Enabled']}\n    assert digest(policy_path) == after['home'][str(policy_path.relative_to(root/'home'))]['sha256']\n"
    assert removal.count(anchor) == 1; removal = removal.replace(anchor,extra+anchor)
    exec(compile(removal,'<SDK-removal-retention>','exec'),m.__dict__)
    home = inspect.getsource(m.classify_home_removal)
    anchor = '    result = []\n    for path in sorted(set(before)|set(after)):'
    extra = "    semantic[str((control/'policy-generation.json').relative_to(home))] = 'owned policy generation exactly matches public ledger'\n    source = pathlib.Path(memberships['selectedCommittedAfter']['source']['canonical_source'])\n    cache = home/'.config/uap/acquired-source'\n    assert source.parent.parent == cache and source.name == 'root' and re.fullmatch(r'[0-9a-f]{64}',source.parent.name)\n    cache_prefix = str(source.parent.relative_to(home))\n"
    assert home.count(anchor) == 1; home = home.replace(anchor,extra+anchor)
    anchor = "        if disposition is None and old and new and stat.S_ISDIR(new['mode']):"
    extra = "        if disposition is None and path in (str((control/'.setup-coordinator.lock').relative_to(home)),str((control/'agent-notifications.json.lock').relative_to(home))):\n            sdk_owned_empty_lock(old,new); disposition = 'owned retained exact coordinator/policy lock'\n        if disposition is None and (path in ('.config/uap/acquired-source','.config/uap/state/tmp','.config/uap/state/client-facts.json') or path == cache_prefix or path.startswith(cache_prefix+'/')):\n            assert old and new and old['uid'] == old['gid'] == new['uid'] == new['gid'] == 1000 and same_preserved_object(old,new,True), 'owned recorded source/cache identity or bytes changed'\n            disposition = 'owned retained recorded SDK source/target cache or staging parent'\n        if disposition is None and path == '.config/uap/state/mutation.lock':\n            assert old and new and all(old[k] == new[k] for k in ('mode','uid','gid','device','inode','nlink')) and new['mode'] == stat.S_IFREG|0o600 and new['uid'] == new['gid'] == 1000 and new['nlink'] == 1\n            for side,facts in (('before',old),('after',new)):\n                receipt = SDK_REMOVAL_METADATA[side]; assert all(facts[k] == receipt[k] for k in ('sha256','byteLength'))\n            assert SDK_REMOVAL_METADATA['after']['stamp'] >= SDK_REMOVAL_METADATA['before']['stamp']\n            disposition = 'owned retained SDK processlock inode; bounded acquisition metadata renewed'\n"
    assert home.count(anchor) == 1; home = home.replace(anchor,extra+anchor)
    exec(compile(home,'<SDK-home-retention>','exec'),m.__dict__)

    # Foreign preimage must retain the same exact socket object during removal.
    preimage = inspect.getsource(m.foreign_preimage)
    assert preimage.count("bounded_tree(root/'home')") == preimage.count('bounded_tree(profile)') == 1
    preimage = preimage.replace("bounded_tree(root/'home')", "bounded_tree(root/'home',vendor_root=root)").replace('bounded_tree(profile)', 'bounded_tree(profile,vendor_root=root)')
    exec(compile(preimage,'<SDK-foreign-preimage-inventory>','exec'),m.__dict__)
    lifecycle = inspect.getsource(m.finish_lifecycle)
    old = "    assert type(native_bytes) is bytes and 0 < len(native_bytes) <= LIMIT, 'actual native bytes required'"
    assert lifecycle.count(old) == 1
    lifecycle = lifecycle.replace(old, "    assert type(native_bytes) is bytes and 0 < len(native_bytes) <= LIMIT, 'bounded reconstructed duplicate payload'\n    cache = pathlib.Path(binding['dataRoot'])/'observations.json'\n    cache_preimage = cache.read_bytes()")
    old = "    check_no_new_effects()\n    installed_readback(root, primary, artifact, 'duplicate', selector)"
    assert lifecycle.count(old) == 1
    lifecycle = lifecycle.replace(old, "    check_no_new_effects()\n    assert cache.read_bytes() == cache_preimage, 'duplicate SDK claim unchanged'\n    (root/'evidence/duplicate-reconstructed.json').write_text(json.dumps({'payload':'RECONSTRUCTED','cacheUnchanged':True,'stdinCapture':'NOT_RUN'})+'\\n')\n    installed_readback(root, primary, artifact, 'duplicate', selector)")
    old = "    cli(root, primary, ['cursor-event', 'stop', '--binding', str(selector)], 'duplicate', native_bytes)"
    assert lifecycle.count(old) == 1
    lifecycle = lifecycle.replace(old, "    assert strict_json(cache_preimage)['boot'] == hashlib.sha256(pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip().encode()).hexdigest(), 'duplicate cache boot changed'\n    assert all(e['until'] > time.clock_gettime(time.CLOCK_BOOTTIME) for e in strict_json(cache_preimage)['entries']), 'duplicate claim expired; enabled replay forbidden'\n"+old)
    lifecycle = lifecycle.replace("        assert records == baseline, 'duplicate/disabled webhook delivery'", "        assert records == baseline, 'duplicate/disabled webhook delivery'\n        assert cache.read_bytes() == cache_preimage, 'duplicate/disabled/update/restart cache unchanged'")
    assert lifecycle.count("bounded_tree(root/'home')") == lifecycle.count("bounded_tree(root/'home/.cursor')") == 1
    lifecycle = lifecycle.replace("bounded_tree(root/'home')", "bounded_tree(root/'home',vendor_root=root)").replace("bounded_tree(root/'home/.cursor')", "bounded_tree(root/'home/.cursor',vendor_root=root)")
    anchor = "    before_ledger = strict_json((pathlib.Path(binding['controlRoot'])/'ownership.json').read_bytes())"
    assert lifecycle.count(anchor) == 1
    lifecycle = lifecycle.replace(anchor, "    globals()['SDK_REMOVAL_METADATA'] = {'before':sdk_mutation_metadata(root)}\n"+anchor)
    anchor = '    classification = classify_removal('
    diagnostic = "    delta = {area:{'added':sorted(set(after_trees[area])-set(before_trees[area])), 'removed':sorted(set(before_trees[area])-set(after_trees[area])), 'changed':sorted(p for p in set(before_trees[area]) & set(after_trees[area]) if before_trees[area][p] != after_trees[area][p])} for area in before_trees}\n    raw = (json.dumps({'before':before_trees,'after':after_trees,'membershipDiff':delta},indent=2)+'\\n').encode()\n    assert len(raw) <= 16*LIMIT, 'full removal diagnostic evidence file bound'\n    (root/'evidence/removal-inventories-before-classification.json').write_bytes(raw)\n"
    assert lifecycle.count(anchor) == 1
    lifecycle = lifecycle.replace(anchor, diagnostic+anchor)
    anchor = '    classification = classify_removal('
    assert lifecycle.count(anchor) == 1
    lifecycle = lifecycle.replace(anchor, "    SDK_REMOVAL_METADATA['after'] = sdk_mutation_metadata(root)\n    (root/'evidence/sdk-retention-metadata.json').write_text(json.dumps({side:{k:v for k,v in row.items() if k != 'stamp'} for side,row in SDK_REMOVAL_METADATA.items()},indent=2)+'\\n')\n"+anchor)
    exec(compile(lifecycle, '<SDK-lifecycle>', 'exec'), m.__dict__)
    main = inspect.getsource(m.main)
    main = main.replace('    observation = ControllerObservation(socket.socket(fileno=args.control_fd),CASE_CLOCK)', '    observation = ControllerObservation(socket.socket(fileno=args.control_fd),CASE_CLOCK)\n    globals()["SDK_CONTROLLER"] = observation')
    old = "        (root/'recipe/native_case.py').write_bytes((HERE/'native_case.py').read_bytes())"
    assert main.count(old) == 1
    main = main.replace(old, old+"\n        (root/'recipe/sdk_observation.py').write_bytes(SDK_ADAPTER_PATH.read_bytes())")
    old = "str(root/'recipe/native_case.py'),'--execute','--inside'"
    assert main.count(old) == 1
    main = main.replace(old, "str(root/'recipe/sdk_observation.py'),'--fixture',str(root/'recipe/native_case.py'),'--execute','--inside'")
    old = "    assert (pathlib.Path(inputs['stage'])/'claude-notifications-linux-amd64').stat().st_size <= TEST_RUNTIME_FILE_LIMIT, 'runtime copy exceeds fixed per-file cap'"
    assert main.count(old) == 1
    main = main.replace(old, "    runtime = pathlib.Path(inputs['stage'])/'claude-notifications-linux-amd64'\n    (root/'evidence/runtime-copy-budget.json').write_text(json.dumps({'sourceSHA':SOURCE_SHA,'primarySHA256':digest(runtime),'primaryBytes':runtime.stat().st_size,'perFileBytes':TEST_RUNTIME_FILE_LIMIT,'aggregateBytes':1024**3},indent=2)+'\\n')\n"+old)
    m.SDK_ADAPTER_PATH = pathlib.Path(__file__).resolve(strict=True)
    exec(compile(main, '<SDK-original-main>', 'exec'), m.__dict__)
    return m


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--fixture', required=True)
    operation = parser.add_mutually_exclusive_group(required=True)
    operation.add_argument('--execute', action='store_true')
    operation.add_argument('--validate', action='store_true')
    parser.add_argument('--inputs'); parser.add_argument('--consent')
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--old-net'); parser.add_argument('--old-pid')
    parser.add_argument('--control-fd', type=int); parser.add_argument('--outer-deadline', type=float)
    args = parser.parse_args()
    module = adapt(load_fixture(args.fixture))
    if args.validate:
        # Compilation/adaptation only: no native, proc reads, DBus or installation.
        assert module.SOURCE_SHA == SOURCE
        assert module.OuterObservation.__name__ == 'SDKOuter'
        print(json.dumps({'method': METHOD, 'fixtureSHA256': FIXTURE_SHA, 'sourceSHA': SOURCE, 'validation': 'syntax-adaptation-only'}))
        return 0
    return module.main(args)


if __name__ == '__main__':
    sys.exit(main())
