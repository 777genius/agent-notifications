//go:build !windows

package opencodeinstall

import (
	"errors"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func windowsShortcutPath(string) (string, error) {
	return "", errors.New("windows shortcut requires Windows")
}
func inspectWindowsShortcut(string) (string, string, string, error) {
	return "", "", "", errors.New("windows shortcut requires Windows")
}
func sameWindowsFile(string, string) bool { return false }
func windowsShortcutReady(string, string, string) error {
	return errors.New("windows shortcut requires Windows")
}

func stageWindowsShortcutForSetup(string, string, bool, installruntime.Ledger) (*installruntime.File, error) {
	return nil, errors.New("windows shortcut setup requires Windows")
}

func StageWindowsShortcut(DesktopProduct, string, string, bool, installruntime.Ledger) (*installruntime.File, error) {
	return nil, errors.New("windows shortcut setup requires Windows")
}

func WindowsShortcutReadyFor(DesktopProduct, string, string) error {
	return errors.New("windows shortcut requires Windows")
}
