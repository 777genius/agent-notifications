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
from ci_macos_scope import PROSE

CLASSIFIER = Path(__file__).with_name('ci_release_scope.py').resolve()
IDENTITY = {'GIT_AUTHOR_NAME': 'iliya', 'GIT_AUTHOR_EMAIL': 'iliyazelenkog@gmail.com',
            'GIT_COMMITTER_NAME': 'iliya', 'GIT_COMMITTER_EMAIL': 'iliyazelenkog@gmail.com'}


def event(base, head):
    repo = {'full_name': 'owner/repo'}
    return {'action': 'synchronize', 'number': 1, 'repository': repo,
            'pull_request': {'number': 1, 'draft': False, 'labels': [],
                             'base': {'sha': base, 'repo': dict(repo)},
                             'head': {'sha': head, 'repo': dict(repo)}}}


class ScopeFixture(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='TEST-release-ci-')
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git('init', '-q')
        self.git('config', 'core.filemode', 'true')
        self.write('scripts/release-progress.mts', 'original\n')
        for path in PROSE:
            self.write(path, 'original prose\n')
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

    def test_exact_existing_prose_only_keeps_native_ci_lightweight(self):
        for path in PROSE:
            self.write(path, 'revised prose\n')
        head = self.commit()
        self.assertEqual(self.mode(), 'mode=docs')
        good = event(self.base, head)
        full = copy.deepcopy(good)
        full['pull_request']['labels'].append({'name': 'ci:full'})
        self.assertEqual(self.mode(payload=full), 'mode=full')
        fork = copy.deepcopy(good)
        fork['pull_request']['head']['repo']['full_name'] = 'fork/repo'
        self.assertEqual(self.mode(payload=fork), 'mode=full')
        self.assertEqual(self.mode('push', good), 'mode=full')
        self.write('cmd/product.go', 'product change\n')
        self.commit()
        self.assertEqual(self.mode(), 'mode=full')

    def test_prose_add_delete_mode_and_symlink_force_full(self):
        for operation in ['add', 'delete', 'mode', 'symlink']:
            with self.subTest(operation=operation):
                self.git('reset', '--hard', self.base)
                self.git('clean', '-fdq')
                target = self.root / 'README.md'
                if operation == 'add':
                    self.write('docs/NEW.md', 'new file\n')
                elif operation == 'delete':
                    target.unlink()
                elif operation == 'mode':
                    target.chmod(0o755)
                else:
                    target.unlink()
                    target.symlink_to('docs/DO_NOT_DISTURB.md')
                self.commit()
                self.assertEqual(self.mode(), 'mode=full')

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
                   lambda p: p['pull_request'].update(number=2),
                   lambda p: p['pull_request']['head'].update(repo={'full_name': 'fork/repo'})]
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



class ClassifierRunnerPolicy(unittest.TestCase):
    def test_read_only_scope_jobs_do_not_queue_on_native_pool(self):
        # These jobs classify Git/Actions evidence, not native E2E. Maintain
        # the exact final native matrices on their original qualified runners.
        import re

        root = Path(__file__).resolve().parents[1]
        for name in ('ci-ubuntu.yml', 'ci-macos.yml', 'ci-windows.yml',
                     'install-recovery-e2e.yml'):
            with self.subTest(workflow=name):
                workflow = (root / '.github/workflows' / name).read_text()
                scope = re.search(
                    r'(?ms)^  scope:\n(.*?)(?=^  [a-z][\w-]+:\s*$|\Z)',
                    workflow,
                )
                self.assertIsNotNone(scope)
                self.assertIn('    runs-on: ubuntu-latest\n', scope.group(1))
                self.assertNotIn('vars.CI_LINUX_RUNNER', scope.group(1))
                self.assertIn('CI_NATIVE_WORKFLOW:', scope.group(1))





class HeavyNativeDraftTierPolicy(unittest.TestCase):
    """Run source-accurate workflow admission checks before any native jobs."""

    def test_native_draft_deferred_until_ready_without_losing_manual_or_schedule(self):
        import re

        root = Path(__file__).resolve().parents[1] / '.github' / 'workflows'
        workflows = {
            'pi-installed-linux.yml': ('linux',),
            'pi-installed-macos.yml': ('macos',),
            'pi-installed-windows.yml': ('build-process',),
            'strict-terminal-native-linux.yml': ('strict-terminal',),
            'pi-native-contracts.yml': ('native',),
            'opencode-range-e2e.yml': ('range',),
            'opencode-v226-source-e2e.yml': (
                'actual-source', 'windows-x64-official-image', 'darwin-arm64-image'),
        }
        simple = "if: github.event_name != 'pull_request' || github.event.pull_request.draft != true"
        fork = ("if: github.event_name != 'pull_request' || "
                "(github.event.pull_request.head.repo.full_name == github.repository && "
                "github.event.pull_request.draft != true)")
        for filename, jobs in workflows.items():
            with self.subTest(workflow=filename):
                source = (root / filename).read_text()
                header, separator, job_source = source.partition('\njobs:\n')
                self.assertTrue(separator, filename + ': missing job declaration')
                self.assertIn('types: [opened, synchronize, reopened, ready_for_review]', header)
                self.assertIn('  workflow_dispatch:', header)
                if filename == 'opencode-range-e2e.yml':
                    self.assertIn('  schedule:', header)
                if filename in ('pi-installed-linux.yml', 'pi-installed-macos.yml'):
                    self.assertIn('  workflow_call:', header)
                blocks = dict(re.findall(
                    r'(?ms)^  ([a-z][\w-]+):\n(.*?)(?=^  [a-z][\w-]+:\s*$|\Z)',
                    job_source,
                ))
                for job in jobs:
                    self.assertIn(job, blocks, filename + ': lost E2E job')
                    expected = fork if filename.startswith('opencode-') else simple
                    self.assertIn('    ' + expected + '\n', blocks[job])
                    self.assertIn('    timeout-minutes:', blocks[job])
                    self.assertNotIn('continue-on-error: true', blocks[job])
        win = (root / 'pi-installed-windows.yml').read_text()
        self.assertIn(
            "if: github.event_name == 'workflow_dispatch' && github.repository == "
            "'777genius/agent-notifications' && github.actor == '777genius'",
            win,
        )
        # Evaluate both expressions' actual event acceptance independently:
        for kind in ('simple', 'fork'):
            for event in ('pull_request', 'workflow_dispatch', 'schedule', 'workflow_call'):
                for draft in (False, True):
                    for same_repo in (False, True):
                        accepted = event != 'pull_request' or (
                            not draft and (same_repo or kind == 'simple'))
                        if event != 'pull_request':
                            self.assertTrue(accepted, (kind, event, draft))
                        elif draft or (kind == 'fork' and not same_repo):
                            self.assertFalse(accepted, (kind, event, draft, same_repo))
                        else:
                            self.assertTrue(accepted, (kind, event, draft, same_repo))


if __name__ == '__main__':
    unittest.main()
