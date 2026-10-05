# Linux selected-client qualification

Status: launcher source inspected; native client qualification pending.

## Verified package checkpoint

The official `chatgpt` 26.930.51102 amd64 archive was downloaded into a new owned
TEST directory. Its full size and SHA256 matched the previously inspected signed
package contract before archive inspection. This fresh inspection reuses the
accepted package hash; it does not claim a fresh signature verification of mutable
repository metadata. The selected launcher, desktop entry and control scripts
were read without installing the package or executing its contents.

`usr/bin/chatgpt` links to `../lib/chatgpt/codex-launcher`. The 63-byte launcher
resolves its own selected installation and executes adjacent `ChatGPT` with
`"$@"`. It preserves argument boundaries and the inherited environment. It does
not select a private profile, consume an activation token or establish how the
binary handles an already running instance. The desktop entry uses
`Exec=chatgpt %U`, declares the `codex` scheme and enables startup notification.

The installation scripts configure repository/keyring files, update the desktop
database and conditionally load AppArmor. Their source contains no application
launch. They must only execute inside the disposable TEST image during a future
installation, never on the worker host.

Public facts and selected source hashes are retained in
`docs/evidence/navigation/2026-10-06/linux-selected-client-launcher.json`.
The raw evidence hash binds this projection to the retained private inspection.

## Next native boundary

Use the exact package in a disposable Ubuntu 24.04 TEST image. Run nonroot with
private HOME/XDG directories, bus, compositor and profile, no host credentials,
projects, agent sockets, display, GPU or input devices. Preserve the application's
sandbox and retain installation/executable/resource hashes.

After one real notification click, cold-launch the absolute verified launcher
with the documented `--ozone-platform=wayland` flag and the approved synthetic
thread URI as separate arguments. The D-Bus activation specification defines
`activation-token` as the value passed via `XDG_ACTIVATION_TOKEN` for a new child.
This is a candidate input contract; actual consumption by this distribution is
still unproven. Do not substitute a manually generated or submission-time token.

Observe independently: selected executable incarnation, client surface identity,
token equality in any actual activation request, compositor focus and visible
target UI. Existing callback token forwarding does not qualify the real client.
An isolated sign-in screen or nonexistent thread cannot prove exact-chat routing;
that requires an existing isolated TEST chat and its visible confirmation.

No client installation, launch, token consumption, focus or chat navigation is
claimed by this checkpoint. Production Linux navigation remains unavailable.

## Observed sandbox prerequisite

The reviewed C namespace probe compiled with `-std=c11 -Wall -Wextra -Werror`
and executed once in a fresh owned container using the previously qualified
immutable Wayland TEST image. UID 1000, `no-new-privileges`, default seccomp,
capabilities dropped, network none and a single owned evidence mount were kept.
The actual `clone(CLONE_NEWUSER | SIGCHLD)` returned `EPERM`; no child was created.
The container was removed by its verified exact ID, followed by fresh ID/name
absence checks. No notification or client launch was attempted.

This disproves that namespace prerequisite under the measured container boundary.
It does not qualify the complete Chromium sandbox, attribute the denial solely
to seccomp, or prove Linux clients universally unsupported. Both bounded reap
loops and the lost-child ownership guard were corrected in three source-review
rounds before execution; the negative raw evidence received independent review.

Facts and source/binary/raw hashes are retained in
`docs/evidence/navigation/2026-10-06/linux-selected-client-namespace-prerequisite.json`.
A separate disposable guest kernel is the next environment candidate. Its KVM
access, boot, isolation and client sandbox remain unqualified. No sandbox-disable
flag or unconfined host policy is introduced to bypass this prerequisite.

## Sources

- [Official Linux support and experimental Wayland flag](https://learn.chatgpt.com/docs/linux/linux-app)
- [D-Bus application activation](https://specifications.freedesktop.org/desktop-entry/latest/dbus.html)
- [Existing chat links](https://learn.chatgpt.com/docs/reference/commands)
