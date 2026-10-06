# Windows Center surface diagnostic

The preceding read-only Windows 11 CI observation found a known WTSActive session and neither configured DisableNotificationCenter registry value. This rejects those two hypotheses for that snapshot, but does not explain why the earlier submitted TEST toast had no matching UI Automation action.

This separate opt-in experiment injects one Win+N chord on a disposable Windows client CI runner, with no toast Show, registration, callback activation or settings edits. Its default remains the read-only policy preflight. It is not a production adapter or native callback qualification.

Before/after observations enumerate only bounded top-level desktop window metadata from held, live, same-user/session Windows Shell owners. Records contain handles, PID, class, visibility and foreground state, never window text or UI Automation names. Identity and liveness checks bracket each record; incomplete enumeration remains explicit. New Shell owners are verified when observed.

The effect process repeats WTS/input desktop checks immediately before input and rejects an existing modifier/N press. Each successful injected down is tracked; only owned unreleased keys receive cleanup key-up. A failed cleanup remains unknown. GetAsyncKeyState and SendInput do not prove exclusive input ownership or successful Shell handling. There is no second chord or automatic retry after an uncertain result.

The controller correlates the native reports to the owned TEST nonce and child PID. Missing terminal evidence retains input uncertainty. Native Show count remains zero, and callback/navigation qualification remain false. Surface metadata changes are observations, not proof that Notification Center opened; EnumWindows does not cover every possible Shell surface.

Validation: strict TypeScript preflight and diff check; independent source review before CI compilation and each separate explicit dispatch. Native observations below are bound to their measured sources.

The guarded CI observation at `bc0714894fe3facefb2eaeb80aa721a6b6661d3b`, run 37478438576, refused input after 36 visited windows and exactly 16 Shell metadata rows; errors=0, live Shell and fresh Active desktop were true. The correlated terminal rejection proves inputAttempted=false and Show=0. Official artifact 11420301370 digest: `c4d3be64da2abda238bfae27ce7946beb879687c58971ec1107598982e657313`. This identifies the metadata row cap, without proving Center visibility or retrospectively qualifying the earlier missing-report run.

Based on that observed normal Shell state, the row cap became 32 with at most 6000 serialized UTF-8 JSON bytes, including commas, per snapshot. The 16-owner, 128-visit, cooperative two-second and identity checks remain. Both snapshot bodies plus wrappers fit the existing 16 KiB report-reader limit. That measured version retained hidden windows.

The subsequent 32-row experiment also refused input on the record cap, after 67 visits with live Shell and ready desktop. Completeness of a bounded metadata export is not input authority: only the independently retained Shell identity, fresh Active/input desktop, held-key guard and explicit TEST opt-in authorize the single chord. Truncation/errors/completion remain in both snapshots; a reported change is only a difference between sampled rows, never evidence of complete surface enumeration or Center opening. Root Shell identity or desktop failure still refuses input. Caps are not increased again.

Run 37481638808 on `cd2605d600379ecc0c386e9bee5d7df5e4d7c871` accepted one chord with four key events, no unreleased owned key and zero Show calls. Official artifact 11422560561 has digest `b399ba47b38e3e3927a08f68dabbd39bee2515d69195582d947ae5bfd5611b7f`; independent review verified source, archive and PID/nonce binding. Both identical samples stopped after 54 visits and 32 records, dominated by hidden Shell windows. This cannot establish Center visibility or absence.

The next diagnostic source exports only held-owner Shell windows observed as visible or foreground, explicitly marked `projection=visible_or_foreground_owned_shell`. Hidden rows no longer consume the export budget. The same visit, owner, row, byte and deadline limits remain. Completion describes only this projection; visibility flags are non-atomic observations and do not prove an unobscured Center or full Shell surface coverage. The separate input authority is unchanged. Native execution of this changed projection remains pending.
