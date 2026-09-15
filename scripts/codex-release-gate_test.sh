#!/bin/sh
set -eu
selector=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/codex-release-gate.sh
check() { expected=$1; shift; actual=$(sh "$selector" "$@"); [ "$actual" = "$expected" ] || { echo "expected $expected, got $actual: $*" >&2; exit 1; }; }
check not-required --paths
check not-required --paths README.md docs/guide.md CHANGELOG.md
check required --first-release
for path in go.mod go.sum .github/workflows/ci-linux.yml .github/workflows/release.yml .codex-plugin/plugin.json .claude-plugin/plugin.json .claude-plugin/marketplace.json hooks/hooks-codex.json config/config.json setup.sh bin/hook-wrapper.sh bin/hook-wrapper.cmd bin/codex-hook-wrapper.sh bin/codex-hook-wrapper.cmd bin/install.sh bin/bootstrap.sh sounds/tone.wav swift-notifier/main.swift claude_icon.png scripts/codex-release-gate.sh scripts/codex-release-gate_test.sh cmd/claude-notifications/main.go; do
 check required --paths "$path"
 check required --paths README.md "$path"
done
for package in codexsource codexsetup analyzer audio config daemon dedup errorhandler hooks logging notifier platform sessionname sounds state summary webhook winfocus teamstate; do check required --paths "internal/$package/behavior.go"; done
check required --paths future/behavior.js
check required --paths commands/notifications-init.md
check required --paths bin/checksums.txt
check required --paths 'internal/hooks/name with spaces.go'
# Independent disposable Git history verifies real diff, deletions, renames,
# empty diffs, invalid refs and initial-release handling without touching checkout.
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT HUP INT TERM
cd "$tmp"
git init -q
git config user.name sandbox-test
git config user.email sandbox@example.invalid
printf 'base\n' > README.md
git add README.md; git commit -qm base
base=$(git rev-parse HEAD)
printf 'docs\n' >> README.md
git add README.md; git commit -qm docs
docs=$(git rev-parse HEAD)
check not-required --diff "$base" "$docs"
check not-required --diff "$docs" "$docs"
printf 'behavior\n' > runtime.go
git add runtime.go; git commit -qm behavior
behavior=$(git rev-parse HEAD)
check required --diff "$docs" "$behavior"
git mv runtime.go archived.md; git commit -qm rename
check required --diff "$behavior" HEAD
if sh "$selector" --diff missing-ref HEAD >/dev/null 2>&1; then echo 'invalid ref accepted' >&2; exit 1; fi
echo 'Codex release selector tests passed'
