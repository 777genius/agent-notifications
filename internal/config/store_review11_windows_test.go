//go:build windows

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWindowsInitEntryProbeRetriesOnlySharingViolation(t *testing.T) {
	calls := 0
	err := checkInitEntryContext(context.Background(), `C:\absent\config.json`, func(string) error {
		calls++
		if calls == 1 {
			return syscall.Errno(32)
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}

	for _, failure := range []error{syscall.Errno(5), &Error{Code: ConfigLinkedPath}} {
		calls = 0
		err = checkInitEntryContext(context.Background(), `C:\absent\config.json`, func(string) error {
			calls++
			return failure
		})
		if !errors.Is(err, failure) || calls != 1 {
			t.Fatalf("failure %v was retried or hidden: err=%v calls=%d", failure, err, calls)
		}
	}
}

func TestWindowsInitEntryProbeSharingViolationDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	calls := 0
	err := checkInitEntryContext(ctx, `C:\absent\config.json`, func(string) error {
		calls++
		return syscall.Errno(32)
	})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigLockTimeout || calls < 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestWindowsUnavailableBaseDoesNotPublishHomeGuard(t *testing.T) {
	env := storeEnv(t)
	env.Vars["APPDATA"] = `relative\base`
	guard := filepath.Join(env.Vars["USERPROFILE"], ".agent-notifications-config.lock")
	_, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigBaseUnavailable {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Lstat(guard); !os.IsNotExist(statErr) {
		t.Fatalf("guard was created for rejected selection: %v", statErr)
	}
}

func TestWindowsLegacySurvivesUnavailableBase(t *testing.T) {
	env := storeEnv(t)
	legacy := filepath.Join(env.Vars["USERPROFILE"], ".claude", "claude-notifications-go", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	env.Vars["APPDATA"] = `relative\base`
	selected, err := resolveForMutation(context.Background(), env)
	if err != nil || !selected.Exists || selected.Source != "legacy" {
		t.Fatalf("selection=%+v err=%v", selected, err)
	}
}
