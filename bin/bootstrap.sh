#!/bin/bash
# bootstrap.sh - One-command install/update for claude-notifications plugin
# Usage: curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/bootstrap.sh | bash

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
INSTALL_SCRIPT_URL="${INSTALL_SCRIPT_URL:-https://raw.githubusercontent.com/${REPO}/main/bin/install.sh}"

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
_KEEP_CONFIG_STAGE=false
PRODUCT=""
BOOTSTRAP_TAG=""
_BOOTSTRAP_TMP=""  # temp file path for trap (set -u safe)
CONFIGURE_NOTIFICATIONS=true
CONFIGURE_BINARY=""
CONFIGURE_ARGS=()

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
    echo -e "${YELLOW}For Windows Claude Code, open Git Bash from the Start menu and run:${NC}" >&2
    echo -e "  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/bin/bootstrap.sh | bash" >&2
    echo "" >&2
    echo -e "${YELLOW}If you intentionally use Claude Code inside WSL, rerun with:${NC}" >&2
    echo -e "  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/bin/bootstrap.sh | env CLAUDE_NOTIFICATIONS_ALLOW_WSL=1 bash" >&2
    echo "" >&2
    exit 1
}

# ──────────────────────────────────────────────

check_prerequisites() {
    command -v python3 >/dev/null 2>&1 || { echo "python3 is required for protected installer metadata and checksum validation." >&2; return 1; }
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
# `marketplace list` only reads local state — no network calls.
marketplace_declared_repo() {
    local tmp
    tmp=$(mktemp "${TMPDIR:-/tmp}/marketplace-list-XXXXXX") || return 1
    claude plugin marketplace list --json </dev/null >"$tmp" 2>/dev/null
    python3 -I - "$tmp" "$MARKETPLACE_NAME" <<'PY'
import json, sys
path, name = sys.argv[1], sys.argv[2]
try:
    with open(path) as f:
        entries = json.load(f)
    for e in entries:
        if e.get('name') == name and e.get('repo'):
            print(e['repo'])
            break
except Exception:
    pass
PY
    rm -f "$tmp"
}

# True if $1 (a repo slug) is one of our own retired marketplace sources.
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

    if command -v python3 &>/dev/null; then
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

    if command -v node &>/dev/null; then
        PLUGIN_KEY="$PLUGIN_KEY" node - "$INSTALLED_JSON" <<'JSEOF' 2>/dev/null || true
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

    if command -v python3 &>/dev/null; then
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

    if command -v node &>/dev/null; then
        PLUGIN_KEY="$PLUGIN_KEY" node - "$INSTALLED_JSON" <<'JSEOF' 2>/dev/null || true
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

  if [ -z "$PLUGIN_ROOT" ] && command -v python3 >/dev/null 2>&1; then
    PLUGIN_ROOT=$(python3 - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
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
    entries = [e for e in d.get('plugins', {}).get(sys.argv[2], []) if isinstance(e, dict) and e.get('installPath')]
    if entries:
        best = max(entries, key=lambda e: ver_tuple(e.get('version')))
        print(best.get('installPath', '') or '')
except Exception:
    pass
PYEOF
)
  fi

  # Node is very likely present because Claude Code is a Node app.
  if [ -z "$PLUGIN_ROOT" ] && command -v node >/dev/null 2>&1; then
    PLUGIN_ROOT=$(PLUGIN_KEY="$PLUGIN_KEY" node - "$INSTALLED_JSON" <<'JSEOF' 2>/dev/null || true
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
try {
  const p = process.argv[2];
  const k = process.env.PLUGIN_KEY;
  const d = JSON.parse(fs.readFileSync(p, 'utf8'));
  const entries = ((d.plugins && d.plugins[k]) || []).filter((entry) => entry && typeof entry === 'object' && entry.installPath);
  if (entries.length > 0) {
    const e = entries.slice().sort(compareVersions).pop();
    process.stdout.write((e && e.installPath) ? String(e.installPath) : '');
  }
} catch (_) {}
JSEOF
)
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

    # Find Python 3
    local python3_path=""
    command -v python3 &>/dev/null && python3_path="$(command -v python3)"

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
    echo -e "${BLUE}One-liner to update in the future (same as install):${NC}"
    echo -e "  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/bin/bootstrap.sh | bash"
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
                CONFIGURE_NOTIFICATIONS=true
                shift ;;
            --skip-agent-notify)
                seen_skip_agent_notify=true
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
        local has_route=false arg
        if [ "${#CONFIGURE_ARGS[@]}" -gt 0 ]; then
            for arg in "${CONFIGURE_ARGS[@]}"; do
                case "$arg" in
                    --navigation|--app|--team-id|--allow-unknown-caller|--allow-caller-asserted)
                        has_route=true ;;
                esac
            done
        fi
        if [ "$has_route" != true ]; then
            CONFIGURE_ARGS+=(--navigation none --allow-unknown-caller true --allow-caller-asserted false)
        fi
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
    CN_PRODUCT="$product" INSTALL_STAGED_ASSETS="$_CONFIG_STAGE" \
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
}

# Only release-verified bytes execute before host registration. Never use an old
# cache binary for config decisions. Paths are metadata, never resolver inputs.
stage_config_helper() {
    # Validate the temp parent before creating anything: TMPDIR can itself be
    # inside a Claude cache or a symlink into a refreshed runtime.
    python3 -I - "${TMPDIR:-/tmp}" "$INSTALLED_JSON" "$PLUGIN_KEY" "$PRODUCT" "$CACHE_DIR" "$MARKETPLACE_DIR" "${CODEX_HOME:-$HOME/.codex}/claude-notifications-go" <<'PYSTAGE' || return 1
import json, os, sys
base,registry,key,product,*roots=sys.argv[1:]
roots = roots[:2] if product == 'claude' else roots[2:] if product == 'codex' else roots
if product != 'codex' and os.path.lexists(registry):
    with open(registry) as f: entries=json.load(f).get('plugins',{}).get(key,[])
    roots.extend(e['installPath'] for e in entries)
def within(base, root):
    base = os.path.normcase(os.path.realpath(base))
    root = os.path.normcase(os.path.realpath(root))
    # Different drives or UNC shares cannot overlap.
    if os.path.splitdrive(base)[0] != os.path.splitdrive(root)[0]:
        return False
    return os.path.commonpath([base, root]) == root
if any(within(base, r) for r in roots):
    sys.exit('Staging must be outside refreshed bundles')
PYSTAGE
    _CONFIG_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-config-XXXXXX") || return 1
    # Snapshot the pre-update registry, not a guessed cache version. Later
    # registrations introduce packaged templates, not historical user settings.
    if [ "$PRODUCT" != codex ] && [ -e "$INSTALLED_JSON" ]; then
        cp "$INSTALLED_JSON" "$_CONFIG_STAGE/installed-before.json" || return 1
    else
        printf '{"plugins":{}}\n' > "$_CONFIG_STAGE/installed-before.json"
    fi
    local os arch name base
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in darwin|linux) ;; mingw*|msys*|cygwin*) os=windows ;; *) return 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) return 1 ;; esac
    name="claude-notifications-$os-$arch"
    [ "$os" != windows ] || name="$name.exe"
    base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/$BOOTSTRAP_TAG"
    fetch_bootstrap_file "$base/checksums.txt" "$_CONFIG_STAGE/checksums.txt" || return 1
    fetch_bootstrap_file "$base/$name" "$_CONFIG_STAGE/$name" || return 1
    python3 -I - "$_CONFIG_STAGE" "$name" <<'PYVERIFY' || return 1
import hashlib, pathlib, sys
root, name = pathlib.Path(sys.argv[1]), sys.argv[2]
entries = [line.split() for line in (root/'checksums.txt').read_text().splitlines()]
expected = [e[0] for e in entries if len(e)==2 and e[1].lstrip('*')==name]
assert len(expected)==1 and hashlib.sha256((root/name).read_bytes()).hexdigest()==expected[0].lower(), 'Helper checksum mismatch'
PYVERIFY
    _CONFIG_HELPER="$_CONFIG_STAGE/$name"
    chmod +x "$_CONFIG_HELPER" || return 1
    [ "$("$_CONFIG_HELPER" --version)" = "claude-notifications $BOOTSTRAP_TAG" ] || return 1
    "$_CONFIG_HELPER" config path --json > "$_CONFIG_STAGE/path.json" || return 1
    python3 -I - "$_CONFIG_STAGE/path.json" <<'PYCAP' || return 1
import json, sys
v=json.load(open(sys.argv[1]))
assert isinstance(v,dict) and isinstance(v.get('path'),str) and v['path'], 'Missing config path capability'
PYCAP
    fetch_bootstrap_file "$INSTALL_SCRIPT_URL" "$_CONFIG_STAGE/install.sh" || return 1
}

# Optional exact-version release templates. Releases without this verified asset
# intentionally leave baseline unknown and require explicit historical import.
stage_historical_baselines() {
    [ "$PRODUCT" != codex ] || return 0
    python3 -I - "$_CONFIG_STAGE/installed-before.json" "$PLUGIN_KEY" "$_CONFIG_STAGE" <<'PYVERSIONS' > "$_CONFIG_STAGE/versions" || return 1
import json,os,re,sys
registry,key,stage=sys.argv[1:]
versions=set()
if os.path.lexists(registry):
    with open(registry) as f: entries=json.load(f).get('plugins',{}).get(key,[])
    for e in entries:
        v=re.sub(r'^v', '', str(e.get('version','')))
        if re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)',v) and os.path.lexists(os.path.join(e['installPath'],'config','config.json')): versions.add(v)
# This output is consumed by a Bash read loop.  Native Windows Python otherwise
# emits CRLF, leaving a trailing CR in the release URL and baseline directory.
sys.stdout.buffer.write(('\n'.join(sorted(versions))+'\n' if versions else '').encode('ascii'))
PYVERSIONS
    local version base dir
    while IFS= read -r version; do
        [ -n "$version" ] || continue
        dir="$_CONFIG_STAGE/baseline-$version"
        mkdir "$dir" || return 1
        base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}/download/v$version"
        fetch_bootstrap_file "$base/checksums.txt" "$dir/checksums.txt" 2>/dev/null || continue
        # Only request a template explicitly included in the release manifest.
        python3 -I - "$dir/checksums.txt" <<'PYHAS' || continue
# Older releases may not publish a config.json checksum entry; skip this
# baseline quietly rather than let assert dump a traceback to the user.
import sys
has_entry = any(len(e) == 2 and e[1].lstrip('*') == 'config.json'
                for e in (line.split() for line in open(sys.argv[1])))
sys.exit(0 if has_entry else 1)
PYHAS
        fetch_bootstrap_file "$base/config.json" "$dir/config.json" 2>/dev/null || continue
        python3 -I - "$dir" <<'PYBASE' || continue
import hashlib,pathlib,sys
p=pathlib.Path(sys.argv[1])
entries=[line.split() for line in (p/'checksums.txt').read_text().splitlines()]
h=[e[0] for e in entries if len(e)==2 and e[1].lstrip('*')=='config.json']
if len(h)!=1 or hashlib.sha256((p/'config.json').read_bytes()).hexdigest()!=h[0].lower():
    sys.exit(1)
(p/'verified').write_text(h[0].lower())
PYBASE
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
    python3 -I - "$_CONFIG_STAGE/installed-before.json" "$PLUGIN_KEY" "$CLAUDE_HOME" "$CACHE_DIR" "$MARKETPLACE_DIR" "${CODEX_HOME:-$HOME/.codex}" "$PRODUCT" "$_CONFIG_STAGE" "$INSTALLED_JSON" "$venv_refresh" <<'PYINPUT' > "$_CONFIG_STAGE/preflight-input.json" || return 1
import json, os, re, sys
registry,key,claude,cache,market,codex,product,stage,current_registry,venv_refresh=sys.argv[1:]
baselines={}
roots=[]; refresh=[]; historical=[]
if product!='codex':
    if os.path.lexists(registry):
        with open(registry) as f: entries=json.load(f).get('plugins',{}).get(key,[])
        assert isinstance(entries,list), 'Invalid Claude registry'
        for entry in entries:
            root=entry.get('installPath')
            assert isinstance(root,str) and os.path.isabs(root), 'Missing absolute recorded installPath'
            if root not in roots: roots.append(root)
            version=re.sub(r'^v', '', str(entry.get('version','')))
            import re
            if re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)',version):
                baseline=os.path.join(stage,'baseline-'+version)
                if os.path.isfile(os.path.join(baseline,'verified')):
                    baselines[root]=dict(baselinePath=os.path.join(baseline,'config.json'),baselineSHA256=open(os.path.join(baseline,'verified')).read())
    refresh.extend([cache,market]+roots)
    if os.path.lexists(current_registry):
        with open(current_registry) as f: current=json.load(f).get('plugins',{}).get(key,[])
        for entry in current:
            root=entry.get('installPath')
            assert isinstance(root,str) and os.path.isabs(root), 'Missing absolute recorded installPath'
            if root not in refresh: refresh.append(root)
    historical.extend(dict(path=os.path.join(r,'config','config.json'),**baselines.get(r,{})) for r in roots)
# Shared configuration history applies even to a Codex-only install.
historical.append({'path':os.path.join(claude,'claude-notifications-go','config.json')})
if product!='claude':
    dest=os.path.join(codex,'claude-notifications-go')
    refresh.append(dest)
    historical.append({'path':os.path.join(dest,'config','config.json')})
if venv_refresh: refresh.append(venv_refresh)
assert all(os.path.isabs(p) for p in refresh), 'Refresh roots must be absolute'
# Baselines come only from checksum-verified artifacts of the recorded version.
protected=[current_registry,os.path.join(claude,'plugins','known_marketplaces.json'),os.path.join(claude,'settings.json')] if product!='codex' else []
json.dump(dict(activeBundleRoots=roots,refreshDirs=refresh,protectedPaths=protected,historicalCandidates=historical),sys.stdout)
PYINPUT
    if "$_CONFIG_HELPER" config preflight-update --stdin --json < "$_CONFIG_STAGE/preflight-input.json" > "$_CONFIG_STAGE/preflight.json"; then
        if python3 -I - "$_CONFIG_STAGE/preflight.json" <<'PYSAFE'
import json,sys
assert json.load(open(sys.argv[1])).get('status')=='safe'
PYSAFE
        then return 0; fi
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
    local source_base="${BOOTSTRAP_SOURCE_BASE_URL:-https://github.com/${REPO}/archive/refs/tags}"
    local release_base="${BOOTSTRAP_RELEASES_BASE_URL:-https://github.com/${REPO}/releases}"
    _BOOTSTRAP_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-codex-XXXXXX") || return 1
    local bundle="$_BOOTSTRAP_STAGE/bundle"
    mkdir "$bundle" || return 1
    fetch_bootstrap_file "$source_base/$tag.tar.gz" "$_BOOTSTRAP_STAGE/source.tar.gz" || return 1
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
        if [ -n "$setup_codex_home" ]; then
            CN_PRODUCT=codex "$binary" setup-codex --plugin-root "$bundle" --skip-agent-notify --codex-home "$setup_codex_home" "$@" </dev/null
        else
            CN_PRODUCT=codex "$binary" setup-codex --plugin-root "$bundle" --skip-agent-notify "$@" </dev/null
        fi
    }
    run_codex_setup --dry-run || return 1
    config_preflight || return 1
    run_codex_setup || return $?
    if [ -n "$setup_codex_home" ]; then
        CONFIGURE_BINARY="$setup_codex_home/claude-notifications-go/bin/claude-notifications"
    else
        CONFIGURE_BINARY="${CODEX_HOME:-$HOME/.codex}/claude-notifications-go/bin/claude-notifications"
    fi
    if [ ! -x "$CONFIGURE_BINARY" ]; then
        echo "Committed Codex runtime binary missing after setup-codex." >&2
        return 1
    fi
    rm -rf "$_BOOTSTRAP_STAGE"
    _BOOTSTRAP_STAGE=""
    echo "Codex installed. Start Codex, run /hooks, review and trust the entries."
}

install_claude() {
    setup_marketplace || return 1
    sync_marketplace_checkout || return 1
    install_plugin || return 1
    find_plugin_root || return 1
    download_binary || return 1
    setup_iterm2_venv || return 1
    CONFIGURE_BINARY="${PLUGIN_ROOT}/bin/claude-notifications"
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
    configure_agent_notify
    [ "$PRODUCT" != claude ] || print_success
}

# Agent-notify is default-on, but a failed configure must not undo hooks/plugin install.
configure_agent_notify() {
    [ "$CONFIGURE_NOTIFICATIONS" = true ] || return 0
    if [ -z "$CONFIGURE_BINARY" ]; then
        CONFIGURE_BINARY="${PLUGIN_ROOT}/bin/claude-notifications"
    fi
    if [ ! -x "$CONFIGURE_BINARY" ]; then
        echo -e "${YELLOW}⚠ Agent-notify setup skipped; installer binary not found.${NC}" >&2
        echo -e "${YELLOW}  Plugin/hooks install succeeded. Retry after the binary is available.${NC}" >&2
        return 0
    fi
    if ! "$CONFIGURE_BINARY" setup-notifications configure --provider "$PRODUCT" "${CONFIGURE_ARGS[@]}"; then
        echo -e "${YELLOW}⚠ Agent-notify setup failed; plugin/hooks install succeeded.${NC}" >&2
        echo -e "${YELLOW}  Desktop/hook notifications still work. Retry:${NC}" >&2
        echo -e "${YELLOW}  \"$CONFIGURE_BINARY\" setup-notifications configure --provider ${PRODUCT} ${CONFIGURE_ARGS[*]}${NC}" >&2
    fi
    return 0
}

main "$@"
