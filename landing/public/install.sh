#!/usr/bin/env bash
# Usage: curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash
# Keep this entry point small: installation runs from one exact release commit.
main() (
    set -euo pipefail
    # Only opt into loader parsing when the new selector is present. Every
    # existing invocation is forwarded verbatim to the released bootstrap.
    original_args=("$@")
    multi=0
    new_setup=0
    ui_mode=""
    ui_args=()
    selector=""
    singleton=""
    channels=0
    json=0
    pending=0
    unknown_arg=""
    seen=""
    route_seen=""
    nav="" app="" team="" caller_unknown="" caller_asserted="" notify=""
    # Classify questions and validate new flags before the first download.
    while [ "$#" -gt 0 ]; do
        key=${1%%=*}
        case "$key" in
            --ui|--plain|--product|--products|--desktop|--webhook|--json)
                case "$key" in
                    --ui|--plain) ;;
                    *) case ",$seen," in *",$key,"*) echo "Use $key once." >&2; exit 1 ;; esac
                       seen=${seen:+$seen,}$key ;;
                esac ;;
        esac
        case "$1" in
            --ui|--ui=*|--plain)
                case "$1" in
                    --plain) mode=plain; shift ;;
                    --ui) [ "$#" -ge 2 ] || { echo "--ui requires a mode." >&2; exit 1; }; mode=$2; shift 2 ;;
                    *) mode=${1#--ui=}; shift ;;
                esac
                case "$mode" in auto|rich|plain) ;; *) echo "Invalid UI mode." >&2; exit 1 ;; esac
                [ -z "$ui_mode" ] || [ "$ui_mode" = "$mode" ] || { echo "Conflicting UI modes." >&2; exit 1; }
                ui_mode=$mode; ui_args=(--ui "$mode") ;;
            --product|--products|--product=*|--products=*)
                [ -z "$selector" ] || { echo "Use one product selector once." >&2; exit 1; }
                selector=$key
                case "$1" in
                    *=*) singleton=${1#*=}; shift ;;
                    *) [ "$#" -ge 2 ] || { echo "$1 requires a value." >&2; exit 1; }; singleton=$2; shift 2 ;;
                esac
                [ -n "$singleton" ] || { echo "Empty product selector." >&2; exit 1; }
                if [ "$selector" = --product ]; then
                    case "$singleton" in claude|codex|both|opencode|gemini) ;; *) echo "Invalid product." >&2; exit 1 ;; esac
                fi ;;
            --desktop|--webhook) channels=$((channels + 1)); shift ;;
            --json) json=1; shift ;;
            --agent-notify|--skip-agent-notify|--request-permission|--preserve-policy|--navigation|--navigation=*|--app|--app=*|--team-id|--team-id=*|--allow-unknown-caller|--allow-unknown-caller=*|--allow-caller-asserted|--allow-caller-asserted=*|--codex-home|--codex-home=*)
                case ",$route_seen," in *",$key,"*) echo "Use $key once." >&2; exit 1 ;; esac
                route_seen=${route_seen:+$route_seen,}$key
                case "$key" in
                    --agent-notify|--skip-agent-notify)
                        [ -z "$notify" ] || { echo "Use one agent-notify option once." >&2; exit 1; }
                        notify=$key; shift; continue ;;
                    --request-permission|--preserve-policy) shift; continue ;;
                esac
                case "$1" in
                    *=*) value=${1#*=}; shift ;;
                    *) [ "$#" -ge 2 ] || { echo "Missing value for $1." >&2; exit 1; }; value=$2; shift 2 ;;
                esac
                case "$key" in
                    --navigation) [ "$value" = none ] || { echo "Invalid navigation." >&2; exit 1; }; nav=$value ;;
                    --app|--codex-home)
                        case "$value" in /*) ;; *) echo "Path must be absolute." >&2; exit 1 ;; esac
                        if [ "$key" = --app ]; then
                            case "$value" in *..*) echo "App path must be physical." >&2; exit 1 ;; esac
                            app=$value
                        fi ;;
                    --team-id) [ -n "$value" ] || exit 1; team=$value ;;
                    --allow-unknown-caller|--allow-caller-asserted)
                        case "$value" in true|false) ;; *) echo "Invalid caller bool." >&2; exit 1 ;; esac
                        if [ "$key" = --allow-unknown-caller ]; then caller_unknown=$value; else caller_asserted=$value; fi ;;
                esac ;;
            --help|-h) shift ;;
            *) unknown_arg=$1; shift ;;
        esac
    done
    if [ -n "$nav$app$team$caller_unknown$caller_asserted" ]; then
        if [ "$nav" = none ]; then
            [ -z "$app$team" ] && [ -n "$caller_unknown" ] && [ -n "$caller_asserted" ] || { echo "Incomplete or conflicting route." >&2; exit 1; }
        else
            [ -n "$app" ] && [ -n "$team" ] && [ -n "$caller_unknown" ] && [ -n "$caller_asserted" ] || { echo "Incomplete route." >&2; exit 1; }
        fi
    fi
    if [ "$notify" = --skip-agent-notify ]; then
        case ",$route_seen," in *,--navigation,*|*,--app,*|*,--team-id,*|*,--allow-unknown-caller,*|*,--allow-caller-asserted,*|*,--preserve-policy,*|*,--request-permission,*) echo "Route flags require agent-notify." >&2; exit 1 ;; esac
    fi
    if [ "$selector" = --product ] && { [ "$singleton" = gemini ] || [ "$singleton" = opencode ]; }; then
        [ -z "$route_seen" ] && [ "$json" -eq 0 ] || { echo "Observers use channel flags only." >&2; exit 1; }
    fi
    set -- "${original_args[@]}"
    if [ -z "$selector" ] || { [ "$selector" = --product ] && [ "$channels" -eq 0 ] && { [ "$singleton" = opencode ] || [ "$singleton" = gemini ]; }; }; then
        pending=1
    fi
    if [ "$pending" -eq 1 ] && [ -n "$unknown_arg" ]; then
        echo "Unknown option: $unknown_arg." >&2; exit 1
    fi
    if [ "$pending" -eq 1 ] && [ "$json" -eq 1 ]; then
        echo "Pending questions require complete explicit product/route/channel input with --json." >&2; exit 1
    fi
    previous=""
    # No arguments delegates interactive selection to the released bootstrap.
    for arg in "$@"; do
        case "$arg" in
            --products|--products=*) multi=1; new_setup=1 ;;
            --product=gemini) new_setup=1 ;;
            gemini) [ "$previous" != --product ] || new_setup=1 ;;
        esac
        previous=$arg
    done
    legacy_product=""
    legacy_args=()
    opencode_args=()
    opencode_channels=0
    if [ "$multi" -eq 1 ]; then
        selector_seen=0
        claude=0 codex=0 opencode=0 gemini=0
        notify=""
        help=0
        while [ "$#" -gt 0 ]; do
            case "$1" in
                --products|--products=*)
                    [ "$selector_seen" -eq 0 ] || { echo "Use --products once." >&2; exit 1; }
                    selector_seen=1
                    case "$1" in
                        --products)
                            [ "$#" -ge 2 ] || { echo "--products requires a comma-separated selection." >&2; exit 1; }
                            selection=$2; shift ;;
                        *) selection=${1#--products=} ;;
                    esac
                    case "$selection" in
                        ''|,*|*,|*,,*) echo "--products contains an empty product." >&2; exit 1 ;;
                    esac
                    remaining=$selection
                    while :; do
                        product=${remaining%%,*}
                        case "$product" in
                            claude) [ "$claude" -eq 0 ] || { echo "Duplicate product: claude." >&2; exit 1; }; claude=1 ;;
                            codex) [ "$codex" -eq 0 ] || { echo "Duplicate product: codex." >&2; exit 1; }; codex=1 ;;
                            opencode) [ "$opencode" -eq 0 ] || { echo "Duplicate product: opencode." >&2; exit 1; }; opencode=1 ;;
                            gemini) [ "$gemini" -eq 0 ] || { echo "Duplicate product: gemini." >&2; exit 1; }; gemini=1 ;;
                            *) echo "Unknown product: $product." >&2; exit 1 ;;
                        esac
                        case "$remaining" in
                            *,*) remaining=${remaining#*,} ;;
                            *) break ;;
                        esac
                    done ;;
                --product|--product=*) echo "--product and --products cannot be combined." >&2; exit 1 ;;
                --agent-notify|--skip-agent-notify)
                    [ -z "$notify" ] || { echo "Use one agent-notify option once." >&2; exit 1; }
                    notify=$1
                    legacy_args+=("$1") ;;
                --desktop|--webhook)
                    for channel in ${opencode_args[@]+"${opencode_args[@]}"}; do
                        [ "$channel" != "$1" ] || { echo "Use each observer channel once." >&2; exit 1; }
                    done
                    opencode_args+=("$1")
                    opencode_channels=$((opencode_channels + 1)) ;;
                --ui|--ui=*)
                    if [ "$1" = --ui ]; then shift; fi ;;
                --plain) ;;
                --help|-h) help=1 ;;
                *) echo "Unsupported --products option: $1." >&2; exit 1 ;;
            esac
            shift
        done
        if [ "$help" -eq 1 ]; then
            echo "Usage: bash install.sh --products claude,codex,opencode,gemini [--agent-notify|--skip-agent-notify] [--desktop] [--webhook]"
            echo "Choose any nonempty subset. Agent-notify options apply to Claude/Codex; Observers require --desktop and/or --webhook."
            exit 0
        fi
        if [ "$claude" -eq 1 ] && [ "$codex" -eq 1 ]; then
            legacy_product=both
        elif [ "$claude" -eq 1 ]; then
            legacy_product=claude
        elif [ "$codex" -eq 1 ]; then
            legacy_product=codex
        fi
        [ -z "$notify" ] || [ -n "$legacy_product" ] || {
            echo "Agent-notify options require Claude or Codex in --products." >&2; exit 1;
        }
        if [ "$opencode" -eq 1 ] || [ "$gemini" -eq 1 ]; then
            [ "$opencode_channels" -gt 0 ] || {
                echo "Selected observers require explicit --desktop and/or --webhook consent." >&2; exit 1;
            }
        elif [ "$opencode_channels" -gt 0 ]; then
            echo "--desktop/--webhook require OpenCode or Gemini in --products." >&2; exit 1
        fi
    fi
    command -v curl >/dev/null 2>&1 || {
        echo "curl is required to install Agent Notifications." >&2
        exit 1
    }
    repo=777genius/agent-notifications
    release_base="https://github.com/$repo/releases/tag/"
    latest_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
    case "$latest_url" in
        "$release_base"*) tag=${latest_url#"$release_base"} ;;
        *) echo "Invalid stable release tag redirect." >&2; exit 1 ;;
    esac
    if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
        echo "Invalid stable release tag redirect." >&2
        exit 1
    fi
    if [ "$multi" -eq 1 ] && [ "$opencode" -eq 1 ]; then
        version=${tag#v}
        major=${version%%.*}
        version=${version#*.}
        minor=${version%%.*}
        # Compare only small numbers, so arbitrary valid semver components
        # cannot overflow Bash arithmetic. OpenCode first shipped in v1.46.0.
        if [ "$major" = 0 ] || { [ "$major" = 1 ] && [ "${#minor}" -le 2 ] && [ "$minor" -lt 46 ]; }; then
            echo "OpenCode requires stable release v1.46.0 or newer; latest is $tag. No products were installed." >&2
            exit 1
        fi
    fi
    stage=$(mktemp -d "${TMPDIR:-/tmp}/agent-notifications-setup.XXXXXX")
    trap 'rm -rf "$stage"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP

    curl -fsSL -H 'Accept: application/vnd.github.sha' "https://api.github.com/repos/$repo/commits/$tag" -o "$stage/commit"
    [ "$(LC_ALL=C wc -c < "$stage/commit" | tr -d '[:space:]')" = 40 ] && LC_ALL=C grep -Eq '^[0-9a-f]{40}$' "$stage/commit" || {
        echo "Invalid release commit." >&2
        exit 1
    }
    IFS= read -r commit < "$stage/commit" || [ -n "${commit:-}" ]
    raw="https://raw.githubusercontent.com/$repo/$commit/bin"
    # A failed or interrupted download must never execute a partial script.
    curl -fsSL "$raw/bootstrap.sh" -o "$stage/bootstrap.sh"
    run_bootstrap() {
        env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" \
            INSTALL_SCRIPT_URL="$raw/install.sh" bash "$stage/bootstrap.sh" "$@"
    }
    if [ "$pending" -eq 1 ]; then
        feature_status=0
        run_bootstrap --selector-capabilities >"$stage/selector-capabilities" 2>/dev/null || feature_status=$?
        if [ "$feature_status" -ne 0 ] || ! printf 'terminal-selector-v1\n' | cmp -s - "$stage/selector-capabilities"; then
            echo "This published release needs terminal selector support. No products were installed. Use complete explicit input or an updated release." >&2
            exit 1
        fi
    fi
    if [ "$new_setup" -eq 1 ]; then
        capability_status=0
        run_bootstrap --capabilities >"$stage/capabilities" 2>/dev/null || capability_status=$?
        if [ "$capability_status" -ne 0 ] || ! printf 'bootstrap-products-v1\n' | cmp -s - "$stage/capabilities"; then
            if [ "$multi" -eq 1 ] && [ "$gemini" -eq 0 ] && [ "$pending" -eq 0 ] && [ -z "$ui_mode" ]; then
                # Older releases accept one legacy product group per invocation.
                # Stop on either failure; the second group may fail after the first installed.
                if [ -n "$legacy_product" ]; then
                    run_bootstrap --product "$legacy_product" ${legacy_args[@]+"${legacy_args[@]}"}
                fi
                if [ "$opencode" -eq 1 ]; then
                    run_bootstrap --product opencode ${opencode_args[@]+"${opencode_args[@]}"}
                fi
                exit 0
            fi
            echo "This published release lacks four-product setup. No products were installed. Use an updated release when available." >&2
            exit 1
        fi
    fi
    if [ "$multi" -eq 0 ]; then
        run_bootstrap "$@"
    else
        # One bootstrap performs every selected prerequisite/capability check
        # before the first product mutates its installation.
        run_bootstrap --products "$selection" ${legacy_args[@]+"${legacy_args[@]}"} ${opencode_args[@]+"${opencode_args[@]}"} ${ui_args[@]+"${ui_args[@]}"}
    fi
)

# All loader code is parsed before the piped entry point performs any work.
main "$@"
