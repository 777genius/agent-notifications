# Cursor CLI notifications

Agent Notifications 1.49.1 adds managed Cursor CLI Stop notifications through the public Universal Agent Plugins (UAP) SDK. Install, update, disable and uninstall preserve unrelated hooks. Cursor IDE is a separate product and is not covered by this integration.

## Qualified hosts

| Platform | Required Cursor CLI version |
| --- | --- |
| Linux amd64 | `2026.09.28-64d2043` |
| macOS arm64 | `2026.09.18-9a7762b` |
| Windows amd64 | `2026.10.01-e373342` |

The installer requires the exact qualified version, an explicit user profile and the actual CLI executable. macOS Intel, Linux arm64, Windows arm64, WSL and other CLI versions are not qualified. Linux also requires the qualified native filesystem identity API; the CI kernel/filesystem evidence does not establish support for every Linux installation. Unsupported hosts stop before activation.

## Install or update

Install the matching official Cursor CLI first. Select the user profile whose `hooks.json` Cursor reads; the usual directory is `~/.cursor`. Use the CLI launcher, not the Cursor editor executable.

On macOS arm64 or Linux amd64, with `cursor-agent` available on `PATH`:

```bash
CURSOR_CLI=$(command -v cursor-agent) || exit 1
CURSOR_PROFILE="$HOME/.cursor"
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- \
  --product cursor --scope-root "$CURSOR_PROFILE" \
  --client-executable "$CURSOR_CLI" --desktop)
```

On Windows amd64, use Git Bash with **native absolute Windows paths**. Replace these examples with the actual profile and official `cursor-agent.cmd` launcher:

```bash
CURSOR_CLI='C:/Users/YOUR_USER/AppData/Local/cursor-agent/cursor-agent.cmd'
CURSOR_PROFILE='C:/Users/YOUR_USER/.cursor'
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- \
  --product cursor --scope-root "$CURSOR_PROFILE" \
  --client-executable "$CURSOR_CLI" --desktop)
```

Read the installer confirmation and restart Cursor CLI after setup. Cursor must be installed separately; do not combine it with other agents in `--products`. The generic installer menu does not infer a trusted Cursor profile for you.

Run the same command to update. Omitting channel flags during update preserves saved consent. `--desktop` selects desktop only, `--webhook` selects webhooks only, and both select both channels. Webhooks require a separately configured destination; installing with `--webhook` does not create one. Do not change an existing unrelated hook to point to Agent Notifications manually.

## Delivery and limits

A native `stop` event causes a fixed informational alert: **Cursor CLI is stopping**. A stopped agent loop does not establish successful task completion. Unknown status values are suppressed; the notification does not classify questions, plans, permissions or errors.

Cursor delivery is silent and informational: no sounds, click-to-focus, focus-aware suppression or task-text analysis is promised. Desktop and webhook consent are separate. Same-generation duplicates are suppressed. Linux installed E2E observed notification-service delivery; macOS arm64 included a visible notification; Windows CI observed the installed child's successful Toast API submission, not a visible interactive banner.

## Inspect, disable and remove

Use the installed executable and control root reported by setup. Keep the same explicit profile and CLI paths. For example, where `AN` is the installed executable:

```bash
"$AN" setup-notifications wizard --action inspect --agents cursor \
  --scope-root "$CURSOR_PROFILE" --client-executable "$CURSOR_CLI" --json
"$AN" setup-notifications wizard --action uninstall --agents cursor \
  --hooks false --agent-notify true --scope-root "$CURSOR_PROFILE" \
  --client-executable "$CURSOR_CLI" --yes --json
```

If setup reported a custom control root, pass that exact `--control-root` too. To disable notifications while retaining registration, rerun the installation command with explicit false values and confirm its plan:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- \
  --product cursor --scope-root "$CURSOR_PROFILE" \
  --client-executable "$CURSOR_CLI" --desktop=false --webhook=false)
```

An empty interactive channel selection cancels setup; it does not disable notifications. An update without channel flags preserves disabled consent. Uninstall removes only owned integration entries and runtime references, preserving foreign hook entries and other clients.

[General installation and prerequisites](INSTALLATION.md) · [Configuration](CONFIGURATION.md) · [Platform release channels](PLATFORM_RELEASE_CHANNELS.md)
