package installruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockProcess(t *testing.T) {
	if path := os.Getenv("PR2_LOCK_CHILD"); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		unlock, err := Lock(ctx, path)
		if unlock != nil {
			unlock()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("contending child: %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), ".component-install.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := Lock(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockProcess$")
	cmd.Env = append(os.Environ(), "PR2_LOCK_CHILD="+path)
	output, err := cmd.CombinedOutput()
	unlock()
	if err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	release, err := Lock(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("lock inode changed")
	}
}

func TestLockRequiresDeadline(t *testing.T) {
	if _, err := Lock(context.Background(), filepath.Join(t.TempDir(), "lock")); err == nil {
		t.Fatal("missing deadline accepted")
	}
}
