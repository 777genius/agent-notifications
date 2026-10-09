"""TEST PID1: exact original local identity, one disposable actor at a time."""
import argparse
import array
import stat
import ctypes
import importlib.util
import json
import os
from pathlib import Path
import resource
import select
import socket
import subprocess
import time


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--root', type=Path, required=True)
    p.add_argument('--fd', type=int, required=True)
    p.add_argument('--actor', required=True)
    p.add_argument('--primary', required=True)
    p.add_argument('--selector', required=True)
    p.add_argument('--mode', required=True)
    p.add_argument('--deadline', type=float, required=True)
    args = p.parse_args()
    spec = importlib.util.spec_from_file_location('TEST_original', args.root/'recipe/native_case.py')
    native = importlib.util.module_from_spec(spec); spec.loader.exec_module(native)
    clock = native.CaseClock(args.deadline)
    peer = socket.socket(fileno=args.fd)
    child = None; stage = inherited = None
    extra_children = []; extra_streams = []; exported = None
    first = None
    try:
        assert os.geteuid() == 0 and os.getpid() == 1
        subprocess.run(['/usr/sbin/ip','link','set','lo','up'], check=True, timeout=clock.wait(2),
                       env={'PATH':'/usr/bin:/bin','TMPDIR':str(args.root/'tmp')})
        links = json.loads(subprocess.check_output(['/usr/sbin/ip','-j','link'], timeout=clock.wait(2)))
        assert [row['ifname'] for row in links] == ['lo']
        for flags in ([], ['-6']):
            assert json.loads(subprocess.check_output(['/usr/sbin/ip', *flags, '-j','route'], timeout=clock.wait(2))) == []
        os.setgroups([]); os.setgid(1000); os.setuid(1000); os.umask(0o077)
        resource.setrlimit(resource.RLIMIT_CORE, (0,0))
        assert ctypes.CDLL(None).prctl(36,1,0,0,0) == 0
        native.control_send(peer, {'kind':'controller','identity':native.proc_identity(1)}, clock)
        environment = native.control_receive(peer, clock)
        assert environment['kind'] == 'environment'
        env = {'PATH':'/usr/bin:/bin','LANG':'C.UTF-8','HOME':str(args.root/'home'),
               'TMPDIR':str(args.root/'tmp'),'XDG_CACHE_HOME':str(args.root/'cache'),
               'TEST_MATRIX_EVIDENCE':str(args.root/'evidence'),
               'TEST_OTHER_EXE':str(args.root/'TEST-helper-other'),
               'TEST_MATRIX_MODE':environment['helperMode'], **environment['services']}
        if environment.get('badHome'): env['HOME'] = str(args.root/'workspace')
        stage, inherited = socket.socketpair()
        env['TEST_MATRIX_STAGE_FD'] = str(inherited.fileno())
        child = subprocess.Popen([args.actor,args.primary,args.selector,args.mode,str(inherited.fileno())],
            stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,
            pass_fds=(inherited.fileno(),),start_new_session=True,env=env,
            cwd=args.root/'home/.cursor',bufsize=0)
        # Own Popen custody exists before readiness or identity qualification.
        inherited.close(); inherited = None
        native.control_send(peer, {'kind':'leader','identity':native.proc_identity(child.pid)},clock)
        go = native.control_receive(peer,clock)
        assert go == {'kind':'go'}
        child.stdin.write(b'TEST_ORIGINAL_STDIN\n'); child.stdin.close()
        # The socket is a stream: bound each read by the original absolute
        # clock and accumulate exactly the original actor readiness bytes.
        ready = bytearray()
        while len(ready) < len(b'LEADER\n'):
            stage.settimeout(clock.wait(1))
            part = stage.recv(len(b'LEADER\n')-len(ready))
            assert part, 'TEST actor readiness EOF'
            ready.extend(part)
        assert ready == b'LEADER\n', 'TEST actor readiness bytes'
        stage.settimeout(clock.wait(1))
        stage.sendall(b'G')
        while child.poll() is None:
            clock.wait(.05)
            if environment['helperMode'] == 'final-incarnation-drift' and exported is None and select.select([stage],[],[],0)[0]:
                data,ancillary,flags,_ = stage.recvmsg(256,socket.CMSG_SPACE(array.array('i').itemsize))
                assert data and not flags, 'TEST stage export EOF/truncation'
                for level,kind,payload in ancillary:
                    assert level == socket.SOL_SOCKET and kind == socket.SCM_RIGHTS and len(payload) == array.array('i').itemsize
                    fd = array.array('i'); fd.frombytes(payload)
                    exported = socket.socket(fileno=fd[0]); extra_streams.append(exported)
                    facts = os.fstat(exported.fileno())
                    assert stat.S_ISSOCK(facts.st_mode) and b'S' in data, 'actual exported TEST endpoint'
                    native.control_send(peer,{'kind':'exported-socket','socket':[facts.st_dev,facts.st_ino]},clock)
            if select.select([peer],[],[],.001)[0]:
                command = native.control_receive(peer,clock)
                if command == {'kind':'pulse'}:
                    native.control_send(peer,{'kind':'pulse-observed'},clock)
                elif command == {'kind':'shutdown-exported'}:
                    assert exported is not None and environment['helperMode'] == 'final-incarnation-drift'
                    before = os.fstat(exported.fileno())
                    exported.shutdown(socket.SHUT_RDWR)
                    assert os.fstat(exported.fileno()).st_ino == before.st_ino
                    native.control_send(peer,{'kind':'exported-shutdown','socket':[before.st_dev,before.st_ino]},clock)
                elif command == {'kind':'oom-read'}:
                    own, inherited_own = socket.socketpair()
                    extra_streams.extend((own,inherited_own))
                    fault = subprocess.Popen([args.actor,args.primary,args.selector,'oom-read',str(inherited_own.fileno())],
                        pass_fds=(inherited_own.fileno(),),stdin=subprocess.DEVNULL,
                        stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True,
                        cwd=args.root/'home/.cursor',env=env)
                    extra_children.append(fault)
                    inherited_own.close()
                    # Actual own allocation/readiness, not a supplied PID file.
                    own.settimeout(clock.wait(1))
                    ready = bytearray()
                    while len(ready) < len(b'LEADER\n'):
                        part = own.recv(len(b'LEADER\n')-len(ready)); assert part
                        ready.extend(part)
                    assert ready == b'LEADER\n'
                    own.sendall(b'G')
                else: raise AssertionError('unexpected TEST controller command')
            time.sleep(.005)
        native.control_send(peer, {'kind':'terminal','exit':child.returncode}, clock)
        assert native.control_receive(peer,clock) == {'kind':'close'}
    except BaseException as error:
        first = error
        raise
    finally:
        failures = []
        for owned in ([child] if child is not None else [])+extra_children:
            try:
                if owned.poll() is None: owned.kill()
                owned.wait(timeout=max(.001,args.deadline+2-time.monotonic()))
            except BaseException as error: failures.append(error)
            for stream in (owned.stdin,owned.stdout,owned.stderr):
                if stream:
                    try: stream.close()
                    except BaseException as error: failures.append(error)
        for stream in (stage,inherited,peer,*extra_streams):
            if stream:
                try: stream.close()
                except BaseException as error: failures.append(error)
        if failures and first is None: raise failures[0]


if __name__ == '__main__': main()
