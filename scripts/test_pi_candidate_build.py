"""A development candidate must never acquire final build provenance."""
import importlib.util
from pathlib import Path
import unittest

PATH = Path(__file__).with_name("pi-candidate-build.py")
SPEC = importlib.util.spec_from_file_location("pi_candidate", PATH)
B = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(B)
SOURCE = "a" * 40


class ProvenanceTests(unittest.TestCase):
    def test_module_graph_stream_and_local_replacement(self):
        graph = B.modules(' {"Path":"main","Main":true}\n {"Path":"sdk","Version":"v1.2.3"}\n')
        B.pinned_graph(graph)
        graph[1]["Replace"] = {"Dir": "/TEST/local/sdk"}
        with self.assertRaisesRegex(RuntimeError, "replacement"):
            B.pinned_graph(graph)

    def test_linked_vcs_and_module_sums_must_be_present(self):
        info = "TEST-main\n\tdep\tsdk\tv1.2.3\th1:TEST\n\tbuild\tvcs.revision=" + SOURCE + "\n\tbuild\tvcs.modified=false\n"
        B.linked_source(info, SOURCE)
        for invalid in (info.replace("modified=false", "modified=true"), info.replace(SOURCE, "b" * 40),
                        info.replace("h1:TEST", ""), info + "\t=>\t/TEST/local/sdk\n"):
            with self.assertRaisesRegex(RuntimeError, "identity_invalid"):
                B.linked_source(invalid, SOURCE)


if __name__ == "__main__":
    unittest.main()
