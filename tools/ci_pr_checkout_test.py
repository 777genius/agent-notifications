#!/usr/bin/env python3
"""Real disposable Git regressions for stale base-edit merge checkouts."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import json
import re
import textwrap

import ci_native_evidence as evidence

SOURCE_ROOT = Path(__file__).resolve().parents[1]
HELPER = SOURCE_ROOT / 'tools/ci_qualify_pr_base.sh'


class CheckoutFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='TEST-pr-base-checkout-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = dict(os.environ, GIT_AUTHOR_NAME='iliya', GIT_AUTHOR_EMAIL='iliyazelenkog@gmail.com',
                        GIT_COMMITTER_NAME='iliya', GIT_COMMITTER_EMAIL='iliyazelenkog@gmail.com',
                        GITHUB_WORKSPACE=str(self.root))
        self.git('init', '-q')
        (self.root / 'shared').write_text('original\n')
        self.base = self.commit()
        (self.root / 'feature').write_text('head input\n')
        self.head = self.commit()
        self.git('checkout', '--detach', self.base)
        self.git('merge', '--no-ff', self.head, '-m', 'ci: stale merge fixture')
        self.stale = self.git('rev-parse', 'HEAD')
        self.git('checkout', '--detach', self.base)
        (self.root / 'new-base').write_text('new target input\n')
        self.new_base = self.commit()
        self.git('checkout', '--detach', self.stale)
        self.env['GITHUB_SHA'] = self.stale

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, env=self.env,
                                       stderr=subprocess.PIPE, text=True, timeout=20).strip()

    def commit(self):
        self.assertTrue(self.git('var', 'GIT_AUTHOR_IDENT').startswith('iliya <iliyazelenkog@gmail.com> '))
        self.assertTrue(self.git('var', 'GIT_COMMITTER_IDENT').startswith('iliya <iliyazelenkog@gmail.com> '))
        self.git('add', '.')
        self.git('commit', '-qm', 'test: disposable source revision')
        return self.git('rev-parse', 'HEAD')

    def qualify(self, base=None, head=None):
        env = dict(self.env, CI_PR_HEAD_SHA=head or self.head, CI_PR_BASE_SHA=base or self.new_base)
        return subprocess.run(['bash', str(HELPER)], cwd=self.root, env=env,
                              capture_output=True, text=True, timeout=30)

    def test_stale_checkout_red_then_exact_event_pair_green(self):
        self.assertFalse((self.root / 'new-base').exists())  # old cached merge lacks the new target
        result = self.qualify()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / 'new-base').read_text(), 'new target input\n')
        self.assertEqual((self.root / 'feature').read_text(), 'head input\n')
        self.assertEqual(self.git('show', '-s', '--format=%P', 'HEAD'), f'{self.new_base} {self.head}')
        self.assertNotEqual(self.git('rev-parse', 'HEAD'), self.stale)
        self.assertEqual(self.env['GITHUB_SHA'], self.stale)
        self.assertIn('not immutable release qualification', result.stdout)
        self.assertEqual(self.git('show', '-s', '--format=%an <%ae>|%cn <%ce>', 'HEAD'),
                         'iliya <iliyazelenkog@gmail.com>|iliya <iliyazelenkog@gmail.com>')

    def test_run_local_merge_cannot_become_immutable_release_evidence(self):
        self.assertEqual(self.qualify().returncode, 0)
        event_path = self.root / 'event.json'
        event_path.write_text(json.dumps({'pull_request': {'head': {'sha': self.head}}}))
        needs = {name: {'result': 'success'} for name in ('scope', 'test', 'swift-test')}
        env = dict(self.env, GITHUB_EVENT_PATH=str(event_path), CI_MODE='full', NEEDS_JSON=json.dumps(needs))
        with patch.dict(os.environ, env), patch('ci_native_evidence.git', side_effect=lambda *args: self.git(*args).encode()):
            with self.assertRaisesRegex(ValueError, 'immutable checkout identity required'):
                evidence.receipt('ci-macos.yml', ('test', 'swift-test'))
        names = (*evidence.CONTRACTS['ci-macos.yml'][2], evidence.CONTRACTS['ci-macos.yml'][1])
        jobs = [{'id': index + 1, 'name': name, 'status': 'completed',
                 'conclusion': 'skipped' if index == len(names) - 1 else 'success'}
                for index, name in enumerate(names)]
        with self.assertRaisesRegex(ValueError, 'missing successful full job'):
            evidence.validate_jobs(jobs, 'ci-macos.yml')

    def test_actual_pinned_bootstrap_isolated_fetch_and_cleanup(self):
        # Only read the product's Git objects into an owned fixture mirror. No
        # product command/provider executes; the tested checkout is this fixture.
        self.git('checkout', '--detach', self.head)
        (self.root / 'shared').write_text('head conflict\n')
        conflict_head = self.commit()
        self.git('checkout', '--detach', self.new_base)
        (self.root / 'shared').write_text('base conflict\n')
        conflict_base = self.commit()
        self.git('checkout', '--detach', self.stale)
        mirror = self.root / 'helper-mirror.git'
        subprocess.run(['git', 'clone', '--bare', '--shared', str(SOURCE_ROOT), str(mirror)],
                       check=True, capture_output=True, env=self.env, timeout=20)
        runner_temp = self.root / 'runner-temp'
        runner_temp.mkdir()
        for workflow in ('ci-macos.yml', 'install-recovery-e2e.yml'):
            text = (SOURCE_ROOT / '.github/workflows' / workflow).read_text()
            block = text.split('      - &qualify_base_edit\n', 1)[1]
            pin = re.search(r'CI_PR_SOURCE_HELPER_SHA: ([0-9a-f]{40})', block).group(1)
            body = block.split('        run: |\n', 1)[1]
            lines = []
            for line in body.splitlines():
                if line and not line.startswith('          '):
                    break
                lines.append(line)
            command = textwrap.dedent('\n'.join(lines))
            pinned = subprocess.check_output(['git', '-C', str(mirror), 'show',
                                              pin + ':tools/ci_qualify_pr_base.sh'], text=True)
            self.assertEqual(pinned, HELPER.read_text())
            for case in ('valid', 'invalid', 'conflict', 'missing'):
                with self.subTest(workflow=workflow, case=case):
                    self.git('reset', '--hard', self.stale)
                    head = 'bad' if case == 'invalid' else conflict_head if case == 'conflict' else self.head
                    base = conflict_base if case == 'conflict' else '0' * 40 if case == 'missing' else self.new_base
                    env = dict(self.env, CI_PR_HEAD_SHA=head,
                               CI_PR_BASE_SHA=base, CI_PR_SOURCE_HELPER_SHA=pin,
                               RUNNER_TEMP=str(runner_temp), GIT_CONFIG_COUNT='1',
                               GIT_CONFIG_KEY_0='url.' + mirror.as_uri() + '.insteadOf',
                               GIT_CONFIG_VALUE_0='https://github.com/777genius/agent-notifications.git')
                    result = subprocess.run(['/bin/bash', '-c', command], cwd=self.root, env=env,
                                            capture_output=True, text=True, timeout=30)
                    self.assertEqual(result.returncode == 0, case == 'valid', result.stderr)
                    self.assertFalse((self.root / '.git/shallow').exists())
                    self.assertEqual(list(runner_temp.iterdir()), [])
                    if case == 'valid':
                        self.assertEqual((self.root / 'new-base').read_text(), 'new target input\n')
                        self.assertEqual(self.git('show', '-s', '--format=%P', 'HEAD'),
                                         f'{self.new_base} {self.head}')

    def test_identical_head_and_base_is_valid_without_fake_merge(self):
        result = self.qualify(base=self.head)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.head)

    def test_conflicting_pair_fails_before_test_source_is_qualified(self):
        self.git('checkout', '--detach', self.head)
        (self.root / 'shared').write_text('head conflict\n')
        head = self.commit()
        self.git('checkout', '--detach', self.new_base)
        (self.root / 'shared').write_text('base conflict\n')
        base = self.commit()
        self.git('checkout', '--detach', self.stale)
        result = self.qualify(base=base, head=head)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Qualified run-local CI input:', result.stdout)

    def test_invalid_revision_fails_before_git_or_shell_interpolation(self):
        result = self.qualify(head='$(touch should-not-exist)')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'should-not-exist').exists())
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.stale)

    def test_missing_revision_cannot_fall_back_to_cached_merge(self):
        result = self.qualify(base='0' * 40)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Qualified run-local CI input:', result.stdout)
        self.assertEqual(self.git('rev-parse', 'HEAD'), self.stale)


if __name__ == '__main__':
    unittest.main()
