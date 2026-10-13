// Package codexcommand contains only the frozen Codex hook command codec.
// It neither interprets shell input nor executes commands.
package codexcommand

import (
	"fmt"
	"path/filepath"
	"strings"
)

const InstallDirName = "claude-notifications-go"

// Events returns the production registration order, as a detached slice.
func Events() []string {
	return []string{"PreToolUse", "Stop", "SubagentStop", "PermissionRequest"}
}

// HookCommands preserves the trust-hashed command bytes, including quoting.
func HookCommands(installDir, event string) (posix, windows string) {
	posixLauncher := filepath.ToSlash(filepath.Join(installDir, "bin", "codex-hook-wrapper.sh"))
	windowsLauncher := filepath.FromSlash(filepath.Join(installDir, "bin", "codex-hook-wrapper.cmd"))
	posix = fmt.Sprintf("sh %s handle-hook %s --product codex", POSIXQuote(posixLauncher), event)
	windows = fmt.Sprintf(`cmd.exe /d /v:off /s /c "%s handle-hook %s --product codex"`, WindowsQuote(windowsLauncher), event)
	return
}

func POSIXQuote(s string) string   { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func WindowsQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, "") + `"` }

// Commands is the exact eight-string recorded registration contract.
func Commands(installDir string) []string {
	commands := make([]string, 0, 8)
	for _, event := range Events() {
		posix, windows := HookCommands(installDir, event)
		commands = append(commands, posix, windows)
	}
	return commands
}
