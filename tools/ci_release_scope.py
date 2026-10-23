#!/usr/bin/env python3
"""PR-only release operator scope. Unknown changes retain full native CI."""
import argparse
import json
import os
import re
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


# These files do not affect the tested product. Entries and modes must still
# match; no directory patterns, additions, removals or executable docs qualify.
METADATA = frozenset({'release-channels.tsv', 'docs/RELEASE.md',
                      'docs/RELEASE_SPEED.md', 'docs/PLATFORM_RELEASE_CHANNELS.md'})
RELEASE_WORKFLOW = '.github/workflows/release.yml'
PLATFORMS = {('darwin', 'amd64'), ('darwin', 'arm64'), ('linux', 'amd64'),
             ('linux', 'arm64'), ('windows', 'amd64')}


def git(*arguments):
    return subprocess.run(['git', *arguments], check=True, capture_output=True, timeout=30).stdout


def entries(revision):
    result = {}
    for record in git('ls-tree', '-rz', '--full-tree', revision).split(b'\0'):
        if record:
            entry, path = record.split(b'\t', 1)
            result[path.decode('utf-8')] = entry.decode('ascii').split(' ')
    return result


def blob(revision, path):
    return git('show', revision + ':' + path)


def stable_exclusion_only(before, after):
    """Exactly one additive stable tag exclusion in the existing tags list."""
    old, new = before.splitlines(keepends=True), after.splitlines(keepends=True)
    if len(new) != len(old) + 1 or not before.endswith(b'\n') or not after.endswith(b'\n'):
        return False
    for index, line in enumerate(new):
        if (re.fullmatch(rb"      - '!v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)'\n", line)
                and line not in old and new[:index] + new[index + 1:] == old):
            # The literal surrounding header and contiguous tag entries make
            # placement structural without interpreting arbitrary workflow YAML.
            prefix = b''.join(new[:index])
            section = prefix.split(b'\non:\n  push:\n    tags:\n')
            return len(section) == 2 and all(
                re.fullmatch(rb"      - '[^'\n]+'\n", item)
                for item in section[1].splitlines(keepends=True))
    return False


def equivalent_tree(source, target):
    """Compare every tracked entry including mode, submodule and symlink OID."""
    old, new = entries(source), entries(target)
    if old.keys() != new.keys():
        return False
    for path in old:
        if old[path] == new[path]:
            continue
        if old[path][:2] != ['100644', 'blob'] or new[path][:2] != ['100644', 'blob']:
            return False
        if path in METADATA:
            continue
        if path == RELEASE_WORKFLOW and stable_exclusion_only(blob(source, path), blob(target, path)):
            continue
        return False
    return True


def channel_source(revision):
    rows = [line.split('\t') for line in blob(revision, 'release-channels.tsv').decode().splitlines()
            if line and not line.startswith('#')]
    if (len(rows) != 5 or any(len(row) != 6 for row in rows)
            or {(row[0], row[1]) for row in rows} != PLATFORMS
            or any(not SHA.fullmatch(row[3]) or not SHA.fullmatch(row[4]) for row in rows)
            or len({row[3] for row in rows}) != 1):
        raise ValueError('channel source is not one frozen five-platform commit')
    return rows[0][3]


def equivalent_source(event_name, event):
    pr = pr_identity(event_name, event)
    if not pr or pr['head']['repo']['full_name'] != event['repository']['full_name']:
        return None
    base, head = pr['base']['sha'], pr['head']['sha']
    merge_base = git('merge-base', base, head).decode().strip()
    changes = raw_changes(git('diff', '--raw', '-z', '--no-abbrev', '--find-renames', merge_base, head, '--'))
    if not changes or any(status != 'M' for status, _ in changes):
        return None
    paths = {paths[0] for _, paths in changes}
    if paths == {RELEASE_WORKFLOW} and stable_exclusion_only(blob(merge_base, RELEASE_WORKFLOW), blob(head, RELEASE_WORKFLOW)):
        source = base
    elif 'release-channels.tsv' in paths and paths <= METADATA:
        source = channel_source(head)
    else:
        return None
    # Frozen product source must belong to this PR history. Never accept a
    # channel pointer to arbitrary otherwise similar code.
    git('merge-base', '--is-ancestor', source, head)
    return source if equivalent_tree(source, 'HEAD') else None


def classify(event_name, event, diff):
    pr = pr_identity(event_name, event)
    if not pr or pr['head']['repo']['full_name'] != event['repository']['full_name']:
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
        event_name = os.environ.get('GITHUB_EVENT_NAME', '')
        mode = classify_checkout(event_name, event)
        if mode == 'full' and os.environ.get('CI_NATIVE_WORKFLOW'):
            source = equivalent_source(event_name, event)
            if source:
                from ci_native_evidence import verify
                verify(os.environ['CI_NATIVE_WORKFLOW'], source, event)
                mode = 'release-equivalent'
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print('Release scope unavailable; retaining full native CI: ' + str(error), file=sys.stderr)
    print('CI mode: ' + mode)
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        output.write('mode=' + mode + '\n')
    return 0


if __name__ == '__main__':
    sys.exit(main())
