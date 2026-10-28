#!/usr/bin/env python3
"""Real PTY regression for the emitted macOS Terminal launch script."""
import importlib.util
import json
import os
from pathlib import Path
import pty
import subprocess
import sys
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("pi_installed_macos", REPO / "scripts/pi-installed-desktop-macos.py")
M = importlib.util.module_from_spec(spec)
spec.loader.exec_module(M)


class TerminalInput(unittest.TestCase):
    def test_actual_async_launcher_keeps_both_ttys_and_collects_child(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-terminal-pty-") as temporary:
            root = Path(temporary).resolve()
            cwd = root / "TEST project with spaces"
            cwd.mkdir()
            result = root / "native-tty.json"
            source = ('import json,os,sys; from pathlib import Path; '
                      'Path(sys.argv[1]).write_text(json.dumps({"pid":os.getpid(),'
                      '"stdin_tty":os.isatty(0),"stdout_tty":os.isatty(1),'
                      '"input_tty":os.ttyname(0) if os.isatty(0) else None,'
                      '"output_tty":os.ttyname(1),"cwd":os.getcwd()}))')
            script = M.terminal_launcher(root, [sys.executable, "-c", source, result],
                                         {"PATH": os.environ["PATH"], "HOME": str(root)}, cwd)
            master, slave = pty.openpty()
            try:
                child = subprocess.Popen(["/bin/bash", "--noprofile", "--norc", script],
                                         stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
                self.assertEqual(child.wait(timeout=5), 0)
                observed = json.loads(result.read_text())
                self.assertIs(observed["stdin_tty"], True)
                self.assertIs(observed["stdout_tty"], True)
                self.assertEqual(observed["input_tty"], observed["output_tty"])
                self.assertEqual(observed["cwd"], str(cwd))
                self.assertEqual(observed["pid"], int((root / "process.pid").read_text()))
                self.assertEqual(int((root / "shell.pid").read_text()), child.pid)
                self.assertEqual((root / "process.exit").read_text().strip(), "0")
                with self.assertRaises(ProcessLookupError):
                    os.kill(observed["pid"], 0)
            finally:
                os.close(master)
                os.close(slave)


if __name__ == "__main__":
    unittest.main()
