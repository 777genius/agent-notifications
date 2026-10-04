#!/bin/bash
# Real LaunchServices E2E, exclusively on a disposable GitHub macOS runner.
# Base RED: private staging is indexed, published generation is never indexed.
set -eo pipefail
src=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$src/test-env.sh"
TEST_ENV_HANDOFF_GOMODCACHE=1
if [ "${_TEST_ENV_READY:-}" != 1 ]; then
    if [ "$(uname -s)" != Darwin ] || [ "${GITHUB_ACTIONS:-}" != true ] || [ -z "${RUNNER_TEMP:-}" ]; then
        echo 'SKIP: real LaunchServices requires an ephemeral GitHub macOS runner'
        exit 0
    fi
    test_env_enter "$0" --runner-temp "$RUNNER_TEMP"
fi
[ "$1" = --runner-temp ] && [ -d "$2" ] || exit 1
runner=$(cd "$2" && pwd -P)
root=$(mktemp -d "$runner/an-launchservices-XXXXXX")
root=$(cd "$root" && pwd -P)
lsregister=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
cleanup() {
    local result=$? path
    if [ "$result" != 0 ]; then
        for path in "$root"/*.log "$root"/*-urls "$root/urls"; do
            [ ! -f "$path" ] || { printf '\nFixture evidence: %s\n' "$path"; cat "$path"; }
        done
    fi
    # Unregister only this fixture's unique ID and paths. Base may register a
    # stage subsequently removed by its own cleanup. Reconstruct only our app
    # at that owned path to let lsregister read its identifier for unregister.
    if [ -f "$root/observed-app-paths" ]; then
        while IFS= read -r path; do
            case "$path" in "$root"/*/ClaudeNotifier.app) ;; *) continue;; esac
            if [ ! -d "$path" ] && [ -d "$root/release/ClaudeNotifier.app" ]; then
                mkdir -p "$(dirname "$path")"
                cp -R "$root/release/ClaudeNotifier.app" "$path"
            fi
            "$lsregister" -u "$path" >/dev/null 2>&1 || true
        done < "$root/observed-app-paths"
    fi
    # Includes retained generations from the two successful publications.
    if [ -d "$root" ]; then
        while IFS= read -r path; do "$lsregister" -u "$path" >/dev/null 2>&1 || true; done < <(find "$root" -type d -name '*.app')
        rm -rf "$root"
    fi
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
test_env_setup "$root"
export E2E_CONTROL_ROOT="$HOME/Library/Application Support/agent-notifications"
mkdir -p "$E2E_CONTROL_ROOT"
chmod 700 "$E2E_CONTROL_ROOT"
for name in HOME XDG_CACHE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_RUNTIME_DIR TMPDIR CLAUDE_CONFIG_DIR E2E_CONTROL_ROOT; do
    physical=$(cd "${!name}" && pwd -P)
    case "$physical" in "$root"/*) ;; *) echo "unsafe $name" >&2; exit 1;; esac
done
export HTTP_PROXY=http://127.0.0.1:1 HTTPS_PROXY=http://127.0.0.1:1 ALL_PROXY=http://127.0.0.1:1
export http_proxy=$HTTP_PROXY https_proxy=$HTTPS_PROXY GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOTELEMETRY=off
repo=$(cd "$src/.." && pwd)
release="$root/release"
mkdir -p "$release/ClaudeNotifier.app/Contents/MacOS" "$release/ClaudeNotifier.app/Contents/Resources" "$root/live"
arch=$(uname -m); [ "$arch" != x86_64 ] || arch=amd64
entry="claude-notifications-darwin-$arch"
(cd "$repo"; go build -o "$release/$entry" ./cmd/claude-notifications)
id="com.777genius.agent-notifications.e2e.$(basename "$root")"
cat > "$root/native.c" <<'C'
#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
 if (argc == 2 && strcmp(argv[1], "--capabilities-json") == 0) {
  puts("{\"schemaVersion\":1,\"protocolVersions\":[1],\"actionKinds\":[\"none\"],\"receiptSupport\":true,\"backend\":\"macos.usernotifications\",\"explicitFeatureEnabledByDefault\":false}");
  return 0;
 }
 return 97; /* No delivery, OS authorization or app launch. */
}
C
cc "$root/native.c" -o "$release/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"
cat > "$release/ClaudeNotifier.app/Contents/Info.plist" <<PLIST
<?xml version="1.0"?><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>$id</string>
<key>CFBundleExecutable</key><string>terminal-notifier-modern</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
</dict></plist>
PLIST
printf '%s\n' '{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}' > "$release/ClaudeNotifier.app/Contents/Resources/managed-runtime.json"
package_app() {
    codesign --force --sign - --timestamp=none --identifier com.777genius.agent-notifications "$release/ClaudeNotifier.app"
    local hash
    hash=$(shasum -a 256 "$release/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern" | awk '{print $1}')
    printf '{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"%s"}\n' "$hash" > "$release/ClaudeNotifier.app.managed-runtime.json"
    rm -f "$release/ClaudeNotifier.app.zip"
    (cd "$release"; zip -qr ClaudeNotifier.app.zip ClaudeNotifier.app ClaudeNotifier.app.managed-runtime.json)
    (cd "$release"; shasum -a 256 "$entry" ClaudeNotifier.app.zip > checksums.txt)
}
package_app
cat > "$root/query.c" <<'C'
#include <CoreServices/CoreServices.h>
#include <stdio.h>
int main(int argc, char **argv) {
 if (argc != 2) return 2;
 CFStringRef id = CFStringCreateWithCString(NULL, argv[1], kCFStringEncodingUTF8);
 CFArrayRef urls = LSCopyApplicationURLsForBundleIdentifier(id, NULL);
 CFRelease(id);
 if (!urls) return 0;
 for (CFIndex i=0; i<CFArrayGetCount(urls); i++) {
  char path[4096];
  if (CFURLGetFileSystemRepresentation(CFArrayGetValueAtIndex(urls, i), true, (UInt8 *)path, sizeof(path))) puts(path);
 }
 CFRelease(urls);
 return 0;
}
C
cc "$root/query.c" -framework CoreServices -o "$root/query"
# The production sourcing idiom used by install_transaction_test.sh. No
# registration, publication, guard, checksum or signature function is replaced.
sed '/^main "\$@"$/d' "$src/install.sh" > "$root/functions.sh"
export INSTALL_TARGET_DIR="$root/live" INSTALL_STAGED_ASSETS="$release"
source "$root/functions.sh"
trap cleanup EXIT
RELEASE_URL=https://fixture.invalid/release
MODERN_NOTIFIER_URL="$RELEASE_URL/ClaudeNotifier.app.zip"
CHECKSUMS_URL="$RELEASE_URL/checksums.txt"
MAX_RETRIES=1 RETRY_DELAY=0
curl() {
    local url='' out='' status=false
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -o) out=$2; shift 2;;
            -w) status=true; shift 2;;
            --connect-timeout|--max-time) shift 2;;
            https://fixture.invalid/release/*) url=$1; shift;;
            --help) return 0;;
            *) shift;;
        esac
    done
    case "$url" in "$RELEASE_URL/ClaudeNotifier.app.zip"|"$RELEASE_URL/checksums.txt"|"$RELEASE_URL/$entry") ;; *) echo 'outbound-attempt' >> "$root/outbound"; return 97;; esac
    [ -n "$out" ] || return 97
    if [[ "$url" == */ClaudeNotifier.app.zip ]]; then printf '%s\n' "$SCRIPT_DIR/ClaudeNotifier.app" >> "$root/observed-app-paths"; fi
    cp "$release/${url##*/}" "$out"
    [ "$status" != true ] || printf 200
    return 0
}
# Private-only acquisition must have no real registration side effect.
private=$(mktemp -d "$root/live/.install-stage.XXXXXX")
(SCRIPT_DIR="$private"; INSTALL_PRIVATE_DOWNLOAD=true; detect_platform; download_checksums; download_terminal_notifier_modern) > "$root/private.log" 2>&1
"$root/query" "$id" > "$root/private-urls"
failed=0
# The release fixture can share this ID in LaunchServices. Only paths inside
# our controlled private acquisition stage prove forbidden stage registration.
if grep -Fq "$private/" "$root/private-urls"; then echo 'FAIL: private staged download registered in LaunchServices'; cat "$root/private-urls"; failed=$((failed+1)); else echo 'PASS: private stage has no registration'; fi
# Make the next registration observation independent of the private-only check.
"$lsregister" -u "$private/ClaudeNotifier.app" >/dev/null 2>&1 || true
rm -rf "$private"
detect_platform
stage_and_promote_runtime > "$root/promote.log" 2>&1
published=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["Native"]["Path"])' "$E2E_CONTROL_ROOT/ownership.json")
case "$published" in "$root"/*) ;; *) echo 'unsafe native path'; exit 1;; esac
printf '%s\n' "$published" >> "$root/observed-app-paths"
# Poll the actual database, bounded. No manual registration can make this green.
n=0
while :; do
    "$root/query" "$id" > "$root/urls"
    if grep -Fxq "$published" "$root/urls"; then break; fi
    n=$((n+1)); [ "$n" -lt 50 ] || break; sleep 0.1
done
if grep -Fxq "$published" "$root/urls"; then echo 'PASS: published generation registered'; else echo 'FAIL: committed generation absent from LaunchServices'; cat "$root/promote.log"; failed=$((failed+1)); fi
if grep -Eq '/\.install-stage\.|/\.candidate-' "$root/urls"; then echo 'FAIL: LaunchServices contains private staging'; cat "$root/urls"; failed=$((failed+1)); fi
"$lsregister" -dump > "$root/ls-dump"
# Check the independent dump for our unique identifier and exact durable path.
if ! python3 -I - "$root/ls-dump" "$id" "$published" <<'PY'
import re,sys
text=open(sys.argv[1]).read()
blocks=[b for b in re.split(r'(?m)^-+\s*$',text) if sys.argv[2] in b]
assert blocks, 'unique app missing from lsregister dump'
assert any(sys.argv[3] in b for b in blocks), 'published path missing from dump'
assert not any('/.install-stage.' in b or '/.candidate-' in b for b in blocks), 'private path indexed in dump'
PY
then failed=$((failed+1)); fi
# Upgrade creates a new durable generation; the original callback path stays.
printf generation-B > "$release/ClaudeNotifier.app/Contents/Resources/generation.marker"
package_app
stage_and_promote_runtime > "$root/update.log" 2>&1
second=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["Native"]["Path"])' "$E2E_CONTROL_ROOT/ownership.json")
[ "$second" != "$published" ] && [ -d "$published" ]
printf '%s\n' "$second" >> "$root/observed-app-paths"
n=0
while :; do
    "$root/query" "$id" > "$root/update-urls"
    if grep -Fxq "$second" "$root/update-urls"; then break; fi
    n=$((n+1)); [ "$n" -lt 50 ] || break; sleep 0.1
done
if grep -Fxq "$published" "$root/update-urls" && grep -Fxq "$second" "$root/update-urls" && ! grep -Eq '/\.install-stage\.|/\.candidate-' "$root/update-urls"; then echo 'PASS: update retains both registered generation paths'; else echo 'FAIL: registered generation retention'; cat "$root/update-urls"; failed=$((failed+1)); fi
# Disposable acquisition must not Commit or register its copied app.
SCRIPT_DIR="$root/disposable"; mkdir "$SCRIPT_DIR"
INSTALL_DISPOSABLE_ACQUISITION=true CN_PRODUCT=codex
before=$(shasum -a 256 "$E2E_CONTROL_ROOT/ownership.json")
stage_and_promote_runtime > "$root/disposable.log" 2>&1
[ "$(shasum -a 256 "$E2E_CONTROL_ROOT/ownership.json")" = "$before" ]
"$root/query" "$id" > "$root/disposable-urls"
if grep -Fq "$root/disposable" "$root/disposable-urls"; then echo 'FAIL: disposable acquisition registered'; failed=$((failed+1)); else echo 'PASS: disposable acquisition has no registration'; fi
[ ! -e "$root/outbound" ]
[ "$failed" = 0 ]
