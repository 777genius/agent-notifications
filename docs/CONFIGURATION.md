[Back to README](../README.md)

# Configuration

Use the current installer and runtime for the configuration commands below. Update older installations before editing settings.

Run `/claude-notifications-go:settings` to configure sounds, volume, webhooks, and other options via an interactive wizard. You can re-run it anytime to reconfigure.

## OpenCode channels and settings

Managed OpenCode uses its own control-root policy, selected with `config path --target opencode --json` and `config inspect --target opencode --json`. It does not use the default shared config target below. See [installed OpenCode settings](opencode-notifications.md#edit-installed-opencode-settings) for custom control roots and revision-checked edits.
It sends alerts for `task_complete`, `question`,
`permission_request` and `opencode_error`. Desktop alerts are silent by
default in published v1.49.11. Unreleased source builds introduce
`notifications.desktop.systemSound: true` to explicitly request the OS default
notification cue when the master sound setting is enabled; this does not
alter the published release until the next qualified platform promotion.
Custom Claude/Codex sounds, volume controls and click-to-focus remain unavailable.
Desktop alerts can include native session titles and current question text;
webhooks use generic text. Claude status templates do not apply.
Channel settings can restrict delivery, but they do not grant consent:
use `setup-opencode install|update` with explicit
`--desktop`, `--webhook` or both. This changes only OpenCode channel consent and
does not enable portable MCP notifications. Webhooks also need a configured,
enabled destination and an enabled status channel. Restart OpenCode after setup
or update. [Full setup and limits](opencode-notifications.md).

## Shared native observer sound policy (unreleased source)

Gemini CLI and Cursor CLI use the normal selected shared configuration, not
the separate managed OpenCode control root. Current public platform channels
(v1.49.11) still deliver silent alerts. In unreleased source builds, the optional
`notifications.desktop.systemSound` Boolean requests the **OS default cue**
only when `notifications.desktop.sound` is enabled. Missing/false settings remain
silent, and `agents.gemini` / `agents.cursor` schema-2 profiles can override
the shared value independently. A selected desktop channel is still required.
No sound file, output device, volume, focus target or chat-route API is added.
The internal Copilot-in-VS-Code observer uses the same policy but is not a
publicly qualified installer target. See the [support matrix](AGENT_SUPPORT_MATRIX.md).

## Manual Configuration

Use the installed config-capable executable (shown as `$NOTIFICATIONS_BIN` in recipes):

```bash
"$NOTIFICATIONS_BIN" config path --json
"$NOTIFICATIONS_BIN" config inspect --json
```

One file is selected per environment context: explicit **E**, otherwise existing **L**, otherwise **N**. Existing L is preserved; there is no automatic migration, copy, merge or synchronization.

| Selection | Native file path |
|---|---|
| E: `AGENT_NOTIFICATIONS_CONFIG` | An absolute **file**, not a directory |
| L: existing legacy file, macOS/Linux | `$HOME/.claude/claude-notifications-go/config.json` |
| L: existing legacy file, Windows | `%USERPROFILE%\.claude\claude-notifications-go\config.json` |
| N: fresh macOS | `$HOME/Library/Application Support/agent-notifications/config.json` |
| N: fresh Linux | Absolute nonempty `$XDG_CONFIG_HOME/agent-notifications/config.json`, otherwise `$HOME/.config/agent-notifications/config.json` |
| N: fresh Windows, safe profile | `%APPDATA%\agent-notifications\config.json` |
| N: Windows sandbox profile, no existing config | Absolute `%PROGRAMDATA%` (native KnownFolder if unset) + `agent-notifications-<Windows user SID>\config.json` |

Relative XDG_CONFIG_HOME is ignored with a diagnostic. Windows uses USERPROFILE for L, not Git Bash HOME; missing/relative APPDATA without L is an error. macOS ignores XDG_CONFIG_HOME. Missing home in automatic mode is an error; a valid explicit E supports portable contexts without HOME/APPDATA.

Unset E enables automatic selection; set-empty, whitespace-only, relative, `~/file` and invalid native paths are errors, never fallback. The resolver does not expand variables or tilde in E. Expand them in the calling shell if intended; Git Bash may use `cygpath -w` to supply a native absolute Windows path. Do not trim legitimate spaces in filenames.

For writable targets, `.lock` filenames and generated `.tmp-<32 hex digits>` / `.backup-<32 hex digits>` names are reserved for coordination and recovery metadata. Choose another filename for a portable config.

If L and N both exist, L wins with a diagnostic even if identical or N is newer. To intentionally select N, set E in **every** new adapter and CLI environment. Unsetting E restores legacy-first selection. Old binaries ignore E. `CODEX_HOME`, `CLAUDE_HOME`, `CLAUDE_CONFIG_DIR`, product, cwd and bundle/install paths do not select notification config. They retain their resource/installation meanings; permission markers, venv and state paths do not move.

`config init` is create-only: existing valid config is a byte/mode/mtime-preserving no-op; invalid existing config is an error. Hooks never write config and use in-memory defaults only for truly missing automatic config after historical recovery checks. Explicit missing E, corrupt/unreadable canonical files and unresolved recovery artifacts are errors, with no bundle/default fallback.

Use the [settings recipe](../commands/settings.md) for `config edit --stdin --expect-revision TOKEN`: private input, only requested JSON Pointer leaf edits, raw values, and an explicit user decision after any conflict. A volume plus one status sound edit preserves every other raw field, status, channel, webhook secret/payload and future-agent setting. Inspect is a safe projection, not a replacement document; absent free-form values are not unset.

Before any updater deletes/refreshes cache, personalized or unknown historical cache-only/custom-root settings require explicit import using a verified new helper: stop old writers, inspect the selected destination, then `config init --from FILE` only if that destination is missing. Never auto-copy a bundle, guess the newest cache, overwrite existing canonical config, or treat this as migration/reset. If preflight/helper is unavailable, stop the update and retain the old runtime. For existing invalid config, stop writers and explicitly repair/recover it while preserving damaged bytes; init cannot reset it.

Existing L with v1-shaped JSON remains readable by the old Go reader, which ignores additive unknown fields. The old wizard loses unknown fields and must not run concurrently with the current settings editor. Update all installed runtimes before selecting N/E paths; keep one canonical configuration file shared by those runtimes.

For support, share only `config inspect --json`, never `cat` of config; keep saved diagnostics private and review paths before posting. The response includes selection (path/source/exists/diagnostics), revision, schemaVersion, valid, optional errorCode, and safe settings: desktopEnabled, desktopSound, volume and known-status enabled/desktopEnabled/webhookEnabled. It omits free-form sounds, webhook secrets/payloads and unknown fields.

On Windows, `ConfigPublicReadable` is a warning about read grants on the selected
configuration file, not a check of `%APPDATA%` or the entire profile. It does not
by itself reject a hook read. `ConfigPermissionDenied` can instead come from an
OS access denial, the config/lock ACL, or a writable ancestor. `config init`
and failed `config inspect` now print an ACL rejection's actual object path,
reason, and trustee SID (or owner SID for an owner rejection). `config init
--json` and failed `config inspect --json` include optional `permissionFailure`
with `objectPath`, `reason`, and available `trusteeSid`, `ownerSid`, `accessMask`.
The mask contains the rejected rights, not the whole grant. OS denial cannot
identify an account and omits SID/mask. Account names are not resolved; match the
SID against `Get-Acl`/`icacls` locally, including unresolved SIDs. Parser/flag
errors remain content-free, and hooks retain their existing output behavior.
A genuinely absent config still inspects as defaults; inspect does not test
whether initialization could write it. Managed reads and
writes inspect each directory component below the volume root, reject reparse
points and unrelated write/delete/ACL-change grants, and pin directories against
replacement. Initialization of a profile target also checks `%USERPROFILE%` to coordinate the
`.agent-notifications-config.lock` selection guard. The automatic ProgramData
target uses a private selection guard beside its config instead. An unrelated read/execute
grant on an ancestor (including `CodexSandboxUsers:(RX)`) is allowed. `OWNER
RIGHTS` grants apply to the actual object owner; they are allowed when that owner
is trusted. New config and lock files grant access only to the current user and
SYSTEM; existing ACLs are never repaired automatically.

Inspect permissions with PowerShell `Get-Acl -LiteralPath <path>` or
`icacls <path>` for the selected file, its `.lock` sibling, containing directories,
and the profile guard. Repair only the object carrying the unexpected grant;
do not remove read access from all of `%APPDATA%` or the profile. Keep the current
user and SYSTEM access, and preserve existing config contents. After repair,
run `config init` and `config inspect --json`. To inspect managed setup after
`installation needs inspection`, run `setup-notifications status --json`
(optionally `--control-root <absolute managed control directory>`).
`--provider claude` belongs to configure/register operations, not status.

### Windows sandbox profile with write/delete grants

If `%USERPROFILE%` grants an unrelated principal `(M,DC)` (Modify and
DeleteChild), automatic initialization and managed reads can return
`ConfigPermissionDenied` even after the config directory is private. A parent
DeleteChild grant permits replacing its immediate children; disabling inheritance
only on the config directory does not secure the whole path. Do not exempt
`CodexSandboxUsers` or remove sandbox-required grants from the entire profile.

For a new installation with no legacy or APPDATA config, the current Windows
runtime automatically selects a private SID-specific directory in the native
ProgramData folder when the profile/creation ancestry fails ACL checks. A supplied
`PROGRAMDATA` must be an absolute path; custom roots retain strict ancestor
checks. The special handling of system add-only rights applies only to the
actual native KnownFolder and its volume root. Run
`config init` normally; no elevation, override or manual move is needed on a
standard Windows installation. CLI and hooks running as that Windows user select
the same file. Once created, it remains selected if the profile ACL is later
repaired. Its directory, config and lock must remain private; hostile precreated
entries, reparse points or unsafe ProgramData permissions cause a clear error,
without permission rewriting or a random alternative directory.

The v1.49.2 wizard/configure path could create this fallback using inherited
Windows ACLs instead of the protected store. Such a directory/file remains
rejected as unsafe; updating does not silently repair or trust its contents.
Stop writers and retain the existing file as untrusted recovery evidence. Review
its settings before importing into a newly provisioned private directory using
the explicit recovery procedure below. Replace webhook credentials if they were
stored in a file exposed to other local users. Fresh setup must create protected
explicit ACLs before publishing configuration bytes, just like `config init`.

Existing legacy/APPDATA config, including malformed or inaccessible entries and
recovery artifacts, retains priority before a private ProgramData store is
established. If a profile config appears alongside that established store,
automatic selection fails with `ConfigConflict`; choose the intended file with
`AGENT_NOTIFICATIONS_CONFIG`. This prevents an untrusted profile writer from
redirecting hooks to newly planted settings. Both files remain untouched. The runtime never hides it by creating a
fresh default or automatically imports potentially unsafe settings. If such an
existing config is below writable ancestry, or when using an older runtime,
use an explicit configuration outside that ancestry. An explicit path still
checks its own directories and private target lock; it can skip an unavailable
profile selection guard. Another path under `%USERPROFILE%`, including
`%LOCALAPPDATA%`, does not solve this case.

For example, an administrator can create a **new**, user-specific directory at
the system drive root. Run this PowerShell provision step as the affected user
with elevation (not as a different administrator account). It refuses an
existing directory and changes no profile or ancestor ACL:

```powershell
$ErrorActionPreference = 'Stop'
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$dir = Join-Path ($env:SystemDrive + '\') ("agent-notifications-" + $sid)
if (Test-Path -LiteralPath $dir) { throw "Directory already exists: $dir" }
New-Item -ItemType Directory -Path $dir | Out-Null
$acl = New-Object System.Security.AccessControl.DirectorySecurity
$acl.SetSecurityDescriptorSddlForm("O:${sid}D:P(A;OICI;FA;;;${sid})(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
Set-Acl -LiteralPath $dir -AclObject $acl
```

Back in the affected user's ordinary shell, set the explicit **file** path:

```powershell
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$dir = Join-Path ($env:SystemDrive + '\') ("agent-notifications-" + $sid)
$env:AGENT_NOTIFICATIONS_CONFIG = Join-Path $dir 'config.json'
```

Using the recorded installed executable, run `config path --json`, then
`config init` and `config inspect --json`. If preserving an existing personalized
config, stop its writers and use `config init --from <verified source file>`
instead of initializing defaults; retain the original file. If the source has a
managed `.lock` under the writable profile, importing directly still checks that
unsafe ancestry. In that case, explicitly make a byte-preserving copy into the
new private directory under a different filename, verify its SHA-256 against the
original, and import that copy (without a source `.lock`). Do not use inspect
output as the source document. Initialization is create-only. Stop if initialization reports another permission error and inspect
that target's complete ACL chain; do not weaken its checks.

Every agent and hook must inherit the same override. To persist it for this
Windows user, after successful initialization:

```powershell
[Environment]::SetEnvironmentVariable('AGENT_NOTIFICATIONS_CONFIG', $env:AGENT_NOTIFICATIONS_CONFIG, 'User')
```

Restart Windows Terminal and agent applications so they inherit the variable,
then confirm the same selected path in their hook context. Leave `%USERPROFILE%`
and `%APPDATA%` pointing to their real locations. This storage workaround is
supported by v1.49.1 and does not depend on the separate OWNER RIGHTS fix.

The following JSON illustrates the schema. Do not replace your existing document with it; apply only explicitly requested leaf edits:

```json
{
  "notifications": {
    "desktop": {
      "enabled": true,
      "sound": true,
      "volume": 1.0,
      "audioDevice": "",
      "clickToFocus": true,
      "terminalBundleId": "",
      "showSessionLabel": true,
      "appIcon": "${AGENT_NOTIFICATIONS_ROOT}/claude_icon.png"
    },
    "webhook": {
      "enabled": false,
      "preset": "slack",
      "url": "",
      "chat_id": "",
      "format": "json",
      "headers": {},
      "payloadFields": {}
    },
    "suppressQuestionAfterTaskCompleteSeconds": 12,
    "suppressQuestionAfterAnyNotificationSeconds": 7,
    "notifyOnSubagentStop": false,
    "suppressForSubagents": true,
    "notifyOnTextResponse": true,
    "respectJudgeMode": true,
    "notifyOnlyWhenUnfocused": false,
    "notifyDelaySeconds": 0,
    "respectDoNotDisturb": "off",
    "replaceNotificationsPerSession": false,
    "suppressFilters": [
      {
        "name": "Suppress ClaudeProbe completions (remote-control)",
        "status": "task_complete",
        "gitBranch": "",
        "folder": "ClaudeProbe"
      }
    ]
  },
  "statuses": {
    "task_complete": {
      "title": "✅ Completed",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/task-complete.mp3"
    },
    "review_complete": {
      "title": "🔍 Review",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/review-complete.mp3"
    },
    "question": {
      "title": "❓ Question",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/question.mp3"
    },
    "plan_ready": {
      "title": "📋 Plan",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/plan-ready.mp3"
    },
    "session_limit_reached": {
      "title": "⏱️ Session Limit Reached",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/error.mp3"
    },
    "api_error": {
      "title": "🔴 API Error: 401",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/error.mp3"
    },
    "api_error_overloaded": {
      "title": "🔴 API Error",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/error.mp3"
    }
  }
}
```

## Session context

Claude/Codex hook notifications prefer the native session name over the generated
word/id label. Claude uses an optional `session_title` hook field, then exact-session
`custom-title`/`ai-title` transcript records. Codex reads the latest exact-session
`thread_name` from `$CODEX_HOME/session_index.jsonl` (default `~/.codex`). These are
bounded, read-only, best-effort native file formats, not a public live Desktop API.
Missing, unsupported, or out-of-window metadata keeps the generated label. Each
hook rereads supported metadata; Claude renames outside the bounded transcript
head/tail windows can be missed. No session is resumed or model called to name it.

Claude `AskUserQuestion` uses the current `tool_input`, rather than a previous
question in the transcript. Codex question hooks use the question text supplied
by the host. Questions appear in the headline; the session and project appear in
the native macOS subtitle or the body on backends without subtitles. Missing
question text keeps the ordinary question fallback. `desktop.showSessionLabel`
hides native and generated names without hiding the question or project.

| Option | Default | Description |
|--------|---------|-------------|
| `notifyOnSubagentStop` | `false` | Send notifications when subagents (Task tool) complete. Has no effect unless `suppressForSubagents` is also set to `false`. |
| `suppressForSubagents` | `true` | Suppress subagent (`SubagentStop`) notifications, plus any `Stop` notification whose transcript is a subagent/teammate transcript. Detection uses the hook event for `SubagentStop` (Claude passes the parent session `transcript_path` to that hook, so a path check alone can't identify it). Set to `false` together with `notifyOnSubagentStop: true` to get a notification each time a subagent finishes. |
| `notifyOnTextResponse` | `true` | Send notifications for text-only responses (no tool usage) |
| `desktop.showSessionLabel` | `true` | Show the native session name, with `[name id]` as fallback. Questions identify the session in the native macOS subtitle or the body on backends without subtitles. `false` hides both kinds of session label without hiding the actual question. |
| `respectJudgeMode` | `true` | Honor `CLAUDE_HOOK_JUDGE_MODE=true` env var to suppress notifications |
| `notifyOnlyWhenUnfocused` | `false` | Skip the desktop notification only when the focused terminal window can be matched to the current Claude session. Best-effort per platform; if focus can't be determined the notification is still shown. |
| `notifyDelaySeconds` | `0` | Wait N seconds before delivering a desktop notification (capped at 25s by the hook timeout). With `notifyOnlyWhenUnfocused`, focus is re-checked after the wait. Webhooks are unaffected. |
| `respectDoNotDisturb` | `"off"` | Honour the desktop's Do Not Disturb state. `"silent"` still delivers the banner (so it reaches the notification centre) but skips the plugin's sound; `"suppress"` skips the notification entirely. Linux only for now (KDE Plasma, GNOME, XFCE, dunst); other platforms always report "not in DND". Webhooks are unaffected. See [Do Not Disturb](DO_NOT_DISTURB.md). |
| `respectDisplaySleep` | `false` | Skip the plugin's own sound while every display is asleep; the banner is still delivered. macOS only for now; other platforms always report "not asleep". Independent of `respectDoNotDisturb`. Webhooks are unaffected. |
| `replaceNotificationsPerSession` | `false` | macOS only. Let a session's newest desktop notification **replace** its previous banner in place instead of stacking a new one. Different sessions never collide. Default `false` keeps every banner so unread alerts are never silently dropped. |
| `suppressQuestionAfterTaskCompleteSeconds` | `12` | Suppress question notifications for N seconds after task complete |
| `suppressQuestionAfterAnyNotificationSeconds` | `7` | Suppress question notifications for N seconds after any notification |
| `suppressFilters` | `[]` | Array of rules to suppress notifications by status, git branch, and/or folder. Each rule is an AND of its fields; omitted fields match any value. Set `gitBranch` to `""` to match sessions outside git repos. |

Each status can be individually disabled by adding `"enabled": false`.

You can also override individual channels per status:

```json
{
  "statuses": {
    "question": {
      "title": "❓ Question",
      "sound": "${AGENT_NOTIFICATIONS_ROOT}/sounds/question.mp3",
      "desktop": { "enabled": true },
      "webhook": { "enabled": false }
    }
  }
}
```

`statuses.<name>.enabled` is still the master switch for both channels. Use
`desktop.enabled` and `webhook.enabled` when you want one channel on and the
other off for the same status.

### Focus-Aware & Delayed Notifications

Two independent options cut notification noise when you're already watching the terminal:

- **`notifyOnlyWhenUnfocused`** - skip the desktop notification only when the focused terminal window can be matched to the current Claude session. On Linux this works on X11 terminals that export `$WINDOWID`, and in [JetBrains IDE terminals](CLICK_TO_FOCUS.md#jetbrains-ides).
- **`notifyDelaySeconds`** - wait N seconds before delivering, so a quick task can finish before any banner appears (capped at 25s to stay within the hook timeout).

They compose: with both set, the plugin waits, then notifies only if the terminal still isn't focused - "tell me once I've looked away."

```json
{
  "notifications": {
    "notifyOnlyWhenUnfocused": true,
    "notifyDelaySeconds": 10
  }
}
```

Both apply to **desktop notifications only** - webhook delivery is never delayed or suppressed. Focus detection is best-effort and degrades safely by notifying when unsure:

- macOS: Ghostty can be matched by exact terminal/session metadata; other terminal apps require the frontmost window title to match the project folder and existing Screen Recording access.
- Linux: X11 sessions compare `$WINDOWID` to the active window. In JetBrains IDE terminals the active window must belong to the IDE process and its title must name the project (KDE Plasma: `kdotool`, X11: `xdotool`); an inherited `$WINDOWID` is ignored there. Other Wayland sessions and terminals without `$WINDOWID` are treated as unknown.
- Windows: the foreground window must belong to the hook process ancestry and its title must contain the project folder. Ambiguous multi-window or multi-tab terminal hosts are treated as unknown.

Unknown means "show the notification", not "suppress it".

### Do Not Disturb

The plugin plays its notification sound itself, in its own process, which is why
the sound used to come through at full volume while the desktop was in Do Not
Disturb: the banner was correctly silenced by the desktop, but nothing had any
say over a separate process's audio.

`respectDoNotDisturb` fixes that. It is `"off"` by default, so nothing changes
until you opt in:

```json
{
  "notifications": {
    "respectDoNotDisturb": "silent"
  }
}
```

- **`"off"`** (default) - DND state is never queried.
- **`"silent"`** - the banner is still delivered, so it lands in the notification centre and shows when DND lifts, but the plugin's sound is skipped.
- **`"suppress"`** - nothing is delivered.

Detection is Linux-only for now - KDE Plasma and other daemons exposing
`org.freedesktop.Notifications.Inhibited`, dunst, XFCE and GNOME. macOS Focus
modes and Windows Focus Assist are not detected yet, so `respectDoNotDisturb`
has no effect there. Like focus detection, it fails open: anything it cannot read
counts as "not in DND" and the notification is delivered with its sound.

Webhooks are unaffected in every mode. See [Do Not Disturb](DO_NOT_DISTURB.md)
for the exact sources per desktop, the latency budget, and how to verify which
one fired.

### Mute Sound While Display Is Asleep

A machine can be fully awake and still have nobody in front of it - the display
timed out and went to sleep. `respectDisplaySleep` skips the plugin's own sound
in that case, the same way `respectDoNotDisturb` does for Do Not Disturb; the
banner is still delivered so it is waiting in the notification centre when the
display wakes up.

It is `false` by default, so nothing changes until you opt in:

```json
{
  "notifications": {
    "respectDisplaySleep": true
  }
}
```

Detection is macOS-only for now, via the same public Quartz Display Services
API used by other window-management tools, and considers every attached
display: on a laptop in clamshell mode with an external monitor still on, the
built-in display reports asleep while you are actively working, so the sound is
muted only when no attached display is awake. Linux and Windows are not
detected yet, so the option has no effect there. Detection fails open the same
way Do Not Disturb detection does: anything it cannot read counts as "not
asleep" and the notification keeps its sound.

This is a different signal from Do Not Disturb - a display can go to sleep
without Focus/DND being on, and DND can be on with the display wide awake - so
the two options are independent and compose freely.

### Replace Per-Session Banners (macOS)

By default every desktop notification carries a unique identifier, so banners stack and nothing is ever lost - a session that finishes several tasks (or, for Codex, crosses the sandbox boundary repeatedly in "Approve for me" mode) produces one banner per event.

On macOS `terminal-notifier -group` is the `UNNotificationRequest.identifier`: delivering a request that reuses a value replaces the previous banner in place. Setting `replaceNotificationsPerSession: true` keys that identifier on the Claude/Codex session id, so a session's newest banner replaces its own previous banner while other sessions stay untouched.

```json
{
  "notifications": {
    "replaceNotificationsPerSession": true
  }
}
```

- Conversation grouping in Notification Center is unchanged - it stays on the session thread (`-threadID`).
- Notifications with no session id (or `unknown`) always stay unique, so unrelated alerts are never collapsed.
- Trade-off: a replaced banner disappears from Notification Center, so earlier alerts from that session are no longer visible. Leave it off (the default) if you step away and need to catch up on every alert.

### Sound Options

**Built-in sounds** (included):
- `${AGENT_NOTIFICATIONS_ROOT}/sounds/task-complete.mp3`
- `${AGENT_NOTIFICATIONS_ROOT}/sounds/review-complete.mp3`
- `${AGENT_NOTIFICATIONS_ROOT}/sounds/question.mp3`
- `${AGENT_NOTIFICATIONS_ROOT}/sounds/plan-ready.mp3`
- `${AGENT_NOTIFICATIONS_ROOT}/sounds/error.mp3`

**System sounds:**
- macOS: `/System/Library/Sounds/Glass.aiff`, `/System/Library/Sounds/Hero.aiff`, etc.
- Linux: `/usr/share/sounds/**/*.ogg` (varies by distribution)
- Windows: Use built-in MP3s (system sounds not easily accessible)

**Supported formats:** MP3, WAV, FLAC, OGG/Vorbis, AIFF

### List Available Sounds

See all available notification sounds on your system:

```bash
# List all sounds (built-in + system)
bin/list-sounds

# Output as JSON
bin/list-sounds --json

# Preview a sound
bin/list-sounds --play task-complete

# Preview at specific volume
bin/list-sounds --play Glass --volume 0.5
```

Or use the skill command: `/claude-notifications-go:sounds`

### Audio Device Selection

On macOS, notification sounds follow **System Settings > Sound > Play sound
effects through**. This alert output may differ from the output used by music,
calls, or sound previews. The plugin does not change either system output or
system volume. Its configured `volume` scales the sound samples; the system alert
volume also applies.

The macOS notification backend does not use `audioDevice`: UserNotifications
has no per-notification output-device parameter. Choosing an alert output in
System Settings affects other system alerts too. The plugin must not temporarily
switch that global output to emulate a per-plugin selection.

On Linux and Windows, `audioDevice` routes notification sounds to a specific
audio output device instead of the system default:

```bash
# List available audio devices
bin/list-devices

# Output:
#   0: MacBook Pro-Lautsprecher
#   1: Babyface (23314790) (default)
#   2: Immersed
```

Then add the device name to the shared file selected by `config path`:

```json
{
  "notifications": {
    "desktop": {
      "audioDevice": "DEVICE_NAME"
    }
  }
}
```

Leave `audioDevice` empty or omit it to use the system default device.

### Test Sound Playback

Preview any sound file with optional volume control:

```bash
# Test built-in sound (full volume)
bin/sound-preview sounds/task-complete.mp3

# Test with reduced volume (30% - recommended for testing)
bin/sound-preview --volume 0.3 sounds/task-complete.mp3

# Test macOS system sound at 30% volume
bin/sound-preview --volume 0.3 /System/Library/Sounds/Glass.aiff

# Test custom sound at 50% volume
bin/sound-preview --volume 0.5 /path/to/your/sound.wav

# Show all options
bin/sound-preview --help
```

**Volume flag:** Use `--volume` to control playback volume (0.0 to 1.0). Default is 1.0 (full volume).
