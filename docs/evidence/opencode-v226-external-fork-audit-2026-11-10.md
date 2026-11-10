# External OpenCode 2.2.6 delivery fork — independent evidence audit (2026-11-10)

This is a scoped evidence review for [issue #642](https://github.com/777genius/agent-notifications/issues/642) and draft [PR #643](https://github.com/777genius/agent-notifications/pull/643). It is **not** a trust row, product qualification, merge approval or release note.

## Sources examined

- Reporter statement and sanitized receipt: [issue comment 6764416813](https://github.com/777genius/agent-notifications/issues/642#issuecomment-6764416813).
- Public fork: `alecsmirnov/agent-notifications`, [commit `eea8a320a1c8bb70d3831c38d88814ffcb4a1fc8`](https://github.com/alecsmirnov/agent-notifications/commit/eea8a320a1c8bb70d3831c38d88814ffcb4a1fc8); `FORK.md` documents the deviations.
- macOS ARM64 [CI run 42794842692](https://github.com/alecsmirnov/agent-notifications/actions/runs/42794842692), on the public commit. Artifact ID `13751774076` is named `claude-notifications-darwin-arm64`.
- Previously tested local build reported from `6c0f7c5d`. GitHub compare `6c0f7c5d...eea8a320` returns **diverged** (7 ahead, 5 behind), not a linear progression or byte-exact correspondence.

## Verified facts and boundaries

1. The public fork CI compiled a native macOS ARM64 Go binary, ran `--version` as its only executable smoke, and uploaded the artifact. **That CI does not run a real OpenCode turn, installed Agent Notifications delivery, a webhook, or a desktop banner.**
2. The reporter supplied a sanitized local `{"status":"submitted","desktop":"submitted"}` receipt and a `usernoted` banner line. They explicitly associate that delivery with the **earlier local build**, not the uploaded artifact. Useful delivery-path evidence, but no independently reproducible, byte-exact positive E2E for the public commit yet.
3. The fork adds the official 2.2.6 Darwin executable hash to a native reader descriptor and clock row, adds `tested_exact` to a vendored integrationctl dependency, and adds JS/Bun 1.4.4 pins. The descriptor reuses the V2 2.0.21 reader contract and the clock row reuses `103000000 / 430000000 / 224000000` ns bounds. **No independently established Bun 1.4.4/source-clock calibration or new native-reader qualification accompanies those grants.**
4. Its own `FORK.md` confirms that Bun 1.4.4 FFI differs from 1.4.2 and that the local macOS desktop helper was ad-hoc re-signed with `LSUIElement=true`. Neither circumstance is evidence that a published, signed Agent Notifications product has an accepted desktop delivery.
5. The fork's `third_party/integrationctl` vendoring and module `replace` are local-build expedients. They must not be copied into the product or treated as UAP's native trust review.

## Existing independent observations (not admission)

- [PR #643 native source E2E](https://github.com/777genius/agent-notifications/actions/runs/42768432303) passed genuine source/installed-denied scenarios on Linux x64, macOS 15 ARM64 and macOS 26 ARM64 with official OpenCode 2.2.6/Bun 1.4.4.
- Its Bun/Go E0 comparisons are actual platform observations, **not Q/R/T bounds or a source-to-binary guarantee**. The installed receiver correctly emits one fixed refusal diagnostic and zero webhook notifications. Thus `nativeObserverQualified=false`, `nativeClockQualified=false`, and `notificationDeliveryQualified=false` stay unchanged.
- Companion [UAP #823](https://github.com/777genius/universal-agent-plugins/pull/823) keeps both official 2.2.6 image candidates `native_source_pending`. Targeted `go test -race ./opencodehost` passed on [run 42765955912](https://github.com/777genius/universal-agent-plugins/actions/runs/42765955912).

## Acceptance evidence required before a positive E2E or merge

1. Reproduce the fork's positive delivery **with the byte-identical public CI artifact** and record only the artifact identity/build revision and redacted acceptance/presentation outcome. Independently verify a receiver built from the exact candidate HEAD; do not compare unrelated binaries.
2. Independently review the specific 2.2.6 V2 event reader and held-process provenance, and the Bun 1.4.4 FFI/JSValue-to-slot source behavior. Bind the runtime and native image evidence without caller-provided authority or fake clock rows.
3. Establish per-platform original `Event.Payload.created` source/JS `Date.now()` wall semantics, Go monotonic-to-wall translation, measured worst-case bounds plus reviewed error margins, reboot/clock adjustment/suspend/stale-event negatives, and invalidation on process/image changes.
4. Only then authorize exactly reviewed reader/clock cells across both projects; run accepted installed root completion, question, permission, terminal error, duplicate/queue/restart/revocation, desktop and webhook E2E on supported physical platforms. Run regression and security CI on final exact SHA.

**Decision:** retain the external fork as a diagnostic reference, not a patch to cherry-pick. Leave PR #643 and UAP #823 **Draft**, and do not merge/release or advertise OpenCode 2.2.6 notification support until the above admission gates are actually satisfied.
