"""One actual Linux boundary contract. Partial during R1353 implementation.

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

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('r1353_native', HERE/'native_case.py')
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
FIXTURE = r'''
#include <unistd.h>
#include <sys/prctl.h>
#include <sys/wait.h>
#include <stdlib.h>
#include <stdio.h>
int main(int argc, char **argv) {
    char go;
    if (prctl(PR_SET_PTRACER, PR_SET_PTRACER_ANY, 0, 0, 0)) return 10;
    puts("TEST_READY"); fflush(stdout);
    if (read(0, &go, 1) != 1) return 11;
    pid_t child = argv[1][0] == 'f' ? fork() : vfork();
    if (child < 0) return 12;
    if (!child) {
        if (write(1, "TEST_CHILD_RAN\n", 15) != 15) _exit(14);
        if (argv[1][0] == 'e') execl("/usr/bin/true", "true", NULL);
        _exit(0);
    }
    int status;
    if (waitpid(child, &status, 0) != child || status) return 13;
    return 0;
}
'''


def stopped(pid):
    raw = (pathlib.Path('/proc')/str(pid)/'stat').read_text()
    return raw[raw.rindex(')')+2:].split()[0] == 't'


def run(binary, fixture, scratch, mode='fork', refusal=None):
    """Inspect real requests, real /proc and original fresh guard functions."""
    root = subprocess.Popen([str(fixture),mode], stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, start_new_session=True)
    assert root.stdout.readline() == b'TEST_READY\n'
    original = native.proc_identity(root.pid)
    peer, inherited = socket.socketpair(socket.AF_UNIX, socket.SOCK_SEQPACKET)
    token = os.urandom(32).hex()
    fd, writer = os.pipe()
    argv = [str(binary),'-f','-xx','--decode-pids=pidns','-s','1048577',
            '-e','trace='+native.TRACE_SYSCALLS,'-o','/proc/self/fd/'+str(writer),
            '-p',str(root.pid)]
    env = {'PATH':'/usr/bin:/bin','LANG':'C','TMPDIR':str(scratch/'tmp')}
    if refusal != 'passive':
        env.update(TEST_R1353_FD=str(inherited.fileno()), TEST_R1353_INCARNATION=token)
    err = (scratch/(mode+'-'+str(refusal)+'.stderr')).open('wb')
    tracer = subprocess.Popen(argv, env=env, pass_fds=(writer,inherited.fileno()),
                              stderr=err, start_new_session=True)
    tracer_birth = native.startup_identity(tracer.pid)
    inherited.close(); os.close(writer)
    owned = {root.pid:original['birth']}
    trace = bytearray(); checkpoints = set(); requests = []; sequences = set()
    sent = {}; completed = []
    deadline = time.monotonic()+12
    started = refused = False
    try:
        while time.monotonic() < deadline:
            ready = select.select([fd,peer],[],[],max(0,deadline-time.monotonic()))[0]
            if fd in ready:
                raw = os.read(fd,65536)
                if raw:
                    trace.extend(raw)
                    checkpoints.update(int(v) for v in re.findall(rb'^# R1353 (\d+)\n',trace,re.M))
                elif tracer.poll() is not None: break
                if refusal == 'passive' and not started:
                    # Stock attachment is verified by fresh original /proc facts.
                    assert native.trace_task_identity(root.pid)['tracer'] == tracer.pid
                    root.stdin.write(b'G'); root.stdin.flush(); started = True
            if peer in ready and refusal != 'passive':
                packet = peer.recv(512)
                if not packet:
                    if refusal: refused = True
                    else:
                        assert not sent, 'EOF before actual restart receipts'
                        assert root.wait(timeout=1) == tracer.wait(timeout=1) == 0, 'EOF is not natural join proof'
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
                seq, phase, tid, birth, child, cbirth = int(fields[3]),fields[4],*map(int,fields[5:9])
                assert seq not in sequences and seq > 0; sequences.add(seq)
                while seq not in checkpoints:
                    assert time.monotonic() < deadline
                    assert select.select([fd],[],[],deadline-time.monotonic())[0]
                    raw = os.read(fd,65536); assert raw
                    trace.extend(raw)
                    checkpoints.update(int(v) for v in re.findall(rb'^# R1353 (\d+)\n',trace,re.M))
                facts = native.trace_task_identity(tid)
                assert facts['birth'] == birth and facts['tracer'] == tracer.pid and stopped(tid)
                for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                    assert facts[field] == original[field], field
                owned[tid] = birth
                if phase == 'clone':
                    cf = native.trace_task_identity(child)
                    assert cf['birth'] == cbirth and cf['tracer'] == tracer.pid and stopped(child)
                    assert cf['ppid'] == facts['tgid'] and cf['nspid'][-1] == child
                    for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                        assert cf[field] == facts[field], field
                    assert native.live_stdin_endpoint(child) == native.live_stdin_endpoint(tid)
                    owned[child] = cbirth
                    assert native.trace_task_identity(child) == cf
                assert native.trace_task_identity(tid) == facts
                requests.append({'phase':phase,'sequence':seq,'tid':tid,'birth':birth,
                                 'child':child,'childBirth':cbirth,'checkpointConsumed':True})
                if phase == 'clone' and refusal:
                    if refusal == 'invalid': peer.send(b'ACK '+packet.replace(token.encode(),b'0'*64,1))
                    elif refusal == 'eof': peer.shutdown(socket.SHUT_WR)
                    elif refusal == 'deadline': pass
                    else: raise AssertionError('unknown refusal case')
                    assert select.select([peer],[],[],6)[0], 'bounded refusal receipt'
                    assert peer.recv(512) == b'', 'refusal must close control channel'
                    assert stopped(tid) and stopped(child), 'no resume on refused custody'
                    assert not select.select([root.stdout],[],[],0)[0], 'child executed before refusal containment'
                    refused = True
                    break
                sent[packet] = requests[-1]
                assert peer.send(b'ACK '+packet) == len(packet)+4
            if root.poll() is not None and tracer.poll() is not None: break
        else: raise AssertionError('actual kernel contract deadline')
        if refusal == 'passive':
            assert root.wait(timeout=1) == 0 and tracer.wait(timeout=1) == 0
            matches = re.findall(rb'clone\(.*?\)\s+=\s+(\d+)',trace)
            assert matches, 'OLD must reach an actual successful short-lived clone'
            child = int(matches[0])
            try: native.trace_task_identity(child)
            except FileNotFoundError: pass
            else: raise AssertionError('OLD did not lose live custody')
            return {'case':'passive-old','result':'RED','actualChild':child,'reason':'fresh original proc guard refuses absent child'}
        assert refused == bool(refusal)
        if not refusal:
            assert root.wait(timeout=1) == 0 and tracer.wait(timeout=1) == 0
            assert not sent and set(completed) == sequences, 'every accepted stop needs actual restart receipt'
            assert sum(r['phase'] == 'clone' for r in requests) == 1
            assert root.stdout.read() == b'TEST_CHILD_RAN\n', 'actual child effect after accepted ACK'
        return {'case':mode+'-'+str(refusal),'result':'GREEN_BOUNDARY_ONLY','requests':requests,'kernelRestartReceipts':completed}
    finally:
        for pid,birth in reversed(list(owned.items())):
            try:
                fresh = native.trace_task_identity(pid)
                assert fresh['birth'] == birth, 'cleanup refuses PID reuse'
                assert pid == root.pid or fresh['tracer'] == tracer.pid, 'cleanup refuses foreign task'
                os.kill(pid,signal.SIGKILL)
            except (FileNotFoundError,ProcessLookupError): pass
        if tracer.poll() is None:
            native.startup_identity(tracer.pid,tracer_birth)
            os.kill(tracer.pid,signal.SIGKILL)  # Fatal owned tracee signals precede tracer containment.
        root.wait(timeout=2); tracer.wait(timeout=2)
        err.close(); os.close(fd); peer.close(); root.stdin.close(); root.stdout.close()
        assert all(not (pathlib.Path('/proc')/str(pid)).exists() for pid in owned), 'every owned process must join'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--derived',type=pathlib.Path,required=True)
    parser.add_argument('--derived-sha256',required=True)
    args = parser.parse_args()
    binary = args.derived.resolve(strict=True)
    assert binary.read_bytes()[:4] == b'\x7fELF' and hashlib.sha256(binary.read_bytes()).hexdigest() == args.derived_sha256, 'exact derived build pin'
    scratch = pathlib.Path(tempfile.mkdtemp(prefix='TEST-linux-r1360-',dir=HERE/'scratch'))
    (scratch/'tmp').mkdir()
    fixture = scratch/'TEST-held-child'
    source = scratch/'TEST-held-child.c'; source.write_text(FIXTURE)
    subprocess.run(['/usr/bin/gcc','-O2','-o',str(fixture),str(source)],check=True,
                   env={'PATH':'/usr/bin:/bin','TMPDIR':str(scratch/'tmp')},timeout=10)
    try:
        cases = [run('/usr/bin/strace',fixture,scratch,refusal='passive')]
        cases += [run(binary,fixture,scratch,mode) for mode in ('fork','vfork','exec')]
        cases += [run(binary,fixture,scratch,refusal=refusal) for refusal in ('invalid','eof','deadline')]
    except Exception as error:
        diagnostic = (scratch/'fork-passive.stderr').read_text()
        receipt = {'status':'NOT_RUN_RUNTIME_UNSUPPORTED' if 'Operation not permitted' in diagnostic else 'FAIL',
                   'wholeContract':'INCOMPLETE','error':type(error).__name__+':'+str(error),
                   'diagnostic':diagnostic,'oldRedProved':False,'newGreenProved':False,
                   'ownedProcessCleanup':'run finally completed root/tracer joins and recorded PID absence',
                   'nativeE':'NOT_RUN','scratch':str(scratch),'derivedSHA256':args.derived_sha256}
        (HERE/'linux-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
        raise
    receipt = {'status':'PARTIAL_BOUNDARY_PASS','wholeContract':'INCOMPLETE',
               'derivedSHA256':hashlib.sha256(binary.read_bytes()).hexdigest(),'scratch':str(scratch),
               'cases':cases,'ownedProcessCleanup':'all recorded births absent; root/tracer wait joined',
               'nativeE':'NOT_RUN','missing':['both clone notification orders witnessed','CLONE_THREAD',
               'native parser full UID1000 admission','exec TID remap','D-Bus lifetime closed cases',
               'reader failure','qualification','independent exact review']}
    (HERE/'linux-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
    print(json.dumps(receipt))


if __name__ == '__main__': main()
