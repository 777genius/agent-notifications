"""Real TEST HTTP exchanges must not resolve a name for numeric loopback."""
from contextlib import ExitStack
import importlib.util
import json
from pathlib import Path
import socket
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1] / "tests/integration"


def load(filename):
    spec = importlib.util.spec_from_file_location(filename[:-3], ROOT / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


F, P = load("pi_native_fixture.py"), load("pi_installed_fixture.py")


def post(address, path, value):
    body = json.dumps(value).encode("utf-8")
    request = (f"POST {path} HTTP/1.0\r\nHost: 127.0.0.1\r\n"
               f"Content-Type: application/json\r\nContent-Length: {len(body)}\r\n\r\n").encode() + body
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as connection:
        connection.settimeout(2)
        connection.connect(address)
        connection.sendall(request)
        response = bytearray()
        while block := connection.recv(4096):
            response.extend(block)
            if len(response) > 32768:
                raise AssertionError("TEST loopback response exceeded bound")
    return bytes(response)


class LoopbackTests(unittest.TestCase):
    def setUp(self):
        self.lookups = ExitStack()
        self.addCleanup(self.lookups.close)
        for name in ("getfqdn", "gethostbyaddr", "gethostbyname", "gethostbyname_ex", "getaddrinfo"):
            self.lookups.enter_context(mock.patch.object(socket, name, side_effect=AssertionError("TEST unexpected DNS lookup")))

    def check_server(self, server, daemon_threads):
        self.assertEqual(server.server_address[0], "127.0.0.1")
        self.assertEqual(server.server_name, "127.0.0.1")
        self.assertEqual(server.server_port, server.server_address[1])
        self.assertTrue(server.allow_reuse_address)
        self.assertTrue(server.block_on_close)
        self.assertEqual(server.daemon_threads, daemon_threads)

    def test_real_model_response_without_dns_and_provider_collected(self):
        provider = F.Provider("success")
        try:
            self.check_server(provider.server, True)
            response = post(provider.server.server_address, "/v1/chat/completions", {"model": "TEST-local", "stream": True})
            self.assertIn(b"200 OK", response)
            self.assertIn(b"TEST_PI_DONE", response)
            self.assertTrue(response.endswith(b"data: [DONE]\n\n"))
            self.assertEqual(len(provider.requests), 1)
        finally:
            provider.close()
        self.assertFalse(provider.thread.is_alive())

    def test_real_webhook_without_dns_and_request_threads_collected(self):
        sink = P.Webhooks()
        try:
            self.check_server(sink.server, False)
            response = post(sink.server.server_address, "/TEST-pi", {
                "schema_version": "1.0", "status": "task_complete", "notification_type": "task_complete",
                "agent_source": "pi", "message": P.COPY["task_complete"], "title": "Pi",
                "timestamp": "2026-10-28T10:00:00Z", "session_id": "", "source": "claude-notifications"})
            self.assertIn(b"204 No Content", response)
            self.assertEqual(sink.wait("task_complete")["status"], "task_complete")
        finally:
            sink.close()
        self.assertFalse(sink.thread.is_alive())


if __name__ == "__main__":
    unittest.main()
