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
No assembly execution has occurred. Do not run it until the full frozen execution
capsule is accepted. A failure/unknown outcome never authorizes
an automatic second notification, click or client launch.
