#!/usr/bin/env python3
"""TEST-only Linux /proc witness for one live OpenCode SSE TCP socket pair.

Never supplies native observer admission. Requires the listening host's accepted
TCP fd and the test reader's client fd to be live in their respective PIDs.
"""
import os
import re
import sys


SOCKET_FD = re.compile(r"socket:\[([0-9]+)\]\Z")


def identity(pid):
    # /proc/<pid>/stat field 2 (comm) may contain ')' and whitespace.
    with open(f"/proc/{pid}/stat", encoding="ascii") as stream:
        text = stream.read()
    close = text.rfind(") ")
    if close == -1:
        raise ValueError("proc_stat_unavailable")
    fields = text[close + 2:].split()  # field 3 (state) is index zero.
    if len(fields) < 20 or not fields[19].isdigit():
        raise ValueError("proc_stat_invalid")
    return int(fields[19]), os.stat(f"/proc/{pid}/ns/net").st_ino


def sockets(pid):
    inodes = set()
    # Missing fds can be caused by legitimate concurrent closes; fail closed
    # by requiring the specific socket in the collected set below.
    with os.scandir(f"/proc/{pid}/fd") as entries:
        for entry in entries:
            try:
                target = os.readlink(entry.path)
            except FileNotFoundError:
                continue
            match = SOCKET_FD.fullmatch(target)
            if match:
                inodes.add(match.group(1))
    return inodes


def connections(pid):
    with open(f"/proc/{pid}/net/tcp", encoding="ascii") as stream:
        next(stream)
        for line in stream:
            fields = line.split()
            if len(fields) > 9 and fields[3] == "01":  # TCP_ESTABLISHED
                yield fields[1], fields[2], fields[9]


def paired(pid, port, client_port, start_tick, netns):
    if identity(pid) != (start_tick, netns):
        return False
    client = os.getppid()  # The Python test harness which owns HTTPConnection.
    if identity(client)[1] != netns:
        return False
    host_fds, client_fds = sockets(pid), sockets(client)
    host_local, host_remote = f"0100007F:{port:04X}", f"0100007F:{client_port:04X}"
    client_local, client_remote = host_remote, host_local
    entries = list(connections(pid))
    host_match = any(a == host_local and b == host_remote and inode in host_fds
                     for a, b, inode in entries)
    client_match = any(a == client_local and b == client_remote and inode in client_fds
                       for a, b, inode in entries)
    return host_match and client_match and identity(pid) == (start_tick, netns)


def main(argv):
    if len(argv) == 2 and argv[0] == "--identity" and argv[1].isdecimal():
        try:
            start, ns = identity(int(argv[1]))
            if not start or not ns:
                return 1
            print(start, ns)
            return 0
        except (OSError, ValueError):
            return 1
    if len(argv) != 5 or not all(value.isdecimal() for value in argv):
        return 2
    pid, port, local, start, ns = map(int, argv)
    if pid <= 0 or not (0 < port < 65536 and 0 < local < 65536) or port == local or start <= 0 or ns <= 0:
        return 2
    try:
        if not paired(pid, port, local, start, ns):
            return 1
    except (OSError, ValueError):
        return 1
    print("TEST_SOCKET_PAIR_MATCH")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
