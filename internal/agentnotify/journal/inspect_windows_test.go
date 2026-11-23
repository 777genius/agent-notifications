//go:build windows

package journal

import (
	"bytes"
	"io"
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

func TestWindowsReadOnlySnapshotHandlePermitsAtomicReplacement(t *testing.T) {
	root := privateJournalRoot(t)
	clock := ClockFunc(func() Sample {
		return Sample{Boot: "test-boot", Seconds: 100, Available: true}
	})
	store, err := Initialize(windowsJournalContext(t), Options{Root: root, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := openRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	// Hold the exact inspection-only handle across a native snapshot rename.
	// Without FILE_SHARE_DELETE this rejects the writer on Windows.
	held, err := openFile(dir, "journal.json", oRDONLY, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	path := filepath.Join(root, "journal.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Use the writer's own private-file creation primitive. os.WriteFile
	// inherits ambient Windows ACLs, which does not qualify a journal
	// snapshot on a restricted private root.
	candidate, err := openFile(dir, "candidate-snapshot.json", oWRONLY|oCREAT|oEXCL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Write(body); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Sync(); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	// Publish through the actual journal writer, rather than os.Rename
	// (which does not select native POSIX replacement on Windows).
	if err := renameAt(dir, "candidate-snapshot.json", "journal.json", true); err != nil {
		t.Fatalf("read-only inspector blocked writer's atomic snapshot publication: %v", err)
	}
	prior, err := held.Stat()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(prior, current) {
		t.Fatal("writer did not replace the destination while inspector retained old handle")
	}
	// Previously opened handle must still read the original immutable bytes.
	oldBody, err := io.ReadAll(held)
	if err != nil || !bytes.Equal(oldBody, body) {
		t.Fatal("prior immutable snapshot was not preserved for the held reader", err)
	}
	namespace, err := InspectReadOnly(windowsJournalContext(t), Options{Root: root, Clock: clock})
	if err != nil || namespace != store.Namespace() {
		t.Fatalf("inspector rejected valid replacement: %q %v", namespace, err)
	}
}
