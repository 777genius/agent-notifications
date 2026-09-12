<h1 align="center"><a href="https://777genius.github.io/agent-notifications/">Agent Notifications</a></h1>

[![Ubuntu CI](https://github.com/777genius/agent-notifications/workflows/Ubuntu%20CI/badge.svg)](https://github.com/777genius/agent-notifications/actions)
[![macOS CI](https://github.com/777genius/agent-notifications/workflows/macOS%20CI/badge.svg)](https://github.com/777genius/agent-notifications/actions)
[![Windows CI](https://github.com/777genius/agent-notifications/workflows/Windows%20CI/badge.svg)](https://github.com/777genius/agent-notifications/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/777genius/agent-notifications)](https://goreportcard.com/report/github.com/777genius/agent-notifications)
[![codecov](https://codecov.io/gh/777genius/agent-notifications/branch/main/graph/badge.svg)](https://codecov.io/gh/777genius/agent-notifications)

<div>
<table>
  <tr>
    <td align="center"><img width="250" height="350" alt="image" src="https://github.com/user-attachments/assets/e7aa6d8e-5d28-48f7-bafe-ad696857b938" /></td>
    <td align="center"><img width="350" alt="image" src="https://i.imgur.com/Nrt6dEo.png" /></td>
    <td align="center"><img width="220" alt="image" src="https://github.com/user-attachments/assets/4b5929d8-1a51-4a15-a3d5-dda5482554cc" /></td>
  </tr>
</table>
</div>

Notifications for Claude Code and Codex CLI (beta), with sounds, git branch display, and webhook integrations. See [Codex support and limitations](#codex-cli-support-beta) for differences between products.

> **Boost your productivity** — check out the [advanced task manager for Claude with a convenient UI](https://github.com/777genius/claude_agent_teams_ui), from the creator of this plugin.

## Table of Contents

  - [Features](#features)
  - [Installation](#installation)
    - [Prerequisites](#prerequisites)
    - [Quick Install (Recommended)](#quick-install-recommended)
    - [Manual Install](#manual-install)
    - [Updating](#updating)
    - [Uninstalling](#uninstalling)
  - [Supported Notification Types](#supported-notification-types)
  - [Codex CLI Support (beta)](#codex-cli-support-beta)
  - [Platform Support](#platform-support)
    - [Click-to-Focus (macOS & Linux)](#click-to-focus-macos--linux)
  - [Configuration](#configuration)
    - [Manual Configuration](#manual-configuration)
    - [Sound Options](#sound-options)
    - [Test Sound Playback](#test-sound-playback)
  - [Manual Testing](#manual-testing)
  - [Contributing](#contributing)
  - [Troubleshooting](#troubleshooting)
  - [Documentation](#documentation)
  - [License](#license)

## Features

- **Cross-platform**: macOS (Intel & Apple Silicon), Linux (x64 & ARM64), Windows 10+ (x64)
- **Claude notification types**: Task Complete, Review Complete, Question, Plan Ready, Session Limit, API Error
- **Click-to-focus** (macOS, Linux): click notification to focus the exact project window and tab — Ghostty, VS Code, iTerm2, Warp (`WARP_FOCUS_URL`), kitty, WezTerm, Alacritty, Hyper, Apple Terminal, GNOME Terminal, Konsole, Tilix, Terminator, XFCE4 Terminal, MATE Terminal. Windows focuses the originating window; Warp on Windows can still select the pane via `WARP_FOCUS_URL`.
- **Multiplexers**: tmux (including iTerm2 -CC integration mode), zellij, WezTerm, kitty — click switches to the correct session/pane/tab
- **Git branch in title**: `✅ Completed main [cat]`
- **Sounds**: MP3/WAV/FLAC/OGG/AIFF, volume control, audio device selection
- **Webhooks**: Slack, Discord, Telegram, Lark/Feishu, Microsoft Teams, ntfy.sh, PagerDuty, Zapier, n8n, Make, custom — with retry, circuit breaker, rate limiting ([docs](docs/webhooks/README.md))
- **[Plugin compatibility](docs/PLUGIN_COMPATIBILITY.md)**: works with [double-shot-latte](https://github.com/obra/double-shot-latte) and other plugins that spawn background Claude instances

## Installation

### Prerequisites

- Claude Code and/or Codex CLI for the products you select
- Python **3.6 or newer**, available as the `python3` command on PATH, is required for installer metadata and checksum validation. Check with `python3 --version`.
- **Windows users:** Git Bash (included with [Git for Windows](https://git-scm.com/download/win)) and native Windows Python available as `python3` from Git Bash. A `python` or `py` command alone is insufficient; use native Python, not WSL Python.
- **macOS/Linux users:** Ensure `python3` is installed and available in the shell running the installer.

### Quick Install (Recommended)

Prefer a guided setup? [Open the installation guide](https://777genius.github.io/agent-notifications/#install) to choose your agent, OS and task.

One command to install or update the notifications plugin for Claude Code, Codex, or both. The interactive menu asks you to choose:

```bash
curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/bootstrap.sh | bash
```

> Windows users: open Git Bash from the Start menu and run this command there. Do not run the `curl ... | bash` command from PowerShell or Windows Terminal if `bash` opens WSL, because that targets Linux paths and binaries instead of Windows.

For automation or terminals without a controlling TTY, choose explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/bootstrap.sh | bash -s -- --product codex
```

Use `claude`, `codex`, or `both`. This installs the notifications plugin; the selected Claude Code / Codex CLI must already be on `PATH`.

After installation:

- **Claude:** restart Claude Code. Optionally run `/claude-notifications-go:settings` to configure sounds.
- **Codex:** start Codex, run `/hooks`, then review and trust the installed hooks. The installer registers them automatically; no JSON editing or manual registration command is needed. Trust approval remains yours.
- **Both:** complete both steps above.

Codex requires a published stable plugin release v1.42.0 or newer. The installer downloads matching source and binaries, respects `CODEX_HOME`, and keeps a permanent runtime copy there. It reports an error if no supported release is published yet.

> If installation fails, use [manual Claude installation](#manual-install) or [manual Codex registration](#manual-codex-registration), depending on the product.

### Manual Install

<details>
<summary>Step-by-step installation inside Claude Code (if bootstrap doesn't work)</summary>

Run these slash commands in the Claude Code chat, not in your system terminal:

```text
# 1) Add marketplace
/plugin marketplace add 777genius/agent-notifications
# 2) Install plugin
/plugin install claude-notifications-go@claude-notifications-go
# 3) Restart Claude Code
# 4) Download binary
/claude-notifications-go:init
# 5) (Optional) Configure sounds and settings
/claude-notifications-go:settings
```

> **Compatibility:** `claude-notifications-go` is the frozen Claude Code marketplace,
> plugin, and command namespace. The public product is **Agent Notifications**, but changing
> these technical identifiers breaks existing installations and updates. See
> [Claude plugin identity compatibility](docs/CLAUDE_PLUGIN_IDENTITY.md).

</details>

> Having issues with installation? See [Troubleshooting](#troubleshooting).

### Updating

Run the same command and choose the product(s) you want to update:

```bash
curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/bootstrap.sh | bash
```

For Claude, restart Claude Code. For Codex, restart Codex and inspect `/hooks`; changed hook definitions may need trust approval again. The installer refreshes the Codex runtime and registration automatically. Existing foreign hooks and shared settings in the file selected by `config path` are preserved.

<details>
<summary>Manual Claude update (if bootstrap didn't work)</summary>

Claude Code also periodically checks for plugin updates automatically. Binaries are updated on the next hook invocation when a version mismatch is detected.

To update manually via Claude Code UI:

1. Run `/plugin`, select **Marketplaces**, choose `claude-notifications-go`, then select **Update marketplace**
2. Select **Installed**, choose `claude-notifications-go`, then select **Update now**

If the binary auto-update didn't work (e.g. no internet at the time), run `/claude-notifications-go:init` to download it manually. If hook definitions changed in the new version, restart Claude Code to apply them.

</details>

### Uninstalling

**Claude:**

```text
/plugin uninstall claude-notifications-go@claude-notifications-go
```

Optionally also remove the marketplace registration: `/plugin marketplace remove claude-notifications-go`.

**Codex:** Codex has no plugin manager, so removal is manual. Delete the hook entries this installer added from `~/.codex/hooks.json` (`%USERPROFILE%\.codex\hooks.json` on Windows), then remove the installed copy at `~/.codex/claude-notifications-go` (`%USERPROFILE%\.codex\claude-notifications-go` on Windows). This does not touch hooks you registered yourself for other tools.

**Configuration:** uninstalling does not delete your saved settings. Run `agent-notifications config path` to find the active file, and remove it yourself if you no longer want it.

## Supported Notification Types

The Claude triggers are listed below. Codex uses a different event mapping, described in [Codex support](#codex-cli-support-beta).

| Status | Icon | Description | Trigger |
|--------|------|-------------|---------|
| Task Complete | ✅ | Main task completed | Stop/SubagentStop hooks (state machine detects active tools like Write/Edit/Bash, or ExitPlanMode followed by tool usage) |
| Review Complete | 🔍 | Code review finished | Stop/SubagentStop hooks (state machine detects only read-like tools: Read/Grep/Glob with no active tools, plus long text response >200 chars) |
| Question | ❓ | Claude has a question | PreToolUse hook (AskUserQuestion) OR Notification hook |
| Plan Ready | 📋 | Plan ready for approval | PreToolUse hook (ExitPlanMode) |
| Session Limit Reached | ⏱️ | Session limit reached | Stop/SubagentStop hooks (state machine detects "Session limit reached" text in last 3 assistant messages) |
| API Error | 🔴 | Authentication expired, rate limit, server error, connection error | Stop/SubagentStop hooks (state machine detects via `isApiErrorMessage` flag + `error` field from JSONL) |
| Permission Request | 🔐 | Codex is waiting for tool approval | Codex `PermissionRequest` hook (Codex only) |

## Codex CLI Support (beta)

The same binary can notify for OpenAI Codex CLI sessions.

### Setup

Use the [one-command installer](#quick-install-recommended) and choose Codex or both.
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
the hook entries into `~/.codex/hooks.json`. Agent-initiated notify (MCP) is enabled by default
with `--navigation none --allow-unknown-caller true --allow-caller-asserted false`. Pass `--skip-agent-notify` for hooks only. Existing foreign hook
definitions and unknown fields are preserved, and every run saves a uniquely named backup of
the previous file next to it.

Then start Codex, run `/hooks`, review the entries and trust them. Open a new session so MCP
can start; `/mcp` should list `agent_notifications`.

Useful flags: `--dry-run` shows what would change, `--print` outputs the JSON so you can merge it
yourself, `--codex-home` and `--plugin-root` override the paths, `--skip-agent-notify` skips MCP
registration. If agent-notify setup fails, hook registration remains in place.

For manual updates, run the registration command again to refresh the installed copy.
Unchanged hook definitions retain trust; changed definitions require review again.
The one-command installer handles this registration step automatically.

Claude Code installation and updates continue to use the [existing installation steps](#installation).
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
- **Question payloads (experimental)** - if Codex emits `PreToolUse` for `request_user_input`,
  the plugin delivers the question/header text. Options, ids, and secret fields are excluded.
  Live firing of this tool hook is not yet qualified; do not rely on it for every question.
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
- Windows support for the Codex route is not declared until the Windows launcher is proven.
- Codex hooks require a trust review (`/hooks` inside Codex); changed definitions require review again.

Both products share one config file (the shared file selected by `config path`).

## Platform Support

**Supported platforms:**
- macOS (Intel & Apple Silicon)
- Linux (x64 & ARM64)
- Windows 10+ (x64)

**No additional dependencies:**
- ✅ Binaries auto-download from GitHub Releases
- ✅ Pure Go - no C compiler needed
- ✅ All libraries bundled
- ✅ Works offline after first setup

**Windows-specific features:**
- Native Toast notifications (Windows 10+)
- After installation, notifications work in PowerShell, CMD, Git Bash, or WSL
- MP3/WAV/OGG/FLAC audio playback via native Windows APIs
- System sounds not accessible - use built-in MP3s or custom files

### Click-to-Focus (macOS & Linux)

Clicking a notification activates your terminal window. Auto-detects terminal and platform.

**macOS** — via AX API with bundle ID detection:

| Terminal | Focus method |
|----------|-------------|
| Ghostty | Exact tab focus via Ghostty AppleScript, with AXDocument fallback |
| VS Code / Insiders / Cursor | AXTitle (focus-window subcommand) |
| iTerm2 | Exact tab/pane targeting via iTerm2 Python API when available, otherwise app-level iTerm activation |
| Warp | Exact window/tab/pane via `WARP_FOCUS_URL` (`warp://session/<uuid>`), AXTitle fallback on older Warp |
| kitty, WezTerm, Alacritty, Hyper, Apple Terminal | AXTitle (focus-window subcommand) |
| Any other (custom `terminalBundleId`) | AXTitle (focus-window subcommand) |

**Linux** — via D-Bus daemon with automatic compositor detection:

| Terminal | Supported compositors |
|----------|----------------------|
| VS Code | GNOME, KDE, Sway, X11 |
| Warp | GNOME, KDE, Sway, X11 — exact pane via `WARP_FOCUS_URL` |
| GNOME Terminal, Konsole, Alacritty, kitty, WezTerm, Tilix, Terminator, XFCE4 Terminal, MATE Terminal | GNOME, KDE, Sway, X11 |
| Any other | Fallback by name |

Linux focus methods (tried in order): Warp session URL (`xdg-open`), GNOME extension, GNOME Shell Eval, GNOME FocusApp, wlrctl (Sway/wlroots), kdotool (KDE), xdotool (X11).

**Multiplexers** (both platforms): tmux (including iTerm2 -CC integration mode), zellij, WezTerm, kitty — click switches to the correct pane/tab.

**iTerm2 note:** to open the exact iTerm2 tab or split pane, enable `iTerm2 > Settings > General > Magic > Enable Python API`. If you just toggled it, restart iTerm2 once. Without the Python API, the plugin falls back to app-level iTerm activation instead of exact tab targeting.

**Windows** — clicking a notification raises the originating terminal **window** (Windows Terminal, VS Code, conhost, …) via a protocol-activated toast. Window-level only: tab/split-pane targeting isn't possible (one window hosts all tabs), and picking among multiple WT windows in one process is best-effort. See the guide for details.

See **[Click-to-Focus Guide](docs/CLICK_TO_FOCUS.md)** for configuration details.

## Configuration

The following workflow requires the coordinated config-capable runtime and installer; older releases may not provide these commands. Do not use a legacy full-file writer as a fallback.

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

Use the [settings recipe](commands/settings.md) for `config edit --stdin --expect-revision TOKEN`: private input, only requested JSON Pointer leaf edits, raw values, and an explicit user decision after any conflict. A volume plus one status sound edit preserves every other raw field, status, channel, webhook secret/payload and future-agent setting. Inspect is a safe projection, not a replacement document; absent free-form values are not unset.

Before any updater deletes/refreshes cache, personalized or unknown historical cache-only/custom-root settings require explicit import using a verified new helper: stop old writers, inspect the selected destination, then `config init --from FILE` only if that destination is missing. Never auto-copy a bundle, guess the newest cache, overwrite existing canonical config, or treat this as migration/reset. If preflight/helper is unavailable, stop the update and retain the old runtime. For existing invalid config, stop writers and explicitly repair/recover it while preserving damaged bytes; init cannot reset it.

Existing L with v1-shaped JSON remains readable by the old Go reader, which ignores additive unknown fields. The old wizard loses unknown fields and is an unsupported concurrent writer. N/E require a bridge-aware runtime, or explicit stopped-writer recovery to a single legacy canonical file with a private backup and all contexts switched; two live copies are not a workaround. No downloadable bridge release is promised here. Native Windows replacement/ACL, macOS/Linux crash/concurrency and OS E2E qualification remain release gates, not results established by this documentation patch.

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


## Manual Testing

The plugin is invoked automatically by Claude Code hooks. To test manually:

```bash
# Test PreToolUse hook
echo '{"session_id":"test","transcript_path":"/path/to/transcript.jsonl","tool_name":"ExitPlanMode"}' | \
  agent-notifications handle-hook PreToolUse

# Test Stop hook
echo '{"session_id":"test","transcript_path":"/path/to/transcript.jsonl"}' | \
  agent-notifications handle-hook Stop
```

## Contributing

See **[CONTRIBUTING.md](CONTRIBUTING.md)** for development setup, testing, building, and submitting changes.
For local plugin workflows and real-`claude` smoke/manual E2E testing, see **[docs/LOCAL_DEVELOPMENT.md](docs/LOCAL_DEVELOPMENT.md)**.

## Troubleshooting

See **[Troubleshooting Guide](docs/troubleshooting.md)** for common issues:

- **Ubuntu 24.04**: `EXDEV: cross-device link not permitted` during `/plugin install` (TMPDIR workaround)
- **Windows**: install issues related to `%TEMP%` / `%TMP%` location
- **Windows / Git Bash**: GitHub Releases download fails because of proxy / TLS inspection / certificate revocation

## Documentation

- **[Architecture](docs/ARCHITECTURE.md)** - Plugin architecture, directory structure, data flow

- **[Local Development And E2E](docs/LOCAL_DEVELOPMENT.md)** - Local marketplace testing, real Claude smoke tests, manual click-to-focus validation

- **[Click-to-Focus](docs/CLICK_TO_FOCUS.md)** - Configuration, supported terminals, platform details

- **[Volume Control Guide](docs/volume-control.md)** - Customize notification volume
  - Configure volume from 0% to 100%
  - Logarithmic scaling for natural sound
  - Per-environment recommendations

- **[Interactive Sound Preview](docs/interactive-sound-preview.md)** - Preview sounds during setup
  - Interactive sound selection
  - Preview before choosing

- **[Plugin Compatibility](docs/PLUGIN_COMPATIBILITY.md)** - Integration with other Claude Code plugins

- **[Troubleshooting](docs/troubleshooting.md)** - Common install/runtime issues
  - Ubuntu 24.04 `EXDEV` during `/plugin install` (TMPDIR workaround)

- **[Webhook Integration Guide](docs/webhooks/README.md)** - Complete guide for webhook setup
  - **[Slack](docs/webhooks/slack.md)** - Slack integration with color-coded attachments
  - **[Discord](docs/webhooks/discord.md)** - Discord integration with rich embeds
  - **[Telegram](docs/webhooks/telegram.md)** - Telegram bot integration
  - **[Lark/Feishu](docs/webhooks/lark.md)** - Lark/Feishu integration with interactive cards
  - **[Custom Webhooks](docs/webhooks/custom.md)** - Any webhook-compatible service
  - **[Configuration](docs/webhooks/configuration.md)** - Retry, circuit breaker, rate limiting
  - **[Monitoring](docs/webhooks/monitoring.md)** - Metrics and debugging
  - **[Troubleshooting](docs/webhooks/troubleshooting.md)** - Common issues and solutions

## License

GPL-3.0 - See [LICENSE](LICENSE) file for details.

For per-agent sound and notification settings, see [shared configuration and schema 2 overrides](docs/AGENT_CONFIGURATION.md).
The primary command is `agent-notifications`; `claude-notifications` remains a permanent compatibility alias.
