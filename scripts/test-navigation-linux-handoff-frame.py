#!/usr/bin/python3
"""The handoff frame cannot inherit renderer-only or incomplete qualification."""
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('handoff_probe', Path(__file__).with_name('navigation-linux-handoff-guest-probe.py'))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
FRAME = (b'NAVIGATION_TEST_HANDOFF_V1 '
         b'f456fddc65a1f27bbc6ddedb24647e90e8c2dc8baea14e670b60e75d7d1e8a0d '
         b'eyJwYXNzZWQiOmZhbHNlfQ==\r\n')
SOURCES = {'TEST-source.py': '1234'}
RESULT = {'scope': 'offline_selected_client_native_handoff_TEST', 'sourceSHA256': SOURCES,
          'passed': True, 'notificationAttempted': True, 'clickAttempted': True,
          'handoffQualified': True, 'activationQualified': True, 'cleanupPassed': True,
          'serverSurfaceFocusJoinObserved': True, 'tokenForwardingQualified': True,
          'focusQualified': True, 'cgroupEmptyObserved': True,
          'navigationQualified': False, 'humanRenderQualified': False, 'retryAllowed': False,
          'activationEvidenceClass': 'peer_bound_selected_connection_unique_live_toplevel_and_compositor_focus'}


class HandoffFrameTest(unittest.TestCase):
    def test_complete_negative_frame_preserves_payload(self):
        self.assertEqual(probe.decode_guest_frame(b'TEST login: ' + FRAME), b'{"passed":false}')

    def test_renderer_marker_truncation_duplicate_and_corruption_rejected(self):
        for data in (FRAME.replace(b'HANDOFF', b'CLIENT'), FRAME.rstrip(b'\r\n'),
                     FRAME + FRAME, FRAME.replace(b'eyJ', b'eyK')):
            with self.subTest(data=data), self.assertRaises((RuntimeError, ValueError)):
                probe.decode_guest_frame(data)

    def test_qualification_requires_chain_cleanup_source_and_honest_precision(self):
        self.assertTrue(probe.qualified_guest_result(RESULT, SOURCES))
        changes = [('passed', False), ('clickAttempted', None), ('cleanupPassed', False),
                   ('cgroupEmptyObserved', False), ('tokenForwardingQualified', False),
                   ('focusQualified', False), ('sourceSHA256', {'other': '1234'}),
                   ('scope', 'offline_selected_client_renderer_preflight'),
                   ('navigationQualified', True), ('humanRenderQualified', True),
                   ('activationEvidenceClass', 'request_only'), ('retryAllowed', True)]
        for key, value in changes:
            negative = copy.deepcopy(RESULT); negative[key] = value
            with self.subTest(key=key):
                self.assertFalse(probe.qualified_guest_result(negative, SOURCES))


if __name__ == '__main__': unittest.main()
