#!/usr/bin/env python3
"""Git fixture E2E: product changes or uncertain diffs must never skip CI."""
import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from ci_release_scope import TOOLS, classify

CLASSIFIER = Path(__file__).with_name('ci_release_scope.py').resolve()
IDENTITY = {'GIT_AUTHOR_NAME': 'iliya', 'GIT_AUTHOR_EMAIL': 'iliyazelenkog@gmail.com',
            'GIT_COMMITTER_NAME': 'iliya', 'GIT_COMMITTER_EMAIL': 'iliyazelenkog@gmail.com'}


def event(base, head):
    repo = {'full_name': 'owner/repo'}
    return {'action': 'synchronize', 'number': 1, 'repository': repo,
            'pull_request': {'number': 1, 'draft': False, 'labels': [],
                             'base': {'sha': base, 'repo': repo},
                             'head': {'sha': head, 'repo': repo}}}


class ScopeFixture(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='TEST-release-ci-')
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git('init', '-q')
        self.git('config', 'core.filemode', 'true')
        self.write('scripts/release-progress.mts', 'original\n')
        self.git('add', '.')
        self.check_identity()
        self.git('commit', '-qm', 'test: fixture base')
        self.base = self.git('rev-parse', 'HEAD').strip().decode()

    def git(self, *args):
        return subprocess.run(['git', *args], cwd=self.root, env={**os.environ, **IDENTITY},
                              check=True, capture_output=True, timeout=15).stdout

    def write(self, path, text):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def check_identity(self):
        for name in ['GIT_AUTHOR_IDENT', 'GIT_COMMITTER_IDENT']:
            self.assertTrue(self.git('var', name).decode().startswith('iliya <iliyazelenkog@gmail.com> '))

    def commit(self):
        self.check_identity()
        self.git('add', '-A')
        self.git('commit', '-qm', 'test: fixture change')
        return self.git('rev-parse', 'HEAD').strip().decode()

    def mode(self, name='pull_request', payload=None):
        payload = payload or event(self.base, self.git('rev-parse', 'HEAD').strip().decode())
        event_path, output = self.root / 'event.json', self.root / 'output.txt'
        event_path.write_text(json.dumps(payload))
        output.write_text('')
        result = subprocess.run(['python3', '-B', str(CLASSIFIER), 'classify'], cwd=self.root,
                                env={**os.environ, 'GITHUB_EVENT_NAME': name,
                                     'GITHUB_EVENT_PATH': str(event_path), 'GITHUB_OUTPUT': str(output)},
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        return output.read_text().strip()

    def test_exact_reviewed_tools_and_documentation_pass(self):
        # An accidentally broad path pattern would also admit the unknown path test.
        for path in TOOLS:
            self.write(path, 'reviewed operator change\n')
        self.commit()
        self.assertEqual(self.mode(), 'mode=release-tools')

    def test_product_version_ci_build_and_unknown_paths_force_full(self):
        for path in ['cmd/claude-notifications/main.go', '.claude-plugin/plugin.json',
                     'release-channels.tsv', 'go.mod', 'bin/install.sh', 'Makefile',
                     'tools/ci_release_scope.py', 'scripts/ci_macos_scope.py',
                     '.github/workflows/ci-ubuntu.yml', 'scripts/release-surprise.mts',
                     'scripts/basic-release-assets/unreviewed.mts']:
            with self.subTest(path=path):
                self.git('reset', '--hard', self.base)
                self.git('clean', '-fdq')
                self.write('scripts/release-progress.mts', 'operator\n')
                self.write(path, 'unreviewed\n')
                self.commit()
                self.assertEqual(self.mode(), 'mode=full')

    def test_delete_rename_mode_and_symlink_force_full(self):
        for operation in ['delete', 'rename', 'mode', 'symlink']:
            with self.subTest(operation=operation):
                self.git('reset', '--hard', self.base)
                self.git('clean', '-fdq')
                target = self.root / 'scripts/release-progress.mts'
                if operation == 'delete':
                    target.unlink()
                elif operation == 'rename':
                    target.rename(self.root / 'scripts/release-wait.mts')
                elif operation == 'mode':
                    target.chmod(0o755)
                else:
                    target.unlink()
                    target.symlink_to('release-wait.mts')
                self.commit()
                self.assertEqual(self.mode(), 'mode=full')

    def test_full_override_and_non_pr_or_unknown_identity_force_full(self):
        self.write('scripts/release-progress.mts', 'operator\n')
        head = self.commit()
        good = event(self.base, head)
        for kind in ['push', 'workflow_dispatch', 'merge_group']:
            self.assertEqual(self.mode(kind, good), 'mode=full')
        changes = [lambda p: p['pull_request']['labels'].append({'name': 'ci:full'}),
                   lambda p: p.update(action='unknown'),
                   lambda p: p['pull_request']['head'].update(sha='0' * 40),
                   lambda p: p['pull_request']['base'].update(sha='a' * 40),
                   lambda p: p['pull_request'].update(labels=None),
                   lambda p: p['pull_request'].update(number=2)]
        for mutate in changes:
            payload = copy.deepcopy(good)
            mutate(payload)
            self.assertEqual(self.mode(payload=payload), 'mode=full')

    def test_empty_and_invalid_raw_diff_force_full(self):
        good = event(self.base, self.base)
        self.assertEqual(self.mode(payload=good), 'mode=full')
        raw = self.git('diff', '--raw', '-z', '--no-abbrev', self.base, self.base)
        for invalid in [raw, b'garbage\0', b':100644 100644 bad bad M\0docs/RELEASE.md\0',
                        b'\xff\0', b'no trailing nul']:
            self.assertEqual(classify('pull_request', good, invalid), 'full')

    def test_base_only_product_changes_do_not_enter_pr_diff(self):
        self.write('scripts/release-progress.mts', 'operator\n')
        head = self.commit()
        self.git('checkout', '-qb', 'base-advanced', self.base)
        self.write('cmd/main.go', 'base product update\n')
        base = self.commit()
        self.assertEqual(self.mode(payload=event(base, head)), 'mode=release-tools')


if __name__ == '__main__':
    unittest.main()
