[Back to README](../README.md)

# Platform Support

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

See **[Click-to-Focus Guide](CLICK_TO_FOCUS.md)** for configuration details.
