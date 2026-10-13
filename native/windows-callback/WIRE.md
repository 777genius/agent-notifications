# WinEnvelope1 (reader 1)

Authority: this layout. Go and C++ implement it independently and consume the
same checked-in golden bytes. It is separate from desktop_thread_v1.

All integers are little endian. Header: ASCII WNCB0001 (8 bytes), reader floor
u16 = 1, kind u8 (snapshot=1, record=2), reserved u8 = 0. Each ordered field is
u32 UTF-8 byte count followed by those bytes. No trailing bytes, empty fields,
invalid UTF-8 or C0/C1 controls. Envelope <=65536 bytes. Unknown floor rejected.

Snapshot fields: generation (32 lowercase hex), canonicalRoot (physical extended
Windows path), ownerSID, helperSHA256 (64 lowercase hex), AUMID (<=128 bytes),
CLSID (lowercase braced GUID), vendorName, publisher, family, selected FullName.
Vendor identity is OpenAI.Codex / CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B /
OpenAI.Codex_2p2nqsd0c76g0. FullName selection does not substitute for that pin.

Record fields: generation, reference (32 random lowercase hex), provider=codex,
opaque threadID (<=4096 UTF-8 bytes), snapshotSHA256. OS callback argument is
exactly WinEnvelope1:open_thread:<reference>. Thread ID is percent-encoded from
raw UTF-8 bytes exactly once; unreserved bytes pass through. Percent is escaped.
Entire opaque IDs "." and ".." reject to prevent URI dot-segment normalization;
embedded dots and encoded slashes remain valid.

Layout: retained root/helper.exe, generation.wne, records/<reference>.wne,
attempts/<fresh random attempt>.intent and .result. Installer B must create
protected owner+SYSTEM ACLs and exclusive immutable files. Callback reads its
physical executable's generation only, validates root/file no-reparse custody,
owner SID, digest and binding before admission. No ENV/config/spool dependencies.

Attempt output is fixed ASCII. Intent layout:
`WinAttempt1 <attemptID> <entryBootMs> <deadlineBootMs> <reference> <snapshotSHA256>`.
The reference and digest join this attempt to its exact immutable owned record
and physical generation. No raw thread ID/path is exported. Each publication
requires WriteFile, FlushFileBuffers and successful explicit CloseHandle before
an effect may enter. Destructor cleanup is not a durability acknowledgement.

A .result accepted is only a candidate, not standalone completion evidence.
A .worker-returned records worker return separately from observed operation
completion. If Query/Launch creation throws without an operation handle,
operations_completion_known=0 and no .drained is published. Only actual terminal
SDK status sets operation completion; worker return is not that evidence.
Unknown creation/completion retains the same worker and admission slot until
the already-established hard own-incarnation lease, including publication
exceptions. A worker return never frees an unproved pending operation.
Authoritative collection requires the matching immutable intent, complete valid
.result, actual .drained operation completion with collectedBootMs strictly less
than the admitted deadline, absence of .late AND .collection-late, and proved
successful collection of that publication. Any missing/partial, close failure,
late or uncertain collection is unknown and grants no replay/fallback. Deadlines
are checked after checked close of both terminal and drain publications; late
markers are exclusive best-effort evidence. Filesystem publication and monotonic
clock checks are not atomic across crashes; marker absence alone cannot prove
successful collection. A ends without implementing the B collector/readiness.
S_OK acknowledges admission, not success. Same-principal injection is possible.

A supplies source, trusted embedded release bytes and an inert Go port. No
registration/setup/delivery is enabled. B must implement complete transactions,
readiness, permission observation, submission and installed canary before enable.
