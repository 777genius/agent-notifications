# OpenCode notifications: cross-platform delivery plan

Status: implementation contract, 2026-09-30. Astra xhigh architecture review completed before coding. The existing Linux amd64 integration is the baseline. No Agent Notifications version bump, tag, release, or package publication belongs to this work.

## Supported targets

Match the current product release matrix: macOS arm64 and amd64, Linux arm64 and amd64, Windows amd64. Windows arm64 is not a released product target. A passing cross-build is not native runtime evidence.

| Host | Desktop port | Qualification |
| --- | --- | --- |
| macOS arm64, amd64 | Existing managed `StructuredDelivery` and attested native app | Native desktop session, explicit notification permission, OS accepted receipt and visible banner |
| Linux arm64, amd64 | Existing `FreedesktopDelivery` | Native process, private D-Bus session, real dunst/Xvfb banner |
| Windows amd64 | Existing `WindowsToastDelivery` | Native Windows desktop session and actual toast observation |

The UAP OpenCode observer and neutral Go wire remain shared. Product-specific policy, delivery, registration and lifecycle stay in Agent Notifications. Do not add a second notifier framework or silently fall back to an unverified Mac notifier.

## Admission and lifecycle

1. Extend `setup-opencode` and its current event gate to the five targets using one platform table, native binary names and platform-specific executable validation. Linux amd64 must retain its observed behavior. Installation owns exactly one JS plugin and one product binary through the existing CAS transaction. Foreign or edited files remain conflicts.
2. OpenCode desktop/webhook consent remains independent of portable MCP `ledger.Enabled` and `policy.Enabled`. In particular, installing OpenCode must not enable portable notifications. The existing `ManagedInstallation.Acquire` requires portable enablement, so macOS needs a consumer-specific native lease: revalidate the exact installation, registration, owned files and `route.openCodeNotifications.desktop` under the component lock, then pin the attested native bundle through handoff. Do not use `AcquireSetupLease` as delivery authority without those checks.
3. On macOS, prepare the verified native bundle and private spool before enabling desktop consent. Surface permission status/request through explicit setup only; event handling never prompts. Webhook-only setup does not require a desktop helper.
4. `remove` first revokes the OpenCode channels with CAS, preserving unrelated policy and consumers; artifact cleanup follows. The revoke path must work even if the macOS native helper is missing or damaged. It may report a separate cleanup conflict. A previously admitted bounded delivery can finish, but a subsequently observed event, including from an already loaded JS plugin, must be denied.
5. Windows JS IPC must accept canonical absolute Windows paths and spawn an owned `.exe` with `shell:false`. Preserve a narrow OS environment needed for process startup, home/config and toast handoff. No broad inheritance of private environment. The Windows backend must resolve its system PowerShell safely without depending on a user-controlled `PATH`. Inspect toast identity/shortcut behavior in a clean profile before claiming banner support.

## PR slices

| Order | Scope | Approximate changed LOC |
| --- | --- | ---: |
| UAP | Make OpenCode placement fixtures native on Windows; add drive, UNC and root cases. Change production API only if a real mismatch appears. | 100-250 |
| Notifications 1 | Five-target setup/gate, binary validation, Windows IPC/environment, Windows delivery wiring, Linux arm64. | 750-1,150 |
| Notifications 2 | macOS consent-specific native lease, helper/spool setup, permission and revoke behavior. | 900-1,600 |
| Qualification | Focused platform tests, native E2E harness/evidence and accurate docs; fix Windows toast identity only if clean-profile E2E exposes it. | 550-1,000 |

UAP PR #359 changed Windows placement tests and CI only; it did not change the observer package or runtime API. Agent Notifications uses the published observer `0.1.0`, which passed the native Windows E2E. No UAP `0.1.1` publication or dependency PR is needed for this slice. No local `replace` or floating `main` belongs in a mergeable consumer PR.

## Regression checks

Tests must go red for a plausible failure: Windows absolute path rejected before spawn, `.exe` rejected for lack of Unix execute bits, wrong binary architecture accepted, foreign/edited plugin overwritten, Mac OpenCode denied because portable MCP is off, Mac revoke blocked by a damaged helper, or unrelated portable policy changed. Keep the existing Linux behavior tests. Verify install/update/remove/recover and a loaded old plugin after removal.

## Native E2E acceptance

Run agent commands only inside **new disposable projects** with isolated OpenCode config, data, cache, control and runtime roots. Record exact product/UAP source SHA, built binary and JS digest, OpenCode version and host identity. Do not run an agent or terminal runtime inside a real user project. Prefer the same OpenCode 1.18.33 version used in Linux qualification; do not update the user's global OpenCode just for testing.

For each native target, load the actual installed JS in OpenCode and trigger four real events: completion, question, permission request and terminal error. Observe one real system banner per event where desktop is enabled, and one loopback webhook POST with generic content and no test secret. Verify channel updates, restart behavior, removal and no delivery from a still loaded old plugin after revocation. A direct `opencode-event` invocation, unit test, CI build or OS submit receipt alone cannot establish visual E2E. macOS Intel must run natively; Rosetta is labelled separately. Windows needs an interactive desktop session.

The final support claim is per target and channel. If a native environment or OS permission is unavailable, leave that target unqualified and state the blocker. Do not release Agent Notifications as cross-platform until the target matrix has real evidence.

## Qualification snapshot, 2026-09-30

The native OpenCode 1.18.33 completion lifecycle passed in disposable projects on all five targets: install, one webhook per turn before and after update/restart, no webhook from an already loaded plugin after remove, and rejection by a restored old executable. These runs verified the published UAP observer `0.1.0`. The user confirmed a visible `Task completed` banner on macOS arm64 with the signed native helper.

The CI runners did not observe desktop banners. macOS Intel, Linux and Windows desktop visuals remain unqualified, as do native desktop visuals for question, permission and error events. Only the user's current Mac is available as an interactive machine. The implementation can be merged behind explicit setup, but this evidence does not authorize a cross-platform Agent Notifications release.
