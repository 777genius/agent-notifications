#!/usr/bin/env python3
"""Observe native GUI/audio prerequisites in owned CI TEST fixtures.

This is never a Pi/product qualification receipt. The known window, click and
audio cue establish independent observers before a later candidate is installed.
Missing or unobserved prerequisites exit 2 and remain unqualified.
"""
import argparse
import array
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import time
import uuid
import wave

REPO = Path(__file__).resolve().parents[1]
PLATFORMS = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}
COMMON = ("native_host", "interactive_gui", "two_windows", "foreground_observer",
          "input_observer", "audio_endpoint", "audio_capture", "known_cue_observed")


class Unqualified(Exception):
    pass


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def classify(host, requested, facts):
    """Only independently observed primitive prerequisites can become ready."""
    checks = dict(facts)
    checks["native_host"] = host == requested
    required = COMMON + (("windows_client", "shell", "uia") if requested == "windows" else ())
    missing = [name for name in required if checks.get(name) is not True]
    return {"status": "prerequisites_ready" if not missing else "unqualified",
            "missing": missing, "checks": checks, "desktop_qualified": False,
            "scope": "synthetic_native_observers_only",
            "candidate_notification_permission": "not_observed_without_candidate",
            "candidate_banner_sound_click": "not_qualified"}


def private_environment(root):
    env = {key: os.environ[key] for key in ("PATH", "SystemRoot", "SYSTEMROOT", "WINDIR", "ComSpec", "PATHEXT", "INCLUDE", "LIB", "LIBPATH") if key in os.environ}
    for name in ("home", "tmp", "config", "cache", "runtime", "data"):
        (root / name).mkdir(mode=0o700)
    env.update(HOME=str(root / "home"), USERPROFILE=str(root / "home"),
               TMPDIR=str(root / "tmp"), TMP=str(root / "tmp"), TEMP=str(root / "tmp"),
               XDG_CONFIG_HOME=str(root / "config"), XDG_CACHE_HOME=str(root / "cache"),
               XDG_RUNTIME_DIR=str(root / "runtime"), XDG_DATA_HOME=str(root / "data"),
               LANG="C.UTF-8", LC_ALL="C.UTF-8")
    return env


class Lab:
    def __init__(self, root, report):
        self.root, self.report = root, report
        self.env = private_environment(root)
        self.children = []

    def run(self, argv, timeout=20, check=True):
        proc = subprocess.Popen([str(x) for x in argv], cwd=self.root, env=self.env,
                                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                start_new_session=os.name != "nt")
        try:
            out, err = proc.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.stop(proc)
            raise Unqualified("fixture_command_timeout")
        if len(out) + len(err) > 256 * 1024:
            raise Unqualified("fixture_output_limit")
        if check and proc.returncode:
            self.report["command_failure"] = {"tool": Path(argv[0]).name, "exit": proc.returncode,
                                               "stdout": out.decode(errors="replace")[-6000:],
                                               "stderr": err.decode(errors="replace")[-4000:]}
            raise Unqualified("fixture_command_failed")
        return out.decode("utf-8"), proc.returncode

    def start(self, argv):
        log = self.root / ("child-" + str(len(self.children)) + ".log")
        stream = log.open("wb")
        try:
            child = subprocess.Popen([str(x) for x in argv], cwd=self.root, env=self.env,
                                     stdin=subprocess.DEVNULL, stdout=stream, stderr=stream,
                                     start_new_session=os.name != "nt")
        finally:
            stream.close()
        self.children.append(child)
        return child

    def stop(self, child):
        if child.poll() is not None:
            return
        if os.name == "nt":
            subprocess.run(["taskkill.exe", "/PID", str(child.pid), "/T", "/F"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
        else:
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        try:
            child.wait(timeout=2)
        except subprocess.TimeoutExpired:
            if os.name != "nt":
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            child.wait(timeout=2)

    def close(self):
        for child in reversed(self.children):
            self.stop(child)
        self.report["owned_children_collected"] = all(child.poll() is not None for child in self.children)


def cue(path):
    sample_rate = 48000
    samples = [round(8000 * math.sin(2 * math.pi * 880 * index / sample_rate)) for index in range(sample_rate // 2)]
    with wave.open(str(path), "wb") as audio:
        audio.setnchannels(1)
        audio.setsampwidth(2)
        audio.setframerate(sample_rate)
        audio.writeframes(struct.pack("<" + "h" * len(samples), *samples))


def wait_for(predicate, seconds=8):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        result = predicate()
        if result:
            return result
        time.sleep(0.05)
    raise Unqualified("fixture_readiness_timeout")


def linux_desktop(lab, terminal_lifetime=90):
    """Owned X11/PulseAudio session reused by the actual installed-Pi fixture."""
    names = ("dbus-daemon", "Xvfb", "openbox", "dunst", "xterm", "xdotool", "pulseaudio", "pactl", "parec", "aplay", "notify-send", "ffmpeg")
    paths = {name: shutil.which(name) for name in names}
    lab.report["tools"] = {name: {"available": bool(path), **({"sha256": digest(path)} if path else {})} for name, path in paths.items()}
    if not all(paths.values()):
        raise Unqualified("missing_linux_fixture_tools")
    # Xvfb chooses an unused display using its own displayfd protocol.
    display_file = lab.root / "display"
    with display_file.open("wb") as stream:
        server = subprocess.Popen([paths["Xvfb"], "-displayfd", str(stream.fileno()), "-screen", "0", "1280x800x24", "-nolisten", "tcp"],
                                  cwd=lab.root, env=lab.env, pass_fds=(stream.fileno(),), stdout=subprocess.DEVNULL,
                                  stderr=subprocess.DEVNULL, start_new_session=True)
    lab.children.append(server)
    display = wait_for(lambda: display_file.read_text().strip())
    if not re.fullmatch(r"[0-9]{1,5}", display):
        raise Unqualified("invalid_owned_display")
    lab.env["DISPLAY"] = ":" + display
    lab.start([paths["openbox"], "--sm-disable"])
    bus_socket = lab.root / "runtime/dbus"
    address = "unix:path=" + str(bus_socket)
    lab.start([paths["dbus-daemon"], "--session", "--nofork", "--address=" + address])
    wait_for(lambda: bus_socket.exists())
    lab.env["DBUS_SESSION_BUS_ADDRESS"] = address
    pulse_socket = lab.root / "runtime/pulse-native"
    lab.start([paths["pulseaudio"], "--daemonize=no", "--exit-idle-time=-1", "--disable-shm=yes", "--log-target=stderr", "-n",
               "--load=module-native-protocol-unix socket=" + str(pulse_socket) + " auth-anonymous=1",
               "--load=module-null-sink sink_name=TEST_pi_observer rate=48000 channels=1"])
    lab.env["PULSE_SERVER"] = "unix:" + str(pulse_socket)
    wait_for(lambda: pulse_socket.exists())
    lab.run([paths["pactl"], "set-default-sink", "TEST_pi_observer"])
    (lab.root / "home/.asoundrc").write_text('pcm.!default { type pulse }\nctl.!default { type pulse }\n')
    dunst = lab.root / "dunstrc"
    dunst.write_text('[global]\norigin = top-right\noffset = 20x20\nwidth = 380\nheight = 120\nfont = Monospace 12\nmouse_left_click = do_action\nmouse_middle_click = close_current\nmouse_right_click = close_all\n[urgency_normal]\ntimeout = 20\n')
    lab.start([paths["dunst"], "-config", dunst])
    nonce = uuid.uuid4().hex[:12]
    window_ids = []
    for role in ("origin", "distractor"):
        title = "TEST-pi-" + role + "-" + nonce
        lab.start([paths["xterm"], "-T", title, "-e", sys.executable, "-c", "import time; time.sleep(" + str(terminal_lifetime) + ")"])
        def search():
            out, status = lab.run([paths["xdotool"], "search", "--onlyvisible", "--name", "^" + title + "$"], check=False)
            return out.strip().splitlines()[0] if status == 0 and out.strip() else None
        window_ids.append(wait_for(search))
    for window in window_ids:
        lab.run([paths["xdotool"], "windowactivate", "--sync", window])
        observed, _ = lab.run([paths["xdotool"], "getactivewindow"])
        if observed.strip() != window:
            raise Unqualified("foreground_fixture_mismatch")
    return paths, server, window_ids


def linux(lab, audio):
    paths, server, window_ids = linux_desktop(lab)
    nonce = uuid.uuid4().hex[:12]
    recording = lab.root / "known-cue.s16le"
    with recording.open("wb") as stream:
        recorder = subprocess.Popen([paths["parec"], "--device=TEST_pi_observer.monitor", "--format=s16le", "--rate=48000", "--channels=1", "--latency-msec=20"],
                                    cwd=lab.root, env=lab.env, stdout=stream, stderr=subprocess.DEVNULL, start_new_session=True)
    lab.children.append(recorder)
    # A running PID or elapsed sleep does not establish monitor readiness. Wait
    # for actual baseline PCM, play once, then drain until its nonzero samples
    # reach the file. Pulse/stdio buffering differs on hosted runners.
    wait_for(lambda: recording.stat().st_size >= 9600)
    baseline_samples = recording.stat().st_size // 2
    lab.run([paths["aplay"], "-q", "--buffer-time=20000", audio])
    drain_until = time.monotonic() + 4
    while time.monotonic() < drain_until:
        current = recording.read_bytes()
        probe = array.array("h", current[:len(current) // 2 * 2])
        if sys.byteorder != "little":
            probe.byteswap()
        if any(abs(value) > 1000 for value in probe):
            break
        time.sleep(0.05)
    lab.stop(recorder)
    raw = recording.read_bytes()
    values = array.array("h", raw[:len(raw) // 2 * 2])
    if sys.byteorder != "little":
        values.byteswap()
    peak = max((abs(value) for value in values), default=0)
    rms = math.sqrt(sum(value * value for value in values) / len(values)) if values else 0
    notify = subprocess.Popen([paths["notify-send"], "--app-name=TEST-pi-observer", "--wait", "--action=default=TEST", "TEST-pi-" + nonce, "Synthetic prerequisite fixture"],
                              cwd=lab.root, env=lab.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    lab.children.append(notify)
    time.sleep(0.5)
    shot = lab.root / "known-notification.png"
    lab.run([paths["ffmpeg"], "-loglevel", "error", "-f", "x11grab", "-video_size", "1280x800", "-i", lab.env["DISPLAY"], "-frames:v", "1", shot])
    lab.run([paths["xdotool"], "mousemove", "1100", "70", "click", "1"])
    try:
        action, _ = notify.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        raise Unqualified("native_fixture_click_not_observed")
    lab.report["measurements"] = {"baseline_samples_before_cue": baseline_samples,
                                   "audio_samples": len(values), "audio_peak_s16": peak, "audio_rms_s16": round(rms, 3),
                                   "fixture_action": action.decode().strip(), "window_count": len(set(window_ids)),
                                   "synthetic_screenshot_sha256": digest(shot)}
    return {"interactive_gui": server.poll() is None, "two_windows": len(set(window_ids)) == 2,
            "foreground_observer": True, "input_observer": action.strip() == b"default",
            "audio_endpoint": True, "audio_capture": len(values) >= 4800,
            "known_cue_observed": peak > 1000 and rms > 100}


def native(lab, audio, osname, windows_audio_fixture=None):
    if osname == "darwin":
        source = REPO / "tests/integration/pi_native_preflight_darwin.swift"
        executable = lab.root / "pi-native-preflight"
        lab.run(["swiftc", "-swift-version", "5", "-parse-as-library", source, "-o", executable], timeout=90)
    else:
        source = REPO / "tests/integration/pi_native_preflight_windows.cpp"
        executable = lab.root / "pi-native-preflight.exe"
        # CI prepares cl through the reviewed Visual Studio developer environment.
        lab.run(["cl.exe", "/nologo", "/std:c++20", "/EHsc", "/W4", "/DUNICODE", "/D_UNICODE", source,
                 "/Fe:" + str(executable), "/Fo:" + str(lab.root / "probe.obj"), "/link", "ole32.lib", "user32.lib", "uuid.lib", "winmm.lib", "wtsapi32.lib", "uiautomationcore.lib", "advapi32.lib", "propsys.lib", "cfgmgr32.lib"], timeout=90)
    lab.report["native_probe"] = {"source_sha256": digest(source), "executable_sha256": digest(executable)}
    route = []
    if windows_audio_fixture is not None:
        spec = importlib.util.spec_from_file_location("pi_windows_audio_fixture", REPO / "scripts/pi-windows-audio-fixture.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        fixture = module.Fixture(windows_audio_fixture)
        route = fixture.arguments()
        lab.report["audio_fixture"] = fixture.value
    out, code = lab.run([executable, audio, *route], timeout=30, check=False)
    value = json.loads(out)
    if not isinstance(value, dict) or set(value) != {"checks", "measurements"} or not isinstance(value["checks"], dict):
        raise Unqualified("native_probe_shape")
    if any(type(flag) is not bool for flag in value["checks"].values()):
        raise Unqualified("native_probe_check_shape")
    lab.report["measurements"] = value["measurements"]
    lab.report["native_exit"] = code
    return value["checks"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("linux", "darwin", "windows"), required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--execute-test-fixtures", action="store_true", required=True)
    parser.add_argument("--windows-audio-fixture", type=Path)
    args = parser.parse_args()
    if args.windows_audio_fixture is not None and args.platform != "windows":
        parser.error("virtual audio fixture requires native Windows")
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
        parser.error("exact source SHA required")
    actual = subprocess.check_output(["git", "-C", REPO, "rev-parse", "HEAD"], text=True).strip()
    if actual != args.source_sha:
        parser.error("checkout SHA differs from requested source")
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-preflight-"))
    root.chmod(0o700)
    (root / ".owned-pi-preflight").write_text("synthetic Pi prerequisites only\n")
    report = {"schema_version": 1, "source_sha": actual, "script_sha256": digest(__file__),
              "platform": args.platform, "host": PLATFORMS.get(platform.system()),
              "os_release": platform.release(), "architecture": platform.machine(),
              "product_executed": False, "pi_executed": False, "desktop_qualified": False}
    lab = Lab(root, report)
    facts = {}
    try:
        if report["host"] != args.platform:
            raise Unqualified("physical_platform_mismatch")
        audio = root / "known-cue.wav"
        cue(audio)
        report["known_cue_sha256"] = digest(audio)
        facts = linux(lab, audio) if args.platform == "linux" else native(lab, audio, args.platform, args.windows_audio_fixture)
    except (Unqualified, OSError, ValueError, subprocess.SubprocessError) as error:
        report["prerequisite_failure"] = str(error) if isinstance(error, Unqualified) else type(error).__name__
    finally:
        lab.close()
        report.update(classify(report["host"], args.platform, facts))
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2, allow_nan=False) + "\n")
        print(json.dumps({"status": report["status"], "missing": report["missing"], "failure": report.get("prerequisite_failure"), "desktop_qualified": False}))
    return 0 if report["status"] == "prerequisites_ready" and report["owned_children_collected"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
