#!/usr/bin/env bash
# Collect Linux click-to-focus diagnostics for Agent Notifications.

set +x
set -uo pipefail
umask 077

REPO="777genius/agent-notifications"
RAW_URL="https://raw.githubusercontent.com/${REPO}/main/scripts/linux-focus-debug.sh"
MARKETPLACE_NAME="claude-notifications-go"
PLUGIN_NAME="claude-notifications-go"
PLUGIN_KEY="${PLUGIN_NAME}@${MARKETPLACE_NAME}"

CLAUDE_HOME="${CLAUDE_CONFIG_DIR:-${CLAUDE_HOME:-$HOME/.claude}}"
KNOWN_MARKETPLACES_JSON="${CLAUDE_HOME}/plugins/known_marketplaces.json"
INSTALLED_JSON="${CLAUDE_HOME}/plugins/installed_plugins.json"

DEFAULT_REPORT_PATH="${PWD}/claude-notifications-linux-focus-report-$(date +%Y%m%d-%H%M%S).txt"
REPORT_PATH="${REPORT_PATH:-$DEFAULT_REPORT_PATH}"
WRITE_TO_STDOUT=0
REPORT_FILE=""

warn_stderr() {
    printf 'Warning: %s\n' "$*" >&2
}

print_help() {
    cat <<EOF
Usage: scripts/linux-focus-debug.sh [--output PATH] [--stdout]

Collects a diagnostic report for Linux click-to-focus issues.

Options:
  --output PATH   Write the report to PATH
  --stdout        Print the report to stdout instead of saving a file
  -h, --help      Show this help

Examples:
  bash scripts/linux-focus-debug.sh
  bash scripts/linux-focus-debug.sh --output "$HOME/focus-report.txt"
  curl -fsSL ${RAW_URL} | bash
  curl -fsSL ${RAW_URL} | REPORT_PATH="$HOME/focus-report.txt" bash

What to do:
  1. Reproduce the click-to-focus issue first.
  2. Run this script immediately after the failed click.
  3. Review the generated report before sharing it in an issue.
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --output)
            if [ $# -lt 2 ] || [ -z "${2:-}" ]; then
                echo "Error: --output requires a non-empty path" >&2
                exit 1
            fi
            REPORT_PATH="$2"
            shift 2
            ;;
        --stdout)
            WRITE_TO_STDOUT=1
            shift
            ;;
        -h|--help|help)
            print_help
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            echo "" >&2
            print_help >&2
            exit 1
            ;;
    esac
done

if [ "$(uname -s 2>/dev/null || echo unknown)" != "Linux" ]; then
    echo "Error: this diagnostic script is for Linux only." >&2
    exit 1
fi

if [ "$WRITE_TO_STDOUT" -eq 1 ]; then
    REPORT_FILE="/dev/stdout"
else
    REPORT_FILE="$REPORT_PATH"
    # Refuse existing files/links; do not truncate a potentially public report.
    if ! mkdir -p "$(dirname "$REPORT_FILE")" 2>/dev/null ||
       ! (set -o noclobber; : > "$REPORT_FILE") 2>/dev/null; then
        warn_stderr "cannot create a new private report; choose an unused --output path"
        exit 1
    fi
fi

write_line() {
    if ! printf '%s\n' "$*" >> "$REPORT_FILE" 2>/dev/null; then
        warn_stderr "failed to append diagnostic report; stopping without fallback output"
        exit 1
    fi
}

write_header() {
    write_line ""
    write_line "=== $1 ==="
}

cmd_exists() {
    command -v "$1" >/dev/null 2>&1
}

run_cmd() {
    local label="$1"
    shift
    write_line ""
    write_line "\$ $label"
    if [ $# -eq 0 ]; then
        write_line "(no command)"
        return 0
    fi

    local output
    if output="$("$@" 2>&1)"; then
        if [ -n "$output" ]; then
            write_line "$output"
        else
            write_line "(no output)"
        fi
    else
        local rc=$?
        if [ -n "$output" ]; then
            write_line "$output"
        fi
        write_line "(exit code: $rc)"
    fi
}

print_var() {
    local name="$1"
    write_line "${name}=${!name-}"
}

json_query() {
    local file="$1"
    local jq_expr="$2"
    local py_expr="$3"

    [ -f "$file" ] || return 0

    if cmd_exists jq; then
        jq -r "${jq_expr} // empty" "$file" 2>/dev/null || true
        return 0
    fi

    if cmd_exists python3; then
        python3 -I - "$file" "$py_expr" <<'PYEOF' 2>/dev/null || true
import json, sys
path = [part for part in sys.argv[2].split('.') if part]
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    cur = data
    for part in path:
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

get_marketplace_source() {
    local source_type source_value
    source_type="$(json_query "$KNOWN_MARKETPLACES_JSON" ".[\"${MARKETPLACE_NAME}\"].source.source" "${MARKETPLACE_NAME}.source.source")"
    case "$source_type" in
        directory)
            source_value="$(json_query "$KNOWN_MARKETPLACES_JSON" ".[\"${MARKETPLACE_NAME}\"].source.path" "${MARKETPLACE_NAME}.source.path")"
            ;;
        github)
            source_value="$(json_query "$KNOWN_MARKETPLACES_JSON" ".[\"${MARKETPLACE_NAME}\"].source.repo" "${MARKETPLACE_NAME}.source.repo")"
            ;;
        *)
            source_value=""
            ;;
    esac
    if [ -n "$source_type" ] && [ -n "$source_value" ]; then
        printf '%s:%s' "$source_type" "$source_value"
    fi
}

get_installed_version() {
    if cmd_exists jq; then
        jq -r ".plugins[\"${PLUGIN_KEY}\"][0].version // empty" "$INSTALLED_JSON" 2>/dev/null || true
        return 0
    fi

    if cmd_exists python3; then
        python3 -I - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    entries = data.get('plugins', {}).get(sys.argv[2], [])
    if entries:
        print(entries[0].get('version', '') or '')
except Exception:
    pass
PYEOF
    fi
}

get_install_path() {
    if cmd_exists jq; then
        jq -r ".plugins[\"${PLUGIN_KEY}\"][0].installPath // empty" "$INSTALLED_JSON" 2>/dev/null || true
        return 0
    fi

    if cmd_exists python3; then
        python3 -I - "$INSTALLED_JSON" "$PLUGIN_KEY" <<'PYEOF' 2>/dev/null || true
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    entries = data.get('plugins', {}).get(sys.argv[2], [])
    if entries:
        print(entries[0].get('installPath', '') or '')
except Exception:
    pass
PYEOF
    fi
}

resolve_plugin_log_path() {
    local install_path="$1"
    if [ -n "$install_path" ] && [ -f "${install_path}/notification-debug.log" ]; then
        printf '%s' "${install_path}/notification-debug.log"
        return 0
    fi

    local fallback="${CLAUDE_HOME}/plugins/cache/${MARKETPLACE_NAME}/${MARKETPLACE_NAME}"
    if [ -d "$fallback" ]; then
        local newest=""
        local newest_mtime=0
        local path
        for path in "$fallback"/*/notification-debug.log; do
            [ -f "$path" ] || continue
            local mtime=0
            mtime="$(stat -c %Y "$path" 2>/dev/null || echo 0)"
            if [ "$mtime" -ge "$newest_mtime" ]; then
                newest="$path"
                newest_mtime="$mtime"
            fi
        done
        [ -n "$newest" ] && printf '%s' "$newest"
    fi
}

write_command_availability() {
    local cmd
    for cmd in claude jq python3 xdotool wmctrl xprop gdbus busctl wlrctl kdotool remotinator notify-send; do
        if cmd_exists "$cmd"; then
            write_line "${cmd}=yes ($(command -v "$cmd"))"
        else
            write_line "${cmd}=no"
        fi
    done
}

write_window_searches() {
    local klass
    for klass in Terminator terminator gnome-terminal-server kitty code Code Alacritty WezTerm wezterm Tilix xfce4-terminal mate-terminal konsole; do
        if cmd_exists xdotool; then
            run_cmd "xdotool search --class ${klass}" xdotool search --class "$klass"
        fi
    done
}

write_focus_capability_summary() {
    local windowid_state="missing"
    local title_hint_state="missing"
    local x11_exact="no"
    local terminator_exact="no"

    if [ -n "${WINDOWID:-}" ]; then
        windowid_state="present"
    fi
    if [ -n "${TERMINATOR_UUID:-}" ] && cmd_exists remotinator; then
        title_hint_state="available"
    elif [ -n "${TERMINATOR_UUID:-}" ]; then
        title_hint_state="terminator-present-but-remotinator-missing"
    fi

    if [ -n "${WINDOWID:-}" ] && { cmd_exists xdotool || cmd_exists wmctrl; }; then
        x11_exact="yes"
    fi
    if [ -n "${TERMINATOR_UUID:-}" ] && cmd_exists remotinator && { cmd_exists xdotool || cmd_exists wmctrl; }; then
        terminator_exact="yes"
    fi

    write_line "windowid_hint=${windowid_state}"
    write_line "terminator_title_hint=${title_hint_state}"
    write_line "x11_exact_focus_possible=${x11_exact}"
    write_line "terminator_exact_title_focus_possible=${terminator_exact}"
    write_line "gnome_focus_tools_available=$( { cmd_exists gdbus || cmd_exists busctl; } && echo yes || echo no )"
    write_line "wlroots_focus_tool_available=$( cmd_exists wlrctl && echo yes || echo no )"
    write_line "kde_focus_tool_available=$( cmd_exists kdotool && echo yes || echo no )"
}

run_window_identity_probe() {
    local window_id="$1"
    local label="$2"
    [ -n "$window_id" ] || return 0

    write_line ""
    write_line "--- ${label}: ${window_id} ---"
    if cmd_exists xdotool; then
        run_cmd "xdotool getwindowname ${window_id}" xdotool getwindowname "$window_id"
        run_cmd "xdotool getwindowpid ${window_id}" xdotool getwindowpid "$window_id"
    fi
    if cmd_exists xprop; then
        run_cmd "xprop -id ${window_id} WM_CLASS WM_NAME _NET_WM_NAME _NET_WM_DESKTOP" \
            xprop -id "$window_id" WM_CLASS WM_NAME _NET_WM_NAME _NET_WM_DESKTOP
    fi
}

write_relevant_processes() {
    # Arguments may contain tokens or webhook URLs; collect names only.
    run_cmd "ps -eo pid,comm" ps -eo pid,comm
}

write_safe_config_inspect() {
    local binary="${CLAUDE_NOTIFICATIONS_BIN:-}" output
    if [ -z "$binary" ] && [ -n "$INSTALL_PATH" ]; then
        binary="${INSTALL_PATH}/bin/claude-notifications"
    fi
    if [ -z "$binary" ] && cmd_exists claude-notifications; then
        binary="$(command -v claude-notifications)"
    fi
    if [ -n "$binary" ] && [ -x "$binary" ]; then
        # Do not include arbitrary stderr from an older/unsupported executable.
        # A nonzero inspect may still emit a valid safe error projection.
        output="$("$binary" config inspect --json 2>/dev/null)" || true
        if cmd_exists python3; then
            output="$(printf '%s' "$output" | python3 -I -c '
import json, math, re, sys
def require(ok):
    if not ok:
        raise ValueError()
def fields(value, allowed):
    require(type(value) is dict and set(value) <= set(allowed))
def boolean(value):
    require(type(value) is bool)
    return value
def pairs(items):
    out = {}
    for key, value in items:
        require(key not in out)
        out[key] = value
    return out
codes = set("""ConfigLegacyImportRequired ConfigOverrideInvalid ConfigHomeUnavailable
ConfigBaseUnavailable ConfigInvalid ConfigUnsupportedSchema ConfigPermissionDenied
ConfigRecoveryRequired ConfigMultipleCandidates ConfigLinkedPath ConfigPublicReadable
ConfigChanged ConfigMissing ConfigEnvConflict ConfigLockTimeout ConfigConflict
ConfigCommitUncertain ConfigLegacyAlternateFound ConfigSelected""".split())
try:
    x = json.load(sys.stdin, object_pairs_hook=pairs)
    fields(x, ("selection", "revision", "schemaVersion", "valid", "errorCode", "settings"))
    q = x["selection"]
    fields(q, ("path", "source", "exists", "diagnostics"))
    require(type(q["path"]) is str and type(q["source"]) is str)
    require(q["source"] in ("explicit", "legacy", "universal"))
    selection = dict(path=q["path"], source=q["source"], exists=boolean(q["exists"]))
    if "diagnostics" in q:
        require(type(q["diagnostics"]) is list)
        diagnostics = []
        for d in q["diagnostics"]:
            fields(d, ("code", "path"))
            require(type(d["code"]) is str and d["code"] in codes)
            safe = dict(code=d["code"])
            if "path" in d:
                require(type(d["path"]) is str)
                safe["path"] = d["path"]
            diagnostics.append(safe)
        selection["diagnostics"] = diagnostics
    out = dict(selection=selection, valid=boolean(x["valid"]))
    if "revision" in x:
        require(type(x["revision"]) is str and re.fullmatch("[0-9a-f]{64}", x["revision"]) is not None)
        out["revision"] = x["revision"]
    if "schemaVersion" in x:
        require(type(x["schemaVersion"]) is int and x["schemaVersion"] > 0)
        out["schemaVersion"] = x["schemaVersion"]
    if "errorCode" in x:
        require(type(x["errorCode"]) is str and x["errorCode"] in codes)
        out["errorCode"] = x["errorCode"]
    if "settings" in x:
        t = x["settings"]
        fields(t, ("desktopEnabled", "desktopSound", "volume", "statuses"))
        safe = {}
        for key in ("desktopEnabled", "desktopSound"):
            if key in t:
                safe[key] = boolean(t[key])
        if "volume" in t:
            require(type(t["volume"]) in (int, float) and math.isfinite(t["volume"]) and 0 <= t["volume"] <= 1)
            safe["volume"] = t["volume"]
        if "statuses" in t:
            fields(t["statuses"], ("task_complete", "review_complete", "question", "plan_ready",
                                  "session_limit_reached", "api_error", "api_error_overloaded", "permission_request"))
            statuses = {}
            for name, value in t["statuses"].items():
                fields(value, ("enabled", "desktopEnabled", "webhookEnabled"))
                statuses[name] = {key: None if val is None else boolean(val) for key, val in value.items()}
            safe["statuses"] = statuses
        out["settings"] = safe
    print(json.dumps(out, indent=2))
except Exception:
    sys.exit(1)
' 2>/dev/null)" && { write_line "$output"; return; }
        fi
    fi
    write_line "Safe config inspect unavailable. Run the installed config-capable executable with config inspect --json in this same environment; report its version if unsupported. Do not copy raw config. No config path was guessed or file initialized."
}

write_focus_tool_interpretation() {
    write_line "- X11 exact focus depends on WINDOWID plus tools like xdotool or wmctrl."
    write_line "- Terminator exact title focus depends on TERMINATOR_UUID plus remotinator."
    write_line "- Wayland sessions often have empty WINDOWID; that is expected and should be reported."
    write_line "- GNOME-specific focus paths depend on gdbus/busctl and, optionally, the activate-window-by-title extension."
}

ACTIVE_WINDOW_ID=""
if cmd_exists xdotool; then
    ACTIVE_WINDOW_ID="$(xdotool getactivewindow 2>/dev/null || true)"
fi

INSTALLED_VERSION="$(get_installed_version)"
INSTALL_PATH="$(get_install_path)"
MARKETPLACE_SOURCE="$(get_marketplace_source)"
PLUGIN_LOG_PATH="$(resolve_plugin_log_path "$INSTALL_PATH")"

write_line "Agent Notifications Linux Focus Debug Report"
write_line "Generated at: $(date -Is 2>/dev/null || date)"
write_line "Hostname: $(hostname 2>/dev/null || echo unknown)"

write_header "Summary"
write_line "claude_home=${CLAUDE_HOME}"
write_line "marketplace_source=${MARKETPLACE_SOURCE:-missing}"
write_line "installed_version=${INSTALLED_VERSION:-missing}"
write_line "install_path=${INSTALL_PATH:-missing}"
write_line "plugin_log_path=${PLUGIN_LOG_PATH:-missing}"
write_line "report_path=${REPORT_FILE}"
write_focus_capability_summary

write_header "Environment"
print_var USER
print_var SHELL
print_var PWD
print_var XDG_SESSION_TYPE
print_var DISPLAY
print_var WAYLAND_DISPLAY
print_var DESKTOP_SESSION
print_var XDG_CURRENT_DESKTOP
print_var GDMSESSION
print_var TERM
print_var TERM_PROGRAM
print_var TERMINATOR_UUID
print_var WINDOWID
print_var TMUX
print_var ZELLIJ
print_var KITTY_WINDOW_ID
print_var CLAUDE_CONFIG_DIR

write_header "System"
run_cmd "uname -a" uname -a
if [ -f /etc/os-release ]; then
    run_cmd "cat /etc/os-release" cat /etc/os-release
fi
run_cmd "date -Is" date -Is

write_header "Command Availability"
write_command_availability

write_header "Claude / Plugin Metadata"
if cmd_exists claude; then
    run_cmd "claude -v" claude -v
fi
if [ -f "$KNOWN_MARKETPLACES_JSON" ]; then
    run_cmd "cat ${KNOWN_MARKETPLACES_JSON}" cat "$KNOWN_MARKETPLACES_JSON"
fi
if [ -f "$INSTALLED_JSON" ]; then
    run_cmd "cat ${INSTALLED_JSON}" cat "$INSTALLED_JSON"
fi
write_header "Safe Configuration Inspect"
write_safe_config_inspect
if [ -n "$INSTALL_PATH" ] && [ -f "${INSTALL_PATH}/.claude-plugin/plugin.json" ]; then
    run_cmd "cat ${INSTALL_PATH}/.claude-plugin/plugin.json" cat "${INSTALL_PATH}/.claude-plugin/plugin.json"
fi

write_header "Relevant Processes"
write_relevant_processes

write_header "Window State"
if cmd_exists xdotool; then
    run_cmd "xdotool getactivewindow" xdotool getactivewindow
fi
if cmd_exists wmctrl; then
    run_cmd "wmctrl -m" wmctrl -m
    run_cmd "wmctrl -l" wmctrl -l
    run_cmd "wmctrl -lx" wmctrl -lx
    run_cmd "wmctrl -lp" wmctrl -lp
fi
if cmd_exists xprop; then
    run_cmd "xprop -root _NET_ACTIVE_WINDOW _NET_CLIENT_LIST_STACKING" \
        xprop -root _NET_ACTIVE_WINDOW _NET_CLIENT_LIST_STACKING
fi
run_window_identity_probe "$ACTIVE_WINDOW_ID" "active_window"
run_window_identity_probe "${WINDOWID:-}" "hook_windowid"

write_header "Window Searches"
write_window_searches
if cmd_exists remotinator; then
    run_cmd "remotinator get_window_title" remotinator get_window_title
fi

write_header "Tool Probes"
if cmd_exists gdbus; then
    run_cmd "gdbus introspect --session --dest org.gnome.Shell --object-path /org/gnome/Shell" \
        gdbus introspect --session --dest org.gnome.Shell --object-path /org/gnome/Shell
    run_cmd "gdbus introspect --session --dest org.gnome.Shell --object-path /de/lucaswerkmeister/ActivateWindowByTitle" \
        gdbus introspect --session --dest org.gnome.Shell --object-path /de/lucaswerkmeister/ActivateWindowByTitle
fi
if cmd_exists busctl; then
    run_cmd "busctl --user list" busctl --user list
fi

write_header "Plugin Logs"
write_line "Raw log contents omitted: they may contain webhook credentials or payloads. Review locally and share only a minimal sanitized excerpt."

write_header "Notes"
write_line "- Review the report for private paths or window titles before sharing it publicly."
write_line "- Reproduce the failed click immediately before running this script for the most useful local investigation."
write_line "- If WINDOWID is empty under Wayland, include that fact when reporting the issue."
write_focus_tool_interpretation

if [ "$WRITE_TO_STDOUT" -eq 0 ]; then
    echo "Linux focus debug report written to:"
    echo "  $REPORT_FILE"
    echo ""
    echo "Please review it, then attach it to the GitHub issue."
fi
