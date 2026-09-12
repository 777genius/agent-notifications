#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
# Disposable offline wrapper + installer regression, also called by install_test.sh.
set -eu
src=$(cd "$(dirname "$0")" && pwd)
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
test_env_setup "$root"
mkdir -p "$root/stubs"
# The suite already entered an allowlist environment above.
ROOT="$root" SRC="$src" bash <<'RUN'
set -eu
cd "$ROOT"
# This fixture exercises the POSIX shared wrapper on every CI host. Native
# commandWindows execution is covered by the Go setup E2E separately.
printf '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n' > stubs/uname
chmod +x stubs/uname
export PATH="$ROOT/stubs:/usr/bin:/bin"
for product in claude codex; do
 mkdir -p "$product/bin" "$product/.claude-plugin"
 cp "$SRC/hook-wrapper.sh" "$product/bin/"
 echo '{"version":"1.42.0"}' > "$product/.claude-plugin/plugin.json"
 cat > "$product/bin/claude-notifications" <<'BIN'
#!/bin/sh
if [ "$1" = version ]; then cat "$(dirname "$0")/version"; else cat "$(dirname "$0")/version" >> "$ROOT/delivered"; fi
BIN
 cat > "$product/bin/install.sh" <<'INSTALL'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
echo install >> "$ROOT/installs"
echo 1.42.0 > "$INSTALL_TARGET_DIR/version"
INSTALL
 chmod +x "$product/bin/"*
done
cp "$SRC/codex-hook-wrapper.sh" codex/bin/
echo 1.41.0 > claude/bin/version
echo 1.42.0 > codex/bin/version
mkdir -p "$XDG_CACHE_HOME/claude-notifications-go"
echo 1.41.0 > "$XDG_CACHE_HOME/claude-notifications-go/verified-version"
sh codex/bin/codex-hook-wrapper.sh handle-hook Stop --product codex
[ "$(cat "$XDG_CACHE_HOME/claude-notifications-go/verified-version")" = 1.41.0 ]
[ ! -e "$HOME/.claude" ]
sh claude/bin/hook-wrapper.sh handle-hook Stop
[ "$(cat installs)" = install ]
[ "$(cat claude/bin/version)" = 1.42.0 ]
# Exercise post-install Codex cache writes too.
echo 1.41.0 > codex/bin/version
echo legacy-canary > "$XDG_CACHE_HOME/claude-notifications-go/verified-version"
sh codex/bin/codex-hook-wrapper.sh handle-hook Stop --product codex
[ "$(cat "$XDG_CACHE_HOME/claude-notifications-go/verified-version")" = legacy-canary ]
# A failed upgrade must never execute an old binary as a Codex hook.
echo 1.41.0 > codex/bin/version
printf '#!/bin/sh\nexit 1\n' > codex/bin/install.sh
before=$(wc -l < delivered)
sh codex/bin/codex-hook-wrapper.sh handle-hook Stop --product codex
[ "$(wc -l < delivered)" = "$before" ]
# Source actual installer functions, substituting local download/OS integration
# seams; execute the real main flow and real venv setup on both main branches.
sed '$d' "$SRC/install.sh" > installer-functions.sh
cat > stubs/uname <<'STUB'
#!/bin/sh
case "$1" in -s) echo Darwin;; -m) echo arm64;; esac
STUB
cat > stubs/python3 <<'STUB'
#!/bin/sh
echo python >> "$ROOT/python-called"
mkdir -p "$3/bin"
printf '#!/bin/sh\nexit 1\n' > "$3/bin/pip"
chmod +x "$3/bin/pip"
STUB
printf '#!/bin/sh\nexit 0\n' > stubs/tmux
for command in curl wget; do
 printf '#!/bin/sh\nexit 97\n' > "stubs/$command"
done
chmod +x stubs/*
export PATH="$ROOT/stubs:/usr/bin:/bin" TERM_PROGRAM=iTerm.app
export INSTALL_TARGET_DIR="$ROOT/codex/bin"
source installer-functions.sh
abort_if_wsl_environment() { :; }
check_required_tools() { :; }
check_write_permissions() { :; }
acquire_lock() { :; }
detect_platform() { PLATFORM=darwin; ARCH=arm64; BINARY_NAME=fixture; CHECKSUMS_PATH="$ROOT/checksums"; }
check_github_availability() { OFFLINE_MODE=false; }
check_existing() { [ "$EXISTING" = yes ]; }
pin_release_urls() { :; }
download_and_verify_binary() { echo downloaded >> "$ROOT/downloads"; }
stage_and_promote_runtime() { download_and_verify_binary; }
verify_executable() { :; }
make_executable() { :; }
create_symlink() { :; }
configure_windows_native_hooks() { :; }
download_utilities() { :; }
download_terminal_notifier_modern() { :; }
create_claude_notifications_app() { :; }
venv="$HOME/.claude/claude-notifications-go/iterm2-venv"
mkdir -p "$venv"
echo keep > "$venv/canary"
export CN_PRODUCT=codex
for EXISTING in yes no; do main >/dev/null; done
[ "$(cat "$venv/canary")" = keep ]
[ ! -e "$ROOT/python-called" ]
[ "$(cat "$ROOT/downloads")" = downloaded ]
rm -rf "$venv"
main >/dev/null
[ ! -e "$venv" ]
# Positive Claude control: failed private pip preserves the broken live tree.
unset CN_PRODUCT
mkdir -p "$venv"
echo keep > "$venv/canary"
main >/dev/null
[ -f "$ROOT/python-called" ]
[ "$(cat "$venv/canary")" = keep ]
echo 'PASS: isolated Codex cache and installer regressions'
RUN
