[Back to README](../README.md)

# Installation

### Prerequisites

- Claude Code and/or Codex CLI for the products you select
- Python **3.6 or newer**, available as the `python3` command on PATH, is required for installer metadata and checksum validation. Check with `python3 --version`.
- **Windows users:** Git Bash (included with [Git for Windows](https://git-scm.com/download/win)) and native Windows Python available as `python3` from Git Bash. A `python` or `py` command alone is insufficient; use native Python, not WSL Python.
- **macOS/Linux users:** Ensure `python3` is installed and available in the shell running the installer.

### Quick Install (Recommended)

Prefer a guided setup? [Open the installation guide](https://777genius.github.io/agent-notifications/#install) to choose your agent, OS and task.

The command below resolves the latest stable release to its immutable commit SHA, then downloads both installer scripts from that commit. The interactive menu asks you to choose:

```bash
(
  set -euo pipefail
  repo=777genius/agent-notifications
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | python3 -I -c 'import json,re,sys; v=json.load(sys.stdin).get("tag_name",""); re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)",v) or sys.exit("Invalid stable release tag"); sys.stdout.buffer.write((v+"\n").encode("ascii"))')
  commit=$(curl -fsSL "https://api.github.com/repos/$repo/commits/$tag" | python3 -I -c 'import json,re,sys; v=json.load(sys.stdin).get("sha",""); re.fullmatch(r"[0-9a-f]{40}",v) or sys.exit("Invalid release commit"); sys.stdout.buffer.write((v+"\n").encode("ascii"))')
  raw="https://raw.githubusercontent.com/$repo/$commit/bin"
  curl -fsSL "$raw/bootstrap.sh" | env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" INSTALL_SCRIPT_URL="$raw/install.sh" bash
)
```

> Windows users: open Git Bash from the Start menu and run this command there. Do not run the `curl ... | bash` command from PowerShell or Windows Terminal if `bash` opens WSL, because that targets Linux paths and binaries instead of Windows.

For automation or terminals without a controlling TTY, choose explicitly:

```bash
(
  set -euo pipefail
  repo=777genius/agent-notifications
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | python3 -I -c 'import json,re,sys; v=json.load(sys.stdin).get("tag_name",""); re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)",v) or sys.exit("Invalid stable release tag"); sys.stdout.buffer.write((v+"\n").encode("ascii"))')
  commit=$(curl -fsSL "https://api.github.com/repos/$repo/commits/$tag" | python3 -I -c 'import json,re,sys; v=json.load(sys.stdin).get("sha",""); re.fullmatch(r"[0-9a-f]{40}",v) or sys.exit("Invalid release commit"); sys.stdout.buffer.write((v+"\n").encode("ascii"))')
  raw="https://raw.githubusercontent.com/$repo/$commit/bin"
  curl -fsSL "$raw/bootstrap.sh" | env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" INSTALL_SCRIPT_URL="$raw/install.sh" bash -s -- --product codex
)
```

Use `claude`, `codex`, or `both`. This installs the notifications plugin; the selected Claude Code / Codex CLI must already be on `PATH`.

After installation:

- **Claude:** restart Claude Code. Optionally run `/claude-notifications-go:settings` to configure sounds.
- **Codex:** start Codex, run `/hooks`, then review and trust the installed hooks. The installer registers them automatically; no JSON editing or manual registration command is needed. Trust approval remains yours.
- **Both:** complete both steps above.

Codex requires a published stable plugin release v1.42.0 or newer. The installer downloads matching source and binaries, respects `CODEX_HOME`, and keeps a permanent runtime copy there. It reports an error if no supported release is published yet.

> If installation fails, use [manual Claude installation](#manual-install) or [manual Codex registration](CODEX.md#manual-codex-registration), depending on the product.

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
> [Claude plugin identity compatibility](CLAUDE_PLUGIN_IDENTITY.md).

</details>

> Having issues with installation? See [Troubleshooting](troubleshooting.md).

### Updating

Run the [secure install command](#quick-install-recommended) again and choose the product(s) you want to update.

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

**Codex:** remove the hooks and runtime registered by this installer manually. Use the same Codex home selected during setup: the explicit `--codex-home` path, otherwise `CODEX_HOME`, otherwise `~/.codex` (`%USERPROFILE%\.codex` on Windows). In that directory, delete only this installer's entries from `hooks.json`, then remove the `claude-notifications-go` directory. Preserve hooks registered for other tools.

**Configuration:** uninstalling does not delete your saved settings. Run `agent-notifications config path` to find the active file, and remove it yourself if you no longer want it.
