#!/usr/bin/env bash
# Partition the authoritative Go package list without dropping future packages.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo 'usage: ci-ubuntu-go-shard.sh cmd-setup|cmd-other|packages|cursorevent' >&2
  exit 2
fi
shard=$1
case "$shard" in cmd-setup|cmd-other|packages|cursorevent) ;; *) echo "unknown Go shard: $shard" >&2; exit 2 ;; esac

package_list=$(go list ./...)
selected=()
while IFS= read -r package; do
  case "$package" in
    github.com/777genius/agent-notifications/cmd/*) owner=cmd ;;
    github.com/777genius/agent-notifications/internal/cursorevent) owner=cursorevent ;;
    '') echo 'empty Go package list entry' >&2; exit 1 ;;
    *) owner=packages ;;
  esac
  if [ "$owner" = "$shard" ] || { [ "$owner" = cmd ] && [[ "$shard" = cmd-* ]]; }; then
    selected+=("$package")
  fi
done <<< "$package_list"
if [ "${#selected[@]}" -eq 0 ]; then
  echo "Go shard has no packages: $shard" >&2
  exit 1
fi

# Complementary top-level filters keep future tests, examples and fuzz seeds in
# cmd-other unless explicitly owned by the setup group. Never filter subtests.
filters=()
setup_pattern='^Test(SetupWizard|SetupCodex)'
case "$shard" in
  cmd-setup) filters=(-run "$setup_pattern") ;;
  cmd-other) filters=(-skip "$setup_pattern") ;;
esac

go test -timeout 20m -v -race -coverprofile="coverage-$shard.txt" -covermode=atomic ${filters[@]+"${filters[@]}"} "${selected[@]}"
