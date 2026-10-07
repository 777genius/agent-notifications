#!/usr/bin/python3
"""Capture actual Notify calls on an explicitly supplied private test D-Bus."""

import argparse
import json
import signal
from pathlib import Path

import dbus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib


INTERFACE = "org.freedesktop.Notifications"


def plain(value):
    if isinstance(value, dbus.Boolean):
        return bool(value)
    if isinstance(value, dict):
        return {str(key): plain(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [plain(item) for item in value]
    if isinstance(value, str):
        return str(value)
    if isinstance(value, int):
        return int(value)
    if isinstance(value, float):
        return float(value)
    raise TypeError(f"Unsupported D-Bus value: {type(value).__name__}")


class Notifications(dbus.service.Object):
    def __init__(self, bus, output):
        super().__init__(bus, "/org/freedesktop/Notifications")
        self.output = output
        self.next_id = 1

    @dbus.service.method(INTERFACE, in_signature="", out_signature="as")
    def GetCapabilities(self):
        return ["body", "actions", "sound"]

    @dbus.service.method(INTERFACE, in_signature="", out_signature="ssss")
    def GetServerInformation(self):
        return ("Private test capture", "agent-notifications tests", "1", "1.2")

    @dbus.service.method(INTERFACE, in_signature="susssasa{sv}i", out_signature="u")
    def Notify(self, app_name, replaces_id, app_icon, summary, body, actions,
               hints, expire_timeout):
        notification_id = self.next_id
        self.next_id += 1
        record = plain({
            "id": notification_id, "app_name": app_name,
            "replaces_id": replaces_id, "app_icon": app_icon,
            "summary": summary, "body": body, "actions": actions,
            "hints": hints, "expire_timeout": expire_timeout,
        })
        self.output.write(json.dumps(record, ensure_ascii=False) + "\n")
        self.output.flush()
        return dbus.UInt32(notification_id)

    @dbus.service.method(INTERFACE, in_signature="u", out_signature="")
    def CloseNotification(self, notification_id):
        pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--address", required=True)
    parser.add_argument("--outputJSONL", required=True)
    parser.add_argument("--readyFile", required=True)
    args = parser.parse_args()
    ready = Path(args.readyFile)
    ready.unlink(missing_ok=True)
    DBusGMainLoop(set_as_default=True)
    bus = dbus.bus.BusConnection(args.address)
    loop = GLib.MainLoop()
    with open(args.outputJSONL, "w", encoding="utf-8") as output:
        service = Notifications(bus, output)
        name = dbus.service.BusName(INTERFACE, bus, do_not_queue=True)
        GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, signal.SIGTERM, loop.quit)
        GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, signal.SIGINT, loop.quit)
        ready.write_text("ready\n", encoding="utf-8")
        try:
            loop.run()
        finally:
            ready.unlink(missing_ok=True)
            service.remove_from_connection()
            del name
            bus.close()


if __name__ == "__main__":
    main()
