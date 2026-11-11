"""TEST-only owned-host SSE observation; never grants production authority."""
import base64
import http.client
import json
import os
import socket
import subprocess
import threading
import time


class NativePeerObservation:
    def __init__(self, host, port, helper):
        self.host, self.port, self.helper = host, port, str(helper)
        self.connection = None
        self.sock = None
        self.local_port = None
        self.expected_start = None
        self.ready = threading.Event()
        self.stopping = threading.Event()
        self.thread = None
        self.events = {}
        self.events_lock = threading.Lock()
        self.error = None
        self.connected = False

    def _tool(self, *args):
        return subprocess.run([self.helper, *map(str, args)], capture_output=True,
                              text=True, timeout=8)

    def start(self):
        if self.host.poll() is not None:
            raise RuntimeError('peer_host_not_live')
        result = self._tool('--identity', self.host.pid)
        parts = result.stdout.strip().split()
        if result.returncode != 0 or len(parts) != 2 or not all(x.isdigit() for x in parts):
            raise RuntimeError('peer_identity_unverified')
        self.expected_start = parts
        self.thread = threading.Thread(target=self._read, daemon=True)
        self.thread.start()
        if not self.ready.wait(10) or self.error or not self.connected:
            raise RuntimeError('peer_feed_not_ready')
        self._check_pair(negatives=True)

    def _read(self):
        try:
            self.connection = http.client.HTTPConnection('127.0.0.1', self.port, timeout=60)
            auth = base64.b64encode(b'opencode:test-only').decode('ascii')
            self.connection.request('GET', '/api/event', headers={'Authorization': 'Basic ' + auth})
            response = self.connection.getresponse()
            if response.status != 200 or not (response.getheader('Content-Type') or '').startswith('text/event-stream'):
                raise RuntimeError('bad_response')
            self.sock = self.connection.sock
            if self.sock is None:
                raise RuntimeError('missing_socket')
            local, peer = self.sock.getsockname(), self.sock.getpeername()
            if local[0] != '127.0.0.1' or peer != ('127.0.0.1', self.port):
                raise RuntimeError('wrong_endpoint')
            self.local_port = local[1]
            while not self.stopping.is_set():
                line = response.readline(262145)
                received_wall_ns = time.time_ns()
                if not line:
                    break
                if len(line) > 262144:
                    raise RuntimeError('frame_limit')
                if not line.startswith(b'data: '):
                    continue
                event = json.loads(line[6:])
                if not isinstance(event, dict):
                    raise RuntimeError('bad_event')
                kind = event.get('type')
                if kind == 'server.connected':
                    if self.connected:
                        raise RuntimeError('duplicate_marker')
                    self.connected = True
                    self.ready.set()
                    continue
                if not self.connected:
                    raise RuntimeError('missing_marker')
                if kind not in ('session.execution.started', 'session.step.ended', 'session.execution.succeeded'):
                    continue
                data = event.get('data')
                if not isinstance(data, dict):
                    raise RuntimeError('bad_data')
                values = (kind, event.get('id'), event.get('created'), data.get('sessionID'))
                if (not isinstance(values[1], str) or not values[1] or type(values[2]) is not int or values[2] <= 0 or
                        not isinstance(values[3], str) or not values[3]):
                    raise RuntimeError('bad_identity')
                with self.events_lock:
                    if len(self.events) >= 4096 or values in self.events:
                        raise RuntimeError('event_limit_or_duplicate')
                    self.events[values] = received_wall_ns
        except Exception:
            if not self.stopping.is_set():
                self.error = 'peer_feed_failed'
        finally:
            self.ready.set()

    def _check_pair(self, negatives=False):
        if (self.host.poll() is not None or self.sock is None or self.expected_start is None or
                self.error or self.thread is None or not self.thread.is_alive()):
            raise RuntimeError('peer_host_not_live')
        try:
            if self.sock.getsockname() != ('127.0.0.1', self.local_port) or self.sock.getpeername() != ('127.0.0.1', self.port):
                raise RuntimeError('peer_endpoint_changed')
        except OSError:
            raise RuntimeError('peer_endpoint_changed') from None
        args = [self.host.pid, self.port, self.local_port, *self.expected_start]
        result = self._tool(*args)
        if result.returncode != 0 or result.stdout.strip() != 'TEST_SOCKET_PAIR_MATCH':
            raise RuntimeError('peer_socket_unverified')
        if negatives:
            wrong_pid = self._tool(os.getpid(), *args[1:])
            wrong_port = self._tool(args[0], args[1], 1 if self.local_port != 1 else 2, *args[3:])
            wrong_start = self._tool(*args[:3], int(self.expected_start[0]) + 1, self.expected_start[1])
            if wrong_pid.returncode == 0 or wrong_port.returncode == 0 or wrong_start.returncode != 1:
                raise RuntimeError('peer_negative_failed')

    def verify_and_close(self, source_rows, session_id):
        expected = {(r.get('type'), r.get('id'), r.get('createdMs'), session_id) for r in source_rows}
        if {x[0] for x in expected} != {'session.execution.started', 'session.step.ended', 'session.execution.succeeded'}:
            raise RuntimeError('peer_events_unverified')
        end = time.monotonic() + 10
        while True:
            with self.events_lock:
                matched = expected.issubset(self.events)
                deltas = [event[2] * 1000000 - self.events[event] for event in expected] if matched else []
            if matched:
                break
            if self.error or time.monotonic() >= end:
                raise RuntimeError('peer_events_unverified')
            time.sleep(.05)
        self._check_pair()
        self.close()
        end = time.monotonic() + 10
        while True:
            if self.host.poll() is not None:
                raise RuntimeError('peer_host_not_live')
            result = self._tool(self.host.pid, self.port, self.local_port, *self.expected_start)
            if result.returncode == 1:
                break
            if time.monotonic() >= end:
                raise RuntimeError('peer_close_unverified')
            time.sleep(.05)
        return min(deltas), max(deltas)

    def close(self):
        if self.stopping.is_set():
            return
        self.stopping.set()
        if self.sock is not None:
            try:
                self.sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        if self.connection is not None:
            self.connection.close()
        if self.thread is not None:
            self.thread.join(timeout=5)
            if self.thread.is_alive():
                raise RuntimeError('peer_cleanup_failed')
