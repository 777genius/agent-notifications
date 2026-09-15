#!/usr/bin/env bash
# Usage: curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/setup.sh | bash
# Keep this entry point small: installation runs from one exact release commit.
main() (
    set -euo pipefail
    for dependency in curl python3; do
        command -v "$dependency" >/dev/null 2>&1 || {
            echo "$dependency is required to install Agent Notifications." >&2
            exit 1
        }
    done

    repo=777genius/agent-notifications
    tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | python3 -I -c '
import json, re, sys
value = json.load(sys.stdin).get("tag_name")
if not isinstance(value, str) or not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", value):
    sys.exit("Invalid stable release tag")
sys.stdout.buffer.write((value + "\n").encode("ascii"))
')
    commit=$(curl -fsSL "https://api.github.com/repos/$repo/commits/$tag" | python3 -I -c '
import json, re, sys
value = json.load(sys.stdin).get("sha")
if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
    sys.exit("Invalid release commit")
sys.stdout.buffer.write((value + "\n").encode("ascii"))
')
    raw="https://raw.githubusercontent.com/$repo/$commit/bin"
    stage=$(mktemp -d "${TMPDIR:-/tmp}/agent-notifications-setup.XXXXXX")
    trap 'rm -rf "$stage"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP

    # A failed or interrupted download must never execute a partial script.
    curl -fsSL "$raw/bootstrap.sh" -o "$stage/bootstrap.sh"
    env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" \
        INSTALL_SCRIPT_URL="$raw/install.sh" bash "$stage/bootstrap.sh" "$@"
)

# All loader code is parsed before the piped entry point performs any work.
main "$@"
