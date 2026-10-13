"""Preparation and proxy lifecycle checks; private fixtures only."""
import argparse
import contextlib
import importlib.util
import io
import json
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
            with patch.object(preview.Path, 'home', return_value=Path(directory)), patch.object(preview.sys, 'platform', 'darwin'), patch.object(preview.os, 'isatty', return_value=True), patch.object(preview, 'source_identity', side_effect=ValueError('source changed')), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.assertEqual(preview.preview(args), 1)
            lab, = state.glob('AllAgents-TEST-*')
            self.assertIn('Preparation failed: source changed', (lab/'preparation.log').read_text())
            self.assertIn(str(lab/'preparation.log'), stdout.getvalue())
            self.assertIn('keep while native app is registered', stdout.getvalue())
            self.assertIn('Preview failed: source changed', stderr.getvalue())

    def test_credential_denied_state_is_rejected_before_creating_profile(self):
        # A TEST cwd beneath real ~/.codex was denied by the CLI sandbox and
        # misreported every supported CLI as broken. Fail before preparation.
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)/'.codex'/'TEST-state'
            args = argparse.Namespace(state_dir=state,plain=True)
            with patch.object(preview.Path,'home',return_value=Path(directory)), patch.object(preview.sys,'platform','darwin'), patch.object(preview.os,'isatty',return_value=True), patch.object(preview,'source_identity',side_effect=ValueError('source unavailable')):
                with self.assertRaisesRegex(ValueError,'credential-denied'):
                    preview.preview(args)
            self.assertFalse(state.exists())

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

    @unittest.skipUnless(sys.platform == 'darwin', 'real sandbox boundary requires macOS')
    def test_allowed_proxy_commands_have_private_bounded_final_receipts(self):
        # The old proxy drops completion/output for install/list, losing the
        # evidence needed to distinguish a failed CLI from a lifecycle timeout.
        with tempfile.TemporaryDirectory() as directory:
            lab, commands, env = self.proxy_lab(Path(directory))
            fixture = lab/'installed-claude'
            fixture.write_text('#!'+sys.executable+' -I\nimport sys\n'
                               "sys.stdout.buffer.write(b'x'*1100000 if 'list' in sys.argv else b'fixture-version\\n')\n"
                               "sys.stderr.write('private diagnostic\\n')\n"
                               "sys.exit(7 if 'install' in sys.argv else 0)\n")
            fixture.chmod(0o700)
            diagnostics = preview.private_directory(Path(directory)/'diagnostics')
            preview.cli_proxies(commands,lab,{'claude':str(fixture)},env,'a'*40,diagnostics=diagnostics)
            for args, status in ((['--version'],0),(['plugin','list','--json'],0),
                                 (['plugin','install','claude-notifications-go@claude-notifications-go'],7)):
                result = subprocess.run([str(commands/'claude'),*args],capture_output=True,timeout=5,env=env)
                self.assertEqual(result.returncode,status,result.stderr)
            rejected = subprocess.run([str(commands/'claude'),'--print','provider prompt'],capture_output=True,timeout=5,env=env)
            self.assertEqual(rejected.returncode,99)
            receipts = [json.loads(line) for line in (diagnostics/'cli-commands.jsonl').read_text().splitlines()]
            self.assertEqual(receipts[-1],{'cli':'claude','args':['--print','provider prompt'],'rejected':True,'exit_code':99})
            self.assertFalse((lab/'cli-commands.jsonl').exists())
            endings = [value for value in receipts if value.get('event') == 'finish']
            self.assertEqual([value['phase'] for value in endings],['version','list','registration'])
            self.assertEqual([value['exit_code'] for value in endings],[0,0,7])
            for value in endings:
                self.assertGreaterEqual(value['monotonic_end_ns'],value['monotonic_start_ns'])
                self.assertGreaterEqual(value['duration_ms'],0)
                self.assertFalse(value['timeout'])
                self.assertFalse(value['cancelled'])
                for path in value['output_paths'].values():
                    self.assertEqual(Path(path).stat().st_mode & 0o777,0o600)
            captured = Path(endings[1]['output_paths']['stdout']).read_bytes()
            self.assertLess(len(captured),1048700)
            self.assertIn(b'output truncated',captured)
            self.assertEqual((diagnostics/'cli-commands.jsonl').stat().st_mode & 0o777,0o600)

    def test_rejected_proxy_command_uses_private_diagnostics_before_invocation(self):
        # Rejections must be durable even when diagnostics is outside the TEST
        # profile, and unsafe receipt leaves must never invoke the real command.
        for leaf in ('missing','symlink','public'):
            with self.subTest(leaf=leaf), tempfile.TemporaryDirectory() as directory:
                lab, commands, env = self.proxy_lab(Path(directory))
                diagnostics = preview.private_directory(Path(directory)/'diagnostics')
                marker = lab/'invoked'
                fixture = lab/'installed-claude'
                fixture.write_text('#!'+sys.executable+' -I\nimport pathlib\n'
                                   'pathlib.Path('+repr(str(marker))+').touch()\n')
                fixture.chmod(0o700)
                # This case tests rejection before any sandbox/CLI execution.
                # Mock only the generator's fixed executable availability check
                # so Linux exercises the actual receipt and refusal boundary.
                is_file = preview.Path.is_file
                def generator_file(path):
                    return path == Path('/usr/bin/sandbox-exec') or is_file(path)
                with patch.object(preview.Path, 'is_file', generator_file):
                    preview.cli_proxies(commands,lab,{'claude':str(fixture)},env,'a'*40,diagnostics=diagnostics)
                receipt = diagnostics/'cli-commands.jsonl'
                original = b'private original\n'
                if leaf == 'symlink':
                    victim = Path(directory)/'victim'
                    victim.write_bytes(original)
                    victim.chmod(0o600)
                    receipt.symlink_to(victim)
                elif leaf == 'public':
                    receipt.write_bytes(original)
                    receipt.chmod(0o644)
                result = subprocess.run([str(commands/'claude'),'--print','provider prompt'],
                                        capture_output=True,timeout=5,env=env)
                if leaf == 'missing':
                    self.assertEqual(result.returncode,99,result.stderr)
                    self.assertEqual(receipt.stat().st_mode & 0o777,0o600)
                    self.assertEqual(json.loads(receipt.read_text()),
                                     {'cli':'claude','args':['--print','provider prompt'],'rejected':True,'exit_code':99})
                else:
                    self.assertNotEqual(result.returncode,99,'unsafe diagnostic leaf accepted')
                    self.assertNotEqual(result.returncode,0)
                    self.assertEqual(receipt.read_bytes(),original)
                    self.assertEqual(receipt.is_symlink(),leaf == 'symlink')
                    if leaf == 'public':
                        self.assertEqual(receipt.stat().st_mode & 0o777,0o644)
                self.assertFalse(marker.exists(),'rejected command was invoked')
                self.assertFalse((lab/'cli-commands.jsonl').exists())
                self.assertEqual(list(diagnostics.glob('*.log')),[])

    @unittest.skipUnless(sys.platform == 'darwin', 'real sandbox boundary requires macOS')
    def test_proxy_cancellation_records_receipt_and_joins_private_children(self):
        # A terminated proxy must retain the interruption receipt and stop a
        # TERM-resistant child even after its leader exits.
        with tempfile.TemporaryDirectory() as directory:
            lab, commands, env = self.proxy_lab(Path(directory))
            pid_file = lab/'fixture-pid'
            fixture = lab/'installed-claude'
            fixture.write_text('#!'+sys.executable+' -I\nimport os,pathlib,signal,time\n'
                               'child=os.fork()\n'
                               'if child == 0: signal.signal(signal.SIGTERM,signal.SIG_IGN)\n'
                               'pathlib.Path('+repr(str(pid_file))+"+('.child' if child == 0 else '')).write_text(str(os.getpid()))\n"
                               'time.sleep(30)\n')
            fixture.chmod(0o700)
            diagnostics = lab
            preview.cli_proxies(commands,lab,{'claude':str(fixture)},env,'a'*40)
            process = subprocess.Popen([str(commands/'claude'),'plugin','list','--json'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True,env=env)
            try:
                child_file = Path(str(pid_file)+'.child')
                deadline = time.monotonic()+5
                while not child_file.exists() and time.monotonic() < deadline: time.sleep(.02)
                self.assertTrue(child_file.exists(),'fixture did not start')
                started = time.monotonic()
                process.terminate()
                stdout, stderr = process.communicate(timeout=5)
                self.assertEqual(process.returncode,143,stderr)
                self.assertLess(time.monotonic()-started,5)
                ending = json.loads((diagnostics/'cli-commands.jsonl').read_text().splitlines()[-1])
                self.assertEqual(ending['event'],'finish')
                self.assertTrue(ending['cancelled'])
                self.assertFalse(ending['timeout'])
                self.assertEqual(ending['signal'],signal.SIGTERM)
                for path in (pid_file,child_file):
                    pid = int(path.read_text())
                    state = subprocess.run(['ps','-o','stat=','-p',str(pid)],capture_output=True,text=True).stdout.strip()
                    self.assertTrue(not state or state.startswith('Z'),'proxy child survived')
            finally:
                for path in (pid_file,Path(str(pid_file)+'.child')):
                    if path.exists():
                        try: os.kill(int(path.read_text()),signal.SIGKILL)
                        except ProcessLookupError: pass
                try: os.killpg(process.pid,signal.SIGKILL)
                except ProcessLookupError: pass
                process.wait(timeout=3)

    def proxy_lab(self, directory):
        lab = preview.private_directory(directory/'TEST-lab')
        commands = preview.private_directory(lab/'commands')
        env = {'PATH':'/usr/bin:/bin'}
        for key in preview.PRIVATE_KEYS:
            env[key] = str(preview.private_directory(lab/key))
        return lab, commands, env

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
