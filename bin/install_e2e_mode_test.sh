#!/bin/bash
# Regression: network diagnostics must not repeat offline/mock categories.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SANDBOX=$(mktemp -d)
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin"
cp "$SCRIPT_DIR/test-env.sh" "$SANDBOX/bin/"
# Source the real suite with only its final invocation delayed, using the same
# sourceable-script pattern as install_e2e_test.sh's installer adapter tests.
# Test bodies are boundary doubles: parsing, sandbox setup, preflight, category
# selection, ordering and summary/exit behavior still execute the real code.
sed '/^main "\$@"$/d' "$SCRIPT_DIR/install_e2e_test.sh" > "$SANDBOX/bin/suite.sh"
cat >> "$SANDBOX/bin/suite.sh" <<'DOUBLES'
[ -d "$HOME" ] && [ -d "$CODEX_HOME" ] && [ -d "$TMPDIR" ] || exit 91
printf 'setup\n' >> "$SCRIPT_DIR/calls"
while read -r _ _ name; do
    case "$name" in
        test_*) eval "$name() { printf '%s\\n' '$name' >> \"\$SCRIPT_DIR/calls\"; }" ;;
    esac
done < <(declare -F)
# Exercise the actual public-release preflight without making public requests.
curl() {
    printf 'preflight\n' >> "$SCRIPT_DIR/calls"
    case "$*" in
        *redirect_url*) printf 'https://github.com/777genius/agent-notifications/releases/tag/v0.0.0\n' ;;
        *http_code*) printf '404' ;;
        *) return 0 ;;
    esac
}
main "$@"
DOUBLES
run_mode() {
    local name=$1; shift
    : > "$SANDBOX/bin/calls"
    bash "$SANDBOX/bin/suite.sh" "$@" > "$SANDBOX/$name.log" 2>&1
    cp "$SANDBOX/bin/calls" "$SANDBOX/$name.calls"
}
run_mode default
run_mode full --real-network
run_mode network --real-network-only
run_mode mock --mock-only
python3 - "$SANDBOX" <<'PY'
import sys
from pathlib import Path
root = Path(sys.argv[1])
network = {'test_real_github_available', 'test_real_full_install', 'test_real_binary_runs',
           'test_real_utilities_installed', 'test_real_terminal_notifier_macos',
           'test_hook_wrapper_real_download', 'test_hook_wrapper_real_no_redownload',
           'test_hook_wrapper_real_binary_runs', 'test_hook_wrapper_real_concurrent_calls'}
for mode, categories in [('default', 'ABCDEF'), ('full', 'ABCDEF'), ('network', 'CF'), ('mock', 'BE')]:
    calls = (root / (mode + '.calls')).read_text().splitlines()
    tests = [line for line in calls if line.startswith('test_')]
    assert calls[0] == 'setup', (mode, 'sandbox setup missing')
    assert len(tests) == len(set(tests)), (mode, 'test called twice')
    log = (root / (mode + '.log')).read_text()
    for category in 'ABCDEF':
        assert ('Category ' + category + ':') in log if category in categories else ('Category ' + category + ':') not in log, (mode, category)
    if mode == 'network':
        assert set(tests) == network, tests
        assert calls.count('preflight') == 2, 'release preflight must run before C/F'
        assert calls.index('preflight') < calls.index('test_real_github_available')
    elif mode in ('default', 'full'):
        assert network.issubset(tests)
        for sentinel in ['test_platform_detection', 'test_mock_download_success',
                         'test_hook_wrapper_exists', 'test_hook_wrapper_mock_download']:
            assert tests.count(sentinel) == 1, (mode, sentinel)
        assert ('preflight' in calls) == (mode == 'full')
    else:
        assert set(tests).isdisjoint(network)
assert (root / 'default.calls').read_text().replace('preflight\n', '') == (root / 'full.calls').read_text().replace('preflight\n', '')
print('PASS: default/full selection unchanged; network-only C/F and setup/preflight; mock-only B/E')
PY
: > "$SANDBOX/bin/calls"
if bash "$SANDBOX/bin/suite.sh" --real-network-only --mock-only > "$SANDBOX/conflict.log" 2>&1; then
    echo 'FAIL: contradictory modes accepted' >&2; exit 1
else
    result=$?
    [ "$result" = 2 ] && [ ! -s "$SANDBOX/bin/calls" ] || exit 1
fi
echo 'PASS: contradictory modes rejected before suite setup'
# Execute the unmodified suite and real C/F bodies too, with only the public
# HTTP transport replaced. An unavailable release must retain its diagnostics
# and skips rather than falling back to offline/mock work or launching helpers.
mkdir -p "$SANDBOX/public-transport"
cat > "$SANDBOX/public-transport/curl" <<'CURL'
#!/bin/bash
log="$(dirname "$0")/requests"
printf '%s\n' "$*" >> "$log"
case "$*" in
    *redirect_url*) printf 'https://github.com/777genius/agent-notifications/releases/tag/v0.0.0\n' ;;
    *http_code*) printf '404' ;;
    *'https://github.com'*) exit 0 ;;
    *) echo 'unexpected transport request' >&2; exit 97 ;;
esac
CURL
chmod +x "$SANDBOX/public-transport/curl"
PATH="$SANDBOX/public-transport:$PATH" bash "$SCRIPT_DIR/install_e2e_test.sh" --real-network-only > "$SANDBOX/actual-network.log" 2>&1
python3 - "$SANDBOX" <<'PY'
import re
import sys
from pathlib import Path
root = Path(sys.argv[1])
log = re.sub(r'\x1b\[[0-9;]*m', '', (root / 'actual-network.log').read_text())
assert 'Category C:' in log and 'Category F:' in log
assert all('Category ' + category + ':' not in log for category in 'ABDE')
assert len(re.findall(r'▶ test_', log)) == 9, log
assert 'Passed:  1' in log and 'Skipped: 8' in log and 'Failed:  0' in log, log
requests = (root / 'public-transport/requests').read_text().splitlines()
assert 2 <= len(requests) <= 3, requests
assert any('redirect_url' in request for request in requests), requests
print('PASS: real C/F bodies preserve public preflight/unavailable-release skips without offline work')
PY
