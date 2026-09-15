package daemon

import (
	"os"
	"os/exec"
	"strings"

	"github.com/777genius/agent-notifications/internal/warpfocus"
)

const claudeNotificationsDesktopEntryID = "claude-notifications"

// escapeJS escapes a string for safe interpolation into JavaScript single-quoted strings.
// Prevents JS injection when values are passed to GNOME Shell.Eval.
func escapeJS(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\x00", `\x00`,
		"\u2028", `\u2028`,
		"\u2029", `\u2029`,
	)
	return r.Replace(s)
}

// GetAppID returns the .desktop app ID for a terminal name.
func GetAppID(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "code", "vscode", "visual studio code":
		return "code.desktop"
	case "gnome-terminal":
		return "org.gnome.Terminal.desktop"
	case "konsole":
		return "org.kde.konsole.desktop"
	case "alacritty":
		return "Alacritty.desktop"
	case "kitty":
		return "kitty.desktop"
	case "wezterm":
		return "org.wezfurlong.wezterm.desktop"
	case "tilix":
		return "com.gexperts.Tilix.desktop"
	case "terminator":
		return "terminator.desktop"
	case "warpterminal", "warp":
		return "dev.warp.Warp.desktop"
	default:
		return strings.ToLower(terminalName) + ".desktop"
	}
}

// GetDesktopEntryID returns the desktop entry ID (without .desktop suffix) for a terminal.
// This is the value expected by the freedesktop "desktop-entry" notification hint.
func GetDesktopEntryID(terminalName string) string {
	return strings.TrimSuffix(GetAppID(terminalName), ".desktop")
}

// GetNotificationDesktopEntryID returns the desktop-entry hint value to use for
// notifications. GNOME on Wayland shows a long-running loading cursor when the
// clicked notification advertises the terminal/editor desktop entry and the app
// never consumes the generated activation token. A dedicated hidden desktop
// file with StartupNotify=false avoids that spinner while preserving click
// handling via our daemon.
func GetNotificationDesktopEntryID(terminalName string) string {
	if isGnomeWaylandSession() && hasClaudeNotificationsDesktopEntry() {
		return claudeNotificationsDesktopEntryID
	}
	return GetDesktopEntryID(terminalName)
}

func isGnomeWaylandSession() bool {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return false
	}

	for _, desktop := range []string{
		os.Getenv("XDG_CURRENT_DESKTOP"),
		os.Getenv("XDG_SESSION_DESKTOP"),
	} {
		for _, part := range strings.Split(desktop, ":") {
			if strings.EqualFold(strings.TrimSpace(part), "GNOME") {
				return true
			}
		}
	}

	return false
}

func hasClaudeNotificationsDesktopEntry() bool {
	_, err := os.Stat(getClaudeNotificationsDesktopEntryPath())
	return err == nil
}

func getClaudeNotificationsDesktopEntryPath() string {
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(homeDir) == "" {
			return ""
		}
		dataHome = homeDir + "/.local/share"
	}

	return dataHome + "/applications/" + claudeNotificationsDesktopEntryID + ".desktop"
}

// GetGnomeWmClass returns the WM_CLASS used by the activate-window-by-title
// GNOME Shell extension for activateByWmClass calls.
// For Wayland-native apps this is the app_id in reverse-domain format.
func GetGnomeWmClass(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "ghostty":
		return "com.mitchellh.ghostty"
	case "wezterm":
		return "org.wezfurlong.wezterm"
	case "code", "vscode", "visual studio code":
		return "code"
	case "alacritty":
		return "Alacritty"
	case "kitty":
		return "kitty"
	case "gnome-terminal":
		return "org.gnome.Terminal"
	case "konsole":
		return "org.kde.konsole"
	case "tilix":
		return "com.gexperts.Tilix"
	case "terminator":
		return "Terminator"
	case "xfce4-terminal":
		return "Xfce4-terminal"
	case "mate-terminal":
		return "Mate-terminal"
	case "warpterminal", "warp":
		return "dev.warp.Warp"
	default:
		return terminalName
	}
}

// GetWlrctlAppID returns the wlroots app_id for a terminal name.
func GetWlrctlAppID(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "ghostty":
		return "com.mitchellh.ghostty"
	case "code", "vscode", "visual studio code":
		return "code"
	case "alacritty":
		return "Alacritty"
	case "kitty":
		return "kitty"
	case "wezterm":
		return "org.wezfurlong.wezterm"
	case "gnome-terminal":
		return "org.gnome.Terminal"
	case "konsole":
		return "org.kde.konsole"
	case "warpterminal", "warp":
		return "dev.warp.Warp"
	default:
		return strings.ToLower(terminalName)
	}
}

// GetKdotoolClass returns the window class for kdotool search.
func GetKdotoolClass(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "code", "vscode", "visual studio code":
		return "code"
	case "alacritty":
		return "Alacritty"
	case "kitty":
		return "kitty"
	case "wezterm":
		return "org.wezfurlong.wezterm"
	case "gnome-terminal":
		return "gnome-terminal-server"
	case "konsole":
		return "konsole"
	case "warpterminal", "warp":
		return "dev.warp.Warp"
	default:
		return strings.ToLower(terminalName)
	}
}

// GetXdotoolClass returns the X11 WM_CLASS for xdotool search.
func GetXdotoolClass(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "code", "vscode", "visual studio code":
		return "Code"
	case "alacritty":
		return "Alacritty"
	case "kitty":
		return "kitty"
	case "wezterm":
		return "org.wezfurlong.wezterm"
	case "gnome-terminal":
		return "Gnome-terminal"
	case "konsole":
		return "konsole"
	case "xfce4-terminal":
		return "Xfce4-terminal"
	case "mate-terminal":
		return "Mate-terminal"
	case "tilix":
		return "Tilix"
	case "terminator":
		return "Terminator"
	case "warpterminal", "warp":
		return "dev.warp.Warp"
	default:
		return terminalName
	}
}

// GetSearchTerm returns a window title search term for a terminal name.
func GetSearchTerm(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "code", "vscode", "visual studio code":
		return "Visual Studio Code"
	case "gnome-terminal":
		return "Terminal"
	case "warpterminal", "warp":
		return "Warp"
	default:
		return terminalName
	}
}

// GetSearchTermWithFolder returns the window title search term, using the project
// folder name for VS Code when available (more specific than "Visual Studio Code").
func GetSearchTermWithFolder(terminalName, folderName string) string {
	switch strings.ToLower(terminalName) {
	case "code", "vscode", "visual studio code":
		if folderName != "" {
			return folderName
		}
	}
	return GetSearchTerm(terminalName)
}

// GetTerminalName detects the current terminal from environment variables.
func GetTerminalName() string {
	// Try TERM_PROGRAM first (set by many terminals). Skip an inherited
	// WarpTerminal value when this process is Cursor/VS Code/etc.
	if termProg := os.Getenv("TERM_PROGRAM"); termProg != "" {
		if termProg != "WarpTerminal" || warpfocus.IsWarpHost() {
			return termProg
		}
	}

	// Check VS Code indicators
	if os.Getenv("VSCODE_INJECTION") != "" || os.Getenv("VSCODE_GIT_IPC_HANDLE") != "" {
		return "Code"
	}

	// Check GNOME Terminal indicators
	if os.Getenv("GNOME_TERMINAL_SCREEN") != "" || os.Getenv("GNOME_TERMINAL_SERVICE") != "" {
		return "gnome-terminal"
	}

	// Check Terminator (does not set TERM_PROGRAM, but always sets TERMINATOR_UUID)
	if os.Getenv("TERMINATOR_UUID") != "" {
		return "terminator"
	}

	// Check Konsole (does not set TERM_PROGRAM, but sets KONSOLE_* vars)
	if os.Getenv("KONSOLE_VERSION") != "" || os.Getenv("KONSOLE_DBUS_SESSION") != "" {
		return "konsole"
	}

	// Check Alacritty (deliberately never sets TERM_PROGRAM, but exports
	// ALACRITTY_WINDOW_ID into every window it spawns). Checked last so that a
	// more specific indicator wins when a supported terminal is nested inside it.
	if os.Getenv("ALACRITTY_WINDOW_ID") != "" {
		return "alacritty"
	}

	if warpfocus.IsWarpHost() && (os.Getenv("WARP_FOCUS_URL") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "" || os.Getenv("WARP_IS_LOCAL_SHELL_SESSION") != "") {
		return "WarpTerminal"
	}

	// Fallback to generic terminal
	return "Terminal"
}

// GetX11WindowID returns the current terminal window's X11 window ID when available.
// It is captured in the hook process and later used by the daemon for exact focus on X11.
func GetX11WindowID() string {
	return strings.TrimSpace(os.Getenv("WINDOWID"))
}

// GetExactWindowTitle returns an exact top-level window title for terminals that expose
// a reliable per-terminal identifier. Currently Terminator can provide this via
// TERMINATOR_UUID + remotinator.
func GetExactWindowTitle(terminalName string) string {
	switch strings.ToLower(terminalName) {
	case "terminator":
		return getTerminatorWindowTitle()
	default:
		return ""
	}
}

func getTerminatorWindowTitle() string {
	if os.Getenv("TERMINATOR_UUID") == "" {
		return ""
	}
	if _, err := exec.LookPath("remotinator"); err != nil {
		return ""
	}

	cmd := exec.Command("remotinator", "get_window_title")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

// GetWezTermPaneID returns the WezTerm pane ID from the environment.
func GetWezTermPaneID() string {
	return strings.TrimSpace(os.Getenv("WEZTERM_PANE"))
}

// GetWezTermSocketPath returns the WezTerm Unix socket path from the environment.
func GetWezTermSocketPath() string {
	return strings.TrimSpace(os.Getenv("WEZTERM_UNIX_SOCKET"))
}

// IsWezTermTerminalName reports whether terminalName identifies WezTerm.
func IsWezTermTerminalName(terminalName string) bool {
	switch strings.ToLower(strings.TrimSpace(terminalName)) {
	case "wezterm", "wezterm-gui", "org.wezfurlong.wezterm", "org.wezfurlong.wezterm.desktop", "com.github.wez.wezterm":
		return true
	default:
		return false
	}
}

// GetWezTermFocusHints returns WezTerm-specific focus hints only when the
// detected focus target is WezTerm. WEZTERM_* variables are often inherited by
// GUI apps launched from WezTerm, so they must not be trusted for other targets.
func GetWezTermFocusHints(terminalName string) (paneID, socketPath string) {
	return normalizeWezTermFocusHints(terminalName, GetWezTermPaneID(), GetWezTermSocketPath())
}

func normalizeWezTermFocusHints(terminalName, paneID, socketPath string) (string, string) {
	if !IsWezTermTerminalName(terminalName) {
		return "", ""
	}
	return strings.TrimSpace(paneID), strings.TrimSpace(socketPath)
}
