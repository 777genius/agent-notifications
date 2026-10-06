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

## Provisioning fixture and first actual failure

The bounded provisioning fixture received three source-review/fix rounds. The
reviews closed a premature success frame, incomplete QMP tail acceptance,
handshake timeout accounting, final serial-size limits and mutable input binding.
Success requires the guest's completed installation and sync, one hash-checked
serial result, independently observed QMP `SHUTDOWN` with `guest=true` and reason
`guest-shutdown`, collected QEMU exit 0 and unchanged input hashes. The default
distribution portal backend is only a provisioning dependency; the previously
qualified GTK 1.15.3 backend is still required for the subsequent token experiment.

The first actual container failed during Python imports before probe `main`:
the staged `operator.py` shadowed the standard-library module. No VM, client or
notification started. Exact owned-container removal and fresh ID/name absence
checks passed. The original failure is retained independently in
`linux-selected-client-guest-import-failed.json`; it is not relabeled as a fixed
run. The reviewed correction uses isolated Python `-I` for the outer controller,
container probe and guest script, and names the staged outer script
`fixture-controller.py`. A fresh captured stage has a distinct exclusive intent
gate; a successful guest boot still does not qualify sandbox or navigation.

The distinct fixed attempt passed provisioning. Seven guest commands completed
with exit 0; the exact selected package was installed, the vendor profile was
loaded and sync completed. The owned QMP peer reported actual KVM and one
guest-initiated shutdown with reason `guest-shutdown`; QEMU was collected with
exit 0. Base, package, seed and both captured sources remained unchanged. Exact
owned-container removal and fresh ID/name absence checks passed. Its private
overlay was frozen read-only after cleanup for a subsequent separate TEST phase.

`linux-selected-client-guest-provisioned.json` records this provisioning scope and
the retained source/raw hashes. Original guest command logs remain in the guest
overlay; their completion-record hashes have not been independently recomputed
from transferred bytes. The overlay hash is a retained host measurement, not a
local image rehash. No client, notification, renderer sandbox, activation token,
focus or chat route was exercised. The next native boot must have both outer
networking and the QEMU NIC disabled, and must not rerun provisioning.

## Sources

- [Official Linux support and experimental Wayland flag](https://learn.chatgpt.com/docs/linux/linux-app)
- [D-Bus application activation](https://specifications.freedesktop.org/desktop-entry/latest/dbus.html)
- [Existing chat links](https://learn.chatgpt.com/docs/reference/commands)
- [Official Ubuntu image verification](https://ubuntu.com/docs/public-images/public-images-how-to/verify-image-checksum/)


## Read-only executable binding and guest preflight source

A subsequent read-only `dpkg-deb --fsys-tarfile` / `tar -xOf` inspection measured
the original executable member in the retained official archive: 319425760 bytes,
SHA256 `207c4fbff7e2fcc1b0789448351ac6eed206206d94c5a0835e5f07c7cd73d6e3`.
Both archive-reader commands exited 0; the value matches the actual provisioned
executable measurement. This is a member-byte comparison, not a fresh whole
archive or repository signature verification. No package code was executed.
The public facts are retained in `linux-selected-client-executable-member.json`.

The new guest-only preflight received three independent source review/fix rounds.
Final source SHA256 `e3003f0746b59add941a0e0930ea57acea6879370f68befbaa8701aae4186a31`.
It creates a new private profile and one no-URI vendor-launcher process as UID
1000. Root remains only the isolated guest supervisor. Exact executable/profile
checks, private Sway/D-Bus socket peer identities, retained pidfds, two consistent
renderer kernel snapshots and final main-process liveness gate the observation.
A fresh root-owned guest cgroup is joined before dropping privileges; exact
cgroup kill, populated=0, pidfd exit and direct-child collection bound teardown.
The [kernel cgroup contract](https://docs.kernel.org/admin-guide/cgroup-v2.html)
defines inheritance and recursive populated state; no host cgroups are changed.

Review fixes closed missing peer/liveness checks, fork-cleanup races, setup errors
outside finalization and errors skipping remaining cleanup or guest poweroff.
The accepted source qualifies only observed renderer restrictions if actually
run successfully. Full client sandbox, rendered window, focus and navigation
fields remain false; `/proc` indicators do not prove every renderer's content
isolation. Python syntax passes. No native client run has occurred.

The next outer controller must use a distinct offline seed and fresh child COW
overlay of the frozen provisioned image, preserving its backing chain. It must
bind source/seed/image hashes and its exclusive one-attempt intent, disable both
outer networking and the QEMU NIC, verify the guest frame, independently observe
QMP guest shutdown and collect VM exit, and retain exact-container cleanup.
A serial completion frame alone cannot qualify VM shutdown.


## Offline VM controller checkpoint

The separate native probe and host TEST operator received three source review/fix
rounds before execution. Probe SHA256
`95502e4482dd5bf226e9f838d60387baea5bc90aa1a00406b24b74e574432d20`;
operator SHA256 `3301aa78e5778a29534c8f16ffdaf783df2994090089527fc6dd29007340fc5b`.
The original accepted provisioning fixture remains unchanged. New code requires
the accepted guest-source hash, frozen provisioned-image/base hashes, exact
three-file backing chain and a fresh NoCloud seed. No APT or package install is
performed. Both Docker networking and QEMU NICs are disabled.

Review fixes added fixed guest-source enforcement, independent error-aware
teardown/integrity checks, source-bound partial timeout logs, operator integrity
and pre-start readback of actual Docker isolation. The outer operator uses one
exclusive intent, validates resources and the owned image, and removes only its
exact container by verified name/ID/owner/image followed by fresh absence checks.
Read-only hardlinks preserve storage but are not an adversarial immutability
boundary against the owner UID; before/after hash gates qualify this controlled
TEST fixture only. Source reviews and Python syntax checks do not prove native
boot or client behavior. A distinct captured one-shot native stage follows.


## Actual offline native attempt: failed

One captured attempt used the reviewed probe `95502e...` and guest `e3003f...`.
KVM was enabled; the guest powered off itself and QEMU was collected with exit 0.
The operator reports exact-container removal and unchanged controlled inputs.
These facts do not qualify application behavior. The original probe failed its
line-start frame parser because getty placed its prompt before the completion
frame on the same line. A read-only replay recovered one complete SHA-matching
frame, without booting the guest again or changing the original failed record.

The recovered guest result also fails: `actual_renderer_evidence_absent`. Client
launch was attempted, but renderer restrictions, full client sandbox, rendered
window, focus and navigation remain unqualified. Guest cleanup reports the owned
cgroup empty and kernel exit for all 11 tracked processes. The independent raw
review accepted publication as negative evidence, with operator-reported input
and container facts distinguished from locally replayed serial/QMP facts.

The public projection is `linux-selected-client-offline-negative.json`. The parser
fix retains the negative guest payload before semantic validation, preserves
unique-frame/size/base64/hash gates, and passes three wire regression tests plus
independent source review. No second native attempt has been made.
