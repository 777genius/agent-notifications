#!/usr/bin/env bash
# Usage: curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash
# Keep this entry point small: installation runs from one exact release commit.
main() (
    set -euo pipefail
    # Only opt into loader parsing when the new selector is present. Every
    # existing invocation is forwarded verbatim to the released bootstrap.
    multi=0
    for arg in "$@"; do
        case "$arg" in --products|--products=*) multi=1 ;; esac
    done
    legacy_product=""
    legacy_args=()
    opencode_args=()
    opencode_channels=0
    if [ "$multi" -eq 1 ]; then
        selector_seen=0
        claude=0 codex=0 opencode=0
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
                        [ "$channel" != "$1" ] || { echo "Use each OpenCode channel once." >&2; exit 1; }
                    done
                    opencode_args+=("$1")
                    opencode_channels=$((opencode_channels + 1)) ;;
                --help|-h) help=1 ;;
                *) echo "Unsupported --products option: $1." >&2; exit 1 ;;
            esac
            shift
        done
        if [ "$help" -eq 1 ]; then
            echo "Usage: bash install.sh --products claude,codex,opencode [--agent-notify|--skip-agent-notify] [--desktop] [--webhook]"
            echo "Choose any nonempty subset. Agent-notify options apply to Claude/Codex; OpenCode requires --desktop and/or --webhook."
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
        if [ "$opencode" -eq 1 ]; then
            [ "$opencode_channels" -gt 0 ] || {
                echo "OpenCode requires explicit --desktop and/or --webhook consent." >&2; exit 1;
            }
        elif [ "$opencode_channels" -gt 0 ]; then
            echo "--desktop/--webhook require OpenCode in --products." >&2; exit 1
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
    if [ "$multi" -eq 0 ]; then
        run_bootstrap "$@"
    else
        if [ -n "$legacy_product" ]; then
            # Capture the original status before printing failure information.
            run_bootstrap --product "$legacy_product" ${legacy_args[@]+"${legacy_args[@]}"} || {
                status=$?
                echo "Claude/Codex installation failed; remaining products were not installed." >&2
                exit "$status"
            }
        fi
        if [ "$opencode" -eq 1 ]; then
            run_bootstrap --product opencode "${opencode_args[@]}" || {
                status=$?
                if [ -n "$legacy_product" ]; then
                    echo "Partial success: Claude/Codex installation completed; OpenCode installation failed." >&2
                fi
                exit "$status"
            }
        fi
    fi
)

# All loader code is parsed before the piped entry point performs any work.
main "$@"
