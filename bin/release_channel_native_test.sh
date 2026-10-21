#!/usr/bin/env bash
# Regression: a same-version source copy must preserve SDK-owned modes/links.
# Every runtime operation is confined to a fresh TEST home and cache.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
if [ "${_TEST_ENV_READY:-}" != 1 ]; then
    set -- "${1:-${CHANNEL_TEST_BINARY:-$(dirname "$0")/claude-notifications}}" \
        "${2:-${CHANNEL_TEST_BOOTSTRAP:-$(dirname "$0")/bootstrap.sh}}" \
        "${3:-${CHANNEL_TEST_INSTALLER:-$(dirname "$0")/install.sh}}"
fi
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BINARY=$1
[ -x "$BINARY" ] || { echo 'Set CHANNEL_TEST_BINARY to a built native CLI.' >&2; exit 1; }
BINARY=$(cd "$(dirname "$BINARY")" && pwd)/$(basename "$BINARY")
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/TEST-channel-native.XXXXXX")
trap 'rm -rf "$TEST_ROOT"' EXIT
test_env_setup "$TEST_ROOT"
export CLAUDE_CONFIG_DIR="$HOME/.claude"
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$CODEX_HOME"
sed '/^main "\$@"$/d' "$2" > "$TEST_ROOT/bootstrap.sh"
source "$TEST_ROOT/bootstrap.sh"
BOOTSTRAP_TAG=$("$BINARY" --version); BOOTSTRAP_TAG=${BOOTSTRAP_TAG#claude-notifications }
version=${BOOTSTRAP_TAG#v}
_CONFIG_STAGE="$TEST_ROOT/stage"
mkdir -p "$_CONFIG_STAGE"
read -r os arch < <(bootstrap_release_os_arch)
asset="claude-notifications-$os-$arch"
[ "$os" != windows ] || asset="$asset.exe"
cp "$BINARY" "$_CONFIG_STAGE/$asset"
_CONFIG_HELPER="$_CONFIG_STAGE/$asset"
cp "$3" "$_CONFIG_STAGE/install.sh"
if command -v sha256sum >/dev/null; then
    (cd "$_CONFIG_STAGE"; sha256sum "$asset" > checksums.txt)
else
    (cd "$_CONFIG_STAGE"; shasum -a 256 "$asset" > checksums.txt)
fi
BOOTSTRAP_RELEASES_BASE_URL=http://127.0.0.1:9/releases
PRODUCT=claude
ROOT_CACHE="$CACHE_DIR/$MARKETPLACE_NAME/$version"
mkdir -p "$ROOT_CACHE/bin" "$ROOT_CACHE/.claude-plugin" "$ROOT_CACHE/skills/agent-notifications"
printf '{"version":"%s"}\n' "$version" > "$ROOT_CACHE/.claude-plugin/plugin.json"
cp "$ROOT/skills/agent-notifications/SKILL.md" "$ROOT_CACHE/skills/agent-notifications/SKILL.md"
"$_CONFIG_HELPER" internal-install-runtime --stage "$_CONFIG_STAGE" --target "$ROOT_CACHE/bin" --entry "$asset" >/dev/null
printf '{"plugins":{}}\n' > "$_CONFIG_STAGE/installed-before.json"
cat > "$TEST_ROOT/cache-cli" <<'CLI'
#!/usr/bin/env bash
set -eu
mkdir -p "$TEST_CACHE/bin" "$TEST_CACHE/.claude-plugin" "$TEST_CACHE/skills/agent-notifications"
printf '{"version":"%s"}\n' "$TEST_VERSION" > "$TEST_CACHE/.claude-plugin/plugin.json"
cp "$TEST_SOURCE/skills/agent-notifications/SKILL.md" "$TEST_CACHE/skills/agent-notifications/SKILL.md"
chmod 0644 "$TEST_CACHE/skills/agent-notifications/SKILL.md"
if [ -n "${TEST_LEGACY_SKILL:-}" ]; then
    # Reproduce a stale cache overlay: the removed legacy skill resurfaces.
    mkdir -p "$TEST_CACHE/skills/agent-notify"
    cp "$TEST_LEGACY_SKILL" "$TEST_CACHE/skills/agent-notify/SKILL.md"
    chmod 0644 "$TEST_CACHE/skills/agent-notify/SKILL.md"
fi
if [ "${TEST_HISTORICAL_LAYOUT:-}" != 1 ]; then
    mkdir -p "$TEST_CACHE/docs/opencode"
fi
for contract in E1-PORTS.md E2-PORTS.md E2A-PORTS.md E2-JS-PORTS.md CLOCK-MATRIX-PORTS.md; do
    if [ "${TEST_HISTORICAL_LAYOUT:-}" != 1 ]; then
        cp "$TEST_SOURCE/docs/opencode/$contract" "$TEST_CACHE/docs/opencode/$contract"
    fi
    cp "$TEST_SOURCE/docs/opencode/$contract" "$TEST_CACHE/$contract"
done
printf 1766603417062 > "$TEST_CACHE/.orphaned_at"
printf refreshed > "$TEST_CACHE/source-generation"
CLI
chmod +x "$TEST_ROOT/cache-cli"
export TEST_CACHE="$ROOT_CACHE" TEST_VERSION="$version" TEST_SOURCE="$ROOT"
_CLAUDE_EXEC="$TEST_ROOT/cache-cli"
get_installed_plugin_version() { printf '%s\n' unknown; }
get_installed_plugin_root() { printf '%s\n' "$ROOT_CACHE"; }
verify_installed_plugin_version() { [ "$(get_manifest_version "$ROOT_CACHE/.claude-plugin/plugin.json")" = "$1" ]; }
find_plugin_root() { PLUGIN_ROOT="$ROOT_CACHE"; }
# Real released native SDK does both promotions and ownership validation.
download_binary() {
    if install_runtime claude "$_CONFIG_STAGE/install.sh" "$ROOT_CACHE/bin" --force > "$TEST_ROOT/promotion.log" 2>&1; then
        return 0
    fi
    cat "$TEST_ROOT/promotion.log" >&2
    return 1
}
# Issue #490: a stale legacy skill resurfaces via the retained backup and a
# stale cache overlay; neither may survive the channel refresh.
printf 'historical legacy skill\n' > "$TEST_ROOT/stale-legacy-skill.md"
mkdir -p "$ROOT_CACHE/skills/agent-notify"
cp "$TEST_ROOT/stale-legacy-skill.md" "$ROOT_CACHE/skills/agent-notify/SKILL.md"
chmod 0600 "$ROOT_CACHE/skills/agent-notify/SKILL.md"
export TEST_LEGACY_SKILL="$TEST_ROOT/stale-legacy-skill.md"
# Seed the same-version backup too; removed source documents must not copy back.
for contract in E1-PORTS.md E2-PORTS.md E2A-PORTS.md E2-JS-PORTS.md CLOCK-MATRIX-PORTS.md; do
    cp "$ROOT/docs/opencode/$contract" "$ROOT_CACHE/$contract"
done
channel_install_plugin
for contract in E1-PORTS.md E2-PORTS.md E2A-PORTS.md E2-JS-PORTS.md CLOCK-MATRIX-PORTS.md .orphaned_at; do
    [ ! -e "$ROOT_CACHE/$contract" ] || { echo "FAIL retired source file survived: $contract" >&2; exit 1; }
done
[ ! -e "$ROOT_CACHE/skills/agent-notify" ] || { echo 'FAIL legacy skills/agent-notify survived the channel refresh' >&2; exit 1; }
[ "$(cat "$ROOT_CACHE/source-generation")" = refreshed ]
[ "$("$ROOT_CACHE/bin/claude-notifications" --version)" = "claude-notifications $BOOTSTRAP_TAG" ]
# A second real native refresh verifies the ledger after source replacement.
"$_CONFIG_HELPER" internal-install-runtime --refresh --stage "$_CONFIG_STAGE" --target "$ROOT_CACHE/bin" --entry "$asset" >/dev/null
printf 'PASS existing managed same-version cache with real native SDK\n'

# A currently selected older release can legitimately retain the root layout.
export TEST_HISTORICAL_LAYOUT=1
channel_install_plugin
for contract in E1-PORTS.md E2-PORTS.md E2A-PORTS.md E2-JS-PORTS.md CLOCK-MATRIX-PORTS.md; do
    cmp "$ROOT/docs/opencode/$contract" "$ROOT_CACHE/$contract"
done
[ -f "$ROOT_CACHE/.orphaned_at" ]
printf 'PASS historical source layout remains installable\n'
