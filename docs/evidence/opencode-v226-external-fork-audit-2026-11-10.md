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

## Local test of the exact public fork artifact — 2026-11-11

On macOS 15.6.1 ARM64, the exact public CI artifact (ZIP SHA-256
`d67b6f596470596dd2e2fb64907d1ab1bc38e7342f2f90fe51affe3ec2360406`,
matching GitHub's artifact digest; executable SHA-256
`511e8af2718bf6f07d11af480d496a37be9181277148fb54a6df4a59b3d05e6c`)
was installed in an isolated TEST home with the official OpenCode 2.2.6
ARM64 image and a loopback-only model provider and webhook. The initial test
failed `expected_delivery_missing`, but **that failure was a test setup error**:
the receiver uses its managed policy snapshot, while the test enabled the
webhook only in an unrelated ambient config file. A separately built diagnostic
variant reported `channels_disabled`. That variant and a test which altered an
already installed plugin are not evidence of the exact artifact's behavior.

After configuring the managed OpenCode policy through the revision-fenced
`config inspect` and `config edit` commands, the **unmodified, digest-verified
public CI artifact** delivered the expected fixed-copy webhooks for genuine
completion, question, permission and terminal-error events. The question and
permission webhooks were observed before their pending requests were answered.
This corrects the earlier negative conclusion. The run used macOS 15.6.1 and a
local scripted model, not the reporter's macOS 26 or a physical desktop banner.
It demonstrates functional delivery by this fork, but does not qualify its
reused reader/clock bounds or establish production admission safety.

With the same public artifact and managed webhook, two additional runs shifted
the source process `Date.now()` by +12 seconds and -12 seconds. Each observed
the corresponding source timestamp mismatch against an unshifted witness and
no webhook within 15 seconds. The test cannot identify where delivery was
stopped; these bounded observations do not establish an admission rejection,
a worst-case timing bound, or resistance to other clock and replay cases.

## Isolated prototype on the current PR base — 2026-11-11

A separate TEST-only build based on PR commit `91829eb5` used a local copy of
the integrationctl dependency and copied the fork's **unqualified** 2.2.6
reader and clock data solely to find integration failures. Its executable
SHA-256 was `f1adada59cad4a0f6bcf38314973b37b3dbc4a0f96a14316d978952e9641fcf9`.
With the official macOS ARM64 OpenCode 2.2.6 image, the managed install and
scripted local provider, completion, question, permission and error each reached
the configured loopback webhook. Separate ±12-second source clock shifts
produced no webhook in the bounded observation window; the rejection phase was
not established.

A second TEST-only build, differing by diagnostic logging (executable SHA-256
`6cfc1c22909c0059de148250030e45232e9bd593e61e2a660b038e6e7fd600fc`), used
the verified signed v1.51.4 `ClaudeNotifier.app` (archive SHA-256
`61775c6c2535c4f4c829012ff0e07a48f3af26fceb46f1d262e895ac26b0b551`).
Its managed receiver reported five desktop and webhook submissions across the
four scenarios; the question turn also completed. For the test window, the
macOS `usernoted` log contained five matching `presenting`/`banner` lines for
the application's bundle identifier. This observes OS presentation on macOS
15.6.1; it does not prove that a person saw each banner or establish behavior on
macOS 26.

These builds intentionally copy unqualified source/clock claims for a local
experiment. They are neither PR artifacts nor safe release candidates. The
positive result isolates the remaining work to authenticating and qualifying
the actual 2.2.6 source, reader and clock contracts rather than implementing a
second notification transport.

## Existing independent observations (not admission)

- [PR #643 native source E2E](https://github.com/777genius/agent-notifications/actions/runs/42768432303) passed genuine source/installed-denied scenarios on Linux x64, macOS 15 ARM64 and macOS 26 ARM64 with official OpenCode 2.2.6/Bun 1.4.4.
- Its Bun/Go E0 comparisons are actual platform observations, **not Q/R/T bounds or a source-to-binary guarantee**. The installed receiver correctly emits one fixed refusal diagnostic and zero webhook notifications. Thus `nativeObserverQualified=false`, `nativeClockQualified=false`, and `notificationDeliveryQualified=false` stay unchanged.
- Companion [UAP #823](https://github.com/777genius/universal-agent-plugins/pull/823) keeps both official 2.2.6 image candidates `native_source_pending`. Targeted `go test -race ./opencodehost` passed on [run 42765955912](https://github.com/777genius/universal-agent-plugins/actions/runs/42765955912).

## Windows x64 observation — 2026-11-11

- [Exact-head CI #42926889289](https://github.com/777genius/agent-notifications/actions/runs/42926889289) independently fetched the official OpenCode 2.2.6 Windows x64 ZIP (SHA-256 `f75c16b33b8523fe1ff41e7d97e3ae654a47d59a0e327def24db2d9291bb24be`), found one executable, and checked its reported version. Its sanitized [receipt artifact #13816093110](https://github.com/777genius/agent-notifications/actions/runs/42926889289/artifacts/13816093110) records executable SHA-256 `98cb0aa854c7c015cf6febafa7afb872c359bfe5a823a526f3b4fd347a9e9677` and all three qualification flags `false`.
- The corresponding Windows candidate row is **image discovery only**. There is no native V2 reader descriptor, trusted clock row, Windows source-event / installed OpenCode turn or accepted desktop/webhook delivery. The first Windows CI run's Go regressions and `--version` did not constitute native notification E2E. Later exact-image tests below supersede that initial coverage gap, **not** the qualification gap.

## Later official Windows x64 native source + denied installation — 2026-11-11

- On exact AN PR #643 SHA [`a7ccc01e925f7a2af01178d55b3657c926fef659`](https://github.com/777genius/agent-notifications/commit/a7ccc01e925f7a2af01178d55b3657c926fef659), [OpenCode 2.2.6 workflow #42995343360](https://github.com/777genius/agent-notifications/actions/runs/42995343360) completed **4/4 SUCCESS**: Linux x64, macOS 15/26 ARM64, Windows 2022. Unlike the earlier archive/version check, the Windows job includes the official image source scenario and an installed *denied* receiver. This closes the Windows source/negative-E2E coverage gap, not the native reader/clock qualification gap.
- The official Windows executable SHA-256 `98cb0aa854c7c015cf6febafa7afb872c359bfe5a823a526f3b4fd347a9e9677` agrees with the observed AN candidate table. No trusted clock-policy row or native reader descriptor was created for that image. The receiver's expected result is a content-free denial and **zero webhooks**; this is not evidence of consented positive delivery or a Windows desktop banner.
- [UAP Draft #850](https://github.com/777genius/universal-agent-plugins/pull/850) stages this Windows image as `native_source_pending`, explicitly with an empty `EvidenceID` and all-zero `EvidenceHashes`. Generic V2 Skills/MCP compatibility remains separate from native notification admission. Any workflow that is queued, skipped or unrelated to source evidence must not be described as passed.

## Acceptance evidence required before a positive E2E or merge

1. The public CI artifact's installed webhook delivery was reproduced as described above. Still independently verify a receiver built from the exact candidate HEAD and the applicable native desktop presentation; do not compare unrelated binaries.
2. Independently review the specific 2.2.6 V2 event reader and held-process provenance, and the Bun 1.4.4 FFI/JSValue-to-slot source behavior. Bind the runtime and native image evidence without caller-provided authority or fake clock rows.
3. Establish per-platform original `Event.Payload.created` source/JS `Date.now()` wall semantics, Go monotonic-to-wall translation, measured worst-case bounds plus reviewed error margins, reboot/clock adjustment/suspend/stale-event negatives, and invalidation on process/image changes.
4. Only then authorize exactly reviewed reader/clock cells across both projects; run accepted installed root completion, question, permission, terminal error, duplicate/queue/restart/revocation, desktop and webhook E2E on supported physical platforms. Run regression and security CI on final exact SHA.

**Decision:** retain the external fork as a diagnostic reference, not a patch to cherry-pick. Leave AN PR #643 **Draft**. UAP #823 is already **MERGED** with `native_source_pending` only; follow-up UAP #850 is Draft and only stages the Windows source candidate. Do not merge #643, release or advertise OpenCode 2.2.6 notification support until the above admission gates are actually satisfied.
