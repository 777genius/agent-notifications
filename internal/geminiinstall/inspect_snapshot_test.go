package geminiinstall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A lazy Windows file-ID lookup after Inspect could accept a replacement at
// the same name. Retain the original inode elsewhere and preserve bytes, mode
// and write time, so only the captured identity can expose the replacement.
func TestInspectSnapshotDetectsSamePathReplacement(t *testing.T) {
	for _, directory := range []bool{false, true} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "node")
			create := func() {
				t.Helper()
				if directory {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			create()
			before := inspectTree(t, base)
			if err := os.Rename(path, filepath.Join(t.TempDir(), "retained")); err != nil {
				t.Fatal(err)
			}
			create()
			stamp := before[path].info.ModTime()
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			after := inspectTree(t, base)
			if before[path].info.Mode() != after[path].info.Mode() ||
				!stamp.Equal(after[path].info.ModTime()) || before[path].contents != after[path].contents {
				t.Fatal("replacement fixture changed more than file identity")
			}
			// Exclude the parent: its changed time must not mask a lazy-ID bug.
			if err := inspectTreeDifference(map[string]inspectPathSnapshot{path: before[path]},
				map[string]inspectPathSnapshot{path: after[path]}); err == nil {
				t.Fatal("readonly observer accepted a same-path inode replacement")
			}
		})
	}
}

// Replacing cached enumeration metadata must not relax the readonly guard.
// Actual content, write-time and mode changes must still fail independently.
func TestInspectSnapshotDetectsMutation(t *testing.T) {
	for _, mutation := range []string{"contents", "file-time", "directory-time", "mode"} {
		t.Run(mutation, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "node")
			if mutation == "directory-time" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			before := inspectTree(t, base)
			stamp := before[path].info.ModTime()
			switch mutation {
			case "contents":
				if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			case "file-time", "directory-time":
				if err := os.Chtimes(path, stamp, stamp.Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(path, 0444); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(path, 0600); err != nil {
						t.Error(err)
					}
				})
			}
			after := inspectTree(t, base)
			if err := inspectTreeDifference(map[string]inspectPathSnapshot{path: before[path]},
				map[string]inspectPathSnapshot{path: after[path]}); err == nil {
				t.Fatalf("readonly observer accepted %s mutation", mutation)
			}
		})
	}
}
