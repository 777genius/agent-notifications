# Cursor CLI notifications

Agent Notifications supports managed Cursor CLI Stop notifications through the public Universal Agent Plugins (UAP) SDK. Install, update, disable and uninstall preserve unrelated hooks. Cursor IDE is a separate product and is not covered by this integration.

The common product selection is available in [platform release v1.50.0](PLATFORM_RELEASE_CHANNELS.md). Earlier platform channels can retain the separate Cursor setup contract from their release notes.

## Qualified hosts

| Platform | Tested Cursor CLI version |
| --- | --- |
| Linux amd64 | `2026.09.28-64d2043` |
| macOS arm64 | `2026.09.18-9a7762b` |
| Windows amd64 | `2026.10.01-e373342` |

The installer validates the selected user profile and actual CLI executable; it does not restrict the CLI version. The versions above record historical qualification, not an admission allowlist. macOS Intel, Linux arm64, Windows arm64 and WSL are not qualified. Linux also requires the qualified native filesystem identity API; the CI kernel/filesystem evidence does not establish support for every Linux installation. Unsupported hosts stop before activation.

## Install or update

Install the official Cursor CLI first and make its agent launcher available on `PATH`. Run the common installer and select **Cursor CLI**, alone or alongside other agents:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash)
```

For an explicit combined selection:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- \
  --products codex,qoder,cursor --desktop)
```

Read the common confirmation, including the Cursor profile and CLI path, then restart Cursor CLI after setup. The normal profile is `~/.cursor`, whose `hooks.json` the CLI reads. For another profile, pass `--scope-root` and `--client-executable` together as clean absolute paths; these override discovery for Cursor and do not change sibling agents' profiles. Use the agent CLI launcher rather than the Cursor editor executable. On Windows amd64, run the installer in Git Bash and use native absolute Windows paths for overrides.

Run the same command to update. Fresh installs default to desktop on and webhooks off. Omitting channel flags during update preserves saved consent, including both channels off. `--desktop` selects desktop only, `--webhook` selects webhooks only, and both select both channels. `--desktop=false --webhook=false` disables delivery while retaining registration. Webhooks require a separately configured destination; installing with `--webhook` does not create one. Do not change an unrelated hook to point to Agent Notifications manually.

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

Pass the exact `--control-root`, `--runtime-root` and `--global-config` paths reported by setup when they differ from your defaults. To disable notifications while retaining registration, use the installed wizard:

```bash
"$AN" setup-notifications wizard --action install --agents cursor \
  --hooks false --agent-notify false --scope-root "$CURSOR_PROFILE" \
  --client-executable "$CURSOR_CLI" --yes --json
```

An empty interactive channel selection cancels setup; it does not disable notifications. An update without channel flags preserves disabled consent. Uninstall removes only owned integration entries and runtime references, preserving foreign hook entries and other clients.

[General installation and prerequisites](INSTALLATION.md) · [Configuration](CONFIGURATION.md) · [Platform release channels](PLATFORM_RELEASE_CHANNELS.md)
