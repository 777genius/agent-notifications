# Cross-platform navigation qualification, 2026-10-05

Status: native Windows/Linux navigation remains unqualified; banner-only capability remains honest. These lanes are part of the full plan, not completed by macOS tests or cross-compilation.

## Current distribution evidence

Official documentation now lists the ChatGPT desktop app with Codex on macOS, Windows and Linux. Linux is a preview for Ubuntu 24.04/26.04, Debian 13, Fedora 43/44 and updated Arch, x64/arm64. Native Wayland is experimental; XWayland is used when available and native focus/window positioning have limitations. Product availability does not establish a supported thread URI, process binding or notification callback contract.

Sources inspected:

- [Official desktop app](https://learn.chatgpt.com/docs/app)
- [Official Linux distribution and Wayland support](https://learn.chatgpt.com/docs/linux/linux-app)
- [Official Windows deployment](https://learn.chatgpt.com/docs/enterprise/windows-deployment)

The Windows deployment page gives a Store product ID, not a verified installed package family name. Do not hard-code the Store product ID as package identity. Qualification must inspect the signed installed package and its manifest on the test machine.

## Windows candidate and current gap

Microsoft documents `LauncherOptions.TargetApplicationPackageFamilyName` for selecting a package during URI launch. This is package selection, not authenticated running-process binding or confirmation that the specified chat rendered. The installed client must declare the protocol and match the trusted package identity.

A desktop notifier cannot assume it is still running at toast click time. Microsoft's COM activation model can launch its registered local callback server. Current `delivery_toast_windows.go` performs PowerShell `Show`, and `windows_toast_xml.go` emits a banner without a durable callback activator. Adding an in-memory Activated listener or ShellExecute alone would not satisfy sender-death acceptance. No new navigation capability is enabled.

- [Microsoft URI launcher](https://learn.microsoft.com/en-us/uwp/api/windows.system.launcher.launchuriasync)
- [Microsoft COM toast activation](https://learn.microsoft.com/en-us/previous-versions/windows/desktop/win32_tile_badge_notif/respond-to-toast-activations)

Required native evidence: unique installed TEST notifier identity, sender exited before click, callback relaunch, immutable action decode with fresh budget, explicit selected package handoff, wrong/default handler exclusion, unknown-effect no retry and old-action/update compatibility. The user confirmed Windows is available only in CI/CD. No separate interactive Windows test desktop is available; CI compile/unit tests do not supply this evidence.

## Linux candidate and current gap

Freedesktop notification actions are delivered as `ActionInvoked` signals to a live D-Bus client. The specification does not promise process activation after sender death. Optional `ActivationToken` arrives for the new click and can be absent; retaining a token from submission is incorrect. Notification persistence alone does not imply callback persistence.

Current `delivery_linux.go` submits Notify without navigation actions, then closes the session connection. It therefore correctly reports navigation unavailable. A durable owner/activation mechanism must be qualified before adding actions: a dead sender's bus unique name or numeric notification ID is not a durable target. Owner changes on daemon restart must invalidate old notification-ID mappings. GNOME/KDE, portal and XDG application activation have different contracts and must not be treated as interchangeable by an untested universal wrapper.

- [Freedesktop notification protocol, actions and activation tokens](https://specifications.freedesktop.org/notification/latest/protocol.html)

The freshly inspected hosted machine is Ubuntu 24.04.5 LTS, with Xvfb and dbus-run-session present, but no discovered weston or dunst executable. This establishes potential infrastructure for isolated transport tests, not a supported interactive desktop, live Wayland activation or real client qualification. Hosted producer admission separately remains blocked by two stopped jobs sharing a workspace; no registry was manually edited.

Next qualification must use a new disposable test desktop/session with synthetic data, selected installed client distribution and a real callback/lifetime observation. Until then the Windows/Linux implementation lanes remain pending under the plan's qualification gate.
