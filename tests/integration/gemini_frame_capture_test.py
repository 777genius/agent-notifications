"""Pure TEST custody checks, never a native hook/agent/runtime launch."""
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
from unittest import mock
import unittest

import gemini_frame_capture as cap


@unittest.skipUnless(sys.platform == "linux", "Linux diagnostic custody")
class CaptureChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="TEST-frame-capture-")
        self.addCleanup(self.temp.cleanup)
        self.lab = Path(self.temp.name)
        os.chmod(self.lab, 0o700)
        (self.lab / ".an-gemini-TEST").write_text("synthetic")
        cap.prepare(self.lab)

    def assert_sealed_only(self):
        self.assertEqual(set((self.lab / cap.ROOT).iterdir()),
                         {self.lab / cap.ROOT / ".lock", self.lab / cap.ROOT / ".sealed"})
        self.assertEqual(cap.capture(self.lab, "AfterAgent", b"late"), "sealed")

    # Red if frames are substituted, raw data is returned publicly, cleanup
    # skips removal, or cardinality cannot independently refuse the 65th frame.
    def test_bounded_publication_classification_and_cleanup(self):
        raw = b"TEST-private-frame"
        for _ in range(cap.SLOTS):
            self.assertEqual(cap.capture(self.lab, "AfterAgent", raw), "recorded")
        self.assertEqual(cap.capture(self.lab, "AfterAgent", raw), "full")
        seen = []
        def classify(event, data):
            seen.append((event, data))
            return {"bytes": len(data)}
        result = cap.finish(self.lab, classify, True)
        self.assertEqual(result["class"], "classified")
        self.assertEqual(len(seen), cap.SLOTS)
        self.assertTrue(all(event == "AfterAgent" and data == raw for event, data in seen))
        self.assertNotIn("TEST-private", json.dumps(result))
        self.assert_sealed_only()

    # Sealing must stop a future late writer even when collection is unknown.
    def test_unknown_collection_seals_and_retains_then_recovers(self):
        self.assertEqual(cap.capture(self.lab, "Notification", b"synthetic"), "recorded")
        result = cap.finish(self.lab, lambda *_: self.fail("unknown collection decoded"), False)
        self.assertEqual(result["class"], "sealed_collection_unverified")
        self.assertTrue((self.lab / cap.ROOT).exists())
        self.assertEqual(cap.capture(self.lab, "Notification", b"late"), "sealed")
        result = cap.finish(self.lab, lambda *_: {"classification": "decoded"}, True)
        self.assertEqual(result["cleanup"], "raw_frames_removed_sealed_custody_retained")
        self.assert_sealed_only()

    # Partial input must be deleted only after drain, never called a complete frame.
    def test_partial_refusal(self):
        slot = self.lab / cap.ROOT / ".slot-00"
        slot.mkdir(mode=0o700)
        (slot / ".pending").write_bytes(b"TEST-private-partial")
        os.chmod(slot / ".pending", 0o600)
        result = cap.finish(self.lab, lambda *_: self.fail("partial frame decoded"), True)
        self.assertEqual(result["class"], "partial_capture")
        self.assertEqual(result["incomplete"], 1)
        self.assert_sealed_only()

    def test_symlink_refusal(self):
        target = self.lab / "TEST-preserved"
        target.write_bytes(b"keep")
        (self.lab / cap.ROOT / ".lock").symlink_to(target)
        self.assertEqual(cap.capture(self.lab, "AfterAgent", b"raw"), "unavailable")
        self.assertEqual(target.read_bytes(), b"keep")

    # Real late opener during deletion must remain on the same closed custody.
    # Old cleanup either recreated a lock and published raw data, or refused
    # via a removed root instead of preserving a monotonic sealed boundary.
    def test_late_opener_during_raw_cleanup(self):
        self.assertEqual(cap.capture(self.lab, "AfterAgent", b"synthetic"), "recorded")
        lock = self.lab / cap.ROOT / ".lock"
        before = cap.identity(lock.stat())
        opened = threading.Event()
        outcomes = []
        original_open, original_unlink = os.open, os.unlink
        writer = None
        def late_writer():
            outcomes.append(cap.capture(self.lab, "AfterAgent", b"late-private"))
        def tracked_open(path, *args, **kwargs):
            fd = original_open(path, *args, **kwargs)
            if threading.current_thread() is writer and path == ".lock":
                opened.set()
            return fd
        def deletion_window(path, *args, **kwargs):
            nonlocal writer
            result = original_unlink(path, *args, **kwargs)
            if path == "AfterAgent.frame":
                writer = threading.Thread(target=late_writer)
                writer.start()
                self.assertTrue(opened.wait(0.5), "late opener did not reach lock")
            return result
        try:
            with mock.patch.object(cap.os, "open", side_effect=tracked_open), \
                 mock.patch.object(cap.os, "unlink", side_effect=deletion_window):
                result = cap.finish(self.lab, lambda *_: {}, True)
                self.assertIsNotNone(writer)
                writer.join(2)
                self.assertFalse(writer.is_alive())
            self.assertEqual(outcomes, ["sealed"])
            self.assertEqual(result["cleanup"], "raw_frames_removed_sealed_custody_retained")
            self.assertEqual(cap.identity(lock.stat()), before)
            self.assert_sealed_only()
        finally:
            if writer is not None:
                writer.join(2)


if __name__ == "__main__":
    unittest.main()
