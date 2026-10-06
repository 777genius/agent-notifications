"""Regression: Unicode JSON expansion cannot exceed the optional wire budget."""
import importlib.util
import json
from pathlib import Path
import unittest

path = Path(__file__).with_name('navigation_linux_guest_client_test.py')
spec = importlib.util.spec_from_file_location('guest_native', path)
guest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guest)


class DiagnosticBudgetTest(unittest.TestCase):
    def test_unicode_expansion_and_omitted_count(self):
        rows = [{'pid': pid, 'executable': '\u2603' * 512, 'appArmor': '\u2603' * 256}
                for pid in range(16)]
        self.assertGreater(len(json.dumps(rows).encode()), 16384)
        kept, omitted = guest.bounded_diagnostics(rows)
        self.assertLessEqual(len(json.dumps(kept, sort_keys=True).encode()), 16384)
        self.assertEqual(len(kept) + omitted, len(rows))
        self.assertEqual(kept, rows[:len(kept)])

    def test_oversized_entry_does_not_hide_later_observation(self):
        small = {'pid': 2, 'observationError': 'owned_process_exited'}
        kept, omitted = guest.bounded_diagnostics([{'executable': 'x' * 17000}, small])
        self.assertEqual(kept, [small])
        self.assertEqual(omitted, 1)


if __name__ == '__main__':
    unittest.main()
