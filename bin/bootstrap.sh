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
LEGACY_MARKETPLACE_REPOS="777genius/claude-notifications-go 777genius/agent-notifications"

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
_PARALLEL_ROOT=""
_PARALLEL_ACTIVE=false
_PARALLEL_IN_FLIGHT=false
_PARALLEL_DISPATCHING=false
_PARALLEL_INTERRUPT_STATUS=0
_BOOTSTRAP_JOB_QUERY=false
_PARALLEL_WORKER=""
_PARALLEL_GATE_HELD=false
_PARALLEL_GATE_ABSENT_FAILURES=0
_PARALLEL_PIDS=()
_PARALLEL_AUX_PIDS=()
_PARALLEL_AUX_DONE=()
_PARALLEL_FORCED_STOP=false
_PARALLEL_HELPER_CAPTURE=""
_PARALLEL_FOLLOW_UP_STARTED=false
_PARALLEL_CLIENTS=()
_PARALLEL_DONE=()
_PARALLEL_LINES=()
_PARALLEL_READY_CLIENTS=""
_PARALLEL_POLICY_READY=false
_PARALLEL_MONITOR_WAS_SET=false
_FINAL_BOARD_RENDERED=false
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
_FROZEN_OPENCODE_CHANNELS=()
_FROZEN_GEMINI_CHANNELS=()
_FROZEN_QODER_CHANNELS=()
_FROZEN_PI_CHANNELS=()
_FROZEN_CURSOR_DESKTOP=false
_FROZEN_CURSOR_WEBHOOK=false
_FROZEN_CURSOR_SEED=false
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
_FROZEN_GLOBAL_CONFIG=""
_FROZEN_RUNTIME=""
_FROZEN_CLAUDE_MCP=""
_FROZEN_CODEX_MCP=""
_FROZEN_OPENCODE=""
_FROZEN_GEMINI=""
_FROZEN_QODER=""
_FROZEN_PI=""
_PI_SETUP_CAPABLE=false
_FROZEN_SCOPE_ROOT=""
_FROZEN_CLIENT_EXEC=""
_STATE_MIGRATION_CAPABLE=false
_FROZEN_STATE_MIGRATION_REQUIRED=""
_STATE_MIGRATION_COMPLETED=false
_CLAUDE_EXEC=claude
_CODEX_EXEC=codex
_OPENCODE_EXEC=opencode
_GEMINI_EXEC=gemini
_QODER_EXEC=qoder
_PI_EXEC=pi
_PROGRESS_IDS=()
_PROGRESS_PHASES=()
_PROGRESS_OUTCOMES=()
_PROGRESS_PID=""
_PROGRESS_EVENTS=""
_PROGRESS_KIND=""
_PROGRESS_GLOBAL_PHASE=preparation
_PROGRESS_LOG=""
_PROGRESS_STDERR=""
_DIAGNOSTIC_LOG=""
_DIAGNOSTIC_ROOT=""
_DIAGNOSTIC_FAILED=false
_DIAGNOSTIC_LABEL=""
_DIAGNOSTIC_NOTED=false
_SETUP_FAILURE_REASON=setup_failed
_SETUP_RECOVERY=unavailable
_PROGRESS_CAP_HELPER=""
_PROGRESS_CAP_SUPPORTED=false
_PROGRESS_SESSION_SUPPORTED=false
_PROGRESS_JOURNAL=""
_PROGRESS_JOURNAL_DISABLED=false
_PROGRESS_STOP_FILE=""
_ACTIVITY_MODE=compact
_ACTIVITY_LIMIT=0
_ACTIVITY_EXPLICIT=false

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

    # PowerShell can resolve bash to WSL bash.exe without WSL_* in the env.
    # These markers exist only inside WSL; a bare /mnt/c is not enough since
    # dual-boot Linux often mounts the Windows drive there.
    local probe_root="${CLAUDE_NOTIFICATIONS_WSL_PROBE_ROOT:-}"
    if [ -e "$probe_root/proc/sys/fs/binfmt_misc/WSLInterop" ] \
        || [ -e "$probe_root/proc/sys/fs/binfmt_misc/WSLInterop-late" ] \
        || [ -d "$probe_root/run/WSL" ]; then
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

# Keep untrusted CLI output and paths printable and bounded in diagnostics.
bootstrap_diagnostic_quote() {
    local LC_ALL=C
    local value=${1:0:512} quoted
    printf -v quoted '%q' "$value"
    printf '%s' "${quoted:0:512}"
    if [ "${#1}" -gt 512 ] || [ "${#quoted}" -gt 512 ]; then printf '...'; fi
}

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
    if [ "$PRODUCT" = gemini ] && ! command -v "$_GEMINI_EXEC" >/dev/null 2>&1; then
        echo "gemini CLI not found in PATH; install Gemini CLI first." >&2
        return 1
    fi
    if [ "$PRODUCT" = qoder ] && ! command -v "$_QODER_EXEC" >/dev/null 2>&1; then
        echo "Qoder CLI not found in selected PATH; install Qoder CLI first." >&2
        return 1
    fi
    if [ "$PRODUCT" = pi ] && ! command -v "$_PI_EXEC" >/dev/null 2>&1; then
        echo "Pi CLI not found in selected PATH; install Pi first." >&2
        return 1
    fi
    # Do not add host CLI min/max ranges, exact pins, or prerelease filters here.
    # New agent releases must not require an installer update just for admission.
    # Test-version pins are reproducibility evidence, not compatibility allowlists.
    # Capability/schema failures are handled by each integration at its boundary.
    if { [ "$PRODUCT" = opencode ] || [ "$PRODUCT" = gemini ] || [ "$PRODUCT" = qoder ] || [ "$PRODUCT" = pi ]; } && [ "$(bootstrap_release_os_arch)" = "windows arm64" ]; then
        echo "Windows arm64 is not supported." >&2
        return 1
    fi
    if { [ "$PRODUCT" = opencode ] || [ "$PRODUCT" = gemini ] || [ "$PRODUCT" = qoder ] || [ "$PRODUCT" = pi ]; } && [ "$(uname -s)" = Darwin ] &&
        { [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop "* ]] || [[ " ${OPENCODE_ARGS[*]-} " = *" --desktop=true "* ]] || [[ " ${_FROZEN_OPENCODE_CHANNELS[*]-} ${_FROZEN_GEMINI_CHANNELS[*]-} ${_FROZEN_QODER_CHANNELS[*]-} " = *" --desktop=true "* ]] || [[ " ${_FROZEN_PI_CHANNELS[*]-} " = *" --pi-desktop=true "* ]]; } && ! command -v unzip >/dev/null 2>&1; then
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

channel_install_plugin() { bootstrap_writer_gate channel_install_plugin_transaction "$@"; }

channel_install_plugin_transaction() (
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
            "$backup"/skills/agent-notifications/SKILL.md; do
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
    # The refreshed plugin root must match the current package: the legacy
    # skill was dropped from skills/ in v1.47.0 and Claude Code would load a
    # leftover copy as a second model-invocable skill (issue #490).
    if [ -n "$PLUGIN_ROOT" ] && [ -e "$PLUGIN_ROOT/skills/agent-notify" ]; then
        config_preflight || return 1
        rm -rf -- "$PLUGIN_ROOT/skills/agent-notify" || return 1
    fi
    # Retire repository-only metadata left by an overlay source refresh.
    # These files were never SDK-managed; preserve edited contracts by refusing.
    local retired
    for retired in E1-PORTS.md E2-PORTS.md E2A-PORTS.md E2-JS-PORTS.md CLOCK-MATRIX-PORTS.md; do
        # The selected release may still use the historical root layout.
        [ -f "$PLUGIN_ROOT/docs/opencode/$retired" ] || continue
        if [ -e "$PLUGIN_ROOT/$retired" ] || [ -L "$PLUGIN_ROOT/$retired" ]; then
            if [ -L "$PLUGIN_ROOT/$retired" ] || ! cmp -s "$PLUGIN_ROOT/$retired" "$PLUGIN_ROOT/docs/opencode/$retired"; then
                echo "Retaining modified legacy contract: $PLUGIN_ROOT/$retired" >&2
                return 1
            fi
            config_preflight || return 1
            rm -- "$PLUGIN_ROOT/$retired" || return 1
        fi
    done
    if [ -f "$PLUGIN_ROOT/docs/opencode/E1-PORTS.md" ] && { [ -e "$PLUGIN_ROOT/.orphaned_at" ] || [ -L "$PLUGIN_ROOT/.orphaned_at" ]; }; then
        if [ -L "$PLUGIN_ROOT/.orphaned_at" ] || [ "$(cat "$PLUGIN_ROOT/.orphaned_at")" != 1766603417062 ]; then
            echo "Retaining modified legacy workspace marker." >&2
            return 1
        fi
        config_preflight || return 1
        rm -- "$PLUGIN_ROOT/.orphaned_at" || return 1
    fi
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

download_binary() { bootstrap_writer_gate download_binary_transaction "$@"; }

download_binary_transaction() {
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

# One parent owns presentation. Parallel workers use private finite IPC records;
# their shell state is never assumed to propagate into the coordinator.
progress_rich_available() {
    [ "${BOOTSTRAP_VERBOSE:-0}" != 1 ] && [ "${_UI_MODE:-auto}" != plain ] &&
        [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ] && [ -t 2 ]
}

progress_helper_available() {
    [ -n "$_CONFIG_HELPER" ] && [ -x "$_CONFIG_HELPER" ] || return 1
    if [ "$_PROGRESS_CAP_HELPER" != "$_CONFIG_HELPER" ]; then
        _PROGRESS_CAP_HELPER=$_CONFIG_HELPER
        _PROGRESS_CAP_SUPPORTED=false
        _PROGRESS_SESSION_SUPPORTED=false
        local capability
        capability=$("$_CONFIG_HELPER" installer-progress --capabilities </dev/null 2>/dev/null) || return 1
        [ "$capability" != installer-progress-v1 ] || _PROGRESS_CAP_SUPPORTED=true
        capability=$("$_CONFIG_HELPER" installer-progress --session-capabilities </dev/null 2>/dev/null) || capability=""
        [ "$capability" != installer-progress-session-v1 ] || _PROGRESS_SESSION_SUPPORTED=true
    fi
    [ "$_PROGRESS_CAP_SUPPORTED" = true ]
}

# Session facts are independent of presentation availability and private logs.
progress_journal_init() {
    [ -z "$_PROGRESS_JOURNAL" ] && [ "$_PROGRESS_JOURNAL_DISABLED" != true ] || return 0
    _PROGRESS_JOURNAL=$(mktemp "${TMPDIR:-/tmp}/bootstrap-session-XXXXXX") || _PROGRESS_JOURNAL_DISABLED=true
}

progress_record() {
    [ "$_PROGRESS_JOURNAL_DISABLED" != true ] && [ -n "$_PROGRESS_JOURNAL" ] || return 0
    local bytes
    if [ ! -f "$_PROGRESS_JOURNAL" ] || [ -L "$_PROGRESS_JOURNAL" ]; then
        _PROGRESS_JOURNAL_DISABLED=true; return 0
    fi
    bytes=$(wc -c < "$_PROGRESS_JOURNAL") || bytes=262144
    if [ "$bytes" -gt 261888 ] || ! printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >> "$_PROGRESS_JOURNAL"; then
        _PROGRESS_JOURNAL_DISABLED=true
    fi
    return 0
}

progress_snapshots() {
    local i=0
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        progress_record snapshot "${_PROGRESS_IDS[$i]}" "${_PROGRESS_PHASES[$i]}" "${_PROGRESS_OUTCOMES[$i]}"
        i=$((i + 1))
    done
}

progress_child_running() {
    # Keep the parent job roster: a pipeline loses it in Bash. Defer an
    # interrupt's exit until Bash finishes parsing these command substitutions.
    local running result=1 _BOOTSTRAP_JOB_QUERY=true _BOOTSTRAP_JOB_QUERY_INTERRUPT=0
    for running in $(jobs -pr) $(jobs -ps); do
        if [ "$running" = "$1" ]; then result=0; break; fi
    done
    _BOOTSTRAP_JOB_QUERY=false
    [ "$_BOOTSTRAP_JOB_QUERY_INTERRUPT" -eq 0 ] || exit "$_BOOTSTRAP_JOB_QUERY_INTERRUPT"
    return "$result"
}

# Join only this job. No watchdog process, reused PID or indefinite graceful wait.
progress_reap() {
    local pid="$1" i=0 status=0 forced=false escalated=false
    while progress_child_running "$pid" && [ "$i" -lt 20 ]; do sleep 0.05; i=$((i + 1)); done
    if progress_child_running "$pid"; then
        escalated=true
        kill -TERM "$pid" 2>/dev/null || true
        i=0
        while progress_child_running "$pid" && [ "$i" -lt 10 ]; do sleep 0.05; i=$((i + 1)); done
        if progress_child_running "$pid"; then kill -KILL "$pid" 2>/dev/null || true; forced=true; fi
    fi
    wait "$pid" 2>/dev/null || status=$?
    if [ "$forced" = true ] && progress_rich_available; then printf '\033[0m\033[?25h' >&2; fi
    if [ "$escalated" = true ] && [ "$status" -eq 0 ]; then status=1; fi
    return "$status"
}

progress_event() {
    progress_record transition "$1" "$2" "$3"
    [ -n "$_PROGRESS_EVENTS" ] && [ "$_PROGRESS_KIND" = helper ] || return 0
    printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$_PROGRESS_EVENTS" || true
}

progress_stage_phase() {
    _PROGRESS_GLOBAL_PHASE=preparation
    case "$1" in
        "Preparing verified installer") _PROGRESS_GLOBAL_PHASE=verified-installer ;;
        "Checking existing installation") _PROGRESS_GLOBAL_PHASE=existing-installation ;;
        "Checking shared settings") _PROGRESS_GLOBAL_PHASE=shared-settings ;;
        "Preparing notification settings") _PROGRESS_GLOBAL_PHASE=notification-settings ;;
        "Configuring agent notification tools") _PROGRESS_GLOBAL_PHASE=notification-tools ;;
        "Installing Claude notifications") _PROGRESS_GLOBAL_PHASE=installing-claude ;;
        "Installing Codex notifications") _PROGRESS_GLOBAL_PHASE=installing-codex ;;
        "Installing OpenCode notifications") _PROGRESS_GLOBAL_PHASE=installing-opencode ;;
        "Installing Gemini CLI notifications") _PROGRESS_GLOBAL_PHASE=installing-gemini ;;
        "Installing Qoder notifications") _PROGRESS_GLOBAL_PHASE=installing-qoder ;;
    esac
}

progress_start() {
    [ -z "$_PARALLEL_WORKER" ] || return 0
    [ -z "$_PROGRESS_PID" ] || return 0
    if progress_helper_available && [ "$_PROGRESS_SESSION_SUPPORTED" = true ] && [ "$_PROGRESS_JOURNAL_DISABLED" != true ]; then
        _PROGRESS_STOP_FILE=$(mktemp "${TMPDIR:-/tmp}/bootstrap-observer-stop-XXXXXX") || return 0
        _PROGRESS_KIND=session
        local activity_args=(--activity "$_ACTIVITY_MODE")
        [ "$_ACTIVITY_LIMIT" -eq 0 ] || activity_args+=(--activity-limit "$_ACTIVITY_LIMIT")
        "$_CONFIG_HELPER" installer-progress --session --events "$_PROGRESS_JOURNAL" --stop-file "$_PROGRESS_STOP_FILE" --ui "${_UI_MODE:-auto}" "${activity_args[@]}" --transient --owner-pid "$$" </dev/null >/dev/null &
        _PROGRESS_PID=$!
        return 0
    fi
    # A failed session stays in the truthful plain owner presentation.
    [ "$_PROGRESS_SESSION_SUPPORTED" != true ] || return 0
    progress_rich_available || return 0
    # Older verified helpers retain the existing plain stage output.
    if [ -n "$_CONFIG_HELPER" ]; then
        progress_helper_available || return 0
        _PROGRESS_EVENTS=$(mktemp "${TMPDIR:-/tmp}/bootstrap-progress-XXXXXX") || return 0
        _PROGRESS_KIND=helper
        local i=0
        while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
            printf '%s\t%s\t%s\n' "${_PROGRESS_IDS[$i]}" "${_PROGRESS_PHASES[$i]}" "${_PROGRESS_OUTCOMES[$i]}" >> "$_PROGRESS_EVENTS"
            i=$((i + 1))
        done
        printf 'global\t%s\trunning\n' "$_PROGRESS_GLOBAL_PHASE" >> "$_PROGRESS_EVENTS"
        "$_CONFIG_HELPER" installer-progress --events "$_PROGRESS_EVENTS" --ui "${_UI_MODE:-auto}" --transient --owner-pid "$$" </dev/null >/dev/null &
        _PROGRESS_PID=$!
    else
        _PROGRESS_KIND=spinner
        _PROGRESS_EVENTS=$(mktemp "${TMPDIR:-/tmp}/bootstrap-progress-XXXXXX") || return 0
        # Only this display loop is asynchronous. The owner remains synchronous.
        (
            trap 'exit 0' INT TERM HUP
            local frame=0 frames='|/-\' label="$1" file="" bytes="" next_label=""
            while :; do
                if [ -s "$_PROGRESS_EVENTS" ]; then
                    { IFS= read -r next_label; IFS= read -r file; } < "$_PROGRESS_EVENTS" || true
                    [ -z "$next_label" ] || label=$next_label
                fi
                bytes=""
                if [ -n "$file" ] && [ -f "$file" ]; then
                    bytes=$(wc -c < "$file" 2>/dev/null | tr -d '[:space:]') || bytes=""
                    [ -z "$bytes" ] || bytes=" ($bytes bytes received)"
                fi
                printf '\r\033[2K  %s %s%s' "${frames:frame:1}" "$label" "$bytes" >&2
                frame=$(((frame + 1) % 4))
                sleep 0.1
            done
        ) </dev/null >/dev/null &
        _PROGRESS_PID=$!
    fi
}

# A file's current byte count is measured, never a guessed percentage.
progress_download_activity() {
    [ "$_PROGRESS_KIND" = spinner ] && [ -n "$_PROGRESS_EVENTS" ] || return 0
    local label="Downloading installer files"
    case "${1##*/}" in
        checksums.txt) label="Downloading release checksums" ;;
        claude-notifications-*) label="Downloading verified installer helper" ;;
        install.sh) label="Downloading runtime installer" ;;
        source.tar.gz) label="Downloading source bundle" ;;
        ClaudeNotifier.app.zip) label="Downloading macOS notifier" ;;
    esac
    printf '%s\n%s\n' "$label" "$1" > "$_PROGRESS_EVENTS" || true
}

progress_stop() {
    local status=${1:-0} pid=$_PROGRESS_PID
    if [ -n "$pid" ]; then
        if [ "$_PROGRESS_KIND" = session ]; then
            printf 'stop\n' > "$_PROGRESS_STOP_FILE" || kill -TERM "$pid" 2>/dev/null || true
        elif [ "$_PROGRESS_KIND" = helper ]; then
            if [ "$status" -eq 0 ]; then printf 'global\t%s\tdone\n' "$_PROGRESS_GLOBAL_PHASE" >> "$_PROGRESS_EVENTS"
            else printf 'global\t%s\tfailed\n' "$_PROGRESS_GLOBAL_PHASE" >> "$_PROGRESS_EVENTS"; fi
            # A full disk or inaccessible event file must not strand the observer.
            if ! printf 'stop\t\t\n' >> "$_PROGRESS_EVENTS"; then
                kill "$pid" 2>/dev/null || true
            fi
        else
            kill "$pid" 2>/dev/null || true
        fi
        if ! progress_reap "$pid"; then
            [ "$_PROGRESS_KIND" != session ] || _PROGRESS_JOURNAL_DISABLED=true
        fi
        [ "$_PROGRESS_KIND" != spinner ] || printf '\r\033[2K' >&2
    fi
    [ -z "$_PROGRESS_EVENTS" ] || rm -f "$_PROGRESS_EVENTS"
    [ -z "$_PROGRESS_STOP_FILE" ] || rm -f "$_PROGRESS_STOP_FILE"
    _PROGRESS_STOP_FILE=""
    _PROGRESS_PID=""; _PROGRESS_EVENTS=""; _PROGRESS_KIND=""
}

# This storage adapter never interprets command output. Private durable captures
# preserve the session's preceding stages as well as the failed stage. The
# installer functions still run synchronously in this shell, preserving state.
bootstrap_diagnostics() {
    local engine root
    engine=$(installer_runtime) || return 1
    root=${_DIAGNOSTIC_ROOT:-${BOOTSTRAP_DIAGNOSTIC_ROOT:-}}
    if [ -z "$root" ]; then
        root=$(bootstrap_control_root) || return 1
        # The transaction control root must remain absent on a fresh install
        # until its writer provisions the existing installation locks.
        root="${root}-installer-diagnostics"
    fi
    if [ "$engine" = python3 ]; then
        python3 -I - "$root" "${TMPDIR:-/tmp}" "$@" <<'PY_DIAGNOSTICS'
import os, stat, sys, tempfile
root, temporary, operation = sys.argv[1:4]
def private_root():
    if not os.path.isabs(root): raise ValueError('absolute diagnostic root required')
    path = os.path.abspath(root)
    tmp = os.path.realpath(temporary)
    if os.path.commonpath([os.path.realpath(path), tmp]) == tmp:
        raise ValueError('diagnostic root is temporary')
    current = os.path.splitdrive(path)[0] + os.sep
    for part in path[len(current):].split(os.sep):
        if not part: continue
        current = os.path.join(current, part)
        try: os.mkdir(current, 0o700)
        except FileExistsError: pass
        info = os.lstat(current)
        if not stat.S_ISDIR(info.st_mode): raise ValueError('unsafe diagnostic parent')
        if os.name != 'nt' and (info.st_uid not in (0, os.getuid()) or
            (info.st_mode & 0o022 and not info.st_mode & stat.S_ISVTX)):
            raise ValueError('untrusted diagnostic parent')
    info = os.lstat(path)
    if hasattr(os, 'getuid') and info.st_uid != os.getuid(): raise ValueError('foreign diagnostic root')
    if os.name != 'nt' and stat.S_IMODE(info.st_mode) != 0o700: raise ValueError('nonprivate diagnostic root')
    return path
try:
    # POSIX modes do not establish a private Windows DACL. Until this adapter
    # verifies native ACLs, refuse persistence rather than claim privacy.
    if os.name == 'nt': raise ValueError('private native Windows diagnostics unavailable')
    path = private_root()
    if operation == 'create':
        fd, name = tempfile.mkstemp(prefix='installer-', suffix='.log', dir=path)
        os.fchmod(fd, 0o600); os.close(fd); print(name)
    elif operation == 'append':
        name, label, code, stdout, stderr = sys.argv[4:9]
        if os.path.dirname(name) != path: raise ValueError('foreign logfile')
        fd = os.open(name, os.O_WRONLY | os.O_APPEND | getattr(os, 'O_NOFOLLOW', 0))
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or (hasattr(os, 'getuid') and info.st_uid != os.getuid()):
            os.close(fd); raise ValueError('unsafe logfile')
        if os.name != 'nt' and stat.S_IMODE(info.st_mode) != 0o600:
            os.close(fd); raise ValueError('nonprivate logfile')
        remaining = max(0, 2 * 1024 * 1024 - info.st_size)
        with os.fdopen(fd, 'ab') as log:
            record = ('\nStage: ' + label + '\nExit status: ' + code + '\n').encode()
            for heading, capture in [('stdout', stdout), ('stderr', stderr)]:
                with open(capture, 'rb') as source: data = source.read(65537)
                record += ('\n' + heading + ':\n').encode() + data[:65536]
                if len(data) > 65536: record += b'\n[diagnostic stream truncated after 65536 bytes]\n'
            marker = b'\n[session diagnostics truncated after 2097152 bytes]\n'
            if remaining:
                log.write(record if len(record) <= remaining else record[:max(0, remaining-len(marker))] + marker[:remaining])
            else: raise ValueError('diagnostic session limit reached')
    elif operation == 'view':
        name = sys.argv[4]
        if os.path.dirname(name) != path or os.path.islink(name): raise ValueError('unsafe logfile')
        # Explicit details remain printable even if a vendor writes terminal controls.
        fd = os.open(name, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or (hasattr(os, 'getuid') and info.st_uid != os.getuid()) or (os.name != 'nt' and stat.S_IMODE(info.st_mode) != 0o600):
            os.close(fd); raise ValueError('nonprivate logfile')
        with os.fdopen(fd, 'rb') as log: data = log.read(2 * 1024 * 1024)
        text = data.decode('utf-8', errors='replace')
        print(''.join(c if c in '\n\t' or (ord(c) >= 32 and ord(c) != 127) else '\\x%02x' % ord(c) for c in text), end='')
except (OSError, ValueError):
    sys.exit(1)
PY_DIAGNOSTICS
    else
        run_isolated_node - "$root" "${TMPDIR:-/tmp}" "$@" <<'JS_DIAGNOSTICS'
const fs = require('fs'), path = require('path'), crypto = require('crypto');
const [root, temporary, operation, ...args] = process.argv.slice(2);
try {
    if (process.platform === 'win32') throw Error('private native Windows diagnostics unavailable');
    if (!path.isAbsolute(root)) throw Error('absolute diagnostic root required');
    const target = path.resolve(root), tmp = fs.realpathSync(temporary);
    if (target === tmp || target.startsWith(tmp + path.sep)) throw Error('temporary diagnostics');
    let current = path.parse(target).root;
    for (const part of target.slice(current.length).split(path.sep).filter(Boolean)) {
        current = path.join(current, part);
        try { fs.mkdirSync(current, {mode: 0o700}); } catch (e) { if (e.code !== 'EEXIST') throw e; }
        const parent = fs.lstatSync(current);
        if (!parent.isDirectory() || (process.platform !== 'win32' &&
            ((!([0, process.getuid()].includes(parent.uid))) || ((parent.mode & 0o022) && !(parent.mode & 0o1000))))) throw Error('unsafe diagnostic parent');
    }
    const owner = fs.lstatSync(target), unix = process.platform !== 'win32';
    if ((process.getuid && owner.uid !== process.getuid()) || (unix && (owner.mode & 0o777) !== 0o700)) throw Error('nonprivate diagnostics');
    if (operation === 'create') {
        const name = path.join(target, 'installer-' + crypto.randomBytes(12).toString('hex') + '.log');
        const fd = fs.openSync(name, fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | (fs.constants.O_NOFOLLOW || 0), 0o600);
        fs.closeSync(fd); console.log(name);
    } else {
        const [name, label, code, stdout, stderr] = args;
        if (path.dirname(name) !== target || fs.lstatSync(name).isSymbolicLink()) throw Error('foreign logfile');
        const fd = fs.openSync(name, (operation === 'view' ? fs.constants.O_RDONLY : fs.constants.O_WRONLY | fs.constants.O_APPEND) | (fs.constants.O_NOFOLLOW || 0));
        try {
            const info = fs.fstatSync(fd);
            if (!info.isFile() || info.nlink !== 1 || (process.getuid && info.uid !== process.getuid()) || (unix && (info.mode & 0o777) !== 0o600)) throw Error('nonprivate logfile');
            if (operation === 'view') {
                const data = Buffer.alloc(Math.min(info.size, 2097152)); fs.readSync(fd, data, 0, data.length, 0);
                process.stdout.write(data.toString('utf8').replace(/[\x00-\x08\x0b-\x1f\x7f]/g, c => '\\x' + c.charCodeAt(0).toString(16).padStart(2, '0')));
            } else {
                let chunks = [Buffer.from('\nStage: ' + label + '\nExit status: ' + code + '\n')];
                for (const [heading, capture] of [['stdout', stdout], ['stderr', stderr]]) {
                    const input = fs.openSync(capture, 'r'), data = Buffer.alloc(65537);
                    let count; try { count = fs.readSync(input, data, 0, data.length, 0); } finally { fs.closeSync(input); }
                    chunks.push(Buffer.from('\n' + heading + ':\n'), data.subarray(0, Math.min(count, 65536)));
                    if (count > 65536) chunks.push(Buffer.from('\n[diagnostic stream truncated after 65536 bytes]\n'));
                }
                let record = Buffer.concat(chunks), remaining = Math.max(0, 2097152 - info.size);
                if (!remaining) throw Error('diagnostic session limit reached');
                const marker = Buffer.from('\n[session diagnostics truncated after 2097152 bytes]\n');
                if (record.length > remaining) record = Buffer.concat([record.subarray(0, Math.max(0, remaining-marker.length)), marker.subarray(0, remaining)]);
                fs.writeFileSync(fd, record);
            }
        } finally { fs.closeSync(fd); }
    }
} catch (_) { process.exit(1); }
JS_DIAGNOSTICS
    fi
}

diagnostic_capture_stage() {
    [ -n "$_PROGRESS_LOG" ] && [ -n "$_PROGRESS_STDERR" ] || return 0
    if [ -z "$_DIAGNOSTIC_LOG" ] && [ "$_DIAGNOSTIC_FAILED" != true ]; then
        _DIAGNOSTIC_ROOT=${BOOTSTRAP_DIAGNOSTIC_ROOT:-}
        if [ -z "$_DIAGNOSTIC_ROOT" ]; then
            if ! _DIAGNOSTIC_ROOT=$(bootstrap_control_root); then
                _DIAGNOSTIC_FAILED=true
                return 0
            fi
            _DIAGNOSTIC_ROOT="${_DIAGNOSTIC_ROOT}-installer-diagnostics"
        fi
        _DIAGNOSTIC_LOG=$(bootstrap_diagnostics create) || _DIAGNOSTIC_FAILED=true
    fi
    if [ -n "$_DIAGNOSTIC_LOG" ]; then
        bootstrap_diagnostics append "$_DIAGNOSTIC_LOG" "$_DIAGNOSTIC_LABEL" "$1" "$_PROGRESS_LOG" "$_PROGRESS_STDERR" || _DIAGNOSTIC_FAILED=true
    fi
}

report_diagnostics() {
    if [ -n "$_DIAGNOSTIC_LOG" ] && [ -f "$_DIAGNOSTIC_LOG" ] && [ ! -L "$_DIAGNOSTIC_LOG" ]; then
        printf 'Diagnostics: %q\n' "$_DIAGNOSTIC_LOG" >&2
    fi
    if [ "$_DIAGNOSTIC_FAILED" = true ]; then
        printf 'Diagnostics could not be fully saved; command output was kept private.\n' >&2
    fi
}

run_setup_stage() {
    local label="$1" status=0
    shift
    if [ -n "$_PARALLEL_WORKER" ]; then
        # The outer worker capture owns all nested output and diagnostics.
        "$@" || status=$?
        return "$status"
    fi
    _PROGRESS_LOG=$(mktemp "${TMPDIR:-/tmp}/bootstrap-output-XXXXXX") || return 1
    _PROGRESS_STDERR=$(mktemp "${TMPDIR:-/tmp}/bootstrap-output-XXXXXX") || { rm -f "$_PROGRESS_LOG"; _PROGRESS_LOG=""; return 1; }
    _DIAGNOSTIC_LABEL=$label
    progress_journal_init
    progress_snapshots
    progress_stage_phase "$label"
    progress_record transition global "$_PROGRESS_GLOBAL_PHASE" running
    progress_start "$label"
    if progress_rich_available && [ -n "$_PROGRESS_PID" ]; then
        : # A started transient display owns rich stage labels.
    elif [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ] || [[ "$label" = Installing* ]] || [ "$label" = "Upgrading UAP registry" ] || progress_rich_available || [ "$_PROGRESS_JOURNAL_DISABLED" = true ]; then
        printf '%s...\n' "$label"
    fi
    "$@" > "$_PROGRESS_LOG" 2> "$_PROGRESS_STDERR" || status=$?
    if [ "$status" -eq 0 ]; then progress_record transition global "$_PROGRESS_GLOBAL_PHASE" done
    else progress_record transition global "$_PROGRESS_GLOBAL_PHASE" failed; fi
    [ "$_PARALLEL_ACTIVE" = true ] || progress_stop "$status"
    diagnostic_capture_stage "$status"
    if [ "$status" -eq 0 ] && [ -s "$_PROGRESS_STDERR" ]; then _DIAGNOSTIC_NOTED=true; fi
    rm -f "$_PROGRESS_LOG" "$_PROGRESS_STDERR"
    _PROGRESS_LOG=""; _PROGRESS_STDERR=""; _DIAGNOSTIC_LABEL=""
    return "$status"
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
                printf '  Run /hooks in Codex, review the entries and trust them.\n' ;;
        esac
        print_delivery_note
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  If no alert arrives, check OS notification permission and your settings.\n'
        fi
        if [ "$PRODUCT" != codex ]; then
            if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
                printf '  Claude settings: /claude-notifications-go:settings\n'
            fi
            print_iterm2_python_api_notice
        fi
    } > "$report"
    publish_setup_summary "$report"
    local status=$?
    rm -f "$report"
    return "$status"
}

print_delivery_note() {
    printf '  Delivery has not been verified. Complete a task after restart to test.\n'
}

# The public loader can collect summaries across separate pinned bootstrap runs.
# This file contains display text, never executable shell or a readiness claim.
publish_setup_summary() {
    if [ -n "${BOOTSTRAP_SUMMARY_FILE:-}" ]; then
        # Separate product runs share one delivery footer in the public loader.
        local combined status=0
        combined=$(mktemp "${TMPDIR:-/tmp}/bootstrap-summary-XXXXXX") || return 1
        touch "$BOOTSTRAP_SUMMARY_FILE" || { rm -f "$combined"; return 1; }
        if awk -v note="$(print_delivery_note)" '$0 != note' "$BOOTSTRAP_SUMMARY_FILE" "$1" > "$combined" &&
            print_delivery_note >> "$combined"; then
            cat "$combined" > "$BOOTSTRAP_SUMMARY_FILE" || status=$?
        else
            status=$?
        fi
        rm -f "$combined"
        return "$status"
    else
        printf '\nInstallation complete\n\n'
        cat "$1"
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
            --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted|--codex-home|--pi-agent-dir|--pi-executable|--scope-root|--client-executable)
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
                [ "${BOOTSTRAP_VERBOSE:-0}" != 1 ] || echo "Codex Desktop found and verified: $app. Your current click-navigation preferences will be kept." >&2
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
    local scope_root="" client_exec="" pi_scope=false pi_scope_args=0
    while [ "$#" -gt 0 ]; do
        key=${1%%=*}
        case "$key" in
            --ui|--plain|--activity|--activity-limit|--product|--products|--help|-h) ;;
            *)
                case ",$seen_flags," in *",$key,"*) echo "Use $key once." >&2; return 1 ;; esac
                seen_flags=${seen_flags:+$seen_flags,}$key ;;
        esac
        case "$1" in
            --activity|--activity=*|--activity-limit|--activity-limit=*)
                local activity_key=${1%%=*} activity_value
                case "$1" in *=*) activity_value=${1#*=}; shift ;; *) [ "$#" -ge 2 ] || { echo "$1 requires a value." >&2; return 1; }; activity_value=$2; shift 2 ;; esac
                if [ "$activity_key" = --activity ]; then
                    case "$activity_value" in compact|expanded|off) ;; *) echo "Invalid activity mode." >&2; return 1 ;; esac
                    [ "$_ACTIVITY_EXPLICIT" != true ] || [ "$_ACTIVITY_MODE" = "$activity_value" ] || { echo "Conflicting activity modes." >&2; return 1; }
                    _ACTIVITY_MODE=$activity_value; _ACTIVITY_EXPLICIT=true
                else
                    case "$activity_value" in [1-9]|1[0-6]) ;; *) echo "Activity limit must be 1..16." >&2; return 1 ;; esac
                    [ "$_ACTIVITY_LIMIT" -eq 0 ] || [ "$_ACTIVITY_LIMIT" -eq "$activity_value" ] || { echo "Conflicting activity limits." >&2; return 1; }
                    _ACTIVITY_LIMIT=$activity_value
                fi ;;
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
            --desktop|--webhook|--desktop=true|--desktop=false|--webhook=true|--webhook=false)
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
            --scope-root|--scope-root=*|--client-executable|--client-executable=*)
                case "$1" in
                    *=*) value=${1#*=}; shift ;;
                    *) [ "$#" -ge 2 ] || { echo "Missing value for $key" >&2; return 1; }; value=$2; shift 2 ;;
                esac
                [ "${#value}" -le 4096 ] || { echo "$key must be at most 4096 characters." >&2; return 1; }
                case "$value" in /*|[A-Za-z]:/*|[A-Za-z]:\\*) ;; *) echo "$key must be absolute." >&2; return 1 ;; esac
                case "$key" in --scope-root) scope_root=$value ;; *) client_exec=$value ;; esac
                CONFIGURE_ARGS+=("$key" "$value") ;;
            --pi-agent-dir|--pi-agent-dir=*|--pi-executable|--pi-executable=*)
                case "$1" in
                    *=*) value=${1#*=}; shift ;;
                    *) [ "$#" -ge 2 ] || { echo "Missing value for $key" >&2; return 1; }; value=$2; shift 2 ;;
                esac
                [ "${#value}" -le 4096 ] || return 1
                case "$value" in /*|[A-Za-z]:/*|[A-Za-z]:\\*) ;; *) echo "$key must be absolute." >&2; return 1 ;; esac
                pi_scope=true
                pi_scope_args=$((pi_scope_args + 2))
                CONFIGURE_ARGS+=("$key" "$value") ;;
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
                echo "Usage: bash bootstrap.sh [--product claude|codex|both|opencode|gemini|qoder|cursor|pi | --products claude,codex,opencode,gemini,qoder,cursor,pi] [--desktop] [--webhook] [--agent-notify|--skip-agent-notify] [--navigation none] [--pi-agent-dir PATH --pi-executable PATH] [--scope-root PATH --client-executable PATH (advanced Cursor override)]"
                exit 0 ;;
            *) echo "Unknown option: $1" >&2; return 1 ;;
        esac
    done
    if [ "$seen_agent_notify" = true ] && [ "$seen_skip_agent_notify" = true ]; then
        echo "--agent-notify and --skip-agent-notify are mutually exclusive." >&2
        return 1
    fi
    if { [ -n "$scope_root" ] && [ -z "$client_exec" ]; } || { [ -z "$scope_root" ] && [ -n "$client_exec" ]; }; then
        echo "--scope-root and --client-executable are required together." >&2; return 1
    fi
    if [ -z "$PRODUCT" ]; then
        # Selection runs later through the release-verified public UAP UI. No
        # persistent root is initialized before cancel/empty are resolved.
        PRODUCT=select
        _SELECTION_PENDING=true
        _INTERACTIVE_INTENT=true
        case " ${CONFIGURE_ARGS[*]-} " in *" --json "*) echo "Pending questions require complete explicit product/route input with --json." >&2; return 1 ;; esac
        # Pure syntax only; applicability is checked after product selection.
        local selected_scope_count=0
        [ -z "$scope_root" ] || selected_scope_count=4
        selected_scope_count=$((selected_scope_count + pi_scope_args))
        if [ "$AGENT_NOTIFY_REQUEST" = skip ] && [ "${#CONFIGURE_ARGS[@]}" -gt "$selected_scope_count" ]; then
            echo "Route flags require --agent-notify." >&2; return 1
        fi
        [ "${#CONFIGURE_ARGS[@]}" -eq "$selected_scope_count" ] || complete_configure_route
        return $?
    fi
    local csv remaining selected seen="" observers=0
    case "$PRODUCT" in
        bundle:*) csv=${PRODUCT#bundle:} ;;
        both) csv=claude,codex ;;
        claude|codex|opencode|gemini|qoder|pi|cursor) csv=$PRODUCT ;;
        *) echo "Invalid product: $PRODUCT; use claude, codex, both, opencode, gemini, qoder, pi or cursor." >&2; return 1 ;;
    esac
    case "$csv" in ''|,*|*,|*,,*) echo "Product selection contains an empty product." >&2; return 1 ;; esac
    if [ "$pi_scope" = true ]; then
        case ",$csv," in *,pi,*) ;; *) echo "Pi profile arguments require Pi selection." >&2; return 1 ;; esac
    fi
    case ",$csv," in
        *,cursor,*) ;;
        *) [ -z "$scope_root$client_exec" ] || { echo "Selected scope/client arguments require Cursor." >&2; return 1; } ;;
    esac
    SELECTED_PRODUCTS=()
    LEGACY_PRODUCT=""
    remaining=$csv
    while :; do
        selected=${remaining%%,*}
        case "$selected" in
            claude|codex|opencode|gemini|qoder|cursor|pi) ;;
            *) echo "Unknown product: $selected" >&2; return 1 ;;
        esac
        case ",$seen," in *",$selected,"*) echo "Duplicate product: $selected" >&2; return 1 ;; esac
        seen=${seen:+$seen,}$selected
        SELECTED_PRODUCTS+=("$selected")
        case "$selected" in
            claude|codex)
                if [ -z "$LEGACY_PRODUCT" ]; then LEGACY_PRODUCT=$selected; else LEGACY_PRODUCT=both; fi ;;
            opencode|gemini|qoder|cursor|pi) observers=$((observers+1)) ;;
        esac
        case "$remaining" in *,*) remaining=${remaining#*,} ;; *) break ;; esac
    done
    if [ "$observers" -eq 0 ] && [ "${#OPENCODE_ARGS[@]}" -gt 0 ]; then
        echo "--desktop/--webhook require OpenCode, Gemini, Qoder, Cursor or Pi." >&2; return 1
    fi
    local advanced_count=$pi_scope_args
    [ -z "$scope_root" ] || advanced_count=$((advanced_count + 4))
    if [ -z "$LEGACY_PRODUCT" ]; then
        if { [ "$seen_agent_notify" = true ] || [ "$seen_skip_agent_notify" = true ]; } && [[ ",$csv," != *,cursor,* ]] || [ "${#CONFIGURE_ARGS[@]}" -ne "$advanced_count" ]; then
            echo "Observers use --desktop/--webhook consent, not agent-notify or navigation flags." >&2
            return 1
        fi
        CONFIGURE_NOTIFICATIONS=false
        return 0
    fi
    if [ "$CONFIGURE_NOTIFICATIONS" = true ]; then
        complete_configure_route || return 1
    elif [ "${#CONFIGURE_ARGS[@]}" -ne "$advanced_count" ]; then
        echo "Route flags require --agent-notify." >&2
        return 1
    fi
}

bootstrap_cleanup() {
    local interrupted_status=$?
    parallel_cancel_jobs
    if [ "$_PARALLEL_IN_FLIGHT" = true ]; then
        _KEEP_CONFIG_STAGE=true
        local client job
        # Save the interrupted parent helper capture before worker-log merging
        # changes these shell-owned paths and the current stage label.
        if [ -n "$_PARALLEL_HELPER_CAPTURE" ] && [ -f "$_PARALLEL_HELPER_CAPTURE" ] && [ ! -L "$_PARALLEL_HELPER_CAPTURE" ] &&
            [ -n "$_PROGRESS_LOG" ] && [ -f "$_PROGRESS_LOG" ] && [ ! -L "$_PROGRESS_LOG" ]; then
            head -c 65536 "$_PARALLEL_HELPER_CAPTURE" >> "$_PROGRESS_LOG" || true
        fi
        diagnostic_capture_stage interrupted
        for client in ${_PARALLEL_CLIENTS[@]+"${_PARALLEL_CLIENTS[@]}"}; do
            job="$_PARALLEL_ROOT/$client"
            _PROGRESS_LOG="$job/stdout"; _PROGRESS_STDERR="$job/stderr"; _DIAGNOSTIC_LABEL="Installing $client notifications"
            if parallel_read_result "$job/result"; then
                if [ "$_PARALLEL_FOLLOW_UP_STARTED" != true ]; then progress_phase "$client" "$_JOB_PHASE" "$_JOB_OUTCOME"; fi
                diagnostic_capture_stage "$_JOB_STATUS"
            else
                if [ "$_PARALLEL_FOLLOW_UP_STARTED" != true ]; then progress_phase "$client" preparation state_requires_inspection; fi
                diagnostic_capture_stage interrupted
            fi
        done
        if [ "$_PARALLEL_FOLLOW_UP_STARTED" = true ]; then parallel_replay_progress; fi
        _PROGRESS_LOG=""; _PROGRESS_STDERR=""; _DIAGNOSTIC_LABEL="Installing selected clients"
    fi
    progress_stop 1
    if [ -n "$_DIAGNOSTIC_LABEL" ]; then
        # The interrupted shared stage does not prove which client's activation
        # began. Preserve authoritative client facts and report owner interruption.
        progress_record transition global "$_PROGRESS_GLOBAL_PHASE" failed
        diagnostic_capture_stage interrupted
        printf 'Setup incomplete: installation was interrupted.\n' >&2
        if [ "$_PARALLEL_FORCED_STOP" = true ]; then printf 'Installer termination required force; recovery files were retained.\n' >&2; fi
        report_progress "${interrupted_status:-1}"
        report_diagnostics
    fi
    [ -z "$_PROGRESS_JOURNAL" ] || rm -f "$_PROGRESS_JOURNAL"
    _PROGRESS_JOURNAL=""
    [ -z "$_PROGRESS_LOG" ] || rm -f "$_PROGRESS_LOG"
    [ -z "$_PROGRESS_STDERR" ] || rm -f "$_PROGRESS_STDERR"
    close_prompt
    [ -z "$_BOOTSTRAP_TMP" ] || rm -f "$_BOOTSTRAP_TMP"
    [ -z "$_BOOTSTRAP_STAGE" ] || rm -rf "$_BOOTSTRAP_STAGE"
    if [ -n "$_PARALLEL_ROOT" ] && [ "$_KEEP_CONFIG_STAGE" != true ]; then rm -rf "$_PARALLEL_ROOT"; fi
    if [ "$_KEEP_PORTABLE_STAGE" != true ]; then
        [ -z "$_PORTABLE_STAGE" ] || rm -rf "$_PORTABLE_STAGE"
    fi
    if [ "$_KEEP_CONFIG_STAGE" != true ]; then
        [ -z "$_CONFIG_STAGE" ] || rm -rf "$_CONFIG_STAGE"
    fi
    return 0
}

bootstrap_interrupt() {
    # Defer across spawn + PID registration so a signal cannot strand a new
    # process group. Queries use a separate function-local parser guard.
    if [ "$_PARALLEL_DISPATCHING" = true ]; then _PARALLEL_INTERRUPT_STATUS=$1
    elif [ "${_BOOTSTRAP_JOB_QUERY:-false}" = true ]; then _BOOTSTRAP_JOB_QUERY_INTERRUPT=$1
    else exit "$1"; fi
}

install_cleanup_traps() {
    # An interrupt may run EXIT while a stage function's stderr is redirected.
    # Retain the display destination so the private log receipt remains visible.
    exec 4>&2
    trap 'bootstrap_cleanup >&4 2>&4' EXIT
    trap 'bootstrap_interrupt 130' INT
    trap 'bootstrap_interrupt 143' TERM
    trap 'bootstrap_interrupt 129' HUP
}

install_runtime() {
    if [ "$1" != codex ]; then bootstrap_writer_gate install_runtime_transaction "$@";
    else install_runtime_transaction "$@"; fi
}

install_runtime_transaction() {
    parallel_phase runtime
    local product="$1" script="$2" target="$3"
    local disposable=false
    [ "$product" = "codex" ] && disposable=true
    INSTALL_DEFER_PROMOTION=1 CN_PRODUCT="$product" INSTALL_STAGED_ASSETS="$_CONFIG_STAGE" \
    INSTALL_DISPOSABLE_ACQUISITION="$disposable" \
    RELEASE_URL="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG" \
    CHECKSUMS_URL="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG/checksums.txt" \
    MODERN_NOTIFIER_URL="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG/ClaudeNotifier.app.zip" \
    INSTALL_TARGET_DIR="$target" bash "$script" "${@:4}" </dev/null 4>&-
}

fetch_bootstrap_file() {
    progress_download_activity "$2"
    if command -v curl >/dev/null 2>&1; then
        bootstrap_external curl -fsSL --connect-timeout 15 --max-time 120 "$1" -o "$2"
    else
        bootstrap_external wget -q -T 120 "$1" -O "$2"
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
    if bootstrap_external "$_CONFIG_HELPER" config init --json; then return 0; fi
    report_config_init_failure
}

report_config_init_failure() {
    _KEEP_CONFIG_STAGE=true
    _SETUP_FAILURE_REASON=config_failed
    if [ -n "$_CONFIG_HELPER" ] && [ -x "$_CONFIG_HELPER" ] && [ ! -L "$_CONFIG_HELPER" ]; then
        _SETUP_RECOVERY=config_available
    fi
    echo "Partial setup: registration succeeded, config initialization failed." >&2
    "$_CONFIG_HELPER" config path --json >&2 || true
    local -a retry=("$_CONFIG_HELPER" config init --json)
    [ -z "${AGENT_NOTIFICATIONS_CONFIG:-}" ] || retry=(env "AGENT_NOTIFICATIONS_CONFIG=$AGENT_NOTIFICATIONS_CONFIG" "${retry[@]}")
    printf 'Config-only retry (no downloads or registration): %s\n' "$(quote_shell_command "${retry[@]}")" >&2
    return 1
}

install_codex() {
    parallel_phase download
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
    parallel_phase verification
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
    run_codex_transaction() {
        run_codex_setup --dry-run || return 1
        config_preflight || return 1
        run_codex_setup
    }
    bootstrap_writer_gate run_codex_transaction || return $?
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
    parallel_phase download
    stage_channel_companions || return 1
    parallel_phase marketplace
    setup_marketplace || return 1
    sync_marketplace_checkout || return 1
    install_plugin || return 1
    parallel_phase verification
    find_plugin_root || return 1
    # The channel transaction already promoted the runtime while its backup
    # was recoverable. Do not repeat that promotion outside the transaction.
    if ! platform_channel_claude; then download_binary || return 1; fi
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
    parallel_phase download
    local root binary runtime os arch base native native_root config_path installed
    local channels=(${OPENCODE_ARGS[@]+"${OPENCODE_ARGS[@]}"})
    if [ -n "$_SELECTOR_INTENT" ]; then channels=("${_FROZEN_OPENCODE_CHANNELS[@]}"); fi
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
    set -- setup-opencode install --binary "$binary" "${channels[@]}"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$root" --home "$INSTALLER_HOME"
        set -- "$@" --opencode-config-dir "$_FROZEN_OPENCODE"
    fi
    if [ "$os" = darwin ] && { [[ " ${channels[*]-} " = *" --desktop "* ]] || [[ " ${channels[*]-} " = *" --desktop=true "* ]]; }; then
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
    run_setup_stage "Installing OpenCode notifications" observer_install_transaction "$root" "$runtime" "$@" </dev/null || return 1
    progress_phase "$PRODUCT" runtime-lookup registration_committed
    native_root="$root"
    [ "$os" != windows ] || native_root=$(cygpath -m "$root") || return 1
    runtime=$("$_CONFIG_HELPER" config installer runtime-root "$native_root") || return 1
    installed="$runtime/claude-notifications-$os-$arch"
    [ "$os" != windows ] || installed="$installed.exe"
    progress_phase "$PRODUCT" config registration_committed
    if [ -z "$_PARALLEL_WORKER" ]; then run_setup_stage "Preparing notification settings" initialize_config || return 1; fi
    config_path=$("$_CONFIG_HELPER" config path) || return 1
    local report
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-summary-XXXXXX") || return 1
    {
        echo "  OpenCode - installed; restart required to load the global plugin."
        print_delivery_note
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            echo "  Silent completion, question, permission and error alerts; no click-to-focus."
            printf '  Shared settings: %s\n' "$config_path"
            if [ "$os" = darwin ] && { [[ " ${channels[*]-} " = *" --desktop "* ]] || [[ " ${channels[*]-} " = *" --desktop=true "* ]]; }; then
                echo "  Check OS notification permission; grant it only if needed."
                printf '    Check: %s\n' "$(quote_shell_command "$installed" setup-opencode permission-status)"
                printf '    Grant if needed: %s\n' "$(quote_shell_command "$installed" setup-opencode request-permission)"
            fi
        fi
        if { [[ " ${channels[*]-} " = *" --webhook "* ]] || [[ " ${channels[*]-} " = *" --webhook=true "* ]]; }; then
            echo "  Webhook consent recorded; configure and enable its destination and status channel in shared settings."
        fi
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Remove: %s\n' "$(opencode_remove_command "$installed")"
        fi
    } > "$report"
    publish_setup_summary "$report"
    local status=$?
    rm -f "$report"
    return "$status"
}

install_gemini() {
    parallel_phase download
    local root binary runtime os arch base native native_root config_path installed
    local channels=(${OPENCODE_ARGS[@]+"${OPENCODE_ARGS[@]}"})
    if [ -n "$_SELECTOR_INTENT" ]; then channels=("${_FROZEN_GEMINI_CHANNELS[@]}"); fi
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
    set -- setup-gemini install --binary "$binary" "${channels[@]}"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$root" --home "$INSTALLER_HOME"
        set -- "$@" --config-root "$_FROZEN_GEMINI"
    fi
    if [ "$os" = darwin ] && { [[ " ${channels[*]-} " = *" --desktop "* ]] || [[ " ${channels[*]-} " = *" --desktop=true "* ]]; }; then
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
    run_setup_stage "Installing Gemini CLI notifications" observer_install_transaction "$root" "$runtime" "$@" </dev/null || return 1
    progress_phase "$PRODUCT" runtime-lookup registration_committed
    native_root="$root"
    [ "$os" != windows ] || native_root=$(cygpath -m "$root") || return 1
    runtime=$("$_CONFIG_HELPER" config installer runtime-root "$native_root") || return 1
    installed="$runtime/claude-notifications-$os-$arch"
    [ "$os" != windows ] || installed="$installed.exe"
    progress_phase "$PRODUCT" config registration_committed
    if [ -z "$_PARALLEL_WORKER" ]; then run_setup_stage "Preparing notification settings" initialize_config || return 1; fi
    config_path=$("$_CONFIG_HELPER" config path) || return 1
    local report status
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-gemini-summary-XXXXXX") || return 1
    {
        echo "  Gemini CLI - installed; restart required."
        print_delivery_note
        echo "  Enable/trust the user hooks in Gemini CLI after restart."
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Shared settings: %s\n' "$config_path"
            echo "  Silent turn completion and tool permission alerts; existing hook/security settings were preserved."
        fi
        echo "  Gemini: use one desktop notification source to avoid duplicate alerts."
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Inspect registration and consent: %s\n' "$(quote_shell_command "$installed" setup-gemini inspect --control-root "$native_root")"
            if [ "$os" = darwin ] && { [[ " ${channels[*]-} " = *" --desktop "* ]] || [[ " ${channels[*]-} " = *" --desktop=true "* ]]; }; then
                printf '  Check permission: %s\n' "$(quote_shell_command "$installed" setup-gemini permission-status --control-root "$native_root")"
                printf '  Grant if needed: %s\n' "$(quote_shell_command "$installed" setup-gemini request-permission --control-root "$native_root")"
            fi
        fi
        if { [[ " ${channels[*]-} " = *" --webhook "* ]] || [[ " ${channels[*]-} " = *" --webhook=true "* ]]; }; then
            echo "  Webhook consent recorded; configure and enable its destination and status channel in shared settings."
        fi
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Remove: %s\n' "$(gemini_remove_command "$installed" "$native_root")"
        fi
    } > "$report"
    publish_setup_summary "$report"
    status=$?
    rm -f "$report"
    return "$status"
}

gemini_remove_command() {
    observer_remove_command "$1" setup-gemini --control-root "$2"
}

install_qoder() {
    parallel_phase download
    local root binary runtime os arch base native native_root config_path installed
    local channels=(${OPENCODE_ARGS[@]+"${OPENCODE_ARGS[@]}"})
    if [ -n "$_SELECTOR_INTENT" ]; then channels=("${_FROZEN_QODER_CHANNELS[@]}"); fi
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
    set -- setup-qoder install --binary "$binary" "${channels[@]}"
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$root" --home "$INSTALLER_HOME"
        set -- "$@" --config-root "$_FROZEN_QODER" --qoder-executable "$_QODER_EXEC"
    fi
    if [ "$os" = darwin ] && { [[ " ${channels[*]-} " = *" --desktop "* ]] || [[ " ${channels[*]-} " = *" --desktop=true "* ]]; }; then
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
    run_setup_stage "Installing Qoder notifications" observer_install_transaction "$root" "$runtime" "$@" </dev/null || return 1
    progress_phase "$PRODUCT" runtime-lookup registration_committed
    native_root="$root"
    [ "$os" != windows ] || native_root=$(cygpath -m "$root") || return 1
    runtime=$("$_CONFIG_HELPER" config installer runtime-root "$native_root") || return 1
    installed="$runtime/claude-notifications-$os-$arch"
    [ "$os" != windows ] || installed="$installed.exe"
    progress_phase "$PRODUCT" config registration_committed
    if [ -z "$_PARALLEL_WORKER" ]; then run_setup_stage "Preparing notification settings" initialize_config || return 1; fi
    config_path=$("$_CONFIG_HELPER" config path) || return 1
    local report status
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-qoder-summary-XXXXXX") || return 1
    {
        echo "  Qoder - installed; restart required."
        print_delivery_note
        echo "  Reload Qoder to load the installed notification plugin."
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Shared settings: %s\n' "$config_path"
            echo "  Native completion, question, permission, plan and teammate hooks; existing settings were preserved."
        fi
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Inspect registration and consent: %s\n' "$(quote_shell_command "$installed" setup-qoder inspect --control-root "$native_root" --config-root "$_FROZEN_QODER")"
            if [ "$os" = darwin ] && { [[ " ${channels[*]-} " = *" --desktop "* ]] || [[ " ${channels[*]-} " = *" --desktop=true "* ]]; }; then
                printf '  Check permission: %s\n' "$(quote_shell_command "$installed" setup-qoder permission-status --control-root "$native_root")"
                printf '  Grant if needed: %s\n' "$(quote_shell_command "$installed" setup-qoder request-permission --control-root "$native_root")"
            fi
        fi
        if { [[ " ${channels[*]-} " = *" --webhook "* ]] || [[ " ${channels[*]-} " = *" --webhook=true "* ]]; }; then
            echo "  Webhook consent recorded; configure and enable its destination and status channel in shared settings."
        fi
        if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then
            printf '  Remove: %s\n' "$(qoder_remove_command "$installed" "$native_root" "$_FROZEN_QODER" "$_QODER_EXEC")"
        fi
    } > "$report"
    publish_setup_summary "$report"
    status=$?
    rm -f "$report"
    return "$status"
}

qoder_remove_command() {
    observer_remove_command "$1" setup-qoder --control-root "$2" --config-root "$3" --qoder-executable "$4"
}

# These rows describe observed phases; they never authorize another phase.
progress_phase() {
    local i=0
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        if [ "${_PROGRESS_IDS[$i]}" = "$1" ]; then
            _PROGRESS_PHASES[$i]=$2; _PROGRESS_OUTCOMES[$i]=$3
            progress_event "$1" "$2" "$3"
            return 0
        fi
        i=$((i + 1))
    done
}

# The final board already presents registration and restart status. Compact only
# our generated display summary after that board successfully renders; this does
# not change installation state or any recovery, trust or delivery instruction.
compact_rich_setup_summary() {
    local summary=${BOOTSTRAP_SUMMARY_FILE:-} combined
    [ -n "$summary" ] && [ -f "$summary" ] && [ ! -L "$summary" ] || return 0
    combined=$(mktemp "${summary}.rich-XXXXXX") || return 0
    if awk -v selected=" ${_PROGRESS_IDS[*]} " '
        function chosen(client) { return index(selected, " " client " ") != 0 }
        BEGIN { tool_prefix = "    Agent notification tool: " }
        {
            client = ""
            if ($0 == "  Claude - installed; restart required.") client = "claude"
            if ($0 == "  Codex - installed; restart required.") client = "codex"
            if ($0 == "  OpenCode - installed; restart required to load the global plugin.") client = "opencode"
            if ($0 == "  Gemini CLI - installed; restart required.") client = "gemini"
            if ($0 == "  Qoder - installed; restart required.") client = "qoder"
            if ($0 == "  Pi - installed; restart Pi to load the native extension.") client = "pi"
            if (client != "") {
                current = client
                if (chosen(client)) next
            }
            if ((current == "claude" || current == "codex") && chosen(current) &&
                index($0, tool_prefix) == 1) {
                if ($0 == tool_prefix "installed.") next
                label = current == "claude" ? "Claude" : "Codex"
                print "  " label " notification tool: " substr($0, length(tool_prefix) + 1)
                next
            }
            print
        }
    ' "$summary" > "$combined"; then
        mv -f -- "$combined" "$summary" || rm -f "$combined"
    else
        rm -f "$combined"
    fi
    return 0
}

progress_write_final_snapshot() {
    local i=0
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        printf '%s\t%s\t%s\n' "${_PROGRESS_IDS[$i]}" "${_PROGRESS_PHASES[$i]}" "${_PROGRESS_OUTCOMES[$i]}" || return 1
        i=$((i + 1))
    done
    printf 'stop\t\t\n'
}

report_progress() {
    _FINAL_BOARD_RENDERED=false
    progress_stop "${1:-0}"
    local rendered=false
    if progress_helper_available && [ "$_PROGRESS_SESSION_SUPPORTED" = true ] && [ "$_PROGRESS_JOURNAL_DISABLED" != true ] && [ -n "$_PROGRESS_JOURNAL" ]; then
        progress_snapshots
        progress_record stop "" "" ""
        local activity_args=(--activity "$_ACTIVITY_MODE")
        [ "$_ACTIVITY_LIMIT" -eq 0 ] || activity_args+=(--activity-limit "$_ACTIVITY_LIMIT")
        if [ "$_PROGRESS_JOURNAL_DISABLED" != true ]; then
            "$_CONFIG_HELPER" installer-progress --session --events "$_PROGRESS_JOURNAL" --ui "${_UI_MODE:-auto}" "${activity_args[@]}" --owner-pid "$$" </dev/null >/dev/null &
            _PROGRESS_PID=$!
            if progress_reap "$_PROGRESS_PID"; then
                rendered=true
                if [ "${1:-0}" -eq 0 ] && progress_rich_available; then compact_rich_setup_summary; fi
            else _PROGRESS_JOURNAL_DISABLED=true; fi
            _PROGRESS_PID=""
        fi
    elif progress_rich_available && progress_helper_available && [ "$_PROGRESS_SESSION_SUPPORTED" != true ]; then
        local events
        events=$(mktemp "${TMPDIR:-/tmp}/bootstrap-progress-final-XXXXXX") || events=""
        if [ -n "$events" ]; then
            _PROGRESS_EVENTS=$events; _PROGRESS_KIND=final
            # Spawn only after every row and the stop sentinel are safely written.
            if progress_write_final_snapshot > "$events"; then
                "$_CONFIG_HELPER" installer-progress --events "$events" --ui "${_UI_MODE:-auto}" --owner-pid "$$" </dev/null >/dev/null &
                _PROGRESS_PID=$!
                if progress_reap "$_PROGRESS_PID"; then
                    rendered=true
                    _FINAL_BOARD_RENDERED=true
                    [ "${1:-0}" -ne 0 ] || compact_rich_setup_summary
                fi
                _PROGRESS_PID=""
            fi
            rm -f "$events"
            _PROGRESS_EVENTS=""; _PROGRESS_KIND=""
        fi
    fi
    [ "$rendered" != true ] || return 0
    local i=0 outcome label
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        outcome=${_PROGRESS_OUTCOMES[$i]}
        if [ "$outcome" = completed ] && [ "${1:-0}" -eq 0 ] && [ "${BOOTSTRAP_VERBOSE:-0}" != 1 ] && [ "$_PROGRESS_JOURNAL_DISABLED" != true ]; then
            i=$((i + 1)); continue
        fi
        case "${_PROGRESS_IDS[$i]}" in claude) label='Claude Code' ;; codex) label='Codex CLI' ;; opencode) label=OpenCode ;; gemini) label='Gemini CLI' ;; qoder) label=Qoder ;; pi) label=Pi ;; cursor) label=Cursor ;; *) label=Client ;; esac
        case "$outcome" in
            not_started) outcome='queued; not started' ;;
            completed) outcome='completed; restart required' ;;
            skipped) outcome='skipped; existing setup kept' ;;
            activation_timeout) outcome='registration saved; activation timed out' ;;
            activation_failed) outcome='registration saved; activation failed' ;;
            activation_canceled) outcome='registration saved; activation canceled' ;;
            activation_deadline) outcome='registration saved; setup deadline reached' ;;
            activation_not_attempted|registration_committed_follow_up_not_started) outcome='registration saved; activation not started' ;;
            registration_committed) outcome='registration saved; follow-up incomplete' ;;
            *) outcome='state requires inspection' ;;
        esac
        printf '%-12s %s\n' "$label" "$outcome" >&2
        i=$((i + 1))
    done
}

# The session mutex supplements (never replaces) each writer's native CAS and
# ownership checks. Reentrancy spans Claude cache rollback and runtime promotion.
bootstrap_writer_gate() {
    local nonblocking=false
    _PARALLEL_GATE_BUSY=false
    if [ "${1:-}" = --try ]; then nonblocking=true; shift; fi
    if [ -z "$_PARALLEL_ROOT" ] || [ "$_PARALLEL_GATE_HELD" = true ]; then "$@"; return $?; fi
    [ ! -e "$_PARALLEL_ROOT/cancel" ] || return 130
    parallel_phase waiting
    while ! mkdir "$_PARALLEL_ROOT/writer.lock" 2>/dev/null; do
        [ ! -e "$_PARALLEL_ROOT/cancel" ] || return 130
        # A legitimate owner can remove or reacquire the directory after mkdir
        # fails. Reject only observed non-directory kinds; absence must retry.
        if [ -L "$_PARALLEL_ROOT/writer.lock" ] || [ -f "$_PARALLEL_ROOT/writer.lock" ] ||
            [ -p "$_PARALLEL_ROOT/writer.lock" ] || [ -S "$_PARALLEL_ROOT/writer.lock" ] ||
            [ -b "$_PARALLEL_ROOT/writer.lock" ] || [ -c "$_PARALLEL_ROOT/writer.lock" ]; then return 1; fi
        # Keep this count across --try calls; a real held directory is never
        # time-limited and resets consecutive absent-path failures.
        if [ -d "$_PARALLEL_ROOT/writer.lock" ]; then
            _PARALLEL_GATE_ABSENT_FAILURES=0
        else
            _PARALLEL_GATE_ABSENT_FAILURES=$((_PARALLEL_GATE_ABSENT_FAILURES + 1))
            if [ "$_PARALLEL_GATE_ABSENT_FAILURES" -ge 32 ]; then
                : > "$_PARALLEL_ROOT/cancel"
                return 1
            fi
        fi
        if [ "$nonblocking" = true ]; then _PARALLEL_GATE_BUSY=true; return 75; fi
        sleep 0.05
    done
    _PARALLEL_GATE_ABSENT_FAILURES=0
    local status=0
    printf '%s\n' "${_PARALLEL_WORKER:-parent}" > "$_PARALLEL_ROOT/writer.lock/owner"
    _PARALLEL_GATE_HELD=true
    parallel_phase runtime
    "$@" || status=$?
    _PARALLEL_GATE_HELD=false
    parallel_release_writer_gate || return 1
    return "$status"
}

parallel_release_writer_gate() {
    if ! rm -f "$_PARALLEL_ROOT/writer.lock/owner" || ! rmdir "$_PARALLEL_ROOT/writer.lock"; then
        # An ownerless lock cannot be reclaimed as a completed transaction.
        # Stop queued writers and policy waiters instead of stranding them.
        : > "$_PARALLEL_ROOT/cancel"
        return 1
    fi
    return 0
}

parallel_phase() {
    [ -n "$_PARALLEL_WORKER" ] || return 0
    progress_phase "$_PARALLEL_WORKER" "$1" running
}

# Mixed fresh installs must seed the approved MCP policy before observer channel
# consent can create that policy with enabled=false. Acquisition stays concurrent.
parallel_policy_barrier() {
    [ -n "$_PARALLEL_WORKER" ] && [ -n "$_PARALLEL_ROOT" ] || return 0
    parallel_phase waiting
    while [ ! -f "$_PARALLEL_ROOT/policy.ready" ]; do
        [ -d "$_PARALLEL_ROOT" ] && [ ! -L "$_PARALLEL_ROOT" ] || return 1
        if [ -e "$_PARALLEL_ROOT/policy.unavailable" ] || [ -e "$_PARALLEL_ROOT/cancel" ]; then
            echo 'Observer registration retained for retry: approved shared policy preparation is unavailable.' >&2
            return 1
        fi
        sleep 0.05
    done
}

observer_install_transaction() {
    parallel_policy_barrier || return 1
    bootstrap_writer_gate observer_install_apply "$@"
}

observer_install_apply() {
    local root="$1" runtime="$2"; shift 2
    # Read the ledger only after acquiring the coordinator, then let the helper
    # plan, save policy and promote bytes under its existing locks/CAS.
    [ -e "$root/ownership.json" ] || set -- "$@" --runtime-root "$runtime"
    "$_CONFIG_HELPER" "$@"
}

parallel_install_available() {
    [ "${#SELECTED_PRODUCTS[@]}" -gt 1 ] && [ "$_PRODUCT_CSV" != cursor ] || return 1
    # Pi's common portable call follows the existing primary stages. Its
    # immutable intent must observe any runtime preparation they committed.
    case ",$_PRODUCT_CSV," in *,pi,*) return 1 ;; esac
    local capability
    capability=$("$_CONFIG_HELPER" installer-progress --parallel-capabilities </dev/null 2>/dev/null) || return 1
    [ "$capability" = installer-parallel-v1 ]
}

# Fixed line records are data, never shell assignments. Each leaf is created
# inside the private session directory and atomically renamed when complete.
parallel_write_result() {
    local status="$1" i=0 phase=preparation outcome=state_requires_inspection
    while [ "$i" -lt "${#_PROGRESS_IDS[@]}" ]; do
        if [ "${_PROGRESS_IDS[$i]}" = "$_PARALLEL_WORKER" ]; then
            phase=${_PROGRESS_PHASES[$i]}; outcome=${_PROGRESS_OUTCOMES[$i]}; break
        fi
        i=$((i + 1))
    done
    if [ "$status" -ne 0 ] && [ "$outcome" = running ]; then outcome=state_requires_inspection; fi
    if [ "$status" -eq 0 ]; then
        case "$_PARALLEL_WORKER" in
            claude|codex)
                phase=MCP; outcome=registration_committed_follow_up_not_started
                if [ "$CONFIGURE_NOTIFICATIONS" != true ]; then phase=complete; outcome=completed; fi ;;
            *) phase=complete; outcome=completed ;;
        esac
    fi
    # Paths containing line delimiters cannot cross this fixed record boundary.
    case "$PLUGIN_ROOT$CONFIGURE_BINARY$_BOOTSTRAP_STAGE" in *$'\n'*|*$'\r'*) return 1 ;; esac
    printf '%s\n' installer-worker-v1 "$status" "$PLUGIN_ROOT" "$CONFIGURE_BINARY" "$phase" "$outcome" \
        "$_BOOTSTRAP_STAGE" > "$_PARALLEL_JOB/result.pending" || return 1
    mv "$_PARALLEL_JOB/result.pending" "$_PARALLEL_JOB/result"
}

parallel_worker() {
    trap - EXIT INT TERM HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP
    umask 077
    _PARALLEL_WORKER=$1; _PARALLEL_JOB=$2
    PRODUCT=$1; _CONFIG_STAGE="$_PARALLEL_JOB/stage"
    _CONFIG_HELPER="$_CONFIG_STAGE/${_CONFIG_HELPER##*/}"
    _BOOTSTRAP_STAGE=""; _PORTABLE_STAGE=""; PLUGIN_ROOT=""; CONFIGURE_BINARY=""
    _PROGRESS_PID=""; _PROGRESS_EVENTS=""; _PROGRESS_KIND=""
    _PROGRESS_JOURNAL="$_PARALLEL_JOB/events"; _PROGRESS_JOURNAL_DISABLED=false
    _PROGRESS_LOG=""; _PROGRESS_STDERR=""; _DIAGNOSTIC_LABEL=""
    BOOTSTRAP_SUMMARY_FILE="$_PARALLEL_JOB/summary"
    export BOOTSTRAP_SUMMARY_FILE
    local status=0
    progress_phase "$PRODUCT" preparation running
    case "$PRODUCT" in
        claude) CN_PRODUCT=claude install_claude || status=$? ;;
        codex) install_codex || status=$? ;;
        opencode) install_opencode || status=$? ;;
        gemini) install_gemini || status=$? ;;
        qoder) install_qoder || status=$? ;;
        *) status=1 ;;
    esac
    if [ "$status" -eq 3 ]; then
        progress_phase "$PRODUCT" config registration_committed
        report_config_init_failure || true
    fi
    if [ "$status" -ne 0 ]; then
        printf 'Retained installer files: %s\n' "$_PARALLEL_JOB" >&2
        [ -z "$_BOOTSTRAP_STAGE" ] || printf 'Retained acquisition: %s\n' "$_BOOTSTRAP_STAGE" >&2
    fi
    parallel_write_result "$status" || return 1
    return "$status"
}

# Run only an external argv command asynchronously. Stateful shell functions
# remain in their owner; worker commands stay inside the worker's primary group.
bootstrap_external() {
    if [ "$_PARALLEL_ACTIVE" != true ] || [ -n "$_PARALLEL_WORKER" ]; then "$@"; return $?; fi
    local monitor_flags=$- dispatching=$_PARALLEL_DISPATCHING status=0 pid index=${#_PARALLEL_AUX_PIDS[@]}
    _PARALLEL_DISPATCHING=true
    set -m
    command "$@" </dev/null &
    pid=$!
    _PARALLEL_AUX_PIDS+=("$pid"); _PARALLEL_AUX_DONE+=(false)
    case "$monitor_flags" in *m*) ;; *) set +m ;; esac
    _PARALLEL_DISPATCHING=$dispatching
    [ "$_PARALLEL_INTERRUPT_STATUS" -eq 0 ] || exit "$_PARALLEL_INTERRUPT_STATUS"
    # Bash wait is interruptible by the parent's signal trap. Its EXIT cleanup
    # can now stop the helper while its native runner still owns child cleanup.
    wait "$pid" || status=$?
    _PARALLEL_AUX_DONE[$index]=true
    return "$status"
}

# Only children launched as monitor-mode jobs belong to these process groups.
# Give native Go owners time to close their separate client-probe groups before
# escalating. All owned jobs are joined before files or presentation are removed.
parallel_cancel_jobs() {
    [ -n "$_PARALLEL_ROOT" ] && [ -d "$_PARALLEL_ROOT" ] || return 0
    local i=0 pid tick=0 alive=false deadline=$((SECONDS + 5))
    : > "$_PARALLEL_ROOT/cancel"
    while [ "$i" -lt "${#_PARALLEL_PIDS[@]}" ]; do
        pid=${_PARALLEL_PIDS[$i]}
        [ "${_PARALLEL_DONE[$i]}" = true ] || kill -TERM -- "-$pid" 2>/dev/null || true
        i=$((i + 1))
    done
    i=0
    while [ "$i" -lt "${#_PARALLEL_AUX_PIDS[@]}" ]; do
        pid=${_PARALLEL_AUX_PIDS[$i]}
        [ "${_PARALLEL_AUX_DONE[$i]}" = true ] || kill -TERM -- "-$pid" 2>/dev/null || true
        i=$((i + 1))
    done
    while [ "$tick" -lt 100 ] && [ "$SECONDS" -lt "$deadline" ]; do
        alive=false; i=0
        while [ "$i" -lt "${#_PARALLEL_PIDS[@]}" ]; do
            if [ "${_PARALLEL_DONE[$i]}" != true ] && kill -0 -- "-${_PARALLEL_PIDS[$i]}" 2>/dev/null; then alive=true; fi
            i=$((i + 1))
        done
        i=0
        while [ "$i" -lt "${#_PARALLEL_AUX_PIDS[@]}" ]; do
            if [ "${_PARALLEL_AUX_DONE[$i]}" != true ] && kill -0 -- "-${_PARALLEL_AUX_PIDS[$i]}" 2>/dev/null; then alive=true; fi
            i=$((i + 1))
        done
        [ "$alive" = true ] || break
        sleep 0.05; tick=$((tick + 1))
    done
    i=0
    while [ "$i" -lt "${#_PARALLEL_PIDS[@]}" ]; do
        pid=${_PARALLEL_PIDS[$i]}
        if [ "${_PARALLEL_DONE[$i]}" != true ]; then
            if kill -0 -- "-$pid" 2>/dev/null; then
                _PARALLEL_FORCED_STOP=true
                kill -KILL -- "-$pid" 2>/dev/null || true
            fi
            wait "$pid" 2>/dev/null || true
            _PARALLEL_DONE[$i]=true
        fi
        i=$((i + 1))
    done
    i=0
    while [ "$i" -lt "${#_PARALLEL_AUX_PIDS[@]}" ]; do
        pid=${_PARALLEL_AUX_PIDS[$i]}
        if [ "${_PARALLEL_AUX_DONE[$i]}" != true ]; then
            if kill -0 -- "-$pid" 2>/dev/null; then
                _PARALLEL_FORCED_STOP=true
                kill -KILL -- "-$pid" 2>/dev/null || true
            fi
            wait "$pid" 2>/dev/null || true
            _PARALLEL_AUX_DONE[$i]=true
        fi
        i=$((i + 1))
    done
    if [ "$_PARALLEL_FORCED_STOP" = true ]; then _KEEP_CONFIG_STAGE=true; _KEEP_PORTABLE_STAGE=true; fi
}

parallel_read_result() {
    local file="$1" bytes version extra
    [ -f "$file" ] && [ ! -L "$file" ] || return 1
    bytes=$(LC_ALL=C wc -c < "$file") || return 1
    [ "$bytes" -le 32768 ] || return 1
    {
        IFS= read -r version && IFS= read -r _JOB_STATUS && IFS= read -r _JOB_PLUGIN &&
        IFS= read -r _JOB_BINARY && IFS= read -r _JOB_PHASE && IFS= read -r _JOB_OUTCOME &&
        IFS= read -r _JOB_STAGE && ! IFS= read -r extra
    } < "$file" || return 1
    [ "$version" = installer-worker-v1 ] || return 1
    case "$_JOB_STATUS" in ''|*[!0-9]*) return 1 ;; esac
    [ "${#_JOB_STATUS}" -le 3 ] && [ "$_JOB_STATUS" -le 255 ] || return 1
    case "$_JOB_PHASE" in preparation|marketplace|download|runtime|verification|waiting|registration|runtime-lookup|config|MCP|complete) ;; *) return 1 ;; esac
    case "$_JOB_OUTCOME" in running|state_requires_inspection|registration_committed|registration_committed_follow_up_not_started|completed) ;; *) return 1 ;; esac
}

parallel_drain_events() {
    local index="$1" client="${_PARALLEL_CLIENTS[$1]}" file="$_PARALLEL_ROOT/${_PARALLEL_CLIENTS[$1]}/events"
    local line=0 kind id phase outcome extra bytes
    [ -f "$file" ] && [ ! -L "$file" ] || return 0
    bytes=$(LC_ALL=C wc -c < "$file") || return 0
    [ "$bytes" -le 262144 ] || return 0
    while IFS=$'\t' read -r kind id phase outcome extra; do
        line=$((line + 1))
        [ "$line" -gt "${_PARALLEL_LINES[$index]}" ] || continue
        [ "$kind" = transition ] && [ "$id" = "$client" ] && [ -z "$extra" ] || continue
        case "$phase" in preparation|marketplace|download|runtime|verification|waiting|registration|runtime-lookup|config|MCP|complete) ;; *) continue ;; esac
        case "$outcome" in running|state_requires_inspection|registration_committed|registration_committed_follow_up_not_started|completed) ;; *) continue ;; esac
        progress_phase "$id" "$phase" "$outcome"
    done < "$file"
    _PARALLEL_LINES[$index]=$line
}

# After the producer has stopped, recover only finite facts from the parent's
# private journal. Do not append while replaying or parse provider output.
parallel_replay_progress() {
    local file=$_PROGRESS_JOURNAL bytes kind id phase outcome extra index
    [ -n "$file" ] && [ -f "$file" ] && [ ! -L "$file" ] || return 0
    bytes=$(LC_ALL=C wc -c < "$file") || return 0
    [ "$bytes" -le 262144 ] || return 0
    while IFS=$'\t' read -r kind id phase outcome extra; do
        case "$kind" in transition|snapshot) ;; *) continue ;; esac
        [ -z "$extra" ] || continue
        case "$id" in claude|codex|opencode|gemini|qoder|cursor) ;; *) continue ;; esac
        case "$phase" in pending|preparation|marketplace|download|runtime|verification|waiting|registration|runtime-lookup|config|MCP|complete) ;; *) continue ;; esac
        case "$outcome" in not_started|running|done|state_requires_inspection|registration_committed|registration_committed_follow_up_not_started|completed|activation_failed|activation_timeout|activation_canceled|activation_deadline|activation_not_attempted|skipped) ;; *) continue ;; esac
        index=0
        while [ "$index" -lt "${#_PROGRESS_IDS[@]}" ]; do
            if [ "${_PROGRESS_IDS[$index]}" = "$id" ]; then
                if [ "$phase" = MCP ] && [ "$outcome" = completed ]; then phase=complete; fi
                _PROGRESS_PHASES[$index]=$phase; _PROGRESS_OUTCOMES[$index]=$outcome
                break
            fi
            index=$((index + 1))
        done
    done < "$file"
    return 0
}

parallel_seed_policy() {
    local status=0
    # Never wait here: a dead holder must still be reaped by the parent loop.
    bootstrap_writer_gate --try parallel_seed_policy_transaction || status=$?
    [ "$_PARALLEL_GATE_BUSY" != true ] || return 75
    if [ "$status" -eq 0 ]; then
        _PARALLEL_POLICY_READY=true
        : > "$_PARALLEL_ROOT/policy.ready"
    else
        : > "$_PARALLEL_ROOT/policy.unavailable"
    fi
    return "$status"
}

parallel_seed_policy_transaction() {
    # Hold one gate across global config initialization and approved policy seed.
    local saved_product=$PRODUCT status=0
    PRODUCT=$LEGACY_PRODUCT
    run_setup_stage "Preparing notification settings" initialize_config || status=$?
    if [ "$status" -eq 0 ] && [ "$CONFIGURE_NOTIFICATIONS" = true ]; then
        run_setup_stage "Preparing approved notification policy" configure_agent_policy portable || status=$?
    fi
    PRODUCT=$saved_product
    return "$status"
}

install_products_parallel() {
    local selected job i remaining status=0 worker_status=0 seed_attempted=false seed_pending=false seed_status=0 legacy_remaining=0
    local seed_plugin="" seed_binary="" notify_status=1 completion_i=0
    local claude_plugin="" claude_binary="" codex_binary="" parent_summary=${BOOTSTRAP_SUMMARY_FILE:-}
    local monitor_flags=$-
    _PARALLEL_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-parallel-XXXXXX") || return 1
    _PARALLEL_ROOT=$(cd -P "$_PARALLEL_ROOT" && pwd -P) || return 1
    chmod 700 "$_PARALLEL_ROOT" || return 1
    _PARALLEL_ACTIVE=true; _PARALLEL_POLICY_READY=false; _PARALLEL_READY_CLIENTS=""; _PARALLEL_GATE_ABSENT_FAILURES=0
    _PARALLEL_PIDS=(); _PARALLEL_CLIENTS=(); _PARALLEL_DONE=(); _PARALLEL_LINES=()
    _PARALLEL_AUX_PIDS=(); _PARALLEL_AUX_DONE=(); _PARALLEL_FORCED_STOP=false; _PARALLEL_FOLLOW_UP_STARTED=false
    # The synchronous portable caller can append finite activation facts to this
    # same private journal after every primary worker has been joined.
    mkdir "$_CONFIG_STAGE/parallel" || return 1
    chmod 700 "$_CONFIG_STAGE/parallel" || return 1
    if [ -n "$_PROGRESS_JOURNAL" ] && [ -f "$_PROGRESS_JOURNAL" ] && [ ! -L "$_PROGRESS_JOURNAL" ]; then
        mv "$_PROGRESS_JOURNAL" "$_CONFIG_STAGE/parallel/progress.events" || return 1
    else
        (umask 077; : > "$_CONFIG_STAGE/parallel/progress.events") || return 1
    fi
    _PROGRESS_JOURNAL="$_CONFIG_STAGE/parallel/progress.events"
    chmod 600 "$_PROGRESS_JOURNAL" || return 1
    # Pre-create every IPC leaf and verified stage before any worker starts.
    for selected in claude codex opencode gemini qoder; do
        case ",$_PRODUCT_CSV," in *",$selected,"*) ;; *) continue ;; esac
        job="$_PARALLEL_ROOT/$selected"
        (umask 077; mkdir "$job" "$job/stage"; : > "$job/events"; : > "$job/summary"; : > "$job/stdout"; : > "$job/stderr") || return 1
        cp -pPR "$_CONFIG_STAGE/." "$job/stage/" || return 1
        case "$selected" in claude|codex) legacy_remaining=$((legacy_remaining + 1)) ;; esac
    done
    if [ -z "$LEGACY_PRODUCT" ]; then
        run_setup_stage "Preparing notification settings" initialize_config || return 1
        _PARALLEL_POLICY_READY=true; : > "$_PARALLEL_ROOT/policy.ready"
    fi
    case "$monitor_flags" in *m*) _PARALLEL_MONITOR_WAS_SET=true ;; *) _PARALLEL_MONITOR_WAS_SET=false ;; esac
    set -m
    _PARALLEL_IN_FLIGHT=true
    progress_start "Installing selected clients"
    _PARALLEL_DISPATCHING=true
    for selected in claude codex opencode gemini qoder; do
        case ",$_PRODUCT_CSV," in *",$selected,"*) ;; *) continue ;; esac
        [ "$_PARALLEL_INTERRUPT_STATUS" -eq 0 ] || break
        job="$_PARALLEL_ROOT/$selected"
        parallel_worker "$selected" "$job" </dev/null > "$job/stdout" 2> "$job/stderr" &
        _PARALLEL_PIDS+=("$!"); _PARALLEL_CLIENTS+=("$selected"); _PARALLEL_DONE+=(false); _PARALLEL_LINES+=(0)
    done
    _PARALLEL_DISPATCHING=false
    [ "$_PARALLEL_INTERRUPT_STATUS" -eq 0 ] || exit "$_PARALLEL_INTERRUPT_STATUS"
    [ "$_PARALLEL_MONITOR_WAS_SET" = true ] || set +m
    remaining=${#_PARALLEL_PIDS[@]}
    while [ "$remaining" -gt 0 ]; do
        i=0
        while [ "$i" -lt "${#_PARALLEL_PIDS[@]}" ]; do
            selected=${_PARALLEL_CLIENTS[$i]}; job="$_PARALLEL_ROOT/$selected"
            if [ "${_PARALLEL_DONE[$i]}" = true ]; then i=$((i + 1)); continue; fi
            parallel_drain_events "$i"
            if ! progress_child_running "${_PARALLEL_PIDS[$i]}"; then
                worker_status=0; wait "${_PARALLEL_PIDS[$i]}" 2>/dev/null || worker_status=$?
                # Join closes the journal. Drain its final transitions before
                # merging the authoritative result, then never drain it again.
                parallel_drain_events "$i"
                # Only an interrupted worker can leave active descendants.
                # Never signal a normally completed, already-joined job group.
                if [ ! -f "$job/result" ]; then
                    kill -KILL -- "-${_PARALLEL_PIDS[$i]}" 2>/dev/null || true
                fi
                _PARALLEL_DONE[$i]=true; remaining=$((remaining - 1))
                if [ -f "$_PARALLEL_ROOT/writer.lock/owner" ] && [ "$(cat "$_PARALLEL_ROOT/writer.lock/owner")" = "$selected" ]; then
                    parallel_release_writer_gate || status=1
                fi
                if parallel_read_result "$job/result" && [ "$_JOB_STATUS" -eq "$worker_status" ]; then
                    progress_phase "$selected" "$_JOB_PHASE" "$_JOB_OUTCOME"
                    if [ "$worker_status" -eq 0 ]; then
                        case "$selected" in
                            claude) claude_plugin=$_JOB_PLUGIN; claude_binary=$_JOB_BINARY ;;
                            codex) codex_binary=$_JOB_BINARY ;;
                        esac
                        case "$selected" in
                            claude|codex)
                                _PARALLEL_READY_CLIENTS=${_PARALLEL_READY_CLIENTS:+$_PARALLEL_READY_CLIENTS,}$selected
                                if [ "$seed_attempted" != true ] && [ "$seed_pending" != true ]; then
                                    seed_pending=true; seed_plugin=$_JOB_PLUGIN; seed_binary=$_JOB_BINARY
                                fi ;;
                        esac
                    else status=1; fi
                else
                    status=1
                    printf 'Worker result unavailable; inspect retained files: %s\n' "$job" >> "$job/stderr"
                    progress_phase "$selected" preparation state_requires_inspection
                fi
                case "$selected" in claude|codex) legacy_remaining=$((legacy_remaining - 1)) ;; esac
                if [ "$legacy_remaining" -eq 0 ] && [ -z "$_PARALLEL_READY_CLIENTS" ] && [ -n "$LEGACY_PRODUCT" ]; then
                    : > "$_PARALLEL_ROOT/policy.unavailable"
                fi
            fi
            i=$((i + 1))
        done
        if [ "$seed_pending" = true ]; then
            PLUGIN_ROOT=$seed_plugin; CONFIGURE_BINARY=$seed_binary; seed_status=0
            parallel_seed_policy || seed_status=$?
            if [ "$_PARALLEL_GATE_BUSY" != true ]; then
                seed_attempted=true; seed_pending=false
                [ "$seed_status" -eq 0 ] || status=1
            fi
        fi
        [ "$remaining" -eq 0 ] || sleep 0.05
    done
    [ "$_PARALLEL_MONITOR_WAS_SET" = true ] || set +m
    # Deterministic diagnostic and display summary order is independent of finish
    # order. No child writes into the parent's summary or diagnostic stream.
    for selected in "${_PARALLEL_CLIENTS[@]}"; do
        job="$_PARALLEL_ROOT/$selected"
        _PROGRESS_LOG="$job/stdout"; _PROGRESS_STDERR="$job/stderr"; _DIAGNOSTIC_LABEL="Installing $selected notifications"
        worker_status=1
        if parallel_read_result "$job/result"; then worker_status=$_JOB_STATUS; fi
        diagnostic_capture_stage "$worker_status"
        [ ! -s "$job/stderr" ] || _DIAGNOSTIC_NOTED=true
    done
    _PROGRESS_LOG=""; _PROGRESS_STDERR=""; _DIAGNOSTIC_LABEL=""
    case ",$_PARALLEL_READY_CLIENTS," in
        *,claude,*) case ",$_PARALLEL_READY_CLIENTS," in *,codex,*) _PARALLEL_READY_CLIENTS=claude,codex ;; esac ;;
    esac
    PLUGIN_ROOT=$claude_plugin; CONFIGURE_BINARY=${codex_binary:-$claude_binary}; PRODUCT=$LEGACY_PRODUCT
    if [ -n "$_PARALLEL_READY_CLIENTS" ]; then
        if [ "$CONFIGURE_NOTIFICATIONS" = true ] && [ "$_PARALLEL_POLICY_READY" = true ]; then
            _PARALLEL_FOLLOW_UP_STARTED=true
            for selected in claude codex; do
                case ",$_PARALLEL_READY_CLIENTS," in *",$selected,"*) progress_phase "$selected" MCP registration_committed ;; esac
            done
            notify_status=0
            run_setup_stage "Configuring agent notification tools" configure_agent_notify || notify_status=$?
            [ "$notify_status" -eq 0 ] || status=1
        elif [ "$CONFIGURE_NOTIFICATIONS" = true ]; then status=1; fi
        # Keep typed per-client MCP failures when the wizard reports partial
        # success. With skipped MCP, primary registration is already complete.
        if [ "$CONFIGURE_NOTIFICATIONS" != true ]; then
            CLAUDE_AGENT_NOTIFY_STATUS='skipped (existing setup kept)'; CODEX_AGENT_NOTIFY_STATUS=$CLAUDE_AGENT_NOTIFY_STATUS
        fi
        for selected in claude codex; do
            case ",$_PARALLEL_READY_CLIENTS," in *",$selected,"*) ;; *) continue ;; esac
            PRODUCT=$selected
            if [ "$CONFIGURE_NOTIFICATIONS" != true ] || [ "$notify_status" -eq 0 ]; then
                progress_phase "$selected" complete completed
            else
                # A partial portable result can still authoritatively complete
                # one selected client. Never advance its failed sibling.
                completion_i=0
                while [ "$completion_i" -lt "${#_PROGRESS_IDS[@]}" ]; do
                    if [ "${_PROGRESS_IDS[$completion_i]}" = "$selected" ] &&
                        [ "${_PROGRESS_PHASES[$completion_i]}" = MCP ] && [ "${_PROGRESS_OUTCOMES[$completion_i]}" = completed ]; then
                        progress_phase "$selected" complete completed
                    fi
                    completion_i=$((completion_i + 1))
                done
            fi
            print_success || status=1
        done
    fi
    for selected in opencode gemini qoder; do
        job="$_PARALLEL_ROOT/$selected"
        if [ -f "$job/summary" ] && [ ! -L "$job/summary" ] && [ -s "$job/summary" ] && [ -n "$parent_summary" ]; then
            publish_setup_summary "$job/summary" || status=1
        fi
    done
    PRODUCT=$LEGACY_PRODUCT
    _PARALLEL_IN_FLIGHT=false
    progress_stop "$status"
    return "$status"
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
                run_setup_stage "Recording configuration recovery" report_config_init_failure || true
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
    if [ -n "$_DIAGNOSTIC_LOG" ] && [ "$_DIAGNOSTIC_FAILED" != true ] && { [ "$_SETUP_RECOVERY" = activation_available ] || [ "$_SETUP_RECOVERY" = config_available ]; }; then
        if [ "$_SETUP_RECOVERY" = config_available ]; then
            printf 'Next: run the config-only retry command in Diagnostics.\n' >&2
        else
            printf 'Next: resume saved activation with the command in Diagnostics.\n' >&2
        fi
    else
        printf 'Next: inspect owned registration before retrying; keep completed clients.\n' >&2
    fi
}

report_setup_failure() {
    local reason
    case "$_SETUP_FAILURE_REASON" in
        state_migration_failed)
            printf 'UAP registry upgrade was not completed. Client registrations have not been changed.\n' >&2
            if [ -f "$_CONFIG_STAGE/state-migration.stderr" ]; then
                cat "$_CONFIG_STAGE/state-migration.stderr" >&2
            fi
            printf 'Run setup again after resolving the registry issue and review the new installation plan.\n' >&2
            report_diagnostics
            return ;;
        probe_timeout) reason='plugin verification timed out.' ;;
        canceled) reason='activation was canceled.' ;;
        deadline) reason='the setup deadline was reached.' ;;
        activation_failed) reason='client activation failed.' ;;
        config_failed) reason='notification settings could not be saved.' ;;
        *) reason='an installer stage failed.' ;;
    esac
    printf 'Setup incomplete: %s\n' "$reason" >&2
    report_progress "${1:-1}"
    printf 'Delivery     not verified; notification permission not checked\n' >&2
    report_partial_recovery
    report_diagnostics
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
        case "$1:$item" in products:claude|products:codex|products:opencode|products:gemini|products:qoder|products:pi|products:cursor|channels:desktop|channels:webhook) ;; *) echo "Invalid selector ID." >&2; return 1 ;; esac
        case ",$seen," in *",$item,"*) echo "Duplicate selector ID." >&2; return 1 ;; esac
        seen=${seen:+$seen,}$item
        case "$remaining" in *,*) remaining=${remaining#*,} ;; *) break ;; esac
    done
}

# Fixed scalar projection. No JSON, eval or fresh discovery; config resolution
# only verifies the frozen authority without replacing the caller's selection.
load_frozen_dispatch() {
    local LC_ALL=C config_explicit=false resolved_config
    [ -z "${AGENT_NOTIFICATIONS_CONFIG:-}" ] || config_explicit=true
    _FROZEN_GLOBAL_CONFIG=""
    _FROZEN_STATE_MIGRATION_REQUIRED=""
    _FROZEN_PI_CHANNELS=()
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
        case "$key" in
            cursor-seed-enabled)
                case "$value" in true|false) ;; *) return 1 ;; esac
                case ",$_PRODUCT_CSV," in *,cursor,*) ;; *) return 1 ;; esac
                _FROZEN_CURSOR_SEED=$value
                continue ;;
            state-migration-required)
                case "$value" in true|false) ;; *) return 1 ;; esac
                _FROZEN_STATE_MIGRATION_REQUIRED=$value
                continue ;;
            opencode-channel-preimage|gemini-channel-preimage|qoder-channel-preimage|pi-channel-preimage|cursor-channel-preimage)
                if [ "$value" != absent ]; then
                    [ "${#value}" -eq 64 ] || return 1
                    case "$value" in *[!0-9a-f]*) return 1 ;; esac
                fi
                case ",$_PRODUCT_CSV," in *",${key%%-*},"*) ;; *) return 1 ;; esac
                case "$key" in
                    opencode-*) _FROZEN_OPENCODE_CHANNELS+=("--channel-preimage=$value") ;;
                    gemini-*) _FROZEN_GEMINI_CHANNELS+=("--channel-preimage=$value") ;;
                    qoder-*) _FROZEN_QODER_CHANNELS+=("--channel-preimage=$value") ;;
                    pi-*) _FROZEN_PI_CHANNELS+=("--$key=$value") ;;
                esac
                continue ;;
        esac
        case "$key" in
            opencode-desktop|opencode-webhook|gemini-desktop|gemini-webhook|qoder-desktop|qoder-webhook|pi-desktop|pi-webhook|cursor-desktop|cursor-webhook)
                case "$value" in true|false) ;; *) return 1 ;; esac
                case ",$_PRODUCT_CSV," in *",${key%-*},"*) ;; *) return 1 ;; esac
                case "$key" in
                    opencode-*) _FROZEN_OPENCODE_CHANNELS+=("--${key#opencode-}=$value") ;;
                    gemini-*) _FROZEN_GEMINI_CHANNELS+=("--${key#gemini-}=$value") ;;
                    qoder-*) _FROZEN_QODER_CHANNELS+=("--${key#qoder-}=$value") ;;
                    cursor-desktop) _FROZEN_CURSOR_DESKTOP=$value ;;
                    cursor-webhook) _FROZEN_CURSOR_WEBHOOK=$value ;;
                    pi-*) _FROZEN_PI_CHANNELS+=("--$key=$value") ;;
                esac
                continue ;;
        esac
        case "$value" in /*|[A-Za-z]:/*|[A-Za-z]:\\*) ;; *) return 1 ;; esac
        case "$key" in
            home) INSTALLER_HOME=$value ;;
            claude-config) CLAUDE_HOME=$value; export CLAUDE_CONFIG_DIR="$value" CLAUDE_HOME ;;
            claude-mcp-config) _FROZEN_CLAUDE_MCP=$value ;;
            codex-home) DEFAULT_CODEX_HOME=$value; export CODEX_HOME="$value" ;;
            codex-mcp-config) _FROZEN_CODEX_MCP=$value ;;
            opencode-config-dir) _FROZEN_OPENCODE=$value; export OPENCODE_CONFIG_DIR="$value" ;;
            gemini-config-root) _FROZEN_GEMINI=$value ;;
            qoder-config-root) _FROZEN_QODER=$value ;;
            pi-agent-dir) _FROZEN_PI=$value ;;
            control-root) _FROZEN_CONTROL=$value ;;
            runtime-root) _FROZEN_RUNTIME=$value ;;
            global-config)
                _FROZEN_GLOBAL_CONFIG=$value
                # Freezing an implicit default must not turn it into an
                # explicit override that bypasses historical config guards.
                if [ "$config_explicit" = true ]; then export AGENT_NOTIFICATIONS_CONFIG="$value"; fi ;;
            claude-executable) _CLAUDE_EXEC=$value ;;
            codex-executable) _CODEX_EXEC=$value ;;
            opencode-executable) _OPENCODE_EXEC=$value ;;
            gemini-executable) _GEMINI_EXEC=$value ;;
            qoder-executable) _QODER_EXEC=$value ;;
            pi-executable) _PI_EXEC=$value ;;
            scope-root) _FROZEN_SCOPE_ROOT=$value ;;
            client-executable) _FROZEN_CLIENT_EXEC=$value ;;
            *) return 1 ;;
        esac
    done <"$result"
    # Fresh Claude/Codex has no managed runtime until its existing hooks
    # preparation succeeds. Keep an observed runtime when present; never invent
    # one or pass an empty override into the later portable admission.
    local required="home control-root global-config" selected
    if [ "$_STATE_MIGRATION_CAPABLE" = true ]; then required="$required state-migration-required"; fi
    for selected in "${SELECTED_PRODUCTS[@]}"; do
        case "$selected" in
            claude) required="$required claude-config claude-mcp-config claude-executable" ;;
            codex) required="$required codex-home codex-mcp-config codex-executable" ;;
            opencode) required="$required opencode-config-dir opencode-executable opencode-desktop opencode-webhook opencode-channel-preimage" ;;
            gemini) required="$required gemini-config-root gemini-executable gemini-desktop gemini-webhook gemini-channel-preimage" ;;
            qoder) required="$required qoder-config-root qoder-executable qoder-desktop qoder-webhook qoder-channel-preimage" ;;
            pi) required="$required pi-agent-dir pi-executable pi-desktop pi-webhook pi-channel-preimage runtime-root" ;;
            cursor) required="$required scope-root client-executable cursor-desktop cursor-webhook cursor-channel-preimage cursor-seed-enabled" ;;
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
    resolved_config=$("$_CONFIG_HELPER" config path) || return 1
    [ "$resolved_config" = "$_FROZEN_GLOBAL_CONFIG" ] || { echo "Frozen notification config authority changed." >&2; return 1; }
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

# The confirmed registry belongs to the parent, never to parallel client
# workers. Keep the helper's bounded, sanitized diagnostics available on a
# refusal; an activation-only retry cannot repair registry preparation.
apply_confirmed_state_migration() {
    local status=0
    _STATE_MIGRATION_COMPLETED=false
    selector_collect migration-apply --intent-file "$_SELECTOR_INTENT" \
        2>"$_CONFIG_STAGE/state-migration.stderr" || status=$?
    cat "$_CONFIG_STAGE/state-migration.stderr" >&2
    [ "$status" -eq 0 ] || return "$status"
    if [ "$_FROZEN_STATE_MIGRATION_REQUIRED" = true ]; then
        selector_exact migrated migration-apply || return 1
        _STATE_MIGRATION_COMPLETED=true
    else
        selector_exact unchanged migration-apply || return 1
    fi
}

# Normalize the existing request once, then relay the same argv to summary and
# checkpoint. JSON is a formatting flag for legacy explicit configure only.
selector_scope_args() {
    _SCOPE_ARGS=()
    local i=0
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        case "${CONFIGURE_ARGS[$i]}" in
            --codex-home|--scope-root|--client-executable|--pi-agent-dir|--pi-executable)
                _SCOPE_ARGS+=("${CONFIGURE_ARGS[$i]}" "${CONFIGURE_ARGS[$((i + 1))]}") ;;
        esac
        i=$((i + 1))
    done
}

selector_effect_args() {
    _EFFECT_ARGS=(--products "$_PRODUCT_CSV")
    if [ -n "$LEGACY_PRODUCT" ] || [[ ",$_PRODUCT_CSV," = *,cursor,* ]]; then
        case "$AGENT_NOTIFY_REQUEST" in explicit) _EFFECT_ARGS+=(--agent-notify) ;; skip) _EFFECT_ARGS+=(--skip-agent-notify) ;; esac
        local i=0
        while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
            case "${CONFIGURE_ARGS[$i]}" in
                --codex-home|--scope-root|--client-executable|--pi-agent-dir|--pi-executable) i=$((i + 2)); continue ;;
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
            --presentation-capabilities) printf '%s\n' bootstrap-presentation-v1; return 0 ;;
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
            echo "No controlling TTY. Specify complete explicit product/route input." >&2; return 1
        fi
        _PROMPT_OPEN=true
    fi
    abort_if_wsl_environment 3>&-
    detect_platform 3>&-
    require_installer_runtime 3>&- || return 1
    resolve_bootstrap_release 3>&- || return 1
    run_setup_stage "Preparing verified installer" stage_config_helper 3>&- >&2 || { report_setup_failure 1; return 1; }
    local state_migration_capability
    state_migration_capability=$("$_CONFIG_HELPER" setup-products migration-capabilities </dev/null 2>/dev/null) || state_migration_capability=""
    _STATE_MIGRATION_CAPABLE=false
    if [ "$state_migration_capability" = state-migration-v1 ]; then _STATE_MIGRATION_CAPABLE=true; fi
    export BOOTSTRAP_RELEASE_TAG="$BOOTSTRAP_TAG" BOOTSTRAP_RELEASE_COMMIT="$BOOTSTRAP_COMMIT"
    export BOOTSTRAP_SOURCE_COMMIT="$BOOTSTRAP_BUNDLE_COMMIT" BOOTSTRAP_SOURCE_REF="${BOOTSTRAP_SOURCE_REF:-}"
    export BOOTSTRAP_RELEASE_CHANNEL="${BOOTSTRAP_RELEASE_CHANNEL:-}"
    # A pruned Claude version can still serve other registered integrations.
    # Repair exact missing managed bytes before the normal read-only plan;
    # its generation must be captured after the repair, never patched in place.
    local cache_repair_capability
    cache_repair_capability=$("$_CONFIG_HELPER" setup-products repair-capabilities </dev/null 2>/dev/null) || cache_repair_capability=""
    if [ "$cache_repair_capability" = cache-repair-v1 ]; then
        local -a cache_repair_args=(--bootstrap)
        if [ "$_INTERACTIVE_INTENT" != true ]; then
            cache_repair_args+=(--yes)
        fi
        selector_collect repair-cache ${cache_repair_args[@]+"${cache_repair_args[@]}"} ${UI_ARGS[@]+"${UI_ARGS[@]}"} || return $?
        [ -s "$_SELECTOR_RESULT" ] || { close_prompt; return 0; }
        # Legacy unattended setup does not otherwise require cmp. Validate the
        # fixed six-byte reply with builtins and the existing size primitive.
        local cache_repair_reply cache_repair_size
        IFS= read -r cache_repair_reply <"$_SELECTOR_RESULT" || return 1
        cache_repair_size=$(LC_ALL=C wc -c <"$_SELECTOR_RESULT") || return 1
        [ "$cache_repair_reply" = ready ] && [ "$cache_repair_size" -eq 6 ] || { echo "Invalid cache repair result bytes." >&2; return 1; }
    fi
    if [ "$_INTERACTIVE_INTENT" = true ] || [[ "$original_product" = bundle:* ]] || [ "$original_product" = gemini ] || [ "$original_product" = qoder ] || [ "$original_product" = pi ]; then
        selector_collect capabilities || return $?
        if printf '%s\n' 'setup-products-v1 claude codex opencode gemini qoder cursor pi' | cmp -s - "$_SELECTOR_RESULT"; then
            _PI_SETUP_CAPABLE=true
        elif printf '%s\n' 'setup-products-v1 claude codex opencode gemini qoder pi' | cmp -s - "$_SELECTOR_RESULT"; then
            _PI_SETUP_CAPABLE=true
        elif printf '%s\n' 'setup-products-v1 claude codex opencode gemini qoder cursor' | cmp -s - "$_SELECTOR_RESULT"; then
            :
        else
            selector_exact 'setup-products-v1 claude codex opencode gemini qoder' capabilities || return 1
        fi
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
    for selected in claude codex opencode gemini qoder cursor pi; do
        case " ${SELECTED_PRODUCTS[*]} " in *" $selected "*) _PRODUCT_CSV=${_PRODUCT_CSV:+$_PRODUCT_CSV,}$selected ;; esac
    done
    case ",$_PRODUCT_CSV," in *,pi,*) [ "$_PI_SETUP_CAPABLE" = true ] || { echo "The matching setup helper does not support Pi." >&2; return 1; } ;; esac
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
    if [ "$_INTERACTIVE_INTENT" != true ]; then
        case ",$_PRODUCT_CSV," in
            *,opencode,*|*,gemini,*|*,qoder,*|*,cursor,*|*,pi,*)
                _SELECTOR_INTENT="$_CONFIG_STAGE/selector-intent.json"
                selector_scope_args
                selector_collect prepare "${_EFFECT_ARGS[@]}" ${_SCOPE_ARGS[@]+"${_SCOPE_ARGS[@]}"} --intent-file "$_SELECTOR_INTENT" || return $?
                selector_exact prepared prepare || return 1 ;;
        esac
    fi
    if [ -z "$_SELECTOR_INTENT" ] && { [ "$_STATE_MIGRATION_CAPABLE" = true ] || parallel_install_available; }; then
        _SELECTOR_INTENT="$_CONFIG_STAGE/selector-intent.json"
        selector_scope_args
        selector_collect prepare "${_EFFECT_ARGS[@]}" ${_SCOPE_ARGS[@]+"${_SCOPE_ARGS[@]}"} --intent-file "$_SELECTOR_INTENT" || return $?
        selector_exact prepared prepare || return 1
    fi
    close_prompt
    if [ -n "$_SELECTOR_INTENT" ]; then load_frozen_dispatch || return $?; fi
    for selected in "${SELECTED_PRODUCTS[@]}"; do
        PRODUCT=$selected
        check_prerequisites || return $?
        _PROGRESS_IDS+=("$selected"); _PROGRESS_PHASES+=(pending); _PROGRESS_OUTCOMES+=(not_started)
    done
    PRODUCT=$original_product
    if [ -z "${BOOTSTRAP_SUMMARY_FILE:-}" ]; then
        if ! progress_rich_available || ! progress_helper_available; then print_header; fi
    fi
    # Keep a sourced invocation from leaking its owned summary into a retry.
    local BOOTSTRAP_SUMMARY_FILE=${BOOTSTRAP_SUMMARY_FILE:-}
    local own_summary=false
    if [ -z "${BOOTSTRAP_SUMMARY_FILE:-}" ]; then
        BOOTSTRAP_SUMMARY_FILE="$_CONFIG_STAGE/summary.txt"
        export BOOTSTRAP_SUMMARY_FILE
        own_summary=true
    fi
    if [ -n "$LEGACY_PRODUCT" ]; then
        PRODUCT=$LEGACY_PRODUCT
        run_setup_stage "Checking existing installation" stage_historical_baselines || { report_setup_failure 1; return 1; }
        run_setup_stage "Checking shared settings" config_preflight || { report_setup_failure 1; return 1; }
    fi
    if [ -n "$_SELECTOR_INTENT" ]; then
        selector_collect preflight --intent-file "$_SELECTOR_INTENT" "${_EFFECT_ARGS[@]}" || return $?
        [ ! -s "$_SELECTOR_RESULT" ] || { echo "Invalid preflight stdout." >&2; return 1; }
    fi
    if [ "$_STATE_MIGRATION_CAPABLE" = true ]; then
        local migration_label="Checking UAP registry"
        [ "$_FROZEN_STATE_MIGRATION_REQUIRED" != true ] || migration_label="Upgrading UAP registry"
        if ! run_setup_stage "$migration_label" apply_confirmed_state_migration; then
            _SETUP_FAILURE_REASON=state_migration_failed
            _KEEP_CONFIG_STAGE=true
            report_setup_failure 1
            return 1
        fi
        if [ "$_STATE_MIGRATION_COMPLETED" = true ]; then printf 'UAP registry upgraded; backup saved.\n'; fi
    fi
    # The verified capability guarantees finite worker phases and subset wizard
    # projection. Older releases retain their original synchronous contract.
    if parallel_install_available; then
        install_products_parallel || status=$?
    elif [ -n "$LEGACY_PRODUCT" ]; then
        PRODUCT=$LEGACY_PRODUCT
        install_legacy_products || status=$?
    fi
    if [ "$status" -eq 0 ] && [ "$_PARALLEL_ACTIVE" != true ]; then
        for selected in opencode gemini qoder; do
            case ",$_PRODUCT_CSV," in *",$selected,"*) ;; *) continue ;; esac
            PRODUCT=$selected
            progress_phase "$selected" preparation state_requires_inspection
            case "$selected" in opencode) install_opencode || status=$? ;; gemini) install_gemini || status=$? ;; qoder) install_qoder || status=$? ;; esac
            [ "$status" -eq 0 ] || break
            progress_phase "$selected" complete completed
        done

    fi
    # Cursor uses its existing qualified single-target caller after every
    # sibling worker and portable follow-up has completed. Parallel selection
    # must never silently drop this sequential target.
    if [ "$status" -eq 0 ] && [[ ",$_PRODUCT_CSV," = *,cursor,* ]]; then
        if [ "$AGENT_NOTIFY_REQUEST" != skip ]; then
            run_setup_stage "Preparing Cursor notification runtime" prepare_cursor_runtime || { report_setup_failure 1; return 1; }
            PRODUCT=cursor; CONFIGURE_BINARY=$_CONFIG_HELPER; PLUGIN_ROOT=""
            progress_phase cursor portable state_requires_inspection
            run_setup_stage "Configuring Cursor notifications" setup_agent_notify_wizard || status=$?
            if [ "$status" -eq 0 ]; then
                local cursor_outcome=completed cursor_i=0
                while [ "$cursor_i" -lt "${#_PROGRESS_IDS[@]}" ]; do
                    if [ "${_PROGRESS_IDS[$cursor_i]}" = cursor ] && [ "${_PROGRESS_OUTCOMES[$cursor_i]}" = skipped ]; then cursor_outcome=skipped; fi
                    cursor_i=$((cursor_i + 1))
                done
                progress_phase cursor complete "$cursor_outcome"
            fi
        else
            progress_phase cursor complete skipped
        fi
    fi
    # Pi refreshes its own immutable intent only after every sibling, including
    # Cursor, has finished using the original confirmed parent.
    if [ "$status" -eq 0 ]; then
        case ",$_PRODUCT_CSV," in
            *,pi,*)
                PRODUCT=pi
                run_setup_stage "Installing Pi notifications" install_pi || status=$?
                if [ "${_PI_CONFIRM_CANCELLED:-false}" = true ]; then
                    echo "Pi integration canceled; prepared runtime retained."
                    return 0
                fi
                [ "$status" -ne 0 ] || progress_phase pi complete completed ;;
        esac
    fi
    if [ "$status" -ne 0 ]; then
        report_setup_failure "$status"
        _KEEP_CONFIG_STAGE=true
    else
        report_progress 0
        if [ "$_DIAGNOSTIC_NOTED" = true ]; then
            if [ -n "$_DIAGNOSTIC_LOG" ] && [ "$_DIAGNOSTIC_FAILED" != true ] && [ -f "$_DIAGNOSTIC_LOG" ] && [ ! -L "$_DIAGNOSTIC_LOG" ]; then
                [ "${BOOTSTRAP_VERBOSE:-0}" != 1 ] || printf 'Maintenance: additional installer details saved in Diagnostics.\n' >&2
            else
                printf 'Maintenance: installer reported additional details.\n' >&2
            fi
        fi
        if [ "$own_summary" != true ] && { [ "$_DIAGNOSTIC_NOTED" = true ] || [ "$_DIAGNOSTIC_FAILED" = true ]; }; then
            report_diagnostics
        fi
    fi
    if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ] && [ -n "$_DIAGNOSTIC_LOG" ]; then
        bootstrap_diagnostics view "$_DIAGNOSTIC_LOG" >&2 || true
    fi
    if [ "$own_summary" = true ]; then
        if [ "$status" -eq 0 ]; then
            if [ "$_FINAL_BOARD_RENDERED" != true ]; then printf '\nInstallation complete\n\n'; else printf '\n'; fi
            [ ! -s "$BOOTSTRAP_SUMMARY_FILE" ] || cat "$BOOTSTRAP_SUMMARY_FILE"
            if [ "$_DIAGNOSTIC_NOTED" = true ] || [ "$_DIAGNOSTIC_FAILED" = true ]; then report_diagnostics; fi
        fi
        unset BOOTSTRAP_SUMMARY_FILE
    fi
    if [ "$status" -eq 0 ]; then bootstrap_promotion; fi
    return "$status"
}

promotion_text() {
    local staged=""
    if progress_helper_available && [ "$_PROGRESS_SESSION_SUPPORTED" = true ]; then
        staged=$(mktemp "${TMPDIR:-/tmp}/bootstrap-promotion-XXXXXX") || staged=""
        if [ -n "$staged" ]; then
            if "$_CONFIG_HELPER" installer-progress --promotion </dev/null > "$staged" 2>/dev/null; then
                cat "$staged"
                rm -f "$staged"
                return 0
            fi
            rm -f "$staged"
        fi
    fi
    printf '\n----------------------------------------\nBoost your productivity\n  Check out Agent Teams AI, a desktop app for AI agent teams\n  with Claude, Codex and more, from the creator of this plugin:\n  https://github.com/777genius/agent-teams-ai\n----------------------------------------\n'
}

# The loader owns a private staging directory and mode-600 handoff leaf.
bootstrap_promotion_file_safe() {
    [ -f "$1" ] && [ ! -L "$1" ] && [ ! -s "$1" ] && [ -O "$1" ] || return 1
    local engine
    engine=$(installer_runtime) || return 1
    if [ "$engine" = python3 ]; then
        python3 -I - "$1" <<'PY_PROMOTION_PRIVATE'
import os, stat, sys
try:
    path=sys.argv[1]
    leaf=os.lstat(path); parent=os.lstat(os.path.dirname(path))
    assert stat.S_ISREG(leaf.st_mode) and stat.S_ISDIR(parent.st_mode)
    assert leaf.st_size==0 and leaf.st_nlink==1
    if os.name!='nt':
        assert leaf.st_uid==parent.st_uid==os.getuid()
        assert stat.S_IMODE(leaf.st_mode)==0o600 and stat.S_IMODE(parent.st_mode)==0o700
except Exception: sys.exit(1)
PY_PROMOTION_PRIVATE
    else
        run_isolated_node - "$1" <<'JS_PROMOTION_PRIVATE'
const fs=require('fs'),path=require('path');
try {
 const leaf=fs.lstatSync(process.argv[2]),parent=fs.lstatSync(path.dirname(process.argv[2]));
 if(!leaf.isFile() || !parent.isDirectory() || leaf.size!==0 || leaf.nlink!==1) process.exit(1);
 if(process.platform!=='win32' && (leaf.uid!==process.getuid() || parent.uid!==process.getuid() || (leaf.mode&0o777)!==0o600 || (parent.mode&0o777)!==0o700)) process.exit(1);
} catch {process.exit(1);}
JS_PROMOTION_PRIVATE
    fi
}

bootstrap_promotion() {
    if [ -n "${BOOTSTRAP_PROMOTION_FILE:-}" ]; then
        if bootstrap_promotion_file_safe "$BOOTSTRAP_PROMOTION_FILE"; then
            promotion_text > "$BOOTSTRAP_PROMOTION_FILE" || true
        fi
        return 0
    fi
    promotion_text || true
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
    # Cursor authorities share the parsed request, but not this legacy policy
    # command. Keep other normalized key/value pairs intact, including values
    # that happen to look like Cursor option names.
    local i=0
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        case "${CONFIGURE_ARGS[$i]}" in
            --scope-root|--client-executable|--pi-agent-dir|--pi-executable) i=$((i + 2)); continue ;;
            --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted|--codex-home)
                configure_cmd+=("${CONFIGURE_ARGS[$i]}" "${CONFIGURE_ARGS[$((i + 1))]}")
                i=$((i + 2)); continue ;;
        esac
        configure_cmd+=("${CONFIGURE_ARGS[$i]}"); i=$((i + 1))
    done
    if [ "${1:-}" = portable ]; then
        configure_cmd+=(--policy-only --preserve-enabled)
    fi
    if ! bootstrap_external "$CONFIGURE_BINARY" "${configure_cmd[@]}"; then
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
    if [ "$PRODUCT" = cursor ] || [ "$PRODUCT" = pi ]; then
        echo "$PRODUCT setup is incomplete; inspect the selected profile and owned state before retrying." >&2
    else
        echo -e "${YELLOW}  Plugin/hooks registration completed. Inspect the selected clients and owned registration before resuming the portable phase with the verified package for this release.${NC}" >&2
    fi
    [ -z "${BOOTSTRAP_TAG:-}" ] || printf '  Release: %s\n' "$BOOTSTRAP_TAG" >&2
    if [ "$AGENT_NOTIFY_REQUEST" = explicit ] || [ -n "$_SELECTOR_INTENT" ]; then
        return 1
    fi
    return 0
}

# The original checkpoint covers runtime preparation and Cursor integration.
# Preserve its shown absent-policy seed; the qualified child rechecks relevant
# original decisions before binding the resulting generation for CAS.
prepare_cursor_runtime() {
    _CURSOR_RUNTIME_PREPARED=false
    local runtime
    [ -n "$_SELECTOR_INTENT" ] && [ -n "$_FROZEN_CONTROL" ] || return 1
    if runtime=$("$_CONFIG_HELPER" config installer runtime-root "$_FROZEN_CONTROL"); then
        [ -z "$_FROZEN_RUNTIME" ] || [ "$runtime" = "$_FROZEN_RUNTIME" ] || return 1
        _FROZEN_RUNTIME=$runtime
    fi
    local -a seed_args=()
    [ "$_FROZEN_CURSOR_SEED" != true ] || seed_args=(--seed-enabled-if-absent)
    prepare_managed_runtime ${seed_args[@]+"${seed_args[@]}"} || return $?
    _CURSOR_RUNTIME_PREPARED=$_MANAGED_RUNTIME_PREPARED
}

# Shared byte acquisition uses the existing managed-runtime writer. Native
# products keep their own immutable integration confirmation after this step.
prepare_managed_runtime() {
    _MANAGED_RUNTIME_PREPARED=false
    local runtime os arch primary native base target="$_FROZEN_RUNTIME/bin"
    [ -n "$_SELECTOR_INTENT" ] && [ -n "$_FROZEN_CONTROL" ] && [ -n "$_FROZEN_RUNTIME" ] || return 1
    read -r os arch < <(bootstrap_release_os_arch) || return 1
    primary="claude-notifications-$os-$arch"
    [ "$os" != windows ] || primary="$primary.exe"
    if runtime=$("$_CONFIG_HELPER" config installer runtime-root "$_FROZEN_CONTROL"); then
        [ "$runtime" = "$_FROZEN_RUNTIME" ] || return 1
        if cmp -s "$_CONFIG_HELPER" "$target/$primary"; then return 0; fi
    else
        if [ -e "$_FROZEN_CONTROL/ownership.json" ] || [ -L "$_FROZEN_CONTROL/ownership.json" ] ||
           [ -e "$_FROZEN_RUNTIME" ] || [ -L "$_FROZEN_RUNTIME" ]; then
            echo "Existing runtime requires inspection; retained files were not replaced." >&2
            return 1
        fi
    fi
    base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
    case "$os" in
        darwin)
            # A preceding selected observer may already have staged this exact
            # signed app. The common Go writer revalidates native custody.
            if [ ! -d "$_CONFIG_STAGE/ClaudeNotifier.app" ]; then
                fetch_bootstrap_file "$base/ClaudeNotifier.app.zip" "$_CONFIG_STAGE/ClaudeNotifier.app.zip" || return 1
                verify_bootstrap_checksum "$_CONFIG_STAGE" ClaudeNotifier.app.zip || return 1
                unzip -q "$_CONFIG_STAGE/ClaudeNotifier.app.zip" -d "$_CONFIG_STAGE" || return 1
            fi ;;
        windows)
            native="claude-notifications-windows-$arch-focus.exe"
            fetch_bootstrap_file "$base/$native" "$_CONFIG_STAGE/$native" || return 1
            verify_bootstrap_checksum "$_CONFIG_STAGE" "$native" || return 1 ;;
    esac
    # Use the existing verified Go writer with the exact frozen control root.
    # It owns private directory creation, native custody and saved-policy CAS.
    "$_CONFIG_HELPER" internal-install-runtime --stage "$_CONFIG_STAGE" --target "$target" \
        --entry "$primary" --control-root "$_FROZEN_CONTROL" "$@" || return 1
    runtime=$("$_CONFIG_HELPER" config installer runtime-root "$_FROZEN_CONTROL") || return 1
    [ "$runtime" = "$_FROZEN_RUNTIME" ] && cmp -s "$_CONFIG_HELPER" "$target/$primary" || return 1
    _MANAGED_RUNTIME_PREPARED=true
}

install_pi() {
    [ -n "$_SELECTOR_INTENT" ] && [ -n "$_FROZEN_PI" ] && [ -n "$_PI_EXEC" ] || return 1
    _PI_CONFIRM_CANCELLED=false
    progress_phase pi preparation state_requires_inspection
    # This records prepared bytes without pretending a Claude installation
    # exists. The common composition retires it after the real Pi consumer commits.
    prepare_managed_runtime --consumer pi-runtime-preparation || return $?
    # Other selected products may have advanced the shared runtime too. Use a
    # Pi-only immutable projection after those stages, without replaying them.
    if [ "$_MANAGED_RUNTIME_PREPARED" = true ] || [ "$_PRODUCT_CSV" != pi ]; then
        refresh_pi_integration_intent || return $?
        [ "$_PI_CONFIRM_CANCELLED" != true ] || return 0
    fi
    CONFIGURE_BINARY=$_CONFIG_HELPER
    PLUGIN_ROOT=""
    progress_phase pi portable state_requires_inspection
    setup_agent_notify_wizard || return $?
    local report
    report=$(mktemp "${TMPDIR:-/tmp}/bootstrap-pi-summary-XXXXXX") || return 1
    {
        echo "  Pi - installed; restart Pi to load the native extension."
        print_delivery_note
    } > "$report"
    publish_setup_summary "$report"
    local status=$?
    rm -f "$report"
    return "$status"
}

refresh_pi_integration_intent() {
    local control=$_FROZEN_CONTROL runtime=$_FROZEN_RUNTIME profile=$_FROZEN_PI client=$_PI_EXEC config=$_FROZEN_GLOBAL_CONFIG
    local original_csv=$_PRODUCT_CSV channel operation=prepare expected_channels="${_FROZEN_PI_CHANNELS[*]}"
    local -a original_products=("${SELECTED_PRODUCTS[@]}") effects=(--products pi) scopes=(--pi-agent-dir "$profile" --pi-executable "$client")
    for channel in "${_FROZEN_PI_CHANNELS[@]}"; do
        case "$channel" in --pi-desktop=*|--pi-webhook=*) effects+=("--${channel#--pi-}") ;; esac
    done
    _SELECTOR_INTENT="$_CONFIG_STAGE/selector-pi-runtime-intent.json"
    if [ "$_INTERACTIVE_INTENT" = true ]; then
        echo "Review Pi integration in the prepared shared runtime."
        exec 3<>/dev/tty || return 1
        _PROMPT_OPEN=true
        operation=confirm
    fi
    selector_collect "$operation" "${effects[@]}" "${scopes[@]}" --intent-file "$_SELECTOR_INTENT" ${UI_ARGS[@]+"${UI_ARGS[@]}"} || { close_prompt; return 1; }
    if [ "$operation" = confirm ] && [ ! -s "$_SELECTOR_RESULT" ]; then
        close_prompt
        _PI_CONFIRM_CANCELLED=true
        return 0
    fi
    if [ "$operation" = confirm ]; then selector_exact approved confirm; else selector_exact prepared prepare; fi || { close_prompt; return 1; }
    close_prompt
    _PRODUCT_CSV=pi; SELECTED_PRODUCTS=(pi)
    local status=0
    load_frozen_dispatch || status=$?
    _PRODUCT_CSV=$original_csv; SELECTED_PRODUCTS=("${original_products[@]}")
    [ "$status" -eq 0 ] || return "$status"
    [ "$_FROZEN_CONTROL" = "$control" ] && [ "$_FROZEN_RUNTIME" = "$runtime" ] &&
        [ "$_FROZEN_PI" = "$profile" ] && [ "$_PI_EXEC" = "$client" ] &&
        [ "$_FROZEN_GLOBAL_CONFIG" = "$config" ] && [ "${_FROZEN_PI_CHANNELS[*]}" = "$expected_channels" ] || return 1
    "$_CONFIG_HELPER" setup-products preflight --intent-file "$_SELECTOR_INTENT" "${effects[@]}" </dev/null
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
        if [ "$PRODUCT" = cursor ] || [ "$PRODUCT" = pi ]; then
            echo "$PRODUCT setup is incomplete; the matching portable setup caller is required." >&2
        else
            echo "agent-notify setup skipped; installed binary predates portable setup. Plugin/hooks install succeeded. Update to a matching release before enabling MCP." >&2
        fi
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
        cursor) agents=cursor ;;
        pi) agents=pi ;;
        *) echo "invalid product for wizard: $PRODUCT" >&2; return 1 ;;
    esac
    while [ "$i" -lt "${#CONFIGURE_ARGS[@]}" ]; do
        if [ "${CONFIGURE_ARGS[$i]}" = "--codex-home" ]; then
            i=$((i + 1))
            wizard_codex_home="${CONFIGURE_ARGS[$i]}"
        fi
        i=$((i + 1))
    done
    if { [ "$PRODUCT" = codex ] || [ "$PRODUCT" = both ]; } && [ -z "$wizard_codex_home" ]; then
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
    if [ "$PRODUCT" = pi ]; then
        [ -n "$_SELECTOR_INTENT" ] && [ -n "$_FROZEN_PI" ] && [ -n "$_PI_EXEC" ] || return 1
    elif [ "$PRODUCT" = cursor ]; then
        [ -n "$_SELECTOR_INTENT" ] && [ -n "$_FROZEN_SCOPE_ROOT" ] && [ -n "$_FROZEN_CLIENT_EXEC" ] || return 1
        # The approved caller derives its observer from the installed primary;
        # the selected vendor executable is only client authority.
    elif [ -n "$_SELECTOR_INTENT" ]; then
        [ "$_PARALLEL_POLICY_READY" = true ] || configure_agent_policy portable || return 1
        [ "$PRODUCT" = codex ] || claude_exec=$_CLAUDE_EXEC
        [ "$PRODUCT" = claude ] || codex_exec=$_CODEX_EXEC
    else
        [ "$_PARALLEL_POLICY_READY" = true ] || configure_agent_policy portable || return 1
        if [ "$PRODUCT" != codex ]; then claude_exec=$(bootstrap_abs_command "$_CLAUDE_EXEC") || true; fi
        if [ "$PRODUCT" != claude ]; then codex_exec=$(bootstrap_abs_command "$_CODEX_EXEC") || true; fi
    fi
    set -- setup-notifications wizard --action install --install-or-update --agents "$agents" --hooks false --agent-notify true --yes \
        --package "$package_root"
    case "$PRODUCT" in cursor|pi) ;; *) set -- "$@" --plugin-root "$plugin_root" ;; esac
    if [ -n "$_SELECTOR_INTENT" ]; then
        if [ "$PRODUCT" != cursor ] && [ -n "$_PARALLEL_READY_CLIENTS" ]; then
            set -- "$@" --bootstrap-ready-clients "$_PARALLEL_READY_CLIENTS" --bootstrap-progress-file "$_PROGRESS_JOURNAL"
        fi
        set -- "$@" --bootstrap-intent-file "$_SELECTOR_INTENT" --control-root "$_FROZEN_CONTROL" --global-config "$_FROZEN_GLOBAL_CONFIG"
        [ -z "$_FROZEN_RUNTIME" ] || set -- "$@" --runtime-root "$_FROZEN_RUNTIME"
        if [ "$PRODUCT" != cursor ] && [ "$PRODUCT" != pi ]; then
            [ -z "$_FROZEN_CODEX_MCP" ] || set -- "$@" --mcp-config "$_FROZEN_CODEX_MCP"
        fi
    else
        [ "$AGENT_NOTIFY_REQUEST" != auto ] || set -- "$@" --preserve-existing-units
    fi
    if [ "$PRODUCT" = pi ]; then
        set -- "$@" --pi-agent-dir "$_FROZEN_PI" --pi-executable "$_PI_EXEC" "${_FROZEN_PI_CHANNELS[@]}"
    elif [ "$PRODUCT" != cursor ]; then
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
    else
        set -- "$@" --scope-root "$_FROZEN_SCOPE_ROOT" --client-executable "$_FROZEN_CLIENT_EXEC"
    fi
    local wizard_result wizard_status=0 structured_result=false result_capability
    result_capability=$("$CONFIGURE_BINARY" installer-progress --result-capabilities </dev/null 2>/dev/null) || result_capability=""
    if [ "$result_capability" = installer-progress-result-v1 ]; then
        structured_result=true
        set -- "$@" --json
    fi
    if [ "$_PARALLEL_ACTIVE" = true ]; then
        wizard_result=$(mktemp "$_CONFIG_STAGE/parallel/wizard-result-XXXXXX") || return 1
        _PARALLEL_HELPER_CAPTURE=$wizard_result
    else
        wizard_result=$(mktemp "${TMPDIR:-/tmp}/bootstrap-wizard-XXXXXX") || return 1
    fi
    bootstrap_external "$CONFIGURE_BINARY" "$@" > "$wizard_result" || wizard_status=$?
    _PARALLEL_HELPER_CAPTURE=""
    # JSON remains diagnostic input. Only the verified helper's finite typed
    # projection may update observer rows; arbitrary error bytes are never facts.
    if [ "$structured_result" = true ]; then
        apply_wizard_progress "$wizard_result" || true
    fi
    if [ "$wizard_status" -ne 0 ]; then
        if [ "$structured_result" = true ] && [ "$_SETUP_RECOVERY" = activation_available ]; then
            local recovery_text
            recovery_text=$("$CONFIGURE_BINARY" installer-progress --result "$wizard_result" --recovery) || recovery_text=""
            if [ -n "$recovery_text" ] && [ "${#recovery_text}" -le 32768 ]; then
                # Keep the validated command ahead of potentially large JSON so
                # bounded diagnostic capture cannot discard the only next step.
                printf '%s\n' "$recovery_text"
            else
                _SETUP_RECOVERY=unavailable
            fi
        fi
        cat "$wizard_result"
        rm -f "$wizard_result"
        # Legacy helpers may still refer to the original ZIP. Current helpers
        # validate recovery from their acquired package before advertising it.
        if [ "$structured_result" != true ] && [ -n "$_PORTABLE_STAGE" ] && [[ "$package_root" = "$_PORTABLE_STAGE/"* ]]; then
            _KEEP_PORTABLE_STAGE=true
        fi
        return "$wizard_status"
    fi
    cat "$wizard_result"
    # Keep failure output human and runnable; only the post-commit read uses JSON.
    set -- setup-notifications wizard --action inspect --agents "$agents" --json
    if [ "$PRODUCT" = pi ]; then
        set -- "$@" --pi-agent-dir "$_FROZEN_PI" --pi-executable "$_PI_EXEC"
    elif [ "$PRODUCT" = cursor ]; then
        set -- "$@" --scope-root "$_FROZEN_SCOPE_ROOT" --client-executable "$_FROZEN_CLIENT_EXEC"
    else
        set -- "$@" --codex-home "$wizard_codex_home" --claude-config "$CLAUDE_HOME"
        [ "$PRODUCT" = codex ] || set -- "$@" --claude-mcp-config "${_FROZEN_CLAUDE_MCP:-${CLAUDE_CONFIG_DIR:-$INSTALLER_HOME}/.claude.json}"
    fi
    if [ -n "$_SELECTOR_INTENT" ]; then
        set -- "$@" --control-root "$_FROZEN_CONTROL" --global-config "$_FROZEN_GLOBAL_CONFIG"
        [ -z "$_FROZEN_RUNTIME" ] || set -- "$@" --runtime-root "$_FROZEN_RUNTIME"
        if [ "$PRODUCT" != cursor ] && [ "$PRODUCT" != pi ]; then
            [ -z "$_FROZEN_CODEX_MCP" ] || set -- "$@" --mcp-config "$_FROZEN_CODEX_MCP"
        fi
    fi
    [ "$_PARALLEL_ACTIVE" != true ] || _PARALLEL_HELPER_CAPTURE=$wizard_result
    if ! bootstrap_external "$CONFIGURE_BINARY" "$@" > "$wizard_result" 2>/dev/null; then
        : > "$wizard_result"
        echo "Warning: installation finished, but agent notification tool status could not be checked." >&2
    fi
    _PARALLEL_HELPER_CAPTURE=""
    CLAUDE_AGENT_NOTIFY_STATUS=$(wizard_tool_status "$wizard_result" claude "$AGENT_NOTIFY_REQUEST")
    CODEX_AGENT_NOTIFY_STATUS=$(wizard_tool_status "$wizard_result" codex "$AGENT_NOTIFY_REQUEST")
    rm -f "$wizard_result"
    return 0
}

apply_wizard_progress() {
    local projection status=0 size kind phase outcome extra i j seen=' '
    local reason=setup_failed recovery=unavailable
    local -a ids=() phases=() outcomes=()
    projection=$(mktemp "${TMPDIR:-/tmp}/bootstrap-result-XXXXXX") || return 1
    "$CONFIGURE_BINARY" installer-progress --result "$1" > "$projection" || status=$?
    size=$(LC_ALL=C wc -c < "$projection") || status=1
    if [ "$status" -ne 0 ] || [ "$size" -gt 4096 ]; then rm -f "$projection"; return 1; fi
    while IFS=$'\t' read -r kind phase outcome extra; do
        if [ -n "$extra" ] || [ -z "$kind" ] || [ -z "$outcome" ]; then status=1; break; fi
        case "$kind" in
            reason)
                [ "$phase" = category ] || { status=1; break; }
                case "$outcome" in probe_timeout|canceled|deadline|activation_failed|setup_failed) reason=$outcome ;; *) status=1; break ;; esac ;;
            next)
                [ "$phase" = category ] || { status=1; break; }
                case "$outcome" in activation_available|unavailable) recovery=$outcome ;; *) status=1; break ;; esac ;;
            claude|codex|opencode|gemini|qoder|pi|cursor)
                if [ "$kind" = pi ]; then
                    [ "$phase" = registration ] || { status=1; break; }
                else
                    [ "$phase" = MCP ] || { status=1; break; }
                fi
                case "$outcome" in activation_failed|activation_not_attempted|activation_timeout|activation_canceled|activation_deadline|completed|skipped|registration_committed|state_requires_inspection) ;; *) status=1; break ;; esac
                case "$seen" in *" $kind "*) status=1; break ;; esac
                seen="$seen$kind "
                j=0
                while [ "$j" -lt "${#_PROGRESS_IDS[@]}" ] && [ "${_PROGRESS_IDS[$j]}" != "$kind" ]; do j=$((j + 1)); done
                [ "$j" -lt "${#_PROGRESS_IDS[@]}" ] || { status=1; break; }
                ids+=("$kind"); phases+=("$phase"); outcomes+=("$outcome") ;;
            *) status=1; break ;;
        esac
    done < "$projection"
    rm -f "$projection"
    [ "$status" -eq 0 ] && [ "${#ids[@]}" -ne 0 ] || return 1
    i=0
    while [ "$i" -lt "${#ids[@]}" ]; do
        progress_phase "${ids[$i]}" "${phases[$i]}" "${outcomes[$i]}"
        i=$((i + 1))
    done
    _SETUP_FAILURE_REASON=$reason; _SETUP_RECOVERY=$recovery
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
