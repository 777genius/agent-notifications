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
    def __init__(self, selected=None):
        self.runs, self.jobs, self.artifacts, self.archives, self.checks = [], {}, {}, {}, []
        self.requests = []
        self.after_checks = None
        self.check_reads = 0
        for index, (workflow, contract) in enumerate(native.CONTRACTS.items(), 10):
            run = self.run(index, workflow)
            if selected:
                run.update(selected[workflow])
            run_id = run['id']
            self.runs.append(run)
            jobs = []
            names = (*contract[2], contract[1], *(['Full macOS CI required'] if workflow == 'ci-macos.yml' else []))
            for number, name in enumerate(names, index * 100):
                job = {'id': number, 'name': name, 'status': 'completed', 'conclusion': 'success',
                       'run_attempt': 1, 'steps': [{'name': 'actual fixture job', 'conclusion': 'success'}]}
                jobs.append(job)
                self.checks.append(self.check(job, run))
            self.jobs[run_id] = jobs
            proof = {'schema': 1, 'mode': 'full', 'repository': REPO, 'workflow': workflow,
                     'run_id': run_id, 'run_attempt': 1, 'source_head': SOURCE,
                     'tested_commit': SOURCE, 'tested_tree': TREE, 'jobs': list(contract[0])}
            self.set_proof(run_id, workflow, proof)

    @staticmethod
    def run(number, workflow, conclusion='success'):
        return {'id': number, 'run_attempt': 1, 'head_sha': SOURCE, 'check_suite_id': number + 1000,
                'path': '.github/workflows/' + workflow, 'event': 'push', 'head_branch': 'main',
                'status': 'completed', 'conclusion': conclusion, 'head_repository': {'full_name': REPO}}

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
        run.update(event='pull_request', head_branch='test/previous')
        self.runs.append(run)
        job = {'id': 1, 'name': name, 'status': 'completed', 'conclusion': 'cancelled', 'steps': [], 'run_attempt': 1}
        self.jobs[1] = [job]
        self.checks.append(self.check(job, run))
        return run, job, self.checks[-1]

    def conditional(self, workflow='ci-windows.yml', name='Release tools scoped contract (Windows)',
                    origin_id=1, check_id=1, latest_id=10000, origin_suite=None):
        origin = next((run for run in self.runs if run['id'] == origin_id), None)
        if origin is None:
            origin = self.run(origin_id, workflow, 'cancelled')
            origin.update(event='pull_request', head_branch='test/previous')
            if origin_suite is not None:
                origin['check_suite_id'] = origin_suite
            self.runs.append(origin)
            self.jobs[origin_id] = []
        selected = next(run for run in self.runs if run['path'] == '.github/workflows/' + workflow
                        and run['conclusion'] == 'success')
        jobs, rows = [], []
        for run, number, conclusion in ((origin, check_id, 'cancelled'), (selected, latest_id, 'skipped')):
            job = {'id': number, 'name': name, 'status': 'completed', 'conclusion': conclusion,
                   'steps': [], 'run_attempt': run['run_attempt']}
            self.jobs[run['id']].append(job)
            row = self.check(job, run)
            self.checks.append(row)
            jobs.append(job)
            rows.append(row)
        return origin, jobs[0], rows[0], selected, jobs[1], rows[1]

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
        for conclusion in ('failure', 'cancelled', 'timed_out', 'action_required', 'startup_failure', 'stale'):
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

    def test_non_completed_latest_source_check_blocks_admission(self):
        for status in ('queued', 'in_progress'):
            with self.subTest(status=status):
                self.api = API()
                native.gh.side_effect = self.api
                row = dict(self.api.checks[0], id=10000, name='Unfinished additional qualification',
                           status=status, conclusion=None)
                self.api.checks.append(row)
                with self.assertRaisesRegex(ValueError, 'latest source check did not succeed'):
                    self.verify()

    def executing_release(self):
        run = API.run(999, 'basic-release.yml')
        run.update(event='workflow_dispatch', status='in_progress', conclusion=None,
                   head_branch='main', actor={'login': '777genius'},
                   triggering_actor={'login': '777genius'})
        self.api.runs.append(run)
        job = {'id': 99900, 'name': 'Verify owner and exact basic candidate',
               'status': 'in_progress', 'conclusion': None, 'run_attempt': 1}
        self.api.jobs[999] = [job]
        self.api.checks.append(self.api.check(job, run))
        patch.dict(os.environ, {'GITHUB_EVENT_NAME': 'workflow_dispatch',
                               'GITHUB_REF': 'refs/heads/main', 'GITHUB_JOB': 'verify-version',
                               'GITHUB_SHA': SOURCE, 'GITHUB_RUN_ID': '999',
                               'GITHUB_RUN_ATTEMPT': '1'}).start()
        return run, job

    def test_exact_current_release_run_is_exempt_but_other_checks_are_not(self):
        run, job = self.executing_release()
        self.assertEqual(self.verify()['status'], 'passed')
        # Check-suite exclusion happens before choosing the latest per name:
        # an external failed Actions job of the same name must not be masked.
        row = self.api.check(job, run)
        row.update(id=99899, conclusion='failure', status='completed',
                   check_suite={'id': self.api.runs[0]['check_suite_id']})
        self.api.checks.append(row)
        with self.assertRaisesRegex(ValueError, 'latest source check did not succeed'):
            self.verify()

    def test_release_run_exemption_requires_exact_owner_identity(self):
        for altered, value in [('event', 'pull_request'), ('head_branch', 'other'),
                               ('run_attempt', 2), ('actor', {'login': 'other'})]:
            with self.subTest(altered=altered):
                self.api = API()
                native.gh.side_effect = self.api
                run, _ = self.executing_release()
                run[altered] = value
                with self.assertRaisesRegex(ValueError, 'executing basic release run'):
                    self.verify()

    def test_optional_skipped_neutral_are_allowed_but_cannot_mask_failures(self):
        for conclusion in ('skipped', 'neutral'):
            with self.subTest(conclusion=conclusion):
                self.api = API()
                native.gh.side_effect = self.api
                row = dict(self.api.checks[0], id=10000, name='Optional native gate',
                           status='completed', conclusion=conclusion)
                self.api.checks.append(row)
                self.assertEqual(self.verify()['status'], 'passed')
                self.api.checks.append(dict(row, id=9999, conclusion='failure'))
                with self.assertRaisesRegex(ValueError, 'masked a failure'):
                    self.verify()

    def test_selected_conditional_skip_supersedes_only_authenticated_unexecuted_cancellation(self):
        origin, _, older, selected, _, latest = self.api.conditional()
        result = self.verify()
        ignored = result['ignored_superseded_conditionals']
        self.assertEqual(len(ignored), 1)
        self.assertEqual(ignored[0]['check_id'], older['id'])
        self.assertEqual(ignored[0]['source_run']['id'], origin['id'])
        self.assertEqual(ignored[0]['replacement_run_id'], selected['id'])
        self.assertEqual(ignored[0]['replacement_check_id'], latest['id'])
        self.assertEqual(result['ignored_superseded_placeholders'], [])

    def test_real_c1_conditional_census_shape_is_accepted(self):
        # Exact seven formerly admitted check pairs from source0233's authentic
        # census. Only SOURCE/tree/artifact contents are normalized by this API.
        selected = {
            'ci-macos.yml': {'id': 41299717757, 'check_suite_id': 111935138271},
            'ci-windows.yml': {'id': 41299717729, 'check_suite_id': 111935138192},
            'ci-ubuntu.yml': {'id': 41299717707, 'check_suite_id': 111935138134},
            'install-recovery-e2e.yml': {'id': 41299717702, 'check_suite_id': 111935138135},
        }
        self.api = API(selected)
        native.gh.side_effect = self.api
        pairs = [
            ('ci-windows.yml', 'Release tools scoped contract (Windows)', 41299680166, 111935041274, 124827960334, 124829193657),
            ('ci-macos.yml', 'Release-equivalent native proof (macOS)', 41299680283, 111935041565, 124827960712, 124829289001),
            ('install-recovery-e2e.yml', 'Release-equivalent native proof (recovery)', 41299680117, 111935041170, 124827960728, 124828393731),
            ('ci-ubuntu.yml', 'Release tools scoped contract (Ubuntu)', 41299680195, 111935041373, 124827960848, 124828273899),
            ('ci-windows.yml', 'Release-equivalent native proof (Windows)', 41299680166, 111935041274, 124827960863, 124829193687),
            ('ci-ubuntu.yml', 'Reused native qualification (Ubuntu)', 41299680195, 111935041373, 124827960867, 124828273797),
            ('ci-macos.yml', "github.event.action == 'edited' && github.event.changes.base == null && (github.event.changes.title != null || github.event.changes.body != null) && 'Metadata edit ignored (release tools)' || 'Release tools scoped contract (macOS)'", 41299680283, 111935041565, 124827960878, 124829289142),
        ]
        for workflow, name, run_id, suite, old_id, latest_id in pairs:
            self.api.conditional(workflow, name, run_id, old_id, latest_id, suite)
        for row in self.api.checks:
            row['app']['id'] = 15368
        ignored = self.verify()['ignored_superseded_conditionals']
        self.assertEqual({row['check_id'] for row in ignored}, {row[4] for row in pairs})
        self.assertTrue(all(row['replacement_run_id'] == selected[row['source_run']['path'].split('/')[-1]]['id']
                            for row in ignored))

    def test_conditional_history_preserves_failure_pending_and_custody_guards(self):
        mutations = [
            ('older-check', 'conclusion', value) for value in
            ('failure', 'timed_out', 'stale', 'action_required', 'startup_failure')]
        mutations += [('older-check', 'status', value) for value in ('queued', 'in_progress')]
        mutations += [('older-job', 'steps', [{'name': 'actually executed'}]), ('older-job', 'run_attempt', 2),
                      ('older-job', 'id', 2), ('older-job', 'name', 'different job'),
                      ('older-check', 'app', {'id': 2, 'slug': 'foreign-app'}),
                      ('older-check', 'app', {'id': 1, 'slug': 'foreign-app'}),
                      ('origin', 'head_repository', {}), ('origin', 'event', 'push'),
                      ('origin', 'head_sha', 'c' * 40), ('origin', 'status', 'in_progress'),
                      ('origin', 'conclusion', 'failure'),
                      ('latest-check', 'conclusion', 'neutral'), ('latest-check', 'check_suite', {'id': 99999}),
                      ('latest-check', 'app', {'id': 2, 'slug': 'foreign-app'}),
                      ('latest-check', 'app', {'id': 1, 'slug': 'foreign-app'}),
                      ('latest-check', 'status', 'queued'), ('latest-check', 'status', 'in_progress'),
                      ('latest-job', 'steps', [{'name': 'actually executed'}]), ('latest-job', 'run_attempt', 2),
                      ('latest-job', 'id', 20000), ('latest-job', 'name', 'different job'),
                      ('latest-job', 'status', 'in_progress'), ('latest-job', 'conclusion', 'success')]
        for target, field, value in mutations:
            with self.subTest(target=target, field=field, value=value):
                self.api = API()
                native.gh.side_effect = self.api
                origin, old_job, older, _, latest_job, latest = self.api.conditional()
                {'origin': origin, 'older-job': old_job, 'older-check': older,
                 'latest-job': latest_job, 'latest-check': latest}[target][field] = value
                with self.assertRaises(ValueError):
                    self.verify()

    def test_conditional_cancellation_does_not_change_latest_placeholder_exception(self):
        self.api.placeholder(name='Real conditional native qualification')
        with self.assertRaisesRegex(ValueError, 'latest source check'):
            self.verify()

    def test_conditional_history_excludes_only_authenticated_operator_suite(self):
        operator, job = self.executing_release()
        origin, _, _, _, _, _ = self.api.conditional()
        job.update(id=9999, name='Release tools scoped contract (Windows)')
        self.api.checks[-3] = self.api.check(job, operator)
        self.assertEqual(self.verify()['status'], 'passed')

        failure = dict(job, id=9998, status='completed', conclusion='failure')
        self.api.checks.append(self.api.check(failure, origin))
        with self.assertRaisesRegex(ValueError, 'masked a failure'):
            self.verify()

    def test_superseded_conditional_origin_is_revalidated_after_admission(self):
        origin, _, _, _, _, _ = self.api.conditional()
        request = 'repos/' + REPO + '/actions/runs/' + str(origin['id'])
        reads = 0

        def gh(path, binary=False):
            nonlocal reads
            if path == request:
                reads += 1
                if reads == 2:
                    origin['run_attempt'] = 2
            return self.api(path, binary)

        native.gh.side_effect = gh
        with self.assertRaisesRegex(ValueError, 'superseded source attempt changed during admission'):
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

    def test_only_owner_main_push_runs_qualify_release_source(self):
        for mutation in ({'event': 'pull_request'}, {'head_branch': 'feature'},
                         {'head_repository': {'full_name': 'fork/repo'}}):
            with self.subTest(mutation=mutation):
                self.api = API()
                native.gh.side_effect = self.api
                self.api.runs[0].update(mutation)
                with self.assertRaisesRegex(ValueError, 'latest source workflow did not succeed'):
                    self.verify()

    def test_foreign_fork_pr_cannot_shadow_owned_main_source_and_check(self):
        run = API.run(999, 'ci-macos.yml', 'action_required')
        run.update(event='pull_request', head_repository={'full_name': 'fork/repo'})
        self.api.runs.append(run)
        job = {'id': 19999, 'name': self.api.checks[0]['name'],
               'status': 'completed', 'conclusion': 'action_required'}
        self.api.checks.append(self.api.check(job, run))
        result = self.verify()
        self.assertEqual([row['check_id'] for row in result['ignored_foreign_pull_checks']], [19999])
        run['head_repository'] = {'full_name': REPO}
        with self.assertRaisesRegex(ValueError, 'latest source check did not succeed'):
            self.verify()

    def test_identical_check_name_from_other_workflow_never_masks_failure(self):
        for status, conclusion in (('completed', 'failure'), ('in_progress', None)):
            with self.subTest(status=status):
                self.api = API()
                native.gh.side_effect = self.api
                first, later = API.run(201, 'release.yml', 'failure'), API.run(202, 'unified-release.yml')
                self.api.runs.extend((first, later))
                name = 'Build linux amd64'
                self.api.checks.append(self.api.check(
                    {'id': 20001, 'name': name, 'status': status, 'conclusion': conclusion}, first))
                self.api.checks.append(self.api.check(
                    {'id': 20002, 'name': name, 'status': 'completed', 'conclusion': 'success'}, later))
                with self.assertRaisesRegex(ValueError, 'latest successful source check masked a failure'):
                    self.verify()

    def test_completed_same_workflow_main_retry_is_proven_not_name_only(self):
        first, later = API.run(201, 'release.yml', 'failure'), API.run(202, 'release.yml')
        self.api.runs.extend((first, later))
        name = 'Build linux amd64'
        self.api.checks.extend((
            self.api.check({'id': 20001, 'name': name, 'status': 'completed',
                            'conclusion': 'failure'}, first),
            self.api.check({'id': 20002, 'name': name, 'status': 'completed',
                            'conclusion': 'success'}, later)))
        result = self.verify()
        self.assertEqual([row['check_id'] for row in result['ignored_successful_retries']], [20001])
        self.api.checks[-2]['conclusion'] = 'action_required'
        with self.assertRaisesRegex(ValueError, 'latest successful source check masked a failure'):
            self.verify()

    def test_same_run_retry_requires_actual_new_attempt(self):
        run = API.run(201, 'release.yml')
        run['run_attempt'] = 2
        self.api.runs.append(run)
        name = 'Build linux amd64'
        old = {'id': 20001, 'name': name, 'status': 'completed', 'conclusion': 'failure',
               'run_attempt': 1}
        new = {'id': 20002, 'name': name, 'status': 'completed', 'conclusion': 'success',
               'run_attempt': 2}
        self.api.jobs[201] = [old, new]
        self.api.checks.extend((self.api.check(old, run), self.api.check(new, run)))
        self.assertEqual(self.verify()['ignored_successful_retries'][0]['check_id'], 20001)
        old['run_attempt'] = 2
        with self.assertRaisesRegex(ValueError, 'latest successful source check masked a failure'):
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
