#!/usr/bin/env bash
# Regression: mixed-platform, duplicate or incomplete snapshots must never select
# a release; interrupted same-version refresh must restore the working cache.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/TEST-release-channels.XXXXXX")
trap 'rm -rf "$TEST_ROOT"' EXIT
test_env_setup "$TEST_ROOT"
export CLAUDE_CONFIG_DIR="$HOME/.claude"
mkdir -p "$HOME"
source "$ROOT/bin/release-channel.sh"
for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do
    selected=$(release_channel_select "$ROOT/release-channels.tsv" "${platform%/*}" "${platform#*/}")
    case "$platform" in darwin/*) expected=v1.46.1 ;; *) expected=v1.48.0 ;; esac
    [ "${selected%%$'\t'*}" = "$expected" ]
done
for fault in duplicate missing invalid_sha invalid_tag ref schema unknown; do
    cp "$ROOT/release-channels.tsv" "$TEST_ROOT/index"
    case "$fault" in
        duplicate) tail -1 "$TEST_ROOT/index" >> "$TEST_ROOT/index" ;;
        missing) sed '$d' "$TEST_ROOT/index" > "$TEST_ROOT/new"; mv "$TEST_ROOT/new" "$TEST_ROOT/index" ;;
        invalid_sha) awk 'BEGIN { FS=OFS="\t" } NR==3 { $4="main" } { print }' "$TEST_ROOT/index" > "$TEST_ROOT/new"; mv "$TEST_ROOT/new" "$TEST_ROOT/index" ;;
        invalid_tag) awk 'BEGIN { FS=OFS="\t" } NR==3 { $3="v01.48.0" } { print }' "$TEST_ROOT/index" > "$TEST_ROOT/new"; mv "$TEST_ROOT/new" "$TEST_ROOT/index" ;;
        ref) sed 's|release/platform-linux-windows|main|g' "$TEST_ROOT/index" > "$TEST_ROOT/new"; mv "$TEST_ROOT/new" "$TEST_ROOT/index" ;;
        schema) sed 's/channels-v1/channels-v2/' "$TEST_ROOT/index" > "$TEST_ROOT/new"; mv "$TEST_ROOT/new" "$TEST_ROOT/index" ;;
        unknown) printf 'linux\tppc64\tv1.47.1\tx\tx\tmain\n' >> "$TEST_ROOT/index" ;;
    esac
    if release_channel_select "$TEST_ROOT/index" darwin amd64 > "$TEST_ROOT/selection" 2>/dev/null; then
        echo "Accepted broken snapshot: $fault" >&2; exit 1
    fi
    [ ! -s "$TEST_ROOT/selection" ]
done
if release_channel_select "$ROOT/release-channels.tsv" windows arm64 >/dev/null 2>&1; then exit 1; fi
# Load definitions without the executable entry point, into this TEST-only home.
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$TEST_ROOT/bootstrap-functions.sh"
source "$TEST_ROOT/bootstrap-functions.sh"
BOOTSTRAP_TAG=v1.47.1
ROOT_CACHE="$CACHE_DIR/$MARKETPLACE_NAME/1.47.1"
mkdir -p "$ROOT_CACHE/bin" "$ROOT_CACHE/.claude-plugin"
printf '{"version":"1.47.1"}\n' > "$ROOT_CACHE/.claude-plugin/plugin.json"
printf 'working source' > "$ROOT_CACHE/bin/source"
printf 'saved settings' > "$CLAUDE_HOME/saved-settings"
cat > "$TEST_ROOT/failing-cli" <<'CLI'
#!/usr/bin/env bash
mkdir -p "$TEST_CACHE/bin"
printf incomplete > "$TEST_CACHE/bin/source"
exit 1
CLI
chmod +x "$TEST_ROOT/failing-cli"
export TEST_CACHE="$ROOT_CACHE"
_CLAUDE_EXEC="$TEST_ROOT/failing-cli"
get_installed_plugin_version() { printf '%s\n' unknown; }
get_installed_plugin_root() { printf '%s\n' "$ROOT_CACHE"; }
config_preflight() { return 0; }
if channel_install_plugin; then echo 'Accepted failed refresh' >&2; exit 1; fi
[ "$(cat "$ROOT_CACHE/bin/source")" = 'working source' ]
[ "$(cat "$CLAUDE_HOME/saved-settings")" = 'saved settings' ]
[ "$(cat "$CACHE_DIR/$MARKETPLACE_NAME"/.channel-incomplete.*/bin/source)" = incomplete ]
# A temporary acquisition target must not lose the source bundle version or
# consult GitHub Latest. This guards the legacy config preflight regression.
(
    mkdir -p "$TEST_ROOT/bundle/.claude-plugin" "$TEST_ROOT/empty-stage"
    printf '{"version":"1.47.1"}\n' > "$TEST_ROOT/bundle/.claude-plugin/plugin.json"
    INSTALL_BUNDLE_MANIFEST="$TEST_ROOT/bundle/.claude-plugin/plugin.json"
    SCRIPT_DIR="$TEST_ROOT/empty-stage"
    RELEASES_BASE_URL=https://example.invalid/releases
    DEFAULT_RELEASE_URL="$RELEASES_BASE_URL/latest/download"
    DEFAULT_CHECKSUMS_URL="$DEFAULT_RELEASE_URL/checksums.txt"
    DEFAULT_MODERN_NOTIFIER_URL="$DEFAULT_RELEASE_URL/ClaudeNotifier.app.zip"
    RELEASE_URL=$DEFAULT_RELEASE_URL
    CHECKSUMS_URL=$DEFAULT_CHECKSUMS_URL
    MODERN_NOTIFIER_URL=$DEFAULT_MODERN_NOTIFIER_URL
    awk '/^pin_release_urls\(\)/ { copy=1 } copy { print } copy && /^}/ { exit }' "$ROOT/bin/install.sh" > "$TEST_ROOT/pin.sh"
    source "$TEST_ROOT/pin.sh"
    pin_release_urls >/dev/null
    [ "$RELEASE_URL" = "$RELEASES_BASE_URL/download/v1.47.1" ]
    [ "$CHECKSUMS_URL" = "$RELEASE_URL/checksums.txt" ]
    INSTALL_BUNDLE_MANIFEST="$TEST_ROOT/absent"
    RELEASE_URL=$DEFAULT_RELEASE_URL
    if pin_release_urls >/dev/null 2>&1; then exit 1; fi
)
printf 'PASS platform selection, source pin and interrupted cache recovery\n' 
