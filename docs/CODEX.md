[Back to README](../README.md)

# Codex CLI Support

The same binary can notify for OpenAI Codex CLI sessions.

### Setup

Use the [one-command installer](INSTALLATION.md#quick-install-recommended) and choose Codex or both.
It downloads matching release source and binaries, registers the hooks, and keeps a stable
runtime copy. Then start Codex and approve the entries in `/hooks`.

### Manual Codex registration

Skip this section if you used the one-command installer. For manual setup, download a
matching release bundle and binary (v1.42.0 or newer). The Go registration command needs
no `jq` and is not automatically added to your `PATH`.

From the bundle directory:

```bash
./bin/agent-notifications setup-codex --plugin-root .
```

On Windows, run the installed primary launcher in PowerShell (the downloaded
`claude-notifications-windows-amd64.exe` remains compatible):

```powershell
.\bin\agent-notifications.bat setup-codex --plugin-root .
```

Run these commands in the bundle directory. If you have explicitly added the binary to
`PATH`, `agent-notifications setup-codex --plugin-root <bundle-directory>` also works.

It installs a self-contained copy of the plugin at `~/.codex/claude-notifications-go` and writes
the hook entries into `~/.codex/hooks.json`. Existing foreign hook definitions and unknown fields are preserved,
and every run saves a uniquely named backup of the previous file next to it.

Then start Codex, run `/hooks`, review the entries and trust them.

Useful flags: `--dry-run` shows what would change, `--print` outputs the JSON so you can merge it
yourself, `--codex-home` and `--plugin-root` override the paths.

For manual updates, run the registration command again to refresh the installed copy.
Unchanged hook definitions retain trust; changed definitions require review again.
The one-command installer handles this registration step automatically.

Claude Code installation and updates continue to use the [existing installation steps](INSTALLATION.md).
Both products share settings at the shared file selected by `config path`; installing
Codex does not require installing Claude Code. Keep your existing settings file when updating.

<details>
<summary>How registration works</summary>

`setup-codex` registers user hooks explicitly, using a stable runtime directory independent
of the plugin cache. This is the setup path covered by this project's installer tests.
The bundle also includes a Codex plugin manifest. Codex versions can differ in plugin-hook
loading; follow the [current Codex hooks documentation](https://learn.chatgpt.com/docs/hooks)
for native plugin setup. Use one registration path to avoid duplicate hooks, and inspect
`/hooks` after installation.

Codex includes the command string in its trust hash, so the registration deliberately points at
the stable `~/.codex/claude-notifications-go` copy rather than a versioned plugin cache
directory — that is what keeps the trust valid across updates.

</details>

What works today:

- **Stop** - a turn finishes; the status comes from the final assistant message: short failure
  reports map to the API Error / Session Limit statuses, a trailing question mark maps to
  Question, otherwise Task Complete. The Codex rollout transcript is not parsed (it is an
  internal, unstable format).
- **Question payloads** - when Codex emits `PreToolUse` for `request_user_input`,
  the plugin delivers the question/header text. Options, ids, and secret fields are excluded.
  Delivery depends on the active Codex mode exposing this tool hook.
- **PermissionRequest** - Codex is waiting for your approval of a tool call; delivered as the
  time-sensitive Permission Request status. Only the tool name is shown, never the tool input.
- **SubagentStop** (opt-in) - with `notifyOnSubagentStop: true` and `suppressForSubagents: false`,
  subagent completions notify with the subagent's final message.

Known limitations:

- PermissionRequest cannot fire when Codex never asks for approval (`bypassPermissions`,
  `--ask-for-approval never`, headless `codex exec`).
- The error statuses for Codex come from a text heuristic over the final message (short messages
  with failure phrasing), not from structured error data - false negatives are possible.
- The `request_user_input` question hook is limited to the modes where Codex exposes that tool.
- Codex hooks require a trust review (`/hooks` inside Codex); changed definitions require review again.

Both products share one config file (the shared file selected by `config path`).
