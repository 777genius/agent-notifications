#!/usr/bin/env python3
"""Regression: unsafe paths/missing evidence must never skip or pass native CI."""
import copy
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from ci_macos_scope import classify, final_gate

SCRIPT = Path(__file__).with_name('ci_macos_scope.py')
EVENT = {'action': 'synchronize', 'pull_request': {'labels': [], 'base': {'sha': 'a' * 40}, 'head': {'sha': 'b' * 40}}}


class ScopeTests(unittest.TestCase):
    def test_modified_prose_only(self):
        for path in ['README.md', 'docs/DO_NOT_DISTURB.md', 'docs/NOTIFICATION_TYPES.md']:
            self.assertEqual(classify('pull_request', EVENT, ('M\0' + path + '\0').encode()), 'docs')
        self.assertEqual(classify('pull_request', EVENT, b'M\0README.md\0M\0docs/DO_NOT_DISTURB.md\0'), 'docs')

    def test_unsafe_paths_and_statuses_fail_open(self):
        for path in ['go.mod', 'go.sum', 'internal/main.go', '.github/workflows/ci-macos.yml',
                     'bin/install.sh', 'scripts/ci_macos_scope.py', 'docs/CI_RUNNERS.md',
                     'docs/qualification/evidence.json', 'docs/handoffs/readme.md',
                     'docs/ARCHITECTURE.md', 'docs/RELEASE.md', 'unknown.md', 'README.md\nother']:
            with self.subTest(path=path):
                self.assertEqual(classify('pull_request', EVENT, ('M\0README.md\0M\0' + path + '\0').encode()), 'full')
        for status in ['A', 'D', 'T', 'U', 'R100', 'C100', 'unknown']:
            self.assertEqual(classify('pull_request', EVENT, (status + '\0README.md\0').encode()), 'full')
        self.assertEqual(classify('pull_request', EVENT, b'R100\0README.md\0docs/DO_NOT_DISTURB.md\0'), 'full')

    def test_empty_truncated_malformed_diff_fails_open(self):
        for diff in [b'', b'M\0README.md', b'M\0README.md\0M\0', b'M\0\xff\0']:
            self.assertEqual(classify('pull_request', EVENT, diff), 'full')

    def test_full_events_label_and_unknown_metadata(self):
        for event_name in ['push', 'workflow_dispatch', 'release', 'merge_group', 'unknown']:
            self.assertEqual(classify(event_name, EVENT, b'M\0README.md\0'), 'full')
        for change in [{'labels': [{'name': 'ci:full'}]}, {'labels': None}, {'labels': [None]},
                       {'labels': [{'name': 123}]}, {'head': {}}, {'base': {'sha': '--bad'}}]:
            event = copy.deepcopy(EVENT)
            event['pull_request'].update(change)
            self.assertEqual(classify('pull_request', event, b'M\0README.md\0'), 'full')
        event = copy.deepcopy(EVENT)
        event['action'] = 'ready_for_review'
        self.assertEqual(classify('pull_request', event, b'M\0README.md\0'), 'full')

    def test_fork_prose_uses_same_conservative_contract(self):
        event = copy.deepcopy(EVENT)
        event['pull_request']['head']['repo'] = {'full_name': 'someone/fork'}
        self.assertEqual(classify('pull_request', event, b'M\0README.md\0'), 'docs')
        event['action'] = 'labeled'
        event['pull_request']['labels'] = [{'name': 'ci:full'}]
        self.assertEqual(classify('pull_request', event, b'M\0README.md\0'), 'full')
        event['action'] = 'unlabeled'
        event['pull_request']['labels'] = []
        self.assertEqual(classify('pull_request', event, b'M\0README.md\0'), 'docs')

    def test_gate_requires_actual_success(self):
        needs = {name: {'result': 'success'} for name in ['scope', 'test', 'swift-test', 'recovery']}
        self.assertTrue(final_gate('full', needs, ['test', 'swift-test'])[0])
        self.assertTrue(final_gate('full', needs, ['recovery'])[0])
        self.assertFalse(final_gate('docs', needs, ['test'])[0])
        for name in needs:
            for result in ['failure', 'skipped', 'cancelled', 'unknown']:
                changed = copy.deepcopy(needs)
                changed[name]['result'] = result
                self.assertFalse(final_gate('full', changed, ['test', 'swift-test', 'recovery'])[0])
        for broken in [{}, [], {'scope': None}]:
            self.assertFalse(final_gate('full', broken, ['test'])[0])

    def test_cli_real_git_and_missing_revision(self):
        # Actual git output, not a source-text assertion or a mocked git result.
        with tempfile.TemporaryDirectory(prefix='TEST-ci-mac-scope-') as tmp:
            root = Path(tmp)
            def git(*args):
                return subprocess.run(['git', '-C', tmp, *args], check=True, text=True, capture_output=True, env={**os.environ, 'GIT_AUTHOR_NAME': 'iliya', 'GIT_AUTHOR_EMAIL': 'iliyazelenkog@gmail.com', 'GIT_COMMITTER_NAME': 'iliya', 'GIT_COMMITTER_EMAIL': 'iliyazelenkog@gmail.com'}).stdout.strip()
            git('init', '-q')
            git('config', 'user.name', 'iliya')
            git('config', 'user.email', 'iliyazelenkog@gmail.com')
            (root / 'README.md').write_text('old\n')
            git('add', '.')
            # Disposable commits still use the owner's identity.
            self.assertTrue(git('var', 'GIT_AUTHOR_IDENT').startswith('iliya <iliyazelenkog@gmail.com> '))
            self.assertTrue(git('var', 'GIT_COMMITTER_IDENT').startswith('iliya <iliyazelenkog@gmail.com> '))
            git('commit', '-qm', 'test: initialize sandbox fixture')
            base = git('rev-parse', 'HEAD')
            (root / 'README.md').write_text('new\n')
            self.assertTrue(git('var', 'GIT_AUTHOR_IDENT').startswith('iliya <iliyazelenkog@gmail.com> '))
            self.assertTrue(git('var', 'GIT_COMMITTER_IDENT').startswith('iliya <iliyazelenkog@gmail.com> '))
            git('commit', '-qam', 'test: update sandbox prose')
            head = git('rev-parse', 'HEAD')
            event = copy.deepcopy(EVENT)
            event['pull_request'].update(base={'sha': base}, head={'sha': head})
            event_path = root / 'event.json'
            output = root / 'output'
            env = {**os.environ, 'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_EVENT_PATH': str(event_path), 'GITHUB_OUTPUT': str(output)}
            def invoke(expected):
                event_path.write_text(json.dumps(event))
                output.write_text('')
                subprocess.run(['python3', str(SCRIPT), 'classify'], cwd=tmp, env=env, check=True, capture_output=True)
                self.assertEqual(output.read_text(), 'mode=' + expected + '\n')
            invoke('docs')
            event['pull_request']['head']['sha'] = 'f' * 40
            invoke('full')
            event_path.write_text('{malformed')
            output.write_text('')
            subprocess.run(['python3', str(SCRIPT), 'classify'], cwd=tmp, env=env, check=True, capture_output=True)
            self.assertEqual(output.read_text(), 'mode=full\n')


if __name__ == '__main__':
    unittest.main()
