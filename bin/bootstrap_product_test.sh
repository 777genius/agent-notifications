#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# Isolated unit/adapter fixtures: no public network, real host CLIs or Go builds.
set -euo pipefail
trap 'printf "TEST bootstrap product fixture failed: %s (status %s)\n" "$BASH_COMMAND" "$?" >&2' ERR
case "${1:-}" in
    '') [ "$#" -eq 0 ] || exit 2; _PRODUCT_TEST_UNIT_ONLY=false ;;
    --unit-only) [ "$#" -eq 1 ] || exit 2; _PRODUCT_TEST_UNIT_ONLY=true ;;
    *) echo "Usage: bash bootstrap_product_test.sh [--unit-only]" >&2; exit 2 ;;
esac
readonly _PRODUCT_TEST_UNIT_ONLY
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SANDBOX=$(mktemp -d "${TMPDIR:-/tmp}/bootstrap-products-TEST-XXXXXX")
SANDBOX=$(cd -P "$SANDBOX" && pwd -P)
trap 'rm -rf "$SANDBOX"' EXIT
test_env_setup "$SANDBOX"
# test-env.sh inherits PATH: retain only named tools, never host agent binaries.
mkdir -p "$SANDBOX/trusted-tools"
# Native Python cannot CreateProcess a Bash wrapper on Windows. Keep the one
# controller executable explicit while child PATH remains the finite allowlist.
TEST_BOOTSTRAP_BASH=$(type -P bash)
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        [ ! -f "$TEST_BOOTSTRAP_BASH.exe" ] || TEST_BOOTSTRAP_BASH="$TEST_BOOTSTRAP_BASH.exe"
        TEST_BOOTSTRAP_BASH=$(cygpath -w "$TEST_BOOTSTRAP_BASH")
        ;;
esac
export TEST_BOOTSTRAP_BASH
for tool in bash sh env cygpath python3 node curl wget tar gzip unzip zip mktemp rm cat cp mv chmod mkdir rmdir ln uname tr wc head cmp grep sed awk dirname basename find sort sha256sum shasum cut xargs sleep date stat diff touch readlink dd od go gcc cc pkg-config; do
    tool_path=$(type -P "$tool" 2>/dev/null || true)
    [ -z "$tool_path" ] || test_env_place_tool "$tool_path" "$SANDBOX/trusted-tools/$tool"
done
export PATH="$SANDBOX/trusted-tools"

# Keep space-containing paths in the bootstrap characterization.
export HOME="$SANDBOX/home space" USERPROFILE="$SANDBOX/home space" CODEX_HOME="$SANDBOX/codex space"
export CLAUDE_CONFIG_DIR="$SANDBOX/claude config" CLAUDE_HOME="$SANDBOX/claude home"
mkdir -p "$HOME" "$CODEX_HOME" "$CLAUDE_CONFIG_DIR" "$CLAUDE_HOME"
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$SANDBOX/functions.sh"
# This source is also loaded by subprocess fixtures later in the suite.
printf '\nbootstrap_macos_command() { return 1; }\n' >> "$SANDBOX/functions.sh"

# Startup path resolution must be nounset-safe on Git Bash and must not require
# a home fallback when an explicit Claude path is available.
env -u HOME -u CLAUDE_CONFIG_DIR -u CLAUDE_HOME USERPROFILE="$SANDBOX/windows profile" \
    bash "$ROOT/bin/bootstrap.sh" --help >/dev/null
env -u HOME -u USERPROFILE -u CLAUDE_HOME CLAUDE_CONFIG_DIR="$SANDBOX/explicit config" \
    bash "$ROOT/bin/bootstrap.sh" --help >/dev/null
env -u HOME -u CLAUDE_CONFIG_DIR -u CLAUDE_HOME USERPROFILE="$SANDBOX/windows profile" \
    bash -c 'source "$1"; [ "$INSTALLER_HOME" = "$2" ] && [ "$CLAUDE_HOME" = "$2/.claude" ]' \
    _ "$SANDBOX/functions.sh" "$SANDBOX/windows profile"
env -u HOME -u USERPROFILE -u CLAUDE_HOME CLAUDE_CONFIG_DIR="$SANDBOX/explicit config" \
    bash -c 'source "$1"; [ -z "$INSTALLER_HOME" ] && [ "$CLAUDE_HOME" = "$2" ]' \
    _ "$SANDBOX/functions.sh" "$SANDBOX/explicit config"
env -u HOME -u USERPROFILE -u CLAUDE_CONFIG_DIR CLAUDE_HOME="$SANDBOX/legacy home" \
    bash -c 'source "$1"; [ -z "$INSTALLER_HOME" ] && [ "$CLAUDE_HOME" = "$2" ]' \
    _ "$SANDBOX/functions.sh" "$SANDBOX/legacy home"
env -u HOME USERPROFILE="$SANDBOX/windows profile" CLAUDE_HOME="$SANDBOX/legacy home" \
    CLAUDE_CONFIG_DIR="$SANDBOX/explicit config" \
    bash -c 'source "$1"; [ "$CLAUDE_HOME" = "$2" ]' \
    _ "$SANDBOX/functions.sh" "$SANDBOX/explicit config"
source "$SANDBOX/functions.sh"
# All macOS application queries in this suite must be explicitly mocked.
bootstrap_macos_command() { echo "Unexpected macOS application query in fixture" >&2; return 1; }

bash "$ROOT/bin/bootstrap_state_migration_test.sh"

# Regression: an omitted route on macOS must reach configure with the registered
# official app (including spaces) and preserve-policy; explicit decisions must
# bypass discovery, and an unverified identity must never become a local route.
(
    trace="$SANDBOX/default-codex-discovery"
    configure_trace="$SANDBOX/default-codex-configure"
    mkdir -p "$SANDBOX/apps with spaces/ChatGPT.app" "$SANDBOX/apps with spaces/Codex.app"
    uname() { printf '%s\n' "${TEST_OS:-Darwin}"; }
    bootstrap_macos_command() {
        printf '%s\n' "$1" >> "$trace"
        case "$1" in
            /usr/bin/osascript)
                [ "$#" -eq 5 ] && [ "$2" = -l ] && [ "$3" = JavaScript ] && [ "$4" = -e ] || return 2
                [ "${TEST_LS_AVAILABLE:-true}" = true ] || return 1
                printf '%s\n' "$TEST_LS_APP" ;;
            /usr/bin/codesign)
                [ "$#" -eq 7 ] && [ "$2" = --verify ] && [ "$3" = --strict ] &&
                    [ "$4" = --all-architectures ] && [ "$5" = -R ] || return 2
                [ "$6" = '=anchor apple generic and identifier "com.openai.codex" and certificate leaf[subject.OU] = "2DC432GLL2" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists' ] || return 2
                [ "$7" = "$(cd -P "$TEST_LS_APP" && pwd -P)" ] || return 2
                [ "${TEST_SIGNATURE_VALID:-true}" = true ] ;;
            *) return 2 ;;
        esac
    }
    cat > "$SANDBOX/default-route-configure" <<'CONFIGURE_ROUTE'
#!/bin/bash
printf '%s\n' "$@" > "$TEST_CONFIGURE_TRACE"
CONFIGURE_ROUTE
    chmod +x "$SANDBOX/default-route-configure"
    CONFIGURE_BINARY="$SANDBOX/default-route-configure"
    export TEST_CONFIGURE_TRACE="$configure_trace"
    for registered_name in ChatGPT Codex; do
        TEST_LS_APP="$SANDBOX/apps with spaces/$registered_name.app"
        physical_app=$(cd -P "$TEST_LS_APP" && pwd -P)
        for product in codex both; do
            : > "$trace"
            PRODUCT=""; CONFIGURE_ARGS=(); CONFIGURE_NOTIFICATIONS=true
            select_product --product "$product"
            [ "${#CONFIGURE_ARGS[@]}" -eq 9 ]
            [ "${CONFIGURE_ARGS[*]}" = "--app $physical_app --team-id 2DC432GLL2 --allow-unknown-caller true --allow-caller-asserted false --preserve-policy" ]
            [ "$(cat "$trace")" = $'/usr/bin/osascript\n/usr/bin/codesign' ]
            configure_agent_policy portable
            printf '%s\n' setup-notifications configure --provider "$product" \
                --app "$physical_app" --team-id 2DC432GLL2 \
                --allow-unknown-caller true --allow-caller-asserted false --preserve-policy \
                --policy-only --preserve-enabled > "$SANDBOX/default-route-expected"
            cmp "$configure_trace" "$SANDBOX/default-route-expected"
        done
    done
    : > "$trace"
    PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=()
    select_product --products codex,gemini --webhook
    [ "${CONFIGURE_ARGS[*]}" = "--app $physical_app --team-id 2DC432GLL2 --allow-unknown-caller true --allow-caller-asserted false --preserve-policy" ]
    [ "$(cat "$trace")" = $'/usr/bin/osascript\n/usr/bin/codesign' ]
    OPENCODE_ARGS=()
    for explicit_route in none app; do
        : > "$trace"
        PRODUCT=""; CONFIGURE_ARGS=()
        if [ "$explicit_route" = none ]; then
            select_product --product codex --navigation none --allow-unknown-caller false --allow-caller-asserted false
            [ "${CONFIGURE_ARGS[*]}" = "--navigation none --allow-unknown-caller false --allow-caller-asserted false" ]
        else
            select_product --product both --app "$physical_app" --team-id ABCDE12345 --allow-unknown-caller false --allow-caller-asserted true
            [ "${CONFIGURE_ARGS[*]}" = "--app $physical_app --team-id ABCDE12345 --allow-unknown-caller false --allow-caller-asserted true" ]
        fi
        [ ! -s "$trace" ]
    done
    for failure in signature missing unsafe; do
        : > "$trace"
        TEST_SIGNATURE_VALID=true; TEST_LS_AVAILABLE=true
        TEST_LS_APP="$SANDBOX/apps with spaces/ChatGPT.app"
        case "$failure" in
            signature) TEST_SIGNATURE_VALID=false ;;
            missing) TEST_LS_AVAILABLE=false ;;
            unsafe) TEST_LS_APP+=$'\nspoof.app' ;;
        esac
        PRODUCT=""; CONFIGURE_ARGS=()
        select_product --product codex 2> "$SANDBOX/default-route-fallback"
        [ "${CONFIGURE_ARGS[*]}" = "--navigation none --allow-unknown-caller true --allow-caller-asserted false --preserve-policy" ]
        grep -q 'defaulting to navigation none' "$SANDBOX/default-route-fallback"
        if [ "$failure" != signature ]; then
            [ "$(cat "$trace")" = /usr/bin/osascript ]
        fi
    done
    for fallback in claude Linux MINGW64_NT; do
        : > "$trace"
        TEST_OS=Darwin; product=codex
        if [ "$fallback" = claude ]; then product=claude; else TEST_OS="$fallback"; fi
        PRODUCT=""; CONFIGURE_ARGS=()
        select_product --product "$product"
        [ "${CONFIGURE_ARGS[*]}" = "--navigation none --allow-unknown-caller true --allow-caller-asserted false --preserve-policy" ]
        [ ! -s "$trace" ]
    done
)

# Consent failures must stop at argument parsing, before acquiring artifacts.
(
    PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=()
    select_product --product opencode --desktop --webhook
    [ "$PRODUCT" = opencode ] && [ "$CONFIGURE_NOTIFICATIONS" = false ]
    [ "${OPENCODE_ARGS[*]}" = "--desktop --webhook" ]
    if BOOTSTRAP_RELEASE_TAG=v1.45.0 resolve_bootstrap_release; then exit 1; fi
    BOOTSTRAP_RELEASE_TAG=v1.46.0 BOOTSTRAP_RELEASE_COMMIT=0123456789abcdef0123456789abcdef01234567 resolve_bootstrap_release
)
# A Gemini-containing selection must share observer flags while retaining the
# legacy group and rejecting duplicate/empty selectors before any acquisition.
(
    PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=()
    select_product --products claude,codex,opencode,gemini --webhook --skip-agent-notify
    [ "$LEGACY_PRODUCT" = both ]
    [ "${SELECTED_PRODUCTS[*]}" = "claude codex opencode gemini" ]
    [ "${OPENCODE_ARGS[*]}" = --webhook ]
)
# Qoder shares observer channels while retaining the selected legacy group.
(
    PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=()
    select_product --products codex,qoder --webhook --skip-agent-notify
    [ "$LEGACY_PRODUCT" = codex ]
    [ "${SELECTED_PRODUCTS[*]}" = "codex qoder" ]
    [ "${OPENCODE_ARGS[*]}" = --webhook ]
    PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=()
    select_product --product qoder --desktop
    [ "$CONFIGURE_NOTIFICATIONS" = false ]
    [ "${SELECTED_PRODUCTS[*]}" = qoder ]
)
for args in "--products gemini,gemini --webhook" "--products claude,,gemini --webhook" "--product gemini --webhook --agent-notify" "--products qoder,qoder --webhook" "--product qoder --agent-notify"; do
    if ( PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=(); select_product $args ); then
        echo "accepted invalid Gemini bundle: $args" >&2; exit 1
    fi
done
for args in "--product claude --desktop" "--product codex --webhook" "--product opencode --desktop --agent-notify" "--product opencode --desktop --skip-agent-notify" "--product opencode --desktop --navigation none"; do
    if ( PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=(); select_product $args ); then
        echo "accepted incompatible OpenCode consent flags: $args" >&2
        exit 1
    fi
done
# Presence is required, but host version output (or a failed --version command)
# must never gate installation. These inert executables must not be launched.
(
    mkdir -p "$SANDBOX/version-cli"
    for product in opencode gemini; do
        cat > "$SANDBOX/version-cli/$product" <<'HOST_VERSION'
#!/bin/bash
printf '0.1.0-future-preview\n'
: > "${BASH_SOURCE[0]%/*}/unexpected-version-probe"
exit 97
HOST_VERSION
        chmod +x "$SANDBOX/version-cli/$product"
    done
    export PATH="$SANDBOX/version-cli:$PATH"
    for PRODUCT in opencode gemini; do
        OPENCODE_ARGS=(--webhook)
        check_prerequisites
    done
    [ ! -e "$SANDBOX/version-cli/unexpected-version-probe" ]
)
# Keep the independent untrusted-diagnostic escaping/size contract.
(
    diagnostic=$(bootstrap_diagnostic_quote $'unknown\e[31m\rterminal-control')
    [[ "$diagnostic" != *$'\e'* && "$diagnostic" != *$'\r'* ]]
    value=$(printf '%700s' '' | tr ' ' x)
    diagnostic=$(bootstrap_diagnostic_quote "$value")
    [ "${#diagnostic}" -le 515 ]
)

quoted=$(quote_shell_command "$SANDBOX/bin space/cli" --package "$SANDBOX/pkg space" --codex-home "$CODEX_HOME")
eval "set -- $quoted"
[ "$#" -eq 5 ] || { echo "quoted argc $#"; exit 1; }
[ "$1" = "$SANDBOX/bin space/cli" ] || { echo "quoted binary $1"; exit 1; }
[ "$3" = "$SANDBOX/pkg space" ] || { echo "quoted package $3"; exit 1; }
[ "$5" = "$CODEX_HOME" ] || { echo "quoted codex home $5"; exit 1; }
for product in claude codex both; do
    PRODUCT=""; select_product --product "$product"; [ "$PRODUCT" = "$product" ]
done
for args in '--product invalid' '--product' '--unknown' '--product claude --product codex'; do
    if ( PRODUCT=""; select_product $args ); then echo "accepted $args"; exit 1; fi
done
TEST_RELEASE_COMMIT="0123456789abcdef0123456789abcdef01234567"
BOOTSTRAP_RAW_BASE_URL="https://raw.example.invalid/repository"
if ( PRODUCT=""; CONFIGURE_ARGS=(); select_product --product codex --navigation none ); then echo "accepted incomplete none"; exit 1; fi
if ( PRODUCT=""; CONFIGURE_ARGS=(); select_product --product codex --allow-unknown-caller true ); then echo "accepted partial consent"; exit 1; fi
PRODUCT=""; CONFIGURE_ARGS=(); CONFIGURE_NOTIFICATIONS=true
select_product --product codex --navigation none --allow-unknown-caller true --allow-caller-asserted false
[ "${#CONFIGURE_ARGS[@]}" -eq 6 ]
PRODUCT=""; CONFIGURE_ARGS=(); CONFIGURE_NOTIFICATIONS=true
select_product --product claude
[ "${CONFIGURE_ARGS[*]}" = "--navigation none --allow-unknown-caller true --allow-caller-asserted false --preserve-policy" ]
cat > "$SANDBOX/configure-parser" <<'EOF'
#!/bin/bash
preserve=0
policy=0
enabled=0
for arg in "$@"; do
    [ "$arg" != --preserve-policy ] || preserve=$((preserve + 1))
    [ "$arg" != --policy-only ] || policy=$((policy + 1))
    [ "$arg" != --preserve-enabled ] || enabled=$((enabled + 1))
done
[ "$preserve" -eq "$EXPECTED_PRESERVE" ] && [ "$policy" -eq 1 ] && [ "$enabled" -eq 1 ]
EOF
chmod +x "$SANDBOX/configure-parser"
(
    CONFIGURE_BINARY="$SANDBOX/configure-parser"
    PRODUCT=claude
    export EXPECTED_PRESERVE=1
    configure_agent_policy portable
    export EXPECTED_PRESERVE=0
    CONFIGURE_ARGS=(--navigation none --allow-unknown-caller true --allow-caller-asserted false)
    configure_agent_policy portable
)
for tag in v1.42.0 v1.43.2 v2.0.0; do
    BOOTSTRAP_RELEASE_TAG="$tag"
    BOOTSTRAP_RELEASE_COMMIT="$TEST_RELEASE_COMMIT"
    INSTALL_SCRIPT_URL=""
    resolve_bootstrap_release
    [ "$BOOTSTRAP_TAG" = "$tag" ]
    [ "$BOOTSTRAP_COMMIT" = "$TEST_RELEASE_COMMIT" ]
    case "$BOOTSTRAP_COMMIT" in *$'\r'*) echo "release commit contains CR"; exit 1 ;; esac
    [ "$(select_bootstrap_install_script)" = "$BOOTSTRAP_RAW_BASE_URL/$TEST_RELEASE_COMMIT/bin/install.sh" ]
done
for tag in v1.41.0 v0.99.0 v1.42.0-rc1 v01.42.0 v1.042.0 v1.42.00 v99999999999999999999.0.0 main; do
    if BOOTSTRAP_RELEASE_TAG="$tag" resolve_bootstrap_release; then exit 1; fi
done
for commit in short 0123456789abcdef0123456789abcdef0123456g 0123456789ABCDEF0123456789ABCDEF01234567; do
    if BOOTSTRAP_RELEASE_TAG=v1.42.0 BOOTSTRAP_RELEASE_COMMIT="$commit" resolve_bootstrap_release; then exit 1; fi
done
for response in short 0123456789abcdef0123456789abcdef0123456g 0123456789ABCDEF0123456789ABCDEF01234567 '{"sha":"0123456789abcdef0123456789abcdef01234567"}'; do
    (
        BOOTSTRAP_RELEASE_TAG=v1.42.0
        unset BOOTSTRAP_RELEASE_COMMIT
        fetch_bootstrap_commit_file() { printf '%s' "$response" > "$2"; }
        resolve_bootstrap_release
        if resolve_bootstrap_commit; then echo "accepted invalid raw commit response" >&2; exit 1; fi
    )
done
place_runtime_cmd() {
    local dest="$1" src="${2:-}"
    [ -n "$src" ] && [ -e "$src" ] || return 0
    [ ! -e "$dest" ] || return 0
    printf '#!/bin/sh\nexec %s "$@"\n' "$(printf "'%s'" "$(printf '%s' "$src" | sed "s/'/'\\\\''/g")")" > "$dest"
    chmod +x "$dest"
}
if command -v node >/dev/null 2>&1; then
    NODE_ONLY="$SANDBOX/node-only-bin"
    mkdir -p "$NODE_ONLY"
    for name in node bash sh mktemp rm cat chmod mkdir ln uname tr wc head cp mv env true false grep sed awk; do
        src=$(type -P "$name" 2>/dev/null || true)
        place_runtime_cmd "$NODE_ONLY/$name" "$src"
    done
    (
        PATH="$NODE_ONLY"
        command -v python3 >/dev/null 2>&1 && { echo "python3 leaked into node-only PATH"; exit 1; }
        command -v node >/dev/null 2>&1 || { echo "node missing from node-only PATH"; exit 1; }
        BOOTSTRAP_RELEASE_TAG=v1.42.0
        unset BOOTSTRAP_RELEASE_COMMIT INSTALL_SCRIPT_URL
        BOOTSTRAP_RAW_BASE_URL="https://raw.example.invalid/repository"
        fetch_bootstrap_commit_file() { printf '%s' "$TEST_RELEASE_COMMIT" > "$2"; }
        resolve_bootstrap_release
        [ -z "$BOOTSTRAP_COMMIT" ]
        resolve_bootstrap_commit
        [ "$BOOTSTRAP_COMMIT" = "$TEST_RELEASE_COMMIT" ]
        [ "$(select_bootstrap_install_script)" = "$BOOTSTRAP_RAW_BASE_URL/$TEST_RELEASE_COMMIT/bin/install.sh" ]
    )
    echo "node-only resolve_bootstrap_release passed"
    STUB_BIN="$SANDBOX/store-stub-bin"
    mkdir -p "$STUB_BIN"
    printf '%s\n' '#!/bin/sh' \
        'echo "Python was not found; run without arguments to install from the Microsoft Store." >&2' \
        'exit 9009' > "$STUB_BIN/python3"
    chmod +x "$STUB_BIN/python3"
    (
        PATH="$STUB_BIN:$NODE_ONLY"
        command -v python3 >/dev/null 2>&1 || { echo "stub python3 missing from PATH"; exit 1; }
        python3 -I -c 'import json' >/dev/null 2>&1 && { echo "stub python3 unexpectedly usable"; exit 1; }
        command -v node >/dev/null 2>&1 || { echo "node missing from stub+node PATH"; exit 1; }
        rt=$(installer_runtime)
        [ "$rt" = node ] || { echo "installer_runtime=$rt"; exit 1; }
        BOOTSTRAP_RELEASE_TAG=v1.42.0
        unset BOOTSTRAP_RELEASE_COMMIT INSTALL_SCRIPT_URL
        BOOTSTRAP_RAW_BASE_URL="https://raw.example.invalid/repository"
        fetch_bootstrap_commit_file() { printf '%s' "$TEST_RELEASE_COMMIT" > "$2"; }
        resolve_bootstrap_release
        [ -z "$BOOTSTRAP_COMMIT" ]
        resolve_bootstrap_commit
        [ "$BOOTSTRAP_COMMIT" = "$TEST_RELEASE_COMMIT" ]
        [ "$(select_bootstrap_install_script)" = "$BOOTSTRAP_RAW_BASE_URL/$TEST_RELEASE_COMMIT/bin/install.sh" ]
    )
    echo "stub python3 falls back to node in installer_runtime"
fi
unset BOOTSTRAP_RELEASE_TAG BOOTSTRAP_RELEASE_COMMIT BOOTSTRAP_RAW_BASE_URL INSTALL_SCRIPT_URL
# The production archive endpoint accepts a commit SHA directly, outside refs/tags.
(
    PRODUCT=codex
    BOOTSTRAP_TAG=v1.42.0
    BOOTSTRAP_COMMIT="$TEST_RELEASE_COMMIT"
    TMPDIR="$SANDBOX/archive-test"; mkdir -p "$TMPDIR"
    request="$TMPDIR/request"
    fetch_bootstrap_file() { printf '%s\n' "$1" > "$request"; return 1; }
    install_cleanup_traps
    if install_codex; then exit 1; fi
    [ "$(cat "$request")" = "https://github.com/${REPO}/archive/$TEST_RELEASE_COMMIT.tar.gz" ]
)
INSTALL_SCRIPT_URL=""
BOOTSTRAP_TAG=v1.43.0
BOOTSTRAP_COMMIT="$TEST_RELEASE_COMMIT"
BOOTSTRAP_RAW_CONTENT_URL="http://example.test"
MANAGED_INSTALL_SCRIPT_URL="http://example.test/main/bin/install.sh"
[ "$(select_bootstrap_install_script)" = "http://example.test/$TEST_RELEASE_COMMIT/bin/install.sh" ]
mkdir -p "$(bootstrap_control_root)"
printf '{}\n' > "$(bootstrap_control_root)/ownership.json"
[ "$(select_bootstrap_install_script)" = "http://example.test/main/bin/install.sh" ]
INSTALL_SCRIPT_URL="http://example.test/override.sh"
[ "$(select_bootstrap_install_script)" = "http://example.test/override.sh" ]
rm -f "$(bootstrap_control_root)/ownership.json"
INSTALL_SCRIPT_URL=""
legacy="$SANDBOX/legacy-cli"
printf '%s\n' '#!/bin/sh' 'echo "setup-codex [--print] [--dry-run] [--codex-home <dir>] [--plugin-root <dir>]"' > "$legacy"
chmod +x "$legacy"
if cli_has_setup_codex_skip_agent_notify "$legacy"; then echo "legacy advertised skip"; exit 1; fi
if cli_has_setup_notifications "$legacy"; then echo "legacy advertised setup-notifications"; exit 1; fi
if cli_has_setup_wizard "$legacy"; then echo "legacy advertised wizard"; exit 1; fi
capable="$SANDBOX/capable-cli"
printf '%s\n' '#!/bin/sh' 'echo "[--agent-notify|--skip-agent-notify]"' 'echo "setup-notifications [--help]"' > "$capable"
chmod +x "$capable"
cli_has_setup_codex_skip_agent_notify "$capable" || { echo "capable missing skip"; exit 1; }
cli_has_setup_notifications "$capable" || { echo "capable missing setup-notifications"; exit 1; }
if cli_has_setup_wizard "$capable"; then echo "narrow help advertised wizard"; exit 1; fi
wizard="$SANDBOX/wizard-cli"
printf '%s\n' '#!/bin/sh' 'echo "setup-notifications wizard"' > "$wizard"
chmod +x "$wizard"
cli_has_setup_wizard "$wizard" || { echo "wizard-cli missing wizard"; exit 1; }
read -r _os _arch < <(bootstrap_release_os_arch)
case "$_os" in linux|darwin|windows) ;; *) echo "unexpected os $_os"; exit 1 ;; esac
case "$_arch" in amd64|arm64) ;; *) echo "unexpected arch $_arch"; exit 1 ;; esac
mkdir -p "$SANDBOX/portable-src"
_asset="agent-notify-portable-${_os}-${_arch}.zip"
printf 'portable-zip-fixture' > "$SANDBOX/portable-src/$_asset"
python3 -I - "$SANDBOX/portable-src" "$_asset" <<'PY'
import hashlib, pathlib, sys
root, name = pathlib.Path(sys.argv[1]), sys.argv[2]
digest = hashlib.sha256((root/name).read_bytes()).hexdigest()
(root/'checksums.txt').write_text(digest+'  '+name+'\n')
PY
BOOTSTRAP_TAG=v1.43.0
fetch_bootstrap_file() { cp "$SANDBOX/portable-src/$(basename "$1")" "$2"; }
acquire_wizard_portable_asset
[ "$WIZARD_PACKAGE_ROOT" = "$_PORTABLE_STAGE/$_asset" ] || { echo "portable asset path $WIZARD_PACKAGE_ROOT"; exit 1; }
rm -rf "$_PORTABLE_STAGE"
_PORTABLE_STAGE=""
mkdir -p "$SANDBOX/temp" "$SANDBOX/config-stage"
printf 'preflight-state' > "$SANDBOX/config-stage/baseline"
printf '%s\n' '#!/bin/sh' 'if [ "$1 $2" = "setup-notifications --help" ]; then echo "--policy-only --preserve-enabled"; exit 0; fi' 'exit 1' > "$SANDBOX/fail-wizard"
chmod +x "$SANDBOX/fail-wizard"
(
    set +e
    export TMPDIR="$SANDBOX/temp"
    _CONFIG_STAGE="$SANDBOX/config-stage"
    CONFIGURE_BINARY="$SANDBOX/fail-wizard"
    PLUGIN_ROOT="$SANDBOX/runtime"
    PRODUCT=claude
    CONFIGURE_ARGS=()
    configure_agent_policy() { return 0; }
    bootstrap_abs_command() { return 1; }
    install_cleanup_traps
    setup_agent_notify_wizard > "$SANDBOX/retry.log" 2>&1
    [ "$?" -ne 0 ]
    printf '%s\n' "$WIZARD_PACKAGE_ROOT" > "$SANDBOX/retry-package-path"
)
_retry_package=$(cat "$SANDBOX/retry-package-path")
[ -f "$_retry_package" ] || { echo "retry package removed: $_retry_package"; exit 1; }
[ ! -e "$SANDBOX/config-stage" ] || { echo 'preflight state retained with portable asset'; exit 1; }
[ ! -s "$SANDBOX/retry.log" ]
BOOTSTRAP_TAG=""
WIZARD_PACKAGE_ROOT=""
# setup_marketplace self-heals a marketplace declared under a retired repo
# name, but leaves an unrelated source conflict alone.
(
    # shellcheck disable=SC2034 # consumed by the sourced setup_marketplace
    MARKETPLACE_SOURCE="new/repo"
    # shellcheck disable=SC2034
    MARKETPLACE_NAME="claude-notifications-go"
    config_preflight() { :; }
    calls="$SANDBOX/marketplace-calls"; declared_repo="777genius/claude-notifications-go"
    marketplace_declared_repo() { printf '%s\n' "$declared_repo"; }
    claude() {
        printf '%s\n' "$*" >> "$calls"
        if [ "$1 $2 $3" = "plugin marketplace add" ]; then
            if [ ! -f "$SANDBOX/marketplace-add-called" ]; then
                touch "$SANDBOX/marketplace-add-called"
                echo "Failed to add marketplace: its network source differs from the one declared for it in settings" >&2
                return 1
            fi
            return 0
        elif [ "$1 $2 $3" = "plugin marketplace list" ]; then
            printf '[{"name":"claude-notifications-go","repo":"%s"}]\n' "$declared_repo"
            return 0
        elif [ "$1 $2 $3" = "plugin marketplace remove" ]; then
            return 0
        fi
        return 0
    }
    for declared_repo in 777genius/claude-notifications-go 777genius/agent-notifications; do
        : > "$calls"; rm -f "$SANDBOX/marketplace-add-called"
        setup_marketplace
        [ "$(grep -c '^plugin marketplace add' "$calls")" = 2 ] || { echo "expected retry add after self-heal"; exit 1; }
        grep -q '^plugin marketplace remove claude-notifications-go$' "$calls" || { echo "expected self-heal remove"; exit 1; }
    done

    declared_repo="someone-else/unrelated-fork"
    : > "$calls"; rm -f "$SANDBOX/marketplace-add-called"
    setup_marketplace
    [ "$(grep -c '^plugin marketplace add' "$calls")" = 1 ] || { echo "unrelated conflict must not retry add"; exit 1; }
    if grep -q '^plugin marketplace remove' "$calls"; then echo "unrelated conflict must not self-heal"; exit 1; fi
)
echo "marketplace self-heal fixtures passed"
# macOS resource refresh outside the cache must also protect explicit config.
(
    PRODUCT=claude
    _CONFIG_STAGE="$SANDBOX/preflight-resource"; mkdir "$_CONFIG_STAGE"
    printf '{"plugins":{}}\n' > "$_CONFIG_STAGE/installed-before.json"
    uname() { printf 'Darwin\n'; }
    capture_preflight() {
        [ "$1 $2 $3" = 'config installer bootstrap' ] || return 1
        [ "${13}" = "$HOME/.claude/claude-notifications-go/iterm2-venv" ] || return 1
        printf '{"status":"safe"}\n'
    }
    _CONFIG_HELPER=capture_preflight
    config_preflight
)
# Dispatch tests preserve shared bundle state and isolate CN_PRODUCT.
# Exercise Gemini's actual bootstrap route before replacing dispatcher effects.
# A captured protocol function is the artifact seam; no host CLI is launched.
(
    uname() { printf 'Linux\n'; }
    bootstrap_release_os_arch() { printf 'linux amd64\n'; }
    gemini_control="$SANDBOX/gemini control ' \$literal"
    bootstrap_control_root() { printf '%s\n' "$gemini_control"; }
    PRODUCT=gemini
    OPENCODE_ARGS=(--webhook)
    _CONFIG_HELPER=capture_gemini
    capture_gemini() {
        case "$1 $2" in
            'setup-gemini install')
                printf '%s\n' "$@" > "$SANDBOX/gemini-argv"
                [ "${GEMINI_INSTALL_FAIL:-0}" = 0 ] ;;
            'config installer') printf '%s\n' "$SANDBOX/gemini runtime" ;;
            'config path') printf '%s\n' "$SANDBOX/shared config.json" ;;
            *) return 1 ;;
        esac
    }
    initialize_config() { printf initialized > "$SANDBOX/gemini-init"; }
    install_gemini > "$SANDBOX/gemini-output"
    expected=$(printf '%s\n' setup-gemini install --binary capture_gemini --webhook --runtime-root "$gemini_control/runtime")
    [ "$(cat "$SANDBOX/gemini-argv")" = "$expected" ] || { echo "Gemini bootstrap lost its actual API or consent" >&2; exit 1; }
    [ -f "$SANDBOX/gemini-init" ]
    grep -F 'Enable/trust the user hooks' "$SANDBOX/gemini-output"
    grep -F 'configure and enable its destination' "$SANDBOX/gemini-output"
    if grep -E 'Shared settings:|Inspect registration|Remove:' "$SANDBOX/gemini-output"; then exit 1; fi
    BOOTSTRAP_VERBOSE=1 install_gemini > "$SANDBOX/gemini-output"
    # Execute the public lifecycle strings against an inert argv recorder with
    # a different default environment. No candidate/helper/native host runs.
    mkdir -p "$SANDBOX/gemini runtime" "$SANDBOX/other default"
    printf 'untouched default\n' > "$SANDBOX/other default/canary"
    export GEMINI_LIFECYCLE_TRACE="$SANDBOX/gemini-lifecycle-argv"
    cat > "$SANDBOX/gemini runtime/claude-notifications-linux-amd64" <<'LIFECYCLE_ARGV'
#!/bin/bash
set -eu
printf '%s\n' "$@" > "$GEMINI_LIFECYCLE_TRACE"
LIFECYCLE_ARGV
    chmod +x "$SANDBOX/gemini runtime/claude-notifications-linux-amd64"
    for label_action in 'Inspect registration and consent:|inspect' 'Remove:|remove'; do
        label=${label_action%|*}; action=${label_action#*|}
        printed=$(sed -n "s/^[[:space:]]*$label //p" "$SANDBOX/gemini-output")
        [ -n "$printed" ]
        XDG_CONFIG_HOME="$SANDBOX/other default" bash -c "$printed"
        expected=$(printf '%s\n' setup-gemini "$action" --control-root "$gemini_control")
        [ "$(cat "$GEMINI_LIFECYCLE_TRACE")" = "$expected" ] || { echo "Gemini printed $action lost the actual control root" >&2; exit 1; }
    done
    # The permission branch must address the same installation. The artifact
    # seam accepts an inert attestation; no helper or permission API is run.
    uname() { printf 'Darwin\n'; }
    bootstrap_release_os_arch() { printf 'darwin amd64\n'; }
    OPENCODE_ARGS=(--desktop)
    _CONFIG_STAGE="$SANDBOX/inert native staging"
    mkdir -p "$_CONFIG_STAGE/ClaudeNotifier.app"
    touch "$_CONFIG_STAGE/ClaudeNotifier.app.managed-runtime.json"
    cp "$SANDBOX/gemini runtime/claude-notifications-linux-amd64" "$SANDBOX/gemini runtime/claude-notifications-darwin-amd64"
    BOOTSTRAP_VERBOSE=1 install_gemini > "$SANDBOX/gemini-output"
    for label_action in 'Check permission:|permission-status' 'Grant if needed:|request-permission'; do
        label=${label_action%|*}; action=${label_action#*|}
        printed=$(sed -n "s/^[[:space:]]*$label //p" "$SANDBOX/gemini-output")
        [ -n "$printed" ]
        XDG_CONFIG_HOME="$SANDBOX/other default" bash -c "$printed"
        expected=$(printf '%s\n' setup-gemini "$action" --control-root "$gemini_control")
        [ "$(cat "$GEMINI_LIFECYCLE_TRACE")" = "$expected" ] || { echo "Gemini printed $action lost the actual control root" >&2; exit 1; }
    done
    # Exercise the actual Windows copy-and-execute branch on this host with an
    # inert executable fixture. The query/render boundary supplies native paths.
    uname() { printf 'MINGW64_NT\n'; }
    bootstrap_release_os_arch() { printf 'windows amd64\n'; }
    native_control="C:/Gemini TEST control ' \$literal"
    cygpath() {
        # The binary needs native backslashes; rendered control paths use -m.
        # Both cross this inert argv boundary without executing a native host.
        [ "$1" = -m ] || [ "$1" = -w ] || return 1
        case "$2" in
            "$gemini_control") printf '%s\n' "$native_control" ;;
            "$gemini_control/runtime") printf '%s/runtime\n' "$native_control" ;;
            *) printf '%s\n' "$2" ;;
        esac
    }
    OPENCODE_ARGS=(--webhook)
    cat > "$SANDBOX/gemini runtime/claude-notifications-windows-amd64.exe" <<'WINDOWS_REMOVER_ARGV'
#!/bin/bash
set -eu
[ "${0##*/}" = remover.exe ] || exit 91
printf '%s\n' "$@" > "$GEMINI_LIFECYCLE_TRACE"
exit "${GEMINI_REMOVE_EXIT:-0}"
WINDOWS_REMOVER_ARGV
    chmod +x "$SANDBOX/gemini runtime/claude-notifications-windows-amd64.exe"
    BOOTSTRAP_VERBOSE=1 install_gemini > "$SANDBOX/gemini-output"
    printed=$(sed -n 's/^[[:space:]]*Remove: //p' "$SANDBOX/gemini-output")
    XDG_CONFIG_HOME="$SANDBOX/other default" bash -c "$printed"
    expected=$(printf '%s\n' setup-gemini remove --control-root "$native_control")
    [ "$(cat "$GEMINI_LIFECYCLE_TRACE")" = "$expected" ] || { echo "Windows remover lost native control-root argv" >&2; exit 1; }
    status=0
    GEMINI_REMOVE_EXIT=37 bash -c "$printed" || status=$?
    [ "$status" = 37 ] || { echo "Windows remover lost failure status" >&2; exit 1; }
    [ -f "$SANDBOX/gemini runtime/claude-notifications-windows-amd64.exe" ]
    [ -z "$(find "$TMPDIR" -maxdepth 1 -name 'agent-notifications-remove.*' -print)" ]
    [ "$(cat "$SANDBOX/other default/canary")" = 'untouched default' ]
    [ "$(find "$SANDBOX/other default" -type f | wc -l)" -eq 1 ]
    rm "$SANDBOX/gemini-init"
    GEMINI_INSTALL_FAIL=1
    if install_gemini > "$SANDBOX/gemini-output"; then
        echo "Gemini bootstrap hid installer failure" >&2; exit 1
    fi
    [ ! -e "$SANDBOX/gemini-init" ] || { echo "Config initialized after Gemini installation failed" >&2; exit 1; }
)
print_header() { :; }; abort_if_wsl_environment() { :; }
check_prerequisites() { :; }; detect_platform() { :; }
resolve_bootstrap_release() { BOOTSTRAP_TAG=v1.43.2; }
install_claude() { [ "$CN_PRODUCT" = claude ]; PLUGIN_ROOT='bundle space'; echo claude >> "$SANDBOX/calls"; }
install_codex() { [ "${CN_PRODUCT:-}" = sentinel ]; echo codex >> "$SANDBOX/calls"; }
cat > "$SANDBOX/repair-helper" <<'REPAIR_PEER'
#!/bin/bash
case "$1 ${2:-}" in
    'setup-products repair-capabilities') printf 'cache-repair-v1\n' ;;
    'setup-products repair-cache') printf 'ready\n' ;;
    *) exit 99 ;;
esac
REPAIR_PEER
chmod +x "$SANDBOX/repair-helper"
stage_historical_baselines() { :; }; stage_config_helper() { _CONFIG_STAGE=$(mktemp -d); _CONFIG_HELPER="$SANDBOX/repair-helper"; }; config_preflight() { :; }; initialize_config() { :; }
export CN_PRODUCT=sentinel
for product in claude codex both; do
    : > "$SANDBOX/calls"
    PRODUCT=""; main --product "$product"
    case "$product" in
        claude) [ "$(cat "$SANDBOX/calls")" = claude ] ;;
        codex) [ "$(cat "$SANDBOX/calls")" = codex ] ;;
        both) [ "$(cat "$SANDBOX/calls")" = "$(printf 'claude\ncodex')" ]; [ "$PLUGIN_ROOT" = 'bundle space' ] ;;
    esac
done
install_codex() { return 1; }
if ( PRODUCT=""; main --product both ); then exit 1; fi
# main installs its own trap; restore test-owned sandbox cleanup.
trap 'rm -rf "$SANDBOX"' EXIT
# The profile directory and default Claude registration file are different.
PRODUCT=claude
BOOTSTRAP_TAG=""
PLUGIN_ROOT="$SANDBOX/wizard-package"
mkdir -p "$PLUGIN_ROOT/portable-package"
printf '{}' > "$PLUGIN_ROOT/portable-package/plugin.json"
CONFIGURE_BINARY="$SANDBOX/capture-wizard"
export WIZARD_CAPTURE="$SANDBOX/wizard-args"
printf '%s\n' '#!/bin/bash' 'if [ "$1 $2" = "setup-notifications --help" ]; then echo "--policy-only --preserve-enabled"; exit 0; fi' 'if [ "${4:-}" = inspect ]; then printf '"'"'{"targets":[]}\n'"'"'; exit 0; fi' 'printf "%s\n" "$@" > "$WIZARD_CAPTURE"' > "$CONFIGURE_BINARY"
chmod +x "$CONFIGURE_BINARY"
configure_agent_policy() { return 0; }
bootstrap_abs_command() { return 1; }
unset CLAUDE_CONFIG_DIR
setup_agent_notify_wizard
grep -Fx -- "$HOME/.claude.json" "$WIZARD_CAPTURE"
grep -Fx -- '--install-or-update' "$WIZARD_CAPTURE"
export CLAUDE_CONFIG_DIR="$SANDBOX/custom claude"
setup_agent_notify_wizard
grep -Fx -- "$CLAUDE_CONFIG_DIR/.claude.json" "$WIZARD_CAPTURE"

# Regression: invalid/incomplete multi selections and pending JSON used to
# acquire a helper or execute host --version before rejecting the request.
# Exercise the actual script entry point with external acquisition/exec canaries.
(
    mkdir -p "$SANDBOX/early-canaries"
    for tool in curl wget claude codex opencode gemini; do
        cat > "$SANDBOX/early-canaries/$tool" <<'EARLY_CANARY'
#!/bin/bash
printf '%s\n' "$0 $*" >> "$EARLY_CANARY_LOG"
exit 99
EARLY_CANARY
        chmod +x "$SANDBOX/early-canaries/$tool"
    done
    export PATH="$SANDBOX/early-canaries:$PATH" EARLY_CANARY_LOG="$SANDBOX/early-effects"
    for args in '--products opencode,opencode' '--products claude,unknown' '--json' '--product gemini --json' '--ui=bad' '--plain --ui=rich' '--navigation none' '--agent-notify --agent-notify'; do
        status=0
        bash "$ROOT/bin/bootstrap.sh" $args >"$SANDBOX/early-output" 2>&1 || status=$?
        [ "$status" -eq 1 ] || { echo "Wrong early refusal status $status for $args" >&2; exit 1; }
        [ ! -e "$EARLY_CANARY_LOG" ] || { cat "$EARLY_CANARY_LOG" >&2; exit 1; }
    done
)
# Regression: normalization overwrote explicit caller false/false, or appended
# a second preserve-policy when the caller supplied it with an omitted route.
(
    PRODUCT=""; CONFIGURE_ARGS=(); OPENCODE_ARGS=(); _UI_MODE=""; UI_ARGS=()
    CONFIGURE_NOTIFICATIONS=true
    select_product --product claude --ui=plain --plain --navigation=none --allow-unknown-caller=false --allow-caller-asserted=false
    [ "${CONFIGURE_ARGS[*]}" = '--navigation none --allow-unknown-caller false --allow-caller-asserted false' ]
    PRODUCT=""; CONFIGURE_ARGS=(); _UI_MODE=""; UI_ARGS=()
    select_product --product claude --preserve-policy
    [ "${CONFIGURE_ARGS[*]}" = '--preserve-policy --navigation none --allow-unknown-caller true --allow-caller-asserted false' ]
)

# Loader-normalized repeated presentation flags must retain the same contract
# at the bootstrap boundary; contradictory values still fail before effects.
(
    PRODUCT=""; _ACTIVITY_MODE=compact; _ACTIVITY_EXPLICIT=false; _ACTIVITY_LIMIT=0
    select_product --product claude --activity expanded --activity=expanded --activity-limit 4 --activity-limit=4
    [ "$_ACTIVITY_MODE" = expanded ] && [ "$_ACTIVITY_LIMIT" -eq 4 ]
    PRODUCT=""; _ACTIVITY_MODE=compact; _ACTIVITY_EXPLICIT=false; _ACTIVITY_LIMIT=0
    if select_product --product claude --activity off --activity=compact 2>/dev/null; then exit 1; fi
    PRODUCT=""; _ACTIVITY_MODE=compact; _ACTIVITY_EXPLICIT=false; _ACTIVITY_LIMIT=0
    if select_product --product claude --activity-limit=4 --activity-limit 5 2>/dev/null; then exit 1; fi
)

# A session display failure must leave a safe live label for later stages.
# The actual stage begins only after that label is visible; its stdout remains
# private and no legacy rich helper is restarted.
(
    progress_rich_available() { return 0; }
    progress_helper_available() { return 0; }
    _PROGRESS_SESSION_SUPPORTED=true; _PROGRESS_JOURNAL_DISABLED=true; _PROGRESS_PID=""
    emit_after_display_failure() {
        [ -z "$_PROGRESS_PID" ]
        grep -F 'Preparing notification settings...' "$SANDBOX/disabled-session-stage.out" >/dev/null
        printf 'PRIVATE_STAGE_OUTPUT\n'
    }
    run_setup_stage 'Preparing notification settings' emit_after_display_failure > "$SANDBOX/disabled-session-stage.out" 2> "$SANDBOX/disabled-session-stage.err"
    [ "$(cat "$SANDBOX/disabled-session-stage.out")" = 'Preparing notification settings...' ]
    [ ! -s "$SANDBOX/disabled-session-stage.err" ]
)

# Frozen delivery-config paths retain the caller's implicit/explicit source.
# Repeated dispatch cannot suppress migration guards by inventing an override;
# a changed default after frozen environment mapping must refuse before writers.
(
    export TEST_FROZEN_GLOBAL="$SANDBOX/frozen delivery config.json"
    export TEST_IMPLICIT_CONFIG="$TEST_FROZEN_GLOBAL"
    export TEST_CONFIG_ATOMS="$SANDBOX/config source atoms"
    _CONFIG_STAGE=$(mktemp -d)
    _CONFIG_HELPER="$_CONFIG_STAGE/helper"
    _SELECTOR_INTENT="$_CONFIG_STAGE/selector-intent.json"
    _PRODUCT_CSV=claude,codex; SELECTED_PRODUCTS=(claude codex)
    printf '%s\0' home "$INSTALLER_HOME" control-root "$SANDBOX/config-source/agent-notifications" \
        global-config "$TEST_FROZEN_GLOBAL" claude-config "$CLAUDE_HOME" claude-mcp-config "$SANDBOX/.claude.json" \
        claude-executable "$SANDBOX/mock-claude" codex-home "$DEFAULT_CODEX_HOME" \
        codex-mcp-config "$SANDBOX/codex.toml" codex-executable "$SANDBOX/mock-codex" > "$TEST_CONFIG_ATOMS"
    cat > "$_CONFIG_HELPER" <<'CONFIG_SOURCE_PEER'
#!/bin/bash
case "$1 ${2:-}" in
    'setup-products intent-args') cat "$TEST_CONFIG_ATOMS" ;;
    'config path') printf '%s\n' "${AGENT_NOTIFICATIONS_CONFIG:-$TEST_IMPLICIT_CONFIG}" ;;
    *) exit 97 ;;
esac
CONFIG_SOURCE_PEER
    chmod +x "$_CONFIG_HELPER"
    for source in implicit explicit; do
        unset AGENT_NOTIFICATIONS_CONFIG
        if [ "$source" = explicit ]; then export AGENT_NOTIFICATIONS_CONFIG="$SANDBOX/original explicit config.json"; fi
        for repeat in 1 2; do
            load_frozen_dispatch
            [ "$_FROZEN_GLOBAL_CONFIG" = "$TEST_FROZEN_GLOBAL" ]
            if [ "$source" = implicit ]; then [ -z "${AGENT_NOTIFICATIONS_CONFIG+x}" ]
            else [ "$AGENT_NOTIFICATIONS_CONFIG" = "$TEST_FROZEN_GLOBAL" ]; fi
        done
    done
    unset AGENT_NOTIFICATIONS_CONFIG
    TEST_IMPLICIT_CONFIG="$SANDBOX/drifted config.json"
    if load_frozen_dispatch; then echo 'Frozen config drift was accepted' >&2; exit 1; fi
    [ -z "${AGENT_NOTIFICATIONS_CONFIG+x}" ]
)

# Cursor used to fail product parsing or lose its two frozen scalar arguments.
# Drive the real main/selector/wizard composition through disposable argv peers;
# no installer, vendor, provider, delivery or materializer executes here.
(
    export TEST_CORE="$SANDBOX/TEST core caller" TEST_FUNCTIONS="$SANDBOX/functions.sh"
    export TEST_RUNNER="$SANDBOX/TEST cursor main" TEST_ATOMS="$SANDBOX/TEST frozen atoms"
    export TEST_TRACE="$SANDBOX/TEST cursor trace"
    mkdir -p "$TEST_TRACE"
    cat > "$TEST_CORE" <<'CURSOR_PEER'
#!/bin/bash
set -euo pipefail
trace_name="${1#--}-${2:-none}"
[ "${2:-}" != wizard ] || trace_name="$trace_name-$4"
printf '%s\0' "$@" > "$TEST_TRACE/$trace_name"
case "$1 ${2:-}" in
    'setup-products capabilities') printf 'setup-products-v1 claude codex opencode gemini qoder cursor\n' ;;
    'setup-products features') printf 'terminal-selector-v1\n' ;;
    'setup-products repair-capabilities') printf 'cache-repair-v1\n' ;;
    'setup-products repair-cache') printf 'ready\n' ;;
    'setup-products select') printf '%s\n' "${TEST_SELECTION:-cursor}" ;;
    'setup-products channels') [ "${TEST_CANCEL:-}" = channels ] || printf '%s\n' "$TEST_CHANNELS" ;;
    'setup-products confirm'|'setup-products prepare')
        reply=approved; [ "$2" != prepare ] || reply=prepared
        [ "${TEST_CANCEL:-}" != confirm ] || exit 0
        while [ "$#" -gt 0 ]; do
            if [ "$1" = --intent-file ]; then printf 'TEST immutable checkpoint' > "$2"; break; fi
            shift
        done
        printf '%s\n' "$reply" ;;
    'setup-products intent-args') cat "$TEST_ATOMS" ;;
    'config path')
        while IFS= read -r -d '' key && IFS= read -r -d '' value; do
            if [ "$key" = global-config ]; then printf '%s\n' "$value"; exit 0; fi
        done < "$TEST_ATOMS"
        exit 97 ;;
    'setup-products preflight') exit "${TEST_PREFLIGHT_EXIT:-0}" ;;
    'setup-notifications --help') printf '%s\n' '--policy-only --preserve-enabled' ;;
    'setup-notifications wizard')
        if [ "${TEST_WIZARD_WARNING:-0}" = 1 ]; then printf '{"warning":"foreign-callback-fixture"}\n' >&2; fi
        if [ "$4" = inspect ]; then printf '{"targets":[]}\n'; else
            [ -z "${TEST_MIXED_FLOW:-}" ] || printf 'cursor\n' >> "$TEST_TRACE/ORDER"
            exit "${TEST_WIZARD_EXIT:-0}"; fi ;;
    *) exit 97 ;;
esac
CURSOR_PEER
    cat > "$TEST_RUNNER" <<'CURSOR_MAIN'
#!/bin/bash
source "$TEST_FUNCTIONS"
abort_if_wsl_environment() { :; }; detect_platform() { :; }; print_header() { :; }
check_prerequisites() { :; }
resolve_bootstrap_release() { BOOTSTRAP_TAG=v2.0.0; }
stage_config_helper() { _CONFIG_STAGE=$(mktemp -d); _CONFIG_HELPER=$TEST_CORE; }
acquire_wizard_portable_asset() { WIZARD_PACKAGE_ROOT="$TEST_TRACE/TEST package with spaces.zip"; }
# This dispatch fixture already supplies its committed observer; actual fresh
# runtime preparation is exercised by the downloaded Cursor bootstrap canary.
prepare_cursor_runtime() { _CURSOR_RUNTIME_PREPARED=false; }
bootstrap_abs_command() { printf unexpected-discovery >> "$TEST_TRACE/mutation"; return 1; }
stage_historical_baselines() { :; }; config_preflight() { :; }
parallel_install_available() { [ "${TEST_MIXED_FLOW:-}" = parallel ]; }
install_products_parallel() {
    _PARALLEL_ACTIVE=true; _PARALLEL_READY_CLIENTS=codex
    mkdir -p "$_CONFIG_STAGE/parallel"
    printf 'codex\nqoder\n' >> "$TEST_TRACE/ORDER"
}
install_legacy_products() {
    if [ "${TEST_MIXED_FLOW:-}" = sequential ]; then printf 'codex\n' >> "$TEST_TRACE/ORDER"; return 0; fi
    printf legacy >> "$TEST_TRACE/mutation"; return 99
}
install_qoder() { printf 'qoder\n' >> "$TEST_TRACE/ORDER"; }
install_opencode() { printf opencode >> "$TEST_TRACE/mutation"; return 99; }
install_gemini() { printf gemini >> "$TEST_TRACE/mutation"; return 99; }
initialize_config() { printf config >> "$TEST_TRACE/mutation"; return 99; }
main "$@"
progress_write_final_snapshot > "$TEST_TRACE/STATUS"
CURSOR_MAIN
    chmod +x "$TEST_CORE"
    python3 - "$SANDBOX" <<'PY_CURSOR'
import os, pathlib, subprocess, sys
if os.name == 'nt':
    print('Cursor controlling-TTY fixtures require POSIX PTY; other unit fixtures still run')
    sys.exit(0)
import fcntl, termios
sandbox = pathlib.Path(sys.argv[1]); trace = pathlib.Path(os.environ['TEST_TRACE'])
# Quotes, dollar signs and spaces must remain scalar bytes, never shell code.
profile = str(sandbox / "TEST frozen cursor ' $literal")
vendor = str(sandbox / 'TEST frozen vendor with spaces')
control = str(sandbox / 'TEST control space' / 'agent-notifications')
runtime = str(sandbox / 'TEST installed core space')
global_config = str(sandbox / 'TEST policy space.json')
atoms = [('home', str(sandbox / 'TEST frozen home')), ('control-root', control),
         ('runtime-root', runtime), ('global-config', global_config),
         ('scope-root', profile), ('client-executable', vendor),
         ('cursor-desktop','true'), ('cursor-webhook','false'), ('cursor-channel-preimage','absent'),('cursor-seed-enabled','true')]
requested = ['--scope-root', str(sandbox / 'TEST requested cursor'),
             '--client-executable', str(sandbox / 'TEST requested vendor')]
def run(args, changes=None, raw=None):
    for file in trace.iterdir(): file.unlink()
    pathlib.Path(os.environ['TEST_ATOMS']).write_bytes(raw if raw is not None else
        b''.join(k.encode()+b'\0'+v.encode()+b'\0' for k,v in atoms))
    master, slave = os.openpty()
    def terminal():
        os.setsid(); fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
    env = dict(os.environ, TEST_CHANNELS='desktop,webhook', **(changes or {}))
    process = subprocess.Popen([os.environ['TEST_BOOTSTRAP_BASH'], os.environ['TEST_RUNNER']]+args,
        env=env, stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, preexec_fn=terminal)
    os.close(slave)
    try: output, _ = process.communicate(timeout=20)
    finally:
        if process.poll() is None: process.kill(); process.communicate()
        os.close(master)
    calls = [p.read_bytes().split(b'\0')[:-1] for p in trace.iterdir() if p.name not in ('mutation','ORDER','STATUS')]
    return process.returncode, calls, output.decode(errors='replace')
def call(calls, prefix):
    matches = [c for c in calls if c[:len(prefix)] == [s.encode() for s in prefix]]
    assert matches, (prefix, calls)
    return matches[0]
def value(args, key): return args[args.index(key.encode())+1]
for selection in (['--product', 'cursor'], ['--products', 'cursor'], []):
    for channels in (['--desktop'], ['--webhook'], ['--desktop', '--webhook'], []):
        code, calls, output = run(selection+requested+['--agent-notify']+channels)
        # Old parsing rejected Cursor; dropped CSV/selector IDs prevented dispatch.
        assert code == 0, output
        confirm = call(calls, ['setup-products', 'confirm' if not selection else 'prepare'])
        # Original explicit scope must reach confirmation before frozen argv replaces it.
        assert value(confirm, '--scope-root') == requested[1].encode()
        assert value(confirm, '--client-executable') == requested[3].encode()
        # Desktop-only/webhook-only must not broaden to the absent sibling channel.
        for flag in ('--desktop', '--webhook'):
            assert (flag.encode() in confirm) == (flag in channels)
        preflight = call(calls, ['setup-products', 'preflight'])
        install = call(calls, ['setup-notifications', 'wizard', '--action', 'install'])
        inspect = call(calls, ['setup-notifications', 'wizard', '--action', 'inspect'])
        # Writers must use the original immutable checkpoint and selected authorities.
        assert value(install, '--bootstrap-intent-file') == value(confirm, '--intent-file') == value(preflight, '--intent-file')
        for args in (install, inspect):
            for key, expected in (('--scope-root', profile), ('--client-executable', vendor),
                    ('--control-root', control), ('--runtime-root', runtime), ('--global-config', global_config), ('--agents', 'cursor')):
                assert value(args, key) == expected.encode(), (key, args)
            # Vendor must never become observer; the caller derives installed primary.
            assert not any(key.encode() in args for key in ('--helper', '--claude-config', '--codex-home', '--claude-mcp-config', '--mcp-config', '--plugin-root'))
        # Only the approved notify unit grammar may enter the caller; no legacy hooks.
        assert value(install, '--hooks') == b'false' and value(install, '--agent-notify') == b'true'
        assert '--install-or-update'.encode() in install and not (trace/'mutation').exists()
# Logging refusal must remain visible after otherwise successful stdout-only
# stages, without inventing a saved log or exposing the machine inspection JSON.
blocked_log = sandbox/'TEST blocked diagnostic root'
blocked_log.write_text('not a directory')
for warning in ('0', '1'):
    code, calls, output = run(['--product', 'cursor']+requested+['--agent-notify', '--desktop'],
                             {'BOOTSTRAP_DIAGNOSTIC_ROOT': str(blocked_log), 'TEST_WIZARD_WARNING': warning})
    assert code == 0 and 'Diagnostics could not be fully saved' in output, output
    assert ('Maintenance: installer reported additional details.' in output) == (warning == '1'), output
    assert not any(value in output for value in ('Diagnostics:', 'saved in Diagnostics', '"targets"', 'foreign-callback-fixture')), output
# Cancellation at either question must finish without writing or granting consent.
for cancel in ('confirm',):
    code, calls, output = run(requested, {'TEST_CANCEL': cancel})
    assert code == 0 and not any(c[:1] == [b'setup-notifications'] for c in calls), output
    # Cancellation must also stop legacy/observer/config writes and discovery.
    assert not (trace/'mutation').exists() and 'Boost your productivity' not in output, output
# A skipped MCP request must not be converted to native consent or a wizard write.
code, calls, output = run(['--product', 'cursor']+requested+['--skip-agent-notify', '--desktop'])
assert code == 0 and not any(c[:1] == [b'setup-notifications'] for c in calls), output
# A skipped unit must not run another installer/config writer before returning.
assert not (trace/'mutation').exists(), output
assert b'cursor\tcomplete\tskipped\n' in (trace/'STATUS').read_bytes(), output
assert 'Cursor' in output and 'skipped; existing setup kept' in output and 'restart required' not in output, output
valid = b''.join(k.encode()+b'\0'+v.encode()+b'\0' for k,v in atoms)
# Duplicate, unknown, relative, missing or truncated frozen scalars must stop before writers.
for raw in (valid+b'scope-root\0'+profile.encode()+b'\0', valid+b'invented-key\0/TEST\0',
        valid.replace(vendor.encode(), b'relative vendor'), valid.rsplit(b'client-executable\0',1)[0],
        valid.replace(b'scope-root\0'+profile.encode()+b'\0', b''), valid[:-1]):
    code, calls, output = run(['--product', 'cursor']+requested+['--desktop'], raw=raw)
    assert code != 0 and not any(c[:1] == [b'setup-notifications'] for c in calls), output
    # A writer followed by failure could otherwise pass the malformed refusal.
    assert not (trace/'mutation').exists(), output
# A changed checkpoint or denied materializer must retain failure, never report success.
for changes in ({'TEST_PREFLIGHT_EXIT':'73'}, {'TEST_WIZARD_EXIT':'77'}):
    code, calls, output = run(['--product', 'cursor']+requested+['--webhook'], changes)
    assert code in (73,77) and not any(c[:4] == [b'setup-notifications',b'wizard',b'--action',b'inspect'] for c in calls), output
    # Checkpoint/caller refusal must not enter unrelated installers/config writes.
    assert not (trace/'mutation').exists(), output
# Missing/duplicate selected scope, mixed products and navigation must fail before acquisition.
for args in (['--product','cursor']+requested[:2],
        ['--product','cursor']+requested+requested[:2],
        ['--product','claude']+requested, ['--product','cursor']+requested+['--navigation','none']):
    code, calls, output = run(args)
    assert code != 0 and not calls and not (trace/'mutation').exists(), output
# Actual common dispatch keeps Cursor after both sequential and parallel siblings.
mixed = atoms + [('codex-home', str(sandbox/'TEST-codex')),
    ('codex-mcp-config',str(sandbox/'TEST-codex'/'config.toml')),
    ('codex-executable',str(sandbox/'TEST-codex-cli')),
    ('qoder-config-root',str(sandbox/'TEST-qoder')),
    ('qoder-executable',str(sandbox/'TEST-qoder-cli')),
    ('qoder-desktop','true'),('qoder-webhook','false'),('qoder-channel-preimage','absent')]
for flow in ('sequential','parallel'):
    code,calls,output = run(['--products','codex,qoder,cursor','--desktop'],
        {'TEST_MIXED_FLOW':flow}, raw=b''.join(k.encode()+b'\0'+v.encode()+b'\0' for k,v in mixed))
    assert code == 0, output
    assert (trace/'ORDER').read_text().splitlines() == ['codex','qoder','cursor'], output
    install = call(calls,['setup-notifications','wizard','--action','install'])
    assert value(install,'--agents') == b'cursor' and b'--bootstrap-ready-clients' not in install
    assert len([c for c in calls if c[:2] == [b'setup-products',b'prepare']]) == 1
    assert not any(c[:2] == [b'setup-products',b'confirm'] for c in calls)
print('Cursor frozen bootstrap argv and refusal fixtures passed (composition only)')
PY_CURSOR
)

printf 'bootstrap product unit fixtures passed\n'
# Detect lost shell state, leaked observers and animation interleaved with advice.
python3 -I - "$SANDBOX" "$TEST_BOOTSTRAP_BASH" <<'PY_PROGRESS'
import errno, os, select, signal, subprocess, sys, time
from pathlib import Path
if os.name == 'nt':
    print('SKIP shell progress PTY fixtures on native Windows')
    sys.exit(0)
import pty
sandbox, bash = Path(sys.argv[1]), sys.argv[2]
helper = sandbox / 'progress-observer'
helper.write_text('''#!/bin/bash
set -eu
[ "$1" = installer-progress ] || exit 2
shift
if [ "$1" = --capabilities ]; then
    [ "${TEST_PROGRESS_OLD:-0}" != 1 ] || exit 1
    printf 'installer-progress-v1\\n'; exit 0
fi
[ "$1" = --events ] || exit 2
[ "${TEST_PROGRESS_BROKEN:-0}" != 1 ] || { echo 'TEST renderer unavailable' >&2; exit 7; }
file=$2
printf '%s\\n' "$$" > "$TEST_PROGRESS_PID"
printf 'OBSERVER_START\\n' >&2
trap 'echo OBSERVER_INTERRUPTED >&2; exit 0' TERM INT HUP
while ! grep -q '^stop' "$file"; do sleep 0.02; done
cp "$file" "$TEST_PROGRESS_COPY"
printf 'OBSERVER_STOP\\n' >&2
''', newline='\n')
helper.chmod(0o700)
script = sandbox / 'progress-lifecycle.sh'
script.write_text('''#!/bin/bash
source "$TEST_PROGRESS_SOURCE"
install_cleanup_traps
_UI_MODE=${TEST_PROGRESS_MODE:-auto}
_CONFIG_HELPER=${TEST_PROGRESS_HELPER:-}
if [ "${TEST_PROGRESS_STOP_FAIL:-0}" = 1 ]; then
    printf() {
        case "$1" in stop*) return 1 ;; esac
        builtin printf "$@"
    }
fi
_PROGRESS_IDS=(claude codex gemini)
_PROGRESS_PHASES=(registration pending pending)
_PROGRESS_OUTCOMES=(state_requires_inspection not_started not_started)
emit_stage() {
    PLUGIN_ROOT=fixture-survived
    progress_phase claude config registration_committed
    if [ -z "$_CONFIG_HELPER" ]; then
        printf abc > "$TMPDIR/download"
        progress_download_activity "$TMPDIR/download"
    fi
    sleep "${TEST_PROGRESS_DELAY:-0.3}"
    printf 'warning: visible fixture warning\\n\\033]0;FOREIGN_CALLBACK_PATH\\007{"unsafe":true}\\n' >&2
    printf 'hidden technical stdout\\n'
    return "${TEST_PROGRESS_FAILURE:-0}"
}
emit_preparation() { printf 'preceding source proof\\n'; }
_UI_MODE=plain run_setup_stage 'Preparing fixture source' emit_preparation
status=0
run_setup_stage 'Installing Claude notifications' emit_stage || status=$?
[ "$PLUGIN_ROOT" = fixture-survived ] || exit 91
[ "${_PROGRESS_OUTCOMES[0]}" = registration_committed ] || exit 92
[ -z "$_PROGRESS_PID" ] && [ -z "$_PROGRESS_EVENTS" ] || exit 93
printf 'STATE_SURVIVED\\n'
if [ "${TEST_PROGRESS_SUMMARY:-0}" = 1 ]; then
    BOOTSTRAP_SUMMARY_FILE="$TMPDIR/summary"
    PRODUCT=both
    CLAUDE_AGENT_NOTIFY_STATUS='not installed (existing opt-out kept)'
    CODEX_AGENT_NOTIFY_STATUS=installed
    print_iterm2_python_api_notice() { :; }
    print_success
    printf '%s\\n' '  OpenCode - installed; restart required to load the global plugin.' \
        '  Gemini CLI - installed; restart required.' \
        '  Enable/trust the user hooks in Gemini CLI after restart.' \
        "  Gemini desktop sources may duplicate alerts; choose one source." \
        '  Webhook consent recorded; configure and enable its destination.' \
        '  warning: preserved summary advice' >> "$BOOTSTRAP_SUMMARY_FILE"
    cp "$BOOTSTRAP_SUMMARY_FILE" "$TMPDIR/summary-before"
    _PROGRESS_IDS=(claude codex opencode gemini)
    _PROGRESS_PHASES=(complete complete complete complete)
    _PROGRESS_OUTCOMES=(completed completed completed completed)
    if [ "${TEST_PROGRESS_SUMMARY_TRANSFORM_FAIL:-0}" = 1 ]; then awk() { return 7; }; fi
    if [ "${TEST_PROGRESS_SUMMARY_REPLACE_FAIL:-0}" = 1 ]; then mv() { return 7; }; fi
fi
if [ "$status" -eq 0 ]; then report_progress 0; report_diagnostics; else report_setup_failure "$status"; fi
if [ "${BOOTSTRAP_VERBOSE:-0}" = 1 ]; then bootstrap_diagnostics view "$_DIAGNOSTIC_LOG" >&2; fi
exit "$status"
''', newline='\n')
def run(name, mode='auto', old=False, color=False, terminal=True, helper_path=helper, failure=0, cancel=False, broken=False, stop_failure=False, summary=False, summary_transform_failure=False, summary_replace_failure=False, verbose=False, dumb=False, narrow=False):
    tmp = sandbox / ('progress-' + name); tmp.mkdir()
    env = os.environ.copy()
    env.update(TERM='dumb' if dumb else 'xterm-256color', TMPDIR=str(tmp), BOOTSTRAP_DIAGNOSTIC_ROOT=str(sandbox/('durable-'+name)), TEST_PROGRESS_SOURCE=str(sandbox/'functions.sh'),
               TEST_PROGRESS_MODE=mode, TEST_PROGRESS_HELPER=str(helper_path or ''),
               TEST_PROGRESS_SUMMARY=str(int(summary)), TEST_PROGRESS_SUMMARY_TRANSFORM_FAIL=str(int(summary_transform_failure)),
               TEST_PROGRESS_SUMMARY_REPLACE_FAIL=str(int(summary_replace_failure)), BOOTSTRAP_VERBOSE=str(int(verbose)),
               TEST_PROGRESS_OLD=str(int(old)), TEST_PROGRESS_STOP_FAIL=str(int(stop_failure)), TEST_PROGRESS_BROKEN=str(int(broken)), TEST_PROGRESS_FAILURE=str(failure),
               TEST_PROGRESS_PID=str(tmp/'pid'), TEST_PROGRESS_COPY=str(tmp/'events'))
    env.pop('NO_COLOR', None)
    if color: env['NO_COLOR'] = '1'
    if cancel: env['TEST_PROGRESS_DELAY'] = '5'
    master, slave = pty.openpty() if terminal else (None, None)
    if narrow:
        import fcntl, struct, termios
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 32, 0, 0))
    proc = subprocess.Popen([bash, str(script)], env=env, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=slave if terminal else subprocess.PIPE,
                            start_new_session=True)
    if slave is not None: os.close(slave)
    data, deadline, cancelled = bytearray(), time.monotonic() + 8, False
    def drain():
        if master is not None and select.select([master], [], [], 0.02)[0]:
            try: data.extend(os.read(master, 65536))
            except OSError as err:
                if err.errno != errno.EIO: raise
    while proc.poll() is None:
        drain()
        if cancel and not cancelled and b'OBSERVER_START' in data:
            os.killpg(proc.pid, signal.SIGTERM); cancelled = True
        assert time.monotonic() < deadline, (name, 'observer failed to join', data)
        time.sleep(0.01)
    out, err = proc.communicate(timeout=1)
    if master is not None:
        drain(); os.close(master); err = bytes(data)
    assert b'\x1b' not in out, (name, 'animation leaked to stdout', out)
    assert not list(tmp.glob('bootstrap-progress-*')), (name, 'event file leaked')
    assert not list(tmp.glob('bootstrap-output-*')), (name, 'captured output leaked')
    if (tmp/'pid').exists():
        observer_pid = int((tmp/'pid').read_text())
        try: os.kill(observer_pid, 0)
        except ProcessLookupError: pass
        else: raise AssertionError((name, 'observer still running', observer_pid))
    logs = list((sandbox/('durable-'+name)).glob('installer-*.log'))
    assert len(logs) == 1, (name, logs, err)
    log = logs[0]
    assert log.parent.stat().st_mode & 0o777 == 0o700 and log.stat().st_mode & 0o777 == 0o600
    captured = log.read_bytes()
    assert b'preceding source proof' in captured, (name, captured)
    if cancel:
        assert b'Exit status: interrupted' in captured and b'Diagnostics:' in err, (name, captured, err)
        assert cancelled and proc.returncode == 143, (name, proc.returncode, err)
        assert b'STATE_SURVIVED' not in out
        return
    assert proc.returncode == failure, (name, proc.returncode, out, err)
    assert b'STATE_SURVIVED' in out and b'warning: visible fixture warning' in captured
    assert b'hidden technical stdout' in captured
    if not verbose:
        assert not any(value in out+err for value in (b'visible fixture warning', b'hidden technical stdout', b'FOREIGN_CALLBACK_PATH', b'{"unsafe"')), (name, out, err)
    else:
        assert b'hidden technical stdout' in err and b'\\x1b]0;FOREIGN_CALLBACK_PATH\\x07' in err, (name, err)
    if failure:
        assert err.count(b'Setup incomplete:') == 1 and err.count(b'Next:') == 1 and err.count(b'Diagnostics:') == 1, (name, err)
    rich = terminal and mode != 'plain' and not old and not color and not verbose and not dumb
    if summary:
        result = (tmp/'summary').read_text()
        compact = rich and not broken and failure == 0 and not summary_transform_failure and not summary_replace_failure
        duplicate_rows = [
            '  Claude - installed; restart required.', '  Codex - installed; restart required.',
            '  OpenCode - installed; restart required to load the global plugin.',
            '  Gemini CLI - installed; restart required.',
        ]
        for row in duplicate_rows:
            assert (row in result.splitlines()) != compact, (name, result)
        if compact:
            assert '  Claude notification tool: not installed (existing opt-out kept).' in result, (name, result)
            assert '  Codex notification tool: installed.' not in result, (name, result)
        else:
            assert (tmp/'summary').read_bytes() == (tmp/'summary-before').read_bytes(), (name, 'fallback summary changed')
            assert result.count('    Agent notification tool: ') == 2, (name, result)
        assert 'Run /hooks in Codex, review the entries and trust them.' in result
        assert 'Enable/trust the user hooks in Gemini CLI after restart.' in result
        assert 'duplicate alerts; choose one source.' in result
        assert 'Webhook consent recorded; configure and enable its destination.' in result
        assert 'Delivery has not been verified.' in result
        assert 'warning: preserved summary advice' in result
        assert not list(tmp.glob('summary.rich-*')), (name, 'summary transform leaked temp')
    if stop_failure:
        assert err.count(b'OBSERVER_START') == 1, (name, 'incomplete final file launched observer', err)
        assert b'OBSERVER_INTERRUPTED' in err, (name, err)
        assert not (tmp/'events').exists(), (name, 'observer consumed incomplete final file')
    elif helper_path and rich and not broken and not summary:
        assert b'OBSERVER_START' in err, (name, err)
        assert err.index(b'OBSERVER_STOP') < err.index(b'Diagnostics:'), (name, err)
        rows = (tmp/'events').read_text()
        assert 'claude\tconfig\tregistration_committed\n' in rows, (name, rows)
        assert 'codex\tpending\tnot_started\n' in rows, (name, rows)
        assert 'gemini\tpending\tnot_started\n' in rows, (name, rows)
    elif not summary: assert b'OBSERVER_START' not in err, (name, err)
    if not rich: assert b'\x1b' not in err and b'\r' not in err.replace(b'\r\n', b'\n'), (name, err)
    if not helper_path: assert b'3 bytes received' in err and b'\x1b[2K' in err, (name, err)
run('rich-delayed')
run('rich-failed', failure=3)
run('plain-failed', mode='plain', failure=3)
run('no-color-failed', color=True, failure=3)
run('redirected-failed', terminal=False, failure=3)
run('dumb-failed', dumb=True, failure=3)
run('narrow-failed', narrow=True, failure=3)
run('plain', mode='plain')
run('no-color', color=True)
run('redirected', terminal=False)
run('old-helper', old=True)
run('spinner', helper_path=None)
run('broken-renderer', broken=True)
run('unwritable-stop', stop_failure=True)
run('cancel', cancel=True)
run('summary-rich', summary=True)
run('summary-plain', mode='plain', summary=True)
run('summary-old', old=True, summary=True)
run('summary-renderer-failed', broken=True, summary=True)
run('summary-install-failed', failure=3, summary=True)
run('summary-transform-failed', summary=True, summary_transform_failure=True)
run('summary-replace-failed', summary=True, summary_replace_failure=True)
run('summary-verbose', summary=True, verbose=True)
print('PASS shell progress state, stderr, lifecycle, fallback and cancellation fixtures')
PY_PROGRESS

# A leaked callback path/control byte, a disposable failure log, or unsafe
# diagnostic storage makes this adapter regression red. Both supported runtimes
# must preserve the same private storage boundary and the original stage exit.
python3 -I - "$SANDBOX" "$TEST_BOOTSTRAP_BASH" <<'PY_DIAGNOSTIC_STORAGE'
import os, pathlib, shutil, subprocess, sys
sandbox, bash = pathlib.Path(sys.argv[1]), sys.argv[2]
script = sandbox/'diagnostic-storage.sh'
script.write_text('''#!/bin/bash
source "$TEST_PROGRESS_SOURCE"
[ "$TEST_DIAGNOSTIC_ENGINE" != node ] || usable_python3() { return 1; }
bootstrap_control_root() { printf '%s\\n' "$TEST_DIAGNOSTIC_CONTROL"; }
_UI_MODE=plain
emit_first() { printf 'earlier-stage-context\\n'; }
emit_failure() { head -c 90000 /dev/zero; printf '\\033]0;foreign-path\\007' >&2; return 37; }
run_setup_stage 'Preparing fixture' emit_first
status=0
run_setup_stage 'Installing fixture' emit_failure || status=$?
[ "$status" -eq 37 ] || exit 99
report_setup_failure "$status"
''', newline='\n')
for engine in ('python3', 'node'):
    if engine == 'node' and not shutil.which('node'): continue
    cases = ('default', 'private', 'public', 'temporary', 'write-failed') if os.name == 'nt' else ('default', 'private', 'symlink', 'public', 'temporary', 'write-failed')
    for case in cases:
        base = sandbox/(engine+'-'+case); base.mkdir(mode=0o700)
        temporary = base/'tmp'; temporary.mkdir(mode=0o700)
        root = base/'diagnostics'
        control = base/'agent-notifications'
        if case == 'default': root = base/'agent-notifications-installer-diagnostics'
        elif case == 'symlink': root.symlink_to(temporary, target_is_directory=True)
        elif case == 'public': root.mkdir(mode=0o755)
        elif case == 'temporary': root = temporary/'diagnostics'
        elif case == 'write-failed': root.write_text('blocked')
        env = dict(os.environ, TEST_PROGRESS_SOURCE=str(sandbox/'functions.sh'),
                   TEST_DIAGNOSTIC_ENGINE=engine, TEST_DIAGNOSTIC_CONTROL=str(control), BOOTSTRAP_DIAGNOSTIC_ROOT=str(root), TMPDIR=str(temporary))
        if case == 'default': env.pop('BOOTSTRAP_DIAGNOSTIC_ROOT')
        result = subprocess.run([bash, str(script)], env=env, capture_output=True, timeout=10)
        assert result.returncode == 0, (engine, case, result.stdout, result.stderr)
        assert b'foreign-path' not in result.stdout+result.stderr and b'\x1b' not in result.stdout+result.stderr
        if os.name == 'nt':
            # Native Windows modes cannot prove the directory DACL. Refuse all
            # persistence until ACL verification exists, including broad roots.
            assert b'Diagnostics could not be fully saved' in result.stderr and b'Diagnostics:' not in result.stderr
            assert not list(base.rglob('installer-*.log'))
        elif case in ('default', 'private'):
            logs = list(root.glob('installer-*.log')); assert len(logs) == 1
            log = logs[0]
            shutil.rmtree(temporary)
            data = log.read_bytes()
            assert b'earlier-stage-context' in data and b'foreign-path' in data
            assert b'[diagnostic stream truncated after 65536 bytes]' in data and len(data) < 70000
            assert root.stat().st_mode & 0o777 == 0o700 and log.stat().st_mode & 0o777 == 0o600
            assert b'Diagnostics:' in result.stderr and b'could not be fully saved' not in result.stderr
        else:
            assert b'Diagnostics could not be fully saved' in result.stderr and b'Diagnostics:' not in result.stderr
            assert not list(base.rglob('installer-*.log'))
        assert not control.exists(), 'diagnostic capture created transaction control state'
print('PASS private durable diagnostic storage or native Windows refusal, truncation, symlink denial and honest write failure')
PY_DIAGNOSTIC_STORAGE

# A lock release between failed mkdir and inspection must retry, while an
# existing non-directory lock, cancel marker or nonblocking contention must keep
# its finite refusal/status contract. No installer/vendor command runs here.
python3 -I - "$SANDBOX" "$TEST_BOOTSTRAP_BASH" <<'PY_WRITER_GATE_RACE'
import os, pathlib, shutil, subprocess, sys
sandbox,bash=pathlib.Path(sys.argv[1]),sys.argv[2]
# Finite PATH entries are extensionless Bash wrappers on native Windows.
# Resolve in the shell that executes them; Python which() applies PATHEXT and
# cannot discover those wrappers. This retains the same allowlisted tools.
real_mkdir,real_rmdir=subprocess.check_output(
    [bash,'-c','type -P mkdir && type -P rmdir'],text=True).splitlines()
assert real_mkdir and real_rmdir
script=sandbox/'writer-gate-race-TEST.sh'
script.write_text(r'''#!/bin/bash
source "$TEST_GATE_SOURCE"
_PARALLEL_ROOT="$TEST_GATE_CASE"
fixture_callback() {
    [ "$_PARALLEL_GATE_HELD" = true ] || return 91
    printf 'acquired\n' >> "$TEST_GATE_CASE/callbacks"
}
set -- fixture_callback
case "$TEST_GATE_MODE" in busy|file-try|symlink-try|always-absent-try) set -- --try fixture_callback ;; esac
status=0
if [ "$TEST_GATE_MODE" = always-absent-try ]; then
    attempts=0
    while [ "$attempts" -lt 40 ]; do
        status=0; bootstrap_writer_gate "$@" || status=$?
        [ "$status" -eq 75 ] || break
        attempts=$((attempts + 1))
    done
else
    bootstrap_writer_gate "$@" || status=$?
fi
printf '%s\n' "$_PARALLEL_GATE_ABSENT_FAILURES" > "$TEST_GATE_CASE/absent-failures"
printf '%s\n' "$status" > "$TEST_GATE_CASE/status"
printf '%s\n' "$_PARALLEL_GATE_BUSY" > "$TEST_GATE_CASE/busy"
''', newline='\n')
mkdir_stub=r'''#!/bin/bash
if [ "$1" = "$TEST_GATE_CASE/writer.lock" ]; then
    printf 'attempt\n' >> "$TEST_GATE_CASE/attempts"
    case "$TEST_GATE_MODE" in always-absent|always-absent-try) exit 1 ;; esac
    if [ "$TEST_GATE_MODE" = race ] && [ ! -e "$TEST_GATE_CASE/released" ]; then
        "$TEST_REAL_MKDIR" "$@" 2>/dev/null
        status=$?
        [ "$status" -ne 0 ] || exit 92
        # The owner releases after mkdir has failed, before the caller sees it.
        "$TEST_REAL_RMDIR" "$@" || exit 93
        : > "$TEST_GATE_CASE/released"
        exit "$status"
    fi
fi
exec "$TEST_REAL_MKDIR" "$@"
'''
for mode in ('race','file','file-try','busy','cancel','always-absent','always-absent-try') + (() if os.name=='nt' else ('symlink','symlink-try')):
    case=sandbox/('writer-gate-'+mode); case.mkdir(mode=0o700)
    tools=case/'tools';tools.mkdir(mode=0o700)
    stub=tools/'mkdir';stub.write_text(mkdir_stub, newline='\n');stub.chmod(0o700)
    lock=case/'writer.lock'
    if mode in ('race','busy'): lock.mkdir(mode=0o700)
    elif mode in ('file','file-try'): lock.write_text('must remain untouched\n')
    elif mode in ('symlink','symlink-try'): lock.symlink_to(case/'missing-target')
    elif mode=='cancel': (case/'cancel').touch()
    env=dict(os.environ,PATH=str(tools)+os.pathsep+os.environ['PATH'],TEST_GATE_MODE=mode,TEST_GATE_CASE=str(case),
             TEST_GATE_SOURCE=str(sandbox/'functions.sh'),TEST_REAL_MKDIR=real_mkdir,TEST_REAL_RMDIR=real_rmdir)
    proc=subprocess.run([bash,str(script)],env=env,capture_output=True,timeout=8)
    assert proc.returncode==0,(mode,proc.stdout,proc.stderr)
    expected={'race':0,'file':1,'file-try':1,'symlink':1,'symlink-try':1,'busy':75,'cancel':130,'always-absent':1,'always-absent-try':1}[mode]
    assert int((case/'status').read_text())==expected,(mode,proc.stdout,proc.stderr)
    assert (case/'busy').read_text()==('true\n' if mode=='busy' else 'false\n')
    if mode=='race':
        assert (case/'callbacks').read_text()=='acquired\n'
        assert (case/'attempts').read_text()=='attempt\nattempt\n'
        assert not lock.exists() and not (case/'cancel').exists()
    else:
        assert not (case/'callbacks').exists(),mode
        if mode in ('file','file-try'): assert lock.read_text()=='must remain untouched\n'
        if mode in ('symlink','symlink-try'): assert lock.is_symlink()
        if mode=='busy': assert lock.is_dir() and (case/'absent-failures').read_text()=='0\n'
        if mode in ('always-absent','always-absent-try'):
            assert (case/'absent-failures').read_text()=='32\n'
            assert len((case/'attempts').read_text().splitlines())==32 and (case/'cancel').is_file()
print('PASS writer gate release race, invalid paths, cancel/nonblocking statuses and bounded absent-path failures')
PY_WRITER_GATE_RACE

# Latches fail if jobs serialize, failures cancel siblings, policy is seeded
# after observer consent, or shared transactions overlap.
python3 -I - "$SANDBOX" "$TEST_BOOTSTRAP_BASH" <<'PY_PARALLEL_INSTALL'
import atexit, os, pathlib, shutil, signal, subprocess, sys, time
children=[]
def cleanup_children():
    for proc in children:
        if proc.poll() is None:
            proc.terminate()
            try: proc.communicate(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill(); proc.communicate(timeout=8)
atexit.register(cleanup_children)
sandbox, bash = pathlib.Path(sys.argv[1]), sys.argv[2]
script = sandbox/'parallel-installer-TEST.sh'
script.write_text(r'''#!/bin/bash
source "$TEST_PARALLEL_SOURCE"
_UI_MODE=plain
_CONFIG_STAGE="$TEST_PARALLEL_CASE/stage"
_CONFIG_HELPER="$_CONFIG_STAGE/helper"
_SELECTOR_INTENT="$TEST_PARALLEL_CASE/frozen-intent"
CONFIGURE_NOTIFICATIONS=true
SELECTED_PRODUCTS=(claude codex opencode gemini qoder)
_PRODUCT_CSV=claude,codex,opencode,gemini,qoder
LEGACY_PRODUCT=both
_PROGRESS_IDS=(claude codex opencode gemini qoder)
_PROGRESS_PHASES=(pending pending pending pending pending)
_PROGRESS_OUTCOMES=(not_started not_started not_started not_started not_started)
BOOTSTRAP_SUMMARY_FILE="$TEST_PARALLEL_CASE/summary"
export BOOTSTRAP_SUMMARY_FILE
# Fixture adapters only: no agent/vendor CLI or real profile is used.
progress_child_running() {
    if [ "${TEST_PARALLEL_FAIL:-}" = trailing ] && [ ! -f "$TEST_PARALLEL_CASE/trailing-injected" ] &&
        [ -f "$_PARALLEL_ROOT/opencode/result" ]; then
        local index=0
        while [ "$index" -lt "${#_PARALLEL_PIDS[@]}" ]; do
            if [ "${_PARALLEL_CLIENTS[$index]}" = opencode ] && [ "${_PARALLEL_PIDS[$index]}" = "$1" ]; then
                # Inject exactly after the parent's ordinary drain, after this
                # worker exits, and before its authoritative result is merged.
                wait "$1" || return 0
                printf 'transition\topencode\truntime\trunning\n' >> "$_PARALLEL_ROOT/opencode/events"
                : > "$TEST_PARALLEL_CASE/trailing-injected"
                return 1
            fi
            index=$((index + 1))
        done
    fi
    local running result=1 _BOOTSTRAP_JOB_QUERY=true _BOOTSTRAP_JOB_QUERY_INTERRUPT=0
    for running in $(jobs -pr) $(jobs -ps); do
        if [ "$running" = "$1" ]; then result=0; break; fi
    done
    _BOOTSTRAP_JOB_QUERY=false
    [ "$_BOOTSTRAP_JOB_QUERY_INTERRUPT" -eq 0 ] || exit "$_BOOTSTRAP_JOB_QUERY_INTERRUPT"
    return "$result"
}
parallel_phase() {
    if [ -z "$_PARALLEL_WORKER" ]; then
        if [ "${TEST_PARALLEL_FAIL:-}" = crash-holder ] && [ "$1" = waiting ] && [ -f "$TEST_PARALLEL_CASE/holder-ready" ]; then
            : > "$TEST_PARALLEL_CASE/seed-attempt"
        fi
        return 0
    fi
    progress_phase "$_PARALLEL_WORKER" "$1" running
}
progress_start() { printf 'renderer-start\n' >> "$TEST_PARALLEL_CASE/renderer"; }
progress_stop() { :; }
diagnostic_capture_stage() {
    printf '%s:%s\n' "$_DIAGNOSTIC_LABEL" "$1" >> "$TEST_PARALLEL_CASE/diagnostics"
    [ -z "$_PROGRESS_STDERR" ] || cat "$_PROGRESS_STDERR" >> "$TEST_PARALLEL_CASE/private-errors"
}
fixture_commit() {
    mkdir "$TEST_PARALLEL_CASE/critical" || { echo overlap >> "$TEST_PARALLEL_CASE/overlap"; return 1; }
    [ "$_PARALLEL_GATE_HELD" = true ] || return 1
    printf '%s\n' "$1" >> "$TEST_PARALLEL_CASE/writers"
    sleep 0.05
    rmdir "$TEST_PARALLEL_CASE/critical" || return 1
    if [ "$1" = qoder ] && [ "${TEST_PARALLEL_FAIL:-}" = qoder ]; then
        echo 'exact Qoder failure receipt' >&2
        return 1
    fi
}
initialize_config() { printf 'init\n' >> "$TEST_PARALLEL_CASE/init"; }
configure_agent_policy() {
    [ "$PRODUCT" = both ] && [ "$1" = portable ] || return 1
    [ "$_SELECTOR_INTENT" = "$TEST_PARALLEL_CASE/frozen-intent" ] || return 1
    [ -f "$TEST_PARALLEL_CASE/codex-ready" ] || [ -f "$TEST_PARALLEL_CASE/claude-ready" ] || return 1
    fixture_commit policy || return 1
    : > "$TEST_PARALLEL_CASE/policy-seeded"
}
install_claude() {
    parallel_phase marketplace
    : > "$TEST_PARALLEL_CASE/claude-started"
    if [ "${TEST_PARALLEL_FAIL:-}" = crash-holder ]; then
        while [ ! -f "$TEST_PARALLEL_CASE/codex-ready" ]; do sleep 0.05; done
        fixture_hold_and_crash() {
            : > "$TEST_PARALLEL_CASE/holder-ready"
            while [ ! -f "$TEST_PARALLEL_CASE/holder-crash" ]; do sleep 0.05; done
            bash -c 'kill -KILL "$PPID"'
        }
        bootstrap_writer_gate fixture_hold_and_crash
        return 1
    fi
    if [ "${TEST_PARALLEL_CANCEL:-}" = 1 ]; then
        bash -c 'printf "%s\n" "$$" > "$TEST_PARALLEL_CASE/grandchild-pid"; while :; do sleep 0.2; done' &
        wait "$!"
    fi
    while [ ! -f "$TEST_PARALLEL_CASE/claude-release" ]; do sleep 0.05; done
    [ "${TEST_PARALLEL_FAIL:-}" != all-legacy ] || return 1
    [ "${TEST_PARALLEL_FAIL:-}" != claude ] || { echo 'exact Claude failure receipt' >&2; return 1; }
    bootstrap_writer_gate fixture_commit claude || return 1
    PLUGIN_ROOT="$TEST_PARALLEL_CASE/claude-owned"
    CONFIGURE_BINARY="$_CONFIG_STAGE/helper"
    : > "$TEST_PARALLEL_CASE/claude-ready"
}
install_codex() {
    parallel_phase download
    [ "${TEST_PARALLEL_FAIL:-}" != all-legacy ] || return 1
    bootstrap_writer_gate fixture_commit codex || return 1
    CONFIGURE_BINARY="$_CONFIG_STAGE/helper"
    : > "$TEST_PARALLEL_CASE/codex-ready"
    if [ "${TEST_PARALLEL_FAIL:-}" = crash-holder ]; then
        while [ ! -f "$TEST_PARALLEL_CASE/holder-ready" ]; do sleep 0.05; done
    fi
}
fixture_observer() {
    parallel_phase download
    : > "$TEST_PARALLEL_CASE/$1-acquired"
    parallel_policy_barrier || return 1
    [ -f "$TEST_PARALLEL_CASE/policy-seeded" ] || return 1
    bootstrap_writer_gate fixture_commit "$1" || return 1
    printf '  %s summary\n' "$1" > "$BOOTSTRAP_SUMMARY_FILE"
    : > "$TEST_PARALLEL_CASE/$1-ready"
}
install_opencode() { fixture_observer opencode; }
install_gemini() { fixture_observer gemini; }
install_qoder() { fixture_observer qoder; }
configure_agent_notify() {
    [ "$PRODUCT" = both ] && [ "$_PARALLEL_POLICY_READY" = true ] || return 1
    [ "$_SELECTOR_INTENT" = "$TEST_PARALLEL_CASE/frozen-intent" ] || return 1
    printf '%s\n' "$_PARALLEL_READY_CLIENTS" > "$TEST_PARALLEL_CASE/mcp-ready-clients"
    [ -f "$TEST_PARALLEL_CASE/opencode-ready" ] && [ -f "$TEST_PARALLEL_CASE/gemini-ready" ] || return 1
    [ "${TEST_PARALLEL_FAIL:-}" = qoder ] || [ -f "$TEST_PARALLEL_CASE/qoder-ready" ] || return 1
    local client
    for client in claude codex; do
        case ",$_PARALLEL_READY_CLIENTS," in *",$client,"*) progress_phase "$client" MCP completed ;; esac
    done
    if [ "${TEST_PARALLEL_FAIL:-}" = mcp-partial ]; then
        progress_phase claude MCP activation_timeout
        return 1
    fi
    [ "${TEST_PARALLEL_FAIL:-}" != mcp ]
}
print_success() { printf '  %s summary\n' "$PRODUCT" >> "$BOOTSTRAP_SUMMARY_FILE"; }
install_cleanup_traps
status=0
install_products_parallel || status=$?
progress_write_final_snapshot > "$TEST_PARALLEL_CASE/final"
printf '%s\n' "$status" > "$TEST_PARALLEL_CASE/status"
exit "$status"
''', newline='\n')
def wait_for(path, proc):
    limit = time.monotonic()+30
    while not path.exists():
        if proc.poll() is not None:
            out, err = proc.communicate()
            raise AssertionError((str(path), proc.returncode, out, err))
        assert time.monotonic()<limit, str(path)
        time.sleep(.02)
for failure in ('', 'claude', 'mcp', 'all-legacy', 'trailing', 'crash-holder', 'mcp-partial', 'gate-release', 'qoder', 'gate-mkdir'):
    case = sandbox/('parallel-'+(failure or 'success')); case.mkdir(mode=0o700)
    (case/'stage').mkdir(); helper=case/'stage'/'helper'; helper.write_text('#!/bin/bash\nexit 0\n', newline='\n'); helper.chmod(0o700)
    (case/'frozen-intent').write_text('immutable fixture intent\n')
    env=dict(os.environ, TEST_PARALLEL_SOURCE=str(sandbox/'functions.sh'), TEST_PARALLEL_CASE=str(case), TEST_PARALLEL_FAIL=failure)
    if failure=='gate-release':
        real_rmdir=subprocess.check_output([bash,'-c','type -P rmdir'],text=True).strip(); assert real_rmdir
        tools=case/'tools'; tools.mkdir(mode=0o700)
        stub=tools/'rmdir'
        stub.write_text('''#!/bin/bash
case "$1" in */writer.lock)
    if [ ! -e "$TEST_PARALLEL_CASE/release-failed" ]; then
        : > "$TEST_PARALLEL_CASE/release-failed"
        exit 1
    fi ;;
esac
exec "$TEST_REAL_RMDIR" "$@"
''', newline='\n')
        stub.chmod(0o700)
        env.update(PATH=str(tools)+os.pathsep+env['PATH'], TEST_REAL_RMDIR=real_rmdir)
    if failure=='gate-mkdir':
        real_mkdir=subprocess.check_output([bash,'-c','type -P mkdir'],text=True).strip(); assert real_mkdir
        tools=case/'tools'; tools.mkdir(mode=0o700)
        stub=tools/'mkdir'
        stub.write_text('''#!/bin/bash
case "$1" in */writer.lock)
    : > "$TEST_PARALLEL_CASE/mkdir-failed"
    exit 1 ;;
esac
exec "$TEST_REAL_MKDIR" "$@"
''', newline='\n')
        stub.chmod(0o700)
        env.update(PATH=str(tools)+os.pathsep+env['PATH'], TEST_REAL_MKDIR=real_mkdir)
    proc=subprocess.Popen([bash, str(script)], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    children.append(proc)
    names=('claude-started','opencode-acquired','gemini-acquired','qoder-acquired') if failure=='all-legacy' else ('claude-started','codex-ready','opencode-ready','gemini-ready','qoder-ready')
    if failure=='gate-mkdir': names=('claude-started','mkdir-failed','opencode-acquired','gemini-acquired','qoder-acquired')
    if failure=='gate-release': names=('claude-started','release-failed','opencode-acquired','gemini-acquired','qoder-acquired')
    if failure=='qoder': names=('claude-started','codex-ready','opencode-ready','gemini-ready','qoder-acquired')
    if failure=='crash-holder':
        for name in ('holder-ready', 'seed-attempt'): wait_for(case/name, proc)
        (case/'holder-crash').touch()
    for name in names: wait_for(case/name, proc)
    if failure=='trailing': wait_for(case/'trailing-injected', proc)
    assert not (case/'claude-release').exists() and proc.poll() is None
    journal=case/'stage'/'parallel'/'progress.events'
    if os.name!='nt': assert journal.stat().st_mode & 0o777 == 0o600
    for stage in case.glob('bootstrap-parallel-*'):
        if os.name!='nt': assert stage.stat().st_mode & 0o777 == 0o700
    (case/'claude-release').touch()
    out, err=proc.communicate(timeout=12)
    assert proc.returncode == (1 if failure in ('claude','mcp','all-legacy','crash-holder','mcp-partial','gate-release','qoder','gate-mkdir') else 0), (failure, out, err)
    assert not (case/'overlap').exists()
    if failure in ('gate-release','gate-mkdir'):
        assert not (case/'mcp-ready-clients').exists() and not (case/'init').exists()
        assert '\tcomplete\tcompleted' not in (case/'final').read_text()
        assert not (case/'summary').exists() or not (case/'summary').read_text()
        continue
    if failure=='all-legacy':
        assert not (case/'policy-seeded').exists() and not (case/'mcp-ready-clients').exists() and not (case/'init').exists()
        assert not (case/'opencode-ready').exists() and not (case/'gemini-ready').exists() and not (case/'qoder-ready').exists()
        assert 'approved shared policy preparation is unavailable' in (case/'private-errors').read_text()
        continue
    assert (case/'init').read_text()=='init\n'
    assert (case/'mcp-ready-clients').read_text()==('codex\n' if failure in ('claude','crash-holder') else 'claude,codex\n')
    assert (case/'frozen-intent').read_text()=='immutable fixture intent\n'
    assert ''.join(line+'\n' for line in (case/'summary').read_text().splitlines() if not line.startswith('  Delivery has not been verified.'))==('' if failure in ('claude','crash-holder') else '  claude summary\n')+'  codex summary\n  opencode summary\n  gemini summary\n'+('' if failure=='qoder' else '  qoder summary\n')
    final=(case/'final').read_text()
    assert 'opencode\tcomplete\tcompleted' in final and 'gemini\tcomplete\tcompleted' in final
    assert 'codex\tcomplete\tcompleted' in final
    if failure=='qoder':
        assert 'qoder\tcomplete\tcompleted' not in final and 'exact Qoder failure receipt' in (case/'private-errors').read_text()
    else: assert 'qoder\tcomplete\tcompleted' in final
    assert 'qoder\n' in (case/'writers').read_text()
    if failure=='mcp-partial': assert 'claude\tMCP\tactivation_timeout' in final
    elif failure not in ('claude','crash-holder'): assert 'claude\tcomplete\tcompleted' in final
    if failure=='claude': assert 'exact Claude failure receipt' in (case/'private-errors').read_text()
    diagnostics=(case/'diagnostics').read_text()
    indexes=[diagnostics.index('Installing '+client+' notifications:') for client in ('claude','codex','opencode','gemini','qoder')]
    assert indexes==sorted(indexes)
# Cancellation must terminate every owned group's descendants before cleanup.
if os.name != 'nt':
    case=sandbox/'parallel-cancel'; case.mkdir(mode=0o700); (case/'stage').mkdir()
    helper=case/'stage'/'helper'; helper.write_text('#!/bin/bash\nexit 0\n', newline='\n'); helper.chmod(0o700)
    (case/'frozen-intent').write_text('fixture\n')
    env=dict(os.environ, TEST_PARALLEL_SOURCE=str(sandbox/'functions.sh'), TEST_PARALLEL_CASE=str(case), TEST_PARALLEL_CANCEL='1')
    proc=subprocess.Popen([bash, str(script)], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    children.append(proc)
    wait_for(case/'grandchild-pid', proc)
    while not (case/'grandchild-pid').stat().st_size: time.sleep(.01)
    pid=int((case/'grandchild-pid').read_text())
    proc.send_signal(signal.SIGTERM)
    out, err=proc.communicate(timeout=8)
    assert proc.returncode==143, (out,err)
    try: os.kill(pid,0)
    except ProcessLookupError: pass
    else:
        state=pathlib.Path('/proc')/str(pid)/'stat'
        assert state.exists() and state.read_text().split()[2]=='Z', (pid,out,err)
print('PASS five concurrent installer jobs including independent Qoder, policy barrier, serialized writers, isolated failure, completion-tail IPC, crashed lock holder, failed gate release and descendant cancellation')
PY_PARALLEL_INSTALL

# The parent must interrupt its synchronous wait while a native helper still
# owns a client probe in a separate group, and allow that containment to close.
python3 -I - "$SANDBOX" "$TEST_BOOTSTRAP_BASH" <<'PY_PARENT_HELPER_CANCEL'
import os, pathlib, signal, subprocess, sys, time
sandbox, bash = pathlib.Path(sys.argv[1]), sys.argv[2]
if os.name == 'nt':
    print('Parent helper process-group fixture requires POSIX; Windows parallel fanout is not advertised')
    raise SystemExit(0)
script=sandbox/'parent-helper-cancel-TEST.sh'
script.write_text(r'''#!/bin/bash
source "$TEST_PROGRESS_SOURCE"
_UI_MODE=plain
_PARALLEL_ROOT="$TEST_AUX_CASE/jobs"
_PARALLEL_ACTIVE=true
_PARALLEL_IN_FLIGHT=true
_CONFIG_STAGE="$TEST_AUX_CASE/stage"
_CONFIG_HELPER="$TEST_AUX_CASE/helper"
CONFIGURE_BINARY="$_CONFIG_HELPER"
PLUGIN_ROOT="$TEST_AUX_CASE/plugin"
PRODUCT=codex
DEFAULT_CODEX_HOME="$TEST_AUX_CASE/codex"
_CODEX_EXEC="$TEST_AUX_CASE/absent-client"
BOOTSTRAP_TAG=""
_PARALLEL_POLICY_READY=true
CONFIGURE_NOTIFICATIONS=true
_PROGRESS_IDS=(codex)
_PROGRESS_PHASES=(MCP)
_PROGRESS_OUTCOMES=(registration_committed)
case "$TEST_AUX_PHASE" in
    typed-progress|typed-complete)
        PRODUCT=both
        _PARALLEL_FOLLOW_UP_STARTED=true
        _PARALLEL_CLIENTS=(claude codex)
        _PROGRESS_IDS=(claude codex)
        _PROGRESS_PHASES=(MCP MCP)
        _PROGRESS_OUTCOMES=(registration_committed registration_committed)
        _PROGRESS_JOURNAL="$_CONFIG_STAGE/parallel/progress.events" ;;
esac
progress_start() { :; }
progress_stop() { :; }
report_progress() { progress_write_final_snapshot > "$TEST_AUX_CASE/final"; }
report_diagnostics() { :; }
diagnostic_capture_stage() {
    if [ -n "$_PROGRESS_LOG" ] && [ -n "$_PROGRESS_STDERR" ]; then
        cat "$_PROGRESS_LOG" "$_PROGRESS_STDERR" >> "$TEST_AUX_CASE/diagnostics"
    fi
}
install_cleanup_traps
case "$TEST_AUX_PHASE" in
    install|inspect|typed-progress|typed-complete) run_setup_stage 'Configuring agent notification tools' setup_agent_notify_wizard ;;
    config) run_setup_stage 'Preparing notification settings' initialize_config ;;
    policy) run_setup_stage 'Preparing approved notification policy' configure_agent_policy portable ;;
    download) run_setup_stage 'Downloading portable package' fetch_bootstrap_file http://fixture.invalid/package "$TEST_AUX_CASE/package" ;;
esac
''', newline='\n')
helper=r'''#!/bin/bash
set -eu
case "$1 ${2:-}" in
    'setup-notifications --help') echo '--policy-only --preserve-enabled'; exit 0 ;;
    'installer-progress '*) exit 1 ;;
    'setup-notifications wizard')
        if [ "$TEST_AUX_PHASE" != "${4:-}" ]; then
            case "$TEST_AUX_PHASE:${4:-}" in typed-progress:install|typed-complete:install) ;;
                *) echo '{"outcome":"completed","targets":[]}'; exit 0 ;; esac
        fi ;;
    'config init') [ "$TEST_AUX_PHASE" = config ] || exit 91 ;;
    'setup-notifications configure') [ "$TEST_AUX_PHASE" = policy ] || exit 92 ;;
    '-fsSL '*) [ "$TEST_AUX_PHASE" = download ] || exit 93 ;;
    *) exit 94 ;;
esac
set -m
bash -c 'trap "exit 0" TERM INT HUP; while :; do sleep 0.1; done' &
probe=$!
set +m
printf '%s\n' "$probe" > "$TEST_AUX_CASE/probe-pid"
printf '%s\n' "$$" > "$TEST_AUX_CASE/helper-pid"
close_containment() {
    trap - TERM INT HUP
    # Longer than the former one-second shell escalation. The helper must stay
    # alive long enough to close the separately grouped native client probe.
    sleep 1.2
    kill -TERM -- "-$probe" 2>/dev/null || true
    wait "$probe" 2>/dev/null || true
    printf 'native-containment-closed\n' > "$TEST_AUX_CASE/closed"
    exit 0
}
trap close_containment TERM INT HUP
case "$TEST_AUX_PHASE" in
    typed-progress|typed-complete)
        printf 'transition\tclaude\tMCP\tactivation_timeout\n' >> "$TEST_AUX_CASE/stage/parallel/progress.events"
        if [ "$TEST_AUX_PHASE" = typed-complete ]; then
            printf 'transition\tcodex\tMCP\tcompleted\n' >> "$TEST_AUX_CASE/stage/parallel/progress.events"
        else
            printf 'transition\tcodex\tverification\trunning\n' >> "$TEST_AUX_CASE/stage/parallel/progress.events"
        fi ;;
esac
printf 'parent-helper-live\n'
printf 'ready\n' > "$TEST_AUX_CASE/started"
wait "$probe"
'''
def wait_file(path, proc):
    limit=time.monotonic()+20
    while not path.exists() or not path.stat().st_size:
        assert proc.poll() is None, proc.communicate()
        assert time.monotonic()<limit, str(path)
        time.sleep(.02)
def gone(pid):
    try: os.kill(pid, 0)
    except ProcessLookupError: return True
    state=pathlib.Path('/proc')/str(pid)/'stat'
    return state.exists() and state.read_text().split()[2]=='Z'
for phase in ('install','inspect','config','policy','download','typed-progress','typed-complete'):
    case=sandbox/('parent-helper-'+phase); case.mkdir(mode=0o700)
    for name in ('jobs','stage','stage/parallel','plugin','plugin/portable-package','tools'): (case/name).mkdir(mode=0o700)
    (case/'plugin'/'portable-package'/'plugin.json').write_text('{}')
    if phase in ('typed-progress','typed-complete'):
        for client in ('claude','codex'):
            job=case/'jobs'/client; job.mkdir(mode=0o700)
            for leaf in ('stdout','stderr'): (job/leaf).write_text('')
            (job/'result').write_text('installer-worker-v1\n0\n\n\nMCP\nregistration_committed_follow_up_not_started\n\n')
    for path in (case/'helper',case/'tools'/'curl'): path.write_text(helper, newline='\n'); path.chmod(0o700)
    env=dict(os.environ, TEST_PROGRESS_SOURCE=str(sandbox/'functions.sh'), TEST_AUX_CASE=str(case), TEST_AUX_PHASE=phase,
             PATH=str(case/'tools')+os.pathsep+os.environ['PATH'])
    proc=subprocess.Popen([bash,str(script)],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    try:
        wait_file(case/'helper-pid',proc); wait_file(case/'probe-pid',proc); wait_file(case/'started',proc)
        owner=int((case/'helper-pid').read_text()); probe=int((case/'probe-pid').read_text())
        started=time.monotonic(); proc.send_signal(signal.SIGTERM)
        out,err=proc.communicate(timeout=9)
        assert proc.returncode==143, (phase,out,err)
        assert time.monotonic()-started<8, phase
        assert (case/'closed').read_text()=='native-containment-closed\n', (phase,out,err)
        assert gone(owner) and gone(probe), (phase,owner,probe,out,err)
        assert (case/'stage').is_dir() and (case/'jobs').is_dir(), phase
        assert b'required force' not in err, (phase,err)
        assert 'parent-helper-live' in (case/'diagnostics').read_text(), phase
        if phase in ('typed-progress','typed-complete'):
            final=(case/'final').read_text()
            assert 'claude\tMCP\tactivation_timeout' in final, final
            if phase=='typed-progress':
                assert 'codex\tverification\trunning' in final and 'codex\tcomplete\tcompleted' not in final, final
            else: assert 'codex\tcomplete\tcompleted' in final, final
    finally:
        if proc.poll() is None:
            proc.terminate()
            try: proc.communicate(timeout=9)
            except subprocess.TimeoutExpired: proc.kill(); proc.communicate(timeout=5)
print('PASS parent cancellation closes helper containment, retains interrupted captures and preserves typed timeout/completion journal facts')
PY_PARENT_HELPER_CANCEL

[ "$_PRODUCT_TEST_UNIT_ONLY" != true ] || exit 0
# Local HTTP and controlling-PTY integration. Installer/registration are explicit
# fake adapters here; the fetched bootstrap, archive extraction and curl are real.
python3 - "$ROOT" "$SANDBOX" <<'PY'
import functools, http.server, io, json, os, pathlib, select, shlex, shutil, signal, subprocess, sys, tarfile, threading, time
if os.name != "nt":
    import pty
root, sandbox = map(pathlib.Path, sys.argv[1:])
web = sandbox / 'http'; web.mkdir()
fixture_bootstrap = (root / 'bin/bootstrap.sh').read_text(encoding='utf-8').replace(
    '\nmain "$@"', '\nbootstrap_macos_command() { return 1; }\nmain "$@"')
(web / 'bootstrap.sh').write_text(fixture_bootstrap, encoding='utf-8', newline='\n')
(web / 'latest').write_text('{"tag_name":"v1.42.0"}')
release_commits = {'v1.42.0': 'a' * 40, 'v1.43.0': 'b' * 40, 'v2.0.0': 'c' * 40}
(web / 'commits').mkdir()
for tag, commit in release_commits.items():
    (web / 'commits' / tag).write_text(commit)
bash = os.environ['TEST_BOOTSTRAP_BASH']
uname_os=subprocess.check_output([bash,'-c','uname -s'],text=True).strip().lower()
uname_arch=subprocess.check_output([bash,'-c','uname -m'],text=True).strip().lower()
asset_os='windows' if uname_os.startswith(('mingw','msys','cygwin')) else uname_os
asset_arch='arm64' if uname_arch in ('arm64','aarch64') else 'amd64'
asset_name='claude-notifications-'+asset_os+'-'+asset_arch+('.exe' if asset_os=='windows' else '')
installer = '''#!/bin/bash
set -eu
[ "$1" = --force ]
[ "${INSTALL_DEFER_PROMOTION:-0}" = 1 ] || { echo "nested installer did not defer promotion" >&2; exit 90; }
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) cp "$INSTALL_STAGED_ASSETS"/claude-notifications-*.exe "$INSTALL_TARGET_DIR/" ;;
    *) cp "$INSTALL_STAGED_ASSETS"/claude-notifications-* "$INSTALL_TARGET_DIR/claude-notifications" ;;
esac
chmod +x "$INSTALL_TARGET_DIR"/claude-notifications*
'''
binary = '''#!''' + sys.executable + '''
import json, os, pathlib, sys
# Match the native Go helper's LF protocol on Windows too. Python's default
# CRLF would leave a trailing carriage return in Bash's baseline version.
sys.stdout.reconfigure(newline='\\n')
args=sys.argv[1:]
if not args:
    sys.exit(2)
if args[0] in ('--help', 'help', '-h'):
    print('Usage:')
    print('  setup-codex [--print] [--dry-run] [--codex-home <dir>] [--plugin-root <dir>]')
    sys.exit()
if os.environ.get('FIXTURE_TRACE'):
    with open(os.environ['FIXTURE_TRACE'],'a') as f: f.write(json.dumps(args)+'\\n')
if args==['--version']:
    print('claude-notifications v1.42.0'); sys.exit()
if args[0]=='setup-codex':
    i=1
    known={'--print','--dry-run','--codex-home','--plugin-root'}
    takes_value={'--codex-home','--plugin-root'}
    while i < len(args):
        if args[i] not in known:
            print('setup-codex: unknown option: '+args[i], file=sys.stderr)
            sys.exit(1)
        if args[i] in takes_value:
            i += 1
            if i >= len(args):
                sys.exit(1)
        i += 1
    if os.environ.get('FAIL_REGISTER')=='1': sys.exit(1)
    if '--dry-run' not in args:
        p=pathlib.Path(os.environ['CODEX_HOME'])
        if '--codex-home' in args:
            p=pathlib.Path(args[args.index('--codex-home')+1])
        p.mkdir(parents=True, exist_ok=True)
        (p/'fixture-registration').write_text('registered')
        dest=p/'claude-notifications-go'/'bin'
        dest.mkdir(parents=True, exist_ok=True)
        target=dest/(pathlib.Path(sys.argv[0]).name if os.name=='nt' else 'claude-notifications')
        target.write_bytes(pathlib.Path(sys.argv[0]).read_bytes())
        target.chmod(0o755)
        if os.name=='nt':
            (dest/'claude-notifications.bat').write_text('@echo off\\r\\n"%~dp0\\\\'+target.name+'" %*\\r\\n')
        if os.environ.get('FAIL_SETUP_INIT')=='1': sys.exit(3)
    sys.exit()
if args[0]=='setup-notifications':
    print('Error: unknown command: setup-notifications', file=sys.stderr)
    sys.exit(1)
if args[:2]==['setup-products','repair-cache']:
    print('ready'); sys.exit()
assert args[0]=='config'
legacy=pathlib.Path(os.environ['HOME'])/'.claude/claude-notifications-go/config.json'
neutral=pathlib.Path(os.environ['XDG_CONFIG_HOME'])/'agent-notifications/config.json'
explicit=os.environ.get('AGENT_NOTIFICATIONS_CONFIG')
p=pathlib.Path(explicit) if explicit else legacy if legacy.exists() else neutral
selected=dict(path=str(p),source='explicit' if explicit else 'legacy' if p==legacy else 'universal',exists=p.exists())
if args[1]=='path': print(json.dumps(selected)); sys.exit()
request=None
if args[1:3]==['installer','capabilities']:
    print('installer-v1'); sys.exit()
if args[1:2]==['installer'] and args[2] in ('root','version'):
    entries=json.loads(pathlib.Path(args[3]).read_text()).get('plugins',{}).get(args[4],[])
    if entries: print(entries[-1]['installPath' if args[2]=='root' else 'version'])
    sys.exit()
if args[1:3]==['installer','versions']:
    registry=json.loads(pathlib.Path(args[3]).read_text())
    for entry in registry.get('plugins',{}).get(args[4],[]):
        if (pathlib.Path(entry['installPath'])/'config/config.json').exists(): print(entry['version'].removeprefix('v'))
    sys.exit()
if args[1:3]==['installer','bootstrap']:
    registry,key,claude,cache,market,codex,product,stage,current,venv=args[3:]
    roots=[]; refresh=[]; historical=[]; protected=[]
    if product!='codex':
        entries=json.loads(pathlib.Path(registry).read_text()).get('plugins',{}).get(key,[])
        roots=[e['installPath'] for e in entries]
        refresh=[cache,market]+roots
        if pathlib.Path(current).exists(): refresh += [e['installPath'] for e in json.loads(pathlib.Path(current).read_text()).get('plugins',{}).get(key,[])]
        protected=[current,str(pathlib.Path(claude)/'plugins/known_marketplaces.json'),str(pathlib.Path(claude)/'settings.json')]
        for e in entries:
            c=dict(path=str(pathlib.Path(e['installPath'])/'config/config.json'))
            b=pathlib.Path(stage)/('baseline-'+e['version'].removeprefix('v'))
            if (b/'verified').exists(): c.update(baselinePath=str(b/'config.json'),baselineSHA256=(b/'verified').read_text().strip())
            historical.append(c)
    historical.append(dict(path=str(pathlib.Path(claude)/'claude-notifications-go/config.json')))
    if product!='claude':
        dest=pathlib.Path(codex)/'claude-notifications-go'
        refresh.append(str(dest)); historical.append(dict(path=str(dest/'config/config.json')))
    if venv: refresh.append(venv)
    if any(pathlib.Path(stage).resolve().is_relative_to(pathlib.Path(d).resolve()) for d in refresh): sys.exit(1)
    request=dict(activeBundleRoots=roots,refreshDirs=refresh,historicalCandidates=historical,protectedPaths=protected)
    args=['config','preflight-update']
if args[1]=='preflight-update':
    if request is None: request=json.load(sys.stdin)
    status='safe'
    if any(p.resolve().is_relative_to(pathlib.Path(d).resolve()) for d in request['refreshDirs']): status='unsafe-target'
    if any(p.resolve()==pathlib.Path(d).resolve() for d in request.get('protectedPaths',[])): status='unsafe-target'
    def customized(c):
        candidate=pathlib.Path(c['path'])
        if not candidate.exists(): return False
        baseline=c.get('baselinePath')
        return not baseline or candidate.read_bytes()!=pathlib.Path(baseline).read_bytes()
    if not explicit and not p.exists() and any(customized(c) for c in request['historicalCandidates']): status='import-required'
    if os.environ.get('FIXTURE_REQUEST'):
        pathlib.Path(os.environ['FIXTURE_REQUEST']).write_text(json.dumps(request))
    print(json.dumps(dict(selected,status=status)))
    sys.exit(0 if status=='safe' else 1)
assert args[1]=='init'
if os.environ.get('FAIL_INIT')=='1': sys.exit(1)
changed=not p.exists()
if changed:
    p.parent.mkdir(parents=True,exist_ok=True); p.write_text('{}')
print(json.dumps(dict(selected,changed=changed)))
'''
for tag, commit in release_commits.items():
    with tarfile.open(web / (commit + '.tar.gz'), 'w:gz') as archive:
        for name, data in {'bin/install.sh': installer, '.claude-plugin/plugin.json': '{"version":"'+tag[1:]+'"}'}.items():
            data = data.encode('utf-8'); entry = tarfile.TarInfo('bundle/' + name); entry.size = len(data); entry.mode = 0o755
            archive.addfile(entry, io.BytesIO(data))
    archive_copy = web / 'archive' / (commit + '.tar.gz')
    archive_copy.parent.mkdir(parents=True, exist_ok=True)
    archive_copy.write_bytes((web / (commit + '.tar.gz')).read_bytes())
    dest = web / 'download' / tag; dest.mkdir(parents=True)
    payload=binary.replace('v1.42.0', tag).encode('utf-8')
    (dest / 'binary').write_bytes(payload)
    (dest / asset_name).write_bytes(payload)
    import hashlib
    (dest / 'checksums.txt').write_bytes((hashlib.sha256(payload).hexdigest()+'  '+asset_name+'\n').encode('ascii'))
    raw = web / 'raw' / commit / 'bin'; raw.mkdir(parents=True)
    (raw / 'install.sh').write_bytes(installer.encode('utf-8'))
    tag_raw = web / 'raw' / tag / 'bin'; tag_raw.mkdir(parents=True)
    (tag_raw / 'install.sh').write_bytes(installer.encode('utf-8'))
capable = binary.replace(
    '  setup-codex [--print] [--dry-run] [--codex-home <dir>] [--plugin-root <dir>]',
    '  setup-codex [--print] [--dry-run] [--codex-home <dir>] [--plugin-root <dir>]\\n                          [--agent-notify|--skip-agent-notify]\\n  setup-notifications',
).replace(
    "known={'--print','--dry-run','--codex-home','--plugin-root'}",
    "known={'--print','--dry-run','--codex-home','--plugin-root','--skip-agent-notify','--agent-notify'}",
).replace(
    "print('Error: unknown command: setup-notifications', file=sys.stderr)\n    sys.exit(1)",
    "sys.exit(0)",
)
dest = web / 'download' / 'v2.0.0'
payload=capable.replace('v1.42.0', 'v2.0.0').encode('utf-8')
(dest / 'binary').write_bytes(payload)
(dest / asset_name).write_bytes(payload)
(dest / 'checksums.txt').write_bytes((hashlib.sha256(payload).hexdigest()+'  '+asset_name+'\n').encode('ascii'))
# Exercise the adapter's raw protocol before shell command substitution can
# hide newline differences. Model Windows text output on every host, in
# addition to running with native Windows Python in Git Bash CI.
protocol_helper=sandbox/'protocol-helper.py'
protocol_helper.write_bytes(binary.replace(
    'import json, os, pathlib, sys\n',
    "import json, os, pathlib, sys\nsys.stdout.reconfigure(newline='\\r\\n')\n",
    1,
).encode('utf-8'))
protocol=subprocess.check_output([sys.executable,str(protocol_helper),'--version'])
assert protocol == b'claude-notifications v1.42.0\n', repr(protocol)
protocol_root=sandbox/'protocol bundle'
(protocol_root/'config').mkdir(parents=True)
(protocol_root/'config/config.json').write_bytes(b'{}')
protocol_registry=sandbox/'protocol-registry.json'
protocol_registry.write_text(json.dumps({'plugins':{'fixture':[{'installPath':str(protocol_root),'version':'v1.40.0'}]}}))
protocol=subprocess.check_output([sys.executable,str(protocol_helper),'config','installer','versions',str(protocol_registry),'fixture'])
assert protocol == b'1.40.0\n', repr(protocol)
(web/'install.sh').write_bytes(installer.encode('utf-8'))
def write_origin_installer(path, origin):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(installer.replace(
        'chmod +x "$INSTALL_TARGET_DIR"/claude-notifications*',
        'printf %s\\\\n '+origin+' > "$INSTALL_TARGET_DIR/script-origin"\nchmod +x "$INSTALL_TARGET_DIR"/claude-notifications*',
        1,
    ), newline='\n')
for tag in ['v1.42.0', 'v1.43.0', 'v2.0.0']:
    write_origin_installer(web / tag / 'bin' / 'install.sh', tag)
write_origin_installer(web / 'main' / 'bin' / 'install.sh', 'managed')
request_paths=[]
class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        request_paths.append(self.path)
        super().do_GET()
    def log_message(self, *args): pass
server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Handler, directory=str(web)))
threading.Thread(target=server.serve_forever, daemon=True).start()
base = 'http://127.0.0.1:' + str(server.server_port)
env_keys = (
    'PATH', 'SystemRoot', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT',
    'HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME',
    'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_RUNTIME_DIR',
    'XDG_CONFIG_DIRS', 'XDG_DATA_DIRS', 'CODEX_HOME', 'CLAUDE_HOME',
    'CLAUDE_CONFIG_DIR', 'TMP', 'TEMP', 'TMPDIR',
)
env = {key: os.environ[key] for key in env_keys if key in os.environ}
env.update(BOOTSTRAP_RELEASE_TAG='v1.42.0', BOOTSTRAP_LATEST_RELEASE_API_URL=base+'/latest', BOOTSTRAP_COMMIT_API_BASE_URL=base+'/commits', BOOTSTRAP_RAW_BASE_URL=base+'/raw', BOOTSTRAP_RAW_CONTENT_URL=base+'/raw', BOOTSTRAP_SOURCE_BASE_URL=base, BOOTSTRAP_RELEASES_BASE_URL=base)
cli = sandbox / 'clis'; cli.mkdir()
(cli / 'codex').write_bytes(b'#!/bin/sh\nexit 99\n'); (cli / 'codex').chmod(0o755)
assert pathlib.Path(bash).is_file(), 'fixture controller Bash must exist'
env['PATH'] = str(cli) + os.pathsep + (os.environ['PATH'] if os.name == 'nt' else '/usr/bin:/bin')
script = str(web / 'bootstrap.sh')
def run(args, expected=0, extra=None):
    process = subprocess.Popen([bash, script]+args, env=dict(env, **(extra or {})), stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        output, _ = process.communicate(timeout=40)
    except subprocess.TimeoutExpired:
        # Killing only Bash leaves curl/helper children holding the output pipe,
        # so subprocess.run's timeout cleanup can itself wait indefinitely.
        if os.name == 'nt':
            subprocess.run([str(pathlib.Path(os.environ['SystemRoot'])/'System32/taskkill.exe'), '/F', '/T', '/PID', str(process.pid)],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
        else:
            os.killpg(process.pid, signal.SIGKILL)
        output, _ = process.communicate(timeout=5)
        raise AssertionError('bootstrap timed out: ' + repr(args) + '\n' + output.decode())
    assert (process.returncode == 0) == (expected == 0), output.decode()
    assert output.count(b'Boost your productivity') == int(expected == 0), output.decode()
    if expected == 0: assert output.rstrip().endswith(b'----------------------------------------'), output.decode()
    return output.decode()
assert 'No controlling TTY'  in run([], 1)
assert 'claude CLI not found' in run(['--product', 'both'], 1)
(cli / 'codex').rename(cli / 'absent')
assert 'codex CLI not found' in run(['--product', 'codex'], 1)
(cli / 'absent').rename(cli / 'codex')
run(['--product', 'codex']); run(['--product', 'codex'])
assert '/commits/v1.42.0' in request_paths
assert '/raw/' + release_commits['v1.42.0'] + '/bin/install.sh' in request_paths
assert '/' + release_commits['v1.42.0'] + '.tar.gz' in request_paths
assert not any('/refs/tags/' + commit in path for commit in release_commits.values() for path in request_paths)
run(['--product', 'codex'], extra={'BOOTSTRAP_RELEASE_TAG':'v1.43.0'})
run(['--product', 'codex'], extra={'BOOTSTRAP_RELEASE_TAG':'v2.0.0'})
assert '/' + release_commits['v2.0.0'] + '.tar.gz' in request_paths
assert '/commits/v2.0.0' in request_paths
pairing={'INSTALL_SCRIPT_URL':'','BOOTSTRAP_RAW_CONTENT_URL':base,'MANAGED_INSTALL_SCRIPT_URL':base+'/main/bin/install.sh'}
request_paths.clear()
run(['--product', 'codex'], extra=dict(pairing, BOOTSTRAP_RELEASE_TAG='v1.43.0'))
assert sum(path.endswith('/' + release_commits['v1.43.0'] + '/bin/install.sh') for path in request_paths)==1
assert not any(path.endswith('/main/bin/install.sh') for path in request_paths)
home=pathlib.Path(env['HOME'])
xdg=sandbox/'xdg-config'
appdata=sandbox/'appdata'
if sys.platform=='darwin':
    ledger=home/'Library/Application Support'/'agent-notifications'/'ownership.json'
    managed_env={}
elif os.name=='nt':
    ledger=appdata/'agent-notifications'/'ownership.json'
    managed_env={'APPDATA':str(appdata)}
else:
    ledger=xdg/'agent-notifications'/'ownership.json'
    managed_env={'XDG_CONFIG_HOME':str(xdg)}
ledger.parent.mkdir(parents=True, exist_ok=True)
ledger.write_text('{"schema":1,"id":"fixture","generation":1,"consumers":{},"files":{}}\n')
request_paths.clear()
run(['--product', 'codex'], extra=dict(pairing, BOOTSTRAP_RELEASE_TAG='v1.43.0', **managed_env))
assert sum(path.endswith('/main/bin/install.sh') for path in request_paths)==1
assert not any(path.endswith('/' + release_commits['v1.43.0'] + '/bin/install.sh') for path in request_paths)
ledger.unlink()
registration = pathlib.Path(env['CODEX_HOME']) / 'fixture-registration'
before = registration.read_bytes()
# Reject mixed binary/source releases before registration and retain live state.
payload_file = web / 'download/v1.42.0' / asset_name
valid_payload = payload_file.read_bytes()
payload_file.write_bytes(valid_payload.replace(b'v1.42.0', b'v1.41.0'))
run(['--product', 'codex'], 1)
assert registration.read_bytes() == before
payload_file.write_bytes(valid_payload)

run(['--product', 'codex'], 1, {'FAIL_REGISTER':'1'})
run(['--product', 'codex'], 1, {'BOOTSTRAP_SOURCE_BASE_URL':base+'/missing'})
assert registration.read_bytes() == before
# Matching Claude manifest with stale runtime must force a fresh staged binary.
live = sandbox / 'live claude'; (live / 'bin').mkdir(parents=True); (live / '.claude-plugin').mkdir()
(live / '.claude-plugin/plugin.json').write_text('{"version":"1.42.0"}')
(live / 'bin/install.sh').write_bytes(installer.encode('utf-8'))
(live / 'bin/claude-notifications').write_text('stale')
command = 'source '+shlex.quote(str(sandbox/'functions.sh'))+'; PRODUCT=both; PLUGIN_ROOT='+shlex.quote(str(live))+'; BOOTSTRAP_TAG=v1.42.0; BOOTSTRAP_COMMIT='+release_commits['v1.42.0']+'; INSTALL_SCRIPT_URL='+shlex.quote(base+'/raw/'+release_commits['v1.42.0']+'/bin/install.sh')+'; install_cleanup_traps; stage_config_helper; config_preflight; install_codex'
r = subprocess.run([bash,'-c',command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
assert r.returncode == 0, r.stdout.decode()
assert (live/'bin/claude-notifications').read_text() == 'stale'
# Menu routing for Claude/both uses explicit adapters; Codex below exercises
# the complete bootstrap HTTP/staging path with fake runtime assets.
dispatch = fixture_bootstrap.replace('main "$@"', '')
dispatch += '\ncheck_prerequisites() { :; }\nresolve_bootstrap_release() { :; }\nstage_historical_baselines() { :; }\nstage_config_helper() { _CONFIG_STAGE=$(mktemp -d); _CONFIG_HELPER=REPAIR_PEER_PATH; }\nconfig_preflight() { :; }\ninitialize_config() { :; }\ninstall_claude() { echo CLAUDE_ADAPTER; }\ninstall_codex() { echo CODEX_ADAPTER; }\nmain "$@"\n'
dispatch = dispatch.replace('REPAIR_PEER_PATH', shlex.quote(str(sandbox/'repair-helper')))
(web / 'dispatch.sh').write_bytes(dispatch.encode('utf-8'))
# The native SelectMany UI is independently exercised against the actual
# candidate by bootstrap_opencode_test.sh. This fixture checks shell routing.
for product, success in [('claude',True), ('codex',True), ('both',True), ('invalid',False)]:
    entry = '/dispatch.sh' if product in ['claude', 'both', 'invalid'] else '/bootstrap.sh'
    command = 'curl -fsSL '+base+entry+' | bash -s -- --product '+shlex.quote(product)
    result = subprocess.run([bash,'-c',command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
    output = result.stdout
    assert (result.returncode==0)==success, output.decode()
    if product in ['claude','both']: assert b'Claude - installed' in output
    if product == 'claude': assert b'Codex - installed' not in output
    if product == 'both': assert b'Codex - installed' in output
    if product == 'invalid': assert b'CLAUDE_ADAPTER' not in output and b'CODEX_ADAPTER' not in output

assert not list(pathlib.Path(env['TMPDIR']).glob('bootstrap-codex-*'))
assert not list(pathlib.Path(env['TMPDIR']).glob('bootstrap-release-*'))
# Protected flow E2E. Real shell orchestration and local downloads; explicit
# fake config CLI models the coordinated contract, not Go resolver evidence.
trace=sandbox/'trace'; request=sandbox/'request.json'
env.update(FIXTURE_TRACE=str(trace),FIXTURE_REQUEST=str(request))
claude_script='''#!''' + sys.executable + '''
import json,os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['FIXTURE_TRACE'],'a') as f: f.write(json.dumps(['claude']+args)+'\\n')
if os.environ.get('FAIL_CLAUDE')=='1': sys.exit(1)
home=pathlib.Path(os.environ['CLAUDE_CONFIG_DIR'])
market=home/'plugins/marketplaces/claude-notifications-go/.claude-plugin'
market.mkdir(parents=True,exist_ok=True)
(market/'plugin.json').write_text('{"version":"1.42.0"}')
if args[1]=='marketplace': sys.exit()
root=home/'plugins/cache/claude-notifications-go/claude-notifications-go/1.42.0'
(root/'.claude-plugin').mkdir(parents=True,exist_ok=True)
(root/'bin').mkdir(exist_ok=True)
(root/'.claude-plugin/plugin.json').write_text('{"version":"1.42.0"}')
registry=home/'plugins/installed_plugins.json'
registry.write_text(json.dumps({'plugins':{'claude-notifications-go@claude-notifications-go':[{'installPath':str(root),'version':'1.42.0'}]}}))
'''
(cli/'claude').write_bytes(claude_script.encode('utf-8')); (cli/'claude').chmod(0o755)
def events():
    return [json.loads(line) for line in trace.read_text().splitlines()] if trace.exists() else []
def path_ids(values):
    return [pathlib.Path(value).resolve() for value in values]
def native_shell_path(value):
    if os.name != 'nt':
        return value
    return subprocess.check_output([bash,'-c','cygpath -w "$1"','_',value],text=True).strip()
def reset_case():
    # Every directory is an explicit child of this fixture, never host state.
    for key in ['HOME','XDG_CONFIG_HOME','CODEX_HOME','CLAUDE_CONFIG_DIR']:
        d=pathlib.Path(env[key]); assert d.is_relative_to(sandbox)
        # Several roots intentionally overlap (for example CODEX_HOME under
        # HOME), so an earlier removal may already have removed this path.
        # Recreate every fixture root while keeping all behavioral assertions.
        if d.exists():
            shutil.rmtree(d)
        d.mkdir(parents=True, exist_ok=True)
    trace.write_text('')
def init_events(): return [e for e in events() if e[:2]==['config','init']]
# Real HTTP/checksum acquisition and public shell orchestration must emit clean,
# physical staging paths. A symlinked TMPDIR ending in / used to produce // in
# --package, rejected by the real Go wizard before any installation action.
if os.name != 'nt':
    reset_case(); request_paths.clear()
    temp_root=sandbox/'physical temp'; temp_root.mkdir()
    temp_alias=sandbox/'temp alias'; temp_alias.symlink_to(temp_root, target_is_directory=True)
    release=web/'download/v2.0.0'
    original_payload=(release/asset_name).read_bytes()
    original_checksums=(release/'checksums.txt').read_bytes()
    wizard_payload=capable.replace('setup-notifications', 'setup-notifications wizard', 1).replace("if args[0]=='setup-notifications':", "if args[:2]==['setup-notifications','--help']:\n    print('--policy-only --preserve-enabled'); sys.exit()\nif args[0]=='setup-notifications':", 1).replace('v1.42.0', 'v2.0.0').encode('utf-8')
    portable_name='agent-notify-portable-'+asset_os+'-'+asset_arch+'.zip'
    portable_payload=b'verified portable argv fixture'
    (release/asset_name).write_bytes(wizard_payload)
    (release/portable_name).write_bytes(portable_payload)
    (release/'checksums.txt').write_text(
        hashlib.sha256(wizard_payload).hexdigest()+'  '+asset_name+'\n'+
        hashlib.sha256(portable_payload).hexdigest()+'  '+portable_name+'\n')
    try:
        run(['--product','codex'], extra={'BOOTSTRAP_RELEASE_TAG':'v2.0.0', 'TMPDIR':str(temp_alias)+'/'})
        es=events()
        wizard_args=next(e for e in es if e[:2]==['setup-notifications','wizard'])
        package=wizard_args[wizard_args.index('--package')+1]
        assert package == os.path.normpath(package) == os.path.realpath(package), package
        assert pathlib.Path(package).parent.parent == temp_root.resolve(), package
        assert pathlib.Path(package).name == portable_name
        for e in es:
            if e[:1]==['setup-codex']:
                bundle=e[e.index('--plugin-root')+1]
                assert bundle == os.path.realpath(bundle), bundle
            if e[:3]==['config','installer','bootstrap']:
                for staging_path in (e[3], e[10]):
                    assert staging_path == os.path.realpath(staging_path), staging_path
        assert '/download/v2.0.0/'+portable_name in request_paths
        assert not list(temp_root.iterdir()), 'canonical staging cleanup failed'
    finally:
        (release/asset_name).write_bytes(original_payload)
        (release/'checksums.txt').write_bytes(original_checksums)
        (release/portable_name).unlink()
for product in ['claude','codex','both']:
    reset_case(); request_paths.clear()
    output=run(['--product',product])
    assert sum(path.endswith('/'+asset_name) for path in request_paths)==1
    neutral=pathlib.Path(env['XDG_CONFIG_HOME'])/'agent-notifications/config.json'
    expected_inits = 2 if product == 'both' else 1
    assert neutral.exists() and len(init_events())==expected_inits, (product, str(neutral), events(), output)
    es=events(); init_index=next(i for i,e in enumerate(es) if e[:2]==['config','init'])
    assert any(e[:2]==['claude','plugin'] or e[:1]==['setup-codex'] for e in es[:init_index])
    # Idempotent repair preserves the exact document.
    neutral.write_bytes(b'{ "future": [1, 2], "secret": "canary" }\n')
    before=neutral.read_bytes(); trace.write_text('')
    run(['--product',product]); assert neutral.read_bytes()==before and len(init_events())==expected_inits

# All files changed by Claude registration are protected before the mocked
# CLI can mutate any one, whether the explicit target exists or is absent.
for target_name in ['installed_plugins.json','known_marketplaces.json','settings.json']:
    for exists in [False,True]:
        reset_case()
        claude_home=pathlib.Path(env['CLAUDE_CONFIG_DIR'])
        targets={
            'installed_plugins.json':claude_home/'plugins/installed_plugins.json',
            'known_marketplaces.json':claude_home/'plugins/known_marketplaces.json',
            'settings.json':claude_home/'settings.json',
        }
        target=targets[target_name]
        target.parent.mkdir(parents=True,exist_ok=True)
        original=b'{"plugins":{},"canary":true}'
        if exists: target.write_bytes(original)
        for name,path in targets.items():
            if name != target_name:
                path.parent.mkdir(parents=True,exist_ok=True)
                path.write_bytes(b'{"sibling":"preserve"}')
        before={name:(path.read_bytes() if path.exists() else None) for name,path in targets.items()}
        output=run(['--product','claude'],1,{'AGENT_NOTIFICATIONS_CONFIG':str(target)})
        assert not any(e[:1]==['claude'] for e in events())
        after={name:(path.read_bytes() if path.exists() else None) for name,path in targets.items()}
        assert after==before
        protected=json.loads(request.read_text())['protectedPaths']
        assert path_ids(protected)==path_ids([
            pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/installed_plugins.json',
            pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/known_marketplaces.json',
            pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'settings.json',
        ])
reset_case()
legacy=pathlib.Path(env['HOME'])/'.claude/claude-notifications-go/config.json'
legacy.parent.mkdir(parents=True); legacy.write_bytes(b'{ "future": {"x":1} }\n')
before=legacy.read_bytes(); run(['--product','both'])
assert legacy.read_bytes()==before
assert not (pathlib.Path(env['XDG_CONFIG_HOME'])/'agent-notifications/config.json').exists()
# Recorded active root wins over a higher unrelated glob. Baseline absent blocks.
def historical(value=b'{"personalized":true}', version='1.40.0'):
    home=pathlib.Path(env['CLAUDE_CONFIG_DIR'])
    active=home/'plugins/cache/claude-notifications-go/claude-notifications-go'/version
    (active/'config').mkdir(parents=True); (active/'config/config.json').write_bytes(value)
    (active/'bin').mkdir(); (active/'bin/runtime').write_bytes(b'working-runtime')
    other=active.parent/'9.99.99/config'; other.mkdir(parents=True); (other/'config.json').write_bytes(b'{}')
    (home/'plugins/installed_plugins.json').write_text(__import__('json').dumps({'plugins':{'claude-notifications-go@claude-notifications-go':[{'installPath':str(active),'version':version}]}}))
    return active
import json
for value in [b'{"personalized":true}',b'{}']:
    reset_case(); active=historical(value)
    output=run(['--product','claude'],1)
    if os.name == 'nt':
        assert 'Diagnostics could not be fully saved' in output and 'Diagnostics:' not in output
        assert 'import-required' not in output and not any(e[:1]==['claude'] for e in events())
    else:
        diagnostic_path=shlex.split(next(line for line in output.splitlines() if line.startswith('Diagnostics:')).split(': ',1)[1])[0]
        diagnostic=pathlib.Path(native_shell_path(diagnostic_path)).read_text()
        assert 'import-required' in diagnostic and 'import-required' not in output and not any(e[:1]==['claude'] for e in events())
    assert (active/'bin/runtime').read_bytes()==b'working-runtime'
    assert path_ids(json.loads(request.read_text())['activeBundleRoots'])==path_ids([active])
# A verified exact-version template permits initialization. Personalized bytes
# against that same template still stop; current template is never substituted.
dest=web/'download/v1.40.0'; dest.mkdir()
(dest/'config.json').write_bytes(b'{}')
(dest/'checksums.txt').write_bytes((hashlib.sha256(b'{}').hexdigest()+'  config.json\n').encode('ascii'))
reset_case(); active=historical(b'{}'); run(['--product','claude'])
assert len(init_events())==1
reset_case(); active=historical(b'{"custom":1}')
run(['--product','claude'],1); assert not any(e[:1]==['claude'] for e in events())
# Alternate Claude home candidate and explicit target overlap.
reset_case()
custom=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'claude-notifications-go/config.json'
custom.parent.mkdir(); custom.write_text('{"custom":true}')
run(['--product','claude'],1); assert not any(e[:1]==['claude'] for e in events())
reset_case(); active=historical()
run(['--product','claude'],1,{'AGENT_NOTIFICATIONS_CONFIG':str(active/'config/config.json')})
assert not any(e[:1]==['claude'] for e in events())
# Codex ignores unrelated registry/baselines, but retains shared history guards.
for product in ['codex', 'claude', 'both']:
    reset_case()
    registry=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/installed_plugins.json'
    registry.parent.mkdir(); registry.write_text('{malformed')
    request_paths.clear()
    run(['--product',product],0 if product=='codex' else 1)
    if product=='codex':
        req=json.loads(request.read_text())
        assert path_ids(req['activeBundleRoots'])==[]
        assert path_ids(req['refreshDirs'])==path_ids([pathlib.Path(env['CODEX_HOME'])/'claude-notifications-go'])
        assert len(init_events())==1
        assert not any(e[:1]==['claude'] for e in events())
        assert not any('v1.40.0' in path for path in request_paths)
    else:
        # Verified helper metadata reads precede rejection; no host mutation.
        assert not any(e[:1] in (['claude'], ['setup-codex']) or e[:2]==['config','init'] for e in events())
reset_case()
custom=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'claude-notifications-go/config.json'
custom.parent.mkdir(); custom.write_text('{"custom":true}')
run(['--product','codex'],1)
assert not any(e[:1]==['setup-codex'] for e in events())
reset_case()
dest=pathlib.Path(env['CODEX_HOME'])/'claude-notifications-go'
(dest/'config').mkdir(parents=True); (dest/'config/config.json').write_text('{}')
run(['--product','codex'],1)
assert not any(e[:1]==['setup-codex'] for e in events())
run(['--product','codex'],1,{'AGENT_NOTIFICATIONS_CONFIG':str(dest/'explicit.json')})
assert not any(e[:1]==['setup-codex'] for e in events())
def config_retry(output):
    assert 'Config-only retry' not in output
    if os.name == 'nt':
        assert 'Diagnostics could not be fully saved' in output and 'Diagnostics:' not in output
        assert 'command in Diagnostics' not in output
        # Persistence is unavailable, but the retained fixture helper still
        # permits the same explicit config-only contract check without effects.
        stage=max(pathlib.Path(native_shell_path(env['TMPDIR'])).glob('bootstrap-config-*'), key=lambda p: p.stat().st_mtime_ns)
        command=[(stage/asset_name).as_posix(), 'config', 'init', '--json']
        return shlex.join(command), command
    diagnostic_path=shlex.split(next(line for line in output.splitlines() if line.startswith('Diagnostics:')).split(': ',1)[1])[0]
    diagnostic=pathlib.Path(native_shell_path(diagnostic_path)).read_text()
    assert 'config-only retry command in Diagnostics' in output
    line=next(line for line in diagnostic.splitlines() if line.startswith('Config-only retry'))
    shell_command=line.split(': ',1)[1]
    return shell_command, shlex.split(shell_command)
# Reserved exit 3 means committed registration; retain checksum-verified helper.
for product in ['codex','both']:
    reset_case()
    output=run(['--product',product],1,{'FAIL_SETUP_INIT':'1'})
    assert 'Setup incomplete:' in output and 'registration failed' not in output
    assert (pathlib.Path(env['CODEX_HOME'])/'fixture-registration').read_text()=='registered'
    # Claude's completed phase remains committed if the later Codex phase fails.
    assert len(init_events()) == (1 if product == 'both' else 0)
    shell_command, command=config_retry(output)
    assert pathlib.Path(native_shell_path(command[0])).read_bytes()==payload_file.read_bytes()
    assert command[1:]==['config','init','--json']
    trace.write_text(''); requests_before=list(request_paths)
    r=subprocess.run([bash,'-c',shell_command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
    assert r.returncode==0, r.stdout.decode()
    assert events()==[['config','init','--json']] and request_paths==requests_before
# A failed phase never initializes config; a completed sibling keeps its init.
for product,fail in [('claude',{'FAIL_CLAUDE':'1'}),('codex',{'FAIL_REGISTER':'1'}),('both',{'FAIL_REGISTER':'1'})]:
    reset_case(); run(['--product',product],1,fail)
    assert len(init_events()) == (1 if product == 'both' else 0)
# Init failure retains a verified executable for a config-only retry.
reset_case()
output=run(['--product','both'],1,{'FAIL_INIT':'1'})
assert 'Setup incomplete:' in output and 'Config-only retry' not in output
shell_command, command=config_retry(output); trace.write_text('')
assert pathlib.Path(native_shell_path(command[0])).read_bytes()==payload_file.read_bytes()
assert command[1:]==['config','init','--json']
r=subprocess.run([bash,'-c',shell_command],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=20)
assert r.returncode==0, r.stdout.decode()
assert events()==[['config','init','--json']]
# Offline staging failure cannot touch a working runtime/registration.
trace.write_text(''); active=pathlib.Path(env['CLAUDE_CONFIG_DIR'])/'plugins/cache/claude-notifications-go/claude-notifications-go/1.42.0'
before=(active/'bin'/ (asset_name if os.name=='nt' else 'claude-notifications')).read_bytes()
run(['--product','both'],1,{'BOOTSTRAP_RELEASES_BASE_URL':base+'/offline'})
assert (active/'bin'/ (asset_name if os.name=='nt' else 'claude-notifications')).read_bytes()==before and not events()
# A checksum-valid older helper without config commands fails before touching
# host registration. A hostile Python environment cannot disable verification.
valid_payload=payload_file.read_bytes()
bad_capability=valid_payload.replace(b"assert args[0]=='config'",b"raise SystemExit(2)")
payload_file.write_bytes(bad_capability)
checksums=payload_file.parent/'checksums.txt'; valid_checksums=checksums.read_bytes()
checksums.write_bytes((hashlib.sha256(bad_capability).hexdigest()+'  '+asset_name+'\n').encode('ascii'))
trace.write_text('')
run(['--product','both'],1)
assert not any(e[:1]==['claude'] or e[:1]==['setup-codex'] for e in events())
payload_file.write_bytes(valid_payload+b'\n#tampered')
trace.write_text('')
run(['--product','both'],1,{'PYTHONOPTIMIZE':'2'})
assert not events()
payload_file.write_bytes(valid_payload); checksums.write_bytes(valid_checksums)
def place_runtime_cmd(dest, src):
    if dest.exists() or not src or not os.path.isfile(src):
        return
    n = src.replace('\\', '/').lower()
    base = os.path.basename(n)
    if base in ('python', 'python.exe', 'python3', 'python3.exe', 'node', 'node.exe') and (
            '/windowsapps/' in n or '/system32/' in n or '/syswow64/' in n):
        return
    dest.write_text('#!/bin/sh\nexec {} "$@"\n'.format(shlex.quote(src.replace('\\', '/'))), newline='\n')
    dest.chmod(0o755)
def fixture_tool(name):
    found = shutil.which(name)
    if found:
        return found
    # Native Windows lookup applies PATHEXT, while our Bash wrappers have no
    # extension. These paths are used only as shell commands in child scripts.
    for directory in os.environ['PATH'].split(os.pathsep):
        candidate = pathlib.Path(directory) / name
        if candidate.is_file():
            return str(candidate)
    return None
if fixture_tool('node'):
    node_only = sandbox / 'http-node-only-bin'
    node_only.mkdir()
    for name in ['bash', 'sh', 'mktemp', 'rm', 'cat', 'chmod', 'mkdir', 'ln', 'uname',
                 'tr', 'wc', 'head', 'cp', 'mv', 'env', 'true', 'false', 'grep', 'sed', 'awk',
                 'tar', 'gzip', 'curl', 'node', 'sha256sum', 'shasum', 'dirname', 'realpath', 'touch']:
        place_runtime_cmd(node_only / name, fixture_tool(name))
    assert not (node_only / 'python3').exists()
    reset_case()
    env['PATH'] = str(cli) + os.pathsep + str(node_only)
    run(['--product', 'codex'])
    assert (pathlib.Path(env['XDG_CONFIG_HOME']) / 'agent-notifications/config.json').exists()
    print('node-only bootstrap HTTP e2e passed')
else:
    print('SKIP node-only bootstrap HTTP e2e: node not available')
print('protected flow fixtures passed (fake config CLI; real Go integration pending)')
server.shutdown(); server.server_close()
print('local HTTP / curl-pipe PTY adapter fixtures passed (fake installer and binary)')
PY

# Stage output is a public contract: success hides machine/binary banners but
# retains warnings privately and shell state; failure retains diagnostics and its code.
(
    # HTTP reset_case recreates HOME; start a new capture session after that
    # fixture instead of reusing its deleted parent-shell diagnostic file.
    _DIAGNOSTIC_LOG=""; _DIAGNOSTIC_ROOT=""; _DIAGNOSTIC_FAILED=false
    _DIAGNOSTIC_LABEL=""; _DIAGNOSTIC_NOTED=false
    emit_stage() { PLUGIN_ROOT=fixture-installed; printf 'Ready to use!\n{"generation":12}\n\033[33mwarning: extension needs activation\033[0m\n\033[33m  Log out and log back in, then run:\033[0m\n  gnome-extensions enable fixture@example.test\n'; printf 'phase prepare\nSetting up notifications: agent notify...\n' >&2; echo 'warning: fixture optional setup' >&2; }
    run_setup_stage 'Installing fixture' emit_stage > "$SANDBOX/stage-ok.out" 2> "$SANDBOX/stage-ok.err"
    [ "$PLUGIN_ROOT" = fixture-installed ]
    grep -F 'Installing fixture...' "$SANDBOX/stage-ok.out"
    run_setup_stage 'Preparing fixture settings' emit_stage > "$SANDBOX/stage-prepare.out" 2>/dev/null
    if [ "$_PROGRESS_JOURNAL_DISABLED" = true ]; then
        grep -F 'Preparing fixture settings...' "$SANDBOX/stage-prepare.out"
    elif grep -F 'Preparing fixture settings...' "$SANDBOX/stage-prepare.out"; then
        exit 1
    fi
    if grep -E 'Ready to use|generation' "$SANDBOX/stage-ok.out"; then exit 1; fi
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            [ -z "$_DIAGNOSTIC_LOG" ] && [ "$_DIAGNOSTIC_FAILED" = true ]
            report_diagnostics > "$SANDBOX/native-diagnostic.out" 2>&1
            grep -F 'Diagnostics could not be fully saved' "$SANDBOX/native-diagnostic.out"
            if grep -F 'Diagnostics:' "$SANDBOX/native-diagnostic.out"; then exit 1; fi
            [ ! -s "$SANDBOX/stage-ok.err" ] ;;
        *)
    grep -F 'Log out and log back in' "$_DIAGNOSTIC_LOG"
    grep -F 'gnome-extensions enable fixture@example.test' "$_DIAGNOSTIC_LOG"
    grep -F 'warning: fixture optional setup' "$_DIAGNOSTIC_LOG"
    [ ! -s "$SANDBOX/stage-ok.err" ]
    if grep -E 'phase prepare|Setting up notifications:' "$SANDBOX/stage-ok.err"; then exit 1; fi
    # A real stage failure must return and preserve recovery advice privately.
    INSTALLED_JSON="$SANDBOX/missing-installed-plugins.json"
    status=0
    run_setup_stage 'Locating fixture plugin' find_plugin_root > "$SANDBOX/root-fail.out" 2> "$SANDBOX/root-fail.err" || status=$?
    [ "$status" -eq 1 ]
    grep -F 'installed_plugins.json not found' "$_DIAGNOSTIC_LOG"
    grep -F 'Try restarting Claude' "$_DIAGNOSTIC_LOG"
    emit_failure() { echo 'diagnostic stdout'; echo 'diagnostic stderr' >&2; return 3; }
    status=0
    run_setup_stage 'Failing fixture' emit_failure > "$SANDBOX/stage-fail.out" 2> "$SANDBOX/stage-fail.err" || status=$?
    [ "$status" -eq 3 ]
    grep -F 'diagnostic stdout' "$_DIAGNOSTIC_LOG"
    grep -F 'diagnostic stderr' "$_DIAGNOSTIC_LOG"
    [ ! -s "$SANDBOX/stage-fail.err" ]
    BOOTSTRAP_VERBOSE=1 run_setup_stage 'Verbose fixture' emit_stage > "$SANDBOX/stage-verbose.out" 2>/dev/null
    bootstrap_diagnostics view "$_DIAGNOSTIC_LOG" > "$SANDBOX/stage-verbose-details.out"
    grep -F 'generation' "$SANDBOX/stage-verbose-details.out"
            ;;
    esac
    PRODUCT=both CLAUDE_AGENT_NOTIFY_STATUS='not configured by this run' CODEX_AGENT_NOTIFY_STATUS='not configured by this run'
    print_iterm2_python_api_notice() { :; }
    print_success > "$SANDBOX/summary.out"
    grep -F 'Claude - installed; restart required.' "$SANDBOX/summary.out"
    grep -F 'Codex - installed; restart required.' "$SANDBOX/summary.out"
    grep -F 'Run /hooks in Codex' "$SANDBOX/summary.out"
    grep -F 'not configured by this run' "$SANDBOX/summary.out"
    grep -F 'Delivery has not been verified.' "$SANDBOX/summary.out"
    [ "$(grep -c '^Installation complete$' "$SANDBOX/summary.out")" -eq 1 ]
    BOOTSTRAP_SUMMARY_FILE="$SANDBOX/aggregate.txt"
    print_success > "$SANDBOX/collected.out"
    [ ! -s "$SANDBOX/collected.out" ]
    print_success > "$SANDBOX/collected.out"
    [ "$(grep -c 'Delivery has not been verified.' "$BOOTSTRAP_SUMMARY_FILE")" -eq 1 ]
    [ "$(sed -n '$p' "$BOOTSTRAP_SUMMARY_FILE")" = "$(print_delivery_note)" ]
    _PROGRESS_IDS=(opencode gemini); _PROGRESS_OUTCOMES=(completed state_requires_inspection); _PROGRESS_PHASES=(complete registration)
    report_progress 1 2> "$SANDBOX/partial-progress.out"
    grep -F 'OpenCode     completed' "$SANDBOX/partial-progress.out"
    grep -F 'Gemini CLI   state requires inspection' "$SANDBOX/partial-progress.out"
    _PROGRESS_JOURNAL_DISABLED=false
    report_progress 0 2> "$SANDBOX/success-progress.out"
    if grep -F 'OpenCode     completed' "$SANDBOX/success-progress.out"; then exit 1; fi
    _PROGRESS_JOURNAL_DISABLED=true
    report_progress 0 2> "$SANDBOX/disabled-progress.out"
    grep -F 'OpenCode     completed; restart required' "$SANDBOX/disabled-progress.out"
    grep -F 'Gemini CLI   state requires inspection' "$SANDBOX/disabled-progress.out"
)

# Structured results distinguish a successful opt-out from an installed sibling.
cat > "$SANDBOX/wizard-summary.json" <<'JSON_STATUS'
{"outcome":"completed","targets":[{"client":"claude","unit":"agent-notify","outcome":"absent"},{"client":"codex","unit":"agent-notify","outcome":"installed"}]}
JSON_STATUS
[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" claude auto)" = 'not installed (existing opt-out kept)' ]
[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" codex)" = installed ]
[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" other)" = 'setup completed; status not checked' ]

[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" claude true)" = 'not installed' ]
