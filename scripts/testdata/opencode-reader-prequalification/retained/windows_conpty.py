#!/usr/bin/env python3
"""Native Windows 10 1809+ ConPTY acceptance for conservative Plain (TERM unset).

No downloads. Requires a verified Windows scanner; only disposable synthetic
profiles/packages and an empty client PATH. Linux syntax checks are not proof.
"""
import argparse
import ctypes
from ctypes import wintypes as W
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import base64
import subprocess
import sys
import tempfile
import threading
import time
import uuid

from harness import Fixture, check, clean, prepare_scanner, scanner_options


class COORD(ctypes.Structure):
    _fields_ = [('X', W.SHORT), ('Y', W.SHORT)]


class STARTUPINFO(ctypes.Structure):
    _fields_ = [('cb', W.DWORD), ('lpReserved', W.LPWSTR), ('lpDesktop', W.LPWSTR),
                ('lpTitle', W.LPWSTR), ('dwX', W.DWORD), ('dwY', W.DWORD),
                ('dwXSize', W.DWORD), ('dwYSize', W.DWORD), ('dwXCountChars', W.DWORD),
                ('dwYCountChars', W.DWORD), ('dwFillAttribute', W.DWORD),
                ('dwFlags', W.DWORD), ('wShowWindow', W.WORD), ('cbReserved2', W.WORD),
                ('lpReserved2', ctypes.POINTER(W.BYTE)), ('hStdInput', W.HANDLE),
                ('hStdOutput', W.HANDLE), ('hStdError', W.HANDLE)]


class STARTUPINFOEX(ctypes.Structure):
    _fields_ = [('StartupInfo', STARTUPINFO), ('lpAttributeList', W.LPVOID)]


class PROCESS_INFORMATION(ctypes.Structure):
    _fields_ = [('hProcess', W.HANDLE), ('hThread', W.HANDLE), ('dwProcessId', W.DWORD), ('dwThreadId', W.DWORD)]


class JOB_BASIC_LIMIT(ctypes.Structure):
    _fields_ = [('per_process_time', ctypes.c_int64), ('per_job_time', ctypes.c_int64),
                ('flags', W.DWORD), ('minimum_working_set', ctypes.c_size_t),
                ('maximum_working_set', ctypes.c_size_t), ('active_process_limit', W.DWORD),
                ('affinity', ctypes.c_size_t), ('priority', W.DWORD), ('scheduling', W.DWORD)]


class JOB_ACCOUNTING(ctypes.Structure):
    _fields_ = [('times', ctypes.c_int64 * 4), ('page_faults', W.DWORD),
                ('total_processes', W.DWORD), ('active_processes', W.DWORD), ('terminated', W.DWORD)]


class JOB_EXTENDED_LIMIT(ctypes.Structure):
    _fields_ = [('basic', JOB_BASIC_LIMIT), ('io_counters', ctypes.c_uint64 * 6),
                ('process_memory', ctypes.c_size_t), ('job_memory', ctypes.c_size_t),
                ('peak_process_memory', ctypes.c_size_t), ('peak_job_memory', ctypes.c_size_t)]


class ConPTY:
    def __init__(self, argv, env, cwd, timeout, evidence=None):
        self.k = ctypes.WinDLL('kernel32', use_last_error=True)
        signatures = {
            'CreateJobObjectW': ([W.LPVOID, W.LPCWSTR], W.HANDLE),
            'SetInformationJobObject': ([W.HANDLE, ctypes.c_int, W.LPVOID, W.DWORD], W.BOOL),
            'AssignProcessToJobObject': ([W.HANDLE, W.HANDLE], W.BOOL),
            'QueryInformationJobObject': ([W.HANDLE, ctypes.c_int, W.LPVOID, W.DWORD, W.LPVOID], W.BOOL),
            'TerminateJobObject': ([W.HANDLE, W.UINT], W.BOOL),
            'ResumeThread': ([W.HANDLE], W.DWORD),
            'CreatePipe': ([ctypes.POINTER(W.HANDLE), ctypes.POINTER(W.HANDLE), W.LPVOID, W.DWORD], W.BOOL),
            'CreatePseudoConsole': ([COORD, W.HANDLE, W.HANDLE, W.DWORD, ctypes.POINTER(W.HANDLE)], ctypes.c_long),
            'ResizePseudoConsole': ([W.HANDLE, COORD], ctypes.c_long),
            'ClosePseudoConsole': ([W.HANDLE], None),
            'InitializeProcThreadAttributeList': ([W.LPVOID, W.DWORD, W.DWORD, ctypes.POINTER(ctypes.c_size_t)], W.BOOL),
            'UpdateProcThreadAttribute': ([W.LPVOID, W.DWORD, ctypes.c_size_t, W.LPVOID, ctypes.c_size_t, W.LPVOID, W.LPVOID], W.BOOL),
            'DeleteProcThreadAttributeList': ([W.LPVOID], None),
            'CreateProcessW': ([W.LPCWSTR, W.LPWSTR, W.LPVOID, W.LPVOID, W.BOOL, W.DWORD, W.LPVOID, W.LPCWSTR, ctypes.POINTER(STARTUPINFOEX), ctypes.POINTER(PROCESS_INFORMATION)], W.BOOL),
            'ReadFile': ([W.HANDLE, W.LPVOID, W.DWORD, ctypes.POINTER(W.DWORD), W.LPVOID], W.BOOL),
            'WriteFile': ([W.HANDLE, W.LPCVOID, W.DWORD, ctypes.POINTER(W.DWORD), W.LPVOID], W.BOOL),
            'WaitForSingleObject': ([W.HANDLE, W.DWORD], W.DWORD),
            'GetExitCodeProcess': ([W.HANDLE, ctypes.POINTER(W.DWORD)], W.BOOL),
            'TerminateProcess': ([W.HANDLE, W.UINT], W.BOOL),
            'OpenProcess': ([W.DWORD, W.BOOL, W.DWORD], W.HANDLE),
            'CloseHandle': ([W.HANDLE], W.BOOL),
        }
        for name, (args, result) in signatures.items():
            function = getattr(self.k, name); function.argtypes = args; function.restype = result
        self.timeout, self.raw, self.error = timeout, bytearray(), None
        self.output_changed = threading.Condition()
        self.reader_done = False
        self.handles = []
        self.job = None
        self.job_assigned = False
        self.status_path = None
        self.hpc = W.HANDLE()
        self.pi = PROCESS_INFORMATION()
        self.reader = None
        self.closed = False
        self.forced = False
        attributes = None
        initialized = False
        try:
            self.job = self.k.CreateJobObjectW(None, None)
            self.ok(self.job)
            self.handles.append(self.job)
            limits = JOB_EXTENDED_LIMIT()
            limits.basic.flags = 0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
            self.ok(self.k.SetInformationJobObject(self.job, 9, ctypes.byref(limits), ctypes.sizeof(limits)))
            input_read, self.input = self.pipe()
            self.output, output_write = self.pipe()
            result = self.k.CreatePseudoConsole(COORD(100, 30), input_read, output_write, 0, ctypes.byref(self.hpc))
            check(result == 0, 'CreatePseudoConsole HRESULT=0x%08x' % (result & 0xffffffff))
            self.reader = threading.Thread(target=self.read, daemon=True)
            self.reader.start()
            size = ctypes.c_size_t()
            self.k.InitializeProcThreadAttributeList(None, 1, 0, ctypes.byref(size))
            attributes = ctypes.create_string_buffer(size.value)
            self.ok(self.k.InitializeProcThreadAttributeList(attributes, 1, 0, ctypes.byref(size)))
            initialized = True
            self.ok(self.k.UpdateProcThreadAttribute(attributes, 0, 0x00020016,
                                                     self.hpc, ctypes.sizeof(W.HANDLE), None, None))
            startup = STARTUPINFOEX()
            startup.StartupInfo.cb = ctypes.sizeof(startup)
            # Explicit NULL standard handles bind to this console, not runner stdio.
            startup.StartupInfo.dwFlags = 0x100  # STARTF_USESTDHANDLES
            startup.StartupInfo.hStdInput = None
            startup.StartupInfo.hStdOutput = None
            startup.StartupInfo.hStdError = None
            startup.lpAttributeList = ctypes.cast(attributes, W.LPVOID)
            command = ctypes.create_unicode_buffer(subprocess.list2cmdline(argv))
            environment = ctypes.create_unicode_buffer('\0'.join(k + '=' + v for k, v in sorted(env.items())) + '\0\0')
            self.ok(self.k.CreateProcessW(None, command, None, None, False, 0x80000 | 0x400 | 0x4,
                                          environment, str(cwd), ctypes.byref(startup), ctypes.byref(self.pi)))
            self.handles.extend([self.pi.hProcess, self.pi.hThread])
            # Assign while suspended so PowerShell/CLI descendants cannot escape
            # failure cleanup. Job termination is always recorded as forced.
            self.ok(self.k.AssignProcessToJobObject(self.job, self.pi.hProcess))
            self.job_assigned = True
            resumed = self.k.ResumeThread(self.pi.hThread)
            self.ok(resumed != 0xffffffff)
            for handle in (input_read, output_write):
                self.k.CloseHandle(handle); self.handles.remove(handle)
        except BaseException as exc:
            try:
                self.close()
            except Exception as cleanup_error:
                raise RuntimeError(repr(exc) + '; startup cleanup: ' + repr(cleanup_error)) from exc
            finally:
                if evidence:
                    (evidence / 'terminal.ansi').write_bytes(self.raw)
                    (evidence / 'startup-error.json').write_text(json.dumps(dict(
                        error=repr(exc), forced=self.forced, reader_error=self.error)), encoding='utf-8')
            raise
        finally:
            if initialized: self.k.DeleteProcThreadAttributeList(attributes)

    def ok(self, result):
        if not result: raise ctypes.WinError(ctypes.get_last_error())

    def pipe(self):
        read, write = W.HANDLE(), W.HANDLE()
        self.ok(self.k.CreatePipe(ctypes.byref(read), ctypes.byref(write), None, 0))
        self.handles.extend([read, write])
        return read, write

    def read(self):
        buf, size = ctypes.create_string_buffer(65536), W.DWORD()
        error = None
        try:
            while self.k.ReadFile(self.output, buf, len(buf), ctypes.byref(size), None):
                if not size.value: return
                with self.output_changed:
                    self.raw.extend(buf.raw[:size.value])
                    if len(self.raw) > 4 * 1024 * 1024:
                        error = 'capture exceeded 4 MiB'
                        self.error = error
                        return
                    self.output_changed.notify_all()
            code = ctypes.get_last_error()
            if code != 109 and not (self.closed and code in (6, 995)):
                error = 'ReadFile WinError ' + str(code)
        except Exception as exc:
            error = 'console reader failure: ' + repr(exc)
        finally:
            with self.output_changed:
                self.error = error
                self.reader_done = True
                self.output_changed.notify_all()

    def send(self, data):
        size = W.DWORD()
        self.ok(self.k.WriteFile(self.input, data, len(data), ctypes.byref(size), None))
        check(size.value == len(data), 'short console input write')

    def poll(self):
        status = self.k.WaitForSingleObject(self.pi.hProcess, 0)
        if status == 258: return None
        check(status == 0, 'process status wait failed')
        code = W.DWORD(); self.ok(self.k.GetExitCodeProcess(self.pi.hProcess, ctypes.byref(code)))
        return code.value

    def wait(self, marker, after=0, *, child_nonce=None, child_final=False):
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            code = self.poll()
            # Read status only after exit (or a protocol marker in the caller).
            # Windows CRT readers may deny deletion during atomic replacement.
            if code is not None and self.status_path and self.status_path.exists():
                state = json.loads(self.status_path.read_text(encoding='utf-8'))
                check('error' not in state, 'console owner native failure: ' + str(state.get('error')))
            # Process signaling does not imply that the independent pipe reader
            # has captured all output. Drain until a marker, reader completion,
            # or the original deadline; never renew the budget after exit.
            with self.output_changed:
                check(self.error is None, self.error)
                captured = clean(bytes(self.raw[after:]))
                # The owner saves completed child status before publishing this
                # nonce-specific marker, then waits for restoration-probe input.
                # It is still alive, so poll() alone cannot detect child failure.
                # Completion is session evidence, not consumable marker output:
                # a previous wait may have advanced past it in a coalesced read.
                transcript = clean(bytes(self.raw)) if child_nonce else ''
                if child_nonce and re.search(
                        r'(?m)^RESTORE_READY_' + re.escape(child_nonce) + r'\r?$', transcript):
                    state = json.loads(self.status_path.read_text(encoding='utf-8'))
                    check('error' not in state,
                          'console owner native failure: ' + str(state.get('error')))
                    # The owner atomically saves probe status before printing
                    # the nonce-bound marker. Earlier phases are not completion.
                    check(state.get('phase') in ('probe', 'done')
                          and type(state.get('exit')) is int
                          and state.get('nonce', child_nonce) == child_nonce,
                          'invalid completed child status: ' + repr(state))
                    assertions = '\n'.join(line for line in transcript.splitlines()
                                           if 'console resources did not settle:' in line
                                           or 'owned lifecycle:' in line)
                    check(child_final and state['exit'] == 0 and re.search(marker, captured),
                          'native console helper exited before ' + marker +
                          ': ' + repr(state) + '\n' + assertions[-2000:] + '\n' + transcript[-6000:])
                found = re.search(marker, captured)
                remaining = deadline - time.monotonic()
                if remaining <= 0: break
                if found: return len(self.raw)
                check(not self.reader_done,
                      'console output EOF (owner exit ' + str(code) + ') before marker: ' + marker +
                      '\n' + clean(bytes(self.raw))[-6000:])
                self.output_changed.wait(min(0.02, remaining))
        raise AssertionError('timeout waiting for ' + marker)

    def close(self, child_pid=None):
        if self.closed: return
        self.closed = True
        errors = []
        def attempt(fn):
            try: fn()
            except Exception as exc: errors.append(str(exc))
        def terminate(handle):
            if not self.k.TerminateProcess(handle, 97):
                # Owner may exit between poll and kill. Only a signaled handle
                # proves that this failure is harmless.
                check(self.k.WaitForSingleObject(handle, 2000) == 0, 'TerminateProcess failed')
            check(self.k.WaitForSingleObject(handle, 2000) == 0, 'process kill timeout')
        try:
            job = getattr(self, 'job', None) if getattr(self, 'job_assigned', True) else None
            if job:
                accounting = JOB_ACCOUNTING()
                self.ok(self.k.QueryInformationJobObject(job, 1, ctypes.byref(accounting),
                                                        ctypes.sizeof(accounting), None))
                if accounting.active_processes:
                    self.forced = True
                    attempt(lambda: self.ok(self.k.TerminateJobObject(job, 97)))
                    # Job termination is asynchronous. Do not race it with a
                    # second TerminateProcess call (ERROR_ACCESS_DENIED).
                    deadline = time.monotonic() + 2
                    while True:
                        self.ok(self.k.QueryInformationJobObject(job, 1, ctypes.byref(accounting),
                                                                ctypes.sizeof(accounting), None))
                        if not accounting.active_processes:
                            break
                        check(time.monotonic() < deadline, 'job termination timeout')
                        time.sleep(0.02)
            if self.pi.hProcess and self.poll() is None:
                self.forced = True
                if not job and child_pid:
                    child = self.k.OpenProcess(0x0001 | 0x00100000, False, child_pid)
                    if child:
                        try: attempt(lambda: terminate(child))
                        finally: attempt(lambda: self.ok(self.k.CloseHandle(child)))
                if job:
                    attempt(lambda: check(self.k.WaitForSingleObject(self.pi.hProcess, 2000) == 0,
                                          'job owner termination timeout'))
                else:
                    attempt(lambda: terminate(self.pi.hProcess))
        except Exception as exc:
            errors.append(str(exc))
        finally:
            if self.hpc:
                # Keep draining during bounded synchronous HPCON shutdown.
                closer = threading.Thread(target=lambda: attempt(lambda: self.k.ClosePseudoConsole(self.hpc)), daemon=True)
                closer.start(); closer.join(3)
                if closer.is_alive(): errors.append('ClosePseudoConsole timeout')
            for handle in self.handles:
                attempt(lambda handle=handle: self.ok(self.k.CloseHandle(handle)))
            if self.reader:
                attempt(lambda: self.reader.join(2))
                if self.reader.is_alive(): errors.append('console reader cleanup timeout')
        if self.error: errors.append(self.error)
        check(not errors, '; '.join(errors))


def finish_console(terminal, status_path, evidence, fixture=None):
    """Preserve the primary failure and record cleanup failures independently."""
    primary = sys.exc_info()[1]
    errors = []
    status = {}
    try:
        if status_path.exists():
            status = json.loads(status_path.read_text(encoding='utf-8'))
    except Exception as exc:
        errors.append('status read: ' + repr(exc))
    try:
        terminal.close(status.get('pid'))
    except Exception as exc:
        errors.append('console close: ' + repr(exc))
    (evidence / 'terminal.ansi').write_bytes(terminal.raw)
    (evidence / 'transcript.txt').write_text(clean(terminal.raw), encoding='utf-8')
    (evidence / 'cleanup.json').write_text(json.dumps(dict(
        forced=terminal.forced, reader_error=terminal.error,
        primary_error=repr(primary) if primary is not None else None,
        cleanup_errors=errors)), encoding='utf-8')
    if fixture is not None:
        (evidence / 'mutations.json').write_text(json.dumps(dict(
            before=fixture.before, after=fixture.mutations())), encoding='utf-8')
    if errors:
        detail = '; '.join(errors)
        if primary is not None:
            raise RuntimeError(repr(primary) + '; cleanup: ' + detail) from primary
        raise AssertionError(detail)


CASES = ('default-yes', 'no', 'yes-lifecycle', 'ctrl-c', 'confirm-ctrl-c', 'eof', 'resize')


def prepare_powershell_fixture(fixture):
    # 458b native evidence contains StartupProfileData-NonInteractive writes.
    # Keep shell startup/cache activity in a separate disposable profile. Never
    # rebaseline or exclude anything from the CLI's full mutation snapshot.
    fixture.unchanged()
    shell_home = fixture.root / 'powershell-home'
    shell_home.mkdir()
    shell_env = dict(fixture.env, PATHEXT='.EXE')
    for key, value in fixture.env.items():
        # Relocate every synthetic profile/config path, retaining its layout.
        if key in ('HOME', 'USERPROFILE'):
            shell_env[key] = str(shell_home)
        elif Path(value).is_relative_to(fixture.home):
            shell_env[key] = str(shell_home / Path(value).relative_to(fixture.home))
    shell_tmp = fixture.root / 'powershell-tmp'
    shell_tmp.mkdir()
    for key in ('TMP', 'TEMP', 'TMPDIR'):
        shell_env[key] = str(shell_tmp)
    return shell_env


def powershell_argv(shell, argv, nonce, cli_env):
    # ProcessStartInfo supplies a child-only environment: changing $env: in the
    # shell would let asynchronous shell cache writes reach the CLI HOME again.
    # No stream redirection: the CLI still inherits the owned ConPTY handles.
    quote = lambda value: "'" + value.replace("'", "''") + "'"
    command = "$ErrorActionPreference = 'Stop'; $start = [System.Diagnostics.ProcessStartInfo]::new(); "
    command += "$start.UseShellExecute = $false; $start.FileName = " + quote(argv[0]) + '; '
    command += "$start.Arguments = " + quote(subprocess.list2cmdline(argv[1:])) + '; '
    command += '$start.EnvironmentVariables.Clear(); '
    for key, value in sorted(cli_env.items()):
        # Encode values separately: PowerShell also treats curly quotes as
        # delimiters, even inside an ASCII single-quoted literal.
        encoded = base64.b64encode(value.encode('utf-16-le')).decode('ascii')
        command += ('$start.EnvironmentVariables[' + quote(key) + '] = '
                    "[System.Text.Encoding]::Unicode.GetString([System.Convert]::FromBase64String('"
                    + encoded + "')); ")
    command += 'Write-Output ' + quote('POWERSHELL_LAUNCH_' + nonce) + '; '
    command += ("$child = [System.Diagnostics.Process]::Start($start); "
                "if ($null -eq $child) { throw 'native CLI did not start' }; "
                "$child.WaitForExit(); exit $child.ExitCode")
    return [str(shell), '-NoLogo', '-NoProfile', '-NonInteractive', '-EncodedCommand',
            base64.b64encode(command.encode('utf-16-le')).decode('ascii')]


def run_case(name, args):
    evidence = args.artifacts / name
    evidence.mkdir()
    with tempfile.TemporaryDirectory(prefix='huh-conpty-é-') as root:
        fixture = Fixture(root, args.scanner_path)
        fixture.env.pop('TERM')
        nonce = uuid.uuid4().hex
        status_path = evidence / 'status.json'
        config = dict(argv=[str(args.binary), 'add', str(fixture.package)], cwd=str(fixture.project),
                      nonce=nonce, status=str(status_path))
        owner_env = fixture.env
        if args.powershell:
            owner_env = prepare_powershell_fixture(fixture)
            config['argv'] = powershell_argv(args.powershell, config['argv'], nonce, fixture.env)
        config_path = evidence / 'job.json'
        config_path.write_text(json.dumps(config), encoding='utf-8')
        terminal = ConPTY([sys.executable, '-I', str(Path(__file__).with_name('windows_job.py').resolve()),
                          str(config_path)], owner_env, fixture.project, args.timeout, evidence=evidence)
        terminal.status_path = status_path
        status = {}
        try:
            terminal.wait('OWNER_READY_' + nonce)
            if args.powershell:
                terminal.wait('POWERSHELL_LAUNCH_' + nonce)
            terminal.wait(r'Choose one or more by number or id, comma-separated \[Enter keeps defaults\]:')
            check('codex' in clean(terminal.raw).lower() and 'cursor' in clean(terminal.raw).lower(),
                  'expected Codex config and synthetic Cursor editor discovery')
            fixture.unchanged()
            if name == 'resize':
                check(terminal.k.ResizePseudoConsole(terminal.hpc, COORD(60, 15)) == 0, 'resize failed')
            if name in ('ctrl-c', 'eof'):
                terminal.send(b'\x03' if name == 'ctrl-c' else b'\x1a\r')
            else:
                offset = len(terminal.raw)
                terminal.send(b'2\r' if name == 'yes-lifecycle' else b'\r')
                terminal.wait(r'Apply this plan\? \[Y/n\]', after=offset)
                check('pty-synthetic' in clean(terminal.raw) and '1.0.0' in clean(terminal.raw), 'missing plan identity')
                fixture.unchanged()
                if name == 'yes-lifecycle':
                    terminal.send(b'y\r')
                    terminal.wait(r'Have you completed activation[^\r\n]*\[y/N\]')
                    fixture.installed(['cursor'])
                    offset = len(terminal.raw)
                    terminal.send(b'n\r')
                    terminal.wait('Activation remains unconfirmed', after=offset)
                    check('Have you completed required authentication' not in clean(terminal.raw[offset:]), 'No advanced to auth')
                else:
                    terminal.send(b'\x03' if name == 'confirm-ctrl-c' else (b'\r' if name == 'default-yes' else b'n\r'))
            terminal.wait('RESTORE_READY_' + nonce)
            status = json.loads(status_path.read_text(encoding='utf-8'))
            check(status['exit'] == (1 if name in ('ctrl-c', 'confirm-ctrl-c', 'eof') else 0), 'unexpected CLI exit: ' + str(status))
            check(status['owner_before'] == status['owner_after'], 'inherited console handles/modes/cursor changed')
            check(status['before'] == status['after'], 'inherited console modes changed')
            check(status['cursor_before'] and status['cursor_after'], 'console cursor was not restored')
            offset = len(terminal.raw)
            terminal.send(('line_' + nonce + '\r').encode())
            terminal.wait('RESTORE_OK_' + nonce, after=offset)
            deadline = time.monotonic() + args.timeout
            while terminal.poll() is None and time.monotonic() < deadline: time.sleep(0.02)
            check(terminal.poll() == 0, 'console owner did not exit cleanly')
            status = json.loads(status_path.read_text(encoding='utf-8'))
            check(status['line_read'] and status['probe_modes'] == status['before'], 'console reuse failed')
            check(status['owner_probe'] == status['owner_before'], 'console handles/modes/cursor changed on reuse')
            check('line_' + nonce in clean(terminal.raw[offset:]), 'kernel echo missing')
            if name == 'yes-lifecycle': fixture.installed(['cursor'])
            elif name == 'default-yes': fixture.installed(['codex', 'cursor'])
            else: fixture.unchanged()
        finally:
            finish_console(terminal, status_path, evidence, fixture)
        check(not terminal.forced, 'forced cleanup cannot qualify as a pass')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--artifacts', required=True, type=Path)
    parser.add_argument('--case', action='append', choices=CASES)
    parser.add_argument('--powershell', type=Path, help='absolute native PowerShell executable; no profile')
    parser.add_argument('--timeout', type=float, default=15)
    scanner_options(parser)
    args = parser.parse_args()
    if os.name != 'nt': parser.error('native Windows required; no simulated pass')
    if args.timeout <= 0: parser.error('timeout must be positive')
    args.binary = args.binary.resolve(strict=True)
    if args.powershell:
        args.powershell = args.powershell.resolve(strict=True)
        if not args.case: args.case = ['default-yes', 'ctrl-c']
    args.artifacts = args.artifacts.resolve()
    args.artifacts.mkdir(parents=True, exist_ok=False)
    scanner = prepare_scanner(args)
    check(args.scanner_path is not None, 'verified Windows scanner required')
    # Freeze a private executable before any run; never execute a changing build output.
    payload = args.binary.read_bytes()
    args.binary = args.artifacts / 'frozen-agentplugins.exe'
    args.binary.write_bytes(payload)
    results = []
    for name in args.case or CASES:
        try:
            run_case(name, args)
            result = dict(case=name, status='passed')
        except Exception as exc: result = dict(case=name, status='failed', error=repr(exc))
        results.append(result); print(json.dumps(result), flush=True)
        (args.artifacts / 'results.json').write_text(json.dumps(dict(platform=platform.platform(),
            python=platform.python_version(), binary_sha256=hashlib.sha256(payload).hexdigest(),
            scanner=scanner, mode='native ConPTY, TERM unset, Plain required', results=results), indent=2), encoding='utf-8')
    return int(any(r['status'] == 'failed' for r in results))


if __name__ == '__main__': sys.exit(main())
