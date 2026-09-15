#!/usr/bin/env bash
# Switch the real Claude Code environment between the published marketplace
# source and the local repo checkout for convenient dev testing.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MARKETPLACE_NAME="claude-notifications-go"
PLUGIN_NAME="claude-notifications-go"
PLUGIN_KEY="${PLUGIN_NAME}@${MARKETPLACE_NAME}"
REMOTE_REPO="777genius/agent-notifications"

REAL_CLAUDE_HOME="${REAL_CLAUDE_HOME:-${CLAUDE_CONFIG_DIR:-$HOME/.claude}}"

KNOWN_MARKETPLACES_JSON="${REAL_CLAUDE_HOME}/plugins/known_marketplaces.json"
INSTALLED_JSON="${REAL_CLAUDE_HOME}/plugins/installed_plugins.json"
BACKUP_FILE="${REAL_CLAUDE_HOME}/claude-notifications-go/dev-real-switch-backup.json"

print_help() {
    cat <<EOF
Usage: scripts/dev-real-plugin.sh <command>

Commands:
  local     Switch the real Claude environment to the local repo source
  remote    Switch back to the published marketplace source
  toggle    Toggle between local and published sources
  status    Show current source/install version in the real Claude environment
  help      Show this help

Environment:
  REAL_CLAUDE_HOME   Override target Claude config dir (default: ~/.claude)
  CLAUDE_CONFIG_DIR  Also respected as an override

Notes:
  - This script modifies the real Claude plugin marketplace entry.
  - The marketplace name stays the same in the UI: ${MARKETPLACE_NAME}
  - After switching, restart Claude Code to ensure the running app picks up the new plugin path.
EOF
}

run_claude() {
    CLAUDE_CONFIG_DIR="${REAL_CLAUDE_HOME}" claude "$@"
}

require_claude() {
    if ! command -v claude >/dev/null 2>&1; then
        echo "Error: claude CLI not found in PATH" >&2
        exit 1
    fi
}

json_value() {
    local file="$1"
    local jq_expr="$2"
    local py_expr="$3"

    [ -f "$file" ] || return 0

    if command -v jq >/dev/null 2>&1; then
        jq -r "${jq_expr} // empty" "$file" 2>/dev/null || true
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$file" "$py_expr" <<'PYEOF' 2>/dev/null || true
import json, sys
path = [part for part in sys.argv[2].split('.') if part]
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    cur = data
    for part in path:
        if isinstance(cur, dict):
            cur = cur.get(part)
        else:
            cur = None
            break
    if isinstance(cur, (str, int, float, bool)):
        print(cur)
except Exception:
    pass
PYEOF
    fi
}

get_marketplace_source_type() {
    json_value "$KNOWN_MARKETPLACES_JSON" ".[\"${MARKETPLACE_NAME}\"].source.source" "${MARKETPLACE_NAME}.source.source"
}

get_marketplace_source_value() {
    local source_type
    source_type="$(get_marketplace_source_type)"
    case "$source_type" in
        directory)
            json_value "$KNOWN_MARKETPLACES_JSON" ".[\"${MARKETPLACE_NAME}\"].source.path" "${MARKETPLACE_NAME}.source.path"
            ;;
        github)
            json_value "$KNOWN_MARKETPLACES_JSON" ".[\"${MARKETPLACE_NAME}\"].source.repo" "${MARKETPLACE_NAME}.source.repo"
            ;;
        *)
            ;;
    esac
}

get_marketplace_display() {
    local source_type source_value
    source_type="$(get_marketplace_source_type)"
    source_value="$(get_marketplace_source_value)"
    if [ -n "$source_type" ] && [ -n "$source_value" ]; then
        printf '%s:%s' "$source_type" "$source_value"
        return 0
    fi
    printf 'missing'
}

get_installed_version() {
    if command -v jq >/dev/null 2>&1; then
        jq -r ".plugins[\"${PLUGIN_KEY}\"][0].version // empty" "$INSTALLED_JSON" 2>/dev/null || true
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    entries = data.get('plugins', {}).get(sys.argv[2], [])
    if entries:
        print(entries[0].get('version', '') or '')
except Exception:
    pass
PYEOF
    fi
}

get_install_path() {
    if command -v jq >/dev/null 2>&1; then
        jq -r ".plugins[\"${PLUGIN_KEY}\"][0].installPath // empty" "$INSTALLED_JSON" 2>/dev/null || true
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    entries = data.get('plugins', {}).get(sys.argv[2], [])
    if entries:
        print(entries[0].get('installPath', '') or '')
except Exception:
    pass
PYEOF
    fi
}

get_cached_plugin_version() {
    local install_path="$1"
    local plugin_json=""
    [ -n "$install_path" ] || return 0
    plugin_json="${install_path}/.claude-plugin/plugin.json"
    if [ -f "$plugin_json" ]; then
        json_value "$plugin_json" ".version" "version"
    fi
}

save_backup_if_needed() {
    local source_type source_value
    source_type="$(get_marketplace_source_type)"
    source_value="$(get_marketplace_source_value)"

    # Only save a backup of a non-local source so "remote" can restore cleanly.
    if [ "$source_type" = "directory" ] && [ "$source_value" = "$REPO_ROOT" ]; then
        return 0
    fi

    mkdir -p "$(dirname "$BACKUP_FILE")"
    cat > "$BACKUP_FILE" <<EOF
{
  "sourceType": "${source_type}",
  "sourceValue": "${source_value}"
}
EOF
}

get_backup_source() {
    if [ -f "$BACKUP_FILE" ]; then
        local backup_type backup_value
        backup_type="$(json_value "$BACKUP_FILE" ".sourceType" "sourceType")"
        backup_value="$(json_value "$BACKUP_FILE" ".sourceValue" "sourceValue")"
        case "$backup_type" in
            github|directory)
                if [ -n "$backup_value" ]; then
                    printf '%s' "$backup_value"
                    return 0
                fi
                ;;
        esac
    fi

    printf '%s' "$REMOTE_REPO"
}

run_real_bootstrap() {
    local marketplace_source="$1"
    CLAUDE_CONFIG_DIR="${REAL_CLAUDE_HOME}" \
    BOOTSTRAP_MARKETPLACE_SOURCE="${marketplace_source}" \
    bash "${REPO_ROOT}/bin/bootstrap.sh"
}

switch_to_local() {
    require_claude
    save_backup_if_needed
    echo "==> Switching real Claude environment to local source"
    run_real_bootstrap "$REPO_ROOT"
    print_status
}

switch_to_remote() {
    local remote_source
    require_claude
    remote_source="$(get_backup_source)"
    echo "==> Switching real Claude environment to published source: ${remote_source}"
    run_real_bootstrap "$remote_source"
    print_status
}

toggle_source() {
    local source_type source_value
    source_type="$(get_marketplace_source_type)"
    source_value="$(get_marketplace_source_value)"

    if [ "$source_type" = "directory" ] && [ "$source_value" = "$REPO_ROOT" ]; then
        switch_to_remote
    else
        switch_to_local
    fi
}

print_status() {
    local source installed_version install_path cache_version
    source="$(get_marketplace_display)"
    installed_version="$(get_installed_version)"
    install_path="$(get_install_path)"
    cache_version="$(get_cached_plugin_version "$install_path")"

    echo "Claude config dir: ${REAL_CLAUDE_HOME}"
    echo "Marketplace source:${source}"
    echo "Installed version: ${installed_version:-missing}"
    echo "Install path:      ${install_path:-missing}"
    echo "Cached plugin ver: ${cache_version:-missing}"
    echo "Backup file:       ${BACKUP_FILE}"
}

main() {
    local command="${1:-status}"
    case "$command" in
        local)  switch_to_local ;;
        remote) switch_to_remote ;;
        toggle) toggle_source ;;
        status) print_status ;;
        help|-h|--help) print_help ;;
        *)
            echo "Unknown command: ${command}" >&2
            echo "" >&2
            print_help >&2
            exit 1
            ;;
    esac
}

main "$@"
