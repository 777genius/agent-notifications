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

## Additional genuine-image stress, still E0 only

Draft #643 adds 12 actual embedded-Bun/Go clock comparisons on each observed Linux x64 and macOS ARM64 port, plus a timer-delayed wall/counter nonregression observation (commits `1cec7ed`, `688d755`). The explicit reports retain `nativeObserverQualified=false`, `nativeClockQualified=false` and `notificationDeliveryQualified=false`. They do not establish event-source Q, reader R, time translation T, native admission or desktop/webhook delivery. A failed observation is a useful evidence gap, not grounds for bypassing admission.

**Decision:** maintain `native_source_pending` in UAP and keep #643 Draft. The
exact Bun image is recognized for custody tests only; do not register an
OpenCode 2.2.6 trusted reader, clock-policy row or release claim based on this
source audit.
