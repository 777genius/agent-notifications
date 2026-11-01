# Qoder completion notifications

The Qoder adapter hooks the same attention events as the Claude adapter:
completion, questions, permission prompts, plan readiness, subagent
completion, idle teammates and (in unreleased source) unexpected stop failures.
Events arrive as native Qoder hook payloads;
the dedicated native bundle registers one handler per event plus a platform
executable. The common installer builds this bundle from the verified release
executable. UAP manages native package delivery, registration, verification,
update and removal; the shared runtime ledger records the notification consumer
and its consent bound to that UAP installation and client binding.
No MCP observer is installed.

This UAP lifecycle and common Cursor/Qoder selection are available in
[platform release v1.49.11](PLATFORM_RELEASE_CHANNELS.md). The extra
`StopFailure` notification is **source-only** until a later qualified native
Qoder package and platform release are published.
Older custom Qoder installations are not adopted or migrated by this flow.

## Hook events

| Event | Notification | Default |
| --- | --- | --- |
| `Stop` | task complete | on |
| `StopFailure` | unexpected agent error; rate limit distinguished | on, unreleased source only |
| `Notification` | question | on |
| `PermissionRequest` | permission request | on |
| `PreToolUse` (matcher `ExitPlanMode\|AskUserQuestion`) | plan ready / question | on |
| `SubagentStop` | subagent task complete | off (opt-in) |
| `TeammateIdle` | idle teammate | on; records idle state, team-completion alert requires a stopped lead |

`Stop` rides the Go SDK's Qoder transport. The other six are decoded directly
as strict hook JSON: the event name inside the payload must match the handler,
oversized payloads are rejected, and no transcript path is assumed —
everything is derived from the event payload itself. Continuation turns never
notify. The Qoder wire carries no turn or subagent identity, so identical
re-prompts or identical subagent completions within the dedup window collapse
to a single notification. `PermissionRequest` notifications name the tool and are labeled
`Qoder requests permission`. A native `StopFailure` is **not** a successful completion:
only the documented `rate_limit` class maps to `api_error_overloaded`, and all
other errors map to the generic `agent_error` status. Error text, details and
last assistant message are excluded from notification copy; configured
webhook/desktop channel consent still applies. This source-only event does
not establish a visible platform banner until native E2E qualification.

## Configuration

**Sound asset release boundary:** Published v1.49.11 Qoder native UAP packages did not include the built-in MP3 assets, so configured cues could be silent. The unreleased source now embeds the existing five reviewed MP3 files into both standalone and managed Qoder bundles and verifies their exact contents in native package and ZIP tests. This does **not** retroactively update v1.49.11, prove audible physical playback, or permit sounds before user consent. [Issue #562](https://github.com/777genius/agent-notifications/issues/562) tracks release/native audio qualification.


Select **Qoder** in the common installer, alone or alongside other agents.
The Qoder CLI must be available on the selected `PATH`; an IDE installation alone
does not supply the plugin-management CLI. Both `qoder` and `qodercli` command
names are supported when available. Installation and update use the same command:

```sh
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash)
```

For an explicit combined selection, pass `--products codex,qoder,cursor --desktop`
to that loader. `--product qoder` selects Qoder alone. The installer shows one
selection and channel confirmation. Fresh installs default to desktop on and
webhooks off. Omitting channel flags on update preserves Qoder's complete saved
choice, including both channels off. `--desktop` selects desktop only,
`--webhook` selects webhooks only, and both select both channels. Use
`--desktop=false --webhook=false` to retain the integration with delivery disabled.
Granting webhook consent does not configure a destination.
Managed hooks require both the recorded channel consent and shared notification
settings. Uninstall removes only the owned plugin registration and bundle; edited
or unowned files cause a refusal. Existing Qoder plugins and settings are preserved.
The native profile root is `QODER_CONFIG_DIR`, defaulting to `~/.qoder`; native
CLI operations are bound to that selected profile using `--config-dir`. Settings use
the shared file reported by `claude-notifications config path`. An existing
`AGENT_NOTIFICATIONS_CONFIG` selection is honored; set it to an absolute config
file path when using a separate TEST profile. Qoder inherits global
notification settings; an optional schema version 2 `agents.qoder` profile
provides overrides. Merge this into the existing document rather than replacing
other agents' settings. Omitted fields continue to inherit global settings;
this example deliberately disables sound for Qoder. `clickToFocus` is shown for
schema completeness only: click-to-focus capture is currently Claude-only and
the knob is inert for Qoder.

```json
{
  "schemaVersion": 2,
  "agents": {
    "qoder": {
      "notifications": {
        "desktop": {
          "enabled": true,
          "sound": false,
          "clickToFocus": false
        },
        "webhook": {"enabled": false}
      }
    }
  }
}
```

Per-event opt-in notes:

- `SubagentStop` follows the shared subagent policy: `notifications.suppressForSubagents`
  suppresses it by default, and even with suppression lifted the explicit
  `notifications.notifyOnSubagentStop` flag must be true before a subagent
  completion notifies.
- `TeammateIdle` needs a team identity in the payload (team name and teammate
  name); without one the handler stays silent.

The hooks run synchronously so Qoder waits for delivery before continuing.
Handlers remain observation-only and do not block task completion with a
policy decision. Each hook has a 40 second timeout, covering the existing
configurable notification delay and bounded webhook shutdown. Desktop
notifications still require the operating system's normal notification
permissions.

## Inspect and remove

The installer prints the retained executable path with `BOOTSTRAP_VERBOSE=1`.
Use that executable for lifecycle commands (retain `--config-root` and
`--control-root` from the printed commands if using a custom profile):

```sh
/absolute/path/to/claude-notifications setup-qoder inspect
/absolute/path/to/claude-notifications setup-qoder remove --qoder-executable /absolute/path/to/qoder
```

Reload Qoder after installation, update or removal so existing sessions pick up
the changed hooks. Click-to-focus to the exact existing Qoder chat is unavailable.

## Build a standalone native package

Build the notifications executable for the machine that will run Qoder, then
package those exact bytes. For example, on Linux amd64:

```sh
GOWORK=off go build -o /absolute/TEST-qoder/claude-notifications ./cmd/claude-notifications
GOWORK=off go run ./cmd/build-qoder-package \
  -version 1.49.5 -os linux -arch amd64 \
  -executable /absolute/TEST-qoder/claude-notifications \
  -workdir /absolute/TEST-qoder/package \
  -output /absolute/TEST-qoder/agent-notify-qoder-linux-amd64.zip
```

Use `darwin` or `windows` and the correct `amd64`/`arm64` for other hosts. Windows
packages contain `bin/claude-notifications.exe`. This command does not cross-build
the executable. Output roots and archives must be new paths. The default version
comes from the same `config.ConsumerVersion` as the existing portable builder.

The expanded package includes both a standard identity `plugin.json` and native
`.qoder-plugin/plugin.json`, plus `hooks/hooks.json`. The package identity is
`agent-notifications-qoder`; each hook names itself
`agent-notifications-qoder-<lowercased event>` and runs
`${QODER_PLUGIN_ROOT}/bin/claude-notifications` (with `.exe` on Windows).
Native arguments are separate fields, allowing package roots with spaces.

Install the expanded package with Qoder's native plugin installer:

```sh
qoder plugins install /absolute/TEST-qoder/package --scope user
qoder plugins list --json
```

For lifecycle qualification, use a disposable TEST profile and actual Qoder
session, observe one completion notification, then update and remove the same
plugin. Registration alone does not prove notification delivery. Qoder Desktop
may require a reload/restart before a newly installed hook becomes active.

## Scope and release status

The unified installation assembles the native bundle locally from the same
checksum-verified executable as the other agents. It does not require a separate
Qoder ZIP in the release asset set. Standalone package builds remain available
for users managing plugins directly. Native lifecycle and real-turn evidence
are recorded separately from synthetic hook-payload tests; see the
[Qoder E2E handoff](handoffs/qoder-native-e2e.md).

Opening the exact existing Qoder Desktop chat from a notification is not
supported here. The [documented Qoder chat deep link](https://docs.qoder.com/user-guide/deeplink) creates a new chat; a
session identifier from the hook is not evidence of an existing-chat deep link.
