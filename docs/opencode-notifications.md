# OpenCode notifications (Linux amd64)

OpenCode notifications are a separate, explicit product integration. They do not
inherit portable MCP consent or change its global `enabled` policy. The supported
host is Linux amd64; no OpenCode process is launched by setup.

Use `claude-notifications setup-opencode install` with an absolute, trusted,
current Linux amd64 `--binary` source. Specify `--runtime-root` for a new
installation; an existing managed installation uses its recorded runtime root.
Select `--desktop`, `--webhook`, or both.
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
