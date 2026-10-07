#!/usr/bin/python3
"""Pure invocation contract: imports no Gio and never launches a client."""
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).resolve().parents[1] / 'tests/integration/navigation_linux_client_callback_test.py'
spec = importlib.util.spec_from_file_location('client_callback_test_contract', path)
callback = importlib.util.module_from_spec(spec)
spec.loader.exec_module(callback)


class InvocationContract(unittest.TestCase):
    def test_vendor_entry_receives_one_canonical_synthetic_uri(self):
        self.assertEqual(callback.invocation('0123456789abcdef0123456789abcdef', 'opaque-token'), [
            '/usr/lib/chatgpt/codex-launcher', '--ozone-platform=wayland',
            'codex://threads/01234567-89ab-cdef-0123-456789abcdef'])

    def test_untrusted_target_cannot_select_another_command_or_scheme(self):
        for target in ('file:///tmp/test', '../test', 'x; echo test', '01234567-89ab-cdef-0123-456789abcdef', None):
            with self.subTest(target=target), self.assertRaises(ValueError):
                callback.invocation(target, 'opaque-token')

    def test_private_bus_cannot_fall_back_to_an_unverified_transport(self):
        path = Path('/var/lib/navigation-client-handoff-TEST/session/runtime/bus.sock')
        address = 'unix:path=' + str(path) + ',guid=' + 'a' * 32
        callback.private_bus_address(address, path)
        for value in (address + ';unix:path=/tmp/other-bus', address + ',nonce=extra',
                      address[:-1], address.replace('bus.sock', 'other.sock')):
            with self.subTest(value=value), self.assertRaises(ValueError):
                callback.private_bus_address(value, path)
        with self.assertRaises(ValueError):
            callback.private_bus_address(address, Path('/tmp/other-bus'))

    def test_opaque_token_must_fit_the_environment_boundary(self):
        for token in (None, '', 'a\0b', 'é' * 2049):
            with self.subTest(token=repr(token)[:40]), self.assertRaises(ValueError):
                callback.invocation('0123456789abcdef0123456789abcdef', token)
        self.assertEqual(len(callback.invocation('0123456789abcdef0123456789abcdef', 'é' * 2048)), 3)


if __name__ == '__main__':
    unittest.main()
