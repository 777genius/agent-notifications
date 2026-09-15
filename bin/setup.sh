#!/usr/bin/env bash
# Usage: curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/setup.sh | bash
# Keep this entry point small: installation runs from one exact release commit.
# Prefer isolated python3; Node is the supported fallback (Claude Code ships it).
decode_github_json() {
    if command -v python3 >/dev/null 2>&1; then
        python3 -I -c "$1"
    elif command -v node >/dev/null 2>&1; then
        NODE_OPTIONS= NODE_PATH= node --no-warnings -e "$2"
    else
        echo "python3 or node is required to install Agent Notifications." >&2
        exit 1
    fi
}

main() (
    set -euo pipefail
    command -v curl >/dev/null 2>&1 || {
        echo "curl is required to install Agent Notifications." >&2
        exit 1
    }
    command -v python3 >/dev/null 2>&1 || command -v node >/dev/null 2>&1 || {
        echo "python3 or node is required to install Agent Notifications." >&2
        exit 1
    }

    repo=777genius/agent-notifications
    tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | decode_github_json '
import json, re, sys
value = json.load(sys.stdin).get("tag_name")
if not isinstance(value, str) or not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", value):
    sys.exit("Invalid stable release tag")
sys.stdout.buffer.write((value + "\n").encode("ascii"))
' '
const fs = require("fs");
let value;
try { value = JSON.parse(fs.readFileSync(0, "utf8")).tag_name; } catch (e) { process.exit(1); }
if (typeof value !== "string" || !/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(value)) {
  process.stderr.write("Invalid stable release tag\n");
  process.exit(1);
}
process.stdout.write(value + "\n");
')
    commit=$(curl -fsSL "https://api.github.com/repos/$repo/commits/$tag" | decode_github_json '
import json, re, sys
value = json.load(sys.stdin).get("sha")
if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
    sys.exit("Invalid release commit")
sys.stdout.buffer.write((value + "\n").encode("ascii"))
' '
const fs = require("fs");
let value;
try { value = JSON.parse(fs.readFileSync(0, "utf8")).sha; } catch (e) { process.exit(1); }
if (typeof value !== "string" || !/^[0-9a-f]{40}$/.test(value)) {
  process.stderr.write("Invalid release commit\n");
  process.exit(1);
}
process.stdout.write(value + "\n");
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
