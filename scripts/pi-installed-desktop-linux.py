#!/usr/bin/env python3
"""Actual installed Pi TUI -> native dunst banner/audio/mouse -> origin X11.

Uses the existing common setup writers and the small owned preflight session.
Only the model/webhook destination is substituted. No product extension, sender,
helper, callback or focus action is replaced. Three-OS acceptance stays separate.
"""
import argparse
import array
import ctypes
import importlib.util
import json
import math
import os
from pathlib import Path
import platform
import queue
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time

REPO = Path(__file__).resolve().parents[1]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


N = module("pi_preflight", REPO / "scripts/pi-native-preflight.py")
F = module("pi_native_fixture", REPO / "tests/integration/pi_native_fixture.py")
P = module("pi_installed_fixture", REPO / "tests/integration/pi_installed_fixture.py")
D = module("pi_installed_process", REPO / "scripts/pi-installed-process-e2e.py")
M = module("pi_audio_oracle", REPO / "scripts/pi-installed-desktop-macos.py")
CASES = ("completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin")


def process_identity(pid):
    """Read a Linux kernel birth identity, checking it across the executable read."""
    try:
        process = Path("/proc") / str(pid)
        before = (process / "stat").read_text().rsplit(")", 1)[1].split()
        executable = os.readlink(process / "exe")
        after = (process / "stat").read_text().rsplit(")", 1)[1].split()
        if before[19] != after[19] or after[0] == "Z":
            return None
        return {"pid": int(pid), "ppid": int(after[1]), "start_ticks": int(after[19]), "executable": executable}
    except (FileNotFoundError, ProcessLookupError):
        return None


def same_process(identity):
    current = process_identity(identity["pid"])
    return current is not None and current["start_ticks"] == identity["start_ticks"]


def process_collected(identity):
    """A reused PID cannot stand in for exit; collect an adopted zombie if ours."""
    try:
        fields = (Path("/proc") / str(identity["pid"]) / "stat").read_text().rsplit(")", 1)[1].split()
        if int(fields[19]) != identity["start_ticks"]:
            return True
        if fields[0] == "Z":
            try:
                return os.waitpid(identity["pid"], os.WNOHANG)[0] == identity["pid"]
            except ChildProcessError:
                return False  # Its original parent must finish joining it.
        return False
    except (FileNotFoundError, ProcessLookupError):
        return True


def process_children(identity):
    P.require(same_process(identity), "original_process_identity_changed")
    # /proc/PID/task/TID/children requires an optional kernel configuration.
    # Read kernel PPID fields instead; this also includes forks by Go threads.
    processes = [path for path in Path("/proc").iterdir() if path.name.isdecimal()]
    P.require(len(processes) <= 65536, "process_inventory_bound")
    children = []
    for path in processes:
        try:
            fields = (path / "stat").read_text().rsplit(")", 1)[1].split()
            if int(fields[1]) == identity["pid"]:
                child = process_identity(int(path.name))
                if child and child["ppid"] == identity["pid"]:
                    children.append(child)
        except (FileNotFoundError, ProcessLookupError):
            pass
    P.require(same_process(identity), "original_process_identity_changed")
    return children


def process_tree(identity):
    result, pending = [], [identity]
    while pending:
        current = pending.pop()
        P.require(len(result) < 64, "owned_helper_subtree_bound")
        if same_process(current):
            result.append(current)
            pending.extend(process_children(current))
    return result


def observe_unchanged_focus(active, keyboard_pid, window, window_pid, stop_capture, seconds=15):
    return P.observe_unchanged_focus(lambda: {"window": active(), "keyboard_pid": keyboard_pid()},
                                     {"window": window, "keyboard_pid": window_pid}, stop_capture,
                                     drain_seconds=0.4, seconds=seconds)


def run(args):
    P.require(platform.system() == "Linux", "actual_linux_host_required")
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-product-desktop-")).resolve()
    root.chmod(0o700)
    (root / "desktop").mkdir(mode=0o700)
    lab = N.Lab(root / "desktop", {})
    sink, initial_provider = P.Webhooks(), F.Provider("success")
    env = F.prepare(root, initial_provider, "success")
    profile = root / "agent"
    env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
    report = {"schema_version": 1, "scope": "actual_installed_pi_linux_desktop", "status": "failed",
              "test_root": str(root), "source_sha": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip(),
              "script_sha256": P.sha(__file__),
              "fixture_sha256": {name: P.sha(REPO / "tests/integration" / name)
                                  for name in ("pi_native_fixture.py", "pi_installed_fixture.py")},
              "working_tree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO)),
              "host_os": platform.system(), "host_architecture": platform.machine(),
              "candidate_binary_sha256": P.sha(args.candidate), "package_sha256": P.sha(args.package),
              "pi_binary_sha256": P.sha(args.pi_executable), "desktop_qualified": False,
              "native_product_observations_passed": False, "cases": [], "lifecycle_cases": []}
    # Adopt only this fixture's orphaned product daemon so cleanup can collect it.
    libc = ctypes.CDLL(None, use_errno=True)
    prior_subreaper = ctypes.c_int()
    P.require(libc.prctl(37, ctypes.byref(prior_subreaper), 0, 0, 0) == 0
              and libc.prctl(36, 1, 0, 0, 0) == 0, "owned_daemon_collection_unavailable")
    daemon_pid = None

    def desktop_env(base):
        value = dict(base)
        for key in ("DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "PULSE_SERVER", "XDG_RUNTIME_DIR"):
            value[key] = lab.env[key]
        value.update(XDG_SESSION_TYPE="x11", TERM="xterm-256color", LANG="C.UTF-8",
                     CLAUDE_PLUGIN_ROOT=str(root / "foreign-TEST-root"))
        (Path(value["HOME"]) / ".asoundrc").write_text('pcm.!default { type pulse }\nctl.!default { type pulse }\n')
        return value

    def active():
        return lab.run(["xdotool", "getactivewindow"])[0].strip()

    def displayed():
        return int(lab.run(["dunstctl", "count", "displayed"])[0].strip())

    def keyboard_pid():
        value, code = lab.run(["xdotool", "getwindowfocus", "getwindowpid"], check=False)
        return int(value.strip()) if code == 0 and value.strip().isdigit() else None

    def type_line(value):
        lab.run(["xdotool", "key", "ctrl+u"])
        lab.run(["xdotool", "type", "--clearmodifiers", "--delay", "40", value])
        lab.run(["xdotool", "key", "Return"])

    def bytes_stats(path, skip=0):
        raw = path.read_bytes()[skip:]
        values = array.array("h", raw[:len(raw) // 2 * 2])
        if sys.byteorder != "little":
            values.byteswap()
        return {"samples": len(values), "peak_s16": max((abs(x) for x in values), default=0),
                "rms_s16": round(math.sqrt(sum(x * x for x in values) / len(values)), 3) if values else 0,
                "nonzero_samples": sum(abs(x) > 128 for x in values)}

    def lifecycle_case(action):
        """Real installed Pi controls cancel already admitted real Go children."""
        case_root = root / ("case-lifecycle-" + action)
        provider, destination = F.Provider("success"), P.Webhooks(hold_response=True)
        terminal = None
        row = {"case": action, "scope": "actual_installed_pi_linux_tui", "status": "running", "phase": "tui_startup"}
        report["lifecycle_cases"].append(row)
        try:
            native_env = desktop_env(F.prepare(case_root, provider, "success", agent_dir=profile))
            P.configure(root / "global/config.json", args.assets_root / "config/config.json", destination,
                        desktop=False, sound=False, click=False)
            argv = ["xterm", "-T", "TEST Pi lifecycle " + action, "-geometry", "100x35+20+150", "-e",
                    args.pi_executable, "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools",
                    "--provider", "TEST-local", "--model", "TEST-local", "--tui-mode", "regular"]
            terminal = subprocess.Popen([str(x) for x in argv], cwd=case_root / "project", env=native_env,
                                        stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                        start_new_session=True)
            lab.children.append(terminal)
            def window():
                out, code = lab.run(["xdotool", "search", "--onlyvisible", "--pid", str(terminal.pid)], check=False)
                found = out.strip().splitlines()
                return found[0] if code == 0 and len(found) == 1 else None
            origin = N.wait_for(window)
            N.wait_for(lambda: any(x["type"] == "session_start" for x in F.witness(case_root)))
            factory = next(x for x in F.witness(case_root) if x["type"] == "factory")
            pi = process_identity(factory["pid"])
            P.require(pi is not None and pi["executable"] == str(args.pi_executable), "native_pi_kernel_identity_missing")
            def helper_children():
                return sorted((child for child in process_children(pi)
                               if child["executable"] == str(root / "runtime/bin" / args.candidate.name)),
                              key=lambda child: child["pid"])
            lab.run(["xdotool", "windowactivate", "--sync", origin])
            N.wait_for(lambda: active() == origin and keyboard_pid() == terminal.pid)
            count = 3 if action == "reload" else 1
            row["phase"] = "admit_held_work"
            admitted = []
            row["helper_admission_counts"] = []
            for index in range(count):
                started = time.monotonic()
                type_line("/TEST-dialog" if action == "prompt-end" else "TEST " + str(index))
                event = "ui_prompt_start" if action == "prompt-end" else "agent_settled"
                N.wait_for(lambda: sum(x["type"] == event for x in F.witness(case_root)) == index + 1, seconds=5)
                P.require(time.monotonic() - started < 5, "native_pi_settlement_or_prompt_stalled")
                row.update(observations_before_control=index + 1, admitted_webhook_requests=len(destination.rows))
                if index == 0:
                    destination.wait("question" if action == "prompt-end" else "task_complete", timeout=5)
                # The first HTTP handoff retains the exclusive installation
                # lease. The second real Go child waits on that same lease;
                # it cannot submit a second request until the first releases.
                helpers = N.wait_for(lambda: (children if len(children := helper_children()) == min(index + 1, 2) else None), seconds=3)
                if index < 2:
                    P.require(all(child in helpers for child in admitted), "native_pi_original_helper_exited_before_control")
                    admitted = helpers
                else:
                    P.require(helpers == admitted, "native_pi_excess_observation_replaced_original_helpers")
                row["helper_admission_counts"].append(len(helpers))
                row["admitted_webhook_requests"] = len(destination.rows)
            destination.quiet(seconds=0.3)
            helpers = helper_children()
            expected = 2 if action == "reload" else 1
            P.require(helpers == admitted and len(helpers) == expected and len(destination.rows) == 1,
                      "native_pi_original_helper_admission_bound_unobserved")
            owned = [identity for helper in helpers for identity in process_tree(helper)]
            P.require(len(owned) >= expected, "native_pi_helper_identity_disappeared_before_control")
            row.update(pi_identity=pi, helper_identities=owned, admitted_helpers=len(helpers),
                       admitted_webhook_requests=len(destination.rows),
                       excess_observation_dropped=action == "reload", settled_before_control=count if action != "prompt-end" else 0)
            started = time.monotonic()
            row["phase"] = "natural_timeout" if action == "timeout" else "original_child_cancellation"
            if action == "timeout":
                # Production HTTP client timeout is 10s, below Pi's 20s consumer
                # and 21s producer bounds. Keep the response held; send no control.
                N.wait_for(lambda: all(process_collected(identity) for identity in owned), seconds=13)
                uptime = float(Path("/proc/uptime").read_text().split()[0])
                lifetime = uptime - helpers[0]["start_ticks"] / os.sysconf("SC_CLK_TCK")
                P.require(9 <= lifetime < 13, "actual_webhook_original_timeout_not_observed")
                P.require(same_process(pi), "helper_timeout_ended_pi")
                row.update(natural_timeout_seconds=round(lifetime, 3), production_http_timeout_seconds=10,
                           no_control_before_timeout=True)
            else:
                type_line("TEST accepted" if action == "prompt-end" else "/" + action)
                N.wait_for(lambda: all(process_collected(identity) for identity in owned), seconds=3)
                row["cancellation_seconds"] = round(time.monotonic() - started, 3)
                P.require(row["cancellation_seconds"] < 3, "native_pi_helper_cancellation_was_not_prompt")
            row["original_helper_subtree_collected"] = True
            # Cancellation cannot retract a second already-admitted handoff
            # that raced the first child's lease release. Collect its request
            # before submitting fresh work; no third/queued effect is allowed.
            while True:
                try:
                    old = destination.events.get(timeout=0.3)
                except queue.Empty:
                    break
                P.require(not destination.failures and old["status"] == "task_complete"
                          and len(destination.rows) <= expected, "native_pi_unadmitted_old_webhook")
            old_requests = [value["request_id"] for value in destination.rows]
            P.require(1 <= len(old_requests) <= expected and len(set(old_requests)) == len(old_requests)
                      and all(re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", value) for value in old_requests),
                      "native_pi_old_request_identity_missing_or_repeated")
            row["original_webhook_request_ids"] = old_requests
            if action == "prompt-end":
                N.wait_for(lambda: any(x["type"] == "ui_prompt_end" for x in F.witness(case_root)))
                P.require(not provider.requests, "native_prompt_end_started_model_work")
            elif action == "quit":
                P.require(terminal.wait(timeout=3) == 0 and not same_process(pi), "native_tui_quit_not_collected")
            elif action in ("new", "reload"):
                row["phase"] = "fresh_observer_after_reset"
                N.wait_for(lambda: any(x["type"] == "session_shutdown" and x.get("reason") == action
                                      for x in F.witness(case_root)))
                N.wait_for(lambda: sum(x["type"] == "factory" for x in F.witness(case_root)) >= 2)
                N.wait_for(lambda: any(x["type"] == "session_start" and x.get("reason") == action
                                      for x in F.witness(case_root)))
                P.require(same_process(pi), "native_session_reset_replaced_pi_process")
                destination.release.set()
                fresh_started = time.monotonic()
                type_line("TEST fresh")
                fresh = destination.wait("task_complete", timeout=5)
                P.require(fresh["received_monotonic"] >= fresh_started and fresh["request_id"] not in old_requests
                          and re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", fresh["request_id"]),
                          "native_pi_fresh_webhook_not_attributed_to_reset_observer")
                row.update(fresh_webhook_request_id=fresh["request_id"], fresh_webhook_received_after_start=True)
                N.wait_for(lambda: sum(x["type"] == "agent_settled" for x in F.witness(case_root)) == count + 1)
                N.wait_for(lambda: not [child for child in process_children(pi)
                                       if child["executable"] == str(root / "runtime/bin" / args.candidate.name)])
                row["fresh_observer_delivered_once"] = True
            destination.quiet(seconds=0.3)
            P.require(len(destination.rows) == len(old_requests) + (1 if action in ("new", "reload") else 0),
                      "native_pi_cancelled_helper_was_resent")
            if terminal.poll() is None:
                row["phase"] = "terminal_collection"
                type_line("/quit")
                P.require(terminal.wait(timeout=3) == 0, "native_lifecycle_terminal_not_collected")
            row.update(status="passed", phase="complete", native_control=None if action == "timeout" else action,
                       terminal_collected=True, no_resend=True)
        finally:
            if terminal:
                lab.stop(terminal)
            destination.close()
            provider.close()

    def case(name, distractor):
        case_root = root / ("case-" + name)
        provider = F.Provider("success", hold_reply=name != "question")
        recorder, terminal, mux_identity, mux_pane = None, None, None, None
        mux_socket = case_root / "tmux.sock"
        multiplexed = name == "tmux-completion"
        progress = {"case": name, "status": "running", "phase": "initialization"}
        if multiplexed:
            report["multiplexer"] = progress

        def tmux(*atoms):
            return lab.run(["tmux", "-S", mux_socket, *atoms])[0].strip()
        sound, click, desktop = name != "sound-off", name != "click-off", name != "desktop-off"
        try:
            native_env = desktop_env(F.prepare(case_root, provider, "success", agent_dir=profile))
            native_env["TEST_PI_DEFER_DIALOG"] = "1"
            P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink,
                        desktop=desktop, sound=sound, click=click)
            lab.run(["dunstctl", "close-all"])
            lab.run(["dunstctl", "history-clear"])
            P.require(displayed() == 0, "product_notification_baseline_not_empty")
            title = "TEST Pi installed origin " + name
            argv = ["xterm", "-T", title, "-geometry", "100x35+20+150", "-e", args.pi_executable,
                    "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools",
                    "--provider", "TEST-local", "--model", "TEST-local", "--tui-mode", "regular"]
            if name != "question":
                argv.append("TEST say done")
            if multiplexed:
                P.require(len(str(mux_socket).encode()) < 104, "owned_tmux_socket_path_bound")
                argv = argv[:6] + ["tmux", "-S", mux_socket, "-f", "/dev/null", "new-session", "-s", "TEST", "-n", "origin", "--"] + argv[6:]
            terminal = subprocess.Popen([str(x) for x in argv], cwd=case_root / "project", env=native_env,
                                        stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                        start_new_session=True)
            lab.children.append(terminal)

            def find_origin():
                out, code = lab.run(["xdotool", "search", "--onlyvisible", "--pid", str(terminal.pid)], check=False)
                found = out.strip().splitlines()
                return found[0] if code == 0 and len(found) == 1 else None

            origin = N.wait_for(find_origin)
            N.wait_for(lambda: (case_root / "witness.jsonl").exists()
                       and any(x["type"] == "session_start" for x in F.witness(case_root)))
            P.require(all(x.get("mode") == "tui" for x in F.witness(case_root) if x["type"] != "factory"), "product_pi_not_actual_tui")
            if multiplexed:
                mux_identity = process_identity(int(tmux("display-message", "-p", "#{pid}")))
                P.require(mux_identity is not None and Path(mux_identity["executable"]).name == "tmux",
                          "owned_tmux_server_identity_unavailable")
                mux_pane = tmux("display-message", "-p", "-t", "TEST:origin", "#{pane_id}")
                pane_pid = int(tmux("display-message", "-p", "-t", "TEST:origin", "#{pane_pid}"))
                P.require(next(x for x in F.witness(case_root) if x["type"] == "factory")["pid"] == pane_pid,
                          "tmux_origin_pane_is_not_actual_pi")
                progress.update(origin_window=origin, origin_xterm_pid=terminal.pid, origin_tmux_pane=mux_pane,
                                tmux_server_identity=mux_identity, origin_pi_pid=pane_pid)
                tmux("new-window", "-d", "-t", "TEST", "-n", "distractor", "/bin/sleep", "300")
                tmux("select-window", "-t", "TEST:distractor")
                P.require(tmux("display-message", "-p", "#{pane_id}") != mux_pane, "tmux_pane_not_backgrounded")
                progress["active_pane_before_click"] = tmux("display-message", "-p", "#{pane_id}")
            recording = case_root / "native-output.s16le"
            with recording.open("wb") as stream:
                recorder = subprocess.Popen(["parec", "--device=TEST_pi_observer.monitor", "--format=s16le", "--rate=48000",
                                             "--channels=1", "--latency-msec=20"], env=lab.env, cwd=lab.root,
                                            stdout=stream, stderr=subprocess.DEVNULL, start_new_session=True)
            lab.children.append(recorder)
            N.wait_for(lambda: recording.stat().st_size >= 9600)
            baseline = recording.stat().st_size
            if name == "question":
                lab.run(["xdotool", "windowactivate", "--sync", origin])
                N.wait_for(lambda: active() == origin and keyboard_pid() == terminal.pid)
                type_line("/TEST-dialog")
            else:
                N.wait_for(lambda: bool(provider.requests))
            lab.run(["xdotool", "windowactivate", "--sync", distractor])
            P.require(active() == distractor, "product_origin_not_backgrounded")
            provider.release.set()
            sink.wait("question" if name == "question" else "task_complete")
            click_observation = None
            if desktop:
                N.wait_for(lambda: displayed() == 1)
                screenshot = case_root / "actual-product-banner.png"
                lab.run(["ffmpeg", "-loglevel", "error", "-f", "x11grab", "-video_size", "1280x800", "-i", lab.env["DISPLAY"], "-frames:v", "1", screenshot])
                P.require(active() == distractor, "product_focused_before_real_click")
                if name == "question":
                    P.require(any(x["type"] == "ui_prompt_start" for x in F.witness(case_root))
                              and not any(x["type"] == "ui_prompt_end" for x in F.witness(case_root)), "product_question_not_pending_at_click")
                if name == "stale-origin":
                    lab.stop(terminal)
                    N.wait_for(lambda: terminal.poll() is not None)
                lab.run(["xdotool", "mousemove", "1100", "70", "click", "1"])
                progress["phase"] = "outer_window_focus_after_click"
                if click and name != "stale-origin":
                    N.wait_for(lambda: active() == origin and keyboard_pid() == terminal.pid)
                    if multiplexed:
                        progress["phase"] = "documented_tmux_window_fallback"
                        progress["active_pane_after_click"] = tmux("display-message", "-p", "#{pane_id}")
                        P.require(progress["active_pane_after_click"] == progress["active_pane_before_click"],
                                  "linux_tmux_unsupported_pane_was_changed")
                else:
                    decoy_pid = int(lab.run(["xdotool", "getwindowpid", distractor])[0].strip())
                    click_observation = observe_unchanged_focus(active, keyboard_pid, distractor, decoy_pid,
                                                                 lambda: lab.stop(recorder))
                foreground_after_click = active()
                keyboard_after_click = keyboard_pid()
                # dunst may keep an action-bearing banner displayed after its
                # action. Observe real focus first, then dismiss this one owned
                # banner so the native history exposes its original content.
                lab.run(["dunstctl", "close-all"])
                N.wait_for(lambda: displayed() == 0)
                history = json.loads(lab.run(["dunstctl", "history"])[0])["data"][0]
                P.require(len(history) == 1 and P.COPY["question" if name == "question" else "task_complete"]
                          in history[0]["body"]["data"], "actual_product_banner_body_not_observed")
            else:
                time.sleep(1)
                P.require(displayed() == 0 and active() == distractor, "product_desktop_off_had_native_effect")
                foreground_after_click = active()
                keyboard_after_click = keyboard_pid()
            # Receiving the independently captured webhook follows the product's
            # synchronous desktop/audio attempt; drain the monitor's PCM buffer.
            if click_observation is None:
                time.sleep(0.4)
            lab.stop(recorder)
            stats = bytes_stats(recording, baseline)
            expected_sound = sound and desktop
            correlation = None
            P.require(stats["samples"] >= 4800, "product_audio_capture_incomplete")
            if expected_sound:
                P.require(stats["peak_s16"] > 1000 and stats["nonzero_samples"] >= 480, "actual_product_cue_not_observed")
                reference = case_root / "configured-reference.wav"
                lab.run(["ffmpeg", "-loglevel", "error", "-i", root / "runtime/sounds" / ("question.mp3" if name == "question" else "task-complete.mp3"),
                         "-ac", "1", "-ar", "48000", "-c:a", "pcm_s16le", reference])
                correlation = M.envelope_match(recording, reference)
                P.require(correlation >= 0.65, "captured_audio_not_configured_cue")
            else:
                P.require(stats["peak_s16"] == 0, "product_sound_off_was_audible")
            if terminal.poll() is None:
                progress["phase"] = "terminal_shutdown"
                lab.run(["xdotool", "windowactivate", "--sync", origin])
                N.wait_for(lambda: active() == origin and keyboard_pid() == terminal.pid)
                if name == "question":
                    type_line("TEST accepted")
                    N.wait_for(lambda: any(x["type"] == "ui_prompt_end" for x in F.witness(case_root)))
                if multiplexed:
                    # Cleanup only: pane selection is unsupported on Linux.
                    # The real click's window/pane result was recorded above.
                    P.require(tmux("display-message", "-p", "#{pane_id}") == progress["active_pane_before_click"],
                              "linux_tmux_unsupported_pane_was_changed")
                    tmux("select-window", "-t", "TEST:origin")
                type_line("/quit")
                if multiplexed:
                    N.wait_for(lambda: mux_pane not in tmux("list-panes", "-a", "-F", "#{pane_id}").splitlines())
                    P.require(same_process(mux_identity), "owned_tmux_server_identity_changed")
                    tmux("kill-server")
                    N.wait_for(lambda: process_collected(mux_identity))
                P.require(terminal.wait(timeout=10) == 0, "product_tui_not_collected")
            rows = F.witness(case_root)
            if name == "question":
                P.require(not provider.requests and sum(x["type"] == "ui_prompt_start" for x in rows) == 1
                          and sum(x["type"] == "ui_prompt_end" for x in rows) == 1
                          and not any(x["type"] == "agent_settled" for x in rows), "product_question_native_lifecycle_wrong")
            else:
                F.verify_case("success", rows, provider.requests)
            sink.quiet()
            row = {"case": name, "native_mode": "tui", "banner_observed": desktop,
                "real_mouse_click": desktop, "origin_window": origin, "distractor_window": distractor,
                "foreground_after_click": foreground_after_click, "keyboard_pid_after_click": keyboard_after_click,
                "unchanged_click_observation": click_observation,
                "origin_xterm_pid": terminal.pid,
                "sound_expected": expected_sound, "audio": stats, "audio_sha256": P.sha(recording),
                "configured_cue_envelope_match": correlation,
                "banner_screenshot_sha256": P.sha(screenshot) if desktop else None,
                "pending_question_at_click": name == "question", "ambient_root_was_foreign": True,
                "terminal_collected": terminal.poll() is not None,
                "checks": {"native_tui": True, "configured_banner_policy": True, "configured_audio_policy": True,
                           "configured_click_policy": True, "native_event_counts": True,
                           "ambient_root_isolated": True, "owned_terminal_collected": terminal.poll() is not None}}
            if multiplexed:
                row.update(origin_tmux_pane=mux_pane, tmux_server_identity=mux_identity,
                           pane_focus_supported=False, outer_window_focus_observed=True,
                           active_pane_before_click=progress["active_pane_before_click"],
                           active_pane_after_click=progress["active_pane_after_click"],
                           owned_tmux_server_collected=process_collected(mux_identity), status="passed")
                report["multiplexer"] = row
            else:
                report["cases"].append(row)
        finally:
            if multiplexed and report["multiplexer"].get("status") != "passed":
                try:
                    progress["foreground_after_failure"] = {"window": active(), "keyboard_pid": keyboard_pid()}
                    if mux_identity and same_process(mux_identity):
                        progress["active_pane_after_failure"] = tmux("display-message", "-p", "#{pane_id}")
                    progress["status"] = "failed"
                except Exception as error:
                    progress["diagnostic_error_class"] = type(error).__name__
            if mux_identity:
                if same_process(mux_identity):
                    tmux("kill-server")
                N.wait_for(lambda: process_collected(mux_identity))
            if terminal:
                lab.stop(terminal)
            if recorder:
                lab.stop(recorder)
            provider.close()

    try:
        paths, server, distractors = N.linux_desktop(lab, terminal_lifetime=600)
        env = desktop_env(env)
        (root / "foreign-TEST-root").mkdir()
        P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink)
        D.command(args.candidate, ["internal-install-runtime", "--stage", args.runtime_stage, "--target", root / "runtime/bin",
                                  "--entry", args.candidate.name, "--control-root", root / "control",
                                  "--consumer", "pi-runtime-preparation"], root, env)
        P.setup(args.candidate, "install", root, args.package, args.pi_executable, env,
                ("--hooks", "false", "--agent-notify", "true", "--pi-desktop", "true", "--pi-webhook", "true"))
        policy = json.loads((root / "control/agent-notifications.json").read_text())
        P.require(policy.get("enabled") is False, "pi_setup_enabled_unrelated_mcp_policy")
        report["unrelated_mcp_policy_enabled"] = False
        view = P.installed(root / "control", profile)
        P.require(P.sha(view["runtime"]["executable"]) == P.sha(args.candidate), "product_installed_candidate_differs")
        report["installed_asset_sha256"] = P.asset_receipts(root / "control", root / "runtime", args.assets_root)
        report["active_leaf"] = view["active"].name
        report["cue_sha256"] = {name: P.sha(root / "runtime/sounds" / name) for name in ("task-complete.mp3", "question.mp3")}
        for name in CASES:
            case(name, distractors[-1])
        for action in ("reload", "new", "prompt-end", "quit", "timeout"):
            lifecycle_case(action)
        case("tmux-completion", distractors[-1])
        report.update(status="passed_native_product_observations", native_product_observations_passed=True)
    except Exception as error:
        report["failure"] = str(error)
    finally:
        # Stop only the daemon proved to belong to this TEST session and writer.
        pid_files = list((lab.root / "runtime").glob("claude-notifications*.pid"))
        for pid_file in pid_files:
            try:
                daemon_pid = int(pid_file.read_text().strip())
                executable = Path(os.readlink("/proc/" + str(daemon_pid) + "/exe"))
                P.require(executable == root / "runtime/bin" / args.candidate.name, "foreign_daemon_cleanup_refused")
                os.kill(daemon_pid, signal.SIGTERM)
                deadline = time.monotonic() + 3
                while time.monotonic() < deadline:
                    pid, _ = os.waitpid(daemon_pid, os.WNOHANG)
                    if pid == daemon_pid:
                        break
                    time.sleep(0.05)
                else:
                    os.kill(daemon_pid, signal.SIGKILL)
                    os.waitpid(daemon_pid, 0)
            except (FileNotFoundError, ProcessLookupError):
                pass
            except (OSError, ValueError, RuntimeError) as error:
                report.update(status="failed", native_product_observations_passed=False,
                              cleanup_failure=str(error))
        lab.close()
        initial_provider.close()
        sink.close()
        libc.prctl(36, prior_subreaper.value, 0, 0, 0)
        report["owned_children_collected"] = lab.report.get("owned_children_collected", False)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        try:
            artifact_root = args.report.parent / "native-captures"
            for case_root in root.glob("case-*"):
                for name in ("actual-product-banner.png", "native-output.s16le"):
                    source = case_root / name
                    if source.is_file() and not source.is_symlink():
                        target = artifact_root / case_root.name / name
                        target.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copy2(source, target)
        except OSError as error:
            report.update(status="failed", native_product_observations_passed=False, artifact_failure=str(error))
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["native_product_observations_passed"] and report["owned_children_collected"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "runtime-stage", "package", "assets-root", "pi-executable", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    for name in ("candidate", "runtime_stage", "package", "assets_root", "pi_executable"):
        setattr(args, name, getattr(args, name).resolve(strict=True))
    raise SystemExit(run(args))
