"""Wire regression: getty prefix, truncation and corruption must not fake success."""
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).resolve().parents[2] / 'scripts/navigation-linux-client-guest-native-probe.py'
spec = importlib.util.spec_from_file_location('native_guest_probe', path)
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
PAYLOAD = b'{"passed":false}'
FRAME = (b'NAVIGATION_TEST_CLIENT_V1 '
         b'f456fddc65a1f27bbc6ddedb24647e90e8c2dc8baea14e670b60e75d7d1e8a0d '
         b'eyJwYXNzZWQiOmZhbHNlfQ==\r\n')


class GuestFrameTest(unittest.TestCase):
    def test_getty_prefix_preserves_negative_payload(self):
        self.assertEqual(probe.decode_guest_frame(b'Ubuntu ttyS0\r\nTEST login: ' + FRAME), PAYLOAD)

    def test_invalid_frames_rejected(self):
        for data in (FRAME + FRAME, FRAME.rstrip(b'\r\n'), FRAME.replace(b'eyJ', b'eyK'), b'no frame'):
            with self.subTest(data=data), self.assertRaises((RuntimeError, ValueError)):
                probe.decode_guest_frame(data)

    def test_budget_enforced(self):
        with self.assertRaises(RuntimeError):
            probe.decode_guest_frame(b'x' * (16 * 1024 * 1024) + FRAME)


if __name__ == '__main__':
    unittest.main()
