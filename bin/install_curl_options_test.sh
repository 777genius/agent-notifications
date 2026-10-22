#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# configure_curl_options regressions (issue #103). No network: curl is stubbed.
set -eo pipefail
root=$(cd "$(dirname "$0")" && pwd)
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
test_env_setup "$sandbox"
sed '/^main "\$@"$/d' "$root/install.sh" > "$sandbox/functions.sh"
printf 'ca\n' > "$sandbox/corp-ca.pem"
printf 'ssl\n' > "$sandbox/ssl-cert.pem"

failures=0
check() {
    local message="$1" platform="$2" expected="$3"
    shift 3
    local actual
    actual=$(
        export INSTALL_TARGET_DIR="$sandbox/target"
        unset CURL_CA_BUNDLE SSL_CERT_FILE
        for assignment in "$@"; do export "$assignment"; done
        source "$sandbox/functions.sh"
        curl() { printf ' --ssl-no-revoke --http1.1\n'; }
        # Hermetic on real Git Bash, where Git's public CA bundle exists.
        find_windows_default_ca_bundle() { return 1; }
        PLATFORM="$platform"
        configure_curl_options
        printf '%s' "${CURL_EXTRA_OPTS[*]}"
    )
    if [ "$actual" = "$expected" ]; then
        echo "PASS: $message"
    else
        echo "FAIL: $message (expected '$expected', got '$actual')" >&2
        failures=$((failures + 1))
    fi
}

check "windows without CA env keeps only --ssl-no-revoke" windows "--ssl-no-revoke"
check "windows passes CURL_CA_BUNDLE as --cacert" windows \
    "--ssl-no-revoke --cacert $sandbox/corp-ca.pem" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows falls back to SSL_CERT_FILE" windows \
    "--ssl-no-revoke --cacert $sandbox/ssl-cert.pem" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "windows prefers CURL_CA_BUNDLE over SSL_CERT_FILE" windows \
    "--ssl-no-revoke --cacert $sandbox/corp-ca.pem" \
    "CURL_CA_BUNDLE=$sandbox/corp-ca.pem" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "windows ignores a missing CURL_CA_BUNDLE file" windows \
    "--ssl-no-revoke --cacert $sandbox/ssl-cert.pem" \
    "CURL_CA_BUNDLE=$sandbox/missing.pem" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "non-windows leaves CA env to curl" linux "" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"

printf 'public\n' > "$sandbox/public-ca.crt"
# A child shell, so the installer's EXIT trap runs as it does in production.
merged=$(SANDBOX="$sandbox" INSTALL_TARGET_DIR="$sandbox/target" TMPDIR="$sandbox" \
    CURL_CA_BUNDLE="$sandbox/corp-ca.pem" "$BASH" -c '
    unset SSL_CERT_FILE
    source "$SANDBOX/functions.sh"
    curl() { printf " --ssl-no-revoke --http1.1\n"; }
    find_windows_default_ca_bundle() { printf "%s" "$SANDBOX/public-ca.crt"; }
    PLATFORM=windows
    configure_curl_options
    bundle="${CURL_EXTRA_OPTS[2]}"
    printf "%s|%s|" "${CURL_EXTRA_OPTS[1]}" "$bundle"
    tr "\n" " " < "$bundle"
')
case "$merged" in
    "--cacert|$sandbox/agent-notifications-ca."*"|public  ca ")
        echo "PASS: windows keeps public CA trust next to CURL_CA_BUNDLE" ;;
    *)
        echo "FAIL: windows should merge CURL_CA_BUNDLE into the public CA bundle (got '$merged')" >&2
        failures=$((failures + 1)) ;;
esac
if ls "$sandbox"/agent-notifications-ca.* >/dev/null 2>&1; then
    echo "FAIL: merged CA bundle should be removed on exit" >&2
    failures=$((failures + 1))
else
    echo "PASS: merged CA bundle is removed on exit"
fi

staged="$sandbox/staged"
mkdir -p "$staged"
# The staged preflight download runs detect_platform in its own subshell.
SANDBOX="$sandbox" INSTALL_TARGET_DIR="$sandbox/target" TMPDIR="$staged" \
    CURL_CA_BUNDLE="$sandbox/corp-ca.pem" AGENT_NOTIFICATIONS_CONFIG="$sandbox/config.json" \
    "$BASH" -c '
    unset SSL_CERT_FILE
    source "$SANDBOX/functions.sh"
    curl() { printf " --ssl-no-revoke --http1.1\n"; }
    find_windows_default_ca_bundle() { printf "%s" "$SANDBOX/public-ca.crt"; }
    install_config_preflight() { return 2; }
    detect_platform() { PLATFORM=windows; configure_curl_options; }
    pin_release_urls() { [ -f "$CURL_CA_MERGED_BUNDLE" ] && printf created > "$SANDBOX/staged-created"; return 1; }
    OFFLINE_MODE=false
    prepare_install_config_preflight "$SANDBOX/target"
' >/dev/null 2>&1 || true
if [ ! -f "$sandbox/staged-created" ]; then
    echo "FAIL: staged preflight should build a merged CA bundle" >&2
    failures=$((failures + 1))
elif ls "$staged"/agent-notifications-ca.* >/dev/null 2>&1; then
    echo "FAIL: staged preflight should remove its merged CA bundle" >&2
    failures=$((failures + 1))
else
    echo "PASS: staged preflight removes its merged CA bundle"
fi

same=$(
    export INSTALL_TARGET_DIR="$sandbox/target"
    export CURL_CA_BUNDLE="$sandbox/public-ca.crt"
    source "$sandbox/functions.sh"
    curl() { printf ' --ssl-no-revoke --http1.1\n'; }
    find_windows_default_ca_bundle() { printf '%s' "$sandbox/public-ca.crt"; }
    PLATFORM=windows
    configure_curl_options
    printf '%s' "${CURL_EXTRA_OPTS[*]}"
)
if [ "$same" = "--ssl-no-revoke --cacert $sandbox/public-ca.crt" ]; then
    echo "PASS: windows passes the public bundle as-is when CURL_CA_BUNDLE is it"
else
    echo "FAIL: windows should not merge the public bundle with itself (got '$same')" >&2
    failures=$((failures + 1))
fi

hint=$(
    export INSTALL_TARGET_DIR="$sandbox/target"
    source "$sandbox/functions.sh"
    PLATFORM=windows
    print_windows_proxy_hint 2>&1
)
if printf '%s' "$hint" | grep -q CURL_CA_BUNDLE; then
    echo "PASS: windows proxy hint mentions CURL_CA_BUNDLE"
else
    echo "FAIL: windows proxy hint should mention CURL_CA_BUNDLE (got '$hint')" >&2
    failures=$((failures + 1))
fi

[ "$failures" -eq 0 ]
