#!/usr/bin/env python3
"""Opt-in dunst 1.9.2 TEST callback probe; no native UI confirmation claim."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True)
    parser.add_argument("--dunstctl", required=True)
    parser.add_argument("--dunst", required=True)
    args = parser.parse_args()
    root = Path(args.root)
    evidence = {"passed": False, "passedScope": "probe_execution_and_positive_controls", "navigationQualified": False, "connectionLifecycleOnly": True, "nativeUIVisualConfirmed": False, "observations": []}
    connections = []
    trusted_root = False

    def record(kind, **fields):
        evidence["observations"].append({"kind": kind, "monotonic": time.monotonic(), **fields})

    def require(condition, reason):
        if not condition:
            raise RuntimeError(reason)

    try:
        require(os.environ.get("AGENT_NOTIFY_NAVIGATION_LINUX_E2E") == "1", "explicit opt-in required")
        require(root.is_absolute() and root.resolve() == root and root.name.startswith("navigation-dbus-test-"), "physical named TEST root required")
        require(root.stat().st_mode & 0o777 == 0o700, "private TEST root required")
        marker = json.loads((root / ".owned-test-root.json").read_text())
        require(marker.get("purpose") == "TEST navigation Linux callback", "owned TEST marker required")
        address = os.environ.get("DBUS_SESSION_BUS_ADDRESS", "")
        display = os.environ.get("DISPLAY", "")
        require(address.startswith("unix:") and address == marker.get("busAddress"), "private bus address does not match marker")
        require("/run/user/" not in address and address != marker.get("userBusAddress"), "user session bus prohibited")
        require(display.startswith(":") and display == marker.get("display") and marker.get("xvfb") is True, "owned Xvfb display required")
        trusted_root = True
        tool = Path(args.dunstctl)
        require(tool.is_absolute() and tool.resolve().is_relative_to(root), "dunstctl must belong to TEST root")
        daemon = Path(args.dunst)
        require(daemon.is_absolute() and daemon.resolve().is_relative_to(root), "dunst must belong to TEST root")
        evidence.update(root=str(root), busAddress=address, display=display, package="dunst 1.9.2-1build2", sourceSHA="12e8b6bf5740c49bfe6c7cb497c3fcefad3811df", senderProcessDeathProved=False, lifetimeBoundary="connection_disconnect")

        import dbus
        from dbus.mainloop.glib import DBusGMainLoop
        from gi.repository import GLib
        DBusGMainLoop(set_as_default=True)
        context = GLib.MainContext.default()

        def connect(role):
            connection = dbus.bus.BusConnection(address)
            connections.append(connection)
            record("connection", role=role, uniqueName=connection.get_unique_name())
            return connection

        def wait(predicate, seconds=3):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                # Drain without an unbounded blocking GLib loop.
                for _ in range(32):
                    if not context.pending():
                        break
                    context.iteration(False)
                if predicate():
                    return True
                time.sleep(0.005)
            return predicate()

        def control(*arguments):
            try:
                result = subprocess.run([str(tool), *arguments], cwd=root, capture_output=True, text=True, timeout=3, check=False)
            except subprocess.TimeoutExpired as error:
                def text(value):
                    return value.decode("utf-8", errors="replace") if isinstance(value, bytes) else (value or "")
                record("dunstctl", arguments=list(arguments), timeout=True, stdout=text(error.stdout), stderr=text(error.stderr))
                raise
            record("dunstctl", arguments=list(arguments), exitCode=result.returncode, stdout=result.stdout, stderr=result.stderr)
            require(result.returncode == 0, "native dunstctl failed")
            return result.stdout.strip()

        observer = connect("observer")
        bus = dbus.Interface(observer.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"), "org.freedesktop.DBus")
        owner = str(bus.GetNameOwner("org.freedesktop.Notifications", timeout=3))
        require(owner.startswith(":"), "daemon unique owner required")
        daemon_pid = int(bus.GetConnectionUnixProcessID(owner, timeout=3))
        require(Path(f"/proc/{daemon_pid}/exe").resolve() == daemon.resolve(), "bus owner is not the extracted TEST daemon")
        evidence.update(daemonPID=daemon_pid, daemonPath=str(daemon), daemonSHA256=hashlib.sha256(daemon.read_bytes()).hexdigest(), dunstctlPath=str(tool))
        server = dbus.Interface(observer.get_object(owner, "/org/freedesktop/Notifications"), "org.freedesktop.Notifications")
        information = [str(value) for value in server.GetServerInformation(timeout=3)]
        record("daemon", owner=owner, information=information)
        require(information[0] == "dunst" and information[2] in ("1.9.2", "1.9.2 (2023-04-20)"), "pinned daemon version required")
        # A private-bus monitor records real daemon emission and destination.
        # Without this evidence, absence at the replacement proves too little.
        monitor = connect("private-monitor")
        monitored = []
        def monitor_message(connection, message):
            if message.get_type() == dbus.lowlevel.MESSAGE_TYPE_SIGNAL and message.get_interface() == "org.freedesktop.Notifications" and message.get_member() == "ActionInvoked":
                arguments = message.get_args_list()
                event = {"id": int(arguments[0]), "action": str(arguments[1]), "sender": message.get_sender(), "destination": message.get_destination()}
                monitored.append(event)
                record("monitor-ActionInvoked", **event)
        monitor.add_message_filter(monitor_message)
        monitoring = dbus.Interface(monitor.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"), "org.freedesktop.DBus.Monitoring")
        monitoring.BecomeMonitor(dbus.Array(["type='signal',interface='org.freedesktop.Notifications',member='ActionInvoked'"], signature="s"), dbus.UInt32(0), timeout=3)
        control("close-all")
        require(control("count", "displayed") == "0", "initial displayed queue not empty")

        def subscribe(connection, role):
            events = []
            def received(identifier, action, sender=None, destination=None):
                event = {"id": int(identifier), "action": str(action), "sender": sender, "destination": destination}
                events.append(event)
                record("ActionInvoked", role=role, **event)
            connection.add_signal_receiver(received, signal_name="ActionInvoked", dbus_interface="org.freedesktop.Notifications", path="/org/freedesktop/Notifications", sender_keyword="sender", destination_keyword="destination")
            return events

        def notify(connection, title):
            native = dbus.Interface(connection.get_object(owner, "/org/freedesktop/Notifications"), "org.freedesktop.Notifications")
            identifier = int(native.Notify("navigation-callback-TEST", dbus.UInt32(0), "", title, "Synthetic TEST only", dbus.Array(["default", "TEST"], signature="s"), dbus.Dictionary({"suppress-sound": dbus.Boolean(True)}, signature="sv"), dbus.Int32(0), timeout=3))
            record("Notify", uniqueName=connection.get_unique_name(), id=identifier, actions=["default", "TEST"])
            require(wait(lambda: control("count", "displayed") == "1"), "one displayed TEST notification required")
            return identifier

        live = connect("live-sender")
        live_events = subscribe(live, "live-sender")
        live_id = notify(live, "TEST live callback")
        control("action", "0")
        require(wait(lambda: len(live_events) > 0), "live native ActionInvoked missing")
        require(len(live_events) == 1 and live_events[0]["id"] == live_id and live_events[0]["action"] == "default" and live_events[0]["sender"] == owner, "live callback contract mismatch")
        require(wait(lambda: any(event["id"] == live_id and event["action"] == "default" and event["sender"] == owner and event["destination"] == live.get_unique_name() for event in monitored)), "native monitor positive control missing")
        live.close()
        control("close-all")
        require(control("count", "displayed") == "0", "live notification not drained")

        doomed = connect("doomed-sender")
        doomed_id = notify(doomed, "TEST callback after sender disconnect")
        old_name = doomed.get_unique_name()
        doomed.close()
        require(wait(lambda: not bool(bus.NameHasOwner(old_name, timeout=3))), "dead unique name still owned")
        record("sender-disconnected", uniqueName=old_name, nameHasOwner=False)
        replacement = connect("replacement")
        require(replacement.get_unique_name() != old_name, "replacement reused unique name")
        replacement_events = subscribe(replacement, "replacement")
        control("action", "0")
        require(wait(lambda: any(event["id"] == doomed_id and event["sender"] == owner for event in monitored)), "disconnected-sender native emission missing")
        wait(lambda: len(replacement_events) > 0, seconds=1)
        stale = [event for event in replacement_events if event["id"] == doomed_id]
        emissions = [event for event in monitored if event["id"] == doomed_id]
        evidence["disconnectedCallbackNotInherited"] = not stale
        record("disconnected-sender-observation", originalUniqueName=old_name, replacementUniqueName=replacement.get_unique_name(), id=doomed_id, replacementReceived=bool(stale), callbacks=stale, nativeEmissions=emissions, observationWindowSeconds=1)
        require(any(event["sender"] == owner and event["destination"] == old_name and event["action"] == "default" for event in emissions), "native emission destination differs from original unique name")
        # Replacement reception remains an observation, not a universal guarantee.
        control("close-all")
        replacement_events.clear()
        fresh_id = notify(replacement, "TEST replacement positive control")
        control("action", "0")
        require(wait(lambda: any(event["id"] == fresh_id and event["action"] == "default" and event["sender"] == owner for event in replacement_events)), "replacement positive control missing")
        require(str(bus.GetNameOwner("org.freedesktop.Notifications", timeout=3)) == owner, "daemon owner changed during probe")
        control("close-all")
        evidence["passed"] = True
    except Exception as error:
        evidence["error"] = {"type": type(error).__name__, "message": str(error)}
    finally:
        for connection in connections:
            try:
                connection.close()
            except Exception as error:
                record("close-error", message=str(error))
                evidence["passed"] = False
        # Invalid/unowned roots never receive files. Emit evidence on stdout too.
        if trusted_root:
            try:
                with open(root / "linux-callback-evidence.json", "x", encoding="utf-8") as output:
                    json.dump(evidence, output, indent=2)
                    output.write("\n")
            except Exception as error:
                evidence["passed"] = False
                evidence["evidenceWriteError"] = str(error)
        print(json.dumps(evidence, indent=2))
    return 0 if evidence["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
