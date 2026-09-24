# Troubleshooting

Common installation and runtime issues.

## `Failed to add marketplace: ... its network source differs from the one declared for it in settings`

### Symptom

Running the installer to update fails during "Setting up marketplace..." with an error
mentioning the marketplace's network source differing from what's declared in settings.

### Why it happens

You installed before the project's GitHub repository was renamed from `claude-notifications-go`
to `agent-notifications`. Claude Code remembers which repo you originally added the marketplace
from and refuses to silently switch it to a different one under the same name — that's a
deliberate safety check, not a bug in Claude Code.

### Fix

The installer (`bin/bootstrap.sh`) detects this specific case and re-registers the marketplace
automatically; your saved notification settings are not affected. If you're still seeing this
error (e.g. from a cached copy of an older installer script), fix it manually and re-run the
installer:

```bash
/plugin marketplace remove claude-notifications-go
```

then run the [installer](INSTALLATION.md#quick-install-recommended) again.

## macOS: VS Code click-to-focus focuses the wrong window

### Symptom

Clicking a notification activates VS Code but raises the wrong window (or the last-active window) instead of the project-specific one.

### Why it happens

VS Code window focus requires **Screen Recording** permission (macOS 10.15+) to read window titles across all Spaces. Without it, the binary falls back to plain app activation.

### Fix

On first use the binary requests Screen Recording access automatically — a macOS dialog will appear. If you dismissed it:

1. Open **System Settings → Privacy & Security → Screen Recording**
2. Enable access for the `claude-notifications` binary (or the terminal running Claude Code)
3. Click the notification again

Once granted, the correct VS Code window will be raised even if it is on a different Space.

## Ubuntu 24.04: `EXDEV: cross-device link not permitted` during `/plugin install`

### Symptom

Plugin installation fails with an error similar to:

```
EXDEV: cross-device link not permitted, rename '.../.claude/plugins/cache/...' -> '/tmp/claude-plugin-temp-...'
```

### Why it happens

Claude Code's plugin installer attempts to move a plugin directory from `~/.claude/...` into `/tmp/...` using `rename()`.
On many Linux systems (including Ubuntu 24.04), `/tmp` is mounted as `tmpfs` (a different filesystem/device), so cross-device `rename()` fails with `EXDEV`.

### Fix (recommended)

Set a temporary directory on the same filesystem as your `~/.claude` (usually under `$HOME`) and start Claude Code from that environment:

```bash
mkdir -p "$HOME/.claude/tmp"
TMPDIR="$HOME/.claude/tmp" claude
```

Then retry:

```text
/plugin install claude-notifications-go@claude-notifications-go
```

### Diagnostics (optional)

```bash
df -T "$HOME" /tmp
mount | grep -E ' on /tmp | on /home '
```

If `/tmp` is `tmpfs` (or otherwise on a different device) and `$HOME` is on `ext4/btrfs/...`, the error is expected without the `TMPDIR` workaround.

## Linux: click-to-focus opens the wrong window

### Symptom

Clicking a notification focuses the wrong terminal window, a stale Terminator window, or does nothing.

### Quick diagnostics

Reproduce the failed click first, then run:

```bash
curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/scripts/linux-focus-debug.sh | bash
```

The script generates a report file in the current directory with:

- `XDG_SESSION_TYPE`, `DISPLAY`, `WAYLAND_DISPLAY`, `TERM_PROGRAM`, `TERMINATOR_UUID`, `WINDOWID`
- installed plugin version/path and marketplace source
- available focus tools like `xdotool`, `wmctrl`, and `remotinator`
- active-window data, `wmctrl` window lists, `xdotool` searches, and recent plugin log lines

Review the file before posting it publicly, because it may include local file paths and window titles.

### Why this helps

Linux click-to-focus behavior depends on the session type, terminal, window manager, and available focus tools. The diagnostic script captures the exact environment needed to explain why the plugin focused the wrong window or could not focus anything at all.

## The notification sound plays even though Do Not Disturb is on

### Symptom

Your desktop is in Do Not Disturb. The banner is correctly held back by the
desktop, but the plugin's sound plays anyway.

### Why it happens

The plugin plays its own audio cue in its own process, rather than asking the
notification server to play one - that is what makes per-status sounds and volume
control work. The notification server is explicitly asked not to play anything
(`suppress-sound`), and it has no say over a separate process's audio.

### Fix

Opt in to DND handling in the shared file selected by `config path`
(`"$NOTIFICATIONS_BIN" config path --json`; see the
[Configuration guide](CONFIGURATION.md)):

```json
{
  "notifications": {
    "respectDoNotDisturb": "silent"
  }
}
```

`"silent"` keeps the banner (it still lands in the notification centre) and drops
the sound; `"suppress"` drops both. The default is `"off"`, which is the old
behaviour.

### If it still plays

Detection deliberately fails open: anything it cannot read counts as "not in
DND", so the notification is delivered. Check, in order:

1. **Platform.** Detection is Linux-only right now. macOS Focus modes and Windows
   Focus Assist are not detected, so the option has no effect there. See
   [DO_NOT_DISTURB.md](DO_NOT_DISTURB.md).
2. **The value is spelled correctly.** An unrecognised value falls back to
   `"off"` and logs `Unknown respectDoNotDisturb value ...` rather than failing
   the config, so a typo looks exactly like the feature being switched off.
3. **Your desktop actually exposes the state.** Run the probe for your daemon:

   ```bash
   gdbus call --session --dest org.freedesktop.Notifications      --object-path /org/freedesktop/Notifications      --method org.freedesktop.DBus.Properties.Get      org.freedesktop.Notifications Inhibited   # KDE Plasma and spec-compliant daemons
   dunstctl is-paused                                            # dunst
   xfconf-query -c xfce4-notifyd -p /do-not-disturb              # XFCE
   gsettings get org.gnome.desktop.notifications show-banners    # GNOME: false means DND
   ```

   If none of these reports DND while your desktop says it is on, the plugin has
   no source to read and will keep delivering.
4. **Which source fired.** With debug logging on, look for a `DND: active (...)`
   line in the plugin log. If the outcome looks right but the source is not the
   one you expect, the detection is wrong even though the result happens to match.

### Related: the terminal bell still rings

`"silent"` mutes the plugin's sound, not the terminal bell - BEL is a tab
indicator on Ghostty, tmux and Windows Terminal, and whether it makes noise is
your terminal's own setting. Set `"terminalBell": false` to turn it off.

## Windows: installer says Linux or installs `linux-amd64`

### Symptom

You run the bootstrap command from Windows Terminal or PowerShell, but the output says `Platform: Linux`, uses paths under `/home/...`, or mentions `claude-notifications-linux-amd64`.

### Why it happens

PowerShell and Windows Terminal can resolve `bash` to WSL. In that case the installer is running inside Linux, not Git Bash, so Linux platform detection is technically correct but not useful for Windows Claude Code.

### Fix

Open Git Bash from the Start menu and run the [secure install command](INSTALLATION.md#quick-install-recommended) there. Do not run it from PowerShell if `bash` opens WSL.

For an intentional WSL installation, add `CLAUDE_NOTIFICATIONS_ALLOW_WSL=1` to the `env` invocation on the final line of that command.

## Windows: install issues related to `%TEMP%` / `%TMP%` location

If your temp directory is on a different drive than your user profile (or where Claude stores plugin cache), you may see similar cross-device move issues.

### Fix

Make sure `%TEMP%` and `%TMP%` point to a directory on the same drive as `%USERPROFILE%` (or where Claude stores its plugin directories), then restart your terminal/app.

## Windows: hooks do not fire or notifications are silent

### Symptom

The plugin is installed and the Windows executable exists, but Claude Code does not show notifications for `Stop`, `ExitPlanMode`, `AskUserQuestion`, or `permission_prompt`. Claude Code debug logs may show hook command failures, or the hook may fail silently.

### Why it happens

The bundled plugin hook configuration uses `bin/hook-wrapper.sh`. On some Windows 11 Claude Code environments, bash/shebang resolution, `${CLAUDE_PLUGIN_ROOT}` expansion, or Unix-only paths like `/dev/tty` are not reliable enough for command hooks.

Claude Code supports PowerShell command hooks on Windows via `"shell": "powershell"`, so the safer Windows workaround is to call the native `.exe` directly with an absolute path.

### Fix

Run the bootstrap installer or `/claude-notifications-go:init` again, then restart Claude Code. On Windows, the installer rewrites the plugin hook file to use PowerShell hooks with an absolute path to the native `.exe`, avoiding the Git Bash/shebang path.

If you need to inspect or apply the configuration manually, generate a PowerShell hook configuration from the installed executable. Replace `<arch>` with `amd64` or `arm64`:

```powershell
.\bin\claude-notifications-windows-<arch>.exe windows-hooks
```

If you downloaded the executable to a different location, pass it explicitly:

```powershell
.\bin\claude-notifications-windows-<arch>.exe windows-hooks --exe "C:\absolute\path\to\claude-notifications-windows-<arch>.exe"
```

To apply it manually, replace the installed plugin's `hooks/hooks.json` with the generated JSON and restart Claude Code. This command only prints JSON - it does not modify files.

### Manual fallback

If you cannot run `windows-hooks`, replace the installed plugin's `hooks/hooks.json` with this block and replace `<absolute-path-to-plugin>` with the actual plugin install directory:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "ExitPlanMode|AskUserQuestion",
        "hooks": [
          {
            "type": "command",
            "command": "$OutputEncoding = [System.Text.UTF8Encoding]::new($false); $input | & \"<absolute-path-to-plugin>\\bin\\claude-notifications-windows-<arch>.exe\" handle-hook PreToolUse",
            "timeout": 30,
            "shell": "powershell"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "permission_prompt",
        "hooks": [
          {
            "type": "command",
            "command": "$OutputEncoding = [System.Text.UTF8Encoding]::new($false); $input | & \"<absolute-path-to-plugin>\\bin\\claude-notifications-windows-<arch>.exe\" handle-hook Notification",
            "timeout": 30,
            "shell": "powershell"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "$OutputEncoding = [System.Text.UTF8Encoding]::new($false); $input | & \"<absolute-path-to-plugin>\\bin\\claude-notifications-windows-<arch>.exe\" handle-hook Stop",
            "timeout": 30,
            "shell": "powershell"
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "$OutputEncoding = [System.Text.UTF8Encoding]::new($false); $input | & \"<absolute-path-to-plugin>\\bin\\claude-notifications-windows-<arch>.exe\" handle-hook SubagentStop",
            "timeout": 30,
            "shell": "powershell"
          }
        ]
      }
    ],
    "TeammateIdle": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "$OutputEncoding = [System.Text.UTF8Encoding]::new($false); $input | & \"<absolute-path-to-plugin>\\bin\\claude-notifications-windows-<arch>.exe\" handle-hook TeammateIdle",
            "timeout": 30,
            "shell": "powershell"
          }
        ]
      }
    ]
  }
}
```

This workaround is based on confirmed Windows 11 behavior from [issue #73](https://github.com/777genius/agent-notifications/issues/73#issuecomment-4364271319).

### Note about beeep logs

If the log contains `beeep.Notify returned a Windows toast false positive after PowerShell fallback`, the COM toast path failed but the PowerShell fallback returned success. Treat it as harmless unless no popup appears.

## Windows / Git Bash: binary download fails from GitHub Releases

### Symptom

Bootstrap or `/claude-notifications-go:init` installs the plugin itself, but downloading `claude-notifications-windows-<arch>.exe` fails with an empty or generic network error.

### Why it happens

`raw.githubusercontent.com` and `github.com` may still work, but release assets are served from GitHub's release CDN. On corporate Windows machines, Git Bash `curl` often fails there because of:

- Proxy authentication or missing proxy environment variables
- TLS inspection with an untrusted corporate root CA
- Schannel certificate revocation checks blocking the request

### What to check

1. If your company requires a proxy, make sure the terminal running Claude Code or bootstrap has `HTTPS_PROXY`, `HTTP_PROXY`, or `ALL_PROXY` configured.
2. If your network inspects TLS traffic, ensure Git Bash `curl` trusts the corporate CA certificate.
3. Retry from another network or from WSL to confirm whether the issue is network-specific.
4. As a fallback, open the latest release page, download the matching `claude-notifications-windows-amd64.exe` or `claude-notifications-windows-arm64.exe`, place it into the plugin `bin` directory, and then re-run `/claude-notifications-go:init`.
