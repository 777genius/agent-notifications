#!/usr/bin/env python3
"""Cheap intermediate platform/prose revisions; skipped native CI never passes."""
import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

PROSE = frozenset({'README.md', 'docs/DO_NOT_DISTURB.md', 'docs/NOTIFICATION_TYPES.md'})
WINDOWS = frozenset({
    '.github/workflows/navigation-windows-appsdk-build.yml',
    'tests/integration/windows_appsdk_build/README.md',
    'tests/integration/windows_appsdk_build/SDKContract.cpp',
    'tests/integration/windows_appsdk_build/SDKContract.vcxproj',
})
SHA = re.compile(r'^[0-9a-f]{40}$')
REPOSITORY = re.compile(r'^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$')
PR_ACTIONS = frozenset({'opened', 'synchronize', 'reopened', 'labeled', 'unlabeled', 'edited'})
REGULAR_MODES = frozenset({'100644', '100755'})
ZERO_SHA = '0' * 40


def pr_identity(event_name, event):
    """Only a complete, supported PR identity can opt out of native checks."""
    if event_name != 'pull_request' or not isinstance(event, dict) or not isinstance(event.get('action'), str) or event['action'] not in PR_ACTIONS:
        return None
    pr = event.get('pull_request', {})
    if not isinstance(pr, dict) or not isinstance(pr.get('labels'), list):
        return None
    if any(not isinstance(label, dict) or not isinstance(label.get('name'), str)
           for label in pr['labels']):
        return None
    if any(label['name'] == 'ci:full' for label in pr['labels']):
        return None
    if type(pr.get('draft')) is not bool or type(event.get('number')) is not int or event['number'] <= 0:
        return None
    if type(pr.get('number')) is not int or pr['number'] != event['number']:
        return None
    repository = event.get('repository')
    if not isinstance(repository, dict) or not isinstance(repository.get('full_name'), str) or not REPOSITORY.fullmatch(repository['full_name']):
        return None
    for side in ('base', 'head'):
        if not isinstance(pr.get(side), dict) or not isinstance(pr[side].get('sha'), str) or not SHA.fullmatch(pr[side]['sha']):
            return None
        repo = pr[side].get('repo')
        if not isinstance(repo, dict) or not isinstance(repo.get('full_name'), str) or not REPOSITORY.fullmatch(repo['full_name']):
            return None
    if pr['base']['repo']['full_name'] != repository['full_name']:
        return None
    return pr


def raw_changes(diff):
    """Read Git's raw NUL records, including both rename paths and tree modes."""
    if not isinstance(diff, bytes) or not diff or not diff.endswith(b'\0'):
        return None
    records = diff[:-1].split(b'\0')
    try:
        records = [record.decode('utf-8') for record in records]
    except UnicodeError:
        return None
    changes, index = [], 0
    while index < len(records):
        header = records[index].split(' ')
        if len(header) != 5 or not re.fullmatch(r':[0-9]{6}', header[0]) or not re.fullmatch(r'[0-9]{6}', header[1]):
            return None
        old_mode, new_mode, old_oid, new_oid, status = header
        old_mode = old_mode[1:]
        if not SHA.fullmatch(old_oid) or not SHA.fullmatch(new_oid):
            return None
        renamed = re.fullmatch(r'R(100|[1-9]?[0-9])', status)
        if status not in {'A', 'M', 'D'} and not renamed:
            return None
        count = 2 if renamed else 1
        paths = records[index + 1:index + 1 + count]
        if len(paths) != count or any(not path or path.startswith('/') or any(part in {'', '.', '..'} for part in path.split('/')) for path in paths):
            return None
        if status == 'A':
            valid_modes = old_mode == '000000' and old_oid == ZERO_SHA and new_mode in REGULAR_MODES and new_oid != ZERO_SHA
        elif status == 'D':
            valid_modes = new_mode == '000000' and new_oid == ZERO_SHA and old_mode in REGULAR_MODES and old_oid != ZERO_SHA
        else:
            valid_modes = old_mode in REGULAR_MODES and new_mode in REGULAR_MODES and old_oid != ZERO_SHA and new_oid != ZERO_SHA
        if not valid_modes:
            return None
        changes.append((status, paths))
        index += 1 + count
    return changes


def classify(event_name, event, diff):
    pr = pr_identity(event_name, event)
    changes = raw_changes(diff) if pr else None
    if not changes:
        return 'full'
    if all(status == 'M' and paths[0] in PROSE for status, paths in changes):
        return 'docs'
    if pr['draft'] is True and all(path in WINDOWS for _, paths in changes for path in paths):
        return 'windows-only'
    return 'full'


def final_gate(mode, needs, jobs):
    if mode != 'full':
        return False, 'Full final-head CI required: add the ci:full PR label, then wait for native jobs.'
    if not isinstance(jobs, (list, tuple)) or any(not isinstance(job, str) for job in jobs) or tuple(jobs) not in {('test', 'swift-test'), ('recovery',)}:
        return False, 'Unknown native job contract; full CI evidence is incomplete.'
    if not isinstance(needs, dict) or set(needs) != {'scope', *jobs}:
        return False, 'Missing or unknown native jobs; full CI evidence is incomplete.'
    if not isinstance(needs, dict) or not isinstance(needs.get('scope'), dict) or needs['scope'].get('result') != 'success':
        return False, 'Scope classification failed; full CI evidence is incomplete.'
    missing = [job for job in jobs if not isinstance(needs.get(job), dict)
               or needs[job].get('result') != 'success']
    return (False, 'Native checks did not succeed: ' + ', '.join(missing)) if missing else (True, 'Full native CI passed.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('classify')
    gate = sub.add_parser('gate')
    gate.add_argument('jobs', nargs='+')
    args = parser.parse_args()
    if args.command == 'gate':
        try:
            needs = json.loads(os.environ.get('NEEDS_JSON', '{}'))
        except ValueError:
            needs = {}
        ok, message = final_gate(os.environ.get('CI_MODE', ''), needs, args.jobs)
        print(message)
        return 0 if ok else 1
    mode = 'full'
    try:
        event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
        event_name = os.environ.get('GITHUB_EVENT_NAME', '')
        diff = b''
        pr = pr_identity(event_name, event)
        if pr:
            base, head = pr['base']['sha'], pr['head']['sha']
            def git(*arguments):
                return subprocess.run(['git', *arguments], check=True, capture_output=True, timeout=30).stdout
            if any(git('cat-file', '-t', revision).strip() != b'commit' for revision in (base, head)):
                raise ValueError('PR revisions must be commits')
            merge_base = git('merge-base', base, head).decode('ascii').strip()
            if not SHA.fullmatch(merge_base):
                raise ValueError('Invalid merge base')
            diff = git('diff', '--raw', '-z', '--no-abbrev', '--find-renames',
                       '--no-ext-diff', '--no-textconv', '--ignore-submodules=none',
                       merge_base, head, '--')
        mode = classify(event_name, event, diff)
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        print('Classification unavailable; retaining full native CI.', file=sys.stderr)
    print('CI mode: ' + mode)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('mode=' + mode + '\n')
    return 0


if __name__ == '__main__':
    sys.exit(main())
