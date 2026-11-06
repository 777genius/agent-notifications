#!/bin/bash
# Real final-owner flows with finite offline function peers, no native/agent calls.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
LAB=$(mktemp -d "${TMPDIR:-/tmp}/presentation-TEST-XXXXXX")
trap 'status=$?; if [ "$status" = 0 ]; then rm -rf "$LAB"; else printf "Presentation fixture retained: %s\n" "$LAB" >&2; fi' EXIT
sed '/^main "\$@"$/d' "$ROOT/bin/install.sh" > "$LAB/install-functions.sh"
export LAB INSTALL_TARGET_DIR="$LAB/runtime"
mkdir -p "$INSTALL_TARGET_DIR"
cat > "$LAB/run.sh" <<'OWNER'
#!/bin/bash
set -eu
source "$LAB/install-functions.sh"
abort_if_wsl_environment() { :; }; check_required_tools() { :; }; check_write_permissions() { :; }
detect_platform() { OFFLINE_MODE=false; PLATFORM=linux; ARCH=amd64; BINARY_NAME=claude-notifications-linux-amd64; BINARY_PATH="$INSTALL_TARGET_DIR/$BINARY_NAME"; }
acquire_lock() { :; }; release_lock() { echo 'TEST lock released'; }; create_symlink() { :; }
download_utilities() { :; }; install_linux_notification_desktop_entry() { :; }; install_gnome_activate_window_extension() { return 1; }
cleanup() { :; }; pin_release_urls() { :; }; guard_install_paths() { :; }; verify_executable() { [ "$CASE" != corrupt ]; }
check_existing() { [ "$CASE" = existing ]; }
check_github_availability() {
    if [ "$CASE" = unavailable ]; then return 1; fi
    case "$CASE" in offline|corrupt) OFFLINE_MODE=true ;; esac
    return 0
}
stage_and_promote_runtime() { [ "$CASE" != failure ]; }
main
OWNER
for case in online existing offline failure unavailable corrupt; do
    status=0
    CASE="$case" bash "$LAB/run.sh" > "$LAB/$case.out" 2>&1 || status=$?
    expected=0; case "$case" in failure|unavailable|corrupt) expected=1 ;; esac
    [ "$status" -eq "$expected" ] || { cat "$LAB/$case.out"; exit 1; }
    banners=$(grep -c 'Boost your productivity' "$LAB/$case.out" || true)
    if [ "$expected" = 0 ]; then
        [ "$banners" = 1 ]
        [ "$(tail -n 1 "$LAB/$case.out")" = '----------------------------------------' ]
        grep -q 'TEST lock released' "$LAB/$case.out"
    else [ "$banners" = 0 ]; fi
    echo "PASS standalone install owner $case (status $status, banners $banners)"
done
CASE=online INSTALL_DEFER_PROMOTION=1 bash "$LAB/run.sh" > "$LAB/nested.out" 2>&1
if grep -q 'Boost your productivity' "$LAB/nested.out"; then exit 1; fi
echo 'PASS nested installer defers its banner'

# The bootstrap may hand trusted static text only to the loader's private leaf.
sed '/^main "\$@"$/d' "$ROOT/bin/bootstrap.sh" > "$LAB/bootstrap-functions.sh"
(
    source "$LAB/bootstrap-functions.sh"
    mkdir "$LAB/private"; chmod 700 "$LAB/private"
    BOOTSTRAP_PROMOTION_FILE="$LAB/private/promotion"
    : > "$BOOTSTRAP_PROMOTION_FILE"; chmod 600 "$BOOTSTRAP_PROMOTION_FILE"
    bootstrap_promotion
    [ "$(grep -c 'Boost your productivity' "$BOOTSTRAP_PROMOTION_FILE")" = 1 ]
    echo 'owner canary' > "$BOOTSTRAP_PROMOTION_FILE"
    bootstrap_promotion; [ "$(cat "$BOOTSTRAP_PROMOTION_FILE")" = 'owner canary' ]
    : > "$BOOTSTRAP_PROMOTION_FILE"
    ln "$BOOTSTRAP_PROMOTION_FILE" "$LAB/private/linked"
    bootstrap_promotion; [ ! -s "$LAB/private/linked" ]
    rm "$LAB/private/linked"
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*) : ;; # chmod does not model native Windows ACLs.
        *)
            : > "$BOOTSTRAP_PROMOTION_FILE"; chmod 644 "$BOOTSTRAP_PROMOTION_FILE"
            bootstrap_promotion; [ ! -s "$BOOTSTRAP_PROMOTION_FILE" ]
            chmod 600 "$BOOTSTRAP_PROMOTION_FILE"; chmod 755 "$LAB/private"
            bootstrap_promotion; [ ! -s "$BOOTSTRAP_PROMOTION_FILE" ]
            chmod 700 "$LAB/private" ;;
    esac
    rm "$BOOTSTRAP_PROMOTION_FILE"
    echo 'foreign canary' > "$LAB/foreign"
    ln -s "$LAB/foreign" "$BOOTSTRAP_PROMOTION_FILE"
    bootstrap_promotion; [ "$(cat "$LAB/foreign")" = 'foreign canary' ]
)
echo 'PASS bootstrap promotion private handoff and refusal'

# Regression for #585: a new Claude marketplace cache already contains an
# executable, while the managed ledger still owns the previous version.
# The installer must run its existing verified relocation path, not short-cut
# with "Binary already installed". No actual network or user profile touched.
profile="$LAB/TEST-cache-upgrade"
newroot="$profile/.claude/plugins/cache/claude-notifications-go/claude-notifications-go/1.51.2"
oldroot="$profile/.claude/plugins/cache/claude-notifications-go/claude-notifications-go/1.51.0"
export XDG_CONFIG_HOME="$profile/.config" INSTALL_TARGET_DIR="$newroot/bin"
mkdir -p "$INSTALL_TARGET_DIR" "$XDG_CONFIG_HOME/agent-notifications"
printf '%s\n' 'agent-notifications-managed-writer-protocol-v1' > "$INSTALL_TARGET_DIR/claude-notifications-linux-amd64"
chmod +x "$INSTALL_TARGET_DIR/claude-notifications-linux-amd64"
# A logical cache alias must compare against the physical root in the ledger.
alias_base="$LAB/cache-upgrade-physical"
alias_root="$profile/cache-alias"
alias_new="$alias_root/plugins/cache/claude-notifications-go/claude-notifications-go/1.51.2"
alias_old_physical="$alias_base/plugins/cache/claude-notifications-go/claude-notifications-go/1.51.0"
mkdir -p "$alias_base/plugins/cache/claude-notifications-go/claude-notifications-go/1.51.2/bin"
ln -s "$alias_base" "$alias_root"
printf '%s\n' 'agent-notifications-managed-writer-protocol-v1' > "$alias_new/bin/claude-notifications-linux-amd64"
chmod +x "$alias_new/bin/claude-notifications-linux-amd64"
cat > "$LAB/cache-upgrade.sh" <<'CACHE'
#!/bin/bash
set -eu
source "$LAB/install-functions.sh"
abort_if_wsl_environment() { :; }; check_required_tools() { :; }; check_write_permissions() { :; }
detect_platform() { PLATFORM=linux; ARCH=amd64; BINARY_NAME=claude-notifications-linux-amd64; BINARY_PATH="$SCRIPT_DIR/$BINARY_NAME"; }
acquire_lock() { :; }; release_lock() { :; }; create_symlink() { :; }
download_utilities() { :; }; install_linux_notification_desktop_entry() { :; }; install_gnome_activate_window_extension() { return 1; }
cleanup() { :; }; pin_release_urls() { :; }; guard_install_paths() { :; }; verify_executable() { :; }
desktop_runtime_usable() { :; }; windows_native_hooks_update_required() { return 1; }
check_github_availability() { OFFLINE_MODE=false; [ "$CACHE_CASE" != offline ] || OFFLINE_MODE=true; }
stage_and_promote_runtime() { printf 'VERIFIED-RELOCATE\n' >> "$LAB/upgrade.trace"; }
main
CACHE
for case in stale aliased same malformed foreign offline; do
    : > "$LAB/upgrade.trace"
    export INSTALL_TARGET_DIR="$newroot/bin"
    case "$case" in
        stale|offline) root_in_ledger="$oldroot" ;;
        aliased) INSTALL_TARGET_DIR="$alias_new/bin"; export INSTALL_TARGET_DIR; root_in_ledger="$alias_old_physical" ;;
        same) root_in_ledger="$newroot" ;;
        foreign) root_in_ledger="$profile/other-managed-runtime" ;;
        malformed) root_in_ledger="$oldroot" ;;
    esac
    if [ "$case" = malformed ]; then
        echo '{invalid' > "$XDG_CONFIG_HOME/agent-notifications/ownership.json"
    else
        printf '{"Consumers":{"claude-hooks":{"RuntimeRoot":"%s"}}}\n' "$root_in_ledger" > "$XDG_CONFIG_HOME/agent-notifications/ownership.json"
    fi
    actual=0
    CACHE_CASE="$case" CN_PRODUCT=claude bash "$LAB/cache-upgrade.sh" > "$LAB/upgrade-$case.out" 2>&1 || actual=$?
    case "$case" in
        stale|aliased)
            [ "$actual" = 0 ] && grep -qx VERIFIED-RELOCATE "$LAB/upgrade.trace" || { cat "$LAB/upgrade-$case.out"; echo 'stale Claude cache skipped verified relocation' >&2; exit 1; }
            ;;
        offline)
            [ "$actual" != 0 ] && [ ! -s "$LAB/upgrade.trace" ] &&
                grep -Fq 'A verified release is required to relocate the versioned Claude cache; existing ownership retained.' "$LAB/upgrade-$case.out" ||
                { cat "$LAB/upgrade-$case.out"; echo 'offline stale cache incorrectly reported success' >&2; exit 1; }
            ;;
        *)
            [ "$actual" = 0 ] && [ ! -s "$LAB/upgrade.trace" ] || { cat "$LAB/upgrade-$case.out"; echo "unexpected relocation for $case" >&2; exit 1; }
            ;;
    esac
done
echo 'PASS Linux Claude cache upgrade requires verified relocation; same, malformed, foreign and offline outcomes preserved'
