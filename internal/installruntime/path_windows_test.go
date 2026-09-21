package installruntime

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsConfinedRegularMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "asset")
	f := File{Path: path, Data: []byte("first"), Mode: 0600}
	if err := safePublish(f, true); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Before = before
	f.Data = []byte("second")
	if err = safePublish(f, true); err != nil {
		t.Fatal(err)
	}
	if err = safePublish(f, true); err != nil {
		t.Fatal("redo:", err)
	}
	f.Remove = true
	f.Before = desired(File{Data: f.Data, Mode: f.Mode})
	if err = safePublish(f, true); err != nil {
		t.Fatal(err)
	}
}
func TestWindowsPinnedParentCannotMove(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	h, anchors, err := windowsParents(filepath.Join(parent, "asset"), false)
	defer closeWindowsParents(h)
	if err != nil {
		t.Fatal(err)
	}
	moved := parent + "-moved"
	if err = os.Rename(parent, moved); err != nil {
		return
	}
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := pathAnchors(filepath.Join(parent, "asset"), false)
	if err != nil {
		t.Fatal(err)
	}
	if checkAnchors(anchors, got) == nil {
		t.Fatal("replacement reused pinned parent identity")
	}
}
func TestWindowsParentIdentitySubstitution(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "asset")
	anchors, err := pathAnchors(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err = safePublish(File{Path: path, Parents: anchors, Data: []byte("package"), Mode: 0600}, true); err == nil {
		t.Fatal("substitution accepted")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("replacement changed")
	}
}

func TestWindowsRejectsAmbiguousNames(t *testing.T) {
	for _, name := range []string{"asset:stream", "asset.", "asset ", "NUL", "COM1.txt", "CONOUT$"} {
		h, _, err := windowsParents(filepath.Join(t.TempDir(), name), true)
		closeWindowsParents(h)
		if err == nil {
			t.Fatalf("ambiguous name accepted: %s", name)
		}
	}
}

func TestWindowsRelativeOpenRejectsTraversal(t *testing.T) {
	h, _, err := windowsParents(filepath.Join(t.TempDir(), "asset"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWindowsParents(h)
	for _, name := range []string{"..", `..\foreign`, `asset:stream`, `nested\asset`} {
		handle, err := windowsOpenAt(h[len(h)-1], name, windows.GENERIC_READ, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE)
		if err == nil {
			windows.CloseHandle(handle)
			t.Fatalf("relative escape accepted: %s", name)
		}
	}
}
func TestWindowsFinalReparsePreservesForeignFile(t *testing.T) {
	root := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(foreign, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "asset")
	if err := os.Symlink(foreign, path); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	for _, remove := range []bool{false, true} {
		if err := safePublish(File{Path: path, Data: []byte("replace"), Mode: 0600, Remove: remove}, true); err == nil {
			t.Fatal("reparse mutation accepted")
		}
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "preserve" {
		t.Fatal("foreign file changed", err)
	}
}

func TestWindowsReplacementKeepsCASInodePinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	old := windowsBeforePublish
	defer func() { windowsBeforePublish = old }()
	windowsBeforePublish = func() error {
		if err := os.Rename(path, path+"-moved"); err == nil {
			if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Error("CAS inode was released before publication")
		}
		return nil
	}
	if err := safePublish(File{Path: path, Before: before, Data: []byte("after"), Mode: 0600}, true); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "after" {
		t.Fatal("replacement failed", err)
	}
}

func TestWindowsReplacementCrashAndFinalEntryConflict(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset")
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := Fingerprint(path)
			if err != nil {
				t.Fatal(err)
			}
			id, err := regularObjectID(path)
			if err != nil {
				t.Fatal(err)
			}
			file := File{Path: path, Before: before, Data: []byte("after"), Mode: 0600, WindowsReplacementID: id}
			old := windowsAfterEvacuate
			defer func() { windowsAfterEvacuate = old }()
			windowsAfterEvacuate = func() error {
				if foreign {
					if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return fmt.Errorf("crash")
			}
			if err = safePublish(file, true); err == nil {
				t.Fatal("missing crash")
			}
			windowsAfterEvacuate = nil
			if foreign {
				if err = safePublish(file, true); err == nil {
					t.Fatal("foreign replacement overwritten")
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != "foreign" {
					t.Fatal("foreign file lost", err)
				}
				return
			}
			if got, err := replacementFingerprint(file); err != nil || got != before {
				t.Fatal("preimage not recoverable", err)
			}
			if err = safePublish(file, true); err != nil {
				t.Fatal("redo failed", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "after" {
				t.Fatal("replacement failed", err)
			}
			if _, err = os.Stat(filepath.Join(filepath.Dir(path), windowsReplacementName(file))); !os.IsNotExist(err) {
				t.Fatal("preimage remains", err)
			}
		})
	}
}

func TestWindowsDeletionPinsComparedInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	old := windowsBeforePublish
	defer func() { windowsBeforePublish = old }()
	windowsBeforePublish = func() error {
		if err := os.Rename(path, path+"-moved"); err == nil {
			t.Fatal("deletion released CAS inode")
		}
		return nil
	}
	if err = safePublish(File{Path: path, Before: before, Remove: true}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("deletion failed", err)
	}
}

func TestWindowsTransactionRecoversEvacuatedPreimage(t *testing.T) {
	ctx, r := request(t)
	path := filepath.Join(r.RuntimeRoot, "asset")
	r.Files = []File{{Path: path, Data: []byte("before"), Mode: 0600}}
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Files = []File{{Path: path, Before: before, Data: []byte("after"), Mode: 0600}}
	old := windowsAfterEvacuate
	defer func() { windowsAfterEvacuate = old }()
	windowsAfterEvacuate = func() error { return fmt.Errorf("crash after evacuation") }
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	windowsAfterEvacuate = nil
	r.Files = nil
	if _, err = Commit(ctx, r); err != nil {
		t.Fatal("transaction replay failed", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "after" {
		t.Fatal("redo bytes", err)
	}
}
