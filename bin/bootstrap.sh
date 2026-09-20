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
# CLAUDE_HOME is a legacy fallback; default to ~/.claude
CLAUDE_HOME="${CLAUDE_CONFIG_DIR:-${CLAUDE_HOME:-$HOME/.claude}}"
if [ -z "$CLAUDE_HOME" ]; then
    CLAUDE_HOME="$HOME/.claude"
fi
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
PRODUCT=""
BOOTSTRAP_TAG=""
BOOTSTRAP_COMMIT=""
_BOOTSTRAP_TMP=""  # temp file path for trap (set -u safe)
CONFIGURE_NOTIFICATIONS=true
AGENT_NOTIFY_REQUEST=auto
CONFIGURE_BINARY=""
CONFIGURE_ARGS=()

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
    echo -e "${YELLOW}For Windows Claude Code, open Git Bash and use the installer at:${NC}" >&2
    echo -e "  https://777genius.github.io/agent-notifications/#install" >&2
    echo "" >&2
    echo -e "${YELLOW}For an intentional WSL install, set CLAUDE_NOTIFICATIONS_ALLOW_WSL=1 on the final bash command.${NC}" >&2
    echo "" >&2
    exit 1
}

# ──────────────────────────────────────────────

check_prerequisites() {
    if [ "${PRODUCT:-claude}" != codex ] && ! command -v claude &>/dev/null; then
        echo -e "${RED}✗ claude CLI not found in PATH${NC}" >&2
        echo "" >&2
        echo -e "${YELLOW}Install Claude Code first:${NC}" >&2
        echo -e "  npm install -g @anthropic-ai/claude-code" >&2
        echo "" >&2
        exit 1
    fi
    if [ "${PRODUCT:-claude}" != claude ] && ! command -v codex &>/dev/null; then
        echo "codex CLI not found in PATH; install Codex first." >&2
        exit 1
    fi
    if [ "${PRODUCT:-claude}" != claude ] && ! command -v tar &>/dev/null; then
        echo "tar is required for the Codex source bundle." >&2
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

    echo -e "${BLUE}Platform:${NC} ${PLATFORM}"
}

# ──────────────────────────────────────────────

is_iterm2_detected() {
    [ "$(uname -s)" = "Darwin" ] || return 1

    [ "${TERM_PROGRAM:-}" = "iTerm.app" ] && return 0
    [ "${__CFBundleIdentifier:-}" = "com.googlecode.iterm2" ] && return 0
    [ -d "/Applications/iTerm.app" ] && return 0
    [ -d "$HOME/Applications/iTerm.app" ] && return 0

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
    claude plugin marketplace list --json </dev/null >"$tmp" 2>/dev/null
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

setup_marketplace() {
    echo ""
    echo -e "${BLUE}📦 Setting up marketplace...${NC}"

    config_preflight || return 1
    local output
    # Try adding marketplace — if already added, update instead
    # </dev/null prevents stdin conflicts when running via `curl | bash`
    if output=$(claude plugin marketplace add "$MARKETPLACE_SOURCE" </dev/null 2>&1); then
        echo -e "${GREEN}✓${NC} Marketplace added"
    else
        if echo "$output" | grep -qi "already"; then
            echo -e "${BLUE}  Marketplace already added, updating...${NC}"
            config_preflight || return 1
            if claude plugin marketplace update "$MARKETPLACE_NAME" </dev/null 2>&1; then
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
            claude plugin marketplace remove "$MARKETPLACE_NAME" </dev/null >/dev/null 2>&1 || true
            config_preflight || return 1
            if output=$(claude plugin marketplace add "$MARKETPLACE_SOURCE" </dev/null 2>&1); then
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
        if output=$(claude plugin update "$PLUGIN_KEY" </dev/null 2>&1); then
            echo -e "${GREEN}✓${NC} Plugin updated"
        else
            echo -e "${YELLOW}  Plugin update failed, will attempt recovery reinstall${NC}"
            echo -e "${YELLOW}  Output: ${output}${NC}"
            update_failed=true
        fi
    else
        clear_plugin_cache || return 1
        config_preflight || return 1
        if output=$(claude plugin install "$PLUGIN_KEY" </dev/null 2>&1); then
            echo -e "${GREEN}✓${NC} Plugin installed"
        else
            if echo "$output" | grep -qi "already installed"; then
                echo -e "${GREEN}✓${NC} Plugin already installed"
            else
                echo -e "${RED}✗ Plugin install failed${NC}" >&2
                echo -e "${YELLOW}Output: ${output}${NC}" >&2
                exit 1
            fi
        fi
    fi

    if [ "$update_failed" = true ] || { [ -n "$expected_version" ] && ! verify_installed_plugin_version "$expected_version"; }; then
        echo -e "${YELLOW}  Installed plugin version does not match marketplace v${expected_version}; reinstalling...${NC}"

        config_preflight || return 1
        claude plugin uninstall "$PLUGIN_KEY" </dev/null >/dev/null 2>&1 || true
        clear_plugin_cache || return 1

        config_preflight || return 1
        if output=$(claude plugin install "$PLUGIN_KEY" </dev/null 2>&1); then
            echo -e "${GREEN}✓${NC} Plugin reinstalled"
        else
            echo -e "${RED}✗ Plugin reinstall failed${NC}" >&2
            echo -e "${YELLOW}Output: ${output}${NC}" >&2
            exit 1
        fi
    fi

    if [ -n "$expected_version" ] && ! verify_installed_plugin_version "$expected_version"; then
        local installed_after=""
        installed_after=$(get_installed_plugin_version)
        echo -e "${RED}✗ Plugin version mismatch after install/update${NC}" >&2
        echo -e "${YELLOW}Expected: v${expected_version}${NC}" >&2
        echo -e "${YELLOW}Installed: v${installed_after:-unknown}${NC}" >&2
        exit 1
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
# This file is auto-generated by bootstrap.sh and is safe to delete after restarting Claude Code.
#
# Behavior:
# - Find current installPath from ~/.claude/plugins/installed_plugins.json
# - Set CLAUDE_PLUGIN_ROOT to that installPath
# - Exec the real hook-wrapper.sh from the current install
#
# IMPORTANT: Must never fail the hook (exit 0 on any error).

CLAUDE_HOME="${CLAUDE_CONFIG_DIR:-${CLAUDE_HOME:-$HOME/.claude}}"
if [ -z "$CLAUDE_HOME" ]; then
  CLAUDE_HOME="$HOME/.claude"
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
        echo -e "${YELLOW}  Try restarting Claude Code and running this script again.${NC}" >&2
        exit 1
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
        echo -e "${YELLOW}  Try: claude plugin install ${PLUGIN_KEY}${NC}" >&2
        exit 1
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
        exit 1
    fi

    install_runtime claude "$_CONFIG_STAGE/install.sh" "$target_dir" --force

}

# ──────────────────────────────────────────────

setup_iterm2_venv() {
    # Only relevant on macOS
    [ "$(uname -s)" = "Darwin" ] || return 0

    is_iterm2_detected || return 0
    config_preflight || return 1

    # Use $HOME/.claude explicitly (not $CLAUDE_HOME) — the Go code resolves
    # the venv path via os.UserHomeDir()/.claude/..., so the venv must be there.
    local VENV_DIR="$HOME/.claude/claude-notifications-go/iterm2-venv"

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

print_success() {
    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN} ✓ Bootstrap Complete!${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo ""
    echo -e "${BOLD}Next steps:${NC}"
    echo -e "  1. ${YELLOW}Restart Claude Code${NC} (exit and reopen)"
    echo -e "  2. Run ${BOLD}/claude-notifications-go:settings${NC} to configure sounds"
    if is_iterm2_detected; then
        echo -e "  3. In ${BOLD}iTerm2${NC}, enable ${BOLD}Settings → General → Magic → Python API${NC}"
    fi
    echo ""
    print_iterm2_python_api_notice
    echo ""
    echo -e "${BLUE}To update later, use the current installer at:${NC}"
    echo -e "  https://777genius.github.io/agent-notifications/#install"
    echo ""
    echo -e "${YELLOW}────────────────────────────────────────────${NC}"
    echo -e "${YELLOW}★${NC} ${BOLD}Boost your productivity${NC}"
    echo -e "  Check out the advanced task manager for Claude"
    echo -e "  with a convenient UI, from the creator of this plugin:"
    echo -e "  ${GREEN}https://github.com/777genius/claude_agent_teams_ui${NC}"
    echo -e "${YELLOW}────────────────────────────────────────────${NC}"
    echo ""
}

# ──────────────────────────────────────────────

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
            --json|--request-permission) ;;
            *) echo "Unknown option: ${CONFIGURE_ARGS[$i]}" >&2; return 1 ;;
        esac
        i=$((i + 1))
    done
    if [ -z "$nav" ] && [ -z "$app" ] && [ -z "$team" ] && [ -z "$unknown" ] && [ -z "$asserted" ]; then
        CONFIGURE_ARGS+=(--navigation none --allow-unknown-caller true --allow-caller-asserted false)
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
    local seen_agent_notify=false seen_skip_agent_notify=false
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --product)
                [ "$#" -ge 2 ] && [ -z "$PRODUCT" ] || { echo "Use --product claude|codex|both once." >&2; return 1; }
                PRODUCT="$2"; shift 2 ;;
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
            --request-permission|--json)
                CONFIGURE_ARGS+=("$1")
                shift ;;
            --help|-h)
                echo "Usage: bash bootstrap.sh [--product claude|codex|both] [--agent-notify|--skip-agent-notify] [--navigation none]"
                exit 0 ;;
            *) echo "Unknown option: $1" >&2; return 1 ;;
        esac
    done
    if [ "$seen_agent_notify" = true ] && [ "$seen_skip_agent_notify" = true ]; then
        echo "--agent-notify and --skip-agent-notify are mutually exclusive." >&2
        return 1
    fi
    if [ "$CONFIGURE_NOTIFICATIONS" = true ]; then
        complete_configure_route || return 1
    fi
    if [ "$CONFIGURE_NOTIFICATIONS" != true ] && [ "${#CONFIGURE_ARGS[@]}" -ne 0 ]; then
        echo "Route flags require --agent-notify." >&2
        return 1
    fi
    if [ -z "$PRODUCT" ]; then
        if ! { exec 3<>/dev/tty; } 2>/dev/null; then
            echo "No controlling TTY. Specify --product claude|codex|both." >&2
            return 1
        fi
        printf 'Install notifications for: 1) Claude  2) Codex  3) Both\nChoice: ' >&3
        local choice=""
        IFS= read -r choice <&3 || true
        exec 3>&-
        case "$choice" in
            1|claude) PRODUCT=claude ;;
            2|codex) PRODUCT=codex ;;
            3|both) PRODUCT=both ;;
            *) echo "Invalid product choice; use claude, codex or both." >&2; return 1 ;;
        esac
    fi
    case "$PRODUCT" in
        claude|codex|both) ;;
        *) echo "Invalid product: $PRODUCT; use claude, codex or both." >&2; return 1 ;;
    esac
}

bootstrap_cleanup() {
    [ -z "$_BOOTSTRAP_TMP" ] || rm -f "$_BOOTSTRAP_TMP"
    [ -z "$_BOOTSTRAP_STAGE" ] || rm -rf "$_BOOTSTRAP_STAGE"
    [ -z "$_PORTABLE_STAGE" ] || rm -rf "$_PORTABLE_STAGE"
    if [ "$_KEEP_CONFIG_STAGE" != true ]; then
        [ -z "$_CONFIG_STAGE" ] || rm -rf "$_CONFIG_STAGE"
    fi
    return 0
}

install_cleanup_traps() {
    trap bootstrap_cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
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

resolve_bootstrap_release() {
    BOOTSTRAP_TAG="${BOOTSTRAP_RELEASE_TAG:-}"
    if [ -z "$BOOTSTRAP_TAG" ]; then
        _BOOTSTRAP_TMP=$(mktemp "${TMPDIR:-/tmp}/bootstrap-release-XXXXXX") || return 1
        fetch_bootstrap_file "${BOOTSTRAP_LATEST_RELEASE_API_URL:-https://api.github.com/repos/${REPO}/releases/latest}" "$_BOOTSTRAP_TMP" || return 1
        BOOTSTRAP_TAG=$(grep -oE '"tag_name"[[:space:]]*:[[:space:]]*"[^"]+"' "$_BOOTSTRAP_TMP" | head -1 | sed -E 's/.*"([^"]+)".*/\1/') || return 1
        rm -f "$_BOOTSTRAP_TMP"
        _BOOTSTRAP_TMP=""
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

    BOOTSTRAP_COMMIT="${BOOTSTRAP_RELEASE_COMMIT:-}"
    if [ -z "$BOOTSTRAP_COMMIT" ]; then
        _BOOTSTRAP_TMP=$(mktemp "${TMPDIR:-/tmp}/bootstrap-commit-XXXXXX") || return 1
        fetch_bootstrap_file "${BOOTSTRAP_COMMIT_API_BASE_URL:-https://api.github.com/repos/${REPO}/commits}/$BOOTSTRAP_TAG" "$_BOOTSTRAP_TMP" || return 1
        BOOTSTRAP_COMMIT=$(
            if usable_python3; then
            python3 -I - "$_BOOTSTRAP_TMP" <<'PYCOMMIT'
import json, re, sys
with open(sys.argv[1], encoding='utf-8') as stream:
    value = json.load(stream).get('sha', '')
if not isinstance(value, str) or re.fullmatch(r'[0-9a-f]{40}', value) is None:
    raise SystemExit('Release tag did not resolve to a commit SHA')
sys.stdout.buffer.write((value + '\n').encode('ascii'))
PYCOMMIT
            elif usable_node; then
            run_isolated_node - "$_BOOTSTRAP_TMP" <<'JSCOMMIT'
const fs = require('fs');
let value;
try {
  value = JSON.parse(fs.readFileSync(process.argv[2], 'utf8')).sha || '';
} catch (e) {
  process.stderr.write('Release tag did not resolve to a commit SHA\n');
  process.exit(1);
}
if (typeof value !== 'string' || !/^[0-9a-f]{40}$/.test(value)) {
  process.stderr.write('Release tag did not resolve to a commit SHA\n');
  process.exit(1);
}
process.stdout.write(value + '\n');
JSCOMMIT
            else
            echo "python3 or node is required for protected installer metadata and checksum validation." >&2
            exit 1
            fi
        ) || return 1
        rm -f "$_BOOTSTRAP_TMP"
        _BOOTSTRAP_TMP=""
    fi
    printf '%s\n' "$BOOTSTRAP_COMMIT" | grep -Eq '^[0-9a-f]{40}$' || {
        echo "Invalid release commit SHA: $BOOTSTRAP_COMMIT" >&2; return 1;
    }

    # Keep explicit overrides separate from the default so managed installs
    # can still select their compatible writer.
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
    # Use the OS temporary root, not a caller-controlled TMPDIR inside a bundle.
    # The verified helper checks canonical overlap before any refresh operation.
    _CONFIG_STAGE=$(mktemp -d "/tmp/bootstrap-config-XXXXXX") || return 1
    # Snapshot the pre-update registry, not a guessed cache version. Later
    # registrations introduce packaged templates, not historical user settings.
    if [ "$PRODUCT" != codex ] && [ -e "$INSTALLED_JSON" ]; then
        cp "$INSTALLED_JSON" "$_CONFIG_STAGE/installed-before.json" || return 1
    else
        printf '{"plugins":{}}\n' > "$_CONFIG_STAGE/installed-before.json"
    fi
    local os arch name base capability
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
    "$_CONFIG_HELPER" config path --json > "$_CONFIG_STAGE/path.json" || return 1
    fetch_bootstrap_file "$(select_bootstrap_install_script)" "$_CONFIG_STAGE/install.sh" || return 1
}

bootstrap_control_root() {
    case "$(uname -s 2>/dev/null)" in
        Darwin)
            printf '%s\n' "$HOME/Library/Application Support/agent-notifications"
            ;;
        MINGW*|MSYS*|CYGWIN*|Windows_NT)
            printf '%s\n' "${APPDATA:-$HOME/AppData/Roaming}/agent-notifications"
            ;;
        *)
            printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/agent-notifications"
            ;;
    esac
}

bootstrap_has_managed_ledger() {
    local ledger
    ledger="$(bootstrap_control_root)/ownership.json"
    [ -f "$ledger" ] || return 1
    [ ! -L "$ledger" ]
}

select_bootstrap_install_script() {
    if [ -n "${INSTALL_SCRIPT_URL:-}" ]; then
        printf '%s\n' "$INSTALL_SCRIPT_URL"
        return 0
    fi
    if bootstrap_has_managed_ledger; then
        printf '%s\n' "$MANAGED_INSTALL_SCRIPT_URL"
        return 0
    fi
    printf '%s\n' "${BOOTSTRAP_RAW_BASE_URL:-$BOOTSTRAP_RAW_CONTENT_URL}/${BOOTSTRAP_COMMIT}/bin/install.sh"
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
        venv_refresh="$HOME/.claude/claude-notifications-go/iterm2-venv"
    fi
    # Resolve config afresh before every destructive operation. Historical roots
    # come from the pre-update registry; current roots extend overlap protection.
    if "$_CONFIG_HELPER" config installer bootstrap "$_CONFIG_STAGE/installed-before.json" "$PLUGIN_KEY" "$CLAUDE_HOME" "$CACHE_DIR" "$MARKETPLACE_DIR" "${CODEX_HOME:-$HOME/.codex}" "$PRODUCT" "$_CONFIG_STAGE" "$INSTALLED_JSON" "$venv_refresh" > "$_CONFIG_STAGE/preflight.json"; then
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
    printf 'Config-only retry (no downloads or registration): %q config init --json\n' "$_CONFIG_HELPER" >&2
    return 1
}

install_codex() {
    local tag="$BOOTSTRAP_TAG" version="${BOOTSTRAP_TAG#v}"
    local source_base="${BOOTSTRAP_SOURCE_BASE_URL:-https://github.com/${REPO}/archive}"
    local release_base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}"
    _BOOTSTRAP_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-codex-XXXXXX") || return 1
    local bundle="$_BOOTSTRAP_STAGE/bundle"
    mkdir "$bundle" || return 1
    fetch_bootstrap_file "$source_base/$BOOTSTRAP_COMMIT.tar.gz" "$_BOOTSTRAP_STAGE/source.tar.gz" || return 1
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
    local setup_codex_home="" i=0
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
    run_codex_setup --dry-run || return 1
    config_preflight || return 1
    run_codex_setup || return $?
    if [ -n "$setup_codex_home" ]; then
        CONFIGURE_BINARY=$(installed_notification_binary "$setup_codex_home/claude-notifications-go") || return 1
    else
        CONFIGURE_BINARY=$(installed_notification_binary "${CODEX_HOME:-$HOME/.codex}/claude-notifications-go") || return 1
    fi
    if [ ! -x "$CONFIGURE_BINARY" ]; then
        echo "Committed Codex runtime binary missing after setup-codex." >&2
        return 1
    fi
    rm -rf "$_BOOTSTRAP_STAGE"
    _BOOTSTRAP_STAGE=""
    echo "Codex installed. Start Codex, run /hooks, review and trust the entries."
}

# Git Bash installs native executables, without an extensionless launcher.
installed_notification_binary() {
    local root="$1" arch
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            case "$(uname -m)" in
                x86_64|amd64) arch=amd64 ;;
                aarch64|arm64) arch=arm64 ;;
                *) return 1 ;;
            esac
            printf '%s/bin/claude-notifications-windows-%s.exe\n' "$root" "$arch" ;;
        *) printf '%s/bin/claude-notifications\n' "$root" ;;
    esac
}

install_claude() {
    setup_marketplace || return 1
    sync_marketplace_checkout || return 1
    install_plugin || return 1
    find_plugin_root || return 1
    download_binary || return 1
    setup_iterm2_venv || return 1
    CONFIGURE_BINARY=$(installed_notification_binary "$PLUGIN_ROOT") || return 1
    if [ "$PRODUCT" = both ]; then
    echo "Agent Notifications installed; continuing with Codex."
    fi
}

main() {
    select_product "$@" || return 1
    print_header
    abort_if_wsl_environment
    check_prerequisites || return 1
    detect_platform
    install_cleanup_traps
    resolve_bootstrap_release || return 1
    stage_config_helper || { echo "Cannot stage verified config helper; existing runtime retained." >&2; return 1; }
    stage_historical_baselines || return 1
    config_preflight || return 1
    if [ "$PRODUCT" != codex ]; then
        # Function assignment scopes child environment while preserving PLUGIN_ROOT.
        CN_PRODUCT=claude install_claude || return 1
    fi
    if [ "$PRODUCT" != claude ]; then
        local codex_status=0
        install_codex || codex_status=$?
        # Reserved CLI result: registration committed, config init failed.
        if [ "$codex_status" -eq 3 ]; then
            report_config_init_failure
            return 1
        elif [ "$codex_status" -ne 0 ]; then
            echo "Codex installation/registration failed; no all-products success." >&2
            [ "$PRODUCT" != both ] || echo "Claude installation completed separately." >&2
            return 1
        fi
    fi
    initialize_config || return 1
    configure_agent_notify || return 1
    [ "$PRODUCT" != claude ] || print_success
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
    [ "$CONFIGURE_NOTIFICATIONS" = true ] || return 0
    if [ -z "$CONFIGURE_BINARY" ]; then
        CONFIGURE_BINARY=$(installed_notification_binary "$PLUGIN_ROOT") || return 1
    fi
    if [ ! -x "$CONFIGURE_BINARY" ]; then
        echo -e "${YELLOW}⚠ Agent-notify setup skipped; installer binary not found.${NC}" >&2
        echo -e "${YELLOW}  Plugin/hooks install succeeded. Retry after the binary is available.${NC}" >&2
        return 0
    fi
    if cli_has_setup_wizard "$CONFIGURE_BINARY"; then
        setup_agent_notify_wizard
        return $?
    fi
    if ! cli_has_setup_notifications "$CONFIGURE_BINARY"; then
        echo -e "${YELLOW}⚠ Agent-notify setup skipped; this published CLI does not support setup-notifications.${NC}" >&2
        echo -e "${YELLOW}  Plugin/hooks install succeeded. Desktop/hook notifications still work.${NC}" >&2
        return 0
    fi
    configure_agent_policy
}

configure_agent_policy() {
    if ! "$CONFIGURE_BINARY" setup-notifications configure --provider "$PRODUCT" "${CONFIGURE_ARGS[@]}"; then
        echo -e "${YELLOW}⚠ Agent-notify setup failed; plugin/hooks install succeeded.${NC}" >&2
        echo -e "${YELLOW}  Desktop/hook notifications still work. Retry:${NC}" >&2
        printf '  %s\n' "$(quote_shell_command "$CONFIGURE_BINARY" setup-notifications configure --provider "$PRODUCT" "${CONFIGURE_ARGS[@]}")" >&2
        return 1
    fi
    return 0
}

# Same-tag portable zip is the wizard source. Auto-default without that asset is
# hooks-only, not a full MCP install. Explicit --agent-notify is incomplete.
report_wizard_portable_missing() {
    local reason="$1"
    local agents="$2"
    echo -e "${YELLOW}⚠ Agent-notify wizard skipped; ${reason}.${NC}" >&2
    echo -e "${YELLOW}  Plugin/hooks install succeeded. Retry:${NC}" >&2
    printf '  %s\n' "$(quote_shell_command "$CONFIGURE_BINARY" setup-notifications wizard --action install --agents "$agents" --hooks false --agent-notify true --yes)" >&2
    if [ "$AGENT_NOTIFY_REQUEST" = explicit ]; then
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
    stage="${_CONFIG_STAGE:-}"
    if [ -z "$stage" ]; then
        _PORTABLE_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-portable-XXXXXX") || return 1
        stage="$_PORTABLE_STAGE"
    fi
    base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
    if [ ! -f "$stage/checksums.txt" ]; then
        fetch_bootstrap_file "$base/checksums.txt" "$stage/checksums.txt" || return 1
    fi
    fetch_bootstrap_file "$base/$asset" "$stage/$asset" || return 1
    verify_bootstrap_checksum "$stage" "$asset" || return 1
    WIZARD_PACKAGE_ROOT="$stage/$asset"
}

setup_agent_notify_wizard() {
    local agents package_root install_root wizard_codex_home="${CODEX_HOME:-$HOME/.codex}" i=0
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
    plugin_root="$PLUGIN_ROOT"
    if [ -z "$plugin_root" ]; then
        plugin_root=$(cd "$(dirname "$CONFIGURE_BINARY")/.." && pwd)
    fi
    if [ -n "${BOOTSTRAP_TAG:-}" ]; then
        if ! acquire_wizard_portable_asset; then
            report_wizard_portable_missing "portable package is missing from $BOOTSTRAP_TAG" "$agents" && return 0
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
        report_wizard_portable_missing "portable package is missing from the accepted release" "$agents" && return 0
        return 1
    fi
    # Preserve policy enablement and accepted route/consent before the wizard
    # migrates client registration to the portable package.
    configure_agent_policy || return 1
    if [ "$PRODUCT" != codex ]; then
        claude_exec=$(bootstrap_abs_command claude) || true
    fi
    if [ "$PRODUCT" != claude ]; then
        codex_exec=$(bootstrap_abs_command codex) || true
    fi
    set -- setup-notifications wizard --action install --agents "$agents" --hooks false --agent-notify true --yes \
        --package "$package_root" --plugin-root "$plugin_root" --helper "$CONFIGURE_BINARY"
    set -- "$@" --codex-home "$wizard_codex_home" --claude-config "$CLAUDE_HOME"
    [ -z "$claude_exec" ] || set -- "$@" --claude-executable "$claude_exec"
    [ -z "$codex_exec" ] || set -- "$@" --codex-executable "$codex_exec"
    if [ "$PRODUCT" != both ] && [ -n "$claude_exec$codex_exec" ]; then
        if [ -n "$claude_exec" ]; then
            set -- "$@" --client-executable "$claude_exec"
        else
            set -- "$@" --client-executable "$codex_exec"
        fi
    fi
    if ! "$CONFIGURE_BINARY" "$@"; then
        echo -e "${YELLOW}⚠ Agent-notify setup failed; plugin/hooks install succeeded.${NC}" >&2
        echo -e "${YELLOW}  Desktop/hook notifications still work. Retry:${NC}" >&2
        printf '  %s\n' "$(quote_shell_command "$CONFIGURE_BINARY" "$@")" >&2
        return 1
    fi
    return 0
}

main "$@"
