import json
import unittest
from unittest.mock import patch

from opencode_v226_darwin_peer_e0 import NativePeerObservation


class Socket:
    def getsockname(self):
        return ('127.0.0.1', 54321)

    def getpeername(self):
        return ('127.0.0.1', 12345)


class Response:
    status = 200

    def __init__(self, events):
        self.lines = iter([b'data: ' + json.dumps(e).encode() + b'\n' for e in events] + [b''])

    def getheader(self, key):
        return 'text/event-stream' if key == 'Content-Type' else None

    def readline(self, limit):
        return next(self.lines)


class Connection:
    def __init__(self, events):
        self.sock = Socket()
        self.response = Response(events)

    def request(self, *_args, **_kwargs):
        pass

    def getresponse(self):
        return self.response


class NativePeerParserTest(unittest.TestCase):
    marker = {'type': 'server.connected', 'data': {}}
    event = {'type': 'session.execution.started', 'id': 'evt_test', 'created': 1,
             'data': {'sessionID': 'ses_test'}}

    def read(self, events):
        peer = NativePeerObservation(None, 12345, '/TEST-unused')
        with patch('opencode_v226_darwin_peer_e0.http.client.HTTPConnection', return_value=Connection(events)):
            peer._read()
        return peer

    def test_fresh_marker_precedes_bounded_event(self):
        peer = self.read([self.marker, self.event])
        self.assertTrue(peer.connected)
        self.assertIsNone(peer.error)
        self.assertEqual(set(peer.events), {('session.execution.started', 'evt_test', 1, 'ses_test')})
        self.assertGreater(peer.events[('session.execution.started', 'evt_test', 1, 'ses_test')], 0)

    def test_missing_or_duplicate_marker_fails_closed(self):
        for events in ([self.event], [self.marker, self.marker]):
            with self.subTest(events=events):
                self.assertEqual(self.read(events).error, 'peer_feed_failed')

    def test_invalid_native_timestamp_fails_closed(self):
        bad = {**self.event, 'created': True}
        self.assertEqual(self.read([self.marker, bad]).error, 'peer_feed_failed')


if __name__ == '__main__':
    unittest.main()
