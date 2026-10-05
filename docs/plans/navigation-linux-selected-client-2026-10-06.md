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
access is observed in the subsequent paused test below; guest boot, guest
isolation and client sandbox remain unqualified. No sandbox-disable flag or
unconfined host policy is introduced to bypass this prerequisite.

The exact archive also ships `etc/apparmor.d/chatgpt`, SHA256
`05be1a8336a80236f4b56798ea7a75accb55f51438afa1e7cb5e94bfa326b1b7`.
Its named profile attaches to `/usr/lib/chatgpt/ChatGPT`, uses vendor-provided
`flags=(unconfined)` and grants `userns`. The generic namespace probe does not
execute that path and cannot predict the client under this profile. Installing
and loading this shipped policy inside the disposable guest is distinct from
weakening Docker or the worker host's policy.

Guest qualification must first retain the active profile and absence of
unexpected `local/chatgpt` overrides. Cold-launch the selected client without a
thread URI, using a private fresh profile; retain its executable incarnation
and `/proc/PID/attr/current`. Observe actual renderer descendants, their ancestry,
namespace IDs, UID mappings, seccomp state and sandbox errors against the guest
and browser-process baseline. A generic `clone` result, a visible window or the
absence of `--no-sandbox` cannot alone qualify Chromium's sandbox. The subsequent
notification experiment uses another fresh profile after this preflight is
stopped and its owned children collected.

## Paused KVM and authenticated guest image

A TEST-only tooling image installs the fresh official Noble package candidates,
records policy/version inventories and runs QEMU as UID 1000. The one native
probe used default Docker security, supplementary group 994 and only `/dev/kvm`
device access, with no host package/group changes. Its private QMP peer matched
the owned QEMU PID. `query-kvm` returned strict `enabled=true, present=true`;
`query-status` returned `prelaunch, running=false`. QEMU quit with collected exit
0; exact container removal and fresh ID/name absence checks passed. No guest
boot, client or notification occurred.

This runtime evidence is bound to source SHA `30033b6d...be391`, not the later
catalog-only fix. The old inner catalog has 17 entries: 16 match final bytes;
the mutable outer controller record contains a historical intermediate hash.
It is not an entirely qualified final artifact catalog. The reviewed source fix
hashes only the probe's closed outputs and explicit immutable inputs; that fixed
source has not been executed natively. The full hashes and limits are retained
in `linux-selected-client-paused-kvm.json` beside the other evidence.

The dated official Ubuntu Noble cloud image was downloaded once into a new TEST
root and verified against its actual detached checksum signature and the
documented full signing fingerprint. The exact filename entry and whole-image
SHA256 matched before inspection. Metadata hashes and the raw evidence binding
are in `linux-selected-client-ubuntu-image-auth.json`. Download/signature success
does not qualify boot or client behavior.

The next bounded phase uses a read-only authenticated base plus a disposable
overlay and seed ISO. Provisioning installs dependencies inside the guest and
shuts down without launching the client. A separate native boot disables both
outer networking and QEMU NICs; the guest receives no host shared directories,
credentials, projects, displays or input devices. Guest sandbox and actual
selected-client observations remain required before the notification experiment.

## Sources

- [Official Linux support and experimental Wayland flag](https://learn.chatgpt.com/docs/linux/linux-app)
- [D-Bus application activation](https://specifications.freedesktop.org/desktop-entry/latest/dbus.html)
- [Existing chat links](https://learn.chatgpt.com/docs/reference/commands)
- [Official Ubuntu image verification](https://ubuntu.com/docs/public-images/public-images-how-to/verify-image-checksum/)
