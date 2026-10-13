#!/bin/bash
# Offline, observable wrapper E2E. Base 27ea072 is RED: every failed hook installs.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
TEST_ENV_HANDOFF_GOMODCACHE=1
test_env_enter "$0" "$@"
set -euo pipefail
src=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$src/.." && pwd)
root=$(mktemp -d)
root=$(cd "$root" && pwd -P)
trap 'rm -rf "$root"' EXIT
test_env_setup "$root"
export E2E_CONTROL_ROOT="$XDG_CONFIG_HOME/agent-notifications"
mkdir -p "$E2E_CONTROL_ROOT"
chmod 700 "$E2E_CONTROL_ROOT"
for name in HOME XDG_CACHE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_RUNTIME_DIR TMPDIR CLAUDE_CONFIG_DIR E2E_CONTROL_ROOT; do
    physical=$(cd "${!name}" && pwd -P)
    case "$physical" in "$root"/*) ;; *) echo "unsafe fixture $name" >&2; exit 1;; esac
done
export http_proxy=http://127.0.0.1:1 https_proxy=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 HTTPS_PROXY=http://127.0.0.1:1 ALL_PROXY=http://127.0.0.1:1
mkdir "$root/tools"
for tool in curl wget; do
    printf '#!/bin/sh\necho outbound-attempt >> "$HOME/outbound"\nexit 97\n' > "$root/tools/$tool"
    chmod +x "$root/tools/$tool"
done
export PATH="$root/tools:$PATH"
cd "$repo"
go build -trimpath -ldflags '-s -w -X github.com/777genius/agent-notifications/internal/config.ConsumerVersion=1.41.0' -o "$root/old-sender" ./cmd/claude-notifications
go build -trimpath -ldflags '-s -w -X github.com/777genius/agent-notifications/internal/config.ConsumerVersion=1.42.0' -o "$root/new-sender" ./cmd/claude-notifications
# Both owned release fixtures must satisfy the external 32 MiB staging contract.
for binary in "$root/old-sender" "$root/new-sender"; do
    [ -f "$binary" ] && [ ! -L "$binary" ] || { echo 'non-regular release fixture' >&2; exit 1; }
    bytes=$(wc -c < "$binary" | tr -d '[:space:]')
    limit=$((32*1024*1024))
    printf 'owned release fixture %s: bytes=%s limit=%s\n' "$binary" "$bytes" "$limit"
    [ "$bytes" -gt 0 ] && [ "$bytes" -le "$limit" ] || { echo 'release fixture exceeds managed-release size contract' >&2; exit 1; }
done

# Assert the fixture independently: main.version is initialized from the
# exported config variable, so setting main.version alone is overwritten.
"$root/old-sender" version | grep -q '1.41.0'
"$root/new-sender" version | grep -q '1.42.0'

fixture() {
    export CASE_ROOT="$root/$1" MODE=fail
    export E2E_CONTROL_ROOT="$CASE_ROOT/control"
    mkdir -p "$CASE_ROOT/plugin/bin" "$CASE_ROOT/plugin/.claude-plugin" "$CASE_ROOT/cache" "$CASE_ROOT/release" "$E2E_CONTROL_ROOT"
    chmod 700 "$E2E_CONTROL_ROOT"
    case "$(cd "$E2E_CONTROL_ROOT" && pwd -P)" in "$root"/*) ;; *) echo 'unsafe case control root' >&2; exit 1;; esac
    cp "$src/hook-wrapper.sh" "$CASE_ROOT/plugin/bin/"
    cp "$src/codex-hook-wrapper.sh" "$CASE_ROOT/plugin/bin/"
    printf '{"version":"1.42.0"}\n' > "$CASE_ROOT/plugin/.claude-plugin/plugin.json"
    cp "$root/old-sender" "$CASE_ROOT/plugin/bin/sender"
    cp "$root/new-sender" "$CASE_ROOT/release/claude-notifications-linux-amd64"
    cat > "$CASE_ROOT/plugin/bin/claude-notifications" <<'SENDER'
#!/bin/sh
if [ "$1" != version ]; then echo dispatched >> "$CASE_ROOT/dispatches"; fi
exec "$(dirname "$0")/sender" "$@"
SENDER
    cat > "$CASE_ROOT/plugin/bin/install.sh" <<'INSTALL'
#!/bin/bash
# agent-notifications-managed-writer-protocol-v1
set -eu
printf '%s\n' "$$" >> "$CASE_ROOT/attempts"
echo fixture-release-request >> "$CASE_ROOT/downloads"
# Capture actual cache ownership at installer entry, including unclaimed runs
# racing expiration. No wrapper function or clock is overridden.
{
    printf 'installer=%s parent=%s\n' "$$" "$PPID"
    while IFS= read -r active; do
        owner=$(cat "$active/owner" 2>/dev/null || true)
        printf 'active=%s owner=%s pid=' "$active" "$owner"
        head -n 1 "$active/installer" 2>/dev/null || true
    done < <(find "$CASE_ROOT/cache" -type d -name active 2>/dev/null)
} > "$CASE_ROOT/install-start.$$"
if [ "${HOLD:-0}" = 1 ]; then
    : > "$CASE_ROOT/entered"
    n=0
    while [ ! -f "$CASE_ROOT/release-barrier" ]; do
        n=$((n+1)); [ "$n" -lt 1000 ] || exit 98
        sleep 0.02
    done
fi
case "$MODE" in
    success)
        "$CASE_ROOT/release/claude-notifications-linux-amd64" internal-install-runtime --control-root "$E2E_CONTROL_ROOT" --stage "$CASE_ROOT/release" --entry claude-notifications-linux-amd64 --target "$INSTALL_TARGET_DIR" --consumer "fixture-$(basename "$CASE_ROOT")"
        ;;
    manual) cp "$CASE_ROOT/release/claude-notifications-linux-amd64" "$INSTALL_TARGET_DIR/sender" ;;
    offline) cp "$CASE_ROOT/release/claude-notifications-linux-amd64" "$INSTALL_TARGET_DIR/sender"; exit 7 ;;
    zero) exit 0 ;;
    *) echo 'fixture download failed' >&2; exit 7 ;;
esac
INSTALL
    chmod +x "$CASE_ROOT/plugin/bin/claude-notifications" "$CASE_ROOT/plugin/bin/install.sh"
    # Avoid the unrelated recent-publication wait without replacing date/sleep.
    touch -t 200001010000 "$CASE_ROOT/plugin/bin"
}
lines() { if [ -f "$1" ]; then wc -l < "$1" | tr -d ' '; else echo 0; fi; }
hook() {
    if [ "${trace_concurrency:-0}" = 1 ]; then
        # Shell execution tracing observes the real wrapper without replacing
        # its functions, filesystem operations, clocks or ownership protocol.
        PS4='+$$: ' XDG_CACHE_HOME="$CASE_ROOT/cache" sh -x "$CASE_ROOT/plugin/bin/hook-wrapper.sh" help > "$CASE_ROOT/out-$1" 2> "$CASE_ROOT/err-$1"
    else
        XDG_CACHE_HOME="$CASE_ROOT/cache" sh "$CASE_ROOT/plugin/bin/hook-wrapper.sh" help > "$CASE_ROOT/out-$1" 2> "$CASE_ROOT/err-$1"
    fi
}
claim() {
    local product=${1:-claude} key
    key=$(printf %s "$CASE_ROOT/plugin/bin" | cksum | cut -d' ' -f1)
    local stamp="$CASE_ROOT/cache/claude-notifications-go"
    [ "$product" != codex ] || stamp="$stamp/codex"
    printf '%s/install-backoff-1.42.0-%s' "$stamp" "$key"
}
age_claims() {
    # Only physical active directories carry the cooldown timestamp.
    local path
    case "$CASE_ROOT" in "$root"/*) ;; *) return 1;; esac
    while IFS= read -r path; do
        touch -t "$1" "$path"
    done < <(find "$CASE_ROOT/cache" -type d -name active -path '*/install-backoff-*/*')
}
no_active_claim() {
    local path; path=$(claim)
    [ ! -L "$path/active" ] && [ ! -e "$path/active" ]
}
parallel_hooks() {
    local i pids='' n=0
    parallel_round=$((${parallel_round:-0}+1))
    rm -f "$CASE_ROOT/entered" "$CASE_ROOT/release-barrier" "$CASE_ROOT"/finished-*
    export HOLD=1
    for i in $(seq 1 20); do (hook "parallel-$parallel_round-$i"; : > "$CASE_ROOT/finished-$i") & pids="$pids $!"; done
    while [ ! -f "$CASE_ROOT/entered" ]; do
        n=$((n+1)); [ "$n" -lt 1000 ] || { echo 'installer barrier never reached'; return 1; }; sleep 0.02
    done
    # Observe loser completion before releasing the owner. On broken base all
    # installers hold the barrier; bound observation and then count the runs.
    n=0
    while [ "$(find "$CASE_ROOT" -name 'finished-*' | wc -l | tr -d ' ')" -lt 19 ]; do
        n=$((n+1)); [ "$n" -lt 150 ] || break; sleep 0.02
    done
    : > "$CASE_ROOT/release-barrier"
    for i in $pids; do wait "$i"; done
    unset HOLD
}
sequential_failure() {
    fixture sequential
    for i in $(seq 1 20); do hook "$i"; done
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ] || { echo "20 hooks made $(lines "$CASE_ROOT/attempts") installs; expected 1"; return 1; }
    [ "$(lines "$CASE_ROOT/downloads")" = 1 ]
    [ "$(lines "$CASE_ROOT/dispatches")" = 20 ]
    grep -q 'Installation of v1.42.0 failed' "$CASE_ROOT/err-1"
    for i in $(seq 2 20); do [ ! -s "$CASE_ROOT/err-$i" ]; done
    grep -q 'Usage' "$CASE_ROOT/out-20"
}
concurrent_expiration() {
    fixture concurrent
    local failures=0 before total
    parallel_hooks
    echo "fresh concurrency: $(lines "$CASE_ROOT/attempts") installs (expected 1)"
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ] || failures=1
    [ "$(find "$CASE_ROOT/cache" -type d -name active | wc -l | tr -d ' ')" -ge 1 ] || failures=1
    # Exercise every expiry even if the fresh window failed, preserving both
    # the cumulative bound and the independent one-run bound for each window.
    for round in 1 2 3; do
        before=$(lines "$CASE_ROOT/attempts")
        age_claims 200001010000
        parallel_hooks
        total=$(lines "$CASE_ROOT/attempts")
        echo "expired concurrency round $round: $total total installs (expected $((round+1))); $((total-before)) new installs (expected 1)"
        if [ "$total" != "$((round+1))" ] || [ "$((total-before))" != 1 ]; then failures=1; fi
    done
    [ "$(lines "$CASE_ROOT/dispatches")" = 80 ] || failures=1
    [ "$(lines "$CASE_ROOT/downloads")" = 4 ] || failures=1
    if [ "$failures" != 0 ]; then
        echo 'concurrent claim bounds violated; actual ownership at installer entry:'
        cat "$CASE_ROOT"/install-start.*
        if [ "${trace_concurrency:-0}" = 1 ]; then
            local trace
            for trace in "$CASE_ROOT"/err-parallel-*; do
                printf '\nWrapper execution trace: %s\n' "$trace"
                cat "$trace"
            done
        fi
    fi
    [ "$failures" = 0 ]
}

active_claim() {
    fixture active
    export HOLD=1
    hook owner & local owner=$!
    local n=0
    while [ ! -e "$CASE_ROOT/entered" ]; do n=$((n+1)); [ "$n" -lt 1000 ] || return 1; sleep 0.02; done
    # An active owner must survive wall-clock skew/expiration of its pathname.
    age_claims 200001010000
    local attempt_before; attempt_before=$(cat "$(claim)/active/owner" 2>/dev/null || true)
    for i in $(seq 1 20); do hook "active-$i" & done
    sleep 1
    local attempt_after; attempt_after=$(cat "$(claim)/active/owner" 2>/dev/null || true)
    : > "$CASE_ROOT/release-barrier"
    wait "$owner"
    wait
    unset HOLD
    [ -n "$attempt_before" ] && [ "$attempt_after" = "$attempt_before" ]
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ]
    hook retry
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ]
}
isolation() {
    fixture isolation
    hook first; hook suppressed
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ] || return 1
    echo '{"version":"1.42.1"}' > "$CASE_ROOT/plugin/.claude-plugin/plugin.json"
    hook version
    [ "$(lines "$CASE_ROOT/attempts")" = 2 ] || return 1
    cp -R "$CASE_ROOT/plugin" "$CASE_ROOT/peer"
    XDG_CACHE_HOME="$CASE_ROOT/cache" sh "$CASE_ROOT/peer/bin/hook-wrapper.sh" help > "$CASE_ROOT/peer-out" 2> "$CASE_ROOT/peer-err"
    [ "$(lines "$CASE_ROOT/attempts")" = 3 ] || return 1
    for i in $(seq 1 20); do
        XDG_CACHE_HOME="$CASE_ROOT/cache" sh "$CASE_ROOT/plugin/bin/codex-hook-wrapper.sh" help > "$CASE_ROOT/codex-out" 2> "$CASE_ROOT/codex-err"
        [ ! -s "$CASE_ROOT/codex-out" ] && [ ! -s "$CASE_ROOT/codex-err" ] || return 1
    done
    [ "$(lines "$CASE_ROOT/attempts")" = 4 ]
    [ "$(lines "$CASE_ROOT/dispatches")" = 4 ]
}
bad_claims() {
    for kind in future file empty; do
        fixture "bad-$kind"
        local path; path=$(claim); mkdir -p "$(dirname "$path")"
        case "$kind" in
            future) hook seed; age_claims 209901010000;;
            file) echo corrupt > "$path";;
            empty) mkdir "$path"; touch -t 200001010000 "$path";;
        esac
        hook 1; hook 2
        # An unknown namespace suppresses mutation; a future failed timestamp
        # and an empty namespace still admit one properly claimed retry.
        local expected=1; [ "$kind" != future ] || expected=2; [ "$kind" != file ] || expected=0
        echo "bad claim $kind: $(lines "$CASE_ROOT/attempts") installs (expected $expected)"
        [ "$(lines "$CASE_ROOT/attempts")" = "$expected" ] || return 1
    done
}
cache_unwritable() {
    fixture cache-unwritable
    rmdir "$CASE_ROOT/cache"; echo blocked > "$CASE_ROOT/cache"
    hook 1; hook 2
    [ "$(lines "$CASE_ROOT/attempts")" = 0 ]
    [ "$(lines "$CASE_ROOT/dispatches")" = 2 ]
}
manual_repair() {
    fixture manual
    hook failed
    MODE=manual INSTALL_TARGET_DIR="$CASE_ROOT/plugin/bin" "$CASE_ROOT/plugin/bin/install.sh"
    hook repaired
    no_active_claim || return 1
    cp "$root/old-sender" "$CASE_ROOT/plugin/bin/sender"
    rm -f "$CASE_ROOT/cache/claude-notifications-go/verified-version"
    hook broken-again
    [ "$(lines "$CASE_ROOT/attempts")" = 3 ]
    grep -q 'Installation of v1.42.0 failed' "$CASE_ROOT/err-broken-again"
}
success() {
    fixture success
    hook failure
    age_claims 200001010000
    export MODE=success
    hook repaired; hook verified
    [ "$(lines "$CASE_ROOT/attempts")" = 2 ] || return 1
    no_active_claim || return 1
    "$CASE_ROOT/plugin/bin/claude-notifications" version | grep -q 1.42.0
    [ -f "$E2E_CONTROL_ROOT/ownership.json" ]
}
contention() {
    fixture contention
    local owner="$CASE_ROOT/plugin/bin/.install.lock/.owner.e2e"
    mkdir -p "$owner"; printf '%s\n' "$$" > "$owner/pid"; : > "$owner/heartbeat"
    hook contended
    no_active_claim && [ ! -s "$CASE_ROOT/err-contended" ] || return 1
    [ -f "$owner/pid" ] || return 1
    rm "$owner/pid" "$owner/heartbeat"; rmdir "$owner" "$(dirname "$owner")"
    hook failed
    [ "$(lines "$CASE_ROOT/attempts")" -ge 1 ]
    grep -q 'Installation of v1.42.0 failed' "$CASE_ROOT/err-failed"
}
offline_publication() {
    fixture offline
    export MODE=offline
    hook published
    no_active_claim
    [ ! -s "$CASE_ROOT/err-published" ]
    hook verified
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ]
    [ "$(lines "$CASE_ROOT/dispatches")" = 2 ]
}
zero_exit() {
    fixture zero
    export MODE=zero
    for i in $(seq 1 20); do hook "$i"; done
    [ "$(lines "$CASE_ROOT/attempts")" = 1 ]
}
failed=0
trace_concurrency=0
if [ "${1:-}" = --trace-concurrency ]; then
    trace_concurrency=1
    shift
    # Tracing writes stderr; run only the concurrency contracts, which observe
    # installer counts and retained dispatch rather than silence assertions.
    [ "$#" = 0 ] || { echo '--trace-concurrency takes no scenario arguments' >&2; exit 2; }
    set -- concurrent_expiration
fi
if [ "$#" = 0 ]; then
    set -- sequential_failure concurrent_expiration active_claim isolation bad_claims cache_unwritable manual_repair success contention offline_publication zero_exit
fi
for scenario in "$@"; do
    case "$scenario" in
        sequential_failure|concurrent_expiration|active_claim|isolation|bad_claims|cache_unwritable|manual_repair|success|contention|offline_publication|zero_exit) ;;
        *) echo "unknown E2E scenario: $scenario" >&2; exit 2;;
    esac
    # Background execution keeps errexit active; only wait is conditional.
    (set -e; "$scenario") &
    case_pid=$!
    if wait "$case_pid"; then echo "PASS: $scenario"; else echo "FAIL: $scenario"; failed=$((failed+1)); fi
done
[ ! -e "$HOME/outbound" ] || { echo 'FAIL: outbound command attempted'; failed=$((failed+1)); }
[ "$failed" = 0 ]
