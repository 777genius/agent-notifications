// ABOUTME: Warp session deep-link capture for click-to-focus.
// ABOUTME: Validates WARP_FOCUS_URL / WARP_TERMINAL_SESSION_UUID and rejects unrelated Warp URIs.
package warpfocus

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

var errInvalidFocusURL = errors.New("warpfocus: not a Warp session focus URL")

const (
	// FocusURLEnv is the channel-aware deep link Warp injects into each pane.
	FocusURLEnv = "WARP_FOCUS_URL"
	// SessionUUIDEnv is the 32-char hex UUID of the originating Warp pane.
	SessionUUIDEnv = "WARP_TERMINAL_SESSION_UUID"
)

const stableSessionScheme = "warp"

var allowedSchemes = map[string]bool{
	"warp":        true,
	"warppreview": true,
	"warposs":     true,
}

// FromEnv returns a validated Warp session focus URL from the current process
// environment. Prefers WARP_FOCUS_URL (channel-aware). Falls back to
// WARP_TERMINAL_SESSION_UUID as warp://session/<hex>, then to tmux's copy of
// those variables when the process is inside tmux. Empty when this process is
// not a Warp host (for example Cursor or VS Code launched from a Warp pane,
// which inherit Warp's environment).
func FromEnv() string {
	if !IsWarpHost() {
		return ""
	}
	if url := Normalize(os.Getenv(FocusURLEnv)); url != "" {
		return url
	}
	if url := FromSessionUUID(os.Getenv(SessionUUIDEnv)); url != "" {
		return url
	}
	if os.Getenv("TMUX") == "" {
		return ""
	}
	if url := Normalize(tmuxEnvironment(FocusURLEnv)); url != "" {
		return url
	}
	return FromSessionUUID(tmuxEnvironment(SessionUUIDEnv))
}

// IsWarpHost reports whether this process is running in a Warp terminal
// session, as opposed to an app that merely inherited Warp's environment
// (Cursor, VS Code, Ghostty launched from a Warp pane).
func IsWarpHost() bool {
	if isEditorHost() {
		return false
	}
	if bundleID := strings.TrimSpace(os.Getenv("__CFBundleIdentifier")); bundleID != "" {
		return strings.HasPrefix(bundleID, "dev.warp.Warp")
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "WarpTerminal":
		return true
	case "tmux", "zellij":
		return os.Getenv("TMUX") != "" || os.Getenv("ZELLIJ") != ""
	case "":
		if os.Getenv("TMUX") != "" || os.Getenv("ZELLIJ") != "" {
			return true
		}
		return os.Getenv(FocusURLEnv) != "" || os.Getenv(SessionUUIDEnv) != "" || os.Getenv("WARP_IS_LOCAL_SHELL_SESSION") != ""
	default:
		return false
	}
}

func isEditorHost() bool {
	if os.Getenv("VSCODE_INJECTION") != "" || os.Getenv("VSCODE_GIT_IPC_HANDLE") != "" {
		return true
	}
	if os.Getenv("TERM_PROGRAM") == "vscode" {
		return true
	}
	bundleID := os.Getenv("__CFBundleIdentifier")
	return strings.HasPrefix(bundleID, "com.todesktop.") ||
		strings.HasPrefix(bundleID, "com.microsoft.VSCode") ||
		strings.HasPrefix(bundleID, "com.visualstudio.code")
}

// FromSessionUUID builds warp://session/<hex> from a 32-char hex UUID or a
// dashed UUID. Preview/OSS channels cannot be inferred from the UUID alone.
func FromSessionUUID(raw string) string {
	hexID := normalizeSessionID(raw)
	if hexID == "" {
		return ""
	}
	return stableSessionScheme + "://session/" + hexID
}

// Normalize accepts an opaque Warp focus URL and returns a canonical
// scheme://session/<32-hex> form, or "" if the value is not a session deep link.
// Action, conversation, settings, and launch URIs are rejected.
func Normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if !allowedSchemes[scheme] {
		return ""
	}
	if u.User != nil || u.Opaque != "" {
		return ""
	}
	if strings.ToLower(u.Host) != "session" {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if path == "" || strings.Contains(path, "/") {
		return ""
	}
	hexID := normalizeSessionID(path)
	if hexID == "" {
		return ""
	}
	return scheme + "://session/" + hexID
}

func normalizeSessionID(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r == '-' {
			continue
		}
		if r < '0' || r > 'f' || (r > '9' && r < 'a') {
			return ""
		}
		b.WriteRune(r)
	}
	id := b.String()
	if len(id) != 32 {
		return ""
	}
	return id
}

func tmuxEnvironment(name string) string {
	tmuxPath := "tmux"
	if path, err := exec.LookPath("tmux"); err == nil {
		tmuxPath = path
	}
	socket := tmuxSocketPath()
	for _, extra := range [][]string{{}, {"-g"}} {
		args := []string{}
		if socket != "" {
			args = append(args, "-S", socket)
		}
		args = append(args, "show-environment")
		args = append(args, extra...)
		args = append(args, name)
		cmd := exec.Command(tmuxPath, args...)
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(output))
		prefix := name + "="
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func tmuxSocketPath() string {
	tmuxEnv := os.Getenv("TMUX")
	if tmuxEnv == "" {
		return ""
	}
	if idx := strings.IndexByte(tmuxEnv, ','); idx > 0 {
		return tmuxEnv[:idx]
	}
	return tmuxEnv
}
