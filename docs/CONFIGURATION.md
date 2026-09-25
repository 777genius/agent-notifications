[Back to README](../README.md)

# Configuration

Use the current installer and runtime for the configuration commands below. Update older installations before editing settings.

Run `/claude-notifications-go:settings` to configure sounds, volume, webhooks, and other options via an interactive wizard. You can re-run it anytime to reconfigure.

### Manual Configuration

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
| N: fresh Windows | `%APPDATA%\agent-notifications\config.json` |

Relative XDG_CONFIG_HOME is ignored with a diagnostic. Windows uses USERPROFILE for L, not Git Bash HOME; missing/relative APPDATA without L is an error. macOS ignores XDG_CONFIG_HOME. Missing home in automatic mode is an error; a valid explicit E supports portable contexts without HOME/APPDATA.

Unset E enables automatic selection; set-empty, whitespace-only, relative, `~/file` and invalid native paths are errors, never fallback. The resolver does not expand variables or tilde in E. Expand them in the calling shell if intended; Git Bash may use `cygpath -w` to supply a native absolute Windows path. Do not trim legitimate spaces in filenames.

For writable targets, `.lock` filenames and generated `.tmp-<32 hex digits>` / `.backup-<32 hex digits>` names are reserved for coordination and recovery metadata. Choose another filename for a portable config.

If L and N both exist, L wins with a diagnostic even if identical or N is newer. To intentionally select N, set E in **every** new adapter and CLI environment. Unsetting E restores legacy-first selection. Old binaries ignore E. `CODEX_HOME`, `CLAUDE_HOME`, `CLAUDE_CONFIG_DIR`, product, cwd and bundle/install paths do not select notification config. They retain their resource/installation meanings; permission markers, venv and state paths do not move.

`config init` is create-only: existing valid config is a byte/mode/mtime-preserving no-op; invalid existing config is an error. Hooks never write config and use in-memory defaults only for truly missing automatic config after historical recovery checks. Explicit missing E, corrupt/unreadable canonical files and unresolved recovery artifacts are errors, with no bundle/default fallback.

Use the [settings recipe](../commands/settings.md) for `config edit --stdin --expect-revision TOKEN`: private input, only requested JSON Pointer leaf edits, raw values, and an explicit user decision after any conflict. A volume plus one status sound edit preserves every other raw field, status, channel, webhook secret/payload and future-agent setting. Inspect is a safe projection, not a replacement document; absent free-form values are not unset.

Before any updater deletes/refreshes cache, personalized or unknown historical cache-only/custom-root settings require explicit import using a verified new helper: stop old writers, inspect the selected destination, then `config init --from FILE` only if that destination is missing. Never auto-copy a bundle, guess the newest cache, overwrite existing canonical config, or treat this as migration/reset. If preflight/helper is unavailable, stop the update and retain the old runtime. For existing invalid config, stop writers and explicitly repair/recover it while preserving damaged bytes; init cannot reset it.

Existing L with v1-shaped JSON remains readable by the old Go reader, which ignores additive unknown fields. The old wizard loses unknown fields and must not run concurrently with the current settings editor. Update all installed runtimes before selecting N/E paths; keep one canonical configuration file shared by those runtimes.

For support, share only `config inspect --json`, never `cat` of config; keep saved diagnostics private and review paths before posting. The response includes selection (path/source/exists/diagnostics), revision, schemaVersion, valid, optional errorCode, and safe settings: desktopEnabled, desktopSound, volume and known-status enabled/desktopEnabled/webhookEnabled. It omits free-form sounds, webhook secrets/payloads and unknown fields.

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

| Option | Default | Description |
|--------|---------|-------------|
| `notifyOnSubagentStop` | `false` | Send notifications when subagents (Task tool) complete. Has no effect unless `suppressForSubagents` is also set to `false`. |
| `suppressForSubagents` | `true` | Suppress subagent (`SubagentStop`) notifications, plus any `Stop` notification whose transcript is a subagent/teammate transcript. Detection uses the hook event for `SubagentStop` (Claude Code passes the parent session `transcript_path` to that hook, so a path check alone can't identify it). Set to `false` together with `notifyOnSubagentStop: true` to get a notification each time a subagent finishes. |
| `notifyOnTextResponse` | `true` | Send notifications for text-only responses (no tool usage) |
| `desktop.showSessionLabel` | `true` | Append the `[name id]` session label to the notification title. |
| `respectJudgeMode` | `true` | Honor `CLAUDE_HOOK_JUDGE_MODE=true` env var to suppress notifications |
| `notifyOnlyWhenUnfocused` | `false` | Skip the desktop notification only when the focused terminal window can be matched to the current Claude Code session. Best-effort per platform; if focus can't be determined the notification is still shown. |
| `notifyDelaySeconds` | `0` | Wait N seconds before delivering a desktop notification (capped at 25s by the hook timeout). With `notifyOnlyWhenUnfocused`, focus is re-checked after the wait. Webhooks are unaffected. |
| `respectDoNotDisturb` | `"off"` | Honour the desktop's Do Not Disturb state. `"silent"` still delivers the banner (so it reaches the notification centre) but skips the plugin's sound; `"suppress"` skips the notification entirely. Linux only for now (KDE Plasma, GNOME, XFCE, dunst); other platforms always report "not in DND". Webhooks are unaffected. See [Do Not Disturb](DO_NOT_DISTURB.md). |
| `respectDisplaySleep` | `false` | Skip the plugin's own sound while every display is asleep; the banner is still delivered. macOS only for now; other platforms always report "not asleep". Independent of `respectDoNotDisturb`. Webhooks are unaffected. |
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

- **`notifyOnlyWhenUnfocused`** - skip the desktop notification only when the focused terminal window can be matched to the current Claude Code session.
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
- Linux: X11 sessions compare `$WINDOWID` to the active window. Wayland or terminals without `$WINDOWID` are treated as unknown.
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

Route notification sounds to a specific audio output device instead of the system default:

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
      "audioDevice": "MacBook Pro-Lautsprecher"
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
