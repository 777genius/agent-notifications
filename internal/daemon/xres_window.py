#!/usr/bin/python3
"""Bounded XRes 1.2 local-window owner and lifetime marker (embedded in Go).

Only the daemon's fixed-argument Go wrapper may invoke this. Never infer owner
from the forgeable EWMH _NET_WM_PID property or from an application title.
"""
import ctypes as ct
import re
import sys

class Spec(ct.Structure):
    _fields_ = [("client", ct.c_ulong), ("mask", ct.c_uint)]

class Value(ct.Structure):
    _fields_ = [("spec", Spec), ("length", ct.c_long), ("value", ct.c_void_p)]

def symbol(lib, name, inputs, returns):
    function = getattr(lib, name)
    function.argtypes, function.restype = inputs, returns
    return function

def fail():
    raise SystemExit(1)

def main():
    if len(sys.argv) != 5:
        fail()
    mode, display_name, xid_text, nonce = sys.argv[1:]
    if mode not in ("owner", "mark", "check") or not re.fullmatch(r":[0-9]{1,4}(?:\.[0-9]{1,2})?", display_name):
        fail()
    if not re.fullmatch(r"[0-9]+", xid_text):
        fail()
    xid = int(xid_text)
    if not 0 < xid < 2**32:
        fail()
    if mode != "owner" and not re.fullmatch(r"[0-9a-f]{32}", nonce):
        fail()
    if mode == "owner" and nonce:
        fail()

    # XRes's fixed ABI is 64-bit for qualified linux/amd64 & linux/arm64.
    if ct.sizeof(ct.c_ulong) != 8 or ct.sizeof(Spec) != 16 or ct.sizeof(Value) != 32:
        fail()
    x = ct.CDLL("libX11.so.6")
    res = ct.CDLL("libXRes.so.1")
    open_display = symbol(x, "XOpenDisplay", [ct.c_char_p], ct.c_void_p)
    close_display = symbol(x, "XCloseDisplay", [ct.c_void_p], ct.c_int)
    query_ext = symbol(res, "XResQueryExtension", [ct.c_void_p, ct.POINTER(ct.c_int), ct.POINTER(ct.c_int)], ct.c_int)
    query_version = symbol(res, "XResQueryVersion", [ct.c_void_p, ct.POINTER(ct.c_int), ct.POINTER(ct.c_int)], ct.c_int)
    query_ids = symbol(res, "XResQueryClientIds", [ct.c_void_p, ct.c_long, ct.POINTER(Spec), ct.POINTER(ct.c_long), ct.POINTER(ct.POINTER(Value))], ct.c_int)
    get_type = symbol(res, "XResGetClientIdType", [ct.POINTER(Value)], ct.c_int)
    get_pid = symbol(res, "XResGetClientPid", [ct.POINTER(Value)], ct.c_int)
    destroy = symbol(res, "XResClientIdsDestroy", [ct.c_long, ct.POINTER(Value)], None)
    open_x = open_display(display_name.encode("ascii"))
    if not open_x:
        fail()
    try:
        event, error, major, minor = ct.c_int(), ct.c_int(), ct.c_int(), ct.c_int()
        if not query_ext(open_x, ct.byref(event), ct.byref(error)) or not query_version(open_x, ct.byref(major), ct.byref(minor)) or (major.value, minor.value) < (1, 2):
            fail()
        query = Spec(xid, 2)
        count = ct.c_long()
        values = ct.POINTER(Value)()
        try:
            rc = query_ids(open_x, 1, ct.byref(query), ct.byref(count), ct.byref(values))
            if rc != 0 or count.value != 1 or not values:
                fail()
            value = values.contents
            if value.spec.mask != 2 or value.length != 4 or not value.value or get_type(values) != 1:
                fail()
            pid = get_pid(values)
            if pid <= 1:
                fail()
        finally:
            if values:
                destroy(count, values)

        if mode != "owner":
            intern = symbol(x, "XInternAtom", [ct.c_void_p, ct.c_char_p, ct.c_int], ct.c_ulong)
            get_prop = symbol(x, "XGetWindowProperty", [ct.c_void_p, ct.c_ulong, ct.c_ulong, ct.c_long, ct.c_long, ct.c_int, ct.c_ulong, ct.POINTER(ct.c_ulong), ct.POINTER(ct.c_int), ct.POINTER(ct.c_ulong), ct.POINTER(ct.c_ulong), ct.POINTER(ct.c_void_p)], ct.c_int)
            free = symbol(x, "XFree", [ct.c_void_p], ct.c_int)
            name = b"_AGENT_NOTIFICATIONS_STRICT_ORIGIN"
            atom = intern(open_x, name, 0 if mode == "mark" else 1)
            if not atom:
                fail()
            actual, fmt, count, remaining, data = ct.c_ulong(), ct.c_int(), ct.c_ulong(), ct.c_ulong(), ct.c_void_p()
            rc = get_prop(open_x, xid, atom, 0, 128, 0, 31, ct.byref(actual), ct.byref(fmt), ct.byref(count), ct.byref(remaining), ct.byref(data))
            if rc != 0 or remaining.value != 0:
                fail()
            try:
                if actual.value == 0 and not data:
                    values = []
                elif actual.value == 31 and fmt.value == 8 and data and count.value <= 512:
                    raw = ct.string_at(data, count.value).decode("ascii")
                    values = raw.split(",") if raw else []
                    if any(not re.fullmatch(r"[0-9a-f]{32}", v) for v in values):
                        fail()
                else:
                    fail()
            finally:
                if data:
                    free(data)
            if mode == "check":
                if nonce not in values:
                    fail()
            else:
                change = symbol(x, "XChangeProperty", [ct.c_void_p, ct.c_ulong, ct.c_ulong, ct.c_ulong, ct.c_int, ct.c_int, ct.c_void_p, ct.c_int], ct.c_int)
                sync = symbol(x, "XSync", [ct.c_void_p, ct.c_int], ct.c_int)
                values = (values + [nonce])[-8:]
                encoded = ",".join(values).encode("ascii")
                buf = ct.create_string_buffer(encoded)
                change(open_x, xid, atom, 31, 8, 0, ct.cast(buf, ct.c_void_p), len(encoded))
                sync(open_x, 0)
        print(pid)
    finally:
        close_display(open_x)

if __name__ == "__main__":
    main()
