//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package installruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Fails if a safe legacy lock is rejected, replaced, truncated, or left public.
func TestLockAdoptsReadOnlyPublicPermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0604, 0640, 0644} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hooks.json.lock")
			if err := os.WriteFile(path, []byte("legacy lock"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			release, err := Lock(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			release()
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) || after.Mode().Perm() != 0600 {
				t.Fatalf("lock identity/permissions changed incorrectly: %v %v", after, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "legacy lock" {
				t.Fatalf("lock contents modified: %q %v", data, err)
			}
		})
	}
}

// Fails if LockExisting repairs permissions or writable acquisition chmods
// before excluding another holder of the same permanent inode.
func TestLockAdoptionWaitsForHolderAndExistingDoesNotMutate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json.lock")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if release, err := Lock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("contention did not time out: %v", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if release, err := LockExisting(ctx, path); err == nil {
		release()
		t.Fatal("non-mutating acquisition adopted public permissions")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("permissions mutated before writable lock acquisition: %v %v", info, err)
	}
	if release, err := Lock(ctx, path); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

// Fails if adoption broadens beyond read-only public access or chmods an unsafe inode.
func TestLockAdoptionRejectsUnsafeModesWithoutMutation(t *testing.T) {
	for _, mode := range []os.FileMode{0620, 0602, 0666, 0700, 0400, 0600 | os.ModeSetuid} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock")
			if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if release, err := Lock(ctx, path); err == nil {
				release()
				t.Fatal("unsafe mode accepted")
			}
			after, err := os.Stat(path)
			if err != nil || before.Mode() != after.Mode() || !os.SameFile(before, after) {
				t.Fatalf("rejected inode modified: %v %v", after, err)
			}
		})
	}
}
