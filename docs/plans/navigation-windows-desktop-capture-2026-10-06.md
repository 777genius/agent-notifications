# Disposable Windows CI desktop capture

Source-only TEST diagnostic; Windows SDK compilation and capture execution are
pending. It investigates whether the fresh runner desktop visibly contains a
first-logon/OOBE blocker. It neither dismisses UI nor qualifies Notification Center,
toast callback, selected client, authenticated chat or navigation.

The existing workflow's explicit manual `desktop_capture` mode sets a separate
opt-in. PRs stay compile/typecheck only; the default remains read-only preflight.
The controller and native helper require the owned UUID root/binary, repository,
manual event, run attempt1 and Windows11 native ARM64 prerequisites. A rerun of the
same job is refused; a later experiment needs an explicitly new dispatch. No retry
or fallback follows an unknown result.

One public GDI BitBlt reads the primary monitor, including layered windows; WIC
encodes PNG in memory. A restored thread-local per-monitor-v2 DPI context, primary
GetMonitorInfo rectangle and checked MM_TEXT DC mapping establish physical-pixel
coordinates. Monitor/rectangle/context are rechecked afterward; origin and contract
are recorded. This does not change display settings. Dimensions must each be <=2048, and encoded bytes <=8MiB
before atomic exclusive file publication. Bounded bitmap/codec buffers may exceed
the encoded artifact limit. A five-second cooperative budget and a 15-second
outer child limit bound the attempt; individual GDI/WIC calls are not cancellable.
Timeout/partial receipts remain failed observations.

WTS/input-desktop and held live same-user/session Windows Shell guards bracket the
snapshot. A separate <=128-visit, <=32-row metadata sample allows fixed Windows
OOBE/Shell executable leaves and exports HWND/PID/class/image leaf/visibility only,
never window titles or UIA text. Metadata is bounded, non-atomic and not absence
proof; pixels omit other displays and may contain occluded, black or stale areas.

Native source, binary, nonce/PID and final PNG SHA256/dimensions are correlated by
the controller. The upload is a fixed small member list, without the executable,
arbitrary directories or temporary PNG. Capture child environment excludes auth
and GitHub command-file variables. No Show/input/client launch/install/registry
mutation/sign-in is performed. No current user desktop is captured locally.

Public SDK references: [GDI capture](https://learn.microsoft.com/en-us/windows/win32/gdi/capturing-an-image),
[BitBlt](https://learn.microsoft.com/en-us/windows/win32/api/wingdi/nf-wingdi-bitblt),
[WIC encoding](https://learn.microsoft.com/en-us/windows/win32/wic/-wic-creating-encoder).

[DPI thread context](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setthreaddpiawarenesscontext)
and [monitor rectangle](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getmonitorinfow).


## Actual first-logon diagnostic and next bounded census

Run 37522160148 on source ce26fb7bf97d5c92c5cc42c132781a789b808f95 passed actual SDK capture. Independent audit accepted141 assertions. Official artifact11441305747 ZIP SHA2568d0c2a13326ac99cea12a26e4e4918f59fe9f6c6d0eac3ed8730dc076766b627 contains nine inert members. The1024x768 PNG visibly shows Choose privacy settings for your device, with a Next button. Capture took94ms; three helpers exited and were collected. No Show/input/install/launch occurred. This proves first-logon OOBE obstruction, not causation of every earlier failure or any Center/callback/navigation qualification.

The new manual-only `oobe_preflight` obtains bounded properties from the actual foreground UIA subtree, with held kernel process identity and same-user/session guards. It invokes no UI action. The purpose is to identify exact OOBE selectors before a separately reviewed finite setup flow. Partial/error snapshots do not prove absence or action readiness; Windows-directory admission is not signer qualification. Native census execution remains pending.
