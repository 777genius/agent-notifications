#!/usr/bin/env python3
"""Isolated Git and API fixtures for full-run evidence; no GitHub effects."""
import copy
import hashlib
import io
import json
import os
import subprocess
from pathlib import Path
import unittest
from unittest.mock import patch
import zipfile

import ci_release_scope_test as fixture
from ci_release_scope import equivalent_source, equivalent_tree, channel_source
import ci_native_evidence as evidence

WORKFLOW = 'ci-macos.yml'


def channels(source, snapshot=None):
    return '# fixture\n' + ''.join('\t'.join((*platform, 'v1.0.1', source, snapshot or source, 'release/platform')) + '\n'
                                    for platform in sorted(fixture_scope_platforms()))


def fixture_scope_platforms():
    return {('darwin', 'amd64'), ('darwin', 'arm64'), ('linux', 'amd64'),
            ('linux', 'arm64'), ('windows', 'amd64')}


class EvidenceFixture(fixture.ScopeFixture):
    def setUp(self):
        super().setUp()
        self.write('release-channels.tsv', channels(self.base))
        self.write('docs/RELEASE.md', 'release docs\n')
        self.write('docs/PLATFORM_RELEASE_CHANNELS.md', 'channel docs\n')
        self.write('internal/runtime.go', 'original product\n')
        self.base = self.commit()

    def promote(self):
        self.write('release-channels.tsv', channels(self.base, 'b' * 40))
        self.write('docs/PLATFORM_RELEASE_CHANNELS.md', 'promotion docs\n')
        return self.commit()


    def test_main_promotion_requires_unique_exact_merge_and_frozen_inputs(self):
        head = self.promote()
        self.git('checkout', '-qB', 'promotion-main-TEST', self.base)
        self.git('merge', '--no-ff', '-m', 'test: promotion merge', head)
        merged = self.git('rev-parse', 'HEAD').decode().strip()
        payload = {'ref': 'refs/heads/main', 'before': self.base, 'after': merged,
                   'repository': {'full_name': 'owner/repo'}}
        pr = fixture.event(self.base, head)['pull_request']
        pr.update(merged_at='2026-11-05T10:00:31Z', merge_commit_sha=merged)
        pr['base']['ref'] = 'main'
        env = {'GITHUB_REPOSITORY': 'owner/repo'}

        def source_for(items, event=payload):
            with patch.dict(os.environ, env), \
                 patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)), \
                 patch('ci_release_scope.merged_pulls', return_value=items):
                return equivalent_source('push', event)

        self.assertEqual(source_for([pr]), self.base)
        for changed in [
            {'ref': 'refs/heads/develop'},
            {'before': 'f' * 40},
            {'after': 'e' * 40},
            {'repository': {'full_name': 'other/repo'}},
        ]:
            self.assertIsNone(source_for([pr], {**payload, **changed}))
        for mutation in (
            lambda x: x.update(merged_at=None),
            lambda x: x.update(merge_commit_sha=head),
            lambda x: x['base'].update(ref='develop'),
            lambda x: x['base'].update(sha='f' * 40),
            lambda x: x['head'].update(sha='f' * 40),
            lambda x: x['head'].update(repo={'full_name': 'foreign/repo'}),
            lambda x: x['head'].update(repo='untrusted'),
            lambda x: x.update(head=None),
            lambda x: x.update(base=None),
        ):
            changed = copy.deepcopy(pr)
            mutation(changed)
            self.assertIsNone(source_for([changed]))
        for items in ([], [pr, pr], None, 'untrusted'):
            self.assertIsNone(source_for(items))

        # The merged source is now different from its qualified PR head.
        self.write('internal/runtime.go', 'unreviewed main-only product change\n')
        tampered = self.commit()
        after_change = {**payload, 'after': tampered, 'before': merged}
        changed_pr = copy.deepcopy(pr)
        changed_pr['merged_at'] = '2026-11-05T10:01:00Z'
        changed_pr['merge_commit_sha'] = tampered
        changed_pr['base']['sha'] = merged
        self.assertIsNone(source_for([changed_pr], after_change))

    def test_squash_head_missing_from_main_fetches_exact_pr_ref_or_denies_reuse(self):
        head = self.promote()
        self.git('checkout', '-qB', 'promotion-squash-TEST', self.base)
        self.write('release-channels.tsv', channels(self.base, 'b' * 40))
        self.write('docs/PLATFORM_RELEASE_CHANNELS.md', 'promotion docs\n')
        squashed = self.commit()
        payload = {'ref': 'refs/heads/main', 'before': self.base, 'after': squashed,
                   'repository': {'full_name': 'owner/repo'}}
        pr = fixture.event(self.base, head)['pull_request']
        pr.update(merged_at='2026-11-05T10:00:31Z', merge_commit_sha=squashed)
        pr['base']['ref'] = 'main'

        def read_missing(*arguments):
            if arguments == ('cat-file', '-e', head + '^{commit}'):
                raise subprocess.CalledProcessError(1, 'git')
            if arguments and arguments[0] == 'fetch':
                self.assertEqual(arguments, ('fetch', '--no-tags', 'origin', 'refs/pull/1/head'))
                return b''
            if arguments == ('rev-parse', 'FETCH_HEAD'):
                return (head + '\n').encode()
            return self.git(*arguments)

        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), \
             patch('ci_release_scope.merged_pulls', return_value=[pr]), \
             patch('ci_release_scope.git', side_effect=read_missing):
            self.assertEqual(equivalent_source('push', payload), self.base)

        def wrong_ref(*arguments):
            if arguments == ('rev-parse', 'FETCH_HEAD'):
                return ('f' * 40 + '\n').encode()
            return read_missing(*arguments)

        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), \
             patch('ci_release_scope.merged_pulls', return_value=[pr]), \
             patch('ci_release_scope.git', side_effect=wrong_ref):
            self.assertIsNone(equivalent_source('push', payload))

    def test_promotion_freezes_release_commit_not_source_snapshot(self):
        head = self.promote()
        with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertEqual(channel_source(head), self.base)
            self.assertEqual(equivalent_source('pull_request', fixture.event(self.base, head)), self.base)
            self.assertTrue(equivalent_tree(self.base, head))

    def test_stacked_promotion_reuses_frozen_candidate_with_inherited_product_changes(self):
        old_base = self.base
        self.write('internal/runtime.go', 'new candidate product and version\n')
        self.write('internal/new-feature.go', 'new qualified candidate file\n')
        candidate = self.commit()
        self.write('release-channels.tsv', channels(candidate))
        head = self.commit()
        with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertEqual(equivalent_source('pull_request', fixture.event(old_base, head)), candidate)
        self.write('internal/runtime.go', 'unqualified promotion product change\n')
        head = self.commit()
        with patch('ci_release_scope.git', side_effect=lambda *args: self.git(*args)):
            self.assertIsNone(equivalent_source('pull_request', fixture.event(old_base, head)))

    def test_every_tracked_product_input_and_mode_blocks(self):
        for path in ['internal/runtime.go', 'go.mod', 'Makefile', '.github/workflows/ci-macos.yml',
                     'tools/ci_native_evidence.py', '.claude-plugin/plugin.json', 'docs/UNKNOWN.md',
                     '.github/workflows/release.yml']:
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

    def metadata_jobs(self, workflow=WORKFLOW):
        label = 'macOS' if workflow == WORKFLOW else 'recovery'
        names = ["github.event.action == 'edited' && 'Metadata edit ignored (scope)' || 'Classify macOS CI scope'",
                 "github.event.action == 'edited' && format('Metadata edit ignored (native {0})', matrix.os)",
                 'Release-equivalent native proof (' + label + ')', evidence.CONTRACTS[workflow][1]]
        return [{'name': name, 'status': 'completed', 'conclusion': 'skipped'} for name in names]

    def test_proven_skipped_metadata_run_ignored_in_initial_and_final_census(self):
        source = 'a' * 40
        good = {'id': 1, 'run_attempt': 1, 'status': 'completed', 'conclusion': 'success'}
        ignored = {'id': 2, 'event': 'pull_request', 'status': 'completed', 'conclusion': 'skipped'}
        for workflow in [WORKFLOW, 'install-recovery-e2e.yml']:
            with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'owner/repo'}), patch('ci_native_evidence.gh', return_value=[]), \
                 patch('ci_native_evidence.pages', side_effect=[[good, ignored], self.metadata_jobs(workflow),
                                                               [good, ignored], self.metadata_jobs(workflow)]), \
                 patch('ci_native_evidence.verify_run', return_value={'run_attempt': 1}) as verify_run:
                self.assertEqual(evidence.verify(workflow, source, fixture.event(source, 'b' * 40), wait_seconds=0),
                                 {'run_attempt': 1})
                self.assertEqual(verify_run.call_args.args[-1], good)

    def test_metadata_exemption_never_ignores_native_failure_pending_or_uncertain_jobs(self):
        good = {'id': 1, 'status': 'completed', 'conclusion': 'success'}
        metadata = {'id': 2, 'event': 'pull_request', 'status': 'completed', 'conclusion': 'skipped'}
        for changes in [{'conclusion': 'failure'}, {'conclusion': 'cancelled'}, {'conclusion': 'success'},
                        {'status': 'in_progress'}, {'event': 'push'}]:
            actual = {**metadata, **changes}
            with patch('ci_native_evidence.pages') as pages:
                self.assertEqual(evidence.latest_source_run('owner/repo', WORKFLOW, [good, actual]), actual)
            pages.assert_not_called()
        jobs = self.metadata_jobs()
        for uncertain in [[], jobs[1:], [*jobs, {'name': 'Test on macOS (1.26)', 'status': 'completed', 'conclusion': 'skipped'}],
                          [dict(jobs[0], conclusion='failure'), *jobs[1:]],
                          [dict(jobs[0], status='queued'), *jobs[1:]]]:
            with patch('ci_native_evidence.pages', return_value=uncertain):
                self.assertEqual(evidence.latest_source_run('owner/repo', WORKFLOW, [good, metadata]), metadata)

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
