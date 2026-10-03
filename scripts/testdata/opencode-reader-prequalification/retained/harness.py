#!/usr/bin/env python3
"""Real Unix PTY evidence; Python 3.10+ stdlib, no downloads or agent runtimes.

Build (from cli):
  GOTOOLCHAIN=local GOMODCACHE=/tmp/uap-go-modcache GOCACHE=/tmp/uap-go-buildcache \
    /tmp/uap-go-toolchain/go/bin/go build -o /tmp/agentplugins-ui ./cmd/agentplugins
Run baseline fixture proof:
  python3 scripts/terminal-ui/harness.py --binary /tmp/agentplugins-ui \
    --case detection --artifacts /tmp/agentplugins-pty-baseline
Run integrated acceptance (failures are failures, never automatic retries):
  python3 scripts/terminal-ui/harness.py --binary /tmp/agentplugins-ui \
    --artifacts /tmp/agentplugins-pty-integrated
Tests: python3 -m unittest discover -s scripts/terminal-ui -p 'test_*.py'
See README.md for assertions, limitations and native Windows instructions.
"""
import argparse
import codecs
import hashlib
import html
import json
import os
from pathlib import Path
import platform
import re
import select
import signal
import socket
import shutil
import tarfile
import zipfile
import uuid
import struct
import subprocess
import sys
import tempfile
import time

if os.name == 'posix':
    import fcntl
    import pty
    import termios

ANSI = re.compile(r'\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[=>]')
SELECT = r'(?i)(choose targets|select (?:the )?(?:clients|targets)|detected supported clients)'
CONFIRM = r'(?i)(install[^\r\n]*\?|apply[^\r\n]*\?|proceed[^\r\n]*\?|confirm installation)'
LIFECYCLE = r'Have you completed activation[^\r\n]*\[y/N\]'
EMPTY_SELECTION = r'(?i)(at least (?:one|1\b)|select one|cannot be empty|must select)'


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def clean(data):
    return ANSI.sub('', data.decode('utf-8', 'replace'))


def selection_choices(data):
    return list(dict.fromkeys(re.findall(
        r'\[[^\]]*\][^\r\n]*\(([a-z0-9_-]+)\)', clean(data))))


def selection_keys(data, selected):
    offered = selection_choices(data)
    check(offered, 'selection has no client rows')
    check(set(selected) <= set(offered), f'selected clients not offered: {set(selected) - set(offered)}')
    keys = bytearray()
    for index, target in enumerate(offered):
        if target not in selected:
            keys.extend(b' ')
        if index + 1 < len(offered):
            keys.extend(b'\x1b[B')
    keys.extend(b'\r')
    return bytes(keys)


def cancel_empty_selection(session, fixture, confirmation):
    # Include every byte from before the invalid submit through cancellation drain.
    offset = len(session.raw)
    session.send(selection_keys(session.raw, set()))
    session.wait(EMPTY_SELECTION,
                 'empty-validation', after=offset)
    fixture.unchanged()
    session.send(b'\x1b')
    session.finish(1)
    check(not re.search(confirmation, clean(session.raw[offset:])),
          'empty advanced to confirmation')
    fixture.unchanged()


def hashes(root):
    """Capture even empty directories and symlinks; never follow a symlink."""
    result = {}
    if root.exists():
        for p in sorted(root.rglob('*')):
            key = str(p.relative_to(root))
            if p.is_symlink():
                result[key] = 'link:' + os.readlink(p)
            elif p.is_file():
                result[key] = hashlib.sha256(p.read_bytes()).hexdigest()
            elif p.is_dir():
                result[key] = 'directory'
    return result


class Screen:
    """Small evidence renderer for common inline TUI CSI, not a VT conformance oracle.

    Raw bytes remain authoritative. Unknown sequences are recorded, not executed.
    """
    def __init__(self, rows=30, cols=100):
        self.rows, self.cols = rows, cols
        self.grid = [[' '] * cols for _ in range(rows)]
        self.row = self.col = 0
        self.pending = ''
        self.decoder = codecs.getincrementaldecoder('utf-8')('replace')
        self.unknown = set()

    def feed(self, data):
        text = self.pending + self.decoder.decode(data)
        self.pending = ''
        i = 0
        while i < len(text):
            c = text[i]
            if c == '\x1b':
                m = re.match(r'\x1b\[([0-?]*)([ -/]*)([@-~])', text[i:])
                if m:
                    params, _, op = m.groups()
                    nums = [int(n) if n.isdigit() else 0 for n in params.split(';')]
                    n = nums[0] or 1
                    if op == 'A': self.row = max(0, self.row - n)
                    elif op == 'B': self.row = min(self.rows - 1, self.row + n)
                    elif op == 'C': self.col = min(self.cols - 1, self.col + n)
                    elif op == 'D': self.col = max(0, self.col - n)
                    elif op in ('H', 'f'):
                        self.row = min(self.rows - 1, n - 1)
                        self.col = min(self.cols - 1, (nums[1] or 1) - 1) if len(nums) > 1 else 0
                    elif op == 'G': self.col = min(self.cols - 1, n - 1)
                    elif op == 'K':
                        start, end = (0, self.cols) if nums[0] == 2 else ((0, self.col + 1) if nums[0] == 1 else (self.col, self.cols))
                        self.grid[self.row][start:end] = [' '] * (end - start)
                    elif op == 'J':
                        if nums[0] in (2, 3): self.grid = [[' '] * self.cols for _ in range(self.rows)]
                        elif nums[0] == 0:
                            self.grid[self.row][self.col:] = [' '] * (self.cols - self.col)
                            for r in range(self.row + 1, self.rows): self.grid[r] = [' '] * self.cols
                    elif op not in ('m', 'h', 'l', 'n', 'c', 't'):
                        self.unknown.add(m.group())
                    i += len(m.group()); continue
                if text[i:].startswith('\x1b]'):
                    end = re.search(r'\x07|\x1b\\', text[i:])
                    if end: i += end.end(); continue
                if len(text) - i < 128:
                    self.pending = text[i:]; break
                self.unknown.add(text[i:i+2]); i += 2; continue
            if c == '\r': self.col = 0
            elif c == '\n':
                self.row += 1
                if self.row >= self.rows:
                    self.grid.pop(0); self.grid.append([' '] * self.cols); self.row -= 1
            elif c == '\b': self.col = max(0, self.col - 1)
            elif c >= ' ' and c != '\x7f':
                if self.col >= self.cols:
                    self.col = 0; self.row = min(self.rows - 1, self.row + 1)
                self.grid[self.row][self.col] = c; self.col += 1
            i += 1

    def snapshot(self):
        return '\n'.join(''.join(row).rstrip() for row in self.grid).rstrip() + '\n'


class Fixture:
    def __init__(self, root, scanner_binary=None, include_opencode=False):
        self.root = Path(root)
        self.home = self.root / 'home-é'
        self.project = self.root / 'project'
        self.package = self.root / 'package'
        self.bin = self.root / 'bin'
        self.data = self.root / 'installer'
        for p in (self.home, self.project, self.package, self.bin, self.data, self.root / 'tmp'):
            p.mkdir(parents=True)
        # Minimal standard package; no MCP URLs, hooks, commands, or executable content.
        (self.package / 'plugin.json').write_text(json.dumps({
            '$schema': 'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json',
            'name': 'pty-synthetic', 'version': '1.0.0', 'description': 'Synthetic terminal fixture',
        }))
        for client in ('codex', 'cursor'):
            (self.home / ('.' + client)).mkdir()
            if os.name == 'nt': continue  # desktop evidence below; no runtime on PATH
            stub = self.bin / client
            stub.write_text('#!/bin/sh\n'
                            'printf "%s\\n" "$0 $*" >> "$STUB_LOG"\n'
                            'if [ "$#" = 1 ] && [ "$1" = --version ]; then\n'
                            '  printf "synthetic 99.0.0\\n"; exit 0\n'
                            'fi\nexit 97\n')
            stub.chmod(0o700)
        # Deliberate allowlist: never copy parent environment or read its config.
        self.env = {'HOME': str(self.home), 'USERPROFILE': str(self.home),
                    'PATH': str(self.bin), 'TERM': 'xterm-256color',
                    'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
                    'AGENTPLUGINS_HOME': str(self.data),
                    'TMPDIR': str(self.root / 'tmp'), 'TMP': str(self.root / 'tmp'),
                    'TEMP': str(self.root / 'tmp'), 'STUB_LOG': str(self.root / 'stub.log'),
                    'HTTP_PROXY': 'http://127.0.0.1:9', 'HTTPS_PROXY': 'http://127.0.0.1:9',
                    'ALL_PROXY': 'http://127.0.0.1:9', 'NO_PROXY': ''}
        if os.name == 'nt':
            for key in ('SystemRoot', 'WINDIR'):
                self.env[key] = os.environ['SystemRoot']
        for key, path in {'XDG_CONFIG_HOME': 'config', 'XDG_DATA_HOME': 'data',
                          'XDG_CACHE_HOME': 'cache', 'XDG_STATE_HOME': 'state',
                          'APPDATA': 'appdata', 'LOCALAPPDATA': 'localappdata',
                          'CODEX_HOME': '.codex', 'CLAUDE_CONFIG_DIR': '.claude',
                          'CURSOR_CONFIG_DIR': '.cursor', 'GEMINI_CLI_HOME': '.gemini'}.items():
            self.env[key] = str(self.home / path)
        if include_opencode:
            (Path(self.env['XDG_CONFIG_HOME']) / 'opencode').mkdir(parents=True)
        if os.name == 'nt':
            # Cursor config alone is not editor evidence. Seed the existing
            # Windows desktop discovery path; inert TEST bytes are never run.
            editor = Path(self.env['LOCALAPPDATA']) / 'Programs/cursor/Cursor.exe'
            editor.parent.mkdir(parents=True)
            editor.write_bytes(b'synthetic TEST Cursor editor; never executable')
        # Existing executable-cache seam, only inside this disposable fixture.
        # This is a SYNTHETIC scanner protocol response, not security evidence.
        machine = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine().lower())
        check(machine is not None, 'unsupported fixture architecture')
        scanner = self.data / 'security' / 'lintai' / '0.1.3' / (platform.system().lower() + '-' + machine) / ('lintai.exe' if os.name == 'nt' else 'lintai')
        scanner.parent.mkdir(parents=True)
        report = {'schema_version': 1, 'tool': {'name': 'lintai', 'version': '0.1.3'},
                  'policy': {'id': 'agent-plugin-install', 'version': 2,
                             'presets': ['recommended', 'preview', 'threat-review', 'supply-chain', 'advisory']},
                  'stats': {'scanned_files': 1, 'skipped_files': 0},
                  'findings': [], 'diagnostics': [], 'runtime_errors': []}
        if scanner_binary:
            shutil.copyfile(scanner_binary, scanner)
        else:
            check(os.name == 'posix', 'Windows requires a verified real scanner')
            scanner.write_text("#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = scan-agent-plugin ] || exit 97\nprintf '%s\\n' '" + json.dumps(report) + "'\n")
        scanner.chmod(0o700)
        self.before = self.mutations()

    def mutations(self):
        # Installer caches/temp acquisition are allowed before consent. State,
        # managed packages, journals, user/client configs and project are not.
        return {'home': hashes(self.home), 'project': hashes(self.project),
                'managed': hashes(self.data / 'managed'),
                'operations': hashes(self.data / 'operations-v2'),
                'state': (self.data / 'state-v2.json').read_text(encoding='utf-8') if (self.data / 'state-v2.json').exists() else None}

    def unchanged(self):
        check(self.mutations() == self.before, 'client/project/state/journal mutated before consent')

    def installed(self, clients, active=()):
        state = self.data / 'state-v2.json'
        check(state.is_file(), 'no persisted installation state after Yes')
        body = json.loads(state.read_text(encoding='utf-8'))
        # Traverse schema objects, not output words or screenshots.
        found = set()
        def walk(v):
            if isinstance(v, dict):
                if 'client_id' in v: found.add(v['client_id'])
                for child in v.values(): walk(child)
            elif isinstance(v, list):
                for child in v: walk(child)
        walk(body)
        check(found == set(clients), f'persisted clients {found}, expected {set(clients)}')
        installations = body.get('installations', [])
        check(len(installations) == 1, 'expected exactly one fresh installation')
        for binding in installations[0].get('clients', {}).values():
            check(binding.get('materialization') == 'materialized', 'binding not materialized')
            target = Path(binding['target_locator']).resolve()
            check(target.is_relative_to(self.root.resolve()), 'target escaped fixture')
            check((target / 'plugin.json').is_file(), 'native package manifest missing')
            check(json.loads((target / 'plugin.json').read_text(encoding='utf-8'))['name'] == 'pty-synthetic',
                  'wrong package materialized')
            check(any(r.get('phase') == 'committed' for r in binding.get('receipts', [])),
                  'no committed mutation receipt')
            check((binding.get('activation') == 'active') == (binding['client_id'] in active),
                  f'wrong activation for {binding["client_id"]}')
            check(binding.get('authentication') not in ('authenticated', 'not_required'),
                  'authentication falsely confirmed')
        check(self.mutations() != self.before, 'Yes produced no mutation')

    def validate_stubs(self):
        log = self.root / 'stub.log'
        if log.exists():
            check(all(line.endswith(' --version') for line in log.read_text(encoding='utf-8').splitlines()),
                  'CLI attempted client runtime or mutation subprocess')


class OwnedProcess:
    """Popen-shaped status for a job reaped by the persistent terminal owner."""
    def __init__(self, session, argv, streams=None):
        self.session = session
        self.pid = session.rpc(op='start', argv=argv, cwd=str(session.fixture.project),
                               env=session.fixture.env, streams=streams or {})
        self.returncode = None
        self.stdin = None

    def poll(self):
        if self.returncode is None:
            self.returncode = self.session.rpc(op='poll', pid=self.pid)
        return self.returncode

    def kill(self):
        self.returncode = self.session.rpc(op='kill', pid=self.pid)

    def wait(self, timeout=2):
        deadline = time.monotonic() + timeout
        while self.poll() is None and time.monotonic() < deadline:
            self.session.pump(0.01)
        check(self.returncode is not None, 'owner job wait timed out')
        return self.returncode


class Session:
    def __init__(self, argv, fixture, evidence, timeout=10, stdin_pipe=False,
                 redirect=None, rows=30, cols=100, read_chunk=65536):
        self.fixture, self.evidence = fixture, evidence
        self.timeout, self.read_chunk = timeout, read_chunk
        self.master, self.slave = pty.openpty()
        self.screen = Screen(rows, cols)
        self.raw = bytearray()
        self.events = []
        self.resize(rows, cols, notify=False)
        self.before = termios.tcgetattr(self.slave)
        self.closed = False
        parent, child = socket.socketpair()
        parent.settimeout(max(2, timeout))
        self.control = parent
        self.channel = parent.makefile('rwb', buffering=0)
        self.owner = subprocess.Popen(
            [sys.executable, '-I', str(Path(__file__).with_name('pty_owner.py')),
             str(self.slave), str(child.fileno())],
            pass_fds=(self.slave, child.fileno()), stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        child.close()
        streams = {}
        pipe_write = None
        if stdin_pipe:
            fifo = evidence / 'held-stdin.fifo'
            os.mkfifo(fifo)
            pipe_write = open(fifo, 'r+b', buffering=0)
            streams['stdin'] = str(fifo)
        for name in ('stdout', 'stderr'):
            if redirect in (name, 'both'): streams[name] = str(evidence / (name + '.raw'))
        try:
            self.process = OwnedProcess(self, argv, streams)
            self.process.stdin = pipe_write
            if stdin_pipe: fifo.unlink()
        except BaseException:
            if pipe_write: pipe_write.close()
            self.channel.close(); self.control.close()
            self.owner.wait(timeout=3)
            self.owner.stderr.close()
            os.close(self.master); os.close(self.slave)
            raise
        self.start = time.monotonic()
        self.restoration = None

    def rpc(self, **request):
        self.channel.write((json.dumps(request) + '\n').encode())
        response = self.channel.readline()
        check(bool(response), 'terminal owner closed status channel')
        result = json.loads(response)
        check('error' not in result, f'terminal owner failed: {result.get("error")}')
        return result['result']

    def resize(self, rows, cols, notify=True):
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
        self.screen.rows, self.screen.cols = rows, cols
        self.screen.grid = [[' '] * cols for _ in range(rows)]
        self.screen.row = self.screen.col = 0
        if notify:
            os.killpg(self.process.pid, signal.SIGWINCH)
            self.events.append({'event': 'resize', 'rows': rows, 'cols': cols})

    def pump(self, timeout=0.1):
        if select.select([self.master], [], [], timeout)[0]:
            try: data = os.read(self.master, self.read_chunk)
            except OSError: return False
            self.raw.extend(data); self.screen.feed(data)
            check(len(self.raw) < 4 * 1024 * 1024, 'capture exceeded 4 MiB bound')
            return bool(data)
        return False

    def drain(self):
        deadline = time.monotonic() + min(1, self.timeout)
        while time.monotonic() < deadline and self.pump(0):
            pass

    def wait(self, marker, label, after=0, suffix=None):
        def ready():
            text = clean(self.raw[after:])
            match = re.search(marker, text)
            return match and (suffix is None or re.match(suffix, text[match.end():]))

        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            if ready():
                self.frame(label); return len(self.raw)
            if self.process.poll() is not None:
                self.pump(0)
                if ready():
                    self.frame(label); return len(self.raw)
                raise AssertionError(f'exited {self.process.returncode} before {label}')
            self.pump(min(0.1, max(0, deadline - time.monotonic())))
        raise AssertionError(f'timeout waiting for semantic marker: {label}')

    def frame(self, label):
        snapshot = self.screen.snapshot()
        (self.evidence / (label + '.frame.txt')).write_text(snapshot)
        # Portable rendered transcript image. Raw capture remains authoritative;
        # this small Screen is not a full native terminal emulator.
        lines = snapshot.splitlines()
        width, height = self.screen.cols * 9 + 32, max(len(lines), 1) * 20 + 52
        rows = ''.join(f'<text x="16" y="{48 + i * 20}">{html.escape(line)}</text>' for i, line in enumerate(lines))
        svg = (f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}">'
               '<rect width="100%" height="100%" fill="#111827"/>'
               '<g fill="#e5e7eb" font-family="monospace" font-size="14" xml:space="preserve">'
               '<text x="16" y="20">Synthetic PTY capture · rendered transcript</text>' + rows + '</g></svg>')
        (self.evidence / (label + '.frame.svg')).write_text(svg)
        self.events.append({'event': label, 'offset': len(self.raw),
                            'elapsed': round(time.monotonic() - self.start, 3)})

    def send(self, keys):
        os.write(self.master, keys)

    def finish(self, expected=0):
        deadline = time.monotonic() + self.timeout
        while self.process.poll() is None and time.monotonic() < deadline: self.pump()
        check(self.process.poll() is not None, 'CLI did not exit within timeout')
        self.drain()
        check(self.process.returncode == expected,
              f'exit {self.process.returncode}, expected {expected}')
        self.frame('exit')
        self.restore_probe()

    def restore_probe(self):
        after = termios.tcgetattr(self.slave)
        same = self.before == after
        transcript = bytes(self.raw)
        hidden = transcript.rfind(b'\x1b[?25l')
        shown = transcript.rfind(b'\x1b[?25h')
        cursor = hidden < 0 or shown > hidden
        # A second process reads the SAME slave without resetting terminal modes.
        # Its successful canonical read + kernel echo demonstrates usable handoff.
        nonce = uuid.uuid4().hex
        ready, answer, success = ('RESTORE_READY_' + nonce, 'line_' + nonce, 'RESTORE_OK_' + nonce)
        probe = OwnedProcess(self, ['/bin/sh', '-c',
                             f'printf "{ready}\\n"; IFS= read -r line; '
                             f'[ "$line" = {answer} ] && printf "{success}\\n"'])
        offset = len(self.raw)
        deadline = time.monotonic() + self.timeout
        try:
            while ready.encode() not in self.raw[offset:] and time.monotonic() < deadline: self.pump()
            check(ready.encode() in self.raw[offset:], 'restoration probe not ready')
            self.send((answer + '\n').encode())
            while probe.poll() is None and time.monotonic() < deadline: self.pump()
            self.drain()
            echo = answer.encode() in self.raw[offset:]
            ok = probe.poll() == 0 and success.encode() in self.raw[offset:]
            self.restoration = {'termios_equal': same, 'cursor_restored': cursor,
                                'next_line_read': ok, 'echo': echo}
            check(same and cursor and ok and echo, f'terminal restoration failed: {self.restoration}')
        finally:
            if probe.poll() is None: probe.kill()
            probe.wait(timeout=2)

    def close(self):
        if self.closed: return
        self.closed = True
        try:
            forced = self.process.poll() is None
            if forced: self.process.kill()
            self.drain()
            (self.evidence / 'terminal.ansi').write_bytes(self.raw)
            (self.evidence / 'transcript.txt').write_text(clean(self.raw), encoding='utf-8')
            (self.evidence / 'events.json').write_text(json.dumps({
                'events': self.events, 'forced_cleanup': forced, 'exit': self.process.returncode,
                'restoration': self.restoration, 'unknown_csi': sorted(self.screen.unknown),
            }, indent=2))
        finally:
            if self.process.stdin: self.process.stdin.close()
            try:
                # Cleanup reset is after evidence, while the owner is STILL alive.
                # Even a failed evidence write must release the terminal and jobs.
                termios.tcsetattr(self.slave, termios.TCSANOW, self.before)
            finally:
                try: self.rpc(op='close')
                finally:
                    self.channel.close(); self.control.close()
                    try: self.owner.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        self.owner.kill(); self.owner.wait(timeout=2)
                        raise AssertionError('terminal owner cleanup timed out')
                    finally:
                        self.owner.stderr.close()
                        os.close(self.master); os.close(self.slave)


def assert_paste_stayed_in_selection(raw, confirmation):
    # Check the entire post-paste transcript, including output drained on exit.
    # Ignoring paste need not repaint, but advancing to preflight/confirm is wrong.
    text = clean(raw)
    check(not re.search(confirmation, text) and not re.search(r'(?m)^Target:|^Plugin:|^Source:', text),
          'paste advanced beyond selection before explicit submit')


CASES = ('detection', 'baseline-lifecycle', 'group-progress', 'group-progress-failure', 'default-yes', 'no', 'yes-lifecycle', 'queued-lifecycle', 'sigterm', 'confirm-sigterm', 'empty',
         'escape', 'lf-escape', 'ctrl-c', 'ctrl-d', 'confirm-escape', 'confirm-ctrl-c',
         'confirm-ctrl-d', 'confirm-lf-escape', 'plain', 'dumb', 'term-unset', 'no-color', 'NO_COLOR',
         'resize', 'tiny', 'width-40', 'width-80', 'width-160', 'slow-terminal',
         'queued', 'paste', 'plain-eof', 'plain-partial-eof',
         'stdin-pipe', 'json', 'json-tty', 'json-explicit', 'stdout-redirect',
         'stderr-redirect', 'both-redirect', 'plain-auto', 'plain-always',
         'plain-never', 'plain-NO_COLOR', 'rich-never', 'color-stderr-visible',
         'color-pipe-human', 'color-pipe-json', 'color-error')


def run_case(name, binary, root, args):
    evidence = root / name
    evidence.mkdir()
    with tempfile.TemporaryDirectory(prefix='agentplugins-pty-') as tmp:
        fixture = Fixture(tmp, getattr(args, "scanner_path", None), include_opencode=name == "group-progress")
        argv = [str(binary), 'add', str(fixture.package)]
        if getattr(args, 'npm_launcher', False):
            node = shutil.which('node')
            check(node, 'node is required for --npm-launcher')
            source = Path(__file__).resolve().parents[2] / 'npm/agentplugins'
            staged = json.loads(subprocess.check_output([node, str(Path(__file__).with_name('stage_npm.js')),
                str(source), str(binary), str(fixture.root / 'npm')], env=fixture.env, timeout=args.timeout))
            fixture.env['AGENTPLUGINS_INTERNAL_PROOF_MODE'] = 'local-frozen-release-asset-v1'
            fixture.env['AGENTPLUGINS_INTERNAL_PROOF_BINARY'] = staged['asset']
            fixture.env['AGENTPLUGINS_CACHE_DIR'] = str(fixture.root / 'npm-cache')
            argv = [node, staged['launcher'], *argv[1:]]
        plain = name in ('tiny', 'plain', 'dumb', 'term-unset', 'plain-eof', 'plain-partial-eof',
                         'stdout-redirect', 'stderr-redirect')
        legacy_plain = plain
        if legacy_plain: argv += ['--color=never']
        if name.startswith('plain-') or name == 'color-stderr-visible': plain = True
        if name.startswith('plain'): argv += ['--plain']
        if name in ('plain-auto', 'color-stderr-visible'): argv += ['--color=auto']
        if name in ('plain-always', 'color-pipe-human', 'color-pipe-json', 'color-error'):
            argv += ['--color=always']
        if name in ('plain-never', 'rich-never'): argv += ['--color=never']
        if name == 'plain-NO_COLOR': fixture.env['NO_COLOR'] = '1'
        if name in ('color-pipe-human', 'color-pipe-json'):
            argv += ['--target=cursor', '--dry-run']
        if name == 'color-pipe-json': argv += ['--format=json']
        if name == 'color-error': argv += ['--target=nonexistent', '--dry-run']
        if name == 'dumb': fixture.env['TERM'] = 'dumb'
        if name == 'term-unset': fixture.env.pop('TERM')
        if name == 'no-color': argv += ['--no-color']
        if name == 'NO_COLOR': fixture.env['NO_COLOR'] = '1'
        if name == 'detection': argv += ['--dry-run']
        if name.startswith('json'): argv += ['--format', 'json']
        if name == 'json-explicit': argv += ['--target', 'cursor', '--dry-run']
        redirect = {'stdout-redirect': 'stdout', 'stderr-redirect': 'stderr',
                    'both-redirect': 'both', 'json': 'stdout',
                    'json-explicit': 'stdout'}.get(name)
        if name == 'color-stderr-visible': redirect = 'stdout'
        if name in ('color-pipe-human', 'color-pipe-json'): redirect = 'both'
        dimensions = {'tiny': (6, 32), 'width-40': (30, 40),
                      'width-80': (30, 80), 'width-160': (30, 160)}
        rows, cols = dimensions.get(name, (30, 100))
        session = Session(argv, fixture, evidence, args.timeout,
                          stdin_pipe=name == 'stdin-pipe', redirect=redirect,
                          rows=rows, cols=cols,
                          read_chunk=7 if name == 'slow-terminal' else 65536)
        try:
            if name in ('color-pipe-human', 'color-pipe-json', 'color-error'):
                session.finish(1 if name == 'color-error' else 0)
                fixture.unchanged()
                if name == 'color-error':
                    check(re.search(rb'\x1b\[31m[^\x1b]+\x1b\[m', session.raw), 'error role/reset missing')
                else:
                    body = (evidence / 'stdout.raw').read_bytes()
                    stderr = (evidence / 'stderr.raw').read_bytes()
                    if name == 'color-pipe-json':
                        check(json.loads(body).get('schema_version') == 1, 'invalid JSON envelope')
                        check(b'\x1b' not in body + stderr, 'forced color contaminated JSON streams')
                    else:
                        check(b'\x1b[36mTarget\x1b[m: cursor' in body, 'label reset/value boundary missing')
                return
            if name in ('stdin-pipe', 'json', 'json-tty', 'json-explicit', 'both-redirect'):
                # stdin-pipe is deliberately held OPEN with no bytes. Reading it hangs
                # and fails the bound; closing first would only prove EOF handling.
                session.finish(0 if name == 'json-explicit' else 1)
                fixture.unchanged()
                if name == 'json-tty':
                    check(not re.search(args.selection, clean(session.raw)), 'TTY JSON prompted')
                    check(b'\x1b' not in session.raw, 'TTY JSON emitted terminal controls')
                elif name == 'json':
                    check((evidence / 'stdout.raw').read_bytes() == b'', 'argument error contaminated stdout')
                    check('automated installation requires --target' in clean(session.raw), 'missing target diagnostic absent')
                    check(not re.search(args.selection, clean(session.raw)), 'JSON prompted')
                elif name.startswith('json'):
                    body = (evidence / 'stdout.raw').read_bytes()
                    parsed = json.loads(body)
                    check(parsed.get('schema_version') == 1, 'invalid JSON envelope')
                    check(b'\x1b' not in body, 'ANSI contaminated JSON stdout')
                    check(not re.search(args.selection, clean(session.raw)), 'JSON prompted')
                return
            session.wait(args.selection, 'selection')
            session.wait(r'(?i)cursor', 'choices-ready')
            check('codex' in clean(session.raw).lower(), 'synthetic Codex not offered')
            fixture.unchanged()
            if name == 'detection':
                # Baseline uses a line selector. This case only proves local discovery
                # and dry-run plumbing, never Huh acceptance.
                session.send(b'\n')
                session.finish()
                fixture.unchanged()
                return
            if name == 'baseline-lifecycle':
                session.send(b'2\n')
                session.wait(LIFECYCLE, 'plain-activation')
                fixture.installed(['cursor'])
                if name != 'queued-lifecycle': session.send(b'n\n')
                session.finish()
                fixture.installed(['cursor'])
                return
            if name == 'group-progress':
                session.send(selection_keys(session.raw, {'cursor', 'opencode'}))
                session.wait(args.confirmation, 'group-confirmation')
                fixture.unchanged()
                session.send(b'\r')
                session.finish()
                fixture.installed(['cursor', 'opencode'])
                raw = bytes(session.raw)
                check(b'\x1b[2A' in raw and raw.count(b'\x1b[2K') >= 6,
                      'live group rows were not redrawn in place')
                check('preparing' in clean(raw) and 'configuring' in clean(raw)
                      and 'installing' in clean(raw), 'group stages missing from PTY stream')
                check('OpenCode' in session.screen.snapshot()
                      and 'Cursor' in session.screen.snapshot(), 'final group board missing selected clients')
                check('1/3  preparing package' not in clean(raw),
                      'static fallback appeared in a live terminal')
                return
            if name == 'group-progress-failure':
                session.send(selection_keys(session.raw, {'codex', 'cursor'}))
                session.wait(args.confirmation, 'group-confirmation')
                fixture.unchanged()
                session.send(b'\r')
                session.finish(1)
                fixture.unchanged()
                raw = bytes(session.raw)
                check(b'\x1b[2A' in raw, 'failed group did not use the live board')
                check('not completed' in clean(raw) and 'no target was changed' in clean(raw),
                      'failed preflight was presented as installed')
                return
            cancel = {'lf-escape': b'\n\x1b', 'escape': b'\x1b', 'ctrl-c': b'\x03', 'ctrl-d': b'\x04'}
            if name == 'sigterm':
                os.kill(session.process.pid, signal.SIGTERM)
                session.finish(1); fixture.unchanged(); return
            if name in cancel:
                session.send(cancel[name]); session.finish(1); fixture.unchanged(); return
            if name == 'empty':
                cancel_empty_selection(session, fixture, args.confirmation)
                return
            if name in ('resize', 'tiny'):
                session.resize(8, 40)
            if name in ('plain-eof', 'plain-partial-eof'):
                if name == 'plain-partial-eof':
                    session.send(b'\n')
                    session.wait(args.confirmation, 'confirmation')
                    fixture.unchanged()
                    # Canonical VEOF flushes a partial line, then a second VEOF
                    # produces read(0). Neither is a raw-mode Ctrl+D event.
                    session.send(b'y\x04\x04')
                else: session.send(b'\x04')
                session.finish(1); fixture.unchanged(); return
            # Single selected Cursor forces the plain activation handoff even
            # when the host has additional installed client applications.
            offset = len(session.raw)
            if name in ('default-yes', 'yes-lifecycle', 'queued-lifecycle'): session.send(selection_keys(session.raw, {'cursor'}))
            elif name == 'queued': session.send(b'\r\r')
            elif name == 'paste': session.send(b'\x1b[200~\ny\nn\n\x1b[201~')
            else: session.send(b'\n' if plain else b'\r')
            if name == 'paste':
                # Pasted multiline text must not grant consent. An implementation
                # may reject it or keep the selection form; either must remain live
                # and mutation-free until explicit cancellation.
                # Ignored paste need not repaint. Escape proves liveness below.
                fixture.unchanged()
                session.send(b'\x1b'); session.finish(1)
                assert_paste_stayed_in_selection(session.raw[offset:], args.confirmation)
                fixture.unchanged(); return
            # Plain output can split the title from its default/input suffix.
            # Observe both on this prompt line within the same wait deadline.
            session.wait(args.confirmation, 'confirmation', after=offset,
                         suffix=r'[^\r\n]*\[Y/n\] ' if plain else None)
            if not plain and name != 'queued':
                session.wait(r'(?s)Yes.*?No.*?enter submit', 'confirmation-controls', after=offset)
            fixture.unchanged()
            check('pty-synthetic' in clean(session.raw) and '1.0.0' in clean(session.raw),
                  'preflight plan omits package identity/version')
            if name != 'queued':
                check(re.search(r'(?i)\byes\b|\[Y/n\]', clean(session.raw[offset:])), 'Yes default not visible')
            if name in ('default-yes', 'no', 'no-color', 'NO_COLOR'):
                check('✓ Yes' in clean(session.raw[offset:]), 'default Yes has no visible selection mark')
            if name == 'confirm-sigterm':
                os.kill(session.process.pid, signal.SIGTERM)
                session.finish(1); fixture.unchanged(); return
            if name.startswith('confirm-'):
                session.send(cancel[name.removeprefix('confirm-')])
                session.finish(1); fixture.unchanged(); return
            if name in ('default-yes', 'yes-lifecycle', 'queued-lifecycle'):
                session.send(b'\rn\n' if name == 'queued-lifecycle' else b'\r')
                session.wait(LIFECYCLE, 'plain-activation')
                fixture.installed(['cursor'])
                # At plain handoff raw/canonical mode must already be restored.
                check(termios.tcgetattr(session.slave) == session.before, 'raw mode leaked into plain lifecycle')
                start = len(session.raw)
                if name != 'queued-lifecycle': session.send(b'n\n')
                session.finish()
                check('Activation remains unconfirmed' in clean(session.raw), 'activation No was lost')
                check('Have you completed required authentication' not in clean(session.raw[start:]),
                      'activation No advanced to auth')
                fixture.installed(['cursor'])
            else:
                if name != 'queued': session.send(b'n\n' if plain else b' \r')
                session.finish(); fixture.unchanged()
            if legacy_plain or name in ('plain-never', 'plain-NO_COLOR'):
                check(b'\x1b' not in session.raw, 'plain emitted terminal controls')
            if name in ('plain-auto', 'plain-always', 'color-stderr-visible'):
                check(b'\x1b[36mTarget\x1b[m: ' in session.raw, 'visible colored label/reset missing')
                check(b'\x1b[?25l' not in session.raw, 'Plain unexpectedly entered rich renderer')
                if name == 'color-stderr-visible':
                    check(b'\x1b' not in (evidence / 'stdout.raw').read_bytes(), 'redirected auto stdout colored')
            if name == 'rich-never':
                check(b'\x1b[?25l' in session.raw, 'never disabled rich interaction')
                check(not re.search(rb'\x1b\[[0-9;:]*m', session.raw), 'never emitted SGR')
            if name in ('default-yes', 'yes-lifecycle', 'queued-lifecycle'):
                check(re.search(rb'\x1b\[33m[^\x1b]+\x1b\[m', session.raw), 'warning role/reset missing')
            if name in ('no-color', 'NO_COLOR'):
                sgr = re.findall(rb'\x1b\[([0-9;:]*)m', session.raw)
                check(not sgr, 'disabled color emitted SGR')
                for value in sgr:
                    nums = [int(n) for n in re.split(rb'[;:]', value) if n]
                    check(not any(30 <= n <= 38 or 40 <= n <= 48 or 90 <= n <= 107 for n in nums), 'color SGR emitted')
        finally:
            (evidence / 'mutations.json').write_text(json.dumps({
                'before': fixture.before, 'after': fixture.mutations()}, indent=2))
            session.close()
            log = fixture.root / 'stub.log'
            if log.exists(): (evidence / 'stub.log').write_bytes(log.read_bytes())
            fixture.validate_stubs()


SCANNER_ARCHIVES = {
    'lintai-v0.1.3-aarch64-apple-darwin.tar.gz': 'be8b263e2323074080d928ea7c2129458299a6d03f7a9f178dfc1aa8e6bc17ff',
    'lintai-v0.1.3-x86_64-unknown-linux-gnu.tar.gz': '2b3d176db752433b904a4b42375543ff398f4841d22e48f7d4f23ded925b72da',
    'lintai-v0.1.3-x86_64-pc-windows-msvc.zip': '2f61f6a83a160afa3feed9ea1722b82d0d938ebff865a4e20d39b5f55270c911',
}


def scanner_options(parser):
    group = parser.add_mutually_exclusive_group()
    group.add_argument('--scanner-archive', type=Path, help='pinned v0.1.3 archive, verified before extraction')
    group.add_argument('--scanner-binary', type=Path, help='real scanner, requires --scanner-sha256')
    parser.add_argument('--scanner-sha256', help='trusted digest of raw scanner executable')


def prepare_scanner(args):
    args.scanner_path = None
    if args.scanner_archive:
        archive = args.scanner_archive
        expected = SCANNER_ARCHIVES.get(archive.name)
        data = archive.read_bytes()
        check(expected is not None and hashlib.sha256(data).hexdigest() == expected,
              'scanner archive name or SHA256 does not match pin')
        # Read the exact verified bytes; never extract paths, links, or devices.
        import io
        if archive.name.endswith('.zip'):
            with zipfile.ZipFile(io.BytesIO(data)) as z:
                names = [n for n in z.namelist() if Path(n).name == 'lintai.exe']
                check(len(names) == 1, 'archive must contain exactly one lintai.exe')
                payload = z.read(names[0])
        else:
            with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as t:
                members = [m for m in t.getmembers() if Path(m.name).name == 'lintai' and m.isfile()]
                check(len(members) == 1, 'archive must contain exactly one regular lintai')
                payload = t.extractfile(members[0]).read()
        source = {'archive': str(archive), 'archive_sha256': expected}
    elif args.scanner_binary:
        payload = args.scanner_binary.read_bytes()
        check(args.scanner_sha256 and hashlib.sha256(payload).hexdigest() == args.scanner_sha256.lower(),
              'scanner binary SHA256 mismatch or missing trusted digest')
        source = {'binary': str(args.scanner_binary)}
    else:
        check(not args.scanner_sha256, '--scanner-sha256 needs --scanner-binary')
        return 'SYNTHETIC cached protocol stub; no security proof'
    args.scanner_path = args.artifacts / ('verified-lintai.exe' if os.name == 'nt' else 'verified-lintai')
    args.scanner_path.write_bytes(payload)
    args.scanner_path.chmod(0o700)
    return dict(kind='real scanner; synthetic package only', executable_sha256=hashlib.sha256(payload).hexdigest(), **source)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--artifacts', required=True, type=Path,
                        help='new evidence directory; existing paths rejected')
    parser.add_argument('--case', action='append', choices=CASES)
    parser.add_argument('--npm-launcher', action='store_true', help='stage exact binary through real npm launcher; local digest seam only')
    parser.add_argument('--timeout', type=float, default=12)
    parser.add_argument('--selection', default=SELECT, help='semantic selection regex')
    parser.add_argument('--confirmation', default=CONFIRM, help='semantic install-confirmation regex')
    scanner_options(parser)
    args = parser.parse_args()
    if os.name != 'posix': parser.error('Unix PTY required; see README.md native Windows instructions')
    if args.timeout <= 0: parser.error('timeout must be positive')
    binary = args.binary.resolve(strict=True)
    args.artifacts.mkdir(parents=True, exist_ok=False)
    scanner_evidence = prepare_scanner(args)
    results = []
    for name in args.case or tuple(c for c in CASES if c not in ('detection', 'baseline-lifecycle')):
        try:
            run_case(name, binary, args.artifacts, args)
            result = {'case': name, 'status': 'passed'}
        except Exception as exc:
            result = {'case': name, 'status': 'failed', 'error': str(exc)}
        results.append(result)
        print(json.dumps(result), flush=True)
        (args.artifacts / 'results.json').write_text(json.dumps({
            'platform': platform.platform(), 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
            'launcher': 'npm stdio:inherit' if args.npm_launcher else 'native',
            'binary': str(binary), 'python': platform.python_version(),
            'scanner': scanner_evidence,
            'network': 'local fixture + version-only stubs + dead proxies; not firewall attestation',
            'results': results}, indent=2))
    return int(any(r['status'] == 'failed' for r in results))


if __name__ == '__main__':
    sys.exit(main())
