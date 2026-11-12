# OpenCode V1/V2 future-compatible notifications — issue #642

Status: reviewed implementation plan; evidence gates remain open. Scope: OpenCode notifications only, with a corresponding UAP contract change **only if required**. No release or unrelated agent work.

## Diagnosis

OpenCode 2.2.6 loads the installed V2 plugin but is refused before event delivery. This is not one misconfigured version check:

- `opencode-plugin/owned-host.mjs` and `plugin.mjs` explicitly compare the V2 version with `2.0.21`.
- The trusted Go runtime profile, UAP native-observer qualification and clock selection require exact official image identities.
- The JS clock policy holds exact OpenCode images. On Darwin/Windows the native clock port also pins the embedded **Bun JavaScript runtime image**; OpenCode 2.2.6 embeds a different Bun version.
- The ordinary installer supports the V1/V2 *configuration and plugin families* more broadly; that generic support is **not** proof that every host can emit qualified notifications.
- Without diagnostics opt-in an unqualified installed plugin simply returns. A working manual OS notification is not an observer E2E.

Changing an exact string into `>=2` or copying a reported hash would not work end-to-end and would silently weaken native provenance, runtime custody, time calibration and per-session deduplication.

## Desired contracts

1. **V1 + V2 delivery**: the selected official interface is chosen from required host capabilities. An OpenCode patch/minor release with the same documented API should not require an Agent Notifications rebuild. Future breaking APIs may need an adapter update; do not claim support before verification.
2. **Ownership + privacy**: retain the installed origin, held host identity/lifetime, private managed delivery config, source and location binding, content-free notifications and negative session controls. Neither `ctx.app.version`, user config, environment nor a caller-provided hash can grant admission.
3. **Independent clocks**: remove dependence on fixed Bun/OpenCode binaries **only after** proving an equivalent source/reader/clock contract across supported platforms. A Bun FFI method still needs runtime ABI and calibration checks. A diagnostic/fallback cannot manufacture original native timestamps or turn an unqualified sample into an authorized event.
4. **No silent disablement**: a single bounded content-free diagnostic at an installed denied startup, and actionable docs/CLI status; never print prompts, session IDs, paths, stderr or environment. Preserve silence for unrendered/unowned test fixtures.
5. **No downgrade bypass**: do not silently change to an unverified observer, manually invoke the OS notifier or send a user-facing 'success' receipt when admission failed.

## Smallest staged implementation

- **A — observable failure**: extend the genuine owned-host preparation result with a fixed refusal category and emit a single safe diagnostic only when the installed binding is authenticated. Keep startup cleanup and existing denial semantics intact. Cover V1 and V2, including mismatch between reported and runtime generation. Document the difference between generic install compatibility and native notification qualification.
- **B — version-independent contract**: design and prove a separate narrow native-provenance adapter using *documented* V1/V2 lifecycle events and a fixed, tested source/clock contract, retaining existing exact-image path as backward-compatible qualification. Remove release-number comparisons once the replacement admission proof exists. Any change to the UAP native reader binder must live in its own matching PR and be consumed by an immutable module version, not a local `replace`.
- **C — real qualification**: native installed OpenCode V1 + 2.0.21 + 2.2.6, plus a changed patch/minor case with unchanged API; completion, question, permission, terminal error, queued/steered input, retries, compaction, child/fork, cancel, restart, revoke/upgrade, foreign edits, clock drift and source changes, with desktop/webhook receipts. Cross-platform Linux/macOS/Windows only where physical runners exist; do not equate synthetic test success with visible banners or real authenticated model turns.

## Critical reviews

1. **Scope / SOLID / DRY:** existing JS entrypoints, one Go admission pipeline, one managed notification backend; no new notification service, global feature flags, generic plugin framework or unrelated refactors.
2. **Security / adversarial:** a semver range, arbitrary image digest or runtime-reported `supported` flag cannot be a trust grant. Reject unknown ABI, malformed native timestamps, replayed events, swapped executable, changed project scope and revoked delivery.
3. **E2E / truthfulness:** source tests and compiled builds do not prove 2.2.6 functionality; native installed event + accepted channel result are mandatory. Note model-provider and physical GUI limitations separately.

## Gates before calling issue fixed

- [ ] Both generations accept future compatible official API revisions without exact binary/version tables.
- [ ] Existing strict V1/V2 qualifying cells and all security negative tests still pass.
- [ ] Installed 2.2.6 produces an authorized terminal notification after a real turn, not just a synthetic frame.
- [ ] All supported platform checks, reviews and reproducible build/release assets validated on final SHA.
- [ ] Unsupported/unknown contracts communicate a visible actionable reason rather than silently stopping.

The generic family classifier in UAP is already broad. Changing only `tested_exact` there is **not** sufficient: the actual native qualification, JS and Go clocks, admission and reader semantics are independent. Do not tick these gates without evidence.

## Implementation checkpoint — 2026-11-09

**Completed, limited to the owned V1/V2 startup boundary:** Go's verified
native-host probe now includes its observed version in the closed profile
receipt only on eligibility. JS compares V2 `context.app.version` to that
runtime-attested value, without an exact `2.0.21` gate; the strict profile
decoder rejects mismatched generations and malformed versions. Unqualified
owned profiles and qualified startup failures yield at most one content-free
warning per startup. Render placeholders and public config remain untrusted.
The tracked JS bundle is regenerated from the reproducible build.

**Review 1 — architecture / scope:** keep one native Go profile operation and
the existing single JS entrypoint; no new process supervisor, version map,
feature flag, SDK fork or UAP generic-family reimplementation. The UAP
`Resolve` broad classifier is already compatible; native observer
qualification is independent.

**Review 2 — security / privacy:** refused to replace exact native image and
clock qualification with a version range, caller-supplied hash, Bun version
override or OS notifier side channel. The profile's optional version is
generated exclusively after held parent + probe + native observer and clock
qualification; unverified responses carry no version. The fixed warning never
prints source values and unrendered/unowned attempts remain silent.

**Review 3 — lifecycle / E2E:** suppress activation warnings on normal V1
retirement, V2 disposal and revoked ownership; preserve actual child reaping,
session suppression, multi-step completion and cancellation. Confirmed that
synthetic source tests **cannot** qualify OpenCode 2.2.6, and a passing JS
suite is not evidence that a real 2.2.6 turn produces a banner. The native
version-independent source/clock qualification and physical installed E2E
are still open; no merge or release should occur while those gates remain
unchecked.

**Clock selection prerequisite:** the JS composition now selects its compiled
clock policy by the host version from the independently verified runtime
profile. Distinct qualified versions can therefore use distinct clock bounds;
unknown, missing or ambiguous versions cannot fall back to the older policy.
This does not add a 2.2.6 policy or authorize an unqualified image.


## Native 2.2.6 producer/provenance decision — 2026-11-12

**Decision (three review lenses: source, security, acceptance): keep 2.2.6
production admission closed.** The official 2.2.6 public `event.subscribe`
and `/api/event` expose JS-origin events through an ephemeral, bounded
SSE subscriber, not an independent original-event producer receipt. The
4,096-frame per-subscriber queue may disconnect on overflow; a
`server.connected` marker is a connection boundary, not a replay cursor.
`created` uses the runtime/Effect wall-clock path (default `Date.now`);
a process-owned socket or signed executable alone cannot prove the event
publisher, source-clock origin, or bounded source-to-receiver delay. Exact
official bytes observed on Linux, macOS and Windows and the Linux `/proc`
and Darwin `libproc` socket E0 witnesses are useful negative/source
evidence, but never product R/Q/T/ABI qualification.

### Smallest safe native reader contract (not implemented / not qualified)

1. **R / first-party source:** obtain events from a *host-core-owned* event
   source before an arbitrary JS plugin or mutable public SSE subscriber
   can forge fields. Bind the received original event ID, event type, root
   session lineage and source generation to a live OS-held process
   identity (PID plus start/exec epoch and executable identity). Use a
   private OS-held channel or another independently demonstrated native
   source primitive. Socket-to-PID ownership proves transport custody
   only; it is insufficient without provenance of the core producer.
   A plugin-provided nonce, digest, version or envelope is not a trust root.
2. **Q / source and ABI:** qualify the *actual embedded Bun 1.4.4* ABI on
   each target platform, and independently read raw monotonic plus wall
   coordinates at the native source and receiver. Specify the coordinate
   translation/error bound before taking samples. No JS `Date.now`,
   borrowed 2.0.21 image row, semver inheritance or observed maximum
   may generate a clock grant.
3. **T / ordering and loss:** prove bounded source→reader→Go admission
   delay, stable per-session order, fresh IDs and generation, bounded
   queues, explicit failure on disconnect/overflow/unknown watermark,
   replay, PID replacement, exec, cancellation and revocation. No
   automatic resume from a live-only feed without a producer-backed
   replay/durable cursor.
4. **Delivery / final-SHA:** after R/Q/T/ABI and the UAP private binder
   are independently reviewed and qualified, run **installed**, genuine
   consented OpenCode 2.2.6 completion, question, permission and both
   terminal-error scenarios; require per-stage fresh webhook receipts
   and distinct correlated native desktop OS banner receipts. Include
   same-run replay/revoke/in-flight/restart negatives and full
   macOS/Linux/Windows final-SHA matrices. Until then keep the existing
   V1 / 2.0.21 qualified paths unchanged.

**Implementation boundary:** first investigate whether unmodified official
2.2.6 exposes an independently verifiable host-core source primitive.
If it does not, this immutable released binary cannot receive a product
grant under the current trust model via its public SSE/plugin API. The
minimal next engineering prerequisite is a first-party host-core
transport/reader contract (possibly upstream), not additional negative
source-only probes. Reuse the existing Go admission and UAP binder; do
not add a generic event broker, background service, broad feature flag,
or fake local signing key. This is an explicit unresolved dependency,
not permission to weaken native admission.

**Current acceptance flags:** `nativeObserverQualified=false`,
`nativeClockQualified=false`,
`notificationDeliveryQualified=false`. #643 remains Draft and v1.51.3
remains unpublished. Pi #644 premerge installed E2E and the verified
post-merge matrices are separate evidence and cannot qualify 2.2.6.

## Owner decision, 2026-11-13: family admission on macOS arm64

The owner chose range support over per-release pinning for official macOS arm64
OpenCode 2.x, accepting the weaker evidence this implies. This supersedes the
earlier "keep 2.2.6 production admission closed" position for that one platform.

- Admission is by who the live process is: OS-verified publisher (Developer ID,
  Team `5NZ4Q7NXJ4`, identifier `opencode`) plus a plain `2.x.y` at or above
  `2.0.21`. The process then resolves to the reviewed 2.0.21 reference image, so
  reader and clock rows are reused, not newly proven per release.
- Unchanged: Linux, Windows, V1, the wire format, and the runtime clock anchors.
  An unreviewed runtime that disagrees with the independent Go clock is refused.
- Residual risks, accepted: the signature verifies the executable, not the code it
  runs (`DYLD_*`, `BUN_BE_BUN`); reader and event-name semantics are assumed
  stable across 2.x; plain `serve` shows rare missed deliveries (4 of 53 runs,
  cause not found).
- Evidence: official 2.2.6 and 2.2.7 darwin arm64 delivery through the repo
  harness, refusal on an ad-hoc re-signed copy, and installed clock-skew refusal.
- Follow-up: V1 above 1.18.33 and darwin amd64 are not covered; the event path
  does not yet know the generation.
