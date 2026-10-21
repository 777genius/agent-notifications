# E1 integration ports (frozen 2026-10-01)

Base: `6ee0d84705d721219ecc56ef8cab6ff2de53becb`. Inputs: contract
`2484bffee93a9c20fc8558cb9d827145da7d92592c03694d9b1b82d63ade7577`, plan
`3f922d83557ace138295bd01c6ed5b9834348687019146ca778b2286037554a3`.

* `opencodeinstall.Request.Renderer BundleRenderer`; interface method
  `RenderRegistration(executable, controlRoot, origin string) ([]byte, error)`.
  E2 supplies its exact third-token renderer here. The default uses the existing
  two-token renderer and records `OriginBound=false`; it is placement only and
  cannot obtain an admission; advisory channels also remain silent for this
  placement. Origin is 64 lowercase hex characters (256 random
  bits). Setup alone supplies it; observers must capture the rendered value.
* `installruntime.Consumer.OpenCode *OpenCodeRegistration`: `Origin`, `Salt`,
  `Namespace`, `BundleSHA256 string`, `OriginBound bool`. Origin/salt/namespace
  are immutable through updates. Namespace is private, never a wire identifier.
* `opencodeevent.AdmissionPort.Admit(context.Context, AdmissionRequest)
  (*Handoff, AdmissionStatus)`; concrete `Admission{ControlRoot string,
  Clock ClockAuthority, TimePolicy TimePolicy}`. Request contains `Fact FactIdentity`, `Origin string`,
  `Provenance Provenance`, `Expected installruntime.PolicySnapshot`,
  `Executable, GOOS, GOARCH string`, `CommandStarted time.Time`.
  Config must be loaded from exactly Expected.Fields before calling. Strict
  decoder must provide root-only canonical facts and immutable private provenance;
  no event-time origin adoption. There is no decoder/wire change in E1.
* `FactIdentity`: `Kind, Session, Execution, Terminal, Request string`,
  `Root bool`. Closed kinds: `turn_idle_verified`, `question_asked`,
  `permission_asked`, `terminal_error`. Terminal holds the completion's canonical
  message identity. Strict private terminal error requires the actual final native
  message identity (V1) or actual terminal native event identity (V2), as well as
  execution. `Provenance.TerminalBinding NativeTerminalIdentity{Kind
  TerminalIdentityKind, ID string}` must match Fact.Terminal. Closed kinds are
  `V1FinalMessage="v1_final_message"`, `V2TerminalEvent="v2_terminal_event"`.
  D/E2 strict private decoder supplies this only after validating the native final
  binding. Missing/mismatched/unverified binding returns `invalid_fact` before any
  store IO. Neutral published V1 terminal_error without messageID stays decodable
  but alone cannot authorize private admission. No observationID parsing or ID
  fabrication. Request is required for attention facts.
  Identities follow the neutral decoder's nonempty <=256-byte bound without
  normalization. No instance/reload/version field enters the key.
* `ClockAuthority.Snapshot(context.Context) (ClockSample, error)` is a **trusted
  system composition** dependency, not supplied by the sender. ClockSample has
  `BootID, Domain, Kind, Fence string`, `TickNS, WallNS, UncertaintyNS int64`.
  ClockSample's syntactic kinds: `continuous`, `uptime`; this policy accepts only
  `continuous`. This port grants no platform qualification by
  itself. E0 SnapshotPort adapter must map independently qualified boot-wide
  nanosecond ticks, native epoch nanoseconds, total comparison uncertainty and stable
  policy fence. Reject unavailable/unqualified snapshots in the adapter;
  never choose kind/domain/uncertainty from IPC. The trusted uncertainty must
  include the independently qualified native-source/JS translation bound;
  missing mapping remains unqualified. No E0 files copied or changed.
* `TimePolicy{ProfileID, RawKind string, NativeReadBoundNS,
  ComparisonBoundNS int64}` is a trusted immutable value composed beside that
  adapter, selected from the qualified platform ledger, never from sender/config.
  NativeReadBoundNS is R (0..103ms); ComparisonBoundNS is complete T (>=2R,
  <=2s, including independently qualified non-native terms). Zero R/T belongs
  only to the independent synthetic test authority, not a shipped platform policy.
  No native profile or translation allowance is invented by E1.
  The adapter validates E0's interval/rule and actual raw kind/domain/boot against
  that policy; it maps MonoLoNs to TickNS, WallUnixNs unchanged, UncertaintyNS=T.
  Raw kinds `linux-boottime`, `darwin-monotonic-raw`,
  `windows-interrupt-precise` map to continuous. E0 `NewSystemSnapshotPort()` is
  the later system source; helper output/receipt is not Go admission authority.
* `TimePolicy.Fence(boot, domain string) string` returns lowercase SHA256 hex
  over uint32 big-endian byte-length-prefixed UTF-8 strings, in order:
  `AN/OpenCode/clock-policy/v1`, boot, domain, RawKind, ProfileID; followed by
  big-endian int64 R then T. Invalid policy/coordinate returns empty. Go checks
  each trusted sample's T and Fence against this immutable value. E2 uses exactly
  these bytes from validated original E0 helper output and its fixed ledger.
  Boot is canonical lowercase UUID in real composition; domain retains E0's actual
  native domain (including Linux time namespace). Fence never uses random JS epoch.
  Domain/sample validation in the E0 adapter remains mandatory.
* `Provenance` contains original `Clock ClockSample`, `NativeCreatedNS`,
  `IngressTickNS`, `SpawnTickNS`, `DeadlineTickNS int64`. The strict decoder maps
  these only after D/E0 finalize their wire contract. Original sample anchors
  wall-to-tick continuity; trusted total allowance must be <=2s and the sender's
  asserted uncertainty cannot exceed it or increase it.
  No receive-time stamping. Max30s ingress-to-spawn, max20s original child
  deadline, max50s ingress-to-now, native age -2s..60s.
  Additive `SourceEpoch string` is a nonempty <=128-byte printable immutable
  qualified JS/source epoch identity, separate from stable Fence and claim identity.
  `EpochStartedTickNS int64` is its original conservative native lower start tick,
  >=0 and <=original Clock.TickNS. D/E2 supplies both from the qualified source
  activation; decoder must reject an epoch mismatch or receipt-time replacement.
  Original Clock/ingress and birth belong to this epoch. After a persisted wall
  discontinuity, later admission requires a distinct fresh epoch whose original
  lower start tick is strictly after the persisted discontinuity tick. The rejected
  frame must be dropped; fresh epoch activation authorizes only future live births.
* `Handoff.Context() context.Context`, `Channels() (bool,bool)`,
  `Deadline() notification.Deadline`, `Close()` (idempotent).
  One handoff owns both channels and the outer kernel leases; store lock is
  released before return. E2 must defer Close through actual bounded provider
  completion, use this Context for both attempts, and never retry an uncertain
  attempt. Native deadline is shortened to 15s for existing notifier protocol.
* On Darwin `Handoff.NativeInstallation() notifier.NativeInstallation` gives a
  validated single-use adapter. Wire it into existing StructuredDelivery with
  the existing spool/probe/process ports; do not call ordinary NativeInstallation
  Acquire first. Native acquisition transfers an extra held reference, released
  by notifier, so closing Handoff cannot release the fence during native IO.
  Ordinary notifier acquisition and policy are unchanged. No native effect is
  enabled by this patch; CLI/consumer final composition belongs to E2.

Stable content-free AdmissionStatus values: `admitted`, `invalid_fact`,
`not_registered`, `snapshot_changed`, `time_authority_unverified`, `expired`,
`store_unavailable`, `duplicate`, `capacity`. Private data/errors never enter
receipts. Schema4 / writer floor3 fence all later writers; packages replacing a
managed writer in these installations need the v3 offline marker as well as v1.
Removal keeps the permanent root-level admission lock inode, persisting captured
private purge identities in the existing redo transaction. Forward recovery only
for registration creation/removal: rollback must not rotate or erase claims.

Integration order: combine E1 first; add E0 trusted adapter and D strict private
decoder; pass E2 third-token renderer; wire one admission in real command with a
20s context started before decode/config; share handoff across providers; build
exact dual bundle/SDK candidate; parent qualifies real CLI and five platforms.
Independent filesystem/process tests are domain evidence, not host qualification.

Private store payload version2 persists native R separately on checkpoint and
each claim. Native progress uses Rprev+Rnow (<=206ms), retention lower elapsed is
nowTick-claimTick-Rclaim-Rnow, and freshness uses complete T once. Record T remains
metadata and cannot grant native tolerance. Older experimental payload version1
with no independent R fails closed without deleting/reinitializing claims; it has
never been an installed qualified candidate. Schema4/writer floor3 stays unchanged.

Under the existing store lock, positively observed same-qualified-clock wall
discontinuity with nonregressing native ticks durably rebaselines every claim tick
and native bound conservatively, preserving keys/origin/salt, status, generation
and complete uncertainty, with zero pruning/new claim/handoff. The triggering
frame returns time_authority_unverified. The new checkpoint and hashed rejected
source epoch/start-tick barrier survive restart. Tick regression, unavailable or
unqualified samples do not repair state. Different boot/domain/policy fence retains
the existing conservative new-coordinate baseline behavior. No store/outer lease
order or ordinary notifier behavior changes.

Private claim HMAC keeps its existing length-prefixed input order:
`AN/OpenCode/admission/v1`, origin, session, execution, kind, Terminal, Request,
with registration Salt as the HMAC-SHA256 key. For terminal_error only, append the
length-prefixed verified TerminalBinding.Kind. TerminalBinding.ID must equal
Terminal and is already included there. Neither host version, reload, JS epoch nor
Fence enters the key. Terminal kind distinguishes native identity classes. Neutral
wire version1 remains unchanged; E2 freezes actual private JSON names with D before
shipping, including this additive typed binding. Real native source/platform and
installed candidate qualification remain parent gates.
