//go:build darwin

package geminiinstall

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

// The reused preparation must select an independent Gemini namespace, retain
// OpenCode's lock, and reject a symlink before writing into a foreign directory.
func TestGeminiSpoolPreservesOpenCodeAndRejectsLinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "control")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, RuntimeRoot: filepath.Join(t.TempDir(), "runtime"), Owner: "existing-installer", ConsumerID: consumerID,
	}); err != nil {
		t.Fatal(err)
	}
	sibling, err := opencodeinstall.PrepareNativeSpool(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	siblingLock, err := os.Stat(filepath.Join(sibling, ".spool.lock"))
	if err != nil {
		t.Fatal(err)
	}
	spool, err := PrepareNativeSpool(ctx, root)
	if err != nil || spool != filepath.Join(root, "gemini-native-spool") {
		t.Fatalf("Gemini spool placement: %s %v", spool, err)
	}
	if again, err := PrepareNativeSpool(ctx, root); err != nil || again != spool {
		t.Fatalf("repeat spool preparation: %s %v", again, err)
	}
	lock, err := os.Lstat(filepath.Join(spool, ".spool.lock"))
	if err != nil || !lock.Mode().IsRegular() || lock.Mode().Perm() != 0600 {
		t.Fatalf("private spool lock: %v %v", lock, err)
	}
	if after, err := os.Stat(filepath.Join(sibling, ".spool.lock")); err != nil || !os.SameFile(siblingLock, after) {
		t.Fatal("Gemini preparation replaced OpenCode's spool lock")
	}
	if err := os.Remove(filepath.Join(spool, ".spool.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spool); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if err := os.Symlink(foreign, spool); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareNativeSpool(ctx, root); err == nil {
		t.Fatal("Gemini accepted a symlinked spool")
	}
	if _, err := os.Lstat(filepath.Join(foreign, ".spool.lock")); !os.IsNotExist(err) {
		t.Fatal("Gemini wrote its lock into a foreign directory")
	}
}
