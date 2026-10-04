#!/bin/bash
# bootstrap.sh - One-command install/update for claude-notifications plugin
# Usage: see https://777genius.github.io/agent-notifications/#install

set -euo pipefail

# Colors and formatting
BOLD='\033[1m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Constants
REPO="777genius/agent-notifications"
MARKETPLACE_SOURCE="${BOOTSTRAP_MARKETPLACE_SOURCE:-$REPO}"
MARKETPLACE_NAME="claude-notifications-go"
PLUGIN_NAME="claude-notifications-go"
PLUGIN_KEY="${PLUGIN_NAME}@${MARKETPLACE_NAME}"
# Public installs pair install.sh with the same published tag as the binary.
# main/bin/install.sh is only used for already-managed runtimes that already
# have an ownership ledger; never mix that writer with a pre-floor release.
INSTALL_SCRIPT_URL="${INSTALL_SCRIPT_URL:-}"
MANAGED_INSTALL_SCRIPT_URL="${MANAGED_INSTALL_SCRIPT_URL:-https://raw.githubusercontent.com/${REPO}/main/bin/install.sh}"
BOOTSTRAP_RAW_CONTENT_URL="${BOOTSTRAP_RAW_CONTENT_URL:-https://raw.githubusercontent.com/${REPO}}"

# Retired GitHub repo name(s) this marketplace was previously declared under.
# Users who added the marketplace before a rename have this baked into their
# settings; Claude Code refuses to silently re-point a declared marketplace
# at a different source, so `marketplace add` fails for them. See
# setup_marketplace() and docs/CLAUDE_PLUGIN_IDENTITY.md.
LEGACY_MARKETPLACE_REPOS="777genius/claude-notifications-go"

# Paths — CLAUDE_CONFIG_DIR is the official Claude Code env var;
# CLAUDE_HOME is a legacy fallback; default to ~/.claude. Git Bash on Windows
# may provide USERPROFILE without HOME, so resolve the installer home before
# expanding any default path while nounset is active.
INSTALLER_HOME="${HOME:-${USERPROFILE:-}}"
CLAUDE_HOME="${CLAUDE_CONFIG_DIR:-${CLAUDE_HOME:-}}"
if [ -z "$CLAUDE_HOME" ] && [ -z "$INSTALLER_HOME" ]; then
    echo "HOME or USERPROFILE must be set to locate the Claude configuration." >&2
    exit 1
fi
if [ -z "$CLAUDE_HOME" ]; then
    CLAUDE_HOME="$INSTALLER_HOME/.claude"
fi
DEFAULT_CODEX_HOME="${CODEX_HOME:-${INSTALLER_HOME:+$INSTALLER_HOME/.codex}}"
INSTALLED_JSON="${CLAUDE_HOME}/plugins/installed_plugins.json"
CACHE_DIR="${CLAUDE_HOME}/plugins/cache/${MARKETPLACE_NAME}"
MARKETPLACE_DIR="${CLAUDE_HOME}/plugins/marketplaces/${MARKETPLACE_NAME}"
MARKETPLACE_PLUGIN_JSON="${MARKETPLACE_DIR}/.claude-plugin/plugin.json"

# State
PLUGIN_ROOT=""
_BOOTSTRAP_STAGE=""
_CONFIG_STAGE=""
_CONFIG_HELPER=""
_PORTABLE_STAGE=""
_KEEP_CONFIG_STAGE=false
_KEEP_PORTABLE_STAGE=false
PRODUCT=""
BOOTSTRAP_TAG=""
BOOTSTRAP_COMMIT=""
BOOTSTRAP_BUNDLE_COMMIT=""
_BOOTSTRAP_TMP=""  # temp file path for trap (set -u safe)
CONFIGURE_NOTIFICATIONS=true
AGENT_NOTIFY_REQUEST=auto
CONFIGURE_BINARY=""
CONFIGURE_ARGS=()
OPENCODE_ARGS=()
CLAUDE_AGENT_NOTIFY_STATUS="not configured by this run"
CODEX_AGENT_NOTIFY_STATUS="not configured by this run"
SELECTED_PRODUCTS=()
LEGACY_PRODUCT=""
_SELECTION_PENDING=false
_CHANNEL_PENDING=false
_INTERACTIVE_INTENT=false
_PROMPT_OPEN=false
UI_ARGS=()
_UI_MODE=""
_SELECTOR_INTENT=""
_FROZEN_CONTROL=""
_FROZEN_RUNTIME=""
_FROZEN_CLAUDE_MCP=""
_FROZEN_CODEX_MCP=""
_FROZEN_OPENCODE=""
_FROZEN_GEMINI=""
_CLAUDE_EXEC=claude
_CODEX_EXEC=codex
_OPENCODE_EXEC=opencode
_GEMINI_EXEC=gemini
_PROGRESS_IDS=()
_PROGRESS_PHASES=()
_PROGRESS_OUTCOMES=()

# Isolated JSON/checksum runtime. Prefer python3 -I; Node is the supported
# fallback because Claude Code already ships it. iTerm2 venv still needs a
# real Python interpreter and stays optional.
# Presence on PATH is not enough: Windows Store/WSL python3 stubs must not win.
usable_python3() {
    command -v python3 >/dev/null 2>&1 || return 1
    python3 -I -c 'import json' </dev/null >/dev/null 2>&1
}

usable_node() {
    command -v node >/dev/null 2>&1 || return 1
    NODE_OPTIONS= NODE_PATH= node --no-warnings -e 'JSON.parse("{}")' </dev/null >/dev/null 2>&1
}

installer_runtime() {
    if usable_python3; then
        printf '%s\n' python3
        return 0
    fi
    if usable_node; then
        printf '%s\n' node
        return 0
    fi
    return 1
}

require_installer_runtime() {
    installer_runtime >/dev/null || {
        echo "python3 or node is required for protected installer metadata and checksum validation." >&2
        return 1
    }
}

run_isolated_node() {
    NODE_OPTIONS= NODE_PATH= node --no-warnings "$@"
}

# ──────────────────────────────────────────────

print_header() {
    echo ""
    echo -e "${BOLD}============================================${NC}"
    echo -e "${BOLD} Agent Notifications — Bootstrap Installer${NC}"
    echo -e "${BOLD}============================================${NC}"
    echo ""
}

# ──────────────────────────────────────────────

is_wsl_environment() {
    case "${CLAUDE_NOTIFICATIONS_ALLOW_WSL:-}" in
        1|true|TRUE|yes|YES) return 1 ;;
    esac

    local os
    os="$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]' || true)"
    [ "$os" = "linux" ] || return 1

    [ -n "${WSL_DISTRO_NAME:-}" ] && return 0
    [ -n "${WSL_INTEROP:-}" ] && return 0

    if [ -r /proc/version ] && grep -qiE 'microsoft|wsl' /proc/version 2>/dev/null; then
        return 0
    fi

    return 1
}

abort_if_wsl_environment() {
    is_wsl_environment || return 0

    echo -e "${RED}✗ WSL environment detected${NC}" >&2
    echo "" >&2
    echo -e "${YELLOW}This command is running inside WSL, so it would install Linux binaries under /home instead of Windows binaries.${NC}" >&2
    echo -e "${YELLOW}If you started this from PowerShell or Windows Terminal, your bash command is probably WSL bash, not Git Bash.${NC}" >&2
    echo "" >&2
    echo -e "${YELLOW}For Windows Claude, open Git Bash and use the installer at:${NC}" >&2
    echo -e "  https://777genius.github.io/agent-notifications/#install" >&2
    echo "" >&2
    echo -e "${YELLOW}For an intentional WSL install, set CLAUDE_NOTIFICATIONS_ALLOW_WSL=1 on the final bash command.${NC}" >&2
    echo "" >&2
    exit 1
}

# ──────────────────────────────────────────────

check_prerequisites() {
    if { [ "${PRODUCT:-claude}" = claude ] || [ "$PRODUCT" = both ]; } && ! command -v "$_CLAUDE_EXEC" &>/dev/null; then
        echo -e "${RED}✗ claude CLI not found in PATH${NC}" >&2
        echo "" >&2
        echo -e "${YELLOW}Install Claude first:${NC}" >&2
        echo -e "  npm install -g @anthropic-ai/claude-code" >&2
        echo "" >&2
        exit 1
    fi
    if { [ "${PRODUCT:-claude}" = codex ] || [ "$PRODUCT" = both ]; } && ! command -v "$_CODEX_EXEC" &>/dev/null; then
        echo "codex CLI not found in PATH; install Codex first." >&2
        exit 1
    fi
    if { [ "${PRODUCT:-claude}" = codex ] || [ "$PRODUCT" = both ]; } && ! command -v tar &>/dev/null; then
        echo "tar is required for the Codex source bundle." >&2
        exit 1
    fi

    if [ "$PRODUCT" = opencode ] && ! command -v "$_OPENCODE_EXEC" >/dev/null 2>&1; then
        echo "opencode CLI not found in PATH; install OpenCode first." >&2
        exit 1
    fi
    if [ "$PRODUCT" = opencode ]; then
        local host_version opencode_cli probe probe_status=0
        opencode_cli=$(command -v "$_OPENCODE_EXEC") || return 1
        case "$opencode_cli" in /*) ;; *) opencode_cli="$PWD/$opencode_cli" ;; esac
        probe=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-opencode-TEST-XXXXXX") || return 1
        mkdir -p "$probe/profile" "$probe/tmp" || { rm -rf "$probe"; return 1; }
        host_version=$(cd "$probe/profile" && env -i PATH="$PATH" HOME="$probe/profile" USERPROFILE="$probe/profile" \
            TMPDIR="$probe/tmp" XDG_CONFIG_HOME="$probe/profile/.config" OPENCODE_CONFIG_DIR="$probe/profile/opencode" \
            "$opencode_cli" --version </dev/null) || probe_status=$?
        rm -rf "$probe"
        [ "$probe_status" -eq 0 ] || { echo "Cannot determine OpenCode version." >&2; return 1; }
        if [[ "$host_version" =~ (^|[^0-9])v?([0-9]+)\.([0-9]+)\.([0-9]+)($|[^0-9]) ]] && [ "${BASH_REMATCH[2]}" = 1 ]; then
            echo "OpenCode notifications were tested with 1.18.33; detected $host_version."
        else
            echo "Unsupported OpenCode version. Tested host: 1.18.33; V2 is not supported. Detected: $host_version" >&2
            exit 1
        fi
        [ "$(bootstrap_release_os_arch)" != "windows arm64" ] || { echo "Windows arm64 is not supported." >&2; exit 1; }
    fi
    if [ "$PRODUCT" = gemini ]; then
        local gemini_cli probe host_version probe_status
        gemini_cli=$(command -v "$_GEMINI_EXEC") || { echo "gemini CLI not found in PATH; install Gemini CLI first." >&2; return 1; }
        probe=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-gemini-TEST-XXXXXX") || return 1
        mkdir -p "$probe/profile/.gemini" "$probe/tmp" || { rm -rf "$probe"; return 1; }
        # Native env discovery checks Gemini/home sentinels even when the
        # caller's environment is empty. Keep cwd and home in the same TEST root.
        : > "$probe/profile/.gemini/.env"
        : > "$probe/profile/.env"
        printf '{}\n' > "$probe/settings.json"
        printf '{}\n' > "$probe/defaults.json"
        printf '{}\n' > "$probe/trust.json"
        probe_status=0
        host_version=$(cd "$probe/profile" && env -i PATH="$PATH" HOME="$probe/profile" USERPROFILE="$probe/profile" \
            GEMINI_CLI_HOME="$probe/profile" TMPDIR="$probe/tmp" XDG_CONFIG_HOME="$probe/profile/.config" \
            GEMINI_CLI_SYSTEM_SETTINGS_PATH="$probe/settings.json" GEMINI_CLI_SYSTEM_DEFAULTS_PATH="$probe/defaults.json" \
            GEMINI_CLI_TRUSTED_FOLDERS_PATH="$probe/trust.json" "$gemini_cli" --version </dev/null) || probe_status=$?
        rm -rf "$probe"
        [ "$probe_status" -eq 0 ] && [ "$host_version" = 0.62.0 ] || {
            echo "Gemini CLI notifications target 0.62.0; detected ${host_version:-unavailable}." >&2; return 1;
        }
        [ "$(bootstrap_release_os_arch)" != "windows arm64" ] || { echo "Windows arm64 is not supported." >&2; return 1; }
    fi
    if { [ "$PRODUCT" = opencode ] || [ "$PRODUCT" = gemini ]; } && [ "$(uname -s)" = Darwin ] &&
        [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop "* ]] && ! command -v unzip >/dev/null 2>&1; then
        echo "unzip is required for the signed macOS notification helper." >&2
        exit 1
    fi

    if ! command -v curl &>/dev/null && ! command -v wget &>/dev/null; then
        echo -e "${RED}✗ curl or wget required${NC}" >&2
        exit 1
    fi
}

# ──────────────────────────────────────────────

detect_platform() {
    local os
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"

    case "$os" in
        darwin)  PLATFORM="macOS" ;;
        linux)   PLATFORM="Linux" ;;
        mingw*|msys*|cygwin*) PLATFORM="Windows (Git Bash)" ;;
        *)       PLATFORM="$os" ;;
    esac

    echo -e "${BLUE}Platform:${NC} ${PLATFORM}" >&2
}

# ──────────────────────────────────────────────

is_iterm2_detected() {
    [ "$(uname -s)" = "Darwin" ] || return 1

    [ "${TERM_PROGRAM:-}" = "iTerm.app" ] && return 0
    [ "${__CFBundleIdentifier:-}" = "com.googlecode.iterm2" ] && return 0
    [ -d "/Applications/iTerm.app" ] && return 0
    [ -n "$INSTALLER_HOME" ] && [ -d "$INSTALLER_HOME/Applications/iTerm.app" ] && return 0

    return 1
}

# ──────────────────────────────────────────────

print_iterm2_python_api_notice() {
    is_iterm2_detected || return 0

    echo ""
    echo -e "${YELLOW}────────────────────────────────────────────${NC}"
    echo -e "${YELLOW}⚠${NC} ${BOLD}iTerm2 detected${NC}"
    echo -e "  To open the ${BOLD}exact iTerm2 tab / split pane${NC} on notification click:"
    echo -e "  1. Open ${BOLD}iTerm2${NC}"
    echo -e "  2. Go to ${BOLD}Settings → General → Magic${NC}"
    echo -e "  3. Enable ${BOLD}Python API${NC}"
    echo -e "  4. If you just toggled it, ${BOLD}restart iTerm2 once${NC}"
    echo -e "${YELLOW}────────────────────────────────────────────${NC}"
}

# ──────────────────────────────────────────────

# Reports (on stdout) the repo currently declared in settings for
# $MARKETPLACE_NAME, or nothing if it isn't declared / can't be read.
marketplace_declared_repo() {
    local tmp
    tmp=$(mktemp "${TMPDIR:-/tmp}/marketplace-list-XXXXXX") || return 1
    "$_CLAUDE_EXEC" plugin marketplace list --json </dev/null >"$tmp" 2>/dev/null
    "$_CONFIG_HELPER" config installer marketplace "$tmp" "$MARKETPLACE_NAME" || true
    rm -f "$tmp"
}
is_legacy_marketplace_repo() {
    local repo="$1" candidate
    for candidate in $LEGACY_MARKETPLACE_REPOS; do
        [ "$repo" = "$candidate" ] && return 0
    done
    return 1
}

platform_channel_claude() {
    [ "${BOOTSTRAP_RELEASE_CHANNEL:-}" = 1 ] && [ "$MARKETPLACE_SOURCE" = "$REPO" ]
}

channel_refuse_downgrade() {
    local installed="${1#v}" selected="${2#v}" i part
    local before_parts=() after_parts=()
    IFS=. read -r -a before_parts <<< "$installed"
    IFS=. read -r -a after_parts <<< "$selected"
    [ "${#before_parts[@]}" = 3 ] && [ "${#after_parts[@]}" = 3 ] || return 1
    for i in 0 1 2; do
        part=${before_parts[$i]}
        [[ "$part" =~ ^(0|[1-9][0-9]*)$ ]] && [ "${#part}" -le 9 ] || return 1
        [ "$part" -le "${after_parts[$i]}" ] || {
            echo 'Platform channel is older than the installed version; installation retained.' >&2; return 1;
        }
        [ "$part" -eq "${after_parts[$i]}" ] || return 0
    done
}

channel_marketplace_add() {
    local ref="$1" declared overlay registry checkout_ref installed_root
    case "$ref" in
        release/platform-macos|release/platform-linux-windows) ;;
        *) printf '%s\n' "$ref" | grep -Eq '^dist/platform-source/[0-9a-f]{40}$' || return 1 ;;
    esac
    config_preflight || return 1
    local installed
    installed=$(get_installed_plugin_version) || return 1
    installed=${installed#v}
    case "$installed" in
        ''|unknown)
            installed_root=$(get_installed_plugin_root) || return 1
            [ -z "$installed_root" ] || installed=$(get_manifest_version "$installed_root/.claude-plugin/plugin.json") ;;
    esac
    if [ -n "$installed" ]; then
        channel_refuse_downgrade "$installed" "$BOOTSTRAP_TAG" || return 1
    fi
    registry="$_CONFIG_STAGE/marketplaces-before.json"
    "$_CLAUDE_EXEC" plugin marketplace list --json </dev/null > "$registry" || return 1
    declared=$("$_CONFIG_HELPER" config installer marketplace "$registry" "$MARKETPLACE_NAME") || return 1
    if [ "${_CHANNEL_ADOPTED:-}" != 1 ]; then
        if [ -z "$declared" ]; then
            # Native list output encodes this fixed ASCII identity literally.
            # An unclassified declaration is custom, never permission to replace.
            if grep -Fq "$MARKETPLACE_NAME" "$registry" || [ -e "$MARKETPLACE_DIR" ]; then
                echo 'Unclassified marketplace declaration retained.' >&2; return 1
            fi
        else
            [ "$declared" = "$REPO" ] || { echo 'Custom marketplace retained.' >&2; return 1; }
            checkout_ref=$(git -C "$MARKETPLACE_DIR" symbolic-ref --quiet --short HEAD) || {
                local snapshot_tag checkout_commit
                checkout_commit=$(git -C "$MARKETPLACE_DIR" rev-parse HEAD) || return 1
                snapshot_tag=$(git -C "$MARKETPLACE_DIR" describe --exact-match --tags HEAD) || return 1
                [ "$snapshot_tag" = "dist/platform-source/$checkout_commit" ] || {
                    echo 'Pinned marketplace retained.' >&2; return 1;
                }
                checkout_ref="$BOOTSTRAP_SOURCE_REF"
            }
            case "$checkout_ref" in
                main|release/platform-macos|release/platform-linux-windows) ;;
                *) echo 'Custom marketplace branch retained.' >&2; return 1 ;;
            esac
        fi
    fi
    overlay="$_CONFIG_STAGE/channel-settings.json"
    # Only this controlled declaration is supplied. Claude's native settings
    # writer preserves unrelated settings, policy, plugin data and enabled state.
    printf '{"extraKnownMarketplaces":{"%s":{"source":{"source":"github","repo":"%s","ref":"%s"}}}}\n' "$MARKETPLACE_NAME" "$REPO" "$ref" > "$overlay"
    "$_CLAUDE_EXEC" --settings "$overlay" plugin marketplace add "$REPO#$ref" </dev/null || return 1
    _CHANNEL_ADOPTED=1
}

channel_install_plugin() (
    # A version number alone is not a source generation. Claude reuses an
    # existing same-version cache, so retain it until the native CLI has copied
    # the complete qualified checkout. Never uninstall or remove a marketplace.
    local old
    root=""; backup=""; failed=""
    old=$(get_installed_plugin_version) || return 1
    root=$(get_installed_plugin_root) || return 1
    old=${old#v}
    case "$old" in
        ''|unknown) [ -z "$root" ] || old=$(get_manifest_version "$root/.claude-plugin/plugin.json") ;;
    esac
    config_preflight || return 1
    channel_restore_cache() {
        if [ -n "$backup" ] && [ -d "$backup" ]; then
            if [ -e "$root" ]; then
                failed=$(mktemp -d "$CACHE_DIR/$MARKETPLACE_NAME/.channel-incomplete.XXXXXX") || return 1
                rmdir "$failed" && mv "$root" "$failed" || return 1
                echo "Incomplete cache retained: $failed" >&2
            fi
            mv "$backup" "$root" || return 1
        fi
    }
    trap channel_restore_cache EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    if [ "$old" = "${BOOTSTRAP_TAG#v}" ] && [ -n "$root" ]; then
        case "$root" in "$CACHE_DIR/$MARKETPLACE_NAME/$old") ;; *) echo 'Unrecognized cache root; retaining installation.' >&2; return 1 ;; esac
        [ -d "$root" ] && [ ! -L "$root" ] || return 1
        backup_parent=$(mktemp -d "$CACHE_DIR/$MARKETPLACE_NAME/.channel-backup.XXXXXX") || return 1
        backup="$backup_parent/cache"
        mv "$root" "$backup" || return 1
    fi
    if [ -n "$old$root" ]; then
        "$_CLAUDE_EXEC" plugin update "$PLUGIN_KEY" </dev/null || return 1
    else
        "$_CLAUDE_EXEC" plugin install "$PLUGIN_KEY" </dev/null || return 1
    fi
    verify_installed_plugin_version "${BOOTSTRAP_TAG#v}" || return 1
    if [ -n "$backup" ]; then
        # Keep the existing runtime's exact fingerprints until its native SDK
        # promotes replacements. This includes launcher links and skill0600;
        # Git's ordinary source copy cannot reproduce their owned state.
        local existing relative
        for existing in "$backup"/bin/claude-notifications* "$backup"/bin/agent-notifications* \
            "$backup"/bin/sound-preview* "$backup"/bin/list-devices* "$backup"/bin/list-sounds* \
            "$backup"/bin/ClaudeNotifier.app "$backup"/bin/AgentNotifications.app "$backup"/bin/terminal-notifier.app \
            "$backup"/skills/agent-notifications/SKILL.md "$backup"/skills/agent-notify/SKILL.md; do
            [ -e "$existing" ] || [ -L "$existing" ] || continue
            relative=${existing#"$backup"/}
            config_preflight || return 1
            mkdir -p "$(dirname "$root/$relative")" || return 1
            rm -rf -- "$root/$relative" || return 1
            cp -pPR "$existing" "$root/$relative" || return 1
        done
        case "$(uname -s)" in
            MINGW*|MSYS*|CYGWIN*)
                config_preflight || return 1
                cp -p "$backup/hooks/hooks.json" "$root/hooks/hooks.json" || return 1 ;;
        esac
    fi
    # Finish native promotion while the old same-version cache is recoverable.
    find_plugin_root || return 1
    download_binary || return 1
    [ -z "$backup" ] || echo "Previous source cache retained for recovery: $backup"
    backup=""
)

setup_marketplace() {
    echo ""
    echo -e "${BLUE}📦 Setting up marketplace...${NC}"

    config_preflight || return 1
    local output
    if platform_channel_claude; then
        channel_marketplace_add "dist/platform-source/$BOOTSTRAP_BUNDLE_COMMIT"
        return $?
    fi
    # Try adding marketplace — if already added, update instead
    # </dev/null prevents stdin conflicts when running via `curl | bash`
    if output=$("$_CLAUDE_EXEC" plugin marketplace add "$MARKETPLACE_SOURCE" </dev/null 2>&1); then
        echo -e "${GREEN}✓${NC} Marketplace added"
    else
        if echo "$output" | grep -qi "already"; then
            echo -e "${BLUE}  Marketplace already added, updating...${NC}"
            config_preflight || return 1
            if "$_CLAUDE_EXEC" plugin marketplace update "$MARKETPLACE_NAME" </dev/null 2>&1; then
                echo -e "${GREEN}✓${NC} Marketplace updated"
            else
                # Update may fail if already up-to-date — that's OK
                echo -e "${GREEN}✓${NC} Marketplace is up to date"
            fi
        elif echo "$output" | grep -qi "source differs" \
            && is_legacy_marketplace_repo "$(marketplace_declared_repo)"; then
            # Declared under a repo name we ourselves retired (rename
            # migration). Claude Code won't silently re-point an existing
            # declaration, and there's no other transparent migration path
            # for a marketplace source change, so re-register it: this
            # only drops the marketplace/plugin *registration*, not the
            # user's saved notification settings (a separate file), and
            # the rest of this script reinstalls the plugin right after.
            echo -e "${BLUE}  Marketplace points at the retired repo name; re-registering...${NC}"
            "$_CLAUDE_EXEC" plugin marketplace remove "$MARKETPLACE_NAME" </dev/null >/dev/null 2>&1 || true
            config_preflight || return 1
            if output=$("$_CLAUDE_EXEC" plugin marketplace add "$MARKETPLACE_SOURCE" </dev/null 2>&1); then
                echo -e "${GREEN}✓${NC} Marketplace re-registered"
            else
                echo -e "${YELLOW}⚠ Marketplace add output: ${output}${NC}"
                echo -e "${YELLOW}  Continuing anyway...${NC}"
            fi
        else
            echo -e "${YELLOW}⚠ Marketplace add output: ${output}${NC}"
            echo -e "${YELLOW}  Continuing anyway...${NC}"
        fi
    fi
}

# ──────────────────────────────────────────────

get_manifest_version() {
    local manifest_path="$1"
    [ -f "$manifest_path" ] || return 0
    grep -Eo '"version"[[:space:]]*:[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"' "$manifest_path" 2>/dev/null \
        | head -n 1 \
        | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' || true
}

get_installed_plugin_version() {
    [ -f "$INSTALLED_JSON" ] || return 0
    if [ -n "${_CONFIG_HELPER:-}" ]; then
        "$_CONFIG_HELPER" config installer version "$INSTALLED_JSON" "$PLUGIN_KEY"
        return
    fi

    if command -v jq &>/dev/null; then
        PLUGIN_KEY="$PLUGIN_KEY" jq -r '
          (.plugins[env.PLUGIN_KEY] // []) as $entries
          | ($entries | map(select((.installPath // "") != ""))) as $with_paths
          | (if ($with_paths | length) == 0 then $entries else $with_paths end) as $candidates
          | ($candidates | map(select(
              ((.version // "") | ascii_downcase | ltrimstr("v")) as $version
              | $version == "" or $version == "unknown"
            ))) as $placeholders
          | if ($placeholders | length) > 0 then $placeholders[-1]
            else ($candidates
              | sort_by((.version // "0.0.0") | split(".") | map(tonumber? // 0))
              | if length == 0 then {} else .[-1] end)
            end
          | .version // empty
        ' "$INSTALLED_JSON" 2>/dev/null || true
        return 0
    fi

    if usable_python3; then
        python3 - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
import json, sys
def ver_tuple(value):
    try:
        parts = str(value or "0.0.0").split(".")
        parts = (parts + ["0", "0", "0"])[:3]
        return tuple(int(p) for p in parts)
    except Exception:
        return (0, 0, 0)
def is_placeholder(value):
    normalized = str(value or '').lower()
    if normalized.startswith('v'):
        normalized = normalized[1:]
    return normalized in ('', 'unknown')
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    entries = d.get('plugins', {}).get(sys.argv[2], [])
    if entries:
        with_paths = [e for e in entries if isinstance(e, dict) and e.get('installPath')]
        candidates = with_paths or [e for e in entries if isinstance(e, dict)]
        if candidates:
            placeholders = [e for e in candidates if is_placeholder(e.get('version'))]
            best = placeholders[-1] if placeholders else max(candidates, key=lambda e: ver_tuple(e.get('version')))
            print(best.get('version', '') or '')
except Exception:
    pass
PYEOF
        return 0
    fi

    if usable_node; then
        PLUGIN_KEY="$PLUGIN_KEY" run_isolated_node - "$INSTALLED_JSON" <<'JSEOF' 2>/dev/null || true
const fs = require('fs');
function parseVersion(value) {
  return String(value || '0.0.0')
    .split('.')
    .slice(0, 3)
    .map((part) => {
      const n = parseInt(part, 10);
      return Number.isFinite(n) ? n : 0;
    });
}
function compareVersions(a, b) {
  const av = parseVersion(a && a.version);
  const bv = parseVersion(b && b.version);
  for (let i = 0; i < 3; i += 1) {
    if (av[i] !== bv[i]) return av[i] - bv[i];
  }
  return 0;
}
function isPlaceholder(value) {
  const normalized = String(value || '').toLowerCase().replace(/^v/, '');
  return normalized === '' || normalized === 'unknown';
}
try {
  const installedPath = process.argv[2];
  const pluginKey = process.env.PLUGIN_KEY;
  const data = JSON.parse(fs.readFileSync(installedPath, 'utf8'));
  const entries = ((data.plugins || {})[pluginKey] || []).filter((entry) => entry && typeof entry === 'object');
  const candidates = entries.filter((entry) => entry.installPath) || entries;
  const pool = candidates.length > 0 ? candidates : entries;
  if (pool.length > 0) {
    const placeholders = pool.filter((entry) => isPlaceholder(entry.version));
    const best = placeholders.length > 0
      ? placeholders[placeholders.length - 1]
      : pool.slice().sort(compareVersions).pop();
    process.stdout.write(String((best && best.version) || ''));
  }
} catch (_) {}
JSEOF
        return 0
    fi

    grep -A6 "\"${PLUGIN_KEY}\"" "$INSTALLED_JSON" 2>/dev/null \
        | grep -Eo '"version"[[:space:]]*:[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"' \
        | tail -n 1 \
        | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' || true
}

get_installed_plugin_root() {
    [ -f "$INSTALLED_JSON" ] || return 0
    if [ -n "${_CONFIG_HELPER:-}" ]; then
        "$_CONFIG_HELPER" config installer root "$INSTALLED_JSON" "$PLUGIN_KEY"
        return
    fi

    if command -v jq &>/dev/null; then
        PLUGIN_KEY="$PLUGIN_KEY" jq -r '
          (.plugins[env.PLUGIN_KEY] // [])
          | map(select((.installPath // "") != "")) as $candidates
          | ($candidates | map(select(
              ((.version // "") | ascii_downcase | ltrimstr("v")) as $version
              | $version == "" or $version == "unknown"
            ))) as $placeholders
          | if ($placeholders | length) > 0 then $placeholders[-1]
            else ($candidates
              | sort_by((.version // "0.0.0") | split(".") | map(tonumber? // 0))
              | if length == 0 then {} else .[-1] end)
            end
          | .installPath // empty
        ' "$INSTALLED_JSON" 2>/dev/null || true
        return 0
    fi

    if usable_python3; then
        python3 - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
import json, sys
def ver_tuple(value):
    try:
        parts = str(value or "0.0.0").split(".")
        parts = (parts + ["0", "0", "0"])[:3]
        return tuple(int(p) for p in parts)
    except Exception:
        return (0, 0, 0)
def is_placeholder(value):
    normalized = str(value or '').lower()
    if normalized.startswith('v'):
        normalized = normalized[1:]
    return normalized in ('', 'unknown')
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    entries = d.get('plugins', {}).get(sys.argv[2], [])
    candidates = [e for e in entries if isinstance(e, dict) and e.get('installPath')]
    if candidates:
        placeholders = [e for e in candidates if is_placeholder(e.get('version'))]
        best = placeholders[-1] if placeholders else max(candidates, key=lambda e: ver_tuple(e.get('version')))
        print(best.get('installPath', '') or '')
except Exception:
    pass
PYEOF
        return 0
    fi

    if usable_node; then
        PLUGIN_KEY="$PLUGIN_KEY" run_isolated_node - "$INSTALLED_JSON" <<'JSEOF' 2>/dev/null || true
const fs = require('fs');
function parseVersion(value) {
  return String(value || '0.0.0')
    .split('.')
    .slice(0, 3)
    .map((part) => {
      const n = parseInt(part, 10);
      return Number.isFinite(n) ? n : 0;
    });
}
function compareVersions(a, b) {
  const av = parseVersion(a && a.version);
  const bv = parseVersion(b && b.version);
  for (let i = 0; i < 3; i += 1) {
    if (av[i] !== bv[i]) return av[i] - bv[i];
  }
  return 0;
}
function isPlaceholder(value) {
  const normalized = String(value || '').toLowerCase().replace(/^v/, '');
  return normalized === '' || normalized === 'unknown';
}
try {
  const installedPath = process.argv[2];
  const pluginKey = process.env.PLUGIN_KEY;
  const data = JSON.parse(fs.readFileSync(installedPath, 'utf8'));
  const entries = ((data.plugins || {})[pluginKey] || [])
    .filter((entry) => entry && typeof entry === 'object' && entry.installPath);
  if (entries.length > 0) {
    const placeholders = entries.filter((entry) => isPlaceholder(entry.version));
    const best = placeholders.length > 0
      ? placeholders[placeholders.length - 1]
      : entries.slice().sort(compareVersions).pop();
    process.stdout.write(String((best && best.installPath) || ''));
  }
} catch (_) {}
JSEOF
        return 0
    fi

    grep -o '"installPath"[[:space:]]*:[[:space:]]*"[^"]*'"${MARKETPLACE_NAME}"'[^"]*"' "$INSTALLED_JSON" 2>/dev/null \
        | tail -n 1 \
        | sed 's/"installPath"[[:space:]]*:[[:space:]]*"//;s/"$//' || true
}

sync_marketplace_checkout() {
    config_preflight || return 1
    echo ""
    echo -e "${BLUE}🔄 Syncing marketplace checkout...${NC}"

    if platform_channel_claude; then
        [ "$(git -C "$MARKETPLACE_DIR" rev-parse HEAD)" = "$BOOTSTRAP_BUNDLE_COMMIT" ] &&
            [ "$(get_manifest_version "$MARKETPLACE_PLUGIN_JSON")" = "${BOOTSTRAP_TAG#v}" ] || {
            echo 'Platform source checkout does not match the qualified snapshot.' >&2; return 1;
        }
        return 0
    fi
    if [ "$MARKETPLACE_SOURCE" != "$REPO" ]; then
        echo -e "${BLUE}  Using custom marketplace source; skipping direct git sync${NC}"
        return 0
    fi

    if [ ! -d "$MARKETPLACE_DIR/.git" ]; then
        echo -e "${YELLOW}  Marketplace checkout not found yet; continuing${NC}"
        return 0
    fi

    if ! command -v git &>/dev/null; then
        echo -e "${YELLOW}  git not found; skipping marketplace checkout sync${NC}"
        return 0
    fi

    local remote_url=""
    remote_url=$(git -C "$MARKETPLACE_DIR" remote get-url origin 2>/dev/null || true)
    case "$remote_url" in
        *"${REPO}"*)
            ;;
        *)
            echo -e "${YELLOW}  Marketplace remote does not match ${REPO}; skipping direct sync${NC}"
            return 0
            ;;
    esac

    local before_version=""
    local after_version=""
    before_version=$(get_manifest_version "$MARKETPLACE_PLUGIN_JSON")

    local is_shallow="false"
    is_shallow=$(git -C "$MARKETPLACE_DIR" rev-parse --is-shallow-repository 2>/dev/null || echo "false")

    if [ "$is_shallow" = "true" ]; then
        if git -C "$MARKETPLACE_DIR" fetch --unshallow origin main >/dev/null 2>&1; then
            :
        elif git -C "$MARKETPLACE_DIR" fetch --deepen=50 origin main >/dev/null 2>&1; then
            :
        elif ! git -C "$MARKETPLACE_DIR" fetch origin main >/dev/null 2>&1; then
            echo -e "${YELLOW}  Could not refresh shallow marketplace checkout; keeping existing checkout${NC}"
            return 0
        fi
    elif ! git -C "$MARKETPLACE_DIR" fetch origin main >/dev/null 2>&1; then
        echo -e "${YELLOW}  Could not fetch marketplace checkout; keeping existing checkout${NC}"
        return 0
    fi

    config_preflight || return 1
    if git -C "$MARKETPLACE_DIR" checkout -q main >/dev/null 2>&1 && \
       git -C "$MARKETPLACE_DIR" merge --ff-only FETCH_HEAD >/dev/null 2>&1; then
        after_version=$(get_manifest_version "$MARKETPLACE_PLUGIN_JSON")
        if [ -n "$after_version" ] && [ "$after_version" != "$before_version" ]; then
            echo -e "${GREEN}✓${NC} Marketplace checkout updated to v${after_version}"
        elif [ -n "$after_version" ]; then
            echo -e "${GREEN}✓${NC} Marketplace checkout already at v${after_version}"
        else
            echo -e "${GREEN}✓${NC} Marketplace checkout synced"
        fi
        return 0
    fi

    local current_head=""
    local fetched_head=""
    current_head=$(git -C "$MARKETPLACE_DIR" rev-parse --short HEAD 2>/dev/null || true)
    fetched_head=$(git -C "$MARKETPLACE_DIR" rev-parse --short FETCH_HEAD 2>/dev/null || true)

    if [ -n "$current_head" ] && [ "$current_head" = "$fetched_head" ]; then
        after_version=$(get_manifest_version "$MARKETPLACE_PLUGIN_JSON")
        if [ -n "$after_version" ]; then
            echo -e "${GREEN}✓${NC} Marketplace checkout already at v${after_version}"
        else
            echo -e "${GREEN}✓${NC} Marketplace checkout already current"
        fi
        return 0
    fi

    echo -e "${YELLOW}  Marketplace checkout fetch succeeded but fast-forward merge failed; keeping existing checkout${NC}"
    return 0
}

verify_installed_plugin_version() {
    local expected_version="$1"
    [ -n "$expected_version" ] || return 0

    local installed_version=""
    installed_version=$(get_installed_plugin_version)
    if [ "$installed_version" = "$expected_version" ]; then
        return 0
    fi

    # Claude Code can record a successfully installed plugin with an unknown or
    # empty registry version. In that case, verify the files at the selected
    # installPath instead of treating the registry placeholder as authoritative.
    case "${installed_version#v}" in
        ""|unknown)
            local installed_root=""
            local manifest_version=""
            installed_root=$(get_installed_plugin_root)
            if [ -n "$installed_root" ]; then
                manifest_version=$(get_manifest_version "${installed_root}/.claude-plugin/plugin.json")
            fi
            [ "$manifest_version" = "$expected_version" ]
            ;;
        *)
            return 1
            ;;
    esac
}

# ──────────────────────────────────────────────

clear_plugin_cache() {
    config_preflight || return 1
    if [ -n "$CACHE_DIR" ] && [ "$CACHE_DIR" != "/" ] && [ -d "$CACHE_DIR" ]; then
        echo -e "${BLUE}  Clearing plugin cache...${NC}"
        rm -rf "$CACHE_DIR" 2>/dev/null || true
    fi
}

# ──────────────────────────────────────────────

install_plugin() {
    echo ""
    echo -e "${BLUE}📦 Installing plugin...${NC}"

    if platform_channel_claude; then
        channel_install_plugin
        return $?
    fi
    # Remember old version directories before clearing cache.
    # After install, we create lightweight "shim" dirs for old versions that
    # forward hook-wrapper.sh to the currently installed version.
    #
    # Why shims (not symlinks)?
    # - Symlinks are unreliable on Windows (permissions / developer mode / Git settings)
    # - Shims are cross-platform and don't require special FS features
    #
    # This keeps a running Claude Code instance working until restart, even if it
    # cached the old version path in memory.
    local version_dir="${CACHE_DIR}/${MARKETPLACE_NAME}"
    local old_versions=()
    if [ -d "$version_dir" ]; then
        for d in "$version_dir"/*/; do
            # Skip symlinks from previous bootstrap runs, only collect real dirs
            [ -d "$d" ] && [ ! -L "${d%/}" ] && old_versions+=("$(basename "$d")")
        done
    fi

    local expected_version=""
    expected_version=$(get_manifest_version "$MARKETPLACE_PLUGIN_JSON")

    local update_failed=false
    local installed_before=""
    local installed_root_before=""
    installed_before=$(get_installed_plugin_version)
    installed_root_before=$(get_installed_plugin_root)

    local output
    if [ -n "$installed_before" ] || [ -n "$installed_root_before" ]; then
        config_preflight || return 1
        if output=$("$_CLAUDE_EXEC" plugin update "$PLUGIN_KEY" </dev/null 2>&1); then
            echo -e "${GREEN}✓${NC} Plugin updated"
        else
            echo -e "${YELLOW}  Plugin update failed, will attempt recovery reinstall${NC}"
            echo -e "${YELLOW}  Output: ${output}${NC}"
            update_failed=true
        fi
    else
        clear_plugin_cache || return 1
        config_preflight || return 1
        if output=$("$_CLAUDE_EXEC" plugin install "$PLUGIN_KEY" </dev/null 2>&1); then
            echo -e "${GREEN}✓${NC} Plugin installed"
        else
            if echo "$output" | grep -qi "already installed"; then
                echo -e "${GREEN}✓${NC} Plugin already installed"
            else
                echo -e "${RED}✗ Plugin install failed${NC}" >&2
                echo -e "${YELLOW}Output: ${output}${NC}" >&2
                return 1
            fi
        fi
    fi

    if [ "$update_failed" = true ] || { [ -n "$expected_version" ] && ! verify_installed_plugin_version "$expected_version"; }; then
        echo -e "${YELLOW}  Installed plugin version does not match marketplace v${expected_version}; reinstalling...${NC}"

        config_preflight || return 1
        "$_CLAUDE_EXEC" plugin uninstall "$PLUGIN_KEY" </dev/null >/dev/null 2>&1 || true
        clear_plugin_cache || return 1

        config_preflight || return 1
        if output=$("$_CLAUDE_EXEC" plugin install "$PLUGIN_KEY" </dev/null 2>&1); then
            echo -e "${GREEN}✓${NC} Plugin reinstalled"
        else
            echo -e "${RED}✗ Plugin reinstall failed${NC}" >&2
            echo -e "${YELLOW}Output: ${output}${NC}" >&2
            return 1
        fi
    fi

    if [ -n "$expected_version" ] && ! verify_installed_plugin_version "$expected_version"; then
        local installed_after=""
        installed_after=$(get_installed_plugin_version)
        echo -e "${RED}✗ Plugin version mismatch after install/update${NC}" >&2
        echo -e "${YELLOW}Expected: v${expected_version}${NC}" >&2
        echo -e "${YELLOW}Installed: v${installed_after:-unknown}${NC}" >&2
        return 1
    fi

    # Create shim dirs for old version paths so running Claude Code instances
    # don't break before restart.
    #
    # Each shim contains only: <old>/bin/hook-wrapper.sh
    # The shim does NOT hardcode the target version; it reads installed_plugins.json
    # on each invocation and forwards to the currently installed installPath.
    if [ -d "$version_dir" ] && [ ${#old_versions[@]} -gt 0 ]; then
        # Determine the newly installed version dir name (first real dir).
        local new_version=""
        for d in "$version_dir"/*/; do
            [ -d "$d" ] && [ ! -L "${d%/}" ] && new_version="$(basename "$d")" && break
        done

        if [ -n "$new_version" ]; then
            for old_ver in "${old_versions[@]}"; do
                # Skip if it matches current version (shouldn't happen, but be safe)
                [ "$old_ver" = "$new_version" ] && continue

                # If something already exists at that path (directory, file, symlink), don't overwrite.
                if [ -e "$version_dir/$old_ver" ]; then
                    continue
                fi

                # Create minimal shim directory structure
                mkdir -p "$version_dir/$old_ver/bin" 2>/dev/null || true

                # Write shim hook-wrapper.sh (POSIX sh) atomically
                local shim_path="$version_dir/$old_ver/bin/hook-wrapper.sh"
                local tmp_path="${shim_path}.tmp.$$"
                cat > "$tmp_path" <<'SHIMEOF' 2>/dev/null || true
#!/bin/sh
# claude-notifications-go shim: forwards old cached hook path to current plugin installPath.
# This file is auto-generated by bootstrap.sh and is safe to delete after restarting Claude.
#
# Behavior:
# - Find current installPath from ~/.claude/plugins/installed_plugins.json
# - Set CLAUDE_PLUGIN_ROOT to that installPath
# - Exec the real hook-wrapper.sh from the current install
#
# IMPORTANT: Must never fail the hook (exit 0 on any error).

SHIM_HOME="${HOME:-${USERPROFILE:-}}"
CLAUDE_HOME="${CLAUDE_CONFIG_DIR:-${CLAUDE_HOME:-}}"
if [ -z "$CLAUDE_HOME" ]; then
  [ -n "$SHIM_HOME" ] || exit 0
  CLAUDE_HOME="$SHIM_HOME/.claude"
fi

INSTALLED_JSON="${CLAUDE_HOME}/plugins/installed_plugins.json"
MARKETPLACE_NAME="claude-notifications-go"
PLUGIN_KEY="claude-notifications-go@claude-notifications-go"
PLUGIN_ROOT=""

if [ -f "$INSTALLED_JSON" ]; then
  # Prefer robust JSON parsing; fall back to grep/sed only if needed.
  if command -v jq >/dev/null 2>&1; then
    PLUGIN_ROOT=$(PLUGIN_KEY="$PLUGIN_KEY" jq -r '
      (.plugins[env.PLUGIN_KEY] // [])
      | map(select((.installPath // "") != ""))
      | sort_by((.version // "0.0.0") | split(".") | map(tonumber? // 0))
      | if length == 0 then {} else .[-1] end
      | .installPath // empty
    ' "$INSTALLED_JSON" 2>/dev/null) || true
  fi

  # Quoted -c/-e scripts keep ")" inside $() from closing command substitution.
  # Heredocs cannot be used here: the $(...) matcher does not treat heredoc
  # bodies as quoted, so Python/JS parentheses would break the shim.
  if [ -z "$PLUGIN_ROOT" ] && command -v python3 >/dev/null 2>&1 && python3 -I -c 'import json' </dev/null >/dev/null 2>&1; then
    PLUGIN_ROOT=$(python3 -I -c '
import json, sys
def ver_tuple(value):
    try:
        parts = str(value or "0.0.0").split(".")
        parts = (parts + ["0", "0", "0"])[:3]
        return tuple(int(p) for p in parts)
    except Exception:
        return (0, 0, 0)
try:
    with open(sys.argv[1]) as f:
        d = json.load(f)
    entries = [e for e in d.get("plugins", {}).get(sys.argv[2], []) if isinstance(e, dict) and e.get("installPath")]
    if entries:
        best = max(entries, key=lambda e: ver_tuple(e.get("version")))
        print(best.get("installPath", "") or "")
except Exception:
    pass
' "$INSTALLED_JSON" "$PLUGIN_KEY" 2>/dev/null) || true
  fi

  # Node is very likely present because Claude Code is a Node app.
  # Inline isolation: this generated file is a standalone POSIX script and
  # cannot call installer helpers that live only in bootstrap.sh.
  if [ -z "$PLUGIN_ROOT" ] && command -v node >/dev/null 2>&1 && NODE_OPTIONS= NODE_PATH= node --no-warnings -e 'JSON.parse("{}")' </dev/null >/dev/null 2>&1; then
    PLUGIN_ROOT=$(PLUGIN_KEY="$PLUGIN_KEY" NODE_OPTIONS= NODE_PATH= node --no-warnings -e '
const fs = require("fs");
function parseVersion(value) {
  return String(value || "0.0.0")
    .split(".")
    .slice(0, 3)
    .map((part) => {
      const n = parseInt(part, 10);
      return Number.isFinite(n) ? n : 0;
    });
}
function compareVersions(a, b) {
  const av = parseVersion(a && a.version);
  const bv = parseVersion(b && b.version);
  for (let i = 0; i < 3; i += 1) {
    if (av[i] !== bv[i]) return av[i] - bv[i];
  }
  return 0;
}
try {
  const p = process.argv[1];
  const k = process.env.PLUGIN_KEY;
  const d = JSON.parse(fs.readFileSync(p, "utf8"));
  const entries = ((d.plugins && d.plugins[k]) || []).filter((entry) => entry && typeof entry === "object" && entry.installPath);
  if (entries.length > 0) {
    const e = entries.slice().sort(compareVersions).pop();
    process.stdout.write((e && e.installPath) ? String(e.installPath) : "");
  }
} catch (_) {}
' "$INSTALLED_JSON" 2>/dev/null) || true
  fi

  if [ -z "$PLUGIN_ROOT" ]; then
    # Best-effort fallback: extract first installPath containing the marketplace name.
    PLUGIN_ROOT=$(grep -o '"installPath"[[:space:]]*:[[:space:]]*"[^"]*'"${MARKETPLACE_NAME}"'[^"]*"' "$INSTALLED_JSON" 2>/dev/null \
      | tail -n 1 \
      | sed 's/"installPath"[[:space:]]*:[[:space:]]*"//;s/"$//' 2>/dev/null) || true
  fi
fi

# Last-resort fallback: try to find any sibling version dir with a real hook-wrapper.sh
if [ -z "$PLUGIN_ROOT" ]; then
  _self_dir="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"
  _ver_dir="$(cd "$_self_dir/.." 2>/dev/null && pwd)"      # <old>/bin
  _ver_root="$(cd "$_ver_dir/.." 2>/dev/null && pwd)"      # <old>
  _parent="$(cd "$_ver_root/.." 2>/dev/null && pwd)"       # .../claude-notifications-go/<versions>
  for d in "$_parent"/*/; do
    [ -d "$d" ] || continue
    if [ -f "${d}bin/hook-wrapper.sh" ]; then
      PLUGIN_ROOT="${d%/}"
      break
    fi
  done
fi

# Extra fallback: stable pointer written by hook-wrapper.sh at runtime
if [ -z "$PLUGIN_ROOT" ]; then
  _PTR_FILE="${CLAUDE_HOME}/claude-notifications-go/plugin-root"
  if [ -f "$_PTR_FILE" ]; then
    IFS= read -r PLUGIN_ROOT < "$_PTR_FILE" 2>/dev/null || true
  fi
fi

if [ -n "$PLUGIN_ROOT" ] && [ -f "$PLUGIN_ROOT/bin/hook-wrapper.sh" ]; then
  export CLAUDE_PLUGIN_ROOT="$PLUGIN_ROOT"
  exec "$PLUGIN_ROOT/bin/hook-wrapper.sh" "$@" || true
fi

exit 0
SHIMEOF
                mv "$tmp_path" "$shim_path" 2>/dev/null || true
                rm -f "$tmp_path" 2>/dev/null || true
                chmod +x "$shim_path" 2>/dev/null || true
                echo -e "${BLUE}  Shim: ${old_ver} → current install (for running session)${NC}"
            done
        fi
    fi
}

# ──────────────────────────────────────────────

find_plugin_root() {
    echo ""
    echo -e "${BLUE}🔍 Locating plugin directory...${NC}"

    if [ ! -f "$INSTALLED_JSON" ]; then
        echo -e "${RED}✗ installed_plugins.json not found at ${INSTALLED_JSON}${NC}" >&2
        echo -e "${YELLOW}  Try restarting Claude and running this script again.${NC}" >&2
        return 1
    fi

    # Try jq first (clean JSON parsing)
    if command -v jq &>/dev/null; then
        PLUGIN_ROOT=$(get_installed_plugin_root)
        if [ "$PLUGIN_ROOT" = "null" ]; then
            PLUGIN_ROOT=""
        fi
    fi

    # Fallback: python3 (available on macOS and most Linux)
    # Pass paths as arguments to avoid shell injection in python code
    if [ -z "$PLUGIN_ROOT" ]; then
        PLUGIN_ROOT=$(get_installed_plugin_root)
    fi

    # Fallback: grep + sed (works everywhere)
    if [ -z "$PLUGIN_ROOT" ]; then
        # Find the installPath that's inside the claude-notifications-go cache dir
        # Note: JSON may have whitespace after colon — "installPath": "..." or "installPath":"..."
        PLUGIN_ROOT=$(grep -o '"installPath"[[:space:]]*:[[:space:]]*"[^"]*'"${MARKETPLACE_NAME}"'[^"]*"' "$INSTALLED_JSON" 2>/dev/null \
            | tail -n 1 \
            | sed 's/"installPath"[[:space:]]*:[[:space:]]*"//;s/"$//' || true)
    fi

    if [ -z "$PLUGIN_ROOT" ] || [ ! -d "$PLUGIN_ROOT" ]; then
        echo -e "${RED}✗ Could not find plugin install path${NC}" >&2
        echo -e "${YELLOW}  installed_plugins.json may not contain the plugin entry yet.${NC}" >&2
        echo -e "${YELLOW}  Try: $_CLAUDE_EXEC plugin install ${PLUGIN_KEY}${NC}" >&2
        return 1
    fi

    echo -e "${GREEN}✓${NC} Plugin root: ${PLUGIN_ROOT}"
}

# ──────────────────────────────────────────────

download_binary() {
    echo ""
    echo -e "${BLUE}📦 Downloading notification binary...${NC}"

    config_preflight || return 1
    local target_dir="${PLUGIN_ROOT}/bin"
    if ! mkdir -p "$target_dir" 2>/dev/null; then
        echo -e "${RED}✗ Cannot create directory: ${target_dir}${NC}" >&2
        return 1
    fi

    install_runtime claude "$_CONFIG_STAGE/install.sh" "$target_dir" --force

}

# ──────────────────────────────────────────────

setup_iterm2_venv() {
    # Only relevant on macOS
    [ "$(uname -s)" = "Darwin" ] || return 0

    is_iterm2_detected || return 0
    config_preflight || return 1

    # Use the resolved user home explicitly (not $CLAUDE_HOME) — the Go code resolves
    # the venv path via os.UserHomeDir()/.claude/..., so the venv must be there.
    [ -n "$INSTALLER_HOME" ] || return 0
    local VENV_DIR="$INSTALLER_HOME/.claude/claude-notifications-go/iterm2-venv"

    # Skip if venv already exists and is functional
    if [ -x "$VENV_DIR/bin/python3" ] && \
       "$VENV_DIR/bin/python3" -c "import iterm2" 2>/dev/null; then
        echo -e "${GREEN}  ✓${NC} iTerm2 Python API venv already set up"
        return 0
    fi

    # Find a working Python 3 (Store/WSL stubs are not usable for venv).
    local python3_path=""
    if usable_python3; then
        python3_path="$(command -v python3)"
    fi

    if [ -z "$python3_path" ]; then
        echo ""
        echo -e "${YELLOW}  ⚠ Python 3 not found. iTerm2 tmux -CC click-to-focus unavailable.${NC}"
        echo -e "${YELLOW}    Install Python 3 and re-run bootstrap to enable.${NC}"
        return 0
    fi

    echo ""
    echo -e "${BLUE}  Setting up iTerm2 Python API support...${NC}"

    if ! "$python3_path" -m venv "$VENV_DIR" 2>/dev/null; then
        echo -e "${YELLOW}  ⚠ Could not create Python venv, skipping${NC}"
        return 0
    fi

    if "$VENV_DIR/bin/pip" install --quiet iterm2 2>/dev/null; then
        echo -e "${GREEN}  ✓${NC} iTerm2 Python API support installed"
        echo -e "${BLUE}    Enable 'Python API' in iTerm2 → Settings → General → Magic${NC}"
    else
        echo -e "${YELLOW}  ⚠ Could not install iterm2 module${NC}"
        rm -rf "$VENV_DIR" 2>/dev/null
    fi
}

# ──────────────────────────────────────────────

# Keep low-level installer diagnostics available without printing nested success
# banners or machine JSON. Functions run in this shell so their state survives.
run_setup_stage() {
    local label="$1" log status=0
    shift
    printf '%s...\n' "$label"
    if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
        "$@"
        return $?
    fi
    log=$(mktemp "${TMPDIR:-/tmp}/bootstrap-output-XXXXXX") || return 1
    if "$@" > "$log" 2> "$log.stderr"; then
        # Preserve all stderr, including skipped/partial setup recovery advice.
        grep -vE '^(phase (prepare|preflight|hooks|agent-notify|complete)|Setting up notifications: (prepare|preflight|hooks|agent notify|complete)\.\.\.)$' "$log.stderr" >&2 || true
        # Keep indented recovery advice with its warning, including colorized lines.
        awk '{
            plain = $0
            gsub(/\033\[[0-9;]*m/, "", plain)
            if (tolower(plain) ~ /⚠|warning|skipped|not installed|manual setup|could not|keeping existing/) {
                print; warning = 1
            } else if (warning && plain ~ /^[[:space:]]+[^[:space:]]/) {
                print
            } else {
                warning = 0
            }
        }' "$log"
    else
        status=$?
        cat "$log" "$log.stderr" >&2
    fi
    rm -f "$log" "$log.stderr"
    if [ "$status" -ne 0 ]; then
        printf 'Setup stopped while %s. Review the details above.\n' "$label" >&2
        return "$status"
    fi
}

print_success() {
    local report
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-summary-XXXXXX") || return 1
    {
        case "$PRODUCT" in
            claude|both)
                printf '  Claude - installed; restart required.\n'
                printf '    Agent notification tool: %s.\n' "$CLAUDE_AGENT_NOTIFY_STATUS" ;;
        esac
        case "$PRODUCT" in
            codex|both)
                printf '  Codex - installed; restart required.\n'
                printf '    Agent notification tool: %s.\n' "$CODEX_AGENT_NOTIFY_STATUS"
                printf '    Run /hooks in Codex, review the entries and trust them.\n' ;;
        esac
        printf '  Delivery has not been verified.\n'
        printf '  Restart the installed agents, then trigger a notification to check delivery.\n'
        printf '  If no alert arrives, check OS notification permission and your settings.\n'
        if [ "$PRODUCT" != codex ]; then
            printf '  Claude settings: /claude-notifications-go:settings\n'
            print_iterm2_python_api_notice
        fi
    } > "$report"
    publish_setup_summary "$report"
    local status=$?
    rm -f "$report"
    return "$status"
}

# The public loader can collect summaries across separate pinned bootstrap runs.
# This file contains display text, never executable shell or a readiness claim.
publish_setup_summary() {
    if [ -n "${BOOTSTRAP_SUMMARY_FILE:-}" ]; then
        cat "$1" >> "$BOOTSTRAP_SUMMARY_FILE"
    else
        printf '\nInstallation complete\n\n'
        cat "$1"
        printf '\nDetailed installer output: rerun with BOOTSTRAP_VERBOSE=1.\n'
    fi
}

# ──────────────────────────────────────────────

# Keep macOS discovery read-only and independent of PATH or application names.
# The small command boundary also lets shell fixtures avoid querying the user's
# LaunchServices database or signing tools.
bootstrap_macos_command() {
    "$@"
}

bootstrap_default_codex_app() {
    local app physical_app
    app=$(bootstrap_macos_command /usr/bin/osascript -l JavaScript -e '
ObjC.import("AppKit");
var url = $.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier("com.openai.codex");
var path = ObjC.unwrap(url.path);
if (typeof path !== "string" || path.length === 0) throw new Error("Codex application unavailable");
path;
' </dev/null 2>/dev/null) || return 1
    # LaunchServices output is untrusted. Resolve symlinks before pinning the
    # app, and reject ambiguous/control-containing paths before verification.
    case "$app" in
        /*.app) ;;
        *) return 1 ;;
    esac
    case "$app" in *[[:cntrl:]]*|*..*) return 1 ;; esac
    [ "${#app}" -le 1024 ] && [ -d "$app" ] || return 1
    physical_app=$(cd -P "$app" && pwd -P) || return 1
    case "$physical_app" in /*.app) ;; *) return 1 ;; esac
    case "$physical_app" in *[[:cntrl:]]*|*..*) return 1 ;; esac
    [ "${#physical_app}" -le 1024 ] || return 1
    # Same offline Developer ID requirement as the configure CLI. An app name
    # or a registered bundle ID alone does not establish the official identity.
    bootstrap_macos_command /usr/bin/codesign --verify --strict --all-architectures -R \
        '=anchor apple generic and identifier "com.openai.codex" and certificate leaf[subject.OU] = "2DC432GLL2" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists' \
        "$physical_app" </dev/null >/dev/null 2>&1 || return 1
    printf '%s\n' "$physical_app"
}

# Match the CLI contract before any installation work. Incomplete consent or
# mixed none/local pairs must not reach configure as a printed retry.
complete_configure_route() {
    local nav="" app="" team="" unknown="" asserted="" i=0
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        case "${CONFIGURE_ARGS[$i]}" in
            --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted|--codex-home)
                i=$((i + 1))
                [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ] || { echo "Missing value for ${CONFIGURE_ARGS[$((i - 1))]}" >&2; return 1; }
                case "${CONFIGURE_ARGS[$((i - 1))]}" in
                    --navigation) nav="${CONFIGURE_ARGS[$i]}" ;;
                    --app) app="${CONFIGURE_ARGS[$i]}" ;;
                    --team-id) team="${CONFIGURE_ARGS[$i]}" ;;
                    --allow-unknown-caller) unknown="${CONFIGURE_ARGS[$i]}" ;;
                    --allow-caller-asserted) asserted="${CONFIGURE_ARGS[$i]}" ;;
                esac ;;
            --json|--request-permission|--preserve-policy) ;;
            *) echo "Unknown option: ${CONFIGURE_ARGS[$i]}" >&2; return 1 ;;
        esac
        i=$((i + 1))
    done
    if [ -z "$nav" ] && [ -z "$app" ] && [ -z "$team" ] && [ -z "$unknown" ] && [ -z "$asserted" ]; then
        if { [ "${LEGACY_PRODUCT:-$PRODUCT}" = codex ] || [ "${LEGACY_PRODUCT:-$PRODUCT}" = both ]; } && [ "$(uname -s)" = Darwin ]; then
            if app=$(bootstrap_default_codex_app); then
                CONFIGURE_ARGS+=(--app "$app" --team-id 2DC432GLL2)
                echo "Verified Codex Desktop default: $app. Existing navigation policy is preserved." >&2
            else
                CONFIGURE_ARGS+=(--navigation none)
                echo "No registered, verified official Codex Desktop app found; defaulting to navigation none. Existing navigation policy is preserved." >&2
            fi
        else
            CONFIGURE_ARGS+=(--navigation none)
            if [ "${LEGACY_PRODUCT:-$PRODUCT}" = codex ] || [ "${LEGACY_PRODUCT:-$PRODUCT}" = both ]; then
                echo "Codex Desktop default navigation requires macOS; defaulting to navigation none. Existing navigation policy is preserved." >&2
            fi
        fi
        CONFIGURE_ARGS+=(--allow-unknown-caller true --allow-caller-asserted false)
        case " ${CONFIGURE_ARGS[*]-} " in *" --preserve-policy "*) ;; *) CONFIGURE_ARGS+=(--preserve-policy) ;; esac
        return 0
    fi
    if [ "$nav" = none ]; then
        if [ -n "$app" ] || [ -n "$team" ]; then
            echo "navigation none cannot combine with --app/--team-id." >&2
            return 1
        fi
        case "$unknown" in true|false) ;; *) echo "navigation none requires --allow-unknown-caller and --allow-caller-asserted." >&2; return 1 ;; esac
        case "$asserted" in true|false) ;; *) echo "navigation none requires --allow-unknown-caller and --allow-caller-asserted." >&2; return 1 ;; esac
        return 0
    fi
    if [ -n "$nav" ]; then
        echo "Invalid navigation: $nav" >&2
        return 1
    fi
    if [ -z "$app" ] || [ -z "$team" ] || [ -z "$unknown" ] || [ -z "$asserted" ]; then
        echo "Incomplete route; supply --app, --team-id, and both consent flags." >&2
        return 1
    fi
    case "$unknown" in true|false) ;; *) echo "Invalid allow-unknown-caller: $unknown" >&2; return 1 ;; esac
    case "$asserted" in true|false) ;; *) echo "Invalid allow-caller-asserted: $asserted" >&2; return 1 ;; esac
    return 0
}

# Product selection must precede any filesystem or host CLI mutation.
select_product() {
    local seen_agent_notify=false seen_skip_agent_notify=false seen_flags="" key value
    while [ "$#" -gt 0 ]; do
        key=${1%%=*}
        case "$key" in
            --ui|--plain|--product|--products|--help|-h) ;;
            *)
                case ",$seen_flags," in *",$key,"*) echo "Use $key once." >&2; return 1 ;; esac
                seen_flags=${seen_flags:+$seen_flags,}$key ;;
        esac
        case "$1" in
            --ui|--ui=*|--plain)
                case "$1" in
                    --plain) value=plain; shift ;;
                    --ui) [ "$#" -ge 2 ] || return 1; value=$2; shift 2 ;;
                    *) value=${1#--ui=}; shift ;;
                esac
                case "$value" in auto|rich|plain) ;; *) echo "Invalid UI mode: $value" >&2; return 1 ;; esac
                [ -z "$_UI_MODE" ] || [ "$_UI_MODE" = "$value" ] || { echo "Conflicting UI modes." >&2; return 1; }
                _UI_MODE=$value; UI_ARGS=(--ui "$value") ;;
            --product|--product=*)
                [ -z "$PRODUCT" ] || { echo "Use one product selector once." >&2; return 1; }
                [ "$1" != --product ] || [ "$#" -ge 2 ] || { echo "--product requires a value." >&2; return 1; }
                case "$1" in
                    --product) PRODUCT="$2"; shift 2 ;;
                    *) PRODUCT=${1#--product=}; shift ;;
                esac ;;
            --products|--products=*)
                [ -z "$PRODUCT" ] || { echo "Use one product selector once." >&2; return 1; }
                case "$1" in
                    --products) [ "$#" -ge 2 ] || return 1; PRODUCT=$2; shift 2 ;;
                    *) PRODUCT=${1#--products=}; shift ;;
                esac
                PRODUCT="bundle:$PRODUCT" ;;
            --desktop|--webhook)
                local existing
                for existing in ${OPENCODE_ARGS[@]+"${OPENCODE_ARGS[@]}"}; do
                    [ "$existing" != "$1" ] || { echo "Use each observer channel once." >&2; return 1; }
                done
                OPENCODE_ARGS+=("$1")
                shift ;;
            --agent-notify)
                seen_agent_notify=true
                AGENT_NOTIFY_REQUEST=explicit
                CONFIGURE_NOTIFICATIONS=true
                shift ;;
            --skip-agent-notify)
                seen_skip_agent_notify=true
                AGENT_NOTIFY_REQUEST=skip
                CONFIGURE_NOTIFICATIONS=false
                shift ;;
            --navigation=*|--app=*|--team-id=*|--allow-unknown-caller=*|--allow-caller-asserted=*|--codex-home=*)
                value=${1#*=}; shift; set -- "$key" "$value" "$@"
                # Already recorded this key; the value branch below consumes it.
                case "$key" in
                    --navigation) [ "$value" = none ] || return 1 ;;
                    --app|--codex-home) case "$value" in /*) ;; *) return 1 ;; esac
                        if [ "$key" = --app ]; then case "$value" in *..*) return 1 ;; esac; fi ;;
                esac
                CONFIGURE_ARGS+=("$key" "$value"); shift 2 ;;
            --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted|--codex-home)
                [ "$#" -ge 2 ] || { echo "Missing value for $1" >&2; return 1; }
                case "$1" in
                    --navigation)
                        [ "$2" = none ] || { echo "Invalid navigation: $2" >&2; return 1; } ;;
                    --app)
                        case "$2" in
                            /*) ;;
                            *) echo "App path must be absolute." >&2; return 1 ;;
                        esac
                        case "$2" in
                            *..*) echo "App path must be a physical path." >&2; return 1 ;;
                        esac ;;
                    --codex-home)
                        case "$2" in
                            /*) ;;
                            *) echo "codex-home must be absolute." >&2; return 1 ;;
                        esac ;;
                esac
                CONFIGURE_ARGS+=("$1" "$2")
                shift 2 ;;
            --request-permission|--preserve-policy|--json)
                CONFIGURE_ARGS+=("$1")
                shift ;;
            --help|-h)
                echo "Usage: bash bootstrap.sh [--product claude|codex|both|opencode|gemini | --products claude,codex,opencode,gemini] [--desktop] [--webhook] [--agent-notify|--skip-agent-notify] [--navigation none]"
                exit 0 ;;
            *) echo "Unknown option: $1" >&2; return 1 ;;
        esac
    done
    if [ "$seen_agent_notify" = true ] && [ "$seen_skip_agent_notify" = true ]; then
        echo "--agent-notify and --skip-agent-notify are mutually exclusive." >&2
        return 1
    fi
    if [ -z "$PRODUCT" ]; then
        # Selection runs later through the release-verified public UAP UI. No
        # persistent root is initialized before cancel/empty are resolved.
        PRODUCT=select
        _SELECTION_PENDING=true
        _INTERACTIVE_INTENT=true
        case " ${CONFIGURE_ARGS[*]-} " in *" --json "*) echo "Pending questions require complete explicit product/route/channel input with --json." >&2; return 1 ;; esac
        # Pure syntax only; applicability is checked after product selection.
        if [ "$AGENT_NOTIFY_REQUEST" = skip ] && [ "${#CONFIGURE_ARGS[@]}" -gt 0 ]; then
            echo "Route flags require --agent-notify." >&2; return 1
        fi
        [ "${#CONFIGURE_ARGS[@]}" -eq 0 ] || complete_configure_route
        return $?
    fi
    local csv remaining selected seen="" observers=0
    case "$PRODUCT" in
        bundle:*) csv=${PRODUCT#bundle:} ;;
        both) csv=claude,codex ;;
        claude|codex|opencode|gemini) csv=$PRODUCT ;;
        *) echo "Invalid product: $PRODUCT; use claude, codex, both, opencode or gemini." >&2; return 1 ;;
    esac
    case "$csv" in ''|,*|*,|*,,*) echo "Product selection contains an empty product." >&2; return 1 ;; esac
    SELECTED_PRODUCTS=()
    LEGACY_PRODUCT=""
    remaining=$csv
    while :; do
        selected=${remaining%%,*}
        case "$selected" in
            claude|codex|opencode|gemini) ;;
            *) echo "Unknown product: $selected" >&2; return 1 ;;
        esac
        case ",$seen," in *",$selected,"*) echo "Duplicate product: $selected" >&2; return 1 ;; esac
        seen=${seen:+$seen,}$selected
        SELECTED_PRODUCTS+=("$selected")
        case "$selected" in
            claude|codex)
                if [ -z "$LEGACY_PRODUCT" ]; then LEGACY_PRODUCT=$selected; else LEGACY_PRODUCT=both; fi ;;
            opencode|gemini) observers=$((observers+1)) ;;
        esac
        case "$remaining" in *,*) remaining=${remaining#*,} ;; *) break ;; esac
    done
    if [ "$observers" -gt 0 ] && [ "${#OPENCODE_ARGS[@]}" -eq 0 ]; then
        if [[ "$PRODUCT" = bundle:* ]] && [ "$_INTERACTIVE_INTENT" != true ]; then
            echo "Selected observers require explicit --desktop and/or --webhook consent." >&2; return 1
        fi
        _CHANNEL_PENDING=true
        _INTERACTIVE_INTENT=true
        case " ${CONFIGURE_ARGS[*]-} " in *" --json "*) echo "Pending questions require complete explicit product/route/channel input with --json." >&2; return 1 ;; esac
    fi
    if [ "$observers" -eq 0 ] && [ "${#OPENCODE_ARGS[@]}" -gt 0 ]; then
        echo "--desktop/--webhook require OpenCode or Gemini." >&2; return 1
    fi
    if [ -z "$LEGACY_PRODUCT" ]; then
        if [ "$seen_agent_notify" = true ] || [ "$seen_skip_agent_notify" = true ] || [ "${#CONFIGURE_ARGS[@]}" -ne 0 ]; then
            echo "Observers use --desktop/--webhook consent, not agent-notify or navigation flags." >&2
            return 1
        fi
        CONFIGURE_NOTIFICATIONS=false
        return 0
    fi
    if [ "$CONFIGURE_NOTIFICATIONS" = true ]; then
        complete_configure_route || return 1
    elif [ "${#CONFIGURE_ARGS[@]}" -ne 0 ]; then
        echo "Route flags require --agent-notify." >&2
        return 1
    fi
}

bootstrap_cleanup() {
    close_prompt
    [ -z "$_BOOTSTRAP_TMP" ] || rm -f "$_BOOTSTRAP_TMP"
    [ -z "$_BOOTSTRAP_STAGE" ] || rm -rf "$_BOOTSTRAP_STAGE"
    if [ "$_KEEP_PORTABLE_STAGE" != true ]; then
        [ -z "$_PORTABLE_STAGE" ] || rm -rf "$_PORTABLE_STAGE"
    fi
    if [ "$_KEEP_CONFIG_STAGE" != true ]; then
        [ -z "$_CONFIG_STAGE" ] || rm -rf "$_CONFIG_STAGE"
    fi
    return 0
}

install_cleanup_traps() {
    trap bootstrap_cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP
}

install_runtime() {
    local product="$1" script="$2" target="$3"
    local disposable=false
    [ "$product" = "codex" ] && disposable=true
    CN_PRODUCT="$product" INSTALL_STAGED_ASSETS="$_CONFIG_STAGE" \
    INSTALL_DISPOSABLE_ACQUISITION="$disposable" \
    RELEASE_URL="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG" \
    CHECKSUMS_URL="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG/checksums.txt" \
    MODERN_NOTIFIER_URL="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG/ClaudeNotifier.app.zip" \
    INSTALL_TARGET_DIR="$target" bash "$script" "${@:4}" </dev/null
}

fetch_bootstrap_file() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --connect-timeout 15 --max-time 120 "$1" -o "$2"
    else
        wget -q -T 120 "$1" -O "$2"
    fi
}

fetch_bootstrap_commit_file() {
    # GitHub's SHA media type returns exactly 40 ASCII bytes, as setup.sh
    # already uses. This also works with helpers from older stable releases.
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -H 'Accept: application/vnd.github.sha' --connect-timeout 15 --max-time 120 "$1" -o "$2"
    else
        wget -q -T 120 --header='Accept: application/vnd.github.sha' "$1" -O "$2"
    fi
}

resolve_bootstrap_release() {
    BOOTSTRAP_TAG="${BOOTSTRAP_RELEASE_TAG:-}"
    if [ -z "$BOOTSTRAP_TAG" ]; then
        local controller raw platform row os arch
        _BOOTSTRAP_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-channel-XXXXXX") || return 1
        controller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"
        if [ -z "$controller" ]; then
            fetch_bootstrap_commit_file "https://api.github.com/repos/$REPO/commits/main" "$_BOOTSTRAP_STAGE/controller" || return 1
            [ "$(wc -c < "$_BOOTSTRAP_STAGE/controller" | tr -d '[:space:]')" = 40 ] || return 1
            controller=$(cat "$_BOOTSTRAP_STAGE/controller")
        fi
        printf '%s\n' "$controller" | grep -Eq '^[0-9a-f]{40}$' || return 1
        raw="$BOOTSTRAP_RAW_CONTENT_URL/$controller"
        fetch_bootstrap_file "$raw/bin/release-channel.sh" "$_BOOTSTRAP_STAGE/channel.sh" || return 1
        fetch_bootstrap_file "$raw/release-channels.tsv" "$_BOOTSTRAP_STAGE/channels.tsv" || return 1
        source "$_BOOTSTRAP_STAGE/channel.sh"
        platform=$(release_channel_platform) || return 1
        IFS=$'\t' read -r os arch <<< "$platform"
        row=$(release_channel_select "$_BOOTSTRAP_STAGE/channels.tsv" "$os" "$arch") || return 1
        IFS=$'\t' read -r BOOTSTRAP_RELEASE_TAG BOOTSTRAP_RELEASE_COMMIT BOOTSTRAP_SOURCE_COMMIT BOOTSTRAP_SOURCE_REF <<< "$row"
        BOOTSTRAP_RELEASE_CHANNEL=1
        [ "$MARKETPLACE_SOURCE" = "$REPO" ] || {
            echo 'Custom source retained. Use explicit matching release/source pins for this marketplace.' >&2; return 1;
        }
        BOOTSTRAP_TAG="$BOOTSTRAP_RELEASE_TAG"
        fetch_bootstrap_commit_file "https://api.github.com/repos/$REPO/commits/$BOOTSTRAP_TAG" "$_BOOTSTRAP_STAGE/release" || return 1
        [ "$(wc -c < "$_BOOTSTRAP_STAGE/release" | tr -d '[:space:]')" = 40 ] &&
            [ "$(cat "$_BOOTSTRAP_STAGE/release")" = "$BOOTSTRAP_RELEASE_COMMIT" ] || return 1
        rm -rf "$_BOOTSTRAP_STAGE"
        _BOOTSTRAP_STAGE=""
    fi
    # Reject prereleases, malformed tags and releases predating setup-codex.
    printf '%s\n' "$BOOTSTRAP_TAG" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || {
        echo "Invalid stable release tag: $BOOTSTRAP_TAG" >&2; return 1;
    }
    local version="${BOOTSTRAP_TAG#v}" major minor component
    for component in ${version//./ }; do
        [ "${#component}" -le 9 ] || { echo "Release version component too large." >&2; return 1; }
    done
    major="${version%%.*}"; minor="${version#*.}"; minor="${minor%%.*}"
    if [ "$major" -lt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -lt 42 ]; }; then
        echo "Agent Notifications requires published release v1.42.0 or newer (the shared config preflight needs it, for every product); found $BOOTSTRAP_TAG." >&2
        return 1
    fi

    if [ "$PRODUCT" = opencode ] && [ "$major" -eq 1 ] && [ "$minor" -lt 46 ]; then
        echo "OpenCode bootstrap requires published release v1.46.0 or newer; found $BOOTSTRAP_TAG." >&2
        return 1
    fi

    BOOTSTRAP_COMMIT="${BOOTSTRAP_RELEASE_COMMIT:-}"
    if [ -n "$BOOTSTRAP_COMMIT" ]; then
        printf '%s\n' "$BOOTSTRAP_COMMIT" | grep -Eq '^[0-9a-f]{40}$' || {
        echo "Invalid release commit SHA: $BOOTSTRAP_COMMIT" >&2; return 1;
        }
    fi

    BOOTSTRAP_BUNDLE_COMMIT="${BOOTSTRAP_SOURCE_COMMIT:-$BOOTSTRAP_COMMIT}"
    if [ -n "$BOOTSTRAP_BUNDLE_COMMIT" ]; then
        printf '%s\n' "$BOOTSTRAP_BUNDLE_COMMIT" | grep -Eq '^[0-9a-f]{40}$' || return 1
    fi
    # Keep explicit overrides separate from the default so managed installs
    # can still select their compatible writer.
}

resolve_bootstrap_commit() {
    if [ -n "$BOOTSTRAP_COMMIT" ]; then
        BOOTSTRAP_BUNDLE_COMMIT="${BOOTSTRAP_SOURCE_COMMIT:-$BOOTSTRAP_COMMIT}"
        return 0
    fi
    _BOOTSTRAP_TMP=$(mktemp "${TMPDIR:-/tmp}/bootstrap-commit-XXXXXX") || return 1
    fetch_bootstrap_commit_file "${BOOTSTRAP_COMMIT_API_BASE_URL:-https://api.github.com/repos/${REPO}/commits}/$BOOTSTRAP_TAG" "$_BOOTSTRAP_TMP" || return 1
    [ "$(LC_ALL=C wc -c < "$_BOOTSTRAP_TMP" | tr -d '[:space:]')" = 40 ] &&
        LC_ALL=C grep -Eq '^[0-9a-f]{40}$' "$_BOOTSTRAP_TMP" || {
        echo "Release tag did not resolve to a commit SHA." >&2; return 1;
    }
    IFS= read -r BOOTSTRAP_COMMIT < "$_BOOTSTRAP_TMP" || [ -n "$BOOTSTRAP_COMMIT" ] || return 1
    BOOTSTRAP_BUNDLE_COMMIT="${BOOTSTRAP_SOURCE_COMMIT:-$BOOTSTRAP_COMMIT}"
    rm -f "$_BOOTSTRAP_TMP"
    _BOOTSTRAP_TMP=""
}

# Only release-verified bytes execute before host registration. Never use an old
# cache binary for config decisions. Paths are metadata, never resolver inputs.
bootstrap_checksum_entry() {
    local manifest="$1" name="$2"
    awk -v name="$name" '
        NF == 2 { file=$2; sub(/^\*/, "", file); if (file == name) { count++; digest=tolower($1) } }
        END { if (count != 1 || length(digest) != 64 || digest ~ /[^0-9a-f]/) exit 1; print digest }
    ' "$manifest"
}

verify_bootstrap_checksum() {
    local root="$1" name="$2" expected actual
    expected=$(bootstrap_checksum_entry "$root/checksums.txt" "$name") || return 1
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum < "$root/$name") || return 1
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 < "$root/$name") || return 1
    else
        echo 'A system SHA-256 tool (sha256sum or shasum) is required.' >&2
        return 1
    fi
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || { echo 'Release checksum mismatch.' >&2; return 1; }
}

stage_config_helper() {
    # Keep acquisition in a private physical temporary stage. TEST callers pass
    # their job scratch as TMPDIR; the default remains the OS temporary root.
    # The verified helper checks canonical overlap before any refresh operation.
    _CONFIG_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-config-XXXXXX") || return 1
    _CONFIG_STAGE=$(cd -P "$_CONFIG_STAGE" && pwd -P) || return 1
    # Snapshot the pre-update registry, not a guessed cache version. Later
    # registrations introduce packaged templates, not historical user settings.
    if { [ "$PRODUCT" = claude ] || [ "$PRODUCT" = both ] || [ "$PRODUCT" = select ] || [ "$LEGACY_PRODUCT" = claude ] || [ "$LEGACY_PRODUCT" = both ]; } && [ -e "$INSTALLED_JSON" ]; then
        cp "$INSTALLED_JSON" "$_CONFIG_STAGE/installed-before.json" || return 1
    else
        printf '{"plugins":{}}\n' > "$_CONFIG_STAGE/installed-before.json"
    fi
    local os arch name base capability companion
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in darwin|linux) ;; mingw*|msys*|cygwin*) os=windows ;; *) return 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) return 1 ;; esac
    name="claude-notifications-$os-$arch"
    [ "$os" != windows ] || name="$name.exe"
    base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
    fetch_bootstrap_file "$base/checksums.txt" "$_CONFIG_STAGE/checksums.txt" || return 1
    fetch_bootstrap_file "$base/$name" "$_CONFIG_STAGE/$name" || return 1
    verify_bootstrap_checksum "$_CONFIG_STAGE" "$name" || return 1
    _CONFIG_HELPER="$_CONFIG_STAGE/$name"
    chmod +x "$_CONFIG_HELPER" || return 1
    [ "$("$_CONFIG_HELPER" --version)" = "claude-notifications $BOOTSTRAP_TAG" ] || return 1
    capability=$("$_CONFIG_HELPER" config installer capabilities) || return 1
    [ "$capability" = installer-v1 ] || {
        echo 'Published helper does not support interpreter-free installation; a newer release is required.' >&2
        return 1
    }
    resolve_bootstrap_commit || return 1
    "$_CONFIG_HELPER" config path --json > "$_CONFIG_STAGE/path.json" || return 1
    fetch_bootstrap_file "$(select_bootstrap_install_script)" "$_CONFIG_STAGE/install.sh" || return 1

}

stage_channel_companions() {
    local os arch name base companion
    read -r os arch < <(bootstrap_release_os_arch) || return 1
    base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
    if [ "${BOOTSTRAP_RELEASE_CHANNEL:-}" = 1 ]; then
        # Required companions are verified before any marketplace/cache mutation.
        case "$os" in
            windows) companion="claude-notifications-windows-$arch-focus.exe" ;;
            darwin) companion=ClaudeNotifier.app.zip ;;
            *) companion="" ;;
        esac
        if [ -n "$companion" ]; then
            fetch_bootstrap_file "$base/$companion" "$_CONFIG_STAGE/$companion" || return 1
            verify_bootstrap_checksum "$_CONFIG_STAGE" "$companion" || return 1
        fi
    fi
}

bootstrap_control_root() {
    [ -z "$_FROZEN_CONTROL" ] || { printf '%s\n' "$_FROZEN_CONTROL"; return 0; }
    local root
    case "$(uname -s 2>/dev/null)" in
        Darwin)
            [ -n "$INSTALLER_HOME" ] || return 1
            root="$INSTALLER_HOME/Library/Application Support"
            ;;
        MINGW*|MSYS*|CYGWIN*|Windows_NT)
            root="${APPDATA:-${INSTALLER_HOME:+$INSTALLER_HOME/AppData/Roaming}}"
            ;;
        *)
            root="${XDG_CONFIG_HOME:-${INSTALLER_HOME:+$INSTALLER_HOME/.config}}"
            ;;
    esac
    [ -n "$root" ] || return 1
    printf '%s/agent-notifications\n' "$root"
}

bootstrap_has_managed_ledger() {
    local root ledger
    root=$(bootstrap_control_root) || return 1
    ledger="$root/ownership.json"
    [ -f "$ledger" ] || return 1
    [ ! -L "$ledger" ]
}

select_bootstrap_install_script() {
    if [ -n "${INSTALL_SCRIPT_URL:-}" ]; then
        printf '%s\n' "$INSTALL_SCRIPT_URL"
        return 0
    fi
    if [ "${BOOTSTRAP_RELEASE_CHANNEL:-}" != 1 ] && bootstrap_has_managed_ledger; then
        printf '%s\n' "$MANAGED_INSTALL_SCRIPT_URL"
        return 0
    fi
    printf '%s\n' "${BOOTSTRAP_RAW_BASE_URL:-$BOOTSTRAP_RAW_CONTENT_URL}/${BOOTSTRAP_BUNDLE_COMMIT}/bin/install.sh"
}

# Released CLIs before agent-notify pairing reject unknown setup-codex flags and
# have no setup-notifications command. Probe advertised help, never guess.
cli_help() {
    "$1" --help </dev/null 2>/dev/null || "$1" help </dev/null 2>/dev/null || true
}

cli_has_setup_codex_skip_agent_notify() {
    cli_help "$1" | grep -Fq -- '--skip-agent-notify'
}

cli_has_setup_notifications() {
    cli_help "$1" | grep -Fq -- 'setup-notifications'
}

cli_has_setup_wizard() {
    cli_help "$1" | grep -Fq -- 'setup-notifications wizard'
}

# Optional exact-version release templates. Releases without this verified asset
# intentionally leave baseline unknown and require explicit historical import.
stage_historical_baselines() {
    [ "$PRODUCT" != codex ] || return 0
    "$_CONFIG_HELPER" config installer versions "$_CONFIG_STAGE/installed-before.json" "$PLUGIN_KEY" > "$_CONFIG_STAGE/versions" || return 1
    local version base dir
    while IFS= read -r version; do
        [ -n "$version" ] || continue
        dir="$_CONFIG_STAGE/baseline-$version"
        mkdir "$dir" || return 1
        base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/v$version"
        fetch_bootstrap_file "$base/checksums.txt" "$dir/checksums.txt" 2>/dev/null || continue
        # Only request a template explicitly included in the release manifest.
        bootstrap_checksum_entry "$dir/checksums.txt" config.json >/dev/null || continue
        fetch_bootstrap_file "$base/config.json" "$dir/config.json" 2>/dev/null || continue
        verify_bootstrap_checksum "$dir" config.json || continue
        bootstrap_checksum_entry "$dir/checksums.txt" config.json > "$dir/verified" || return 1
    done < "$_CONFIG_STAGE/versions"
}

config_preflight() {
    local venv_refresh=""
    if [ "$PRODUCT" != codex ] && [ "$(uname -s)" = Darwin ]; then
        # Resource location, not a second config resolver. Both asset installers
        # can recreate this venv outside the plugin cache.
        [ -z "$INSTALLER_HOME" ] || venv_refresh="$INSTALLER_HOME/.claude/claude-notifications-go/iterm2-venv"
    fi
    # Resolve config afresh before every destructive operation. Historical roots
    # come from the pre-update registry; current roots extend overlap protection.
    if "$_CONFIG_HELPER" config installer bootstrap "$_CONFIG_STAGE/installed-before.json" "$PLUGIN_KEY" "$CLAUDE_HOME" "$CACHE_DIR" "$MARKETPLACE_DIR" "$DEFAULT_CODEX_HOME" "$PRODUCT" "$_CONFIG_STAGE" "$INSTALLED_JSON" "$venv_refresh" > "$_CONFIG_STAGE/preflight.json"; then
        return 0
    fi
    _KEEP_CONFIG_STAGE=true
    echo "Config preflight stopped setup; runtime retained before this operation." >&2
    cat "$_CONFIG_STAGE/preflight.json" >&2
    printf 'Verified helper retained: %q\nExplicit recovery: %q config init --from <historical-file> --json; then rerun bootstrap.\n' "$_CONFIG_HELPER" "$_CONFIG_HELPER" >&2
    return 1
}

initialize_config() {
    if "$_CONFIG_HELPER" config init --json; then return 0; fi
    report_config_init_failure
}

report_config_init_failure() {
    _KEEP_CONFIG_STAGE=true
    echo "Partial setup: registration succeeded, config initialization failed." >&2
    "$_CONFIG_HELPER" config path --json >&2 || true
    local -a retry=("$_CONFIG_HELPER" config init --json)
    [ -z "${AGENT_NOTIFICATIONS_CONFIG:-}" ] || retry=(env "AGENT_NOTIFICATIONS_CONFIG=$AGENT_NOTIFICATIONS_CONFIG" "${retry[@]}")
    printf 'Config-only retry (no downloads or registration): %s\n' "$(quote_shell_command "${retry[@]}")" >&2
    return 1
}

install_codex() {
    stage_channel_companions || return 1
    local tag="$BOOTSTRAP_TAG" version="${BOOTSTRAP_TAG#v}"
    local source_base="${BOOTSTRAP_SOURCE_BASE_URL:-https://github.com/${REPO}/archive}"
    local release_base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}"
    _BOOTSTRAP_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-codex-XXXXXX") || return 1
    _BOOTSTRAP_STAGE=$(cd -P "$_BOOTSTRAP_STAGE" && pwd -P) || return 1
    local bundle="$_BOOTSTRAP_STAGE/bundle"
    mkdir "$bundle" || return 1
    fetch_bootstrap_file "$source_base/$BOOTSTRAP_BUNDLE_COMMIT.tar.gz" "$_BOOTSTRAP_STAGE/source.tar.gz" || return 1
    tar -xzf "$_BOOTSTRAP_STAGE/source.tar.gz" --strip-components=1 -C "$bundle" || return 1
    [ "$(get_manifest_version "$bundle/.claude-plugin/plugin.json")" = "$version" ] || {
        echo "Source bundle must match Codex-capable release $tag (minimum v1.42.0)." >&2; return 1;
    }
    [ -f "$bundle/bin/install.sh" ] || return 1
    RELEASE_URL="$release_base/download/$tag" \
    CHECKSUMS_URL="$release_base/download/$tag/checksums.txt" \
    MODERN_NOTIFIER_URL="$release_base/download/$tag/ClaudeNotifier.app.zip" \
        install_runtime codex "$_CONFIG_STAGE/install.sh" "$bundle/bin" --force || return 1
    local binary="$bundle/bin/claude-notifications" arch
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            case "$(uname -m)" in
                x86_64|amd64) arch=amd64 ;;
                aarch64|arm64) arch=arm64 ;;
                *) echo "Unsupported Windows architecture" >&2; return 1 ;;
            esac
            binary="$bundle/bin/claude-notifications-windows-$arch.exe" ;;
    esac
    local actual
    actual=$(CN_PRODUCT=codex "$binary" --version) || return 1
    [ "$actual" = "claude-notifications v$version" ] || {
        echo "Binary must match $tag and support setup-codex." >&2; return 1;
    }
    local setup_codex_home="${_SELECTOR_INTENT:+$DEFAULT_CODEX_HOME}" i=0
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        if [ "${CONFIGURE_ARGS[$i]}" = "--codex-home" ]; then
            i=$((i + 1))
            setup_codex_home="${CONFIGURE_ARGS[$i]}"
        fi
        i=$((i + 1))
    done
    run_codex_setup() {
        if cli_has_setup_codex_skip_agent_notify "$binary"; then
            set -- --skip-agent-notify "$@"
        fi
        if [ -n "$setup_codex_home" ]; then
            CN_PRODUCT=codex "$binary" setup-codex --plugin-root "$bundle" --codex-home "$setup_codex_home" "$@" </dev/null
        else
            CN_PRODUCT=codex "$binary" setup-codex --plugin-root "$bundle" "$@" </dev/null
        fi
    }
    if [ "${BOOTSTRAP_RELEASE_CHANNEL:-}" = 1 ]; then
        local installed_version
        installed_version=$(get_manifest_version "${setup_codex_home:-$DEFAULT_CODEX_HOME}/claude-notifications-go/.claude-plugin/plugin.json")
        [ -z "$installed_version" ] || channel_refuse_downgrade "$installed_version" "$BOOTSTRAP_TAG" || return 1
    fi
    run_codex_setup --dry-run || return 1
    config_preflight || return 1
    run_codex_setup || return $?
    progress_phase codex runtime-lookup registration_committed
    if [ -n "$setup_codex_home" ]; then
        CONFIGURE_BINARY=$(installed_notification_binary "$setup_codex_home/claude-notifications-go") || return 1
    else
        [ -n "$DEFAULT_CODEX_HOME" ] || { echo "HOME, USERPROFILE, CODEX_HOME, or --codex-home is required for Codex setup." >&2; return 1; }
        CONFIGURE_BINARY=$(installed_notification_binary "$DEFAULT_CODEX_HOME/claude-notifications-go") || return 1
    fi
    if ! notification_command_available "$CONFIGURE_BINARY"; then
        echo "Committed Codex runtime binary missing after setup-codex." >&2
        return 1
    fi
    rm -rf "$_BOOTSTRAP_STAGE"
    _BOOTSTRAP_STAGE=""
    echo "Codex installed. Start Codex, run /hooks, review and trust the entries."
}

# Windows uses the ledger-owned BAT launcher; it does not rely on POSIX execute
# bits and keeps the client command stable across architecture updates.
installed_notification_binary() {
    local root="$1"
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            printf '%s/bin/claude-notifications.bat\n' "$root" ;;
        *) printf '%s/bin/claude-notifications\n' "$root" ;;
    esac
}

notification_command_available() {
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*) [ -f "$1" ] ;;
        *) [ -x "$1" ] ;;
    esac
}

install_claude() {
    stage_channel_companions || return 1
    setup_marketplace || return 1
    sync_marketplace_checkout || return 1
    install_plugin || return 1
    find_plugin_root || return 1
    download_binary || return 1
    if platform_channel_claude; then
        channel_marketplace_add "$BOOTSTRAP_SOURCE_REF" || {
            echo 'Installed snapshot works, but channel tracking could not be enabled. Rerun setup.' >&2; return 1;
        }
    fi
    setup_iterm2_venv || return 1
    CONFIGURE_BINARY=$(installed_notification_binary "$PLUGIN_ROOT") || return 1
    if [ "$PRODUCT" = both ]; then
    echo "Agent Notifications installed; continuing with Codex."
    fi
}

# A mapped Windows executable cannot delete itself. Run a temporary copy and
# preserve its status while cleaning up after the process has exited.
observer_remove_command() {
    local binary="$1" action="$2"
    shift 2
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            local script
            script=$(cat <<'REMOVE_SCRIPT'
set -eu
stage=$(mktemp -d "${TMPDIR:-/tmp}/agent-notifications-remove.XXXXXX")
trap 'status=$?; rm -rf "$stage"; exit "$status"' EXIT
cp "$1" "$stage/remover.exe"
shift
"$stage/remover.exe" "$@"
REMOVE_SCRIPT
)
            quote_shell_command bash -c "$script" _ "$binary" "$action" remove "$@" ;;
        *) quote_shell_command "$binary" "$action" remove "$@" ;;
    esac
}

opencode_remove_command() { observer_remove_command "$1" setup-opencode; }

# OpenCode setup owns its plugin, native executable and independent channel
# consent. Acquisition stays temporary; only the verified Go transaction writes
# the managed installation. It never launches or installs the OpenCode host.
install_opencode() {
    local root binary runtime os arch base native native_root config_path installed
    root=$(bootstrap_control_root) || return 1
    read -r os arch < <(bootstrap_release_os_arch) || return 1
    [ "$os-$arch" != windows-arm64 ] || { echo "Windows arm64 is not a supported release target." >&2; return 1; }
    binary="$_CONFIG_HELPER"
    runtime="${_FROZEN_RUNTIME:-$root/runtime}"
    if [ "$os" = windows ]; then
        command -v cygpath >/dev/null 2>&1 || { echo "Git Bash cygpath is required for native Windows paths." >&2; return 1; }
        binary=$(cygpath -m "$binary") || return 1
        runtime=$(cygpath -m "$runtime") || return 1
    fi
    set -- setup-opencode install --binary "$binary" "${OPENCODE_ARGS[@]}"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$root" --home "$INSTALLER_HOME"
        set -- "$@" --opencode-config-dir "$_FROZEN_OPENCODE"
    fi
    # Existing shared components supply their authoritative runtime directory.
    [ -e "$root/ownership.json" ] || set -- "$@" --runtime-root "$runtime"
    if [ "$os" = darwin ] && [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop "* ]]; then
        base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
        native="$_CONFIG_STAGE/ClaudeNotifier.app"
        if [ ! -d "$native" ]; then
            fetch_bootstrap_file "$base/ClaudeNotifier.app.zip" "$_CONFIG_STAGE/ClaudeNotifier.app.zip" || return 1
            verify_bootstrap_checksum "$_CONFIG_STAGE" ClaudeNotifier.app.zip || return 1
            unzip -q "$_CONFIG_STAGE/ClaudeNotifier.app.zip" -d "$_CONFIG_STAGE" || return 1
        fi
        [ -f "$native.managed-runtime.json" ] || { echo "Signed native helper attestation is missing." >&2; return 1; }
        # StageNative checks sealed decoder floor, attestation and OS signature.
        set -- "$@" --native-app "$native"
    fi
    progress_phase "$PRODUCT" registration state_requires_inspection
run_setup_stage "Installing OpenCode notifications" "$_CONFIG_HELPER" "$@" </dev/null || return 1
    progress_phase "$PRODUCT" runtime-lookup registration_committed
    native_root="$root"
    [ "$os" != windows ] || native_root=$(cygpath -m "$root") || return 1
    runtime=$("$_CONFIG_HELPER" config installer runtime-root "$native_root") || return 1
    installed="$runtime/claude-notifications-$os-$arch"
    [ "$os" != windows ] || installed="$installed.exe"
    progress_phase "$PRODUCT" config registration_committed
    run_setup_stage "Preparing notification settings" initialize_config || return 1
    config_path=$("$_CONFIG_HELPER" config path) || return 1
    local report
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-summary-XXXXXX") || return 1
    {
        echo "  OpenCode - installed; restart required to load the global plugin."
        echo "  Delivery has not been verified."
        echo "  Silent completion, question, permission and error alerts; no click-to-focus."
        printf '  Shared settings: %s\n' "$config_path"
        if [ "$os" = darwin ] && [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop "* ]]; then
            echo "  Check OS notification permission; grant it only if needed."
            printf '    Check: %s\n' "$(quote_shell_command "$installed" setup-opencode permission-status)"
            printf '    Grant if needed: %s\n' "$(quote_shell_command "$installed" setup-opencode request-permission)"
        fi
        if [[ " ${OPENCODE_ARGS[*]-} " = *" --webhook "* ]]; then
            echo "  Webhook consent recorded; configure and enable its destination and status channel in shared settings."
        fi
        echo "  Restart OpenCode, then complete a task to check notification delivery."
        printf '  Remove: %s\n' "$(opencode_remove_command "$installed")"
    } > "$report"
    publish_setup_summary "$report"
    local status=$?
    rm -f "$report"
    return "$status"
}

install_gemini() {
    local root binary runtime os arch base native native_root config_path installed
    root=$(bootstrap_control_root) || return 1
    read -r os arch < <(bootstrap_release_os_arch) || return 1
    [ "$os-$arch" != windows-arm64 ] || { echo "Windows arm64 is not a supported release target." >&2; return 1; }
    binary="$_CONFIG_HELPER"
    runtime="${_FROZEN_RUNTIME:-$root/runtime}"
    if [ "$os" = windows ]; then
        command -v cygpath >/dev/null 2>&1 || { echo "Git Bash cygpath is required for native Windows paths." >&2; return 1; }
        binary=$(cygpath -w "$binary") || return 1
        runtime=$(cygpath -m "$runtime") || return 1
    fi
    set -- setup-gemini install --binary "$binary" "${OPENCODE_ARGS[@]}"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$root" --home "$INSTALLER_HOME"
        set -- "$@" --config-root "$_FROZEN_GEMINI"
    fi
    # Existing shared components supply their authoritative runtime directory.
    [ -e "$root/ownership.json" ] || set -- "$@" --runtime-root "$runtime"
    if [ "$os" = darwin ] && [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop "* ]]; then
        base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
        native="$_CONFIG_STAGE/ClaudeNotifier.app"
        if [ ! -d "$native" ]; then
            fetch_bootstrap_file "$base/ClaudeNotifier.app.zip" "$_CONFIG_STAGE/ClaudeNotifier.app.zip" || return 1
            verify_bootstrap_checksum "$_CONFIG_STAGE" ClaudeNotifier.app.zip || return 1
            unzip -q "$_CONFIG_STAGE/ClaudeNotifier.app.zip" -d "$_CONFIG_STAGE" || return 1
        fi
        [ -f "$native.managed-runtime.json" ] || { echo "Signed native helper attestation is missing." >&2; return 1; }
        # StageNative checks sealed decoder floor, attestation and OS signature.
        set -- "$@" --native-app "$native"
    fi
    progress_phase "$PRODUCT" registration state_requires_inspection
run_setup_stage "Installing Gemini CLI notifications" "$_CONFIG_HELPER" "$@" </dev/null || return 1
    progress_phase "$PRODUCT" runtime-lookup registration_committed
    native_root="$root"
    [ "$os" != windows ] || native_root=$(cygpath -m "$root") || return 1
    runtime=$("$_CONFIG_HELPER" config installer runtime-root "$native_root") || return 1
    installed="$runtime/claude-notifications-$os-$arch"
    [ "$os" != windows ] || installed="$installed.exe"
    progress_phase "$PRODUCT" config registration_committed
    run_setup_stage "Preparing notification settings" initialize_config || return 1
    config_path=$("$_CONFIG_HELPER" config path) || return 1
    local report status
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-gemini-summary-XXXXXX") || return 1
    {
        echo "  Gemini CLI - installed; restart required."
        echo "  Delivery has not been verified."
        printf '  Shared settings: %s\n' "$config_path"
        echo "  Restart Gemini CLI and enable/trust the user hooks, then complete a task to check delivery."
        echo "  Silent turn completion and tool permission alerts; existing hook/security settings were preserved."
        echo "  Gemini's built-in desktop notifications may duplicate alerts; choose one desktop source or use --webhook only."
        printf '  Inspect registration and consent: %s\n' "$(quote_shell_command "$installed" setup-gemini inspect --control-root "$native_root")"
        if [ "$os" = darwin ] && [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop "* ]]; then
            printf '  Check permission: %s\n' "$(quote_shell_command "$installed" setup-gemini permission-status --control-root "$native_root")"
            printf '  Grant if needed: %s\n' "$(quote_shell_command "$installed" setup-gemini request-permission --control-root "$native_root")"
        fi
        if [[ " ${OPENCODE_ARGS[*]-} " = *" --webhook "* ]]; then
            echo "  Webhook consent recorded; configure and enable its destination and status channel in shared settings."
        fi
        printf '  Remove: %s\n' "$(gemini_remove_command "$installed" "$native_root")"
    } > "$report"
    publish_setup_summary "$report"
    status=$?
    rm -f "$report"
    return "$status"
}

gemini_remove_command() {
    observer_remove_command "$1" setup-gemini --control-root "$2"
}

# These rows describe observed phases; they never authorize another phase.
progress_phase() {
    local i=0
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        if [ "${_PROGRESS_IDS[$i]}" = "$1" ]; then
            _PROGRESS_PHASES[$i]=$2; _PROGRESS_OUTCOMES[$i]=$3; return 0
        fi
        i=$((i + 1))
    done
}

report_progress() {
    local i=0 outcome
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        outcome=${_PROGRESS_OUTCOMES[$i]}
        if [ "$outcome" = registration_committed ]; then
            outcome="registration_committed; follow_up_failed:${_PROGRESS_PHASES[$i]}"
        fi
        printf '%s: %s (phase: %s)\n' "${_PROGRESS_IDS[$i]}" "$outcome" "${_PROGRESS_PHASES[$i]}" >&2
        i=$((i + 1))
    done
}

install_legacy_products() {
    local selected status=0
    for selected in claude codex; do
        case ",$_PRODUCT_CSV," in *",$selected,"*) ;; *) continue ;; esac
        progress_phase "$selected" registration state_requires_inspection
        if [ "$selected" = claude ]; then
            CN_PRODUCT=claude run_setup_stage "Installing Claude notifications" install_claude || return $?
        else
            run_setup_stage "Installing Codex notifications" install_codex || status=$?
            if [ "$status" -eq 3 ]; then
                progress_phase codex config registration_committed
                report_config_init_failure || true
                return 1
            fi
            [ "$status" -eq 0 ] || return "$status"
        fi
        progress_phase "$selected" config registration_committed
        run_setup_stage "Preparing notification settings" initialize_config || return 1
        if [ "$CONFIGURE_NOTIFICATIONS" = true ]; then
            progress_phase "$selected" MCP registration_committed_follow_up_not_started
        else
            progress_phase "$selected" complete completed
        fi
    done
    if [ "$CONFIGURE_NOTIFICATIONS" = true ]; then
        for selected in claude codex; do
            case ",$_PRODUCT_CSV," in *",$selected,"*) progress_phase "$selected" MCP registration_committed ;; esac
        done
    fi
    run_setup_stage "Configuring agent notification tools" configure_agent_notify || return 1
    for selected in claude codex; do
        case ",$_PRODUCT_CSV," in *",$selected,"*) progress_phase "$selected" complete completed ;; esac
    done
    print_success || return 1
    return 0
}

report_partial_recovery() {
    local i=0 id outcome root
    root=$(bootstrap_control_root) || return 0
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        id=${_PROGRESS_IDS[$i]}; outcome=${_PROGRESS_OUTCOMES[$i]}
        case "$outcome" in
            not_started|completed) i=$((i + 1)); continue ;;
        esac
        case "$id" in
            gemini)
                local -a inspect=("$_CONFIG_HELPER" setup-gemini inspect --control-root "$root" --home "$INSTALLER_HOME")
                if [ -n "$_FROZEN_GEMINI" ]; then inspect+=(--config-root "$_FROZEN_GEMINI")
                elif [ -n "${GEMINI_CLI_HOME:-}" ]; then inspect+=(--gemini-home "$GEMINI_CLI_HOME"); fi
                printf 'Inspect Gemini state: %s\n' "$(quote_shell_command "${inspect[@]}")" >&2 ;;
            *)
                printf 'Inspect %s owned registration at %q/ownership.json before recovery of %s.\n' "$id" "$root" "${_PROGRESS_PHASES[$i]}" >&2 ;;
        esac
        if [ "$outcome" = registration_committed ] && [ "${_PROGRESS_PHASES[$i]}" = config ]; then
            local -a retry=("$_CONFIG_HELPER" config init --json)
            [ -z "${AGENT_NOTIFICATIONS_CONFIG:-}" ] || retry=(env "AGENT_NOTIFICATIONS_CONFIG=$AGENT_NOTIFICATIONS_CONFIG" "${retry[@]}")
            printf 'Config-only retry: %s\n' "$(quote_shell_command "${retry[@]}")" >&2
        fi
        i=$((i + 1))
    done
}

close_prompt() {
    if [ "$_PROMPT_OPEN" = true ]; then exec 3>&-; _PROMPT_OPEN=false; fi
}

# Retain the child status and raw bytes until the complete record is checked.
selector_collect() {
    local operation=$1; shift
    local status=0
    _SELECTOR_RESULT="$_CONFIG_STAGE/selector-$operation.result"
    if [ "$_PROMPT_OPEN" = true ]; then
        "$_CONFIG_HELPER" setup-products "$operation" "$@" <&3 2>&3 3>&- >"$_SELECTOR_RESULT" || status=$?
    else
        "$_CONFIG_HELPER" setup-products "$operation" "$@" </dev/null 3>&- >"$_SELECTOR_RESULT" || status=$?
    fi
    [ "$status" -eq 0 ] || return "$status"
    local size
    size=$(LC_ALL=C wc -c <"$_SELECTOR_RESULT" 3>&-) || return 1
    [ "$size" -le 64 ] || { echo "Oversized selector result." >&2; return 1; }
}

selector_exact() {
    printf '%s\n' "$1" 3>&- | cmp -s - "$_SELECTOR_RESULT" 3>&- || {
        echo "Invalid selector $2 result bytes." >&2; return 1;
    }
}

selector_record() {
    _SELECTOR_RECORD=""
    [ -s "$_SELECTOR_RESULT" ] || return 0
    IFS= read -r _SELECTOR_RECORD <"$_SELECTOR_RESULT" || return 1
    selector_exact "$_SELECTOR_RECORD" record || return 1
    local item remaining=$_SELECTOR_RECORD seen=""
    while :; do
        item=${remaining%%,*}
        case "$1:$item" in products:claude|products:codex|products:opencode|products:gemini|channels:desktop|channels:webhook) ;; *) echo "Invalid selector ID." >&2; return 1 ;; esac
        case ",$seen," in *",$item,"*) echo "Duplicate selector ID." >&2; return 1 ;; esac
        seen=${seen:+$seen,}$item
        case "$remaining" in *,*) remaining=${remaining#*,} ;; *) break ;; esac
    done
}

# Fixed scalar projection. No JSON, eval, fresh discovery, or default selection.
load_frozen_dispatch() {
    local LC_ALL=C
    local result="$_CONFIG_STAGE/intent-args.result" status=0 size key value seen=""
    "$_CONFIG_HELPER" setup-products intent-args --intent-file "$_SELECTOR_INTENT" >"$result" </dev/null || status=$?
    [ "$status" -eq 0 ] || return "$status"
    size=$(LC_ALL=C wc -c <"$result") || return 1
    [ "$size" -gt 0 ] && [ "$size" -le 65536 ] || return 1
    while :; do
        key=""
        if ! IFS= read -r -d '' key; then [ -z "$key" ] || return 1; break; fi
        IFS= read -r -d '' value || return 1
        case ",$seen," in *",$key,"*) return 1 ;; esac
        seen=${seen:+$seen,}$key
        [ "${#value}" -le 4096 ] || return 1
        case "$value" in /*|[A-Za-z]:/*|[A-Za-z]:\\*) ;; *) return 1 ;; esac
        case "$key" in
            home) INSTALLER_HOME=$value ;;
            claude-config) CLAUDE_HOME=$value; export CLAUDE_CONFIG_DIR="$value" CLAUDE_HOME ;;
            claude-mcp-config) _FROZEN_CLAUDE_MCP=$value ;;
            codex-home) DEFAULT_CODEX_HOME=$value; export CODEX_HOME="$value" ;;
            codex-mcp-config) _FROZEN_CODEX_MCP=$value ;;
            opencode-config-dir) _FROZEN_OPENCODE=$value; export OPENCODE_CONFIG_DIR="$value" ;;
            gemini-config-root) _FROZEN_GEMINI=$value ;;
            control-root) _FROZEN_CONTROL=$value ;;
            runtime-root) _FROZEN_RUNTIME=$value ;;
            global-config) export AGENT_NOTIFICATIONS_CONFIG="$value" ;;
            claude-executable) _CLAUDE_EXEC=$value ;;
            codex-executable) _CODEX_EXEC=$value ;;
            opencode-executable) _OPENCODE_EXEC=$value ;;
            gemini-executable) _GEMINI_EXEC=$value ;;
            *) return 1 ;;
        esac
    done <"$result"
    # Fresh Claude/Codex has no managed runtime until its existing hooks
    # preparation succeeds. Keep an observed runtime when present; never invent
    # one or pass an empty override into the later portable admission.
    local required="home control-root global-config" selected
    for selected in "${SELECTED_PRODUCTS[@]}"; do
        case "$selected" in
            claude) required="$required claude-config claude-mcp-config claude-executable" ;;
            codex) required="$required codex-home codex-mcp-config codex-executable" ;;
            opencode) required="$required opencode-config-dir opencode-executable" ;;
            gemini) required="$required gemini-config-root gemini-executable" ;;
        esac
    done
    for key in $required; do case ",$seen," in *",$key,"*) ;; *) echo "Missing frozen authority: $key" >&2; return 1 ;; esac; done
    # Legacy writers resolve os.UserConfigDir rather than accepting a public
    # bootstrap control flag. Freeze their existing Linux/Windows authority too.
    case "$(uname -s)" in
        Linux)
            case "$_FROZEN_CONTROL" in */agent-notifications) export XDG_CONFIG_HOME="${_FROZEN_CONTROL%/agent-notifications}" ;; *) echo "Frozen legacy control authority cannot be mapped." >&2; return 1 ;; esac ;;
        MINGW*|MSYS*|CYGWIN*)
            case "$_FROZEN_CONTROL" in
                */agent-notifications) export APPDATA="${_FROZEN_CONTROL%/agent-notifications}" ;;
                *\\agent-notifications) export APPDATA="${_FROZEN_CONTROL%\\agent-notifications}" ;;
                *) echo "Frozen legacy control authority cannot be mapped." >&2; return 1 ;;
            esac ;;
    esac
    local i=0
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        if [ "${CONFIGURE_ARGS[$i]}" = --codex-home ]; then CONFIGURE_ARGS[$((i + 1))]=$DEFAULT_CODEX_HOME; fi
        i=$((i + 1))
    done
    INSTALLED_JSON="$CLAUDE_HOME/plugins/installed_plugins.json"
    CACHE_DIR="$CLAUDE_HOME/plugins/cache/$MARKETPLACE_NAME"
    MARKETPLACE_DIR="$CLAUDE_HOME/plugins/marketplaces/$MARKETPLACE_NAME"
    MARKETPLACE_PLUGIN_JSON="$MARKETPLACE_DIR/.claude-plugin/plugin.json"
}

# Normalize the existing request once, then relay the same argv to summary and
# checkpoint. JSON is a formatting flag for legacy explicit configure only.
selector_scope_args() {
    _SCOPE_ARGS=()
    local i=0
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        if [ "${CONFIGURE_ARGS[$i]}" = --codex-home ]; then
            _SCOPE_ARGS+=(--codex-home "${CONFIGURE_ARGS[$((i + 1))]}")
        fi
        i=$((i + 1))
    done
}

selector_effect_args() {
    _EFFECT_ARGS=(--products "$_PRODUCT_CSV")
    if [ -n "$LEGACY_PRODUCT" ]; then
        case "$AGENT_NOTIFY_REQUEST" in explicit) _EFFECT_ARGS+=(--agent-notify) ;; skip) _EFFECT_ARGS+=(--skip-agent-notify) ;; esac
        local i=0
        while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
            case "${CONFIGURE_ARGS[$i]}" in
                --codex-home) i=$((i + 2)); continue ;;
                --json) i=$((i + 1)); continue ;;
            esac
            _EFFECT_ARGS+=("${CONFIGURE_ARGS[$i]}"); i=$((i + 1))
        done
    fi
    _EFFECT_ARGS+=(${OPENCODE_ARGS[@]+"${OPENCODE_ARGS[@]}"})
}

main() {
    if [ "$#" -eq 1 ]; then
        case "$1" in
            --capabilities) printf '%s\n' bootstrap-products-v1; return 0 ;;
            --selector-capabilities) printf '%s\n' terminal-selector-v1; return 0 ;;
        esac
    fi
    select_product "$@" || return 1
    # Plain/accessibility output and the caller's explicit NO_COLOR decision
    # apply to bootstrap diagnostics as well as the shared prompt renderer.
    if [ -n "${NO_COLOR:-}" ] || [ "$_UI_MODE" = plain ] || [ "${TERM:-}" = dumb ]; then
        BOLD=""; GREEN=""; BLUE=""; YELLOW=""; RED=""; NC=""
    fi
    local selected status=0 original_product=$PRODUCT
    _PROGRESS_IDS=(); _PROGRESS_PHASES=(); _PROGRESS_OUTCOMES=()
    install_cleanup_traps
    if [ "$_INTERACTIVE_INTENT" = true ]; then
        if ! { exec 3<>/dev/tty; } 2>/dev/null; then
            echo "No controlling TTY. Specify complete explicit product/route/channel input." >&2; return 1
        fi
        _PROMPT_OPEN=true
    fi
    abort_if_wsl_environment 3>&-
    detect_platform 3>&-
    require_installer_runtime 3>&- || return 1
    resolve_bootstrap_release 3>&- || return 1
    run_setup_stage "Preparing verified installer" stage_config_helper 3>&- >&2 || { echo "Cannot stage verified config helper; existing runtime retained." >&2; return 1; }
    export BOOTSTRAP_RELEASE_TAG="$BOOTSTRAP_TAG" BOOTSTRAP_RELEASE_COMMIT="$BOOTSTRAP_COMMIT"
    export BOOTSTRAP_SOURCE_COMMIT="$BOOTSTRAP_BUNDLE_COMMIT" BOOTSTRAP_SOURCE_REF="${BOOTSTRAP_SOURCE_REF:-}"
    export BOOTSTRAP_RELEASE_CHANNEL="${BOOTSTRAP_RELEASE_CHANNEL:-}"
    if [ "$_INTERACTIVE_INTENT" = true ] || [[ "$original_product" = bundle:* ]] || [ "$original_product" = gemini ]; then
        selector_collect capabilities || return $?
        selector_exact 'setup-products-v1 claude codex opencode gemini' capabilities || return 1
    fi
    if [ "$_INTERACTIVE_INTENT" = true ]; then
        selector_collect features || return $?
        selector_exact terminal-selector-v1 features || return 1
    fi
    if [ "$_SELECTION_PENDING" = true ]; then
        selector_scope_args
        selector_collect select ${_SCOPE_ARGS[@]+"${_SCOPE_ARGS[@]}"} ${UI_ARGS[@]+"${UI_ARGS[@]}"} || return $?
        selector_record products || return 1
        [ -n "$_SELECTOR_RECORD" ] || { close_prompt; return 0; }
        PRODUCT=""; SELECTED_PRODUCTS=(); LEGACY_PRODUCT=""; _SELECTION_PENDING=false
        # Parse the exact accepted set with the original flags; retain interaction.
        OPENCODE_ARGS=(); CONFIGURE_ARGS=()
        select_product --products "$_SELECTOR_RECORD" "$@" || return 1
    fi
    _PRODUCT_CSV=""
    # Canonical product order remains the existing command order.
    for selected in claude codex opencode gemini; do
        case " ${SELECTED_PRODUCTS[*]} " in *" $selected "*) _PRODUCT_CSV=${_PRODUCT_CSV:+$_PRODUCT_CSV,}$selected ;; esac
    done
    if [ "$_CHANNEL_PENDING" = true ]; then
        selector_collect channels --products "$_PRODUCT_CSV" ${UI_ARGS[@]+"${UI_ARGS[@]}"} || return $?
        selector_record channels || return 1
        [ -n "$_SELECTOR_RECORD" ] || { close_prompt; return 0; }
        case ",$_SELECTOR_RECORD," in *",desktop,"*) OPENCODE_ARGS+=(--desktop) ;; esac
        case ",$_SELECTOR_RECORD," in *",webhook,"*) OPENCODE_ARGS+=(--webhook) ;; esac
    fi
    selector_effect_args
    if [ "$_INTERACTIVE_INTENT" = true ]; then
        _SELECTOR_INTENT="$_CONFIG_STAGE/selector-intent.json"
        selector_scope_args
        selector_collect confirm "${_EFFECT_ARGS[@]}" ${_SCOPE_ARGS[@]+"${_SCOPE_ARGS[@]}"} --intent-file "$_SELECTOR_INTENT" ${UI_ARGS[@]+"${UI_ARGS[@]}"} || return $?
        [ -s "$_SELECTOR_RESULT" ] || { close_prompt; return 0; }
        selector_exact approved confirm || return 1
        [ -f "$_SELECTOR_INTENT" ] && [ ! -L "$_SELECTOR_INTENT" ] || { echo "Missing confirmed intent." >&2; return 1; }
    fi
    close_prompt
    if [ -n "$_SELECTOR_INTENT" ]; then load_frozen_dispatch || return $?; fi
    for selected in "${SELECTED_PRODUCTS[@]}"; do
        PRODUCT=$selected
        check_prerequisites || return $?
        _PROGRESS_IDS+=("$selected"); _PROGRESS_PHASES+=(pending); _PROGRESS_OUTCOMES+=(not_started)
    done
    PRODUCT=$original_product
    [ -n "${BOOTSTRAP_SUMMARY_FILE:-}" ] || print_header
    local own_summary=false
    if [ "${#SELECTED_PRODUCTS[@]}" -gt 1 ] && [ -z "${BOOTSTRAP_SUMMARY_FILE:-}" ]; then
        BOOTSTRAP_SUMMARY_FILE="$_CONFIG_STAGE/summary.txt"
        export BOOTSTRAP_SUMMARY_FILE
        own_summary=true
    fi
    if [ -n "$LEGACY_PRODUCT" ]; then
        PRODUCT=$LEGACY_PRODUCT
        run_setup_stage "Checking existing installation" stage_historical_baselines || return 1
        run_setup_stage "Checking shared settings" config_preflight || return 1
    fi
    if [ -n "$_SELECTOR_INTENT" ]; then
        selector_collect preflight --intent-file "$_SELECTOR_INTENT" "${_EFFECT_ARGS[@]}" || return $?
        [ ! -s "$_SELECTOR_RESULT" ] || { echo "Invalid preflight stdout." >&2; return 1; }
    fi
    # One initial checkpoint; writers retain their own existing CAS/locks.
    if [ -n "$LEGACY_PRODUCT" ]; then
        PRODUCT=$LEGACY_PRODUCT
        install_legacy_products || status=$?
    fi
    if [ "$status" -eq 0 ]; then
        for selected in opencode gemini; do
            case ",$_PRODUCT_CSV," in *",$selected,"*) ;; *) continue ;; esac
            PRODUCT=$selected
            progress_phase "$selected" preparation state_requires_inspection
            case "$selected" in opencode) install_opencode || status=$? ;; gemini) install_gemini || status=$? ;; esac
            [ "$status" -eq 0 ] || break
            progress_phase "$selected" complete completed
        done
    fi
    report_progress
    if [ "$status" -ne 0 ]; then
        report_partial_recovery
        _KEEP_CONFIG_STAGE=true
        printf 'Partial setup or uncertain state. Inspect the failed product/phase before recovery; completed siblings must not be reinstalled.\nVerified helper retained: %q\n' "$_CONFIG_HELPER" >&2
    fi
    if [ "$own_summary" = true ]; then
        if [ "$status" -eq 0 ]; then
            printf '\nInstallation complete\n\n'
            cat "$BOOTSTRAP_SUMMARY_FILE"
            printf '\nDetailed installer output: rerun with BOOTSTRAP_VERBOSE=1.\n'
        fi
        unset BOOTSTRAP_SUMMARY_FILE
    fi
    return "$status"
}

# Quote argv so a user can paste the retry command into bash. Custom roots with
# spaces must survive this string (§9.2 / §9.4). Do not print the result through
# echo -e: printf %q emits backslashes that -e would interpret.
quote_shell_command() {
    local quoted="" arg
    for arg in "$@"; do
        quoted="${quoted:+$quoted }$(printf '%q' "$arg")"
    done
    printf '%s' "$quoted"
}

# Agent-notify is default-on. A failed setup must not undo hooks/plugin install,
# but it is incomplete: bootstrap does not print overall success.
configure_agent_notify() {
    [ "$CONFIGURE_NOTIFICATIONS" = true ] || { CLAUDE_AGENT_NOTIFY_STATUS="skipped (existing setup kept)"; CODEX_AGENT_NOTIFY_STATUS="$CLAUDE_AGENT_NOTIFY_STATUS"; return 0; }
    if [ -z "$CONFIGURE_BINARY" ]; then
        CONFIGURE_BINARY=$(installed_notification_binary "$PLUGIN_ROOT") || return 1
    fi
    if ! notification_command_available "$CONFIGURE_BINARY"; then
        echo -e "${YELLOW}⚠ Agent-notify setup skipped; installer binary not found.${NC}" >&2
        echo -e "${YELLOW}  Plugin/hooks install succeeded. Retry after the binary is available.${NC}" >&2
        if [ "$AGENT_NOTIFY_REQUEST" = explicit ] || [ -n "$_SELECTOR_INTENT" ]; then return 1; fi
        return 0
    fi
    case "$(uname -s 2>/dev/null)" in
        Darwin|Linux|MINGW*|MSYS*|CYGWIN*) use_wizard=true ;;
        *) use_wizard=false ;;
    esac
    if [ "$use_wizard" = true ] && cli_has_setup_wizard "$CONFIGURE_BINARY"; then
        setup_agent_notify_wizard
        return $?
    fi
    if [ -n "$_SELECTOR_INTENT" ]; then
        echo "Confirmed portable phase requires the matching setup wizard; hooks registration must be inspected separately." >&2
        return 1
    fi
    if ! cli_has_setup_notifications "$CONFIGURE_BINARY"; then
        echo -e "${YELLOW}⚠ Agent-notify setup skipped; this published CLI does not support setup-notifications.${NC}" >&2
        echo -e "${YELLOW}  Plugin/hooks registration completed; notification delivery is unverified.${NC}" >&2
        if [ "$AGENT_NOTIFY_REQUEST" = explicit ] || [ -n "$_SELECTOR_INTENT" ]; then return 1; fi
        return 0
    fi
    configure_agent_policy || return 1
    CLAUDE_AGENT_NOTIFY_STATUS="configured; activation not verified"
    CODEX_AGENT_NOTIFY_STATUS="$CLAUDE_AGENT_NOTIFY_STATUS"
}

configure_agent_policy() {
    case "$(uname -s 2>/dev/null)" in
        Darwin|Linux|MINGW*|MSYS*|CYGWIN*) ;;
        *)
            echo -e "${YELLOW}⚠ Agent-notify MCP skipped: unsupported_platform (this installer supports macOS, Linux, and Windows).${NC}" >&2
            echo -e "${YELLOW}  Plugin/hooks install succeeded. Not a full MCP installation.${NC}" >&2
            [ "$AGENT_NOTIFY_REQUEST" = explicit ] && return 1
            return 0 ;;
    esac
    local -a configure_cmd=(setup-notifications configure --provider "$PRODUCT")
    configure_cmd+=(${CONFIGURE_ARGS[@]+"${CONFIGURE_ARGS[@]}"})
    if [ "${1:-}" = portable ]; then
        configure_cmd+=(--policy-only --preserve-enabled)
    fi
    if ! "$CONFIGURE_BINARY" "${configure_cmd[@]}"; then
        echo -e "${YELLOW}⚠ Agent-notify setup failed; plugin/hooks install succeeded.${NC}" >&2
        echo -e "${YELLOW}  Plugin/hooks registration completed; inspect policy state before this config-only retry:${NC}" >&2
        printf '  %s\n' "$(quote_shell_command "$CONFIGURE_BINARY" "${configure_cmd[@]}")" >&2
        return 1
    fi
    return 0
}

# Same-tag portable zip is the wizard source. Auto-default without that asset is
# hooks-only, not a full MCP install. Explicit --agent-notify is incomplete.
report_wizard_portable_missing() {
    local reason="$1"
    echo -e "${YELLOW}⚠ Agent-notify wizard skipped; ${reason}.${NC}" >&2
    echo -e "${YELLOW}  Plugin/hooks registration completed. Inspect the selected clients and owned registration before resuming the portable phase with the verified package for this release.${NC}" >&2
    [ -z "${BOOTSTRAP_TAG:-}" ] || printf '  Release: %s\n' "$BOOTSTRAP_TAG" >&2
    if [ "$AGENT_NOTIFY_REQUEST" = explicit ] || [ -n "$_SELECTOR_INTENT" ]; then
        return 1
    fi
    return 0
}

bootstrap_abs_command() {
    local found
    found=$(command -v "$1" 2>/dev/null) || return 1
    case "$found" in
        /*|[A-Za-z]:/*|[A-Za-z]:\\*) printf '%s\n' "$found" ;;
        *) return 1 ;;
    esac
}

bootstrap_release_os_arch() {
    local os arch
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in darwin|linux) ;; mingw*|msys*|cygwin*) os=windows ;; *) return 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) return 1 ;; esac
    printf '%s %s\n' "$os" "$arch"
}

# Same-tag portable zip is the install source for agent-notify. Git templates
# without the release binary are only a last-resort offline fallback.
acquire_wizard_portable_asset() {
    local os arch asset base stage
    [ -n "${BOOTSTRAP_TAG:-}" ] || return 1
    read -r os arch < <(bootstrap_release_os_arch) || return 1
    asset="agent-notify-portable-${os}-${arch}.zip"
    _PORTABLE_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-portable-XXXXXX") || return 1
    # Wizard paths must be clean and physical, including TMPDIR aliases and /tmp on macOS.
    _PORTABLE_STAGE=$(cd -P "$_PORTABLE_STAGE" && pwd -P) || return 1
    stage="$_PORTABLE_STAGE"
    base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
    if [ ! -f "$stage/checksums.txt" ]; then
        fetch_bootstrap_file "$base/checksums.txt" "$stage/checksums.txt" || return 1
    fi
    fetch_bootstrap_file "$base/$asset" "$stage/$asset" || return 1
    verify_bootstrap_checksum "$stage" "$asset" || return 1
    WIZARD_PACKAGE_ROOT="$stage/$asset"
}

setup_agent_notify_wizard() {
    local policy_help
    policy_help=$("$CONFIGURE_BINARY" setup-notifications --help </dev/null 2>/dev/null) || policy_help=""
    if [[ "$policy_help" != *--policy-only* || "$policy_help" != *--preserve-enabled* ]]; then
        echo "agent-notify setup skipped; installed binary predates portable setup. Plugin/hooks install succeeded. Update to a matching release before enabling MCP." >&2
        if [ "$AGENT_NOTIFY_REQUEST" = explicit ] || [ -n "$_SELECTOR_INTENT" ]; then return 1; fi
        return 0
    fi
    local agents package_root install_root wizard_codex_home="$DEFAULT_CODEX_HOME" i=0
    local claude_exec="" codex_exec="" plugin_root
    WIZARD_PACKAGE_ROOT=""
    case "$PRODUCT" in
        claude) agents=claude ;;
        codex) agents=codex ;;
        both) agents=claude,codex ;;
        *) echo "invalid product for wizard: $PRODUCT" >&2; return 1 ;;
    esac
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        if [ "${CONFIGURE_ARGS[$i]}" = "--codex-home" ]; then
            i=$((i + 1))
            wizard_codex_home="${CONFIGURE_ARGS[$i]}"
        fi
        i=$((i + 1))
    done
    if [ "$PRODUCT" != claude ] && [ -z "$wizard_codex_home" ]; then
        echo "HOME, USERPROFILE, CODEX_HOME, or --codex-home is required for Codex setup." >&2
        return 1
    fi
    plugin_root="$PLUGIN_ROOT"
    if [ -z "$plugin_root" ]; then
        plugin_root=$(cd "$(dirname "$CONFIGURE_BINARY")/.." && pwd)
    fi
    if [ -n "${BOOTSTRAP_TAG:-}" ]; then
        if ! acquire_wizard_portable_asset; then
            report_wizard_portable_missing "portable package is missing from $BOOTSTRAP_TAG" && return 0
            return 1
        fi
        package_root="$WIZARD_PACKAGE_ROOT"
    elif [ -f "$plugin_root/portable-package/plugin.json" ]; then
        package_root="$plugin_root/portable-package"
    else
        install_root=$(cd "$(dirname "$CONFIGURE_BINARY")/.." && pwd)
        if [ -f "$install_root/portable-package/plugin.json" ]; then
            package_root="$install_root/portable-package"
            plugin_root="$install_root"
        fi
    fi
    if [ -z "${package_root:-}" ]; then
        report_wizard_portable_missing "portable package is missing from the accepted release" && return 0
        return 1
    fi
    # Preserve policy enablement and accepted route/consent before the wizard
    # migrates client registration to the portable package.
    configure_agent_policy portable || return 1
    if [ -n "$_SELECTOR_INTENT" ]; then
        [ "$PRODUCT" = codex ] || claude_exec=$_CLAUDE_EXEC
        [ "$PRODUCT" = claude ] || codex_exec=$_CODEX_EXEC
    else
        if [ "$PRODUCT" != codex ]; then claude_exec=$(bootstrap_abs_command "$_CLAUDE_EXEC") || true; fi
        if [ "$PRODUCT" != claude ]; then codex_exec=$(bootstrap_abs_command "$_CODEX_EXEC") || true; fi
    fi
    set -- setup-notifications wizard --action install --install-or-update --agents "$agents" --hooks false --agent-notify true --yes \
        --package "$package_root" --plugin-root "$plugin_root"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --bootstrap-intent-file "$_SELECTOR_INTENT" --control-root "$_FROZEN_CONTROL" --global-config "$AGENT_NOTIFICATIONS_CONFIG"
        [ -z "$_FROZEN_RUNTIME" ] || set -- "$@" --runtime-root "$_FROZEN_RUNTIME"
        [ -z "$_FROZEN_CODEX_MCP" ] || set -- "$@" --mcp-config "$_FROZEN_CODEX_MCP"
    else
        [ "$AGENT_NOTIFY_REQUEST" != auto ] || set -- "$@" --preserve-existing-units
    fi
    case "$(uname -s 2>/dev/null)" in
        MINGW*|MSYS*|CYGWIN*) ;; # Go resolves the native managed .exe from the installed primary.
        *) set -- "$@" --helper "$CONFIGURE_BINARY" ;;
    esac
    set -- "$@" --codex-home "$wizard_codex_home" --claude-config "$CLAUDE_HOME"
    if [ "$PRODUCT" != codex ]; then
        set -- "$@" --claude-mcp-config "${_FROZEN_CLAUDE_MCP:-${CLAUDE_CONFIG_DIR:-$INSTALLER_HOME}/.claude.json}"
    fi
    [ -z "$claude_exec" ] || set -- "$@" --claude-executable "$claude_exec"
    [ -z "$codex_exec" ] || set -- "$@" --codex-executable "$codex_exec"
    if [ "$PRODUCT" != both ] && [ -n "$claude_exec$codex_exec" ]; then
        if [ -n "$claude_exec" ]; then
            set -- "$@" --client-executable "$claude_exec"
        else
            set -- "$@" --client-executable "$codex_exec"
        fi
    fi
    local wizard_result wizard_status=0
    wizard_result=$(mktemp "${TMPDIR:-/tmp}/bootstrap-wizard-XXXXXX") || return 1
    "$CONFIGURE_BINARY" "$@" > "$wizard_result" || wizard_status=$?
    if [ "$wizard_status" -ne 0 ]; then
        cat "$wizard_result"
        rm -f "$wizard_result"
        # The retry command refers to the verified same-release ZIP. Retain its
        # staging directory so EXIT cleanup cannot invalidate that command.
        if [ -n "$_PORTABLE_STAGE" ] && [[ "$package_root" = "$_PORTABLE_STAGE/"* ]]; then
            _KEEP_PORTABLE_STAGE=true
        fi
        echo -e "${YELLOW}⚠ Agent-notify setup failed; plugin/hooks install succeeded.${NC}" >&2
        echo -e "${YELLOW}  Inspect the selected clients and owned registration before resuming the failed MCP phase; notification delivery is unverified.${NC}" >&2
        [ "$_KEEP_PORTABLE_STAGE" != true ] || printf '  Verified package retained: %s\n' "$package_root" >&2
        echo "  Use the wizard's retry or next resume command above; it retains the selected clients and owned identity." >&2
        case "$(uname -s 2>/dev/null)" in MINGW*|MSYS*|CYGWIN*) echo "  On Windows, run that command in PowerShell (the printed quoting is PowerShell syntax)." >&2 ;; esac
        echo "  If no safe resume command is shown, inspect the owned state and obtain fresh consent for the failed phase; preserve completed siblings." >&2
        return 1
    fi
    if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
        cat "$wizard_result"
    fi
    # Keep failure output human and runnable; only the post-commit read uses JSON.
    set -- setup-notifications wizard --action inspect --agents "$agents" --json \
        --codex-home "$wizard_codex_home" --claude-config "$CLAUDE_HOME"
    [ "$PRODUCT" = codex ] || set -- "$@" --claude-mcp-config "${_FROZEN_CLAUDE_MCP:-${CLAUDE_CONFIG_DIR:-$INSTALLER_HOME}/.claude.json}"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$_FROZEN_CONTROL" --global-config "$AGENT_NOTIFICATIONS_CONFIG"
        [ -z "$_FROZEN_RUNTIME" ] || set -- "$@" --runtime-root "$_FROZEN_RUNTIME"
        [ -z "$_FROZEN_CODEX_MCP" ] || set -- "$@" --mcp-config "$_FROZEN_CODEX_MCP"
    fi
    if ! "$CONFIGURE_BINARY" "$@" > "$wizard_result" 2>/dev/null; then
        : > "$wizard_result"
        echo "Warning: installation finished, but agent notification tool status could not be checked." >&2
    fi
    CLAUDE_AGENT_NOTIFY_STATUS=$(wizard_tool_status "$wizard_result" claude "$AGENT_NOTIFY_REQUEST")
    CODEX_AGENT_NOTIFY_STATUS=$(wizard_tool_status "$wizard_result" codex "$AGENT_NOTIFY_REQUEST")
    rm -f "$wizard_result"
    return 0
}

# --json is the stable operator contract. A successful preserved opt-out is
# different from an installed tool. Inspect omits the mutation reason, so auto
# supplies the preserve-existing intent; missing results stay explicitly unknown.
wizard_tool_status() {
    local engine status=""
    engine=$(installer_runtime) || { printf '%s\n' 'setup completed; status not checked'; return 0; }
    if [ "$engine" = python3 ]; then
        status=$(python3 -I - "$1" "$2" "${3:-}" <<'PY_STATUS'
import json, sys
try:
    result = json.load(open(sys.argv[1]))
    rows = [t for t in result.get('targets', []) if t.get('client') == sys.argv[2] and t.get('unit') == 'agent-notify']
    if len(rows) == 1:
        target = rows[0]
        if target.get('outcome') in ('completed', 'installed'):
            print('installed')
        elif target.get('outcome') == 'absent':
            print('not installed (existing opt-out kept)' if (target.get('reason') == 'preserved_existing_opt_out' or sys.argv[3] == 'auto') else 'not installed')
except (ValueError, OSError, TypeError, AttributeError):
    pass
PY_STATUS
) || status=""
    else
        status=$(run_isolated_node - "$1" "$2" "${3:-}" <<'JS_STATUS'
try {
    const r = JSON.parse(require('fs').readFileSync(process.argv[2], 'utf8'));
    const rows = (r.targets || []).filter(t => t.client === process.argv[3] && t.unit === 'agent-notify');
    if (rows.length === 1) {
        const t = rows[0];
        if (['completed', 'installed'].includes(t.outcome)) console.log('installed');
        else if (t.outcome === 'absent') console.log((t.reason === 'preserved_existing_opt_out' || process.argv[4] === 'auto') ? 'not installed (existing opt-out kept)' : 'not installed');
    }
} catch (_) {}
JS_STATUS
) || status=""
    fi
    printf '%s\n' "${status:-setup completed; status not checked}"
}

main "$@"
