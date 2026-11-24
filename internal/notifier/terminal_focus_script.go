package notifier

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// buildFocusScript returns the shell command for -execute in terminal-notifier.
// For Warp: opens WARP_FOCUS_URL so Warp raises the originating window/tab/pane.
// For Ghostty: uses AXDocument attribute (OSC 7 CWD) via Accessibility API,
// falling back to plain app activation.
// For all apps (including Electron editors and regular terminals): invokes the
// binary's focus-window subcommand which uses CGS + AXTitle APIs to find and
// raise the correct window across Spaces.
// Returns "" when cwd is empty or unusable (caller should use -activate instead),
// except Warp/iTerm2 session targeting which can work without cwd.
func buildFocusScript(bundleID, cwd string) string {
	return buildFocusScriptWithOptions(bundleID, cwd, "")
}

func buildFocusScriptWithOptions(bundleID, cwd, ghosttyTerminalID string) string {
	if isIterm2BundleID(bundleID) {
		return buildIterm2FocusScript(cwd)
	}

	if focusURL := warpFocusURL(); focusURL != "" {
		return buildWarpFocusScript(focusURL, bundleID, cwd)
	}

	if isGhosttyBundleID(bundleID) {
		if cwd == "" {
			return ""
		}
		return buildGhosttyFocusScript(bundleID, cwd, ghosttyTerminalID)
	}

	if cwd == "" {
		return ""
	}

	folderName := filepath.Base(cwd)
	if folderName == "" || folderName == "." || folderName == string(filepath.Separator) {
		return ""
	}

	if isElectronEditorBundleID(bundleID) {
		return buildElectronEditorFocusScript(bundleID, cwd)
	}

	// All other terminals: use focus-window subcommand (AXTitle matching + CGS Space switching).
	// Previously used AppleScript (-execute osascript), but macOS Tahoe (26.x) broke
	// Automation permission prompts for notification click handlers — osascript fails silently.
	// The focus-window approach uses Accessibility + Screen Recording instead of Automation,
	// with graceful fallback to app-level activation when permissions are not granted.
	// See: https://github.com/777genius/agent-notifications/issues/47
	return buildBinaryFocusScript(bundleID, cwd, "")
}

// isElectronEditorBundleID reports whether bundleID belongs to an Electron-based
// editor (VS Code, Cursor, etc.). These apps don't support AppleScript window
// enumeration (-1708) and require the binary focus-window subcommand instead.
func isElectronEditorBundleID(bundleID string) bool {
	return bundleID == "com.microsoft.VSCode" ||
		bundleID == "com.microsoft.VSCodeInsiders" ||
		bundleID == "com.todesktop.230313mzl4w4u92" // Cursor
}

// isGhosttyBundleID reports whether bundleID is Ghostty.
func isGhosttyBundleID(bundleID string) bool {
	return bundleID == "com.mitchellh.ghostty"
}

// shellQuote wraps s in single quotes, escaping internal single quotes
// using the '\" technique (end quote, literal apostrophe, resume quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildBinaryFocusScript builds the -execute script for apps that use the
// binary's focus-window subcommand (all macOS terminals including Electron editors and Ghostty).
// Returns "" (causing -activate fallback) if os.Executable() fails.
func buildBinaryFocusScript(bundleID, cwd, ghosttyTerminalID string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	cmd := shellQuote(exe) + " focus-window " + shellQuote(bundleID) + " " + shellQuote(cwd)
	if isGhosttyBundleID(bundleID) && ghosttyTerminalID != "" {
		cmd += " --ghostty-terminal-id " + shellQuote(ghosttyTerminalID)
	}
	return cmd
}

// buildElectronEditorFocusScript builds the -execute script for Electron-based
// editors (VS Code, Cursor). Invokes the binary's focus-window subcommand which
// activates the app, waits for AXWindows to populate, then raises the window
// matching cwd.
func buildElectronEditorFocusScript(bundleID, cwd string) string {
	return buildBinaryFocusScript(bundleID, cwd, "")
}

// buildGhosttyFocusScript builds the -execute script for Ghostty.
// Invokes the binary's focus-window subcommand which activates Ghostty,
// waits for AXWindows to populate, then raises the window matching cwd via
// AXDocument (OSC 7 file:// URL). AXDocument is window-level only; tabs and
// split panes within a window are not individually addressable.
func buildGhosttyFocusScript(bundleID, cwd, ghosttyTerminalID string) string {
	return buildBinaryFocusScript(bundleID, cwd, ghosttyTerminalID)
}

// cwdToFileURL converts an absolute path to a file:// URL. Ghostty exposes the
// window CWD (set via OSC 7) as a file:// URL in the AXDocument attribute.
// Uses net/url for RFC-3986-compliant percent-encoding.
func cwdToFileURL(cwd string) string {
	u := url.URL{Scheme: "file", Path: strings.TrimRight(cwd, "/") + "/"}
	return u.String()
}
