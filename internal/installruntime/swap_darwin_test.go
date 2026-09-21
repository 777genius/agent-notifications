package installruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// Actual disposable-filesystem gate, intentionally Darwin-only. This proves
// directory exchange, not queued notification dispatch or native process drain.
func TestDarwinBundleExchange(t *testing.T) {
	root := t.TempDir()
	staged, live := filepath.Join(root, "staged.app"), filepath.Join(root, "live.app")
	for _, p := range []string{staged, live} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "identity"), []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Stat(live)
	if err != nil {
		t.Fatal(err)
	}
	if err := swapBundles(staged, live); err != nil {
		t.Fatalf("filesystem does not qualify for callback updates: %v", err)
	}
	after, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("old bundle inode not retained")
	}
	data, err := os.ReadFile(filepath.Join(live, "identity"))
	if err != nil || string(data) != staged {
		t.Fatal("new bundle not installed")
	}
}
