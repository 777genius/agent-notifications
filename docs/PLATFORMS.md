[Back to README](../README.md)

# Platform Support

**Supported platforms:**
- macOS (Intel & Apple Silicon)
- Linux (x64 & ARM64)
- Windows 10+ (x64)

**Self-contained runtime after setup:**
- ✅ Binaries auto-download from GitHub Releases
- ✅ Pure Go - no C compiler needed
- ✅ Core Go notification libraries bundled; guarded Linux/X11 terminal focus needs the host tools listed in the [focus guide](CLICK_TO_FOCUS.md#guarded-local-cli-routing-qoder-gemini-cli-cursor-cli)
- ✅ Works offline after first setup

**Windows-specific features:**
- Native Toast notifications (Windows 10+)
- After installation, notifications work in PowerShell, CMD, Git Bash, or WSL
- MP3/WAV/OGG/FLAC audio playback via native Windows APIs
- System sounds not accessible - use built-in MP3s or custom files

### Click-to-Focus (agent- and OS-specific)

Clicking an eligible notification can activate the originating terminal window or pane, depending on agent, OS and terminal. **Qoder CLI, Gemini CLI and Cursor CLI** use a separate guarded **local Linux/X11 window-only opt-in** with explicit schema-2 consent and verified origin. Wayland, macOS, Windows and unverified sessions remain informational-only for these three agents. Shared strict X11 daemon/click mechanics were tested with a Go fixture; authenticated installed CLI-to-click E2E remains unqualified. [Consent and limits](CLICK_TO_FOCUS.md#guarded-local-cli-routing-qoder-gemini-cli-cursor-cli).

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
| JetBrains IDEs (IntelliJ IDEA, PhpStorm, WebStorm, PyCharm, GoLand, …) | GNOME, KDE, Sway, X11 — the IDE window, not the terminal tab |
| Any other | Fallback by name |

Linux focus methods (tried in order): Warp session URL (`xdg-open`), GNOME extension, GNOME Shell Eval, GNOME FocusApp, wlrctl (Sway/wlroots), kdotool (KDE), xdotool (X11).

**Multiplexers:** tmux and kitty (macOS only), zellij and WezTerm (macOS & Linux) — click switches to the correct pane/tab. Linux zellij targets the exact pane; see the [Click-to-Focus Guide](CLICK_TO_FOCUS.md#multiplexers) for the full per-platform breakdown.

**iTerm2 note:** to open the exact iTerm2 tab or split pane, enable `iTerm2 > Settings > General > Magic > Enable Python API`. If you just toggled it, restart iTerm2 once. Without the Python API, the plugin falls back to app-level iTerm activation instead of exact tab targeting.

**Windows (legacy qualified agents only)** — clicking a notification raises the originating terminal **window** (Windows Terminal, VS Code, conhost, …) through a protocol-activated toast. This does **not** add focus for Qoder/Gemini/Cursor. Window-only: tabs/splits are not targetable, and selection among multiple WT windows in one process is best-effort. See the guide.

See **[Click-to-Focus Guide](CLICK_TO_FOCUS.md)** for configuration details.
