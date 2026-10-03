package installruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistedIdentityDarwinPurgeObjects(t *testing.T) {
	for _, mode := range []string{"renumber", "inode", "fingerprint", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "asset")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			entries, err := capturePurgeTree(root)
			if err != nil {
				t.Fatal(err)
			}
			for rel, e := range entries {
				e.ObjectID = renumberID(e.ObjectID)
				e.Directory = renumberID(e.Directory)
				entries[rel] = e
			}
			if mode == "inode" || mode == "symlink" {
				saved := filepath.Join(t.TempDir(), "original")
				if err = os.Rename(path, saved); err != nil {
					t.Fatal(err)
				}
				if mode == "inode" {
					err = os.WriteFile(path, []byte("original"), 0600)
				} else {
					err = os.Symlink(saved, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			} else if mode == "fingerprint" {
				if err = os.WriteFile(path, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = cleanupPurgeTree(PurgeTree{Path: root, Entries: entries}, nil)
			if mode == "renumber" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("purge not completed", err)
				}
			} else {
				if err == nil {
					t.Fatal("foreign purge entry accepted")
				}
				if _, err = os.Lstat(path); err != nil {
					t.Fatal("foreign entry removed", err)
				}
			}
		})
	}
}
