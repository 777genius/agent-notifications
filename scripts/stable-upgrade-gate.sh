#!/bin/bash
# One release/recovery entry point; preparation freezes all release inputs before
# either isolated execution. No provider call and no client doubles.
set -euo pipefail
[ "$#" = 3 ] || { echo 'usage: stable-upgrade-gate.sh CANDIDATE PORTABLE_ZIP TEST_OUTPUT' >&2; exit 2; }
candidate=$1 package=$2 test_root=$3
scripts=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
mkdir -p "$test_root"
python3 -B "$scripts/stable-upgrade-e2e_test.py"
python3 -B "$scripts/stable-upgrade-e2e.py" prepare --output "$test_root/stable-latest"
# This actual published schema-4 writer remains covered when Latest advances.
python3 -B "$scripts/stable-upgrade-e2e.py" prepare --stable-tag v1.49.8 --output "$test_root/stable-schema4"
npm install --prefix "$test_root/native-clients" --no-audit --no-fund \
  @anthropic-ai/claude-code@2.1.311 @openai/codex@0.162.0
status=0
for fixture in latest schema4; do
    python3 -B "$scripts/stable-upgrade-e2e.py" run \
      --pin "$test_root/stable-$fixture/stable-pin.json" \
      --candidate-binary "$candidate" --candidate-package "$package" \
      --claude "$test_root/native-clients/node_modules/.bin/claude" \
      --codex "$test_root/native-clients/node_modules/.bin/codex" \
      --output "$test_root/TEST-$fixture" > "$test_root/$fixture.log" 2>&1 || status=1
    cat "$test_root/$fixture.log"
done
exit "$status"
