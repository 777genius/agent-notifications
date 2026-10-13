# Windows TEST taskbar UIA discriminator

Scope: a read-only disposable Windows client CI projection. The preceding visible-window run completed its nine-row projection, but its unchanged before/after snapshots did not identify an accessible Notification Center action or prove that Win+N opened the Center. This discriminator inspects taskbar identifiers before considering any further input or toast submission.

`taskbar_uia` is an explicit workflow-dispatch mode, exclusive with `center_surface` and `native_callback`. Default preflight remains read-only. The new mode returns before registration, Show, Invoke and cleanup; it performs no keyboard or mouse input. Its outer child timeout is 15 seconds. A timeout or missing report cannot establish successful traversal.

The native probe requires one primary `Shell_TrayWnd`, a held live Windows Shell process in the same SID/session, and fresh Active/input-desktop preflight. It brackets HWND/class/PID observations and requires UIA root PID/native HWND to match that root. Provider transitions require independently held matching SID/session and Windows Shell image ownership; an unverified provider subtree is skipped and recorded. These diagnostic owner checks are not production signature or Center-semantic qualification.

Limits: 128 cumulative visited/pending nodes, depth 16, 16 retained owners, two-second cooperative UIA deadline, 128 UTF-16 units per identifier, and 10,000 serialized UTF-8 bytes for rows including commas. Every UIA call checks the cooperative deadline; the outer timeout bounds a blocking provider call. Failed properties, provider skips, errors, truncation, expired deadline and incomplete root observations are retained separately. Completion applies only to the accepted-provider projection.

Only AutomationId, ClassName, control type, offscreen state, provider PID, tree indices/depth and property HRESULTs are exported. Name, Value, window text and Invoke patterns are never queried by this mode. The controller checks PID/nonce correlation, export bounds, tree shape, fields and completeness prerequisites.

Relevant public API contracts: [FindWindowExW](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-findwindowexw), [UIA ElementFromHandle](https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-obtainingelements), and [CurrentNativeWindowHandle](https://learn.microsoft.com/en-us/previous-versions/dd319225(v=vs.85)).

Native evidence is pending. `centerActionQualified`, `nativeCallbackQualified` and `navigationQualified` remain false. An observed identifier alone never authorizes a guessed system Invoke, and an incomplete/negative projection is not an absence proof. Full P5 acceptance still requires the real installed notification callback after sender death and selected supported client handoff.
