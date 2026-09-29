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
