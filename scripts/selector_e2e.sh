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
python3 -I scripts/preview-installer_test.py > "$evidence/preview-preparation.log" 2>&1 || {
    status=$?
    printf 'Preview qualification failed; details: %s\n' "$evidence/preview-preparation.log" >&2
    exit "$status"
}
source_commit=$(git rev-parse HEAD)
go build -trimpath -ldflags="-s -w -X main.selectorSourceCommit=$source_commit" -o "$binary" ./cmd/claude-notifications
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
    -run '^(TestBootstrap|TestSetupProductsScopedPresence)' ./cmd/claude-notifications > "$evidence/tests.jsonl" || {
    status=$?
    printf 'Bootstrap tests failed; details: %s\n' "$evidence/tests.jsonl" >&2
    python3 - "$evidence/tests.jsonl" <<'PY' >&2 || true
import json, pathlib, sys
events = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
failed = {event.get('Test', '') for event in events if event.get('Action') == 'fail'}
for event in events:
    if event.get('Action') == 'output' and event.get('Test', '') in failed:
        sys.stdout.write(event.get('Output', ''))
PY
    exit "$status"
}
python3 scripts/selector_e2e_check.py "$evidence/tests.jsonl"
