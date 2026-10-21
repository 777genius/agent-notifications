# Qoder completion notifications

The Qoder adapter hooks the same attention events as the Claude adapter:
completion, questions, permission prompts, plan readiness, subagent
completion, and idle teammates. Events arrive as native Qoder hook payloads;
the dedicated native bundle registers one handler per event plus a platform
executable. It does not install an MCP observer or change the existing
portable package and managed-runtime ledger.

## Hook events

| Event | Notification | Default |
| --- | --- | --- |
| `Stop` | task complete | on |
| `Notification` | question | on |
| `PermissionRequest` | permission request | on |
| `PreToolUse` (matcher `ExitPlanMode\|AskUserQuestion`) | plan ready / question | on |
| `SubagentStop` | subagent task complete | off (opt-in) |
| `TeammateIdle` | idle teammate | on, but silent without team identity |

`Stop` rides the Go SDK's Qoder transport. The other five are decoded directly
as strict hook JSON: the event name inside the payload must match the handler,
oversized payloads are rejected, and no transcript path is assumed —
everything is derived from the event payload itself. Continuation turns never
notify. `PermissionRequest` notifications name the tool and are labeled
`Qoder requests permission`.

## Configuration

Installing this separate package opts into the Qoder hooks; the existing
generic portable installer does not select Qoder automatically. Settings use
the shared file reported by `claude-notifications config path`. An existing
`AGENT_NOTIFICATIONS_CONFIG` selection is honored; set it to an absolute config
file path when using a separate TEST profile. Qoder inherits global
notification settings; an optional schema version 2 `agents.qoder` profile
provides overrides. Merge this into the existing document rather than replacing
other agents' settings. Omitted fields continue to inherit global settings;
this example deliberately disables sound and click-to-focus for Qoder:

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

## Build a local native package

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
`agent-notifications-qoder-<event>` and runs
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

This package builder is source support. Existing 1.49.5 public assets predate the
Qoder adapter. The current basic release operator seals an exact asset set; this
change does not add Qoder ZIPs to that seal or advertise an already published
Qoder release. Build from the updated source until a separately qualified release
includes this adapter and its package.

Opening the exact existing Qoder Desktop chat from a notification is not
supported here. The [documented Qoder chat deep link](https://docs.qoder.com/user-guide/deeplink) creates a new chat; a
session identifier from the hook is not evidence of an existing-chat deep link.
