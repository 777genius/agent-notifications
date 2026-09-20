package installruntime

import (
	"fmt"
	"path/filepath"
)

// OwnedNotificationCommand selects the installed entry, including native Windows
// architecture names. Never infer an executable from an unowned filesystem file.
func OwnedNotificationCommand(l Ledger, root string) (string, error) {
	var found string
	for _, name := range []string{"claude-notifications", "claude-notifications.exe", "claude-notifications-windows-amd64.exe", "claude-notifications-windows-arm64.exe"} {
		path := filepath.Join(root, "bin", name)
		if _, ok := OwnedFile(l, path); !ok {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("ambiguous owned notification command")
		}
		found = path
	}
	if found == "" {
		return "", fmt.Errorf("owned notification command missing")
	}
	return found, nil
}
