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


def wait_for_consent(request, observe, seconds=130):
    """Observe the exact candidate dialog or the actual request's early exit."""
    # Observe the one normal request through its helper's 120 s authorization
    # budget (Go allows 125 s inside a 130 s setup lease). This is an observation
    # cap, not a guarantee about the CLI's total lifetime before native setup.
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        dialogs = observe()
        if dialogs or request.poll() is not None:
            return dialogs
        time.sleep(0.05)
    raise RuntimeError("actual_candidate_notification_consent_dialog_unavailable")


def permission_receipt(stdout, stderr, exit_code):
    """Retain bounded setup diagnostics; exit zero alone never proves a grant.

    These are the installed product's fixed setup-notifications JSON envelope
    and diagnostics, not Pi/provider output or notification contents.
    """
    bounded = len(stdout) + len(stderr) <= 65536
    receipt = {"exit_code": exit_code, "stdout_bytes": len(stdout), "stderr_bytes": len(stderr),
               "output_bounded": bounded, "stdout": stdout[:32768].decode("utf-8", errors="replace"),
               "stderr": stderr[:32768].decode("utf-8", errors="replace")}
    if bounded:
        try:
            value = json.loads(stdout)
        except (ValueError, UnicodeError):
            value = None
        if isinstance(value, dict):
            permission = value.get("permission")
            if permission in ("allowed", "denied", "undetermined", "unavailable"):
                receipt["permission"] = permission
                receipt["matching_reason"] = value.get("reason") == "permission_" + permission
    return receipt


def observed_permission(receipt):
    P.require(receipt["exit_code"] == 0 and receipt["stderr_bytes"] == 0 and receipt["output_bounded"]
              and receipt.get("matching_reason") is True
              and receipt.get("permission") in ("allowed", "denied", "undetermined"),
              "normal_notification_consent_request_failed")
    return receipt["permission"]


def verify_permission_grant(request_receipt, fresh_receipt):
    requested, fresh = observed_permission(request_receipt), observed_permission(fresh_receipt)
    P.require(requested == "allowed" and fresh == "allowed",
              "candidate_signed_helper_notification_permission_" + (requested if requested != "allowed" else fresh))
    return fresh


def consent_observer(request, observe, app, receipt):
    """Activate at most the one existing verified request helper, never relaunch."""
    attempted = False

    def observed():
        nonlocal attempted
        dialogs = observe("notification-consent")["dialogs"]
        if not dialogs and not attempted and request.poll() is None:
            state = observe("helper-processes", str(app))
            receipt["helper_observation"] = state
            helpers = state.get("installed_helpers", [])
            if len(helpers) == 1 and helpers[0].get("finished_launching") is True \
                    and helpers[0].get("process_start_identity") and request.poll() is None:
                attempted = True
                receipt["helper_activation"] = observe("activate-permission-helper", str(app), str(helpers[0]["pid"]),
                                                       helpers[0]["process_start_identity"])
        return dialogs

    return observed


def sample_permission_helper(observe, app, receipt, evidence_root):
    """Sample only the original activated helper; diagnostics never prove consent."""
    original = receipt.get("helper_activation", {}).get("before", {})
    pid, start = original.get("pid"), original.get("process_start_identity")
    result = {"status": "inconclusive_original_identity_unavailable", "desktop_qualified": False,
              "duration_seconds": 1, "sampling_interval_ms": 10, "timeout_seconds": 5,
              "output_limit_bytes": 262144}
    if not isinstance(pid, int) or isinstance(pid, bool) or pid <= 0 or not isinstance(start, str) or not start:
        return result

    def same_helper():
        helpers = observe("helper-processes", str(app), timeout=3).get("installed_helpers", [])
        return len(helpers) == 1 and helpers[0].get("pid") == pid \
            and helpers[0].get("process_start_identity") == start

    if not same_helper():
        result["status"] = "inconclusive_original_identity_changed"
        return result
    # A fixed basename in the owned evidence directory is the only sample path.
    sample = evidence_root.resolve(strict=True) / "permission-helper.sample.txt"
    P.require(not sample.exists() and not sample.is_symlink(), "permission_sample_path_already_exists")
    result.update(pid=pid, process_start_identity=start, identity_verified_before=True)
    try:
        try:
            child = subprocess.run(["/usr/bin/sample", str(pid), "1", "10", "-file", str(sample)],
                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                   timeout=5)
            result.update(exit_code=child.returncode, status="inconclusive_sample_output_unavailable"
                          if child.returncode == 0 else "inconclusive_sample_command_failed")
        except subprocess.TimeoutExpired:
            result["status"] = "inconclusive_sample_timeout"
        except OSError:
            result["status"] = "inconclusive_sample_unavailable"
        result["identity_verified_after"] = same_helper()
        if not result["identity_verified_after"]:
            result["status"] = "inconclusive_original_identity_changed"
        elif result.get("exit_code") == 0 and sample.is_file() and not sample.is_symlink():
            result["output_bytes"] = sample.stat().st_size
            if 0 < result["output_bytes"] <= result["output_limit_bytes"]:
                result.update(status="sample_collected", artifact=sample.name, sha256=P.sha(sample))
            else:
                result["status"] = "inconclusive_sample_output_limit"
    finally:
        # Keep no output if either ownership check or the output bound failed.
        if result.get("status") != "sample_collected":
            sample.unlink(missing_ok=True)
    return result


def observe_unchanged_focus(terminal, stop_capture, seconds=15):
    def observed():
        current = terminal.front()
        return {"window": current["window"], "tty": current["tty"]}
    return P.observe_unchanged_focus(observed, {"window": terminal.window, "tty": terminal.tty},
                                     stop_capture, drain_seconds=3, seconds=seconds)


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


def terminal_launcher(root, argv, env, cwd):
    """Write the exact Terminal child script, preserving the native input TTY.

    Noninteractive Bash otherwise redirects an asynchronous command's stdin to
    /dev/null. Preserve the actual inherited terminal before the backgrounded
    child so PID/wait ownership does not silently turn real Pi into non-TUI.
    """
    script = root / "launch.command"
    entries = [shlex.quote(key + "=" + value) for key, value in env.items()]
    launch = " ".join(["/usr/bin/env", "-i", *entries,
                       'TERM_PROGRAM=Apple_Terminal', 'TERM=xterm-256color',
                       'TERM_SESSION_ID="${TERM_SESSION_ID:-}"', *map(shlex.quote, map(str, argv))])
    script.write_text("#!/bin/bash\nset -eu\nprintf '%s\\n' \"$$\" > " + shlex.quote(str(root / "shell.pid"))
                      + "\ncd " + shlex.quote(str(cwd)) + "\nexec 9<&0\nset +e\n" + launch
                      + " <&9 &\nchild=$!\nexec 9<&-\nprintf '%s\\n' \"$child\" > " + shlex.quote(str(root / "process.pid"))
                      + "\nwait \"$child\"\ncode=$?\nprintf '%s\\n' \"$code\" > "
                      + shlex.quote(str(root / "process.exit")) + "\nexit 0\n")
    script.chmod(0o700)
    return script


class Terminal:
    """An owned real Terminal window and its observed native TTY/Pi PID."""
    def __init__(self, root, argv, env, observer, cwd=None):
        self.root, self.observer, self.closed = root, observer, False
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.pid_path, self.exit_path = root / "process.pid", root / "process.exit"
        self.shell_path = root / "shell.pid"
        cwd = cwd or root / "project"
        script = terminal_launcher(root, argv, env, cwd)
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
    args.report.parent.mkdir(parents=True, exist_ok=True)
    sink, initial_provider = P.Webhooks(), F.Provider("success")
    env = F.prepare(root, initial_provider, "success")
    env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
    profile, terminals, helpers, app = root / "agent", [], [], None
    permission_request = None
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

    def observer(*argv, timeout=20):
        return json.loads(command([args.observer, *argv], timeout=timeout))

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
            screenshot, webhook_received, click_observation = None, False, None
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
                    click_observation = observe_unchanged_focus(decoy, lambda: (case_root / "capture-stop").touch())
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
            if click_observation is None:
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
                "unchanged_click_observation": click_observation,
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
        permission = json.loads(D.command(args.candidate, ["setup-notifications", "permission-status", "--control-root", root / "control", "--json"], root, env))
        report["notification_consent"] = {"initial_permission": permission.get("permission"), "requested": False}
        if permission.get("permission") == "undetermined":
            started = time.monotonic()
            permission_request = subprocess.Popen([str(args.candidate), "setup-notifications", "request-permission",
                                        "--control-root", str(root / "control"), "--expected-generation", str(ledger["Generation"]), "--json"],
                                       cwd=root / "project", env=env, stdin=subprocess.DEVNULL,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            helpers.append(permission_request)
            report["notification_consent"]["requested"] = True
            dialogs = wait_for_consent(permission_request,
                consent_observer(permission_request, observer, app, report["notification_consent"]))
            report["notification_consent"].update(dialogs=dialogs, completed_without_dialog=not dialogs)
            if dialogs:
                command(["/usr/sbin/screencapture", "-x", args.report.parent / "actual-notification-consent.png"])
                click = observer("allow-notification-consent")
                report["notification_consent"].update(physical_allow_click=click["physical_allow_click"],
                    screenshot_sha256=P.sha(args.report.parent / "actual-notification-consent.png"))
            out, err = permission_request.communicate(timeout=10)
            receipt = permission_receipt(out, err, permission_request.returncode)
            report["notification_consent"].update(request_result=receipt,
                elapsed_seconds=round(time.monotonic() - started, 3), request_collected=True)
            fresh_out = D.command(args.candidate, ["setup-notifications", "permission-status", "--control-root", root / "control", "--json"], root, env)
            fresh_receipt = permission_receipt(fresh_out, b"", 0)
            report["notification_consent"]["fresh_permission_result"] = fresh_receipt
            permission = {"permission": verify_permission_grant(receipt, fresh_receipt)}
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
        if permission_request is not None:
            report["notification_consent"].setdefault("elapsed_seconds", round(time.monotonic() - started, 3))
            report["notification_consent"]["request_running_at_failure"] = permission_request.poll() is None
            try:
                report["notification_consent"]["installed_helper_processes_at_failure"] = observer("helper-processes", str(app))
            except Exception as observation_error:
                report["notification_consent"]["helper_process_observation_failed"] = str(observation_error)
        try:
            screenshot = args.report.parent / "native-failure.png"
            command(["/usr/sbin/screencapture", "-x", screenshot])
            report["native_failure_screenshot_sha256"] = P.sha(screenshot)
        except Exception as capture_error:
            report["native_failure_capture"] = str(capture_error)
        if permission_request is not None and report.get("failure") == "actual_candidate_notification_consent_dialog_unavailable":
            consent = report["notification_consent"]
            try:
                consent["failure_diagnostics"] = observer("permission-diagnostics", str(app), timeout=4)
            except Exception:
                consent["failure_diagnostics"] = {"status": "inconclusive_observer_unavailable"}
            try:
                consent["helper_sample"] = sample_permission_helper(observer, app, consent, args.report.parent)
            except Exception:
                consent["helper_sample"] = {"status": "inconclusive_sample_collection_failed"}
    finally:
        failures = []
        for terminal in reversed(terminals):
            try:
                terminal.close()
            except Exception as error:
                failures.append(str(error))
        for child in helpers:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.communicate(timeout=3)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.communicate(timeout=3)
            if child is permission_request:
                out, err = child.communicate(timeout=3)
                report["notification_consent"]["request_result"] = permission_receipt(out, err, child.returncode)
                report["notification_consent"]["request_collected"] = child.poll() is not None
        if app is not None:
            try:
                wait_for(lambda: not observer("helper-processes", str(app))["installed_helper_pids"],
                         "owned_native_notification_helper_not_collected", seconds=15)
                report["native_helper_processes_collected"] = True
            except Exception as error:
                failures.append(str(error))
                report["native_helper_processes_collected"] = False
        initial_provider.close()
        sink.close()
        report["owned_children_collected"] = not failures and all(t.collected() for t in terminals) and all(p.poll() is not None for p in helpers)
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
