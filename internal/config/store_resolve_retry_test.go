package config

import (
	"context"
	"errors"
	"io/fs"
	"syscall"
	"testing"
	"time"
)

func retryResolverEnv(readDir func(string) ([]fs.DirEntry, error)) EnvSnapshot {
	return EnvSnapshot{
		GOOS: "windows",
		Vars: map[string]string{
			OverrideEnv: `C:\shared\absent\config.json`,
		},
		Lstat:   func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
		ReadDir: readDir,
	}
}

func TestMutationResolveRetriesOnlyWindowsSharingViolation(t *testing.T) {
	calls := 0
	env := retryResolverEnv(func(string) ([]fs.DirEntry, error) {
		calls++
		if calls == 1 {
			return nil, syscall.Errno(32)
		}
		return nil, fs.ErrNotExist
	})
	selected, err := resolveForMutation(context.Background(), env)
	if err != nil || selected.Path == "" || selected.Exists || calls != 2 {
		t.Fatalf("selection=%+v err=%v calls=%d", selected, err, calls)
	}

	for _, failure := range []error{fs.ErrPermission, &Error{Code: ConfigRecoveryRequired}} {
		calls = 0
		env.ReadDir = func(string) ([]fs.DirEntry, error) {
			calls++
			return nil, failure
		}
		_, err = resolveForMutation(context.Background(), env)
		if err == nil || calls != 1 {
			t.Fatalf("failure %v was retried or hidden: err=%v calls=%d", failure, err, calls)
		}
	}
}

func TestMutationResolveSharingViolationHonorsDeadline(t *testing.T) {
	calls := 0
	env := retryResolverEnv(func(string) ([]fs.DirEntry, error) {
		calls++
		return nil, syscall.Errno(32)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := resolveForMutation(ctx, env)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigLockTimeout || calls < 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
