#!/usr/bin/env python3
"""Required Windows fan-in and receipt contracts, using disposable fixtures."""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

from ci_native_evidence import CONTRACTS, validate_jobs

WORKFLOW = Path(__file__).resolve().parents[1] / '.github/workflows/ci-windows.yml'


def job(name):
    match = re.search(r'^  ' + re.escape(name) + r':\n(.*?)(?=^  [\w-]+:|\Z)',
                      WORKFLOW.read_text(), re.M | re.S)
    if match is None:
        raise AssertionError('required job missing: ' + name)
    return match.group(1)


def run_block(text, step):
    match = re.search(r'^      - name: ' + re.escape(step) + r'\n(.*?)(?=^      - |\Z)',
                      text, re.M | re.S)
    if match is None:
        raise AssertionError('required step missing: ' + step)
    return '\n'.join(line[10:] for line in match.group(1).split('        run: |\n', 1)[1].splitlines()
                     if line.strip()) + '\n'


class WindowsShards(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix='TEST-windows-shards-')
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)

    def run_shell(self, block, **env):
        return subprocess.run(['bash', '-c', block], cwd=self.root, env={**os.environ, **env},
                              capture_output=True, timeout=10).returncode

    def test_required_names_reject_missing_failed_cancelled_and_skipped_shards(self):
        text = job('test')
        self.assertIn('name: Test on Windows (${{ matrix.go }})', text)
        self.assertIn('needs: [scope, go-tests, installer-native]', text)
        self.assertIn('always()', text)
        self.assertNotIn('!cancelled()', text)
        block = run_block(text, 'Require every Windows shard to succeed')
        env = {'SCOPE_RESULT': 'success', 'GO_TESTS_RESULT': 'success', 'INSTALLER_NATIVE_RESULT': 'success'}
        self.assertEqual(self.run_shell(block, **env), 0)
        for key in env:
            for outcome in ['', 'failure', 'cancelled', 'skipped']:
                self.assertNotEqual(self.run_shell(block, **{**env, key: outcome}), 0, (key, outcome))

    def test_coverage_is_required_before_aggregate_can_pass(self):
        block = run_block(job('test'), 'Require atomic Go coverage')
        path = self.root / 'coverage.txt'
        self.assertNotEqual(self.run_shell(block), 0)
        for text in ['', 'mode: set\n', 'mode: atomic\n']:
            path.write_text(text)
            self.assertNotEqual(self.run_shell(block), 0)
        path.write_text('mode: atomic\nfixture.go:1.1,2.1 1 1\n')
        self.assertEqual(self.run_shell(block), 0)

    def test_shards_are_independent_native_jobs_for_both_versions(self):
        for name in ['go-tests', 'installer-native']:
            text = job(name)
            self.assertIn('needs: scope', text)
            self.assertIn('runs-on: windows-latest', text)
            self.assertIn("go: ['1.25', '1.26']", text)
            self.assertIn('fail-fast: false', text)
            self.assertIn('timeout-minutes: 45', text)
            self.assertNotIn('continue-on-error:', text)
        # All observation packages remain sequential in one job and preserve
        # first-failure recording, race coverage, repetition and trace retention.
        go = job('go-tests')
        self.assertIn('Qualify observation caches without cross-package contention', go)
        self.assertIn('go test -p 1 -v -race', go)
        self.assertIn("-count=20 -run '^TestChannelConsentIndependentAndNoRetry$'", go)
        self.assertIn('windows-observation-failure-go-${{ matrix.go }}', go)
        self.assertIn('-timeout=15m', go)
        native = job('installer-native')
        self.assertIn('windows-config-test-pr-${{ github.event.pull_request.number }}-go-${{ matrix.go }}', native)
        self.assertIn('timeout-minutes: 10', native)

    def test_receipt_requires_all_new_native_shards_and_latest_retry_success(self):
        keys, evidence, names = CONTRACTS['ci-windows.yml']
        self.assertEqual(keys, ('go-tests', 'installer-native', 'test'))
        expected = {name + ' (' + version + ')' for version in ['1.25', '1.26']
                    for name in ['Windows Go tests', 'Windows installers and native E2E', 'Test on Windows']}
        self.assertEqual(set(names), expected)
        text = job('native-input-evidence')
        self.assertIn('needs: [scope, go-tests, installer-native, test]', text)
        self.assertIn('--jobs go-tests installer-native test', text)
        jobs = [{'id': index, 'name': name, 'status': 'completed', 'conclusion': 'success'}
                for index, name in enumerate([*names, evidence])]
        validate_jobs(jobs, 'ci-windows.yml')
        for index in range(len(jobs)):
            with self.assertRaises(ValueError):
                validate_jobs(jobs[:index] + jobs[index + 1:], 'ci-windows.yml')
            failed = {**jobs[index], 'id': 100, 'conclusion': 'failure'}
            with self.assertRaises(ValueError):
                validate_jobs([*jobs, failed], 'ci-windows.yml')
            validate_jobs([*jobs, failed, {**failed, 'id': 101, 'conclusion': 'success'}], 'ci-windows.yml')


if __name__ == '__main__':
    unittest.main()
