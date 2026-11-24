//go:build windows

package opencodeinstall

import (
	"github.com/777genius/agent-notifications/internal/desktopinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func stageWindowsShortcutForSetup(home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	return desktopinstall.StageWindowsShortcut(desktopinstall.OpenCodeDesktop, home, target, desktop, ledger)
}
func StageWindowsShortcut(product DesktopProduct, home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	return desktopinstall.StageWindowsShortcut(product, home, target, desktop, ledger)
}
func StageLocalWindowsShortcut(key, home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	return desktopinstall.StageLocalWindowsShortcut(key, home, target, desktop, ledger)
}
func StageCursorWindowsShortcut(key, home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	return desktopinstall.StageCursorWindowsShortcut(key, home, target, desktop, ledger)
}
func WindowsShortcutReady(controlRoot, executable string) error {
	return desktopinstall.WindowsShortcutReady(controlRoot, executable)
}
func WindowsShortcutReadyFor(product DesktopProduct, controlRoot, executable string) error {
	return desktopinstall.WindowsShortcutReadyFor(product, controlRoot, executable)
}
func WindowsLocalShortcutReady(key, controlRoot, executable string) error {
	return desktopinstall.WindowsLocalShortcutReady(key, controlRoot, executable)
}
func WindowsCursorShortcutReady(key, controlRoot, executable string) error {
	return desktopinstall.WindowsCursorShortcutReady(key, controlRoot, executable)
}
