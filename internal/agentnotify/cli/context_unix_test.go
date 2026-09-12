//go:build linux || darwin

package cli

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureContext(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "context.json")
	raw := `{"provider":"codex","session":"s"}`
	if e = os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	if b, e := ReadSecureContext(context.Background(), path); e != nil || string(b) != raw {
		t.Fatal(string(b), e)
	}
	for _, mode := range []os.FileMode{0644, 0660, 0700, 0000} {
		if e = os.Chmod(path, mode); e != nil {
			t.Fatal(e)
		}
		if _, e = ReadSecureContext(context.Background(), path); e == nil {
			t.Fatalf("accepted mode %o", mode)
		}
	}
	os.Chmod(path, 0600)
	link := filepath.Join(root, "link")
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadSecureContext(context.Background(), link); e == nil {
		t.Fatal("symlink accepted")
	}
	parent := filepath.Join(root, "parent")
	os.Symlink(root, parent)
	if _, e = ReadSecureContext(context.Background(), filepath.Join(parent, "context.json")); e == nil {
		t.Fatal("ancestor symlink accepted")
	}
	if e = os.WriteFile(path, []byte(strings.Repeat(" ", ContextLimit+1)), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadSecureContext(context.Background(), path); e == nil {
		t.Fatal("oversize accepted")
	}
	if _, e = ReadSecureContext(context.Background(), root); e == nil {
		t.Fatal("directory accepted")
	}
	fifo := filepath.Join(root, "fifo")
	if e = unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadSecureContext(context.Background(), fifo); e == nil {
		t.Fatal("fifo accepted")
	}
	os.WriteFile(path, []byte(raw), 0600)
	hard := filepath.Join(root, "hard")
	if e = os.Link(path, hard); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadSecureContext(context.Background(), path); e == nil {
		t.Fatal("hardlink accepted")
	}
	os.Remove(hard)
	if os.Geteuid() == 0 {
		if e = os.Chown(path, 65534, -1); e != nil {
			t.Fatal(e)
		}
		if _, e = ReadSecureContext(context.Background(), path); e == nil {
			t.Fatal("wrong owner accepted")
		}
	}
}
