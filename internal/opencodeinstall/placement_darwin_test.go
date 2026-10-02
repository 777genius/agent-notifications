//go:build darwin

package opencodeinstall

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// A direct install recorded /tmp; the confirmed selector pins /private/tmp.
// Update/remove must preserve the owned key without accepting a user alias or
// another root. This uses inert native-format bytes and fresh TEST paths only.
func TestDarwinPlacementPreservesOwnedSystemAlias(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "TEST-opencode-owned-alias-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	source := filepath.Join(base, "TEST-native-source")
	if err := os.WriteFile(source, fixtureBinary(runtime.GOOS, runtime.GOARCH, "v1"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"), BinarySource: source, HomeDir: filepath.Join(base, "home"), OpenCodeConfigDir: filepath.Join(base, "opencode"), Webhook: true, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	root, err := installruntime.CanonicalPath(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	original, _, err := installruntime.ReadOwnership(root)
	if err != nil {
		t.Fatal(err)
	}
	registration := filepath.Join(r.OpenCodeConfigDir, "plugins", pluginName)
	if original.Consumers[consumerID].Registration != registration {
		t.Fatal("fixture did not publish the legacy alias")
	}
	originalPlugin, err := os.ReadFile(registration)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := installruntime.CanonicalPath(r.OpenCodeConfigDir)
	if err != nil || canonical == r.OpenCodeConfigDir {
		t.Fatalf("native system alias: %q %v", canonical, err)
	}
	r.Action = Update
	r.OpenCodeConfigDir = canonical
	if err := Apply(ctx, r); err != nil {
		t.Fatalf("canonical update: %v", err)
	}
	updated, _, err := installruntime.ReadOwnership(root)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Consumers[consumerID].Registration != registration || len(updated.Files) != len(original.Files) || updated.Generation <= original.Generation {
		t.Fatal("update changed ownership keys or did not commit")
	}
	for _, target := range []string{filepath.Join(base, "other root"), filepath.Join(base, "user alias")} {
		if filepath.Base(target) == "user alias" {
			if err := os.Symlink(canonical, target); err != nil {
				t.Fatal(err)
			}
		}
		changed := r
		changed.OpenCodeConfigDir = target
		if err := Apply(ctx, changed); err == nil {
			t.Fatalf("accepted changed root %s", target)
		}
		after, _, err := installruntime.ReadOwnership(root)
		if err != nil || after.Generation != updated.Generation {
			t.Fatalf("refusal committed: %v", err)
		}
		plugin, err := os.ReadFile(registration)
		if err != nil || string(plugin) != string(originalPlugin) {
			t.Fatalf("refusal changed plugin: %v", err)
		}
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatalf("canonical remove: %v", err)
	}
	if _, err := os.Lstat(registration); !os.IsNotExist(err) {
		t.Fatalf("owned plugin retained: %v", err)
	}
	removed, _, err := installruntime.ReadOwnership(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := removed.Consumers[consumerID]; exists {
		t.Fatal("owned consumer retained")
	}
}
