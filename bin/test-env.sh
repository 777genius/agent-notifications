#!/bin/bash
# Shared shell fixture environment. Source this file; it does not run a test.
test_env_setup() {
    local base="$1"
    export HOME="$base/home" USERPROFILE="$base/home"
    export APPDATA="$base/appdata" LOCALAPPDATA="$base/localappdata"
    export XDG_CONFIG_HOME="$base/config" XDG_CACHE_HOME="$base/cache"
    export XDG_DATA_HOME="$base/data" XDG_STATE_HOME="$base/state" XDG_RUNTIME_DIR="$base/run"
    export XDG_CONFIG_DIRS="$base/config-dirs" XDG_DATA_DIRS="$base/data-dirs"
    export CODEX_HOME="$base/codex" CLAUDE_HOME="$base/claude" CLAUDE_CONFIG_DIR="$base/claude"
    export TMPDIR="$base/tmp" TMP="$base/tmp" TEMP="$base/tmp"
    unset AGENT_NOTIFICATIONS_CONFIG
    mkdir -p "$HOME" "$APPDATA" "$LOCALAPPDATA" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" \
        "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_RUNTIME_DIR" "$XDG_CONFIG_DIRS" \
        "$XDG_DATA_DIRS" "$CODEX_HOME" "$CLAUDE_HOME" "$TMPDIR"
    chmod 700 "$XDG_RUNTIME_DIR"
}

# Re-enter through env -i before executing fixture code. The readiness flag is
# shell-local so nested suites also get a fresh environment and sandbox.
test_env_enter() {
    [ "${_TEST_ENV_READY:-}" = 1 ] && return 0
    local script="$1"; shift
    local base helper result name gomodcache
    local -a platform_env=() toolchain_env=()
    helper="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
    base=$(mktemp -d /tmp/agent-notifications-fixture-XXXXXX) || exit 1
    # Native Windows Python/curl need these to find OS DLLs and executables.
    for name in SystemRoot SYSTEMROOT WINDIR COMSPEC PATHEXT; do
        if [ -n "${!name:-}" ]; then platform_env+=("$name=${!name}"); fi
    done
    # Some fixtures build the real Go binary after isolation. Let those suites
    # explicitly hand off only the already-prepared module cache; keep all
    # other Go and user configuration inside the disposable HOME.
    if [ "${TEST_ENV_HANDOFF_GOMODCACHE:-}" = 1 ]; then
        gomodcache="${GOMODCACHE:-}"
        if [ -z "$gomodcache" ] && command -v go >/dev/null 2>&1; then
            gomodcache=$(GOTOOLCHAIN=local go env GOMODCACHE) || exit 1
        fi
        if [ -n "$gomodcache" ]; then
            toolchain_env+=("GOMODCACHE=$gomodcache")
        fi
    fi
    trap 'rm -rf "$base"' EXIT
    env -i PATH="$PATH" "${platform_env[@]}" "${toolchain_env[@]}" bash --noprofile --norc -c '
        _TEST_ENV_READY=1
        source "$1"
        test_env_setup "$2"
        shift 2
        source "$0"
    ' "$script" "$helper" "$base" "$@"
    result=$?
    exit "$result"
}
