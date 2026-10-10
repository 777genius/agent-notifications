"""One actual Linux TEST boundary contract. R1368 original-pair source increment.

Never starts native_case main, Cursor, product code, or a vendor runtime.
The final closed-case qualification is deliberately still INCOMPLETE.
"""
import argparse
import tempfile
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import select
import signal
import socket
import subprocess
import time
import sys

FIXTURES = pathlib.Path(__file__).resolve().parent/"contract-fixtures-r1364"

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('r1353_native', HERE/'native_case.py')
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
FIXTURE = (FIXTURES/'held_child.c').read_text()


def stopped(pid):
    raw = (pathlib.Path('/proc')/str(pid)/'stat').read_text()
    return raw[raw.rindex(')')+2:].split()[0] == 't'


class ContractFailure(RuntimeError):
    def __init__(self, failure, cleanup, case, observation=None):
        super().__init__(type(failure).__name__+':'+str(failure)[:240])
        self.first_failure = failure
        self.cleanup = cleanup
        self.case = case
        self.observation = observation


def remaining(deadline):
    value = deadline-time.monotonic()
    if value <= 0:
        raise TimeoutError('absolute Linux contract deadline')
    return value


def ready_line(pipe, deadline, observation=None, retain=None):
    # Unbuffered fd reads: readiness is inside the same absolute work budget.
    data = bytearray()
    while b'\n' not in data:
        if not select.select([pipe], [], [], remaining(deadline))[0]:
            raise TimeoutError('fixture readiness deadline')
        chunk = os.read(pipe.fileno(), 1)
        if retain is not None: retain(chunk)
        if observation is not None:
            observation['readinessBytes'] += chunk.hex()
            observation['readinessEOF'] = not chunk
        assert chunk, 'fixture readiness EOF'
        data.extend(chunk)
        assert len(data) <= 64, 'fixture readiness byte cap'
    assert data == b'TEST_READY\n', 'fixture readiness bytes'


class Ownership:
    """Resource custody only; admission still uses the original live guards."""
    def __init__(self):
        self.children = []
        self.resources = []
        self.tasks = {}
        self.unresolved = set()
        self.tracer_pid = None

    def spawn(self, *args, role=None, **kwargs):
        child = subprocess.Popen(*args, **kwargs)
        # Register the actual unreaped own child before any qualification.
        record = {'child':child, 'birth':None, 'startup':None, 'role':role}
        self.children.append(record)
        for stream in (child.stdin, child.stdout, child.stderr):
            if stream is not None: self.resources.append(stream)
        return child, record

    def qualify(self, record):
        identity = native.startup_identity(record['child'].pid)
        record.update(birth=identity['birth'], startup=identity)
        return identity

    def cleanup(self, natural, seconds=4):
        end = time.monotonic()+seconds
        evidence = {'complete':False, 'naturalCompletion':natural,
                    'forcedContainment':False, 'children':[], 'tasks':[],
                    'resourcesClosed':False, 'pumps':[], 'errors':[]}
        def attempt(label, action):
            try: return action()
            except BaseException as error:
                evidence['errors'].append({'operation':label,'error':type(error).__name__})
                return None
        tracers = [r['child'] for r in self.children if r.get('role') == 'tracer' or r['child'].pid == self.tracer_pid]
        if not natural and hasattr(self,'observer_peer'):
            attempt('first control refusal',lambda:self.observer_peer.shutdown(socket.SHUT_RDWR))
        # Discover actual traced threads, including constructor/clone custody
        # not yet admitted into the request ledger. Same fresh actor group,
        # namespace and original identity facts are required for a signal.
        if not natural and tracers:
            try:
                original = self.actor_identity
                for process in pathlib.Path('/proc').iterdir():
                    if not process.name.isdecimal(): continue
                    try: tids = list((process/'task').iterdir())
                    except (FileNotFoundError,ProcessLookupError): continue
                    for entry in tids:
                        try:
                            status = dict(line.split(':',1) for line in (entry/'status').read_text().splitlines() if ':' in line)
                            if int(status['TracerPid']) != self.tracer_pid: continue
                            raw = (entry/'stat').read_text(); tail = raw[raw.rindex(')')+2:].split()
                            if tail[0] in ('Z','X','x'):
                                pid,birth = int(entry.name),int(tail[19])
                                assert pid not in self.tasks or self.tasks[pid] == birth, 'terminal tracee birth drift'
                                self.tasks[pid] = birth; continue
                            facts = native.trace_task_identity(int(entry.name))
                        except (FileNotFoundError,ProcessLookupError): continue
                        assert facts['tracer'] == self.tracer_pid, 'tracee tracer drift'
                        assert facts['pgid'] == facts['sid'] == self.children[0]['child'].pid, 'foreign traced group'
                        for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                            assert facts[field] == original[field], 'tracee containment authority'
                        assert facts['pid'] not in self.tasks or self.tasks[facts['pid']] == facts['birth'], 'tracee containment birth drift'
                        assert native.trace_task_identity(facts['pid']) == facts, 'fresh tracee containment bookend'
                        self.tasks[facts['pid']] = facts['birth']
            except BaseException as error:
                evidence['errors'].append({'operation':'fresh whole tracer custody','error':type(error).__name__})
        # An unreaped own Popen and an admitted task can describe the same
        # child. Use own-child custody for its cleanup, rather than attempting
        # fresh *live admission* after killing its group. That admission race
        # was inferred from R1367; its exact exception cause was not proved.
        own = {record['child'].pid:record for record in self.children}
        for pid,birth in reversed(list(self.tasks.items())):
            if pid in own:
                attempt('overlapping own-child birth',lambda pid=pid,birth=birth:
                        require_own_birth(own[pid],birth))
                continue
            def contain_task(pid=pid,birth=birth):
                try: raw = (pathlib.Path('/proc')/str(pid)/'stat').read_text()
                except (FileNotFoundError, ProcessLookupError): return
                tail = raw[raw.rindex(')')+2:].split()
                assert int(tail[19]) == birth, 'cleanup refuses reused task'
                if tail[0] in ('Z','X','x'): return
                facts = native.trace_task_identity(pid)
                assert facts['birth'] == birth, 'cleanup refuses reused task'
                assert pid == self.children[0]['child'].pid or facts['tracer'] == self.tracer_pid, 'cleanup refuses foreign tracer'
                if not natural and tracers:
                    assert facts['pgid'] == facts['sid'] == self.children[0]['child'].pid, 'owned cleanup group drift'
                    for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                        assert facts[field] == self.actor_identity[field], 'owned cleanup task authority'
                    assert native.trace_task_identity(pid) == facts, 'fresh cleanup task bookend'
                if not natural:
                    os.kill(pid,signal.SIGKILL)
                    evidence['forcedContainment'] = True
            attempt('task containment', contain_task)
        # Non-Popen tasks were checked before group termination. The root is
        # still an unreaped child we created. Terminal state does not invalidate
        # this cleanup custody, and does not admit a task/edge/ACK. Before any
        # group signal retain exact birth, parent, group and session checks.
        if not natural and self.children:
            record = self.children[0]; child = record['child']
            if record['startup'] is not None and child.returncode is None:
                def group_contain():
                    raw = (pathlib.Path('/proc')/str(child.pid)/'stat').read_text()
                    tail = raw[raw.rindex(')')+2:].split()
                    assert (int(raw[:raw.index('(')].strip()),int(tail[19]),
                            int(tail[1]),int(tail[2]),int(tail[3])) == tuple(
                        record['startup'][key] for key in ('pid','birth','ppid','pgid','sid')), 'own cleanup tuple drift'
                    assert int(tail[1]) == os.getpid() and int(tail[2]) == int(tail[3]) == child.pid, 'own fixture group custody'
                    os.killpg(child.pid,signal.SIGKILL)
                    evidence['forcedContainment'] = True
                attempt('fixture group containment',group_contain)
        # Own Popen custody survives failure of full identity qualification.
        # Popen custody cannot authorize killing a tracer with unknown/failed
        # tracee containment. C alone performs terminal EXIT/wait removal;
        # signals to ordinary own actor children remain separate custody.
        for record in self.children:
            child = record['child']
            if not natural and child.returncode is None and child not in tracers:
                def kill_child(child=child):
                    child.kill()
                    evidence['forcedContainment'] = True
                attempt('own child containment', kill_child)
        # Reap ptrace first: an actor/PID1 join may depend on those waits.
        for record in sorted(self.children,key=lambda r:r['child'] not in tracers):
            child = record['child']
            joined = attempt('own child join',lambda child=child:child.wait(timeout=remaining(end)))
            row = {'pid':child.pid,'birth':record['birth'],'joined':joined is not None,
                   'exit':joined,'pidAbsent':False,'freshBirth':None}
            def child_absence():
                try: raw = (pathlib.Path('/proc')/str(child.pid)/'stat').read_text()
                except FileNotFoundError: row['pidAbsent'] = True; return
                row['freshBirth'] = int(raw[raw.rindex(')')+2:].split()[19])
                assert record['birth'] is not None and row['freshBirth'] != record['birth'], 'own child PID still present'
            attempt('own child absence', child_absence)
            evidence['children'].append(row)
        for pid,birth in set(self.tasks.items()) | self.unresolved:
            row = {'pid':pid,'birth':birth,'pidAbsent':False,'freshBirth':None}
            def task_absence(pid=pid,birth=birth,row=row):
                path = pathlib.Path('/proc')/str(pid)/'stat'
                try: raw = path.read_text()
                except FileNotFoundError: row['pidAbsent'] = True; return
                fresh = int(raw[raw.rindex(')')+2:].split()[19]); row['freshBirth'] = fresh
                assert fresh != birth, 'owned task survives joins'
            attempt('owned task absence',task_absence)
            evidence['tasks'].append(row)
        evidence['unqualifiedRequestBirths'] = sorted(self.unresolved)
        before = len(evidence['errors'])
        live_tracer = any(child.poll() is None for child in tracers)
        if not live_tracer:
            for resource in reversed(self.resources):
                attempt('resource close',lambda resource=resource:
                        os.close(resource) if isinstance(resource,int) else resource.close())
        else:
            evidence['errors'].append({'operation':'tracer custody preserved','error':'terminal wait unresolved'})
        evidence['resourcesClosed'] = not live_tracer and before == len(evidence['errors'])
        evidence['complete'] = cleanup_complete(evidence)
        return evidence


def require_own_birth(record, birth):
    assert record['birth'] == birth, 'overlapping own-child/task birth drift'


def cleanup_complete(evidence):
    return (not evidence['errors'] and evidence['resourcesClosed'] and
            all(row['joined'] and (row['pidAbsent'] or row['birth'] is not None and
                row['freshBirth'] is not None and row['freshBirth'] != row['birth'])
                for row in evidence['children']) and
            all(row['pidAbsent'] or row['freshBirth'] is not None and
                row['freshBirth'] != row['birth'] for row in evidence['tasks']) and
            all(row['joined'] for row in evidence['pumps']))


# Mutate only the ACK echo of a genuine held request. No invented syscall
# record or changed register/result is supplied to the observer/parser.
ACK_REFUSALS = {'invalid':1,'wrong-tracer':2,'wrong-sequence':3,'wrong-phase':4,
                'wrong-pid':5,'wrong-birth':6,'wrong-child':7,'wrong-child-birth':8,
                'wrong-syscall':9,'wrong-flags':10,'wrong-arg':11}


def mutate_ack(packet, refusal):
    fields = packet.decode('ascii').split()
    index = ACK_REFUSALS[refusal]
    if index == 1:
        fields[index] = '0'*64 if fields[index] != '0'*64 else '1'*64
    elif index == 4: fields[index] = 'record'
    elif index == 9: fields[index] = 'read'
    else: fields[index] = str(int(fields[index])+1)
    return ' '.join(fields).encode('ascii')


def run(binary, fixture, scratch, mode='fork', refusal=None, timeout=12):
    """Inspect real requests and original live guards, never qualify native."""
    deadline = time.monotonic()+timeout
    owner = Ownership()
    trace = bytearray(); checkpoints = set(); requests = []; sequences = set()
    sent = {}; completed = []
    started = refused = natural = injected = False
    result = failure = None
    case = mode+'-'+str(refusal)
    raw_files = {}; packets = []; retention_errors = []
    def retain(kind, data):
        stream, row, cap = raw_files[kind]
        offset = row['bytes']
        kept = data[:max(0,cap-offset)]
        count = stream.write(kept); stream.flush(); row['bytes'] += count
        assert count == len(kept), 'raw retention short write'
        if kind == 'control':
            assert len(packets) < 16384, 'raw control packet inventory cap'
            packets.append({'offset':offset,'bytes':len(kept),'observedBytes':len(data),'direction':control_direction})
        if len(kept) != len(data):
            row['disposition'] = 'PARTIAL_BYTE_CAP'
            raise AssertionError('raw '+kind+' retention byte cap')
    control_direction = None
    observation = {'phase':'actor-spawn','actor':None,'readinessBytes':'','readinessEOF':False,'ready':False}
    try:
        for kind,suffix,cap in (('trace','.trace',8*native.LIMIT),('control','.control',8*native.LIMIT),('actor','.actor.stdout',65536+64)):
            path = scratch/(case+suffix)
            raw_files[kind] = (path.open('xb'),{'path':str(path),'bytes':0,'disposition':'CONSUMED_ONLY'},cap)
        actor_cwd = scratch/(case+'.actor'); actor_cwd.mkdir()
        root, root_record = owner.spawn([str(fixture),mode],stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,start_new_session=True,bufsize=0,cwd=actor_cwd,
            env={'PATH':'/usr/bin:/bin','LANG':'C','TMPDIR':str(scratch/'tmp')})
        observation['actor'] = owner.qualify(root_record)
        observation['phase'] = 'actor-readiness'
        ready_line(root.stdout,deadline,observation,lambda raw:retain('actor',raw))
        observation['ready'] = True
        observation['phase'] = 'observer-resources'
        original = native.proc_identity(root.pid)
        owner.actor_identity = original
        owner.tasks[root.pid] = original['birth']
        peer, inherited = socket.socketpair(socket.AF_UNIX, socket.SOCK_SEQPACKET)
        owner.resources.extend((peer,inherited))
        owner.observer_peer = peer
        token = os.urandom(32).hex()
        fd, writer = os.pipe()
        owner.resources.extend((fd,writer))
        argv = [str(binary),'-d','-f','-xx','--decode-pids=pidns','-s','1048577',
                '-e','trace='+native.TRACE_SYSCALLS,'-o','/proc/self/fd/'+str(writer),
                '-p',str(root.pid)]
        env = {'PATH':'/usr/bin:/bin','LANG':'C','TMPDIR':str(scratch/'tmp')}
        if refusal != 'passive':
            env.update(TEST_R1353_FD=str(inherited.fileno()), TEST_R1353_INCARNATION=token)
        observation['phase'] = 'stderr-open'
        err = (scratch/(mode+'-'+str(refusal)+'.stderr')).open('wb')
        owner.resources.append(err)
        remaining(deadline)
        observation['phase'] = 'tracer-spawn'
        tracer, tracer_record = owner.spawn(argv,role='tracer',env=env,
            pass_fds=(writer,inherited.fileno()),stderr=err,start_new_session=True)
        owner.tracer_pid = tracer.pid
        owner.qualify(tracer_record)
        inherited.close(); os.close(writer); owner.resources.remove(writer)
        while time.monotonic() < deadline:
            ready = select.select([fd,peer],[],[],max(0,deadline-time.monotonic()))[0]
            if fd in ready:
                raw = os.read(fd,65536)
                if raw:
                    retain('trace',raw)
                    trace.extend(raw)
                    assert len(trace) <= 8*native.LIMIT, 'raw real record byte cap'
                    checkpoints.update(int(v) for v in re.findall(rb'^# R1353 (\d+)\n',trace,re.M))
                elif tracer.poll() is not None: break
                if refusal == 'passive' and not started:
                    # Stock attachment is verified by fresh original /proc facts.
                    assert native.trace_task_identity(root.pid)['tracer'] == tracer.pid
                    root.stdin.write(b'G'); root.stdin.flush(); started = True
            if peer in ready and refusal != 'passive':
                packet, ancillary, packet_flags, _ = peer.recvmsg(516)
                control_direction = 'RECEIVED'; retain('control',packet)
                assert not ancillary and not packet_flags and len(packet) < 516, 'control packet truncation/ancillary'
                if not packet:
                    if refusal:
                        assert injected, 'control EOF before the actual tested refusal'
                        refused = True
                    else:
                        assert not sent, 'EOF before actual restart receipts'
                        assert root.wait(timeout=remaining(deadline)) == tracer.wait(timeout=remaining(deadline)) == 0, 'EOF is not natural join proof'
                    break
                if packet.startswith(b'DONE '):
                    request = sent.pop(packet[5:],None)
                    assert request is not None, 'unrequested/duplicate actual restart receipt'
                    completed.append(request['sequence'])
                    if not started:
                        root.stdin.write(b'G'); root.stdin.flush(); started = True
                    continue
                fields = packet.decode('ascii').split()
                assert len(fields) == 17 and fields[:3] == ['R1353',token,str(tracer.pid)]
                assert fields[4] in ('task','clone','entry','record'), 'closed request phase'
                assert all(re.fullmatch(r'0|[1-9][0-9]{0,19}',fields[i]) for i in (3,5,6,7,8,10,11,12,13,14,15,16)), 'request numeric ABI'
                seq, phase, tid, birth, child, cbirth = int(fields[3]),fields[4],*map(int,fields[5:9])
                assert (child > 0 and cbirth > 0) if phase == 'clone' else child == cbirth == 0, 'clone pair ABI'
                assert seq == len(sequences)+1; sequences.add(seq)
                while seq not in checkpoints:
                    assert time.monotonic() < deadline
                    assert select.select([fd],[],[],deadline-time.monotonic())[0]
                    raw = os.read(fd,65536); assert raw
                    retain('trace',raw)
                    trace.extend(raw)
                    assert len(trace) <= 8*native.LIMIT, 'raw real record byte cap'
                    checkpoints.update(int(v) for v in re.findall(rb'^# R1353 (\d+)\n',trace,re.M))
                owner.unresolved.add((tid,birth))
                if child: owner.unresolved.add((child,cbirth))
                facts = native.trace_task_identity(tid)
                assert facts['birth'] == birth and facts['tracer'] == tracer.pid and stopped(tid)
                for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                    assert facts[field] == original[field], field
                owner.tasks[tid] = birth
                if phase == 'clone':
                    cf = native.trace_task_identity(child)
                    assert cf['birth'] == cbirth and cf['tracer'] == tracer.pid and stopped(child)
                    assert (cf['tgid'] == facts['tgid'] or cf['ppid'] == facts['tgid']) and cf['nspid'][-1] == child
                    for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                        assert cf[field] == facts[field], field
                    assert native.live_stdin_endpoint(child) == native.live_stdin_endpoint(tid)
                    owner.tasks[child] = cbirth
                    assert native.trace_task_identity(child) == cf
                assert native.trace_task_identity(tid) == facts
                owner.unresolved.discard((tid,birth))
                owner.unresolved.discard((child,cbirth))
                requests.append({'phase':phase,'sequence':seq,'tid':tid,'birth':birth,
                                 'child':child,'childBirth':cbirth,'checkpointConsumed':True})
                if phase == 'clone' and refusal:
                    if refusal in ACK_REFUSALS:
                        bad = mutate_ack(packet,refusal)
                        assert bad != packet
                        ack = b'ACK '+bad; count = peer.send(ack)
                        control_direction = 'SENT_ACK'; retain('control',ack[:count])
                        assert count == len(ack)
                    elif refusal == 'eof': peer.shutdown(socket.SHUT_WR)
                    elif refusal == 'deadline': pass
                    else: raise AssertionError('unknown refusal case')
                    injected = True
                    assert select.select([peer],[],[],min(6,remaining(deadline)))[0], 'bounded refusal receipt'
                    packet = peer.recv(512)
                    control_direction = 'RECEIVED_REFUSAL'; retain('control',packet)
                    assert packet == b'', 'refusal must close control channel'
                    assert stopped(tid) and stopped(child), 'no resume on refused custody'
                    assert not drain_output(root.stdout,retain=lambda raw:retain('actor',raw)), 'child executed before refusal containment'
                    refused = True
                    break
                sent[packet] = requests[-1]
                ack = b'ACK '+packet; count = peer.send(ack)
                control_direction = 'SENT_ACK'; retain('control',ack[:count])
                assert count == len(ack)
            if root.poll() is not None and tracer.poll() is not None: break
        else: raise AssertionError('actual kernel contract deadline')
        if refusal == 'passive':
            assert root.wait(timeout=remaining(deadline)) == 0 and tracer.wait(timeout=remaining(deadline)) == 0
            matches = re.findall(rb'clone\(.*?\)\s+=\s+(\d+)',trace)
            assert matches, 'OLD must reach an actual successful short-lived clone'
            child = int(matches[0])
            try: native.trace_task_identity(child)
            except FileNotFoundError: pass
            else: raise AssertionError('OLD did not lose live custody')
            natural = True
            result = {'case':'passive-old','result':'RED','actualChild':child,'reason':'fresh original proc guard refuses absent child'}
        if refusal != 'passive':
            assert refused == bool(refusal) and injected == bool(refusal), 'refusal needs the actual tested held stop'
        if not refusal:
            assert root.wait(timeout=remaining(deadline)) == 0 and tracer.wait(timeout=remaining(deadline)) == 0
            assert not sent and set(completed) == sequences, 'every accepted stop needs actual restart receipt'
            assert sum(r['phase'] == 'clone' for r in requests) == 1
            assert drain_output(root.stdout,retain=lambda raw:retain('actor',raw)) == b'TEST_CHILD_RAN\n', 'actual child effect after accepted ACK'
            if mode == 'thread':
                assert any(b'CLONE_THREAD' in line for line in trace.splitlines()), 'actual CLONE_THREAD record required'
            if mode in ('vfork','exec'):
                assert re.search(rb'vfork\(.*?\)\s+=\s+[1-9][0-9]*|<\.\.\. vfork resumed>.*?=\s+[1-9][0-9]*',trace), 'actual vfork parent return required'
            natural = True
        if result is None:
            result = {'case':case,'result':'GREEN_BOUNDARY_ONLY','requests':requests,
                      'kernelRestartReceipts':completed}
    except BaseException as error:
        failure = error
    finally:
        try: cleanup = owner.cleanup(natural)
        except BaseException as error:
            cleanup = {'complete':False,'disposition':'UNKNOWN','error':type(error).__name__+':'+str(error)[:240]}
            if failure is None: failure = error
        for stream,row,cap in raw_files.values():
            try:
                stream.close()
                raw = pathlib.Path(row['path']).read_bytes()
                row['bytes'] = len(raw); row['sha256'] = hashlib.sha256(raw).hexdigest()
            except BaseException as error: retention_errors.append(type(error).__name__+':'+str(error)[:240])
        observation['rawObservations'] = {kind:row for kind,(_,row,_) in raw_files.items()}
        observation['controlPackets'] = packets
        observation['retentionErrors'] = retention_errors
        if failure is None and retention_errors: failure = RuntimeError('raw retention incomplete')
        if failure is None and not cleanup['complete']: failure = AssertionError('cleanup incomplete or unproved')
        try:
            (scratch/(case+'.raw-receipt.json')).write_text(json.dumps({'case':case,'rawObservations':observation['rawObservations'],
                'controlPackets':packets,'retentionErrors':retention_errors,'cleanup':cleanup,
                'firstFailure':None if failure is None else type(failure).__name__+':'+str(failure)[:240]})+'\n')
        except BaseException as error:
            if failure is None: failure = error
    if failure is not None:
        raise ContractFailure(failure,cleanup,case,observation) from failure
    if not cleanup['complete']:
        raise ContractFailure(AssertionError('cleanup incomplete or unproved'),cleanup,case,observation)
    result['rawObservations'] = observation['rawObservations']
    result['controlPackets'] = packets
    result['cleanup'] = cleanup
    return result


def drain_output(pipe, cap=65536, retain=None):
    output = bytearray()
    while select.select([pipe],[],[],0)[0]:
        part = os.read(pipe.fileno(),8192)
        if retain is not None: retain(part)
        if not part: break
        output.extend(part)
        assert len(output) <= cap, 'fixture output byte cap'
    return bytes(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--derived',type=pathlib.Path,required=True)
    parser.add_argument('--derived-sha256',required=True)
    args = parser.parse_args()
    binary = args.derived.resolve(strict=True)
    assert binary.read_bytes()[:4] == b'\x7fELF' and hashlib.sha256(binary.read_bytes()).hexdigest() == args.derived_sha256, 'exact derived build pin'
    scratch = pathlib.Path(tempfile.mkdtemp(prefix='TEST-linux-r1364-',dir=HERE/'scratch'))
    (scratch/'tmp').mkdir()
    fixture = scratch/'TEST-held-child'
    source = scratch/'TEST-held-child.c'; source.write_text(FIXTURE)
    cases = []; build = None
    compiler = pathlib.Path('/usr/bin/gcc').resolve(strict=True)
    command = [str(compiler),'-O2','-pthread','-o',str(fixture),str(source)]
    binding = {'source':str(source),'sourceSHA256':hashlib.sha256(source.read_bytes()).hexdigest(),
               'compiler':str(compiler),'compilerSHA256':hashlib.sha256(compiler.read_bytes()).hexdigest(),
               'sourceBytes':source.stat().st_size,'command':command,'disposition':'NOT_STARTED',
               'joined':False,'descendantJoins':'UNKNOWN'}
    try:
        try:
            (scratch/'held-child-build-binding.json').write_text(json.dumps(binding,indent=2)+'\n')
            build = subprocess.Popen(command,env={'PATH':'/usr/bin:/bin','TMPDIR':str(scratch/'tmp')},start_new_session=True)
            binding.update(disposition='RUNNING',pid=build.pid)
            (scratch/'held-child-build-binding.json').write_text(json.dumps(binding,indent=2)+'\n')
            assert build.wait(timeout=10) == 0, 'lower fixture compiler failed'
            elf = fixture.read_bytes(); assert elf[:4] == b'\x7fELF', 'lower held-child ELF'
            binding.update(disposition='RETURNED',joined=True,elfSHA256=hashlib.sha256(elf).hexdigest(),elfBytes=len(elf))
            assert hashlib.sha256(compiler.read_bytes()).hexdigest() == binding['compilerSHA256'], 'lower compiler drift'
        except BaseException as error:
            binding['firstFailure'] = type(error).__name__+':'+str(error)[:240]
            binding['disposition'] = 'PARTIAL_FAILURE'
            if build is not None and build.returncode is None:
                try:
                    raw = (pathlib.Path('/proc')/str(build.pid)/'stat').read_text(); tail = raw[raw.rindex(')')+2:].split()
                    assert int(tail[1]) == os.getpid() and int(tail[2]) == int(tail[3]) == build.pid, 'owned compiler group'
                    os.killpg(build.pid,signal.SIGKILL); build.wait(timeout=2); binding['joined'] = True
                except BaseException as cleanup_error: binding['cleanupError'] = type(cleanup_error).__name__
            raise
        finally:
            try: (scratch/'held-child-build-binding.json').write_text(json.dumps(binding,indent=2)+'\n')
            except BaseException as error:
                if 'firstFailure' not in binding: raise
                binding['retentionError'] = type(error).__name__
        cases.append(run('/usr/bin/strace',fixture,scratch,refusal='passive'))
        for mode in ('fork','vfork','exec','thread'):
            cases.append(run(binary,fixture,scratch,mode))
        for refusal in (*ACK_REFUSALS,'eof','deadline'):
            cases.append(run(binary,fixture,scratch,refusal=refusal))
    except Exception as error:
        diagnostic_path = scratch/'fork-passive.stderr'
        diagnostic = diagnostic_path.read_text()[:65536] if diagnostic_path.exists() else ''
        receipt = {'status':'NOT_RUN_RUNTIME_UNSUPPORTED' if 'Operation not permitted' in diagnostic else 'FAIL',
                   'wholeContract':'INCOMPLETE','error':type(error).__name__+':'+str(error),
                   'diagnosticClassification':'ptrace unavailable' if 'Operation not permitted' in diagnostic else 'not classified',
                   'oldRedProved':any(case.get('case') == 'passive-old' for case in cases),
                   'newGreenProved':False,'completedCases':cases,
                   'ownedProcessCleanup':getattr(error,'cleanup',{'complete':False,'reason':'no run cleanup evidence'}),
                   'nativeE':'NOT_RUN','scratch':str(scratch),'derivedSHA256':args.derived_sha256,
                   'heldChildBuild':binding,'failedObservation':getattr(error,'observation',None)}
        try: (scratch/'linux-receipt-r1364.json').write_text(json.dumps(receipt,indent=2)+'\n')
        except BaseException as retention_error: error.add_note('lower receipt retention:'+type(retention_error).__name__)
        raise
    receipt = {'status':'PARTIAL_BOUNDARY_PASS','wholeContract':'INCOMPLETE',
               'derivedSHA256':hashlib.sha256(binary.read_bytes()).hexdigest(),'scratch':str(scratch),
               'heldChildBuild':binding,'cases':cases,'ownedProcessCleanup':[case['cleanup'] for case in cases],
               'nativeE':'NOT_RUN','missing':['both clone notification orders witnessed','original parser CLONE_THREAD admission',
               'native parser full UID1000 admission','exec TID remap','D-Bus lifetime closed cases',
               'reader failure','qualification','independent exact review']}
    (scratch/'linux-receipt-r1364.json').write_text(json.dumps(receipt,indent=2)+'\n')
    print(json.dumps(receipt))


if __name__ == '__main__':
    if '--original-pair' in sys.argv:
        # This executable path constructs both ORIGINAL classes itself. It does
        # not require a caller to supply an unspecified previously live pair.
        sys.argv.remove('--original-pair')
        pair_spec = importlib.util.spec_from_file_location('TEST_integrated_pair',FIXTURES/'integrated_pair.py')
        pair = importlib.util.module_from_spec(pair_spec); pair_spec.loader.exec_module(pair)
        sys.exit(pair.main())
    main()
