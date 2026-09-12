#!/bin/bash
# Offline adapter boundary spies; no native UI, downloads, or real profiles.
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd)
sandbox=$(mktemp -d)
trap 'rm -rf "$sandbox"' EXIT
export HOME="$sandbox/home" XDG_CONFIG_HOME="$sandbox/config" XDG_CACHE_HOME="$sandbox/cache"
export XDG_DATA_HOME="$sandbox/data" TMPDIR="$sandbox" USERPROFILE="$HOME"
mkdir -p "$HOME" "$sandbox/bin"
export INSTALL_TARGET_DIR="$sandbox/bin" ADAPTER_SPY="$sandbox/executed"
sed '/^main "\$@"$/d' "$root/install.sh" > "$sandbox/functions.sh"
(
 source "$sandbox/functions.sh"
 PLATFORM=windows FORCE_UPDATE=false
 BINARY_NAME=claude-notifications-windows-amd64.exe
 BINARY_PATH="$SCRIPT_DIR/$BINARY_NAME"
 printf '#!/bin/sh\nprintf executed >> "$ADAPTER_SPY"\n' > "$BINARY_PATH"
 chmod +x "$BINARY_PATH"
 desktop_runtime_usable() { return 0; }
 if windows_native_hooks_json; then exit 1; fi
 if check_existing; then exit 1; fi
 if refresh_existing_runtime; then exit 1; fi
 [ ! -e "$ADAPTER_SPY" ]
)
echo 'PASS: Windows probe/check-existing/refresh reject old writer with zero execution'
# Exercise the complete production wrapper with an old update script and no sender.
cp "$root/hook-wrapper.sh" "$sandbox/bin/hook-wrapper.sh"
printf '#!/bin/sh\nprintf executed >> "$ADAPTER_SPY"\n' > "$sandbox/bin/install.sh"
chmod +x "$sandbox/bin/install.sh"
sh "$sandbox/bin/hook-wrapper.sh" stop
[ ! -e "$ADAPTER_SPY" ]
echo 'PASS: actual hook wrapper refuses historical installer delegation'
# Ordinary legacy hook dispatch remains available, with no writer marker required.
printf '#!/bin/sh\nprintf "%%s\\n" "$*" >> "$ADAPTER_SPY"\n' > "$sandbox/bin/claude-notifications"
chmod +x "$sandbox/bin/claude-notifications"
sh "$sandbox/bin/hook-wrapper.sh" stop
 grep -qx stop "$ADAPTER_SPY"
echo 'PASS: ordinary hook dispatch preserved'
# Exercise actual production staging with an old executable and supplied modern
# package. Only downloads and the commit executable are inert fixture seams.
(
 export INSTALL_TARGET_DIR="$sandbox/native"
 mkdir -p "$INSTALL_TARGET_DIR"
 source "$sandbox/functions.sh"
 detect_platform() {
  PLATFORM=darwin ARCH=amd64 BINARY_NAME=claude-notifications-darwin-amd64
  BINARY_PATH="$SCRIPT_DIR/$BINARY_NAME"
 }
 detect_platform
 old="$SCRIPT_DIR/terminal-notifier.app/Contents/MacOS/terminal-notifier"
 mkdir -p "$(dirname "$old")"
 printf '#!/bin/sh\nprintf old-native >> "$ADAPTER_SPY"\n' > "$old"
 chmod +x "$old"
 download_and_verify_binary() {
  cat > "$BINARY_PATH" <<'PAYLOAD'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
[ "$1" != --version ] || { echo claude-notifications-1.0.0; exit 0; }
[ "$1" = internal-install-runtime ] || exit 2
shift
stage= required=false
while [ "$#" -gt 0 ]; do
 case "$1" in
  --stage) stage=$2; shift 2 ;;
  --require-native) required=true; shift ;;
  *) shift ;;
 esac
done
[ "$required" = true ] || exit 3
[ "$(cat "$stage/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern")" = supplied-new-native ] || exit 4
[ -f "$stage/ClaudeNotifier.app.managed-runtime.json" ] || exit 5
echo 'managed-runtime committed generation=1'
PAYLOAD
  chmod +x "$BINARY_PATH"
 }
 # Keep the real offline writer check, omit unrelated version-size heuristics.
 verify_executable() { LC_ALL=C grep -aqF agent-notifications-managed-writer-protocol-v1 "$BINARY_PATH"; }
 download_terminal_notifier_modern() {
  mkdir -p "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS"
  printf supplied-new-native > "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
  chmod +x "$SCRIPT_DIR/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
  printf supplied-external-attestation > "$SCRIPT_DIR/ClaudeNotifier.app.managed-runtime.json"
 }
 download_terminal_notifier() { echo 'legacy fallback must not run' >&2; exit 9; }
 rm "$ADAPTER_SPY"
 stage_and_promote_runtime
 [ ! -e "$ADAPTER_SPY" ]
 grep -q old-native "$old"
 # Missing required release must fail, retaining the old concrete helper.
 download_terminal_notifier_modern() { return 1; }
 if stage_and_promote_runtime; then exit 1; fi
 [ ! -e "$ADAPTER_SPY" ]
 grep -q old-native "$old"
)
echo 'PASS: old executable does not suppress newer staged release; unavailable promotion fails closed'
