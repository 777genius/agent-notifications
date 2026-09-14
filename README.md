<p align="center">
  <a href="https://777genius.github.io/agent-notifications/"><img src="brand/agent-notifications-logo-transparent.png" width="148" alt="Agent Notifications logo" /></a>
</p>
<h1 align="center"><a href="https://777genius.github.io/agent-notifications/">Agent Notifications</a></h1>

[![Ubuntu CI](https://github.com/777genius/agent-notifications/workflows/Ubuntu%20CI/badge.svg)](https://github.com/777genius/agent-notifications/actions)
[![macOS CI](https://github.com/777genius/agent-notifications/workflows/macOS%20CI/badge.svg)](https://github.com/777genius/agent-notifications/actions)
[![Windows CI](https://github.com/777genius/agent-notifications/workflows/Windows%20CI/badge.svg)](https://github.com/777genius/agent-notifications/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/777genius/agent-notifications.svg)](https://pkg.go.dev/github.com/777genius/agent-notifications)
[![codecov](https://codecov.io/gh/777genius/agent-notifications/graph/badge.svg?branch=main)](https://codecov.io/gh/777genius/agent-notifications)

<div>
<table>
  <tr>
    <td align="center"><img width="250" height="350" alt="image" src="https://github.com/user-attachments/assets/e7aa6d8e-5d28-48f7-bafe-ad696857b938" /></td>
    <td align="center"><img width="350" alt="image" src="https://i.imgur.com/Nrt6dEo.png" /></td>
    <td align="center"><img width="220" alt="image" src="https://github.com/user-attachments/assets/4b5929d8-1a51-4a15-a3d5-dda5482554cc" /></td>
  </tr>
</table>
</div>

Desktop notifications and sounds for **Claude Code and Codex CLI**. Know when a task finishes, an agent needs input, or a tool needs approval. Click a notification to return to work.

## Features

- **Task and attention alerts:** completions, reviews, questions, plans, session limits and API errors for Claude; completions and permission requests for Codex, with opt-in subagent alerts. [Event details](docs/NOTIFICATION_TYPES.md)
- **Click-to-focus:** return to the originating terminal or editor, with exact tab/pane targeting for supported integrations including Ghostty, iTerm2, Warp, tmux, kitty and WezTerm. [Supported terminals](docs/CLICK_TO_FOCUS.md)
- **Useful context:** project, git branch and session labels in notifications.
- **Custom sounds:** built-in or custom MP3, WAV, FLAC, OGG and AIFF, with volume control, previews and audio output selection.
- **Less noise:** focus-aware delivery, optional delay, duplicate-question suppression and filters by status, branch or folder.
- **Your settings per agent:** shared configuration with separate Claude and Codex overrides; control desktop and webhook delivery per status. [Agent settings](docs/AGENT_CONFIGURATION.md)
- **Webhooks:** Slack, Discord, Telegram, Lark/Feishu and custom endpoints, including Teams, ntfy, PagerDuty, Zapier, n8n and Make. Retries, rate limits and circuit breakers are built in. [Integrations](docs/webhooks/README.md)
- **Cross-platform:** macOS (Intel/Apple Silicon), Linux (x64/ARM64) and Windows 10+ (x64). [Platform details](docs/PLATFORMS.md)

[Codex setup and event behavior](docs/CODEX.md)

## Install Or Update

Requires Claude Code and/or Codex CLI, plus **Python 3.6+** available as `python3`. On Windows, use **Git Bash with native Windows Python**.

```bash
set -euo pipefail
repo=777genius/agent-notifications
tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | python3 -I -c 'import json,re,sys; v=json.load(sys.stdin).get("tag_name",""); re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)",v) or sys.exit("Invalid stable release tag"); sys.stdout.buffer.write((v+"\n").encode("ascii"))')
commit=$(curl -fsSL "https://api.github.com/repos/$repo/commits/$tag" | python3 -I -c 'import json,re,sys; v=json.load(sys.stdin).get("sha",""); re.fullmatch(r"[0-9a-f]{40}",v) or sys.exit("Invalid release commit"); sys.stdout.buffer.write((v+"\n").encode("ascii"))')
raw="https://raw.githubusercontent.com/$repo/$commit/bin"
curl -fsSL "$raw/bootstrap.sh" | env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" INSTALL_SCRIPT_URL="$raw/install.sh" bash
```

The command resolves the latest stable release to its immutable commit before executing either installer script. Choose **Claude**, **Codex**, or **both**. For non-interactive setup, append `-s -- --product claude`, `codex`, or `both` after the final `bash`.

- **Claude:** restart Claude Code.
- **Codex:** restart Codex, open `/hooks`, then review and trust the installed hooks.

Run the same command to update. [Guided installer](https://777genius.github.io/agent-notifications/#install) · [Manual installation, updates and removal](docs/INSTALLATION.md)

## Settings

In Claude Code, run `/claude-notifications-go:settings` for the configuration wizard or `/claude-notifications-go:sounds` to browse and preview sounds.

The primary CLI is `agent-notifications`. Use `agent-notifications config path` to locate your settings and `agent-notifications config inspect --json` to inspect them safely. The `claude-notifications` alias and Claude slash-command namespace remain compatible with existing installations.

[Configuration reference](docs/CONFIGURATION.md) · [Per-agent overrides](docs/AGENT_CONFIGURATION.md) · [Sound previews](docs/interactive-sound-preview.md)

## Documentation

- [Troubleshooting](docs/troubleshooting.md)
- [Plugin compatibility](docs/PLUGIN_COMPATIBILITY.md)
- [Architecture](docs/ARCHITECTURE.md) and [local development](docs/LOCAL_DEVELOPMENT.md)
- [Contributing](CONTRIBUTING.md) and [changelog](CHANGELOG.md)

GPL-3.0. See [LICENSE](LICENSE).
