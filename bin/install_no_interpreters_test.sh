#!/bin/bash
set -eo pipefail
root=$(cd "$(dirname "$0")" && pwd)
source "$root/test-env.sh"
test_env_enter "$0" "$@"
native_helper="${1:-}"
box=$(mktemp -d)
trap 'rm -rf "$box"' EXIT
test_env_setup "$box"
mkdir -p "$box/path" "$box/assets" "$box/target"
# A minimal PATH deliberately omits Python, Node, Go and jq. shasum uses the
# system Perl runtime, which is outside the omitted development dependencies.
for tool in awk cat chmod cp grep mktemp uname tr dirname mkdir rm sleep ps shasum sha256sum; do
    location=$(command -v "$tool" || true)
    [ -z "$location" ] || ln -s "$location" "$box/path/$tool"
done
sed '/^main "\$@"$/d' "$root/bootstrap.sh" > "$box/bootstrap.sh"
sed '/^main "\$@"$/d' "$root/install.sh" > "$box/install.sh"
# The fixture helper is written before restricting PATH. It verifies argv
# round-trips shell metacharacters without evaluation or loss of newlines.
printf '%s\n' '#!/bin/bash' \
    'if [ "${HANG:-}" = 1 ]; then sleep 60 & printf "%s" "$!" > "$CHILD_PID_FILE"; if [ -n "${PARENT_PID_FILE:-}" ]; then printf "%s" "$PPID" > "$PARENT_PID_FILE"; fi; wait; fi' \
    'if [ "$3" = preflight ] && [ "${LEAK:-}" = 1 ]; then printf "SECRET-CANARY %s\nConfigInvalid\n" "$AGENT_NOTIFICATIONS_CONFIG" >&2; exit 1; fi' \
    'case "$3" in capabilities) printf "installer-v1\n" ;; preflight) [ "$4" = "$EXPECTED_TARGET" ] && [ "${REJECT:-}" != 1 ] ;; *) exit 1 ;; esac' > "$box/helper"
chmod +x "$box/helper"
printf 'release bytes\n' > "$box/assets/binary"
if command -v sha256sum >/dev/null; then
    digest=$(sha256sum < "$box/assets/binary")
else
    digest=$(shasum -a 256 < "$box/assets/binary")
fi
printf '%s  binary\n' "${digest%% *}" > "$box/assets/checksums.txt"
if [ -n "$native_helper" ]; then
    native_version=$($native_helper --version)
    native_tag=${native_version#claude-notifications }
    [ "$native_tag" != "$native_version" ]
    case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
        darwin) native_os=darwin ;; linux) native_os=linux ;; mingw*|msys*|cygwin*) native_os=windows ;; *) exit 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) native_arch=amd64 ;; arm64|aarch64) native_arch=arm64 ;; *) exit 1 ;; esac
    native_name="claude-notifications-${native_os}-${native_arch}"
    [ "$native_os" != windows ] || native_name="$native_name.exe"
    mkdir -p "$box/native"
    cp "$native_helper" "$box/native/$native_name"
    if command -v sha256sum >/dev/null; then
        native_digest=$(sha256sum < "$box/native/$native_name")
    else
        native_digest=$(shasum -a 256 < "$box/native/$native_name")
    fi
    printf '%s  %s\n' "${native_digest%% *}" "$native_name" > "$box/native/checksums.txt"
fi

list_process_pairs() {
    local pairs
    if pairs=$(ps -eo pid=,ppid= 2>/dev/null); then
        printf '%s\n' "$pairs"
        return
    fi
    # MSYS ps does not implement GNU -o. Its default table prefixes data rows
    # with an unlabelled status byte, while -ef has aligned named columns.
    ps -ef | awk '
        NR == 1 {
            for (i = 1; i <= NF; i++) {
                if ($i == "PID") pid_col = i
                if ($i == "PPID") ppid_col = i
            }
            if (!pid_col || !ppid_col) exit 1
            next
        }
        { print $pid_col, $ppid_col }
    '
}

(
    export PATH="$box/path"
    for tool in python3 node go jq; do ! command -v "$tool"; done
    source "$box/bootstrap.sh"
    verify_bootstrap_checksum "$box/assets" binary
    printf '%s  binary\n' "${digest%% *}" >> "$box/assets/checksums.txt"
    if verify_bootstrap_checksum "$box/assets" binary; then exit 1; fi
    export INSTALL_TARGET_DIR="$box/target"
    source "$box/install.sh"
    export AGENT_NOTIFICATIONS_CONFIG="$box/config.json"
    export EXPECTED_TARGET="$box/quote\" backslash\\"$'\n'"literal\$(false)"
    INSTALL_CONFIG_HELPER="$box/helper"
    PLATFORM=linux
    install_config_preflight "$EXPECTED_TARGET"
    export REJECT=1
    if install_config_preflight "$EXPECTED_TARGET"; then exit 1; fi
    unset REJECT
    export LEAK=1
    diagnostic=$(install_config_preflight "$EXPECTED_TARGET" 2>&1 || true)
    case "$diagnostic" in *SECRET-CANARY*|*"$AGENT_NOTIFICATIONS_CONFIG"*) exit 1 ;; esac
    case "$diagnostic" in *ConfigInvalid*) ;; *) exit 1 ;; esac
    unset LEAK
    export HANG=1 CHILD_PID_FILE="$box/helper-child.pid" INSTALL_HELPER_TIMEOUT_SECONDS=1
    if install_config_preflight "$EXPECTED_TARGET"; then exit 1; else [ "$?" = 2 ]; fi
    child=$(cat "$CHILD_PID_FILE")
    i=0
    while kill -0 "$child" 2>/dev/null && [ "$i" -lt 40 ]; do sleep .05; i=$((i + 1)); done
    ! kill -0 "$child" 2>/dev/null
    unset HANG CHILD_PID_FILE INSTALL_HELPER_TIMEOUT_SECONDS
    # Interrupt the actual helper runner while it waits. Monitor mode keeps
    # asynchronous fixtures from inheriting SIGINT as ignored.
    trap 'if [ -n "${runner:-}" ]; then
        if [ -s "${PARENT_PID_FILE:-}" ]; then kill -TERM "$(cat "$PARENT_PID_FILE")" 2>/dev/null || true; fi
        kill -TERM "$runner" 2>/dev/null || true
        wait "$runner" 2>/dev/null || true
    fi; cleanup_install_config' EXIT
    for signal in INT TERM; do
        export HANG=1 CHILD_PID_FILE="$box/$signal-child.pid"
        export PARENT_PID_FILE="$box/$signal-parent.pid" INSTALL_HELPER_TIMEOUT_SECONDS=60
        before_traps=$(trap -p)
        set -m
        (set -u; run_install_helper "$box/helper" config installer capabilities) >"$box/$signal.out" 2>"$box/$signal.err" &
        runner=$!
        set +m
        i=0
        while [ ! -s "$PARENT_PID_FILE" ] && [ "$i" -lt 100 ]; do sleep .02; i=$((i + 1)); done
        [ -s "$PARENT_PID_FILE" ]
        helper_parent=$(cat "$PARENT_PID_FILE")
        child=$(cat "$CHILD_PID_FILE")
        # Wait for the watchdog's sleep to exist before delivering the signal.
        i=0
        timers=""
        while [ -z "$timers" ] && [ "$i" -lt 100 ]; do
            processes=$(list_process_pairs)
            siblings=$(printf '%s\n' "$processes" | awk -v p="$helper_parent" '$2 == p {printf "%s ", $1}')
            timers=$(printf '%s\n' "$processes" | awk -v parents="$siblings" -v child="$child" 'BEGIN {n=split(parents,a," "); for(i=1;i<=n;i++) p[a[i]]=1} p[$2] && $1 != child {print $1}')
            [ -n "$timers" ] || sleep .02
            i=$((i + 1))
        done
        [ -n "$timers" ]
        kill -"$signal" "$helper_parent"
        i=0
        while kill -0 "$runner" 2>/dev/null && [ "$i" -lt 100 ]; do sleep .02; i=$((i + 1)); done
        if kill -0 "$runner" 2>/dev/null; then
            kill -KILL -- "-$runner" 2>/dev/null || true
            printf 'FAIL: %s cleanup did not finish promptly\n' "$signal" >&2
            exit 1
        fi
        if wait "$runner"; then exit 1; else status=$?; fi
        runner=""
        case "$signal:$status" in INT:130|TERM:143) ;; *) exit 1 ;; esac
        for pid in $siblings $timers "$child"; do
            i=0
            while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 100 ]; do sleep .02; i=$((i + 1)); done
            ! kill -0 "$pid" 2>/dev/null
        done
        ! grep -q 'unbound variable' "$box/$signal.err"
        for capture in "$TMPDIR"/install-helper.*; do [ ! -e "$capture" ]; done
        [ "$(trap -p)" = "$before_traps" ]
    done
    unset HANG CHILD_PID_FILE PARENT_PID_FILE INSTALL_HELPER_TIMEOUT_SECONDS
    INSTALL_CONFIG_HELPER="$box/nonexistent"
    if install_config_preflight "$EXPECTED_TARGET"; then exit 1; else [ "$?" = 2 ]; fi
    if [ -n "$native_helper" ]; then
        fetch_bootstrap_file() {
            case "$2" in
                */checksums.txt) cp "$box/native/checksums.txt" "$2" ;;
                */"$native_name") cp "$box/native/$native_name" "$2" ;;
                */install.sh) cp "$box/install.sh" "$2" ;;
                *) return 1 ;;
            esac
        }
        PRODUCT=codex
        BOOTSTRAP_TAG="$native_tag"
        _CONFIG_STAGE=""
        _CONFIG_HELPER=""
        stage_config_helper
        [ -x "$_CONFIG_HELPER" ]
        INSTALL_CONFIG_HELPER="$_CONFIG_HELPER"
        export AGENT_NOTIFICATIONS_CONFIG="$box/target/config.json"
        if install_config_preflight "$box/target"; then exit 1; fi
        export AGENT_NOTIFICATIONS_CONFIG="$box/separate/config.json"
        install_config_preflight "$box/target"
        config_preflight
        rm -rf -- "$_CONFIG_STAGE"
    fi
)
printf 'PASS: interpreter-free transport, fail-closed helper and checksum validation\n'
