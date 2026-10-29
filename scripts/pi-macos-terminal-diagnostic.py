#!/usr/bin/env python3
"""Diagnostic only: unchanged product fixture in a LaunchServices Terminal."""
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time

root = Path(tempfile.mkdtemp(prefix="TEST-pi-terminal-driver-")).resolve()
root.chmod(0o700)
repo = Path(__file__).resolve().parents[1]
argv = sys.argv[1:]
if argv and argv[0] == "--":
    argv.pop(0)
if not argv:
    raise SystemExit("unchanged product command required")
evidence = Path(argv[argv.index("--report") + 1]).parent
command = " ".join(shlex.quote(value) for value in argv)
script = root / "launch.command"
script.write_text("#!/bin/bash\nset -u\ncd " + shlex.quote(str(repo))
                  + "\n/usr/bin/tty >" + shlex.quote(str(root / "tty"))
                  + "\nprintf '%s\\n' \"$$\" >" + shlex.quote(str(root / "shell.pid"))
                  + "\nset +e\n" + command + " >" + shlex.quote(str(root / "stdout"))
                  + " 2>" + shlex.quote(str(root / "stderr"))
                  + " &\nchild=$!\nprintf '%s\\n' \"$child\" >" + shlex.quote(str(root / "process.pid"))
                  + "\nwait \"$child\"\ncode=$?\nprintf '%s\\n' \"$code\" >" + shlex.quote(str(root / "exit")) + "\n")
script.chmod(0o700)
subprocess.run(["/usr/bin/open", "-n", "-a", "/System/Applications/Utilities/Terminal.app", str(script)], check=True, timeout=15)
started = time.monotonic()
while not (root / "process.pid").exists() and time.monotonic() - started < 15:
    time.sleep(0.1)
subprocess.run(["/usr/sbin/screencapture", "-x", str(evidence / "driver-started.png")], timeout=10, check=False)
if not (root / "process.pid").is_file():
    raise SystemExit("owned native Terminal did not execute its .command")
tty = (root / "tty").read_text().strip()
if not re.fullmatch(r"/dev/ttys[a-z0-9]+", tty):
    raise SystemExit("real Terminal TTY not observed")
(evidence / "terminal-driver.json").write_text(json.dumps({"scope":"diagnostic_only", "native_tty":tty,
    "shell_pid":int((root / "shell.pid").read_text()), "fixture_pid":int((root / "process.pid").read_text()),
    "desktop_qualified":False}, indent=2)+"\n")
while not (root / "exit").exists() and time.monotonic() - started < 900:
    time.sleep(0.1)
if not (root / "exit").is_file():
    raise SystemExit("unchanged product fixture did not finish within diagnostic deadline")
for name, stream in (("stdout", sys.stdout), ("stderr", sys.stderr)):
    path = root / name
    if path.is_file():
        if path.stat().st_size > 4 * 1024 * 1024:
            raise SystemExit("diagnostic driver output limit")
        stream.write(path.read_text())
raise SystemExit(int((root / "exit").read_text().strip()))
