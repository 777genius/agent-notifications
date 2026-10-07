#!/usr/bin/env python3
"""Cheap intermediate prose revisions; a skipped native suite never passes its gate."""
import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

PROSE = frozenset({'README.md', 'docs/DO_NOT_DISTURB.md', 'docs/NOTIFICATION_TYPES.md'})
SHA = re.compile(r'^[0-9a-f]{40}$')


def classify(event_name, event, diff):
    if event_name != 'pull_request' or not isinstance(event, dict):
        return 'full'
    pr = event.get('pull_request', {})
    if not isinstance(pr, dict) or not isinstance(pr.get('labels'), list):
        return 'full'
    if any(not isinstance(label, dict) or not isinstance(label.get('name'), str)
           for label in pr['labels']):
        return 'full'
    if any(label['name'] == 'ci:full' for label in pr['labels']):
        return 'full'
    if event.get('action') == 'ready_for_review':
        return 'full'
    for side in ('base', 'head'):
        if not isinstance(pr.get(side), dict) or not isinstance(pr[side].get('sha'), str) or not SHA.fullmatch(pr[side]['sha']):
            return 'full'
    # git's NUL format preserves spaces/newlines and cannot silently truncate API pages.
    if not diff or not diff.endswith(b'\0'):
        return 'full'
    records = diff[:-1].split(b'\0')
    if len(records) % 2:
        return 'full'
    try:
        changes = [(records[i].decode('utf-8'), records[i + 1].decode('utf-8'))
                   for i in range(0, len(records), 2)]
    except UnicodeError:
        return 'full'
    return 'docs' if all(status == 'M' and name in PROSE for status, name in changes) else 'full'


def final_gate(mode, needs, jobs):
    if mode != 'full':
        return False, 'Full final-head CI required: add the ci:full PR label, then wait for native jobs.'
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
        if event_name == 'pull_request':
            pr = event['pull_request']
            base, head = pr['base']['sha'], pr['head']['sha']
            if SHA.fullmatch(base) and SHA.fullmatch(head):
                diff = subprocess.run(['git', 'diff', '--name-status', '-z', '--find-renames',
                                       base + '...' + head], check=True, capture_output=True,
                                      timeout=30).stdout
        mode = classify(event_name, event, diff)
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        print('Classification unavailable; retaining full native CI.', file=sys.stderr)
    print('CI mode: ' + mode)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('mode=' + mode + '\n')
    return 0


if __name__ == '__main__':
    sys.exit(main())
