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
CLAIM_ONLY="${1:-}" CLAIM_SOURCE="${2:-$src/hook-wrapper.sh}" ROOT="$root" SRC="$src" bash <<'RUN'
set -eu
# Release and join only our fixture hooks before the outer sandbox cleanup.
trap ': > "$ROOT/release-install"; for pid in ${pids:-}; do wait "$pid" || true; done' EXIT
cd "$ROOT"
# This fixture exercises the POSIX shared wrapper on every CI host. Native
# commandWindows execution is covered by the Go setup E2E separately.
printf '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n' > stubs/uname
chmod +x stubs/uname
export PATH="$ROOT/stubs:/usr/bin:/bin"
# Reproduce the failed-owner-lookup race at the actual production boundary.
# Only an unsuccessful readlink is delayed; B is a real live claimant process.
# A must dispatch without permission (including permission with an empty claim).
sed -n '/^backoff_path()/,/^# === Main Logic ===/p' "$CLAIM_SOURCE" > claim-functions.sh
mkdir claim-tools
export REAL_READLINK=$(command -v readlink)
cat > claim-tools/readlink <<'CLAIM_READLINK'
#!/bin/sh
if result=$("$REAL_READLINK" "$@" 2>/dev/null); then
 printf '%s\n' "$result"
 exit 0
fi
if [ "${DELAY_MISSING:-0}" = 1 ] && [ "$1" = "$BACKOFF/active" ] &&
   mkdir "$CASE_DIR/read-paused" 2>/dev/null; then
 n=0
 while [ ! -e "$CASE_DIR/resume-read" ]; do
  n=$((n + 1)); [ "$n" -lt 500 ] || exit 98
  sleep 0.02
 done
fi
exit 1
CLAIM_READLINK
cat > claimant.sh <<'CLAIMANT'
#!/bin/sh
. "$ROOT/claim-functions.sh"
SCRIPT_DIR="$CASE_DIR/plugin/bin"
STAMP_DIR="$CASE_DIR/cache"
TARGET_VER=1.42.0
backoff_path "$TARGET_VER"
export BACKOFF
if claim_install; then status=0; else status=$?; fi
printf '%s\n%s\n' "$status" "$INSTALL_CLAIM" > "$CASE_DIR/result-$ROLE"
if [ "$ROLE" = B ]; then
 n=0
 while [ ! -e "$CASE_DIR/release-B" ]; do
  n=$((n + 1)); [ "$n" -lt 500 ] || exit 98
  sleep 0.02
 done
fi
CLAIMANT
chmod +x claim-tools/readlink
claim_barrier() {
 local n=0
 while [ ! -e "$1" ]; do
  n=$((n + 1)); [ "$n" -lt 500 ] || { echo "claim barrier timeout: $1" >&2; return 1; }
  sleep 0.02
 done
}
failed_lookup_race() (
 export CASE_DIR="$ROOT/lookup-$1"
 mkdir -p "$CASE_DIR/plugin/bin" "$CASE_DIR/cache"
 trap ': > "$CASE_DIR/resume-read"; : > "$CASE_DIR/release-B"; wait "${a_pid:-}" 2>/dev/null || true; wait "${b_pid:-}" 2>/dev/null || true' EXIT
 # Seed/retire a failed claim for the expired case, using real functions.
 . "$ROOT/claim-functions.sh"
 SCRIPT_DIR="$CASE_DIR/plugin/bin" STAMP_DIR="$CASE_DIR/cache" TARGET_VER=1.42.0
 backoff_path "$TARGET_VER"
 if [ "$1" = expired ]; then
  claim_install
  : > "$INSTALL_CLAIM/failed"
  touch -t 200001010000 "$INSTALL_CLAIM"
  retire_backoff "$INSTALL_CLAIM"
 fi
 DELAY_MISSING=1 ROLE=A PATH="$ROOT/claim-tools:$PATH" sh claimant.sh &
 a_pid=$!
 claim_barrier "$CASE_DIR/read-paused"
 # A observed no owner. Publish B while A's unsuccessful read is suspended.
 ROLE=B sh claimant.sh &
 b_pid=$!
 claim_barrier "$CASE_DIR/result-B"
 [ "$(sed -n '1p' "$CASE_DIR/result-B")" = 0 ]
 b=$(sed -n '2p' "$CASE_DIR/result-B")
 [ -n "$b" ] && [ -d "$b" ]
 [ "$(cat "$b/pid")" = "$b_pid" ] && kill -0 "$b_pid"
 # An aged live owner must still win over the cooldown timestamp.
 touch -t 200001010000 "$b"
 : > "$CASE_DIR/resume-read"
 wait "$a_pid"
 [ "$(sed -n '1p' "$CASE_DIR/result-A")" = 1 ] || {
  echo "FAIL: $1 failed lookup allowed duplicate installer; A status/claim:" >&2
  cat "$CASE_DIR/result-A" >&2
  return 1
 }
 [ -z "$(sed -n '2p' "$CASE_DIR/result-A")" ]
 [ "$(readlink "$BACKOFF/active")" = "${b##*/}" ]
 [ -d "$b" ] && [ ! -e "$b/retire" ] && kill -0 "$b_pid"
 : > "$CASE_DIR/release-B"
 wait "$b_pid"
 echo "PASS: $1 failed lookup suppresses A; live B survives"
)
lookup_failures=0
for lookup_case in fresh expired; do
 failed_lookup_race "$lookup_case" &
 lookup_pid=$!
 if wait "$lookup_pid"; then :; else lookup_failures=$((lookup_failures + 1)); fi
done
[ "$lookup_failures" = 0 ]
# Stable invalid symlinks still permit best-effort installation and survive it.
(
 . "$ROOT/claim-functions.sh"
 SCRIPT_DIR="$ROOT/foreign-plugin/bin" STAMP_DIR="$ROOT/foreign-cache" TARGET_VER=1.42.0
 backoff_path "$TARGET_VER"
 mkdir -p "$BACKOFF" "$ROOT/foreign-directory"
 echo keep > "$ROOT/foreign-directory/canary"
 ln -s "$ROOT/foreign-directory" "$BACKOFF/attempt.foreign"
 for invalid in ../foreign-directory attempt.missing attempt.foreign; do
  ln -sn "$invalid" "$BACKOFF/active"
  claim_install
  [ -z "$INSTALL_CLAIM" ]
  [ "$(readlink "$BACKOFF/active")" = "$invalid" ]
  [ "$(cat "$ROOT/foreign-directory/canary")" = keep ]
  [ -L "$BACKOFF/attempt.foreign" ]
  rm "$BACKOFF/active"
 done
)
echo 'PASS: invalid/foreign links remain intact with best-effort fallback'
[ "$CLAIM_ONLY" != claim-race ] || exit 0
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
[ -z "$(find "$XDG_CACHE_HOME/claude-notifications-go" -name active -type l -print)" ]
# Exercise post-install Codex cache writes too.
echo 1.41.0 > codex/bin/version
echo legacy-canary > "$XDG_CACHE_HOME/claude-notifications-go/verified-version"
sh codex/bin/codex-hook-wrapper.sh handle-hook Stop --product codex
[ "$(cat "$XDG_CACHE_HOME/claude-notifications-go/verified-version")" = legacy-canary ]
# A failed upgrade must never execute an old binary as a Codex hook.
echo 1.41.0 > codex/bin/version
printf '#!/bin/sh\nexit 1\n' > codex/bin/install.sh
before=$(wc -l < delivered)
sh codex/bin/codex-hook-wrapper.sh handle-hook Stop --product codex > codex-failed.stdout 2> codex-failed.stderr
[ ! -s codex-failed.stdout ]
[ ! -s codex-failed.stderr ]
[ "$(wc -l < delivered)" = "$before" ]
# An absent Windows binary and a failed lazy install must notify once per
# package version; repeated hooks must stay quiet until the version changes.
mkdir -p failed/bin failed/.claude-plugin
cp "$SRC/hook-wrapper.sh" failed/bin/hook-wrapper.sh
echo '{"version":"1.42.0"}' > failed/.claude-plugin/plugin.json
cat > failed/bin/install.sh <<'FAILED_INSTALL'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
exit 7
FAILED_INSTALL
chmod +x failed/bin/install.sh
mkdir -p failed/bin/.install.lock/.owner.test
printf '%s\n' "$$" > failed/bin/.install.lock/.owner.test/pid
: > failed/bin/.install.lock/.owner.test/heartbeat
contended=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
[ -z "$contended" ]
[ -z "$(find "$ROOT/failed-cache" -name active -type l -print)" ]
rm failed/bin/.install.lock/.owner.test/pid failed/bin/.install.lock/.owner.test/heartbeat
rmdir failed/bin/.install.lock/.owner.test failed/bin/.install.lock
# A competing installer can publish the lock directory before its owner.
mkdir failed/bin/.install.lock
(
 sleep 0.2
 mkdir failed/bin/.install.lock/.owner.publishing
 printf '%s\n' "$$" > failed/bin/.install.lock/.owner.publishing/pid
 : > failed/bin/.install.lock/.owner.publishing/heartbeat
) &
publishing_pid=$!
publishing=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
wait "$publishing_pid"
[ -z "$publishing" ]
[ -z "$(find "$ROOT/failed-cache" -name active -type l -print)" ]
rm failed/bin/.install.lock/.owner.publishing/pid failed/bin/.install.lock/.owner.publishing/heartbeat
rmdir failed/bin/.install.lock/.owner.publishing failed/bin/.install.lock
first=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
second=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
case "$first" in *'"systemMessage"'*'Installation of v1.42.0 failed'*) : ;; *) exit 1 ;; esac
[ -z "$second" ]
echo '{"version":"1.42.1"}' > failed/.claude-plugin/plugin.json
third=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
case "$third" in *'"systemMessage"'*'Installation of v1.42.1 failed'*) : ;; *) exit 1 ;; esac
echo '{"version":"1.42.2"}' > failed/.claude-plugin/plugin.json
mkdir failed/bin/.install.lock
abandoned=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
case "$abandoned" in *'"systemMessage"'*'Installation of v1.42.2 failed'*) : ;; *) exit 1 ;; esac
rmdir failed/bin/.install.lock
echo '{"version":"1.42.3"}' > failed/.claude-plugin/plugin.json
mkdir failed/bin/.install.lock
(
 sleep 0.2
 printf '#!/bin/sh\nif [ "$1" = version ]; then echo 1.42.3; else echo ready; fi\n' > failed/bin/claude-notifications-windows-amd64.exe
 chmod +x failed/bin/claude-notifications-windows-amd64.exe
 rmdir failed/bin/.install.lock
) &
winner_pid=$!
winner=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
wait "$winner_pid"
case "$winner" in *'Installation of v1.42.3 failed'*) exit 1 ;; esac
rm failed/bin/claude-notifications-windows-amd64.exe
echo '{"version":"1.42.4"}' > failed/.claude-plugin/plugin.json
mkdir -p failed/bin/.install.lock/.owner.reused
printf '%s\n' "$$" > failed/bin/.install.lock/.owner.reused/pid
: > failed/bin/.install.lock/.owner.reused/heartbeat
touch -t 200001010000 failed/bin/.install.lock/.owner.reused/heartbeat
stale=$(OS=Windows_NT XDG_CACHE_HOME="$ROOT/failed-cache" sh failed/bin/hook-wrapper.sh handle-hook Stop)
case "$stale" in *'"systemMessage"'*'Installation of v1.42.4 failed'*) : ;; *) exit 1 ;; esac
rm failed/bin/.install.lock/.owner.reused/pid failed/bin/.install.lock/.owner.reused/heartbeat
rmdir failed/bin/.install.lock/.owner.reused failed/bin/.install.lock
# Regression: a failed upgrade with a usable older binary must emit one
# stderr diagnostic per target version while continuing to dispatch the old
# Claude hook. On the base wrapper both attempts are silent.
mkdir -p upgrade/bin upgrade/.claude-plugin
cp "$SRC/hook-wrapper.sh" upgrade/bin/hook-wrapper.sh
cat > upgrade/bin/claude-notifications <<'UPGRADE_BINARY'
#!/bin/sh
if [ "$1" = version ]; then
 echo version >> "$ROOT/upgrade-version-probes"
 cat "$(dirname "$0")/version"
else
 cat "$(dirname "$0")/version" >> "$ROOT/delivered"
fi
UPGRADE_BINARY
chmod +x upgrade/bin/claude-notifications
echo 1.41.0 > upgrade/bin/version
echo '{"version":"1.42.0"}' > upgrade/.claude-plugin/plugin.json
cat > upgrade/bin/install.sh <<'FAILED_UPGRADE'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
exit 7
FAILED_UPGRADE
chmod +x upgrade/bin/install.sh
before=$(wc -l < delivered)
XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-first.stdout 2> upgrade-first.stderr
first_probes=$(wc -l < upgrade-version-probes)
mkdir upgrade-stubs
printf '#!/bin/sh\necho sleep >> "$ROOT/upgrade-sleeps"\n' > upgrade-stubs/sleep
chmod +x upgrade-stubs/sleep
PATH="$ROOT/upgrade-stubs:$PATH" XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-second.stdout 2> upgrade-second.stderr
second_probes=$(wc -l < upgrade-version-probes)
[ ! -e upgrade-sleeps ]
[ "$((second_probes - first_probes))" -le 3 ]
[ ! -s upgrade-first.stdout ] && [ ! -s upgrade-second.stdout ]
grep -q 'Installation of v1.42.0 failed' upgrade-first.stderr
[ ! -s upgrade-second.stderr ]
[ -d "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.0" ]
[ "$(cat "$ROOT/upgrade-cache/claude-notifications-go/verified-version")" = 1.41.0 ]
[ "$(wc -l < delivered)" -eq "$((before + 2))" ]
echo '{"version":"1.42.1"}' > upgrade/.claude-plugin/plugin.json
XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-third.stdout 2> upgrade-third.stderr
[ ! -s upgrade-third.stdout ]
grep -q 'Installation of v1.42.1 failed' upgrade-third.stderr
[ "$(wc -l < delivered)" -eq "$((before + 3))" ]
# Regression: a verified working binary retires its version's failure stamp,
# so a later failure of that version is reported again. On the base wrapper
# the stamp outlives the repair and the next failure is silent.
echo '{"version":"1.42.0"}' > upgrade/.claude-plugin/plugin.json
cat > upgrade/bin/install.sh <<'REPAIRED_UPGRADE'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
echo 1.42.0 > "$INSTALL_TARGET_DIR/version"
REPAIRED_UPGRADE
# Manual repair bypasses hook cooldown; the next hook verifies and clears it.
INSTALL_TARGET_DIR="$ROOT/upgrade/bin" sh upgrade/bin/install.sh
XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-repaired.stdout 2> upgrade-repaired.stderr
[ ! -s upgrade-repaired.stdout ]
[ ! -s upgrade-repaired.stderr ]
[ -z "$(find "$ROOT/upgrade-cache" -path "*install-backoff-1.42.0-*/active" -type l -print)" ]
[ "$(cat "$ROOT/upgrade-cache/claude-notifications-go/verified-version")" = 1.42.0 ]
[ ! -d "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.0" ]
[ -d "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.1" ]
mv upgrade/bin/claude-notifications upgrade/bin/claude-notifications.wiped
cat > upgrade/bin/install.sh <<'FAILED_AGAIN'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
exit 7
FAILED_AGAIN
XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-wiped.stdout 2> upgrade-wiped.stderr
grep -q 'Installation of v1.42.0 failed' upgrade-wiped.stdout
[ ! -s upgrade-wiped.stderr ]
[ -d "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.0" ]
mv upgrade/bin/claude-notifications.wiped upgrade/bin/claude-notifications
# A version verified outside the install path retires its stamp as well,
# from the cache-miss probe and from a cache hit.
echo '{"version":"1.42.1"}' > upgrade/.claude-plugin/plugin.json
echo 1.42.1 > upgrade/bin/version
XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-verified.stdout 2> upgrade-verified.stderr
[ ! -s upgrade-verified.stdout ]
[ ! -s upgrade-verified.stderr ]
[ "$(cat "$ROOT/upgrade-cache/claude-notifications-go/verified-version")" = 1.42.1 ]
[ ! -d "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.1" ]
mkdir "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.1"
XDG_CACHE_HOME="$ROOT/upgrade-cache" sh upgrade/bin/hook-wrapper.sh handle-hook Stop > upgrade-cached.stdout 2> upgrade-cached.stderr
[ ! -s upgrade-cached.stdout ]
[ ! -s upgrade-cached.stderr ]
[ ! -d "$ROOT/upgrade-cache/claude-notifications-go/install-failed-1.42.1" ]
# Regression: a concurrent installer that removes its lock just before
# publishing a working binary must leave no failure message and must dispatch
# that binary. On the base wrapper the lock-free gap emits a false failure.
mkdir -p racing/bin racing/.claude-plugin
cp "$SRC/hook-wrapper.sh" racing/bin/hook-wrapper.sh
echo '{"version":"1.42.0"}' > racing/.claude-plugin/plugin.json
cat > race-new-binary <<'RACE_BINARY'
#!/bin/sh
if [ "$1" = version ]; then echo 1.42.0; else echo published >> "$ROOT/race-delivered"; fi
RACE_BINARY
chmod +x race-new-binary
cat > racing/bin/install.sh <<'RACE_INSTALL'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
mkdir "$INSTALL_TARGET_DIR/.install.lock"
(
 rmdir "$INSTALL_TARGET_DIR/.install.lock"
 : > "$ROOT/race-lock-gone"
 sleep 0.8
 mv "$ROOT/race-new-binary" "$INSTALL_TARGET_DIR/claude-notifications"
) &
while [ ! -e "$ROOT/race-lock-gone" ]; do sleep 0.01; done
exit 7
RACE_INSTALL
chmod +x racing/bin/install.sh
XDG_CACHE_HOME="$ROOT/race-cache" sh racing/bin/hook-wrapper.sh handle-hook Stop > race.stdout 2> race.stderr
[ ! -s race.stdout ] && [ ! -s race.stderr ]
[ "$(cat race-delivered)" = published ]
[ "$(cat "$ROOT/race-cache/claude-notifications-go/verified-version")" = 1.42.0 ]
[ ! -d "$ROOT/race-cache/claude-notifications-go/install-failed-1.42.0" ]
# Behavioral backoff regression: actual wrappers, real filesystem and clock.
# The installer barrier holds the winner until all seven other hooks dispatch.
# Plain ln -s follows active and makes these counts exceed one.
mkdir -p cooldown/bin cooldown/.claude-plugin
cp "$SRC/hook-wrapper.sh" cooldown/bin/
cp upgrade/bin/claude-notifications cooldown/bin/
echo 1.41.0 > cooldown/bin/version
echo '{"version":"1.42.0"}' > cooldown/.claude-plugin/plugin.json
cat > cooldown/bin/install.sh <<'COOLDOWN_INSTALL'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
echo attempt >> "$ROOT/cooldown-attempts"
if [ -e "$ROOT/hold-install" ]; then
 : > "$ROOT/installer-entered"
 while [ ! -e "$ROOT/release-install" ]; do sleep 0.02; done
fi
exit 7
COOLDOWN_INSTALL
chmod +x cooldown/bin/install.sh
cool_hook() {
 XDG_CACHE_HOME="$ROOT/cooldown-cache" sh cooldown/bin/hook-wrapper.sh handle-hook Stop
}
cool_claim="$ROOT/cooldown-cache/claude-notifications-go/install-backoff-1.42.0-$(printf %s "$ROOT/cooldown/bin" | cksum | cut -d' ' -f1)"
wait_file() {
 n=0
 while [ ! -e "$1" ]; do
  n=$((n + 1)); [ "$n" -lt 3000 ] || { echo "barrier timeout: $1" >&2; exit 1; }
  sleep 0.02
 done
}
parallel_claim() {
 rm -f installer-entered release-install
 rm -rf cooldown-done
 mkdir cooldown-done
 : > hold-install
 pids=''
 for i in 1 2 3 4 5 6 7 8; do
  (cool_hook > "cooldown-$i.out" 2> "cooldown-$i.err"; : > "cooldown-done/$i") &
  pids="$pids $!"
 done
 wait_file installer-entered
 # Force a running install's cache mtime beyond the cooldown: PID ownership
 # must protect it even while a second wave arrives.
 owner=$(readlink "$cool_claim/active")
 [ -d "$cool_claim/$owner" ]
 touch -t 200001010000 "$cool_claim/$owner"
 n=0
 while [ "$(find cooldown-done -type f | wc -l)" -lt 7 ]; do
  n=$((n + 1)); [ "$n" -lt 3000 ] || { echo 'concurrent claim failed' >&2; cat cooldown-attempts >&2; ls -la "$cool_claim" "$cool_claim/"* >&2; cat cooldown-*.err >&2; : > release-install; exit 1; }
  sleep 0.02
 done
 [ "$(wc -l < cooldown-attempts)" -eq "$expected_attempts" ]
 cool_hook > cooldown-long.out 2> cooldown-long.err
 [ "$(wc -l < cooldown-attempts)" -eq "$expected_attempts" ]
 : > release-install
 for pid in $pids; do wait "$pid"; done
 rm hold-install
 [ "$(wc -l < cooldown-attempts)" -eq "$expected_attempts" ]
 # No nested links may have been created inside the active attempt.
 [ "$(find "$cool_claim" -type l | wc -l)" -eq 1 ] || { echo "active link was dereferenced: nested claims created" >&2; exit 1; }
}
before=$(wc -l < delivered)
expected_attempts=1
parallel_claim
for i in 1 2 3 4 5 6 7 8 9 10; do cool_hook > "cooldown-seq-$i.out" 2> "cooldown-seq-$i.err"; done
[ "$(wc -l < cooldown-attempts)" -eq 1 ]
[ "$(wc -l < delivered)" -eq "$((before + 19))" ]
[ "$(cat cooldown-*.err | grep -c 'Installation of v1.42.0 failed')" -eq 1 ]
# Eight simultaneous expired claim takeovers must still elect only one winner.
owner=$(readlink "$cool_claim/active")
touch -t 200001010000 "$cool_claim/$owner"
expected_attempts=2
parallel_claim
# Future mtime is invalid, so it permits exactly one immediate retry.
owner=$(readlink "$cool_claim/active")
touch -t 209901010000 "$cool_claim/$owner"
cool_hook > cooldown-future.out 2> cooldown-future.err
[ "$(wc -l < cooldown-attempts)" -eq 3 ]
# Corrupt namespaces and foreign active entries fall back without deleting them.
rm -rf "$cool_claim"
printf corrupt > "$cool_claim"
cool_hook > cooldown-corrupt.out 2> cooldown-corrupt.err
[ "$(cat "$cool_claim")" = corrupt ]
[ "$(wc -l < cooldown-attempts)" -eq 4 ]
rm "$cool_claim"
mkdir "$cool_claim"
printf foreign > "$cool_claim/active"
cool_hook > cooldown-foreign.out 2> cooldown-foreign.err
[ "$(cat "$cool_claim/active")" = foreign ]
[ "$(wc -l < cooldown-attempts)" -eq 5 ]
rm -rf "$cool_claim"
# An unavailable cache (regular file ancestor, independent of root privileges)
# must run the installer, preserving old-binary dispatch.
printf blocked > blocked-cache
XDG_CACHE_HOME="$ROOT/blocked-cache" sh cooldown/bin/hook-wrapper.sh handle-hook Stop > cooldown-blocked.out 2> cooldown-blocked.err
[ "$(wc -l < cooldown-attempts)" -eq 6 ]
# Version and install root changes each permit an immediate attempt.
cool_hook > cooldown-new.out 2> cooldown-new.err
[ "$(wc -l < cooldown-attempts)" -eq 7 ]
echo '{"version":"1.42.1"}' > cooldown/.claude-plugin/plugin.json
cool_hook > cooldown-version.out 2> cooldown-version.err
[ "$(wc -l < cooldown-attempts)" -eq 8 ]
cp -R cooldown other-root
XDG_CACHE_HOME="$ROOT/cooldown-cache" sh other-root/bin/hook-wrapper.sh handle-hook Stop > cooldown-root.out 2> cooldown-root.err
[ "$(wc -l < cooldown-attempts)" -eq 9 ]
# Product isolation retains the Codex minimum-version guard and quiet output.
CN_PRODUCT=codex XDG_CACHE_HOME="$ROOT/cooldown-cache" sh cooldown/bin/hook-wrapper.sh handle-hook Stop > cooldown-codex.out 2> cooldown-codex.err
[ "$(wc -l < cooldown-attempts)" -eq 10 ]
[ ! -s cooldown-codex.out ] && [ ! -s cooldown-codex.err ]
# A nominal zero exit without the promised version is still a failed attempt.
echo '{"version":"1.42.2"}' > cooldown/.claude-plugin/plugin.json
cat > cooldown/bin/install.sh <<'EMPTY_SUCCESS'
#!/bin/sh
# agent-notifications-managed-writer-protocol-v1
echo attempt >> "$ROOT/cooldown-attempts"
exit 0
EMPTY_SUCCESS
cool_hook > cooldown-empty-success.out 2> cooldown-empty-success.err
cool_hook > cooldown-empty-repeat.out 2> cooldown-empty-repeat.err
[ "$(wc -l < cooldown-attempts)" -eq 11 ]
grep -q 'Installation of v1.42.2 failed' cooldown-empty-success.err
[ ! -s cooldown-empty-repeat.err ]
# Deterministic delayed-reader regression for the expired-takeover ABA race:
# capture A, retire A, install B, then let A's delayed retirement resume.
# Source only the actual production function boundary, with real namespaces.
(
 sed -n '/^backoff_path()/,/^# === Main Logic ===/p' "$SRC/hook-wrapper.sh" > "$ROOT/claim-functions.sh"
 . "$ROOT/claim-functions.sh"
 BACKOFF="$ROOT/delayed-claim"
 mkdir "$BACKOFF"
 a=$(mktemp -d "$BACKOFF/attempt.XXXXXXXXXX")
 ln -sn "${a##*/}" "$BACKOFF/active"
 backoff_owner
 observed="$BACKOFF_OWNER"
 retire_backoff "$observed"
 b=$(mktemp -d "$BACKOFF/attempt.XXXXXXXXXX")
 ln -sn "${b##*/}" "$BACKOFF/active"
 if retire_backoff "$observed"; then echo 'delayed expiry stole successor claim' >&2; exit 1; fi
 [ "$(readlink "$BACKOFF/active")" = "${b##*/}" ]
 [ -d "$b" ] && [ ! -e "$b/retire" ]
 # Likewise a delayed installer completion cannot retire the new owner.
 INSTALL_CLAIM="$observed"
 finish_install_claim
 [ "$(readlink "$BACKOFF/active")" = "${b##*/}" ]
)
echo 'PASS: fresh/expired concurrent claims, live owner, cooldown, corruption, isolation and retained dispatch'

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

# Focused runs leave the independently owned diagnostics to integration.
case "${1:-}" in claim-race|wrapper-only) exit 0 ;; esac

# Keep the independent diagnostics suite, after the owned behavioral cases so
# any legacy diagnostics expectation cannot prevent concurrency validation.
bash "$src/hook-wrapper-logs_test.sh"
