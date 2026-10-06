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
new-marker completion reader and complete assembly review are still outstanding.
