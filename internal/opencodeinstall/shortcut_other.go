//go:build !windows

package opencodeinstall

import (
	"errors"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const OpenCodeToastAppID = "Genius.AgentNotifications.OpenCode"

func windowsShortcutPath(string) (string, error) {
	return "", errors.New("windows shortcut requires Windows")
}
func inspectWindowsShortcut(string) (string, string, string, error) {
	return "", "", "", errors.New("windows shortcut requires Windows")
}
func sameWindowsPath(string, string) bool { return false }
func sameWindowsFile(string, string) bool { return false }
func windowsShortcutReady(string, string, string) error {
	return errors.New("windows shortcut requires Windows")
}

func stageWindowsShortcutForSetup(string, string, bool, installruntime.Ledger) (*installruntime.File, error) {
	return nil, errors.New("windows shortcut setup requires Windows")
}
