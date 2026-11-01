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
NON_BLOCKING = {'success', 'skipped', 'neutral'}


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


def authentic_candidate_pull(repository, source, run):
    # The candidate's PR must have run against a base already contained in C.
    # Otherwise GitHub executes a synthetic merge containing unreviewed base
    # workflow code, which can manufacture its own "native receipt" artifact.
    pulls = run.get('pull_requests')
    if run.get('event') != 'pull_request' or not isinstance(pulls, list) or len(pulls) != 1:
        return False
    pr = pulls[0]
    base, head = pr.get('base') or {}, pr.get('head') or {}
    repo_id = (run.get('head_repository') or {}).get('id')
    base_sha = base.get('sha')
    if (type(repo_id) is not int or type(pr.get('number')) is not int
            or (base.get('repo') or {}).get('id') != repo_id
            or (head.get('repo') or {}).get('id') != repo_id
            or head.get('sha') != source or run.get('head_sha') != source
            or base.get('ref') != 'main' or not isinstance(base_sha, str)
            or not re.fullmatch(r'[a-f0-9]{40}', base_sha)):
        return False
    # GitHub's own merge-base, not an artifact-authored tested_commit, proves
    # the executed PR merge cannot add a workflow absent from the candidate.
    ancestry = native.gh('repos/' + repository + '/compare/' + base_sha + '...' + source + '?per_page=1')
    return (ancestry.get('status') in ('ahead', 'identical')
            and (ancestry.get('base_commit') or {}).get('sha') == base_sha
            and (ancestry.get('merge_base_commit') or {}).get('sha') == base_sha)


def select_run(repository, workflow, runs, source):
    owned = [run for run in runs
             if run.get('path') == '.github/workflows/' + workflow
             and (run.get('head_repository') or {}).get('full_name') == repository]
    # A main-push source is always authoritative, even if its CI failed.
    main = [run for run in owned
            if run.get('event') == 'push' and run.get('head_branch') == 'main']
    if main:
        return native.latest_source_run(repository, workflow, main)
    # Pre-main assets may use only a same-repo PR with an authenticated
    # ancestor main base; full native jobs/receipts are verified separately.
    return native.latest_source_run(repository, workflow,
                                    [run for run in owned
                                     if authentic_candidate_pull(repository, source, run)])


def foreign_pull_check(check, runs, repository):
    # A fork PR can reuse a candidate SHA but cannot qualify or veto main.
    # Ignore only the Actions check in its uniquely identified foreign suite.
    if check['app_slug'] != 'github-actions':
        return None
    origins = [run for run in runs if run.get('check_suite_id') == check['suite_id']]
    if len(origins) != 1:
        return None
    run = origins[0]
    owner = (run.get('head_repository') or {}).get('full_name')
    if run.get('event') != 'pull_request' or not owner or owner == repository:
        return None
    return {'check_id': check['id'], 'source_run_id': run['id'], 'foreign_head_repository': owner}


def identity(run):
    value = {key: run.get(key) for key in ('id', 'run_attempt', 'head_sha', 'check_suite_id',
                                           'path', 'event', 'status', 'conclusion')}
    # Freeze the GitHub PR association that authorizes pre-main admission.
    value['head_repo_id'] = (run.get('head_repository') or {}).get('id')
    value['pull_refs'] = [{
        'number': pr.get('number'),
        'base_sha': (pr.get('base') or {}).get('sha'),
        'base_ref': (pr.get('base') or {}).get('ref'),
        'base_repo_id': ((pr.get('base') or {}).get('repo') or {}).get('id'),
        'head_sha': (pr.get('head') or {}).get('sha'),
        'head_repo_id': ((pr.get('head') or {}).get('repo') or {}).get('id')
    } for pr in (run.get('pull_requests') or [])]
    return value


def unresolved(name):
    # Actions can cancel before matrix/name expressions have been expanded.
    # Syntax alone never exempts a check: its exact unexecuted job and an
    # independently verified newer full run are required below.
    return '${{' in name or name.startswith('github.event.') and ' && ' in name and ' || ' in name


def superseded_unexecuted(check, runs, selected, repository, source, old_jobs):
    if check['status'] != 'completed' or check['conclusion'] != 'cancelled':
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


def obsolete_placeholder(check, runs, selected, repository, source, old_jobs):
    if not unresolved(check['name']):
        return None
    return superseded_unexecuted(check, runs, selected, repository, source, old_jobs)


def superseded_conditional(check, latest, runs, selected, repository, source, old_jobs, selected_jobs):
    if (latest['status'] != 'completed' or latest['conclusion'] != 'skipped'
            or latest['app_slug'] != 'github-actions'
            or check['app_id'] != latest['app_id'] or check['name'] != latest['name']):
        return None
    proof = superseded_unexecuted(check, runs, selected, repository, source, old_jobs)
    if proof is None:
        return None
    workflow = proof['source_run']['path'].removeprefix('.github/workflows/')
    replacement = selected[workflow]['run']
    if latest['suite_id'] != replacement['check_suite_id'] or check['id'] >= latest['id']:
        return None
    if replacement['id'] not in selected_jobs:
        selected_jobs[replacement['id']] = native.pages('repos/' + repository + '/actions/runs/'
                                                       + str(replacement['id']) + '/jobs?filter=all', 'jobs')
    jobs = [job for job in selected_jobs[replacement['id']] if job.get('id') == latest['id']]
    if (len(jobs) != 1 or jobs[0].get('name') != latest['name']
            or jobs[0].get('status') != 'completed' or jobs[0].get('conclusion') != 'skipped'
            or jobs[0].get('run_attempt') != replacement['run_attempt'] or jobs[0].get('steps') != []):
        return None
    return {**proof, 'replacement_check_id': latest['id'], 'replacement_check_conclusion': 'skipped'}


def superseded_successful_retry(older, latest, runs, repository, source, job_cache):
    # A green same-named check cannot retire a failure from another workflow,
    # an unfinished job, or an action_required/stale/startup_failure result.
    if (older['status'] != 'completed' or older['conclusion'] not in ('failure', 'cancelled', 'timed_out')
            or older['app_slug'] != 'github-actions' or latest['app_slug'] != 'github-actions'):
        return None
    origins = [run for run in runs if run.get('check_suite_id') == older['suite_id']]
    replacements = [run for run in runs if run.get('check_suite_id') == latest['suite_id']]
    if len(origins) != 1 or len(replacements) != 1:
        return None
    origin, replacement = origins[0], replacements[0]
    # Before main promotion a verified same-repository PR can also supersede
    # a prior failed PR attempt. Never use a PR retry to overrule an existing
    # main-push authority for this workflow and source.
    main_retry = replacement.get('event') == 'push' and replacement.get('head_branch') == 'main'
    pre_main_retry = (authentic_candidate_pull(repository, source, replacement)
                      and not any(run.get('path') == replacement.get('path')
                                  and run.get('head_sha') == source and run.get('event') == 'push'
                                  and run.get('head_branch') == 'main'
                                  and (run.get('head_repository') or {}).get('full_name') == repository
                                  for run in runs))
    if (origin.get('path') != replacement.get('path')
            or not isinstance(replacement.get('path'), str)
            or not replacement['path'].startswith('.github/workflows/')
            or origin.get('head_sha') != source or replacement.get('head_sha') != source
            or (origin.get('head_repository') or {}).get('full_name') != repository
            or (replacement.get('head_repository') or {}).get('full_name') != repository
            or origin.get('event') not in ('push', 'pull_request')
            or not (main_retry or pre_main_retry)
            or origin.get('status') != 'completed'
            or replacement.get('status') != 'completed' or replacement.get('conclusion') != 'success'
            or origin['id'] > replacement['id']):
        return None
    if origin['id'] == replacement['id']:
        # Another same-name matrix job in the same attempt is not a retry.
        run_id = origin['id']
        if run_id not in job_cache:
            job_cache[run_id] = native.pages('repos/' + repository + '/actions/runs/'
                                            + str(run_id) + '/jobs?filter=all', 'jobs')
        old = [job for job in job_cache[run_id] if job.get('id') == older['id']]
        new = [job for job in job_cache[run_id] if job.get('id') == latest['id']]
        if (len(old) != 1 or len(new) != 1
                or old[0].get('name') != older['name'] or new[0].get('name') != latest['name']
                or old[0].get('status') != 'completed' or old[0].get('conclusion') != older['conclusion']
                or new[0].get('status') != 'completed' or new[0].get('conclusion') != 'success'
                or type(old[0].get('run_attempt')) is not int
                or type(replacement.get('run_attempt')) is not int
                or old[0]['run_attempt'] >= replacement['run_attempt']
                or new[0].get('run_attempt') != replacement['run_attempt']):
            return None
    return {'check_id': older['id'], 'replacement_check_id': latest['id'],
            'origin_run': identity(origin), 'replacement_run': identity(replacement)}


def executing_release_run(source, repository, runs):
    # Only the current owner-dispatched release operator may exclude its own
    # check suite. Other runs and foreign-app checks remain in the census.
    if (os.environ.get('GITHUB_EVENT_NAME') != 'workflow_dispatch'
            or os.environ.get('GITHUB_REF') != 'refs/heads/main'
            or os.environ.get('GITHUB_JOB') != 'verify-version'
            or os.environ.get('GITHUB_SHA') != source
            or os.environ.get('GITHUB_REPOSITORY') != repository):
        return None
    run_id, attempt = os.environ.get('GITHUB_RUN_ID', ''), os.environ.get('GITHUB_RUN_ATTEMPT', '')
    require(re.fullmatch(r'[1-9][0-9]*', run_id) and re.fullmatch(r'[1-9][0-9]*', attempt),
            'invalid executing basic release run identity')
    matching = [run for run in runs if run['id'] == int(run_id)]
    require(len(matching) == 1, 'executing basic release run missing from source census')
    run = matching[0]
    require(run.get('event') == 'workflow_dispatch'
            and run.get('path') == '.github/workflows/basic-release.yml'
            and run.get('head_sha') == source and run.get('head_branch') == 'main'
            and run.get('run_attempt') == int(attempt)
            and run.get('status') == 'in_progress' and run.get('conclusion') is None
            and type(run.get('check_suite_id')) is int
            and (run.get('actor') or {}).get('login') == '777genius'
            and (run.get('triggering_actor') or {}).get('login') == '777genius'
            and (run.get('head_repository') or {}).get('full_name') == repository,
            'executing basic release run is not the authenticated owner operator')
    return run

def verify(source, repository):
    require(repository in REPOSITORIES and re.fullmatch(r'[a-f0-9]{40}', source), 'invalid release source')
    require(native.git('rev-parse', 'HEAD').decode().strip() == source, 'candidate checkout differs from source')
    before = checks(repository, source)
    runs = source_runs(repository, source)
    operator_run = executing_release_run(source, repository, runs)
    event = {'repository': {'full_name': repository}}
    selected, selected_jobs = {}, {}
    for workflow in native.CONTRACTS:
        run = select_run(repository, workflow, runs, source)
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
            selected_jobs[run['id']] = jobs
            guards = [job for job in jobs if job.get('name') == 'Full macOS CI required']
            require(guards and all(type(job.get('id')) is int for job in guards),
                    'full macOS gate missing')
            guard = max(guards, key=lambda job: job['id'])
            require(guard.get('status') == 'completed' and guard.get('conclusion') == 'success',
                    'full macOS gate did not succeed')
            selected[workflow]['full_gate_job_id'] = guard['id']
    latest, eligible, ignored_foreign = {}, [], []
    for check in before:
        if (operator_run is not None and check['app_slug'] == 'github-actions'
                and check['suite_id'] == operator_run['check_suite_id']):
            continue
        foreign = foreign_pull_check(check, runs, repository)
        if foreign is not None:
            ignored_foreign.append(foreign)
            continue
        eligible.append(check)
        key = (check['app_id'], check['name'])
        if key not in latest or latest[key]['id'] < check['id']:
            latest[key] = check
    ignored, ignored_conditionals, ignored_retries, old_jobs = [], [], [], {}
    for check in latest.values():
        if check['status'] == 'completed' and check['conclusion'] in NON_BLOCKING:
            # A job name is not an identity; also examine bad historical rows
            # when the latest check is successful, not only for skips.
            older = [row for row in eligible if row['app_id'] == check['app_id']
                     and row['name'] == check['name'] and row['id'] < check['id']]
            for row in older:
                if row['status'] == 'completed' and row['conclusion'] in NON_BLOCKING:
                    continue
                if check['conclusion'] != 'success':
                    exception = superseded_conditional(row, check, runs, selected, repository, source,
                                                       old_jobs, selected_jobs)
                    require(exception is not None,
                            'latest skipped/neutral source check masked a failure: ' + check['name'])
                    ignored_conditionals.append(exception)
                else:
                    retry = superseded_successful_retry(row, check, runs, repository, source, selected_jobs)
                    require(retry is not None,
                            'latest successful source check masked a failure: ' + check['name'])
                    ignored_retries.append(retry)
            continue
        exception = obsolete_placeholder(check, runs, selected, repository, source, old_jobs)
        require(exception is not None, 'latest source check did not succeed: ' + check['name'])
        ignored.append(exception)
    # No new push/run/attempt/check result may replace the census used above.
    final_runs = source_runs(repository, source)
    if operator_run is not None:
        require(any(run['id'] == operator_run['id'] and identity(run) == identity(operator_run)
                    for run in final_runs), 'executing basic release run changed during admission')
    for workflow, value in selected.items():
        final = select_run(repository, workflow, final_runs, source)
        require(final is not None and identity(final) == value['run'],
                'latest source workflow changed during admission: ' + workflow)
    for run_id in old_jobs:
        initial = next(run for run in runs if run['id'] == run_id)
        require(identity(native.gh('repos/' + repository + '/actions/runs/' + str(run_id))) == identity(initial),
                'superseded source attempt changed during admission')
    for proof in ignored_retries:
        for key in ('origin_run', 'replacement_run'):
            old = proof[key]
            current = [run for run in final_runs if run['id'] == old['id']]
            require(len(current) == 1 and identity(current[0]) == old,
                    'successful source retry changed during admission')
    require(checks(repository, source) == before, 'source check census changed during admission')
    require(native.git('rev-parse', 'HEAD').decode().strip() == source, 'candidate checkout changed during admission')
    return {'schema_version': 1, 'status': 'passed', 'repository': repository, 'source_sha': source,
            'selected_workflows': selected, 'ignored_superseded_placeholders': ignored,
            'ignored_superseded_conditionals': ignored_conditionals,
            'ignored_successful_retries': ignored_retries,
            'ignored_foreign_pull_checks': ignored_foreign,
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
