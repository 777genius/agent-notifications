#!/usr/bin/env python3
"""Immutable full-native checkout receipts; scoped gates never produce evidence."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import zipfile

from ci_release_scope import SHA, equivalent_source, equivalent_tree, git

CONTRACTS = {
    'ci-macos.yml': (('test', 'swift-test'), 'Native input evidence (macOS)',
                     ('Test on macOS (1.25)', 'Test on macOS (1.26)', 'Swift notifier tests')),
    'ci-windows.yml': (('test',), 'Native input evidence (Windows)',
                       ('Test on Windows (1.25)', 'Test on Windows (1.26)')),
    'install-recovery-e2e.yml': (('recovery',), 'Native input evidence (recovery)',
                               ('Offline recovery (ubuntu-latest, native Go 1.26)',
                                'Offline recovery (macos-15, native Go 1.26)')),
    'ci-ubuntu.yml': (('go-tests', 'installer-native', 'opencode-plugin', 'lint', 'test'),
                      'Native input evidence (Ubuntu)',
                      tuple('Ubuntu Go tests (' + version + ', ' + shard + ')'
                            for version in ('1.25', '1.26') for shard in ('cmd', 'packages', 'cursorevent')) +
                      tuple('Ubuntu installers and native E2E (' + version + ')' for version in ('1.25', '1.26')) +
                      ('Test on Ubuntu (1.25)', 'Test on Ubuntu (1.26)', 'OpenCode plugin bundle', 'Lint')),
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def artifact_name(workflow, run_id, attempt):
    return 'native-inputs-' + workflow + '-' + str(run_id) + '-' + str(attempt)


def receipt(workflow, jobs):
    dependencies = CONTRACTS[workflow][0]
    needs = json.loads(os.environ['NEEDS_JSON'])
    require(tuple(jobs) == dependencies and os.environ.get('CI_MODE') == 'full', 'full job contract required')
    require(set(needs) == {'scope', *jobs} and all(
        isinstance(value, dict) and value.get('result') == 'success' for value in needs.values()),
        'all full dependencies must succeed')
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
    source = event.get('pull_request', {}).get('head', {}).get('sha', os.environ['GITHUB_SHA'])
    tested = git('rev-parse', 'HEAD').decode().strip()
    require(SHA.fullmatch(source) and tested == os.environ['GITHUB_SHA'], 'immutable checkout identity required')
    return {'schema': 1, 'repository': os.environ['GITHUB_REPOSITORY'], 'workflow': workflow,
            'run_id': int(os.environ['GITHUB_RUN_ID']), 'run_attempt': int(os.environ['GITHUB_RUN_ATTEMPT']),
            'source_head': source, 'tested_commit': tested,
            'tested_tree': git('rev-parse', 'HEAD^{tree}').decode().strip(), 'jobs': list(jobs), 'mode': 'full'}


def gh(path, binary=False):
    result = subprocess.run(['gh', 'api', path], check=True, capture_output=True, timeout=45).stdout
    return result if binary else json.loads(result)


def pages(path, key):
    result = []
    for page in range(1, 11):
        data = gh(path + ('&' if '?' in path else '?') + 'per_page=100&page=' + str(page))[key]
        require(isinstance(data, list), 'invalid API list')
        result.extend(data)
        if len(data) < 100:
            return result
    raise ValueError('API pagination limit exceeded')


def validate_receipt(proof, workflow, run, repository):
    require(isinstance(proof, dict) and proof.get('schema') == 1 and proof.get('mode') == 'full', 'full receipt required')
    expected = {'repository': repository, 'workflow': workflow, 'run_id': run['id'],
                'run_attempt': run['run_attempt'], 'source_head': run['head_sha'],
                'jobs': list(CONTRACTS[workflow][0])}
    require(all(proof.get(key) == value for key, value in expected.items()), 'receipt/run identity mismatch')
    require(all(isinstance(proof.get(key), str) and SHA.fullmatch(proof[key])
                for key in ('tested_commit', 'tested_tree')), 'invalid tested checkout')


def validate_jobs(jobs, workflow):
    expected = (*CONTRACTS[workflow][2], CONTRACTS[workflow][1])
    for name in expected:
        matches = [job for job in jobs if job.get('name') == name]
        require(matches and all(type(job.get('id')) is int for job in matches)
                and len({job['id'] for job in matches}) == len(matches), 'ambiguous full job history: ' + name)
        latest = max(matches, key=lambda job: job['id'])
        require(latest.get('conclusion') == 'success' and latest.get('status') == 'completed',
                'missing successful full job: ' + name)


def download_receipt(artifact):
    require(artifact.get('expired') is False and type(artifact.get('id')) is int, 'immutable artifact missing')
    digest = artifact.get('digest', '')
    require(isinstance(digest, str) and digest.startswith('sha256:') and len(digest) == 71, 'artifact digest required')
    archive = gh('repos/' + os.environ['GITHUB_REPOSITORY'] + '/actions/artifacts/' + str(artifact['id']) + '/zip', True)
    require(hashlib.sha256(archive).hexdigest() == digest[7:], 'artifact digest mismatch')
    with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
        require(bundle.namelist() == ['native-input-receipt.json'], 'unexpected artifact contents')
        entry = bundle.getinfo('native-input-receipt.json')
        require(entry.file_size <= 100000, 'receipt too large')
        return json.loads(bundle.read(entry))


def verify_run(workflow, source, event, run):
    repository = event['repository']['full_name']
    path = 'repos/' + repository + '/actions/runs/' + str(run['id'])
    # Reload final immutable attempt identity before consuming its artifacts.
    run = gh(path)
    require(run.get('head_sha') == source and run.get('status') == 'completed'
            and run.get('conclusion') == 'success' and run.get('event') in ('push', 'pull_request')
            and (run.get('head_repository') or {}).get('full_name') == repository
            and run.get('path') == '.github/workflows/' + workflow, 'unqualified source workflow run')
    attempt = run['run_attempt']
    validate_jobs(pages(path + '/jobs?filter=all', 'jobs'), workflow)
    artifacts = pages(path + '/artifacts', 'artifacts')
    matches = [item for item in artifacts if item.get('name') == artifact_name(workflow, run['id'], attempt)]
    require(len(matches) == 1, 'exact full-run receipt artifact required')
    proof = download_receipt(matches[0])
    validate_receipt(proof, workflow, run, repository)
    tested = proof['tested_commit']
    try:
        git('cat-file', '-e', tested + '^{commit}')
    except subprocess.SubprocessError:
        git('fetch', '--no-tags', '--depth=1', 'origin', tested)
    require(git('rev-parse', tested + '^{tree}').decode().strip() == proof['tested_tree'], 'tested tree mismatch')
    require(equivalent_tree(tested, 'HEAD'), 'tested merge checkout differs from current input tree')
    final = gh(path)
    require(final.get('run_attempt') == attempt and final.get('status') == 'completed'
            and final.get('conclusion') == 'success', 'source attempt changed during verification')
    return proof


def verify(workflow, source, event, wait_seconds=2700):
    require(workflow in CONTRACTS and os.environ['GITHUB_REPOSITORY'] == event['repository']['full_name'], 'unknown evidence contract')
    repository = event['repository']['full_name']
    # A main merge/squash may have only PR recovery evidence. Its associated PR
    # head is an additional candidate; the tested checkout tree still must match.
    sources = [source]
    associated = gh('repos/' + repository + '/commits/' + source + '/pulls')
    for pr in associated:
        candidate = pr.get('head', {}).get('sha')
        if (pr.get('merged_at') and pr.get('merge_commit_sha') == source
                and (pr.get('head', {}).get('repo') or {}).get('full_name') == repository
                and isinstance(candidate, str) and SHA.fullmatch(candidate) and candidate not in sources):
            sources.append(candidate)
    deadline = time.monotonic() + wait_seconds
    while True:
        pending = False
        failures = []
        for candidate in sources:
            runs = pages('repos/' + repository + '/actions/workflows/' + workflow + '/runs?head_sha=' + candidate, 'workflow_runs')
            for run in sorted(runs, key=lambda item: item['id'], reverse=True)[:1]:
                if run.get('status') != 'completed':
                    pending = True
                    continue
                if run.get('conclusion') != 'success':
                    raise ValueError('Latest source native run did not succeed')
                try:
                    return verify_run(workflow, candidate, event, run)
                except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, zipfile.BadZipFile) as error:
                    failures.append(str(error))
        if not pending or time.monotonic() >= deadline:
            raise ValueError('No qualified native input receipt: ' + (failures[0] if failures else 'source run absent, failed, or wait budget expired'))
        time.sleep(min(20, max(0, deadline - time.monotonic())))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('receipt', 'verify'))
    parser.add_argument('--workflow', required=True, choices=CONTRACTS)
    parser.add_argument('--output', default='native-input-receipt.json')
    parser.add_argument('--jobs', nargs='+', default=[])
    args = parser.parse_args()
    try:
        if args.command == 'receipt':
            Path(args.output).write_text(json.dumps(receipt(args.workflow, args.jobs), sort_keys=True) + '\n')
        else:
            event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
            source = equivalent_source(os.environ['GITHUB_EVENT_NAME'], event)
            require(source, 'release-equivalent source required')
            proof = verify(args.workflow, source, event)
            print('Verified full native checkout ' + proof['tested_commit'] + ' from run ' + str(proof['run_id']))
        return 0
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError, zipfile.BadZipFile) as error:
        print('Native evidence unavailable: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
