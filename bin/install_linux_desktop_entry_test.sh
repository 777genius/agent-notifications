#!/bin/bash
# The hidden GNOME notification desktop entry must use the bundled icon.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
LAB=$(mktemp -d "${TMPDIR:-/tmp}/desktop-entry-TEST-XXXXXX")
trap 'rm -rf "$LAB"' EXIT
sed '/^main "\$@"$/d' "$ROOT/bin/install.sh" > "$LAB/install-functions.sh"
mkdir -p "$LAB/plugin/bin"

run_entry() {
    rm -rf "$LAB/data"
    XDG_DATA_HOME="$LAB/data" INSTALL_TARGET_DIR="$LAB/plugin/bin" bash -c '
        source "$1"
        guard_install_paths() { :; }
        PLATFORM=linux
        install_linux_notification_desktop_entry >/dev/null
    ' _ "$LAB/install-functions.sh"
}

entry="$LAB/data/applications/claude-notifications.desktop"
icon="$LAB/data/icons/claude-notifications.png"

printf 'png-fixture' > "$LAB/plugin/claude_icon.png"
run_entry
grep -qx "Icon=$icon" "$entry" || { echo "FAIL: desktop entry does not use bundled icon" >&2; cat "$entry" >&2; exit 1; }
[ "$(cat "$icon")" = png-fixture ] || { echo "FAIL: bundled icon not copied" >&2; exit 1; }
grep -qx 'StartupNotify=false' "$entry"
echo 'PASS: desktop entry uses a stable copy of the bundled icon'

rm -f "$LAB/plugin/claude_icon.png"
run_entry
grep -qx 'Icon=utilities-terminal' "$entry" || { echo "FAIL: missing icon must fall back to utilities-terminal" >&2; exit 1; }
echo 'PASS: desktop entry falls back to utilities-terminal without bundled icon'
