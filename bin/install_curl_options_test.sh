#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# CA selection and transfer regressions (issue #103). No network: curl is stubbed.
set -eo pipefail
root=$(cd "$(dirname "$0")" && pwd)
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
test_env_setup "$sandbox"
sed '/^main "\$@"$/d' "$root/install.sh" > "$sandbox/functions.sh"
printf 'ca\n' > "$sandbox/corp-ca.pem"
printf 'ssl\n' > "$sandbox/ssl-cert.pem"
mkdir -p "$sandbox/curl-bin"
cat > "$sandbox/curl-bin/curl" <<'CURL'
#!/bin/bash
case "$1" in
    --version)
        case "${TEST_CURL_TLS:-schannel}" in
            schannel) tls=Schannel ;;
            openssl) tls=OpenSSL/3.0.8 ;;
            multissl-*)
                backend="${CURL_SSL_BACKEND:-${TEST_CURL_TLS#multissl-}}"
                case "$backend" in
                    schannel) tls='(OpenSSL/3.0.8) Schannel' ;;
                    *) tls='OpenSSL/3.0.8 (Schannel)' ;;
                esac ;;
            schannel-crlf)
                printf 'curl 8.10.1 (Windows) libcurl/8.10.1 Schannel\r\n'
                exit 0 ;;
            empty) exit 0 ;;
            *) exit 1 ;;
        esac
        printf 'curl 8.10.1 (Windows) libcurl/8.10.1 %s zlib/1.3\n' "$tls"
        printf 'Release-Date: 2024-09-18\nProtocols: http https\nFeatures: SSL MultiSSL\n'
        exit "${TEST_CURL_VERSION_STATUS:-0}" ;;
    --help) printf ' --ssl-no-revoke --http1.1\n' ;;
    *) echo 'unexpected curl request' >&2; exit 99 ;;
esac
CURL
chmod +x "$sandbox/curl-bin/curl"
export PATH="$sandbox/curl-bin:$PATH"

failures=0
check() {
    local message="$1" platform="$2" expected="$3"
    shift 3
    local actual
    actual=$(
        export INSTALL_TARGET_DIR="$sandbox/target"
        unset CURL_CA_BUNDLE SSL_CERT_FILE CURL_SSL_BACKEND TEST_CURL_TLS TEST_CURL_VERSION_STATUS TEST_CURL_MISSING
        for assignment in "$@"; do export "$assignment"; done
        source "$sandbox/functions.sh"
        # Hermetic on real Git Bash, where Git's public CA bundle exists.
        find_windows_default_ca_bundle() { return 1; }
        if [ "${TEST_CURL_MISSING:-0}" = 1 ]; then PATH="$sandbox/no-tools"; fi
        PLATFORM="$platform"
        configure_curl_options
        printf '%s|%s' "${CURL_EXTRA_OPTS[*]}" "$WINDOWS_CA_BUNDLE"
    )
    if [ "$actual" = "$expected" ]; then
        echo "PASS: $message"
    else
        echo "FAIL: $message (expected '$expected', got '$actual')" >&2
        failures=$((failures + 1))
    fi
}

check "windows without CA env keeps only --ssl-no-revoke" windows "--ssl-no-revoke|"
check "windows selects CURL_CA_BUNDLE as a verification fallback" windows \
    "--ssl-no-revoke|$sandbox/corp-ca.pem" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows falls back to SSL_CERT_FILE" windows \
    "--ssl-no-revoke|$sandbox/ssl-cert.pem" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "windows prefers CURL_CA_BUNDLE over SSL_CERT_FILE" windows \
    "--ssl-no-revoke|$sandbox/corp-ca.pem" \
    "CURL_CA_BUNDLE=$sandbox/corp-ca.pem" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "windows ignores a missing CURL_CA_BUNDLE file" windows \
    "--ssl-no-revoke|$sandbox/ssl-cert.pem" \
    "CURL_CA_BUNDLE=$sandbox/missing.pem" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "non-windows leaves CA env to curl" linux "|" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows OpenSSL leaves CURL_CA_BUNDLE to curl" windows "--ssl-no-revoke|" \
    "TEST_CURL_TLS=openssl" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows OpenSSL leaves SSL_CERT_FILE to curl" windows "--ssl-no-revoke|" \
    "TEST_CURL_TLS=openssl" "SSL_CERT_FILE=$sandbox/ssl-cert.pem"
check "single-backend OpenSSL ignores a Schannel environment request" windows "--ssl-no-revoke|" \
    "TEST_CURL_TLS=openssl" "CURL_SSL_BACKEND=schannel" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "MultiSSL ignores inactive Schannel" windows "--ssl-no-revoke|" \
    "TEST_CURL_TLS=multissl-openssl" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "MultiSSL uses active Schannel" windows "--ssl-no-revoke|$sandbox/corp-ca.pem" \
    "TEST_CURL_TLS=multissl-schannel" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "MultiSSL uses Schannel selected by CURL_SSL_BACKEND" windows "--ssl-no-revoke|$sandbox/corp-ca.pem" \
    "TEST_CURL_TLS=multissl-openssl" "CURL_SSL_BACKEND=schannel" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "MultiSSL leaves CA env alone when CURL_SSL_BACKEND selects OpenSSL" windows "--ssl-no-revoke|" \
    "TEST_CURL_TLS=multissl-schannel" "CURL_SSL_BACKEND=openssl" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows recognizes active Schannel at the end of a CRLF line" windows "--ssl-no-revoke|$sandbox/corp-ca.pem" \
    "TEST_CURL_TLS=schannel-crlf" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows does not change CA trust when curl version fails" windows "--ssl-no-revoke|" \
    "TEST_CURL_VERSION_STATUS=2" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "windows does not change CA trust when curl version is empty" windows "--ssl-no-revoke|" \
    "TEST_CURL_TLS=empty" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"
check "missing curl leaves options empty" windows "|" \
    "TEST_CURL_MISSING=1" "CURL_CA_BUNDLE=$sandbox/corp-ca.pem"

printf 'public\n' > "$sandbox/public-ca.crt"
# A child shell, so the installer's EXIT trap runs as it does in production.
merged=$(SANDBOX="$sandbox" INSTALL_TARGET_DIR="$sandbox/target" TMPDIR="$sandbox" \
    CURL_CA_BUNDLE="$sandbox/corp-ca.pem" "$BASH" -c '
    unset SSL_CERT_FILE
    source "$SANDBOX/functions.sh"
    find_windows_default_ca_bundle() { printf "%s" "$SANDBOX/public-ca.crt"; }
    PLATFORM=windows
    configure_curl_options
    bundle="$WINDOWS_CA_BUNDLE"
    printf "fallback|%s|" "$bundle"
    tr "\n" " " < "$bundle"
')
case "$merged" in
    "fallback|$sandbox/agent-notifications-ca."*"|public  ca ")
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
    find_windows_default_ca_bundle() { printf '%s' "$sandbox/public-ca.crt"; }
    PLATFORM=windows
    configure_curl_options
    printf '%s|%s' "${CURL_EXTRA_OPTS[*]}" "$WINDOWS_CA_BUNDLE"
)
if [ "$same" = "--ssl-no-revoke|$sandbox/public-ca.crt" ]; then
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

# Exercise real transfer calls through the wrapper, including set -e callers.
mkdir -p "$sandbox/transfer-bin"
cat > "$sandbox/transfer-bin/curl" <<'CURL'
#!/bin/bash
case "$1" in --version|--help) exec "$TEST_OPTIONS_CURL" "$@" ;; esac
kind=native output=""
[ "$1" != --cacert ] || kind=fallback
printf '%s\n' "$kind" >> "$TEST_CASE/calls"
printf '%s\n' "$@" > "$TEST_CASE/$kind.args"
while [ "$#" -gt 0 ]; do
    case "$1" in
        --cacert)
            [ "$2" = "$TEST_EXPECTED_CA" ] || exit 99
            shift 2 ;;
        -o) output="$2"; shift 2 ;;
        *) shift ;;
    esac
done
case "$TEST_TRANSFER_MODE" in
    term|int)
        if [ "$TEST_TRANSFER_MODE" = term ]; then kill -TERM "$PPID"; else kill -INT "$PPID"; fi
        exit 0 ;;
    network)
        printf 'native-output\n\n000'
        printf 'native-network-error\n' >&2
        exit 7 ;;
    bad-ca-file)
        printf 000
        printf 'native-ca-file-error\n' >&2
        exit 77 ;;
    reject)
        printf 000
        printf '%s-certificate-error\n' "$kind" >&2
        exit 60 ;;
    corporate)
        if [ "$kind" = native ]; then
            [ -z "$output" ] || printf partial-native > "$output"
            printf 000
            printf 'native-certificate-error\n' >&2
            exit 60
        fi ;;
esac
if [ -n "$output" ]; then
    printf '%s-body\n\n' "$kind" > "$output"
else
    printf '%s-body\n\n' "$kind"
fi
printf 200
printf '%s-stderr\n' "$kind" >&2
CURL
chmod +x "$sandbox/transfer-bin/curl"

transfer_check() {
    local name="$1" mode="$2" expected_status="$3" expected_stdout="$4" expected_stderr="$5" expected_calls="$6" file_output="${7:-}"
    local case_dir="$sandbox/transfer-$name" status=0
    mkdir -p "$case_dir/tmp"
    SANDBOX="$sandbox" TEST_CASE="$case_dir" TEST_TRANSFER_MODE="$mode" \
        TEST_OPTIONS_CURL="$sandbox/curl-bin/curl" INSTALL_TARGET_DIR="$case_dir/target" \
        TMPDIR="$case_dir/tmp" TEST_NAME="$name" TEST_FILE_OUTPUT="$file_output" \
        PATH="$sandbox/transfer-bin:$PATH" "$BASH" -c '
        unset SSL_CERT_FILE CURL_SSL_BACKEND TEST_CURL_TLS
        source "$SANDBOX/functions.sh"
        export CURL_CA_BUNDLE="$SANDBOX/corp-ca.pem"
        find_windows_default_ca_bundle() { printf "%s" "$SANDBOX/public-ca.crt"; }
        [ "$TEST_NAME" != no-fallback ] || export TEST_CURL_TLS=openssl
        PLATFORM=windows
        configure_curl_options
        export TEST_EXPECTED_CA="$WINDOWS_CA_BUNDLE"
        if [ "$TEST_NAME" = mktemp-failure ]; then mktemp() { return 1; }; fi
        set -- --connect-timeout 2 --max-time 3 --location --write-out "%{http_code}" https://release.test/resource
        [ -z "$TEST_FILE_OUTPUT" ] || set -- "$@" -o "$TEST_CASE/output file"
        installer_curl "$@"
    ' >"$case_dir/stdout" 2>"$case_dir/stderr" || status=$?
    printf '%b' "$expected_stdout" > "$case_dir/expected-stdout"
    printf '%b' "$expected_stderr" > "$case_dir/expected-stderr"
    printf '%b\n' "$expected_calls" > "$case_dir/expected-calls"
    if [ "$status" != "$expected_status" ] ||
       ! cmp -s "$case_dir/stdout" "$case_dir/expected-stdout" ||
       ! cmp -s "$case_dir/stderr" "$case_dir/expected-stderr" ||
       ! cmp -s "$case_dir/calls" "$case_dir/expected-calls"; then
        echo "FAIL: $name transfer (status $status, expected $expected_status)" >&2
        failures=$((failures + 1))
    elif [ -n "$(ls -A "$case_dir/tmp")" ]; then
        echo "FAIL: $name transfer leaked temporary files" >&2
        failures=$((failures + 1))
    else
        echo "PASS: $name transfer preserves output/status/attempt count and cleans up"
    fi
    if [ -f "$case_dir/fallback.args" ]; then
        tail -n +3 "$case_dir/fallback.args" > "$case_dir/fallback-original.args"
        if ! cmp -s "$case_dir/native.args" "$case_dir/fallback-original.args"; then
            echo "FAIL: $name retry changed the original curl arguments" >&2
            failures=$((failures + 1))
        fi
    fi
    if [ -n "$file_output" ]; then
        printf 'fallback-body\n\n' > "$case_dir/expected-body"
        if ! cmp -s "$case_dir/output file" "$case_dir/expected-body"; then
            echo "FAIL: retry did not overwrite the requested output file" >&2
            failures=$((failures + 1))
        fi
    fi
}

transfer_check native-store native 0 'native-body\n\n200' 'native-stderr\n' native
transfer_check corporate corporate 0 'fallback-body\n\n200' 'fallback-stderr\n' 'native\nfallback'
transfer_check file-output corporate 0 200 'fallback-stderr\n' 'native\nfallback' file
transfer_check untrusted reject 60 000 'fallback-certificate-error\n' 'native\nfallback'
transfer_check non-certificate network 7 'native-output\n\n000' 'native-network-error\n' native
transfer_check bad-ca-file bad-ca-file 77 000 'native-ca-file-error\n' native
transfer_check mktemp-failure native 0 'native-body\n\n200' 'native-stderr\n' native
transfer_check no-fallback corporate 60 000 'native-certificate-error\n' native
transfer_check term term 143 '' '' native
transfer_check int int 130 '' '' native

reset=$(
    export INSTALL_TARGET_DIR="$sandbox/target" CURL_CA_BUNDLE="$sandbox/corp-ca.pem"
    source "$sandbox/functions.sh"
    find_windows_default_ca_bundle() { return 1; }
    PLATFORM=windows
    configure_curl_options
    [ -n "$WINDOWS_CA_BUNDLE" ] || exit 1
    export TEST_CURL_TLS=openssl
    configure_curl_options
    [ -z "$WINDOWS_CA_BUNDLE" ] || exit 1
    export TEST_CURL_TLS=schannel
    configure_curl_options
    unset CURL_CA_BUNDLE
    configure_curl_options
    printf '%s' "$WINDOWS_CA_BUNDLE"
) || reset=unexpected-failure
if [ -z "$reset" ]; then
    echo 'PASS: reconfiguration clears stale CA fallback after backend/env changes'
else
    echo 'FAIL: reconfiguration retained a stale CA fallback' >&2
    failures=$((failures + 1))
fi

[ "$failures" -eq 0 ]
