# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.43.0] - 2026-09-11

### Added
- **Warp: exact pane/chat click-to-focus** — notification clicks open Warp's session deep link (`WARP_FOCUS_URL` / `warp://session/<uuid>`) so the originating window, tab, and pane (including the agent chat in that pane) come to the front. Falls back to the previous AXTitle `focus-window` path when the URL is absent or Warp cannot resolve it. Also works on Linux (`xdg-open`) and Windows (toast protocol), and composes with tmux/zellij inside Warp ([#175](https://github.com/777genius/agent-notifications/pull/175)).
- **Webhook agent identity** — custom JSON payloads include `schema_version`, `notification_type`, and `agent_source` (`claude`/`codex`); Slack, Discord, Telegram, and Lark show the agent name; custom templates can use `${{agent_source}}` ([#170](https://github.com/777genius/agent-notifications/pull/170)).
- **Landing: flag images in the language switcher** ([#173](https://github.com/777genius/agent-notifications/pull/173)).

### Changed
- Document the frozen `claude-notifications-go` Claude Code plugin identity ([#166](https://github.com/777genius/agent-notifications/pull/166)).
- Add Uninstall instructions to the README ([#167](https://github.com/777genius/agent-notifications/pull/167)).

### Fixed
- Ignore inherited `WARP_FOCUS_URL` in Cursor/VS Code launched from Warp so notification clicks stay on the editor instead of stealing focus back to Warp ([#175](https://github.com/777genius/agent-notifications/pull/175)).
- Drop the garbage `[unknown .]` prefix when a notification has no usable session name or folder ([#172](https://github.com/777genius/agent-notifications/pull/172)).
- Self-heal a marketplace still declared under the retired GitHub repo name so bootstrap and updates work after the rename to `agent-notifications` ([#174](https://github.com/777genius/agent-notifications/pull/174)).
- Clarify the bootstrap version gate and silence the shared-config baseline traceback ([#171](https://github.com/777genius/agent-notifications/pull/171)).
- Prefix landing flag image paths with the app base URL so they load on GitHub Pages ([#173](https://github.com/777genius/agent-notifications/pull/173)).

## [1.42.0] - 2026-09-11

### Added
- **Universal configuration** - agent-neutral config location for fresh installs, schema 2 per-agent overrides, and the `agent-notifications` CLI alias. Existing Claude config files remain selected without automatic migration; schema 1 remains supported.
- **One-command setup** - choose Claude Code, Codex CLI or both, with automatic hook registration.
- **Codex CLI support (beta)** - register Stop, PermissionRequest, experimental question hooks, and opt-in SubagentStop with `setup-codex`. Review and trust the entries in Codex `/hooks` once. Claude Code is not required.
- **Safe Codex setup and updates** - install a stable runtime copy, preserve unrelated hooks and existing settings, save backups, and retain hook trust when registration is unchanged.
- **`permission_request` notification status** - sound, webhook formatting, and time-sensitive desktop delivery. Approval notifications require Codex to actually request approval.
- **Richer Codex notifications** - classify short failure messages; optionally notify on subagent completion. Question-tool hook firing remains experimental and is not guaranteed in every mode.

### Changed
- **Product and repository rebrand** - renamed the public product to Agent Notifications and the repository to `agent-notifications`; existing plugin IDs, commands, binary and release asset names, environment variables, OS integration IDs, remain unchanged for compatibility. Existing configuration paths are retained; fresh installs use an agent-neutral path.
- **Minimum Go version is now 1.22** (was 1.21), required by the typed hook SDK.
- **Source-neutral event pipeline** - Claude and Codex use separate decoders with a shared notification pipeline. Both products share the selected config file, with optional per-agent overrides; existing webhooks also receive Codex notifications unless overridden.

### Fixed
- Deliver macOS notifications whose text or title is `-help` or `--help` instead of interpreting their content as a CLI help request.
- Preserve the Windows GUI focus helper when installing the stable Codex runtime.
- Keep Codex update stamps separate from Claude, preserve distinct concurrent Codex notifications, reject destructive destination symlinks, and skip Claude iTerm2 setup during Codex installation and updates.

## [1.41.0] - 2026-08-31

### Added
- **Optional session label in notification titles** - new `desktop.showSessionLabel` setting (default `true`) keeps existing behavior; set it to `false` to drop the `[name id]` session label from titles so the branch/folder subtitle gets the space ([#134](https://github.com/777genius/agent-notifications/pull/134))
- **Linux: Alacritty click-to-focus detection** - terminal detection now recognizes `ALACRITTY_WINDOW_ID` (Alacritty deliberately sets no `TERM_PROGRAM`), so notification clicks can raise Alacritty windows instead of doing nothing ([#121](https://github.com/777genius/agent-notifications/pull/121))

### Fixed
- **Ghostty: cwd fallback no longer switches to an arbitrary same-directory tab** - click-to-focus selects a terminal by working directory only when exactly one tab matches, leaving ambiguous sessions to the existing window-level fallback instead ([#115](https://github.com/777genius/agent-notifications/issues/115)).
- **Windows: toast clicks no longer flash a console window and raise the right window across projects** - protocol activation now targets a GUI-subsystem `-focus.exe` sibling binary (no console auto-allocation, with fallback to the main binary on older installs), and window selection prefers the window whose title contains the project folder when one process owns several windows ([#118](https://github.com/777genius/agent-notifications/issues/118), [#120](https://github.com/777genius/agent-notifications/pull/120))
- **Bootstrap: unknown registry versions no longer trigger reinstall loops** - when Claude Code records the installed plugin version as `unknown` or empty, bootstrap verifies the manifest at the recorded install path instead of treating the placeholder as a mismatch; explicit version mismatches and stale or missing manifests still fail closed ([#132](https://github.com/777genius/agent-notifications/issues/132), [#133](https://github.com/777genius/agent-notifications/pull/133))

## [1.40.1] - 2026-07-17

### Changed
- **CI/CD now tests the supported Go range and release artifacts before publishing** - workflows use Go 1.21 and 1.26, run Swift notifier tests, pin current GitHub Actions, validate release metadata, and smoke-test every platform artifact before creating a release.
- **Audio documentation now matches the actual playback backend** - platform and format guidance reflects the current cross-platform audio implementation.

### Fixed
- **Plugin paths resolve without an ambient `CLAUDE_PLUGIN_ROOT`** - configuration defaults use the executable-derived plugin root, preserve literal dollar signs in paths, retain omitted fields in partial status overrides, and keep explicit empty sound settings ([#107](https://github.com/777genius/agent-notifications/issues/107), [#109](https://github.com/777genius/agent-notifications/pull/109)).
- **macOS release binaries retain compatibility with macOS 12 and later** - Darwin builds now set and verify a 12.0 deployment target instead of inheriting the runner OS version.
- **Windows installer guidance consistently points users to Git Bash** - bootstrap and download failures no longer suggest unsupported shell paths.
- **Windows terminal bell fallback handles cyclic process snapshots** - ancestor discovery no longer returns or retries duplicate process IDs.

## [1.40.0] - 2026-06-22

### Added
- **Windows: notification click-to-focus and terminal bell support** - Windows notifications can now bring the originating terminal session back into focus, and terminal bell delivery can ring the originating Windows Terminal tab when supported ([#36](https://github.com/777genius/agent-notifications/issues/36), [#94](https://github.com/777genius/agent-notifications/pull/94))
- **Focus-aware and delayed desktop notifications** - two independent, composable options let notifications model where your attention is. `notifyOnlyWhenUnfocused` (default `false`) skips the desktop notification only when the focused terminal window can be matched to the current Claude Code session; `notifyDelaySeconds` (default `0`, capped at 25s by the hook timeout) waits before delivering and, when paired with `notifyOnlyWhenUnfocused`, re-checks focus after the wait. Focus detection is best-effort per platform (Ghostty/session or title-matched macOS windows, exact X11 window IDs, Windows foreground ancestry plus project-title match) and degrades safely to delivering the notification when focus can't be determined. Both apply to desktop notifications only; webhook delivery is unaffected ([#93](https://github.com/777genius/agent-notifications/issues/93))

### Fixed
- **Windows: bootstrap no longer silently installs Linux binaries from WSL** - the bootstrap and binary installer now detect WSL, stop before touching plugin/download state, and tell Windows users to rerun from Git Bash; intentional WSL installs can opt in with `CLAUDE_NOTIFICATIONS_ALLOW_WSL=1` ([#98](https://github.com/777genius/agent-notifications/issues/98))
- **Hooks: subagent stops are detected from the hook event** - focus-aware notification handling now uses the Claude hook event type instead of inferring subagent stops from `transcript_path`, avoiding unsafe misclassification in edge cases ([#97](https://github.com/777genius/agent-notifications/issues/97))

## [1.39.4] - 2026-06-13

### Fixed
- **Windows: notification sounds now survive toast false-positive errors** - sound playback is independent from `beeep.Notify`, so Windows `doc.LoadXml(tmpl)` false positives no longer drop the audio cue while the toast is delivered ([#90](https://github.com/777genius/agent-notifications/issues/90), [#91](https://github.com/777genius/agent-notifications/pull/91))
- **Windows: toast delivery is more resilient to go-toast COM fallback noise** - Windows beeep calls now pin the OS thread around WinRT/COM notification delivery and downgrade successful PowerShell fallback `doc.LoadXml(tmpl)` errors to warnings instead of failed desktop notifications ([#90](https://github.com/777genius/agent-notifications/issues/90))
- **Linux: Konsole click-to-focus now detects KDE terminal sessions** - terminal detection recognizes `KONSOLE_VERSION` and `KONSOLE_DBUS_SESSION`, allowing KDE/Wayland focus helpers to search for the correct `konsole` window class ([#92](https://github.com/777genius/agent-notifications/pull/92))

## [1.39.3] - 2026-05-28

### Fixed
- **Linux: GNOME click-to-focus now recognizes Wayland-native terminal WM classes** - GNOME focusing now uses reverse-domain WM class/app IDs for terminals such as Ghostty and WezTerm, so notification clicks can raise the correct window on Wayland sessions ([#89](https://github.com/777genius/agent-notifications/pull/89))
- **Linux: WezTerm notification clicks can target the exact pane safely** - the daemon now carries WezTerm pane/socket hints from the hook process, raises the matching WezTerm window before switching panes, and ignores inherited `WEZTERM_*` variables when the focus target is not actually WezTerm ([#89](https://github.com/777genius/agent-notifications/pull/89))

## [1.39.2] - 2026-05-27

### Fixed
- **Windows: Stop hooks no longer corrupt non-ASCII JSON payloads** - Windows hook generation now uses direct exec-form `.exe` hooks with `args` instead of routing stdin through PowerShell/Git Bash shell pipelines, preventing locale-dependent JSON parse failures such as `invalid character '\r' in string literal` and `invalid character 'b' after object key:value pair` ([#87](https://github.com/777genius/agent-notifications/issues/87))
- **Plugin hooks use argument-vector execution by default** - bundled hook configuration now passes wrapper arguments via `args`, and installer validation rejects Windows hook configs that still route through shell commands, wrappers, `.bat`/`.cmd` shims, or `$input` pipes

## [1.39.1] - 2026-05-10

### Fixed
- **macOS: notification delivery no longer steals focus** - `ClaudeNotifier.app` now launches through `open -g`, so banner delivery does not briefly bring the notifier app to the foreground before any notification click ([#82](https://github.com/777genius/agent-notifications/issues/82))
- **Plugin setup commands stay out of model context** - setup/config slash commands remain manually available from the slash menu while opting out of model invocation metadata to reduce prompt overhead after setup ([#83](https://github.com/777genius/agent-notifications/issues/83))

## [1.39.0] - 2026-05-06

### Added
- **Discord webhooks now render native embeds** - Discord notifications use structured embed author, field, and footer data instead of relying only on plain-text webhook content
- **Windows PowerShell hook generation** - `claude-notifications windows-hooks` can generate PowerShell-native Claude hook configuration for Windows installs

### Changed
- **Status config cleanup** - removed the legacy Ghostty `keywords` field from generated status configuration and aligned architecture documentation

### Fixed
- **Windows: native hooks no longer depend on Git Bash script execution** - installer now rewrites PreToolUse, Notification, Stop, SubagentStop, and TeammateIdle hooks to call the native Windows `.exe` through PowerShell with an absolute path, avoiding silent `hook-wrapper.sh` launch failures on Windows 10/11 ([#79](https://github.com/777genius/agent-notifications/issues/79), [#80](https://github.com/777genius/agent-notifications/pull/80))
- **Discord embed robustness** - webhook rendering now skips empty Discord embed fields and clamps overlong author names before sending payloads

## [1.38.0] - 2026-04-18

### Added
- **Per-channel status overrides for desktop and webhook notifications** - each `statuses.<name>` entry can now define `desktop.enabled` and `webhook.enabled` independently, so you can disable webhook delivery for specific statuses while keeping desktop notifications on, or do the reverse ([#74](https://github.com/777genius/agent-notifications/issues/74))

### Fixed
- **CI stability for async webhook tests** - relaxed an overly strict `SendAsync` timing assertion under race-enabled CI, removing a macOS false negative without weakening the async behavior guarantee

## [1.37.0] - 2026-04-15

### Added
- **Webhook payload templates now expose richer runtime fields** - custom webhook bodies can now reference structured notification/runtime data such as session metadata, git context, cwd-derived values, and rendered message fields, making integrations like Lark/Slack/custom endpoints much easier to shape without patching the plugin

### Fixed
- **Installer: release download verification is stricter and safer** - release asset validation now rejects suspicious or incomplete downloads more defensively before replacing installed binaries, reducing the chance of ending up with a corrupted update
- **Ghostty: click-to-focus can now switch to the correct tab, not just the window** - `focus-window` now tries Ghostty's AppleScript terminal focus first and falls back to the previous `AXDocument` window-level path if Automation is unavailable or the exact terminal cannot be resolved ([#72](https://github.com/777genius/agent-notifications/issues/72))

## [1.36.7] - 2026-04-10

### Fixed
- **macOS: click-to-focus now restores minimized terminal windows before focusing them** - notification clicks no longer silently activate the app while leaving the matched window minimized in the Dock; the window is restored first and then raised on the follow-up retry, covering both the Ghostty `AXDocument` path and the generic `AXTitle` matcher ([#67](https://github.com/777genius/agent-notifications/issues/67), [#68](https://github.com/777genius/agent-notifications/pull/68))
- **macOS: minimized-window restore now gets a guaranteed post-animation retry** - the internal retry loop now handles the edge case where the restore happens on the last normal attempt, ensuring the final raise still runs after the Dock animation completes

## [1.36.6] - 2026-04-07

### Fixed
- **iTerm2: degraded click-to-focus no longer drops into misleading Screen Recording prompts** — when the iTerm2 Python API helper is unavailable, notification clicks now fall back to plain iTerm activation instead of the generic window-title matcher, which keeps the degraded path predictable and avoids prompting for the wrong permission
- **iTerm2: clearer setup guidance in both bootstrap and runtime prompts** — bootstrap now visibly points iTerm2 users to `Settings → General → Magic → Python API`, and the runtime one-time warning also covers the common “helper cannot reconnect after toggling Python API” case

## [1.36.5] - 2026-04-07

### Fixed
- **macOS: ClaudeNotifier notification clicks work again on iTerm2** — removed the accessory-app bundle setting that caused Notification Center click callbacks to relaunch `ClaudeNotifier` incorrectly and drop the click action before it reached the app
- **iTerm2: exact tab targeting now matches real `ITERM_SESSION_ID` values** — the helper now normalizes the `wXtYpZ:UUID` environment format to the `wXtYpZ.UUID` format exposed by the iTerm2 Python API, so exact tab selection no longer falls back to ambiguous `cwd` matching when multiple tabs share the same directory

## [1.36.4] - 2026-04-07

### Fixed
- **iTerm2: notification clicks now reliably bring iTerm2 to the foreground before selecting the exact tab or split pane** — the helper now explicitly activates the iTerm2 app before activating the target tab/session, fixing the post-`1.36.3` case where the right tab could be selected internally but the iTerm2 window stayed behind another app when the notification was clicked

## [1.36.3] - 2026-04-07

### Fixed
- **iTerm2: exact click-to-focus for tabs and split panes** — plain iTerm2 notifications now target the exact tab or split pane via the existing Python helper instead of only doing window-level focus by `cwd`; the tmux+iTerm2 path also safely targets the correct client/tab in multi-tab setups, avoiding cross-tab focus jumps and preserving fallbacks when the helper is unavailable ([#63](https://github.com/777genius/agent-notifications/issues/63), [#66](https://github.com/777genius/agent-notifications/pull/66))
- **iTerm2: clearer setup remediation when Python API is disabled** — the notifier now runs a preflight healthcheck for the iTerm2 Python API and shows a throttled one-time notification telling the user to enable it in iTerm2 settings, instead of silently degrading exact targeting
- **macOS: notification permission remediation is more robust** — native notifier permission problems now surface cleaner remediation/fallback behavior, with cross-platform tests updated to keep CI stable ([#64](https://github.com/777genius/agent-notifications/pull/64))

## [1.36.2] - 2026-04-02

### Fixed
- **GNOME/Wayland: notification click no longer leaves a long loading cursor** — on Ubuntu 24.04 GNOME/Wayland, notification clicks now use a hidden `claude-notifications.desktop` entry with `StartupNotify=false`, avoiding an unconsumed activation token while preserving click-to-focus behavior and the earlier Nautilus fallback fix ([#61](https://github.com/777genius/agent-notifications/issues/61))

## [1.36.1] - 2026-04-02

### Fixed
- **macOS: ClaudeNotifier notifications use native app attribution again** — the notifier now launches through LaunchServices instead of executing the app binary directly, preventing `UNUserNotificationCenter` from degrading into Script Editor attribution on recent macOS releases ([#59](https://github.com/777genius/agent-notifications/issues/59))
- **macOS: ClaudeNotifier release artifacts are signed and notarized again** — CI restored Developer ID signing, hardened runtime, notarization, and stapling for `ClaudeNotifier.app` release assets, while preserving local ad-hoc builds for development
- **macOS: delivery fallback remains available** — native ClaudeNotifier delivery is preferred, but macOS notification fallback behavior remains available if the bundled notifier cannot be used

## [1.36.0] - 2026-03-28

### Added
- **Team mode support** — smart notification handling for Claude Code teams ([#48](https://github.com/777genius/agent-notifications/issues/48)). New `teamMode` config option with three modes:
  - `always` (default) — notifications work as usual, no suppression
  - `wait-all` — suppresses lead's Stop notification, waits until all teammates go idle, then sends a single consolidated notification
  - `never` — completely silent in team mode
- **TeammateIdle hook** — new hook event for tracking when team members finish their work
- **Install script promo** — shows link to [claude_agent_teams_ui](https://github.com/777genius/claude_agent_teams_ui) after installation

### Removed
- **OSC terminal notifications** — removed the `internal/osc` package (OSC escape sequences for SSH/tmux). Feature proved unreliable across terminal emulators
- **Platform-specific toast helpers** — removed Windows toast and cross-platform toast abstractions in favor of simpler direct notification calls

### Changed
- Simplified ClaudeNotifier.app build pipeline and entitlements
- Streamlined notifier and hook handler internals

## [1.34.1] - 2026-03-26

### Fixed
- **tmux: click-to-focus in multi-session setups** — notification click now correctly switches to the right tmux session before selecting the window/pane. Previously, `select-window` only searched within the current session, so clicking a notification in a multi-session setup (e.g. one session per git worktree) would stay on whichever session was last active ([#54](https://github.com/777genius/agent-notifications/issues/54))

## [1.34.0] - 2026-03-25

### Fixed
- **macOS: JetBrains IDE click-to-focus** — added en-dash (`–`, U+2013) separator support to window title matching. JetBrains IDEs (PhpStorm, IntelliJ, WebStorm, etc.) use en-dash in window titles (`file.go – project – PhpStorm`), which was not recognized by the existing em-dash/hyphen matching. Now correctly finds and raises the right JetBrains window on notification click ([#50](https://github.com/777genius/agent-notifications/issues/50))

## [1.33.0] - 2026-03-15

### Fixed
- **macOS Tahoe: click-to-focus for all terminals** — replaced AppleScript-based window activation with the `focus-window` binary subcommand (AXTitle + CGS APIs) for regular terminals (iTerm2, Terminal.app, Alacritty, Warp, Hyper, etc.). macOS Tahoe (26.x) broke Automation permission prompts for notification click handlers, causing osascript to fail silently. The new approach uses Accessibility + Screen Recording instead of Automation, with graceful fallback to app-level activation when permissions are not granted ([#47](https://github.com/777genius/agent-notifications/issues/47))
- **macOS: app always activates on click** — when window title doesn't match the project folder name, the terminal app now still gets activated (app-level focus) instead of doing nothing
- **Symlink resolution** — `focus-window` now resolves symlinks via `filepath.EvalSymlinks` for more robust binary path handling

### Changed
- **CI: real-network E2E tests non-blocking** — marked real-network install E2E tests as `continue-on-error` across all platforms to prevent flaky network timeouts from failing CI jobs

## [1.32.0] - 2026-03-07

### Added
- **Local plugin development tooling** — new scripts (`dev-local-plugin.sh`, `dev-real-plugin.sh`, `e2e-real-claude.sh`) and Makefile targets for isolated and real-Claude plugin workflows, so contributors can reproduce plugin updates and click-to-focus issues with less setup
- **Linux focus diagnostics** — `linux-focus-debug.sh` script for troubleshooting click-to-focus issues on Linux
- **Developer documentation** — `docs/LOCAL_DEVELOPMENT.md` guide, expanded `CONTRIBUTING.md`, `docs/CLICK_TO_FOCUS.md`, and `docs/troubleshooting.md`

### Fixed
- **Linux: exact window targeting for click-to-focus** — notification clicks now capture exact window hints (X11 window ID, WM class, PID) from the hook process and use them before falling back to generic terminal title matching, significantly improving reliability on multi-window setups
- **Bootstrap plugin updates** — hardened marketplace checkout and installed plugin version verification during bootstrap, so one-line installs recover cleanly when Claude leaves an older cached plugin version in place

## [1.31.0] - 2026-03-07

### Added
- **Linux: X11 window ID for click-to-focus** — notifications now pass the terminal's X11 window ID (`$WINDOWID`) to the daemon, enabling direct `xdotool windowactivate` targeting instead of unreliable title-based matching. Includes `TryFocusWithWindowID` priority path and automatic fallback to existing methods
- **tmux pane target fallback** — `GetTmuxPaneTarget` now uses the resolved tmux binary and explicit socket path for better compatibility across environments (e.g., when `TMUX_PANE` is not set)

### Fixed
- **Notification suppression default** — changed default `SuppressQuestionAfterAnyNotificationSeconds` from 0 to 7 seconds, preventing repeated "question" notifications immediately after other notifications

### Changed
- Improved installation instructions and error handling in `install.sh` — better diagnostic messages for curl/transport failures, especially for Windows users behind corporate proxies
- Clarified README and `setup.sh` to specify commands should be run inside Claude Code

## [1.30.0] - 2026-03-03

### Added
- **macOS: iTerm2 + tmux -CC (control mode) click-to-focus** — when using iTerm2's tmux integration (`tmux -CC`), clicking a notification now switches to the correct iTerm2 tab via the iTerm2 Python API. Standard `tmux select-window` doesn't work in control mode; the plugin auto-detects -CC mode and falls back gracefully if the Python API is unavailable ([#41](https://github.com/777genius/agent-notifications/issues/41))
- **Automatic iTerm2 Python venv setup** — `bootstrap.sh` and `install.sh` now create a Python venv with the `iterm2` module for tmux -CC support (macOS only, when iTerm2 and tmux are detected)

## [1.29.2] - 2026-03-03

### Fixed
- **Linux: click-to-focus reliability** — focus methods (`activate-window-by-title`, GNOME `FocusApp`, `xdotool`) now properly validate activation results instead of silently succeeding when no window was actually focused. `xdotool windowactivate` uses `--sync` to ensure the activation request completes ([#44](https://github.com/777genius/agent-notifications/issues/44))
- **Linux: terminal detection in hook process** — focus target is now detected in the hook process instead of the daemon, since the daemon may have been started from a different environment where terminal-specific env vars (e.g. `TERMINATOR_UUID`) are not available ([#44](https://github.com/777genius/agent-notifications/issues/44))
- **"Updated to" message shown repeatedly** — the `[claude-notifications] Updated to vX.Y.Z` system message is now shown only once per version, using a stamp file to prevent duplicates across hook invocations

### Changed
- **bootstrap.sh: shim directories replace symlinks** — old cached version paths now use a lightweight shim `hook-wrapper.sh` that forwards to the current install, instead of symlinks (which are unreliable on Windows)

## [1.29.1] - 2026-03-02

### Fixed
- **Linux: click-to-focus for Terminator** — Terminator does not set `TERM_PROGRAM`, causing the daemon to fall back to generic `"Terminal"` and fail to find any Terminator window. Now detects Terminator via its `TERMINATOR_UUID` environment variable, which it always sets ([#44](https://github.com/777genius/agent-notifications/issues/44))

## [1.29.0] - 2026-03-02

### Added
- **macOS: click-to-focus support for Cursor IDE** — Cursor (VS Code fork) is now recognized as an Electron-based editor, using the binary `focus-window` subcommand instead of AppleScript (which fails on Electron apps with error -1708). Bundle ID `com.todesktop.230313mzl4w4u92` is auto-detected via `__CFBundleIdentifier` ([#39](https://github.com/777genius/agent-notifications/issues/39))

### Changed
- Renamed internal `isVSCodeBundleID` → `isElectronEditorBundleID` and `buildVSCodeFocusScript` → `buildElectronEditorFocusScript` to better reflect that the logic applies to all Electron-based editors, not just VS Code

## [1.28.0] - 2026-03-02

### Added
- **Linux: desktop-entry notification hint** — GNOME now correctly identifies the source app when clicking notifications, focusing the terminal/VS Code instead of opening Nautilus. Also adds `suppress-sound` hint to prevent duplicate notification sounds ([#42](https://github.com/777genius/agent-notifications/pull/42)) — contributed by [@ductrantrong](https://github.com/ductrantrong)

### Fixed
- **tmux: click-to-focus targets wrong pane** — `GetTmuxPaneTarget()` now reads `$TMUX_PANE` env var (stable, per-process) instead of `tmux display-message` (returns whichever pane is active). Fixes click-to-focus switching to the wrong tmux tab when the user has navigated away ([#41](https://github.com/777genius/agent-notifications/issues/41))
- **bootstrap.sh: respect `CLAUDE_CONFIG_DIR`** — bootstrap now checks `CLAUDE_CONFIG_DIR` (official Claude Code env var) before `CLAUDE_HOME` when locating the plugins directory ([#43](https://github.com/777genius/agent-notifications/issues/43))

## [1.27.0] - 2026-02-27

### Added
- **Configurable suppress filters** — new `suppressFilters` config array lets users suppress notifications matching specific conditions (status, git branch, folder name). All specified fields in a rule must match (AND logic); omitted fields act as wildcards. Contributed by [@ekain-fr](https://github.com/ekain-fr) ([#40](https://github.com/777genius/agent-notifications/pull/40))

### Fixed
- **Suppress filter check before dedup lock** — moved `ShouldFilter()` before `AcquireLock()` in hook handler to prevent filtered events from consuming dedup lock slots and blocking subsequent legitimate notifications

## [1.26.0] - 2026-02-24

### Added
- **Suppress notifications for subagents** — new `suppressForSubagents` config option (default: `true`) detects subagent sessions by `/subagents/` in `transcript_path` and suppresses notifications from both Stop and SubagentStop hooks. Covers in-process subagents, teammates, and Task tool completions

### Fixed
- **Cooldown `0` was impossible to set** — `ApplyDefaults()` could not distinguish "not configured" from explicit `0`, silently overwriting it with `12`. Changed `suppressQuestionAfterTaskCompleteSeconds` and `suppressQuestionAfterAnyNotificationSeconds` from `int` to `*int` so `null` = default, `0` = disabled ([#37](https://github.com/777genius/agent-notifications/issues/37))
- **`permission_prompt` notifications never fired** — `suppressQuestionAfterAnyNotificationSeconds` default of `12` was too aggressive, suppressing mid-task question notifications. Changed default to `0` (disabled). `suppressQuestionAfterTaskCompleteSeconds` (`12s`) remains sufficient for duplicate protection
- **Validate both cooldown fields** — `Validate()` now checks `suppressQuestionAfterAnyNotificationSeconds` for negative values (previously only checked `suppressQuestionAfterTaskCompleteSeconds`)

## [1.25.2] - 2026-02-21

### Fixed
- **Windows: remove redundant `sh` prefix from hook commands** — Claude Code already spawns a shell for hooks; the extra `sh` was misinterpreted as a script filename on some Windows environments, causing "cannot execute binary file" ([#35](https://github.com/777genius/agent-notifications/pull/35))

## [1.25.1] - 2026-02-21

### Fixed
- **Windows: "cannot execute binary file" in non-MSYS shells** — added fallback Windows detection via `$OS` environment variable (set to `Windows_NT` on all Windows), fixing hook failures when `uname -s` doesn't return `MINGW`/`MSYS`/`CYGWIN`
- **Git text-symlink detection** — `hook-wrapper.sh` now detects when `bin/claude-notifications` is a Git text-symlink stub (plain text file containing a target path) instead of a real symlink, and resolves or invalidates it to prevent "cannot execute binary file" errors
- **Windows: re-detect binary after auto-install** — after `run_install` on Windows, `hook-wrapper.sh` re-runs `detect_windows_binary` to prefer the freshly downloaded `.exe` over the `.bat` wrapper

## [1.25.0] - 2026-02-21

### Fixed
- **Windows: "cannot execute binary file" on Stop hook** — Git Bash (MINGW) cannot `exec` `.bat` files directly; they require `cmd.exe` for interpretation. Added `detect_windows_binary` to prefer the native `.exe` over the `.bat` wrapper, and `run_windows_bat` to route `.bat` execution through `cmd.exe /c call`
- **CI race condition** — real-network E2E tests now skip gracefully when the latest release binary isn't yet available (happens when CI and Release workflow run in parallel)

## [1.24.0] - 2026-02-21

### Added
- **Session ID prefix in notification titles** — notifications now show `[bold 06ddb8f7]` instead of just `[bold]`, making it easy to distinguish sessions even when Claude Code assigns different session IDs within the same conversation (e.g. plan mode → implementation)
- **Duration and actions for all notification types** — question, plan, review, and session limit notifications now consistently show tool counts and elapsed time (e.g. `📝 2 new  ⏱ 45s`), not just task_complete

### Changed
- Refactored summary generation: extracted `getActionsString()` and `appendActions()` helpers to eliminate duplicated duration/tool-count logic across all status types

## [1.23.0] - 2026-02-21

### Added
- **Ghostty click-to-focus** — clicking a notification focuses the correct Ghostty window via AXDocument (OSC 7 CWD file:// URL) ([#34](https://github.com/777genius/agent-notifications/pull/34))
  - `raiseWindowByAXDocument` C function matches windows by AXDocument attribute
  - `cwdToFileURL` converts CWD to RFC-3986-compliant file:// URL via `net/url`
  - One-time Accessibility permission prompt on first use
- **Retry with backoff for AX window focus** — both Ghostty and non-Ghostty paths now retry up to 3 times (150/250/400ms) instead of a single fixed sleep
  - Best case: 150ms (was 800ms for Ghostty, 600ms for others)
  - 3 chances to find the window instead of 1

### Fixed
- `raiseWindowByAXTitle` now checks `AXIsProcessTrusted()` and returns -1 on missing Accessibility permission instead of silently failing with "window not found"
- `cwdToFileURL` strips trailing slash to avoid double-slash in file:// URLs

### Changed
- Split `raiseWindowByTitle` into `findSwitchAndActivate` (one-shot) + `raiseWindowByAXTitle` (retriable)
- Updated documentation: README, Click-to-Focus guide, added Plugin Compatibility doc

## [1.22.0] - 2026-02-20

### Added
- **Zellij click-to-focus support** — clicking a notification switches to the correct zellij tab ([#33](https://github.com/777genius/agent-notifications/issues/33))
  - Detects `$ZELLIJ` / `$ZELLIJ_SESSION_NAME` environment variables
  - Parses active tab name from `zellij action dump-layout` (KDL format)
  - Executes `zellij -s <session> action go-to-tab-name <tab>` on notification click
- **Multiplexer registry architecture** — extensible system for terminal multiplexer integrations
  - Adding a new multiplexer = one file + one line in the registry
  - `notifier.go` no longer needs changes when adding multiplexer support
  - Priority order: tmux (first), zellij (second) — first detected wins
- **E2E tests for zellij** — full end-to-end tests with real zellij sessions via `creack/pty`

### Changed
- **Refactored tmux integration** — tmux click-to-focus now uses the shared multiplexer registry instead of hardcoded `if IsTmux()` checks

## [1.21.0] - 2026-02-20

### Added
- **Window-specific click-to-focus** — clicking a notification now focuses the exact project window instead of just activating the app ([#31](https://github.com/777genius/agent-notifications/pull/31))
  - macOS VS Code: CGo AXUIElement API via `focus-window` subcommand with cross-Space support (private CGS APIs)
  - macOS other terminals: AppleScript title search by folder name with `exit repeat` fix
  - Linux: folder name threaded through daemon IPC to xdotool/wlrctl/gdbus focus methods
- **Screen Recording permission prompt** — one-time notification when Screen Recording access is needed, with click-to-open System Settings
- **`SendQuickNotification()`** — standalone function for one-off notifications without a Notifier instance
- **Emoji-based compact notification format** — shorter, more readable notification messages

### Fixed
- **Window title matching** — replaced substring matching with component-based matching (split by " — " / " - ") to prevent false positives (e.g., folder `app` no longer matches `my-app`)
- **AppleScript escaping** — `sanitizeForAppleScript` now properly escapes `"`, `\`, and `'` instead of stripping them
- **Double punctuation** — notification messages no longer end with duplicate periods
- **Timestamp-filtered summaries** — task and review summaries now use only recent messages

### Changed
- **`SendDesktop` signature** — now accepts `cwd` for window-specific focus: `SendDesktop(status, message, sessionID, cwd)`

## [1.20.0] - 2026-02-19

### Added
- **ClaudeNotifier.app Swift rewrite** — full rewrite of the macOS notifier as a Swift package with UNUserNotificationCenter, hybrid fallback to osascript, and click-to-focus via NSWorkspace
- **Notification subtitle** — git branch and folder name shown as native subtitle (`main · my-project`) instead of being crammed into the title
- **Notification actions** — "Open" and "Dismiss" buttons on long-press/swipe via UNNotificationCategory
- **Thread grouping** — notifications grouped by session ID via `threadIdentifier`, no longer replacing each other
- **Time-sensitive notifications** — API errors and session limits break through Focus Mode via `interruptionLevel = .timeSensitive` (macOS 12+)
- **`-nosound` flag** — suppresses Swift notification sound so Go audio player is the single sound source (fixes double sound)
- **tmux click-to-focus** — clicking a notification in tmux switches to the correct window and pane via `-execute`
- **tmux E2E tests** — comprehensive integration tests for tmux pane detection, args building, and socket path extraction
- **install.sh ClaudeNotifier.app support** — downloads signed+notarized ClaudeNotifier.app.zip from GitHub Releases

### Fixed
- **tmux shell quoting** — tmux path and socket path properly quoted with single quotes in `-execute` commands
- **ARC delegate lifetime** — `withExtendedLifetime(appDelegate)` prevents premature deallocation in callback mode
- **AppleScript newline escaping** — newlines and carriage returns stripped from title/message/subtitle before passing to osascript
- **build-app.sh codesign** — `CODESIGN_FLAGS` converted from string to bash array to handle spaces in entitlements path
- **Flaky CI connectivity check** — `SKIP_CONNECTIVITY_CHECK` env var for E2E tests to avoid flaky `curl https://github.com` timeouts on macOS runners

### Changed
- **`SendDesktop` signature** — now accepts `sessionID` for thread grouping: `SendDesktop(status, message, sessionID)`
- **Notification title** — cleaned up to show only status emoji + session name, metadata moved to subtitle

## [1.19.0] - 2026-02-17

### Fixed
- **Config survives plugin updates** — config.json moved to `~/.claude/claude-notifications-go/config.json` outside the plugin cache, so settings are no longer lost when bootstrap.sh clears the cache during updates ([#30](https://github.com/777genius/agent-notifications/issues/30))
- **Automatic migration** — existing config is auto-migrated from the old location on first run (atomic write with temp file + rename)
- **Resilient fallback chain** — corrupted config never crashes the plugin; falls back to legacy path or defaults with stderr warnings
- **Cross-platform test isolation** — `setTestHome` helper correctly sets both HOME and USERPROFILE for Windows compatibility
- **Lint errcheck** — fixed unchecked `os.Chmod` return value in test cleanup

### Changed
- **Settings wizard** — now writes config to both stable and legacy paths for backward compatibility with older binary versions
- **Documentation** — all config path references updated across README, webhook guides, and architecture docs

## [1.18.0] - 2026-02-16

### Added
- **ntfy.sh webhook documentation** — custom webhook example for push notifications via ntfy.sh ([#29](https://github.com/777genius/agent-notifications/pull/29))

### Fixed
- **Bootstrap: version symlinks** — after update, creates symlinks from old version dirs to the new one so running Claude Code sessions don't break before restart
- **Bootstrap: Bash 3.2 compatibility** — fixed empty array handling for macOS default bash

### Changed
- **Updating section in README** — simplified to use the same bootstrap command as installation
- **Bootstrap success message** — clarifies that update command is same as install

## [1.17.0] - 2026-02-16

### Added
- **One-command bootstrap installer** — `curl -fsSL .../bootstrap.sh | bash` handles marketplace setup, plugin install, and binary download in a single command
- **`error.mp3` sound** — dedicated error alert sound for API errors and session limit notifications (replaces reused `question.mp3`)

### Changed
- **Session names** — simplified from two-word `bold-cat` to single-word `cat` format for cleaner notification titles
- **Notification title format** — session ID moved after branch name: `✅ Completed main [cat]` instead of `✅ Completed [bold-cat] main`
- **README installation section** — bootstrap method is now primary, manual install moved to collapsible fallback

## [1.16.0] - 2026-02-16

### Added
- **Terminal bell for tab indicators** 🔔 ([#28](https://github.com/777genius/agent-notifications/pull/28), thanks @retr0h!)
  - Sends BEL character (`\a`) to trigger terminal tab indicators (Ghostty tab highlight, tmux window bell flag)
  - Works independently of desktop notifications — bell fires even when desktop notifications are disabled
  - New `terminalBell` config option (default: `true`)
  - Cross-platform: `/dev/tty` on Unix, `os.Stdout` on Windows
  - Graceful degradation: silently skipped if TTY unavailable (Docker, CI, piped environments)
- **`list-sounds` CLI utility** — lists all available notification sound files ([#23](https://github.com/777genius/agent-notifications/issues/23))
- **`/sounds` skill command** — interactive sound browser with preview from Claude Code

### Changed
- Updated notification sound files for plan readiness and review completion statuses

## [1.15.1] - 2026-02-15

### Added
- **GNOME Terminal detection** — detect GNOME Terminal via `GNOME_TERMINAL_SCREEN` / `GNOME_TERMINAL_SERVICE` env vars for click-to-focus support on Linux ([#22](https://github.com/777genius/agent-notifications/pull/22), thanks @ductrantrong)

### Fixed
- **Windows checksum verification** — strip leading backslash from `sha256sum` output on Windows (MSYS2/Git Bash/Cygwin) which caused checksum mismatch ([#26](https://github.com/777genius/agent-notifications/issues/26))

## [1.15.0] - 2026-02-07

### Changed
- **Unified API Error notifications** — merged `api_error` (401) and `api_error_overloaded` into a single API Error type in documentation
- **Updating section** — documented auto-update mechanism via `hook-wrapper.sh` with manual fallback
- **Release checklist** — added [docs/RELEASE.md](docs/RELEASE.md) with version bump locations, auto-update explanation, and full release steps

### Fixed
- **install.sh** — `install_gnome_activate_window_extension()` now returns failure code when GNOME shell/extensions not found or extension couldn't be enabled ([#19](https://github.com/777genius/agent-notifications/pull/19))

## [1.14.0] - 2026-01-16

### Added
- **Per-status notification control** 🎛️ ([#16](https://github.com/777genius/agent-notifications/issues/16))
  - Disable individual notification types (e.g., disable only `task_complete` while keeping others)
  - New `enabled` field in status config: `"enabled": false` to disable a specific type
  - Backward compatible: missing `enabled` field defaults to `true`
  - Updated `/notifications-settings` wizard with Step 4.5 for selecting notification types
  - Example config to disable task_complete:
    ```json
    {
      "statuses": {
        "task_complete": { "enabled": false, "title": "...", "sound": "..." }
      }
    }
    ```

### Technical
- Added `Enabled *bool` field to `StatusInfo` struct (pointer for nil = true default)
- New methods: `IsStatusEnabled()`, `IsStatusDesktopEnabled()`, `IsStatusWebhookEnabled()`
- Updated `sendNotifications()` to check per-status enabled
- 14 new tests for per-status enabled functionality

## [1.13.0] - 2026-01-11

### Added
- **Console notification on plugin update** - shows warning message in Claude Code console when binary is installed or updated
  - First install: `[claude-notifications] Installed v1.13.0`
  - Update: `[claude-notifications] Updated to v1.13.0`
  - Uses `systemMessage` JSON format for Claude Code hook response

### Technical
- Added `systemMessage` output to `hook-wrapper.sh` for user-visible notifications
- New E2E test for systemMessage output

## [1.12.0] - 2026-01-11

### Added
- **Automatic version-aware updates** - wrapper now checks if binary version matches plugin version
  - After Claude auto-updates plugin files, binary is automatically updated on next hook call
  - Compares `claude-notifications version` output with `plugin.json` version
  - Uses `--force` flag to replace outdated binaries
  - Fallback: if version check fails, still works based on file existence

### Technical
- Added version comparison logic to `hook-wrapper.sh`
- 4 new E2E tests for version checking (mismatch triggers update, match skips update, etc.)

## [1.11.0] - 2026-01-11

### Added
- **Auto-download after plugin auto-update** - binaries now download automatically when hook is first called
  - Previously: after Claude auto-updated the plugin, binary was missing and hooks failed
  - Now: `hook-wrapper.sh` detects missing binary and triggers download on first use
  - Zero downtime: if download fails, hooks exit gracefully without blocking Claude
  - POSIX-compatible wrapper works on macOS, Linux, and Windows (Git Bash)

### Technical
- New `bin/hook-wrapper.sh` - lazy binary download wrapper
- Updated `hooks/hooks.json` - all hooks now use wrapper
- 17 new E2E tests for hook-wrapper covering offline, mock, and real network scenarios

## [1.10.0] - 2026-01-10

### Added
- **`notifyOnTextResponse` config option** - notifications now arrive for text-only responses (e.g., extended thinking "Baked for 32s")
  - Default: `true` (enabled)
  - Set to `false` in config to disable notifications when Claude responds without using tools

### Fixed
- Fixed test flakiness on Go 1.25+ by cleaning up stale state files between test runs

## [1.9.0] - 2026-01-10

### Added
- **Windows CI tests** - install.sh now tested on all 3 platforms (macOS, Linux, Windows)
- **Binary execution verification** - installer now verifies downloaded binary actually runs (`--version` check)
- **Network error diagnostics** - detailed hints for DNS, SSL, timeout, and firewall issues
- **Graceful offline mode** - if GitHub is unreachable but binary exists, uses existing installation

### Improved
- **Cross-platform compatibility** for install.sh:
  - Windows-compatible `ping` syntax (`-n/-w` instead of `-c/-W`)
  - Portable temp directory (`${TMPDIR:-${TEMP:-/tmp}}`)
  - Proper `.bat` wrapper creation on Windows
  - Extended regex with `grep -E` for portability
- **E2E test coverage** - 35+ tests covering offline, mock server, and real network scenarios
- **Utility downloads are now non-blocking** - if sound-preview or list-devices fail, main install continues

### Fixed
- Fixed installer hanging when optional utility downloads fail
- Fixed checksum verification for cross-platform builds

## [1.8.0] - 2026-01-10

### Added
- **double-shot-latte compatibility** 🤝
  - Notifications are now automatically suppressed when running in background judge mode
  - Detects `CLAUDE_HOOK_JUDGE_MODE=true` environment variable set by [double-shot-latte](https://github.com/obra/double-shot-latte) plugin
  - Zero configuration required - just update the plugin and it works automatically
  - Other plugin developers can use the same mechanism to suppress notifications in background Claude instances

### Documentation
- Added **🤝 Plugin Compatibility** section to README
- Documented how other plugins can suppress notifications using `CLAUDE_HOOK_JUDGE_MODE=true`

## [1.7.2] - 2026-01-10

### Improved
- **Auto-update now always works** - `/claude-notifications-go:notifications-init` reliably updates binaries even from old cached plugins
  - Downloads latest `install.sh` directly from GitHub before running
  - Uses `--force` flag to replace existing binaries
  - Cross-platform temp directory (`$TMPDIR`, `$TEMP`, `/tmp` fallback)
  - Fixes: old cached plugins used outdated installer without utility download support

## [1.7.1] - 2026-01-10

### Fixed
- **Installer now downloads utility binaries** ([#14](https://github.com/777genius/agent-notifications/issues/14))
  - `sound-preview` and `list-devices` were missing after `/claude-notifications-go:notifications-init`
  - Installer script now downloads all three binaries
  - Creates proper symlinks for all utilities

## [1.7.0] - 2026-01-10

### Added
- **Audio device selection support** 🔊 (thanks @tkaufmann!)
  - Route notification sounds to a specific audio output device
  - New `audioDevice` config option in `notifications.desktop` section
  - New `list-devices` CLI tool to enumerate available audio devices
  - New `--device` flag for `sound-preview` utility

### Changed
- **Audio backend replacement** - Replaced `oto/v3` (beep/speaker) with `malgo` (miniaudio bindings)
  - Better cross-platform audio support
  - Native device enumeration and selection
  - More reliable playback on all platforms

### Fixed
- **Windows CI test failures** - Fixed `.exe` extension handling in cross-platform tests
- **Memory safety** - DeviceID now properly copied instead of storing pointer to freed memory
- **Player state check** - Play() now returns error if player is already closed
- **WaitGroup race condition** - Added `closing` flag to prevent race between Close() and playSoundAsync()
- **CI test resilience** - Audio tests now skip gracefully in CI environments without audio backend

## [1.6.6] - 2026-01-10

### Fixed
- **Ghost notifications after 60 seconds** 👻 ([#11](https://github.com/777genius/agent-notifications/issues/11))
  - `idle_prompt` hook was firing 60 seconds after `PreToolUse(AskUserQuestion)`
  - This caused duplicate "Question" notifications with delay
  - Now Notification hook only responds to `permission_prompt`, ignoring `idle_prompt`
  - `AskUserQuestion` is already covered by PreToolUse hook (instant notification)

## [1.6.5] - 2025-12-31

### Fixed
- **Proper Unicode handling for multibyte characters** 🌍 (thanks @patrick-fu!)
  - Text truncation now uses rune count instead of byte count
  - Emoji, CJK, Cyrillic and other multibyte characters no longer get cut mid-character
  - `truncateText` and `extractFirstSentence` work correctly with international text

## [1.6.4] - 2025-12-31

### Fixed
- **CI tests now pass in GitHub Actions** 🧪
  - `TestGetGitBranch_RealRepo` failed in CI due to detached HEAD from PR checkout
  - Now uses isolated temporary git repository with known branch name
  - Added `TestGetGitBranch_DetachedHead` to verify empty string for detached HEAD

## [1.6.3] - 2025-12-26

### Fixed
- **Fixed fallback logic in content lock** 🔒
  - v1.6.2 had a bug: when lock was busy, code still proceeded without lock
  - Now correctly exits when another process holds the lock
  - Only uses fallback on actual errors (e.g., /tmp unavailable)

## [1.6.2] - 2025-12-26

### Fixed
- **Race condition in content-based deduplication** 🔒
  - Stop and Notification hooks were running simultaneously, both passing duplicate check
  - Added shared content lock to serialize duplicate check and state update
  - Now only one hook can check and save notification state at a time
  - Prevents duplicate notifications when different hooks fire near-simultaneously

## [1.6.1] - 2025-12-26

### Fixed
- **Version numbers no longer break sentence extraction** 🔧
  - Text like "Бинарник v1.6.0 установлен!" was incorrectly cut at "v1."
  - Now correctly handles version numbers, decimals, IP addresses
  - Dots after digits are no longer treated as sentence endings

## [1.6.0] - 2025-12-26

### Added
- **Folder name in notification titles** 📁
  - Notification titles now show the project folder name
  - Format: `✅ Completed [session-name] main my-project`
  - Helps identify which project the notification is from

- **Content-based duplicate detection** 🔇
  - Prevents duplicate notifications with similar text within 3 minutes
  - Normalizes messages (ignores trailing dots, case differences)
  - Example: "Completed" and "Question" with same text won't both show
  - Fixes issue where different hooks sent near-identical notifications

## [1.5.0] - 2025-12-25

### Added
- **Git branch name in notifications** 🌿
  - Notification titles now show the current git branch
  - Format: `✅ Completed [session-name] main`
  - Only shown when working in a git repository

## [1.4.2] - 2025-12-24

### Fixed
- **Click-to-focus now works on macOS Sequoia (15.x)** 🎯
  - Removed `-sender` option that was conflicting with `-activate`
  - Trade-off: notifications no longer show custom Claude icon
  - Click-to-focus now reliably activates terminal window

## [1.4.1] - 2025-12-24

### Fixed
- Skip terminal-notifier integration test in CI (no NotificationCenter available)

## [1.4.0] - 2025-12-24

### Added
- **Click-to-focus notifications on macOS** 🎯
  - Clicking a notification activates your terminal window
  - Auto-detects terminal app (Warp, iTerm, Terminal, kitty, Alacritty, etc.)
  - Uses `terminal-notifier` under the hood
  - Enable with `"clickToFocus": true` in desktop config (enabled by default)
  - Manual override: `"terminalBundleID": "com.your.terminal"`

- **Claude icon in notifications** 🤖 *(removed in 1.4.2 due to macOS Sequoia conflict)*
  - Custom Claude icon displayed on the left side of macOS notifications
  - Auto-creates `ClaudeNotifications.app` on first notification
  - ⚠️ Removed in v1.4.2: `-sender` option conflicted with click-to-focus on macOS 15.x

### Changed
- **Shorter notification titles**
  - `✅ Task Completed` → `✅ Completed`
  - `🔍 Review Completed` → `🔍 Review`
  - `❓ Claude Has Questions` → `❓ Question`
  - `📋 Plan Ready for Review` → `📋 Plan`

### Technical
- Terminal bundle ID detection via `__CFBundleIdentifier` and `TERM_PROGRAM`
- ~~Uses `-sender com.claude.notifications` for reliable icon display~~ (removed in 1.4.2)

## [1.3.0] - 2025-12-24

### Added
- **Lark/Feishu webhook support** - New webhook preset for Lark (飞书) notifications
  - Interactive card format with colored headers based on notification status
  - Supports all notification types (Task Complete, Review Complete, Question, Plan Ready)
  - Wide screen mode for better readability
  - Session ID included in notifications
  - Add `"preset": "lark"` to webhook configuration to enable

## [1.2.1] - 2025-12-14

### Fixed
- **Webhook notifications never sent** ([#6](https://github.com/777genius/agent-notifications/issues/6))
  - `Shutdown()` now waits for in-flight HTTP requests to complete before exit
  - Added `defer webhookSvc.Shutdown(5s)` to `HandleHook()` for graceful shutdown
  - Previously: `cancel()` was called immediately, interrupting HTTP requests
  - Now: `cancel()` is only called after completion or on timeout

### Added
- E2E test `TestE2E_WebhookGracefulShutdown` - deterministic graceful shutdown verification
- Unit tests for `Shutdown()` + `SendAsync()` combination
- Updated `webhookInterface` to include `Shutdown(timeout)` method

## [1.2.0] - 2025-11-03

### Added
- **Subagent notification control** - New config option `notifyOnSubagentStop`
  - Prevents premature "Completed" notifications when Task agents (subagents) finish
  - Main Claude session continues working without distracting notifications
  - Default: `false` (notifications disabled for subagents)
  - Users can enable via `"notifyOnSubagentStop": true` in config if desired
  - Fixes issue where Plan/Explore agents triggered completion notifications while Claude was still thinking

### Changed
- SubagentStop hook now checks config before sending notifications
- Split SubagentStop and Stop hook handling for better control

### Technical Details
- Added `NotifyOnSubagentStop` boolean field to `NotificationsConfig` struct
- Updated hook handler in `internal/hooks/hooks.go` to respect config setting
- Added comprehensive tests for both enabled and disabled states
- All existing tests pass with new functionality

## [1.1.2] - 2025-10-25

### Fixed
- **Volume control on macOS** 🔊
  - Replaced `effects.Volume` with `effects.Gain` for reliable volume control
  - Volume settings (e.g., 30%) now work correctly on macOS
  - Simplified volume conversion logic (linear instead of logarithmic)
  - Affects both notification sounds and `sound-preview` utility
  - All tests passing with new implementation
- **GitHub Actions build step** - Windows builds now work correctly
  - Added `shell: bash` to build step for all platforms
  - Resolved PowerShell syntax error preventing Windows builds from completing

### Changed
- Simplified `volumeToGain()` function - removed complex logarithmic calculations
- Updated documentation in code to reflect linear gain formula: `output = input * (1 + Gain)`

## [1.1.1] - 2025-10-25

### Fixed
- **Missing sound-preview binary** - fixes `/notifications-settings` sound preview
  - Added `sound-preview` utility to build system
  - Now built for all platforms (darwin, linux, windows)
  - Included in GitHub Releases
  - Supports interactive sound preview during settings configuration
  - Handles MP3, WAV, FLAC, OGG, AIFF formats

## [1.1.0] - 2025-10-25

### Added
- **New notification type: API Error 401** 🔴
  - Detects authentication errors when OAuth token expires
  - Shows "🔴 API Error: 401" with message "Please run /login"
  - Triggered when both "API Error: 401" and "Please run /login" appear in assistant messages
  - Priority detection (checks before tool-based detection)
  - Added comprehensive tests for API error detection

### Improved
- **Binary size optimization** - 30% smaller release binaries
  - Production builds now use `-ldflags="-s -w" -trimpath` flags
  - Binary size reduced from ~10 MB to ~7 MB per platform
  - Faster download times for users (5 seconds instead of 8 seconds)
  - Better privacy (no developer paths in binaries)
  - Deterministic builds across different machines
  - Development builds unchanged (still include debug symbols)

### Changed
- Updated notification count from 5 to 6 types in README
- All tests passing with new features

## [1.0.3] - 2025-10-24

### Fixed
- Critical bug in duration calculation ("Took" time in notifications)
  - User text messages were not being detected in transcript parsing
  - `GetLastUserTimestamp` now correctly parses string content format
  - Duration now shows accurate time (e.g., "Took 5m" instead of "Took 2h 30m")
  - Tool counting now accurate (prevents showing inflated counts like "Edited 32 files")
- Added custom JSON marshaling/unmarshaling for `MessageContent` to handle both string and array content formats

### Technical Details
- Fixed `pkg/jsonl/jsonl.go`: Added `ContentString` field and custom `UnmarshalJSON`/`MarshalJSON` methods
- User messages with `"content": "text"` format now properly parsed (previously only array format worked)
- All existing tests pass + added new tests for string content parsing

## [1.0.2] - 2025-10-23

### Added
- Linux ARM64 support for Raspberry Pi and other ARM64 Linux systems (#2)
  - Native ARM64 runner (`ubuntu-24.04-arm`) for reliable builds
  - Full audio and notification support via CGO
  - Automatic binary download via `/claude-notifications-go:notifications-init` command

### Fixed
- Webhook configuration validation now only runs when webhooks are enabled (#1)
  - Previously caused "invalid webhook preset: none" error even with webhooks disabled
  - Preset and format validation now conditional on `webhook.enabled` flag

### Changed
- Documentation updates for clarity and platform-specific instructions

## [1.0.1] - 2025-10-22

### Added
- Windows ARM64 binary support
- Windows CMD and PowerShell compatibility improvements

### Fixed
- Plugin installation and hook integration issues
- Plugin manifest command paths
- POSIX-compliant OS detection for better cross-platform support

## [1.0.0] - 2025-10-20

### Added
- Initial release of Claude Notifications plugin
- Cross-platform desktop notifications (macOS, Linux, Windows)
- Smart notification system with 5 types:
  - Task Complete
  - Review Complete
  - Question
  - Plan Ready
  - Session Limit Reached
- State machine analysis for accurate notification detection
- Webhook integrations (Slack, Discord, Telegram, Custom)
- Enterprise-grade webhook features:
  - Retry logic with exponential backoff
  - Circuit breaker for fault tolerance
  - Rate limiting with token bucket algorithm
- Audio notification support (MP3, WAV, FLAC, OGG, AIFF)
- Volume control (0-100%)
- Interactive setup wizards
- Two-phase lock deduplication system
- Friendly session names
- Pre-built binaries for all platforms
- GitHub Releases distribution

### Fixed
- Error handling improvements across webhook and notifier packages
- Data race in error handler
- Question notification cooldown system
- Cross-platform path normalization

[1.0.2]: https://github.com/777genius/agent-notifications/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/777genius/agent-notifications/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/777genius/agent-notifications/releases/tag/v1.0.0
