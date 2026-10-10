"""Preparation output and lifecycle checks; no installed CLI or native app runs."""
import argparse
import contextlib
import importlib.util
import io
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

MODULE = Path(__file__).with_name('preview-installer.py')
SPEC = importlib.util.spec_from_file_location('preview_installer', MODULE)
preview = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(preview)


class Terminal(io.StringIO):
    def isatty(self):
        return True


class PreparationTests(unittest.TestCase):
    def test_plain_capabilities_and_exact_verbose_contract(self):
        # Fails if accessibility modes acquire escape frames or technical noise.
        for plain, environment in ((True, {'TERM': 'xterm'}),
                                   (False, {'TERM': 'dumb'}),
                                   (False, {'TERM': 'xterm', 'NO_COLOR': '1'}),
                                   (False, {'TERM': 'xterm', 'BOOTSTRAP_VERBOSE': 'true'})):
            with self.subTest(plain=plain, environment=environment), tempfile.TemporaryDirectory() as directory:
                stream = Terminal() if environment.get('BOOTSTRAP_VERBOSE') != 'true' else io.StringIO()
                log = Path(directory)/'preparation.log'
                with preview.Preparation(log, plain, stream, environment) as output:
                    output.stage('Building preview')
                    preview.detail('technical SHA and executable path')
                    preview.run_command([sys.executable, '-I', '-c', "import sys; print('build stdout'); print('build stderr', file=sys.stderr)"], check=True)
                self.assertNotIn('\x1b', stream.getvalue())
                self.assertNotIn('technical SHA', stream.getvalue())
                self.assertNotIn('build stdout', stream.getvalue())
                self.assertEqual(stream.getvalue().count('Building preview...'), 1)
                self.assertIn('technical SHA and executable path', log.read_text())
                self.assertIn('build stdout', log.read_text())
                self.assertIn('build stderr', log.read_text())
                self.assertEqual(log.stat().st_mode & 0o777, 0o600)
        with tempfile.TemporaryDirectory() as directory:
            stream = Terminal()
            with preview.Preparation(Path(directory)/'log', stream=stream,
                                     environment={'TERM': 'xterm', 'BOOTSTRAP_VERBOSE': '1'}) as output:
                self.assertFalse(output.rich)
                preview.detail('verbose detail')
            self.assertIn('verbose detail', stream.getvalue())
        with tempfile.TemporaryDirectory() as directory:
            stream = Terminal()
            with preview.Preparation(Path(directory)/'log', stream=stream,
                                     environment={'TERM': 'xterm', 'NO_COLOR': ''}) as output:
                self.assertTrue(output.rich)
            self.assertIn('\x1b[36m', stream.getvalue())

    def test_failed_command_diagnostics_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            stream, log = Terminal(), Path(directory)/'log'
            with self.assertRaises(subprocess.CalledProcessError):
                with preview.Preparation(log, stream=stream, environment={'TERM': 'xterm'}) as output:
                    preview.run_command([sys.executable, '-I', '-c', "import sys; print('failure detail', file=sys.stderr); sys.exit(7)"], capture_output=True, text=True, check=True)
            self.assertFalse(output.worker.is_alive())
            self.assertIn('failure detail', log.read_text())
            self.assertIn('Preparation failed', log.read_text())
            self.assertNotIn('failure detail', stream.getvalue())

    def test_preview_failure_retains_profile_and_log_hint(self):
        # Fails if early source errors lose their fresh TEST profile/log.
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)/'TEST-state'
            stdout, stderr = io.StringIO(), io.StringIO()
            args = argparse.Namespace(state_dir=state, plain=True)
            with patch.object(preview.sys, 'platform', 'darwin'), patch.object(preview.os, 'isatty', return_value=True), patch.object(preview, 'source_identity', side_effect=ValueError('source changed')), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.assertEqual(preview.preview(args), 1)
            lab, = state.glob('AllAgents-TEST-*')
            self.assertIn('Preparation failed: source changed', (lab/'preparation.log').read_text())
            self.assertIn(str(lab/'preparation.log'), stdout.getvalue())
            self.assertIn('native app registration may exist', stdout.getvalue())
            self.assertIn('Preview failed: source changed', stderr.getvalue())

    @unittest.skipUnless(os.name == 'posix', 'PTY lifecycle requires POSIX')
    def test_pty_first_frame_and_prompt_handoff(self):
        # A real terminal detects late first paint and renderer writes after prompts.
        with tempfile.TemporaryDirectory() as directory:
            log, release = Path(directory)/'log', Path(directory)/'release-operation'
            builder = "import pathlib,time; release=pathlib.Path("+repr(str(release))+"); exec('while not release.exists(): time.sleep(.02)'); print('private build output')"
            script = self.import_script()+f"""
with m.Preparation(pathlib.Path({str(log)!r})) as output:
    print('OPERATION_ENTERED', flush=True)
    m.run_command([sys.executable, '-I', '-c', {builder!r}], check=True)
assert not output.worker.is_alive()
print('BOOTSTRAP_PROMPT', flush=True)
time.sleep(.3)
"""
            process, master = self.pty_process(script)
            try:
                # Startup latency is irrelevant. Assert ordering against the
                # expensive operation's entry, then let that operation proceed.
                first, deadline = b'', time.monotonic()+10
                while b'OPERATION_ENTERED' not in first and time.monotonic() < deadline:
                    if select.select([master], [], [], .1)[0]:
                        first += os.read(master, 65536)
                self.assertIn(b'OPERATION_ENTERED', first, 'operation did not start')
                self.assertIn(b'Checking preview source', first.split(b'OPERATION_ENTERED', 1)[0])
                release.touch()
                terminal = first+self.read_terminal(master, process)
                self.assertEqual(process.wait(timeout=3), 0, terminal.decode(errors="replace"))
                self.assertNotIn(b'private build output', terminal)
                self.assertIn('private build output', log.read_text())
                self.assertIn(b'\x1b[36m', terminal)
                self.assertEqual(terminal.split(b'BOOTSTRAP_PROMPT', 1)[1].strip(), b'')
            finally:
                self.close_process(process, master)

    @unittest.skipUnless(os.name == 'posix', 'cancellation requires POSIX')
    def test_pty_cancellation_stops_preparation_process_group(self):
        # Fails if the leader exits on TERM but its TERM-resistant child survives.
        # Output deliberately goes directly to the log, so communicate cannot
        # infer descendant liveness from an inherited pipe.
        with tempfile.TemporaryDirectory() as directory:
            pid_file, log = Path(directory)/'builder-pids', Path(directory)/'log'
            builder = "import os,pathlib,signal,time; child=os.fork(); signal.signal(signal.SIGTERM, signal.SIG_IGN) if child == 0 else None; pathlib.Path("+repr(str(pid_file))+"+('.child' if child == 0 else '')).write_text(str(os.getpid())); time.sleep(30)"
            script = self.import_script()+f"""
try:
    with m.Preparation(pathlib.Path({str(log)!r})) as output:
        m.run_command([sys.executable, '-I', '-c', {builder!r}], check=True)
except KeyboardInterrupt:
    assert not output.worker.is_alive()
    print('CANCELLED', flush=True)
"""
            process, master = self.pty_process(script)
            try:
                deadline = time.monotonic()+3
                child_file = Path(str(pid_file)+'.child')
                while not (pid_file.exists() and child_file.exists()) and time.monotonic() < deadline:
                    time.sleep(.02)
                self.assertTrue(child_file.exists(), 'builder did not start')
                os.kill(process.pid, signal.SIGINT)
                terminal = self.read_terminal(master, process)
                self.assertEqual(process.wait(timeout=3), 0, terminal.decode(errors="replace"))
                self.assertIn(b'CANCELLED', terminal)
                self.assertEqual(terminal.split(b'CANCELLED', 1)[1].strip(), b'')
                self.assertIn('Preparation cancelled', log.read_text())
                for pid_path in (pid_file, child_file):
                    pid = int(pid_path.read_text())
                    # Orphan zombies may briefly await init; they no longer execute.
                    result = subprocess.run(['ps', '-o', 'stat=', '-p', str(pid)], capture_output=True, text=True)
                    self.assertTrue(not result.stdout.strip() or result.stdout.strip().startswith('Z'), 'preparation child survived cancellation')
            finally:
                # Regression failures must not leave this test's own group alive.
                if pid_file.exists():
                    try:
                        os.killpg(int(pid_file.read_text()), signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                self.close_process(process, master)

    def import_script(self):
        return f"import importlib.util,pathlib,sys,time\ns=importlib.util.spec_from_file_location('preview', {str(MODULE)!r})\nm=importlib.util.module_from_spec(s)\ns.loader.exec_module(m)\n"

    def pty_process(self, script):
        master, slave = pty.openpty()
        environment = dict(os.environ, TERM='xterm-256color', BOOTSTRAP_VERBOSE='0')
        environment.pop('NO_COLOR', None)
        process = subprocess.Popen([sys.executable, '-I', '-c', script], stdin=slave, stdout=slave, stderr=slave,
                                   env=environment, start_new_session=True)
        os.close(slave)
        return process, master

    def read_terminal(self, master, process):
        output, deadline = b'', time.monotonic()+5
        while time.monotonic() < deadline:
            if select.select([master], [], [], .1)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                if not data:
                    break
                output += data
            elif process.poll() is not None:
                break
        return output

    def close_process(self, process, master):
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=3)
        os.close(master)


if __name__ == '__main__':
    unittest.main()
