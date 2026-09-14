//go:build windows

package journal

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func windowsJournalContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func privateJournalRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(strings.TrimPrefix(root, `\\?\`))
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err = journalRestrictPrivate(h); err != nil {
		t.Fatal(err)
	}
	if err = journalRequirePrivate(h); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWindowsInitializeOpenAdmit(t *testing.T) {
	root := privateJournalRoot(t)
	clock := ClockFunc(func() Sample { return Sample{Boot: "win-boot", Seconds: 100, Available: true} })
	s, err := Initialize(windowsJournalContext(t), Options{Root: root, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(windowsJournalContext(t), Options{Root: root, Clock: clock})
	if err != nil || opened.Namespace() != s.Namespace() {
		t.Fatal(opened, err)
	}
	a := Admission{Key: Key{Source: "codex/local", Session: "session", Kind: Explicit, Request: "windows-journal"}, Digest: sha256.Sum256([]byte("payload")), TrackingID: "tracking"}
	got, err := opened.Admit(windowsJournalContext(t), a)
	if err != nil || !got.Fresh {
		t.Fatal(got, err)
	}
	again, err := opened.Lookup(windowsJournalContext(t), a.Key, a.Digest)
	if err != nil || !again.Found || again.Fresh {
		t.Fatal(again, err)
	}
}

func TestWindowsInitializeRefusesExistingState(t *testing.T) {
	root := privateJournalRoot(t)
	clock := ClockFunc(func() Sample { return Sample{Boot: "win-boot", Seconds: 100, Available: true} })
	if _, err := Initialize(windowsJournalContext(t), Options{Root: root, Clock: clock}); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(windowsJournalContext(t), Options{Root: root, Clock: clock}); err == nil {
		t.Fatal("reinitialize accepted")
	}
}
