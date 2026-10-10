[Back to README](../README.md)

# Installation

## Gemini availability

The public installer selects a release from the [platform channels](PLATFORM_RELEASE_CHANNELS.md). Gemini CLI is available on Linux amd64/arm64 and Windows amd64; macOS amd64/arm64 includes Claude, Codex and OpenCode. Rerun setup once to adopt a normal Claude marketplace's platform channel.

## Local installer preview (macOS)

From a clean checkout, open a terminal and run:

```bash
make preview-installer
# Optional line prompts and a custom reusable cache:
make preview-installer PREVIEW_ARGS='--plain --state-dir /path/to/private-TEST-cache'
# Use an explicitly installed Gemini CLI in the TEST profile:
make preview-installer PREVIEW_ARGS='--gemini-cli /absolute/path/to/gemini'
```

This builds the committed source and its real portable package into a private
reusable TEST cache, then runs the installer with fresh HOME, working directory
and client profiles. It discovers the actual Claude, Codex, OpenCode and Gemini
executables before creating the private PATH. Missing CLIs remain absent; an
unsupported installed version is reported by the real installer. The optional
`--gemini-cli` override selects a real executable for this preview only.
Native Claude installations use a private local copy of the installed executable
to avoid launch delays on external disks. SHA256 and the macOS signature are
verified; logs retain both the installed path and the TEST execution path.
Select the clients, then review and confirm the plan. Use `--plain` for line prompts.

Python 3.9+, Go and macOS signing/sandbox tools are required. `gh` downloads the
latest signed public helper and verifies its published checksum; alternatively
pass `--native-zip /path/to/ClaudeNotifier.app.zip`. `--binary /path/to/binary`
skips the main build only when its supported source provenance matches clean HEAD.
Go still builds optional sound/device utilities from the exact committed source
and builds the portable package from that source and the verified binary.

Real CLI proxies permit only version checks and the installer's exact local
plugin registration/listing commands. They deny CLI network access, Keychain and
host credential/profile reads. Git acquisition of this repository is redirected
to a private bare snapshot of the same commit; finite release downloads resolve
to the candidate binary, native helper, real portable zip, optional utilities
and checksum manifest.
Unexpected downloads and CLI commands fail. The native installer runs outside
the CLI sandbox so it can perform its normal macOS app registration.

This previews installation only. It does not launch an agent/model session or
verify notification delivery or release qualification. Source proof, acquisition
and real CLI command logs remain in the printed TEST directory, along with all
profiles and native app registrations. Keep that directory until its TEST
registration has been inspected and safely removed. No automatic deletion occurs.
Use `--state-dir /path/to/private-TEST-cache` to reuse your own mode-700 cache.

## Prerequisites

- Install the selected agent CLIs first. OpenCode accepts stable V1 >= 1.18.29 and V2 >= 2.0.0; this does not qualify every version in that range. See [OpenCode setup and limits](opencode-notifications.md), including the stock Windows V1 original-event-age limitation. Gemini requires exactly CLI 0.62.0 on Linux/Windows; other versions are rejected.
- `curl`, Bash, and a working `python3` or `node` on `PATH` for installer metadata and checksum validation. A Windows Store Python stub is not sufficient.
- Git for Claude marketplace setup, `tar` for Codex bundles, and `unzip` for the macOS desktop helper.
- **Windows users:** Git Bash (included with [Git for Windows](https://git-scm.com/download/win)). Do not use WSL for a native Windows installation.
- Python is additionally needed for optional iTerm2 exact tab/pane targeting; Node.js can satisfy the installer requirement but cannot replace the iTerm2 Python API.

### Quick Install (Recommended)

Prefer a guided setup? [Open the installation guide](https://777genius.github.io/agent-notifications/#install) to choose your agents, OS and task.

The short setup loader handles release lookup and validation internally, then downloads the installer from its qualified immutable source snapshot. Run it and choose Claude, Codex, OpenCode, Gemini CLI (Linux/Windows), or a combination:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash)
```

> Windows users: open Git Bash from the Start menu and run this command there. Do not run the `curl ... | bash` command from PowerShell or Windows Terminal if `bash` opens WSL, because that targets Linux paths and binaries instead of Windows.

For automation or terminals without a controlling TTY, choose explicitly and preserve download failures in the exit status:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --product codex)
```

Use `claude`, `codex`, or `both` for Claude/Codex. For OpenCode, use `--product opencode --desktop`, `--webhook`, or both channel flags to choose channels explicitly. The selected host CLI must already be on `PATH`; this installs notifications only.

You can select any combination of the supported agents in the guided setup. For Claude, Codex and OpenCode, use one loader command:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --products claude,codex,opencode --desktop)
```

Use `claude,opencode` or `codex,opencode` for two agents. The loader downloads and validates one installer, then runs Claude/Codex setup followed by OpenCode setup. It stops at the first failure; an earlier successful installation remains installed. Fix the reported error and rerun the same command to complete setup.

Add `,gemini` to `--products` on Linux/Windows to include Gemini. `--desktop` and `--webhook` apply to each selected OpenCode/Gemini integration independently; configure webhook URLs separately. The public installer defaults fresh OpenCode/Gemini installations to desktop on and webhooks off, and preserve saved channels on update when flags are omitted. `--skip-agent-notify` applies only to the Claude/Codex notification tool. These flags do not change Claude/Codex notification channels. Single-agent commands and `--product both` remain supported.

After installation:

- **Claude:** restart Claude. Optionally run `/claude-notifications-go:settings` to configure sounds.
- **Codex:** start Codex, run `/hooks`, then review and trust the installed hooks. The installer registers them automatically; no JSON editing or manual registration command is needed. Trust approval remains yours.
- **Both:** complete both steps above.
- **Gemini CLI (Linux/Windows):** restart Gemini to reload its hooks. [Setup and limits](gemini-notifications.md).
- **OpenCode:** restart OpenCode to load its global plugin. On Mac, explicitly grant notification permission. OpenCode bootstrap requires release v1.46.0 or newer; see [OpenCode setup, channels and limits](opencode-notifications.md).

When the selected release supports the portable wizard, the installer also registers the
`agent-notify` MCP server and `agent-notifications` skill for the selected Claude/Codex clients. Restart each selected client
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
Automatic setup keeps absent MCP clients off when an existing managed portable installation or MCP binding is detected. Skipping setup alone does not record a permanent per-client opt-out.
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

### Recover an entirely absent installer consumer

Ordinary install and removal remain strict about every managed asset. For an
abandoned secondary runtime, use the exact consumer ID, physical runtime root,
installation ID and positive generation from that installation's ownership
record. A missing registration alone does not establish an orphan. Keep retained
assets intact and preview first:

```sh
claude-notifications internal-install-runtime --recover-orphan-consumer \
  --consumer 'codex:/absolute/TEST/codex/hooks.json' \
  --runtime-root /absolute/TEST/codex/claude-notifications-go \
  --control-root /absolute/TEST/config/agent-notifications \
  --expected-installation-id INSTALLATION_ID --expected-generation 18 \
  --dry-run --json
```

After inspecting the bounded result, repeat the same command without `--dry-run`.
The transaction removes only that consumer's absent runtime file ownership; it
preserves retained consumers and policy, and never deletes or recreates payload
or the separately recorded sibling registration. Any surviving registration,
changed retained asset, stale generation or unsupported ownership is a conflict.
Use `setup-notifications status --control-root ABS --json` before and after.
Preview and status are read-only and do not execute native qualification probes.
Recovery has a three-minute deadline and cannot combine with ordinary installer
flags, native purge, refresh or relocation. There is no force/hash bypass.

An interrupted transaction is a separate explicit operation:

```sh
claude-notifications internal-install-runtime --recover-pending --control-root ABS --json
claude-notifications internal-install-runtime --rollback-pending --control-root ABS --json
```

Select one pending operation; do not add orphan selection, stage or target flags.
Pending modes require an existing private managed control root, valid ownership
and an existing transaction journal and permanent locks. An installation with no
pending journal is refused without creating locks or changing its generation.
The durable journal supplies its own before/after fences, so these modes do not
accept an orphan generation or consumer. Rollback restores ownership metadata,
not missing payload, and can leave status invalid. If replay detects reappearance
or retained corruption, preserve the marker and inspect the conflict; never
remove the marker manually or combine pending replay with a new orphan decision.
