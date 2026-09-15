#!/bin/sh
# Prints required or not-required. Conservative by design: unknown behavior
# paths require the gate, including future packages. No live provider is run.
# Usage: --paths PATH... | --diff BASE HEAD | --first-release
set -eu
select_path() {
 case "$1" in
  scripts/codex-release-gate.sh|scripts/codex-release-gate_test.sh) return 0 ;;
  commands/*|.claude-plugin/*|.codex-plugin/*|hooks/*|config/*|bin/*|sounds/*|swift-notifier/*|.github/workflows/*) return 0 ;;
  *.md|*.rst|*.txt|docs/*|LICENSE|LICENSE.*) return 1 ;;
  *) return 0 ;;
 esac
}
case "${1-}" in
 --first-release) [ "$#" -eq 1 ] || exit 2; echo required; exit 0 ;;
 --paths)
  shift
  for path do
   # Reject ambiguous paths rather than allowing traversal to bypass selection.
   case "$path" in ''|/*|../*|*/../*) echo 'invalid repository path' >&2; exit 2;; esac
   path=${path#./}
   if select_path "$path"; then echo required; exit 0; fi
  done
  echo not-required ;;
 --diff)
  [ "$#" -eq 3 ] || exit 2
  # Disable rename detection: both the deleted and added path must be checked.
  # Git quotes unusual names; these conservatively select required.
  paths=$(git -c core.quotePath=true diff --no-ext-diff --no-renames --name-only "$2" "$3" --) || exit 2
  required=false
  while IFS= read -r path; do
   [ -n "$path" ] || continue
   if select_path "$path"; then required=true; fi
  done <<PATHS
$paths
PATHS
  if "$required"; then echo required; else echo not-required; fi ;;
 *) echo 'usage: codex-release-gate.sh --paths [PATH...] | --diff BASE HEAD | --first-release' >&2; exit 2 ;;
esac
