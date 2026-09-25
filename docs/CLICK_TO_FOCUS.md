# Click-to-Focus

Use [shared OS path selection](CONFIGURATION.md#manual-configuration) and the [revision-checked settings recipe](../commands/settings.md). Examples show fields, not replacement documents. Keep unchanged by default, submit only requested leaf edits through private stdin, and ask for an explicit decision after CAS conflicts. Safe inspect omits free-form values; omission is not an instruction to reset them.

Clicking a notification activates your terminal window — no more hunting for the right window.

## Supported Terminals & IDEs

Quick reference before the platform-by-platform detail below. Precision levels: **pane/tab** targets the exact split or tab; **window** raises the whole app window (may land on the wrong tab if several are open); **best-effort** falls back to a generic name/title match that can pick the wrong window.

| App | <img src="assets/platforms/apple.svg" width="14" height="14" alt=""/> macOS | <img src="assets/platforms/linux.svg" width="14" height="14" alt=""/> Linux | <img src="assets/platforms/windows.svg" width="14" height="14" alt=""/> Windows | Notes |
|---|---|---|---|---|
| <img src="assets/terminals/ghostty.svg" width="16" height="16" alt=""/> **Ghostty** | ✅ Tab (AppleScript), window via AXDocument fallback | ⚠️ Window (mapped in the daemon, not listed in the compositor table above) | ❌ | |
| <img src="assets/terminals/iterm2.svg" width="16" height="16" alt=""/> **iTerm2** | ✅ Tab/pane (Python API), otherwise app-level activation | ❌ | ❌ | Requires enabling iTerm2's Python API, then a restart |
| <img src="assets/terminals/warp.svg" width="16" height="16" alt=""/> **Warp** | ✅ Window/tab/pane via `WARP_FOCUS_URL` | ✅ Pane via `WARP_FOCUS_URL` | ✅ Pane via `WARP_FOCUS_URL` | No Accessibility/Screen Recording needed — Warp resolves its own window, tab, and pane |
| <img src="assets/terminals/kitty.svg" width="16" height="16" alt=""/> **kitty** | ✅ Pane with remote control (`KITTY_LISTEN_ON`), window otherwise | ✅ Window | ❌ | Exact pane targeting needs kitty's remote control enabled |
| <img src="assets/terminals/wezterm.svg" width="16" height="16" alt=""/> **WezTerm** | ✅ Pane via `WEZTERM_PANE` | ✅ Pane via `WEZTERM_PANE` | ❌ | |
| <img src="assets/terminals/vscode.svg" width="16" height="16" alt=""/> **VS Code** / Insiders | ✅ Window | ✅ Window | ✅ Window | |
| <img src="assets/terminals/cursor.svg" width="16" height="16" alt=""/> **Cursor** | ✅ Window | ⚠️ Falls back to generic window matching | ✅ Window | |
| <img src="assets/terminals/jetbrains.svg" width="16" height="16" alt=""/> **JetBrains IDEs** (IntelliJ IDEA, PhpStorm, WebStorm, PyCharm, GoLand, etc.) | ✅ Window | ✅ Window (the IDE window, not the terminal tab) | ✅ Window | Linux: detected via the IDE's process tree and `product-info.json` window class — see [JetBrains IDEs](#jetbrains-ides) below |
| <img src="assets/terminals/generic-terminal.svg" width="16" height="16" alt=""/> **Apple Terminal** | ✅ Window | — | — | Also the macOS fallback when no other terminal is detected |
| <img src="assets/terminals/alacritty.svg" width="16" height="16" alt=""/> **Alacritty** | ✅ Window | ✅ Window | ❌ | |
| <img src="assets/terminals/hyper.svg" width="16" height="16" alt=""/> **Hyper** | ✅ Window | ⚠️ Falls back to generic window matching | ❌ | |
| <img src="assets/terminals/gnome-terminal.svg" width="16" height="16" alt=""/> **GNOME Terminal** | — | ✅ Window | — | |
| <img src="assets/terminals/konsole.svg" width="16" height="16" alt=""/> **Konsole** | — | ✅ Window | — | |
| <img src="assets/terminals/tilix.svg" width="16" height="16" alt=""/> **Tilix** | — | ✅ Window | — | |
| <img src="assets/terminals/terminator.svg" width="16" height="16" alt=""/> **Terminator** | — | ✅ Window (exact title via `remotinator`) | — | |
| <img src="assets/terminals/xfce4-terminal.svg" width="16" height="16" alt=""/> **XFCE4 Terminal** | — | ✅ Window | — | |
| <img src="assets/terminals/mate-terminal.png" width="16" height="16" alt=""/> **MATE Terminal** | — | ✅ Window | — | |
| <img src="assets/terminals/windows-terminal.png" width="16" height="16" alt=""/> **Windows Terminal** | — | — | ✅ Window | Tab/split-pane targeting isn't possible — there is no public Windows API for it |
| <img src="assets/terminals/powershell.svg" width="16" height="16" alt=""/> **conhost** (cmd.exe, PowerShell, legacy Git Bash) | — | — | ✅ Window | |
| <img src="assets/terminals/conemu.png" width="16" height="16" alt=""/> **ConEmu** | — | — | ✅ Window | |
| <img src="assets/terminals/tmux.svg" width="16" height="16" alt=""/> **tmux** | ✅ Pane/window | ❌ | ❌ | Also works inside iTerm2 `-CC` control mode via the Python API |
| <img src="assets/terminals/zellij.svg" width="16" height="16" alt=""/> **zellij** | ✅ Tab | ✅ Pane (exact, via `$ZELLIJ_PANE_ID`) | ❌ | See [Multiplexers](#multiplexers) for `zellijFocus` tuning |
| Any other app | ✅ Window, via custom `terminalBundleId` | Fallback by name | ✅ Window | |

Legend: ✅ supported · ⚠️ best-effort / not officially documented · ❌ not supported · — not applicable on this OS.
Logo sources and licenses are listed in [`assets/terminals/NOTICE`](assets/terminals/NOTICE).

## Configuration

In the shared file selected by `config path`:

```json
{
  "notifications": {
    "desktop": {
      "clickToFocus": true,
      "terminalBundleId": ""
    }
  }
}
```

| Option | Default | Description |
|--------|---------|-------------|
| `clickToFocus` | `true` | Enable click-to-focus on macOS and Linux |
| `terminalBundleId` | `""` | macOS only: override auto-detected terminal. Use bundle ID like `com.googlecode.iterm2` |

## macOS

Auto-detects your terminal via `TERM_PROGRAM` / `__CFBundleIdentifier`. Uses `terminal-notifier` (auto-installed via `/claude-notifications-go:init`).

| Terminal | Focus method |
|----------|-------------|
| Ghostty | Exact tab focus via Ghostty AppleScript, with AXDocument retry fallback |
| VS Code / Insiders / Cursor | AXTitle via focus-window subcommand |
| iTerm2 | Exact tab/pane targeting via iTerm2 Python API when available, otherwise app-level iTerm activation |
| Warp | Exact window/tab/pane via `WARP_FOCUS_URL` (`open warp://session/<uuid>`) when the hook ran inside Warp, with AXTitle `focus-window` fallback on older Warp. Cursor/VS Code launched from Warp keep editor focus. |
| kitty, WezTerm, Alacritty, Hyper, Apple Terminal | AXTitle via focus-window subcommand |
| Any other (custom `terminalBundleId`) | AXTitle via focus-window subcommand |

To find your terminal's bundle ID: `osascript -e 'id of app "YourTerminal"'`

### Permissions

Warp session deep links (`WARP_FOCUS_URL`) do not need Accessibility or Screen Recording — Warp handles window/tab/pane focus itself.

All other terminals with click-to-focus may require up to two permissions for window-level focus:

- **Accessibility** — to enumerate and raise the correct window via the AX API
- **Screen Recording** — to read window titles across Spaces (macOS 10.15+)

Screen Recording is requested automatically via system prompt on first use.
Accessibility is prompted via a one-time notification with a link to System Settings.

Without these permissions, clicking a notification still activates the terminal app,
but raises whichever window was last active rather than the project-specific one.

## Linux

Uses a background D-Bus daemon. Auto-detects terminal and compositor.

| Terminal | Supported compositors |
|----------|----------------------|
| VS Code | GNOME, KDE, Sway, X11 |
| Warp | GNOME, KDE, Sway, X11 — exact pane via `WARP_FOCUS_URL` |
| GNOME Terminal, Konsole, Alacritty, kitty, WezTerm, Tilix, Terminator, XFCE4 Terminal, MATE Terminal | GNOME, KDE, Sway, X11 |
| JetBrains IDEs (IntelliJ IDEA, PhpStorm, WebStorm, PyCharm, GoLand, …) | GNOME, KDE, Sway, X11 — the IDE window, not the terminal tab |
| Any other | Fallback by name |

Focus methods (tried in order):

1. **Warp**: `xdg-open` of `$WARP_FOCUS_URL` (`warp://session/<uuid>`) when the hook ran inside Warp
2. **GNOME**: `activate-window-by-title` extension, Shell Eval, FocusApp (GNOME 45+)
3. **Sway / wlroots**: `wlrctl`
4. **KDE Plasma**: `kdotool`
5. **X11** (XFCE, MATE, Cinnamon, i3, bspwm): `xdotool`

Falls back to standard notifications if no focus tool is available.

### JetBrains IDEs

Works in the built-in terminal of JetBrains IDEs. The plugin finds the IDE in the process tree and reads its window class from the IDE's `product-info.json`.

- A click raises the window of the project the session runs in. The project name is `.idea/.name` if set, else the name of the folder that holds `.idea/`.
- The terminal tab inside the IDE isn't selected: JetBrains has no external API for it.
- KDE Plasma (`kdotool`) and X11 (`xdotool`) raise the window whose title starts with the project name. If none matches, they raise another window of the IDE.
- Two open projects with the same name: JetBrains adds each project's path to its title, and the plugin picks the window with this session's path.
- GNOME (`activate-window-by-title` extension) matches the project name anywhere in a window title. Sway (`wlrctl`) raises a window of the IDE, not always the project's.

### Diagnostics

If Linux click-to-focus focuses the wrong window, run the diagnostic script immediately after reproducing the failed click:

```bash
curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/scripts/linux-focus-debug.sh | bash
```

It writes a report file in the current directory with:

- session type and terminal environment variables
- available focus tools (`xdotool`, `wmctrl`, `remotinator`, etc.)
- current window information and window lists
- installed plugin metadata and recent `notification-debug.log` lines

Review the file before sharing it publicly, because it may include local paths and window titles.

## Multiplexers

Clicking a notification switches to the correct session/pane/tab, on top of raising the window.
Inside Warp, the Warp session URL is opened first to raise the originating window/tab, then
the supported multiplexer target is selected.

| Multiplexer | macOS | Linux |
|-------------|-------|-------|
| tmux | ✅ | — |
| zellij | ✅ (active tab) | ✅ (exact pane) |
| WezTerm | ✅ | ✅ |
| kitty | ✅ | — |

Where a multiplexer is unsupported the window is still raised; only the pane/tab switch is skipped.

On Linux, zellij is targeted by pane rather than by tab name: the hook records `$ZELLIJ_SESSION_NAME`
and `$ZELLIJ_PANE_ID`, and the click runs `zellij -s <session> action focus-pane-id <pane>`. That is
exact where a tab name is not — tab names need not be unique, and the tab that is *focused* when the
notification fires is whichever one you switched to, not the one Claude is running in.

Pane targeting is used only where it can actually work: the session has to export `$ZELLIJ_PANE_ID`
for the action to name, and the installed zellij has to accept `focus-pane-id`. Either one missing
falls back to `go-to-tab-name`, so no zellij version loses behaviour it previously had. Both are
settled when the notification is sent, and the second is settled by asking the installed zellij
whether it accepts the subcommand — not by comparing version numbers, though for reference the
subcommand arrived in 0.44.1. macOS always uses `go-to-tab-name`.

Override the choice with `zellijFocus` in `~/.config/agent-notifications/config.json`:

```json
{
  "notifications": {
    "desktop": {
      "zellijFocus": "auto"
    }
  }
}
```

| Value | Behaviour |
|-------|-----------|
| `auto` (default) | `pane` when the session exports a pane ID and the installed zellij accepts `focus-pane-id`, otherwise `tab` |
| `pane` | Always target the pane; where there is no pane ID, or zellij rejects the subcommand, the window is raised with no switch |
| `tab` | Always use the legacy tab-name path |
| `off` | Raise the window only, never touch zellij |

The setting is read on Linux; the macOS click path always uses the tab name.

The `tab` path is approximate by nature, which is why it is only the fallback. Tab names are not
unique, `zellij action rename-tab` can change one after the notification is sent, and a tab holds
many panes — so it lands on whichever pane that tab last had focused, not necessarily the one Claude
is running in.

### iTerm2 + tmux Control Mode (-CC)

When using iTerm2's tmux integration (`tmux -CC`), standard `tmux select-window` doesn't switch iTerm2 tabs. The plugin detects control mode automatically and uses the iTerm2 Python API instead.

**Requirements:**
1. Python 3 installed
2. iTerm2 → Settings → General → Magic → **Enable Python API**
3. iterm2 venv (set up automatically by `bootstrap.sh` / `install.sh`)

**Manual setup** (if automatic setup failed):
```bash
python3 -m venv ~/.claude/claude-notifications-go/iterm2-venv
~/.claude/claude-notifications-go/iterm2-venv/bin/pip install iterm2
```

**Diagnostics:**
```bash
# Show the plugin root path (run inside Claude Code hook context)
echo "$CLAUDE_PLUGIN_ROOT"

# List all iTerm2 tabs with tmux pane mappings
~/.claude/claude-notifications-go/iterm2-venv/bin/python3 \
  "$CLAUDE_PLUGIN_ROOT/scripts/iterm2-select-tab.py" --list
```

If the Python API is not available, the plugin falls back to standard `tmux select-window` (which may not switch iTerm2 tabs in -CC mode). If you just toggled the setting, restart iTerm2 once. For plain iTerm2, the fallback is app-level activation instead of exact tab targeting.

## Windows

Clicking a notification raises the terminal **window** that started the task. Enabled by the same `clickToFocus` flag; no extra configuration. In Warp, the toast also carries `WARP_FOCUS_URL`, and the click handler opens that session deep link first so the originating tab/pane is selected before the generic HWND fallback.

How it works (no admin rights, no COM server):

1. When the notification fires, the plugin walks up the process tree to the terminal window hosting Claude (Windows Terminal, VS Code, conhost, ConEmu, …) and records its window handle, PID, title and project folder. When one process owns several windows (e.g. Windows Terminal's shared "monarch"), it prefers the window whose title contains the project folder name to tell them apart.
2. The toast is shown via [go-toast](https://git.sr.ht/~jackmordaunt/go-toast) with **protocol activation**, carrying that context in a `claude-notify-focus:` URI. A per-user handler for that scheme is registered under `HKCU\Software\Classes` (idempotent; refreshed if the binary moves), pointing at a second, GUI-subsystem build of the same binary (`claude-notifications-windows-amd64-focus.exe`) instead of the normal console-subsystem one — so the click never flashes a console window. If that sibling isn't present (older install), the handler falls back to the main binary.
3. Clicking the toast launches the URI, which re-runs the focus-handler binary's `focus-windows` subcommand. It re-finds the window (by handle, then PID, then title/folder) and raises it with `ShowWindow` + `SetForegroundWindow`.

### Scope: window-level only

Focus is **window-level**. Windows Terminal runs every tab and split pane inside one top-level window, and Win32 can only raise *windows*, not tabs — there is no public API to focus a specific WT tab by session (it's an open feature request on Windows Terminal). So:

- A single terminal window → raised reliably.
- Multiple **separate** windows → best effort (the foreground / most-recent window is chosen); it may not be the exact one when the session is in a background window.
- **Tabs and split panes** inside a window are not individually targetable.

### Terminal bell (tab indicator)

Separately from click-to-focus, the `terminalBell` option (on by default) now works on Windows. A Claude Code hook is spawned with its *own* hidden console (`CREATE_NO_WINDOW`), so writing to its own `CONOUT$` would ring a private, invisible console. Instead the plugin detaches that console and attaches to an ancestor's (`AttachConsole`) — the Claude session's console, which is the visible pane's ConPTY — then writes a BEL byte there. The BEL reaches the **originating** Windows Terminal pane and WT flags that tab's bell indicator.

Unlike click-to-focus, the bell **is** tab-accurate: because the BEL is written into that specific pane's console, Windows Terminal knows exactly which tab to flag — even with multiple tabs or split panes in one window. It's the one reliable per-tab "this session finished" signal.

It is best-effort and never blocks or fails a notification: if no ancestor console can be reached, the write is a silent, debug-logged no-op — the same way the Unix `/dev/tty` path degrades.

**Visibility (Windows Terminal `bellStyle`).** The background-tab bell glyph appears with WT's default settings — no configuration needed. For a more attention-grabbing cue that also flashes the window and taskbar (useful when the session is in a background window), set in WT settings:

```jsonc
// Settings → Profiles → Defaults → Advanced → Bell notification style
"bellStyle": "all"            // or ["audible", "window", "taskbar"]
```

Set `"terminalBell": false` in the plugin config to disable the bell entirely.
