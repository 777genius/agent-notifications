"""Diagnostic only: normal public setup CLI in its own real native Terminal."""
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import time


class TerminalPermissionRequest:
    def __init__(self, argv, cwd, env, observer, evidence):
        self.root = Path(tempfile.mkdtemp(prefix="TEST-pi-permission-terminal-")).resolve()
        self.root.chmod(0o700)
        root = self.root
        self.returncode = None
        self.args = list(map(str, argv))
        entries = [shlex.quote(key + "=" + value) for key, value in env.items()]
        code = "import os,sys; os.setsid(); os.execv(sys.argv[1], sys.argv[1:])"
        launch = " ".join(["/usr/bin/env", "-i", *entries, shlex.quote(sys.executable), "-c", shlex.quote(code),
                           *map(shlex.quote, self.args)])
        script = root / "launch.command"
        script.write_text("#!/bin/bash\nset -eu\ncd " + shlex.quote(str(cwd))
                          + "\n/usr/bin/tty >" + shlex.quote(str(root / "tty"))
                          + "\nwhile [ ! -f " + shlex.quote(str(root / "start")) + " ]; do /bin/sleep 0.05; done\n"
                          + "exec 9<&0\nset +e\n" + launch + " <&9 >" + shlex.quote(str(root / "stdout"))
                          + " 2>" + shlex.quote(str(root / "stderr")) + " &\nchild=$!\nexec 9<&-\n"
                          + "printf '%s\\n' \"$child\" >" + shlex.quote(str(root / "pid"))
                          + "\nwait \"$child\"\ncode=$?\nprintf '%s\\n' \"$code\" >"
                          + shlex.quote(str(root / "exit")) + "\nexit 0\n")
        script.chmod(0o700)
        subprocess.run(["/usr/bin/open", "-n", "-a", "/System/Applications/Utilities/Terminal.app", str(script)],
                       check=True, timeout=15)
        deadline = time.monotonic() + 15
        while not (root / "tty").exists() and time.monotonic() < deadline:
            time.sleep(0.05)
        foreground = observer("foreground")
        if not (root / "tty").is_file() or foreground.get("bundle_id") != "com.apple.Terminal":
            raise RuntimeError("normal_permission_driver_terminal_not_foreground")
        tty = (root / "tty").read_text().strip()
        if not tty.startswith("/dev/ttys"):
            raise RuntimeError("normal_permission_driver_native_tty_missing")
        self.identity = {"scope": "diagnostic_only", "normal_public_cli": True, "native_tty": tty,
                         "foreground": foreground, "terminal_window_retained_until_job_cleanup": True,
                         "desktop_qualified": False}
        (Path(evidence) / "permission-terminal-driver.json").write_text(json.dumps(self.identity, indent=2) + "\n")
        (root / "start").touch()
        while not (root / "pid").exists() and time.monotonic() < deadline:
            time.sleep(0.05)
        if not (root / "pid").is_file():
            raise RuntimeError("normal_permission_driver_cli_pid_missing")
        self.pid = int((root / "pid").read_text())
        if self.pid <= 0:
            raise RuntimeError("normal_permission_driver_cli_pid_invalid")

    def poll(self):
        if (self.root / "exit").is_file():
            self.returncode = int((self.root / "exit").read_text())
        return self.returncode

    def communicate(self, timeout):
        deadline = time.monotonic() + timeout
        while self.poll() is None and time.monotonic() < deadline:
            time.sleep(0.05)
        if self.poll() is None:
            raise subprocess.TimeoutExpired(self.args, timeout)
        result = []
        for name in ("stdout", "stderr"):
            with (self.root / name).open("rb") as stream:
                result.append(stream.read(65537))
        return tuple(result)
