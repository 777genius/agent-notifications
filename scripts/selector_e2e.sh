#!/usr/bin/env bash
# Qualify the current native helper using only disposable bootstrap fixtures.
set -euo pipefail

if [[ $# != 1 || "$1" != /* ]]; then
    echo 'Usage: selector_e2e.sh /absolute/TEST-evidence-directory' >&2
    exit 2
fi
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
evidence=$1
mkdir -p -- "$evidence"
binary="$evidence/claude-notifications"
cd -- "$repo"
go build -trimpath -ldflags='-s -w' -o "$binary" ./cmd/claude-notifications
python3 - "$binary" "$evidence/binary.json" <<'PY'
import hashlib, json, pathlib, sys
binary = pathlib.Path(sys.argv[1])
size = binary.stat().st_size
if size > 32 * 1024 * 1024:
    raise SystemExit('selector helper exceeds the 32 MiB release bound')
pathlib.Path(sys.argv[2]).write_text(json.dumps({
    'path': str(binary), 'bytes': size,
    'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
}, indent=2) + '\n')
PY
SELECTOR_TEST_BINARY="$binary" go test -json -count=1 -timeout=10m \
    -run '^(TestBootstrap|TestSetupProductsScopedPresence)' ./cmd/claude-notifications > "$evidence/tests.jsonl"
python3 scripts/selector_e2e_check.py "$evidence/tests.jsonl"
