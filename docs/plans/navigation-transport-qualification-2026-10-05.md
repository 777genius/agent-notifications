# Navigation transport qualification

Status: partial evidence, fast path not qualified. Date: 2026-10-05.

Implementation base: `cccc0208358763c4a2650ed84e51145ec3d5a4f1`.
Scope follows `notification-navigation-adapters-plan-2026-10-03.md` P2.

## macOS public API findings

- `NSWorkspace.open(_:withApplicationAt:configuration:completionHandler:)` selects an application URL. It does not accept a verified process endpoint. Replacing static verification with process verification while retaining this opener does not establish receiver binding.
- Apple Events support both `typeKernelProcessID` and `typeMachPort`. PID addressing alone leaves exit/reuse between verification and dispatch unproven. A Mach port descriptor establishes an address, but its existence alone does not establish the receiver's signing identity.
- Apple's documented `kSecGuestAttributeMachPort` is explicitly **not implemented**. Therefore the proposed direct bridge from an Apple Event destination port to `SecCodeCopyGuestWithAttributes` cannot be assumed to work.
- `kSecGuestAttributeAudit` exists, but the documentation examined does not establish how a third-party sender obtains an authenticated audit token for an Apple Event destination before handing it the URL. An authenticated reply after submission would be too late to prevent a wrong first receiver.

These findings reject one assumed implementation route. They do not prove all public transports impossible. No private API, PID lookup convention, or inferred success substitutes for endpoint qualification.

## Decision and next evidence

Keep full static validation and existing opener during measurement work. Do not enable a dynamic-validation fast path yet.

For a candidate transport, require a documented endpoint identity mechanism, bounded handoff, supported Codex distribution, resource-validation guarantees, TCC behavior, and synthetic exit/relaunch/reused-PID/two-copy tests. Then test the actual signed client in an isolated OS account or VM with synthetic data. The user now explicitly permits native testing on their Mac, using synthetic notifications/chats. Executing a tampered copy remains unqualified without proving separate profile/singleton isolation or using a disposable OS account/VM.

Windows and Linux remain unqualified; Linux delivery or cross-compilation alone does not prove durable native callback or selected-client routing. The native notification callback with action `none` is observed; one targeted-chat E2E also passed, with user-confirmed visual navigation and the existing static verifier/opener.

## Primary sources

- [Apple typeKernelProcessID and typeMachPort](https://developer.apple.com/documentation/applicationservices/apple_event_manager/1542936-typekernelprocessid)
- [Apple guest attribute keys](https://developer.apple.com/documentation/security/guest-attribute-dictionary-keys)
- [Apple kSecGuestAttributeMachPort: not implemented](https://developer.apple.com/documentation/security/ksecguestattributemachport?language=objc)
- [Apple SecCodeCopyGuestWithAttributes](https://developer.apple.com/documentation/security/seccodecopyguestwithattributes(_:_:_:_:))
- [Apple NSWorkspace open](https://developer.apple.com/documentation/appkit/nsworkspace/open(_:withapplicationat:configuration:completionhandler:))

## Native public transport fixtures

The reproducible opt-in runner is `scripts/navigation-native-probes.py --execute-test-fixtures`. It compiles only a prehashed source snapshot, records binary hashes and negative phase output, and creates expiring disposable receiver applications. Independent read-only review accepted the observer-liveness, strict event-log and failure-evidence fixes.

- Public PID-to-Mach-port coercion failed with `-1700`, despite an existing own-process port. This candidate is rejected.
- Public `GetProcessForPID` via a small C bridge produced a ProcessSerialNumber descriptor. These SDK calls are deprecated and unavailable directly to Swift, which is a portability limitation.
- Two separate copies with the same test bundle ID were alive during dispatch. Only the selected receiver received GURL. After its controlled exit/relaunch the saved old PSN failed with `-600`; the restarted and other receiver remained alive and received no events.
- The no-prompt permission preflight returned `-1744`, while the actual GURL send succeeded. Permission preflight alone is therefore not a valid verdict for this fixture. No production client was activated.

This is one native restart observation on macOS 15.6.1 arm64. It does not prove PID reuse, application update, authenticated signing binding, actual Codex routing, or all supported OS releases. No fast path is enabled.

## Installed distribution resource observation

Read-only observation of `/Applications/ChatGPT.app`: Codex 26.930.51102, Team `2DC432GLL2`, Electron package 42.3.0. The observed ASAR header matches its embedded SHA256; embedded-ASAR-integrity and only-load-app-from-ASAR fuses are enabled. However, its ASAR manifest declares 401 unpacked files, including 72 JavaScript files (236,969 bytes) and reachable module entry points.

Electron 42.3.0 source returns unpacked file metadata before loading archive integrity and directly reads unpacked files from the filesystem. Thus these two fuses alone do not establish executable resource integrity for dynamic-only validation. Main/Renderer/GPU retain library validation; the Service helper disables it. Snapshot integrity has its own upstream validation and should not be assumed absent. Actual customized runtime enforcement and tampered-distribution isolation are unproven.

Full existing static validation remains required. A successful process identity check alone cannot replace that postcondition. Evidence and the complete observed unpacked JavaScript manifest are under `docs/evidence/navigation/2026-10-05/installed-resource-observation.json`.

## Observed baseline

50 read-only calls through the real callback handler, executor and static verifier passed. The opener was a spy: no native handoff or client activation is claimed. Verification p50=339.013ms, p95=478.540ms; first/max=2871.355ms. Queue p95=0.043ms. These samples come from one measurement process, not 50 fresh helper callbacks; native cold-start measurement remains required.

The separate unique test notifier submitted an action-none notification. After the user's click, its native OS callback logged `ignored` in 0.224ms. This proves notification reactivation/callback delivery for that fixture, not navigation or a measured sender-exit trace.

Additional primary sources:

- [Apple ProcessSerialNumber](https://developer.apple.com/documentation/applicationservices/processserialnumber)
- [Apple AESendMode](https://developer.apple.com/documentation/applicationservices/aesendmode)
- [Electron 42.3.0 archive implementation](https://github.com/electron/electron/blob/e0127e99aeaa66bb580538fc7409831b5248cccb/shell/common/asar/archive.cc)
- [Electron 42.3.0 unpacked filesystem reads](https://github.com/electron/electron/blob/e0127e99aeaa66bb580538fc7409831b5248cccb/lib/node/asar-fs-wrapper.ts)

The actual typed native notification callback took 4.34218s until opener completion/terminal success: 4.25546s in static signature verification and 86.213ms in the opener. The user confirmed the specified test chat opened with delay. These boundaries exclude helper startup before callback and visible rendering after opener completion; the selected endpoint fast path remains unqualified.
