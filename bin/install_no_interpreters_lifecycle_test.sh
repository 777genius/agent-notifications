#!/bin/bash
set -eo pipefail
umask 077

root=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck disable=SC1091 # resolved from the repository root above
source "$root/bin/test-env.sh"
test_env_enter "$0" "$@"

native_helper=${1:-}
if [ -z "$native_helper" ] || [ ! -x "$native_helper" ]; then
    echo "usage: $0 /absolute/path/to/claude-notifications" >&2
    exit 2
fi
case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
    linux) ;;
    *)
        echo "SKIP: interpreter-free lifecycle qualification runs on Linux artifacts"
        exit 0
        ;;
esac

native_dir=$(cd "$(dirname "$native_helper")" && pwd -P)
native_helper="$native_dir/$(basename "$native_helper")"
box=$(mktemp -d)
trap 'rm -rf "$box"' EXIT
test_env_setup "$box"
cd "$box"
box=$(pwd -P)

case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "unsupported Linux architecture" >&2; exit 1 ;;
esac
entry="claude-notifications-linux-$arch"

mkdir -p \
    "$box/path" "$box/stage" "$box/runtime/bin" "$box/control" \
    "$box/package-r1/bin" "$box/package-r1/skills/agent-notify" \
    "$box/package-r2/bin" "$box/package-r2/skills/agent-notify" \
    "$box/codex/foreign-plugin" "$box/claude/skills/foreign-plugin" \
    "$box/scope" "$box/global" "$box/home" "$box/xdg"

# The controller prepares release-like ZIP fixtures. Python is deliberately not
# exposed to the target process below; the plan permits build/test tooling on
# the controller while forbidding Python, Node and Go in the installer tree.
for version in 1.0.0 1.0.1; do
    case "$version" in 1.0.0) package="$box/package-r1" ;; *) package="$box/package-r2" ;; esac
    # shellcheck disable=SC2016 # literal JSON schema key
    printf '%s\n' \
        '{' \
        '  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",' \
        '  "name": "agent-notify",' \
        "  \"version\": \"$version\"" \
        '}' > "$package/plugin.json"
    cp "$root/portable-package/mcp.json" "$package/mcp.json"
    cp "$root/portable-package/skills/agent-notify/SKILL.md" "$package/skills/agent-notify/SKILL.md"
    cp "$native_helper" "$package/bin/claude-notifications"
    chmod 700 "$package/bin/claude-notifications"
    python3 -I - "$package" "$box/package-$version.zip" <<'PY'
import pathlib
import sys
import zipfile

source = pathlib.Path(sys.argv[1])
target = pathlib.Path(sys.argv[2])
with zipfile.ZipFile(target, "w", compression=zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(source.rglob("*")):
        if path.is_file():
            archive.write(path, path.relative_to(source).as_posix())
PY
done

cp "$native_helper" "$box/stage/$entry"
chmod 700 "$box/stage/$entry"
printf '%s\n' 'foreign-codex-entry' > "$box/codex/foreign-plugin/sentinel"
printf '%s\n' 'foreign-claude-entry' > "$box/claude/skills/foreign-plugin/sentinel"

# Claude's native verifier asks the selected executable for the user plugin
# inventory. This fixture reports only the real managed path materialized by
# the installer; it does not perform registration or lifecycle mutations.
# shellcheck disable=SC2016 # the generated probe expands these variables later
printf '%s\n' \
    '#!/bin/sh' \
    'case "$*" in' \
    '  "plugin list --json")' \
    '    for p in "$CLAUDE_CONFIG_DIR"/skills/agent-notify-*; do' \
    '      if [ -d "$p" ]; then' \
    '        version=$(sed -n '\''s/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'\'' "$p/.claude-plugin/plugin.json")' \
    '        printf "[{\"id\":\"agent-notify@skills-dir\",\"version\":\"%s\",\"scope\":\"user\",\"enabled\":true,\"installPath\":\"%s\"}]\n" "$version" "$p"' \
    '        exit 0' \
    '      fi' \
    '    done' \
    '    printf "[]\n";;' \
    '  *) printf "{\"ok\":true}\n";;' \
    'esac' > "$box/client-probe"
chmod 700 "$box/client-probe"

for tool in basename cat chmod cmp cp dirname find grep mkdir mktemp mv rm sed sha256sum shasum tr uname; do
    location=$(command -v "$tool" || true)
    [ -z "$location" ] || ln -s "$location" "$box/path/$tool"
done

(
    export PATH="$box/path"
    export HOME="$box/home"
    export XDG_CONFIG_HOME="$box/xdg"
    export CODEX_HOME="$box/codex"
    export CLAUDE_CONFIG_DIR="$box/claude"
    for tool in python python3 node npm go jq; do
        if command -v "$tool" >/dev/null; then exit 1; fi
    done

    "$native_helper" internal-install-runtime \
        --stage "$box/stage" --target "$box/runtime/bin" --entry "$entry" \
        --control-root "$box/control"

    # shellcheck disable=SC2054 # claude,codex is one CLI value
    common=(
        --agents claude,codex --hooks false --agent-notify true
        --control-root "$box/control" --runtime-root "$box/runtime"
        --global-config "$box/global/config.json"
        --codex-home "$box/codex" --claude-config "$box/claude"
        --claude-executable "$box/client-probe" --codex-executable "$box/client-probe"
        --helper "$box/runtime/bin/$entry" --scope-root "$box/scope"
    )
    run_wizard() {
        local name=$1 action=$2 package=$3
        shift 3
        "$native_helper" setup-notifications wizard \
            --action "$action" --package "$package" --yes --json "$@" "${common[@]}" \
            > "$box/$name.json"
        grep -Eq '"outcome":"(completed|unchanged)"' "$box/$name.json"
    }
    installation_id() {
        sed -n 's/.*"installationID":"\([^"]*\)".*/\1/p' "$1"
    }

    run_wizard install install "$box/package-1.0.0.zip"
    installed_id=$(installation_id "$box/install.json")
    [ -n "$installed_id" ]
    run_wizard repeat install "$box/package-1.0.0.zip"
    [ "$(installation_id "$box/repeat.json")" = "$installed_id" ]

    data_dir=""
    for candidate in "$box"/uap/plugin-data/agent-notify-*; do
        if [ -d "$candidate" ]; then data_dir=$candidate; break; fi
    done
    [ -n "$data_dir" ]
    printf '%s\n' 'retained-user-data' > "$data_dir/retained.txt"

    run_wizard update update "$box/package-1.0.1.zip"
    [ "$(installation_id "$box/update.json")" = "$installed_id" ]

    projection=""
    for candidate in "$box/claude"/skills/agent-notify-*; do
        if [ -d "$candidate" ]; then projection=$candidate; break; fi
    done
    [ -n "$projection" ]
    mv "$projection" "$box/removed-claude-projection"
    run_wizard repair repair "$box/package-1.0.1.zip"
    repaired=false
    for candidate in "$box/claude"/skills/agent-notify-*; do
        [ -f "$candidate/.mcp.json" ] && repaired=true
    done
    [ "$repaired" = true ]

    run_wizard remove uninstall "$box/package-1.0.1.zip" --external-uninstalled
    grep -q 'foreign-codex-entry' "$box/codex/foreign-plugin/sentinel"
    grep -q 'foreign-claude-entry' "$box/claude/skills/foreign-plugin/sentinel"
    grep -q 'retained-user-data' "$data_dir/retained.txt"

    run_wizard reinstall install "$box/package-1.0.1.zip"
    [ "$(installation_id "$box/reinstall.json")" = "$installed_id" ]
    grep -q 'retained-user-data' "$data_dir/retained.txt"

    cp "$box/uap/state/state-v2.json" "$box/state-before-invalid-config.json"
    printf '%s' '{' > "$box/malformed-config.json"
    export AGENT_NOTIFICATIONS_CONFIG="$box/malformed-config.json"
    if "$native_helper" config installer preflight "$box/runtime" >/dev/null 2>&1; then
        echo 'malformed config passed native preflight' >&2
        exit 1
    fi
    cmp "$box/state-before-invalid-config.json" "$box/uap/state/state-v2.json"
)

echo "PASS: interpreter-free artifact fresh/repeat/update/repair/remove/reinstall lifecycle"
