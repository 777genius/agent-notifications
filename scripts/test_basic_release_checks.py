#!/usr/bin/env python3
"""Mocked public API/custody checks; no Actions, build or agent execution."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit
import zipfile

SPEC = importlib.util.spec_from_file_location('basic_release_checks', Path(__file__).with_name('basic-release-checks.py'))
checks = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(checks)
native = checks.native
SOURCE, TREE = 'a' * 40, 'b' * 40
REPO = '777genius/agent-notifications'


class API:
    def __init__(self):
        self.runs, self.jobs, self.artifacts, self.archives, self.checks = [], {}, {}, {}, []
        self.requests = []
        self.after_checks = None
        self.check_reads = 0
        for index, (workflow, contract) in enumerate(native.CONTRACTS.items(), 10):
            run = self.run(index, workflow)
            self.runs.append(run)
            jobs = []
            names = (*contract[2], contract[1], *(['Full macOS CI required'] if workflow == 'ci-macos.yml' else []))
            for number, name in enumerate(names, index * 100):
                job = {'id': number, 'name': name, 'status': 'completed', 'conclusion': 'success',
                       'run_attempt': 1, 'steps': [{'name': 'actual fixture job', 'conclusion': 'success'}]}
                jobs.append(job)
                self.checks.append(self.check(job, run))
            self.jobs[index] = jobs
            proof = {'schema': 1, 'mode': 'full', 'repository': REPO, 'workflow': workflow,
                     'run_id': index, 'run_attempt': 1, 'source_head': SOURCE,
                     'tested_commit': SOURCE, 'tested_tree': TREE, 'jobs': list(contract[0])}
            self.set_proof(index, workflow, proof)

    @staticmethod
    def run(number, workflow, conclusion='success'):
        return {'id': number, 'run_attempt': 1, 'head_sha': SOURCE, 'check_suite_id': number + 1000,
                'path': '.github/workflows/' + workflow, 'event': 'pull_request', 'status': 'completed',
                'conclusion': conclusion, 'head_repository': {'full_name': REPO}}

    @staticmethod
    def check(job, run):
        return {**{key: job[key] for key in ('id', 'name', 'status', 'conclusion')}, 'head_sha': SOURCE,
                'app': {'id': 1, 'slug': 'github-actions'}, 'check_suite': {'id': run['check_suite_id']}}

    def set_proof(self, run_id, workflow, proof):
        stream = io.BytesIO()
        with zipfile.ZipFile(stream, 'w') as archive:
            archive.writestr('native-input-receipt.json', json.dumps(proof))
        body = stream.getvalue()
        self.archives[run_id] = body
        self.artifacts[run_id] = [{'id': run_id, 'name': native.artifact_name(workflow, run_id, 1),
                                   'expired': False, 'digest': 'sha256:' + hashlib.sha256(body).hexdigest()}]

    def placeholder(self, workflow='ci-ubuntu.yml', name='Ubuntu Go tests (${{ matrix.go }}, ${{ matrix.shard }})'):
        run = self.run(1, workflow, 'cancelled')
        self.runs.append(run)
        job = {'id': 1, 'name': name, 'status': 'completed', 'conclusion': 'cancelled', 'steps': [], 'run_attempt': 1}
        self.jobs[1] = [job]
        self.checks.append(self.check(job, run))
        return run, job, self.checks[-1]

    def __call__(self, request, binary=False):
        self.requests.append(request)
        path = urlsplit(request).path
        query = parse_qs(urlsplit(request).query)
        prefix = 'repos/' + REPO + '/'
        if path == prefix + 'actions/runs':
            key, rows = 'workflow_runs', self.runs
        elif path == prefix + 'commits/' + SOURCE + '/check-runs':
            key, rows = 'check_runs', self.checks
            if query.get('page') == ['1']:
                self.check_reads += 1
                if self.check_reads == 2 and self.after_checks:
                    self.after_checks(self)
        elif path.startswith(prefix + 'actions/runs/'):
            suffix = path.removeprefix(prefix + 'actions/runs/').split('/')
            run_id = int(suffix[0])
            if len(suffix) == 1:
                return copy.deepcopy(next(run for run in self.runs if run['id'] == run_id))
            if suffix[1] == 'jobs':
                key, rows = 'jobs', self.jobs[run_id]
            else:
                assert suffix[1] == 'artifacts', request
                key, rows = 'artifacts', self.artifacts[run_id]
        elif path.startswith(prefix + 'actions/artifacts/'):
            assert binary, request
            return self.archives[int(path.split('/')[-2])]
        else:
            raise AssertionError(request)
        page = int(query.get('page', ['1'])[0])
        return {key: copy.deepcopy(rows[(page - 1) * 100:page * 100])}


class AdmissionTests(unittest.TestCase):
    def setUp(self):
        self.api = API()
        self.head, self.tree, self.equivalent = SOURCE, TREE, True
        self.addCleanup(patch.stopall)
        patch.dict(os.environ, GITHUB_REPOSITORY=REPO).start()
        patch.object(native, 'gh', side_effect=self.api).start()
        patch.object(native, 'git', side_effect=self.git).start()
        patch.object(native, 'equivalent_tree', side_effect=lambda *_: self.equivalent).start()

    def git(self, *args):
        if args == ('rev-parse', 'HEAD'):
            return self.head.encode()
        if args == ('cat-file', '-e', SOURCE + '^{commit}'):
            return b''
        if args == ('rev-parse', SOURCE + '^{tree}'):
            return self.tree.encode()
        raise AssertionError(args)

    def verify(self):
        return checks.verify(SOURCE, REPO)

    def test_full_native_receipts_and_exact_selected_attempts_are_preserved(self):
        result = self.verify()
        self.assertEqual(result['status'], 'passed')
        self.assertEqual(set(result['selected_workflows']), set(native.CONTRACTS))
        for workflow, selection in result['selected_workflows'].items():
            self.assertEqual(selection['run']['run_attempt'], 1)
            self.assertEqual(selection['receipt']['workflow'], workflow)
            self.assertEqual(selection['receipt']['tested_tree'], TREE)

    def test_pagination_accepts_more_than_one_hundred_checks(self):
        template = self.api.checks[0]
        self.api.checks.extend({**template, 'id': 10000 + index, 'name': 'unrelated success ' + str(index)}
                               for index in range(130))
        self.assertGreater(len(self.verify()['check_census']), 100)
        self.assertTrue(any('/check-runs?' in path and 'page=2' in path for path in self.api.requests))

    def test_only_exact_unexecuted_superseded_placeholders_are_ignored(self):
        for workflow, name in [('ci-ubuntu.yml', 'Ubuntu Go tests (${{ matrix.go }}, ${{ matrix.shard }})'),
                               ('ci-macos.yml', "github.event.action == 'edited' && false || 'Swift notifier tests'")]:
            with self.subTest(workflow=workflow):
                self.api = API()
                native.gh.side_effect = self.api
                self.api.placeholder(workflow, name)
                ignored = self.verify()['ignored_superseded_placeholders']
                self.assertEqual(len(ignored), 1)
                self.assertEqual(ignored[0]['source_run']['id'], 1)
                self.assertGreater(ignored[0]['replacement_run_id'], 1)

    def test_placeholder_requires_exact_actions_job_attempt_without_steps(self):
        changes = [('steps', [{'name': 'executed'}]), ('run_attempt', 2), ('id', 2)]
        for key, value in changes:
            with self.subTest(key=key):
                self.api = API()
                native.gh.side_effect = self.api
                _, job, _ = self.api.placeholder()
                job[key] = value
                with self.assertRaisesRegex(ValueError, 'latest source check'):
                    self.verify()

    def test_real_failure_cancellation_timeout_and_foreign_app_stay_blocking(self):
        for conclusion in ('failure', 'cancelled', 'timed_out', 'action_required'):
            with self.subTest(conclusion=conclusion):
                self.api = API()
                native.gh.side_effect = self.api
                _, _, row = self.api.placeholder(name='Real custom native qualification')
                row['conclusion'] = conclusion
                with self.assertRaisesRegex(ValueError, 'latest source check'):
                    self.verify()
        self.api = API()
        native.gh.side_effect = self.api
        _, _, row = self.api.placeholder()
        row['app'] = {'id': 2, 'slug': 'other-app'}
        with self.assertRaisesRegex(ValueError, 'latest source check'):
            self.verify()

    def test_latest_source_workflow_failure_cannot_inherit_prior_green(self):
        for conclusion in ('failure', 'cancelled', 'timed_out'):
            with self.subTest(conclusion=conclusion):
                self.api = API()
                native.gh.side_effect = self.api
                self.api.runs.append(API.run(999, 'ci-ubuntu.yml', conclusion))
                with self.assertRaisesRegex(ValueError, 'latest source workflow did not succeed'):
                    self.verify()

    def test_full_macos_guard_remains_required_beyond_native_receipt(self):
        self.api.jobs[10] = [job for job in self.api.jobs[10] if job['name'] != 'Full macOS CI required']
        with self.assertRaisesRegex(ValueError, 'full macOS gate missing'):
            self.verify()

    def test_other_app_cannot_mask_failed_actions_check_with_same_name(self):
        row = dict(self.api.checks[0], id=10000, name='Additional qualification', conclusion='failure')
        self.api.checks.append(row)
        self.api.checks.append({**row, 'id': 10001, 'conclusion': 'success', 'app': {'id': 2, 'slug': 'other-app'}})
        with self.assertRaisesRegex(ValueError, 'latest source check'):
            self.verify()

    def test_missing_concrete_job_receipt_and_bad_custody_are_rejected(self):
        for mutation in ('missing-job', 'skipped-job', 'missing-receipt', 'digest', 'source', 'attempt', 'mode'):
            with self.subTest(mutation=mutation):
                self.api = API()
                native.gh.side_effect = self.api
                if mutation == 'missing-job':
                    self.api.jobs[10].pop()
                elif mutation == 'skipped-job':
                    self.api.jobs[10][0]['conclusion'] = 'skipped'
                elif mutation == 'missing-receipt':
                    self.api.artifacts[10] = []
                elif mutation == 'digest':
                    self.api.artifacts[10][0]['digest'] = 'sha256:' + '0' * 64
                else:
                    with zipfile.ZipFile(io.BytesIO(self.api.archives[10])) as archive:
                        proof = json.loads(archive.read('native-input-receipt.json'))
                    proof[{'source': 'source_head', 'attempt': 'run_attempt', 'mode': 'mode'}[mutation]] = 'wrong'
                    self.api.set_proof(10, 'ci-macos.yml', proof)
                with self.assertRaises(ValueError):
                    self.verify()

    def test_wrong_checkout_or_receipt_tree_cannot_qualify(self):
        self.head = 'c' * 40
        with self.assertRaisesRegex(ValueError, 'candidate checkout differs'):
            self.verify()
        self.head, self.tree = SOURCE, 'd' * 40
        with self.assertRaisesRegex(ValueError, 'tested tree mismatch'):
            self.verify()
        self.tree, self.equivalent = TREE, False
        with self.assertRaisesRegex(ValueError, 'tested merge checkout differs'):
            self.verify()

    def test_changed_final_census_run_or_attempt_is_rejected(self):
        for mutation in ('check', 'new-run', 'attempt'):
            with self.subTest(mutation=mutation):
                self.api = API()
                native.gh.side_effect = self.api
                # Alter the second source-run census, after canonical receipts.
                original = self.api.__call__
                run_reads = 0
                def gh(path, binary=False):
                    nonlocal run_reads
                    if '/actions/runs?head_sha=' in path and 'page=1' in path:
                        run_reads += 1
                        if run_reads == 2:
                            if mutation == 'new-run':
                                self.api.runs.append(API.run(999, 'ci-ubuntu.yml'))
                            elif mutation == 'attempt':
                                self.api.runs[0]['run_attempt'] = 2
                            else:
                                self.api.checks[0]['conclusion'] = 'failure'
                    return original(path, binary)
                native.gh.side_effect = gh
                with self.assertRaisesRegex(ValueError, 'changed during admission'):
                    self.verify()


if __name__ == '__main__':
    unittest.main()
