"""Closed native desktop receipt regression tests; no OS presenter or webhook."""
import importlib.util
import json
from threading import Lock
from pathlib import Path
import tempfile
import unittest

source = Path(__file__).with_name('opencode-v226-source-e2e.py')
spec = importlib.util.spec_from_file_location('opencode_v226_source', source)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
desktop_submitted = module.desktop_submitted
wait_for_delivery = module.wait_for_delivery


class NativeDesktopReceiptTests(unittest.TestCase):
    correlation = '12345678-1111-4222-8333-123456789abc'
    nonce = '54345678-1111-4222-8333-123456789abc'

    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='TEST-desktop-spool-')
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.directory = self.root / 'control' / 'opencode-native-spool' / self.correlation
        self.directory.mkdir(parents=True)
        self.receipt = self.directory / (self.nonce + '.receipt')
        self.valid = {'schemaVersion': 1, 'correlationID': self.correlation,
                      'nonce': self.nonce, 'notificationID': self.correlation,
                      'status': 'submitted', 'reason': 'os_accepted', 'retrySafe': False}

    def write(self, value):
        self.receipt.write_text(json.dumps(value), encoding='utf-8')

    def test_exact_accepted_receipt_counted(self):
        self.write(self.valid)
        self.assertEqual(desktop_submitted(self.root), {self.correlation})

    def test_other_valid_native_outcomes_never_counted(self):
        for status, reason in [('unknown', 'timeout'), ('rejected', 'permission_denied')]:
            with self.subTest(status=status):
                self.write({**self.valid, 'status': status, 'reason': reason})
                self.assertEqual(desktop_submitted(self.root), set())

    def test_partial_or_forged_receipt_cannot_qualify_desktop(self):
        bad = [
            {'nonce': self.correlation}, {'notificationID': self.nonce},
            {'correlationID': self.nonce}, {'reason': 'timeout'},
            {'retrySafe': True}, {'schemaVersion': True},
            {'status': 'unknown', 'reason': 'os_accepted'},
            {'status': 'rejected', 'reason': 'os_accepted'},
            {'extra': 'unknown'}, {'nonce': None}
        ]
        for change in bad:
            with self.subTest(change=change):
                self.write({**self.valid, **change})
                with self.assertRaisesRegex(RuntimeError, '^desktop_receipt_invalid$'):
                    desktop_submitted(self.root)
        partial = dict(self.valid)
        del partial['nonce']
        self.write(partial)
        with self.assertRaisesRegex(RuntimeError, '^desktop_receipt_invalid$'):
            desktop_submitted(self.root)

    def test_duplicate_fields_rejected_instead_of_last_value_winning(self):
        raw = json.dumps(self.valid)
        self.receipt.write_text(raw[:-1] + ',"status":"submitted"}', encoding='utf-8')
        with self.assertRaisesRegex(RuntimeError, '^desktop_receipt_invalid$'):
            desktop_submitted(self.root)

    def test_wrong_filename_and_symlink_rejected(self):
        self.write(self.valid)
        moved = self.directory / (self.correlation + '.receipt')
        self.receipt.rename(moved)
        with self.assertRaisesRegex(RuntimeError, '^desktop_receipt_invalid$'):
            desktop_submitted(self.root)
        moved.rename(self.receipt)
        target = self.root / 'outside.json'
        target.write_text(json.dumps(self.valid), encoding='utf-8')
        self.receipt.unlink()
        self.receipt.symlink_to(target)
        with self.assertRaisesRegex(RuntimeError, '^desktop_receipt_invalid$'):
            desktop_submitted(self.root)

    def test_oversized_and_invalid_json_fail_closed(self):
        for raw in (b'{' + b' ' * 8192 + b'}', b'not-json'):
            with self.subTest(size=len(raw)):
                self.receipt.write_bytes(raw)
                with self.assertRaisesRegex(RuntimeError, '^desktop_receipt_invalid$'):
                    desktop_submitted(self.root)



class FreshWebhookEvidenceTests(unittest.TestCase):
    def test_stale_matching_webhook_does_not_qualify_new_event(self):
        matching = json.dumps({
            'schema_version': 1, 'status': 'submitted',
            'notification_type': 'question', 'agent_source': 'opencode',
            'message': 'OpenCode asked a question', 'timestamp': 'TEST',
            'session_id': '', 'source': 'TEST', 'title': 'OpenCode'
        }).encode('utf-8')

        class FakeWebhook:
            def __init__(self):
                self.lock = Lock()
                self.bodies = [matching]

        webhook = FakeWebhook()
        with self.assertRaisesRegex(RuntimeError, '^expected_delivery_missing$'):
            wait_for_delivery(webhook, 'question', 'OpenCode asked a question', after=1, timeout=.02)
        webhook.bodies.append(matching)
        wait_for_delivery(webhook, 'question', 'OpenCode asked a question', after=1, timeout=.02)


if __name__ == '__main__':
    unittest.main()
