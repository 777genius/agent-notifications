"""Real Linux socket ownership and negative lifecycle tests (TEST-only)."""
import os
from pathlib import Path
import socket
import subprocess
import sys
import unittest

from opencode_v226_linux_peer_e0 import identity


HELPER = Path(__file__).with_name('opencode_v226_linux_peer_e0.py')


@unittest.skipUnless(sys.platform.startswith('linux') and Path('/proc/self/net/tcp').exists(), 'Linux procfs')
class LinuxPeerTests(unittest.TestCase):
    def test_owned_socket_pair_and_negatives(self):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
            listener.bind(('127.0.0.1', 0))
            listener.listen(1)
            port = listener.getsockname()[1]
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client:
                client.connect(('127.0.0.1', port))
                server, peer = listener.accept()
                with server:
                    self.assertEqual(peer[0], '127.0.0.1')
                    local = client.getsockname()[1]
                    start, ns = identity(os.getpid())

                    def call(*args):
                        return subprocess.run([sys.executable, str(HELPER), *map(str, args)],
                                              capture_output=True, timeout=5)

                    self.assertEqual(call('--identity', os.getpid()).stdout.strip(),
                                     f'{start} {ns}'.encode())
                    args = [os.getpid(), port, local, start, ns]
                    result = call(*args)
                    self.assertEqual((result.returncode, result.stdout.strip()),
                                     (0, b'TEST_SOCKET_PAIR_MATCH'), result.stderr)
                    self.assertEqual(call(os.getppid(), *args[1:]).returncode, 1)
                    self.assertEqual(call(os.getpid(), port, 1 if local != 1 else 2, start, ns).returncode, 1)
                    self.assertEqual(call(os.getpid(), port, local, start + 1, ns).returncode, 1)
                    self.assertEqual(call(os.getpid(), port, local, start, ns + 1).returncode, 1)
                self.assertEqual(call(*args).returncode, 1)  # accepted fd reaped

    def test_invalid_args(self):
        result = subprocess.run([sys.executable, str(HELPER), '--identity', 'not-pid'],
                                capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 2)
        result = subprocess.run([sys.executable, str(HELPER), '0', '1', '2', '3', '4'],
                                capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 2)


if __name__ == '__main__':
    unittest.main()
