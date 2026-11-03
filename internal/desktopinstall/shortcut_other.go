//go:build !windows

package desktopinstall

import (
	"errors"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func WindowsShortcutPath(string) (string, error) {
	return "", errors.New("windows shortcut requires Windows")
}
func InspectWindowsShortcut(string) (string, string, string, error) {
	return "", "", "", errors.New("windows shortcut requires Windows")
}
func SameWindowsFile(string, string) bool { return false }
func WindowsShortcutReadyInHome(string, string, string) error {
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

func StageCursorWindowsShortcut(string, string, string, bool, installruntime.Ledger) (*installruntime.File, error) {
	return nil, errors.New("windows shortcut setup requires Windows")
}
