//go:build linux || darwin

package installruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistedIdentityMatches(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		stored, fresh, parent string
		want                  bool
	}{
		{"exact", "5:10", "5:10", "5:2", true},
		{"exact at a mount point", "5:10", "5:10", "7:2", true},
		{"renumbered volume", "6:10", "5:10", "5:2", true},
		{"different inode", "6:11", "5:10", "5:2", false},
		{"same device, different inode", "5:11", "5:10", "5:2", false},
		{"mount point substituted", "6:10", "5:10", "7:2", false},
		{"missing directory", "6:10", "", "5:2", false},
		{"no parent", "6:10", "5:10", "", false},
		{"empty stored", "", "5:10", "5:2", false},
		{"windows-shaped stored", "6:0:10", "5:10", "5:2", false},
		{"malformed stored", "10", "5:10", "5:2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PersistedIdentityMatches(tc.stored, tc.fresh, tc.parent); got != tc.want {
				t.Fatalf("PersistedIdentityMatches(%q, %q, %q) = %v", tc.stored, tc.fresh, tc.parent, got)
			}
		})
	}
}

func TestRefreshNativeIdentitiesLeavesSharedRecordIntact(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var gens []NativeGeneration
	for _, name := range []string{"generation-a.app", "generation-b.app"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		id, err := nativeDirectoryID(path)
		if err != nil {
			t.Fatal(err)
		}
		gens = append(gens, NativeGeneration{Path: path, DirectoryID: shiftDevice(t, id)})
	}
	live := NativeRecord{Path: gens[1].Path, DirectoryID: gens[1].DirectoryID, PreviousPath: gens[0].Path, PreviousDirectoryID: gens[0].DirectoryID, Published: gens}
	stale := gens[0].DirectoryID
	retired := live
	if err := refreshNativeIdentities(&retired); err != nil {
		t.Fatal(err)
	}
	if live.Published[0].DirectoryID != stale {
		t.Fatal("refresh rewrote the record it was copied from")
	}
	requireFreshNativeIdentities(t, &retired)
}
