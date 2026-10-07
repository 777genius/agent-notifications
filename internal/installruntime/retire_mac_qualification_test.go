//go:build darwin

package installruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Opt-in because this invokes the real macOS process/resource observer. All
// resources belong to this test; it never launches or terminates an app.
func TestMacNativeDrainOpenAndMappedResources(t *testing.T) {
	if os.Getenv("AGENT_NOTIFY_TEST_NATIVE_DRAIN") != "1" {
		t.Skip("explicit macOS resource qualification only")
	}
	parent := t.TempDir()
	bundle := filepath.Join(parent, "Old.app")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bundle, "resource")
	if err := os.WriteFile(path, make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	scan := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return verifyNativeDrain(ctx, bundle)
	}
	if err := scan(); err != nil {
		t.Fatalf("empty bundle cannot be qualified: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := scan(); err == nil {
		t.Fatal("open resource was incorrectly retired")
	}
	mapped, err := unix.Mmap(int(f.Fd()), 0, 4096, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if mapped != nil {
			_ = unix.Munmap(mapped)
		}
	})
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scan(); err == nil {
		t.Fatal("mapping without an open fd was incorrectly retired")
	}
	retained := filepath.Join(parent, "Retained.app")
	if err = os.Rename(bundle, retained); err != nil {
		t.Fatal(err)
	}
	bundle = retained
	if err := scan(); err == nil {
		t.Fatal("rename hid the mapped resource")
	}
	if err = unix.Munmap(mapped); err != nil {
		t.Fatal(err)
	}
	mapped = nil
	if err := scan(); err != nil {
		t.Fatalf("drained renamed bundle cannot retire: %v", err)
	}
}
