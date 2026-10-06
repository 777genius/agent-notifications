# Offline TEST handoff factory checkpoint

The guest controller preparation remains in PR #360. This dependent scope owns
the host assembly, explicit single attempt, completion frame and host cleanup.
It must not remove the guest's early refusal until the complete assembly passes
review. Native completion and selected-chat navigation remain unqualified.

## Runtime staging

`navigation-linux-handoff-runtime-stage.py` authenticates both retained runtime
TAR bytes against the prior positive evidence, validates every member before
filesystem writes, and writes regular files manually into a fresh TEST tree.
It rejects links, special files, absolute/traversal/noncanonical paths, normalized
duplicates, file ancestors and bounded-size violations. Executable byte hashes
must match; permissions are normalized to read-only files and directories.

Five pure tests cover observable archive failures and rejection before destination
creation. A separate file-only replay staged both retained archives into a new
temporary TEST directory and verified 148 file hashes, read-only modes and rejection
of destination reuse. No runtime was loaded or executed and no VM was booted.

The caller must own a private parent, preserve failed staging as a failed attempt
and never treat a partial tree as ready. Same-UID adversarial immutability is not
claimed. Host QEMU wiring, the nine-source manifest, ISO/cloud-init installation,
new-marker completion reader and complete assembly review were outstanding at
the staging-only checkpoint.

## Complete assembly source

The host controller now freezes the source capsule and nine-file guest manifest,
copies the two qualified runtime archives, uses an owned offline QEMU container,
and retains actual terminal state and exact cleanup diagnostics. The probe creates
one new COW overlay/readonly ISO, observes paused KVM through its owned QMP peer,
and sends one bounded `cont`. The guest bootstrap verifies the full runtime catalog
before installation, then invokes the controller from the readonly seed.

The new-marker frame reader preserves negative payloads and requires native
callback/token/focus plus collected cleanup. It cannot qualify renderer-only
preflight or authenticated chat selection. Three pure frame tests pass. The old
renderer fixture sources remain unchanged so historical evidence retains its
exact binding; this assembly requires its own observed native evidence.

R1 fixed publication/shutdown, exact cleanup absence, actual container terminal
state and QMP pre-send deadlines/paused-state observation. After these repairs were
accepted, the controller's temporary refusal was removed for renewed R2/R3 review.
R2 and R3 accepted the repaired complete source. A final archive listing confirmed
that `gtk.portal` is present in the GTK tree, so its pinned share directory now
precedes the distro share directory in discovery; the final R3 accepted this delta.
The first execution followed acceptance of the full frozen capsule; its negative
result is retained below. Every new execution requires its own reviewed capsule. A failure/unknown outcome never authorizes
an automatic second notification, click or client launch.


## First actual assembly: FAILED, retained

Source `0d194f48cbf2618e0792ed8e464f45e45f3c9223` ran once in a fresh,
offline TEST VM. The sender exited 1 before click. Its mandatory `sender.sha256`
file was absent from the actual readonly ISO; this guard precedes callback and SDK
imports. The private archive and its ISO-listing audit retain the source/seed/frame
bindings. `notificationAttempted=true` records producer intent, not proof that
AddNotification ran. No click, selected-client handoff or navigation was qualified.

The guest cleanup observation also failed with a combined incarnation/UID error.
That message does not establish which condition failed. Nine retained kernel exits,
empty owned cgroup, guest poweroff, collected QEMU and terminal/removed host
container were observed. Those containment facts do not repair the FAILED result.
The readonly ISO-audit container's original absence parser rejected the CLI text;
its failed receipt is preserved alongside a separate exact ID/name reconciliation.

The sender now authenticates itself with the existing bounded canonical manifest,
avoiding an independently maintained sidecar. Two pure tests reject changed source,
missing/ambiguous authority and oversized manifests. Final cleanup may retain any
UID in the exact root-owned cgroup while preserving birth/pidfd/membership guards;
selected-peer admission stays UID1000. Per-PID observation failures continue the
sweep and still fail the cleanup verdict. Independent focused reviews accepted
both corrections. The negative evidence must be bound and a fresh final capsule
reviewed before a distinct planned attempt; no automatic retry is authorized.

Independent raw review passed 412 assertions, including all 78 archive members
and direct readonly ISO parsing of the manifest and nine guest payloads. The
zero-SDK-call conclusion uses actual ISO and frozen source control flow, without
a captured sender traceback. This accepts the negative checkpoint, not native
qualification.


## Corrected actual assembly: FAILED, contained

Exact source `1511cb4adb6b5293cf32f7569c70219c5831db5d` used a new frozen
capsule and fresh offline VM/root. The sender exited 0, the native Notify/reply
assigned ID1 and the controller reached callback launch publication after its sole
CLICK command. The next owned-group enumeration failed `selected_peer_uid_not_1000`.
This precedes selected callback/client admission; the recorded error identifies no
PID and cannot distinguish an unrelated descendant from the selected peer.
Typed receiver identity, final pointer/token checks, activation and navigation were
not qualified. The semantic handoff outcome remains unknown and is not replayable.

Independent raw review passed 413 assertions, including 61 retained files, actual
ISO and source/command/frame bindings. Cleanup passed: all 13 retained incarnations
exited, the owned cgroup was empty, guest shutdown and collected QEMU exit0 were
observed, and the exact terminal host container was removed. These facts contain
the experiment without turning the negative semantic outcome positive.

The next source correction separates UID-independent owned-group retention from
unconditional current UID1000 admission for selected callback and client, even if
the group sweep already populated their handles. Selected failures record phase,
PID, birth, UID fields and cgroup without argv. Incarnation/pidfd/membership guards
remain mandatory. Any further experiment requires a newly reviewed source/capsule
and fresh owned VM/root/nonce; it must not replay this unknown semantic attempt.
