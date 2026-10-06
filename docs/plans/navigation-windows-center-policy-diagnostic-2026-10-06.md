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
