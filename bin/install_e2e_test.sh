#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
# Native Windows fixtures build the real Go executable. Preserve only Go's
# pre-populated module cache across env -i so those builds remain offline.
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) TEST_ENV_HANDOFF_GOMODCACHE=1 ;;
esac
test_env_enter "$0" "$@"
# install_e2e_test.sh - End-to-end tests for install.sh
#
# Usage:
#   bash bin/install_e2e_test.sh                # Run offline tests only
#   bash bin/install_e2e_test.sh --real-network # Include real network tests
#   bash bin/install_e2e_test.sh --verbose      # Verbose output
#   bash bin/install_e2e_test.sh --mock-only    # Only mock server tests

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
INSTALL_SCRIPT="$SCRIPT_DIR/install.sh"
MOCK_SERVER="$SCRIPT_DIR/mock_server.py"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_SKIPPED=0

# Flags
RUN_REAL_NETWORK=false
RUN_MOCK_ONLY=false
VERBOSE=false
ALLOW_WINDOWS_REAL_NETWORK_TESTS="${ALLOW_WINDOWS_REAL_NETWORK_TESTS:-false}"

# Mock server state
MOCK_PID=""
MOCK_PORT=18888

# Parse command line arguments
for arg in "$@"; do
    case $arg in
        --real-network) RUN_REAL_NETWORK=true ;;
        --mock-only) RUN_MOCK_ONLY=true ;;
        --verbose|-v) VERBOSE=true ;;
        --help|-h)
            echo "Usage: $0 [options]"
            echo "Options:"
            echo "  --real-network  Include tests that make real network requests"
            echo "  --mock-only     Only run mock server tests"
            echo "  --verbose, -v   Verbose output"
            exit 0
            ;;
    esac
done

# All generated fixtures and user state belong to this disposable suite run.
SUITE_DIR=$(mktemp -d)
FIXTURES_DIR="$SUITE_DIR/fixtures"
test_env_setup "$SUITE_DIR"

# Offline tests must never inherit public release endpoints or user proxies.
if [ "$RUN_REAL_NETWORK" != true ]; then
    export SKIP_CONNECTIVITY_CHECK=true
    export RELEASE_URL="http://127.0.0.1:1"
    export CHECKSUMS_URL="$RELEASE_URL/checksums.txt"
    export RELEASES_BASE_URL="$RELEASE_URL"
    export LATEST_RELEASE_API_URL="$RELEASE_URL/api/latest"
    export MODERN_NOTIFIER_URL="$RELEASE_URL/ClaudeNotifier.app.zip"
    export NOTIFIER_URL="$RELEASE_URL/valid.zip"
    export http_proxy="http://127.0.0.1:1" https_proxy="http://127.0.0.1:1" ALL_PROXY="http://127.0.0.1:1"
    export HTTP_PROXY="$http_proxy" HTTPS_PROXY="$https_proxy" all_proxy="$ALL_PROXY"
    export NO_PROXY="localhost,127.0.0.1" no_proxy="localhost,127.0.0.1"
    export PIP_NO_INDEX=1
fi

#=============================================================================
# Test Utilities
#=============================================================================

setup_test_dir() {
    TEST_DIR=$(mktemp -d)
    if [ "$VERBOSE" = true ]; then
        echo "  Test dir: $TEST_DIR"
    fi
}

cleanup_test_dir() {
    if [ -n "${TEST_DIR:-}" ] && [ -d "$TEST_DIR" ]; then
        rm -rf "$TEST_DIR"
    fi
}

write_installed_plugin_registry() {
    local output_path="$1"
    local version="$2"
    local install_path="$3"

    cat > "$output_path" <<EOF
{
  "plugins": {
    "claude-notifications-go@claude-notifications-go": [
      {"version": "$version", "installPath": "$install_path"}
    ]
  }
}
EOF
}

run_bootstrap_version_verification() {
    local claude_home="$1"
    local sourceable_bootstrap="$2"
    local expected_version="$3"

    CLAUDE_CONFIG_DIR="$claude_home" bash -c \
        'source "$1"; verify_installed_plugin_version "$2"' \
        _ "$sourceable_bootstrap" "$expected_version"
}

run_bootstrap_install_plugin() {
    local claude_home="$1"
    local sourceable_bootstrap="$2"
    local command_log="$3"

    CLAUDE_CONFIG_DIR="$claude_home" COMMAND_LOG="$command_log" bash -c '
        source "$1"
        claude() {
            printf "%s\n" "$*" >> "$COMMAND_LOG"
        }
        # Registry-only adapter test; protected preflight has dedicated local HTTP fixtures.
        config_preflight() { :; }
        install_plugin
    ' _ "$sourceable_bootstrap"
}

# Get normalized platform name (matching install.sh)
get_platform() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$os" in
        darwin) echo "darwin" ;;
        linux) echo "linux" ;;
        mingw*|msys*|cygwin*) echo "windows" ;;
        *) echo "unknown" ;;
    esac
}

# Get normalized architecture (matching install.sh)
get_arch() {
    local arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "unknown" ;;
    esac
}

# Get binary name for current platform
get_binary_name() {
    local platform=$(get_platform)
    local arch=$(get_arch)
    if [ "$platform" = "windows" ]; then
        echo "claude-notifications-${platform}-${arch}.exe"
    else
        echo "claude-notifications-${platform}-${arch}"
    fi
}

# Check if on Windows
is_windows() {
    [ "$(get_platform)" = "windows" ]
}

real_network_tests_supported() {
    if [ "$RUN_REAL_NETWORK" != true ]; then
        return 1
    fi

    # Hosted Windows runners are significantly flakier for real GitHub/CDN downloads:
    # the step can hang or lose runner connectivity even when the install logic is fine.
    # Keep the deterministic coverage on Windows via offline + mock tests, and allow
    # opt-in real-network runs when someone explicitly wants to exercise them.
    if is_windows && [ -n "${CI:-}" ] && [ "$ALLOW_WINDOWS_REAL_NETWORK_TESTS" != "true" ]; then
        return 1
    fi

    return 0
}

# Cross-platform timeout command
# macOS doesn't have timeout by default, use gtimeout if available or run without timeout
run_with_timeout() {
    local seconds="$1"
    shift
    if command -v timeout &>/dev/null; then
        timeout "$seconds" "$@"
    elif command -v gtimeout &>/dev/null; then
        gtimeout "$seconds" "$@"
    else
        # No timeout available, run without it
        "$@"
    fi
}

# Use the same executable payload and checksum as the main Windows fixture.
prepare_focus_fixture() {
    local destination="$1" checksum="$2"
    if is_windows; then
        local focus_name="claude-notifications-windows-$(get_arch)-focus.exe"
        cp "$FIXTURES_DIR/mock_binary" "$destination/$focus_name"
        echo "$checksum  $focus_name" >> "$destination/checksums.txt"
    fi
}

assert_desktop_runtime() {
    local destination="$1"
    case "$(get_platform)" in
        windows) assert_executable "$destination/claude-notifications-windows-$(get_arch)-focus.exe" "Windows focus handler installed" ;;
        darwin) assert_executable "$destination/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern" "Modern notifier executable installed" ;;
    esac
}

# Start mock server
start_mock_server() {
    local port="${1:-$MOCK_PORT}"

    if ! command -v python3 &>/dev/null; then
        echo "python3 not available, skipping mock tests"
        return 1
    fi

    # Create fixtures directory
    mkdir -p "$FIXTURES_DIR"

    # Create mock binary - a real executable shell script padded to 2MB
    # This mimics the real binary for verify_executable checks
    if [ ! -f "$FIXTURES_DIR/mock_binary" ]; then
        cat > "$FIXTURES_DIR/mock_binary" << 'MOCK_EOF'
#!/bin/bash
# Mock claude-notifications binary for testing
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
    echo "claude-notifications version 1.0.0-mock (test binary)"
    exit 0
fi
if [ "$1" = "help" ] || [ "$1" = "--help" ]; then
    echo "claude-notifications mock binary"
    exit 0
fi
echo "Mock binary executed with args: $@"
exit 0
MOCK_EOF
        chmod +x "$FIXTURES_DIR/mock_binary"
        # Pad to 2MB to pass size check (append nulls)
        dd if=/dev/zero bs=1024 count=2000 >> "$FIXTURES_DIR/mock_binary" 2>/dev/null
    fi

    # Create valid checksums.txt
    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/mock_binary" | awk '{print $1}')
    elif command -v sha256sum &>/dev/null; then
        checksum=$(sha256sum "$FIXTURES_DIR/mock_binary" | awk '{print $1}')
    else
        checksum="dummy_checksum"
    fi
    echo "$checksum  mock_binary" > "$FIXTURES_DIR/checksums.txt"

    # Build both real app layouts; the installer checks the executable inside.
    python3 - "$FIXTURES_DIR" <<'ZIP_EOF'
import os
import sys
import zipfile
for archive, app, binary in (
    ("valid.zip", "terminal-notifier.app", "terminal-notifier"),
    ("ClaudeNotifier.app.zip", "ClaudeNotifier.app", "terminal-notifier-modern"),
):
    with zipfile.ZipFile(os.path.join(sys.argv[1], archive), "w") as bundle:
        entry = zipfile.ZipInfo(f"{app}/Contents/MacOS/{binary}")
        entry.create_system = 3
        entry.external_attr = 0o100755 << 16
        bundle.writestr(entry, "#!/bin/sh\nexit 0\n")
ZIP_EOF

    SAVED_MODERN_NOTIFIER_URL="${MODERN_NOTIFIER_URL-}"
    SAVED_NOTIFIER_URL="${NOTIFIER_URL-}"
    export MODERN_NOTIFIER_URL="http://localhost:$port/ClaudeNotifier.app.zip"
    export NOTIFIER_URL="http://localhost:$port/valid.zip"

    # Start server
    python3 "$MOCK_SERVER" "$port" "$FIXTURES_DIR" &
    MOCK_PID=$!

    # Wait for server to be ready (up to 5 seconds)
    local retries=0
    local max_retries=50
    while [ $retries -lt $max_retries ]; do
        if ! kill -0 $MOCK_PID 2>/dev/null; then
            echo "Failed to start mock server (process exited)"
            return 1
        fi
        if curl -s -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null; then
            break
        fi
        sleep 0.1
        retries=$((retries + 1))
    done

    if [ $retries -ge $max_retries ]; then
        echo "Mock server failed to respond after 5 seconds"
        kill $MOCK_PID 2>/dev/null || true
        MOCK_PID=""
        return 1
    fi

    if [ "$VERBOSE" = true ]; then
        echo "  Mock server started on port $port (PID: $MOCK_PID, ready after $((retries * 100))ms)"
    fi
}

stop_mock_server() {
    if [ -n "${MOCK_PID:-}" ]; then
        kill $MOCK_PID 2>/dev/null || true
        wait $MOCK_PID 2>/dev/null || true
        MOCK_PID=""
        if [ -n "${SAVED_MODERN_NOTIFIER_URL:-}" ]; then
            export MODERN_NOTIFIER_URL="$SAVED_MODERN_NOTIFIER_URL"
        else
            unset MODERN_NOTIFIER_URL
        fi
        if [ -n "${SAVED_NOTIFIER_URL:-}" ]; then
            export NOTIFIER_URL="$SAVED_NOTIFIER_URL"
        else
            unset NOTIFIER_URL
        fi
    fi
}

# Cleanup on exit
cleanup() {
    stop_mock_server
    cleanup_test_dir
    rm -rf "$SUITE_DIR"
}
trap cleanup EXIT INT TERM

#=============================================================================
# Assertions
#=============================================================================

assert_eq() {
    local expected="$1" actual="$2" msg="$3"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ "$expected" = "$actual" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg"
        echo -e "    Expected: '$expected'"
        echo -e "    Actual:   '$actual'"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_contains() {
    local haystack="$1" needle="$2" msg="$3"
    TESTS_RUN=$((TESTS_RUN + 1))
    if echo "$haystack" | grep -qE "$needle"; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (pattern not found: '$needle')"
        if [ "$VERBOSE" = true ]; then
            echo "    Output: ${haystack:0:200}..."
        fi
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_not_contains() {
    local haystack="$1" needle="$2" msg="$3"
    TESTS_RUN=$((TESTS_RUN + 1))
    if ! echo "$haystack" | grep -qE "$needle"; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (pattern found but shouldn't be: '$needle')"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_file_exists() {
    local file="$1" msg="$2"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ -f "$file" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (file not found: $file)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_file_not_exists() {
    local file="$1" msg="$2"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ ! -f "$file" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (file exists but shouldn't: $file)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_dir_exists() {
    local dir="$1" msg="$2"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ -d "$dir" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (directory not found: $dir)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_dir_not_exists() {
    local dir="$1" msg="$2"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ ! -d "$dir" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (directory exists but shouldn't: $dir)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_executable() {
    local file="$1" msg="$2"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ -x "$file" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (not executable: $file)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

assert_exit_code() {
    local expected="$1" actual="$2" msg="$3"
    TESTS_RUN=$((TESTS_RUN + 1))
    if [ "$expected" -eq "$actual" ]; then
        echo -e "  ${GREEN}✓${NC} $msg"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        return 0
    else
        echo -e "  ${RED}✗${NC} $msg (exit code: $actual, expected: $expected)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi
}

skip_test() {
    local msg="$1" reason="$2"
    echo -e "  ${YELLOW}⊘${NC} SKIP: $msg ($reason)"
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
}

pass_test() {
    local msg="$1"
    TESTS_RUN=$((TESTS_RUN + 1))
    echo -e "  ${GREEN}✓${NC} $msg"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail_test() {
    local msg="$1" detail="$2"
    TESTS_RUN=$((TESTS_RUN + 1))
    echo -e "  ${RED}✗${NC} $msg ($detail)"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

#=============================================================================
# Category A: Offline Tests (no network required)
#=============================================================================

test_platform_detection() {
    echo -e "\n${CYAN}▶ test_platform_detection${NC}"

    local expected_platform=$(get_platform)
    local expected_arch=$(get_arch)

    setup_test_dir

    output=$(INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 5 bash "$INSTALL_SCRIPT" 2>&1 || true)

    assert_contains "$output" "Platform:.*$expected_platform" "Platform detected correctly"
    assert_contains "$output" "$expected_arch" "Architecture detected correctly"

    cleanup_test_dir
}

test_binary_name_format() {
    echo -e "\n${CYAN}▶ test_binary_name_format${NC}"
    setup_test_dir

    output=$(INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 5 bash "$INSTALL_SCRIPT" 2>&1 || true)

    # Binary name should contain platform and arch
    assert_contains "$output" "Binary:.*claude-notifications-" "Binary name has correct prefix"

    cleanup_test_dir
}

test_wsl_guard_blocks_linux_install() {
    echo -e "\n${CYAN}▶ test_wsl_guard_blocks_linux_install${NC}"
    setup_test_dir

    local fake_path="$TEST_DIR/fakebin"
    mkdir -p "$fake_path"

    cat > "$fake_path/uname" <<'UNAME_EOF'
#!/bin/sh
case "$1" in
  -s) echo "Linux" ;;
  -m) echo "x86_64" ;;
  *) echo "Linux" ;;
esac
UNAME_EOF
    chmod +x "$fake_path/uname"

    local output exit_code
    output=$(INSTALL_TARGET_DIR="$TEST_DIR" PATH="$fake_path:$PATH" WSL_DISTRO_NAME=Ubuntu bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?

    assert_exit_code 1 $exit_code "WSL install exits before Linux setup"
    assert_contains "$output" "WSL environment detected" "WSL guard message shown"
    assert_contains "$output" "PowerShell or Windows Terminal" "WSL guard explains PowerShell bash routing"
    assert_contains "$output" "Git Bash" "Windows Git Bash guidance shown"
    assert_not_contains "$output" "Platform:" "WSL guard stops before platform output"
    assert_dir_not_exists "$TEST_DIR/.install.lock" "WSL guard stops before acquiring install lock"

    cleanup_test_dir
}

test_bootstrap_version_verification_uses_installed_manifest_for_unknown_registry() {
    echo -e "\n${CYAN}▶ test_bootstrap_version_verification_uses_installed_manifest_for_unknown_registry${NC}"
    setup_test_dir

    local claude_home="$TEST_DIR/claude"
    local plugin_root="$claude_home/plugins/cache/claude-notifications-go/claude-notifications-go/1.40.1"
    local sourceable_bootstrap="$TEST_DIR/bootstrap-functions.sh"
    mkdir -p "$claude_home/plugins" "$plugin_root/.claude-plugin"

    # Load the production helpers without executing bootstrap's main function.
    sed '/^main "\$@"$/d' "$SCRIPT_DIR/bootstrap.sh" > "$sourceable_bootstrap"

    write_installed_plugin_registry "$claude_home/plugins/installed_plugins.json" "unknown" "$plugin_root"
    cat > "$plugin_root/.claude-plugin/plugin.json" <<'EOF'
{"name":"claude-notifications-go","version":"1.40.1"}
EOF

    local exit_code
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 0 "$exit_code" "Bootstrap accepts the installed manifest when the registry version is unknown"

    write_installed_plugin_registry "$claude_home/plugins/installed_plugins.json" "" "$plugin_root"
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 0 "$exit_code" "Bootstrap accepts the installed manifest when the registry version is empty"

    write_installed_plugin_registry "$claude_home/plugins/installed_plugins.json" "vunknown" "$plugin_root"
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 0 "$exit_code" "Bootstrap accepts the installed manifest when the registry version is vunknown"

    cat > "$claude_home/plugins/installed_plugins.json" <<EOF
{
  "plugins": {
    "claude-notifications-go@claude-notifications-go": [
      {"version": "1.39.0", "installPath": "$claude_home/plugins/cache/claude-notifications-go/claude-notifications-go/1.39.0"},
      {"version": "unknown", "installPath": "$plugin_root"}
    ]
  }
}
EOF
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 0 "$exit_code" "Bootstrap verifies the path paired with a current unknown entry before older numeric versions"

    write_installed_plugin_registry "$claude_home/plugins/installed_plugins.json" "1.39.0" "$plugin_root"
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 1 "$exit_code" "Bootstrap preserves explicit registry version mismatch failures"

    write_installed_plugin_registry "$claude_home/plugins/installed_plugins.json" "unknown" "$plugin_root"
    cat > "$plugin_root/.claude-plugin/plugin.json" <<'EOF'
{"name":"claude-notifications-go","version":"1.39.0"}
EOF
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 1 "$exit_code" "Bootstrap rejects an unknown registry version when the installed manifest is stale"

    rm -f "$plugin_root/.claude-plugin/plugin.json"
    run_bootstrap_version_verification "$claude_home" "$sourceable_bootstrap" "1.40.1"
    exit_code=$?
    assert_exit_code 1 "$exit_code" "Bootstrap rejects an unknown registry version without an installed manifest"

    cleanup_test_dir
}

test_bootstrap_empty_registry_version_preserves_installed_manifest() {
    echo -e "\n${CYAN}▶ test_bootstrap_empty_registry_version_preserves_installed_manifest${NC}"
    setup_test_dir

    local claude_home="$TEST_DIR/claude"
    local plugin_root="$claude_home/plugins/cache/claude-notifications-go/claude-notifications-go/1.40.1"
    local marketplace_manifest="$claude_home/plugins/marketplaces/claude-notifications-go/.claude-plugin/plugin.json"
    local sourceable_bootstrap="$TEST_DIR/bootstrap-functions.sh"
    local command_log="$TEST_DIR/claude-commands.log"
    mkdir -p "$claude_home/plugins" "$plugin_root/.claude-plugin" "$(dirname "$marketplace_manifest")"

    sed '/^main "\$@"$/d' "$SCRIPT_DIR/bootstrap.sh" > "$sourceable_bootstrap"
    write_installed_plugin_registry "$claude_home/plugins/installed_plugins.json" "" "$plugin_root"
    cat > "$plugin_root/.claude-plugin/plugin.json" <<'EOF'
{"name":"claude-notifications-go","version":"1.40.1"}
EOF
    cat > "$marketplace_manifest" <<'EOF'
{"name":"claude-notifications-go","version":"1.40.1"}
EOF

    local exit_code
    run_bootstrap_install_plugin "$claude_home" "$sourceable_bootstrap" "$command_log"
    exit_code=$?
    assert_exit_code 0 "$exit_code" "Bootstrap keeps a path-bearing empty-version installation"
    assert_file_exists "$plugin_root/.claude-plugin/plugin.json" "Bootstrap does not clear the manifest before verification"
    assert_contains "$(cat "$command_log")" "plugin update" "Bootstrap updates a path-bearing empty-version installation"
    assert_not_contains "$(cat "$command_log")" "plugin install" "Bootstrap avoids an unnecessary reinstall for an empty registry version"

    cleanup_test_dir
}

test_lock_created() {
    echo -e "\n${CYAN}▶ test_lock_created${NC}"

    # Skip on Windows - trap/lock behavior differs in Git Bash
    if is_windows; then
        skip_test "Lock created" "trap behavior differs on Windows"
        return
    fi

    setup_test_dir

    local fake_bin="$TEST_DIR/fake-bin"
    mkdir -p "$fake_bin"
    cat > "$fake_bin/curl" <<'FAKE_CURL_EOF'
#!/bin/sh
if [ "$1" = "--help" ]; then
    exit 0
fi
sleep 5
exit 28
FAKE_CURL_EOF
    chmod +x "$fake_bin/curl"

    PATH="$fake_bin:$PATH" RELEASE_URL="http://127.0.0.1:1" CHECKSUMS_URL="http://127.0.0.1:1/checksums.txt" \
        INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 10 bash "$INSTALL_SCRIPT" >/dev/null 2>&1 &
    local pid=$!

    local i=0
    while [ $i -lt 20 ] && [ ! -d "$TEST_DIR/.install.lock" ]; do
        sleep 0.1
        i=$((i + 1))
    done

    assert_dir_exists "$TEST_DIR/.install.lock" "Lock created while install is running"

    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true
    rm -rf "$TEST_DIR/.install.lock"

    cleanup_test_dir
}

test_lock_prevents_parallel() {
    echo -e "\n${CYAN}▶ test_lock_prevents_parallel${NC}"
    setup_test_dir

    # Create lock manually to simulate another install running
    mkdir -p "$TEST_DIR/.install.lock"

    set +e
    output=$(INSTALL_TARGET_DIR="$TEST_DIR" bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Another installation" "Lock prevents parallel install"
    assert_exit_code 1 $exit_code "Exit code is 1 when locked"

    cleanup_test_dir
}

test_lock_stale_removal() {
    echo -e "\n${CYAN}▶ test_lock_stale_removal${NC}"

    # This test is difficult to do reliably without modifying install.sh
    # The stale lock check uses 600 seconds (10 minutes)
    # We'll skip this test in normal runs
    skip_test "Stale lock removal" "requires 10+ minute old lock"
}

test_lock_cleanup_on_exit() {
    echo -e "\n${CYAN}▶ test_lock_cleanup_on_exit${NC}"

    # Skip on Windows - trap behavior differs in Git Bash
    if is_windows; then
        skip_test "Lock cleanup on exit" "trap behavior differs on Windows"
        return
    fi

    setup_test_dir

    # Run install with unreachable URL to fail fast
    RELEASE_URL="http://127.0.0.1:1" CHECKSUMS_URL="http://127.0.0.1:1/checksums.txt" \
        INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 10 bash "$INSTALL_SCRIPT" 2>&1 || true

    # Lock should be cleaned up by trap
    assert_dir_not_exists "$TEST_DIR/.install.lock" "Lock cleaned up after exit"

    cleanup_test_dir
}

test_no_write_permission() {
    echo -e "\n${CYAN}▶ test_no_write_permission${NC}"

    # Skip on Windows - chmod doesn't work the same way
    if is_windows; then
        skip_test "No write permission" "chmod not supported on Windows"
        return
    fi

    setup_test_dir

    mkdir -p "$TEST_DIR/readonly"
    chmod 555 "$TEST_DIR/readonly"

    set +e
    output=$(INSTALL_TARGET_DIR="$TEST_DIR/readonly" bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "No write permission" "Permission error detected"
    assert_exit_code 1 $exit_code "Exit code is 1 when no write permission"

    chmod 755 "$TEST_DIR/readonly"
    cleanup_test_dir
}

test_install_target_dir() {
    echo -e "\n${CYAN}▶ test_install_target_dir${NC}"
    setup_test_dir

    custom_dir="$TEST_DIR/custom/install/path"
    mkdir -p "$custom_dir"

    # Use unreachable URL to fail fast and capture output
    output=$(RELEASE_URL="http://127.0.0.1:1" CHECKSUMS_URL="http://127.0.0.1:1/checksums.txt" \
             INSTALL_TARGET_DIR="$custom_dir" run_with_timeout 10 bash "$INSTALL_SCRIPT" 2>&1 || true)

    # The script should try to install to the custom directory
    # We verify by checking if it tried to access that directory
    assert_contains "$output" "Binary Setup|Platform:" "Script started with custom dir"

    cleanup_test_dir
}

test_directory_auto_created() {
    echo -e "\n${CYAN}▶ test_directory_auto_created${NC}"
    setup_test_dir

    nonexistent="$TEST_DIR/does/not/exist"

    # Use unreachable URL to fail fast
    output=$(RELEASE_URL="http://127.0.0.1:1" CHECKSUMS_URL="http://127.0.0.1:1/checksums.txt" \
             INSTALL_TARGET_DIR="$nonexistent" run_with_timeout 10 bash "$INSTALL_SCRIPT" 2>&1 || true)

    # Directory should be created automatically
    assert_dir_exists "$nonexistent" "Directory auto-created"

    cleanup_test_dir
}

test_transport_error_diagnostics() {
    echo -e "\n${CYAN}▶ test_transport_error_diagnostics${NC}"

    if ! command -v curl &>/dev/null; then
        skip_test "Transport error diagnostics" "curl not available"
        return
    fi

    setup_test_dir

    set +e
    output=$(RELEASE_URL="http://127.0.0.1:1" \
             CHECKSUMS_URL="http://127.0.0.1:1/checksums.txt" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             SKIP_CONNECTIVITY_CHECK=true \
             run_with_timeout 15 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Download failed before an HTTP response was received|No HTTP response received from the release server" "Transport failure summary shown"
    assert_contains "$output" "Retrying once with compatibility mode|Connection to the release host failed|TLS/certificate validation failed|Windows/Git Bash downloads can fail behind corporate proxies" "Transport guidance shown"
    assert_exit_code 1 $exit_code "Exit code is 1 on transport failure"

    cleanup_test_dir
}

test_required_tools_curl_wget() {
    echo -e "\n${CYAN}▶ test_required_tools_curl_wget${NC}"

    # This test would require temporarily hiding curl/wget which is risky
    # We'll verify the check exists by looking at the script output
    setup_test_dir

    # If we have curl or wget, the script should proceed past the check
    output=$(INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 5 bash "$INSTALL_SCRIPT" 2>&1 || true)

    # Should not contain "Missing required tools: curl or wget"
    assert_not_contains "$output" "Missing required tools.*curl" "curl/wget available"

    cleanup_test_dir
}

test_force_preserves_binaries() {
    echo -e "\n${CYAN}▶ test_force_preserves_binaries${NC}"
    setup_test_dir

    # Create fake binary files
    local binary_name=$(get_binary_name)
    touch "$TEST_DIR/$binary_name"

    # Run with --force and use unreachable URL so it fails fast after cleanup
    # SKIP_CONNECTIVITY_CHECK bypasses the curl to github.com (flaky on CI)
    output=$(RELEASE_URL="http://127.0.0.1:1" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             SKIP_CONNECTIVITY_CHECK=true \
             run_with_timeout 5 bash "$INSTALL_SCRIPT" --force 2>&1 || true)

    # Old files must survive a failed download
    assert_contains "$output" "preserving live files" "Force preservation message shown"
    assert_file_exists "$TEST_DIR/$binary_name" "Old binary preserved"

    cleanup_test_dir
}

test_force_preserves_symlinks() {
    echo -e "\n${CYAN}▶ test_force_preserves_symlinks${NC}"
    setup_test_dir

    # Git Bash can copy ln -s targets instead of creating a symlink. Exercise
    # the actual launcher form the installer uses on each platform.
    if is_windows; then
        printf '@echo off\r\nREM existing launcher\r\n' > "$TEST_DIR/claude-notifications.bat"
        cp "$TEST_DIR/claude-notifications.bat" "$TEST_DIR/expected-launcher"
    else
        touch "$TEST_DIR/target_binary"
        ln -s target_binary "$TEST_DIR/claude-notifications" || return 1
    fi

    # Run with --force and unreachable URL
    # SKIP_CONNECTIVITY_CHECK bypasses the curl to github.com (flaky on CI)
    RELEASE_URL="http://127.0.0.1:1" \
    INSTALL_TARGET_DIR="$TEST_DIR" \
    SKIP_CONNECTIVITY_CHECK=true \
    run_with_timeout 5 bash "$INSTALL_SCRIPT" --force 2>&1 || true

    # Launcher bytes or symlink target must survive a failed download.
    local preserved=false
    if is_windows; then
        cmp -s "$TEST_DIR/expected-launcher" "$TEST_DIR/claude-notifications.bat" && preserved=true
    elif [ -L "$TEST_DIR/claude-notifications" ] && [ "$(readlink "$TEST_DIR/claude-notifications")" = target_binary ]; then
        preserved=true
    fi
    if [ "$preserved" != true ]; then
        echo -e "  ${RED}✗${NC} Launcher lost or changed"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    else
        echo -e "  ${GREEN}✓${NC} Launcher preserved by --force"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    fi
    TESTS_RUN=$((TESTS_RUN + 1))

    cleanup_test_dir
}

test_windows_native_hooks_configured_existing_binary() {
    echo -e "\n${CYAN}▶ test_windows_native_hooks_configured_existing_binary${NC}"
    setup_test_dir

    local plugin_root="$TEST_DIR/plugin"
    local bin_dir="$plugin_root/bin"
    local hooks_dir="$plugin_root/hooks"
    local fake_path="$TEST_DIR/fakebin"

    mkdir -p "$bin_dir" "$hooks_dir" "$fake_path"
    printf '{"hooks":{}}\n' > "$hooks_dir/hooks.json"

    cat > "$fake_path/uname" <<'UNAME_EOF'
#!/bin/sh
case "$1" in
  -s) echo "MINGW64_NT-10.0" ;;
  -m) echo "x86_64" ;;
  *) echo "MINGW64_NT-10.0" ;;
esac
UNAME_EOF
    chmod +x "$fake_path/uname"

    cat > "$bin_dir/claude-notifications-windows-amd64.exe" <<'FAKE_EXE_EOF'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
    echo "claude-notifications v1.38.0"
    exit 0
fi
if [ "$1" = "windows-hooks" ]; then
    exe=""
    while [ "$#" -gt 0 ]; do
        if [ "$1" = "--exe" ]; then
            shift
            exe="$1"
        fi
        shift
    done
    printf '{\n  "hooks": {\n    "Stop": [\n      {\n        "hooks": [\n          {\n            "type": "command",\n            "command": "%s",\n            "args": ["handle-hook", "Stop"],\n            "timeout": 30\n          }\n        ]\n      }\n    ]\n  }\n}\n' "$exe"
    exit 0
fi
exit 0
FAKE_EXE_EOF
    chmod +x "$bin_dir/claude-notifications-windows-amd64.exe"
    # Complete desktop runtime so hook tests reach their intended behavior.
    printf '#!/bin/sh\nexit 0\n' > "$bin_dir/claude-notifications-windows-amd64-focus.exe"
    chmod +x "$bin_dir/claude-notifications-windows-amd64-focus.exe"

    touch "$bin_dir/sound-preview-windows-amd64.exe"
    touch "$bin_dir/list-devices-windows-amd64.exe"
    touch "$bin_dir/list-sounds-windows-amd64.exe"

    local output exit_code
    output=$(INSTALL_TARGET_DIR="$bin_dir" PATH="$fake_path:$PATH" bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?

    assert_exit_code 0 $exit_code "Installer succeeds with existing Windows binary"
    assert_executable "$bin_dir/claude-notifications-windows-amd64-focus.exe" "Existing focus handler preserved"
    assert_contains "$output" "Windows exec-form hooks configured" "Windows hooks configuration message shown"

    local hooks_json
    hooks_json=$(cat "$hooks_dir/hooks.json")
    assert_contains "$hooks_json" '"args"[[:space:]]*:[[:space:]]*\[' "hooks.json uses exec-form args"
    assert_not_contains "$hooks_json" '"shell"[[:space:]]*:' "hooks.json does not force a shell"
    assert_not_contains "$hooks_json" '\$input' "hooks.json does not pipe stdin through a shell"
    assert_not_contains "$hooks_json" '"command"[[:space:]]*:[^\n]*\|' "hooks.json commands do not contain shell pipes"
    assert_not_contains "$hooks_json" 'hook-wrapper' "hooks.json bypasses shell wrapper on Windows"
    assert_not_contains "$hooks_json" '\.bat|\.cmd' "hooks.json does not use Windows shell shims"
    assert_contains "$hooks_json" '"handle-hook"' "hooks.json calls hook handler"
    assert_contains "$hooks_json" '"Stop"' "hooks.json passes Stop event"
    assert_contains "$hooks_json" 'claude-notifications-windows-amd64\.exe' "hooks.json points at Windows exe"

    cleanup_test_dir
}

test_windows_old_binary_forces_update_before_hooks() {
    echo -e "\n${CYAN}▶ test_windows_old_binary_forces_update_before_hooks${NC}"
    setup_test_dir

    local plugin_root="$TEST_DIR/plugin"
    local bin_dir="$plugin_root/bin"
    local hooks_dir="$plugin_root/hooks"
    local fake_path="$TEST_DIR/fakebin"

    mkdir -p "$bin_dir" "$hooks_dir" "$fake_path"
    printf '{"hooks":{}}\n' > "$hooks_dir/hooks.json"

    cat > "$fake_path/uname" <<'UNAME_EOF'
#!/bin/sh
case "$1" in
  -s) echo "MINGW64_NT-10.0" ;;
  -m) echo "x86_64" ;;
  *) echo "MINGW64_NT-10.0" ;;
esac
UNAME_EOF
    chmod +x "$fake_path/uname"

    cat > "$bin_dir/claude-notifications-windows-amd64.exe" <<'OLD_EXE_EOF'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
    echo "claude-notifications v1.37.0"
    exit 0
fi
echo "unknown command: $1" >&2
exit 1
OLD_EXE_EOF
    chmod +x "$bin_dir/claude-notifications-windows-amd64.exe"
    # Complete desktop runtime so hook tests reach their intended behavior.
    printf '#!/bin/sh\nexit 0\n' > "$bin_dir/claude-notifications-windows-amd64-focus.exe"
    chmod +x "$bin_dir/claude-notifications-windows-amd64-focus.exe"

    local output exit_code
    set +e
    output=$(INSTALL_TARGET_DIR="$bin_dir" PATH="$fake_path:$PATH" SKIP_CONNECTIVITY_CHECK=true \
        RELEASE_URL="file:///no-such-claude-notifications-release" CHECKSUMS_URL="file:///no-such-claude-notifications-release/checksums.txt" \
        run_with_timeout 15 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set +e

    assert_exit_code 1 $exit_code "Old Windows binary does not complete setup silently"
    assert_contains "$output" "cannot generate exec-form hooks" "Installer detects old binary without exec-form windows-hooks"
    assert_contains "$output" "Updating claude-notifications-windows-amd64.exe before rewriting hooks" "Installer chooses update path"
    assert_not_contains "$output" "Setup complete" "Installer does not report success with stale hooks"

    cleanup_test_dir
}

test_windows_wrapper_exec_form_forces_update_before_hooks() {
    echo -e "\n${CYAN}▶ test_windows_wrapper_exec_form_forces_update_before_hooks${NC}"
    setup_test_dir

    local plugin_root="$TEST_DIR/plugin"
    local bin_dir="$plugin_root/bin"
    local hooks_dir="$plugin_root/hooks"
    local fake_path="$TEST_DIR/fakebin"

    mkdir -p "$bin_dir" "$hooks_dir" "$fake_path"
    printf '{"hooks":{}}\n' > "$hooks_dir/hooks.json"

    cat > "$fake_path/uname" <<'UNAME_EOF'
#!/bin/sh
case "$1" in
  -s) echo "MINGW64_NT-10.0" ;;
  -m) echo "x86_64" ;;
  *) echo "MINGW64_NT-10.0" ;;
esac
UNAME_EOF
    chmod +x "$fake_path/uname"

    cat > "$bin_dir/claude-notifications-windows-amd64.exe" <<'WRAPPER_EXE_EOF'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
    echo "claude-notifications v1.38.0"
    exit 0
fi
if [ "$1" = "windows-hooks" ]; then
    printf '{\n  "hooks": {\n    "Stop": [\n      {\n        "hooks": [\n          {\n            "type": "command",\n            "command": "${CLAUDE_PLUGIN_ROOT}/bin/hook-wrapper.sh",\n            "args": ["handle-hook", "Stop"],\n            "timeout": 30\n          }\n        ]\n      }\n    ]\n  }\n}\n'
    exit 0
fi
exit 0
WRAPPER_EXE_EOF
    chmod +x "$bin_dir/claude-notifications-windows-amd64.exe"
    # Complete desktop runtime so hook tests reach their intended behavior.
    printf '#!/bin/sh\nexit 0\n' > "$bin_dir/claude-notifications-windows-amd64-focus.exe"
    chmod +x "$bin_dir/claude-notifications-windows-amd64-focus.exe"

    local output exit_code
    set +e
    output=$(INSTALL_TARGET_DIR="$bin_dir" PATH="$fake_path:$PATH" SKIP_CONNECTIVITY_CHECK=true \
        RELEASE_URL="file:///no-such-claude-notifications-release" CHECKSUMS_URL="file:///no-such-claude-notifications-release/checksums.txt" \
        run_with_timeout 15 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set +e

    assert_exit_code 1 $exit_code "Wrapper exec-form Windows hooks do not complete setup silently"
    assert_contains "$output" "cannot generate exec-form hooks" "Installer rejects wrapper-based exec-form hooks"
    assert_contains "$output" "Updating claude-notifications-windows-amd64.exe before rewriting hooks" "Installer updates before writing direct exe hooks"
    assert_not_contains "$output" "Setup complete" "Installer does not report success with wrapper hooks"

    cleanup_test_dir
}

test_windows_sh_exec_form_forces_update_before_hooks() {
    echo -e "\n${CYAN}▶ test_windows_sh_exec_form_forces_update_before_hooks${NC}"
    setup_test_dir

    local plugin_root="$TEST_DIR/plugin"
    local bin_dir="$plugin_root/bin"
    local hooks_dir="$plugin_root/hooks"
    local fake_path="$TEST_DIR/fakebin"

    mkdir -p "$bin_dir" "$hooks_dir" "$fake_path"
    printf '{"hooks":{}}\n' > "$hooks_dir/hooks.json"

    cat > "$fake_path/uname" <<'UNAME_EOF'
#!/bin/sh
case "$1" in
  -s) echo "MINGW64_NT-10.0" ;;
  -m) echo "x86_64" ;;
  *) echo "MINGW64_NT-10.0" ;;
esac
UNAME_EOF
    chmod +x "$fake_path/uname"

    cat > "$bin_dir/claude-notifications-windows-amd64.exe" <<'SH_EXE_EOF'
#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
    echo "claude-notifications v1.38.0"
    exit 0
fi
if [ "$1" = "windows-hooks" ]; then
    printf '{\n  "hooks": {\n    "Stop": [\n      {\n        "hooks": [\n          {\n            "type": "command",\n            "command": "sh",\n            "args": ["%s", "handle-hook", "Stop"],\n            "timeout": 30\n          }\n        ]\n      }\n    ]\n  }\n}\n' "$0"
    exit 0
fi
exit 0
SH_EXE_EOF
    chmod +x "$bin_dir/claude-notifications-windows-amd64.exe"
    # Complete desktop runtime so hook tests reach their intended behavior.
    printf '#!/bin/sh\nexit 0\n' > "$bin_dir/claude-notifications-windows-amd64-focus.exe"
    chmod +x "$bin_dir/claude-notifications-windows-amd64-focus.exe"

    local output exit_code
    set +e
    output=$(INSTALL_TARGET_DIR="$bin_dir" PATH="$fake_path:$PATH" SKIP_CONNECTIVITY_CHECK=true \
        RELEASE_URL="file:///no-such-claude-notifications-release" CHECKSUMS_URL="file:///no-such-claude-notifications-release/checksums.txt" \
        run_with_timeout 15 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set +e

    assert_exit_code 1 $exit_code "sh-mediated Windows hooks do not complete setup silently"
    assert_contains "$output" "cannot generate exec-form hooks" "Installer rejects sh-mediated Windows hooks"
    assert_contains "$output" "Updating claude-notifications-windows-amd64.exe before rewriting hooks" "Installer updates before writing direct exe hooks"
    assert_not_contains "$output" "Setup complete" "Installer does not report success with sh-mediated hooks"

    cleanup_test_dir
}

test_windows_native_hooks_real_exec_launch() {
    echo -e "\n${CYAN}▶ test_windows_native_hooks_real_exec_launch${NC}"

    if ! is_windows; then
        skip_test "Windows native exec hook launch" "not on Windows"
        return
    fi

    if ! command -v go >/dev/null 2>&1; then
        skip_test "Windows native exec hook launch" "go not available"
        return
    fi

    setup_test_dir

    local plugin_root="$TEST_DIR/plugin"
    local bin_dir="$plugin_root/bin"
    local hooks_dir="$plugin_root/hooks"
    mkdir -p "$bin_dir" "$hooks_dir"
    printf '{"hooks":{}}\n' > "$hooks_dir/hooks.json"

    local exe_path="$bin_dir/claude-notifications-windows-amd64.exe"
    if ! (cd "$REPO_ROOT" && go build -o "$exe_path" ./cmd/claude-notifications); then
        fail_test "Build real Windows notification binary" "go build failed"
        cleanup_test_dir
        return
    fi

    printf '#!/bin/sh\nexit 0\n' > "$bin_dir/claude-notifications-windows-amd64-focus.exe"
    chmod +x "$bin_dir/claude-notifications-windows-amd64-focus.exe"

    touch "$bin_dir/sound-preview-windows-amd64.exe"
    touch "$bin_dir/list-devices-windows-amd64.exe"
    touch "$bin_dir/list-sounds-windows-amd64.exe"

    local output exit_code
    output=$(INSTALL_TARGET_DIR="$bin_dir" bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?

    assert_exit_code 0 $exit_code "Installer succeeds with real Windows binary"
    assert_contains "$output" "Windows exec-form hooks configured" "Installer rewrites hooks for exec form"

    local hooks_json
    hooks_json=$(cat "$hooks_dir/hooks.json")
    assert_contains "$hooks_json" '"args"[[:space:]]*:[[:space:]]*\[' "real hooks.json uses exec-form args"
    assert_not_contains "$hooks_json" '"shell"[[:space:]]*:' "real hooks.json does not force a shell"
    assert_not_contains "$hooks_json" '\$input' "real hooks.json does not pipe stdin through a shell"
    assert_not_contains "$hooks_json" '"command"[[:space:]]*:[^\n]*\|' "real hooks.json commands do not contain shell pipes"
    assert_not_contains "$hooks_json" 'hook-wrapper' "real hooks.json bypasses shell wrapper on Windows"
    assert_not_contains "$hooks_json" '\.bat|\.cmd' "real hooks.json does not use Windows shell shims"
    assert_contains "$hooks_json" '"handle-hook"' "real hooks.json calls hook handler"
    assert_contains "$hooks_json" '"Stop"' "real hooks.json contains Stop hook"

    set +e
    output=$(printf '{"session_id":"ci-win","transcript_path":"","cwd":""}\n' | "$exe_path" handle-hook Stop 2>&1)
    exit_code=$?
    set +e

    if [ "$exit_code" -ne 0 ]; then
        echo "$output"
    fi

    assert_exit_code 0 $exit_code "Generated exec-form hook command launches real exe"

    cleanup_test_dir
}

test_windows_real_hook_schedules_lazy_update() {
    echo -e "\n${CYAN}▶ test_windows_real_hook_schedules_lazy_update${NC}"

    if ! is_windows; then
        skip_test "Windows real hook schedules lazy update" "not on Windows"
        return
    fi

    if ! command -v go >/dev/null 2>&1; then
        skip_test "Windows real hook schedules lazy update" "go not available"
        return
    fi

    if ! command -v powershell.exe >/dev/null 2>&1; then
        skip_test "Windows real hook schedules lazy update" "PowerShell not available"
        return
    fi

    setup_test_dir

    local plugin_root="$TEST_DIR/plugin"
    local bin_dir="$plugin_root/bin"
    local manifest_dir="$plugin_root/.claude-plugin"
    mkdir -p "$bin_dir" "$manifest_dir"
    printf '{"version":"9.99.0"}\n' > "$manifest_dir/plugin.json"
    cp "$INSTALL_SCRIPT" "$bin_dir/install.sh"

    local exe_path="$bin_dir/claude-notifications-windows-amd64.exe"
    if ! (cd "$REPO_ROOT" && go build -o "$exe_path" ./cmd/claude-notifications); then
        fail_test "Build real Windows notification binary for lazy update" "go build failed"
        cleanup_test_dir
        return
    fi

    local fake_bash_go="$TEST_DIR/fake-bash.go"
    local fake_bash="$TEST_DIR/fake-bash.exe"
    local fake_bash_log="$TEST_DIR/fake-bash.log"
    cat > "$fake_bash_go" <<'FAKE_BASH_GO_EOF'
package main

import (
	"os"
	"path/filepath"
	"strings"
)

func main() {
	logPath := os.Getenv("FAKE_BASH_LOG")
	if logPath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	_ = os.WriteFile(logPath, []byte(strings.Join(os.Args[1:], "\n")), 0644)
}
FAKE_BASH_GO_EOF
    if ! go build -o "$fake_bash" "$fake_bash_go"; then
        fail_test "Build fake bash for lazy update" "go build failed"
        cleanup_test_dir
        return
    fi

    local fake_bash_for_windows="$fake_bash"
    local fake_bash_log_for_windows="$fake_bash_log"
    local fixture_config="$TEST_DIR/config.json"
    local fixture_config_for_windows="$fixture_config"
    if command -v cygpath >/dev/null 2>&1; then
        fake_bash_for_windows="$(cygpath -w "$fake_bash" 2>/dev/null || printf '%s' "$fake_bash")"
        fake_bash_log_for_windows="$(cygpath -w "$fake_bash_log" 2>/dev/null || printf '%s' "$fake_bash_log")"
        fixture_config_for_windows="$(cygpath -w "$fixture_config" 2>/dev/null || printf '%s' "$fixture_config")"
    fi

    # Keep the real hook fixture independent of arbitrary bundle or user
    # configuration; this test exercises updater scheduling, not recovery.
    if ! AGENT_NOTIFICATIONS_CONFIG="$fixture_config_for_windows" \
        "$exe_path" config init --json >/dev/null; then
        fail_test "Initialize config for Windows lazy update" "config init failed"
        cleanup_test_dir
        return
    fi

    local output exit_code
    set +e
    output=$(printf '{"session_id":"ci-win","transcript_path":"","cwd":""}\n' | \
        env AGENT_NOTIFICATIONS_CONFIG="$fixture_config_for_windows" \
            CLAUDE_NOTIFICATIONS_BASH="$fake_bash_for_windows" \
            FAKE_BASH_LOG="$fake_bash_log_for_windows" \
            CLAUDE_HOOK_JUDGE_MODE=true \
            "$exe_path" handle-hook Stop 2>&1)
    exit_code=$?
    set +e

    if [ "$exit_code" -ne 0 ]; then
        echo "$output"
    fi
    assert_exit_code 0 $exit_code "Real Windows hook exits successfully with plugin version mismatch"

    local i=0
    while [ $i -lt 80 ] && [ ! -f "$fake_bash_log" ]; do
        sleep 0.1
        i=$((i + 1))
    done

    assert_file_exists "$fake_bash_log" "Windows hook schedules lazy update through bash"
    if [ -f "$fake_bash_log" ]; then
        local fake_log
        fake_log=$(cat "$fake_bash_log")
        assert_contains "$fake_log" '^-lc$|^-l$' "Lazy update invokes bash login mode"
        assert_contains "$fake_log" '^-lc$|^-c$' "Lazy update invokes bash command mode"
        assert_contains "$fake_log" 'install\.sh.*--force' "Lazy update uses install.sh --force"
    fi

    cleanup_test_dir
}

test_force_preserves_apps_macos() {
    echo -e "\n${CYAN}▶ test_force_preserves_apps_macos${NC}"

    if [ "$(uname)" != "Darwin" ]; then
        skip_test "macOS apps" "not on macOS"
        return
    fi

    setup_test_dir

    # Create fake .app directories
    mkdir -p "$TEST_DIR/terminal-notifier.app/Contents"
    mkdir -p "$TEST_DIR/ClaudeNotifications.app/Contents"

    # Run with --force and unreachable URL
    # SKIP_CONNECTIVITY_CHECK bypasses the curl to github.com (flaky on CI)
    RELEASE_URL="http://127.0.0.1:1" \
    INSTALL_TARGET_DIR="$TEST_DIR" \
    SKIP_CONNECTIVITY_CHECK=true \
    run_with_timeout 5 bash "$INSTALL_SCRIPT" --force 2>&1 || true

    # Apps must survive a failed download
    [ -d "$TEST_DIR/terminal-notifier.app" ] && pass_test "terminal-notifier preserved" || fail_test "terminal-notifier preserved" "missing"
    [ -d "$TEST_DIR/ClaudeNotifications.app" ] && pass_test "icon app preserved" || fail_test "icon app preserved" "missing"

    cleanup_test_dir
}

#=============================================================================
# Category B: Mock Server Tests
#=============================================================================

test_mock_download_success() {
    echo -e "\n${CYAN}▶ test_mock_download_success${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Download success" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Download success" "mock server failed"; return; }

    # Create platform-specific mock binary name
    local binary_name=$(get_binary_name)

    # Copy mock_binary to expected name
    cp "$FIXTURES_DIR/mock_binary" "$FIXTURES_DIR/$binary_name"

    # Update checksums
    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$FIXTURES_DIR/checksums.txt"
    prepare_focus_fixture "$FIXTURES_DIR" "$checksum"

    # Run install
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
             NOTIFIER_URL="http://localhost:$MOCK_PORT/valid.zip" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 60 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?

    assert_exit_code 0 $exit_code "Install completed successfully"
    assert_desktop_runtime "$TEST_DIR"
    assert_file_exists "$TEST_DIR/$binary_name" "Binary downloaded"
    # On Windows, wrapper is .bat file; on Unix it's a symlink
    if is_windows; then
        assert_file_exists "$TEST_DIR/claude-notifications.bat" "Wrapper created"
    else
        assert_file_exists "$TEST_DIR/claude-notifications" "Symlink created"
    fi

    # Cleanup mock files
    rm -f "$FIXTURES_DIR/$binary_name"

    stop_mock_server
    cleanup_test_dir
}

test_mock_download_404() {
    echo -e "\n${CYAN}▶ test_mock_download_404${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Download 404" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Download 404" "mock server failed"; return; }

    # Run once and capture both output and exit code
    set +e
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT/404" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/404/checksums.txt" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 30 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Download failed|Installation Failed|failed" "Download failure detected"
    assert_exit_code 1 $exit_code "Exit code is 1 on download failure"

    stop_mock_server
    cleanup_test_dir
}

test_mock_download_500() {
    echo -e "\n${CYAN}▶ test_mock_download_500${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Download 500" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Download 500" "mock server failed"; return; }

    set +e
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT/500" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/500/checksums.txt" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 30 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Download failed|Installation Failed|failed" "500 error detected"
    assert_exit_code 1 $exit_code "Exit code is 1 on 500"

    stop_mock_server
    cleanup_test_dir
}

test_mock_file_too_small() {
    echo -e "\n${CYAN}▶ test_mock_file_too_small${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "File too small" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "File too small" "mock server failed"; return; }

    set +e
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT/small-file" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 30 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "too small|Download failed|Installation Failed" "Small file rejected"
    assert_exit_code 1 $exit_code "Exit code is 1 for small file"

    stop_mock_server
    cleanup_test_dir
}

test_mock_checksum_mismatch() {
    echo -e "\n${CYAN}▶ test_mock_checksum_mismatch${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Checksum mismatch" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Checksum mismatch" "mock server failed"; return; }

    # Create platform-specific mock binary
    local binary_name=$(get_binary_name)

    cp "$FIXTURES_DIR/mock_binary" "$FIXTURES_DIR/$binary_name"
    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$FIXTURES_DIR/checksums.txt"
    prepare_focus_fixture "$FIXTURES_DIR" "$checksum"

    set +e
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT/wrong-checksum" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 30 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Checksum mismatch|Verification Failed" "Checksum mismatch detected"
    assert_exit_code 1 $exit_code "Exit code is 1 on checksum mismatch"

    rm -f "$FIXTURES_DIR/$binary_name"

    stop_mock_server
    cleanup_test_dir
}

test_mock_partial_download_reports_transport_error() {
    echo -e "\n${CYAN}▶ test_mock_partial_download_reports_transport_error${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Partial download" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Partial download" "mock server failed"; return; }

    local binary_name=$(get_binary_name)

    cp "$FIXTURES_DIR/mock_binary" "$FIXTURES_DIR/$binary_name"

    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$FIXTURES_DIR/checksums.txt"
    prepare_focus_fixture "$FIXTURES_DIR" "$checksum"

    set +e
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT/partial-close" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
             NOTIFIER_URL="http://localhost:$MOCK_PORT/valid.zip" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 30 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "interrupted mid-download|Download failed|Installation Failed" "Partial transfer reported as download failure"
    assert_not_contains "$output" "Checksum mismatch" "Partial transfer does not degrade into checksum mismatch"
    assert_exit_code 1 $exit_code "Exit code is 1 on partial download"

    rm -f "$FIXTURES_DIR/$binary_name"

    stop_mock_server
    cleanup_test_dir
}

test_mock_wrong_payload_recovers_after_retry() {
    echo -e "\n${CYAN}▶ test_mock_wrong_payload_recovers_after_retry${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Wrong payload retry" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Wrong payload retry" "mock server failed"; return; }

    local binary_name=$(get_binary_name)

    cp "$FIXTURES_DIR/mock_binary" "$FIXTURES_DIR/$binary_name"

    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$FIXTURES_DIR/checksums.txt"
    prepare_focus_fixture "$FIXTURES_DIR" "$checksum"

    set +e
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT/wrong-then-ok" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
             NOTIFIER_URL="http://localhost:$MOCK_PORT/valid.zip" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 60 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Payload looks like text instead of a raw executable|Detected payload" "Installer explains the unexpected payload"
    assert_contains "$output" "Verification failed, retrying with a fresh download" "Installer retries after checksum failure"
    assert_contains "$output" "Checksum verified|Installation Complete" "Retry eventually succeeds"
    assert_exit_code 0 $exit_code "Exit code is 0 after recovering from wrong payload"
    assert_file_exists "$TEST_DIR/$binary_name" "Recovered binary installed"
    assert_desktop_runtime "$TEST_DIR"

    rm -f "$FIXTURES_DIR/$binary_name"

    stop_mock_server
    cleanup_test_dir
}

test_mock_pin_latest_to_exact_tag() {
    echo -e "\n${CYAN}▶ test_mock_pin_latest_to_exact_tag${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Pin latest release" "python3 not available"
        return
    fi

    setup_test_dir
    export MOCK_LATEST_TAG="v-test.1"
    start_mock_server $MOCK_PORT || { unset MOCK_LATEST_TAG; skip_test "Pin latest release" "mock server failed"; return; }

    local binary_name=$(get_binary_name)
    local pinned_dir="$FIXTURES_DIR/download/$MOCK_LATEST_TAG"
    mkdir -p "$pinned_dir"
    cp "$FIXTURES_DIR/mock_binary" "$pinned_dir/$binary_name"

    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$pinned_dir/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$pinned_dir/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$pinned_dir/checksums.txt"
    prepare_focus_fixture "$pinned_dir" "$checksum"
    cp "$FIXTURES_DIR/ClaudeNotifier.app.zip" "$pinned_dir/"

    set +e
    output=$(unset RELEASE_URL CHECKSUMS_URL MODERN_NOTIFIER_URL; SKIP_CONNECTIVITY_CHECK=true \
             RELEASES_BASE_URL="http://localhost:$MOCK_PORT" \
             LATEST_RELEASE_API_URL="http://localhost:$MOCK_PORT/api/latest" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 60 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_contains "$output" "Release:.*$MOCK_LATEST_TAG" "Installer resolves latest to a concrete tag"
    assert_contains "$output" "From: http://localhost:$MOCK_PORT/download/$MOCK_LATEST_TAG/$binary_name" "Pinned download URL is used"
    assert_exit_code 0 $exit_code "Install succeeds with pinned release tag"
    assert_file_exists "$TEST_DIR/$binary_name" "Pinned release binary downloaded"
    assert_desktop_runtime "$TEST_DIR"

    rm -rf "$FIXTURES_DIR/download"
    unset MOCK_LATEST_TAG

    stop_mock_server
    cleanup_test_dir
}

test_mock_zip_corrupted() {
    echo -e "\n${CYAN}▶ test_mock_zip_corrupted${NC}"

    if [ "$(uname)" != "Darwin" ]; then
        skip_test "Corrupted zip" "terminal-notifier only on macOS"
        return
    fi

    if ! command -v python3 &>/dev/null; then
        skip_test "Corrupted zip" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT || { skip_test "Corrupted zip" "mock server failed"; return; }

    # First do a successful main binary download, then test terminal-notifier
    local binary_name=$(get_binary_name)

    cp "$FIXTURES_DIR/mock_binary" "$FIXTURES_DIR/$binary_name"
    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$FIXTURES_DIR/checksums.txt"
    prepare_focus_fixture "$FIXTURES_DIR" "$checksum"

    local exit_code=0
    output=$(RELEASE_URL="http://localhost:$MOCK_PORT" \
             CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
             MODERN_NOTIFIER_URL="http://localhost:$MOCK_PORT/corrupted.zip" \
             NOTIFIER_URL="http://localhost:$MOCK_PORT/corrupted.zip" \
             INSTALL_TARGET_DIR="$TEST_DIR" \
             run_with_timeout 30 bash "$INSTALL_SCRIPT" 2>&1) || exit_code=$?

    assert_exit_code 1 "$exit_code" "Corrupt notifier prevents successful install"
    assert_file_not_exists "$TEST_DIR/claude-notifications" "Incomplete runtime is not published"
    # Missing desktop runtime must fail without publishing a ready installation
    assert_contains "$output" "not a valid zip|Could not extract|extraction" "Corrupted zip detected"

    rm -f "$FIXTURES_DIR/$binary_name"

    stop_mock_server
    cleanup_test_dir
}

#=============================================================================
# Category C: Real Network Tests (optional)
#=============================================================================

test_real_github_available() {
    echo -e "\n${CYAN}▶ test_real_github_available${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "GitHub available" "--real-network not specified"
        return
    fi

    if ! real_network_tests_supported; then
        skip_test "GitHub available" "real-network tests disabled on Windows CI by default"
        return
    fi

    if curl -s --max-time 10 -I https://github.com &>/dev/null; then
        echo -e "  ${GREEN}✓${NC} GitHub is reachable"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "  ${RED}✗${NC} GitHub is not reachable"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
    TESTS_RUN=$((TESTS_RUN + 1))
}

test_real_full_install() {
    echo -e "\n${CYAN}▶ test_real_full_install${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Real full install" "--real-network not specified"
        return
    fi

    if ! real_network_tests_supported; then
        skip_test "Real full install" "real-network tests disabled on Windows CI by default"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Real full install" "release binary not yet available"
        return
    fi

    setup_test_dir

    set +e
    output=$(INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 120 bash "$INSTALL_SCRIPT" 2>&1)
    exit_code=$?
    set -e

    assert_exit_code 0 $exit_code "Install completed successfully"
    # On Windows, wrapper is .bat file; on Unix it's a symlink
    if is_windows; then
        assert_file_exists "$TEST_DIR/claude-notifications.bat" "Wrapper created"
    else
        assert_file_exists "$TEST_DIR/claude-notifications" "Symlink created"
    fi

    cleanup_test_dir
}

test_real_binary_runs() {
    echo -e "\n${CYAN}▶ test_real_binary_runs${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Binary runs" "--real-network not specified"
        return
    fi

    if ! real_network_tests_supported; then
        skip_test "Binary runs" "real-network tests disabled on Windows CI by default"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Binary runs" "release binary not yet available"
        return
    fi

    setup_test_dir

    INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 120 bash "$INSTALL_SCRIPT" 2>&1 || true

    # Determine correct binary/wrapper path
    local binary_path
    if is_windows; then
        binary_path="$TEST_DIR/claude-notifications.bat"
    else
        binary_path="$TEST_DIR/claude-notifications"
    fi

    if [ -f "$binary_path" ]; then
        version_output=$("$binary_path" --version 2>&1 || true)
        assert_contains "$version_output" "claude-notifications" "Binary outputs version"
    else
        echo -e "  ${RED}✗${NC} Binary not found or not executable"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        TESTS_RUN=$((TESTS_RUN + 1))
    fi

    cleanup_test_dir
}

test_real_utilities_installed() {
    echo -e "\n${CYAN}▶ test_real_utilities_installed${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Utilities installed" "--real-network not specified"
        return
    fi

    if ! real_network_tests_supported; then
        skip_test "Utilities installed" "real-network tests disabled on Windows CI by default"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Utilities installed" "release binary not yet available"
        return
    fi

    setup_test_dir

    INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 120 bash "$INSTALL_SCRIPT" 2>&1 || true

    # On Windows, utilities use .bat wrappers
    if is_windows; then
        assert_file_exists "$TEST_DIR/sound-preview.bat" "sound-preview installed"
        assert_file_exists "$TEST_DIR/list-devices.bat" "list-devices installed"
        assert_file_exists "$TEST_DIR/list-sounds.bat" "list-sounds installed"
    else
        assert_file_exists "$TEST_DIR/sound-preview" "sound-preview installed"
        assert_file_exists "$TEST_DIR/list-devices" "list-devices installed"
        assert_file_exists "$TEST_DIR/list-sounds" "list-sounds installed"
    fi

    cleanup_test_dir
}

test_real_terminal_notifier_macos() {
    echo -e "\n${CYAN}▶ test_real_terminal_notifier_macos${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "terminal-notifier" "--real-network not specified"
        return
    fi

    if ! real_network_tests_supported; then
        skip_test "terminal-notifier" "real-network tests disabled on Windows CI by default"
        return
    fi

    if [ "$(uname)" != "Darwin" ]; then
        skip_test "terminal-notifier" "not on macOS"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "terminal-notifier" "release binary not yet available"
        return
    fi

    setup_test_dir

    INSTALL_TARGET_DIR="$TEST_DIR" run_with_timeout 120 bash "$INSTALL_SCRIPT" 2>&1 || true

    # ClaudeNotifier.app is the modern notifier (preferred over legacy terminal-notifier.app)
    assert_dir_exists "$TEST_DIR/ClaudeNotifier.app" "ClaudeNotifier.app installed"
    assert_executable "$TEST_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern" "terminal-notifier-modern executable"

    cleanup_test_dir
}

#=============================================================================
# Category D: Hook Wrapper Tests (Offline)
#=============================================================================

test_hook_wrapper_exists() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_exists${NC}"

    assert_file_exists "$SCRIPT_DIR/hook-wrapper.sh" "hook-wrapper.sh exists"
}

test_hook_wrapper_is_executable() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_is_executable${NC}"

    assert_executable "$SCRIPT_DIR/hook-wrapper.sh" "hook-wrapper.sh is executable"
}

test_hook_wrapper_posix_syntax() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_posix_syntax${NC}"

    # Check that wrapper uses #!/bin/sh (POSIX), not #!/bin/bash
    first_line=$(head -n1 "$SCRIPT_DIR/hook-wrapper.sh")
    if echo "$first_line" | grep -q "#!/bin/sh"; then
        pass_test "Wrapper uses POSIX #!/bin/sh shebang"
    else
        fail_test "Wrapper uses POSIX #!/bin/sh shebang" "Found: $first_line"
    fi
}

test_hook_wrapper_no_bashisms() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_no_bashisms${NC}"

    # Check for common bash-only features that break POSIX sh
    # Note: ${VAR:-default} and ${VAR:+alt} are POSIX-compliant, so exclude :- and :+
    if grep -E '\[\[|\$\{[^}]+:[^-+]|\bfunction\b|\bsource\b' "$SCRIPT_DIR/hook-wrapper.sh" >/dev/null 2>&1; then
        fail_test "No bashisms in wrapper" "Found bash-specific syntax"
    else
        pass_test "No bashisms in wrapper"
    fi
}

test_hook_wrapper_graceful_no_install_script() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_graceful_no_install_script${NC}"

    setup_test_dir

    # Copy wrapper only (no install.sh, no binary)
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"

    # Run wrapper - should exit gracefully (exit 0)
    output=$(echo '{"session_id":"test","transcript_path":"","cwd":"/tmp"}' | \
        sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop 2>&1)
    exit_code=$?

    assert_exit_code 0 $exit_code "Wrapper exits gracefully when install.sh missing"

    cleanup_test_dir
}

test_hook_wrapper_graceful_download_fail() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_graceful_download_fail${NC}"

    setup_test_dir

    # Copy wrapper and install script
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"
    cp "$SCRIPT_DIR/install.sh" "$TEST_DIR/"

    # Run with invalid URL - should exit gracefully
    output=$(echo '{"session_id":"test","transcript_path":"","cwd":"/tmp"}' | \
        RELEASE_URL="http://127.0.0.1:1" sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop 2>&1)
    exit_code=$?

    assert_exit_code 0 $exit_code "Wrapper exits gracefully when download fails"

    cleanup_test_dir
}

test_hook_wrapper_detects_platform() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_detects_platform${NC}"

    # Verify wrapper contains platform detection logic
    if grep -q 'MINGW\|MSYS\|CYGWIN' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper has Windows platform detection"
    else
        fail_test "Wrapper has Windows platform detection" "Missing MINGW/MSYS/CYGWIN check"
    fi
}

test_hook_wrapper_path_with_spaces() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_path_with_spaces${NC}"

    # Create test dir with spaces
    TEST_DIR_SPACES=$(mktemp -d)/path\ with\ spaces
    mkdir -p "$TEST_DIR_SPACES"

    # Copy wrapper
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR_SPACES/"

    # Run wrapper - should handle spaces correctly
    output=$(echo '{"session_id":"test","transcript_path":"","cwd":"/tmp"}' | \
        sh "$TEST_DIR_SPACES/hook-wrapper.sh" handle-hook Stop 2>&1)
    exit_code=$?

    assert_exit_code 0 $exit_code "Wrapper handles paths with spaces"

    rm -rf "$(dirname "$TEST_DIR_SPACES")"
}

test_hook_wrapper_passes_all_arguments() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_passes_all_arguments${NC}"

    setup_test_dir

    # Create a mock binary that echoes arguments
    # On Windows, create shell script and point CLAUDE_NOTIFICATIONS_BIN to it
    # (avoids .bat/cmd.exe issues in the test harness)
    if is_windows; then
        cat > "$TEST_DIR/mock-binary.sh" << 'MOCK_EOF'
#!/bin/sh
echo "ARGS:$*"
MOCK_EOF
        chmod +x "$TEST_DIR/mock-binary.sh"
        MOCK_ENV="CLAUDE_NOTIFICATIONS_BIN=$TEST_DIR/mock-binary.sh"
    else
        cat > "$TEST_DIR/claude-notifications" << 'MOCK_EOF'
#!/bin/sh
echo "ARGS:$*"
MOCK_EOF
        chmod +x "$TEST_DIR/claude-notifications"
        MOCK_ENV=""
    fi

    # Copy wrapper
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"

    # Run wrapper with multiple arguments
    output=$(echo '{}' | env $MOCK_ENV sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop --extra-arg 2>&1)

    if echo "$output" | grep -q "ARGS:handle-hook Stop --extra-arg"; then
        pass_test "Wrapper passes all arguments to binary"
    else
        fail_test "Wrapper passes all arguments to binary" "Got: $output"
    fi

    cleanup_test_dir
}

test_hook_wrapper_exec_replaces_process() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_exec_replaces_process${NC}"

    # Verify wrapper invokes the binary (via run_binary or exec)
    if grep -q 'run_binary "\$@"' "$SCRIPT_DIR/hook-wrapper.sh" || grep -q 'exec "\$BINARY"' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper invokes binary for process execution"
    else
        fail_test "Wrapper invokes binary for process execution" "Missing run_binary or exec call"
    fi
}

test_hook_wrapper_silent_install() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_silent_install${NC}"

    # Verify install.sh output is redirected to /dev/null
    if grep -q '>/dev/null 2>&1' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper runs install.sh silently"
    else
        fail_test "Wrapper runs install.sh silently" "Missing output redirection"
    fi
}

test_hook_wrapper_install_non_blocking() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_install_non_blocking${NC}"

    # Verify install.sh failure doesn't block (|| true)
    if grep -q '|| true' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper handles install.sh failure gracefully"
    else
        fail_test "Wrapper handles install.sh failure gracefully" "Missing '|| true'"
    fi
}

test_hook_wrapper_version_check_exists() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_version_check_exists${NC}"

    # Verify wrapper has version checking logic (BIN_VER and PLG_VER variables)
    if grep -q 'BIN_VER' "$SCRIPT_DIR/hook-wrapper.sh" && \
       grep -q 'PLG_VER' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper has version checking logic"
    else
        fail_test "Wrapper has version checking logic" "Missing version variables"
    fi
}

test_hook_wrapper_version_mismatch_triggers_update() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_version_mismatch_triggers_update${NC}"

    # Create proper directory structure: bin/ and .claude-plugin/ at same level
    local ROOT_DIR="${TMPDIR:-${TEMP:-/tmp}}/wrapper-version-test-$$"
    rm -rf "$ROOT_DIR"
    mkdir -p "$ROOT_DIR/bin" "$ROOT_DIR/.claude-plugin"

    # Create mock binary that reports old version
    cat > "$ROOT_DIR/bin/mock-binary.sh" << 'MOCK_EOF'
#!/bin/sh
if [ "$1" = "version" ]; then echo "claude-notifications version 1.0.0"; fi
MOCK_EOF
    chmod +x "$ROOT_DIR/bin/mock-binary.sh"

    if ! is_windows; then
        cp "$ROOT_DIR/bin/mock-binary.sh" "$ROOT_DIR/bin/claude-notifications"
        chmod +x "$ROOT_DIR/bin/claude-notifications"
    fi

    # Create plugin.json with newer version (at root level, not in bin/)
    echo '{"version": "2.0.0"}' > "$ROOT_DIR/.claude-plugin/plugin.json"

    # Copy wrapper to bin/
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$ROOT_DIR/bin/"
    chmod +x "$ROOT_DIR/bin/hook-wrapper.sh"

    # Create dummy install.sh that creates a marker file
    cat > "$ROOT_DIR/bin/install.sh" << 'INSTALL_EOF'
#!/bin/sh
touch "$INSTALL_TARGET_DIR/.update-triggered"
INSTALL_EOF
    chmod +x "$ROOT_DIR/bin/install.sh"

    # Run wrapper (on Windows, use CLAUDE_NOTIFICATIONS_BIN to bypass .bat detection)
    local run_env=""
    if is_windows; then
        run_env="CLAUDE_NOTIFICATIONS_BIN=$ROOT_DIR/bin/mock-binary.sh"
    fi
    echo '{}' | env $run_env sh "$ROOT_DIR/bin/hook-wrapper.sh" handle-hook Stop >/dev/null 2>&1 || true

    # Check if update was triggered
    if [ -f "$ROOT_DIR/bin/.update-triggered" ]; then
        pass_test "Version mismatch triggers update"
    else
        fail_test "Version mismatch triggers update" "Update not triggered"
    fi

    rm -rf "$ROOT_DIR"
}

test_hook_wrapper_version_match_no_update() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_version_match_no_update${NC}"

    # Create proper directory structure: bin/ and .claude-plugin/ at same level
    local ROOT_DIR="${TMPDIR:-${TEMP:-/tmp}}/wrapper-match-test-$$"
    rm -rf "$ROOT_DIR"
    mkdir -p "$ROOT_DIR/bin" "$ROOT_DIR/.claude-plugin"

    # Create mock binary that reports matching version
    cat > "$ROOT_DIR/bin/mock-binary.sh" << 'MOCK_EOF'
#!/bin/sh
if [ "$1" = "version" ]; then echo "claude-notifications version 1.0.0"; fi
echo "EXECUTED"
MOCK_EOF
    chmod +x "$ROOT_DIR/bin/mock-binary.sh"

    if ! is_windows; then
        # Unix: create symlink as expected by wrapper
        cp "$ROOT_DIR/bin/mock-binary.sh" "$ROOT_DIR/bin/claude-notifications"
        chmod +x "$ROOT_DIR/bin/claude-notifications"
    fi

    # Create plugin.json with SAME version
    echo '{"version": "1.0.0"}' > "$ROOT_DIR/.claude-plugin/plugin.json"

    # Copy wrapper to bin/
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$ROOT_DIR/bin/"
    chmod +x "$ROOT_DIR/bin/hook-wrapper.sh"

    # Create install.sh that creates a marker file
    cat > "$ROOT_DIR/bin/install.sh" << 'INSTALL_EOF'
#!/bin/sh
touch "$INSTALL_TARGET_DIR/.update-triggered"
INSTALL_EOF
    chmod +x "$ROOT_DIR/bin/install.sh"

    # Run wrapper (on Windows, use CLAUDE_NOTIFICATIONS_BIN to bypass .bat detection)
    local run_env=""
    if is_windows; then
        run_env="CLAUDE_NOTIFICATIONS_BIN=$ROOT_DIR/bin/mock-binary.sh"
    fi
    output=$(echo '{}' | env $run_env sh "$ROOT_DIR/bin/hook-wrapper.sh" handle-hook Stop 2>&1) || true

    # Check that update was NOT triggered (versions match)
    if [ ! -f "$ROOT_DIR/bin/.update-triggered" ]; then
        pass_test "Version match skips update"
    else
        fail_test "Version match skips update" "Update was triggered unnecessarily"
    fi

    rm -rf "$ROOT_DIR"
}

test_hook_wrapper_uses_force_on_update() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_uses_force_on_update${NC}"

    # Verify wrapper uses --force when updating existing binary
    if grep -q '\-\-force' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper uses --force flag for updates"
    else
        fail_test "Wrapper uses --force flag for updates" "Missing --force"
    fi
}

test_hook_wrapper_outputs_system_message() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_outputs_system_message${NC}"

    # Verify wrapper outputs systemMessage JSON for Claude Code console
    if grep -q 'systemMessage' "$SCRIPT_DIR/hook-wrapper.sh"; then
        pass_test "Wrapper outputs systemMessage for console notification"
    else
        fail_test "Wrapper outputs systemMessage" "Missing systemMessage output"
    fi
}

#=============================================================================
# Category E: Hook Wrapper Tests (Mock Server)
#=============================================================================

test_hook_wrapper_mock_download() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_mock_download${NC}"

    if ! command -v python3 &>/dev/null; then
        skip_test "Hook wrapper mock download" "python3 not available"
        return
    fi

    setup_test_dir
    start_mock_server $MOCK_PORT

    if [ -z "$MOCK_PID" ]; then
        skip_test "Hook wrapper mock download" "Mock server failed to start"
        return
    fi

    # Create platform-specific mock binary (same as test_mock_download_success)
    local binary_name=$(get_binary_name)
    cp "$FIXTURES_DIR/mock_binary" "$FIXTURES_DIR/$binary_name"

    # Update checksums
    local checksum
    if command -v shasum &>/dev/null; then
        checksum=$(shasum -a 256 "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    else
        checksum=$(sha256sum "$FIXTURES_DIR/$binary_name" | awk '{print $1}')
    fi
    echo "$checksum  $binary_name" > "$FIXTURES_DIR/checksums.txt"
    prepare_focus_fixture "$FIXTURES_DIR" "$checksum"

    # Copy wrapper and install script
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"
    cp "$SCRIPT_DIR/install.sh" "$TEST_DIR/"
    chmod +x "$TEST_DIR/hook-wrapper.sh" "$TEST_DIR/install.sh"

    # Run wrapper with mock server URL
    output=$(echo '{"session_id":"test","transcript_path":"","cwd":"/tmp"}' | \
        RELEASE_URL="http://localhost:$MOCK_PORT" \
        CHECKSUMS_URL="http://localhost:$MOCK_PORT/checksums.txt" \
        sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop 2>&1) || true

    # Cleanup mock files
    rm -f "$FIXTURES_DIR/$binary_name"

    stop_mock_server

    # Check the wrapper installed the complete desktop runtime.
    assert_desktop_runtime "$TEST_DIR"
    # Check binary was downloaded
    if is_windows; then
        assert_file_exists "$TEST_DIR/claude-notifications.bat" "Binary downloaded via wrapper (mock)"
    else
        assert_file_exists "$TEST_DIR/claude-notifications" "Binary downloaded via wrapper (mock)"
    fi

    cleanup_test_dir
}

#=============================================================================
# Category F: Hook Wrapper Tests (Real Network)
#=============================================================================

test_hook_wrapper_real_download() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_real_download${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Hook wrapper real download" "--real-network not specified"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Hook wrapper real download" "release binary not yet available"
        return
    fi

    setup_test_dir

    # Copy wrapper and install script (but NOT binary)
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"
    cp "$SCRIPT_DIR/install.sh" "$TEST_DIR/"

    # Run wrapper - should auto-download binary
    output=$(echo '{"session_id":"test","transcript_path":"","cwd":"/tmp"}' | \
        sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop 2>&1) || true

    # Check binary was downloaded
    if is_windows; then
        assert_file_exists "$TEST_DIR/claude-notifications.bat" "Binary downloaded via wrapper"
    else
        assert_file_exists "$TEST_DIR/claude-notifications" "Binary downloaded via wrapper"
    fi

    cleanup_test_dir
}

test_hook_wrapper_real_no_redownload() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_real_no_redownload${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Hook wrapper no re-download" "--real-network not specified"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Hook wrapper no re-download" "release binary not yet available"
        return
    fi

    setup_test_dir

    # First install binary normally
    INSTALL_TARGET_DIR="$TEST_DIR" bash "$INSTALL_SCRIPT" 2>&1 || true

    # Copy wrapper
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"

    # Get binary modification time
    if is_windows; then
        BINARY="$TEST_DIR/claude-notifications.bat"
    else
        BINARY="$TEST_DIR/claude-notifications"
    fi
    mtime_before=$(stat -c %Y "$BINARY" 2>/dev/null || stat -f %m "$BINARY" 2>/dev/null)

    # Small sleep to ensure mtime would change if re-downloaded
    sleep 1

    # Run wrapper
    output=$(echo '{"session_id":"test","transcript_path":"","cwd":"/tmp"}' | \
        sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop 2>&1) || true

    # Binary should NOT be re-downloaded (same mtime)
    mtime_after=$(stat -c %Y "$BINARY" 2>/dev/null || stat -f %m "$BINARY" 2>/dev/null)

    if [ "$mtime_before" = "$mtime_after" ]; then
        pass_test "Existing binary not re-downloaded"
    else
        fail_test "Existing binary not re-downloaded" "mtime changed: $mtime_before -> $mtime_after"
    fi

    cleanup_test_dir
}

test_hook_wrapper_real_binary_runs() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_real_binary_runs${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Hook wrapper binary runs" "--real-network not specified"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Hook wrapper binary runs" "release binary not yet available"
        return
    fi

    setup_test_dir

    # Copy wrapper and install script
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"
    cp "$SCRIPT_DIR/install.sh" "$TEST_DIR/"

    # Run wrapper with --version to verify binary actually executes
    output=$(sh "$TEST_DIR/hook-wrapper.sh" --version 2>&1) || true

    if echo "$output" | grep -qE "claude-notifications|[0-9]+\.[0-9]+\.[0-9]+"; then
        pass_test "Binary executes via wrapper"
    else
        fail_test "Binary executes via wrapper" "Output: $output"
    fi

    cleanup_test_dir
}

test_hook_wrapper_real_concurrent_calls() {
    echo -e "\n${CYAN}▶ test_hook_wrapper_real_concurrent_calls${NC}"

    if [ "$RUN_REAL_NETWORK" != true ]; then
        skip_test "Hook wrapper concurrent calls" "--real-network not specified"
        return
    fi

    if [ "$RELEASE_BINARY_AVAILABLE" != true ]; then
        skip_test "Hook wrapper concurrent calls" "release binary not yet available"
        return
    fi

    setup_test_dir

    # Copy wrapper and install script
    cp "$SCRIPT_DIR/hook-wrapper.sh" "$TEST_DIR/"
    cp "$SCRIPT_DIR/install.sh" "$TEST_DIR/"

    # Run 3 concurrent wrapper calls
    (echo '{}' | sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop >/dev/null 2>&1) &
    pid1=$!
    (echo '{}' | sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop >/dev/null 2>&1) &
    pid2=$!
    (echo '{}' | sh "$TEST_DIR/hook-wrapper.sh" handle-hook Stop >/dev/null 2>&1) &
    pid3=$!

    # Wait for all to complete
    wait $pid1 $pid2 $pid3

    # Verify binary exists and is not corrupted
    # Windows: check -f (file exists) since .bat files aren't executable
    # Unix: check -x (executable)
    if is_windows; then
        BINARY="$TEST_DIR/claude-notifications.bat"
        if [ -f "$BINARY" ]; then
            pass_test "Concurrent calls don't corrupt installation"
        else
            fail_test "Concurrent calls don't corrupt installation" "Binary missing"
        fi
    else
        BINARY="$TEST_DIR/claude-notifications"
        if [ -x "$BINARY" ]; then
            pass_test "Concurrent calls don't corrupt installation"
        else
            fail_test "Concurrent calls don't corrupt installation" "Binary missing or not executable"
        fi
    fi

    cleanup_test_dir
}

#=============================================================================
# Main
#=============================================================================

main() {
    echo ""
    echo -e "${BOLD}========================================${NC}"
    echo -e "${BOLD} install.sh E2E Tests${NC}"
    echo -e "${BOLD}========================================${NC}"
    echo ""
    echo "Options:"
    echo "  Real network tests: $RUN_REAL_NETWORK"
    echo "  Mock only: $RUN_MOCK_ONLY"
    echo "  Verbose: $VERBOSE"

    if [ "$RUN_MOCK_ONLY" != true ]; then
        # Category A: Offline Tests
        echo ""
        echo -e "${BOLD}Category A: Offline Tests${NC}"
        test_platform_detection
        test_binary_name_format
        test_wsl_guard_blocks_linux_install
        test_bootstrap_version_verification_uses_installed_manifest_for_unknown_registry
        test_bootstrap_empty_registry_version_preserves_installed_manifest
        test_lock_created
        test_lock_prevents_parallel
        test_lock_stale_removal
        test_lock_cleanup_on_exit
        test_no_write_permission
        test_install_target_dir
        test_directory_auto_created
        test_transport_error_diagnostics
        test_required_tools_curl_wget
        test_force_preserves_binaries
        test_force_preserves_symlinks
        test_windows_native_hooks_configured_existing_binary
        test_windows_old_binary_forces_update_before_hooks
        test_windows_wrapper_exec_form_forces_update_before_hooks
        test_windows_sh_exec_form_forces_update_before_hooks
        test_windows_native_hooks_real_exec_launch
        test_windows_real_hook_schedules_lazy_update
        test_force_preserves_apps_macos
    fi

    # Category B: Mock Server Tests
    echo ""
    echo -e "${BOLD}Category B: Mock Server Tests${NC}"
    test_mock_download_success
    test_mock_download_404
    test_mock_download_500
    test_mock_file_too_small
    test_mock_checksum_mismatch
    test_mock_partial_download_reports_transport_error
    test_mock_wrong_payload_recovers_after_retry
    test_mock_pin_latest_to_exact_tag
    test_mock_zip_corrupted

    if [ "$RUN_MOCK_ONLY" != true ]; then
        # Pre-check: is the latest release binary actually available AND matches our version?
        # Avoids false failures when CI runs before Release workflow finishes uploading assets.
        # Race condition: pre-check may pass (old release), but by the time install.sh runs,
        # /releases/latest may have switched to the new release (without binaries yet).
        RELEASE_BINARY_AVAILABLE=false
        if real_network_tests_supported; then
            local _check_url="https://github.com/777genius/agent-notifications/releases/latest/download"
            local _check_bin
            case "$(uname -s)-$(uname -m)" in
                Linux-x86_64)       _check_bin="claude-notifications-linux-amd64" ;;
                Linux-aarch64)      _check_bin="claude-notifications-linux-arm64" ;;
                Darwin-arm64)       _check_bin="claude-notifications-darwin-arm64" ;;
                Darwin-x86_64)      _check_bin="claude-notifications-darwin-amd64" ;;
                MINGW*|MSYS*|CYGWIN*) _check_bin="claude-notifications-windows-amd64.exe" ;;
                *)                  _check_bin="claude-notifications-linux-amd64" ;;
            esac

            # Step 1: Check that latest release version matches our plugin.json version
            # This prevents the race where /releases/latest switches mid-test
            local _our_version=""
            local _plugin_json="$SCRIPT_DIR/../.claude-plugin/plugin.json"
            if [ -f "$_plugin_json" ]; then
                _our_version=$(grep '"version"' "$_plugin_json" | head -1 | sed 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
            fi
            local _latest_tag=""
            _latest_tag=$(curl -sI -o /dev/null -w "%{redirect_url}" --connect-timeout 5 "https://github.com/777genius/agent-notifications/releases/latest" 2>/dev/null | sed 's|.*/tag/v||')

            if [ -n "$_our_version" ] && [ -n "$_latest_tag" ] && [ "$_our_version" != "$_latest_tag" ]; then
                echo -e "  ${YELLOW}⚠${NC} Release version mismatch: ours=v${_our_version}, latest=v${_latest_tag} — download tests will be skipped"
            else
                # Step 2: Check that the binary is actually downloadable
                local _http_code
                _http_code=$(curl -sL -o /dev/null -w "%{http_code}" --connect-timeout 5 "${_check_url}/${_check_bin}" 2>/dev/null || echo "000")
                if [ "$_http_code" = "200" ]; then
                    RELEASE_BINARY_AVAILABLE=true
                else
                    echo -e "  ${YELLOW}⚠${NC} Latest release binary not yet available (HTTP $_http_code) — download tests will be skipped"
                fi
            fi
        elif [ "$RUN_REAL_NETWORK" = true ]; then
            echo -e "  ${YELLOW}⚠${NC} Real-network tests disabled on Windows CI by default — Category C will be skipped"
        fi

        # Category C: Real Network Tests
        echo ""
        echo -e "${BOLD}Category C: Real Network Tests${NC}"
        test_real_github_available
        test_real_full_install
        test_real_binary_runs
        test_real_utilities_installed
        test_real_terminal_notifier_macos
    fi

    if [ "$RUN_MOCK_ONLY" != true ]; then
        # Category D: Hook Wrapper Tests (Offline)
        echo ""
        echo -e "${BOLD}Category D: Hook Wrapper Tests (Offline)${NC}"
        test_hook_wrapper_exists
        test_hook_wrapper_is_executable
        test_hook_wrapper_posix_syntax
        test_hook_wrapper_no_bashisms
        test_hook_wrapper_graceful_no_install_script
        test_hook_wrapper_graceful_download_fail
        test_hook_wrapper_detects_platform
        test_hook_wrapper_path_with_spaces
        test_hook_wrapper_passes_all_arguments
        test_hook_wrapper_exec_replaces_process
        test_hook_wrapper_silent_install
        test_hook_wrapper_install_non_blocking
        test_hook_wrapper_version_check_exists
        test_hook_wrapper_version_mismatch_triggers_update
        test_hook_wrapper_version_match_no_update
        test_hook_wrapper_uses_force_on_update
        test_hook_wrapper_outputs_system_message
    fi

    # Category E: Hook Wrapper Tests (Mock Server)
    echo ""
    echo -e "${BOLD}Category E: Hook Wrapper Tests (Mock Server)${NC}"
    test_hook_wrapper_mock_download

    if [ "$RUN_MOCK_ONLY" != true ]; then
        # Category F: Hook Wrapper Tests (Real Network)
        echo ""
        echo -e "${BOLD}Category F: Hook Wrapper Tests (Real Network)${NC}"
        test_hook_wrapper_real_download
        test_hook_wrapper_real_no_redownload
        test_hook_wrapper_real_binary_runs
        test_hook_wrapper_real_concurrent_calls
    fi

    # Summary
    echo ""
    echo -e "${BOLD}========================================${NC}"
    echo -e "${BOLD} Summary${NC}"
    echo -e "${BOLD}========================================${NC}"
    echo -e "Total:   $TESTS_RUN"
    echo -e "Passed:  ${GREEN}$TESTS_PASSED${NC}"
    echo -e "Failed:  ${RED}$TESTS_FAILED${NC}"
    echo -e "Skipped: ${YELLOW}$TESTS_SKIPPED${NC}"
    echo ""

    if [ $TESTS_FAILED -gt 0 ]; then
        echo -e "${RED}Some tests failed!${NC}"
        exit 1
    else
        echo -e "${GREEN}All tests passed!${NC}"
        exit 0
    fi
}

main "$@"
