#!/usr/bin/env bash
# This run-local merge is CI input, never remotely fetchable release evidence.
set -euo pipefail
cd "${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is required}"
head=${CI_PR_HEAD_SHA:?CI_PR_HEAD_SHA is required}
base=${CI_PR_BASE_SHA:?CI_PR_BASE_SHA is required}
for revision in "$head" "$base"; do
  if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
    echo 'Invalid immutable PR revision' >&2
    exit 1
  fi
  if ! git cat-file -e "$revision^{commit}" 2>/dev/null; then
    git -c http.lowSpeedLimit=1024 -c http.lowSpeedTime=60 fetch --no-tags origin "$revision"
    git cat-file -e "$revision^{commit}"
  fi
done
# checkout already owns a clean disposable CI worktree. Never accept the cached
# refs/pull merge commit as proof of the event's newly selected target base.
git checkout --detach "$base"
git -c user.name=iliya -c user.email=iliyazelenkog@gmail.com merge --no-commit --no-ff "$head"
tree=$(git write-tree)
if [ "$base" = "$head" ]; then
  tested=$head
else
  export GIT_AUTHOR_NAME=iliya GIT_AUTHOR_EMAIL=iliyazelenkog@gmail.com
  export GIT_COMMITTER_NAME=iliya GIT_COMMITTER_EMAIL=iliyazelenkog@gmail.com
  export GIT_AUTHOR_DATE=2000-01-01T00:00:00Z GIT_COMMITTER_DATE=2000-01-01T00:00:00Z
  [[ "$(git var GIT_AUTHOR_IDENT)" == 'iliya <iliyazelenkog@gmail.com> '* ]]
  [[ "$(git var GIT_COMMITTER_IDENT)" == 'iliya <iliyazelenkog@gmail.com> '* ]]
  tested=$(printf 'ci: qualify exact PR base edit\n' |
    git commit-tree "$tree" -p "$base" -p "$head")
fi
git reset --hard "$tested"
test "$(git rev-parse HEAD^{tree})" = "$tree"
printf 'Qualified run-local CI input: head=%s base=%s tested=%s\n' "$head" "$base" "$tested"
printf 'This changed-base checkout is not immutable release qualification.\n'
