//go:build windows

package journal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsInspectReadOnlyValidatesCanonicalJournalWithoutChangingState(t *testing.T) {
	root := privateJournalRoot(t)
	clock := ClockFunc(func() Sample { return Sample{Boot: "test-boot", Seconds: 100, Available: true} })
	store, err := Initialize(windowsJournalContext(t), Options{Root: root, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "journal.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := InspectReadOnly(windowsJournalContext(t), Options{Root: root, Clock: clock})
	if err != nil || identity != store.Namespace() {
		t.Fatalf("Windows read-only inspect: %q %v", identity, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only inspection changed Windows journal", err)
	}
	damaged := []byte("{broken journal\n")
	if err := os.WriteFile(path, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectReadOnly(windowsJournalContext(t), Options{Root: root, Clock: clock}); err == nil {
		t.Fatal("Windows inspector accepted corrupt journal")
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, damaged) {
		t.Fatal("inspector altered corrupt Windows journal", err)
	}
}
