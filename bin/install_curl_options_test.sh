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
