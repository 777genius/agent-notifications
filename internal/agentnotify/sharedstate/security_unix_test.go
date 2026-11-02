//go:build linux || darwin

package sharedstate

import (
	"os"
	"path/filepath"
	"testing"
)

func makePrivateFixture(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if info.IsDir() {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

// Metadata attacks must fail before reading secrets, overwriting unrelated data,
// or silently adopting permissions. Hardlinks are refused even when owned.
func TestRefuseUnsafeFilesystemNodes(t *testing.T) {
	t.Run("public root", func(t *testing.T) {
		root := testRoot(t)
		if err := os.Chmod(root, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root, keyA, runtimeA); err == nil {
			t.Fatal("public root accepted")
		}
	})
	t.Run("root symlink", func(t *testing.T) {
		root := testRoot(t)
		link := filepath.Join(t.TempDir(), "TEST-link")
		if err := os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(link, keyA, runtimeA); err == nil {
			t.Fatal("symlink root accepted")
		}
	})
	t.Run("namespace symlink", func(t *testing.T) {
		root := testRoot(t)
		if err := os.Symlink(testRoot(t), filepath.Join(root, ".shared-mcp")); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root, keyA, runtimeA); err == nil {
			t.Fatal("symlink namespace accepted")
		}
	})
	for _, kind := range []string{"symlink", "hardlink", "permissions"} {
		t.Run("record "+kind, func(t *testing.T) {
			s := openStore(t, testRoot(t), keyA, runtimeA)
			release := locked(t, s)
			defer release()
			target := filepath.Join(testRoot(t), "unrelated")
			before := []byte("private unrelated bytes")
			if err := os.WriteFile(target, before, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.dir, recordName)
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			case "permissions":
				err = os.WriteFile(path, before, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Read(); err == nil {
				t.Fatal("unsafe record read")
			}
			if err := s.Publish(endpoint()); err == nil {
				t.Fatal("unsafe record overwritten")
			}
			if err := s.Remove(endpoint()); err == nil {
				t.Fatal("unsafe record removed")
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != string(before) {
				t.Fatal("unrelated file changed")
			}
		})
	}
	t.Run("lock permissions", func(t *testing.T) {
		s := openStore(t, testRoot(t), keyA, runtimeA)
		path := filepath.Join(s.dir, "start.lock")
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(s.root, keyA, runtimeA); err == nil {
			t.Fatal("unsafe lock adopted")
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0644 {
			t.Fatal("unsafe lock repaired")
		}
	})
	t.Run("directory substitution", func(t *testing.T) {
		s := openStore(t, testRoot(t), keyA, runtimeA)
		if err := os.Rename(s.dir, s.dir+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(s.dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := s.Publish(endpoint()); err == nil {
			t.Fatal("replacement directory accepted")
		}
	})
}
