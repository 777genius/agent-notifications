# Windows App SDK build prerequisite

This compile-only project prepares the unpackaged notification callback canary.
It verifies the restored C++ projections and links the explicit bootstrap ABI.
The workflow never runs the binary, deploys runtime packages, registers a callback,
or submits a notification. Green compilation does not qualify native navigation.

Dependencies are exact stable package versions, checked on 2026-10-07:
[Windows App SDK 2.5.1](https://www.nuget.org/packages/Microsoft.WindowsAppSDK/2.5.1)
and [CppWinRT 3.0.260818.1](https://www.nuget.org/packages/Microsoft.Windows.CppWinRT/3.0.260818.1).
The committed `packages.lock.json` comes from successful Windows build run
`37616367344` on source `5f9edea4e1d2587e32495bda1cbcab1955f19a1c`, runner image
`20260924.168.1`. The original CRLF artifact has SHA256
`c0e64fe63a6dd7d4daf56ba387cfbc65d27cbd057b6f02a0e276c89864818a2b`.
The repository normalizes JSON to LF; the committed lock has SHA256
`a934c725e24227fbce8ec55b5fb338d18b4dd2731661fe3c426af893f177525c`.
Restore uses locked mode, and the workflow exports the lock hash, exact source SHA,
runner image, and diagnostic logs. A changed dependency graph must fail restore;
update the reviewed lock before adding any native execution.

CI also verifies the already restored Runtime 2.5.1 nupkg using installed .NET
SDK 10+ `nuget verify --all`, matching its signature-neutral content hash to the
lock and checking unchanged raw SHA256. Its exclusive cache stays outside the
artifact directory. Missing tooling, timeout or incomplete proof fails closed;
there is no tool installation, second package download or native execution.
This qualifies CI-restored bytes only, not a separately downloaded local archive.

The raw XML activation nonce comes from `AppNotificationActivatedEventArgs.Argument()`.
`Arguments()` is a distinct map for builder arguments. Both types are checked
against the SDK-generated projection to catch that contract mismatch before E2E.

Next: qualify signed runtime package bytes/deployment, actual token elevation and
SDK `IsSupported`, then one genuine click after collected sender exit with a fresh
TEST identity. Preserve the classic `ERROR_NOT_FOUND`/Show-not-called result;
this project neither retries that test nor establishes its cause.
