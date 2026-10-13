# Windows Notification Center policy checkpoint

The previous native TEST attempts retained one owned toast in history, but did
not find its action in Shell UI. Accepted Win+N input does not prove Center
opened. This checkpoint tests a separate configuration hypothesis without
another toast or a settings change.

Manual dispatch of `navigation-windows-native-e2e.yml` defaults to
`mode=policy_preflight`. It compiles the exact source and creates one fresh
owned TEST root. The native observer reads only
`Software\Policies\Microsoft\Windows\Explorer\DisableNotificationCenter`
from HKCU and HKLM with native64 query-only handles. Each observation is
`present` with a boolean, `not_present`, or `error` with a status. Wrong type,
size, denied access and DWORD values outside 0/1 remain errors. Both hives
must be inspected successfully; missing is not an error or an enabled value.

The controller also retains the existing client/session/input-desktop
preflight. It returns before sender, AUMID/COM registration, shortcut creation,
Show, UI input, Invoke or callback cleanup. No package or certificate operator
is used. Diagnostic success keeps both native callback and navigation
qualification false. Missing controller mode defaults to read-only; native
submission requires explicit workflow `mode=native_callback` and controller
`NAVIGATION_WINDOWS_PREFLIGHT_ONLY=0`. That mode conservatively stops before
submission if either hive explicitly configures the disable policy.

These are raw registry observations, not effective Shell policy or a verdict
on the earlier runs. [Microsoft documents the policy and reboot requirement](https://learn.microsoft.com/en-us/windows/client-management/mdm/policy-csp-admx-taskbar#disablenotificationcenter).
Absence/zero does not prove Center visible. A positive observation does not
authorize removing the policy, restarting Shell, rebooting the runner or
another Show. The next action must follow the retained observation.

Source typecheck and review are separate from Windows target compilation and
the actual no-Show observation. This checkpoint does not qualify a Windows
production adapter or selected-client chat navigation.

The first actual [read-only run](https://github.com/777genius/agent-notifications/actions/runs/37470806144),
source `c1a43819778b35b17e01145ab72627d2ebc7d824`, passed target compilation
and the two diagnostic steps. Independent raw review matched official artifact
11417422920 and ZIP SHA256
`08d83f1508b00f0e5f44cc7deb515162403f4642d4ad574e907a1097a28847e6`.
Both policy observations were `not_present`; the existing desktop preflight was
ready. Show was zero, submission false and both qualification flags false.
This eliminates a present disable DWORD for that snapshot, not effective policy
or Center visibility.

The next source checkpoint adds a read-only connection-state query to the
existing native preflight. WinSta0 and an input desktop do not establish an
actively connected session. Query only the helper's own actual session with
`WTSQuerySessionInformationW(WTSConnectState)`; preserve API status, buffer size,
raw enum and whether it is recognized. Free the WTS buffer on success or error.
Only a recognized `WTSActive` satisfies readiness. Errors, malformed data and
unknown enums remain unavailable; there is no fallback to a default session.
The Windows SDK supplies Wtsapi32; no dependency installation is added.

Remote-session, primary-screen dimensions and foreground-presence metrics are
auxiliary snapshots, not display/focus or Center proof. The preflight now embeds
its PID/nonce, so the controller can check exact second-child correlation. No
window names, usernames, keyboard input, settings, Show or callback are added.
Actual new-source WTS observations and target compilation remain pending.
See [WTS states](https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/ne-wtsapi32-wts_connectstate_class)
and [session query](https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/nf-wtsapi32-wtsquerysessioninformationw).
