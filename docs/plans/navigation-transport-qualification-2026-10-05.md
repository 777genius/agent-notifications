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

For a candidate transport, require a documented endpoint identity mechanism, bounded handoff, supported Codex distribution, resource-validation guarantees, TCC behavior, and synthetic exit/relaunch/reused-PID/two-copy tests. Then test the actual signed client in an isolated OS account or VM with synthetic data. Current user ChatGPT/Codex activation is prohibited for these tests.

Windows and Linux remain unqualified; Linux delivery or cross-compilation alone does not prove durable native callback or selected-client routing. There is no native installed-client E2E result in this document.

## Primary sources

- [Apple typeKernelProcessID and typeMachPort](https://developer.apple.com/documentation/applicationservices/apple_event_manager/1542936-typekernelprocessid)
- [Apple guest attribute keys](https://developer.apple.com/documentation/security/guest-attribute-dictionary-keys)
- [Apple kSecGuestAttributeMachPort: not implemented](https://developer.apple.com/documentation/security/ksecguestattributemachport?language=objc)
- [Apple SecCodeCopyGuestWithAttributes](https://developer.apple.com/documentation/security/seccodecopyguestwithattributes(_:_:_:_:))
- [Apple NSWorkspace open](https://developer.apple.com/documentation/appkit/nsworkspace/open(_:withapplicationat:configuration:completionhandler:))
