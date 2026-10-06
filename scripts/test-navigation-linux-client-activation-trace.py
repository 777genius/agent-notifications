#!/usr/bin/python3
"""A real client request must survive libwayland's object-ID format change."""
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).resolve().parent.parent / 'tests/integration/navigation_linux_client_handoff_guest_test.py'
spec = importlib.util.spec_from_file_location('client_handoff', path)
guest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guest)


class ClientActivationTraceTest(unittest.TestCase):
    def test_both_native_object_id_formats_and_exact_token(self):
        for trace in (
            '[827920.0] -> xdg_activation_v1@22.activate("TEST.click+token", wl_surface@36)\n',
            '[05:23:07.432885] -> xdg_activation_v1#22.activate("TEST.click+token", wl_surface#36)\n',
        ):
            with self.subTest(trace=trace):
                self.assertEqual(guest.client_activation_surfaces(trace, 'TEST.click+token'), ['36'])
                self.assertEqual(guest.client_activation_surfaces(trace, 'TESTxclick+token'), [])

    def test_unrelated_or_malformed_records_do_not_qualify(self):
        traces = (
            'xdg_activation_v1#22.activate("other-token", wl_surface#36)',
            'xdg_activation_v1#22.activate("TEST-token", wl_surface@36)',
            'xdg_activation_v1@22.activate("TEST-token", wl_surface#36)',
            'xdg_activation_v1#22.activate("TEST-token", wl_surface#36',
            'xdg_activation_token_v1#42.done("TEST-token")',
        )
        for trace in traces:
            with self.subTest(trace=trace):
                self.assertEqual(guest.client_activation_surfaces(trace, 'TEST-token'), [])

    def test_duplicate_requests_are_retained_for_single_request_admission(self):
        trace = 'xdg_activation_v1#22.activate("TEST-token", wl_surface#36)\n'
        self.assertEqual(guest.client_activation_surfaces(trace + trace, 'TEST-token'), ['36', '36'])


if __name__ == '__main__': unittest.main()
