# Windows Center surface diagnostic

The preceding read-only Windows 11 CI observation found a known WTSActive session and neither configured DisableNotificationCenter registry value. This rejects those two hypotheses for that snapshot, but does not explain why the earlier submitted TEST toast had no matching UI Automation action.

This separate opt-in experiment injects one Win+N chord on a disposable Windows client CI runner, with no toast Show, registration, callback activation or settings edits. Its default remains the read-only policy preflight. It is not a production adapter or native callback qualification.

Before/after observations enumerate only bounded top-level desktop window metadata from held, live, same-user/session Windows Shell owners. Records contain handles, PID, class, visibility and foreground state, never window text or UI Automation names. Identity and liveness checks bracket each record; incomplete enumeration remains explicit. New Shell owners are verified when observed.

The effect process repeats WTS/input desktop checks immediately before input and rejects an existing modifier/N press. Each successful injected down is tracked; only owned unreleased keys receive cleanup key-up. A failed cleanup remains unknown. GetAsyncKeyState and SendInput do not prove exclusive input ownership or successful Shell handling. There is no second chord or automatic retry after an uncertain result.

The controller correlates the native reports to the owned TEST nonce and child PID. Missing terminal evidence retains input uncertainty. Native Show count remains zero, and callback/navigation qualification remain false. Surface metadata changes are observations, not proof that Notification Center opened; EnumWindows does not cover every possible Shell surface.

Validation: strict TypeScript preflight and diff check; independent source review before CI compilation and the separate explicit dispatch. Actual native evidence is pending.
