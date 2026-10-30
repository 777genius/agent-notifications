#!/usr/bin/env python3
"""Real common-install Pi in Windows Terminal -> native toast/audio/click.

Requires a configured Client render route or an explicitly verified virtual
capture fixture, plus real Windows Terminal. No notification sender or product
frame is supplied.
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
import threading
import time
import uuid
import wave

REPO = Path(__file__).resolve().parents[1]
CASES = ("completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin")
CAPTURE_BUDGET_SECONDS = 45
POWERSHELL_LAUNCHER = '''param([Parameter(Mandatory=$true)][string]$Spec)
$ErrorActionPreference = 'Stop'
$inputSpec = Get-Content -LiteralPath $Spec -Raw | ConvertFrom-Json
$state = [ordered]@{ phase='read_spec'; powershell_pid=$PID }
function SaveState([string]$Phase) {
  $state['phase'] = $Phase
  [IO.File]::WriteAllText([string]$inputSpec.state_path, ($state | ConvertTo-Json -Compress))
}
function CaptureFailure {
  try { & ([string]$inputSpec.observer) screenshot ([string]$inputSpec.failure_image) | Out-Null } catch { }
}
try {
  $terminal = @{}
  foreach ($key in @('WT_SESSION', 'WT_PROFILE_ID')) {
    $value = [Environment]::GetEnvironmentVariable($key, 'Process')
    if ($value) { $terminal[$key] = $value }
  }
  $state['wt_session_present'] = [bool]$terminal['WT_SESSION']
  SaveState 'terminal_environment'
  if (!$terminal['WT_SESSION']) { throw 'Real Windows Terminal session unavailable' }
  Get-ChildItem Env: | ForEach-Object { [Environment]::SetEnvironmentVariable($_.Name, $null, 'Process') }
  foreach ($entry in $inputSpec.environment.PSObject.Properties) {
    [Environment]::SetEnvironmentVariable($entry.Name, [string]$entry.Value, 'Process')
  }
  foreach ($key in $terminal.Keys) { [Environment]::SetEnvironmentVariable($key, $terminal[$key], 'Process') }
  SaveState 'private_environment'
  Set-Location -LiteralPath $inputSpec.cwd
  if ($inputSpec.executable) {
    $childArgs = @($inputSpec.arguments | ForEach-Object { [string]$_ })
    SaveState 'child_running'
    & ([string]$inputSpec.executable) @childArgs
    $code = $LASTEXITCODE
    $state['child_exit_code'] = $code
    SaveState 'child_exited'
    if ($code -ne 0 -or ![IO.File]::Exists([string]$inputSpec.witness_path)) { CaptureFailure }
  } else { Start-Sleep -Seconds 180; $code = 0 }
  [IO.File]::WriteAllText([string]$inputSpec.exit_path, [string]$code)
} catch {
  $detail = ($_ | Out-String) + $_.ScriptStackTrace
  $state['error'] = $detail.Substring(0, [Math]::Min(4096, $detail.Length))
  SaveState 'launcher_failed'
  CaptureFailure
  throw
}
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
A = module("pi_windows_audio_fixture", REPO / "scripts/pi-windows-audio-fixture.py")


def wait_for(predicate, code, seconds=15):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError(code)


def observe_unchanged_focus(native, window, stop_capture, seconds=15):
    def observed():
        current = native("foreground")
        return {"foreground_hwnd": current["hwnd"], "keyboard_hwnd": current["keyboard_hwnd"]}

    def stop():
        if not stop_capture.exists():
            stop_capture.write_text("stop\n")
    return P.observe_unchanged_focus(observed, {"foreground_hwnd": window["hwnd"], "keyboard_hwnd": window["hwnd"]},
                                     stop, drain_seconds=0.5, seconds=seconds)


def stop_helper(helper, stop):
    if helper and helper.poll() is None:
        if not stop.exists():
            stop.write_text("stop\n")
        helper.communicate(timeout=5)


def cleanup_case(actions, *, primary_failure, report, case):
    failures = []
    for resource, close in actions:
        try:
            close()
        except Exception as error:
            failures.append({"case": case, "resource": resource, "failure": str(error)[-2000:]})
    if failures:
        report.setdefault("case_cleanup_failures", []).extend(failures)
        # Cleanup must attempt every owned resource without replacing the
        # observation that caused it. A cleanup-only failure still refuses.
        if primary_failure is None:
            raise RuntimeError("native_case_cleanup_failed:" + failures[0]["failure"])


def start_toast_watcher(native, title, body, root, deadline, helpers):
    ready = root / "early-toast-ready.json"
    child = subprocess.Popen([str(native.executable), "toast-watch", title, body,
                              str(root / "early-product-banner.bmp"), str(ready)],
                             stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             creationflags=subprocess.CREATE_NO_WINDOW)
    helpers.append(child)
    try:
        wait_for(ready.exists, "early_toast_observer_not_ready", seconds=max(0, min(5, deadline - time.monotonic())))
        P.require(ready.is_file() and not ready.is_symlink() and ready.stat().st_size <= 4096,
                  "early_toast_observer_ready_file_invalid")
        P.require(json.loads(ready.read_text()) == {"ready": True, "observation_only": True, "budget_ms": 15000},
                  "early_toast_observer_ready_invalid")
    except Exception:
        collect_toast_watcher(child, root / "early-toast-observation.json", deadline)
        raise
    return child


def collect_toast_watcher(child, receipt, deadline):
    if child is None:
        return
    value = {"observation_only": True, "qualification_input": False}
    try:
        out, errors = child.communicate(timeout=max(0.1, deadline - time.monotonic()))
    except subprocess.TimeoutExpired:
        child.kill()  # Only this directly owned observation process.
        out, errors = child.communicate(timeout=3)
        value["deadline_expired"] = True
    value.update(exit_code=child.returncode, stdout_bytes=len(out), stderr_bytes=len(errors))
    if len(out) + len(errors) <= 65536:
        value["stderr"] = errors.decode(errors="replace")[-2000:]
        try:
            value["observation"] = json.loads(out)
        except ValueError:
            value["invalid_stdout"] = out.decode(errors="replace")[-2000:]
    receipt.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


TOAST_READINESS = r'''$ErrorActionPreference='Stop'
$phase='notifier'
try {
  [Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime] | Out-Null
  $id='Claude Code Notifications'
  $notifier=[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id)
  $setting=[string]$notifier.Setting
  $phase='history'
  $items=@([Windows.UI.Notifications.ToastNotificationManager]::History.GetHistory($id))
  if ($items.Count -gt 64) { throw 'history_bound' }
  $matched=0
  foreach ($item in $items) {
    $texts=@($item.Content.GetElementsByTagName('text'))
    if ($texts.Count -gt 16) { throw 'history_text_bound' }
    if (@($texts | Where-Object { $_.InnerText -ceq $env:TEST_PI_EXPECTED_TOAST_TITLE }).Count) { $matched++ }
  }
  @{status='observed';app_id=$id;setting=$setting;history_total=$items.Count;exact_title_history_matches=$matched} | ConvertTo-Json -Compress
} catch {
  @{status='unavailable';phase=$phase;setting=$setting;hresult=$_.Exception.HResult} | ConvertTo-Json -Compress
}
'''


def shell_notification_state():
    # Advisory shell state only. Do not warm the actual PowerShell/WinRT sender.
    import ctypes
    shell = ctypes.WinDLL("shell32", use_last_error=True)
    shell.SHQueryUserNotificationState.argtypes = (ctypes.POINTER(ctypes.c_int),)
    shell.SHQueryUserNotificationState.restype = ctypes.c_long
    state = ctypes.c_int()
    status = shell.SHQueryUserNotificationState(ctypes.byref(state))
    return {"hresult": status, "state": state.value if status >= 0 else None}


def toast_readiness(title):
    # Read only this candidate's fixed native identity; return no history text.
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.GetSystemDirectoryW.argtypes = (wintypes.LPWSTR, wintypes.UINT)
    kernel.GetSystemDirectoryW.restype = wintypes.UINT
    directory = ctypes.create_unicode_buffer(32768)
    count = kernel.GetSystemDirectoryW(directory, len(directory))
    P.require(0 < count < len(directory), "native_system_powershell_directory_unavailable")
    powershell = Path(directory.value) / "WindowsPowerShell/v1.0/powershell.exe"
    env = dict(os.environ, TEST_PI_EXPECTED_TOAST_TITLE=title or "")
    result = command([powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", TOAST_READINESS], timeout=15, env=env)
    P.require(len(result.stdout) <= 4096, "native_toast_readiness_output_bound")
    value = json.loads(result.stdout.decode(encoding="utf-8-sig"))
    value["shell_notification_state"] = shell_notification_state()
    return value


def command(argv, *, timeout=25, allowed=(0,), env=None, handles=()):
    startup = None
    if handles:
        startup = subprocess.STARTUPINFO()
        startup.lpAttributeList = {"handle_list": list(handles)}
    try:
        for handle in handles:
            os.set_handle_inheritable(handle, True)
        result = subprocess.run(list(map(str, argv)), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, env=env, startupinfo=startup, close_fds=True,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
    finally:
        for handle in handles:
            os.set_handle_inheritable(handle, False)
    P.require(len(result.stdout) + len(result.stderr) <= 1024 * 1024, "native_observer_output_limit")
    P.require(result.returncode in allowed, "native_command_failed:" + Path(argv[0]).name + ":mode="
              + str(argv[1])[:64] + ":exit=" + str(result.returncode) + ":stderr="
              + result.stderr.decode(errors="replace")[-2000:] + ":stdout="
              + result.stdout.decode(errors="replace")[-2000:])
    return result


class ProcessHandle:
    def __init__(self, pid):
        P.require(type(pid) is int and 0 < pid <= 0xFFFFFFFF, "owned_pi_process_pid_invalid")
        import ctypes
        from ctypes import wintypes
        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        self.kernel.OpenProcess.argtypes = (wintypes.DWORD, wintypes.BOOL, wintypes.DWORD)
        self.kernel.OpenProcess.restype = wintypes.HANDLE
        self.kernel.CloseHandle.argtypes = (wintypes.HANDLE,)
        self.kernel.CloseHandle.restype = wintypes.BOOL
        self.value = self.kernel.OpenProcess(0x00101001, False, pid)  # query, terminate, synchronize
        P.require(self.value, "owned_pi_process_handle_unavailable")

    def close(self):
        if self.value is not None:
            P.require(self.kernel.CloseHandle(self.value), "owned_pi_process_handle_close_failed")
            self.value = None


class Native:
    def __init__(self, executable, fixture=None):
        self.executable, self.fixture = executable, fixture

    def route_arguments(self):
        return self.fixture.arguments() if self.fixture else []

    def __call__(self, *args, allowed=(0,)):
        if args and args[0] == "preflight":
            args = (*args, *self.route_arguments())
        result = command([self.executable, *args], allowed=allowed)
        return json.loads(result.stdout.decode())

    def focus(self, window):
        self("activate", window["hwnd"], window["pid"], window["title"])
        wait_for(lambda: self("foreground")["hwnd"] == window["hwnd"], "owned_window_not_foreground")

    def type(self, window, value):
        self("type", window["hwnd"], window["pid"], window["title"], value)

    def held_process(self, handle, mode, *args):
        P.require(handle.value is not None, "owned_pi_process_handle_already_closed")
        result = command([self.executable, mode, handle.value, *args], handles=(handle.value,))
        return json.loads(result.stdout.decode())


class Terminal:
    def __init__(self, wt, powershell, root, title, executable, argv, env, cwd, native, *, owned):
        self.root, self.native, self.pi_pid, self.closed = root, native, None, False
        self.executable = str(executable) if executable else None
        self.pi_identity, self.process_handle, self.window_closed = None, None, False
        self.process_cleanup = None
        self.cleanup_stage_failures = []
        self.window = {"title": title}
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.exit_path = root / "process.exit"
        spec = root / "launch.json"
        # Windows environment names are case-insensitive. Python can supply
        # both aliases, which PowerShell's JSON object reader must not receive.
        environment = {key.upper(): value for key, value in env.items()}
        spec.write_text(json.dumps({"environment": environment, "cwd": str(cwd), "executable": str(executable) if executable else None,
                                    "arguments": list(map(str, argv)), "exit_path": str(self.exit_path),
                                    "state_path": str(root / "launch-state.json"), "observer": str(native.executable),
                                    "failure_image": str(root / "launch-failure.bmp"),
                                    "witness_path": str(root.parent / "witness.jsonl")}), encoding="utf-8")
        launcher = root / "launch.ps1"
        launcher.write_text(POWERSHELL_LAUNCHER)
        # The real Terminal supplies WT_SESSION. Only its own child script
        # carries that actual value into the otherwise private Pi environment.
        launch_env = {key: value for key, value in os.environ.items() if key.upper() in {
            "PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
            "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "TMP", "TEMP"}}
        # Window discovery can fail after wt has launched. Keep the partially
        # initialized owner reachable so final cleanup still observes/closes it.
        owned.append(self)
        command([wt, "-w", "new", "--title", title, "--suppressApplicationTitle", "-d", cwd,
                 powershell, "-NoLogo", "-NoProfile", "-File", launcher, "-Spec", spec], env=launch_env)
        found = wait_for(lambda: native("window", title).get("hwnd"), "real_windows_terminal_window_missing")
        self.window = {**native("window", title), "title": title}
        P.require(self.window["hwnd"] == found, "native_terminal_window_changed")

    def bind_process(self, pid):
        self.pi_pid = pid
        self.process_handle = ProcessHandle(pid)
        identity = self.native.held_process(self.process_handle, "process-identity-handle", self.executable)
        P.require(identity.get("present") is True and identity.get("pid") == pid
                  and isinstance(identity.get("creation_time"), str) and identity["creation_time"].isdigit()
                  and int(identity["creation_time"]) > 0
                  and os.path.normcase(identity.get("executable", "")) == os.path.normcase(self.executable),
                  "owned_pi_process_identity_unverified")
        self.pi_identity = identity

    def collect(self):
        wait_for(self.exit_path.exists, "native_pi_exit_not_collected", seconds=12)
        P.require(self.exit_path.read_text().strip() == "0", "native_pi_exit_failed")
        self.close()

    def close(self):
        if self.closed:
            return
        failures = []

        def failed(stage, error):
            failures.append(error)
            self.cleanup_stage_failures.append({"stage": stage, "failure": str(error)[-2000:]})

        try:
            if self.executable and not (self.process_cleanup and self.process_cleanup.get("collected") is True):
                P.require(self.pi_identity is not None and self.process_handle is not None,
                          "partial_pi_process_cleanup_unverified")
                self.process_cleanup = self.native.held_process(self.process_handle, "collect-owned-handle",
                                                                self.pi_identity["creation_time"], self.executable)
                P.require(self.process_cleanup.get("collected") is True, "owned_pi_process_cleanup_unverified")
        except Exception as error:
            failed("process_collection", error)
        try:
            if self.process_handle is not None:
                self.process_handle.close()
                self.process_handle = None
        except Exception as error:
            failed("handle_close", error)
        try:
            if not self.window_closed:
                found = self.native("window", self.window["title"])
                if found.get("hwnd"):
                    if not self.window.get("hwnd"):
                        self.window = {**found, "title": self.window["title"]}
                    self.native("close", self.window["hwnd"], self.window["pid"], self.window["title"])
                    wait_for(lambda: not self.native("window", self.window["title"]).get("hwnd"), "owned_terminal_window_not_closed")
                else:
                    P.require(self.window.get("hwnd"), "partial_terminal_cleanup_unverified")
                self.window_closed = True
        except Exception as error:
            failed("window_close", error)
        if failures:
            raise failures[0]
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


def mark_diagnostic_unqualified(report):
    # TEMPORARY: scope metadata alone does not make aggregate qualification refuse.
    report["diagnostic_observations_complete"] = report["native_product_observations_passed"]
    report["native_product_observations_passed"] = False
    if report["diagnostic_observations_complete"]:
        report["status"] = "unqualified_diagnostic"


def run(args):
    P.require(os.name == "nt", "actual_windows_client_host_required")
    root = P.new_test_root("TEST-pi-windows-desktop-")
    report = {"schema_version": 1, "status": "unqualified", "scope": "temporary_pi_windows_delivery_diagnostic", "diagnostic_only": True,
              "test_root": str(root), "source_sha": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip(),
              "working_tree_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO)),
              "host_os": platform.system(), "host_architecture": platform.machine(), "script_sha256": P.sha(__file__),
              "candidate_binary_sha256": P.sha(args.candidate), "package_sha256": P.sha(args.package),
              "pi_binary_sha256": P.sha(args.pi_executable), "native_observer_sha256": P.sha(args.observer),
              "desktop_qualified": False, "native_product_observations_passed": False, "product_executed": False, "cases": []}
    report["execution_mode"] = "x64_emulation" if platform.machine().lower() in ("arm64", "aarch64") else "native_amd64"
    native, sink, initial_provider, terminals, helpers = Native(args.observer), None, None, [], []
    diagnostic_title = None

    def observe_failure():
        if report.get("failure_observation_attempted"):
            return
        report["failure_observation_attempted"] = True
        try:
            for terminal in terminals:
                state = terminal.root / "launch-state.json"
                if state.is_file() and not state.is_symlink() and state.stat().st_size <= 65536:
                    shutil.copy2(state, terminal.root / "launch-at-failure.json")
            report["failure_foreground"] = native("foreground")
            native("screenshot", root / "failure-desktop.bmp")
        except Exception as diagnostic_error:
            report["failure_observation_error"] = str(diagnostic_error)
        # TEMPORARY: inspect the original owned Terminal only after failure.
        # This cannot acknowledge a modal, launch an app or change any setting.
        try:
            original = next((terminal for terminal in reversed(terminals) if terminal.executable and not terminal.window_closed and terminal.window.get("hwnd")), None)
            report["terminal_settings_diagnostic"] = native("terminal-settings-readonly", original.window["hwnd"], original.window["pid"], original.window["title"]) if original else {"status": "unavailable"}
        except Exception:
            report["terminal_settings_diagnostic"] = {"status": "unavailable"}
        # TEMPORARY: normal read-only UI observation only after the primary
        # failure and confirmed own history. It cannot create a passing banner.
        if diagnostic_title:
            try:
                readiness = toast_readiness(diagnostic_title)
                report["native_toast_readiness_after"] = readiness
                if readiness.get("status") == "observed" and readiness.get("setting") == "Enabled" and readiness.get("exact_title_history_matches") == 1:
                    report["notification_center_diagnostic"] = native("notification-center-readonly", diagnostic_title, root / "notification-center.bmp")
            except Exception:
                report["notification_center_diagnostic"] = {"status": "unavailable"}

    try:
        fixture = A.Fixture(args.windows_audio_fixture) if args.windows_audio_fixture else None
        native.fixture = fixture
        cue = root / "known-prerequisite-cue.wav"
        N.cue(cue)
        prerequisite = native("preflight", cue)
        report["prerequisites"] = N.classify("windows", "windows", prerequisite["checks"])
        report["prerequisite_measurements"] = prerequisite["measurements"]
        report["audio_capture_route"] = prerequisite["measurements"].get("audio_route")
        if fixture:
            report["audio_fixture"] = fixture.value
            P.require(report["audio_capture_route"] == fixture.value["route"], "native_prerequisite_route_changed")
        P.require(report["prerequisites"]["status"] == "prerequisites_ready", "windows_client_native_prerequisites_unavailable")
        if fixture:
            report["audio_fixture_verified"] = True
        report.update(windows_client=True, render_route_observed=True)
        wt, powershell = shutil.which("wt.exe"), shutil.which("pwsh.exe")
        P.require(wt and powershell, "existing_windows_terminal_and_powershell_required")
        sink, initial_provider = P.Webhooks(), F.Provider("success")
        env = F.prepare(root, initial_provider, "success")
        env["TEST_PI_NATIVE_DIAGNOSTIC"] = "1"
        env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
        (root / "foreign-TEST-root").mkdir()
        P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink)
        D.command(args.candidate, ["internal-install-runtime", "--stage", args.runtime_stage, "--target", root / "runtime/bin",
                                  "--entry", args.candidate.name, "--control-root", root / "control",
                                  "--consumer", "pi-runtime-preparation"], root, env)
        P.setup(args.candidate, "install", root, args.package, args.pi_executable, env,
                ("--hooks", "false", "--agent-notify", "true", "--pi-desktop", "true", "--pi-webhook", "true"))
        P.require(json.loads((root / "control/agent-notifications.json").read_text(encoding="utf-8")).get("enabled") is False,
                  "pi_setup_enabled_unrelated_mcp_policy")
        report["unrelated_mcp_policy_enabled"] = False
        view = P.installed(root / "control", root / "agent")
        P.require(P.sha(view["runtime"]["executable"]) == P.sha(args.candidate), "native_installed_candidate_mismatch")
        report["installed_asset_sha256"] = P.asset_receipts(root / "control", root / "runtime", args.assets_root)
        report["product_executed"] = True
        try:
            report["native_shell_state_before"] = shell_notification_state()
        except Exception as error:
            report["native_shell_state_before"] = {"status": "unavailable", "failure": str(error)[-2000:]}
        for name in CASES:
            case_root = root / ("case-" + name)
            provider = F.Provider("success", hold_reply=name != "question")
            origin = distractor = capture = neutral = watcher = None
            case_deadline = None
            primary_failure = None
            stop_capture, stop_neutral = case_root / "capture.stop", case_root / "neutral.stop"
            try:
                case_env = F.prepare(case_root, provider, "success", agent_dir=root / "agent")
                case_env["TEST_PI_NATIVE_DIAGNOSTIC"] = "1"
                case_env.update(TEST_PI_DEFER_DIALOG="1", CLAUDE_PLUGIN_ROOT=str(root / "foreign-TEST-root"))
                nonce = uuid.uuid4().hex
                origin_title, distractor_title, toast_title = "TEST-Pi-origin-" + nonce, "TEST-Pi-distractor-" + nonce, "TEST-Pi-toast-" + nonce
                diagnostic_title = toast_title
                project = case_root / origin_title
                (case_root / "project").rename(project)
                distraction = case_root / distractor_title; distraction.mkdir()
                desktop, sound, click = name != "desktop-off", name != "sound-off", name != "click-off"
                P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink, desktop=desktop, sound=sound, click=click)
                cfg = json.loads((root / "global/config.json").read_text(encoding="utf-8"))
                # Match the actual configured title exactly, without the
                # default presentation suffix " [Pi]".
                cfg["notifications"]["desktop"]["showSessionLabel"] = False
                event = "question" if name == "question" else "task_complete"
                cfg["statuses"][event]["title"] = toast_title
                (root / "global/config.json").write_text(json.dumps(cfg) + "\n", encoding="utf-8")
                argv = ["--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools", "--provider", "TEST-local",
                        "--model", "TEST-local", "--tui-mode", "regular"]
                if name != "question":
                    argv.append("TEST say done")
                origin = Terminal(wt, powershell, case_root / "origin", origin_title, args.pi_executable, argv, case_env, project, native, owned=terminals)
                wait_for(lambda: (case_root / "witness.jsonl").exists()
                         and any(row["type"] == "session_start" for row in F.witness(case_root)), "real_pi_tui_start_missing")
                rows = F.witness(case_root)
                P.require(all(row.get("mode") == "tui" for row in rows if row["type"] != "factory"), "actual_pi_mode_not_tui")
                origin.bind_process(next(row["pid"] for row in rows if row["type"] == "factory"))
                distractor = Terminal(wt, powershell, case_root / "distractor", distractor_title, None, [], case_env, distraction, native, owned=terminals)
                pcm = case_root / "native-output.s16le"
                capture = subprocess.Popen([str(args.observer), "capture", str(pcm), str(stop_capture), *native.route_arguments()], stdout=subprocess.PIPE,
                                           stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)
                helpers.append(capture)
                ready = json.loads(capture.stdout.readline())
                P.require(ready.get("ready") is True, "actual_audio_capture_not_ready")
                P.require(ready.get("audio_route") == report["audio_capture_route"], "native_capture_readiness_route_changed")
                capture_started = time.monotonic()
                case_deadline = capture_started + CAPTURE_BUDGET_SECONDS
                timing = {"case": name, "scope": "diagnostic_only", "capture_budget_seconds": CAPTURE_BUDGET_SECONDS}
                report.setdefault("toast_observation_timings", []).append(timing)
                watcher = start_toast_watcher(native, toast_title, P.COPY[event], case_root, case_deadline, helpers)
                timing["early_watcher_ready_seconds"] = round(time.monotonic() - capture_started, 3)
                if name == "question":
                    native.focus(origin.window)
                    webhook_deadline = time.monotonic() + 25
                    native.type(origin.window, "/TEST-dialog")
                else:
                    wait_for(lambda: bool(provider.requests), "native_pi_model_request_missing")
                native.focus(distractor.window)
                timing["provider_release_seconds"] = round(time.monotonic() - capture_started, 3)
                if name != "question":
                    webhook_deadline = time.monotonic() + 25
                provider.release.set()
                # The trigger precedes Go admission. Observe its 20s product
                # budget with a fixed margin, never renewed after typing/focus.
                sink.wait(event, timeout=max(0, min(webhook_deadline, case_deadline) - time.monotonic()))
                timing["webhook_received_seconds"] = round(time.monotonic() - capture_started, 3)
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
                    timing["late_toast_observer_started_seconds"] = round(time.monotonic() - capture_started, 3)
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
                P.require(capture.returncode == 0 and not errors, "native_audio_capture_failed")
                audio = json.loads(rest)
                P.require(audio.get("audio_route") == report["audio_capture_route"], "native_capture_terminal_route_changed")
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
            except Exception as error:
                primary_failure = str(error)
                report["native_observation_failure"] = {"case": name, "failure": primary_failure}
                report["case_failure"] = {"case": name, "failure": str(error)}
                observe_failure()  # Before the owned Terminal windows are closed.
                raise
            finally:
                actions = [(role, terminal.close) for role, terminal in (("origin", origin), ("distractor", distractor)) if terminal]
                actions.extend((("capture", lambda: stop_helper(capture, stop_capture)),
                                ("neutral", lambda: stop_helper(neutral, stop_neutral)), ("provider", provider.close),
                                ("toast_watcher", lambda: collect_toast_watcher(watcher, case_root / "early-toast-observation.json", case_deadline))))
                cleanup_case(actions, primary_failure=primary_failure, report=report, case=name)
        report.update(status="passed_native_product_observations", native_product_observations_passed=True)
    except Exception as error:
        report["failure"] = str(error)
        observe_failure()
    finally:
        failures = [row["failure"] for row in report.get("case_cleanup_failures", [])]
        for terminal in reversed(terminals):
            try:
                terminal.close()
            except Exception as error:
                failures.append(str(error))
        report["terminal_cleanup"] = [{"case": terminal.root.parent.name, "role": terminal.root.name,
                                      "process_identity": terminal.pi_identity, "process_cleanup": terminal.process_cleanup,
                                      "stage_failures": terminal.cleanup_stage_failures,
                                      "window_closed": terminal.window_closed, "collected": terminal.closed}
                                     for terminal in terminals]
        for helper in helpers:
            if helper.poll() is None:
                helper.kill(); helper.communicate(timeout=3)
        if initial_provider:
            initial_provider.close()
        if sink:
            sink.close()
        if "native_toast_readiness_after" not in report:
            try:
                report["native_toast_readiness_after"] = toast_readiness(diagnostic_title)
            except Exception as error:
                report["native_toast_readiness_after"] = {"status": "unavailable", "failure": str(error)[-2000:]}
        report["owned_children_collected"] = not failures and all(t.closed for t in terminals) and all(p.poll() is not None for p in helpers)
        if failures:
            report.update(status="failed", native_product_observations_passed=False, cleanup_failures=failures)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        for case_root in root.glob("case-*"):
            for name in ("native-output.s16le", "actual-product-banner.bmp", "witness.jsonl",
                         "early-product-banner.bmp", "early-toast-ready.json", "early-toast-observation.json",
                         "origin/launch-state.json", "origin/launch-at-failure.json", "origin/process.exit", "origin/launch-failure.bmp",
                         "distractor/launch-state.json", "distractor/launch-at-failure.json", "distractor/process.exit", "distractor/launch-failure.bmp"):
                source = case_root / name
                bound = 64 * 1024 * 1024 if source.suffix in (".bmp", ".s16le") else 65536
                if source.is_file() and not source.is_symlink() and source.stat().st_size <= bound:
                    target = args.report.parent / "native-captures" / case_root.name / name
                    target.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(source, target)
        failure_image = root / "failure-desktop.bmp"
        if failure_image.is_file() and failure_image.stat().st_size <= 64 * 1024 * 1024:
            shutil.copy2(failure_image, args.report.parent / failure_image.name)
        center_image = root / "notification-center.bmp"
        if center_image.is_file() and center_image.stat().st_size <= 64 * 1024 * 1024:
            shutil.copy2(center_image, args.report.parent / center_image.name)
        mark_diagnostic_unqualified(report)
        args.report.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["native_product_observations_passed"] and report["owned_children_collected"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "runtime-stage", "package", "assets-root", "pi-executable", "observer", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--windows-audio-fixture", type=Path)
    args = parser.parse_args()
    for name in ("candidate", "runtime_stage", "package", "assets_root", "pi_executable", "observer"):
        setattr(args, name, getattr(args, name).resolve(strict=True))
    raise SystemExit(run(args))
