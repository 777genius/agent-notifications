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
