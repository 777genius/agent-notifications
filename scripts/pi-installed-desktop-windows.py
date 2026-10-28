#!/usr/bin/env python3
"""Real common-install Pi in Windows Terminal -> native toast/audio/click.

Requires an existing configured Client render route and real Windows Terminal.
No driver, terminal emulator, notification sender or product frame is supplied.
The only substitutes are the private loopback model and webhook destination.
"""
import argparse
import audioop
import importlib.util
import json
import os
from pathlib import Path
import platform
import queue
import shutil
import subprocess
import tempfile
import threading
import time
import uuid
import wave

REPO = Path(__file__).resolve().parents[1]
CASES = ("completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin")
POWERSHELL_LAUNCHER = '''param([Parameter(Mandatory=$true)][string]$Spec)
$ErrorActionPreference = 'Stop'
$inputSpec = Get-Content -LiteralPath $Spec -Raw | ConvertFrom-Json
$terminal = @{}
foreach ($key in @('WT_SESSION', 'WT_PROFILE_ID')) {
  $value = [Environment]::GetEnvironmentVariable($key, 'Process')
  if ($value) { $terminal[$key] = $value }
}
if (!$terminal['WT_SESSION']) { throw 'Real Windows Terminal session unavailable' }
Get-ChildItem Env: | ForEach-Object { [Environment]::SetEnvironmentVariable($_.Name, $null, 'Process') }
foreach ($entry in $inputSpec.environment.PSObject.Properties) {
  [Environment]::SetEnvironmentVariable($entry.Name, [string]$entry.Value, 'Process')
}
foreach ($key in $terminal.Keys) { [Environment]::SetEnvironmentVariable($key, $terminal[$key], 'Process') }
Set-Location -LiteralPath $inputSpec.cwd
if ($inputSpec.executable) {
  $childArgs = @($inputSpec.arguments | ForEach-Object { [string]$_ })
  & ([string]$inputSpec.executable) @childArgs
  $code = $LASTEXITCODE
} else { Start-Sleep -Seconds 180; $code = 0 }
[IO.File]::WriteAllText([string]$inputSpec.exit_path, [string]$code)
exit 0
'''


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


F = module("pi_native_fixture", REPO / "tests/integration/pi_native_fixture.py")
P = module("pi_installed_fixture", REPO / "tests/integration/pi_installed_fixture.py")
D = module("pi_installed_process", REPO / "scripts/pi-installed-process-e2e.py")
N = module("pi_preflight", REPO / "scripts/pi-native-preflight.py")
# Pure PCM envelope comparison; no macOS APIs run when importing this module.
M = module("pi_audio_oracle", REPO / "scripts/pi-installed-desktop-macos.py")


def wait_for(predicate, code, seconds=15):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError(code)


def observe_unchanged_focus(native, window, stop_capture, seconds=15):
    """Observe the whole click budget, even when the decoy is already focused.

    Stop the independent cue recording after its existing drain interval; the
    remaining focus observation must not extend WASAPI's bounded capture.
    """
    started, samples = time.monotonic(), 0
    while True:
        observed = native("foreground")
        P.require(observed["hwnd"] == window["hwnd"] and observed["keyboard_hwnd"] == window["hwnd"],
                  "disabled_or_stale_click_changed_foreground_or_keyboard")
        samples += 1
        elapsed = time.monotonic() - started
        if elapsed >= 0.5 and not stop_capture.exists():
            stop_capture.write_text("stop\n")
        if elapsed >= seconds:
            return {"elapsed_seconds": round(elapsed, 3), "samples": samples,
                    "foreground_hwnd": observed["hwnd"], "keyboard_hwnd": observed["keyboard_hwnd"]}
        time.sleep(0.05)


def command(argv, *, timeout=25, allowed=(0,), env=None):
    result = subprocess.run(list(map(str, argv)), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=timeout, env=env,
                            creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
    P.require(len(result.stdout) + len(result.stderr) <= 1024 * 1024, "native_observer_output_limit")
    P.require(result.returncode in allowed, "native_command_failed:" + Path(argv[0]).name + ":"
              + result.stderr.decode(errors="replace")[-2000:])
    return result


class Native:
    def __init__(self, executable):
        self.executable = executable

    def __call__(self, *args, allowed=(0,)):
        result = command([self.executable, *args], allowed=allowed)
        return json.loads(result.stdout.decode())

    def focus(self, window):
        self("activate", window["hwnd"], window["pid"], window["title"])
        wait_for(lambda: self("foreground")["hwnd"] == window["hwnd"], "owned_window_not_foreground")

    def type(self, window, value):
        self("type", window["hwnd"], window["pid"], window["title"], value)


class Terminal:
    def __init__(self, wt, powershell, root, title, executable, argv, env, cwd, native):
        self.root, self.native, self.pi_pid, self.closed = root, native, None, False
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.exit_path = root / "process.exit"
        spec = root / "launch.json"
        spec.write_text(json.dumps({"environment": env, "cwd": str(cwd), "executable": str(executable) if executable else None,
                                    "arguments": list(map(str, argv)), "exit_path": str(self.exit_path)}))
        launcher = root / "launch.ps1"
        launcher.write_text(POWERSHELL_LAUNCHER)
        # The real Terminal supplies WT_SESSION. Only its own child script
        # carries that actual value into the otherwise private Pi environment.
        launch_env = {key: value for key, value in os.environ.items() if key.upper() in {
            "PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
            "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "TMP", "TEMP"}}
        command([wt, "-w", "new", "--title", title, "--suppressApplicationTitle", "-d", cwd,
                 powershell, "-NoLogo", "-NoProfile", "-File", launcher, "-Spec", spec], env=launch_env)
        found = wait_for(lambda: native("window", title).get("hwnd"), "real_windows_terminal_window_missing")
        self.window = {**native("window", title), "title": title}
        P.require(self.window["hwnd"] == found, "native_terminal_window_changed")

    def collect(self):
        wait_for(self.exit_path.exists, "native_pi_exit_not_collected", seconds=12)
        P.require(self.exit_path.read_text().strip() == "0", "native_pi_exit_failed")
        self.close()

    def close(self):
        if self.closed:
            return
        if self.pi_pid and not self.exit_path.exists():
            command(["taskkill.exe", "/PID", self.pi_pid, "/T", "/F"], allowed=(0, 128))
        found = self.native("window", self.window["title"])
        if found.get("hwnd"):
            self.native("close", self.window["hwnd"], self.window["pid"], self.window["title"])
            wait_for(lambda: not self.native("window", self.window["title"]).get("hwnd"), "owned_terminal_window_not_closed")
        self.closed = True


def normalized_pcm(raw, rate):
    return raw if rate == 48000 else audioop.ratecv(raw, 2, 1, rate, 48000, None)[0]


def reference(native, source, output):
    decoded = output.with_suffix(".native.wav")
    native("decode", source, decoded)
    with wave.open(str(decoded)) as file:
        P.require(file.getsampwidth() == 2 and file.getnchannels() in (1, 2), "native_reference_format")
        data, rate = file.readframes(file.getnframes()), file.getframerate()
        if file.getnchannels() == 2:
            data = audioop.tomono(data, 2, 0.5, 0.5)
    with wave.open(str(output), "wb") as file:
        file.setparams((1, 2, 48000, 0, "NONE", "not compressed"))
        file.writeframes(normalized_pcm(data, rate))


def run(args):
    P.require(os.name == "nt", "actual_windows_client_host_required")
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-windows-desktop-")).resolve()
    root.chmod(0o700)
    report = {"schema_version": 1, "status": "unqualified", "scope": "actual_installed_pi_windows_desktop",
              "test_root": str(root), "source_sha": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip(),
              "working_tree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO)),
              "host_os": platform.system(), "host_architecture": platform.machine(), "script_sha256": P.sha(__file__),
              "candidate_binary_sha256": P.sha(args.candidate), "package_sha256": P.sha(args.package),
              "pi_binary_sha256": P.sha(args.pi_executable), "native_observer_sha256": P.sha(args.observer),
              "desktop_qualified": False, "native_product_observations_passed": False, "product_executed": False, "cases": []}
    report["execution_mode"] = "x64_emulation" if platform.machine().lower() in ("arm64", "aarch64") else "native_amd64"
    native, sink, initial_provider, terminals, helpers = Native(args.observer), None, None, [], []
    try:
        cue = root / "known-prerequisite-cue.wav"
        N.cue(cue)
        prerequisite = native("preflight", cue)
        report["prerequisites"] = N.classify("windows", "windows", prerequisite["checks"])
        report["prerequisite_measurements"] = prerequisite["measurements"]
        P.require(report["prerequisites"]["status"] == "prerequisites_ready", "windows_client_native_prerequisites_unavailable")
        report.update(windows_client=True, render_route_observed=True)
        wt, powershell = shutil.which("wt.exe"), shutil.which("pwsh.exe")
        P.require(wt and powershell, "existing_windows_terminal_and_powershell_required")
        sink, initial_provider = P.Webhooks(), F.Provider("success")
        env = F.prepare(root, initial_provider, "success")
        env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
        (root / "foreign-TEST-root").mkdir()
        P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink)
        D.command(args.candidate, ["internal-install-runtime", "--stage", args.runtime_stage, "--target", root / "runtime/bin",
                                  "--entry", args.candidate.name, "--control-root", root / "control",
                                  "--consumer", "pi-runtime-preparation"], root, env)
        P.setup(args.candidate, "install", root, args.package, args.pi_executable, env,
                ("--hooks", "false", "--agent-notify", "true", "--pi-desktop", "true", "--pi-webhook", "true"))
        P.require(json.loads((root / "control/agent-notifications.json").read_text()).get("enabled") is False,
                  "pi_setup_enabled_unrelated_mcp_policy")
        report["unrelated_mcp_policy_enabled"] = False
        view = P.installed(root / "control", root / "agent")
        P.require(P.sha(view["runtime"]["executable"]) == P.sha(args.candidate), "native_installed_candidate_mismatch")
        report["installed_asset_sha256"] = P.asset_receipts(root / "control", root / "runtime", args.assets_root)
        report["product_executed"] = True
        for name in CASES:
            case_root = root / ("case-" + name)
            provider = F.Provider("success", hold_reply=name != "question")
            origin = distractor = capture = neutral = None
            stop_capture, stop_neutral = case_root / "capture.stop", case_root / "neutral.stop"
            try:
                case_env = F.prepare(case_root, provider, "success", agent_dir=root / "agent")
                case_env.update(TEST_PI_DEFER_DIALOG="1", CLAUDE_PLUGIN_ROOT=str(root / "foreign-TEST-root"))
                nonce = uuid.uuid4().hex
                origin_title, distractor_title, toast_title = "TEST-Pi-origin-" + nonce, "TEST-Pi-distractor-" + nonce, "TEST-Pi-toast-" + nonce
                project = case_root / origin_title
                (case_root / "project").rename(project)
                distraction = case_root / distractor_title; distraction.mkdir()
                desktop, sound, click = name != "desktop-off", name != "sound-off", name != "click-off"
                P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink, desktop=desktop, sound=sound, click=click)
                cfg = json.loads((root / "global/config.json").read_text())
                # Match the actual configured title exactly, without the
                # default presentation suffix " [Pi]".
                cfg["notifications"]["desktop"]["showSessionLabel"] = False
                event = "question" if name == "question" else "task_complete"
                cfg["statuses"][event]["title"] = toast_title
                (root / "global/config.json").write_text(json.dumps(cfg) + "\n")
                argv = ["--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools", "--provider", "TEST-local",
                        "--model", "TEST-local", "--tui-mode", "regular"]
                if name != "question":
                    argv.append("TEST say done")
                origin = Terminal(wt, powershell, case_root / "origin", origin_title, args.pi_executable, argv, case_env, project, native)
                terminals.append(origin)
                wait_for(lambda: (case_root / "witness.jsonl").exists()
                         and any(row["type"] == "session_start" for row in F.witness(case_root)), "real_pi_tui_start_missing")
                rows = F.witness(case_root)
                P.require(all(row.get("mode") == "tui" for row in rows if row["type"] != "factory"), "actual_pi_mode_not_tui")
                origin.pi_pid = next(row["pid"] for row in rows if row["type"] == "factory")
                distractor = Terminal(wt, powershell, case_root / "distractor", distractor_title, None, [], case_env, distraction, native)
                terminals.append(distractor)
                pcm = case_root / "native-output.s16le"
                capture = subprocess.Popen([str(args.observer), "capture", str(pcm), str(stop_capture)], stdout=subprocess.PIPE,
                                           stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)
                helpers.append(capture)
                ready = json.loads(capture.stdout.readline())
                P.require(ready.get("ready") is True, "actual_loopback_capture_not_ready")
                if name == "question":
                    native.focus(origin.window); native.type(origin.window, "/TEST-dialog")
                else:
                    wait_for(lambda: bool(provider.requests), "native_pi_model_request_missing")
                native.focus(distractor.window)
                provider.release.set()
                sink.wait(event)
                image = case_root / "actual-product-banner.bmp"
                click_observation = None
                if desktop:
                    if name == "question":
                        rows = F.witness(case_root)
                        P.require(any(row["type"] == "ui_prompt_start" for row in rows)
                                  and not any(row["type"] == "ui_prompt_end" for row in rows), "native_question_not_pending")
                    expected_window = origin.window if click else distractor.window
                    if name == "stale-origin":
                        origin.close()
                        neutral_title = "TEST-Pi-neutral-" + nonce
                        neutral = subprocess.Popen([str(args.observer), "neutral", neutral_title, str(stop_neutral)], stdout=subprocess.PIPE,
                                                   stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)
                        helpers.append(neutral)
                        expected_window = {**json.loads(neutral.stdout.readline()), "title": neutral_title}
                        native.focus(expected_window)
                    found = native("toast-click", toast_title, P.COPY[event], image)
                    P.require(found.get("found") is True and found.get("real_mouse_click") is True, "native_product_toast_not_physically_clicked")
                    if name in ("click-off", "stale-origin"):
                        click_observation = observe_unchanged_focus(native, expected_window, stop_capture)
                    else:
                        def returned_to_origin():
                            observed = native("foreground")
                            return observed["hwnd"] == expected_window["hwnd"] and observed["keyboard_hwnd"] == expected_window["hwnd"]
                        wait_for(returned_to_origin, "native_click_returned_to_wrong_window")
                else:
                    found = native("toast", toast_title, P.COPY[event], image, allowed=(0, 3))
                    P.require(found.get("found") is False and native("foreground")["hwnd"] == distractor.window["hwnd"],
                              "disabled_desktop_had_native_effect")
                if not stop_capture.exists():
                    time.sleep(0.5)
                    stop_capture.write_text("stop\n")
                rest, errors = capture.communicate(timeout=5)
                P.require(capture.returncode == 0 and not errors, "native_loopback_capture_failed")
                audio = json.loads(rest)
                P.require(audio.get("complete") is True and audio["elapsed_ms"] >= 500, "native_audio_window_incomplete")
                expected_sound = sound and desktop
                correlation = None
                if expected_sound:
                    P.require(audio["peak"] > 0.01 and audio["nonzero_samples"] >= 480, "actual_configured_cue_not_recorded")
                    reference_path = case_root / "reference.wav"
                    reference(native, root / "runtime/sounds" / ("question.mp3" if event == "question" else "task-complete.mp3"), reference_path)
                    comparison = case_root / "comparison.s16le"
                    comparison.write_bytes(normalized_pcm(pcm.read_bytes(), audio["sample_rate"]))
                    correlation = M.envelope_match(comparison, reference_path)
                    P.require(correlation >= 0.65, "captured_audio_not_configured_cue")
                else:
                    P.require(audio["peak"] <= 0.0005, "disabled_sound_was_audible")
                if not origin.closed:
                    native.focus(origin.window)
                    if name == "question":
                        native.type(origin.window, "TEST accepted")
                        wait_for(lambda: any(row["type"] == "ui_prompt_end" for row in F.witness(case_root)), "native_question_not_collected")
                    native.type(origin.window, "/quit"); origin.collect()
                rows = F.witness(case_root)
                if name == "question":
                    P.require(not provider.requests and sum(row["type"] == "ui_prompt_start" for row in rows) == 1
                              and sum(row["type"] == "ui_prompt_end" for row in rows) == 1, "native_question_lifecycle_wrong")
                else:
                    F.verify_case("success", rows, provider.requests)
                sink.quiet()
                report["cases"].append({"case": name, "native_mode": "tui", "banner_observed": desktop,
                    "real_mouse_click": desktop, "pending_question_at_click": name == "question", "audio": audio,
                    "configured_cue_envelope_match": correlation, "audio_sha256": P.sha(pcm),
                    "banner_screenshot_sha256": P.sha(image) if desktop else None,
                    "origin_window": origin.window, "distractor_window": distractor.window,
                    "unchanged_click_observation": click_observation,
                    "checks": {key: True for key in ("native_tui", "configured_banner_policy", "configured_audio_policy",
                               "configured_click_policy", "native_event_counts", "ambient_root_isolated", "owned_terminal_collected")}})
            finally:
                if origin:
                    origin.close()
                if distractor:
                    distractor.close()
                if capture and capture.poll() is None:
                    stop_capture.write_text("stop\n"); capture.communicate(timeout=5)
                if neutral and neutral.poll() is None:
                    stop_neutral.write_text("stop\n"); neutral.communicate(timeout=5)
                provider.close()
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
        for helper in helpers:
            if helper.poll() is None:
                helper.kill(); helper.communicate(timeout=3)
        if initial_provider:
            initial_provider.close()
        if sink:
            sink.close()
        report["owned_children_collected"] = not failures and all(t.closed for t in terminals) and all(p.poll() is not None for p in helpers)
        if failures:
            report.update(status="failed", native_product_observations_passed=False, cleanup_failures=failures)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        for case_root in root.glob("case-*"):
            for name in ("native-output.s16le", "actual-product-banner.bmp"):
                source = case_root / name
                if source.is_file():
                    target = args.report.parent / "native-captures" / case_root.name / name
                    target.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(source, target)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["native_product_observations_passed"] and report["owned_children_collected"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "runtime-stage", "package", "assets-root", "pi-executable", "observer", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    for name in ("candidate", "runtime_stage", "package", "assets_root", "pi_executable", "observer"):
        setattr(args, name, getattr(args, name).resolve(strict=True))
    raise SystemExit(run(args))
