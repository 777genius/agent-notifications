//go:build linux || darwin

package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPurgeResumesInteriorDeletion(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			ctx, r := request(t)
			var err error
			r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Commit(ctx, r); err != nil {
				t.Fatal(err)
			}
			r.Native = nil
			r.RemoveConsumer = true
			r.PurgeNative = true
			reached := false
			r.Fault = func(p string) error {
				if strings.HasPrefix(p, "purge-entry:") {
					reached = true
					return fmt.Errorf("interior crash")
				}
				return nil
			}
			if _, err = Commit(ctx, r); err == nil || !reached {
				t.Fatalf("interior fault: %v %v", reached, err)
			}
			marker := filepath.Join(r.ControlRoot, "transaction.json")
			data, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := decodeTransaction(data)
			if err != nil {
				t.Fatal(err)
			}
			added := filepath.Join(tx.Native.Staged, "foreign")
			if foreign {
				if err = os.WriteFile(added, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r.Fault = nil
			_, err = Commit(ctx, r)
			if foreign {
				if err == nil {
					t.Fatal("foreign entry accepted")
				}
				got, e := os.ReadFile(added)
				if e != nil || string(got) != "keep" {
					t.Fatal("foreign removed")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(tx.Native.Staged); !os.IsNotExist(err) {
					t.Fatal("tombstone remains")
				}
				if _, err = os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("marker remains")
				}
			}
		})
	}
}

func TestPurgePreservesIdenticalForeignInode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "asset")
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := capturePurgeTree(root)
	if err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "original")
	if err = os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cleanupPurgeTree(PurgeTree{Path: root, Entries: entries}, nil); err == nil {
		t.Fatal("identical foreign inode accepted")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "same bytes" {
		t.Fatal("foreign inode removed", err)
	}
}
