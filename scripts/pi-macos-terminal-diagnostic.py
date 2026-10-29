#!/usr/bin/env python3
"""Diagnostic only: run the unchanged actual product fixture from real Terminal."""
import os
from pathlib import Path
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
command = " ".join(shlex.quote(value) for value in argv)
script = root / "launch.command"
script.write_text("#!/bin/bash\nset -u\ncd " + shlex.quote(str(repo))
                  + "\nset +e\n" + command + " >" + shlex.quote(str(root / "stdout"))
                  + " 2>" + shlex.quote(str(root / "stderr"))
                  + "\ncode=$?\nprintf '%s\\n' \"$code\" >" + shlex.quote(str(root / "exit")) + "\n")
script.chmod(0o700)


def apple_quote(value):
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'


def apple(source):
    try:
        return subprocess.check_output(["/usr/bin/osascript", "-e", source], text=True, timeout=15).strip()
    except subprocess.TimeoutExpired:
        evidence = Path(argv[argv.index("--report") + 1]).parent
        subprocess.run(["/usr/sbin/screencapture", "-x", str(evidence / "driver-failure.png")], timeout=10, check=False)
        raise


identity = apple('tell application "Terminal"\nactivate\nset t to do script '
                 + apple_quote("/bin/bash --noprofile --norc " + shlex.quote(str(script)))
                 + '\nset custom title of t to "TEST Pi native setup driver"\nset w to front window'
                 + '\nreturn (id of w as text) & "|" & (tty of t as text)\nend tell')
window, tty = identity.split("|", 1)
if not window.isdecimal() or not tty.startswith("/dev/ttys"):
    raise SystemExit("owned native driver Terminal identity unavailable")
deadline = time.monotonic() + 900
while not (root / "exit").exists() and time.monotonic() < deadline:
    time.sleep(0.1)
if not (root / "exit").is_file():
    raise SystemExit("unchanged product fixture did not finish within diagnostic deadline")
for name, stream in (("stdout", sys.stdout), ("stderr", sys.stderr)):
    path = root / name
    if path.is_file():
        if path.stat().st_size > 4 * 1024 * 1024:
            raise SystemExit("diagnostic driver output limit")
        stream.write(path.read_text())
apple('tell application "Terminal"\nif exists window id ' + window
      + ' then\nif (tty of selected tab of window id ' + window + ') is '
      + apple_quote(tty) + ' then close window id ' + window + ' saving no\nend if\nend tell')
raise SystemExit(int((root / "exit").read_text().strip()))
