#!/bin/bash
# Real installer boundary: a native Go binary must persist an owned runtime and
# global plugin in a disposable profile, with independent webhook consent.
# The Windows private-root fixture reuses the already prepared offline Go cache.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) TEST_ENV_HANDOFF_GOMODCACHE=1 ;;
esac
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
[ "$#" -eq 1 ] || { echo "Usage: bash bin/bootstrap_opencode_test.sh /absolute/native/test-binary" >&2; exit 2; }
TEST_BINARY="$1"
[ -f "$TEST_BINARY" ] || exit 2
# CI qualifies release-mode bytes: unstripped Windows debug symbols can exceed
# the product's existing 32 MiB executable bound. Keep the actual size visible.
source_size=$(wc -c < "$TEST_BINARY" | tr -d '[:space:]')
printf 'OpenCode fixture native source: %s bytes\n' "$source_size"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SANDBOX=$(mktemp -d /tmp/bootstrap-opencode-XXXXXX)
trap 'rm -rf "$SANDBOX"' EXIT
# Give all Windows sandbox children the established private inherited DACL
# before creating HOME/config/plugin paths. chmod alone does not create it.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        (cd "$ROOT" && GOTMPDIR="$(cygpath -m "$TMPDIR")" go run scripts/opencode-private-root-windows.go "$(cygpath -m "$SANDBOX")") ;;
esac
test_env_setup "$SANDBOX"
export OPENCODE_CONFIG_DIR="$SANDBOX/opencode profile"
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        # OpenCode's strict placement contract requires filepath.Clean(path) ==
        # path. Native Windows environment paths therefore need backslashes.
        export USERPROFILE="$(cygpath -w "$USERPROFILE")" APPDATA="$(cygpath -w "$APPDATA")"
        export LOCALAPPDATA="$(cygpath -w "$LOCALAPPDATA")" OPENCODE_CONFIG_DIR="$(cygpath -w "$OPENCODE_CONFIG_DIR")"
        export XDG_CONFIG_HOME="$(cygpath -w "$XDG_CONFIG_HOME")"
        ;;
esac
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$SANDBOX/functions.sh"
source "$SANDBOX/functions.sh"
_CONFIG_HELPER="$TEST_BINARY"
PRODUCT=opencode
OPENCODE_ARGS=(--webhook)
install_opencode
control=$(bootstrap_control_root)
read -r os arch < <(bootstrap_release_os_arch)
name="claude-notifications-$os-$arch"
[ "$os" != windows ] || name="$name.exe"
installed="$control/runtime/$name"
[ -f "$installed" ]
[ -f "$OPENCODE_CONFIG_DIR/plugins/agent-notifications.js" ]
"$installed" config inspect --json > "$SANDBOX/inspect.json"
# A repeated public install must reuse the recorded runtime and preserve config.
cp "$control/ownership.json" "$SANDBOX/ownership-before.json"
config=$("$installed" config path)
case "$os" in windows) config=$(cygpath -u "$config") ;; esac
cp "$config" "$SANDBOX/config-before.json"
install_opencode
cmp "$config" "$SANDBOX/config-before.json"
python3 - "$control" "$OPENCODE_CONFIG_DIR" "$installed" <<'PY'
import json, pathlib, sys
control, profile, binary = map(pathlib.Path,sys.argv[1:])
ledger=json.loads((control/'ownership.json').read_text())
assert len(ledger['Consumers']) == 1, ledger['Consumers']
policy=json.loads((control/'agent-notifications.json').read_text())
assert policy['route']['openCodeNotifications'] == {'desktop':False,'webhook':True}
assert policy.get('enabled',False) is False
assert binary.read_bytes(), 'runtime disappeared with installer staging'
assert (profile/'plugins/agent-notifications.js').read_bytes(), 'global plugin missing'
PY
# Execute the same command printed by public setup. On Windows its temporary
# remover must preserve failure status and avoid deleting a foreign plugin.
remove_command=$(opencode_remove_command "$installed")
plugin="$OPENCODE_CONFIG_DIR/plugins/agent-notifications.js"
cp "$plugin" "$SANDBOX/owned-plugin.js"
printf 'foreign plugin edit\n' > "$plugin"
if bash -c "$remove_command"; then
    echo "removal accepted an edited plugin" >&2
    exit 1
fi
[ -f "$installed" ]
printf 'foreign plugin edit\n' > "$SANDBOX/foreign-plugin.js"
cmp "$plugin" "$SANDBOX/foreign-plugin.js"
cp "$SANDBOX/owned-plugin.js" "$plugin"
bash -c "$remove_command"
python3 - "$control" <<'PYREMOVE'
import json, pathlib, sys
root=pathlib.Path(sys.argv[1])
ledger=json.loads((root/'ownership.json').read_text())
assert not ledger['Consumers'], ledger['Consumers']
policy=json.loads((root/'agent-notifications.json').read_text())
assert policy['route']['openCodeNotifications'] == {'desktop':False,'webhook':False}
PYREMOVE
[ ! -e "$installed" ]
[ ! -e "$OPENCODE_CONFIG_DIR/plugins/agent-notifications.js" ]
echo "OpenCode bootstrap real native lifecycle passed in disposable profile"
