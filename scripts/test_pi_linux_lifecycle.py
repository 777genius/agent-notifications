"""Native Linux kernel identity guards; desktop qualification stays separate."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest

SPEC = importlib.util.spec_from_file_location("pi_linux_desktop", Path(__file__).with_name("pi-installed-desktop-linux.py"))
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


@unittest.skipUnless(sys.platform == "linux", "Linux kernel /proc identity only")
class KernelLifetime(unittest.TestCase):
    def test_original_birth_is_required_and_reparenting_cannot_fake_exit(self):
        child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(20)"])
        try:
            identity = M.N.wait_for(lambda: M.process_identity(child.pid))
            self.assertEqual(identity["pid"], child.pid)
            self.assertGreater(identity["start_ticks"], 0)
            self.assertTrue(M.same_process(identity))
            self.assertTrue(M.same_process({**identity, "ppid": -1}))
            self.assertFalse(M.same_process({**identity, "start_ticks": identity["start_ticks"] + 1}))
            self.assertFalse(M.process_collected(identity))
            child.terminate()
            M.N.wait_for(lambda: M.process_collected(identity), seconds=3)
            self.assertFalse(M.same_process(identity))
        finally:
            if child.poll() is None:
                child.kill()
            child.wait(timeout=3)

    def test_subtree_records_real_child_kernel_identity(self):
        import ctypes
        libc, prior = ctypes.CDLL(None, use_errno=True), ctypes.c_int()
        self.assertEqual(libc.prctl(37, ctypes.byref(prior), 0, 0, 0), 0)
        self.assertEqual(libc.prctl(36, 1, 0, 0, 0), 0)
        child = subprocess.Popen([sys.executable, "-c", "import subprocess,sys,time; subprocess.Popen([sys.executable,'-c','import time;time.sleep(20)']);time.sleep(20)"],
                                 start_new_session=True)
        identities = []
        try:
            identity = M.N.wait_for(lambda: M.process_identity(child.pid))
            identities = M.N.wait_for(lambda: (tree if len(tree := M.process_tree(identity)) == 2 else None))
            self.assertEqual(identities[0], identity)
            self.assertEqual(identities[1]["ppid"], identity["pid"])
            self.assertTrue(all(M.same_process(value) for value in identities))
        finally:
            import os
            import signal
            os.killpg(child.pid, signal.SIGKILL)
            child.wait(timeout=3)
            for value in identities:
                M.N.wait_for(lambda: M.process_collected(value), seconds=3)
            libc.prctl(36, prior.value, 0, 0, 0)


if __name__ == "__main__":
    unittest.main()
