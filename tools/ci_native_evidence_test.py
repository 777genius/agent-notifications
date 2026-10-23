#!/usr/bin/env python3
"""Isolated Git and API fixtures for full-run evidence; no GitHub effects."""
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch
import zipfile

import ci_release_scope_test as fixture
from ci_release_scope import equivalent_source, equivalent_tree, stable_exclusion_only, channel_source
import ci_native_evidence as evidence

WORKFLOW = 'ci-macos.yml'
RELEASE = b"name: Release\n\non:\n  push:\n    tags:\n      - 'v*'\n      - '!v1.0.0'\n\npermissions:\n  contents: read\n"
ADDED = b"      - '!v1.0.1'\n"


def channels(source, snapshot=None):
    return '# fixture\n' + ''.join('\t'.join((*platform, 'v1.0.1', source, snapshot or source, 'release/platform')) + '\n'
                                    for platform in sorted(fixture_scope_platforms()))


def fixture_scope_platforms():
    return {('darwin', 'amd64'), ('darwin', 'arm64'), ('linux', 'amd64'),
            ('linux', 'arm64'), ('windows', 'amd64')}


class EvidenceFixture(fixture.ScopeFixture):
    def setUp(self):
        super().setUp()
        self.write('.github/workflows/release.yml', RELEASE.decode())
        self.write('release-channels.tsv', channels(self.base))
        self.write('docs/RELEASE.md', 'release docs\n')
        self.write('docs/PLATFORM_RELEASE_CHANNELS.md', 'channel docs\n')
        self.write('internal/runtime.go', 'original product\n')
        self.base = self.commit()

    def promote(self):
        self.write('release-channels.tsv', channels(self.base, 'b' * 40))
        self.write('docs/PLATFORM_RELEASE_CHANNELS.md', 'promotion docs\n')
        return self.commit()

    def test_promotion_freezes_release_commit_not_source_snapshot(self):
        head = self.promote()
        with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertEqual(channel_source(head), self.base)
            self.assertEqual(equivalent_source('pull_request', fixture.event(self.base, head)), self.base)
            self.assertTrue(equivalent_tree(self.base, head))

    def test_exact_operator_exclusion_qualifies(self):
        self.write('.github/workflows/release.yml', RELEASE.replace(b'\npermissions:', ADDED + b'\npermissions:').decode())
        head = self.commit()
        with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertEqual(equivalent_source('pull_request', fixture.event(self.base, head)), self.base)

    def test_every_tracked_product_input_and_mode_blocks(self):
        for path in ['internal/runtime.go', 'go.mod', 'Makefile', '.github/workflows/ci-macos.yml',
                     'tools/ci_native_evidence.py', '.claude-plugin/plugin.json', 'docs/UNKNOWN.md']:
            with self.subTest(path=path):
                self.git('reset', '--hard', self.base)
                self.promote()
                self.write(path, 'changed input\n')
                head = self.commit()
                with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
                    self.assertIsNone(equivalent_source('pull_request', fixture.event(self.base, head)))
        self.git('reset', '--hard', self.base)
        self.promote()
        (self.root / 'docs/RELEASE.md').chmod(0o755)
        head = self.commit()
        with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertFalse(equivalent_tree(self.base, head))

    def test_frozen_source_requires_five_unanimous_rows_and_ancestry(self):
        for content in [channels('a' * 40), channels(self.base).replace('windows\tamd64', 'linux\tamd64'),
                        channels(self.base).replace(self.base, 'b' * 40, 1),
                        channels(self.base).split('windows')[0]]:
            self.git('reset', '--hard', self.base)
            self.write('release-channels.tsv', content)
            head = self.commit()
            with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
                with self.assertRaises((ValueError, evidence.subprocess.SubprocessError)):
                    equivalent_source('pull_request', fixture.event(self.base, head))

    def test_receipt_requires_real_full_dependencies_and_exact_checkout(self):
        event_path = self.root / 'payload.json'
        event_path.write_text(json.dumps(fixture.event(self.base, self.base)))
        env = {'CI_MODE': 'full', 'NEEDS_JSON': json.dumps({key: {'result': 'success'}
               for key in ['scope', 'test', 'swift-test']}), 'GITHUB_EVENT_PATH': str(event_path),
               'GITHUB_REPOSITORY': 'owner/repo', 'GITHUB_SHA': self.base,
               'GITHUB_RUN_ID': '12', 'GITHUB_RUN_ATTEMPT': '2'}
        with patch.dict(os.environ, env), patch('ci_native_evidence.git', side_effect=lambda *args: self.git(*args)):
            proof = evidence.receipt(WORKFLOW, ['test', 'swift-test'])
            self.assertEqual(proof['tested_commit'], self.base)
            for mode in ['release-equivalent', 'docs', 'release-tools']:
                with patch.dict(os.environ, {'CI_MODE': mode}), self.assertRaises(ValueError):
                    evidence.receipt(WORKFLOW, ['test', 'swift-test'])
            with patch.dict(os.environ, {'GITHUB_SHA': 'a' * 40}), self.assertRaises(ValueError):
                evidence.receipt(WORKFLOW, ['test', 'swift-test'])
            for status in ['skipped', 'failure', 'cancelled']:
                needs = json.loads(env['NEEDS_JSON']); needs['test']['result'] = status
                with patch.dict(os.environ, {'NEEDS_JSON': json.dumps(needs)}), self.assertRaises(ValueError):
                    evidence.receipt(WORKFLOW, ['test', 'swift-test'])

    def test_tested_merge_tree_is_verified_instead_of_successful_pr_head(self):
        head = self.promote()
        proof = {'schema': 1, 'mode': 'full', 'repository': 'owner/repo', 'workflow': WORKFLOW,
                 'run_id': 12, 'run_attempt': 2, 'source_head': self.base,
                 'tested_commit': self.base, 'tested_tree': self.git('rev-parse', self.base + '^{tree}').decode().strip(),
                 'jobs': ['test', 'swift-test']}
        run = {'id': 12, 'run_attempt': 2, 'head_sha': self.base, 'status': 'completed',
               'conclusion': 'success', 'event': 'pull_request', 'head_repository': {'full_name': 'owner/repo'},
               'path': '.github/workflows/' + WORKFLOW}
        jobs = [{'id': index, 'name': name, 'status': 'completed', 'conclusion': 'success'}
                for index, name in enumerate( (*evidence.CONTRACTS[WORKFLOW][2], evidence.CONTRACTS[WORKFLOW][1]))]
        artifacts = [{'name': evidence.artifact_name(WORKFLOW, 12, 2)}]
        with patch('ci_native_evidence.gh', return_value=run), patch('ci_native_evidence.pages', side_effect=[jobs, artifacts]), \
             patch('ci_native_evidence.download_receipt', return_value=proof), \
             patch('ci_native_evidence.git', side_effect=lambda *args: self.git(*args)), \
             patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertEqual(evidence.verify_run(WORKFLOW, self.base, fixture.event(self.base, head), run), proof)
        # Same successful head/run receipt, different actually tested merge tree.
        self.write('internal/runtime.go', 'unrelated base product change\n')
        wrong_merge = self.commit()
        proof['tested_commit'] = wrong_merge
        proof['tested_tree'] = self.git('rev-parse', wrong_merge + '^{tree}').decode().strip()
        self.git('checkout', '--detach', head)
        with patch('ci_native_evidence.gh', return_value=run), patch('ci_native_evidence.pages', side_effect=[jobs, artifacts]), \
             patch('ci_native_evidence.download_receipt', return_value=proof), \
             patch('ci_native_evidence.git', side_effect=lambda *args: self.git(*args)), \
             patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)), self.assertRaisesRegex(ValueError, 'tested merge checkout'):
            evidence.verify_run(WORKFLOW, self.base, fixture.event(self.base, head), run)


class EvidencePolicy(unittest.TestCase):
    def test_exclusion_is_one_stable_additive_line_in_tags(self):
        valid = RELEASE.replace(b'\npermissions:', ADDED + b'\npermissions:')
        self.assertTrue(stable_exclusion_only(RELEASE, valid))
        for invalid in [valid + ADDED, valid.replace(b'contents: read', b'contents: write'),
                        valid.replace(b'!v1.0.1', b'!v*'), RELEASE + ADDED,
                        valid.replace(b'!v1.0.1', b'!v01.0.1'), RELEASE.replace(b'!v1.0.0', b'!v1.0.1')]:
            self.assertFalse(stable_exclusion_only(RELEASE, invalid))

    def test_skipped_duplicate_or_missing_full_jobs_never_prove_success(self):
        jobs = [{'id': index, 'name': name, 'status': 'completed', 'conclusion': 'success'}
                for index, name in enumerate( (*evidence.CONTRACTS[WORKFLOW][2], evidence.CONTRACTS[WORKFLOW][1]))]
        evidence.validate_jobs(jobs, WORKFLOW)
        for invalid in [jobs[:-1], jobs + [jobs[0]], [dict(jobs[0], conclusion='skipped'), *jobs[1:]]]:
            with self.assertRaises(ValueError):
                evidence.validate_jobs(invalid, WORKFLOW)


    def test_failed_only_retry_preserves_earlier_success_and_uses_latest_job(self):
        names = (*evidence.CONTRACTS[WORKFLOW][2], evidence.CONTRACTS[WORKFLOW][1])
        jobs = [{'id': index, 'name': name, 'status': 'completed', 'conclusion': 'success'}
                for index, name in enumerate(names)]
        jobs[0]['conclusion'] = 'failure'
        retried = dict(jobs[0], id=100, conclusion='success')
        evidence.validate_jobs([*jobs, retried], WORKFLOW)
        with self.assertRaises(ValueError):
            evidence.validate_jobs([*jobs, retried, dict(retried, id=101, conclusion='failure')], WORKFLOW)

    def test_latest_failed_run_cannot_hide_behind_old_green(self):
        payload = fixture.event('a' * 40, 'b' * 40)
        runs = [{'id': 1, 'status': 'completed', 'conclusion': 'success'},
                {'id': 2, 'status': 'completed', 'conclusion': 'failure'}]
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=[]), \
             patch('ci_native_evidence.pages', return_value=runs), \
             patch('ci_native_evidence.verify_run') as verify_run, self.assertRaises(ValueError):
            evidence.verify(WORKFLOW, 'a' * 40, payload, wait_seconds=0)
        verify_run.assert_not_called()


    def test_failed_primary_run_cannot_fall_back_to_associated_green_pr(self):
        source, associated = 'a' * 40, 'b' * 40
        payload = fixture.event(source, 'c' * 40)
        prs = [{'merged_at': '2026-10-23T00:00:00Z', 'merge_commit_sha': source,
                'head': {'sha': associated, 'repo': {'full_name': 'owner/repo'}}}]
        primary = [{'id': 2, 'status': 'completed', 'conclusion': 'failure'}]
        older_green = [{'id': 1, 'status': 'completed', 'conclusion': 'success'}]
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=prs), \
             patch('ci_native_evidence.pages', side_effect=[primary, older_green]), \
             patch('ci_native_evidence.verify_run') as verify_run, self.assertRaisesRegex(ValueError, 'Latest source native run'):
            evidence.verify(WORKFLOW, source, payload, wait_seconds=0)
        verify_run.assert_not_called()

    def test_pending_primary_never_accepts_associated_old_green(self):
        source, associated = 'a' * 40, 'b' * 40
        prs = [{'merged_at': '2026-10-23T00:00:00Z', 'merge_commit_sha': source,
                'head': {'sha': associated, 'repo': {'full_name': 'owner/repo'}}}]
        for status in ['queued', 'in_progress']:
            with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=prs), \
                 patch('ci_native_evidence.pages', return_value=[{'id': 2, 'status': status}]) as pages, \
                 patch('ci_native_evidence.verify_run') as verify_run, self.assertRaises(ValueError):
                evidence.verify(WORKFLOW, source, fixture.event(source, 'c' * 40), wait_seconds=0)
            self.assertEqual(pages.call_count, 1)
            verify_run.assert_not_called()

    def test_latest_run_and_attempt_are_rechecked_after_proof(self):
        source = 'a' * 40
        run = {'id': 1, 'run_attempt': 1, 'status': 'completed', 'conclusion': 'success'}
        for changed in [run, dict(run, id=2), dict(run, run_attempt=2), dict(run, status='in_progress')]:
            with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=[]), \
                 patch('ci_native_evidence.pages', side_effect=[[run], [changed]]), \
                 patch('ci_native_evidence.verify_run', return_value={'run_attempt': 1}):
                if changed == run:
                    self.assertEqual(evidence.verify(WORKFLOW, source, fixture.event(source, 'b' * 40), wait_seconds=0), {'run_attempt': 1})
                else:
                    with self.assertRaisesRegex(ValueError, 'latest source run changed'):
                        evidence.verify(WORKFLOW, source, fixture.event(source, 'b' * 40), wait_seconds=0)

    def test_primary_started_during_associated_proof_blocks_acceptance(self):
        source, associated = 'a' * 40, 'b' * 40
        prs = [{'merged_at': '2026-10-23T00:00:00Z', 'merge_commit_sha': source,
                'head': {'sha': associated, 'repo': {'full_name': 'owner/repo'}}}]
        run = {'id': 1, 'run_attempt': 1, 'status': 'completed', 'conclusion': 'success'}
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=prs), \
             patch('ci_native_evidence.pages', side_effect=[[], [run], [run], [{'id': 2, 'status': 'in_progress'}]]), \
             patch('ci_native_evidence.verify_run', return_value={'run_attempt': 1}), \
             self.assertRaisesRegex(ValueError, 'primary source qualification changed'):
            evidence.verify(WORKFLOW, source, fixture.event(source, 'c' * 40), wait_seconds=0)

    def test_artifact_digest_and_exact_members_required(self):
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, 'w') as archive:
            archive.writestr('native-input-receipt.json', '{"schema":1}')
        data = buffer.getvalue()
        artifact = {'id': 7, 'expired': False, 'digest': 'sha256:' + hashlib.sha256(data).hexdigest()}
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=data):
            self.assertEqual(evidence.download_receipt(artifact), {'schema': 1})
            for changes in [{'expired': True}, {'digest': 'sha256:' + '0' * 64}, {'digest': None}]:
                with self.assertRaises(ValueError):
                    evidence.download_receipt({**artifact, **changes})

    def test_receipt_attempt_and_workflow_binding(self):
        run = {'id': 12, 'run_attempt': 2, 'head_sha': 'a' * 40}
        proof = {'schema': 1, 'mode': 'full', 'repository': 'owner/repo', 'workflow': WORKFLOW,
                 'run_id': 12, 'run_attempt': 2, 'source_head': 'a' * 40,
                 'tested_commit': 'b' * 40, 'tested_tree': 'c' * 40, 'jobs': ['test', 'swift-test']}
        evidence.validate_receipt(proof, WORKFLOW, run, 'owner/repo')
        for changes in [{'mode': 'release-equivalent'}, {'run_attempt': 1}, {'workflow': 'ci-windows.yml'},
                        {'repository': 'fork/repo'}, {'source_head': 'd' * 40}, {'jobs': ['test']}]:
            with self.assertRaises(ValueError):
                evidence.validate_receipt({**proof, **changes}, WORKFLOW, run, 'owner/repo')

    def test_missing_source_and_wait_budget_fall_back_without_model_polling(self):
        payload = fixture.event('a' * 40, 'b' * 40)
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=[]), \
             patch('ci_native_evidence.pages', return_value=[]), self.assertRaisesRegex(ValueError, 'source run absent'):
            evidence.verify(WORKFLOW, 'a' * 40, payload, wait_seconds=0)
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=[]), \
             patch('ci_native_evidence.pages', return_value=[{'id': 1, 'status': 'in_progress'}]), \
             patch('ci_native_evidence.time.sleep') as sleep, self.assertRaises(ValueError):
            evidence.verify(WORKFLOW, 'a' * 40, payload, wait_seconds=0)
        sleep.assert_not_called()


if __name__ == '__main__':
    unittest.main()
