# OpenCode 2.2.6 official event-clock source audit — 2026-11-10

**Scope:** source lineage and remaining proof obligations for [#642](https://github.com/777genius/agent-notifications/issues/642). This is research evidence, **not** a production observer/clock qualification, a new trusted row, or positive notification E2E.

## Independently located upstream source

The official OpenCode [`v2.2.6` Git tag](https://github.com/anomalyco/opencode/tree/v2.2.6) resolves to commit `dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06` (checked against the GitHub Git-ref API, independently matching our real-source E2E's `OFFICIAL_TAG_SHA`).

At this immutable source commit:

- [`packages/core/src/bus.ts`, lines 510–533](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L510-L533) constructs `Event.Payload` in `makePayload`. It sets the envelope's `created` to **`yield* Clock.currentTimeMillis`**, then passes the envelope through `publishEvent`. This is not an event-data `time` field and is not supplied by the Agent Notifications plugin.
- [`packages/schema/src/event.ts`, lines 67–78 and 110](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/schema/src/event.ts#L67-L78) declares `PayloadBase.created` as a number and validates the serialized shape as finite. It does **not** by itself establish clock accuracy, immutability at a plugin boundary, or caller/process authority.
- [`packages/core/src/bus.ts`, lines 495–507](https://github.com/anomalyco/opencode/blob/dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06/packages/core/src/bus.ts#L495-L507) publishes the payload to the live PubSub after listener notification. Stored/replayed event decoding may use `created ?? 0`, so replay and genuine first-ingress live events **must not** be conflated.
- Our [real official-image E2E](../../scripts/opencode-v226-source-e2e.py) independently observes native `session.execution.started`, `session.step.ended`, `session.execution.succeeded`, question/permission and terminal failure. It requires positive numeric `entry.created` and only checks that native event epoch is within a **coarse ±5-second runner wall window**. Source evidence and runtime observation agree on the field location; a five-second plausibility window is *not* a millisecond Q bound.

## Exact trust boundaries still open

1. **Clock provenance (Q):** upstream's `Clock.currentTimeMillis` is an Effect clock service. Independently audit the concrete production service/layer, its Bun 1.4.4 wall-clock implementation, any injection/override path, millisecond quantization, wall corrections and suspend/reboot effects. Do not assume the source value equals an uncompromised host `Date.now()` just because both are integers.
2. **Reader/transport (R):** prove the running official, OS-held process and *original* V2 event envelope can be authenticated by the native reader, not only copied by a JS plugin/subscriber. Bind the reader's event identity, ordering, location, process/boot lifetime and disposal to the existing private native provenance. No `ctx.app.version`, untrusted user paths, imported fixtures or externally provided digest may supply authority.
3. **Translation/freshness (T):** independently measure source event wall vs trusted Go clock source on real official 2.2.6/Bun 1.4.4 for each platform, with reviewed worst-case bounds and safety margins. Include positive and negative time shifts, queue/steer delays, replay, restart, provenance swap and revocation. The existing 103/430/224-ms older-image rows **cannot** be copied to this binary.
4. **Installed authorization:** only after Q/R/T and compatible UAP native-observer binding can a new trusted cell be considered. Then prove accepted root completion, question, permission and terminal error through installed 2.2.6 to consented webhook and applicable real desktop on final SHA, with negatives for replay, revocation, stale events and restart. Existing source-E2E `passed_denied_only` and E0 clock observations never satisfy that gate.

## Additional genuine-image stress, still E0 only

Draft #643 adds 12 actual embedded-Bun/Go clock comparisons on each observed Linux x64 and macOS ARM64 port, plus a timer-delayed wall/counter nonregression observation (commits `1cec7ed`, `688d755`). The explicit reports retain `nativeObserverQualified=false`, `nativeClockQualified=false` and `notificationDeliveryQualified=false`. They do not establish event-source Q, reader R, time translation T, native admission or desktop/webhook delivery. A failed observation is a useful evidence gap, not grounds for bypassing admission.

**Decision:** maintain `native_source_pending` in UAP and keep #643 Draft. Do not register an OpenCode 2.2.6 trusted reader, Bun image, clock-policy row or release claim based on this source audit.
