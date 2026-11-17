#!/usr/bin/env python3
"""Real Xvfb/dunst/Xterm product-daemon click qualification, not an agent CLI run."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time

REPO = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("native_desktop_preflight", REPO / "scripts/pi-native-preflight.py")
N = importlib.util.module_from_spec(spec)
spec.loader.exec_module(N)


def qualify(binary, candidate, output):
    root = Path(tempfile.mkdtemp(prefix="TEST-strict-")).resolve()
    root.chmod(0o700)
    lab_root = root / "desktop"
    lab_root.mkdir(mode=0o700)
    report = {"schema_version": 1, "scope": "native_strict_x11_callback_only",
              "product_cli_e2e": False, "status": "failed", "cases": []}
    lab = N.Lab(lab_root, report)
    try:
        _, _, windows = N.linux_desktop(lab, terminal_lifetime=120)
        distractor = windows[1]
        lab.env.update(XDG_SESSION_TYPE="x11", TERM="xterm-256color")
        lab.start([candidate, "--daemon"])
        socket = Path(lab.env["XDG_RUNTIME_DIR"]) / "claude-notifications.sock"
        N.wait_for(lambda: socket.exists(), seconds=10)
        def active():
            return lab.run(["xdotool", "getactivewindow"])[0].strip()
        def displayed():
            return int(lab.run(["dunstctl", "count", "displayed"])[0].strip())
        for scenario in ("positive", "revoke", "policy_revoke", "spoof"):
            lab.run(["dunstctl", "close-all"])
            lab.run(["dunstctl", "history-clear"])
            N.wait_for(lambda: displayed() == 0)
            environment = dict(lab.env)
            environment.update(TEST_STRICT_TERMINAL_NATIVE="1",
                               TEST_STRICT_TERMINAL_CASE=scenario,
                               TEST_STRICT_TERMINAL_ROOT=str(root),
                               TEST_STRICT_SPOOF_WINDOW=distractor)
            title = "TEST-strict-sender-" + scenario
            done = root / (scenario + "-done")
            cmd = ['xterm', '-T', title, '-geometry', '80x25+30+170', '-e',
                   'bash', '-c',
                   '"$1" -test.run=^TestStrictTerminalNativeSender$; printf "%s\\n" "$?" > "$2"; sleep 120',
                   'strict-test', str(binary), str(done)]
            proc = subprocess.Popen(cmd, env=environment, cwd=root,
                                    stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                    stderr=subprocess.DEVNULL, start_new_session=True)
            lab.children.append(proc)
            def find_sender():
                out, code = lab.run(["xdotool", "search", "--onlyvisible", "--name", "^" + re.escape(title) + "$"], check=False)
                windows = out.strip().splitlines()
                return windows[0] if code == 0 and len(windows) == 1 else None
            sender_window = N.wait_for(find_sender)
            N.wait_for(lambda: done.is_file(), seconds=25)
            result = done.read_text().strip()
            if result != "0":
                raise RuntimeError("native TEST sender exited nonzero: " + scenario + "/" + result)
            row = json.loads((root / (scenario + ".json")).read_text())
            N.wait_for(lambda: not Path("/proc/" + str(row["sender"])).exists(), seconds=8)
            lab.run(["xdotool", "windowactivate", "--sync", distractor])
            N.wait_for(lambda: active() == distractor)
            if scenario == "positive":
                # A forged ActionInvoked signal from a different D-Bus client
                # must NOT consume the strict click or focus the window.
                lab.run(["dbus-send", "--session", "--type=signal",
                         "/org/freedesktop/Notifications",
                         "org.freedesktop.Notifications.ActionInvoked",
                         f"uint32:{row['notification_id']}", "string:default"])
                time.sleep(0.4)
                if active() != distractor:
                    raise RuntimeError("forged D-Bus action changed focus")
            if scenario == "spoof":
                if row["focus"] or row["window"] != sender_window:
                    raise RuntimeError("forged window was accepted as a focus target")
                N.wait_for(lambda: displayed() == 1)
                # The daemon really displayed a banner, but it must have no
                # executable focus action, even if clicked by native input.
                lab.run(["xdotool", "mousemove", "1100", "70", "click", "1"])
                time.sleep(1)
                if active() != distractor:
                    raise RuntimeError("forged daemon banner focused a foreign terminal")
            else:
                if not row["focus"] or row["window"] != sender_window:
                    raise RuntimeError("native sender did not prove its own terminal")
                N.wait_for(lambda: displayed() == 1)
                if scenario == "revoke":
                    cfg = root / scenario / "config.json"
                    data = json.loads(cfg.read_text())
                    data["notifications"]["desktop"]["clickToFocus"] = False
                    tmp = cfg.with_suffix(".test-tmp")
                    tmp.write_text(json.dumps(data) + "\n")
                    os.chmod(tmp, 0o600)
                    os.replace(tmp, cfg)
                if scenario == "policy_revoke":
                    policy = root / scenario / "control" / "agent-notifications.json"
                    data = json.loads(policy.read_text())
                    data["enabled"] = False
                    tmp = policy.with_suffix(".test-tmp")
                    tmp.write_text(json.dumps(data) + "\n")
                    os.chmod(tmp, 0o600)
                    os.replace(tmp, policy)
                lab.run(["xdotool", "mousemove", "1100", "70", "click", "1"])
                if scenario == "positive":
                    N.wait_for(lambda: active() == sender_window, seconds=8)
                    got_pid = int(lab.run(["xdotool", "getwindowfocus", "getwindowpid"])[0].strip())
                    if got_pid != proc.pid:
                        raise RuntimeError("click focused a foreign native terminal")
                else:
                    time.sleep(1)
                    if active() != distractor:
                        raise RuntimeError("revoked consent still focused native terminal")
            report["cases"].append({"scenario": scenario, "status": "passed",
                                    "real_daemon": True, "physical_click": True,
                                    "focus_restored": scenario == "positive",
                                    "sender_exited_before_click": True,
                                    "forged_signal_ignored": scenario == "positive"})
            lab.run(["dunstctl", "close-all"])
            lab.stop(proc)
        report["status"] = "passed"
    finally:
        lab.close()
        report["owned_children_collected"] = report.get("owned_children_collected", False)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, sort_keys=True, indent=2) + "\n")
    if report["status"] != "passed" or not report["owned_children_collected"]:
        raise RuntimeError("native callback qualification incomplete")
    return report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--test-binary", type=Path, required=True)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    qualify(args.test_binary.resolve(strict=True), args.candidate.resolve(strict=True), args.report.resolve())
    print("NATIVE_STRICT_X11_CLICK_PASS", flush=True)


if __name__ == "__main__":
    main()
