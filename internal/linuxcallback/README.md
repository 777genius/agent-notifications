# Linux desktop thread callback v1

This is an opt-in selected-installation URI handoff. `chat_id` means the exact
opaque `codex://threads/<one segment>` request reached the selected supported
launcher; it does not confirm a visible or authenticated chat, account, profile,
rendering, token consumption or a full client sandbox.

`setup-linux-callback --installation-root /usr/lib/chatgpt` requires an existing
managed installation. It publishes a random installation-specific reader,
immutable snapshot, desktop entry and D-Bus service in the existing transaction.
It preserves the explicit local-routing policy. Other paths/releases are
unavailable. No app, bus, permission dialog or test runtime is launched by setup.
The generated vendor manifest must be authenticated from the pinned official
package before this opt-in can become eligible. The initial empty manifest is
intentionally unavailable.

The supported stack is initially limited to the independently frozen Portal
1.22.1 and GTK 1.15.3 executable builds qualified by the selected-client native
capsule. Actual unique owners, bus GUID, same-user PID/birth, held pidfd and
root-owned executable bytes are checked. An interface/package version, D-Bus
name or arbitrary operator digest cannot opt in another build. These runtimes
are not automatically installed. A missing explicit Unix session address,
missing Registry, changed owners or missing click-time token fails closed.

The sender registers its installed application ID and writes one random action
record before its sole AddNotification. This record binds snapshot, selected
thread, bus GUID and GTK/frontend/notification owner generations. The portal ID
is the opaque key; AddNotification returns no frontend/server Notify ID. Neither
TEST eavesdrop observations nor a direct ActivateAction is shipping authority.

The D-Bus service exports org.freedesktop.Application.ActivateAction. godbus
injects actual HeaderFieldSender as dbus.Sender, outside the wire signature.
Cold callbacks read only their retained immutable snapshot and record. They
require the pinned GTK caller, matching fresh owners, installed reader/service/
desktop registration integrity, complete selected vendor resources and a fresh
three-second boot-continuous click budget. Each invocation has one possible
launcher call; an unknown effect has no retry, URI-handler or other-installation
fallback. No RemoveNotification or CloseNotification is issued.

The selected absolute vendor launcher receives one encoded URI argv and the
native activation token through the supported environment. Only a bounded
session environment is retained; loader/Electron injection variables are not
inherited. Filesystem verification and process rechecks are conservative and
non-atomic. They do not freeze third-party updates or prove renderer identity.

Setup B does not revoke admitted A actions. Global enable/click-to-focus changes
affect new submissions; retained admitted actions remain governed by their
existing snapshot while the callback installation remains intact. Missing,
removed or tampered immutable callback assets fail closed. No immediate
cancellation of already-admitted actions after disable/uninstall is promised.
Readers and records are never retired without a proven native notification
lifetime. This permits compatible reader rollback and sender death.

The manifest consumer accepts canonical JSON with version/packageSHA256/root/
launcher/executable/entries. Paths are absolute package-reference paths, with
root /usr/lib/chatgpt, launcher /usr/lib/chatgpt/codex-launcher, executable
/usr/lib/chatgpt/ChatGPT and the exact /usr/bin/chatgpt symlink. Entries include
the root directory and all tree directories, regular files and exact symlink
targets. Limits: 20,000 entries, 512 MiB per regular file, 4 GiB total and 8 MiB
manifest. Every expected resource is verified, and undeclared children refuse.
The full embedded manifest digest is bound into each snapshot.

Focused commands for an isolated Linux TEST runner:

```sh
go test -race ./internal/linuxcallback ./internal/agentnotify/origin ./internal/agentnotify ./internal/agentnotify/runtime ./internal/installruntime ./internal/notifier
go test ./cmd/claude-notifications
```

Tests cover actual wire-sender injection on a new private TEST bus, forged
caller/key rejection, owner restart before/during verification, fresh late-click
budget, unknown opener effect without retry, opaque URI encoding, retained A
reader after B policy, None without target probing, and canceled hung bus
handshake with observed peer closure. Source editing only was performed on the
owner's Mac. Native qualification of this shipping CLI/service wiring still
requires its own isolated TEST Linux execution; prototype native evidence alone
does not establish the new code's installed callback behavior.
