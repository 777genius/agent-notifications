//go:build linux

package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStorePortableReadOnlyHomeUnprivileged(t *testing.T) {
	if os.Geteuid() == 0 {
		// Copy only this test executable into a disposable user-owned directory:
		// the Go build directory and the parent's private fixtures are inaccessible.
		root, err := os.MkdirTemp("/tmp", "agent-config-unprivileged-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(root, "config.test")
		if err = os.WriteFile(child, data, 0555); err != nil {
			t.Fatal(err)
		}
		if err = os.Chown(root, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(child, "-test.run=^TestStorePortableReadOnlyHomeUnprivileged$", "-test.v")
		cmd.Env = []string{"TMPDIR=" + root, "HOME=" + root, "GOMAXPROCS=2"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("unprivileged regression: %v\n%s", err, out)
		}
		return
	}
	env := storeEnv(t)
	home := env.Vars["HOME"]
	if err := os.Chmod(home, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0700) })
	_, autoErr := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if autoErr == nil {
		t.Fatal("automatic mode bypassed unusable HOME")
	}
	env.Vars[OverrideEnv] = filepath.Join(t.TempDir(), "portable.json")
	r, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if err != nil || !r.Changed {
		t.Fatalf("portable init with readable readonly HOME: %v", err)
	}
	_, err = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	if err != nil {
		t.Fatalf("portable edit: %v", err)
	}
	if _, err = os.Lstat(filepath.Join(home, ".agent-notifications-config.lock")); !os.IsNotExist(err) {
		t.Fatalf("unexpected guard: %v", err)
	}
}

func TestStorePortableCannotBypassExistingUnreadableGuard(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission denial requires unprivileged runner")
	}
	env := storeEnv(t)
	guard := filepath.Join(env.Vars["HOME"], ".agent-notifications-config.lock")
	if err := os.WriteFile(guard, nil, 0400); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "portable.json")
	env.Vars[OverrideEnv] = target
	_, err := EnsureInitialized(context.Background(), InitRequest{Env: env})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatalf("existing inaccessible guard bypassed: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target changed: %v", err)
	}
}
