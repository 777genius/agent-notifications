# Notification and setup boundaries: audit and implementation plan

Date: 2026-11-04. Baseline: `76df2574b22640e1de18de40c9c36d07cdb0da3d`.

Status: researched; independent plan review accepted. Implementation and final
qualification are recorded below as they happen. This is a source refactor, not a
release candidate or a new native navigation qualification.

## Goal

Stop agent-specific behavior and installation lifecycle policy from accumulating
inside shared handlers and the CLI. Fix the concrete state-isolation and partial
operation-reporting defects discovered during the audit. Keep existing public
commands, installation identities, stored formats and supported capabilities.

The user accepts a large refactor (roughly 15–25 thousand changed lines where
justified). That is a capacity allowance, not a target. Moves count as moves;
large files alone do not justify new abstractions. Work is limited to the four
boundaries below, with independent review before and after implementation.

## Evidence and priorities

| Priority | Baseline evidence | Consequence and chosen response |
| --- | --- | --- |
| High | `internal/hooks/hooks.go`: `handlePreToolUse` writes `ev.Session.SessionID`, although normal Qoder state uses `qoderKeys`; `handleTeammateIdle` uses the Claude team manager for any admitted product | Qoder can write a Claude state record and affect Claude team completion. Make state identity and team capability explicit in the existing handler; reproduce both paths before fixing them. |
| High | `internal/agentnotify/portablesetup/uap.go`: successful `m.apply` is followed by `Complete`, inspection, binding commit, client-facts write and handoff cleanup that return plain errors | A real materialization/removal can be reported without its completed facts. Preserve the exact UAP result through finalization, and distinguish finalization failure from failed activation in the wizard. |
| High | `internal/geminievent/consumer.go`: outer panic recovery replaces the entire receipt with suppressed, even after the desktop effect returned submitted | A later admission failure can erase an earlier known effect. Preserve completed channel facts and conservative uncertainty; keep per-agent admission and existing channel ordering. |
| Medium | `internal/notifier/delivery_toast_windows.go` and Gemini/Cursor/Copilot integration code import `internal/opencodeinstall` for shared AUMIDs, spool preparation and shortcut mechanics | Adding an independent agent expands another agent's installer. Extract the already shared platform mechanics into a neutral adapter package while preserving lease ownership and fixed identities. |
| Medium | `cmd/claude-notifications/setup_wizard.go`: `runInstallOrUpdate` chooses update/add phases, observes policy, handles registry migration and verifies installation | Application policy is owned by a command adapter. Move this use case into the existing `setupwizard` application package; keep parsing, terminal interaction and presentation in the CLI. |
| Medium | `setupwizard/wizard.go` has 4,449 lines; `portablesetup/uap.go` has 2,363; `hooks/hooks.go` has 1,402 | Related policies are difficult to review in isolation. Organize existing responsibilities into cohesive files while making the concrete ownership changes above. Do not build another setup engine or event pipeline. |

These are source findings, not a claim that every failure was observed in a
user installation. The regressions below must establish the changed behavior.

## Architecture and invariants

- Host event sources keep their own protocol and classification. Retain the
  existing `EventSource`, `Handler`, `notification.DeliveryPort`, request and
  receipt contracts. Do not add a universal event model, bus or dependency
  injection container.
- Hook application code owns admission, source-specific state keys and when to
  notify. Effects remain behind existing notifier/webhook seams. Claude team
  aggregation is not implicitly a capability of Qoder or Codex.
- `internal/desktopinstall` owns the existing fixed native desktop identities,
  private Darwin spool setup and Windows shortcut operations. It may depend on
  `installruntime` and portable identity types; it must not import `notifier` or
  a product installer. Product-specific registration and policy leases remain
  with their current owners.
- Portable materialization owns the distinction between no mutation, committed
  files, external activation and finalization. A later failure cannot erase
  earlier facts. No automatic replay or fallback is introduced.
- `setupwizard` owns update/add decisions and their policy/generation fences.
  CLI code translates arguments, obtains explicit confirmation and renders
  results. Retain the ordering of revocation, recovery, apply and consent.
- No stored schema, raw identifier convention, AUMID, shortcut name, spool path,
  plugin file, command name, release version or public JSON field is renamed.
- Keep independent desktop/webhook consent, sound default-off, opt-outs,
  cancellation/deadline behavior, unknown-effect handling, ownership checks and
  fail-closed recovery. Do not weaken tests or native proof requirements.

## Scope and exclusions

Included: the four ownership changes, the proved state/result defect classes, focused
regressions, existing end-to-end/PTY/installation tests, relevant architecture
documentation and independent review corrections.

Excluded: release publication/promotion (#588/#589), Windows retained callbacks
(#423/#424/#577), Pi runner policy work (#569/#579), new agent support, new focus
or sound capabilities, native callback protocol changes, changes to the durable
installation transaction kernel, a client-facts storage rewrite, replacement of
UAP/plugin-kit-ai, dependency upgrades, landing-page work, blanket code cleanup,
feature flags and a new test framework.

The existing Cursor transport's placement in `copilotvscodeinstall` is recorded
as follow-up debt. Its shared proof/lease owner is tightly coupled to the current
admission model. Do not replace that owner with an opaque generic authorization
object during this refactor.

Two inherited limitations identified during review have explicit follow-ups:
[binding-keyed client facts #591](https://github.com/777genius/agent-notifications/issues/591)
requires coordinated SDK indexing and legacy sidecar migration;
[omitted Local component recovery #592](https://github.com/777genius/agent-notifications/issues/592)
requires verified frozen-package resolution before Local adapter composition.
Neither can be fixed safely by changing a map key or one component-name literal.
This pass preserves available exact Local selections and keeps refusals when
the selected names or source cannot be proved. It does not claim reconstruction
of arbitrary component names from the boolean-only durable intent.

Further review identified inherited policy work outside the extraction:
[shared Cursor shortcut targets #593](https://github.com/777genius/agent-notifications/issues/593)
needs a policy for distinct verified primaries and legitimate primary upgrades;
[delayed Claude Stop reservations #594](https://github.com/777genius/agent-notifications/issues/594)
needs explicit in-flight, expiry and failed-send semantics without recording a
successful delivery before the effect. Client-facts atomic publication and
shared-writer coordination are included in #591's storage work. No shortcut
identity, event-lock lifetime or delivery-claim policy is changed here.

[SDK group sibling compatibility #595](https://github.com/777genius/agent-notifications/issues/595)
tracks a separately verified SDK limitation: a same-source group can preserve an
unselected third client, but a new-revision group update lacks that client's
compatibility input even when the host supplies exact KnownTargets. A host-only
field addition is ineffective; the SDK contract and dependency must change.
The selected mutation scope and all-binding compatibility gate remain intact.

## Implementation slices

### S1. Product-owned hook state and lifecycle

1. Add focused regressions showing Qoder interactive events use only the Qoder
   state identity, including a session ID shared with Claude and a path-like ID.
2. Show a Qoder team event cannot consume a Claude team completion claim or
   change Claude team state. Preserve the existing genuine Claude team scenario.
   Qoder currently has no owned team-state source and its Stop does not update
   the Claude lead state. Keep TeammateIdle registration/decoding, suppress its
   unsupported team effects, and correct the claim in `docs/QODER.md`.
3. Derive state identity once through the existing event-key contract and pass
   it to state mutations. Explicitly gate product-only behavior.
4. Separate source admission/classification, Claude transcript/team handling,
   and shared delivery/content helpers into cohesive responsibilities. Preserve
   the existing order of early dedup, classification, state updates and effects.
5. Run existing Claude/Codex/Qoder hook tests, including privacy, StopFailure,
   dedup, stale transcript and team completion. Add no tests for mere file moves.
6. Fix the separately reproduced Gemini late-panic receipt loss with a focused
   regression: desktop submitted, later webhook admission panics. Preserve the
   known desktop result without reporting a wholly suppressed operation. Keep
   early no-effect panic distinct; do not add retries or a universal consumer.

### S2. Neutral native desktop installation mechanics

1. Extract fixed product identities, Darwin spool preparation and Windows
   shortcut staging/readiness into `internal/desktopinstall`.
2. Preserve existing product wrapper APIs as thin delegates where needed for
   source compatibility. Move independent consumers to the neutral owner.
3. Leave OpenCode `NativeInstallation`, `RegistrationLease` and permission
   orchestration in `opencodeinstall`; moving them would reverse dependencies.
4. Preserve all platform build constraints, qualified architecture checks,
   no-follow checks, permanent locks, COM cleanup and last-binding retention.
5. Reuse Windows shortcut and Darwin spool tests; keep integration tests through
   public product entry points. Cross-compile all affected platform packages and
   run native jobs on their actual OS before declaring those lanes verified.
   Update focused test package targets and path filters in
   `cursor-windows-shell.yml` and `opencode-native-e2e.yml` if tests move; a
   zero-matching-test success is not qualification.

### S3. Preserve portable operation results through finalization

1. Keep existing exported materializer/service signatures and `ResultError`.
   Capture the exact result after successful UAP Apply at one finalization error
   boundary per single/group install/remove or retained switch operation.
   Retain existing preservation of error-returning Apply results, including
   partial mutations. An unchanged Apply is also a fact, not proof that the
   following host finalization succeeded.
2. Preserve that result for every subsequent failure, including binding
   completion, inspection, Pi handoff, facts recording and handoff cleanup.
   Mark finalization failures distinctly so the wizard does not report an
   activation failure when activation succeeded.
3. Read the final group installation view once; propagate inspection errors
   instead of silently ignoring them. Require exactly one matching binding for
   each requested client, allow existing unselected sibling bindings, and retain
   successful siblings' facts on failure.
4. Update existing wizard error mappings to keep materialization/activation or
   absence facts while reporting incomplete finalization. Installation resume
   uses only the verified existing intent and acquired source; removal retains
   its existing idempotent uninstall retry and normal admission. Do not retry
   effects automatically or invent a new command.
5. Split materializer identity/legacy recovery, engine composition, local
   package preparation, single/group lifecycle and result mapping into cohesive
   files in the same package. Do not rewrite transaction/recovery semantics.
6. Use real fixture install/activation callbacks and filesystem faults to prove
   post-commit failure reporting. Cover single and group install, plus removal
   finalization. Test facts through the caller, not only the error wrapper.

### S4. Setup application ownership and reviewable modules

1. Move the existing install-or-update use case and its phase validation,
   post-install inspection and preserved-opt-out result construction from the
   CLI into `internal/agentnotify/setupwizard`.
2. Keep the existing request/result and bounded phase loop. Preserve immutable
   bootstrap projection checks, policy/generation CAS, registry migration
   provenance, selected-agent limits and partial completed phase results.
3. Organize wizard request/result contracts, evaluation, planning, installation,
   removal, recovery and presentation helpers into cohesive files. Keep explicit
   static calls; do not add a controller registry or speculative interfaces.
4. Retain command-level E2E tests and PTY tests. Relocate only tests whose unit
   now belongs to the application; retain binary tests through the public CLI.
5. Update `docs/ARCHITECTURE.md` to describe the changed dependencies and state
   the remaining boundaries accurately. No unrelated documentation rewrite.

The broader Cursor/Local confirmation and consent flow stays in its current
composition owner for this pass. Its prompt/revoke/confirm/CAS sequence is not
part of the install-or-update ownership move.

## Validation and edge cases

| Boundary | Required evidence |
| --- | --- |
| Hook state isolation | Same ID in Claude/Qoder; path-like Qoder ID; Qoder team event leaves Claude state untouched; real Claude team completion remains correct |
| Hook compatibility | Existing Claude/Codex/Qoder classification, privacy, dedup, StopFailure, cooldown, opt-out and shared delivery tests |
| Partial effect receipts | Gemini late admission panic retains definitive submitted/rejected/suppressed receipts; an in-flight or unknown effect stays unknown; early panic remains suppressed; no second attempt |
| Portable finalization | Real committed single/group operation followed by a controlled failure; exact materialization/activation or removal facts retained; no false success or automatic effect retry |
| Setup lifecycle | Install → inspect → update/repair → remove, both clients, mixed opt-outs, pending recovery, policy drift after confirmation, partial activation and retained data |
| CLI boundary | Existing flag grammar, clean JSON/stdout, exit codes, non-TTY behavior and PTY consent tests |
| Desktop mechanics | Exact constants/paths and build tags; native foreign/mismatched shortcut refusal, last-binding retention and private no-follow spool behavior |
| Cross-platform regression | Go format/vet, affected race tests, required Ubuntu/macOS/Windows CI, existing recovery/native integration jobs |
| Final review | At least two independent critic rounds on implementation; fix actionable findings and repeat affected evidence before the final review |

Baseline environment note: local Go 1.26.10 is available. The first run passed
notification, Cursor, Copilot and hooks, but Codex installer fixtures refused
ambient variables duplicated by case (for example `HTTP_PROXY`/`http_proxy`).
Retest with a disposable child environment containing one equivalent spelling
per key. Preserve product validation; do not change tests to accept ambiguity.

Tests using fixture clients establish CLI/lifecycle behavior, not physical
desktop visibility or click-to-focus. This refactor adds no native capability
claim. Keep skipped native tests distinct from passed tests.

## Delivery, review and rollback

One branch and draft PR based on the baseline above. Commit the reviewed plan
first. Use author `777genius <iliyazelenkog@gmail.com>` and push every commit
immediately. Keep each implementation slice separately reviewable. Do useful
local verification and criticism while CI runs; investigate failures instead of
waiting idle or bypassing a gate.

Independent reviewers inspect architecture, state/consent safety and executable
behavior. Findings must identify an actual path, expected/actual behavior and
severity; stylistic preferences do not justify additional architecture. Review
the integrated final head after all fixes, including applicable GitHub comments.

Rollback is a normal revert of the relevant source slice. Because no persisted
schema or shipped identity is changed, no user-state migration is required.
Coordinate with the release's frozen-main ancestry requirement before any merge;
this refactor must not silently alter the already sealed release source.

## Execution record

- Research: three independent GPT-6 Astra reviewers at ultra reasoning effort;
  repository rules, current open PRs, established navigation/installation plans
  and relevant source paths inspected.
- Plan review: three independent reviewers accepted the four boundaries.
  Incorporated their restrictions: keep Cursor/Local consent composition,
  preserve native lease owners and exact identities, update focused CI filters,
  allow unselected group siblings, and never convert a successful Apply result
  into overall success after failed finalization.
- Baseline regression reproduction: two Qoder state/team probes and Gemini
  late-panic probe all fail on the audited baseline with the expected symptoms.
  The probes used temporary Go overlays; permanent regressions accompany fixes.
- Implementation: all four slices implemented. The hook handler delegates to
  product-owned policy; shared native mechanics have a neutral package owner;
  portable operations preserve successful UAP results through host finalization;
  install-or-update belongs to the setup application. Materializer and wizard
  responsibilities are separated into cohesive files without a new framework.
- Review round 1: non-author reviewers checked each slice. Corrections include
  the selector/bootstrap CI path filter, selection of the exact group binding
  while retaining unrelated profiles, suppression of the generic retry after a
  checked `resume`, and recognition of completed group removals whose SDK target
  materialization fields are empty. Both presentation regressions failed before
  their fixes and passed afterwards. No SDK result is rewritten to manufacture
  a successful overall operation.
- CI follow-up: the Linux linter found an unused OpenCode-specific forwarding
  helper copied into `desktopinstall`. Removed the unused Windows and non-Windows
  copies; the live product delegate stays in `opencodeinstall`. This cleanup
  changes no native operation and is checked by a further independent review.
- Integrated review: independent reviewers inspect interactions between result
  mapping, CLI presentation, setup phases, product state and native ownership;
  the delivery PR records their final verdicts against the published tree.
- Post-PR review: reproduced the reported setup boundary failures before their
  fixes. Explicit installation inspection now scopes bindings and retained-data
  facts to that installation. Post-install verification refuses an incomplete
  inspection even when installed targets remain visible. Planning propagates
  unreadable recovery-state errors before acquisition or readiness; inspection
  never repairs the malformed journal. Valid known recovery stays owned by Run.
  Initial and final inspection errors inside install-or-update retain the
  requested mutation action, so the read-only inspect exit convention cannot
  turn a failed setup into CLI success. Their original causes remain available.
- Result precision: finalization preserves the distinction between a removal
  performed by this operation and a previously absent group target, using the
  pinned SDK's actual result shapes. Notification follow-up actions identify
  only ready selected clients; the global permission command and selected-client
  restart behavior stay unchanged. Gemini uses the same receipt aggregation on
  normal completion and a later admission panic, retaining definite effects and
  uncertainty without reopening a durable claim.
- Native ownership follow-up: fixed-product Windows shortcut staging now checks
  ledger ownership before admitting an existing removal preimage, while still
  allowing an externally absent owned file to be retired. The transaction kernel
  already refused out-of-band owned-file drift; this closes the earlier staging
  boundary rather than claiming a demonstrated deletion bypass. Regression
  coverage includes owned, absent and user-edited files for both fixed products.
- Further independent review: each post-PR correction is reviewed by a
  non-author critic, including the real pending-journal verification fixture,
  mixed group-removal facts, scoped inspection and native cleanup semantics.
  These corrections stay inside the original result, admission and ownership
  boundaries; they introduce no new framework, retries or schema migration.
- Additional review round: group removal checks every live selected Local
  profile before revocation, independent of request order, while an explicitly
  already-absent target remains an idempotent no-op. Real mixed Local/Codex
  lifecycle regressions cover both orderings, mismatch refusal without writes,
  exact cleanup and the absent sibling case. A first draft that refused absent
  Local targets was caught by independent review and corrected before commit.
- Caller-state and reporting corrections: repair copies the caller's package
  map before filling per-client roots; omitted update/repair unit selection
  propagates inspection failures and honors the requested installation ID.
  Removing hooks still reports completed when the portable target was already
  absent. The public inspect command returns a failure exit for an actual
  inspection error, while a valid readable pending state remains reportable.
- Local recovery review: preserve supplied exact MCP/skill selections when
  restoring enabled units and clear only disabled units. The proposed blanket
  legacy-name replacement was rejected because the shipped package declares
  the canonical name. Exact-selection lifecycle coverage includes both package
  spellings; omitted legacy source reconstruction is tracked in #592, not
  represented as solved by the boolean intent tests.
- CI fixture correction: a controlled delayed-publication reproduction showed
  the native cancellation fixture recreating a claim after revocation. It now
  claims before reading the request and publishing the started marker, matching
  the existing Swift protocol. The same reproduction and focused race tests
  pass without changing production code, deadlines or revocation assertions.
  The matching CI failure does not expose its precise scheduling interleaving.
- Subsequent review round: direct projection refresh now compares the request
  profile to its frozen migration target before handoff. A real reserved Codex
  migration proves the host refusal and the valid-profile continuation. Before
  the fix the SDK also refused the mismatch; this closes the host boundary and
  is not evidence of a successful wrong-profile mutation.
- Durable attestation: uninstall propagates all failures to persist observed
  external removal before entering subsequent removal work. A real native-list
  fixture corrupts the owned pending intent at that boundary; the original
  typed snapshot error, reservation, ledger, UAP state and locator survive.
  A positive control persists the attestation and reaches removal, then cancels
  deliberately; it does not claim a completed native uninstall in this container.
- Composed source ownership: single and group operations release only their
  newly created Local snapshot when returning before the first kernel handoff
  begins. After entering handoff they retain it on every outcome, because later
  recovery or SDK state can require it. Real lifecycle regressions reproduced
  the early leak for single and both group orders, preserve original and already
  committed sources, and verify retention after a later source-confirmation
  refusal. The kernel, source schema and post-handoff cleanup policy are unchanged.
- Current-head review: a real three-client lifecycle disproved the suggested
  host-only KnownTargets fix. Same-source grouped installation preserves the
  unselected Claude binding, profile, consumer and locator. A new-revision group
  and a direct SDK Prepare with exact sibling facts both refuse with the same
  missing compatibility-input error. Track the SDK change in #595; do not ship
  unused propagation or bypass its compatibility gate.
- Local verification: complete hook, Gemini event and portable materializer
  suites pass with the race detector; focused finalization, recovery
  presentation, selection and CLI contract regressions pass. Real fixture
  lifecycle faults cover single/group install, final group inspection, and
  single/group removal. A malformed foreign journal is neither ignored nor
  recovered by the reporting path. Targeted `go vet` and formatting checks pass.
- Source compatibility evidence: the native shortcut/spool implementation and
  installation identities retain their behavior; existing native tests remain
  present. Declaration comparisons account for all moved materializer/wizard
  code, with the intentional result and admission changes reviewed separately.
- Platform evidence: Windows native-mechanics test binaries and Darwin
  native-mechanics builds compile. This environment cannot execute desktop COM
  or prove visible delivery. Some existing daemon and CLI fixture tests also
  fail on the unchanged baseline because Unix sockets or process-containment
  inspection are unavailable here. No guard or test is weakened. Full platform
  CI and its exact revision remain separately reported in the delivery PR.
- Delivery: [PR #590](https://github.com/777genius/agent-notifications/pull/590).
  Every commit is published immediately through the GitHub integration. Main
  and the sealed release branches remain separate from this refactor.

### Continued E2E review

The full platform checkpoint for `ec35261018358f255a8db81a0a2b25cb9e23f33e`
remains historical evidence. Twelve later inline threads and the accompanying
review summary were rechecked before continuing delivery. Two threads repeated
the already fixed frozen-profile and composed-snapshot findings. The remaining
corrections stay inside the same admission, ownership and reporting boundaries:

- Reject mixed normalized group operations before composition or recovery.
  Check every selected Local/Cursor authority before shared recovery, including
  the earlier public `Run` kernel replay. A shared recovery request must identify
  one control root; the read-only authority API rejects nil context. Actual
  interrupted transactions prove refusal without replay and valid continuation.
- Check the exact single-removal locator before freezing or publishing its
  intent. A missing locator remains valid for an already revoked removal retry.
  For unanchored managed Codex uninstall, require exact canonical recorded-profile
  equality, or the pinned SDK's profile/binding proof when the host sidecar is absent,
  before native observation or intent effects. Existing recorded-profile conflicts
  and malformed metadata remain errors.
- Copy caller MCP configuration before both pending restoration and discovery.
  Returned Local retry argv now carries supplied exact MCP and skill names through
  additive JSON flags and the real CLI parser. Existing boolean defaults and
  explicit consent remain unchanged; omitted frozen-source reconstruction is
  still the separate #592 follow-up.
- Preserve each omitted notification selection on update/repair even when hooks
  were explicit. Explicit global/per-client choices retain precedence. The
  existing missing-sibling repair fixture now explicitly requests both units;
  all its refusal and no-mutation assertions remain intact.
- Derive successful uninstall outcomes from actual hook/notification removals,
  including retained-empty portable state. Codex removal uses the transactional
  `Replaced` fact so an empty retry remains unchanged. Inspection propagates
  unreadable live-profile metadata with its typed cause and binding ID while
  retaining known installed facts; readable absent legacy metadata remains valid.
- Permit an externally missing owned Windows shortcut to be retired or repaired
  while preserving registration, peer and edited-file checks. Carry its admitted
  preimage through COM rendering to kernel CAS, rejecting a concurrent replacement.
  Native tests cover Local and Cursor through actual shortcut and kernel APIs.
- Make the synthetic native reader recognize cancellation that already revoked
  its request. The started-process test explicitly cancels after publishing its
  exact identity; a separate real-deadline test covers revocation before reading.
  Active read/parse errors, revoke-before-stop ordering and cleanup stay asserted.

Non-author critics reviewed these corrections in multiple rounds and found two
additional paths before publication: early public kernel replay and the misleading
already-absent hook result. Both were reproduced and corrected. Regressions cover
public Plan/Run, returned argv, actual materializer lifecycles and interrupted
transactions rather than private helper output alone.

Local Codex activation/removal still encounters the baseline container `/proc`
containment limitation. The new profile-admission fixture uses the SDK's supported
manual materialization and cancels its positive public uninstall after durable
absence attestation; it does not claim native activation or completed SDK removal.
Windows COM and Darwin physical-profile authority require native CI. The delivery
PR records fresh workflow results for the published revision, separately from
local tests and from the sealed release's external qualification requirements.
