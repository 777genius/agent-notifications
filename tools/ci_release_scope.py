#!/usr/bin/env python3
"""PR-only release operator scope. Unknown changes retain full native CI."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from ci_macos_scope import SHA, pr_identity, raw_changes  # noqa: E402

# Exact paths only. Build/CI policy, versions, loader/installer, native harnesses,
# dependencies and new operator files require full CI until explicitly reviewed.
TOOLS = frozenset({
    'scripts/release-progress.mts',
    'scripts/release-progress.test.mts',
    'scripts/release-preflight.mts',
    'scripts/release-preflight.test.mts',
    'scripts/release-wait.mts',
    'scripts/release-wait.test.mts',
    'scripts/release-public-verify.mts',
    'scripts/release-public-verify.test.mts',
    'scripts/basic-release-assets.mts',
    'scripts/basic-release-assets/assets.test.mts',
    'scripts/basic-release-assets/fake-gh.mts',
    'scripts/basic-release-assets/README.md',
    'docs/RELEASE.md',
    'docs/RELEASE_SPEED.md',
})


def classify(event_name, event, diff):
    if not pr_identity(event_name, event):
        return 'full'
    changes = raw_changes(diff)
    if not changes:
        return 'full'
    records, index, seen = diff[:-1].split(b'\0'), 0, set()
    for status, paths in changes:
        old_mode, new_mode, *_ = records[index].decode('ascii').split(' ')
        # Deletions, renames and all mode changes are outside this contract.
        if status not in {'A', 'M'} or (status == 'M' and old_mode[1:] != new_mode):
            return 'full'
        path = paths[0]
        if path not in TOOLS or path in seen:
            return 'full'
        seen.add(path)
        index += 2
    return 'release-tools'


def classify_checkout(event_name, event):
    pr = pr_identity(event_name, event)
    if not pr:
        return 'full'

    def git(*arguments):
        return subprocess.run(['git', *arguments], check=True, capture_output=True, timeout=30).stdout

    base, head = pr['base']['sha'], pr['head']['sha']
    if any(git('cat-file', '-t', revision).strip() != b'commit' for revision in (base, head)):
        return 'full'
    merge_base = git('merge-base', base, head).decode('ascii').strip()
    if not SHA.fullmatch(merge_base):
        return 'full'
    diff = git('diff', '--raw', '-z', '--no-abbrev', '--find-renames', '--no-ext-diff',
               '--no-textconv', '--ignore-submodules=none', merge_base, head, '--')
    return classify(event_name, event, diff)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['classify'])
    parser.parse_args()
    mode = 'full'
    try:
        event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
        mode = classify_checkout(os.environ.get('GITHUB_EVENT_NAME', ''), event)
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        print('Release scope unavailable; retaining full native CI.', file=sys.stderr)
    print('CI mode: ' + mode)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('mode=' + mode + '\n')
    return 0


if __name__ == '__main__':
    sys.exit(main())
