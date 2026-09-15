# Shared configuration and agent overrides

`agent-notifications` is the primary CLI. `claude-notifications` is a permanent
compatibility alias. Both installed launchers call the same platform binary;
`version` displays the invoked name. Release executable names, plugin ID,
installation directories and existing hook commands stay compatible.
On Windows use `bin\agent-notifications.bat`; the legacy `.bat` and absolute
`claude-notifications-windows-*.exe` paths remain supported.

## Upgrade an existing configuration

1. Update both Claude and Codex runtimes before enabling schema 2.
2. Run `agent-notifications version` and `agent-notifications config inspect --json`.
3. Find the selected file using `agent-notifications config path`.
4. Change `schemaVersion` to `2`, then add overrides manually:

```json
{
  "schemaVersion": 2,
  "notifications": {
    "desktop": {
      "volume": 0.8,
      "appIcon": "${AGENT_NOTIFICATIONS_ROOT}/claude_icon.png"
    }
  },
  "agents": {
    "claude": {"notifications": {"desktop": {"volume": 0.5}}},
    "codex": {"notifications": {"desktop": {"sound": false, "volume": 0}}}
  }
}
```

5. Run `agent-notifications config inspect --json` again. It validates the global
   configuration and all stored profiles without requiring inactive-agent secrets.

No command automatically migrates an existing file. Missing `schemaVersion` means
schema 1; schema 1 rejects `agents`. New installations still start with schema 1.
Older runtimes must be upgraded before using schema 2.

## Merge contract

Program defaults are overlaid by global settings, then by `agents.claude` or
`agents.codex`, selected explicitly by the hook. Config path selection remains
explicit environment path, existing legacy path, then universal path (E > L > N).

Missing fields inherit; `false`, `0`, empty strings and empty arrays are explicit
values. Objects merge recursively, including statuses by name. Webhook `headers`
and `payloadFields` replace the entire dictionary: `{}` clears inherited values,
including credentials. Schema 2 preserves volume zero; schema 1 keeps its historic
zero-to-one behavior. Null anywhere in an agent override is rejected; remove the
field to inherit instead. Known-field casing collisions and invalid types fail.

Agent IDs must match `[a-z][a-z0-9_-]{0,63}`. Overrides allow only `notifications`,
`statuses`, and `debug`; nested agent sections and schema metadata are forbidden.
Unknown valid agent IDs and additive top-level settings are preserved for future
runtimes. Unknown fields inside `agents.<id>` are rejected by strict agent
validation rather than preserved. Current runtimes apply only `claude` and `codex`.

`config edit` and the settings wizard edit global fields only and preserve agent
sections. `/agents/...` edits are rejected; use a text editor for these sections.
`config inspect` reports global settings, not an agent-specific effective view.

## Resource placeholders

`${AGENT_NOTIFICATIONS_ROOT}` is the resource bundle root, not the config directory.
`${CLAUDE_PLUGIN_ROOT}` is a permanent alias. Resolution prefers the explicit asset
context, then environment `AGENT_NOTIFICATIONS_ROOT`, then `CLAUDE_PLUGIN_ROOT`, then
`.`. Expansion happens once, preserving literal dollar signs in the resolved root.
Expanded paths are never written back to config. Existing files are not rewritten.

Claude-managed hook commands must retain `${CLAUDE_PLUGIN_ROOT}` because Claude
itself substitutes that name. The new name is for config resource paths.

## Rollback

Copy the desired agent values into global settings, remove `agents`, and set
`schemaVersion` back to `1` before starting an older runtime. There is no automatic
downgrade. Keep a backup if you need to restore individual profiles later.
