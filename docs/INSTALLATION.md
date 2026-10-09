[Back to README](../README.md)

# Installation

## Gemini availability

The public installer selects **1.47.1 for Linux amd64/arm64 and Windows amd64**,
and **1.46.1 for macOS amd64/arm64**. Gemini CLI is available on Linux/Windows;
macOS keeps Claude, Codex and OpenCode. GitHub Latest remains 1.46.1 for legacy
installers. Rerun setup once to adopt a normal Claude marketplace's platform
channel. [Channel selection and promotion](PLATFORM_RELEASE_CHANNELS.md).

## Local installer preview (macOS OpenCode)

From a clean checkout, open a terminal and run:

```bash
bash scripts/preview-installer.sh
```

This builds the current source into a private reusable TEST cache and previews the
real installer with a fresh HOME. Select OpenCode, then review and confirm the
plan. Terminal UI settings are inherited; use `--plain` for line prompts.
Python 3.9+, Go and macOS signing tools are required. `gh` downloads the latest
signed public helper and verifies its published checksum; alternatively pass
`--native-zip /path/to/ClaudeNotifier.app.zip`. `--binary /path/to/binary` skips
building only when the binary's source provenance matches clean HEAD.

Only a version-only OpenCode TEST adapter is available. No agent, account or
provider is launched; this does not qualify notification delivery or all products.
Profiles and native app registrations are retained at the printed path. Keep that
directory until its TEST registration has been inspected and safely removed.
Use `--state-dir /path/to/private-TEST-cache` to reuse your own mode-700 cache.

## Prerequisites

- Claude, Codex CLI and/or OpenCode for stable setup (published OpenCode support is tested with 1.18.33; published V2 support is not declared). The dual-API candidate targets 1.18.33, 2.0.0 and 2.0.21; final platform qualification and publication remain pending. See [OpenCode candidate setup and limits](opencode-notifications.md), including the stock Windows V1 original-event-age limitation. Gemini CLI 0.62.0 is the tested host on Linux/Windows.
- `curl` and Bash
- **Windows users:** Git Bash (included with [Git for Windows](https://git-scm.com/download/win)). Do not use WSL for a native Windows installation.
- Python remains optional only for iTerm2 exact tab/pane targeting.

### Quick Install (Recommended)

Prefer a guided setup? [Open the installation guide](https://777genius.github.io/agent-notifications/#install) to choose your agents, OS and task.

The short setup loader handles release lookup and validation internally, then downloads the installer from its qualified immutable source snapshot. Run it and choose Claude, Codex, Claude + Codex, or OpenCode:

```bash
curl -fsSL https://agent-notifications.com/install.sh | bash
```

> Windows users: open Git Bash from the Start menu and run this command there. Do not run the `curl ... | bash` command from PowerShell or Windows Terminal if `bash` opens WSL, because that targets Linux paths and binaries instead of Windows.

For automation or terminals without a controlling TTY, choose explicitly and preserve download failures in the exit status:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --product codex)
```

Use `claude`, `codex`, or `both` for Claude/Codex. For OpenCode, use `--product opencode --desktop`, `--webhook`, or both channel flags (explicit consent required). The selected host CLI must already be on `PATH`; this installs notifications only.

You can select any combination of the three agents in the guided setup. Mixed selections containing OpenCode use one loader command:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --products claude,codex,opencode --desktop)
```

Use `claude,opencode` or `codex,opencode` for two agents. The loader downloads and validates one installer, then runs Claude/Codex setup followed by OpenCode setup. It stops at the first failure; an earlier successful installation remains installed. Fix the reported error and rerun the same command to complete setup.

`--desktop` and `--webhook` grant consent only for OpenCode; choose at least one, and configure webhook URLs separately. `--skip-agent-notify` applies only to the Claude/Codex notification tool. These flags do not change Claude/Codex notification channels. Single-agent commands and `--product both` remain supported.

After installation:

- **Claude:** restart Claude. Optionally run `/claude-notifications-go:settings` to configure sounds.
- **Codex:** start Codex, run `/hooks`, then review and trust the installed hooks. The installer registers them automatically; no JSON editing or manual registration command is needed. Trust approval remains yours.
- **Both:** complete both steps above.
- **OpenCode:** restart OpenCode to load its global plugin. On Mac, explicitly grant notification permission. OpenCode bootstrap requires release v1.46.0 or newer; see [OpenCode setup, channels and limits](opencode-notifications.md).

When the selected release supports the portable wizard, the installer also registers the
`agent-notify` MCP server and `agent-notifications` skill for the selected clients. Restart each selected client
and open a new session before checking its MCP tools. The agent can call `notify` during a
task, not only after a Stop hook. Check the read-only `notification_status` first:
when `navigation.capability` is `eligible`, prefer `navigation: "required"` to keep
the configured return to the chat, including for informational notifications.
Eligibility uses this call's client context and configuration; it does not verify
that the app, profile or chat can actually open. Use `navigation: "none"` only for
an alert that does not need a return to the chat, including setups without a desktop
route. Never silently downgrade a request that requires a return to the chat.
`notify` returns `submitted` when the OS accepted the request, which does not prove
that a banner was visible or the chat opened.
Desktop notification permission and a visible test send must be checked separately.

The installer reports incomplete setup separately from successful hooks. Keep its retry
command and selected profile if MCP or skill setup fails; rerun the installer or its
`setup-notifications wizard` repair action after the cause is resolved. `--skip-agent-notify`
is an explicit hooks-only choice and does not remove an existing MCP installation.
Automatic updates preserve an absent MCP client when another selected client already has a binding.
Use an explicit `--agent-notify` request to add that client later.
An interrupted installation made by an older UAP release may have a schema 3 directory
journal without ownership proof. A newer installer stops with `recovery_required` and
preserves its files for manual inspection; do not delete that journal to force a retry.

Codex requires a published stable plugin release v1.42.0 or newer. The installer downloads matching source and binaries, respects `CODEX_HOME`, and keeps a permanent runtime copy there. It reports an error if no supported release is published yet.

> If installation fails, use [manual Claude installation](#manual-install) or [manual Codex registration](CODEX.md#manual-codex-registration), depending on the product.

### Manual Install

<details>
<summary>Step-by-step installation inside Claude (if bootstrap doesn't work)</summary>

Run these slash commands in the Claude chat, not in your system terminal:

```text
# 1) Add marketplace
/plugin marketplace add 777genius/agent-notifications
# 2) Install plugin
/plugin install claude-notifications-go@claude-notifications-go
# 3) Restart Claude
# 4) Download binary
/claude-notifications-go:init
# 5) (Optional) Configure sounds and settings
/claude-notifications-go:settings
```

> **Compatibility:** `claude-notifications-go` is the frozen Claude marketplace,
> plugin, and command namespace. The public product is **Agent Notifications**, but changing
> these technical identifiers breaks existing installations and updates. See
> [Claude plugin identity compatibility](CLAUDE_PLUGIN_IDENTITY.md).

</details>

> Having issues with installation? See [Troubleshooting](troubleshooting.md).

### Updating

Run the [secure install command](#quick-install-recommended) again and choose the product(s) you want to update.

For OpenCode, rerun with explicitly chosen desktop/webhook flags; the idempotent install action updates the registered runtime. Restart OpenCode. For Claude, restart Claude. For Codex, restart Codex and inspect `/hooks`; changed hook definitions may need trust approval again. The installer refreshes the Codex runtime and registration automatically. Existing foreign hooks and shared settings in the file selected by `config path` are preserved.

<details>
<summary>Manual Claude update (if bootstrap didn't work)</summary>

Claude also periodically checks for plugin updates automatically. Binaries are updated on the next hook invocation when a version mismatch is detected.

To update manually via Claude UI:

1. Run `/plugin`, select **Marketplaces**, choose `claude-notifications-go`, then select **Update marketplace**
2. Select **Installed**, choose `claude-notifications-go`, then select **Update now**

If the binary auto-update didn't work (e.g. no internet at the time), run `/claude-notifications-go:init` to download it manually. If hook definitions changed in the new version, restart Claude to apply them.

</details>

### Uninstalling

**OpenCode:** use the installer's printed **Remove** command, then restart OpenCode. On Windows it runs a temporary executable copy so the managed `.exe` can be deleted; on macOS/Linux it invokes the installed executable directly. Removal revokes its consent before deleting owned files and preserves other consumers. [Detailed removal/recovery commands](opencode-notifications.md#change-channels-remove-or-recover).

**Claude:**

```text
/plugin uninstall claude-notifications-go@claude-notifications-go
```

Optionally also remove the marketplace registration: `/plugin marketplace remove claude-notifications-go`.

**Codex:** remove the hooks and runtime registered by this installer manually. Use the same Codex home selected during setup: the explicit `--codex-home` path, otherwise `CODEX_HOME`, otherwise `~/.codex` (`%USERPROFILE%\.codex` on Windows). In that directory, delete only this installer's entries from `hooks.json`, then remove the `claude-notifications-go` directory. Preserve hooks registered for other tools.

**Configuration:** uninstalling does not delete your saved settings. Run `agent-notifications config path` to find the active file, and remove it yourself if you no longer want it.

### Reading the installation result

The installer ends with a summary for the selected agents. Installed means that
registration succeeded; it does not confirm notification delivery. Restart the
agents, review and trust Codex entries with `/hooks`, and send a test notification.
If a notification does not arrive, check OS permissions and notification settings.
Skipped agent-notify setup is reported separately from installed automatic hooks.
For detailed download and setup diagnostics, prefix the final installer command
with `BOOTSTRAP_VERBOSE=1`. Failed stages always print their diagnostics.
