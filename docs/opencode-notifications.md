# OpenCode notifications (Linux amd64)

OpenCode notifications are a separate, explicit product integration. They do not
inherit portable MCP consent or change its global `enabled` policy. The supported
host is Linux amd64; no OpenCode process is launched by setup.

Use `claude-notifications setup-opencode install` with an absolute, trusted,
current Linux amd64 `--binary` source. Specify `--runtime-root` for a new
installation; an existing managed installation uses its recorded runtime root.
Select `--desktop`, `--webhook`, or both.
Setup installs one managed Linux amd64 executable, one self-contained global
OpenCode plugin file, and its ownership/consent records. It does not install
OpenCode itself. Start or restart OpenCode after `install` or `update` so it
loads the current plugin bytes; restart after `remove` to unload old code.
For example, in a disposable test environment:

```sh
claude-notifications setup-opencode install \
  --binary /absolute/path/claude-notifications-linux-amd64 \
  --runtime-root /absolute/path/managed/runtime \
  --desktop
```

`update` uses the same flags and an explicit channel selection. `remove` needs
the original OpenCode config environment and managed runtime root; it revokes
OpenCode channel consent before removing the plugin and registration. If the
shared control root contains other consumers, their runtime stays installed.
`recover` replays only the installruntime journal after an interrupted setup.

Placement is global: `OPENCODE_CONFIG_DIR/plugins/agent-notifications.js` when
set, else `XDG_CONFIG_HOME/opencode/plugins/...`, else
`~/.config/opencode/plugins/...`. The CLI also accepts `--opencode-config-dir`,
`--xdg-config-home`, and `--home` for explicit placement. A foreign, symlinked,
or edited plugin file is preserved and causes setup to stop. A loaded plugin
from a removed registration cannot pass the current event gate.

The desktop and webhook flags authorize only those channels. Product settings
may further disable them. The event path rechecks policy, consumer registration,
owned plugin and executable identities on each request and has no recovery or
write behavior.
An already admitted delivery can finish after `remove`; this MVP does not
promise a strict in-flight revocation barrier.

If the OpenCode desktop app also shows system notifications, its
**Settings > General > Notifications** switches for **Agent**,
**Permissions**, and **Errors** can be turned off to avoid overlap with the
corresponding Agent Notifications channels. Other notification plugins in the
OpenCode profile may also produce duplicates. The isolated qualification below
loaded only this plugin; it does not prove duplicate-free behavior in a
profile with other notification sources.

## Linux amd64 qualification

On 2026-09-29, a fresh disposable Linux amd64 project and isolated OpenCode
profile loaded the bundle from Notifications commit
`2da9d192b1863f9d2220e760ad3fc47177ae15d2`.
The test used OpenCode 1.18.33 and a locally built Linux amd64 binary
(`sha256:5a83226dedb208c80c2597b5fa48de745bbbf6ddb5f7afdf4b7d95f7c111ee54`).
The bundle source hash was
`sha256:0148e31bbd1b9bca49c674689a5f621a26d72142974b9cc663b8a96f72461b81`.
The operator retained test data and logs in the disposable sandbox. No agent
command ran in a product checkout.

- A loopback webhook received `task_complete`, `question`,
  `permission_request`, and `opencode_error` from the loaded OpenCode plugin.
  The question came from a native `question.asked` triggered through the
  OpenCode SDK with the question tool enabled. The error came from a test
  request for a nonexistent model. Captured payloads contained none of the
  test prompt markers.
- With a private D-Bus session and test implementation of
  `org.freedesktop.Notifications`, the loaded plugin made one `Notify` call
  for a completed turn and one for a terminal error. Each call used generic
  product copy; no duplicate call was observed in those runs.
- `install`, `update` between desktop and webhook consent, and `remove`
  completed. While the OpenCode server still held the old plugin in memory,
  a new error after `remove` produced no webhook request.

One-shot `opencode run` can exit after printing an answer before asynchronous
notification delivery finishes. The persistent OpenCode server runs above
verified both loaded desktop outcomes. This MVP is best effort at process
shutdown; it does not promise delivery after the host exits.

### Visible desktop check (2026-09-30)

A second disposable, Git-initialized test project used the merged Notifications
binary (`sha256:a1a70f35a6fbdd40ac50123dca5582c6fb4fe6e88724427bfc5f7e8d0460e7aa`)
and OpenCode 1.18.33. `setup-opencode install --desktop` created a mode-0755
managed executable and a mode-0600 global plugin. The loaded OpenCode config
listed only that plugin. In a private D-Bus session with a real `dunst` daemon
and Xvfb display, native completion, question, permission, and error events each
rendered one visible system banner (`dunstctl count displayed` was 1 at each
capture). The banners used generic text and omitted the test prompt markers.
The [four captured banners](evidence/opencode-1.18.33-x11-notifications.png)
show the actual X11 pixels, in that order. This qualifies the Linux X11/dunst
path, not every desktop environment or the OpenCode desktop app's own switches.
