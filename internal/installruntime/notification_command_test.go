package installruntime

import (
	"path/filepath"
	"testing"
)

func TestOwnedNotificationCommandMissing(t *testing.T) {
	root := t.TempDir()
	if _, err := OwnedNotificationCommand(Ledger{Files: map[string]Identity{}}, root); err == nil {
		t.Fatal("expected missing owned command error")
	}
}

func TestOwnedNotificationCommandAmbiguous(t *testing.T) {
	root := t.TempDir()
	files := map[string]Identity{
		filepath.Join(root, "bin", "claude-notifications"):         {},
		filepath.Join(root, "bin", "claude-notifications.exe"):     {},
	}
	if _, err := OwnedNotificationCommand(Ledger{Files: files}, root); err == nil {
		t.Fatal("expected ambiguous owned command error")
	}
}
