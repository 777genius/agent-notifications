#!/usr/bin/env bash
# Usage: curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/setup.sh | bash
# Keep this entry point small: installation runs from one exact release commit.
main() (
    set -euo pipefail
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
    env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" \
        INSTALL_SCRIPT_URL="$raw/install.sh" bash "$stage/bootstrap.sh" "$@"
)

# All loader code is parsed before the piped entry point performs any work.
main "$@"
