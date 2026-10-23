#!/usr/bin/env python3
"""Exercise package partition and required-check fan-in using disposable fixtures."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

from ci_ubuntu_coverage import SHARDS, merge

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'scripts/ci-ubuntu-go-shard.sh'
WORKFLOW = ROOT / '.github/workflows/ci-ubuntu.yml'
MODULE = 'github.com/777genius/agent-notifications'
PACKAGES = [MODULE, MODULE + '/cmd/claude-notifications', MODULE + '/cmd/future-command',
            MODULE + '/internal/cursorevent', MODULE + '/internal/future', MODULE + '/pkg/notify']


def job(name):
    text = WORKFLOW.read_text()
    match = re.search(r'^  ' + re.escape(name) + r':\n(.*?)(?=^  [\w-]+:|\Z)', text, re.M | re.S)
    if match is None:
        raise AssertionError('required job missing: ' + name)
    return match.group(1)


def run_block(job_text, step_name):
    match = re.search(r'^      - name: ' + re.escape(step_name) + r'\n(.*?)(?=^      - |\Z)',
                      job_text, re.M | re.S)
    if match is None:
        raise AssertionError('required step missing: ' + step_name)
    lines = match.group(1).split('        run: |\n', 1)[1].splitlines()
    return '\n'.join(line[10:] for line in lines if line.strip()) + '\n'


class UbuntuShards(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix='TEST-ubuntu-shards-')
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        stub = self.bin / 'go'
        stub.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
if sys.argv[1:] == ['list', './...']:
    print(os.environ['TEST_PACKAGES'])
    sys.exit(int(os.environ.get('TEST_LIST_EXIT', '0')))
Path(os.environ['TEST_GO_ARGS']).write_text(json.dumps(sys.argv[1:]))
sys.exit(int(os.environ.get('TEST_TEST_EXIT', '0')))
''')
        stub.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(self.bin) + os.pathsep + os.environ['PATH'],
                    'TEST_PACKAGES': '\n'.join(PACKAGES), 'TEST_GO_ARGS': str(self.root / 'args.json')}

    def shard(self, shard, **env):
        return subprocess.run(['bash', str(SCRIPT), shard], cwd=self.root, env={**self.env, **env},
                              capture_output=True, text=True, timeout=10)

    def test_complete_disjoint_partition_and_unchanged_test_strength(self):
        actual = {}
        for shard in SHARDS:
            result = self.shard(shard)
            self.assertEqual(result.returncode, 0, result.stderr)
            args = json.loads((self.root / 'args.json').read_text())
            self.assertEqual(args[:7], ['test', '-timeout', '20m', '-v', '-race',
                                       '-coverprofile=coverage-' + shard + '.txt', '-covermode=atomic'])
            actual[shard] = args[7:]
            if shard.startswith('cmd-'):
                self.assertEqual(args[7:9], ['-run' if shard == 'cmd-setup' else '-skip', '^Test(SetupWizard|SetupCodex)'])
                actual[shard] = args[9:]
        self.assertEqual(actual['cmd-setup'], [PACKAGES[1], PACKAGES[2]])
        self.assertEqual(actual['cmd-other'], actual['cmd-setup'])
        self.assertEqual(actual['cursorevent'], [PACKAGES[3]])
        self.assertEqual(actual['packages'], [PACKAGES[0], PACKAGES[4], PACKAGES[5]])
        flattened = sum([v for k, v in actual.items() if k != 'cmd-setup'], [])
        self.assertCountEqual(flattened, PACKAGES)
        self.assertEqual(len(flattened), len(set(flattened)))

    def test_invalid_empty_or_failed_list_never_runs_tests_and_test_failure_propagates(self):
        for shard, env in [('unknown', {}), ('cmd-other', {'TEST_PACKAGES': MODULE}),
                           ('packages', {'TEST_PACKAGES': ''}), ('cmd-other', {'TEST_LIST_EXIT': '7'})]:
            with self.subTest(shard=shard, env=env):
                self.assertNotEqual(self.shard(shard, **env).returncode, 0)
                self.assertFalse((self.root / 'args.json').exists())
        self.assertEqual(self.shard('cursorevent', TEST_TEST_EXIT='9').returncode, 9)

    def test_required_aggregate_rejects_each_missing_failed_or_cancelled_shard(self):
        aggregate = job('test')
        self.assertIn('needs: [scope, go-tests, installer-native, cursor-physical]', aggregate)
        self.assertIn('always()', aggregate)
        self.assertNotIn('!cancelled()', aggregate)
        self.assertIn('name: Test on Ubuntu (${{ matrix.go }})', aggregate)
        block = run_block(aggregate, 'Require every test shard to succeed')
        env = {'SCOPE_RESULT': 'success', 'GO_TESTS_RESULT': 'success',
               'INSTALLER_NATIVE_RESULT': 'success', 'CURSOR_PHYSICAL_RESULT': 'success'}
        def run(values):
            return subprocess.run(['bash', '-c', block], cwd=self.root,
                                  env={**os.environ, **values}, capture_output=True, timeout=10).returncode
        self.assertEqual(run(env), 0)
        for key in env:
            for outcome in ['', 'failure', 'cancelled', 'skipped']:
                self.assertNotEqual(run({**env, key: outcome}), 0, (key, outcome))

    def test_coverage_fan_in_requires_every_atomic_profile(self):
        block = run_block(job('test'), 'Require and combine complete atomic coverage').replace('tools/ci_ubuntu_coverage.py', str(ROOT / 'tools/ci_ubuntu_coverage.py'))
        def run():
            return subprocess.run(['bash', '-c', block], cwd=self.root,
                                  capture_output=True, timeout=10).returncode
        for shard in SHARDS:
            (self.root / ('coverage-' + shard + '.txt')).write_text('mode: atomic\n' + shard + ':1.1,2.1 1 1\n')
        self.assertEqual(run(), 0)
        coverage = (self.root / 'coverage.txt').read_text()
        self.assertEqual(coverage.count('mode: atomic'), 1)
        for shard in SHARDS:
            path = self.root / ('coverage-' + shard + '.txt')
            original = path.read_text()
            for content in ['', 'mode: atomic\n', original.replace('atomic', 'set')]:
                path.write_text(content)
                self.assertNotEqual(run(), 0)
            path.unlink()
            self.assertNotEqual(run(), 0)
            path.write_text(original)

    def test_overlapping_cli_coverage_sums_counts_and_rejects_inconsistent_blocks(self):
        for shard in SHARDS:
            (self.root / ('coverage-' + shard + '.txt')).write_text('mode: atomic\nfixture.go:1.1,2.1 2 3\n')
        merge(self.root)
        self.assertEqual((self.root / 'coverage.txt').read_text(), 'mode: atomic\nfixture.go:1.1,2.1 2 12\n')
        path = self.root / 'coverage-cmd-other.txt'
        for line in ['fixture.go:1.1,2.1 3 1', 'fixture.go:1.1,2.1 2 -1', 'malformed']:
            path.write_text('mode: atomic\n' + line + '\n')
            with self.assertRaises(ValueError):
                merge(self.root)

    def test_full_shards_are_parallel_and_both_toolchains_remain(self):
        for name in ['go-tests', 'installer-native']:
            text = job(name)
            self.assertIn('needs: scope', text)
            self.assertIn("go: ['1.25', '1.26']", text)
            self.assertIn('fail-fast: false', text)
            self.assertNotIn('continue-on-error: true', text.split('      - name: Run install.sh E2E diagnostics')[0])
        self.assertIn('shard: [cmd-setup, cmd-other, packages, cursorevent]', job('go-tests'))
        self.assertNotIn("matrix.go == '1.26' && 'ubuntu-24.04'", job('installer-native'))
        self.assertIn('vars.CI_LINUX_RUNNER', job('go-tests'))

    def test_real_go_partition_keeps_future_tests_examples_fuzz_seeds_and_subtests(self):
        (self.root / 'go.mod').write_text('module ' + MODULE + '\n\ngo 1.25\n')
        command = self.root / 'cmd' / 'claude-notifications'
        command.mkdir(parents=True)
        (command / 'fixture.go').write_text('package main\nfunc value() int { return 1 }\n')
        (command / 'fixture_test.go').write_text('''package main
import("testing";"fmt")
func TestSetupWizardFixture(t *testing.T) { t.Run("nested",func(t *testing.T){if value()!=1 {t.Fatal("value")}}) }
func TestSetupCodexFixture(t *testing.T) { if value()!=1 {t.Fatal("value")} }
func TestFutureFeature(t *testing.T) { t.Run("TestSetupWizardNested",func(t *testing.T){if value()!=1 {t.Fatal("value")}}) }
func Example() { fmt.Println(value()); // Output: 1
}
func FuzzValue(f *testing.F) { f.Add(1); f.Fuzz(func(t *testing.T,n int){ value() }) }
''')
        environment = {**os.environ, 'GOWORK': 'off', 'GOENV': 'off', 'GOTOOLCHAIN': 'local'}
        executions = []
        for shard in ['cmd-setup', 'cmd-other']:
            result = subprocess.run(['bash', str(SCRIPT), shard], cwd=self.root,
                                    env=environment, capture_output=True, text=True, timeout=120)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            executions.append(set(re.findall(r'^=== RUN   ([^\n]+)$', result.stdout, re.M)))
        self.assertFalse(executions[0] & executions[1])
        self.assertEqual(executions[0] | executions[1], {
            'TestSetupWizardFixture', 'TestSetupWizardFixture/nested', 'TestSetupCodexFixture',
            'TestFutureFeature', 'TestFutureFeature/TestSetupWizardNested',
            'Example', 'FuzzValue', 'FuzzValue/seed#0'})


if __name__ == '__main__':
    unittest.main()
