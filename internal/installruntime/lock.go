// Package installruntime owns the shared installation transaction primitives.
// Lock order is component, then config paths in canonical sorted order.
// Config-only migration must never acquire the component lock.
package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ControlRoot is independent of any client's installation or cache directory.
func ControlRoot() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agent-notifications"), nil
}

// Lock holds a permanent inode. Closing releases the kernel lock; the path is
// never unlinked, including after crashes. Callers must supply a deadline.
func Lock(ctx context.Context, path string) (func(), error) {
	return lock(ctx, path, true)
}

// LockExisting acquires the permanent inode without creating any filesystem state.
func LockExisting(ctx context.Context, path string) (func(), error) {
	return lock(ctx, path, false)
}

func lock(ctx context.Context, path string, create bool) (func(), error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, fmt.Errorf("installation lock requires a deadline")
	}
	if create {
		if err := ensureDir(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	f, err := openLock(path, create)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		acquired, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if acquired {
			info, statErr := f.Stat()
			named, nameErr := os.Lstat(path)
			if statErr != nil || nameErr != nil || !os.SameFile(info, named) {
				f.Close()
				return nil, fmt.Errorf("installation lock inode changed")
			}
			return func() { _ = f.Close() }, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// A newly created control/config directory also needs its parent entry flushed;
// syncing only transaction.json's immediate directory cannot establish that link.
func ensureDir(path string, mode os.FileMode) error {
	var missing []string
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("installation parent is not a directory")
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, p)
		if filepath.Dir(p) == p {
			return err
		}
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	for _, p := range missing {
		if err := syncDir(filepath.Dir(p)); err != nil {
			return err
		}
	}
	return nil
}
