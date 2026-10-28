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
CASES = ("completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin")


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
              "native_product_observations_passed": False, "cases": []}
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

    def case(name, distractor):
        case_root = root / ("case-" + name)
        provider = F.Provider("success", hold_reply=name != "question")
        recorder, terminal = None, None
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
                if click and name != "stale-origin":
                    N.wait_for(lambda: active() == origin and keyboard_pid() == terminal.pid)
                else:
                    time.sleep(0.5)
                    P.require(active() == distractor, "product_click_disabled_or_stale_target_activated")
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
            time.sleep(0.4)
            lab.stop(recorder)
            stats = bytes_stats(recording, baseline)
            expected_sound = sound and desktop
            P.require(stats["samples"] >= 4800, "product_audio_capture_incomplete")
            if expected_sound:
                P.require(stats["peak_s16"] > 1000 and stats["nonzero_samples"] >= 480, "actual_product_cue_not_observed")
            else:
                P.require(stats["peak_s16"] == 0, "product_sound_off_was_audible")
            if terminal.poll() is None:
                lab.run(["xdotool", "windowactivate", "--sync", origin])
                N.wait_for(lambda: active() == origin and keyboard_pid() == terminal.pid)
                if name == "question":
                    type_line("TEST accepted")
                    N.wait_for(lambda: any(x["type"] == "ui_prompt_end" for x in F.witness(case_root)))
                type_line("/quit")
                P.require(terminal.wait(timeout=10) == 0, "product_tui_not_collected")
            rows = F.witness(case_root)
            if name == "question":
                P.require(not provider.requests and sum(x["type"] == "ui_prompt_start" for x in rows) == 1
                          and sum(x["type"] == "ui_prompt_end" for x in rows) == 1
                          and not any(x["type"] == "agent_settled" for x in rows), "product_question_native_lifecycle_wrong")
            else:
                F.verify_case("success", rows, provider.requests)
            sink.quiet()
            report["cases"].append({"case": name, "native_mode": "tui", "banner_observed": desktop,
                "real_mouse_click": desktop, "origin_window": origin, "distractor_window": distractor,
                "foreground_after_click": foreground_after_click, "keyboard_pid_after_click": keyboard_after_click,
                "origin_xterm_pid": terminal.pid,
                "sound_expected": expected_sound, "audio": stats, "audio_sha256": P.sha(recording),
                "banner_screenshot_sha256": P.sha(screenshot) if desktop else None,
                "pending_question_at_click": name == "question", "ambient_root_was_foreign": True,
                "terminal_collected": terminal.poll() is not None})
        finally:
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
