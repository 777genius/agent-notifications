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
for tool in bash sh env cygpath python3 node curl wget tar gzip unzip zip mktemp rm cat cp mv chmod mkdir ln uname tr wc head cmp grep sed awk dirname basename find sort sha256sum shasum cut xargs sleep date stat diff touch readlink dd od go gcc cc pkg-config; do
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
for args in "--products gemini,gemini --webhook" "--products claude,,gemini --webhook" "--product gemini --webhook --agent-notify"; do
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
# A real disposable PATH executable covers common --version output formats.
# No installed user host is queried by these prerequisite checks.
(
    mkdir -p "$SANDBOX/version-cli"
    cat > "$SANDBOX/version-cli/opencode" <<'HOST_VERSION'
#!/bin/bash
[ "$#" -eq 1 ] && [ "$1" = --version ] || exit 2
[ "$PWD" -ef "$HOME" ] && [ "$HOME" -ef "$USERPROFILE" ] || exit 3
case "$PWD" in */bootstrap-opencode-TEST-*/profile) ;; *) exit 4 ;; esac
cat "${BASH_SOURCE[0]%/*}/version"
HOST_VERSION
    chmod +x "$SANDBOX/version-cli/opencode"
    export PATH="$SANDBOX/version-cli:$PATH"
    PRODUCT=opencode
    OPENCODE_ARGS=(--webhook)
    for version in '1.18.29' '1.18.33' 'v1.18.33' 'OpenCode version: v1.18.33' '1.19.0' '2.0.0' 'v2.0.21'; do
        printf '%s\n' "$version" > "$SANDBOX/version-cli/version"
        check_prerequisites
    done
    for version in '1.18.28' '1.17.99' '3.0.0' '2.0.0-beta.1' '2.0.0+build' '02.0.0' '2.00.0' '2.0.000' '2.9999999.0' 'OpenCode v2.0.0 (compatibility 1.18.33)' 'unknown' $'2.0.0\n1.18.33'; do
        printf '%s\n' "$version" > "$SANDBOX/version-cli/version"
        if ( check_prerequisites ); then
            echo "accepted unsupported host output: $version" >&2
            exit 1
        fi
    done
)
# Gemini's prerequisite may execute only the disposable version fixture here.
# Assert actual isolated cwd/home, dotenv sentinels and system redirects rather
# than matching shell source. A changed version must stop before installation.
(
    mkdir -p "$SANDBOX/gemini-version-cli"
    cat > "$SANDBOX/gemini-version-cli/gemini" <<'GEMINI_VERSION'
#!/bin/bash
[ "$#" -eq 1 ] && [ "$1" = --version ] || exit 2
[ "$PWD" -ef "$HOME" ] && [ "$USERPROFILE" -ef "$HOME" ] && [ "$GEMINI_CLI_HOME" -ef "$HOME" ] || exit 3
[ -z "${GEMINI_SETUP_ENV_CANARY:-}" ] || exit 4
for file in "$HOME/.gemini/.env" "$HOME/.env"; do
    [ -f "$file" ] && [ ! -s "$file" ] || exit 5
done
for file in "$GEMINI_CLI_SYSTEM_SETTINGS_PATH" "$GEMINI_CLI_SYSTEM_DEFAULTS_PATH" "$GEMINI_CLI_TRUSTED_FOLDERS_PATH"; do
    [ -f "$file" ] || exit 6
done
printf '0.62.0\n'
GEMINI_VERSION
    chmod +x "$SANDBOX/gemini-version-cli/gemini"
    export PATH="$SANDBOX/gemini-version-cli:$PATH" GEMINI_SETUP_ENV_CANARY=synthetic-not-a-secret
    PRODUCT=gemini
    OPENCODE_ARGS=(--webhook)
    check_prerequisites
    sed 's/0\.62\.0/0.63.0/' "$SANDBOX/gemini-version-cli/gemini" > "$SANDBOX/gemini-version-cli/next"
    mv "$SANDBOX/gemini-version-cli/next" "$SANDBOX/gemini-version-cli/gemini"
    chmod +x "$SANDBOX/gemini-version-cli/gemini"
    if (check_prerequisites); then
        echo "accepted unqualified Gemini version" >&2; exit 1
    fi
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
)
_retry_package=$(sed -n 's/^  Verified package retained: //p' "$SANDBOX/retry.log")
[ -f "$_retry_package" ] || { echo "retry package removed: $_retry_package"; exit 1; }
[ ! -e "$SANDBOX/config-stage" ] || { echo 'preflight state retained with portable asset'; exit 1; }
grep -F -- "Verified package retained: $_retry_package" "$SANDBOX/retry.log" >/dev/null
grep -F -- "Use the wizard's retry or next resume command above" "$SANDBOX/retry.log" >/dev/null
BOOTSTRAP_TAG=""
WIZARD_PACKAGE_ROOT=""
# setup_marketplace self-heals a marketplace declared under a retired repo
# name, but leaves an unrelated source conflict alone.
(
    # shellcheck disable=SC2034 # consumed by the sourced setup_marketplace
    MARKETPLACE_SOURCE="new/repo"
    # shellcheck disable=SC2034
    MARKETPLACE_NAME="claude-notifications-go"
    # shellcheck disable=SC2034
    LEGACY_MARKETPLACE_REPOS="old/retired-repo"
    config_preflight() { :; }
    calls="$SANDBOX/marketplace-calls"; declared_repo="old/retired-repo"
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
    : > "$calls"; rm -f "$SANDBOX/marketplace-add-called"
    setup_marketplace
    [ "$(grep -c '^plugin marketplace add' "$calls")" = 2 ] || { echo "expected retry add after self-heal"; exit 1; }
    grep -q '^plugin marketplace remove claude-notifications-go$' "$calls" || { echo "expected self-heal remove"; exit 1; }

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
    install_gemini > "$SANDBOX/gemini-output"
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
    install_gemini > "$SANDBOX/gemini-output"
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
stage_historical_baselines() { :; }; stage_config_helper() { _CONFIG_STAGE=$(mktemp -d); }; config_preflight() { :; }; initialize_config() { :; }
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
    for args in '--products opencode' '--products claude,gemini' '--json' '--product gemini --json' '--ui=bad' '--plain --ui=rich' '--navigation none' '--agent-notify --agent-notify'; do
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
    'setup-products capabilities') printf 'setup-products-v1 claude codex opencode gemini\n' ;;
    'setup-products features') printf 'terminal-selector-v1\n' ;;
    'setup-products select') printf '%s\n' "${TEST_SELECTION:-cursor}" ;;
    'setup-products channels') [ "${TEST_CANCEL:-}" = channels ] || printf '%s\n' "$TEST_CHANNELS" ;;
    'setup-products confirm')
        [ "${TEST_CANCEL:-}" != confirm ] || exit 0
        while [ "$#" -gt 0 ]; do
            if [ "$1" = --intent-file ]; then printf 'TEST immutable checkpoint' > "$2"; break; fi
            shift
        done
        printf 'approved\n' ;;
    'setup-products intent-args') cat "$TEST_ATOMS" ;;
    'setup-products preflight') exit "${TEST_PREFLIGHT_EXIT:-0}" ;;
    'setup-notifications --help') printf '%s\n' '--policy-only --preserve-enabled' ;;
    'setup-notifications wizard')
        if [ "$4" = inspect ]; then printf '{"targets":[]}\n'; else exit "${TEST_WIZARD_EXIT:-0}"; fi ;;
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
bootstrap_abs_command() { printf unexpected-discovery >> "$TEST_TRACE/mutation"; return 1; }
install_legacy_products() { printf legacy >> "$TEST_TRACE/mutation"; return 99; }
install_opencode() { printf opencode >> "$TEST_TRACE/mutation"; return 99; }
install_gemini() { printf gemini >> "$TEST_TRACE/mutation"; return 99; }
initialize_config() { printf config >> "$TEST_TRACE/mutation"; return 99; }
main "$@"
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
         ('scope-root', profile), ('client-executable', vendor)]
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
    calls = [p.read_bytes().split(b'\0')[:-1] for p in trace.iterdir() if p.name != 'mutation']
    return process.returncode, calls, output.decode(errors='replace')
def call(calls, prefix):
    return next(c for c in calls if c[:len(prefix)] == [s.encode() for s in prefix])
def value(args, key): return args[args.index(key.encode())+1]
for selection in (['--product', 'cursor'], ['--products', 'cursor'], []):
    for channels in (['--desktop'], ['--webhook'], ['--desktop', '--webhook'], []):
        code, calls, output = run(selection+requested+['--agent-notify']+channels)
        # Old parsing rejected Cursor; dropped CSV/selector IDs prevented dispatch.
        assert code == 0, output
        confirm = call(calls, ['setup-products', 'confirm'])
        # Original explicit scope must reach confirmation before frozen argv replaces it.
        assert value(confirm, '--scope-root') == requested[1].encode()
        assert value(confirm, '--client-executable') == requested[3].encode()
        # Desktop-only/webhook-only must not broaden to the absent sibling channel.
        for flag in ('--desktop', '--webhook'):
            assert (flag.encode() in confirm) == (not channels or flag in channels)
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
# Cancellation at either question must finish without writing or granting consent.
for cancel in ('channels', 'confirm'):
    code, calls, output = run(['--product', 'cursor']+requested, {'TEST_CANCEL': cancel})
    assert code == 0 and not any(c[:1] == [b'setup-notifications'] for c in calls), output
    # Cancellation must also stop legacy/observer/config writes and discovery.
    assert not (trace/'mutation').exists(), output
# A skipped MCP request must not be converted to native consent or a wizard write.
code, calls, output = run(['--product', 'cursor']+requested+['--skip-agent-notify', '--desktop'])
assert code == 0 and not any(c[:1] == [b'setup-notifications'] for c in calls), output
# A skipped unit must not run another installer/config writer before returning.
assert not (trace/'mutation').exists(), output
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
    assert code in (73,1) and not any(c[:4] == [b'setup-notifications',b'wizard',b'--action',b'inspect'] for c in calls), output
    # Checkpoint/caller refusal must not enter unrelated installers/config writes.
    assert not (trace/'mutation').exists(), output
# Missing/duplicate selected scope, mixed products and navigation must fail before acquisition.
for args in (['--product','cursor'], ['--product','cursor']+requested[:2],
        ['--product','cursor']+requested+requested[:2], ['--products','claude,cursor']+requested,
        ['--product','claude']+requested, ['--product','cursor']+requested+['--navigation','none']):
    code, calls, output = run(args)
    assert code != 0 and not calls and not (trace/'mutation').exists(), output
print('Cursor frozen bootstrap argv and refusal fixtures passed (composition only)')
PY_CURSOR
)

printf 'bootstrap product unit fixtures passed\n'
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
(web / 'bootstrap.sh').write_text(fixture_bootstrap, encoding='utf-8')
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
    ))
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
    return output.decode()
assert 'No controlling TTY' in run([], 1)
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
dispatch += '\ncheck_prerequisites() { :; }\nresolve_bootstrap_release() { :; }\nstage_historical_baselines() { :; }\nstage_config_helper() { _CONFIG_STAGE=$(mktemp -d); }\nconfig_preflight() { :; }\ninitialize_config() { :; }\ninstall_claude() { echo CLAUDE_ADAPTER; }\ninstall_codex() { echo CODEX_ADAPTER; }\nmain "$@"\n'
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
    assert 'import-required' in output and not any(e[:1]==['claude'] for e in events())
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
# Reserved exit 3 means committed registration; retain checksum-verified helper.
for product in ['codex','both']:
    reset_case()
    output=run(['--product',product],1,{'FAIL_SETUP_INIT':'1'})
    assert 'Partial setup' in output and 'registration failed' not in output
    assert (pathlib.Path(env['CODEX_HOME'])/'fixture-registration').read_text()=='registered'
    # Claude's completed phase remains committed if the later Codex phase fails.
    assert len(init_events()) == (1 if product == 'both' else 0)
    line=next(line for line in output.splitlines() if line.startswith('Config-only retry'))
    shell_command=line.split(': ',1)[1]
    command=shlex.split(shell_command)
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
assert 'Partial setup' in output and 'Config-only retry' in output
line=next(line for line in output.splitlines() if line.startswith('Config-only retry'))
shell_command=line.split(': ',1)[1]; command=shlex.split(shell_command); trace.write_text('')
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
    dest.write_text('#!/bin/sh\nexec {} "$@"\n'.format(shlex.quote(src.replace('\\', '/'))))
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
                 'tar', 'gzip', 'curl', 'node', 'sha256sum', 'shasum', 'dirname', 'realpath']:
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
# preserves warnings and shell state; failure replays diagnostics and its code.
(
    emit_stage() { PLUGIN_ROOT=fixture-installed; printf 'Ready to use!\n{"generation":12}\n\033[33mwarning: extension needs activation\033[0m\n\033[33m  Log out and log back in, then run:\033[0m\n  gnome-extensions enable fixture@example.test\n'; printf 'phase prepare\nSetting up notifications: agent notify...\n' >&2; echo 'warning: fixture optional setup' >&2; }
    run_setup_stage 'Installing fixture' emit_stage > "$SANDBOX/stage-ok.out" 2> "$SANDBOX/stage-ok.err"
    [ "$PLUGIN_ROOT" = fixture-installed ]
    grep -F 'Installing fixture...' "$SANDBOX/stage-ok.out"
    if grep -E 'Ready to use|generation' "$SANDBOX/stage-ok.out"; then exit 1; fi
    grep -F 'Log out and log back in' "$SANDBOX/stage-ok.out"
    grep -F 'gnome-extensions enable fixture@example.test' "$SANDBOX/stage-ok.out"
    grep -F 'warning: fixture optional setup' "$SANDBOX/stage-ok.err"
    if grep -E 'phase prepare|Setting up notifications:' "$SANDBOX/stage-ok.err"; then exit 1; fi
    # A real stage failure must return so captured recovery advice is replayed.
    INSTALLED_JSON="$SANDBOX/missing-installed-plugins.json"
    status=0
    run_setup_stage 'Locating fixture plugin' find_plugin_root > "$SANDBOX/root-fail.out" 2> "$SANDBOX/root-fail.err" || status=$?
    [ "$status" -eq 1 ]
    grep -F 'installed_plugins.json not found' "$SANDBOX/root-fail.err"
    grep -F 'Try restarting Claude' "$SANDBOX/root-fail.err"
    emit_failure() { echo 'diagnostic stdout'; echo 'diagnostic stderr' >&2; return 3; }
    status=0
    run_setup_stage 'Failing fixture' emit_failure > "$SANDBOX/stage-fail.out" 2> "$SANDBOX/stage-fail.err" || status=$?
    [ "$status" -eq 3 ]
    grep -F 'diagnostic stdout' "$SANDBOX/stage-fail.err"
    grep -F 'diagnostic stderr' "$SANDBOX/stage-fail.err"
    BOOTSTRAP_VERBOSE=1 run_setup_stage 'Verbose fixture' emit_stage > "$SANDBOX/stage-verbose.out" 2>/dev/null
    grep -F 'generation' "$SANDBOX/stage-verbose.out"
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
    grep -F 'Delivery has not been verified.' "$BOOTSTRAP_SUMMARY_FILE"
)

# Structured results distinguish a successful opt-out from an installed sibling.
cat > "$SANDBOX/wizard-summary.json" <<'JSON_STATUS'
{"outcome":"completed","targets":[{"client":"claude","unit":"agent-notify","outcome":"absent"},{"client":"codex","unit":"agent-notify","outcome":"installed"}]}
JSON_STATUS
[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" claude auto)" = 'not installed (existing opt-out kept)' ]
[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" codex)" = installed ]
[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" other)" = 'setup completed; status not checked' ]

[ "$(wizard_tool_status "$SANDBOX/wizard-summary.json" claude true)" = 'not installed' ]
