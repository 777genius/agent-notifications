#!/usr/bin/env bash
# Real-Claude smoke/manual E2E harness for Agent Notifications.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MARKETPLACE_NAME="claude-notifications-go"
PLUGIN_NAME="claude-notifications-go"
PLUGIN_KEY="${PLUGIN_NAME}@${MARKETPLACE_NAME}"

REAL_CLAUDE_HOME="${REAL_CLAUDE_HOME:-${CLAUDE_CONFIG_DIR:-$HOME/.claude}}"
INSTALLED_JSON="${REAL_CLAUDE_HOME}/plugins/installed_plugins.json"

DEFAULT_TIMEOUT_SECONDS="${E2E_CLAUDE_TIMEOUT_SECONDS:-90}"

OS_NAME="$(uname -s 2>/dev/null || echo unknown)"
PLATFORM="unknown"
case "$OS_NAME" in
    Darwin) PLATFORM="macos" ;;
    Linux)  PLATFORM="linux" ;;
    MINGW*|MSYS*|CYGWIN*) PLATFORM="windows" ;;
esac

print_help() {
    cat <<EOF
Usage: scripts/e2e-real-claude.sh <command>

Commands:
  smoke-installed       Run a real Claude smoke test against the installed plugin
  smoke-plugin-dir      Run a real Claude smoke test via --plugin-dir using this repo
  manual-click-installed  Trigger a real notification with the installed plugin, then wait for you to click it
  manual-click-plugin-dir Trigger a real notification via --plugin-dir, then wait for you to click it
  status                Show the currently installed plugin path/version
  help                  Show this help

Environment:
  REAL_CLAUDE_HOME            Claude config dir to target (default: ~/.claude)
  E2E_CLAUDE_TIMEOUT_SECONDS  Timeout for the Claude subprocess (default: ${DEFAULT_TIMEOUT_SECONDS})

What this verifies:
  - Real claude CLI execution
  - Plugin loading (installed or --plugin-dir)
  - Hook execution by checking notification-debug.log delta
  - Basic Stop-hook smoke path

What this does NOT fully automate:
  - Native notification click on macOS/Linux desktop shells
  - Exact click-to-focus verification after clicking the notification

Use `status` to see which modes are supported in the current environment.
EOF
}

require_claude() {
    if ! command -v claude >/dev/null 2>&1; then
        echo "Error: claude CLI not found in PATH" >&2
        exit 1
    fi
}

is_ci_environment() {
    [ -n "${CI:-}" ]
}

has_linux_desktop_session() {
    [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ]
}

supports_smoke_modes() {
    case "$PLATFORM" in
        macos|linux) return 0 ;;
        *) return 1 ;;
    esac
}

supports_manual_click_modes() {
    if is_ci_environment; then
        return 1
    fi

    case "$PLATFORM" in
        macos)
            return 0
            ;;
        linux)
            has_linux_desktop_session
            return $?
            ;;
        *)
            return 1
            ;;
    esac
}

mode_support_reason() {
    local kind="$1"

    case "$kind" in
        smoke)
            case "$PLATFORM" in
                macos)
                    echo "supported on macOS"
                    ;;
                linux)
                    echo "supported on Linux"
                    ;;
                windows)
                    echo "unsupported: this harness is a bash/Unix workflow and is not maintained for Windows"
                    ;;
                *)
                    echo "unsupported: unrecognized platform '${OS_NAME}'"
                    ;;
            esac
            ;;
        manual)
            if is_ci_environment; then
                echo "unsupported: manual click validation is disabled in CI/headless environments"
                return 0
            fi

            case "$PLATFORM" in
                macos)
                    echo "supported on macOS local desktop sessions"
                    ;;
                linux)
                    if has_linux_desktop_session; then
                        echo "supported on Linux desktop sessions"
                    else
                        echo "unsupported: Linux manual click validation requires DISPLAY or WAYLAND_DISPLAY"
                    fi
                    ;;
                windows)
                    echo "unsupported: this harness is not maintained for Windows desktop sessions"
                    ;;
                *)
                    echo "unsupported: unrecognized platform '${OS_NAME}'"
                    ;;
            esac
            ;;
        *)
            echo "unknown"
            ;;
    esac
}

print_supported_modes() {
    echo "Detected platform: ${PLATFORM} (${OS_NAME})"
    echo "CI environment:    $( [ -n "${CI:-}" ] && echo yes || echo no )"
    if [ "$PLATFORM" = "linux" ]; then
        echo "DISPLAY:           ${DISPLAY:-missing}"
        echo "WAYLAND_DISPLAY:   ${WAYLAND_DISPLAY:-missing}"
    fi
    echo "Smoke modes:       $(mode_support_reason smoke)"
    echo "Manual-click:      $(mode_support_reason manual)"
}

require_mode_supported() {
    local kind="$1"

    case "$kind" in
        smoke)
            if supports_smoke_modes; then
                return 0
            fi
            ;;
        manual)
            if supports_manual_click_modes; then
                return 0
            fi
            ;;
        *)
            echo "Internal error: unknown mode kind '${kind}'" >&2
            exit 1
            ;;
    esac

    echo "Error: requested mode is not supported in the current environment." >&2
    echo "Reason: $(mode_support_reason "$kind")" >&2
    echo "" >&2
    print_supported_modes >&2
    exit 1
}

json_query() {
    local file="$1"
    local jq_expr="$2"

    [ -f "$file" ] || return 0

    if command -v jq >/dev/null 2>&1; then
        jq -r "${jq_expr} // empty" "$file" 2>/dev/null || true
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$file" "$jq_expr" <<'PYEOF' 2>/dev/null || true
import json, sys
expr = sys.argv[2].strip()
if expr.startswith('.'):
    expr = expr[1:]
parts = []
buf = ''
in_brackets = False
for ch in expr:
    if ch == '[':
        in_brackets = True
        if buf:
            parts.append(buf)
            buf = ''
        continue
    if ch == ']':
        in_brackets = False
        if buf:
            parts.append(buf.strip('"'))
            buf = ''
        continue
    if ch == '.' and not in_brackets:
        if buf:
            parts.append(buf)
            buf = ''
        continue
    buf += ch
if buf:
    parts.append(buf.strip('"'))

try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    cur = data
    for part in parts:
        if isinstance(cur, dict):
            cur = cur.get(part)
        else:
            cur = None
            break
    if isinstance(cur, (str, int, float, bool)):
        print(cur)
except Exception:
    pass
PYEOF
    fi
}

get_installed_plugin_root() {
    json_query "$INSTALLED_JSON" ".plugins[\"${PLUGIN_KEY}\"][0].installPath"
}

get_installed_plugin_version() {
    json_query "$INSTALLED_JSON" ".plugins[\"${PLUGIN_KEY}\"][0].version"
}

get_repo_version() {
    json_query "${REPO_ROOT}/.claude-plugin/plugin.json" ".version"
}

uuidgen_fallback() {
    if command -v python3 >/dev/null 2>&1; then
        python3 - <<'PYEOF'
import uuid
print(uuid.uuid4())
PYEOF
        return 0
    fi

    date +%s
}

get_file_size() {
    local path="$1"
    if [ -f "$path" ]; then
        wc -c < "$path" | tr -d '[:space:]'
    else
        printf '0'
    fi
}

normalize_output() {
    local path="$1"
    tr -d '\r\n`' < "$path" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

slice_file_from_offset() {
    local path="$1"
    local offset="$2"
    [ -f "$path" ] || return 0

    python3 - "$path" "$offset" <<'PYEOF'
import sys
path = sys.argv[1]
offset = int(sys.argv[2])
with open(path, 'rb') as f:
    f.seek(offset)
    data = f.read()
sys.stdout.write(data.decode('utf-8', errors='replace'))
PYEOF
}

run_claude_with_timeout() {
    local workdir="$1"
    local stdout_file="$2"
    local stderr_file="$3"
    local timeout_seconds="$4"
    shift 4

    python3 - "$workdir" "$stdout_file" "$stderr_file" "$timeout_seconds" "$@" <<'PYEOF'
import os, subprocess, sys
cwd, stdout_path, stderr_path, timeout = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
cmd = sys.argv[5:]

with open(stdout_path, 'wb') as out, open(stderr_path, 'wb') as err:
    try:
        completed = subprocess.run(
            cmd,
            cwd=cwd,
            stdin=subprocess.DEVNULL,
            stdout=out,
            stderr=err,
            timeout=timeout,
            env=os.environ.copy(),
        )
        raise SystemExit(completed.returncode)
    except subprocess.TimeoutExpired:
        raise SystemExit(124)
PYEOF
}

print_status() {
    local installed_root installed_version cached_version
    installed_root="$(get_installed_plugin_root)"
    installed_version="$(get_installed_plugin_version)"
    cached_version=""
    if [ -n "$installed_root" ]; then
        cached_version="$(json_query "${installed_root}/.claude-plugin/plugin.json" ".version")"
    fi

    echo "Claude config dir: ${REAL_CLAUDE_HOME}"
    echo "Repo root:         ${REPO_ROOT}"
    echo "Repo version:      $(get_repo_version)"
    echo "Installed version: ${installed_version:-missing}"
    echo "Install path:      ${installed_root:-missing}"
    echo "Cached plugin ver: ${cached_version:-missing}"
    echo ""
    print_supported_modes
}

run_smoke() {
    local mode="$1"
    local plugin_root prompt expected_version expected_stdout timeout_seconds
    local session_id tmp_dir stdout_file stderr_file debug_file plugin_log before_size debug_before_size
    local delta_file claude_delta_file rc

    require_mode_supported smoke
    require_claude

    case "$mode" in
        installed)
            plugin_root="$(get_installed_plugin_root)"
            if [ -z "$plugin_root" ]; then
                echo "Error: installed plugin root not found in ${INSTALLED_JSON}" >&2
                exit 1
            fi
            ;;
        plugin-dir)
            plugin_root="${REPO_ROOT}"
            ;;
        *)
            echo "Unknown smoke mode: ${mode}" >&2
            exit 1
            ;;
    esac

    expected_version="$(get_repo_version)"
    expected_stdout="${expected_version}"
    timeout_seconds="${DEFAULT_TIMEOUT_SECONDS}"
    session_id="$(uuidgen_fallback)"

    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/claude-e2e-XXXXXX")"
    stdout_file="${tmp_dir}/claude.stdout"
    stderr_file="${tmp_dir}/claude.stderr"
    debug_file="${tmp_dir}/claude.debug.log"
    plugin_log="${plugin_root}/notification-debug.log"
    delta_file="${tmp_dir}/plugin-log.delta"
    claude_delta_file="${tmp_dir}/claude-debug.delta"

    before_size="$(get_file_size "$plugin_log")"
    debug_before_size="$(get_file_size "$debug_file")"

    prompt="Use the Read tool to read .claude-plugin/plugin.json and reply with only the version string."

    local cmd=(claude -p
        --session-id "$session_id"
        --permission-mode bypassPermissions
        --tools=Read
        --debug-file "$debug_file"
    )
    if [ "$mode" = "plugin-dir" ]; then
        cmd+=("--plugin-dir=$REPO_ROOT")
    fi
    cmd+=("$prompt")

    set +e
    run_claude_with_timeout "$REPO_ROOT" "$stdout_file" "$stderr_file" "$timeout_seconds" "${cmd[@]}"
    rc=$?
    set -e

    slice_file_from_offset "$plugin_log" "$before_size" > "$delta_file"
    slice_file_from_offset "$debug_file" "$debug_before_size" > "$claude_delta_file"

    echo "==> Claude smoke test (${mode})"
    echo "Session ID:        ${session_id}"
    echo "Plugin root:       ${plugin_root}"
    echo "Plugin log:        ${plugin_log}"
    echo "Claude debug file: ${debug_file}"
    echo "Exit code:         ${rc}"
    echo "Expected output:   ${expected_stdout}"
    echo "Actual output:     $(tr -d '\r' < "$stdout_file" | tr -d '\n')"

    local output_ok=0 hook_ok=0
    if [ "$(normalize_output "$stdout_file")" = "$expected_stdout" ]; then
        output_ok=1
    fi
    if grep -Eq "=== Hook triggered: Stop ===|=== Hook triggered: PreToolUse ===|Desktop notification sent via|terminal-notifier executed:" "$delta_file"; then
        hook_ok=1
    fi

    echo ""
    echo "==> Plugin log delta"
    if [ -s "$delta_file" ]; then
        cat "$delta_file"
    else
        echo "(no new plugin log lines)"
    fi

    echo ""
    echo "==> Claude stderr"
    if [ -s "$stderr_file" ]; then
        cat "$stderr_file"
    else
        echo "(empty)"
    fi

    if [ "$rc" -eq 0 ] && [ "$output_ok" -eq 1 ] && [ "$hook_ok" -eq 1 ]; then
        echo ""
        echo "Smoke test passed."
        return 0
    fi

    echo ""
    echo "Smoke test failed." >&2
    exit 1
}

run_manual_click() {
    local mode="$1"
    local plugin_root session_id tmp_dir stdout_file stderr_file debug_file plugin_log before_size
    local delta_file rc

    require_mode_supported manual
    require_claude

    case "$mode" in
        installed)
            plugin_root="$(get_installed_plugin_root)"
            if [ -z "$plugin_root" ]; then
                echo "Error: installed plugin root not found in ${INSTALLED_JSON}" >&2
                exit 1
            fi
            ;;
        plugin-dir)
            plugin_root="${REPO_ROOT}"
            ;;
        *)
            echo "Unknown manual-click mode: ${mode}" >&2
            exit 1
            ;;
    esac

    session_id="$(uuidgen_fallback)"
    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/claude-click-XXXXXX")"
    stdout_file="${tmp_dir}/claude.stdout"
    stderr_file="${tmp_dir}/claude.stderr"
    debug_file="${tmp_dir}/claude.debug.log"
    plugin_log="${plugin_root}/notification-debug.log"
    delta_file="${tmp_dir}/plugin-log.delta"
    before_size="$(get_file_size "$plugin_log")"

    local prompt="Use the Read tool to read .claude-plugin/plugin.json and reply with only the version string."

    local cmd=(claude -p
        --session-id "$session_id"
        --permission-mode bypassPermissions
        --tools=Read
        --debug-file "$debug_file"
    )
    if [ "$mode" = "plugin-dir" ]; then
        cmd+=("--plugin-dir=$REPO_ROOT")
    fi
    cmd+=("$prompt")

    set +e
    run_claude_with_timeout "$REPO_ROOT" "$stdout_file" "$stderr_file" "$DEFAULT_TIMEOUT_SECONDS" "${cmd[@]}"
    rc=$?
    set -e

    slice_file_from_offset "$plugin_log" "$before_size" > "$delta_file"

    echo "==> Manual click test (${mode})"
    echo "Session ID:        ${session_id}"
    echo "Plugin root:       ${plugin_root}"
    echo "Plugin log:        ${plugin_log}"
    echo "Claude debug file: ${debug_file}"
    echo "Exit code:         ${rc}"
    echo ""
    echo "A desktop notification should have been sent if desktop notifications are enabled."
    echo "Click the latest notification now and verify that the correct window is focused."
    echo ""
    echo "==> Plugin log delta"
    if [ -s "$delta_file" ]; then
        cat "$delta_file"
    else
        echo "(no new plugin log lines)"
    fi

    if [ -t 0 ]; then
        echo ""
        read -r -p "Press Enter after you finish the manual click check... " _
    fi
}

main() {
    local command="${1:-status}"
    case "$command" in
        smoke-installed)       run_smoke installed ;;
        smoke-plugin-dir)      run_smoke plugin-dir ;;
        manual-click-installed)  run_manual_click installed ;;
        manual-click-plugin-dir) run_manual_click plugin-dir ;;
        status)                print_status ;;
        help|-h|--help)        print_help ;;
        *)
            echo "Unknown command: ${command}" >&2
            echo "" >&2
            print_help >&2
            exit 1
            ;;
    esac
}

main "$@"
