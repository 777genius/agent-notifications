# Windows App SDK build prerequisite

This compile-only project prepares the unpackaged notification callback canary.
It verifies the restored C++ projections and links the explicit bootstrap ABI.
The workflow never runs the binary, deploys runtime packages, registers a callback,
or submits a notification. Green compilation does not qualify native navigation.

Dependencies are exact stable package versions, checked on 2026-10-07:
[Windows App SDK 2.5.1](https://www.nuget.org/packages/Microsoft.WindowsAppSDK/2.5.1)
and [CppWinRT 3.0.260818.1](https://www.nuget.org/packages/Microsoft.Windows.CppWinRT/3.0.260818.1).
The first successful restore exports `packages.lock.json`, its SHA256, the exact
source SHA, runner image, and build log. Review and commit that resolved lock
before adding any native execution; this initial graph-discovery job is not a
locked native canary. Subsequent native builds must use locked restore.

The raw XML activation nonce comes from `AppNotificationActivatedEventArgs.Argument()`.
`Arguments()` is a distinct map for builder arguments. Both types are checked
against the SDK-generated projection to catch that contract mismatch before E2E.

Next: qualify signed runtime package bytes/deployment, actual token elevation and
SDK `IsSupported`, then one genuine click after collected sender exit with a fresh
TEST identity. Preserve the classic `ERROR_NOT_FOUND`/Show-not-called result;
this project neither retries that test nor establishes its cause.
