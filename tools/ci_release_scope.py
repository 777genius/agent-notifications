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


# These files do not affect the tested product. Entries and modes must still
# match; no directory patterns, additions, removals or executable docs qualify.
METADATA = frozenset({'release-channels.tsv', 'docs/RELEASE.md',
                      'docs/RELEASE_SPEED.md', 'docs/PLATFORM_RELEASE_CHANNELS.md'})
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




def merged_pulls(repository, commit):
    """Read the GitHub-owned merge association, never PR body or branch metadata."""
    response = subprocess.run(
        ['gh', 'api', 'repos/' + repository + '/commits/' + commit + '/pulls?per_page=2'],
        check=True, capture_output=True, timeout=30).stdout
    return json.loads(response)


def merged_main_promotion(event):
    """Only an exact-tree, uniquely associated main merge can reuse frozen release inputs."""
    if not isinstance(event, dict) or event.get('ref') != 'refs/heads/main':
        return None
    repository = event.get('repository')
    if not isinstance(repository, dict):
        return None
    name, after, before = repository.get('full_name'), event.get('after'), event.get('before')
    if (not isinstance(name, str) or name != os.environ.get('GITHUB_REPOSITORY')
            or not isinstance(after, str) or not SHA.fullmatch(after)
            or not isinstance(before, str) or not SHA.fullmatch(before)
            or git('rev-parse', 'HEAD').decode().strip() != after):
        return None
    parents = git('rev-list', '--parents', '-n', '1', after).decode().split()
    if len(parents) not in (2, 3) or parents[0] != after or parents[1] != before:
        return None
    paths = git('diff', '--name-only', '-z', '--no-renames', before, after, '--').split(b'\0')
    if b'release-channels.tsv' not in paths:
        return None
    associated = merged_pulls(name, after)
    if not isinstance(associated, list) or len(associated) != 1:
        return None
    pr = associated[0]
    if not isinstance(pr, dict):
        return None
    source = pr.get('head')
    target = pr.get('base')
    if not isinstance(source, dict) or not isinstance(target, dict):
        return None
    head = source.get('sha')
    if (not isinstance(head, str) or not SHA.fullmatch(head)
            or not isinstance(pr.get('merged_at'), str) or not pr['merged_at']
            or pr.get('merge_commit_sha') != after
            or target.get('ref') != 'main' or target.get('sha') != before
            or (source.get('repo') or {}).get('full_name') != name
            or (target.get('repo') or {}).get('full_name') != name
            or (len(parents) == 3 and parents[2] != head)
            ):
        return None
    try:
        if git('rev-parse', head + '^{tree}') != git('rev-parse', after + '^{tree}'):
            return None
    except subprocess.SubprocessError:
        return None
    return pr

def equivalent_source(event_name, event):
    pr = pr_identity(event_name, event)
    if not pr and event_name == 'push':
        candidate = merged_main_promotion(event)
        if candidate:
            # Reuse the same strict PR identity and frozen candidate verification.
            pr = pr_identity('pull_request', {
                'action': 'synchronize', 'number': candidate.get('number'),
                'repository': event['repository'], 'pull_request': candidate,
            })
    if not pr or pr['head']['repo']['full_name'] != event['repository']['full_name']:
        return None
    base, head = pr['base']['sha'], pr['head']['sha']
    merge_base = git('merge-base', base, head).decode().strip()
    changes = raw_changes(git('diff', '--raw', '-z', '--no-abbrev', '--find-renames', merge_base, head, '--'))
    if not changes:
        return None
    paths = {paths[0] for _, paths in changes}
    if 'release-channels.tsv' not in paths:
        return None
    # A stacked promotion includes C's version/product changes against
    # main. Compare against frozen C below, not against the older PR base.
    source = channel_source(head)
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
