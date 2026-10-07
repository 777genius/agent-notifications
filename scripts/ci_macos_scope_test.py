#!/usr/bin/env python3
"""Unsafe scope, endpoint types and missing final evidence retain native CI."""
import copy
import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(os.environ.get('TEST_SCOPE_SCRIPT', Path(__file__).with_name('ci_macos_scope.py'))).resolve()
spec = importlib.util.spec_from_file_location('scope_policy', SCRIPT)
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)
classify, final_gate = policy.classify, policy.final_gate
WINDOWS = [
    '.github/workflows/navigation-windows-appsdk-build.yml',
    'tests/integration/windows_appsdk_build/README.md',
    'tests/integration/windows_appsdk_build/SDKContract.cpp',
    'tests/integration/windows_appsdk_build/SDKContract.vcxproj',
    'tests/integration/windows_appsdk_build/packages.lock.json',
]
EVENT = {'action': 'synchronize', 'number': 402, 'repository': {'full_name': '777genius/agent-notifications'},
         'pull_request': {'number': 402, 'draft': True, 'labels': [],
                          'base': {'sha': 'a' * 40, 'repo': {'full_name': '777genius/agent-notifications'}},
                          'head': {'sha': 'b' * 40, 'repo': {'full_name': 'someone/fork'}}}}


def raw(status, *paths, old_mode='100644', new_mode='100644'):
    old_oid, new_oid = 'a' * 40, 'b' * 40
    if status == 'A':
        old_mode, old_oid = '000000', '0' * 40
    if status == 'D':
        new_mode, new_oid = '000000', '0' * 40
    return ('\0'.join([f':{old_mode} {new_mode} {old_oid} {new_oid} {status}', *paths]) + '\0').encode()


class ScopeTests(unittest.TestCase):
    def test_modified_prose_preserves_existing_scope(self):
        for draft in [True, False]:
            event = copy.deepcopy(EVENT)
            event['pull_request']['draft'] = draft
            for path in ['README.md', 'docs/DO_NOT_DISTURB.md', 'docs/NOTIFICATION_TYPES.md']:
                self.assertEqual(classify('pull_request', event, raw('M', path)), 'docs')

    def test_exact_windows_paths_and_both_rename_endpoints(self):
        for path in WINDOWS:
            for status in ['A', 'M', 'D']:
                self.assertEqual(classify('pull_request', EVENT, raw(status, path)), 'windows-only')
        self.assertEqual(classify('pull_request', EVENT, raw('R100', WINDOWS[2], WINDOWS[3])), 'windows-only')
        for old, new in [('other.cpp', WINDOWS[2]), (WINDOWS[2], 'other.vcxproj')]:
            self.assertEqual(classify('pull_request', EVENT, raw('R100', old, new)), 'full')
        self.assertEqual(classify('pull_request', EVENT, raw('M', 'README.md') + raw('M', WINDOWS[1])), 'full')

    def test_unknown_paths_statuses_and_endpoint_types(self):
        for path in ['go.mod', 'go.sum', 'internal/main.go', '.github/workflows/ci-macos.yml',
                     'bin/install.sh', 'scripts/ci_macos_scope.py', 'docs/CI_RUNNERS.md',
                     'docs/qualification/evidence.json', 'docs/handoffs/readme.md',
                     'docs/ARCHITECTURE.md', 'docs/RELEASE.md', 'unknown.md', 'README.md\nother',
                     'tests/integration/windows_appsdk_build/other.cpp', 'somewhere/SDKContract.vcxproj']:
            with self.subTest(path=path):
                self.assertEqual(classify('pull_request', EVENT, raw('M', path)), 'full')
        for status in ['T', 'U', 'C100', 'R101', 'unknown']:
            self.assertEqual(classify('pull_request', EVENT, raw(status, WINDOWS[2])), 'full')
        for mode in ['120000', '160000', '040000']:
            for status in ['M', 'A', 'D']:
                self.assertEqual(classify('pull_request', EVENT, raw(status, WINDOWS[2], old_mode=mode, new_mode=mode)), 'full')
        for status in ['A', 'D', 'R100']:
            paths = ['README.md', 'docs/DO_NOT_DISTURB.md'] if status == 'R100' else ['README.md']
            self.assertEqual(classify('pull_request', EVENT, raw(status, *paths)), 'full')

    def test_malformed_diff_never_skips(self):
        valid = raw('M', WINDOWS[2])
        for diff in [None, '', [], b'', valid[:-1], valid + b'M\0', valid.replace(b'100644', b'120000', 1),
                     valid.replace(b'a' * 40, b'a' * 7, 1), b'M\0' + WINDOWS[2].encode() + b'\0',
                     raw('M', ''), raw('M', '/' + WINDOWS[2]), raw('M', './' + WINDOWS[2]),
                     raw('M', 'tests/../' + WINDOWS[2]), valid + b'\xff\0', raw('R100', WINDOWS[2])]:
            self.assertEqual(classify('pull_request', EVENT, diff), 'full')

    def test_full_events_and_malformed_identity(self):
        for event_name in ['push', 'workflow_dispatch', 'release', 'merge_group', 'unknown']:
            self.assertEqual(classify(event_name, EVENT, raw('M', WINDOWS[2])), 'full')
        for action in ['ready_for_review', 'unknown', None, {}, []]:
            event = copy.deepcopy(EVENT)
            event['action'] = action
            self.assertEqual(classify('pull_request', event, raw('M', WINDOWS[2])), 'full')
        for change in [{'draft': False}, {'draft': 'true'}, {'draft': 1}, {'draft': None},
                       {'labels': [{'name': 'ci:full'}]}, {'labels': None}, {'labels': [None]},
                       {'labels': [{'name': 123}]}, {'head': {}}, {'base': {'sha': '--bad'}},
                       {'number': True}, {'number': 401}]:
            event = copy.deepcopy(EVENT)
            event['pull_request'].update(change)
            self.assertEqual(classify('pull_request', event, raw('M', WINDOWS[2])), 'full')
        for side in ['base', 'head']:
            for change in [{'sha': 'a' * 39}, {'sha': '--bad'}, {'repo': None},
                           {'repo': {'full_name': '../repo'}}, {'repo': {'full_name': 1}}]:
                event = copy.deepcopy(EVENT)
                event['pull_request'][side].update(change)
                self.assertEqual(classify('pull_request', event, raw('M', WINDOWS[2])), 'full')
        for change in [{'number': 0}, {'number': True}, {'repository': None},
                       {'repository': {'full_name': 'different/repo'}}]:
            event = copy.deepcopy(EVENT)
            event.update(change)
            self.assertEqual(classify('pull_request', event, raw('M', WINDOWS[2])), 'full')

    def test_label_requests_full_and_removal_reclassifies_same_head(self):
        event = copy.deepcopy(EVENT)
        event['action'] = 'labeled'
        event['pull_request']['labels'] = [{'name': 'ci:full'}]
        self.assertEqual(classify('pull_request', event, raw('M', WINDOWS[2])), 'full')
        event['action'] = 'unlabeled'
        event['pull_request']['labels'] = []
        self.assertEqual(classify('pull_request', event, raw('M', WINDOWS[2])), 'windows-only')

    def test_gate_requires_known_successful_native_contract(self):
        for jobs in [['test', 'swift-test'], ['recovery']]:
            needs = {name: {'result': 'success'} for name in ['scope', *jobs]}
            self.assertTrue(final_gate('full', needs, jobs)[0])
            for mode in ['docs', 'windows-only', '', 'unknown']:
                self.assertFalse(final_gate(mode, needs, jobs)[0])
            for name in needs:
                for result in ['failure', 'skipped', 'cancelled', 'unknown']:
                    changed = copy.deepcopy(needs)
                    changed[name]['result'] = result
                    self.assertFalse(final_gate('full', changed, jobs)[0])
                changed = copy.deepcopy(needs)
                del changed[name]
                self.assertFalse(final_gate('full', changed, jobs)[0])
            self.assertFalse(final_gate('full', {**needs, 'new-native-job': {'result': 'success'}}, jobs)[0])
        for jobs in [None, [[]], [], ['test'], ['test', 'swift-test', 'unknown'], ['recovery', 'recovery']]:
            self.assertFalse(final_gate('full', {'scope': {'result': 'success'}}, jobs)[0])
        for broken in [{}, [], {'scope': None}]:
            self.assertFalse(final_gate('full', broken, ['recovery'])[0])


class GitScopeTests(unittest.TestCase):
    def setUp(self):
        self.sandbox = tempfile.TemporaryDirectory(prefix='TEST-ci-mac-scope-')
        self.root = Path(self.sandbox.name)
        self.env = {**os.environ, 'GIT_AUTHOR_NAME': 'iliya', 'GIT_AUTHOR_EMAIL': 'iliyazelenkog@gmail.com',
                    'GIT_COMMITTER_NAME': 'iliya', 'GIT_COMMITTER_EMAIL': 'iliyazelenkog@gmail.com'}
        self.git('init', '-q')
        self.git('config', 'user.name', 'iliya')
        self.git('config', 'user.email', 'iliyazelenkog@gmail.com')
        for path in ['README.md', *WINDOWS[1:3], 'source.go', 'unknown.cpp']:
            self.write(path, 'old fixture\n')
        self.base = self.commit()
        self.event = copy.deepcopy(EVENT)
        self.event['pull_request']['base']['sha'] = self.base

    def tearDown(self):
        self.sandbox.cleanup()

    def git(self, *args):
        return subprocess.run(['git', '-C', str(self.root), *args], check=True, text=True,
                              capture_output=True, env=self.env).stdout.strip()

    def write(self, path, value):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(value)

    def commit(self, add=True):
        if add:
            self.git('add', '.')
        for role in ['GIT_AUTHOR_IDENT', 'GIT_COMMITTER_IDENT']:
            self.assertTrue(self.git('var', role).startswith('iliya <iliyazelenkog@gmail.com> '))
        self.git('commit', '-qm', 'test: update disposable CI scope fixture')
        return self.git('rev-parse', 'HEAD')

    def invoke(self, expected):
        self.event['pull_request']['head']['sha'] = self.git('rev-parse', 'HEAD')
        event_path, output = self.root / 'event.json', self.root / 'output'
        event_path.write_text(json.dumps(self.event))
        output.write_text('')
        env = {**self.env, 'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_EVENT_PATH': str(event_path), 'GITHUB_OUTPUT': str(output)}
        subprocess.run(['python3', str(SCRIPT), 'classify'], cwd=self.root, env=env, check=True, capture_output=True)
        self.assertEqual(output.read_text(), 'mode=' + expected + '\n')
        event_path.unlink()
        output.unlink()

    def reset(self):
        self.git('reset', '--hard', self.base)
        self.git('clean', '-fdq')

    def test_windows_add_modify_delete_and_rename_from_actual_git(self):
        for status in ['add', 'modify', 'delete', 'rename']:
            with self.subTest(status=status):
                self.reset()
                if status == 'add':
                    self.write(WINDOWS[0], 'workflow fixture\n')
                    self.write(WINDOWS[3], 'project fixture\n')
                elif status == 'modify':
                    self.write(WINDOWS[2], 'changed fixture\n')
                elif status == 'delete':
                    (self.root / WINDOWS[2]).unlink()
                else:
                    self.git('mv', WINDOWS[2], WINDOWS[3])
                self.commit()
                self.invoke('windows-only')

    def test_real_source_config_and_rename_escapes_require_full(self):
        for kind in ['source', 'config', 'rename-in', 'rename-out', 'mixed']:
            with self.subTest(kind=kind):
                self.reset()
                if kind == 'rename-in':
                    self.git('mv', 'unknown.cpp', WINDOWS[3])
                elif kind == 'rename-out':
                    self.git('mv', WINDOWS[2], 'elsewhere.cpp')
                else:
                    self.write(WINDOWS[2], 'changed fixture\n')
                    self.write({'source': 'source.go', 'config': '.github/workflows/ci-macos.yml', 'mixed': 'README.md'}[kind], 'unsafe change\n')
                self.commit()
                self.invoke('full')

    def test_symlink_and_gitlink_endpoints_require_full(self):
        for kind in ['symlink-add', 'symlink-type', 'gitlink-add', 'symlink-delete', 'gitlink-delete']:
            with self.subTest(kind=kind):
                self.reset()
                if kind in {'gitlink-add', 'gitlink-delete'}:
                    self.git('update-index', '--add', '--cacheinfo', '160000,' + self.base + ',' + WINDOWS[3])
                    endpoint = self.commit(add=False)
                    if kind == 'gitlink-delete':
                        self.event['pull_request']['base']['sha'] = endpoint
                        self.git('update-index', '--force-remove', WINDOWS[3])
                        self.commit(add=False)
                elif kind == 'symlink-add':
                    (self.root / WINDOWS[3]).symlink_to('SDKContract.cpp')
                    self.commit()
                else:
                    target = self.root / WINDOWS[2]
                    target.unlink()
                    target.symlink_to('README.md')
                    endpoint = self.commit()
                    if kind == 'symlink-delete':
                        self.event['pull_request']['base']['sha'] = endpoint
                        target.unlink()
                        self.commit()
                self.invoke('full')
                self.event['pull_request']['base']['sha'] = self.base

    def test_cli_label_draft_and_ready_at_exact_same_head(self):
        self.write(WINDOWS[0], 'workflow fixture\n')
        self.commit()
        self.invoke('windows-only')
        self.event['pull_request']['labels'] = [{'name': 'ci:full'}]
        self.invoke('full')
        self.event['pull_request']['labels'] = []
        self.event['pull_request']['draft'] = False
        self.invoke('full')
        self.event['pull_request']['draft'] = True
        self.event['action'] = 'ready_for_review'
        self.invoke('full')

    def test_merge_base_not_base_tip_defines_pr_changes(self):
        self.write('source.go', 'base-only change\n')
        base_tip = self.commit()
        self.git('checkout', '--detach', self.base)
        self.write(WINDOWS[0], 'workflow fixture\n')
        self.commit()
        self.event['pull_request']['base']['sha'] = base_tip
        self.invoke('windows-only')
        self.event['pull_request']['base']['sha'] = 'f' * 40
        self.invoke('full')

    def test_prose_and_malformed_payload_cli(self):
        self.write('README.md', 'new prose\n')
        self.commit()
        self.invoke('docs')
        event_path, output = self.root / 'event.json', self.root / 'output'
        event_path.write_text('{malformed')
        env = {**self.env, 'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_EVENT_PATH': str(event_path), 'GITHUB_OUTPUT': str(output)}
        subprocess.run(['python3', str(SCRIPT), 'classify'], cwd=self.root, env=env, check=True, capture_output=True)
        self.assertEqual(output.read_text(), 'mode=full\n')


if __name__ == '__main__':
    unittest.main()
