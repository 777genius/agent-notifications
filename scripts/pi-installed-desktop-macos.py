#!/usr/bin/env python3
"""Actual common-install Pi TUI -> macOS banner/audio/click -> exact Terminal.

Only the loopback model and webhook destination are fixtures. Product source,
authority, native helper, sound and click callback use the normal owned runtime.
Missing GUI/permission/audio observations fail; this lane never grants the final
three-platform desktop qualification itself.
"""
import argparse
import array
import importlib.util
import json
import math
import os
from pathlib import Path
import platform
import shlex
import shutil
import signal
import subprocess
import tempfile
import time
import uuid
import wave

REPO = Path(__file__).resolve().parents[1]
CASES = ("completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin")
HELPER_SHA = "4e23f2870e1cebd565f3f00a459fe0d6f909109226af7761be60933d819e59cc"
HELPER_SOURCE = "c96f5152b7d95322731dcefa535c561c1c94309a"


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


F = module("pi_native_fixture", REPO / "tests/integration/pi_native_fixture.py")
P = module("pi_installed_fixture", REPO / "tests/integration/pi_installed_fixture.py")
D = module("pi_installed_process", REPO / "scripts/pi-installed-process-e2e.py")


def wait_for(predicate, reason, seconds=12):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError(reason)


def command(argv, *, timeout=20):
    result = subprocess.run(list(map(str, argv)), stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    P.require(len(result.stdout) + len(result.stderr) < 1024 * 1024, "native_observer_output_limit")
    P.require(result.returncode == 0, "native_command_failed:" + Path(argv[0]).name + ":"
              + result.stderr.decode(errors="replace")[-1000:] + result.stdout.decode(errors="replace")[-1000:])
    return result.stdout.decode()


def applescript(source):
    return command(["/usr/bin/osascript", "-e", source]).strip()


def quoted(value):
    # AppleScript string syntax. Shell arguments inside it are independently
    # escaped by shlex.quote before this language boundary.
    return '"' + str(value).replace("\\", "\\\\").replace('"', '\\"') + '"'


class Terminal:
    """An owned real Terminal window and its observed native TTY/Pi PID."""
    def __init__(self, root, argv, env, observer, cwd=None):
        self.root, self.observer, self.closed = root, observer, False
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.pid_path, self.exit_path = root / "process.pid", root / "process.exit"
        self.shell_path = root / "shell.pid"
        script = root / "launch.command"
        entries = [shlex.quote(key + "=" + value) for key, value in env.items()]
        launch = " ".join(["/usr/bin/env", "-i", *entries,
                           'TERM_PROGRAM=Apple_Terminal', 'TERM=xterm-256color',
                           'TERM_SESSION_ID="${TERM_SESSION_ID:-}"', *map(shlex.quote, map(str, argv))])
        cwd = cwd or root / "project"
        script.write_text("#!/bin/bash\nset -eu\nprintf '%s\\n' \"$$\" > " + shlex.quote(str(self.shell_path))
                          + "\ncd " + shlex.quote(str(cwd)) + "\nset +e\n" + launch
                          + " &\nchild=$!\nprintf '%s\\n' \"$child\" > " + shlex.quote(str(self.pid_path))
                          + "\nwait \"$child\"\ncode=$?\nprintf '%s\\n' \"$code\" > "
                          + shlex.quote(str(self.exit_path)) + "\nexit 0\n")
        script.chmod(0o700)
        result = applescript('tell application "Terminal"\nset t to do script '
                             + quoted("/bin/bash --noprofile --norc " + shlex.quote(str(script)))
                             + '\nset custom title of t to ' + quoted(cwd.name)
                             + '\nset w to front window\nreturn (id of w as text) & "|" & (tty of t as text)\nend tell')
        window, self.tty = result.split("|", 1)
        P.require(window.isdecimal() and self.tty.startswith("/dev/ttys"), "native_terminal_identity_unavailable")
        self.window = int(window)
        wait_for(self.pid_path.exists, "native_pi_pid_missing")
        self.pid = int(self.pid_path.read_text())
        self.shell_pid = int(self.shell_path.read_text())

    def focus(self):
        applescript('tell application "Terminal"\nactivate\nset index of window id '
                    + str(self.window) + ' to 1\nend tell')
        wait_for(lambda: self.is_front(), "owned_terminal_could_not_be_foregrounded")

    def front(self):
        native = self.observer("foreground")
        if native.get("bundle_id") != "com.apple.Terminal":
            return {"native": native, "window": None, "tty": None}
        value = applescript('tell application "Terminal"\nreturn (id of front window as text) & "|" & '
                            '(tty of selected tab of front window as text)\nend tell')
        window, tty = value.split("|", 1)
        return {"native": native, "window": int(window), "tty": tty}

    def is_front(self):
        value = self.front()
        return value["window"] == self.window and value["tty"] == self.tty

    def type(self, text):
        P.require(self.is_front(), "physical_input_target_is_not_owned_terminal")
        self.observer("type", text)

    def collected(self):
        if not self.exit_path.exists():
            return False
        try:
            os.kill(self.pid, 0)
            return False
        except ProcessLookupError:
            return True

    def close(self):
        if self.closed:
            return
        if self.pid_path.exists() and not self.collected():
            try:
                os.kill(self.pid, signal.SIGTERM)
                wait_for(self.collected, "owned_terminal_process_not_collected", seconds=3)
            except (ProcessLookupError, RuntimeError):
                try:
                    os.kill(self.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                wait_for(self.collected, "owned_terminal_process_not_reaped", seconds=3)
        # Only the exact window/TTY created above is eligible for fixture close.
        applescript('tell application "Terminal"\nif exists window id ' + str(self.window)
                    + ' then\nif (tty of selected tab of window id ' + str(self.window) + ') is '
                    + quoted(self.tty) + ' then close window id ' + str(self.window) + ' saving no\nend if\nend tell')
        self.closed = True


def samples(path):
    raw = path.read_bytes()
    values = array.array("h", raw[:len(raw) // 2 * 2])
    if __import__("sys").byteorder != "little":
        values.byteswap()
    return values


def envelope_match(recording, reference):
    """Compare the observed cue's 10ms energy contour with its actual asset.

    This checks configured cue identity in addition to recorded energy. The
    reference is decoded only, never played. Centered cosine tolerates the
    capture's native gain and OS resampling while rejecting a flat ambient tone.
    """
    with wave.open(str(reference)) as source:
        P.require(source.getframerate() == 48000 and source.getnchannels() == 1 and source.getsampwidth() == 2,
                  "configured_cue_reference_format")
        ref = array.array("h", source.readframes(source.getnframes()))
    data = samples(recording)
    def bins(values):
        return [math.sqrt(sum(x * x for x in values[i:i + 480]) / 480)
                for i in range(0, len(values) - 479, 480)]
    target, actual = bins(ref), bins(data)
    while target and target[-1] < 8:
        target.pop()
    while target and target[0] < 8:
        target.pop(0)
    P.require(len(target) >= 3 and len(actual) >= len(target), "configured_cue_recording_too_short")
    mean = sum(target) / len(target)
    target = [x - mean for x in target]
    target_norm = math.sqrt(sum(x * x for x in target))
    P.require(target_norm > 0, "configured_cue_reference_has_no_contour")
    best = 0
    for offset in range(len(actual) - len(target) + 1):
        segment = actual[offset:offset + len(target)]
        mean = sum(segment) / len(segment)
        segment = [x - mean for x in segment]
        norm = math.sqrt(sum(x * x for x in segment))
        if norm > 0:
            best = max(best, sum(a * b for a, b in zip(segment, target)) / (norm * target_norm))
    return round(best, 6)


def run(args):
    P.require(platform.system() == "Darwin", "actual_macos_host_required")
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-installed-macos-")).resolve()
    root.chmod(0o700)
    sink, initial_provider = P.Webhooks(), F.Provider("success")
    env = F.prepare(root, initial_provider, "success")
    env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
    profile, terminals = root / "agent", []
    report = {"schema_version": 1, "scope": "actual_installed_pi_macos_desktop", "status": "failed",
              "test_root": str(root), "source_sha": command(["git", "-C", REPO, "rev-parse", "HEAD"]).strip(),
              "working_tree_dirty": bool(command(["git", "-C", REPO, "status", "--porcelain"])),
              "script_sha256": P.sha(__file__), "observer_source_sha256": P.sha(REPO / "tests/integration/pi_installed_desktop_darwin.swift"),
              "observer_binary_sha256": P.sha(args.observer),
              "fixture_sha256": {name: P.sha(REPO / "tests/integration" / name)
                                  for name in ("pi_native_fixture.py", "pi_installed_fixture.py")},
              "host_os": platform.system(), "host_architecture": platform.machine(),
              "candidate_binary_sha256": P.sha(args.candidate), "package_sha256": P.sha(args.package),
              "pi_binary_sha256": P.sha(args.pi_executable), "pi_acquisition": json.loads(args.pi_acquisition.read_text()),
              "helper": {"release": "v1.49.10", "source_tree": HELPER_SOURCE,
                         "archive_sha256": P.sha(args.helper_archive)},
              "desktop_qualified": False, "native_product_observations_passed": False,
              "native_helper_verified": False, "cases": []}

    def observer(*argv):
        return json.loads(command([args.observer, *argv]))

    def case(name, decoy):
        case_root = root / ("case-" + name)
        provider = F.Provider("success", hold_reply=name != "question")
        terminal, recorder = None, None
        desktop, sound, click = name != "desktop-off", name != "sound-off", name != "click-off"
        expected = "question" if name == "question" else "task_complete"
        try:
            native_env = F.prepare(case_root, provider, "success", agent_dir=profile)
            native_env.update(AGENT_NOTIFICATIONS_CONFIG=str(root / "global/config.json"), TEST_PI_DEFER_DIALOG="1",
                              CLAUDE_PLUGIN_ROOT=str(root / "foreign-TEST-root"))
            P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink,
                        desktop=desktop, sound=sound, click=click)
            P.require(not observer("banner", P.COPY[expected])["matches"], "candidate_banner_baseline_not_empty")
            argv = [args.pi_executable, "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools",
                    "--provider", "TEST-local", "--model", "TEST-local", "--tui-mode", "regular"]
            if name != "question":
                argv.append("TEST say done")
            cwd = case_root / "project" / ("TEST-Pi-" + name)
            cwd.mkdir()
            terminal = Terminal(case_root, argv, native_env, observer, cwd)
            terminals.append(terminal)
            wait_for(lambda: (case_root / "witness.jsonl").exists()
                     and any(x["type"] == "session_start" for x in F.witness(case_root)), "actual_pi_tui_start_missing")
            rows = F.witness(case_root)
            P.require(any(x["type"] == "factory" and x["pid"] == terminal.pid for x in rows)
                      and all(x.get("mode") == "tui" for x in rows if x["type"] != "factory"), "actual_native_pi_tui_identity_wrong")
            recorder = subprocess.Popen([str(args.observer), "capture", str(case_root)], stdin=subprocess.DEVNULL,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            wait_for((case_root / "capture-ready.json").exists, "native_product_capture_not_ready")
            baseline = json.loads((case_root / "capture-ready.json").read_text())
            P.require(baseline["audio_peak"] < 0.0001, "native_audio_baseline_not_quiet")
            if name == "question":
                terminal.focus()
                terminal.type("/TEST-dialog")
            else:
                wait_for(lambda: bool(provider.requests), "actual_pi_model_request_missing")
            decoy.focus()
            P.require(decoy.is_front() and not terminal.is_front(), "native_pi_origin_not_backgrounded")
            provider.release.set()
            screenshot, webhook_received = None, False
            if desktop:
                banner = wait_for(lambda: observer("banner", P.COPY[expected])["matches"], "actual_product_banner_not_observed")
                P.require(decoy.is_front(), "product_focused_before_physical_notification_click")
                if name == "question":
                    rows = F.witness(case_root)
                    P.require(any(x["type"] == "ui_prompt_start" for x in rows)
                              and not any(x["type"] == "ui_prompt_end" for x in rows), "native_question_not_pending_at_click")
                screenshot = case_root / "actual-product-banner.png"
                command(["/usr/sbin/screencapture", "-x", screenshot])
                if name == "stale-origin":
                    sink.wait(expected)
                    webhook_received = True
                    terminal.close()
                    decoy.focus()
                clicked = observer("click", P.COPY[expected])
                P.require(clicked["physical_mouse_click"] and clicked["matches"], "physical_product_banner_click_missing")
                if click and name != "stale-origin":
                    wait_for(terminal.is_front, "product_click_did_not_focus_exact_origin")
                else:
                    time.sleep(0.5)
                    P.require(decoy.is_front(), "disabled_or_stale_product_click_changed_terminal")
                after = terminal.front()
            else:
                time.sleep(1.5)
                P.require(not observer("banner", P.COPY[expected])["matches"] and decoy.is_front(), "desktop_off_had_native_effect")
                banner, after = [], terminal.front()
            # The real helper may remain alive until the actual notification
            # click. Its webhook is observed after that normal helper lifecycle.
            if not webhook_received:
                sink.wait(expected)
            # The configured task-complete cue is 2.544s. Capture its complete
            # native playback, including the tail after a prompt physical click.
            time.sleep(3)
            (case_root / "capture-stop").touch()
            out, err = recorder.communicate(timeout=5)
            P.require(recorder.returncode == 0 and not err, "native_capture_not_collected")
            captured = json.loads(out)
            audio = captured["measurements"]
            P.require(audio["audio_samples"] >= 4800 and audio["screen_frames"] > 0, "native_audio_observer_incomplete")
            expected_sound = desktop and sound
            recording = case_root / "native-output.s16le"
            if expected_sound:
                P.require(audio["audio_peak"] > 0.03 and audio["audio_nonzero_samples"] >= 480, "actual_configured_product_cue_not_recorded")
                reference = case_root / "configured-cue-reference.wav"
                cue = root / "runtime/sounds" / ("question.mp3" if name == "question" else "task-complete.mp3")
                command(["/usr/bin/afconvert", "-f", "WAVE", "-d", "LEI16@48000", "-c", "1", cue, reference])
                audio["configured_cue_envelope_cosine"] = envelope_match(recording, reference)
                P.require(audio["configured_cue_envelope_cosine"] >= 0.9, "recorded_output_did_not_match_configured_cue")
            else:
                P.require(audio["audio_peak"] < 0.0001, "sound_off_had_recorded_output")
            if not terminal.closed:
                terminal.focus()
                if name == "question":
                    terminal.type("TEST accepted")
                    wait_for(lambda: any(x["type"] == "ui_prompt_end" for x in F.witness(case_root)), "native_question_not_resolved")
                terminal.type("/quit")
                wait_for(terminal.collected, "actual_pi_tui_not_collected")
                P.require(terminal.exit_path.read_text().strip() == "0", "actual_pi_tui_exit_failed")
                terminal.close()
            rows = F.witness(case_root)
            if name == "question":
                P.require(not provider.requests and sum(x["type"] == "ui_prompt_start" for x in rows) == 1
                          and sum(x["type"] == "ui_prompt_end" for x in rows) == 1
                          and not any(x["type"] == "agent_settled" for x in rows), "native_question_lifecycle_wrong")
            else:
                F.verify_case("success", rows, provider.requests)
            sink.quiet()
            report["cases"].append({"case": name, "native_mode": "tui", "origin_window": terminal.window,
                "origin_tty": terminal.tty, "origin_pi_pid": terminal.pid, "decoy_window": decoy.window,
                "decoy_tty": decoy.tty, "foreground_after_click": after, "banner_matches": banner,
                "real_mouse_click": desktop, "banner_observed": desktop, "sound_expected": expected_sound,
                "audio": audio, "audio_sha256": P.sha(recording),
                "banner_screenshot_sha256": P.sha(screenshot) if screenshot else None,
                "pending_question_at_click": name == "question", "terminal_collected": terminal.collected(),
                "checks": {key: True for key in ("native_tui", "configured_banner_policy", "configured_audio_policy",
                    "configured_click_policy", "native_event_counts", "ambient_root_isolated", "owned_terminal_collected")}})
        finally:
            if recorder and recorder.poll() is None:
                (case_root / "capture-stop").touch()
                try:
                    recorder.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    recorder.kill()
                    recorder.communicate(timeout=3)
            if terminal:
                terminal.close()
            provider.close()
            retained = args.report.parent / ("case-" + name)
            retained.mkdir(parents=True, exist_ok=True)
            for filename in ("actual-product-banner.png", "native-output.s16le", "capture-ready.json"):
                source = case_root / filename
                if source.is_file() and source.stat().st_size <= 16 * 1024 * 1024:
                    shutil.copyfile(source, retained / filename)

    try:
        report["native_preflight"] = observer("preflight")
        P.require(report["pi_acquisition"].get("version") == "1.4.0"
                  and report["pi_acquisition"].get("binary_sha256") == report["pi_binary_sha256"]
                  and command([args.pi_executable, "--version"]).strip() == "1.4.0",
                  "official_pinned_pi_identity_wrong")
        P.require(report["helper"]["archive_sha256"] == HELPER_SHA
                  and command(["git", "-C", REPO, "rev-parse", "HEAD:swift-notifier"]).strip() == HELPER_SOURCE,
                  "signed_helper_source_or_archive_mismatch")
        (root / "foreign-TEST-root").mkdir()
        P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink)
        D.command(args.candidate, ["internal-install-runtime", "--stage", args.runtime_stage, "--target", root / "runtime/bin",
                                  "--entry", args.candidate.name, "--control-root", root / "control",
                                  "--consumer", "pi-runtime-preparation"], root, env)
        P.setup(args.candidate, "install", root, args.package, args.pi_executable, env,
                ("--hooks", "false", "--agent-notify", "true", "--pi-desktop", "true", "--pi-webhook", "true"))
        ledger = json.loads((root / "control/ownership.json").read_text())
        native = ledger.get("Native")
        P.require(native and native["Path"] and native["InstalledTreeSHA256"], "installed_signed_native_receipt_missing")
        app = Path(native["Path"])
        helper = app / "Contents/MacOS/terminal-notifier-modern"
        P.require(P.sha(helper) == P.sha(args.runtime_stage / "ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"),
                  "installed_signed_helper_changed")
        report["helper"].update(installed_path=str(app), installed_tree_sha256=native["InstalledTreeSHA256"],
                                executable_sha256=P.sha(helper), source_stage_attestation_sha256=P.sha(args.runtime_stage / "ClaudeNotifier.app.managed-runtime.json"))
        report["native_helper_verified"] = True
        token, nonce = str(uuid.uuid4()), str(uuid.uuid4())
        permission = json.loads(command([helper, "--capabilities-json", "--permission-status", "--correlation-id", token, "--nonce", nonce]))
        P.require(permission.get("correlationID") == token and permission.get("nonce") == nonce, "candidate_helper_permission_identity_wrong")
        report["candidate_notification_permission"] = permission.get("permission")
        P.require(permission.get("permission") == "allowed", "candidate_signed_helper_notification_permission_unavailable")
        policy = json.loads((root / "control/agent-notifications.json").read_text())
        P.require(policy.get("enabled") is False, "pi_setup_enabled_unrelated_mcp_policy")
        report["unrelated_mcp_policy_enabled"] = False
        view = P.installed(root / "control", profile)
        P.require(P.sha(view["runtime"]["executable"]) == P.sha(args.candidate), "installed_candidate_bytes_differ")
        report["installed_asset_sha256"] = P.asset_receipts(root / "control", root / "runtime", args.assets_root)
        report["active_leaf"] = view["active"].name
        decoy_root = root / "decoy"
        (decoy_root / "project").mkdir(parents=True)
        decoy_cwd = decoy_root / "project/TEST-Pi-decoy"
        decoy_cwd.mkdir()
        decoy = Terminal(decoy_root, ["/bin/sleep", "240"], {"PATH": "/usr/bin:/bin", "HOME": str(root / "home")}, observer, decoy_cwd)
        terminals.append(decoy)
        for name in CASES:
            case(name, decoy)
        report.update(status="passed_native_product_observations", native_product_observations_passed=True)
    except Exception as error:
        report["failure"] = str(error)
    finally:
        failures = []
        for terminal in reversed(terminals):
            try:
                terminal.close()
            except Exception as error:
                failures.append(str(error))
        initial_provider.close()
        sink.close()
        report["owned_children_collected"] = not failures and all(t.collected() for t in terminals)
        if failures or not report["owned_children_collected"]:
            report.update(status="failed", native_product_observations_passed=False, cleanup_failures=failures)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["native_product_observations_passed"] and report["owned_children_collected"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "runtime-stage", "package", "assets-root", "pi-executable", "observer", "pi-acquisition", "helper-archive", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    options = parser.parse_args()
    for name in ("candidate", "runtime_stage", "package", "assets_root", "pi_executable", "observer", "pi_acquisition", "helper_archive"):
        setattr(options, name, getattr(options, name).resolve(strict=True))
    raise SystemExit(run(options))
