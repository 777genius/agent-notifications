# OpenCode 2.2.6 official event-clock source audit — 2026-11-10

**Scope:** source lineage and remaining proof obligations for [#642](https://github.com/777genius/agent-notifications/issues/642). This is research evidence, **not** a production observer/clock qualification, a new trusted row, or positive notification E2E.

## Independently located upstream source

The official OpenCode [`v2.2.6` Git tag](https://github.com/anomalyco/opencode/tree/v2.2.6) resolves to commit `dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06` (checked against the GitHub Git-ref API, independently matching our real-source E2E's `OFFICIAL_TAG_SHA`).

For the local Darwin ARM64 ZIP, `gh release verify-asset v2.2.6` against
`anomalyco/opencode` verified the release attestation and digest
`15491608f6b0bc4debe5eac35eab629a2b07ea9a1fbac68ad53bcea59fcc4b45`;
the release attestation also names that tag's commit. A separate default
`gh attestation verify` query for SLSA build provenance returned HTTP 404 for
this digest. The release verification ties the downloaded bytes to the release,
but does not independently establish how the binary was built from the source.

At this immutable source commit:

- [`packages/core/src/bus.ts`, lines 510–533](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L510-L533) constructs `Event.Payload` in `makePayload`. It sets the envelope's `created` to **`yield* Clock.currentTimeMillis`**, then passes the envelope through `publishEvent`. This is not an event-data `time` field and is not supplied by the Agent Notifications plugin.
- [`packages/schema/src/event.ts`, lines 67–78 and 110](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/schema/src/event.ts#L67-L78) declares `PayloadBase.created` as a number and validates the serialized shape as finite. It does **not** by itself establish clock accuracy, immutability at a plugin boundary, or caller/process authority.
- [`packages/core/src/bus.ts`, lines 495–507](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L495-L507) publishes the payload to the live PubSub after listener notification. Stored/replayed event decoding may use `created ?? 0`, so replay and genuine first-ingress live events **must not** be conflated.
- Our [real official-image E2E](../../scripts/opencode-v226-source-e2e.py) independently observes native `session.execution.started`, `session.step.ended`, `session.execution.succeeded`, question/permission and terminal failure. It requires positive numeric `entry.created` and only checks that native event epoch is within a **coarse ±5-second runner wall window**. Source evidence and runtime observation agree on the field location; a five-second plausibility window is *not* a millisecond Q bound.

## Transitive Effect 4.1.0 default clock — pinned source, not a live grant

- OpenCode's official `v2.2.6` [root catalog](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/package.json) selects `effect: 4.1.0`. Its [`bun.lock`](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/bun.lock) independently records `effect@4.1.0` with an integrity digest (lockfile blob `4ce7c36cc46367698e28c883e6ba992c8866b62f`).
- The upstream [Effect `effect@4.1.0` tag](https://github.com/Effect-TS/effect/tree/effect%404.1.0), Git commit `9a91b1926a5fbfaffd302a7a41a94d4d53a1154e`, [`packages/effect/src/internal/effect.ts` lines 6362–6376](https://github.com/Effect-TS/effect/blob/9a91b1926a5fbfaffd302a7a41a94d4d53a1154e/packages/effect/src/internal/effect.ts#L6362-L6376) establishes the **default** `ClockRef` as `new ClockImpl()`. Its `currentTimeMillisUnsafe()` directly returns `Date.now()`, and `currentTimeMillis` is `sync(() => this.currentTimeMillisUnsafe())`.
- That same Effect source, [lines 6419–6435](https://github.com/Effect-TS/effect/blob/9a91b1926a5fbfaffd302a7a41a94d4d53a1154e/packages/effect/src/internal/effect.ts#L6419-L6435), implements `currentTimeNanos` using a **different** monotonic-anchored wall clock with a one-second re-anchor threshold. **Do not** attribute that behavior to `currentTimeMillis`, and do not infer a 1-second or 2-ms Q bound from either implementation.
- The source chain `OpenCode Bus.makePayload → Effect Clock.currentTimeMillis → default ClockImpl → Date.now()` is now traceable at pinned upstream revisions. This is evidence of a **default implementation**, not that every production publication runs under the unmodified default Clock reference or global `Date`: Effect services can be replaced by a layer, and plugins share an executable process. Compiler/Bun image provenance, production fiber context, source-wall bounds, wall adjustment, queueing and event replay still need independent live qualification.

## Independently verified source-origin and latency constraints — 2026-11-11

The same pinned official [`Bus.makePayload` source](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L510-L535) exposes two additional limitations that any new, release-independent native admission design must handle:

- **Envelope location and ID are not independent process provenance.** `makePayload` accepts caller-provided `options.location` (line 515) and `options.id` (line 520), and its clock is an injectable Effect service. These fields can be useful correlation hints, **not** independent evidence of the OS-held publisher, ownership, event authenticity or a trustworthy wall clock. A new reader must bind original live event delivery to the held process and source location through a separately reviewed trust path, not grant authority from these bytes.
- **Source `created` is not the delivery time.** The official `publish` first invokes `makePayload` and only then `publishEvent` (lines 529–533); durable publication can acquire an [aggregate lock](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L451-L486) before notifying subscribers. `notify` in turn invokes listeners before [publishing to live PubSub](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L495-L506). Thus a measured Bun↔Go clock delta alone **does not bound event queue/lock/listener delay**, nor establish replay resistance. Publication latency and dropped/stale events need a separate R/T stress budget and fail-closed policy.
- **Source validation cannot authorize unsupported releases.** Even though `created` is defined in the official Bus, a JS plugin receipt or caller-supplied digest cannot prove the executing source path, original event lifetime or absence of third-party overrides. Successful source E2E and wall-skew observation stay `E0/Q0`; they are not a new `qualifiedClockRows` entry.

**Safety decision:** do not promote the official 2.2.6 candidate or copy the 2.0.21 Q/R/T margins. A positive enabled delivery test is meaningful only *after* independent held-process reader, actual source-clock and publication-latency qualification. A future architecture may need an upstream first-party attested event endpoint or a strictly reviewed OS-held source adapter; source-visible `created`, `id` and `location` alone are insufficient.

## Exact trust boundaries still open

1. **Clock provenance (Q):** the pinned transitive Effect default `Clock.currentTimeMillis` uses `Date.now()` (source chain above), but independently verify the **live production fiber's** actual service/layer, Bun 1.4.4 runtime, any injection/override or global-Date mutation path, millisecond quantization, wall corrections and suspend/reboot effects. No measured event-source Q bound or production authority follows from the default implementation alone.
2. **Reader/transport (R):** prove the running official, OS-held process and *original* V2 event envelope can be authenticated by the native reader, not only copied by a JS plugin/subscriber. Bind the reader's event identity, ordering, location, process/boot lifetime and disposal to the existing private native provenance. No `ctx.app.version`, untrusted user paths, imported fixtures or externally provided digest may supply authority.
3. **Translation/freshness (T):** independently measure source event wall vs trusted Go clock source on real official 2.2.6/Bun 1.4.4 for each platform, with reviewed worst-case bounds and safety margins. Include positive and negative time shifts, queue/steer delays, replay, restart, provenance swap and revocation. The existing 103/430/224-ms older-image rows **cannot** be copied to this binary.
4. **Installed authorization:** only after Q/R/T and compatible UAP native-observer binding can a new trusted cell be considered. Then prove accepted root completion, question, permission and terminal error through installed 2.2.6 to consented webhook and applicable real desktop on final SHA, with negatives for replay, revocation, stale events and restart. Existing source-E2E `passed_denied_only` and E0 clock observations never satisfy that gate.

## Bun 1.4.4 FFI source checkpoint

The upstream `bun-v1.4.4` tag resolves to
[`38afee6c84fcf48c0edb2466aa4b27d287a59c30`](https://github.com/oven-sh/bun/tree/38afee6c84fcf48c0edb2466aa4b27d287a59c30).
Its [`ffi_body.rs`](https://github.com/oven-sh/bun/blob/38afee6c84fcf48c0edb2466aa4b27d287a59c30/src/runtime/ffi/ffi_body.rs)
SHA-256 is `111f0b5b74e210eb5cb798bcad2e9528bac99c82caa4e57587b40b9fc1a1011a`;
the 1.4.2 file is `73595190f84a807ff0dc7867afc4fac43cbca2f5d9fa3cd31c5be6a5cd165dfe`.
The 1.4.4 FFI trampoline exposes `Bun__FFI__jsValueToSlotSlow`, replacing the
previous separate integer conversion bindings. Its pinned
[`JSCFFIBridge.cpp`](https://github.com/oven-sh/bun/blob/38afee6c84fcf48c0edb2466aa4b27d287a59c30/src/jsc/bindings/JSCFFIBridge.cpp#L133-L159)
(SHA-256 `960b2a1dca2e9e7146a31848d6701f6004e20f17c9f95a09f7af5f6bc224f489`)
performs pointer-object handling and delegates conversion to JavaScriptCore's
`writeSlotFromJSValue`. This identifies the changed boundary to audit; it does
not independently prove the native pointer/u64 ABI, the OpenCode binary's source
provenance or a timing bound. The real-image E0 and successful fork delivery
remain observations, not substitutes for those checks.

The same Bun tag's [WebKit dependency definition](https://github.com/oven-sh/bun/blob/38afee6c84fcf48c0edb2466aa4b27d287a59c30/scripts/build/deps/webkit.ts#L1-L6)
defaults to WebKit commit `d6375c72affe0f70222cdcb45012b10f0bf3a9d5`.
Its [CurrentTime.cpp](https://github.com/oven-sh/WebKit/blob/d6375c72affe0f70222cdcb45012b10f0bf3a9d5/Source/WTF/wtf/CurrentTime.cpp)
has SHA-256 `917066d3283b21bfe5173eab3bf575c3d95740d0e34080007807f582083df866`,
identical to the pinned CurrentTime source previously audited for Bun 1.4.2.
On Darwin that source reads wall time using `clock_gettime(CLOCK_REALTIME)`
and continuous time using `mach_continuous_time`. This narrows the source
delta, but a build can override the WebKit revision; byte-identical source for
this file does not itself prove the shipped executable's full build provenance,
the active Effect clock, or a scheduling/error bound.

At that WebKit revision, the [FFI conversion source](https://github.com/oven-sh/WebKit/blob/d6375c72affe0f70222cdcb45012b10f0bf3a9d5/Source/JavaScriptCore/ffi/FFIConversions.cpp#L311-L413)
(SHA-256 `e7c979e4a01e3e64306579cfb533b7b457dbc4ab2eaac9c839f483a0dd3e55f6`)
maps null pointer arguments to zero and dispatches pointer and unsigned-64-bit
arguments to separate conversion paths; its u64 return path constructs a
BigInt. The product additionally rejects non-safe or misaligned numeric
addresses and validates u64 arithmetic. This narrows the Bun slow-path audit,
but does not by itself cover every JIT/fast path, ABI, buffer lifetime or
prove which source was compiled into the executing OpenCode binary.

The same pinned [FFI inline-cache implementation](https://github.com/oven-sh/WebKit/blob/d6375c72affe0f70222cdcb45012b10f0bf3a9d5/Source/JavaScriptCore/ffi/FFIICStub.cpp#L163-L223)
and [DFG implementation](https://github.com/oven-sh/WebKit/blob/d6375c72affe0f70222cdcb45012b10f0bf3a9d5/Source/JavaScriptCore/ffi/FFIDFGCodegen.cpp#L246-L304)
also contain separate integer/BigInt and numeric-pointer conversion paths,
with cases outside their fast paths delegated to slower handling. On the local
official macOS ARM64 2.2.6 image, an optional E0 stress run completed 4,096
additional native-clock samples after twelve independent Go comparisons;
counter and wall observations did not regress. This demonstrates repeated
calls on one machine, not which JIT tier executed or a worst-case bound.

The exact official Darwin ARM64 image and Bun 1.4.4 tuple can now pass the
separate file-custody check. With `--production-source`, the E0 test exercises
the actual `createDarwinClock()`/held-image path, rather than only an equivalent
test construction. On macOS 15.6.1 it completed twelve Go comparisons and
4,096 repeat samples. This adds no compiled clock-policy row or UAP observer
grant; the installed 2.2.6 host remains denied until those separate gates are
properly qualified.

A later production-source E0 attempt failed the former combined clock
containment check; the exact failed subcondition was not recorded. After adding
separate, content-free span/tick/wall diagnostic codes, a new run passed,
including a deliberate wrong-image rejection. The original failure is not
explained by that success and must not be discarded when considering a bound.

Further local production-source sampling separated the subconditions. One of
20 runs failed the 224 ms calibration span check. In a subsequent 40-run study,
two spans exceeded it (approximately 528 ms and 271 ms); the other 38 runs
passed and the largest passing span was approximately 100 ms. These samples
show that the combined sample/helper span can exceed the bound on this host; they are neither
a universal worst case nor proof of the earlier unclassified failure. Activation
can retry an overlong but otherwise valid bracket around a reaped helper within its original two-second
budget; it never accepts the overlong sample or changes the configured bound.
In a separate TEST-only build using the same unqualified prototype data, an
injected 300 ms delay after the first helper was followed by successful
completion, question, permission and error webhooks. This exercises the retry
against real OpenCode/Bun and Go processes; its copied trust data remain
unqualified and the experiment is not a release E2E.

On macOS 26 CI, exact candidate `2b7dc8a4` failed E0 with an observed
466.9 ms bracket against the 224 ms limit. This is retained as a failed
observation, not counted as a pass. The E0 harness now mirrors the bounded
activation retry: at most three fully validated brackets per comparison within
two seconds, never accepting an overlong bracket. Successful output lists every
discarded duration; a failed run reports the final failed bracket. A local
TEST injection of a 300 ms delay produced a
322.5 ms discarded bracket followed by 12 valid comparisons; it does not
establish a universal bound or grant qualification.

## Additional genuine-image stress, still E0 only

Draft #643 adds 12 actual embedded-Bun/Go clock comparisons on each observed Linux x64 and macOS ARM64 port, plus a timer-delayed wall/counter nonregression observation (commits `1cec7ed`, `688d755`). The explicit reports retain `nativeObserverQualified=false`, `nativeClockQualified=false` and `notificationDeliveryQualified=false`. They do not establish event-source Q, reader R, time translation T, native admission or desktop/webhook delivery. A failed observation is a useful evidence gap, not grounds for bypassing admission.

The official Darwin ARM64 executable also passes strict Apple code-signature
verification with identifier `opencode`, Apple generic anchor and Developer ID
team `5NZ4Q7NXJ4`. The same requirement rejects a modified byte, and a wrong
team requirement rejects the unmodified image. This is a possible building
block for future publisher-bound host discovery; it does not by itself prove
event semantics or a clock bound, or authorize other builds by the same signer.
`spctl --assess --type execute` rejects this
standalone CLI as not being an app, so no notarization conclusion is inferred
from the signature check.

In a separate isolated macOS service run, the Security framework also
accepted the live OpenCode PID under that publisher requirement; the same
requirement rejected the test harness process and the wrong team requirement
rejected OpenCode. This is a point-in-time live-signature observation. It does
not prove a stable process lifetime, prevent PID reuse or executable swaps,
establish source/event semantics, or authorize an otherwise unqualified host.
Any production use would need to compose and independently review it with
the existing held-process and clock checks.

The Darwin held-image path now additionally checks this live publisher for the
exact 2.2.6 candidate before its existing final process, mapping and lifetime
revalidation. A focused native test accepts the official test process and
rejects unrelated and reaped processes. This adds a required condition only;
it does not register a qualified observer or clock row, expand the image
allowlist, or establish generic V2 support.

For the exact macOS ARM64 2.2.6 image, the candidate wall witness now reads
Darwin `clock_gettime(CLOCK_REALTIME)` independently of JavaScript `Date.now`.
The existing qualified images retain their previous clock implementation. On
the real image, 12 comparisons with an independent Go helper and 4,096 native
samples passed with JavaScript wall time shifted forward 12 seconds; a wrong
image was refused. This is an E0 observation, not qualification of the new
clock algorithm or proof of the producer's event-time bound.

An isolated TEST-only prototype with copied, unqualified trust rows delivered
four real OpenCode event webhooks and produced five correlated native macOS
banners. Separate runs first confirmed a completion webhook in the same host
and installed receiver, then shifted its event source by plus or minus 12
seconds and observed no subsequent webhook. The control rules out a receiver
that never worked, but does not identify the rejection phase. These runs
exercise behavior only: their copied trust rows are not evidence for production
admission.

The reported `serve --service` entry was also exercised on macOS ARM64 with
an isolated service configuration and loopback model provider. The ordinary
candidate refused startup and sent no webhook. A separate TEST-only prototype
with unqualified copied trust rows delivered the four event webhooks and had
five desktop submissions correlated with actual macOS banners. Service mode
changes the host's working directory and credential handling; testing this
entry therefore covers the reporter's launch path, but does not grant its
observer or clock qualification.

On the same real service path, a second TEST plugin attempted to rewrite the
event ID, creation time and nested session ID in its own subscription. A separate subscriber
observed the original matching fields for execution start, step end and
completion, and the TEST-only prototype delivered the expected webhooks.
This is evidence for this particular subscription-isolation scenario, not a
general protection against arbitrary code sharing the OpenCode process or a
reader qualification. The private correlation values are never exported.

An independent TEST-only installed prototype exposed a completion failure
after automatic compaction: the real 2.2.6 context contained a compaction
marker rather than the original user row. The marker ID corresponded to the
native compaction-start event. The V2 adapter now accepts that replacement
only after a matching ordered, same-scope automatic start/end in the current
run and an exact marker in the final context. A repeated real-host TEST run
delivered one completion for both retry and automatic compaction; negative
unit cases cover manual, missing, reordered, foreign-scope, wrong-marker and
new-user paths. These observations use unqualified prototype trust data and
do not grant production admission.

## Actual local model in an unqualified TEST build

On macOS ARM64, the pinned official OpenCode 2.2.6 executable ran turns against
the locally installed Ollama `qwen2.5-coder:0.5b` model through a TEST-only
OpenAI-compatible loopback proxy. The proxy verified the model name and a real
`stop` response; no external model account or user authorization file was used.
The installed notification binary was a TEST-only prototype with unqualified
reader/clock data, not the production candidate.

The first native turn succeeded but its expected completion webhook did not
arrive within the harness deadline. Four later turns using that same prototype
binary each delivered one completion webhook. Nine further turns using a
separate TEST-only build with composition diagnostics enabled also each
delivered one. The first missed delivery was not reproduced or explained; its
native execution and terminal step succeeded. These results demonstrate a real
local model path but do not establish production qualification or reliability.

For comparison, the clean ordinary product binary at `2ab6c3e3` delivered a
completion webhook from the pinned OpenCode 2.0.21 Darwin ARM64 executable
using the same real local model and TEST-only loopback proxy. The packed SDK
contents matched the public `3e438a2` source commit. The temporary harness
was modified to route only that test provider to Ollama; no desktop result or
OpenCode 2.2.6 production qualification is implied.

The real-host source harness now exercises automatic compaction and the final
native assistant proof in CI. In a local TEST-only delivery run it observed a
new completion after compaction; the pre-fix TEST binary failed the same
delivery assertion. Additional isolated prototype scenarios passed for
child-tool execution, steering, separate locations and same-session recovery
after queueing, dismissal, permission rejection and provider failure. These
remain prototype observations, not qualified production notification E2E.
After a successful control delivery, a separate TEST installation preserved a
foreign edit to the installed plugin and emitted no completion or question
webhook across the full negative observation windows. The semantic harness can
repeat this as `--cases success,foreign-success,foreign-question` in its own
disposable installation; it never repairs or deletes the edited file.

Two further runs repeated the same-host control and ±12-second shift using a
separate TEST-only build that forced the existing fixed-code composition
diagnostics on. Each run recorded one successful activation, a control
delivery, and `prep.catch.birth` after the shifted turn; no shifted webhook was
observed. This identifies the refusal in that instrumented prototype, not in
the ordinary build. The instrumentation and copied trust rows must not be
treated as a production binary or as independent qualification.

**Decision:** maintain `native_source_pending` in UAP and keep #643 Draft. The
exact Bun image is recognized for custody tests only; do not register an
OpenCode 2.2.6 trusted reader, clock-policy row or release claim based on this
source audit.
