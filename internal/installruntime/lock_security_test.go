//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package installruntime

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockRejectsUnsafeInodes(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "fifo", "public", "foreign-owner"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "lock")
			target := filepath.Join(root, "target")
			if err := os.WriteFile(target, []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			case "public":
				err = os.WriteFile(path, nil, 0644)
			case "foreign-owner":
				if os.Geteuid() != 0 {
					t.Skip("requires disposable chown privilege")
				}
				err = os.WriteFile(path, nil, 0600)
				if err == nil {
					err = os.Chown(path, 1, -1)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			release, err := Lock(ctx, path)
			if err == nil {
				release()
				t.Fatal("unsafe lock accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "foreign" {
				t.Fatal("foreign target modified")
			}
		})
	}
}
