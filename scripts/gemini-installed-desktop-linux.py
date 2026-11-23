#!/usr/bin/env python3
"""Installed Gemini local gateway -> native hook -> product banner -> X11 click.

Only the model service and webhook destination use the existing bounded localhost
fixture. The CLI, installed hook command, daemon, banner and focus route are real.
The daemon is explicitly started: this does not qualify daemon autostart, Google
authentication, HID input, other events, other agents or other desktop platforms.
"""
import argparse
import ctypes
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace

REPO = Path(__file__).resolve().parents[1]
CASES = ("positive", "click-off", "revoke-after-banner", "stale-origin")
CLI_EXIT_OBSERVER = r'''umask 077
set -C
an_test_exit_receipt=$1
shift
"$@"
an_test_cli_exit=$?
printf '%s\n' "$an_test_cli_exit" > "$an_test_exit_receipt" || exit 125
exit "$an_test_cli_exit"
'''


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


N = module("gemini_desktop_primitives", REPO / "scripts/pi-native-preflight.py")
G0 = module("gemini_desktop_provider", REPO / "tests/integration/gemini_native_e2e.py")
G5 = module("gemini_desktop_contracts", REPO / "tests/integration/gemini_notifications_e2e.py")
XRES_OWNER_HELPER = REPO / "scripts/navigation-linux-portal-callback-probe.py"
XRES_OWNER_WORKER = r'''
import importlib.util
import json
import os
import sys

spec = importlib.util.spec_from_file_location("TEST_xres_owner", sys.argv[1])
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)
window = None if sys.argv[3] == "-" else int(sys.argv[3])
owner = helper.XResWindowOwner(sys.argv[2])
try:
    result = {"schema_version": 1, "xres_version": owner.version,
              "window": window, "pid": None if window is None else owner.pid(window),
              "worker_pid": os.getpid(), "parent_pid": os.getppid(),
              "owned_unmapped_window_self_check": True}
finally:
    owner.close()
# Xlib can terminate this worker even during close. Emit no success until it
# returns; the parent never imports the helper or owns an Xlib connection.
result["connection_closed"] = True
print(json.dumps(result, separators=(",", ":")))
'''


class Failed(Exception):
    pass


def require(value, code):
    if not value:
        raise Failed(code)


def sha(path):
    return hashlib.sha256(G5.bounded_read(Path(path), 128 * 1024 * 1024)).hexdigest()


def write_json(path, value):
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    temporary = path.with_suffix(".TEST-new")
    with temporary.open("x", encoding="utf-8") as stream:
        os.chmod(temporary, 0o600)
        stream.write(json.dumps(value, sort_keys=True) + "\n")
    os.replace(temporary, path)


def run_command(argv, cwd, env, timeout=20):
    return G0.bounded_process([str(x) for x in argv], b"", cwd, env, timeout)


def json_command(argv, cwd, env, timeout=20, permit_required=False):
    code, out, err = run_command(argv, cwd, env, timeout)
    require(not err, "operator_stderr_not_empty")
    try:
        value = json.loads(out)
    except (ValueError, UnicodeError):
        raise Failed("operator_json_invalid")
    require(type(value) is dict, "operator_json_object_required")
    allowed = permit_required and code == 1 and value.get("reason") == "configuration_required"
    require(code == 0 or allowed, "operator_command_failed")
    return value


def generation(value):
    result = value.get("generation")
    require(type(result) is int and 0 < result < 2**64, "fresh_positive_generation_required")
    return result


def vcs_fields(output, expected_sha, architecture):
    fields = {}
    for line in output.splitlines():
        match = re.fullmatch(r"\s*build\s+([A-Za-z0-9_.]+)=(.*)", line)
        if match:
            require(match[1] not in fields, "duplicate_build_setting")
            fields[match[1]] = match[2]
    require(fields.get("vcs") == "git" and fields.get("vcs.revision") == expected_sha
            and fields.get("vcs.modified") == "false", "candidate_source_identity_mismatch")
    require(fields.get("GOOS") == "linux" and fields.get("GOARCH") == architecture
            and fields.get("CGO_ENABLED") == "1", "candidate_native_build_mismatch")
    return {key: fields[key] for key in
            ("vcs", "vcs.revision", "vcs.modified", "GOOS", "GOARCH", "CGO_ENABLED")}


def sdk_projection(raw, cwd):
    """Observe one closed SDK JSON prefix; never claim a complete SDK flush."""
    require(len(raw) <= 2 * 1024 * 1024, "sdk_capture_bound")
    text, offset, records, hooks = raw.decode("utf-8"), 0, 0, []
    decoder = json.JSONDecoder()
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        require(records < 1024, "sdk_record_bound")
        try:
            item, end = decoder.raw_decode(text, offset)
        except ValueError:
            raise Failed("sdk_prefix_invalid")
        require(type(item) is dict and end - offset <= G0.LIMIT, "sdk_record_shape")
        records += 1
        offset = end
        attributes = item.get("attributes", {})
        if not isinstance(attributes, dict) or attributes.get("event.name") != "gemini_cli.hook_call":
            continue
        require(attributes.get("hook_name") == G5.OWN["AfterAgent"]
                and attributes.get("hook_event_name") == "AfterAgent"
                and attributes.get("hook_type") == "command", "unexpected_native_hook")
        require(attributes.get("success") is True and type(attributes.get("exit_code")) is int
                and attributes["exit_code"] == 0,
                "native_product_hook_unsuccessful")
        incoming = attributes.get("hook_input")
        require(isinstance(incoming, str) and len(incoming) <= G0.LIMIT, "sdk_hook_input_bound")
        try:
            value = json.loads(incoming)
        except ValueError:
            raise Failed("sdk_hook_input_invalid")
        require(type(value) is dict and value.get("cwd") == str(cwd)
                and value.get("hook_event_name") == "AfterAgent"
                and value.get("stop_hook_active") is False, "native_after_agent_contract")
        require(all(isinstance(value.get(key), str) and 0 < len(value[key]) <= 4096
                    for key in ("session_id", "timestamp")), "native_hook_identity_missing")
        hooks.append(value["session_id"])
    require(len(hooks) == 1, "one_native_after_agent_required")
    return {"records": records, "owned_AfterAgent_successes": 1,
            "capture": "closed_observed_prefix", "SDK_flush_complete": False,
            "absence_means": "unknown"}


def validated_focus_sample(observe, expected):
    sample = observe()
    require(sample == expected, "click_focus_receipt_mismatch")
    return sample


def unchanged_focus(observe, expected, seconds=15, now=time.monotonic, pause=time.sleep):
    started, samples = now(), 0
    while True:
        require(observe() == expected, "negative_click_changed_focus")
        samples += 1
        elapsed = now() - started
        if elapsed >= seconds:
            return {"seconds": round(elapsed, 3), "samples": samples,
                    "unchanged_at_observed_samples": True,
                    "observation_method": "sampled_polling",
                    "requested_sleep_max_seconds": 0.05,
                    "continuous_transition_proof": False}
        pause(min(0.05, seconds - elapsed))


def observe_native_click(desktop, row, expected, positive):
    # Preserve the server's default action binding for strict notifications.
    # An action-free informational banner is dismissed with its middle button.
    button = 1 if row["default_action"] else 2
    desktop.click(button)
    # Establish actual native delivery before starting the focus acceptance
    # window. A delayed action must not arrive after an already-passed receipt.
    native_notify = desktop.observer.after_click(row)
    native_notify.update(mouse_button=button,
                         input_semantics="default_action" if row["default_action"]
                         else "informational_user_dismissal")
    observation = None
    if positive:
        N.wait_for(lambda: desktop.focus() == expected,
                   seconds=8, failure="native_click_did_not_focus_origin")
    else:
        observation = unchanged_focus(desktop.focus, expected)
    observed = validated_focus_sample(desktop.focus, expected)
    return native_notify, observation, observed


def process_record(pid):
    try:
        path = Path("/proc") / str(pid)
        before = (path / "stat").read_text().rsplit(")", 1)[1].split()
        try:
            executable = os.readlink(path / "exe")
        except (FileNotFoundError, PermissionError):
            executable = ""
        after = (path / "stat").read_text().rsplit(")", 1)[1].split()
        if before[19] != after[19]:
            return None
        return {"pid": int(pid), "ppid": int(after[1]), "state": after[0],
                "birth": int(after[19]), "executable": executable}
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        return None


def children(pid):
    entries = list(Path("/proc").iterdir())
    require(len(entries) <= 65536, "process_inventory_bound")
    found = []
    for path in entries:
        if path.name.isdecimal():
            record = process_record(int(path.name))
            if record and record["ppid"] == pid:
                found.append(record)
    return found


def descendants(pid):
    result, pending = [], [pid]
    while pending:
        require(len(result) < 128, "owned_process_tree_bound")
        current = children(pending.pop())
        result.extend(current)
        pending.extend(item["pid"] for item in current if item["state"] != "Z")
    return result


def still_present(record):
    current = process_record(record["pid"])
    return current is not None and current["birth"] == record["birth"]


class HeldProvider(G0.Fixture):
    """Hold only a real model response; never inject an event or hook payload."""
    def __init__(self, lab):
        super().__init__(lab, G5.capture)
        self.request_seen = threading.Event()
        self.release = threading.Event()

    def arm_turn(self):
        self.release.clear()
        self.request_seen.clear()
        self.arm("plain")

    def answer(self, path, body):
        if path.endswith(":streamGenerateContent") and not self.request_seen.is_set():
            self.request_seen.set()
            require(self.release.wait(45), "model_response_gate_timeout")
        return super().answer(path, body)

    def __exit__(self, *args):
        self.release.set()
        return super().__exit__(*args)


def config_document(url, click):
    return {"schemaVersion": 2,
            "notifications": {"desktop": {"enabled": True, "sound": False, "clickToFocus": True},
                              "webhook": {"enabled": False}},
            "agents": {"gemini": {"notifications": {
                "desktop": {"enabled": True, "sound": False, "systemSound": False,
                            "terminalBell": False, "clickToFocus": click,
                            "terminalClickConsentVersion": 1},
                "webhook": {"enabled": True, "preset": "custom", "format": "json",
                            "url": url + "/capture", "headers": {}, "payloadFields": {},
                            "retry": {"enabled": False}, "rateLimit": {"enabled": False},
                            "circuitBreaker": {"enabled": False}}}}}}


class Installation:
    def __init__(self, args, root, env, selected):
        self.args, self.root, self.env, self.selected = args, root, env, selected
        self.options = SimpleNamespace(binary=str(args.candidate), desktop=True, native_app=None)
        self.executable = args.candidate
        self.registered = False
        self.install_attempted = False
        self.cleanup_attempts = []
        self.candidate_hash = sha(args.candidate)

    def status(self):
        value = json_command([self.executable, "setup-notifications", "status",
                              "--control-root", self.root / "an-control",
                              "--runtime-root", self.root / "an-runtime",
                              "--global-config", self.selected, "--json"],
                             self.root / "profile", self.env, permit_required=True)
        generation(value)
        return value

    def change(self, enable):
        before = self.status()
        argv = [self.executable, "setup-notifications", "enable" if enable else "disable",
                "--control-root", self.root / "an-control",
                "--runtime-root", self.root / "an-runtime",
                "--expected-generation", str(generation(before)), "--json"]
        if enable:
            argv += ["--global-config", self.selected, "--navigation", "none",
                     "--allow-unknown-caller", "false", "--allow-caller-asserted", "false"]
        value = json_command(argv, self.root / "profile", self.env, timeout=180)
        require(value.get("reason") == ("enabled" if enable else "disabled")
                and value.get("explicitIntent") is enable
                and value.get("runtimeEligible") is enable, "managed_policy_transition_failed")
        after = self.status()
        require(after.get("explicitIntent") is enable and after.get("runtimeEligible") is enable
                and generation(after) == generation(value), "managed_policy_readback_failed")
        snapshot, settings = G5.snapshot(self.root, self.candidate_hash)
        require(snapshot["policy"].get("enabled") is enable
                and snapshot["policy"].get("route", {}).get("geminiNotifications")
                == {"desktop": True, "webhook": True}, "gemini_channels_not_preserved")
        require(snapshot["settings_sha256"] == self.settings_hash, "managed_policy_changed_native_hooks")
        return {"before": generation(before), "after": generation(after), "enabled": enable}

    def install(self):
        # A failed transaction may already have changed managed state.
        self.install_attempted = True
        G5.setup(G0, self.options, self.root, self.env, "install")
        self.registered = True
        snapshot, settings = G5.snapshot(self.root, self.candidate_hash)
        require(set(settings.get("hooks", {})) == set(G5.OWN), "nonproduct_hook_group")
        for event, name in G5.OWN.items():
            groups = settings["hooks"][event]
            require(len(groups) == 1 and len(groups[0]["hooks"]) == 1
                    and groups[0]["hooks"][0].get("name") == name, "nonproduct_hook_command")
        self.settings_hash = snapshot["settings_sha256"]
        self.executable = Path(snapshot["ledger"]["Consumers"][G5.CONSUMER]["Commands"][0])
        require(sha(self.executable) == self.candidate_hash, "installed_candidate_hash_mismatch")
        selected = json_command([self.executable, "config", "path", "--json"],
                                self.root / "profile", self.env)
        require(selected.get("path") == str(self.selected), "canonical_config_selection_mismatch")
        G5.inspect(G0, self.options, self.root, self.env, True)
        return self.change(True)

    def remove(self):
        # Each supported cleanup step has its own bounded subprocesses. A
        # failed disable (including partial initialization) must not skip
        # removal, and a failed removal must not skip the absence inspection.
        self.cleanup_attempts = []
        for stage, operation in (
                ("disable", lambda: self.change(False)),
                ("remove", lambda: G5.setup(G0, self.options, self.root, self.env, "remove")),
                ("inspect_absent", lambda: G5.inspect(G0, self.options, self.root, self.env, False))):
            result = {"stage": stage, "status": "failed"}
            self.cleanup_attempts.append(result)
            try:
                operation()
            except Exception as error:
                result["error"] = failure_code(error)
            else:
                result["status"] = "passed"
                if stage == "inspect_absent":
                    self.registered = False
                    self.install_attempted = False
        require(all(item["status"] == "passed" for item in self.cleanup_attempts),
                "managed_cleanup_failed")


def source_identity(args, env):
    architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
    require(platform.system() == "Linux" and architecture is not None, "native_linux_host_required")
    facts = {}
    for name, argv in (
            ("sha", ["git", "rev-parse", "HEAD"]),
            ("tree", ["git", "rev-parse", "HEAD^{tree}"]),
            ("tracked_changes", ["git", "status", "--porcelain", "--untracked-files=no"])):
        code, out, err = run_command(argv, REPO, env)
        require(code == 0 and not err, "checkout_identity_unavailable")
        facts[name] = out.decode("ascii").strip()
    require(facts["sha"] == args.source_sha and not facts["tracked_changes"]
            and re.fullmatch(r"[0-9a-f]{40}", facts["tree"]), "checkout_source_identity_mismatch")
    go = shutil.which("go")
    require(go is not None, "go_build_identity_tool_missing")
    code, out, err = run_command([go, "version", "-m", args.candidate], REPO, env)
    require(code == 0 and not err, "candidate_build_identity_unavailable")
    return {"source_sha": facts["sha"], "source_tree": facts["tree"],
            "tracked_source_clean": True,
            "candidate_build": vcs_fields(out.decode("utf-8"), args.source_sha, architecture)}


def fixture_settings(root, args):
    settings_path = G0.install_test_hooks(root, {}, args.hook_shell, args.node_executable)
    settings = G5.read_json(settings_path)
    # The existing helper supplies isolated upstream settings. Its recorder
    # groups are removed BEFORE product installation; no recorder is executed.
    settings["hooks"] = {}
    settings["security"]["environmentVariableRedaction"] = {
        "enabled": True,
        "allowed": ["DISPLAY", "WINDOWID", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR",
                    "XDG_SESSION_TYPE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
                    "XDG_DATA_HOME", "XDG_STATE_HOME", "AGENT_NOTIFICATIONS_CONFIG", "LC_ALL"]}
    write_json(settings_path, settings)


def notify_call_projection(signature, arguments, sender, serial):
    """Accept only the two real product Notify contracts; retain no payload."""
    require(signature == "susssasa{sv}i" and len(arguments) == 8, "native_notify_signature")
    require(isinstance(sender, str) and re.fullmatch(r":[0-9]+\.[0-9]+", sender)
            and 0 < int(serial) < 2**32, "native_notify_sender_identity")
    app, replacement, icon, title, body, actions, hints, expiry = arguments
    require(all(isinstance(value, str) and len(value) <= 8192 for value in (app, icon, title, body))
            and int(replacement) == 0 and title == "Gemini CLI"
            and body == G5.COPY["task_complete"], "native_notify_fixed_copy")
    require(isinstance(actions, (list, tuple)) and len(actions) <= 2
            and all(isinstance(value, str) and len(value) <= 64 for value in actions)
            and isinstance(hints, dict) and len(hints) <= 32
            and -(2**31) <= int(expiry) < 2**31, "native_notify_argument_bound")
    strict = app == "claude-notifications" and list(actions) == ["default", "Focus Terminal"]
    plain = app == "agent-notifications" and not actions
    require(strict or plain, "native_notify_action_contract")
    return {"sender": sender, "serial": int(serial), "default_action": strict,
            "id": None, "finished": False}


class NotifyObserver:
    """Read-only typed observer on the owned bus; never invokes Notify/action."""
    def __init__(self, address, dunst, daemon):
        import dbus
        from dbus.mainloop.glib import DBusGMainLoop
        from gi.repository import GLib
        DBusGMainLoop(set_as_default=True)
        self.dbus, self.context = dbus, GLib.MainContext.default()
        self.connections, self.calls, self.actions, self.closed, self.error = [], [], [], [], None
        self.dunst, self.daemon = dunst, daemon
        self.daemon_identity = process_record(daemon.pid)
        try:
            observer = dbus.bus.BusConnection(address)
            observer.set_exit_on_disconnect(False)
            self.connections.append(observer)
            self.bus = dbus.Interface(observer.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"),
                                      "org.freedesktop.DBus")
            self.owner = str(self.bus.GetNameOwner("org.freedesktop.Notifications", timeout=3))
            require(dunst.poll() is None
                    and int(self.bus.GetConnectionUnixProcessID(self.owner, timeout=3)) == dunst.pid,
                    "notification_owner_is_not_owned_dunst")
            monitor = dbus.bus.BusConnection(address)
            monitor.set_exit_on_disconnect(False)
            self.connections.append(monitor)
            monitor.add_message_filter(self.message)
            monitoring = dbus.Interface(monitor.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"),
                                        "org.freedesktop.DBus.Monitoring")
            rules = ["type='method_call',interface='org.freedesktop.Notifications',member='Notify'",
                     "type='method_return',sender='" + self.owner + "'",
                     "type='signal',sender='" + self.owner
                     + "',interface='org.freedesktop.Notifications',member='ActionInvoked'",
                     "type='signal',sender='" + self.owner
                     + "',interface='org.freedesktop.Notifications',member='NotificationClosed'"]
            # Synchronous acknowledgement establishes subscription readiness.
            monitoring.BecomeMonitor(dbus.Array(rules, signature="s"), dbus.UInt32(0), timeout=3)
        except Exception:
            self.close()
            raise

    def message(self, connection, message):
        try:
            kind, sender = message.get_type(), message.get_sender()
            if (kind == self.dbus.lowlevel.MESSAGE_TYPE_METHOD_CALL
                    and message.get_interface() == "org.freedesktop.Notifications"
                    and message.get_member() == "Notify"):
                require(len(self.calls) < 16
                        and message.get_path() == "/org/freedesktop/Notifications"
                        and message.get_destination() in ("org.freedesktop.Notifications", self.owner),
                        "unexpected_native_notify_route")
                row = notify_call_projection(str(message.get_signature()), message.get_args_list(),
                                             sender, message.get_serial())
                require(not any((item["sender"], item["serial"]) == (row["sender"], row["serial"])
                                for item in self.calls), "duplicate_native_notify_serial")
                self.calls.append(row)
            elif kind == self.dbus.lowlevel.MESSAGE_TYPE_METHOD_RETURN and sender == self.owner:
                matched = [row for row in self.calls if row["sender"] == message.get_destination()
                           and row["serial"] == message.get_reply_serial()]
                if matched:
                    args = message.get_args_list()
                    require(len(matched) == 1 and matched[0]["id"] is None
                            and str(message.get_signature()) == "u" and len(args) == 1
                            and 0 < int(args[0]) < 2**32
                            and not any(row["id"] == int(args[0]) for row in self.calls),
                            "native_notify_id_reply_mismatch")
                    matched[0]["id"] = int(args[0])
            elif (kind == self.dbus.lowlevel.MESSAGE_TYPE_SIGNAL and sender == self.owner
                  and message.get_interface() == "org.freedesktop.Notifications"
                  and message.get_member() == "ActionInvoked"):
                args = message.get_args_list()
                require(len(self.actions) < 16 and str(message.get_signature()) == "us"
                        and message.get_path() == "/org/freedesktop/Notifications"
                        and len(args) == 2 and str(args[1]) == "default",
                        "unexpected_native_action_signal")
                self.actions.append({"id": int(args[0]), "destination": message.get_destination()})
            elif (kind == self.dbus.lowlevel.MESSAGE_TYPE_SIGNAL and sender == self.owner
                  and message.get_interface() == "org.freedesktop.Notifications"
                  and message.get_member() == "NotificationClosed"):
                args = message.get_args_list()
                require(len(self.closed) < 16 and str(message.get_signature()) == "uu"
                        and message.get_path() == "/org/freedesktop/Notifications"
                        and len(args) == 2 and 0 < int(args[0]) < 2**32
                        and int(args[1]) in (1, 2, 3, 4), "unexpected_native_close_signal")
                self.closed.append({"id": int(args[0]), "reason": int(args[1]),
                                    "destination": message.get_destination()})
        except Exception as error:
            self.error = failure_code(error)
        # Consume monitored calls without dispatching or replying on this
        # connection. True is integer 1 (NOT_YET_HANDLED), not this enum.
        return self.dbus.lowlevel.HANDLER_RESULT_HANDLED

    def pump(self):
        require(self.connections and all(connection.get_is_connected() for connection in self.connections),
                "native_monitor_disconnected")
        for _ in range(32):
            if not self.context.pending():
                break
            self.context.iteration(False)
        require(self.error is None, self.error or "native_monitor_failed")

    def begin(self):
        self.pump()
        require(all(row["finished"] for row in self.calls), "unclaimed_native_notification")
        return len(self.calls)

    def before_click(self, marker, strict):
        def observed():
            self.pump()
            require(len(self.calls) <= marker + 1, "multiple_native_banners_for_one_turn")
            return len(self.calls) == marker + 1 and self.calls[-1]["id"] is not None
        N.wait_for(observed, seconds=5, failure="actual_notify_and_id_not_observed")
        row = self.calls[-1]
        require(row["default_action"] is strict, "native_banner_action_precondition_failed")
        require(self.dunst.poll() is None
                and str(self.bus.GetNameOwner("org.freedesktop.Notifications", timeout=3)) == self.owner,
                "owned_notification_server_changed")
        if strict:
            require(self.daemon.poll() is None and self.daemon_identity is not None
                    and still_present(self.daemon_identity)
                    and int(self.bus.GetConnectionUnixProcessID(row["sender"], timeout=3)) == self.daemon.pid,
                    "strict_notification_not_from_owned_product_daemon")
        require(not any(action["id"] == row["id"] for action in self.actions),
                "native_action_preceded_x11_click")
        require(not any(event["id"] == row["id"] for event in self.closed),
                "native_banner_closed_before_x11_click")
        return row

    def after_click(self, row):
        dismissal = None
        def events():
            self.pump()
            return [event for event in self.actions if event["id"] == row["id"]]
        if row["default_action"]:
            N.wait_for(lambda: bool(events()), seconds=3, failure="real_dunst_action_not_observed")
            observed = events()
            require(len(observed) == 1 and observed[0]["destination"] in (None, row["sender"]),
                    "real_dunst_action_destination_mismatch")
        else:
            def dismissals():
                self.pump()
                return [event for event in self.closed if event["id"] == row["id"]]
            # The TEST server's middle binding performs close_current. With
            # no action, only a real user-dismissed close
            # proves the click reached this banner. Expiry/DBus closure cannot.
            # This check precedes history(), which itself calls close-all.
            N.wait_for(lambda: bool(dismissals()), seconds=3,
                       failure="informational_click_delivery_not_observed")
            dismissed = dismissals()
            require(len(dismissed) == 1 and dismissed[0]["reason"] == 2
                    and dismissed[0]["destination"] == row["sender"],
                    "informational_click_not_owned_user_dismissal")
            dismissal = {"notification_id": dismissed[0]["id"], "reason": dismissed[0]["reason"]}
            require(not events(), "informational_banner_emitted_action")
        row["finished"] = True
        return {"notification_id": row["id"], "default_action_present_before_mutation": row["default_action"],
                "product_daemon_sender_verified": row["default_action"],
                "authentic_dunst_action_observed": row["default_action"],
                "informational_user_dismissal_observed": not row["default_action"],
                "native_user_dismissal": dismissal,
                "native_click_delivery": "ActionInvoked" if row["default_action"] else "NotificationClosed_reason_2",
                "Notify_reply_id_joined": True}

    def complete(self):
        self.pump()
        require(len(self.calls) == len(CASES) and all(row["finished"] for row in self.calls)
                and len(self.actions) == len(CASES) - 1, "native_notification_observation_incomplete")

    def close(self):
        errors = []
        for connection in reversed(self.connections):
            try:
                connection.close()
            except Exception:
                errors.append("native_monitor_connection_close_failed")
        self.connections = []
        require(not errors, "native_monitor_connection_close_failed")


def xres_worker_result(raw, window):
    require(isinstance(raw, str) and 0 < len(raw.encode("utf-8")) <= 4096,
            "native_window_owner_reply_bound")
    def closed_object(pairs):
        require(len({key for key, _ in pairs}) == len(pairs),
                "native_window_owner_duplicate_field")
        return dict(pairs)
    try:
        value = json.loads(raw, object_pairs_hook=closed_object)
    except (ValueError, UnicodeError):
        raise Failed("native_window_owner_reply_invalid")
    fields = {"schema_version", "xres_version", "window", "pid", "worker_pid",
              "parent_pid", "owned_unmapped_window_self_check", "connection_closed"}
    require(type(value) is dict and set(value) == fields
            and type(value["schema_version"]) is int and value["schema_version"] == 1,
            "native_window_owner_reply_shape")
    version = value["xres_version"]
    require(type(version) is list and len(version) == 2
            and all(type(part) is int and 0 <= part <= 65535 for part in version)
            and tuple(version) >= (1, 2), "native_window_owner_version_invalid")
    require(type(value["worker_pid"]) is int and 0 < value["worker_pid"] < 2**31
            and value["worker_pid"] != os.getpid()
            and type(value["parent_pid"]) is int and value["parent_pid"] == os.getpid(),
            "native_window_owner_worker_identity_invalid")
    if window is None:
        require(value["window"] is None and value["pid"] is None,
                "native_window_owner_probe_shape")
    else:
        require(type(value["window"]) is int and value["window"] == window
                and type(value["pid"]) is int and 0 < value["pid"] < 2**31,
                "native_window_owner_pid_invalid")
    require(value["owned_unmapped_window_self_check"] is True
            and value["connection_closed"] is True, "native_window_owner_worker_incomplete")
    return value


class IsolatedWindowOwner:
    """Each Xlib connection belongs to one synchronous, bounded child."""
    def __init__(self, lab):
        self.lab, self.version = lab, None
        self.report = {"api": "XRes_local_client_PID", "isolation": "bounded_child_per_query",
                       "parent_x11_connection": False, "worker_timeout_seconds": 5,
                       "receipt_max_bytes": 4096, "queries": []}

    def query(self, window):
        require(len(self.report["queries"]) < 8, "native_window_owner_query_bound")
        attempt = {"window": window, "status": "failed"}
        self.report["queries"].append(attempt)
        try:
            try:
                code, out, err = run_command(
                    [sys.executable, "-I", "-B", "-c", XRES_OWNER_WORKER, XRES_OWNER_HELPER,
                     self.lab.env["DISPLAY"], "-" if window is None else str(window)],
                    self.lab.root, self.lab.env, timeout=5)
            except subprocess.TimeoutExpired:
                raise Failed("native_window_owner_worker_timeout")
            attempt["worker_exit"] = code
            require(code == 0, "native_window_owner_worker_failed")
            require(not err, "native_window_owner_worker_stderr")
            value = xres_worker_result(out.decode("utf-8"), window)
            require(self.version is None or value["xres_version"] == self.version,
                    "native_window_owner_version_changed")
            attempt.update(value, status="passed")
            return value
        except Exception as error:
            attempt["failure"] = failure_code(error)
            raise

    def probe(self):
        value = self.query(None)
        self.version = value["xres_version"]
        self.report.update(version=self.version, owned_unmapped_window_self_check=True)

    def pid(self, window):
        require(re.fullmatch(r"[1-9][0-9]{0,9}", str(window))
                and int(window) < 2**32, "native_window_owner_xid_invalid")
        return self.query(int(window))["pid"]


class GeminiDesktopLab(N.Lab):
    """Verify the owned Dunst's ordinary action/dismissal bindings at startup."""
    def start(self, argv):
        if Path(argv[0]).name == "dunst":
            config = self.root / "dunstrc"
            require(len(argv) == 3 and argv[1] == "-config" and Path(argv[2]) == config
                    and config.is_file() and not config.is_symlink(), "owned_dunst_config_required")
            content = config.read_text()
            require(content.count("mouse_left_click = do_action\n") == 1
                    and content.count("mouse_middle_click = close_current\n") == 1,
                    "owned_dunst_binding_mismatch")
            self.report["dunst_click_bindings"] = {"1": "do_action", "2": "close_current"}
        return super().start(argv)


class Desktop:
    def __init__(self, lab, paths, distractor, window_owner):
        self.lab, self.paths, self.distractor = lab, paths, distractor
        self.window_owner = window_owner
        self.distractor_pid = self.window_pid(distractor)
        daemons = [child for child in lab.children
                   if Path(child.args[0]).resolve() == Path(paths["dunst"]).resolve()]
        require(len(daemons) == 1, "owned_banner_server_missing")
        self.banner_server = daemons[0]
        self.banner_server_identity = process_record(self.banner_server.pid)
        require(self.banner_server_identity is not None, "owned_banner_server_identity_missing")

    def x(self, *args):
        return self.lab.run([self.paths["xdotool"], *args], timeout=5)[0].strip()

    def window_pid(self, window):
        return int(self.x("getwindowpid", window))

    def focus(self):
        if hasattr(self, "observer"):
            self.observer.pump()
        return {"window": self.x("getactivewindow"),
                "keyboard_pid": int(self.x("getwindowfocus", "getwindowpid"))}

    def activate(self, window, pid):
        self.x("windowactivate", "--sync", window)
        N.wait_for(lambda: self.focus() == {"window": window, "keyboard_pid": pid},
                   seconds=5, failure="owned_window_focus_not_observed")

    def displayed(self):
        return int(self.lab.run(["dunstctl", "count", "displayed"], timeout=5)[0].strip())

    def reset(self):
        self.lab.run(["dunstctl", "close-all"], timeout=5)
        self.lab.run(["dunstctl", "history-clear"], timeout=5)
        N.wait_for(lambda: self.displayed() == 0, failure="banner_baseline_not_empty")

    def origin(self, terminal, title):
        def locate():
            require(terminal.poll() is None, "actual_cli_terminal_exited")
            out, code = self.lab.run([self.paths["xdotool"], "search", "--onlyvisible",
                                      "--pid", str(terminal.pid)], timeout=5, check=False)
            xid = N.owned_visible_xterm_window(out, code)
            if xid and self.window_pid(xid) == terminal.pid:
                return xid
            # A title only locates a candidate; the held xterm PID must match.
            titled, status = self.lab.run(
                [self.paths["xdotool"], "search", "--onlyvisible", "--name", "^" + re.escape(title) + "$"],
                timeout=5, check=False)
            candidate = N.owned_visible_xterm_window(titled, status)
            owner, owner_status = self.lab.run(
                [self.paths["xdotool"], "getwindowpid", candidate], timeout=5,
                check=False) if candidate else ("", 1)
            return N.verified_title_window(titled, status, owner, owner_status, terminal.pid)
        return N.wait_for(locate, seconds=18, failure="actual_cli_window_not_mapped")

    def click(self, button=1):
        require(type(button) is int and button in (1, 2), "native_click_button_invalid")
        require(self.displayed() == 1 and self.banner_server.poll() is None,
                "product_banner_missing_at_click")
        self.x("mousemove", "1100", "70")
        pointer = dict(line.split("=", 1) for line in self.x("getmouselocation", "--shell").splitlines())
        window = pointer.get("WINDOW", "")
        require(window.isascii() and window.isdecimal() and int(window) > 0
                and still_present(self.banner_server_identity)
                and self.window_owner.pid(window) == self.banner_server.pid
                and self.banner_server.poll() is None,
                "click_pointer_not_over_owned_banner")
        self.x("click", str(button))

    def history(self):
        self.lab.run(["dunstctl", "close-all"], timeout=5)
        N.wait_for(lambda: self.displayed() == 0)
        value = json.loads(self.lab.run(["dunstctl", "history"], timeout=5)[0])
        rows = value["data"][0]
        require(len(rows) == 1 and rows[0]["summary"]["data"] == "Gemini CLI"
                and G5.COPY["task_complete"] in rows[0]["body"]["data"],
                "native_product_banner_copy_not_observed")

    def quit(self, terminal, origin, nodes):
        require(terminal.poll() is None, "actual_cli_exited_before_quit")
        self.activate(origin, terminal.pid)
        self.x("type", "--clearmodifiers", "--delay", "1", "--", "/quit")
        self.x("key", "--clearmodifiers", "Return")
        N.wait_for(lambda: terminal.poll() is not None, seconds=12,
                   failure="actual_cli_graceful_exit_unconfirmed")
        require(terminal.returncode == 0, "actual_cli_exit_nonzero")
        N.wait_for(lambda: all(not still_present(node) for node in nodes), seconds=5,
                   failure="actual_cli_descendants_not_collected")


def actual_cli_nodes(terminal, args):
    result = []
    for record in descendants(terminal.pid):
        if record["state"] != "Z" and record["executable"] == str(args.node_executable):
            try:
                argv = G5.bounded_read(Path("/proc") / str(record["pid"]) / "cmdline", 65536)
            except FileNotFoundError:
                continue
            if str(args.gemini_executable).encode() in argv.split(b"\0"):
                result.append(record)
    return result


def run_case(name, args, root, installation, provider, desktop, report):
    progress = {"case": name, "phase": "prepare"}
    report["active_case"] = progress
    provider.arm_turn()
    write_json(installation.selected, config_document(provider.url, name != "click-off"))
    if not installation.status().get("explicitIntent"):
        progress["policy_enable"] = installation.change(True)
    snapshot, _ = G5.snapshot(root, installation.candidate_hash)
    require(snapshot["settings_sha256"] == installation.settings_hash, "native_hooks_changed")
    telemetry = root / "sdk-private/telemetry.json"
    telemetry.write_bytes(b"")
    desktop.reset()
    notify_marker = desktop.observer.begin()
    with provider.lock:
        deliveries_before = len(provider.deliveries)
        streams_before = provider.counts.get("streamGenerateContent", 0)
    title = "TEST Gemini installed " + name
    exit_receipt = root / ("cli-exit-" + name + ".txt")
    require(not exit_receipt.exists(), "cli_exit_receipt_already_present")
    # This shell only waits for the real child and records its numeric exit.
    # Every command/path is a positional argument, never evaluated shell text.
    argv = [desktop.paths["xterm"], "-T", title,
            "-geometry", "100x35+20+150", "-e", args.hook_shell, "-c", CLI_EXIT_OBSERVER,
            "TEST-gemini-exit-observer", exit_receipt, args.node_executable, args.gemini_executable,
            "--model", "gemini-2.5-flash", "--approval-mode", "default",
            "--skip-trust", "--prompt-interactive", "AN_TEST_PLAIN"]
    require("WINDOWID" not in installation.env, "inherited_window_id_forbidden")
    terminal = subprocess.Popen([str(x) for x in argv], cwd=root / "profile", env=installation.env,
                                stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, start_new_session=True)
    desktop.lab.children.append(terminal)
    nodes = []
    try:
        terminal_record = process_record(terminal.pid)
        require(terminal_record is not None, "owned_terminal_identity_missing")
        progress["phase"] = "native_model_request"
        origin = desktop.origin(terminal, title)
        nodes = N.wait_for(lambda: actual_cli_nodes(terminal, args), seconds=12,
                           failure="actual_installed_cli_process_not_observed")
        N.wait_for(lambda: provider.request_seen.is_set(), seconds=40,
                   failure="actual_cli_local_model_request_missing")
        require(provider.error is None and not provider.release.is_set(), "model_gate_failed")
        desktop.activate(desktop.distractor, desktop.distractor_pid)
        expected = {"window": desktop.distractor, "keyboard_pid": desktop.distractor_pid}
        require(desktop.focus() == expected and desktop.displayed() == 0,
                "origin_not_backgrounded_before_response")
        provider.release.set()
        progress["phase"] = "native_product_banner"
        def delivered():
            require(provider.error is None, "local_fixture_protocol_error")
            with provider.lock:
                return len(provider.deliveries) == deliveries_before + 1
        N.wait_for(delivered, seconds=18, failure="native_product_webhook_missing")
        N.wait_for(lambda: desktop.displayed() == 1, seconds=10, failure="native_product_banner_missing")
        require(desktop.focus() == expected, "product_focused_before_click")
        screenshot = root / ("banner-" + name + ".png")
        desktop.lab.run([desktop.paths["ffmpeg"], "-loglevel", "error", "-f", "x11grab",
                         "-video_size", "1280x800", "-i", installation.env["DISPLAY"],
                         "-frames:v", "1", screenshot], timeout=8)
        native_call = desktop.observer.before_click(notify_marker, name != "click-off")
        if name == "revoke-after-banner":
            progress["policy_revocation"] = installation.change(False)
        elif name == "stale-origin":
            desktop.quit(terminal, origin, nodes)
            desktop.activate(desktop.distractor, desktop.distractor_pid)
            require(not still_present(terminal_record) and terminal.poll() == 0, "stale_origin_still_alive")
        progress["phase"] = "native_banner_click"
        require(desktop.focus() == expected, "focus_changed_before_click")
        native_notify, observation, observed = observe_native_click(
            desktop, native_call, {"window": origin, "keyboard_pid": terminal.pid}
            if name == "positive" else expected, name == "positive")
        desktop.history()
        if terminal.poll() is None:
            desktop.quit(terminal, origin, nodes)
        progress["phase"] = "native_receipt_projection"
        require(G5.bounded_read(exit_receipt, 32) == b"0\n", "native_cli_exit_receipt_not_zero")
        sdk = sdk_projection(G5.bounded_read(telemetry, 2 * 1024 * 1024), root / "profile")
        # There is one real native turn per fresh CLI. A second capture is a
        # failure; this bounded quiet window is not an unlimited replay claim.
        time.sleep(0.4)
        with provider.lock:
            require(provider.error is None and provider.deliveries[deliveries_before:] == ["task_complete"],
                    "native_delivery_count_mismatch")
            stream_calls = provider.counts.get("streamGenerateContent", 0) - streams_before
        require(stream_calls >= 1 and all(not still_present(node) for node in nodes),
                "native_turn_or_collection_missing")
        report["cases"].append({"case": name, "status": "passed", "actual_cli_mode": "interactive_TUI",
            "provider_stream_calls": stream_calls, "native_hook": sdk, "native_notify": native_notify,
            "product_webhooks": 1, "banner_observed": True, "banner_sha256": sha(screenshot),
            "click_input": "xdotool_X11_XTest", "mouse_button": native_notify["mouse_button"],
            "pointer_over_owned_banner": True, "banner_owner_observer": "XRes_1.2_local_client_PID",
            "native_click_delivery_verified": True,
            "focus_observation_anchor": "native_click_delivery_observed",
            "origin_window": origin, "origin_xterm_pid": terminal.pid,
            "origin_cli_pids": [node["pid"] for node in nodes],
            "foreground_after_click": observed, "unchanged_focus_observation": observation,
            "focus_restored": name == "positive", "terminal_exit": terminal.returncode,
            "cli_exit": 0, "cli_exit_observer": "owned_bash_wait_for_actual_child",
            "terminal_and_cli_collected": True,
            "policy_transition": progress.get("policy_revocation") or progress.get("policy_enable")})
        del report["active_case"]
    finally:
        provider.release.set()
        desktop.lab.stop(terminal)


def collect_adopted(baseline):
    """Only kernel-proven new direct children of this subreaper may be stopped."""
    collected = 0
    deadline = time.monotonic() + 6
    while time.monotonic() < deadline:
        owned = [row for row in children(os.getpid()) if (row["pid"], row["birth"]) not in baseline]
        if not owned:
            return collected
        for row in owned:
            if row["state"] != "Z" and still_present(row):
                try:
                    os.kill(row["pid"], signal.SIGTERM if time.monotonic() < deadline - 2 else signal.SIGKILL)
                except ProcessLookupError:
                    pass
            try:
                pid, _ = os.waitpid(row["pid"], os.WNOHANG)
                if pid:
                    collected += 1
            except ChildProcessError:
                require(not still_present(row), "owned_adopted_child_not_collectible")
        time.sleep(0.05)
    raise Failed("owned_adopted_children_remain")


def failure_code(error):
    if isinstance(error, (Failed, G0.Red, G5.Red, N.Unqualified)):
        value = str(error)
        if re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{0,95}", value):
            return value
    return "unexpected_" + type(error).__name__


def run(args):
    report = {"schema_version": 1, "status": "failed",
              "scope": "installed_gemini_local_gateway_linux_x11",
              "native_product_observations_passed": False, "cases": [],
              "qualification": {"Google_model_service": "unqualified",
                                "authenticated_cloud_flow": "unqualified",
                                "other_events_and_platforms": "unqualified",
                                "other_agents_and_Qoder_audio": "unqualified",
                                "daemon_autostart": "unqualified",
                                "input": "xdotool_X11_XTest_not_HID",
                                "model_provider": "bounded_localhost_HTTP_substitute",
                                "sound": "disabled_not_qualified"}}
    outer = root = lab = provider = installation = None
    observer = window_owner = None
    prior_subreaper, libc, subreaper_set = ctypes.c_int(), None, False
    baseline = set()
    try:
        require(platform.system() == "Linux", "native_linux_host_required")
        for key in ("candidate", "gemini_executable", "node_executable", "cli_install_root", "hook_shell"):
            setattr(args, key, G0.physical(getattr(args, key)))
        outer = Path(tempfile.mkdtemp(prefix="TEST-gemini-desktop-")).resolve()
        outer.chmod(0o700)
        root = G0.new_lab(outer / "gemini")
        env = G0.minimal_env(root, args.node_executable, args.hook_shell)
        env.pop("GEMINI_API_KEY", None)
        env["SHELL"] = str(args.hook_shell)
        # Preserve the actual upstream strict-redaction indicators. The supported
        # safe-name allowlist supplies X11 context without weakening redaction.
        for key in ("GITHUB_SHA", "SURFACE"):
            if key in os.environ:
                env[key] = os.environ[key]
        report.update(source_identity(args, env))
        report.update(host_os=platform.system(), host_architecture=platform.machine(),
                      candidate_binary_sha256=sha(args.candidate), script_sha256=sha(Path(__file__).resolve()),
                      node_binary_sha256=sha(args.node_executable),
                      hook_shell_sha256=sha(args.hook_shell),
                      cli=G0.cli_identity(args.gemini_executable, args.cli_install_root, args.gemini_version),
                      fixture_sha256={path: sha(REPO / path) for path in
                          ("scripts/pi-native-preflight.py", "scripts/navigation-linux-portal-callback-probe.py",
                           "tests/integration/gemini_native_e2e.py",
                           "tests/integration/gemini_notifications_e2e.py")})
        fixture_settings(root, args)
        desktop_root = outer / "desktop"
        desktop_root.mkdir(mode=0o700)
        lab = GeminiDesktopLab(desktop_root, {})
        libc = ctypes.CDLL(None, use_errno=True)
        baseline = {(row["pid"], row["birth"]) for row in children(os.getpid())}
        require(libc.prctl(37, ctypes.byref(prior_subreaper), 0, 0, 0) == 0
                and libc.prctl(36, 1, 0, 0, 0) == 0, "owned_child_collection_unavailable")
        subreaper_set = True
        paths, _, windows = N.linux_desktop(lab, terminal_lifetime=600)
        window_owner = IsolatedWindowOwner(lab)
        report["native_window_owner"] = window_owner.report
        window_owner.probe()
        report["dunst_click_bindings"] = lab.report["dunst_click_bindings"]
        report["desktop_tools"] = {name: lab.report["tools"][name] for name in
                                   ("Xvfb", "openbox", "dunst", "xterm", "xdotool", "dbus-daemon", "ffmpeg")}
        env.update({key: lab.env[key] for key in
                    ("DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "PULSE_SERVER")})
        env["XDG_SESSION_TYPE"] = "x11"
        selected = root / "profile/.claude/claude-notifications-go/config.json"
        env["AGENT_NOTIFICATIONS_CONFIG"] = str(selected)
        require("WINDOWID" not in env and not any(key in env for key in
                    ("SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "WAYLAND_DISPLAY",
                     "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "GOOGLE_API_KEY", "GEMINI_API_KEY")),
                "inherited_origin_or_cloud_credentials_forbidden")
        provider = HeldProvider(root)
        provider.__enter__()
        require(re.fullmatch(r"http://127\.0\.0\.1:[0-9]{1,5}", provider.url), "loopback_provider_required")
        env["GOOGLE_GEMINI_BASE_URL"] = provider.url
        code, out, err = run_command([args.node_executable, args.gemini_executable, "--version"],
                                     root / "profile", env, 20)
        report["cli_version_probe"] = {"exit_code": code, "stdout_sha256": G0.digest(out),
                                       "stderr_sha256": G0.digest(err)}
        require(code == 0 and out.decode("utf-8").strip() == args.gemini_version,
                "actual_cli_version_mismatch")
        write_json(selected, config_document(provider.url, True))
        installation = Installation(args, root, env, selected)
        report["initial_enable"] = installation.install()
        report["installed_binary_sha256"] = sha(installation.executable)
        # Track the actual foreground daemon as an owned child. Native hooks
        # still perform their own ordinary admission, capture and submission.
        lab.env = dict(env)
        daemon = lab.start([installation.executable, "--daemon"])
        socket = Path(env["XDG_RUNTIME_DIR"]) / "claude-notifications.sock"
        def daemon_ready():
            require(daemon.poll() is None, "owned_product_daemon_exited")
            return socket.is_socket()
        N.wait_for(daemon_ready, seconds=10, failure="owned_product_daemon_not_ready")
        identity = process_record(daemon.pid)
        require(identity is not None and identity["executable"] == str(installation.executable),
                "owned_product_daemon_identity_mismatch")
        report["daemon"] = {"explicit_foreground_start": True, "owned_pid": daemon.pid,
                            "installed_binary_sha256": report["installed_binary_sha256"]}
        desktop = Desktop(lab, paths, windows[-1], window_owner)
        observer = NotifyObserver(env["DBUS_SESSION_BUS_ADDRESS"], desktop.banner_server, daemon)
        desktop.observer = observer
        report["native_notify_observer"] = "readonly_typed_BecomeMonitor_on_owned_bus"
        for name in CASES:
            run_case(name, args, root, installation, provider, desktop, report)
        observer.complete()
        report["native_product_observations_passed"] = True
    except Exception as error:
        report["failure"] = failure_code(error)
    finally:
        cleanup_errors = []
        if provider:
            provider.release.set()
        if observer:
            try:
                observer.close()
                report["native_monitor_connections_closed"] = True
            except Exception as error:
                cleanup_errors.append(failure_code(error))
        if installation and (installation.registered or installation.install_attempted):
            try:
                installation.remove()
                report["managed_cleanup"] = "disabled_and_removed"
            except Exception as error:
                cleanup_errors.append(failure_code(error))
            finally:
                report["managed_cleanup_attempts"] = installation.cleanup_attempts
        if provider and provider.server:
            try:
                provider.__exit__(None, None, None)
                report["provider_thread_collected"] = not provider.thread.is_alive()
            except Exception as error:
                cleanup_errors.append(failure_code(error))
        if lab:
            try:
                lab.close()
                report["owned_direct_children_collected"] = lab.report.get("owned_children_collected", False)
            except Exception as error:
                cleanup_errors.append(failure_code(error))
            if "command_failure" in lab.report:
                fault = lab.report["command_failure"]
                report["fixture_command_failure"] = {"tool": fault["tool"], "exit": fault["exit"]}
        if subreaper_set:
            try:
                report["adopted_children_collected"] = collect_adopted(baseline)
            except Exception as error:
                cleanup_errors.append(failure_code(error))
            if libc.prctl(36, prior_subreaper.value, 0, 0, 0) != 0:
                cleanup_errors.append("subreaper_restore_failed")
        clean = (not cleanup_errors and report.get("owned_direct_children_collected") is True
                 and report.get("native_monitor_connections_closed") is True
                 and report.get("provider_thread_collected") is True
                 and report.get("managed_cleanup") == "disabled_and_removed")
        report["owned_children_collected"] = clean
        if outer and not cleanup_errors:
            try:
                shutil.rmtree(outer)
                report["private_fixture_removed"] = True
            except OSError:
                cleanup_errors.append("private_fixture_removal_failed")
        if cleanup_errors:
            report["cleanup_failures"] = cleanup_errors
        passed = (report["native_product_observations_passed"] and clean
                  and not cleanup_errors and len(report["cases"]) == len(CASES))
        report["status"] = "passed_local_provider_desktop_chain" if passed else "failed"
        report["native_product_observations_passed"] = passed
        write_json(args.report, report)
        print(json.dumps({"status": report["status"], "failure": report.get("failure"),
                          "authenticated_cloud_flow": "unqualified"}))
    return 0 if report["native_product_observations_passed"] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "gemini-executable", "node-executable", "cli-install-root", "hook-shell", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--gemini-version", choices=tuple(G0.QUALIFICATION_PINS), default="0.62.0")
    parser.add_argument("--source-sha", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
        parser.error("exact source SHA required")
    if not args.report.is_absolute() or args.report != args.report.resolve():
        parser.error("physical absolute report path required")
    return run(args)


if __name__ == "__main__":
    raise SystemExit(main())
