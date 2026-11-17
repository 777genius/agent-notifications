package opencodeinstall

import "github.com/777genius/agent-notifications/internal/desktopinstall"

// Product lifecycle tests retain explicit test-home observations while the
// shared platform adapter owns all shortcut inspection and readiness checks.
func windowsShortcutPath(home string) (string, error) {
	return desktopinstall.WindowsShortcutPath(home)
}
func inspectWindowsShortcut(path string) (string, string, string, error) {
	return desktopinstall.InspectWindowsShortcut(path)
}
func sameWindowsFile(a, b string) bool {
	return desktopinstall.SameWindowsFile(a, b)
}
func windowsShortcutReady(controlRoot, executable, home string) error {
	return desktopinstall.WindowsShortcutReadyInHome(controlRoot, executable, home)
}
