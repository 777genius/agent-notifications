#!/usr/bin/env bash
# Local, interactive macOS OpenCode TEST preview. See --help.
set -euo pipefail
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
exec python3 -I "$script_dir/preview-installer.py" "$@"
