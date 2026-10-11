"""Actual shell/observer PTY contracts in fresh TEST profiles; no agent execution.

Supply --binary from the candidate build. --evidence retains byte recordings,
resize events and lifecycle receipts for replay in an actual terminal emulator.
"""
import argparse
import codecs
import threading
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import struct
import subprocess
import tempfile
import termios
import time

ROOT = Path(__file__).resolve().parent.parent

FIXTURE = r'''#!/bin/bash
set -euo pipefail
source "$TEST_SOURCE"
install_cleanup_traps
_CONFIG_HELPER="$TEST_HELPER"
_UI_MODE=${TEST_UI:-auto}
_ACTIVITY_MODE=${TEST_ACTIVITY:-compact}
_ACTIVITY_LIMIT=${TEST_LIMIT:-0}
_PROGRESS_IDS=(claude codex gemini)
_PROGRESS_PHASES=(pending pending pending)
_PROGRESS_OUTCOMES=(not_started not_started not_started)
printf 'TEST PREAMBLE\n'
first() {
    progress_phase claude registration registration_committed
    progress_phase claude config registration_committed
    if [ "${TEST_FAULT:-}" = journal-full ]; then
        python3 -I -c 'import pathlib,sys; pathlib.Path(sys.argv[1]).write_bytes(b"x"*262145)' "$_PROGRESS_JOURNAL"
        progress_phase claude config registration_committed
    elif [ "${TEST_FAULT:-}" = append-failure ]; then
        chmod 400 "$_PROGRESS_JOURNAL"
        progress_phase claude config registration_committed
        [ "$_PROGRESS_JOURNAL_DISABLED" = true ] || return 91
    elif [ "${TEST_FAULT:-}" = malformed ]; then
        printf 'transition\tclaude\t\033[2JREJECTED_PRIVATE\trunning\n' >> "$_PROGRESS_JOURNAL"
    fi
    printf 'PRIVATE_SECRET --argv /private/path\n' 
    printf '%s\n' "$_PROGRESS_PID" > "$TEST_FIRST_PID"
    touch "$TEST_READY1"
    while [ ! -f "$TEST_RELEASE1" ]; do sleep 0.02; done
}
second() {
    if [ "${TEST_SHARED_PENDING:-0}" = 1 ]; then progress_phase gemini MCP registration_committed; fi
    if [ "${TEST_STATIC:-0}" = 1 ]; then progress_phase codex MCP activation_timeout
    else progress_phase codex MCP registration_committed; fi
    printf '%s\n' "$_PROGRESS_PID" > "$TEST_SECOND_PID"
    touch "$TEST_READY2"
    while [ ! -f "$TEST_RELEASE2" ]; do sleep 0.02; done
    if [ "${TEST_FAIL:-0}" = 1 ]; then
        progress_phase codex MCP "$TEST_ACTIVATION"
        return 7
    fi
    progress_phase codex complete completed
}
run_setup_stage 'Installing Claude notifications' first
progress_phase claude complete completed
printf 'TEST prompt [continue]: '
IFS= read -r answer
[ "$answer" = continue ] || exit 8
status=0
run_setup_stage 'Configuring agent notification tools' second || status=$?
progress_phase gemini MCP activation_not_attempted
report_progress "$status"
if [ "$status" -ne 0 ]; then
    printf 'Setup incomplete: TEST failure\n' >&2
    report_diagnostics
else
    printf 'TEST instructions: restart; delivery unverified\n'
    report_diagnostics
    bootstrap_promotion
fi
exit "$status"
'''


def assert_dead(pid):
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return
    raise AssertionError('Observer still alive: '+str(pid))


def run_case(args, name, size=(80, 24), ui='auto', activity='compact', limit=0,
             no_color=False, dumb=False, verbose=False, fail=False, cancel=False,
             resize=False, helper=None, terminal=True, owner_death=False, fault="", cancel_second=False, static=False, absent=False, activation="activation_timeout", shared_pending=False):
    lab = Path(tempfile.mkdtemp(prefix=name+'-TEST-', dir=args.evidence))
    home = lab/'TEST home'
    project = lab/'new TEST project'
    tmp = lab/'tmp'
    tools = lab/'tools'
    for folder in (home, project, tmp, tools):
        folder.mkdir(mode=0o700)
    for tool in ('bash sh env python3 node mktemp rm cat cp mv chmod mkdir uname tr wc '
                 'grep sed awk dirname basename sleep date stat touch').split():
        path = shutil.which(tool)
        if path:
            (tools/tool).symlink_to(path)
    source = lab/'bootstrap-functions.sh'
    source.write_text((ROOT/'bin/bootstrap.sh').read_text().replace('main "$@"\n', ''))
    script = lab/'fixture.sh'
    script.write_text(FIXTURE)
    env = {'PATH': str(tools), 'HOME': str(home), 'USERPROFILE': str(home),
           'CLAUDE_CONFIG_DIR': str(home/'claude'), 'CODEX_HOME': str(home/'codex'),
           'TMPDIR': str(tmp), 'TERM': 'dumb' if dumb else 'xterm-256color',
           'TEST_SOURCE': str(source), 'TEST_REAL_HELPER': str(args.binary), 'TEST_HELPER': '' if absent else str(helper or args.binary),
           'TEST_UI': ui, 'TEST_ACTIVITY': activity, 'TEST_LIMIT': str(limit),
           'TEST_SHARED_PENDING': str(int(shared_pending)), 'TEST_ACTIVATION': activation, 'TEST_STATIC': str(int(static)), 'TEST_FAIL': str(int(fail)), 'TEST_FAULT': fault, 'BOOTSTRAP_VERBOSE': str(int(verbose)),
           'BOOTSTRAP_DIAGNOSTIC_ROOT': str(lab/'diagnostics')}
    for suffix in ('FIRST_PID', 'SECOND_PID', 'READY1', 'READY2', 'RELEASE1', 'RELEASE2'):
        env['TEST_'+suffix] = str(lab/suffix)
    if no_color:
        env['NO_COLOR'] = '1'
    master, slave = pty.openpty() if terminal else (None, None)
    records = []
    def geometry(cols, rows):
        fcntl.ioctl(slave if slave is not None else master, termios.TIOCSWINSZ,
                    struct.pack('HHHH', rows, cols, 0, 0))
        records.append({'resize': [cols, rows]})
    if terminal:
        geometry(*size)
    process = subprocess.Popen([str(tools/'bash'), str(script)], cwd=project, env=env,
                               stdin=slave if terminal else subprocess.PIPE,
                               stderr=slave if terminal else subprocess.PIPE,
                               stdout=slave if terminal else subprocess.PIPE,
                               # Kill the installer inside a surviving terminal
                               # session. Killing the macOS PTY session leader
                               # revokes its slave, so restoration is best effort.
                               start_new_session=not owner_death)
    if slave is not None:
        os.close(slave); slave = None
    data = bytearray()
    pipes = [master] if terminal else [process.stderr.fileno()]
    started = time.monotonic()
    reader = None
    if terminal:
        # Continuously drain through slave closure. Polling only the owner PID
        # can lose final PTY bytes on macOS after its orphan observer exits.
        def read_terminal():
            decoder = codecs.getincrementaldecoder('utf-8')('replace')
            while True:
                try: chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno in (errno.EIO, errno.EBADF): break
                    raise
                if not chunk: break
                data.extend(chunk)
                records.append({'data': decoder.decode(chunk)})
        reader = threading.Thread(target=read_terminal, daemon=True)
        reader.start()
    def drain(wait=0.02):
        if terminal:
            time.sleep(wait)
            return
        if select.select(pipes, [], [], wait)[0]:
            try:
                chunk = os.read(pipes[0], 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                return
            data.extend(chunk)
            if terminal and chunk:
                records.append({'data': chunk.decode(errors='replace')})
    def until(check, description):
        deadline = time.monotonic()+5
        while not check():
            drain()
            assert process.poll() is None, (name, description, process.returncode, data)
            assert time.monotonic()<deadline, (name, description, data)
        # Collect a live frame after a producer-side barrier.
        end = time.monotonic()+0.16
        while time.monotonic()<end:
            drain()
    try:
        until(lambda: (lab/'READY1').exists(), 'first stage readiness')
        if terminal:
            records.append({'checkpoint': 'first-live'})
        first_pid = int((lab/'FIRST_PID').read_text())
        assert b'PRIVATE_SECRET' not in data
        if cancel or owner_death:
            if owner_death:
                os.kill(process.pid, signal.SIGKILL)
            else:
                os.killpg(process.pid, signal.SIGTERM)
        else:
            pause_requested=time.monotonic()
            (lab/'RELEASE1').touch()
            # The owner prompt is on stdout; reading it ensures join happened.
            out = bytearray()
            deadline = time.monotonic()+3
            while b'TEST prompt [continue]:' not in (data if terminal else out):
                drain()
                if not terminal and select.select([process.stdout], [], [], 0.02)[0]:
                    out.extend(os.read(process.stdout.fileno(), 65536))
                assert time.monotonic()<deadline, (name, 'prompt handoff', out, data)
            assert_dead(first_pid)
            pause_elapsed=time.monotonic()-pause_requested
            assert pause_elapsed<2.25, (name,'pause join exceeded bound',pause_elapsed)
            if terminal:
                records.append({'checkpoint': 'prompt'})
                os.write(master, b'continue\n')
            else:
                process.stdin.write(b'continue\n'); process.stdin.flush()
            until(lambda: (lab/'READY2').exists(), 'second stage readiness')
            second_value=(lab/'SECOND_PID').read_text().strip()
            second_pid = int(second_value) if second_value else None
            if terminal:
                records.append({'checkpoint': 'second-live'})
            if resize:
                for cols, rows in ((50, 12), (108, 30), (32, 10)):
                    geometry(cols, rows)
                    end = time.monotonic()+0.25
                    while time.monotonic()<end:
                        drain()
                    records.append({'checkpoint': 'resized-'+str(cols)})
            if cancel_second: os.killpg(process.pid, signal.SIGTERM)
            else: (lab/'RELEASE2').touch()
        deadline = time.monotonic()+5
        while process.poll() is None:
            drain()
            assert time.monotonic()<deadline, (name, 'observer join deadline', data)
        for _ in range(4):
            drain(0.01)
        if reader is not None:
            reader.join(timeout=1)
            assert not reader.is_alive(), (name, 'observer still owns terminal')
        tail, stderr = process.communicate(timeout=1)
        output = bytes(data) if terminal else (bytes(out) if not (cancel or owner_death) else b'')+(tail or b'')
        if stderr:
            data.extend(stderr)
        if terminal:
            records.append({'checkpoint': 'final'})
        expected = -9 if owner_death else 143 if cancel or cancel_second else 7 if fail else 0
        assert process.returncode == expected, (name, process.returncode, output, data)
        deadline=time.monotonic()+1
        while True:
            try:
                assert_dead(first_pid)
                break
            except AssertionError:
                assert time.monotonic()<deadline
                time.sleep(0.02)
        if not (cancel or owner_death) and second_pid is not None:
            assert_dead(second_pid)
        (lab/'events.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in records))
        (lab/'stderr.txt').write_bytes(data)
        (lab/'stdout.txt').write_bytes(output)
        assert b'UNEXPECTED_V1_ARG' not in output+data
        assert b'REJECTED_PRIVATE' not in output+data
        assert b'PRIVATE_SECRET' not in output+data, (name, 'private input leaked')
        if terminal and ui != 'plain' and not (no_color or dumb or verbose or absent):
            assert b'\x1b[?25h' in data, (name, 'cursor not restored', first_pid, bytes(data))
        elif not absent:
            assert b'\x1b' not in data+output, (name, 'plain got terminal controls')
        banners=output.count(b'Boost your productivity')
        assert banners == int(not (fail or cancel or cancel_second or owner_death)), (name, banners, output, data)
        if banners:
            assert output.index(b'TEST instructions')<output.index(b'Boost your productivity')
            assert output.rstrip().endswith(b'----------------------------------------')
        if fail:
            expected_words={'activation_timeout':(b'Timed out',b'activation timed out'),'activation_canceled':(b'Canceled',b'activation canceled'),'activation_deadline':(b'Deadline reached',b'deadline reached')}[activation]
            assert any(word in data for word in expected_words)
            assert b'Not started' in data or b'activation not started' in data
            assert b'Installed' in data or b'complete - done' in data
        if not owner_death:
            assert not list(tmp.iterdir()), (name, 'owned temporary files leaked', list(tmp.iterdir()))
            logs=list((lab/'diagnostics').glob('installer-*.log'))
            assert len(logs)==1
            assert logs[0].stat().st_mode & 0o777 == 0o600
            assert b'PRIVATE_SECRET' in logs[0].read_bytes()
        if cancel_second:
            assert b'Installed; restart required' in data or b'completed; restart required' in data
            assert b'Registration saved; follow-up pending' in data or b'registration saved; follow-up incomplete' in data
            assert b'Canceled' not in data and b'activation canceled' not in data
            if not shared_pending: assert b'Not started' in data or b'not started' in data
        if fault:
            # A disabled session must use final owner text, never the old rich
            # renderer that ignores the new geometry contract.
            assert b'Claude Code  completed; restart required' in data
        if activity=='off':
            assert b'Recent activity' not in data
        if activity=='expanded' and not (cancel or cancel_second or owner_death):
            assert b'Session activity (safe checkpoints)' in data
        (lab/'events.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in records))
        (lab/'stderr.txt').write_bytes(data)
        (lab/'stdout.txt').write_bytes(output)
        receipt={'case':name,'status':process.returncode,'size':size,'duration':round(time.monotonic()-started,3),
                 'first_observer_pid':first_pid,'second_observer_pid':None if cancel or owner_death else second_pid,
                 'pause_join_seconds':None if cancel or owner_death else round(pause_elapsed,3),
                 'banner_count':banners,'recording':str(lab/'events.jsonl')}
        (lab/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
        print('PASS '+name,flush=True)
        return receipt
    finally:
        if process.poll() is None:
            if owner_death: process.kill()
            else: os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=2)
        if master is not None:
            os.close(master)
        if reader is not None:
            reader.join(timeout=1)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args=parser.parse_args()
    args.binary=args.binary.resolve(strict=True)
    args.evidence=args.evidence.resolve()
    args.evidence.mkdir(parents=True,exist_ok=True,mode=0o700)
    hung = args.evidence/'hung-observer-fixture'
    hung.write_text('#!'+shutil.which('python3')+'\n'+r"""import os, signal, sys, time
if sys.argv[2:] == ['--capabilities']: print('installer-progress-v1'); sys.exit(0)
if sys.argv[2:] == ['--session-capabilities']: print('installer-progress-session-v1'); sys.exit(0)
if sys.argv[2:] == ['--promotion']: os.execv(os.environ['TEST_REAL_HELPER'], [os.environ['TEST_REAL_HELPER']]+sys.argv[1:])
signal.signal(signal.SIGTERM, signal.SIG_IGN)
if os.environ.get('TEST_FAULT')=='stopped': os.kill(os.getpid(),signal.SIGSTOP)
while True: time.sleep(.1)
""")
    hung.chmod(0o700)
    old=args.evidence/'strict-v1-observer-fixture'
    old.write_text('#!'+shutil.which('python3')+'\n'+r"""import os,sys
args=sys.argv[2:]
if args == ['--session-capabilities']: sys.exit(2)
if args == ['--capabilities']: print('installer-progress-v1'); sys.exit(0)
allowed={'--events','--ui','--owner-pid','--transient'}
i=0
while i<len(args):
 if args[i] not in allowed: print('UNEXPECTED_V1_ARG', file=sys.stderr); sys.exit(92)
 i+=1 if args[i]=='--transient' else 2
os.execv(os.environ['TEST_REAL_HELPER'],[os.environ['TEST_REAL_HELPER']]+sys.argv[1:])
""")
    old.chmod(0o700)
    receipts=[]
    for name,options in (
        ('normal',{}),('narrow50',{'size':(50,12)}),('narrow32',{'size':(32,10)}),
        ('wide',{'size':(108,30)}),('resize',{'resize':True}),('resize-static',{'resize':True,'static':True}),
        ('expanded',{'activity':'expanded'}),('off',{'activity':'off'}),
        ('count1',{'limit':1}),('count16',{'limit':16}),
        ('plain',{'ui':'plain'}),('plain-off',{'ui':'plain','activity':'off'}),('no-color',{'no_color':True}),
        ('dumb',{'dumb':True}),('verbose',{'verbose':True}),('redirected',{'terminal':False}),
        ('absent-helper',{'absent':True}),('strict-v1',{'helper':old}),('hung-observer',{'helper':hung,'fault':'hung'}),('stopped-observer',{'helper':hung,'fault':'stopped'}),('append-failure',{'fault':'append-failure'}),('journal-full',{'fault':'journal-full'}),('malformed',{'fault':'malformed'}),
        ('partial',{'fail':True}),('typed-canceled',{'fail':True,'activation':'activation_canceled'}),('typed-deadline',{'fail':True,'activation':'activation_deadline'}),('cancel',{'cancel':True}),('cancel-second',{'cancel_second':True}),('cancel-shared-pending',{'cancel_second':True,'shared_pending':True}),('owner-death',{'owner_death':True}),
    ):
        receipts.append(run_case(args,name,**options))
    (args.evidence/'pty-receipts.json').write_text(json.dumps(receipts,indent=2)+'\n')

if __name__=='__main__':
    main()
