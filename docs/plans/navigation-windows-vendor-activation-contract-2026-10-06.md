# Windows selected-vendor SDK TEST slice

Status: source-only. C++ SDK compile and native execution are pending. No package
installation, URI activation, toast callback or client navigation is qualified.
The accepted metadata observations identify OpenAI.Codex ARM64 version
26.930.7945.0, publisher CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B and family
OpenAI.Codex_2p2nqsd0c76g0. The earlier final PowerShell projection timed out;
those bounded SDK/signature observations do not turn its whole run green.

Separate modes require NAVIGATION_WINDOWS_VENDOR_NATIVE_TEST=1, CI/GITHUB_ACTIONS,
the existing fresh UUID root marker and exact probe executable. No arbitrary
package, family or URI argument is accepted. The future controller must verify
signature with signtool /pa /all and full archive/probe SHA256 before creating
`vendor-custody.proof` with these exact newline-terminated lines:

    TEST vendor custody <root UUID>
    signtool-pa-all-success
    <client.msix lowercase SHA256>
    <navigation-native-probe.exe lowercase SHA256>

This is controller-owned custody, not a cryptographic signature on a sidecar or
same-user-adversarial protection. The SDK helper independently hashes both files,
holds deny-write/delete handles and checks the archive's actual hard-pinned SDK
name, publisher, family, fullName and absence of PackageDependency before effects. Custody alone is not a vendor
signature verifier. The current-user absence proof additionally binds token SID;
it cannot authorize installing/updating a preexisting selected family.

`vendor-state-before/after` only query current-user exact-family state.
`vendor-install` writes exclusive intent before one AddPackageAsync, no dependency
URLs, DeploymentOptions.None. Terminal success is separately persisted.
`vendor-query-settings/thread` query the exact family using QueryUriSupportAsync.
`vendor-launch-settings/thread` require Available and permit one launch total,
TargetApplicationPackageFamilyName set, FallbackUri null. Thread URI contains the
fresh TEST UUID; settings URI is fixed. No Store/default/browser fallback or retry.
`vendor-remove` requires prior absence, same custody/install intent, known terminal
Add success and exactly one matching installed fullName, then removes only that
current-user package and rechecks exact-family absence. No explicit dependency-removal
calls are made. Windows may remove unused dependencies/last-user package payload
and shut down associated apps; these are SDK-managed consequences, not excluded
by the fixture. The pinned no-PackageDependency manifest boundary is mandatory.

Async success requires Completed and primary ErrorCode S_OK before GetResults;
deployment ExtendedErrorCode must also succeed before a completion proof.
Async operations use a cooperative 150s overall budget, cancellation and fresh
pre-effect checks. Future controller needs a strict external deadline and must
collect each process before advancing. Timeout/cancellation after effect entry
remains unknown; it does not prove OS quiescence/no-effect. Unknown Add has no
automatic Remove path here. Retain reports and classify cleanup as unknown until a
separately reviewed reconciliation can establish safe ownership/quiescence.

Result is package-selected handoff acceptance only, not process/window binding,
authenticated chat, target confirmation or the still-required genuine toast cold
callback. Existing toast/default modes are not wired to this lane.

Public API contracts: [FindPackagesForUser](https://learn.microsoft.com/en-us/uwp/api/windows.management.deployment.packagemanager.findpackagesforuser?view=winrt-26100),
[AddPackageAsync](https://learn.microsoft.com/en-us/uwp/api/windows.management.deployment.packagemanager.addpackageasync?view=winrt-26100),
[RemovePackageAsync](https://learn.microsoft.com/en-us/uwp/api/windows.management.deployment.packagemanager.removepackageasync?view=winrt-26100),
[QueryUriSupportAsync](https://learn.microsoft.com/en-us/uwp/api/windows.system.launcher.queryurisupportasync?view=winrt-26100).
