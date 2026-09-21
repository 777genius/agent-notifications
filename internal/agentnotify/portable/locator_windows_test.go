//go:build windows

package portable

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func windowsPortableContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func windowsRestrict(t *testing.T, path string) {
	t.Helper()
	if err := installruntime.RestrictPrivatePath(path); err != nil {
		t.Fatal(err)
	}
}

func windowsPortableFixture(t *testing.T) Binding {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(strings.TrimPrefix(root, `\\?\`))
	b := Binding{
		Version: 1, Integration: Codex, InstallationID: "uap-install", BindingID: "binding", ScopeID: "user",
		Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope"), DataRoot: filepath.Join(root, "data"),
		ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global", "config.json"),
		RuntimeRoot: filepath.Join(root, "runtime"), Primary: "primary",
	}
	for _, p := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		windowsRestrict(t, p)
	}
	r := installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "existing", Files: []installruntime.File{{Path: filepath.Join(b.RuntimeRoot, b.Primary), Data: []byte("inert primary"), Mode: 0700}}}
	l, err := installruntime.Commit(windowsPortableContext(t), r)
	if err != nil {
		t.Fatal(err)
	}
	windowsRestrict(t, b.ControlRoot)
	windowsRestrict(t, b.RuntimeRoot)
	b.ComponentID = l.ID
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	r.Files = nil
	r.ConsumerID = key
	r.Consumer = consumer
	r.ExpectedGeneration = &l.Generation
	if _, err = installruntime.Commit(windowsPortableContext(t), r); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWindowsPublishAcquireRevoke(t *testing.T) {
	b := windowsPortableFixture(t)
	name, err := Publish(b)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := Acquire(windowsPortableContext(t), b.DataRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Executable != filepath.Join(b.RuntimeRoot, b.Primary) {
		t.Fatal("wrong runtime")
	}
	lease.Release()
	if _, err = Publish(b); err != nil {
		t.Fatal(err)
	}
	if err = RevokeLocator(b); err != nil {
		t.Fatal(err)
	}
	if _, err = Acquire(windowsPortableContext(t), b.DataRoot, name); err == nil {
		t.Fatal("revoked locator accepted")
	}
}

func TestWindowsPublishConflict(t *testing.T) {
	b := windowsPortableFixture(t)
	name, err := Publish(b)
	if err != nil {
		t.Fatal(err)
	}
	other := b
	other.BindingID = "other"
	if err = os.WriteFile(filepath.Join(b.DataRoot, name), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Publish(b); err == nil {
		t.Fatal("conflicting locator accepted")
	}
	_ = other
}

func TestWindowsIdentityModePrimaryAccepted(t *testing.T) {
	b := windowsPortableFixture(t)
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(b.RuntimeRoot, b.Primary)
	mode := snapshot.Ledger.Files[primary].Mode
	if mode&0111 != 0 {
		t.Fatalf("windows identityMode must drop unix execute bits, got %o", mode)
	}
	name, err := Publish(b)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := Acquire(windowsPortableContext(t), b.DataRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}
