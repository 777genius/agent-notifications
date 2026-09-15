#!/bin/sh
# codex-hook-wrapper.sh - dedicated Codex launcher.
#
# The configured command string in hooks/hooks-codex.json is part of the
# frozen Codex trust identity; this file's CONTENTS are not hashed, so the
# launcher behavior may evolve between releases while the path stays stable.
#
# Responsibilities:
#   - mark the invocation as the Codex route (CN_PRODUCT=codex) so the shared
#     wrapper skips Claude-specific side effects (pointer file writes);
#   - resolve the plugin root from the native Codex PLUGIN_ROOT export,
#     falling back to the script location;
#   - carry the minimum Codex-capable binary version for the shared wrapper's
#     old-binary guard (pre-Codex binaries silently ignore --product and
#     would misroute the payload into the Claude decoder).
#
# RELIABILITY: observation hooks must never block Codex; every failure path
# exits 0 with empty output.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)" || exit 0

CN_PRODUCT=codex
export CN_PRODUCT

# First release with Codex support. Keep in sync with the release that ships
# this file (see docs/RELEASE.md).
CN_CODEX_MIN_VERSION="1.42.0"
export CN_CODEX_MIN_VERSION

if [ -z "${PLUGIN_ROOT:-}" ]; then
    PLUGIN_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
fi
export PLUGIN_ROOT

# Invoke through sh explicitly: marketplace installs copy the bundle into a
# cache and executable-bit preservation is not guaranteed there.
exec sh "$SCRIPT_DIR/hook-wrapper.sh" "$@"
