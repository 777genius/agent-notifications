#!/usr/bin/env bash
# Run the real /init Bash block with an inert installer and offline curl.
# No network, user profile, desktop notification, or MCP registration.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
sed -n '/^SKIP_AGENT_NOTIFY=false$/,/^\x60\x60\x60$/p' "$root/commands/init.md" | sed '$d' > "$sandbox/init.sh"
grep -q '^SKIP_AGENT_NOTIFY=false$' "$sandbox/init.sh"

mkdir -p "$sandbox/mock"
cat > "$sandbox/mock/curl" <<'CURL'
#!/bin/bash
set -eu
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then out=$2; shift 2; else shift; fi
done
[ -n "$out" ] || exit 96
cp "$INIT_STUB" "$out"
CURL
cat > "$sandbox/mock-installer" <<'INSTALL'
#!/bin/bash
: > "$INIT_ARGS_LOG"
for arg in "$@"; do printf '%s\n' "$arg" >> "$INIT_ARGS_LOG"; done
if [ "$INIT_CASE" = "creates" ]; then
  mkdir -p "$XDG_CONFIG_HOME/agent-notifications"
  printf 'TEST managed ledger\n' > "$XDG_CONFIG_HOME/agent-notifications/ownership.json"
fi
exit 0
INSTALL
chmod +x "$sandbox/mock/curl" "$sandbox/mock-installer"

for scenario in missing present creates relative; do
  dir="$sandbox/$scenario"
  mkdir -p "$dir/plugin/bin" "$dir/config" "$dir/home"
  export CLAUDE_PLUGIN_ROOT="$dir/plugin"
  export XDG_CONFIG_HOME="$dir/config" HOME="$dir/home" INIT_CASE="$scenario"
  export INIT_STUB="$sandbox/mock-installer" INIT_ARGS_LOG="$dir/args"
  [ "$scenario" != relative ] || export XDG_CONFIG_HOME=relative-config
  if [ "$scenario" = present ]; then
    mkdir -p "$XDG_CONFIG_HOME/agent-notifications"
    printf 'TEST existing ledger\n' > "$XDG_CONFIG_HOME/agent-notifications/ownership.json"
  fi
  result=0
  PATH="$sandbox/mock:$PATH" bash "$sandbox/init.sh" > "$dir/log" 2>&1 || result=$?
  case "$scenario" in
    missing)
      [ "$result" -ne 0 ] || { echo 'missing ledger reported init success' >&2; exit 1; }
      grep -q 'Managed runtime ownership was not established' "$dir/log"
      grep -qx -- --force "$dir/args"
      ;;
    present)
      [ "$result" -eq 0 ] || { cat "$dir/log" >&2; exit 1; }
      [ ! -s "$dir/args" ] || { echo 'existing ledger unexpectedly forced installation' >&2; exit 1; }
      ;;
    creates)
      [ "$result" -eq 0 ] || { cat "$dir/log" >&2; exit 1; }
      grep -qx -- --force "$dir/args"
      [ -f "$XDG_CONFIG_HOME/agent-notifications/ownership.json" ]
      ;;
    relative)
      [ "$result" -ne 0 ] || { echo 'relative config home accepted' >&2; exit 1; }
      grep -q 'XDG_CONFIG_HOME must be an absolute path' "$dir/log"
      [ ! -f "$dir/args" ] || { echo 'relative config home invoked installer' >&2; exit 1; }
      ;;
  esac
done
echo 'PASS: /init rejects relative config homes and offline missing ownership; registered/fresh installs continue'
