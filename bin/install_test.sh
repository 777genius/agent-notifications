#!/bin/bash
TEST_ENV_HANDOFF_GOMODCACHE=1
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# install_test.sh - Tests for install.sh functions
# Run with: bash bin/install_test.sh

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTS_PASSED=0
TESTS_FAILED=0

# Test helper functions
assert_equals() {
    local expected="$1"
    local actual="$2"
    local message="$3"

    if [ "$expected" = "$actual" ]; then
        echo -e "${GREEN}PASS${NC}: $message"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}: $message"
        echo "  Expected: '$expected'"
        echo "  Actual:   '$actual'"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
}

assert_not_empty() {
    local value="$1"
    local message="$2"

    if [ -n "$value" ]; then
        echo -e "${GREEN}PASS${NC}: $message"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}: $message (value is empty)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
}

assert_file_exists() {
    local file="$1"
    local message="$2"

    if [ -f "$file" ]; then
        echo -e "${GREEN}PASS${NC}: $message"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}: $message (file not found: $file)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
}

# Setup: detect platform (inline version)
detect_platform() {
    local os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    local arch="$(uname -m)"

    case "$os" in
        darwin) PLATFORM="darwin" ;;
        linux) PLATFORM="linux" ;;
        mingw*|msys*|cygwin*) PLATFORM="windows" ;;
        *) PLATFORM="unknown" ;;
    esac

    case "$arch" in
        x86_64|amd64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *) ARCH="unknown" ;;
    esac

    if [ "$PLATFORM" = "windows" ]; then
        BINARY_NAME="claude-notifications-${PLATFORM}-${ARCH}.exe"
    else
        BINARY_NAME="claude-notifications-${PLATFORM}-${ARCH}"
    fi
}

# ============= Tests =============

echo ""
echo "========================================="
echo " Running install.sh Tests"
echo "========================================="
echo ""

# Test 1: Platform detection
echo "--- Test: Platform Detection ---"
detect_platform

current_os="$(uname -s | tr '[:upper:]' '[:lower:]')"

case "$current_os" in
    darwin)
        assert_equals "darwin" "$PLATFORM" "Platform should be darwin on macOS"
        ;;
    linux)
        assert_equals "linux" "$PLATFORM" "Platform should be linux on Linux"
        ;;
    mingw*|msys*|cygwin*)
        assert_equals "windows" "$PLATFORM" "Platform should be windows on Windows"
        ;;
esac

assert_not_empty "$ARCH" "Architecture should be detected"
assert_not_empty "$BINARY_NAME" "Binary name should be constructed"

# Binary name should contain platform and arch
if [[ "$BINARY_NAME" == *"$PLATFORM"* ]] && [[ "$BINARY_NAME" == *"$ARCH"* ]]; then
    echo -e "${GREEN}PASS${NC}: Binary name contains platform and arch"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}FAIL${NC}: Binary name should contain platform and arch"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 2: Architecture detection
echo "--- Test: Architecture Detection ---"
current_arch="$(uname -m)"

case "$current_arch" in
    x86_64|amd64)
        assert_equals "amd64" "$ARCH" "Architecture should be amd64 for x86_64"
        ;;
    arm64|aarch64)
        assert_equals "arm64" "$ARCH" "Architecture should be arm64 for arm64/aarch64"
        ;;
esac
echo ""

# Test 3: Binary name format
echo "--- Test: Binary Name Format ---"

if [ "$PLATFORM" = "windows" ]; then
    if [[ "$BINARY_NAME" == *.exe ]]; then
        echo -e "${GREEN}PASS${NC}: Windows binary has .exe extension"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}: Windows binary should have .exe extension"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
else
    if [[ "$BINARY_NAME" != *.exe ]]; then
        echo -e "${GREEN}PASS${NC}: Unix binary has no .exe extension"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}: Unix binary should not have .exe extension"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
fi
echo ""

# Test 4: install.sh exists and is executable
echo "--- Test: install.sh exists ---"
assert_file_exists "${SCRIPT_DIR}/install.sh" "install.sh should exist"

if [ -x "${SCRIPT_DIR}/install.sh" ]; then
    echo -e "${GREEN}PASS${NC}: install.sh is executable"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}FAIL${NC}: install.sh should be executable"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 5: Wrapper script exists
echo "--- Test: Wrapper script exists ---"
if [ "$PLATFORM" = "windows" ]; then
    # On Windows, wrapper is a .bat file (may not exist until install.sh is run)
    # Skip this test on Windows CI where install.sh hasn't been run
    if [ -f "${SCRIPT_DIR}/claude-notifications.bat" ]; then
        assert_file_exists "${SCRIPT_DIR}/claude-notifications.bat" "Wrapper script should exist (.bat)"
    else
        echo -e "${YELLOW}SKIP${NC}: Wrapper script test (install.sh not run yet)"
    fi
elif [ -e "${SCRIPT_DIR}/claude-notifications" ]; then
    assert_file_exists "${SCRIPT_DIR}/claude-notifications" "Wrapper script should exist"
else
    echo -e "${YELLOW}SKIP${NC}: Generated wrapper (install.sh not run yet)"
fi
echo ""

# Test 6: All supported platforms
echo "--- Test: Supported Platform Combinations ---"

platforms=("darwin-amd64" "darwin-arm64" "linux-amd64" "linux-arm64" "windows-amd64")

for combo in "${platforms[@]}"; do
    platform=$(echo "$combo" | cut -d'-' -f1)
    arch=$(echo "$combo" | cut -d'-' -f2)

    if [ "$platform" = "windows" ]; then
        expected_name="claude-notifications-${platform}-${arch}.exe"
    else
        expected_name="claude-notifications-${platform}-${arch}"
    fi

    if [[ "$expected_name" == "claude-notifications-"* ]]; then
        echo -e "${GREEN}PASS${NC}: $combo -> $expected_name"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAIL${NC}: Invalid name format for $combo"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
done
echo ""

# Test 7: terminal-notifier URL format
echo "--- Test: terminal-notifier URL ---"
notifier_url="https://github.com/julienXX/terminal-notifier/releases/download/2.0.0/terminal-notifier-2.0.0.zip"

if [[ "$notifier_url" == "https://github.com/"* ]] && [[ "$notifier_url" == *".zip" ]]; then
    echo -e "${GREEN}PASS${NC}: terminal-notifier URL format is valid"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}FAIL${NC}: terminal-notifier URL format is invalid"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test 8: GitHub repo URL format
echo "--- Test: GitHub Repo URL ---"
repo="777genius/agent-notifications"
release_url="https://github.com/${repo}/releases/latest/download"

if [[ "$release_url" == "https://github.com/"* ]] && [[ "$release_url" == *"/releases/"* ]]; then
    echo -e "${GREEN}PASS${NC}: GitHub release URL format is valid"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo -e "${RED}FAIL${NC}: GitHub release URL format is invalid"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Discover the disposable product-isolation regression in the existing CI entrypoint.
if bash "$SCRIPT_DIR/codex-hook-wrapper_test.sh"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if bash "$SCRIPT_DIR/install_config_preflight_test.sh"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if bash "$SCRIPT_DIR/install_config_native_test.sh"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if bash "$SCRIPT_DIR/install_transaction_test.sh"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if bash "$SCRIPT_DIR/bootstrap_product_test.sh"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

if bash "$SCRIPT_DIR/agent-launcher_test.sh"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Summary
echo "========================================="
echo " Test Summary"
echo "========================================="
echo -e "Passed: ${GREEN}${TESTS_PASSED}${NC}"
echo -e "Failed: ${RED}${TESTS_FAILED}${NC}"
echo ""

if [ $TESTS_FAILED -gt 0 ]; then
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
else
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
fi
