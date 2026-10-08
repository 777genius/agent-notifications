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

Checkpoint B draft operator/capacity extension (not installed qualification):

* Reader1 snapshot/record bytes and callback activation remain unchanged.
  Kind 3 is a fixed operator-only Show frame: reference, literal title, literal
  body and silent 0/1. Kind 4 is an owner-private operator ticket: random nonce,
  fixed mode, original decimal boot deadline and exact snapshot-byte SHA256.
  Both require header/version/flags/field count/trailing-byte validation before
  effects. Native mutators join the ticket and held generation; model arguments
  cannot supply the ticket, selected package, registry path or helper authority.
* WCAP0001 is exactly 72 bytes: eight ASCII magic bytes, four LE uint64 counters
  (records, reserved record bytes, attempts, reserved attempt bytes), then raw
  SHA256 of the first 40 bytes. Independent Go/C++ fixtures cover the same layout.
  A single owner-protected, regular single-link exclusive lock serializes fixed
  pending/next/state publication. Torn/pending reservations refuse admission.
  Charges never decrease after crash/unknown. Bounds are 1,024/64 MiB records and
  2,048/64 MiB attempts, with 64 KiB/32 KiB charges before respective effects.
* The existing owner transaction journals preparing, participants_applied,
  commit_decided, binding_published, committed or pre-decision rollback_decided.
  Windows participants require schema/floor 5 because floor 4 readers ignore
  unknown fields. Ordinary schema 4 writes stay schema 4. A decided commit only
  verifies installed participants and finishes policy/binding publication;
  changed values or objects are never silently repaired. Private generations
  from interrupted/rolled-back setup remain charged toward the maximum of four.
* Fixed shared registry parents must already exist. The operator never silently
  creates Software/Classes/CLSID or AppUserModelId outside its concrete unique
  participant. Missing parents refuse setup. Fresh installed qualification must
  record this prerequisite without pre-creating those parents to force success.
* Go discovery currently requires exactly one returned family package and an
  official x64 main package with absent/empty resourceId. NULL resourceId is a
  documented absence; malformed spans or nonempty resource IDs refuse. Multiple
  registered/resource/side-by-side identities conservatively refuse; no first,
  newest, architecture fallback or resource launch target is selected. This is
  an explicit availability limit to measure on the fresh canary image.
* Successful readonly readiness performs no registration/repair/Show/vendor
  activation. Enabled permits the opted-in classic route; all four Disabled
  states block every navigation mode, including BestEffort informational
  fallback. Only Setting getter HRESULT 0x80070490 after successful notifier
  creation permits permission_unknown. It never means Enabled/authorized.
* The exact retained helper uses its unchanged 65s hard lease. Caller expiry
  classifies the result without killing it at the action deadline. Owned child
  collection is bounded at 70s plus 5s, including pipe drain; an uncollected child
  retains physical custody. Before mutation/Show, operator.pending is durable,
  immutable and bound to nonce/mode/end/snapshot. Native joins it before effects.
  Fresh Open, automatic recovery and a restarted Go supervisor refuse a pending
  or collected obligation. Actual collection alone never permits automatic
  replay; late/unknown facts remain blocked for reviewed recovery. The two fixed
  files are each bounded 4 KiB. Normal timely collection deletes only the admitted
  file incarnation with checked close. This cross-restart guarantee applies to
  mutating admission. Readonly observe/readback/ready never persist tickets or
  write on failure. Active lifetime admission precedes every operator handle
  read and spans complete record reservation/publication, so Close retains live
  physical handles until that admitted work returns. Each storage mutation
  rechecks the original deadline; expiry leaves unresolved charges untouched.
  An uncollected readonly actor retains live physical custody;
  a fresh independent readonly request may query metadata again, without any
  inferred success, registration/Show/launch or fallback after possible effects.
* Native Show returned is OS API handoff only. No output claims rendered chat,
  target focus, global broker/SDK drain, an atomic PFN version lock or genuine
  click cryptographic provenance. Unknown never authorizes fallback/retry.

This branch is source preparation. Production main stays A/inactive, and B plus
its separate installed-canary proof must remain draft/unmerged/unreleased until
independent frozen review and genuine fresh x64 installed qualification pass.
