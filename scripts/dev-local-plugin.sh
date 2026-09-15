#!/usr/bin/env bash
# Local development helper for Agent Notifications plugin installs/updates.
# Keeps user state and temporary files inside an explicit development sandbox.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MARKETPLACE_NAME="claude-notifications-go"
PLUGIN_NAME="claude-notifications-go"
PLUGIN_KEY="${PLUGIN_NAME}@${MARKETPLACE_NAME}"

DEFAULT_DEV_HOME="${HOME}/.claude-dev/claude-notifications-go"
if [ "${_DEV_ENV_READY:-}" != 1 ]; then
    # Ambient agent/config overrides must never select the developer's profile.
    dev_root="${DEV_CLAUDE_HOME:-$DEFAULT_DEV_HOME}"
    case "$dev_root" in
        /*) ;;
        *) echo "DEV_CLAUDE_HOME must be an absolute sandbox path" >&2; exit 1 ;;
    esac
    [ "$dev_root" != / ] && [ "$dev_root" != "$HOME" ] || exit 1
    platform_env=()
    for name in SystemRoot SYSTEMROOT WINDIR COMSPEC PATHEXT; do
        if [ -n "${!name:-}" ]; then platform_env+=("$name=${!name}"); fi
    done
    exec env -i PATH="$PATH" "${platform_env[@]}" bash --noprofile --norc -c '
        _DEV_ENV_READY=1
        source "$1/bin/test-env.sh"
        test_env_setup "$2/environment"
        export DEV_CLAUDE_HOME="$2" CLAUDE_HOME="$2" CLAUDE_CONFIG_DIR="$2"
        shift 2
        source "$0"
    ' "$0" "$REPO_ROOT" "$dev_root" "$@"
fi
DEV_CLAUDE_HOME="$CLAUDE_CONFIG_DIR"
DEFAULT_DEV_HOME="$DEV_CLAUDE_HOME"

INSTALLED_JSON="${DEV_CLAUDE_HOME}/plugins/installed_plugins.json"
KNOWN_MARKETPLACES_JSON="${DEV_CLAUDE_HOME}/plugins/known_marketplaces.json"
MARKETPLACE_PLUGIN_JSON="${DEV_CLAUDE_HOME}/plugins/marketplaces/${MARKETPLACE_NAME}/.claude-plugin/plugin.json"
REPO_PLUGIN_JSON="${REPO_ROOT}/.claude-plugin/plugin.json"

print_help() {
    cat <<EOF
Usage: scripts/dev-local-plugin.sh <command>

Commands:
  install    Add/update local marketplace and install/update the plugin
  update     Refresh local marketplace and update the installed plugin
  bootstrap  Run local bootstrap.sh against the isolated CLAUDE_CONFIG_DIR
  status     Show repo/marketplace/installed versions and paths
  reset      Delete the isolated CLAUDE_CONFIG_DIR
  help       Show this help

Environment:
  DEV_CLAUDE_HOME   Absolute sandbox root (Claude metadata plus environment/)

Default isolated config dir:
  ${DEFAULT_DEV_HOME}
EOF
}

run_claude() {
    CLAUDE_CONFIG_DIR="${DEV_CLAUDE_HOME}" claude "$@"
}

require_claude() {
    if ! command -v claude >/dev/null 2>&1; then
        echo "Error: claude CLI not found in PATH" >&2
        exit 1
    fi
}

json_query() {
    local file="$1"
    local expr="$2"

    [ -f "$file" ] || return 0

    if command -v jq >/dev/null 2>&1; then
        jq -r "$expr // empty" "$file" 2>/dev/null || true
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$file" "$expr" <<'PYEOF' 2>/dev/null || true
import json, sys

path = sys.argv[2].strip()
if not path:
    sys.exit(0)

try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
except Exception:
    sys.exit(0)

current = data
for part in path.split('.'):
    if not part:
        continue
    if isinstance(current, dict):
        current = current.get(part)
    else:
        current = None
    if current is None:
        sys.exit(0)

if isinstance(current, (dict, list)):
    sys.exit(0)

print(current)
PYEOF
        return 0
    fi

    if command -v node >/dev/null 2>&1; then
        node - "$file" "$expr" <<'JSEOF' 2>/dev/null || true
const fs = require('fs');
const file = process.argv[2];
const expr = process.argv[3];

try {
  let current = JSON.parse(fs.readFileSync(file, 'utf8'));
  for (const part of expr.split('.')) {
    if (!part) continue;
    current = current && typeof current === 'object' ? current[part] : undefined;
  }
  if (typeof current === 'string' || typeof current === 'number' || typeof current === 'boolean') {
    process.stdout.write(String(current));
  }
} catch (_) {}
JSEOF
        return 0
    fi
}

get_repo_version() {
    json_query "$REPO_PLUGIN_JSON" ".version"
}

get_marketplace_version() {
    json_query "$MARKETPLACE_PLUGIN_JSON" ".version"
}

get_marketplace_source_path() {
    [ -f "$KNOWN_MARKETPLACES_JSON" ] || return 0

    if command -v jq >/dev/null 2>&1; then
        jq -r ".[\"${MARKETPLACE_NAME}\"].source.path // empty" "$KNOWN_MARKETPLACES_JSON" 2>/dev/null || true
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$KNOWN_MARKETPLACES_JSON" "$MARKETPLACE_NAME" <<'PYEOF' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    entry = data.get(sys.argv[2], {})
    source = entry.get('source', {})
    path = source.get('path', '') if isinstance(source, dict) else ''
    if path:
        print(path)
except Exception:
    pass
PYEOF
    fi
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
        return 0
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
        return 0
    fi
}

ensure_dev_home() {
    mkdir -p "${DEV_CLAUDE_HOME}"
}

sync_local_marketplace() {
    echo "==> Syncing local marketplace from ${REPO_ROOT}"
    local output
    if output=$(run_claude plugin marketplace add "${REPO_ROOT}" </dev/null 2>&1); then
        echo "$output"
        return 0
    fi

    if echo "$output" | grep -qi "already"; then
        run_claude plugin marketplace update "${MARKETPLACE_NAME}" </dev/null
        return 0
    fi

    echo "$output" >&2
    exit 1
}

clear_plugin_cache() {
    local cache_dir="${DEV_CLAUDE_HOME}/plugins/cache/${MARKETPLACE_NAME}"
    if [ -d "$cache_dir" ]; then
        rm -rf "$cache_dir"
    fi
}

install_or_update_plugin() {
    local installed_version=""
    installed_version="$(get_installed_version)"
    clear_plugin_cache

    if [ -n "$installed_version" ]; then
        echo "==> Updating installed plugin (${installed_version})"
        run_claude plugin update "${PLUGIN_KEY}" </dev/null
    else
        echo "==> Installing plugin"
        run_claude plugin install "${PLUGIN_KEY}" </dev/null
    fi
}

print_status() {
    local repo_version marketplace_version marketplace_source installed_version install_path cache_version

    repo_version="$(get_repo_version)"
    marketplace_version="$(get_marketplace_version)"
    marketplace_source="$(get_marketplace_source_path)"
    installed_version="$(get_installed_version)"
    install_path="$(get_install_path)"
    cache_version=""
    if [ -n "$install_path" ] && [ -f "${install_path}/.claude-plugin/plugin.json" ]; then
        cache_version="$(json_query "${install_path}/.claude-plugin/plugin.json" ".version")"
    fi
    if [ -z "$marketplace_version" ] && [ -n "$marketplace_source" ]; then
        marketplace_version="${repo_version:-local-directory}"
    fi

    echo "Claude config dir: ${DEV_CLAUDE_HOME}"
    echo "Repo root:         ${REPO_ROOT}"
    echo "Repo version:      ${repo_version:-missing}"
    echo "Marketplace src:   ${marketplace_source:-missing}"
    echo "Marketplace file:  ${MARKETPLACE_PLUGIN_JSON}"
    echo "Marketplace ver:   ${marketplace_version:-missing}"
    echo "Installed ver:     ${installed_version:-missing}"
    echo "Install path:      ${install_path:-missing}"
    echo "Cached plugin ver: ${cache_version:-missing}"
    echo "Known marketplaces:${KNOWN_MARKETPLACES_JSON}"
}

run_local_bootstrap() {
    echo "==> Running local bootstrap.sh against isolated Claude config"
    CLAUDE_CONFIG_DIR="${DEV_CLAUDE_HOME}" \
    BOOTSTRAP_MARKETPLACE_SOURCE="${REPO_ROOT}" \
    INSTALL_SCRIPT_URL="file://${REPO_ROOT}/bin/install.sh" \
    bash "${REPO_ROOT}/bin/bootstrap.sh"
}

cmd_install() {
    require_claude
    ensure_dev_home
    sync_local_marketplace
    install_or_update_plugin
    print_status
}

cmd_update() {
    require_claude
    ensure_dev_home
    sync_local_marketplace
    clear_plugin_cache
    echo "==> Updating plugin from local marketplace"
    run_claude plugin update "${PLUGIN_KEY}" </dev/null
    print_status
}

cmd_bootstrap() {
    require_claude
    ensure_dev_home
    run_local_bootstrap
    print_status
}

cmd_reset() {
    echo "==> Removing ${DEV_CLAUDE_HOME}"
    if rm -rf "${DEV_CLAUDE_HOME}" 2>/dev/null; then
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "${DEV_CLAUDE_HOME}" <<'PYEOF'
import shutil, sys
shutil.rmtree(sys.argv[1], ignore_errors=True)
PYEOF
    fi
}

main() {
    local command="${1:-status}"
    case "$command" in
        install)   cmd_install ;;
        update)    cmd_update ;;
        bootstrap) cmd_bootstrap ;;
        status)    print_status ;;
        reset)     cmd_reset ;;
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
