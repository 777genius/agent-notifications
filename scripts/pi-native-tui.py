#!/usr/bin/env python3
"""Real Pi TUI submission/reply on a native PTY; no desktop claim."""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import tempfile
import threading
import time

FIXTURE = Path(__file__).resolve().parents[1] / "tests/integration/pi_native_fixture.py"
SPEC = importlib.util.spec_from_file_location("pi_fixture", FIXTURE)
F = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(F)


def execute(args):
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-native-tui-")).resolve()
    root.chmod(0o700)
    provider = F.Provider("success")
    env = F.prepare(root, provider, "success")
    env.update(TERM="xterm-256color", LANG="C.UTF-8")
    report = {"schema_version": 1, "status": "failed", "scope": "actual_pi_tui_pty",
              "test_root": str(root),
              "source_sha": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=FIXTURE.parents[2], text=True).strip(),
              "pi_binary_sha256": hashlib.sha256(args.pi_executable.read_bytes()).hexdigest(),
              "host_os": platform.system(), "host_architecture": platform.machine(), "desktop_qualified": False}
    child, master, reader = None, None, None
    output_bytes, errors = [0], []
    output_head, output_tail = bytearray(), bytearray()
    try:
        if os.name == "nt":
            F.require(args.pty_bridge is not None, "native_conpty_bridge_required")
            bridge = args.pty_bridge.resolve(strict=True)
            report["conpty_bridge_sha256"] = hashlib.sha256(bridge.read_bytes()).hexdigest()
            child = subprocess.Popen([bridge, args.pi_executable, root / "project"], cwd=root / "project", env=env,
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        else:
            import fcntl
            import pty
            import struct
            import termios
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
            child = subprocess.Popen([args.pi_executable, "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools",
                                      "--provider", "TEST-local", "--model", "TEST-local", "--tui-mode", "regular", "TEST say done"],
                                     cwd=root / "project", env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
            os.close(slave)

        def drain():
            while True:
                try:
                    # read() can wait for a full buffer on a live Windows pipe;
                    # read1() retains actual partial startup output immediately.
                    block = child.stdout.read1(4096) if os.name == "nt" else os.read(master, 4096)
                except OSError:
                    break  # POSIX PTY returns EIO when its slave closes.
                if not block:
                    break
                output_bytes[0] += len(block)
                output_head.extend(block[:max(0, 8192 - len(output_head))])
                output_tail.extend(block)
                if len(output_tail) > 8192:
                    del output_tail[:-8192]
                if output_bytes[0] > 4 * 1024 * 1024:
                    errors.append("native_tui_output_limit")
                    break

        reader = threading.Thread(target=drain, daemon=True)
        reader.start()
        deadline = time.monotonic() + 20
        rows = []
        while time.monotonic() < deadline:
            if (root / "witness.jsonl").exists():
                rows = F.witness(root)
                if any(row["type"] == "agent_settled" for row in rows):
                    break
            F.require(child.poll() is None and not errors, "native_tui_exited_before_reply")
            time.sleep(0.05)
        F.require(any(row["type"] == "agent_settled" for row in rows), "native_tui_reply_timeout")
        F.require(all(row.get("mode") == "tui" for row in rows if row["type"] != "factory"), "native_tui_mode_not_observed")
        F.verify_case("success", rows, provider.requests)
        if os.name == "nt":
            child.stdin.write(b"/quit\r")
            child.stdin.flush()
        else:
            os.write(master, b"/quit\r")
        code = child.wait(timeout=10)
        if os.name == "nt":
            child.stdin.close()
        reader.join(timeout=2)
        F.require(code == 0 and not errors and not reader.is_alive(), "native_tui_shutdown_not_collected")
        rows = F.witness(root)
        F.require(any(row["type"] == "session_shutdown" and row.get("reason") == "quit" for row in rows), "native_tui_quit_missing")
        report.update(status="passed_actual_pi_tui", provider_requests=len(provider.requests),
                      native_counts=F.verify_case("success", rows, provider.requests), terminal_bytes=output_bytes[0], child_collected=True)
    except Exception as error:
        report["failure"] = str(error)
    finally:
        if child and child.poll() is None:
            if os.name == "nt":
                subprocess.run(["taskkill.exe", "/PID", str(child.pid), "/T", "/F"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
            else:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            child.wait(timeout=3)
        if master is not None:
            os.close(master)
        if reader:
            reader.join(timeout=2)
        provider.close()
        # These are partial observations, never a substitute for the successful
        # native-mode/settled/quit assertions above. The TEST transcript is bounded.
        report["startup_diagnostics"] = {
            "provider_requests": len(provider.requests), "console_bytes": output_bytes[0],
            "console_head": output_head.decode("utf-8", errors="replace"),
            "console_tail": output_tail.decode("utf-8", errors="replace"),
            "conpty_stages_in_console_stream": os.name == "nt",
            "child_returncode": child.poll() if child else None,
            "owned_child_collected": child is not None and child.poll() is not None,
            "reader_collected": reader is not None and not reader.is_alive(),
            "reader_errors": errors,
        }
        try:
            observed = F.witness(root) if (root / "witness.jsonl").exists() else []
            report["startup_diagnostics"].update(
                observed_counts=dict(Counter(row["type"] for row in observed)),
                observed_modes=sorted({row["mode"] for row in observed if "mode" in row}),
                observed_outcomes=[row.get("outcome") for row in observed if row["type"] == "agent_before_settle"],
            )
        except Exception as error:
            report["startup_diagnostics"]["witness_read_failure"] = str(error)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["status"] == "passed_actual_pi_tui" else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pi-executable", type=Path, required=True)
    parser.add_argument("--pty-bridge", type=Path)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    args.pi_executable = args.pi_executable.resolve(strict=True)
    raise SystemExit(execute(args))
