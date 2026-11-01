#!/usr/bin/env python3
"""Bind basic release admission to current source CI and immutable full receipts."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tools'))
import ci_native_evidence as native  # noqa: E402

REPOSITORIES = {'777genius/agent-notifications', 'universal-agent-plugins/agent-notifications'}
BAD = {'failure', 'cancelled', 'timed_out'}


def require(ok, reason):
    if not ok:
        raise ValueError(reason)


def source_runs(repository, source):
    runs = native.pages('repos/' + repository + '/actions/runs?head_sha=' + source, 'workflow_runs')
    require(all(run.get('head_sha') == source and type(run.get('id')) is int for run in runs)
            and len({run['id'] for run in runs}) == len(runs), 'ambiguous source run census')
    return runs


def checks(repository, source):
    rows = native.pages('repos/' + repository + '/commits/' + source + '/check-runs?filter=all', 'check_runs')
    result = []
    for row in rows:
        app, suite = row.get('app') or {}, row.get('check_suite') or {}
        require(type(row.get('id')) is int and row['id'] > 0 and row.get('head_sha') == source
                and isinstance(row.get('name'), str) and row['name']
                and type(app.get('id')) is int and type(suite.get('id')) is int,
                'invalid source check identity')
        result.append({'id': row['id'], 'name': row['name'], 'status': row.get('status'),
                       'conclusion': row.get('conclusion'), 'app_id': app['id'],
                       'app_slug': app.get('slug'), 'suite_id': suite['id']})
    require(result and len({row['id'] for row in result}) == len(result), 'ambiguous source check census')
    return sorted(result, key=lambda row: row['id'])


def select_run(repository, workflow, runs):
    return native.latest_source_run(repository, workflow,
                                    [run for run in runs if run.get('path') == '.github/workflows/' + workflow])


def identity(run):
    return {key: run.get(key) for key in ('id', 'run_attempt', 'head_sha', 'check_suite_id',
                                         'path', 'event', 'status', 'conclusion')}


def unresolved(name):
    # Actions can cancel before matrix/name expressions have been expanded.
    # Syntax alone never exempts a check: its exact unexecuted job and an
    # independently verified newer full run are required below.
    return '${{' in name or name.startswith('github.event.') and ' && ' in name and ' || ' in name


def obsolete_placeholder(check, runs, selected, repository, source, old_jobs):
    if check['status'] != 'completed' or check['conclusion'] != 'cancelled' or not unresolved(check['name']):
        return None
    if check['app_slug'] != 'github-actions':
        return None
    origins = [run for run in runs if run.get('check_suite_id') == check['suite_id']]
    if len(origins) != 1:
        return None
    origin = origins[0]
    workflow = origin.get('path', '').removeprefix('.github/workflows/')
    replacement = selected.get(workflow)
    if (replacement is None or origin['id'] >= replacement['run']['id']
            or origin.get('head_sha') != source or origin.get('event') != 'pull_request'
            or origin.get('status') != 'completed' or origin.get('conclusion') != 'cancelled'):
        return None
    path = 'repos/' + repository + '/actions/runs/' + str(origin['id'])
    if origin['id'] not in old_jobs:
        current = native.gh(path)
        require(identity(current) == identity(origin)
                and (current.get('head_repository') or {}).get('full_name') == repository,
                'superseded source run changed')
        old_jobs[origin['id']] = native.pages(path + '/jobs?filter=all', 'jobs')
    jobs = [job for job in old_jobs[origin['id']] if job.get('id') == check['id']]
    if (len(jobs) != 1 or jobs[0].get('name') != check['name']
            or jobs[0].get('status') != 'completed' or jobs[0].get('conclusion') != 'cancelled'
            or jobs[0].get('run_attempt') != origin.get('run_attempt')
            or jobs[0].get('steps') != []):
        return None
    return {'check_id': check['id'], 'name': check['name'], 'source_run': identity(origin),
            'replacement_run_id': replacement['run']['id'],
            'replacement_run_attempt': replacement['run']['run_attempt']}


def verify(source, repository):
    require(repository in REPOSITORIES and re.fullmatch(r'[a-f0-9]{40}', source), 'invalid release source')
    require(native.git('rev-parse', 'HEAD').decode().strip() == source, 'candidate checkout differs from source')
    before = checks(repository, source)
    runs = source_runs(repository, source)
    event = {'repository': {'full_name': repository}}
    selected = {}
    for workflow in native.CONTRACTS:
        run = select_run(repository, workflow, runs)
        require(run is not None and run.get('status') == 'completed' and run.get('conclusion') == 'success',
                'latest source workflow did not succeed: ' + workflow)
        # Canonical verifier checks concrete expected jobs, full receipt/run
        # identity, authenticated artifact digest and tested checkout tree.
        proof = native.verify_run(workflow, source, event, run)
        require(proof.get('run_id') == run['id'] and proof.get('run_attempt') == run.get('run_attempt'),
                'source attempt changed before verification')
        selected[workflow] = {'run': identity(run), 'receipt': proof}
        if workflow == 'ci-macos.yml':
            # Preserve the basic operator's explicit full-macOS gate in
            # addition to the canonical test/Swift receipt dependencies.
            jobs = native.pages('repos/' + repository + '/actions/runs/' + str(run['id'])
                                + '/jobs?filter=all', 'jobs')
            guards = [job for job in jobs if job.get('name') == 'Full macOS CI required']
            require(guards and all(type(job.get('id')) is int for job in guards),
                    'full macOS gate missing')
            guard = max(guards, key=lambda job: job['id'])
            require(guard.get('status') == 'completed' and guard.get('conclusion') == 'success',
                    'full macOS gate did not succeed')
            selected[workflow]['full_gate_job_id'] = guard['id']
    latest = {}
    for check in before:
        key = (check['app_id'], check['name'])
        if key not in latest or latest[key]['id'] < check['id']:
            latest[key] = check
    ignored, old_jobs = [], {}
    for check in latest.values():
        if check['conclusion'] not in BAD:
            continue
        exception = obsolete_placeholder(check, runs, selected, repository, source, old_jobs)
        require(exception is not None, 'latest source check did not succeed: ' + check['name'])
        ignored.append(exception)
    # No new push/run/attempt/check result may replace the census used above.
    final_runs = source_runs(repository, source)
    for workflow, value in selected.items():
        final = select_run(repository, workflow, final_runs)
        require(final is not None and identity(final) == value['run'],
                'latest source workflow changed during admission: ' + workflow)
    for run_id in old_jobs:
        initial = next(run for run in runs if run['id'] == run_id)
        require(identity(native.gh('repos/' + repository + '/actions/runs/' + str(run_id))) == identity(initial),
                'superseded source attempt changed during admission')
    require(checks(repository, source) == before, 'source check census changed during admission')
    require(native.git('rev-parse', 'HEAD').decode().strip() == source, 'candidate checkout changed during admission')
    return {'schema_version': 1, 'status': 'passed', 'repository': repository, 'source_sha': source,
            'selected_workflows': selected, 'ignored_superseded_placeholders': ignored,
            'check_census': before,
            'check_census_sha256': hashlib.sha256(json.dumps(before, sort_keys=True).encode()).hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--source-root', type=Path, default=Path.cwd())
    parser.add_argument('--report', required=True, type=Path)
    args = parser.parse_args()
    report = args.report.resolve()
    try:
        os.chdir(args.source_root)
        proof = verify(args.source_sha, os.environ.get('GITHUB_REPOSITORY', ''))
        # Preserve the exact selected input evidence without overwriting a
        # prior invocation's receipt. This helper never re-runs a CI workload.
        with report.open('x') as output:
            output.write(json.dumps(proof, sort_keys=True, indent=2) + '\n')
        print('Verified current source CI: ' + args.source_sha)
        return 0
    except (OSError, ValueError, KeyError, TypeError, native.subprocess.SubprocessError,
            native.zipfile.BadZipFile) as error:
        print('Basic release CI unavailable: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
