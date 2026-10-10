#!/usr/bin/env python3
"""Executable ORIGINAL observer/session TEST pair; external reviewed root only.

This module never calls native main or its product/native launch flows. Profiling
records actual calls/bytes and schedules real TEST process signals. It replaces
no original method, parser, proc identity, syscall record, receipt or assertion.
Missing source is explicitly inventoried; individual outcomes cannot qualify
the entire matrix or native eligibility.
"""
import argparse
import ctypes
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import resource
import re
import select
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time

HERE = Path(__file__).resolve().parent
IMPLEMENTATION = HERE.parent
ORIGINAL_ROOT = IMPLEMENTATION
PINS = {'native_case.py':'b99478b2b5b23cef65cf58f31d19ff382cfe4ddb4fcb185caa8856255310e113',
        'src/strace.c':'656ccbcd614055b777a11d69bc55e35a0256426f421ea4f7c58026e95fa029e5',
        'src/defs.h':'9914057470fbec3428b051c4a307fe48e7c19207192a936b88c5b14b8a624eae',
        'src/syscall.c':'390ff1bc410226b4c843ee046ba8e576879e0edc2b3b3087c79afb4ceb506803'}
OLD_PINS = dict(PINS,**{'native_case.py':'07047821776a91e3095eb5d4baed8467eb019ad31a8649fd58d67ca5ce46b6cc',
                      'src/strace.c':'7953842ccb83b6419172217943b0f1c40ce4b10f3514a093ea9feb333f1da452'})
spec = importlib.util.spec_from_file_location('TEST_original', IMPLEMENTATION/'native_case.py')
native = importlib.util.module_from_spec(spec); spec.loader.exec_module(native)

# A name in this table requires executable observations below, not just a label.
CASES = {
    'fork':('fork','stdin',None), 'thread':('thread','stdin',None),
    'vfork-exit':('vfork-exit','stdin',None), 'vfork-exec':('vfork-exec','stdin',None),
    'execve':('execve','stdin',None), 'execveat':('execveat','stdin',None),
    'exec-tid-remap':('remap','stdin',None),
    'clone-orders':('clone-orders','stdin',None),
    'bus-close':('execve','bus-close',None), 'delayed-controls':('execve','bus-close',None),
    'alias-close':('execve','alias-close',None), 'dup2':('execve','dup2',None),
    'dup3':('execve','dup3',None), 'shutdown':('execve','shutdown',None),
    'close-range-unshare':('execve','close-range-unshare',None),
    'exit-group':('execve','exit-group',None),
    'shared-close':('execve','shared-close',None),
    'shared-alias-close':('execve','shared-alias-close',None),
    'shared-dup2':('execve','shared-dup2',None),
    'shared-dup3':('execve','shared-dup3',None),
    'shared-shutdown':('execve','shared-shutdown',None),
    'shared-close-range-unshare':('execve','shared-close-range-unshare',None),
    'thread-exit':('execve','thread-exit',None),
    'last-table-exit':('execve','last-table-exit','thread exit lacks surviving owner/table'),
    'owner-exit':('execve','owner-exit',None),
    'connect-refused':('execve','connect-refused','held credential/monitor deadline'),
    'hello-refused':('execve','hello-refused','held credential/monitor deadline'),
    'auth-absent':('execve','auth-absent','held credential/monitor deadline'),
    'auth-refused':('execve','auth-refused','held credential/monitor deadline'),
    'hello-absent':('execve','hello-absent','held credential/monitor deadline'),
    'double-name':('execve','double-name','multiple/ambiguous selected bus connections'),
    'wrong-home':('execve','stdin','actual helper HOME'),
    'wrong-fd0':('bad-fd0','stdin','stdin endpoint inode/link mismatch'),
    'wrong-exe':('bad-exe','stdin','source-qualified USER helper executable/cwd'),
    'wrong-birth':('execve','stdin','held task birth'),
    'wrong-name':('execve','wrong-name','actual bus name authority'),
    'wrong-socket':('execve','bus-close',None),
    'wrong-table':('execve','bus-close','held task/table incarnation changed'),
    'output-reader-failure':('execve','bus-close','I/O operation on closed file'),
    'debug-reader-failure':('execve','bus-close','I/O operation on closed file'),
    'output-reader-deadline':('execve','bus-close','complete real trace checkpoint deadline'),
    'fatal-die':('execve','bus-close',None),
    'diagnostic-eof':('execve','bus-close',None),
    'pid-control-eof':('execve','bus-close','standard session control failed'),
    'uid-control-eof':('execve','bus-close','standard session control failed'),
    'monitor-eof':('execve','bus-close','monitor continuity ended early'),
    'diagnostic-race':('execve','bus-close',None),
    'final-ack-race':('execve','bus-close',None),
    'final-incarnation-drift':('execve','final-incarnation-drift',None),
    'control-eof':('execve','bus-close','held channel EOF/truncation/ancillary'),
    'credential-deadline':('execve','bus-close','held credential/monitor deadline'),
    'session-child-failure':('execve','stdin',None),
    'session-fd-failure':('execve','stdin',None),
    'tracer-child-failure':('execve','stdin',None),
    'session-netfd-failure':('execve','stdin',None),
    'session-identity-failure':('execve','stdin',None),
    'tracer-identity-failure':('execve','stdin',None),
}
CASES.update({'session-thread-start-'+str(i):('execve','stdin',None) for i in range(1,9)})
CASES.update({'tracer-thread-start-'+str(i):('execve','stdin',None) for i in range(1,4)})
UNIMPLEMENTED = [
    'genuine ordinary unheld ptrace_restart failure with another original stop in custody; allocator die is a distinct implemented case',
    'original live trace-output EOF trigger with held-stop retention; real read errors/checkpoint deadlines and service/monitor EOF are distinct implemented cases',
]


def digest(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def stat_birth(pid):
    text = (Path('/proc')/str(pid)/'stat').read_text()
    return int(text[text.rindex(')')+2:].split()[19])


def descriptors():
    live = set()
    for value in os.listdir('/proc/self/fd'):
        fd = int(value)
        try: os.fstat(fd)
        except OSError as error:
            if error.errno == 9: continue
            raise
        live.add(fd)
    return live


def wait_for(predicate, end, label, failure=None):
    while True:
        if predicate(): return
        if failure and failure(): raise RuntimeError(failure())
        left = end-time.monotonic()
        assert left > 0, label+' absolute/no-progress deadline'
        time.sleep(min(.005,left))


def atomic_json(path, value):
    temporary = path.with_name(path.name+'.publishing')
    with temporary.open('x') as f:
        json.dump(value,f,indent=2); f.write('\n'); f.flush(); os.fsync(f.fileno())
    os.replace(temporary,path)


class Custody:
    """Popen/FD/worker custody survives all fallible qualification paths."""
    def __init__(self):
        self.children = []; self.streams = []; self.fds = []; self.workers = []
        self.first = None; self.failure_lock = threading.Lock()
    def fail(self, error):
        with self.failure_lock:
            if self.first is None: self.first = error
    def popen(self, *args, **kwargs):
        child = subprocess.Popen(*args,**kwargs)
        record = {'child':child,'birth':None}; self.children.append(record)
        self.streams.extend(s for s in (child.stdin,child.stdout,child.stderr) if s)
        return child,record
    def stream(self, value): self.streams.append(value); return value
    def fd(self, value): self.fds.append(value); return value
    def worker(self, value): self.workers.append(value); return value


class Observations:
    """Passive Python-call witness, including actual raw observer bytes.

    Signal scheduling applies only to newly qualified own TEST control children.
    The original control callback retains its own bounded waits and RPCs. No
    callback waits for its own monitored roundtrip; the root thread resumes each
    independently paused process. A profiler exception makes the case fail.
    """
    def __init__(self, case, owner):
        self.case,self.owner = case,owner
        self.lock = threading.Lock()
        self.session = self.trace = None
        self.namespace_child = self.controller = None
        self.trace_bytes = bytearray(); self.debug_bytes = bytearray()
        self.requests = []; self.contexts = []; self.completed = []; self.published = []
        self.sends = []; self.entries = []
        self.paused_controls = []; self.resumed_controls = []; self.active = False
        self.thread_starts = 0
        self.thread_fault = None
        self.identity_fault = None
        self.socket_moved = None
        self.connect_failure = None
        self.birth_refusal = None
        self.monitor_bytes = bytearray(); self.messages_seen = []
        self.blocked_hold = None
        self.reader_armed = False; self.reader_fault = None
        self.original_children = {'session':[],'trace':[]}
        self.killed_controls = []; self.control_eofs = []; self.diagnostic_eofs = []
        self.credential_gate = None
        self.exec_record_gate = None
        self.held_channel_eofs = []
        self.code = {
            native.OwnedNotificationsSession.__init__.__code__:'session-init',
            native.PassiveStrace.__init__.__code__:'trace-init',
            native.PassiveStrace.consume_trace.__code__:'trace',
            native.PassiveStrace.consume_debug.__code__:'debug',
            native.PassiveStrace.ack_held.__code__:'request',
            native.OwnedNotificationsSession.live.__code__:'live',
            native.startup_identity.__code__:'startup',
            native.StdinAliasTrace.held_entry.__code__:'entry',
            threading.Thread.start.__code__:'thread-start',
            native.DBusBinaryStream.feed.__code__:'monitor-bytes',
            native.OwnedNotificationsSession.message.__code__:'message',
            subprocess.Popen.__init__.__code__:'original-popen',
            native.OwnedNotificationsSession.command.__code__:'command',
            native.OwnedNotificationsSession.held_credentials.__code__:'credentials-ready',
            native.PassiveStrace.dispatch_held.__code__:'dispatcher-return',
        }
        for cls,method,tag in ((native.PassiveStrace,'complete_held','complete'),
                               (native.OwnedNotificationsSession,'publish_failure','failure')):
            if hasattr(cls,method): self.code[getattr(cls,method).__code__] = tag
    def profile(self, frame, event, result):
        tag = self.code.get(frame.f_code)
        if tag is None: return
        try:
            local = frame.f_locals
            exec_wait = None; exec_notify = False
            with self.lock:
                if event == 'call':
                    if tag == 'session-init': self.session = local['self']
                    elif tag == 'trace-init': self.trace = local['self']
                    elif tag in ('trace','debug'):
                        target = self.trace_bytes if tag == 'trace' else self.debug_bytes
                        target.extend(local['raw']); assert len(target) <= 256*native.LIMIT
                    elif tag == 'request':
                        self.requests.append(local['packet'])
                    elif tag == 'complete':
                        self.contexts.append(local['context'])
                        if self.case in ('wrong-table','output-reader-deadline','fatal-die') and local['context'][-1] is not None and self.blocked_hold is None:
                            self.blocked_hold = {'context':local['context'],'release':threading.Event()}
                    elif tag == 'failure':
                        self.published.append({'at':time.monotonic(),'error':type(local['error']).__name__+':'+str(local['error'])[:240]})
                    elif tag == 'thread-start':
                        worker = local['self']
                        if self.trace is not None and worker in getattr(self.trace,'threads',[]) and getattr(worker._target,'__name__',None) == 'pump':
                            self.code[worker._target.__code__] = 'trace-pump'
                        if self.session is not None and worker in getattr(self.session,'pumps',[]) and getattr(worker._target,'__name__',None) == 'drain':
                            self.code[worker._target.__code__] = 'diagnostic-drain'
                        self.thread_start_fault(worker)
                    elif tag == 'entry':
                        self.entries.append({'task':local['task'],'name':local['name'],
                                             'arguments':local['arguments'],'at':time.monotonic()})
                    elif tag == 'monitor-bytes':
                        self.monitor_bytes.extend(local['chunk']); assert len(self.monitor_bytes) <= 16*native.LIMIT
                    elif tag == 'message': self.messages_seen.append(local['message'])
                elif event == 'c_call' and tag == 'complete' and getattr(result,'__name__',None) == 'send':
                    trace = local['self']
                    if getattr(result,'__self__',None) is trace.observer_peer:
                        self.sends.append({'packet':local['packet'],'at':time.monotonic(),
                                           'errorAtSend':trace.error or trace.session.error})
                elif event == 'return':
                    if tag == 'dispatcher-return' and local.get('packet') == b'' and not local.get('ancillary') and not local.get('flags'):
                        self.held_channel_eofs.append({'at':time.monotonic(),'actualOriginalRecvmsgEOF':True,
                            'firstError':local['self'].error,'origin':'PassiveStrace.dispatch_held'})
                    elif tag == 'credentials-ready' and self.case in ('final-ack-race','final-incarnation-drift') and result is not None:
                        assert self.credential_gate is None
                        self.credential_gate = {'conn':local['conn'],'name':result,'release':threading.Event(),
                            'controls':local['self'].credential_receipts[result],'at':time.monotonic()}
                        assert all(local['self'].roundtrip_present(control) for control in self.credential_gate['controls'])
                    elif tag == 'diagnostic-drain' and local.get('chunk') == b'':
                        self.diagnostic_eofs.append({'path':str(local['path']),'at':time.monotonic(),
                                                     'originalEOF':True,'closed':local['pipe'].closed})
                    elif tag == 'command' and local.get('child') is not None and local.get('output') is not None:
                        for row in self.killed_controls:
                            if row['child'] is local['child']:
                                self.control_eofs.append({'pid':row['identity']['pid'],'birth':row['identity']['birth'],
                                    'method':row['method'],'stdout':local['output'].decode('utf-8','strict'),
                                    'stderrBytes':len(local['error']),'exit':local['child'].returncode,
                                    'joined':local['child'].returncode is not None})
                    elif tag == 'original-popen':
                        parent = frame.f_back
                        # Capture ORIGINAL allocated own Popen custody at its
                        # actual return, before startup/live qualification. This
                        # also observes the OLD constructor without rewriting it.
                        if parent is not None and Path(parent.f_code.co_filename).resolve() == ORIGINAL_ROOT/'native_case.py':
                            child = local['self']
                            if hasattr(child,'pid') and child.pid > 0:
                                obj = parent.f_locals.get('self')
                                stage = 'trace' if obj is self.trace else 'session' if obj is self.session else None
                                if stage is not None:
                                    self.original_children[stage].append(child)
                                    if not any(row['child'] is child for row in self.owner.children):
                                        self.owner.children.append({'child':child,'birth':None})
                                    self.owner.streams.extend(s for s in (child.stdin,child.stdout,child.stderr) if s)
                    elif tag == 'thread-start' and self.thread_fault is not None and self.thread_fault['thread'] is local['self']:
                        self.restore_thread_limit()
                        self.thread_fault['started'] = local['self'].ident is not None
                    elif tag == 'startup': self.startup_fault(local,result)
                    elif tag == 'entry' and self.case == 'connect-refused' and local['name'] == 'connect' and local['task'].get('capture'):
                        session = local['session']
                        assert self.socket_moved is None and session.error is None
                        original = session.directory/'bus'
                        moved = session.directory/'TEST-connect-unavailable'
                        assert not moved.exists() and original.lstat().st_ino == session.socket_identity['inode']
                        original.rename(moved)
                        self.socket_moved = (original,moved)
                    elif tag == 'trace' and self.socket_moved is not None:
                        captures = list(local['self'].parser.selected)
                        conns = [conn for cap in captures for conn in cap.get('busConnections',[]) if conn['outcome'] is not None]
                        if conns:
                            assert len(conns) == 1 and not conns[0]['connected']
                            assert 'ENOENT' in conns[0]['outcome'], 'actual connect refusal missing'
                            self.connect_failure = conns[0]['outcome']
                            original,moved = self.socket_moved
                            moved.rename(original); self.socket_moved = None
                    elif tag == 'complete':
                        context = local['context']
                        trace = local['self']
                        self.completed.append({'packet':context[0], 'at':time.monotonic(),
                            'acked':context[0] in trace.pending_acks or context[1] <= trace.observer_sequence and
                                (context[3],context[4]) in trace.acked_keys})
                    elif tag == 'live' and self.active and self.case in ('delayed-controls','pid-control-eof','uid-control-eof') and result is not None:
                        argv = result['argv']
                        methods = ('org.freedesktop.DBus.GetConnectionUnixProcessID','org.freedesktop.DBus.GetConnectionUnixUser')
                        if any(method in argv for method in methods):
                            assert local['name'] == 'dbus-send' and local['child'].pid == result['pid']
                            assert native.proc_identity(result['pid'],result) == result
                            method = next(method for method in methods if method in argv)
                            if self.case == 'delayed-controls':
                                os.kill(result['pid'],signal.SIGSTOP)
                                self.paused_controls.append({'child':local['child'],'identity':result,'at':time.monotonic(),'method':method})
                            elif (self.case == 'pid-control-eof' and method == methods[0]) or (self.case == 'uid-control-eof' and method == methods[1]):
                                self.killed_controls.append({'child':local['child'],'identity':result,'method':method})
                                local['child'].kill()
                if event == 'call' and self.case == 'wrong-home' and self.active and tag in ('trace','request'):
                    if tag == 'trace' and self.exec_record_gate is None:
                        for returned in re.finditer(b'^([1-9][0-9]*) <\\.\\.\\. (execve|execveat) resumed>[^\\n]*\\s=\\s0\\n',self.trace_bytes,re.M):
                            tid = int(returned[1]); syscall = returned[2].decode('ascii')
                            prefix = self.trace.parser.pending.get(tid,'')
                            if not prefix.startswith(syscall+'('): continue
                            argtext = prefix.partition('[')[2].partition(']')[0]
                            argv = [native.decoded_hex_string(value).decode('utf-8','strict')
                                    for value in re.findall('"(?:\\\\x[0-9a-fA-F]{2})*"',argtext)]
                            if argv != self.trace.parser.helper_argv: continue
                            checkpoints = re.findall(b'^# R1353 ([1-9][0-9]{0,6})\\n',self.trace_bytes[:returned.start()],re.M)
                            assert checkpoints, 'wrong HOME actual exec checkpoint absent'
                            birth = self.trace.parser.tasks[tid]['key'][1]
                            expected = ['R1353',self.trace.incarnation,str(self.trace.tracer['pid']),
                                        str(int(checkpoints[-1])+1),'record',str(tid),str(birth),'0','0',syscall]
                            self.exec_record_gate = exec_wait = {'expected':expected,'release':threading.Event()}
                            break
                    if self.exec_record_gate is not None:
                        gate = self.exec_record_gate
                        matching = [packet for packet in self.requests
                                    if packet.decode('ascii','strict').split()[:10] == gate['expected']
                                    and len(packet.decode('ascii','strict').split()) == 17]
                        assert len(matching) <= 1, 'duplicate wrong HOME original exec record'
                        if matching:
                            gate['packet'] = matching[0]; gate['release'].set(); exec_notify = True
            if exec_notify:
                with self.trace.parser.changed: self.trace.parser.changed.notify_all()
            if exec_wait is not None:
                # The original pump holds parser.changed here. Its Condition
                # releases every RLock level so pending DONE receipts can pass.
                # No witness lock, ACK, or parser/checkpoint completion is awaited.
                with self.trace.parser.changed:
                    assert self.trace.parser.changed.wait_for(lambda:self.trace.error or self.session.error or
                        exec_wait['release'].is_set(),self.trace.clock.wait(1)), 'wrong HOME original exec record deadline'
                    assert self.trace.error is None and self.session.error is None, self.trace.error or self.session.error
            if event == 'call' and tag == 'request' and self.case == 'wrong-birth' and self.active and self.birth_refusal is None:
                self.original_birth_refusal(local['self'],local['packet'])
            if event == 'c_return' and tag == 'trace-pump' and self.reader_armed and self.reader_fault is None and getattr(result,'__name__',None) == 'read':
                wanted = self.trace.child.stderr if self.case == 'debug-reader-failure' else self.trace.trace_pipe
                if local['stream'] is wanted:
                    self.reader_fault = {'at':time.monotonic(),'kind':self.case,'actualOriginalReadReturned':True}
                    if self.case == 'output-reader-deadline':
                        release = threading.Event(); self.reader_fault['release'] = release
                        # c_return is before the original pump acquires its
                        # parser lock. Genuine bytes remain in that read; no
                        # checkpoint/text is invented or fed by the fixture.
                        release.wait(self.trace.clock.wait(2))
                    else:
                        wanted.close()
                        self.reader_fault['actualOwnStreamClosed'] = wanted.closed
            if event == 'call' and tag == 'complete' and self.blocked_hold is not None and local['context'] is self.blocked_hold['context']:
                # Pause before ORIGINAL complete_held's body, outside every
                # original validation/RPC/records lock and our observation lock.
                # The root tests a misbound context at that same actual stop,
                # then releases this ordinary original worker after refusal.
                assert self.blocked_hold['release'].wait(local['self'].clock.wait(2)), 'negative table scheduling deadline'
            if event == 'return' and tag == 'credentials-ready' and self.case in ('final-ack-race','final-incarnation-drift') and result is not None:
                # Fresh original peer/socket/name and both independently
                # consumed controls have completed. Pause before final ACK
                # validation, with no original lock held, to let an actual
                # diagnostic-reader failure win publication deterministically.
                assert self.credential_gate['release'].wait(local['self'].clock.wait(2)), 'ready final ACK scheduling deadline'
        except BaseException as error:
            self.owner.fail(error)
            # Profiling cannot turn an observation failure into authority.
            if self.session is not None:
                if hasattr(self.session,'publish_failure'): self.session.publish_failure(error)

    def release_held_worker(self):
        if self.blocked_hold is not None: self.blocked_hold['release'].set()
        if self.reader_fault is not None and 'release' in self.reader_fault: self.reader_fault['release'].set()
        if self.credential_gate is not None: self.credential_gate['release'].set()

    def thread_start_fault(self, worker):
        stage = 'tracer' if self.case.startswith('tracer-thread-start-') else 'session' if self.case.startswith('session-thread-start-') else None
        if stage is None: return
        obj = self.trace if stage == 'tracer' else self.session
        if obj is None: return
        allocated = getattr(obj,'threads',[]) if stage == 'tracer' else getattr(obj,'pumps',[])+[getattr(obj,'monitor_pump',None)]
        if worker not in allocated: return
        self.thread_starts += 1
        if self.thread_starts != int(self.case.rsplit('-',1)[1]): return
        assert self.thread_fault is None and worker.ident is None
        limits = resource.getrlimit(resource.RLIMIT_AS)
        size = int(Path('/proc/self/statm').read_text().split()[0])*os.sysconf('SC_PAGE_SIZE')
        ceiling = size+4096
        if limits[0] != resource.RLIM_INFINITY: ceiling = min(ceiling,limits[0])
        self.thread_fault = {'thread':worker,'limits':limits,'ceiling':ceiling,
                             'ordinal':self.thread_starts,'restored':False,'started':None}
        # This isolated TEST process encounters real pthread allocation failure.
        # No Thread method or returned value is replaced. Cached stacks can
        # defeat the intended fault; such a run must refuse qualification.
        resource.setrlimit(resource.RLIMIT_AS,(ceiling,limits[1]))

    def restore_thread_limit(self):
        if self.thread_fault is not None and not self.thread_fault['restored']:
            resource.setrlimit(resource.RLIMIT_AS,self.thread_fault['limits'])
            self.thread_fault['restored'] = True

    def startup_fault(self, local, result):
        if self.identity_fault is not None or result is None: return
        obj = self.trace if self.case == 'tracer-identity-failure' else self.session if self.case == 'session-identity-failure' else None
        if obj is None: return
        children = self.original_children['trace' if obj is self.trace else 'session']
        matches = [child for child in children if child.pid == local['pid'] and child.returncode is None]
        if not matches: return
        assert len(matches) == 1 and native.startup_identity(matches[0].pid,result) == result
        containment = None
        if obj is self.trace:
            # Even the real tracer-identity fault may otherwise detach a held
            # startup stop. Preserve tracer custody if the namespace gate fails.
            # Real control EOF refuses C before fatal namespace containment.
            # C terminal reaping ends the own tracer, causing the ORIGINAL
            # ensuing identity sample to fail. No invented first error.
            self.trace.observer_peer.shutdown(socket.SHUT_WR)
            containment = contain_owned_namespace(self.controller,self.namespace_child,matches[0].pid,
                                                  min(self.trace.clock.outer+5,time.monotonic()+2),tracer=matches[0])
        self.identity_fault = {'containmentGate':containment,'pid':matches[0].pid,'birth':result['birth'],'stage':'trace' if obj is self.trace else 'session'}
        if obj is not self.trace: matches[0].kill()

    def original_birth_refusal(self, trace, packet):
        fields = packet.decode('ascii').split()
        assert len(fields) == 17
        # Deliberately misbind a genuine request to a *different actual live
        # owned birth*. No syscall text, task facts or ledger entry is changed.
        # This calls ORIGINAL ack_held, rather than sending corrupt ACK bytes.
        other = native.proc_identity(self.session.monitor['child'].pid,self.session.monitor['identity'])
        assert other['birth'] != int(fields[6]), 'distinct genuine wrong-birth input absent'
        wrong = fields.copy(); wrong[6] = str(other['birth'])
        invalid = ' '.join(wrong).encode('ascii')
        self.birth_refusal = {'actualRequest':packet.decode('ascii'),'misboundRequest':invalid.decode('ascii'),
                              'otherActualOwnedBirth':[other['pid'],other['birth']]}
        try: trace.ack_held(invalid)
        except AssertionError as error:
            assert str(error) == 'held task birth' and trace.first_error is error
            self.birth_refusal['actualOriginalGuard'] = str(error)
        else: raise AssertionError('original dispatcher accepted a mismatched actual birth')

    def snapshot_context(self):
        with self.lock:
            live = [c for c in self.contexts if c[-1] is not None and not c[-1]['acked']]
            return live[-1] if live else None

    def resume_controls(self):
        with self.lock:
            pending = [p for p in self.paused_controls if not p.get('resumed')]
        for row in pending:
            # STOP genuinely delayed the original PID or UID control RPC. The
            # child is still unreaped own Popen; fresh facts precede resume.
            assert row['child'].poll() is None
            assert native.proc_identity(row['identity']['pid'],row['identity']) == row['identity']
            def actually_stopped():
                raw = (Path('/proc')/str(row['identity']['pid'])/'stat').read_text()
                return raw[raw.rindex(')')+2:].split()[0] == 'T'
            wait_for(actually_stopped,min(time.monotonic()+.05,row['at']+.5),'original credential control STOP readiness')
            assert native.proc_identity(row['identity']['pid'],row['identity']) == row['identity']
            os.kill(row['identity']['pid'],signal.SIGCONT)
            row['resumed'] = True
            self.resumed_controls.append({'pid':row['identity']['pid'],'birth':row['identity']['birth'],
                                         'method':row['method'],'actualStopped':True})


def actual_clone_orders(witness):
    raw = bytes(witness.debug_bytes)
    observations = []; parent_indices = {}; births_for_pid = {}
    for packet in witness.requests:
        fields = packet.decode('ascii').split()
        if len(fields) != 17 or fields[4] != 'clone': continue
        parent,child = int(fields[5]),int(fields[7])
        for pid,birth in ((parent,int(fields[6])),(child,int(fields[8]))):
            known = births_for_pid.setdefault(pid,birth)
            assert known == birth, 'debug order TID reuse is ambiguous: qualification refused'
        parent_events = list(re.finditer(rb'\[wait\(0x[0-9a-f]+\) = '+str(parent).encode()+rb'\].*?EVENT_(?:FORK|CLONE|VFORK) \([123]\)',raw))
        child_events = list(re.finditer(rb'\[wait\(0x[0-9a-f]+\) = '+str(child).encode()+rb'\].*?EVENT_STOP \(128\)',raw))
        # TID reuse cannot be inferred from numeric IDs; every observation is
        # bound to the original exact request births and its actual checkpoint.
        index = parent_indices.get(parent,0); parent_indices[parent] = index+1
        if len(parent_events) <= index or len(child_events) != 1: continue
        p,c = parent_events[index].start(),child_events[0].start()
        observations.append({'sequence':int(fields[3]),'parentBirth':[parent,int(fields[6])],
                             'childBirth':[child,int(fields[8])],
                             'order':'parent-first' if p < c else 'child-first',
                             'parentDebugOffset':p,'childDebugOffset':c})
    return observations


def leader_clone_witness(trace,witness,thread):
    contexts = [context for context in witness.contexts if context[2] == 'clone' and
                trace.parser.task_for_key((context[3],context[4])).get('nativeLeader')]
    assert contexts, 'actual leader clone admission absent'
    rows = []
    for context in contexts:
        fields = context[0].decode('ascii').split()
        assert bool(int(fields[10]) & 0x10000) == thread
        parent = trace.parser.task_for_key((context[3],context[4]))
        child = trace.parser.task_for_key((int(fields[7]),int(fields[8])))
        assert (child['facts']['tgid'] == parent['facts']['tgid']) == thread
        assert (child['fds'] is parent['fds']) == thread
        if not thread: assert child['facts']['ppid'] == parent['facts']['tgid']
        assert any(row['packet'] == context[0] and row['errorAtSend'] is None for row in witness.sends)
        rows.append({'parentBirth':parent['key'],'childBirth':child['key'],'actualRawFlags':int(fields[10]),
                     'actualSameGroup':thread,'originalSharedFDTable':thread,'sequence':context[1]})
    return rows


def strict_vfork_returns(trace,witness):
    raw = bytes(witness.trace_bytes)
    contexts = [context for context in witness.contexts if context[2] == 'clone' and context[5] == 'vfork' and
                trace.parser.task_for_key((context[3],context[4])).get('nativeLeader')]
    assert len(contexts) == 1, 'actual leader held vfork event not observed'
    rows = []
    for context in contexts:
        fields = context[0].decode('ascii').split()
        child_key = (int(fields[7]),int(fields[8]))
        child = trace.parser.task_for_key(child_key)
        marker = b'# R1353 '+str(context[1]).encode()+b'\n'
        offset = raw.index(marker)
        pattern = (rb'^(?:\[pid\s+)?'+str(context[3]).encode()+rb'(?:\])?\s+'
                   rb'(?:vfork\(.*?\)|<\.\.\. vfork resumed>.*?)\s+=\s+([1-9][0-9]*)'
                   rb'(?: /\* ([1-9][0-9]*) in strace\x27s PID NS \*/)?$')
        matches = list(re.finditer(pattern,raw[offset:],re.M))
        assert len(matches) == 1, 'one strict actual leader vfork parent return after its checkpoint required'
        match = matches[0]
        assert int(match[1]) == child['facts']['nspid'][-1]
        assert match[2] is None or int(match[2]) == child_key[0]
        assert child.get('exited') and child['parent'] == (context[3],context[4])
        assert (context[3],context[4]) not in trace.parser.clone_edges, 'actual vfork return not reconciled'
        rows.append({'sequence':context[1],'parentBirth':[context[3],context[4]],'admittedChildBirth':child_key,
                     'actualLocalReturn':int(match[1]),'actualOuterReturn':None if match[2] is None else int(match[2]),
                     'checkpointOffset':offset,'returnOffset':offset+match.start(),
                     'liveAdmissionBeforeReturn':True,'originalTerminalHistoryReconciled':True})
    return rows


def selected_exec_witness(trace,witness,capture,mode):
    syscall = 'execveat' if mode == 'execveat' else 'execve'
    assert capture['events'][0]['syscall'].startswith(syscall+'(')
    key = capture['events'][0]['tidBirth']
    records = [context for context in witness.contexts if context[2] == 'record' and context[5] == syscall and
               (context[3],context[4]) == key and context[8] is capture]
    assert len(records) == 1 and any(row['packet'] == records[0][0] and row['errorAtSend'] is None for row in witness.sends)
    calls = [event['syscall'] for event in capture['events']]
    dup = [i for i,call in enumerate(calls) if re.match(r'(?:dup\(0\)|dup[23]\(0,|fcntl\(0,\s*F_DUPFD)',call)]
    reads = [(i,call) for i,call in enumerate(calls) if call.startswith('read(')]
    assert dup and reads and dup[0] < reads[0][0]
    assert re.search(r'\)\s+=\s+0$',reads[-1][1]), 'last actual stdin read was not EOF'
    assert all(not re.search(r'\)\s+=\s+0$',call) for _,call in reads[:-1]), 'read after actual EOF'
    return {'actualExecSyscall':syscall,'actualHeldSequence':records[0][1],'originalExecIdentity':capture['identity'],
            'originalLiveFD0Endpoint':capture['endpoint'],'actualReadAndLineageRecords':capture['events'],
            'originalConsumedBytes':bytes(capture['bytes']).decode('ascii'),'orderedDupReadEOF':True}


def tools_for(args,owner,clock):
    names = ('nsenter','setpriv','xauth','Xvfb','dbus-daemon','dbus-monitor','dbus-send','dunst')
    tools = {}
    for name in names:
        path = shutil.which(name)
        assert path, 'actual original service prerequisite missing: '+name
        path = str(Path(path).resolve(strict=True))
        info = Path(path).stat()
        assert stat.S_ISREG(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o022
        tools[name] = {'path':path,'sha256':digest(path),'bytes':info.st_size}
    packages = {'nsenter':'util-linux','setpriv':'util-linux','xauth':'xauth','Xvfb':'xvfb',
                'dbus-daemon':'dbus-daemon','dbus-monitor':'dbus-bin','dbus-send':'dbus-bin','dunst':'dunst'}
    for name,package in packages.items():
        child,row = owner.popen(['/usr/bin/dpkg-query','--show','--showformat=${binary:Package}\t${Version}\t${db:Status-Status}\n',package],
            stdout=subprocess.PIPE,stderr=subprocess.PIPE,env={'PATH':'/usr/bin:/bin','LANG':'C','TMPDIR':str(args.mount_child/'tmp')})
        metadata,error = child.communicate(timeout=clock.wait(1))
        assert child.returncode == 0 and not error and len(metadata) <= 8192
        columns = metadata.decode().strip().split('\t')
        assert len(columns) == 3 and columns[0].split(':')[0] == package and columns[2] == 'installed'
        tools[name].update(package=package,version=columns[1],installedMetadataSHA256=hashlib.sha256(metadata).hexdigest())
    stock = Path('/usr/bin/strace')
    child,row = owner.popen(['/usr/bin/dpkg-query','--show','--showformat=${binary:Package}\t${Version}\t${db:Status-Status}\n','strace'],
        stdout=subprocess.PIPE,stderr=subprocess.PIPE,env={'PATH':'/usr/bin:/bin','LANG':'C','TMPDIR':str(args.mount_child/'tmp')})
    metadata,error = child.communicate(timeout=clock.wait(1))
    assert child.returncode == 0 and not error and metadata.decode().strip() == 'strace\t6.8-0ubuntu2\tinstalled', 'original stock package qualified separately'
    tools['stock-strace'] = {'path':str(stock),'sha256':digest(stock),'package':'strace','version':'6.8-0ubuntu2',
                             'installedMetadataSHA256':hashlib.sha256(metadata).hexdigest()}
    binary = args.derived.resolve(strict=True)
    assert digest(binary) == args.derived_sha256 and binary.read_bytes()[:4] == b'\x7fELF'
    tools['strace'] = {'path':str(binary),'sha256':args.derived_sha256,'qualification':'REVIEWED_DERIVED_BUILD',
                       'stockPackageClaim':False,'sources':PINS}
    return tools


def verify_bindings(args):
    for name,pin in PINS.items(): assert digest(ORIGINAL_ROOT/name) == pin, 'original frozen source drift: '+name
    bindings = json.loads(args.build_bindings.read_text())
    assert bindings['qualification'] == 'BUILD_ONLY' and bindings['pass']
    assert bindings['allBuildChildrenJoined'] and bindings['allBuildStreamsClosed']
    assert 'go1.25.8' in bindings['helperBuildInfo'] and 'github.com/godbus/dbus/v5\tv5.2.2' in bindings['helperBuildInfo']
    assert bindings['sources'] == PINS
    assert bindings['derivedSHA256'] == args.derived_sha256
    assert bindings['helperSHA256'] == args.helper_sha256 == digest(args.helper)
    assert bindings['actorSHA256'] == args.actor_sha256 == digest(args.actor)
    assert bindings['fixtureSources'] == {path.name:digest(path) for path in sorted(HERE.iterdir()) if path.is_file()}
    assert bindings['linuxContractSHA256'] == digest(IMPLEMENTATION/'linux_contract.py')
    assert bindings['goVersion'] == 'go1.25.8' and bindings['godbusVersion'] == 'v5.2.2'
    assert bindings['helperBuildInfoContains'] == ['go1.25.8','github.com/godbus/dbus/v5 v5.2.2']
    assert bindings['compilerSHA256'] and bindings['goExecutableSHA256'] and bindings['commands']
    return bindings


def namespace_absent(controller):
    survivors = []
    for process in Path('/proc').iterdir():
        if not process.name.isdecimal(): continue
        try: tids = list((process/'task').iterdir())
        except (FileNotFoundError,ProcessLookupError): continue
        for entry in tids:
            try:
                if os.readlink(entry/'ns/pid') == controller['ns']['pid']:
                    survivors.append({'pid':int(entry.name),'birth':stat_birth(int(entry.name))})
            except (FileNotFoundError,ProcessLookupError): pass
    return survivors


def contain_owned_namespace(controller, supervisor, tracer_pid, end, gate=None, tracer=None):
    assert controller is not None and supervisor is not None, 'missing owned namespace containment qualification'
    if gate is None:
        gate = {'qualified':False,'terminalTracerJoined':False,'supervisorJoined':False,'namespaceAbsent':False,'forcedContainment':False}
    try: birth = stat_birth(controller['pid'])
    except FileNotFoundError:
        assert namespace_absent(controller) == [], 'missing PID1 with surviving namespace'
    else:
        assert birth == controller['birth'], 'refuse reused controller PID'
        raw = (Path('/proc')/str(controller['pid'])/'stat').read_text()
        state = raw[raw.rindex(')')+2:].split()[0]
        assert controller['nspid'][-1] == 1 and controller['ns']['pid'] != os.readlink('/proc/self/ns/pid'), 'private owned PID1 required'
        if state not in ('Z','X','x'):
            facts = native.trace_task_identity(controller['pid'])
            assert all(facts[k] == controller[k] for k in ('pid','birth','ppid','tgid','uids','gids','groups','nspid','ns','uidMap','gidMap'))
            assert facts['tracer'] in (0,tracer_pid), 'foreign controller tracer'
            assert native.trace_task_identity(controller['pid']) == facts, 'fresh PID1 containment bookend'
            os.kill(controller['pid'],signal.SIGKILL)
            gate['forcedContainment'] = True
    gate['qualified'] = True
    # C EXIT-event removal and actual ptrace waits must precede the supervisor
    # join that may depend on them. Never kill the tracer to manufacture it.
    assert tracer_pid == 0 or tracer is not None and tracer.pid == tracer_pid, 'missing own tracer Popen custody'
    if tracer is not None:
        tracer.wait(timeout=max(.001,end-time.monotonic()))
    gate['terminalTracerJoined'] = True
    supervisor.wait(timeout=max(.001,end-time.monotonic()))
    gate['supervisorJoined'] = True
    assert namespace_absent(controller) == [], 'namespace survives supervisor join'
    gate['namespaceAbsent'] = True
    return gate


def cleanup_pair(owner, witness, namespace_child, controller, natural, end):
    errors = []; forced = False
    trace,session = witness.trace,witness.session
    fault_gate = (witness.identity_fault or {}).get('containmentGate') or {}
    forced = bool(fault_gate.get('forcedContainment') or getattr(trace,'fatal_containment_requested',False))
    def attempt(label, fn):
        try: return fn()
        except BaseException as error:
            errors.append({'operation':label,'error':type(error).__name__+':'+str(error)[:200]})
    containment = {'qualified':False,'terminalTracerJoined':False,'supervisorJoined':False,'namespaceAbsent':False,'forcedContainment':False}
    tracer_children = list(getattr(trace,'owned_children',()))+witness.original_children['trace']
    tracer_custody = any(child.poll() is None for child in tracer_children)
    if not natural:
        if trace is not None: attempt('first channel refusal',lambda:trace.refuse_held(owner.first or RuntimeError('TEST forced cleanup')))
        # Refuse before the fatal signal. C terminal reaping precedes the
        # supervisor wait; completed joins and fresh absence gate later kills.
        if trace is not None and PINS != OLD_PINS and hasattr(trace,'force_contain'):
            attempt('original terminal observer containment',lambda:trace.force_contain(owner.first or RuntimeError('TEST forced cleanup')))
            forced = forced or getattr(trace,'fatal_containment_requested',False)
        if controller is not None and namespace_child is not None:
            def contain_namespace():
                nonlocal forced
                observed = contain_owned_namespace(controller,namespace_child,
                    getattr(getattr(trace,'child',None),'pid',0),end,containment,
                    tracer=getattr(trace,'child',None))
                containment.update(observed)
                forced = forced or observed['forcedContainment']
                original_root = getattr(session,'root',getattr(trace,'root',None))
                if original_root is not None:
                    atomic_json(original_root/'evidence/namespace-supervisor-joined.json',
                                {'pid':namespace_child.pid,'joined':True,'controller':controller})
            attempt('owned namespace containment gate',contain_namespace)
            forced = forced or containment['forcedContainment']
        elif namespace_child is not None and not tracer_custody:
            if namespace_child.poll() is None:
                attempt('provisional own namespace launcher kill',namespace_child.kill); forced = True
            attempt('namespace supervisor join',lambda:namespace_child.wait(timeout=max(.001,end-time.monotonic())))
    tracer_kill_permitted = all(containment[key] for key in ('qualified','terminalTracerJoined','supervisorJoined','namespaceAbsent'))
    if not natural:
        if trace is not None and tracer_kill_permitted:
            if hasattr(trace,'failure_join'): attempt('original observer join',lambda:trace.failure_join(end))
        elif tracer_custody:
            errors.append({'operation':'tracer custody preserved','error':'owned namespace containment/supervisor join unresolved'})
        if session is not None and hasattr(session,'failure_join'):
            attempt('original session join',lambda:session.failure_join(end))
    # Independently account for every original constructor resource, including
    # instances whose __init__ raised before the caller obtained a reference.
    original_children = []
    if trace is not None: original_children.extend(getattr(trace,'owned_children',[]))
    if session is not None:
        original_children.extend(r['child'] for r in session.processes+session.control_processes)
    for child in original_children:
        if not any(row['child'] is child for row in owner.children): owner.children.append({'child':child,'birth':None})
        owner.streams.extend(s for s in (child.stdin,child.stdout,child.stderr) if s)
    if trace is not None:
        owner.streams.extend(getattr(trace,'owned_streams',[]))
        owner.fds.extend(getattr(trace,'owned_fds',set()))
        owner.workers.extend(getattr(trace,'threads',[])+getattr(trace,'held_workers',[]))
        for name in ('trace_pipe','observer_peer'):
            if hasattr(trace,name): owner.streams.append(getattr(trace,name))
    if session is not None:
        owner.fds.extend(getattr(session,'owned_fds',()))
        owner.workers.extend(session.pumps)
        if hasattr(session,'monitor_pump'): owner.workers.append(session.monitor_pump)
    child_rows = []
    for row in owner.children:
        child = row['child']
        is_tracer = child in tracer_children
        if child.poll() is None and (not is_tracer or tracer_kill_permitted):
            attempt('unreaped own child kill',child.kill); forced = True
        joined = attempt('each child join',lambda child=child:child.wait(timeout=max(.001,end-time.monotonic())))
        absent = False; replacement = None
        try: replacement = stat_birth(child.pid)
        except FileNotFoundError: absent = True
        child_rows.append({'pid':child.pid,'birth':row['birth'],'joined':joined is not None,'exit':joined,
                           'pidAbsent':absent,'freshBirth':replacement})
    protected_streams = (getattr(trace,'owned_streams',[])+[s for child in tracer_children for s in (child.stdin,child.stdout,child.stderr) if s]) if tracer_custody and not tracer_kill_permitted else []
    protected_fds = getattr(trace,'owned_fds',set()) if tracer_custody and not tracer_kill_permitted else set()
    for stream in owner.streams:
        if not any(stream is protected for protected in protected_streams): attempt('each stream close',stream.close)
    for fd in set(owner.fds)-set(protected_fds): attempt('each allocated fd close',lambda fd=fd:os.close(fd))
    pumps = []
    for worker in set(owner.workers):
        if worker.ident is not None: attempt('each worker join',lambda worker=worker:worker.join(max(0,end-time.monotonic())))
        pumps.append({'name':worker.name,'joined':not worker.is_alive()})
    tasks = []
    if trace is not None and hasattr(trace,'parser'):
        keys = set(trace.custody_keys) | {t['key'] for t in trace.parser.tasks.values()} | set(trace.parser.remap_history)
        for pid,birth in sorted(keys):
            absent = False; replacement = None
            try: replacement = stat_birth(pid)
            except FileNotFoundError: absent = True
            tasks.append({'pid':pid,'birth':birth,'pidAbsent':absent,'freshBirth':replacement})
    survivors = attempt('fresh whole namespace absence',lambda:namespace_absent(controller)) if controller else []
    complete = not errors and all(r['joined'] and (r['pidAbsent'] or r['birth'] is not None and
        r['freshBirth'] is not None and r['freshBirth'] != r['birth']) for r in child_rows) and all(
        r['pidAbsent'] or r['freshBirth'] is not None and r['freshBirth'] != r['birth'] for r in tasks) and all(
        p['joined'] for p in pumps) and survivors == []
    return {'complete':complete,'naturalCompletion':natural and not forced,'forcedContainment':forced,
            'children':child_rows,'tasks':tasks,'pumps':pumps,'resourcesClosed':not errors,
            'namespaceSurvivors':survivors,'errors':errors,'containmentGate':containment,
            'tracerKillPermitted':tracer_kill_permitted,'tracerCustodyPreserved':tracer_custody and not tracer_kill_permitted}


def guard_refusal_witness(witness, context, root, end):
    trace,session = witness.trace,witness.session
    wait_for(lambda:trace.error is not None and session.error is not None,end,'original first refusal')
    first = session.first_error
    assert first is not None and trace.first_error is first
    assert all(row['errorAtSend'] is None for row in witness.sends), 'original ACK attempted after first failure'
    if context is not None:
        packet,seq,phase,tid,birth,syscall,arguments,table,capture,conn = context
        assert not conn['acked'] and packet not in trace.pending_acks, 'watched ACK after first refusal'
        before = native.trace_task_identity(tid)
        assert before['birth'] == birth and before['tracer'] == trace.child.pid
        text = (Path('/proc')/str(tid)/'stat').read_text()
        assert text[text.rindex(')')+2:].split()[0] == 't', 'watched syscall resumed after refusal'
        # Observe a real interval after publication; no resend/restart actions.
        until = min(end,time.monotonic()+.08)
        while time.monotonic() < until:
            assert session.first_error is first and not conn['acked'] and packet not in trace.pending_acks
            assert native.trace_task_identity(tid) == before
            time.sleep(.005)
        return {'firstFailure':session.error,'watchedSequence':seq,'watchedSyscall':syscall,
                'actualHeldBirth':[tid,birth],'watchedACK':False,'actualStopRetained':True,
                'noObservedWatchedRestartAfterPublication':True}
    unsent = [packet for packet in witness.requests if not any(row['packet'] == packet for row in witness.sends)]
    assert unsent, 'no actual unacknowledged original request witnessed on refusal'
    packet = unsent[-1]
    fields = packet.decode('ascii').split()
    seq,tid,birth = int(fields[3]),int(fields[5]),int(fields[6])
    facts = native.trace_task_identity(tid)
    assert facts['birth'] == birth and facts['tracer'] == trace.child.pid
    raw = (Path('/proc')/str(tid)/'stat').read_text()
    assert raw[raw.rindex(')')+2:].split()[0] == 't'
    before_sends = len(witness.sends)
    until = min(end,time.monotonic()+.08)
    while time.monotonic() < until:
        assert trace.first_error is first is session.first_error
        assert len(witness.sends) == before_sends and packet not in trace.pending_acks
        assert native.trace_task_identity(tid) == facts
        time.sleep(.005)
    return {'firstFailure':session.error,'watchedSequence':seq,'watchedSyscall':fields[9],
            'actualHeldBirth':[tid,birth],'watchedACK':False,'actualStopRetained':True,
            'noObservedWatchedRestartAfterPublication':True}


def exact_failure_origin(error, functions):
    origins = []
    tb = error.__traceback__
    while tb is not None:
        for name,function in functions.items():
            if tb.tb_frame.f_code is function.__code__:
                origins.append({'function':name,'line':tb.tb_lineno})
        tb = tb.tb_next
    return origins


def control_eof_witness(witness, context, end):
    trace,session = witness.trace,witness.session
    marker = b'R1353 refusal: control EOF; held stops require owned containment'
    wait_for(lambda:witness.held_channel_eofs and marker in witness.debug_bytes,end,
             'actual original control EOF and C refusal diagnostic',lambda:witness.owner.first)
    first = session.first_error
    assert type(first) is AssertionError and str(first) == 'held channel EOF/truncation/ancillary'
    origins = exact_failure_origin(first,{'PassiveStrace.dispatch_held':native.PassiveStrace.dispatch_held})
    assert origins and trace.first_error is first and session.error == trace.error
    assert context is not None and not context[-1]['acked'] and context[0] not in trace.pending_acks
    refusals = re.findall(rb'R1353 refusal: ([^;\n]+); held stops require owned containment',witness.debug_bytes)
    assert refusals == [b'control EOF'], 'unrelated or earlier C refusal cannot qualify control EOF'
    return {'originalRecvmsgEOF':witness.held_channel_eofs,'originalFirstErrorOrigin':origins,
            'actualCRefusal':'control EOF','watchedACK':False}


def actor_evidence(root,label):
    path = root/'evidence'/('helper-'+label+'.json')
    info = path.lstat()
    assert stat.S_ISREG(info.st_mode) and info.st_uid == info.st_gid == 1000 and info.st_size <= 8192
    value = native.strict_json(path.read_bytes())
    assert value['label'] == label
    return value


def failed_hello_witness(session,root):
    actor = actor_evidence(root,'hello-refused')
    assert actor['error'], 'real godbus Hello failure absent'
    calls = [m for m in session.messages if m['type'] == 1 and m['header'].get(3) == 'Hello'
             and m['header'].get(2) == 'org.freedesktop.DBus' and m['signature'] == 's'
             and m['body'] == ['TEST invalid Hello argument']]
    assert len(calls) == 1, 'actual failed first Hello call missing'
    call = calls[0]
    replies = [m for m in session.messages if m['type'] in (2,3) and m['header'].get(6) == call['header'][7]
               and m['header'].get(5) == call['serial']]
    assert len(replies) == 1 and replies[0]['type'] == 3, 'actual failed Hello ERROR reply missing'
    assert call['header'][7] not in session.credentials and call['header'][7] not in session.active_names
    return {'actor':actor,'call':call,'errorReply':replies[0]}


def shared_table_witness(witness,root,case):
    actor = actor_evidence(root,'destructive-thread')
    trace = witness.trace
    capture, = trace.parser.selected
    conn, = capture['busConnections']
    names = {'shared-close':'close','shared-alias-close':'close','shared-dup2':'dup2','shared-dup3':'dup3',
             'shared-shutdown':'shutdown','shared-close-range-unshare':'close_range'}
    matches = [row for row in witness.entries if row['name'] == names[case] and
               row['task']['facts']['nspid'][-1] == actor['tid'] and row['task'].get('capture') is capture]
    assert matches and actor['tid'] != capture['identity']['nspid'][-1]
    # Original CLONE_FILES admission itself includes real kcmp and the whole
    # fresh FD set; the actual held context must use that original table.
    rows = [context for context in witness.contexts if context[-1] is conn and
            context[5] == names[case] and trace.parser.task_for_key((context[3],context[4]))['facts']['nspid'][-1] == actor['tid']]
    assert rows, 'independent shared-table destructive entry did not reach original credential hold'
    context = rows[0]
    intents = [context for context in witness.contexts if context[2] == 'entry' and context[5] == 'connect' and context[8] is capture]
    assert len(intents) == 1 and context[3] != intents[0][3] and context[7] is intents[0][7]
    assert conn['acked'] and any(row['packet'] == context[0] and row['errorAtSend'] is None for row in witness.sends)
    return {'actor':actor,'destructiveSequence':context[1],'syscall':context[5],
            'connectBirth':conn['intent'],'destructiveBirth':[context[3],context[4]],
            'originalSharedTableReference':True,'originalCloneFDProofRequired':True}


def last_table_witness(witness,root):
    actor = actor_evidence(root,'exiting-thread')
    assert actor['unshare'] is True
    trace = witness.trace
    rows = [row for row in witness.entries if row['name'] == 'exit' and row['task']['facts']['nspid'][-1] == actor['tid']]
    assert len(rows) == 1
    task = rows[0]['task']; capture = task['capture']; conn, = capture['busConnections']
    owner = trace.parser.tasks[conn['owner']['pid']]
    assert task['fds'] is not owner['fds'] and any(task['fds'] is table for table in conn['tables'])
    assert any('CLOSE_RANGE_UNSHARE' in event['syscall'] and event['tidBirth'] == task['key'] for event in capture['events'])
    libc = ctypes.CDLL(None,use_errno=True)
    comparison = libc.syscall(312,task['key'][0],owner['key'][0],2,0,0)
    assert comparison > 0, 'actual last-thread table remains shared with peer owner'
    assert not conn['acked'] or any(row['packet'].decode().split()[9] == 'close_range' for row in witness.sends)
    return {'actor':actor,'heldThreadBirth':task['key'],'survivingOwnerBirth':owner['key'],
            'actualKCMP_FILES':comparison,'originalTableLineageDistinct':True}


def surviving_thread_witness(witness,root):
    actor = actor_evidence(root,'exiting-thread')
    assert actor['unshare'] is False
    trace = witness.trace
    capture, = trace.parser.selected
    rows = [context for context in witness.contexts if context[2] == 'entry' and context[5] == 'exit' and
            trace.parser.task_for_key((context[3],context[4]))['facts']['nspid'][-1] == actor['tid']]
    assert len(rows) == 1 and rows[0][-1] is None
    owner = trace.parser.tasks[capture['identity']['pid']]
    assert rows[0][7] is owner['fds']
    assert any(row['packet'] == rows[0][0] and row['errorAtSend'] is None for row in witness.sends)
    return {'actor':actor,'threadBirth':[rows[0][3],rows[0][4]],'survivingOwnerBirth':owner['key'],
            'originalSurvivingOwnerAndTableGuard':True}


def owner_exit_witness(witness):
    rows = [context for context in witness.contexts if context[2] == 'entry' and context[5] == 'exit' and context[-1] is not None]
    assert len(rows) == 1
    context = rows[0]; conn = context[-1]
    assert (context[3],context[4]) == (conn['owner']['pid'],conn['owner']['birth']) and conn['acked']
    assert any(row['packet'] == context[0] and row['errorAtSend'] is None for row in witness.sends)
    return {'ownerBirth':conn['intent'],'originalCredentialACKBeforeOwnerExit':context[1],
            'consumedControls':context[8]['busLifetimeReceipts'][0]['controls']}


def constructor_case(case):
    return case in ('session-child-failure','session-fd-failure','session-netfd-failure',
                    'session-identity-failure','tracer-child-failure','tracer-identity-failure') or '-thread-start-' in case


def constructor_fault_witness(case,witness,error,result):
    original_frames = []
    tb = error.__traceback__
    while tb is not None:
        frame = tb.tb_frame
        if Path(frame.f_code.co_filename).resolve() == ORIGINAL_ROOT/'native_case.py':
            original_frames.append({'function':frame.f_code.co_name,'line':tb.tb_lineno,
                                    'pid':frame.f_locals.get('pid')})
        tb = tb.tb_next
    result['actualConstructorFirstErrorOrigin'] = original_frames
    if not original_frames: return False
    if '-thread-start-' in case:
        fault = witness.thread_fault
        valid = fault is not None and fault['restored'] and fault['started'] is False and type(error) is RuntimeError and 'start new thread' in str(error)
        result['actualThreadAllocationFault'] = None if fault is None else {
            key:val for key,val in fault.items() if key != 'thread'}
        return valid
    if case.endswith('identity-failure'):
        fault = witness.identity_fault
        result['actualOwnChildIdentityFault'] = fault
        if fault is None: return False
        failed_identity = any(row['function'] in ('proc_identity','startup_identity') and row['pid'] == fault['pid'] for row in original_frames)
        failed_live = (fault['stage'] == 'session' and type(error) is AssertionError and str(error) == 'standard process failed before identity' and any(row['function'] == 'live' for row in original_frames))
        return (failed_identity or failed_live) and isinstance(error,(AssertionError,FileNotFoundError,ProcessLookupError))
    if case == 'session-netfd-failure':
        return type(error) is FileNotFoundError and error.filename == '/proc/'+str(witness.controller['pid'])+'/ns/net'
    if case == 'session-fd-failure': return isinstance(error,OSError) and error.errno == 24
    stage = 'trace' if case == 'tracer-child-failure' else 'session'
    allocated = [child for child in witness.original_children[stage] if isinstance(child.args,list) and '/usr/bin/false' in child.args]
    result['actualRefusingConstructorChildren'] = [{'pid':child.pid,'argv':child.args,'exit':child.poll()} for child in allocated]
    origins = {'startup_identity','proc_identity','live','ready'}
    return bool(allocated) and any(row['function'] in origins for row in original_frames) and isinstance(error,(AssertionError,FileNotFoundError,ProcessLookupError,native.IncompleteInstalledContract))


def retain_old_constructor_resources(error,witness,owner):
    """Compensation AFTER observing OLD RED, never original cleanup proof.

    Exact old original constructor frames retain their actual pipe allocation
    results even when no instance was returned. No descriptor enumeration or
    guessed number grants cleanup ownership.
    """
    trace,session = witness.trace,witness.session
    streams = list(owner.streams)
    if trace is not None:
        streams.extend(getattr(trace,name) for name in ('observer_peer','trace_pipe') if hasattr(trace,name))
    owner.streams.extend(streams)
    wrapped = set()
    for stream in streams:
        try: wrapped.add(stream.fileno())
        except ValueError: pass
    if session is not None and hasattr(session,'netfd'):
        fd = session.netfd
        if fd not in wrapped:
            try: link = os.readlink('/proc/self/fd/'+str(fd))
            except FileNotFoundError: link = None
            if link is not None:
                assert link == session.controller['ns']['net'], 'OLD namespace fd was replaced; refuse compensation'
                owner.fd(fd)
    tb = error.__traceback__
    while tb is not None:
        local = tb.tb_frame.f_locals
        if trace is not None and local.get('self') is trace and Path(tb.tb_frame.f_code.co_filename).resolve() == ORIGINAL_ROOT/'native_case.py':
            for name in ('readfd','writefd'):
                fd = local.get(name)
                if type(fd) is not int or fd in wrapped: continue
                try: info = os.fstat(fd)
                except OSError as failure:
                    if failure.errno == 9: continue  # Actual closed own allocation, not an error suppression during cleanup.
                    raise
                assert stat.S_ISFIFO(info.st_mode), 'OLD constructor pipe fd drift'
                owner.fd(fd)
        tb = tb.tb_next


def replace_owned_socket(native,trace,session,owner):
    original = session.directory/'bus'
    old = session.directory/'TEST-original-bus'
    before = original.lstat()
    assert before.st_ino == session.socket_identity['inode'] and not old.exists()
    original.rename(old)
    replacement = owner.stream(socket.socket(socket.AF_UNIX,socket.SOCK_STREAM))
    replacement.bind(str(original)); replacement.listen(1)
    os.chown(original,before.st_uid,before.st_gid)
    os.chmod(original,stat.S_IMODE(before.st_mode))
    after = original.lstat()
    assert (after.st_dev,after.st_ino) != (before.st_dev,before.st_ino)
    assert (after.st_uid,after.st_gid,after.st_mode) == (before.st_uid,before.st_gid,before.st_mode)
    try: session.assert_continuous()
    except AssertionError as error:
        trace.refuse_held(error)
        assert trace.first_error is error is session.first_error
    else: raise AssertionError('original continuous guard accepted a replaced actual bus socket')
    return {'originalSocket':[before.st_dev,before.st_ino],'replacementSocket':[after.st_dev,after.st_ino],
            'sameOwnedPathModeUIDGID':True,'originalGuard':'OwnedNotificationsSession.assert_continuous',
            'scope':'real owned listener replacement while original selected socket remains held'}


def wrong_table_refusal(witness,context):
    trace,session = witness.trace,witness.session
    assert witness.blocked_hold and witness.blocked_hold['context'] is context
    with trace.parser.lock:
        other = trace.parser.tasks[trace.controller['pid']]
        actual = trace.parser.tasks[context[3]]
        assert actual['fds'] is context[7] and other['fds'] is not actual['fds']
        other_facts = native.trace_task_identity(other['key'][0])
        assert other_facts == other['facts']
        wrong = list(context); wrong[7] = other['fds']
    libc = ctypes.CDLL(None,use_errno=True)
    comparison = libc.syscall(312,context[3],other['key'][0],2,0,0)
    assert comparison > 0, 'different genuine original FD table not witnessed'
    assert native.proc_identity(session.monitor['child'].pid,session.monitor['identity']) == session.monitor['identity']
    os.kill(session.monitor['child'].pid,signal.SIGCONT)
    # This uses actual original credentials/consumed roundtrips and fresh peer
    # checks. Only the supplied context's table binding is deliberately wrong;
    # the original ledger, real socket and every identity remain untouched.
    try: trace.complete_held(tuple(wrong))
    except AssertionError as error:
        assert str(error) == 'held task/table incarnation changed'
        assert trace.first_error is error is session.first_error
    else: raise AssertionError('original dispatcher accepted a different actual FD table')
    finally: witness.release_held_worker()
    return {'actualHeldBirth':[context[3],context[4]],'otherActualBirth':other['key'],
            'actualKCMP_FILES':comparison,'originalGuard':'held task/table incarnation changed'}


def run_pair(args,root):
    owner = Custody(); witness = Observations(args.case,owner)
    controller = launcher = trace = session = None; natural = False
    clock = native.CaseClock(time.monotonic()+110)
    result = {'case':args.case,'kernelOutcome':'FAIL','RuntimeProof':'PENDING','nativeEligibility':'HOLD',
              'wholeContract':'INCOMPLETE','scope':'original pair case only'}
    old_profile = sys.getprofile()
    constructor_fds = None
    constructor_stage = None
    try:
        sys.setprofile(witness.profile); threading.setprofile(witness.profile)
        assert os.geteuid() == 0 and sys.platform == 'linux' and os.uname().machine == 'x86_64'
        assert os.readlink('/proc/self/ns/pid') == os.readlink('/proc/1/ns/pid'), 'original parser host PID namespace'
        assert Path('/tmp').stat().st_ino == (root/'tmp-display').stat().st_ino and Path('/tmp').stat().st_dev == (root/'tmp-display').stat().st_dev
        native.qualified_runner(root/'home/.cursor',args.machine_id)
        tools = tools_for(args,owner,clock); bindings = verify_bindings(args)
        outer,inner = socket.socketpair(); owner.stream(outer); owner.stream(inner)
        mode,helper_mode,expected = CASES[args.case]
        command = ['/usr/bin/unshare','--net','--pid','--fork','--mount-proc','--kill-child=KILL',
            sys.executable,'-B','-S',str(root/'recipe/matrix_controller.py'),'--root',str(root),
            '--fd',str(inner.fileno()),'--actor',str(root/'TEST-actor'),'--primary',str(root/'TEST-helper'),
            '--selector',str(root/'TEST-selector'),'--mode',mode,'--deadline',str(clock.outer)]
        out = owner.stream((root/'evidence/controller.stdout').open('xb'))
        err = owner.stream((root/'evidence/controller.stderr').open('xb'))
        launcher,record = owner.popen(command,pass_fds=(inner.fileno(),),start_new_session=True,stdout=out,stderr=err,
            cwd=root/'workspace',env={'PATH':'/usr/bin:/bin','TMPDIR':str(root/'tmp'),'PYTHONDONTWRITEBYTECODE':'1'})
        record['birth'] = native.startup_identity(launcher.pid)['birth']; inner.close()
        atomic_json(root/'evidence/namespace-supervisor-qualified.json',{'pid':launcher.pid,'birth':record['birth']})
        hello = native.control_receive(outer,clock); assert hello['kind'] == 'controller'
        observer = native.OuterObservation(launcher,outer,{},clock)
        controller = observer.controller_identity(hello['identity'])
        atomic_json(root/'evidence/controller-qualified.json',controller)
        witness.controller,witness.namespace_child = controller,launcher
        if args.case == 'session-netfd-failure':
            # Actual prior owned PID1 death makes the ORIGINAL namespace-open
            # fail. Its retained tuple is negative input, never live authority.
            assert native.proc_identity(controller['pid'],controller) == controller
            os.kill(controller['pid'],signal.SIGKILL)
            launcher.wait(timeout=clock.wait(1))
            assert not (Path('/proc')/str(controller['pid'])).exists()
        if args.case == 'session-child-failure':
            # Real standard child exits; no original authority function replaced.
            tools['xauth'] = {'path':'/usr/bin/false','sha256':digest('/usr/bin/false')}
        constructor_fds = descriptors()
        constructor_stage = 'session'
        if args.case == 'session-fd-failure':
            # A real pipe/open allocation failure in the original constructor.
            count = len(list(Path('/proc/self/fd').iterdir()))
            soft,hard = resource.getrlimit(resource.RLIMIT_NOFILE)
            resource.setrlimit(resource.RLIMIT_NOFILE,(count+2,hard))
            try: session = native.OwnedNotificationsSession(controller,root,clock,tools,90)
            finally: resource.setrlimit(resource.RLIMIT_NOFILE,(soft,hard))
        else: session = native.OwnedNotificationsSession(controller,root,clock,tools,90)
        if args.case == 'tracer-child-failure':
            tools['strace'] = {'path':'/usr/bin/false','sha256':digest('/usr/bin/false'),'qualification':'TEST_REAL_REFUSAL_ACTOR'}
        installed = (root/'TEST-helper',root/'TEST-selector','TEST-binding','TEST-generation')
        constructor_fds = descriptors()
        constructor_stage = 'trace'
        trace = native.PassiveStrace(controller,installed,root,clock,tools,session)
        assert not constructor_case(args.case), 'intended original constructor fault did not occur'
        services = {key:session.env[key] for key in ('DBUS_SESSION_BUS_ADDRESS','DISPLAY','XAUTHORITY')}
        monitor_stopped = args.case in ('delayed-controls','monitor-eof','diagnostic-race','final-ack-race','final-incarnation-drift','control-eof','credential-deadline','hello-refused','owner-exit','wrong-name','wrong-socket','wrong-table','output-reader-failure','debug-reader-failure','output-reader-deadline','fatal-die','diagnostic-eof','pid-control-eof','uid-control-eof')
        if monitor_stopped:
            assert native.proc_identity(session.monitor['child'].pid,session.monitor['identity']) == session.monitor['identity']
            os.kill(session.monitor['child'].pid,signal.SIGSTOP)
        native.control_send(outer,{'kind':'environment','services':services,'helperMode':helper_mode,
                                  'badHome':args.case == 'wrong-home'},clock)
        leader = native.control_receive(outer,clock); assert leader['kind'] == 'leader'
        trace.select_leader(leader['identity'],time.monotonic()+clock.wait(1))
        witness.active = True; clock.native_started()
        native.control_send(outer,{'kind':'go'},clock)
        watched = None
        if monitor_stopped:
            # Real Go startup trace progress must not consume the idle allowance.
            absolute_end = min(clock.work,clock.outer-8)
            with trace.parser.lock: progress = trace.parser.events
            idle_end = min(absolute_end,time.monotonic()+.6)
            while True:
                assert time.monotonic() < absolute_end, 'actual held destructive context absolute deadline'
                with trace.parser.lock: current_progress = trace.parser.events
                if current_progress > progress:
                    progress = current_progress; idle_end = min(absolute_end,time.monotonic()+.6)
                assert time.monotonic() < min(absolute_end,idle_end), 'actual held destructive context absolute/no-progress deadline'
                if trace.error or owner.first: raise RuntimeError(trace.error or owner.first)
                watched = witness.snapshot_context()
                assert time.monotonic() < min(absolute_end,idle_end), 'actual held destructive context late acquisition'
                if watched is not None: break
                time.sleep(min(.005,max(0,min(absolute_end,idle_end)-time.monotonic())))
            if args.case == 'monitor-eof': session.monitor['child'].kill()
            elif args.case in ('diagnostic-race','final-ack-race'):
                if args.case == 'final-ack-race':
                    os.kill(session.monitor['child'].pid,signal.SIGCONT)
                    wait_for(lambda:witness.credential_gate is not None,time.monotonic()+clock.wait(.9),
                             'actual original credentials ready before final ACK',lambda:trace.error or owner.first)
                    gate = witness.credential_gate
                    assert gate['conn'] is watched[-1] and not gate['conn']['acked']
                    assert gate['name'] in session.active_names and all(session.roundtrip_present(c) for c in gate['controls'])
                    result['originalReadyBeforeFinalACKFailure'] = {'name':gate['name'],'controls':gate['controls'],
                                                                 'actualHeldSequence':watched[1]}
                probe_spec = importlib.util.spec_from_file_location('TEST_reader_probe',HERE/'original_reader_boundary.py')
                probe = importlib.util.module_from_spec(probe_spec); probe_spec.loader.exec_module(probe)
                result['readerProbe'] = probe.diagnostic_failure_at_held_ack(native,trace,session,root)
                witness.release_held_worker()
            elif args.case == 'final-incarnation-drift':
                os.kill(session.monitor['child'].pid,signal.SIGCONT)
                wait_for(lambda:witness.credential_gate is not None,time.monotonic()+clock.wait(.9),
                         'actual original credentials consumed before incarnation drift',lambda:trace.error or owner.first)
                gate = witness.credential_gate
                assert gate['conn'] is watched[-1] and not gate['conn']['acked'] and gate['name'] in session.active_names
                assert all(session.roundtrip_present(c) for c in gate['controls']) and session.error is None
                export = native.control_receive(outer,clock)
                assert export['kind'] == 'exported-socket' and tuple(export['socket']) == watched[-1]['socket']
                native.control_send(outer,{'kind':'shutdown-exported'},clock)
                changed = native.control_receive(outer,clock)
                assert changed == {'kind':'exported-shutdown','socket':export['socket']}
                name = gate['name']
                wait_for(lambda:name not in session.active_names,time.monotonic()+clock.wait(.5),
                         'actual monitored selected name departure after endpoint shutdown',lambda:session.error or owner.first)
                departure = [m for m in session.messages if m['type'] == 4 and m['header'].get(7) == 'org.freedesktop.DBus' and
                    m['header'].get(3) == 'NameOwnerChanged' and m['body'] == [name,name,'']]
                assert len(departure) == 1 and session.error is None and trace.error is None
                assert all(session.roundtrip_present(c) for c in gate['controls'])
                result['actualFinalIncarnationDrift'] = {'consumedControls':gate['controls'],'name':name,
                    'actualMonitoredDeparture':departure[0],'realExportedSocket':export['socket'],
                    'independentShutdownResult':changed,'actualHeldSequence':watched[1]}
                witness.release_held_worker()
            elif args.case == 'control-eof':
                assert trace.error is None and session.error is None and not watched[-1]['acked']
                result['actualControlWriteShutdown'] = {'sequence':watched[1],'at':time.monotonic(),
                    'originalPeerFD':trace.observer_peer.fileno()}
                trace.observer_peer.shutdown(socket.SHUT_WR)
            elif args.case == 'wrong-socket': result['actualSocketRefusal'] = replace_owned_socket(native,trace,session,owner)
            elif args.case == 'wrong-table': result['actualTableRefusal'] = wrong_table_refusal(witness,watched)
            elif args.case in ('output-reader-failure','debug-reader-failure','output-reader-deadline'):
                witness.reader_armed = True
                native.control_send(outer,{'kind':'pulse-fd'} if args.case == 'output-reader-deadline' else {'kind':'pulse'},clock)
            elif args.case == 'fatal-die':
                assert witness.blocked_hold and witness.blocked_hold['context'] is watched
                assert native.proc_identity(trace.child.pid,trace.tracer) == trace.tracer
                limits = resource.prlimit(trace.child.pid,resource.RLIMIT_AS)
                size = int((Path('/proc')/str(trace.child.pid)/'statm').read_text().split()[0])*os.sysconf('SC_PAGE_SIZE')
                ceiling = size+4096
                if limits[0] != resource.RLIM_INFINITY: ceiling = min(ceiling,limits[0])
                resource.prlimit(trace.child.pid,resource.RLIMIT_AS,(ceiling,limits[1]))
                result['fatalAllocationPressure'] = {'tracerBirth':[trace.child.pid,trace.tracer['birth']],
                    'oldLimits':limits,'newSoftLimit':ceiling,'scope':'isolated own TEST tracer only'}
                native.control_send(outer,{'kind':'oom-read'},clock)
            elif args.case == 'diagnostic-eof':
                display = session.display_process
                assert native.proc_identity(display['child'].pid,display['identity']) == display['identity']
                display['child'].kill()
                path = str(root/'evidence/session-Xvfb.stderr')
                wait_for(lambda:any(row['path'] == path for row in witness.diagnostic_eofs),
                         time.monotonic()+clock.wait(.2),'actual original diagnostic-reader EOF')
                result['actualDiagnosticEOF'] = [row for row in witness.diagnostic_eofs if row['path'] == path]
                os.kill(session.monitor['child'].pid,signal.SIGCONT)
            elif args.case in ('delayed-controls','hello-refused','owner-exit','wrong-name','pid-control-eof','uid-control-eof'): os.kill(session.monitor['child'].pid,signal.SIGCONT)
            # credential-deadline leaves the real monitor process stopped;
            # the original dispatcher deadline must refuse, with no fallback.
        terminal = None; no_progress = time.monotonic()+clock.wait(2)
        progress = (len(witness.requests),trace.parser.events)
        while terminal is None and trace.error is None and owner.first is None:
            if args.case == 'delayed-controls': witness.resume_controls()
            left = min(clock.wait(.05),max(.001,no_progress-time.monotonic()))
            if select.select([outer],[],[],left)[0]:
                terminal = native.control_receive(outer,clock)
                if terminal == {'kind':'pulse-observed'}:
                    assert witness.reader_armed
                    result['actualControllerPulse'] = terminal
                    terminal = None
                    continue
                assert terminal['kind'] == 'terminal' and terminal['exit'] == 0
            current_progress = (len(witness.requests),trace.parser.events)
            if current_progress != progress:
                progress = current_progress; no_progress = min(clock.outer-8,time.monotonic()+2)
            assert time.monotonic() < no_progress, 'original pair terminal no-progress deadline'
        if expected or args.case in ('monitor-eof','diagnostic-race','final-ack-race','final-incarnation-drift','control-eof','credential-deadline','owner-exit','wrong-socket','fatal-die','diagnostic-eof'):
            if args.case in ('auth-absent','auth-refused','hello-absent','connect-refused','last-table-exit'):
                watched = witness.snapshot_context()
                if args.case != 'last-table-exit': assert watched is not None, 'actual pre-close credential hold not reached'
            result['refusal'] = guard_refusal_witness(witness,None if args.case == 'owner-exit' else watched,root,time.monotonic()+clock.wait(1))
            if expected: assert expected in session.error, 'different failure cannot prove the required guard'
            if args.case == 'final-incarnation-drift':
                first = session.first_error
                assert type(first) is AssertionError and str(first) in ('tracked bus socket is not freshly connected','active monitored name incarnation')
                origins = exact_failure_origin(first,{'PassiveStrace.complete_held':native.PassiveStrace.complete_held,
                    'StdinAliasTrace.bus_fd':native.StdinAliasTrace.bus_fd})
                assert any(row['function'] == 'PassiveStrace.complete_held' for row in origins), 'earlier incarnation failure cannot prove final revalidation'
                result['actualFinalIncarnationDrift']['originalFinalFailureOrigin'] = origins
                result['actualFinalIncarnationDrift']['originalFirstError'] = str(first)
            if args.case == 'control-eof':
                result['actualOriginalControlEOF'] = control_eof_witness(witness,watched,time.monotonic()+clock.wait(.2))
            if args.case == 'connect-refused':
                assert witness.connect_failure and 'ENOENT' in witness.connect_failure
                conn = watched[-1]
                assert conn['outcome'] == witness.connect_failure and not conn['connected'] and not conn['acked']
                result['actualConnectRefusal'] = witness.connect_failure
            if args.case == 'hello-refused':
                result['actualHelloRefusal'] = failed_hello_witness(session,root)
            if args.case == 'wrong-birth':
                assert witness.birth_refusal and witness.birth_refusal.get('actualOriginalGuard') == 'held task birth'
                result['actualBirthRefusal'] = witness.birth_refusal
            if args.case == 'wrong-name':
                actor = actor_evidence(root,'wrong-name')
                spoofed = [m for m in witness.messages_seen if m['type'] == 4 and m['header'].get(3) == 'NameOwnerChanged' and
                           m['header'].get(2) == 'org.freedesktop.DBus' and m['header'].get(7) == actor['name']]
                assert len(spoofed) == 1 and spoofed[0]['body'] == [actor['name'],actor['name'],'']
                assert actor['name'] in session.active_names and spoofed[0] not in session.messages
                result['actualNameAuthorityRefusal'] = spoofed[0]
            if args.case in ('output-reader-failure','debug-reader-failure','output-reader-deadline'):
                assert witness.reader_fault and witness.reader_fault['actualOriginalReadReturned']
                result['actualOriginalReaderFault'] = {k:v for k,v in witness.reader_fault.items() if k != 'release'}
            if args.case == 'fatal-die':
                wait_for(lambda:b'Out of memory' in witness.debug_bytes,time.monotonic()+clock.wait(.2),
                         'genuine C fatal allocator diagnostic',lambda:owner.first)
                assert b'cleanup with unresolved observer custody' in witness.debug_bytes
                assert trace.child.poll() is None and native.proc_identity(trace.child.pid,trace.tracer) == trace.tracer
                result['actualFatalDie'] = {'diagnostic':'Out of memory','cleanupGuardRefusedChannel':True,
                                          'tracerRetainedWithOutstandingCustody':True}
            if args.case in ('pid-control-eof','uid-control-eof'):
                assert len(witness.control_eofs) == 1
                control, = witness.control_eofs
                assert control['joined'] and control['stdout'] == '' and control['exit'] == -signal.SIGKILL
                result['actualOriginalControlEOF'] = control
            if args.case == 'diagnostic-eof':
                assert result['actualDiagnosticEOF'] and all(row['originalEOF'] and row['closed'] for row in result['actualDiagnosticEOF'])
                assert any(text in session.error for text in ('session process not live','Notifications owner loss/replacement',
                           'owned standard session process exited','standard session control failed','No such file'))
            if args.case == 'owner-exit':
                result['ownerExitCustody'] = owner_exit_witness(witness)
                # The next destructive entry cannot use the exited owner as
                # fresh peer authority. A generic timeout cannot prove this.
                assert any(text in session.error for text in ('actual argv budget','fresh selected ACK identity','live trace task required','trace task metadata race'))
                assert watched[-1]['acked'], 'owner exit needs earlier original credential ACK'
                result['refusal'] = guard_refusal_witness(witness,None,root,time.monotonic()+clock.wait(1))
            if args.case == 'last-table-exit': result['lastTableExit'] = last_table_witness(witness,root)
            owner.fail(session.first_error)
            result['kernelOutcome'] = 'EXPECTED_ORIGINAL_REFUSAL'
        else:
            assert trace.error is None and session.error is None and owner.first is None
            wait_for(lambda:all(t.get('exited') for tid,t in trace.parser.tasks.items() if tid != controller['pid']),
                     time.monotonic()+clock.wait(1),'all actual terminal records',lambda:trace.error)
            with trace.parser.lock:
                captures = list(trace.parser.selected)
                if mode not in ('fork','thread','vfork-exit','clone-orders'):
                    assert len(captures) == 1
                    capture = captures[0]
                    assert capture['dupSeen'] and capture['eof'] and capture['exited'] and capture['reading'] is None
                    assert bytes(capture['bytes']) == b'TEST_ORIGINAL_STDIN\n'
                    assert any('read(' in event['syscall'] or '<... read resumed>' in event['syscall'] for event in capture['events'])
                    result['actualOriginalExecAdmission'] = selected_exec_witness(trace,witness,capture,mode)
                    if helper_mode != 'stdin':
                        receipts = capture.get('busLifetimeReceipts',[]); assert receipts
                        assert all(session.roundtrip_present(c) for receipt in receipts for c in receipt['controls'])
                        names = {r['name'] for r in receipts}; assert len(names) == 1
                        wait_for(lambda:not names & session.active_names,time.monotonic()+clock.wait(1),'actual name departure')
                        result['credentialControls'] = [c for r in receipts for c in r['controls']]
                        result['nameOwnershipDeparted'] = sorted(names)
                if args.case == 'exec-tid-remap': assert trace.parser.remap_history, 'actual exec TID remap not exercised'
                if args.case.startswith('shared-'): result['sharedTableDestruction'] = shared_table_witness(witness,root,args.case)
                if args.case == 'thread-exit': result['survivingOwnerThreadExit'] = surviving_thread_witness(witness,root)
            if args.case == 'delayed-controls':
                assert {r['method'] for r in witness.resumed_controls} == {
                    'org.freedesktop.DBus.GetConnectionUnixProcessID','org.freedesktop.DBus.GetConnectionUnixUser'}
                result['independentPausedOriginalControls'] = witness.resumed_controls
            result['actualSelectedCount'] = len(captures)
            if mode in ('fork','thread'):
                result['actualLeaderClone'] = leader_clone_witness(trace,witness,mode == 'thread')
            if mode in ('vfork-exit','vfork-exec'):
                result['vforkAdmissionPrecededActualParentReturn'] = strict_vfork_returns(trace,witness)
            result['cloneNotificationOrders'] = actual_clone_orders(witness)
            if args.case == 'clone-orders':
                assert {row['order'] for row in result['cloneNotificationOrders']} == {'parent-first','child-first'}, 'both genuine clone orders absent: qualification refused'
                assert len({tuple(row['childBirth']) for row in result['cloneNotificationOrders']}) >= 64
            trace.close(); session.close()
            native.control_send(outer,{'kind':'close'},clock)
            assert launcher.wait(timeout=clock.wait(2,cleanup=True)) == 0
            natural = True; result['kernelOutcome'] = 'EXPECTED_ORIGINAL_SUCCESS'
    except BaseException as error:
        witness.restore_thread_limit()
        owner.fail(error)
        if constructor_case(args.case):
            # These are constructor failure checks; success requires actual
            # original constructor allocation evidence and complete cleanup.
            obj = witness.trace if args.case.startswith('tracer-') else witness.session
            if obj is not None and constructor_fds is not None:
                actual_fds = descriptors()
                children = witness.original_children[constructor_stage]
                workers = getattr(obj,'threads',[])+getattr(obj,'held_workers',[]) if constructor_stage == 'trace' else obj.pumps
                original_joined = all(c.returncode is not None and not (Path('/proc')/str(c.pid)).exists() for c in children)
                original_closed = all(s.closed for c in children for s in (c.stdin,c.stdout,c.stderr) if s)
                task_rows = []
                if constructor_stage == 'trace' and hasattr(obj,'parser'):
                    keys = set(getattr(obj,'custody_keys',())) | {task['key'] for task in obj.parser.tasks.values()} | set(obj.parser.remap_history)
                    for pid,birth in sorted(keys):
                        try: fresh = stat_birth(pid)
                        except FileNotFoundError: fresh = None
                        task_rows.append({'pid':pid,'birth':birth,'pidAbsent':fresh is None,
                                          'freshBirth':fresh,'originalBirthAbsent':fresh != birth})
                notes = list(getattr(error,'__notes__',()))
                unresolved_notes = [note for note in notes if 'unproved' in note]
                result['originalConstructorBeforeOuterCleanup'] = {
                    'stage':constructor_stage,'descriptorDelta':sorted(actual_fds-constructor_fds),
                    'descriptorLoss':sorted(constructor_fds-actual_fds),'allOriginalChildrenJoined':original_joined,
                    'allOriginalChildStreamsClosed':original_closed,'allOriginalWorkersJoined':all(not t.is_alive() for t in workers),
                    'firstFailurePublished':getattr(obj,'first_error',None) is error,
                    'ownedFDsRemaining':sorted(getattr(obj,'owned_fds',())),
                    'actualOriginalTaskAbsence':task_rows,'cleanupUnprovedNotes':unresolved_notes}
                fault_valid = constructor_fault_witness(args.case,witness,error,result)
                if (fault_valid and actual_fds == constructor_fds and original_joined and original_closed and
                    all(not t.is_alive() for t in workers) and getattr(obj,'first_error',None) is error and
                    not getattr(obj,'owned_fds',()) and not unresolved_notes and all(row['originalBirthAbsent'] for row in task_rows)):
                    result['kernelOutcome'] = 'EXPECTED_CONSTRUCTOR_REFUSAL'
                else:
                    result['kernelOutcome'] = 'RED_ORIGINAL_CONSTRUCTOR_CLEANUP'
                    if args.comparison == 'OLD' and fault_valid and (actual_fds != constructor_fds or
                        not original_joined or not original_closed or any(t.is_alive() for t in workers) or
                        any(not row['originalBirthAbsent'] for row in task_rows)):
                        result['kernelOutcome'] = 'OBSERVED_OLD_RED_BOUNDARY'
                        retain_old_constructor_resources(error,witness,owner)
            else: result['kernelOutcome'] = 'FAIL_BEFORE_REQUIRED_CONSTRUCTOR'
        elif args.comparison == 'OLD' and args.case in ('diagnostic-race','final-ack-race') and type(error) is AssertionError and str(error) == 'diagnostic failure published across final ACK validation lock':
            assert watched is not None and not watched[-1]['acked'] and watched[0] not in trace.pending_acks
            assert session.error is not None and 'FileExistsError' in session.error
            assert session.pumps and not session.pumps[-1].is_alive()
            result['kernelOutcome'] = 'OBSERVED_OLD_RED_BOUNDARY'
            result['oldDiagnosticPublication'] = {'actualOriginalDiagnosticFailure':session.error,
                'originalPumpJoinedWhileFinalValidationLockHeld':True,'watchedConnectionAcknowledged':False,
                'scope':'observable publication across the original final ACK validation lock'}
            retain_old_constructor_resources(error,witness,owner)
        else: result['kernelOutcome'] = 'FAIL'
    finally:
        witness.restore_thread_limit()
        witness.release_held_worker()
        try: result['cleanup'] = cleanup_pair(owner,witness,launcher,controller,natural,clock.outer+5)
        except BaseException as error:
            result['cleanup'] = {'complete':False,'error':type(error).__name__+':'+str(error)[:240]}
            owner.fail(error)
        sys.setprofile(old_profile); threading.setprofile(None)
        # Logs are genuine bytes observed at original consume methods. Missing
        # bytes never become generated syscall records or positive authority.
        (root/'evidence/original.trace').write_bytes(witness.trace_bytes)
        (root/'evidence/original.debug').write_bytes(witness.debug_bytes)
        (root/'evidence/original.monitor').write_bytes(witness.monitor_bytes)
        result['firstFailure'] = None if owner.first is None else type(owner.first).__name__+':'+str(owner.first)[:240]
        result['requests'] = [packet.decode('ascii') for packet in witness.requests]
        result['sourcePins'] = PINS
        result['comparison'] = args.comparison
        expected = result['kernelOutcome'].startswith('EXPECTED_') if args.comparison == 'NEW' else result['kernelOutcome'] == 'OBSERVED_OLD_RED_BOUNDARY'
        result['pass'] = expected and result['cleanup']['complete']
        result['unimplementedSource'] = UNIMPLEMENTED
        if not result['cleanup']['complete']: result['kernelOutcome'] = 'FAIL_UNRESOLVED_CLEANUP'
        atomic_json(root/'evidence/integrated-pair-receipt.json',result)
    return result


def traversable_by_test_uid(path):
    for parent in (path,*path.parents):
        info = parent.stat()
        bits = (info.st_mode >> 6) if info.st_uid == 1000 else (info.st_mode >> 3) if info.st_gid == 1000 else info.st_mode
        assert bits & 1, 'UID1000 cannot traverse original absolute owned layout: '+str(parent)


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--case',required=True,choices=sorted(CASES))
    for name in ('derived','helper','actor','build-bindings','scratch-parent','workspace'):
        parser.add_argument('--'+name,type=Path,required=True)
    for name in ('derived','helper','actor'): parser.add_argument('--'+name+'-sha256',required=True)
    parser.add_argument('--machine-id',required=True,help='exact separately approved original runner ID; original qualified_runner remains binding')
    parser.add_argument('--mount-child',type=Path)
    parser.add_argument('--comparison',choices=('NEW','OLD'),default='NEW')
    parser.add_argument('--original-source-root',type=Path,default=IMPLEMENTATION)
    return parser.parse_args()


def main():
    global native,PINS,ORIGINAL_ROOT
    args = arguments()
    ORIGINAL_ROOT = args.original_source_root.resolve(strict=True)
    PINS = OLD_PINS if args.comparison == 'OLD' else PINS
    for name,pin in PINS.items(): assert digest(ORIGINAL_ROOT/name) == pin, 'exact selected ORIGINAL source binding: '+name
    if args.comparison == 'OLD':
        assert constructor_case(args.case) or args.case in ('diagnostic-race','final-ack-race'), 'OLD binding covers constructor/publication boundaries only; no whole OLD claim'
    selected = importlib.util.spec_from_file_location('TEST_bound_original',ORIGINAL_ROOT/'native_case.py')
    native = importlib.util.module_from_spec(selected); selected.loader.exec_module(native)
    assert os.geteuid() == 0, 'external root TEST contract only'
    if args.mount_child:
        root = args.mount_child.resolve(strict=True)
        # unshare --mount --propagation private already created this namespace.
        assert os.readlink('/proc/self/ns/mnt') != (root/'parent-mnt').read_text().strip()
        mount = subprocess.Popen(['/usr/bin/mount','--bind',str(root/'tmp-display'),'/tmp'],
            env={'PATH':'/usr/bin:/bin','TMPDIR':str(root/'tmp')},stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        try:
            out,err = mount.communicate(timeout=2); assert mount.returncode == 0 and len(out)+len(err) <= 8192
        finally:
            if mount.poll() is None: mount.kill()
            mount.wait(timeout=2)
            for stream in (mount.stdout,mount.stderr): stream.close()
        print(json.dumps(run_pair(args,root)))
        return 0 if json.loads((root/'evidence/integrated-pair-receipt.json').read_text())['pass'] else 1
    return run_outer(args)


def own_outer_descendant(pid, child, birth):
    """Cleanup custody from actual kernel ancestry, never a saved PID alone."""
    seen = set()
    for _ in range(32):
        assert pid > 0 and pid not in seen, 'emergency ancestry cycle/unknown parent'
        seen.add(pid)
        raw = (Path('/proc')/str(pid)/'stat').read_text()
        tail = raw[raw.rindex(')')+2:].split()
        actual_birth,parent = int(tail[19]),int(tail[1])
        if pid == child.pid:
            assert actual_birth == birth and parent == os.getpid(), 'own mount launcher tuple drift'
            return
        assert actual_birth >= birth, 'foreign pre-existing emergency task'
        if parent == os.getpid():
            actual_children = (Path('/proc/self/task')/str(os.getpid())/'children').read_text().split()
            assert str(pid) in actual_children, 'not an actual own adopted child'
            assert stat_birth(pid) == actual_birth, 'adopted child birth drift'
            return
        assert stat_birth(pid) == actual_birth, 'emergency ancestry birth drift'
        pid = parent
    raise AssertionError('emergency ancestry depth cap')


def emergency_outer_join(root, child, birth, end):
    """External watchdog containment, never a positive kernel/cleanup proof.

    This dedicated TEST supervisor is a subreaper. Only its actual kernel-owned
    adopted children are candidates; birth/cwd/executable and original recorded
    namespace facts are revalidated. No PID file alone authorizes a signal.
    """
    errors = []; adopted = []; forced = False
    contained = supervisor_joined = False
    controller = None
    qualified = root/'evidence/controller-qualified.json'
    if qualified.exists():
        try:
            controller = json.loads(qualified.read_text())
            try: fresh = native.trace_task_identity(controller['pid'])
            except FileNotFoundError: fresh = None
            if fresh is not None:
                assert all(fresh[k] == controller[k] for k in ('pid','birth','tgid','uids','gids','groups','nspid','ns','uidMap','gidMap'))
                own_outer_descendant(controller['pid'],child,birth)
                assert native.trace_task_identity(controller['pid']) == fresh
                os.kill(controller['pid'],signal.SIGKILL); forced = True
            contained = True
        except BaseException as error: errors.append('PID1 containment:'+type(error).__name__)
    if child.poll() is None and (contained or not qualified.exists()):
        try: child.kill(); forced = True
        except BaseException as error: errors.append('mount child containment:'+type(error).__name__)
    try: child.wait(timeout=max(.001,end-time.monotonic()))
    except BaseException as error: errors.append('mount child join:'+type(error).__name__)
    # The namespace launcher is now either joined by the inner owner or an
    # actual adopted child. No later catch-all kill may bypass this gate.
    if contained and child.returncode is not None:
        try:
            supervisor = json.loads((root/'evidence/namespace-supervisor-qualified.json').read_text())
            joined_path = root/'evidence/namespace-supervisor-joined.json'
            if joined_path.exists():
                joined = json.loads(joined_path.read_text())
                assert joined['pid'] == supervisor['pid'] and joined['joined'] and joined['controller'] == controller
                assert not (Path('/proc')/str(supervisor['pid'])).exists(), 'joined supervisor still present'
            else:
                raw = (Path('/proc')/str(supervisor['pid'])/'stat').read_text()
                tail = raw[raw.rindex(')')+2:].split()
                assert int(tail[19]) == supervisor['birth'] and int(tail[1]) == os.getpid(), 'unqualified adopted namespace supervisor'
                while True:
                    pid,status = os.waitpid(supervisor['pid'],os.WNOHANG)
                    if pid == supervisor['pid']: break
                    assert time.monotonic() < end, 'namespace supervisor not joined'
                    time.sleep(min(.005,max(0,end-time.monotonic())))
            assert namespace_absent(controller) == [], 'namespace survives emergency supervisor join'
            supervisor_joined = True
        except BaseException as error: errors.append('namespace supervisor gate:'+type(error).__name__)
    kill_permitted = contained and supervisor_joined
    allowed = {str(Path(p).resolve()) for p in (sys.executable,'/usr/bin/unshare',str(Path(root/'TEST-actor')),
                                               str(Path(root/'TEST-helper')),str(Path(root/'TEST-helper-other')))}
    for name in ('strace','Xvfb','dunst','dbus-daemon','dbus-monitor','dbus-send','xauth','nsenter','setpriv','dpkg-query'):
        path = shutil.which(name)
        if path: allowed.add(str(Path(path).resolve()))
    # The reviewed derived tracer may have a non-stock path, bound below by
    # its exact current source build and SHA passed to this one invocation.
    bindings_path = root/'outer-bindings.json'
    if bindings_path.exists():
        declared = json.loads(bindings_path.read_text())
        tracer = Path(declared['derived']).resolve(strict=True)
        if digest(tracer) == declared['derivedSHA256']: allowed.add(str(tracer))
    children_path = Path('/proc/self/task')/str(os.getpid())/'children'
    while time.monotonic() < end:
        pids = [int(value) for value in children_path.read_text().split()]
        if not pids: break
        progress = False
        for pid in pids:
            try:
                raw = (Path('/proc')/str(pid)/'stat').read_text(); tail = raw[raw.rindex(')')+2:].split()
                actual_birth = int(tail[19])
                assert int(tail[1]) == os.getpid() and actual_birth >= birth, 'foreign pre-existing child'
                if tail[0] != 'Z':
                    if not kill_permitted:
                        # Preserve all unresolved adopted custody, including an
                        # unidentified tracer, rather than guessing a safe kill.
                        continue
                    facts = native.proc_identity(pid)
                    assert facts['exe'] in allowed and facts['ppid'] == os.getpid() and facts['birth'] == actual_birth
                    assert facts['uids'] in ([0]*4,[1000]*4)
                    assert stat_birth(pid) == actual_birth
                    os.kill(pid,signal.SIGKILL); forced = True
                joined,status = os.waitpid(pid,os.WNOHANG)
                if joined:
                    adopted.append({'pid':pid,'birth':actual_birth,'joined':True,
                                    'pidAbsent':not (Path('/proc')/str(pid)).exists(),'status':status})
                    progress = True
            except (FileNotFoundError,ChildProcessError): progress = True
            except BaseException as error: errors.append('adopted join:'+type(error).__name__)
        if not progress: time.sleep(min(.005,max(0,end-time.monotonic())))
    remaining = children_path.read_text().split()
    return {'complete':not errors and not remaining and all(r['pidAbsent'] for r in adopted),
            'forcedContainment':forced,'adoptedChildren':adopted,'remainingChildren':remaining,'errors':errors,
            'tracerKillPermitted':kill_permitted,'tracerCustodyPreserved':bool(remaining) and not kill_permitted}


def run_outer(args):
    owner = Custody(); root = child = None; result = None; birth = None
    cleanup_errors = []; watchdog = False; emergency = None
    try:
        verify_bindings(args)
        parent = args.scratch_parent.resolve(strict=True); workspace = args.workspace.resolve(strict=True)
        assert workspace.is_relative_to('/srv/workers') and parent.is_relative_to(workspace)
        assert parent.stat().st_uid == os.geteuid() and not parent.stat().st_mode & 0o022
        root = Path(tempfile.mkdtemp(prefix='TEST-r1366-',dir=parent))
        # Every subsequent allocation and fallible layout qualification is
        # covered by this outer finally. The source worktree is untouched.
        os.chown(root,1000,1000); os.chmod(root,0o700)
        for name in ('home','cache','tmp','workspace','evidence','recipe','tmp-display'):
            (root/name).mkdir(mode=0o700); os.chown(root/name,1000,1000)
        (root/'home/.cursor').mkdir(mode=0o700); os.chown(root/'home/.cursor',1000,1000)
        os.chmod(root/'tmp-display',0o1777)
        for source,destination in ((ORIGINAL_ROOT/'native_case.py',root/'recipe/native_case.py'),
                                   (HERE/'matrix_controller.py',root/'recipe/matrix_controller.py'),
                                   (args.helper,root/'TEST-helper'),(args.helper,root/'TEST-helper-other'),
                                   (args.actor,root/'TEST-actor')):
            destination.write_bytes(source.read_bytes()); os.chmod(destination,0o755)
        (root/'TEST-selector').write_text('TEST selector fixture\n')
        (root/'parent-mnt').write_text(os.readlink('/proc/self/ns/mnt')+'\n')
        atomic_json(root/'outer-bindings.json',{'derived':str(args.derived.resolve()),'derivedSHA256':args.derived_sha256})
        traversable_by_test_uid(root)
        assert ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0) == 0, 'external TEST subreaper prerequisite'
        stdout = owner.stream((root/'evidence/outer.stdout').open('xb'))
        stderr = owner.stream((root/'evidence/outer.stderr').open('xb'))
        command = ['/usr/bin/unshare','--mount','--propagation','private',sys.executable,'-B','-S',str(Path(__file__).resolve()),*sys.argv[1:],'--mount-child',str(root)]
        child,record = owner.popen(command,start_new_session=True,stdout=stdout,stderr=stderr,
            cwd=root,env={'PATH':'/usr/bin:/bin','TMPDIR':str(root/'tmp'),'PYTHONDONTWRITEBYTECODE':'1'})
        birth = record['birth'] = native.startup_identity(child.pid)['birth']
        try: child.wait(timeout=118)
        except subprocess.TimeoutExpired: watchdog = True; raise
        receipt = root/'evidence/integrated-pair-receipt.json'
        result = json.loads(receipt.read_text()) if receipt.exists() else {'pass':False,'reason':'no inner cleanup receipt'}
    except BaseException as error: owner.fail(error)
    finally:
        if child is not None:
            # A failed/missing receipt invokes independent cleanup even if the
            # broken inner driver already exited. Outer cleanup cannot turn
            # missing original joins or a watchdog firing into PASS.
            if watchdog or result is None or not result.get('cleanup',{}).get('complete'):
                try: emergency = emergency_outer_join(root,child,birth or 0,time.monotonic()+4)
                except BaseException as error: cleanup_errors.append(type(error).__name__+':'+str(error)[:200])
            else:
                try:
                    if child.poll() is None: child.kill()
                    child.wait(timeout=2)
                except BaseException as error: cleanup_errors.append(type(error).__name__)
        for stream in owner.streams:
            try: stream.close()
            except BaseException as error: cleanup_errors.append(type(error).__name__)
    result = result or {'pass':False,'reason':str(owner.first)[:240]}
    result['outerJoined'] = child is not None and child.returncode is not None
    result['outerPIDAbsent'] = child is not None and not (Path('/proc')/str(child.pid)).exists()
    result['outerWatchdog'] = watchdog
    result['emergencyCleanup'] = emergency
    result['outerCleanupErrors'] = cleanup_errors
    result['pass'] = result['pass'] and result['outerJoined'] and result['outerPIDAbsent'] and owner.first is None and not cleanup_errors and not watchdog
    result['scratch'] = str(root) if root else None
    if root is not None: atomic_json(root/'outer-receipt.json',result)
    print(json.dumps(result))
    return 0 if result['pass'] else 1


if __name__ == '__main__': sys.exit(main())
