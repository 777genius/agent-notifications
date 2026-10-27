//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func privateBootstrapProgressFixture(_ *testing.T, _, _, _ string) {}

func TestBootstrapProgressRejectsBorrowedOrPublicJournal(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public"} {
		t.Run(kind, func(t *testing.T) {
			intent, path := bootstrapProgressFixture(t)
			outside := filepath.Join(filepath.Dir(intent), "TEST-canary")
			if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				link := os.Link
				if kind == "symlink" {
					link = os.Symlink
				}
				if err := link(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, close, err := openBootstrapProgress(intent, path); err == nil {
				close()
				t.Fatal("accepted unsafe progress destination")
			}
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != "untouched" {
				t.Fatalf("canary changed: %q %v", got, err)
			}
		})
	}
}

func TestBootstrapProgressRefusesSymlinkBetweenAppends(t *testing.T) {
	intent, path := bootstrapProgressFixture(t)
	write, closeProgress, err := openBootstrapProgress(intent, path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProgress()
	write("claude", "verification", "running")
	retained := path + ".retained"
	if err := os.Rename(path, retained); err != nil {
		t.Fatal(err)
	}
	// Even a symlink to the admitted inode must fail the non-reparse boundary.
	if err := os.Symlink(retained, path); err != nil {
		t.Fatal(err)
	}
	write("codex", "verification", "running")
	got, err := os.ReadFile(retained)
	if err != nil || string(got) != "transition\tclaude\tverification\trunning\n" {
		t.Fatalf("symlink accepted: %q %v", got, err)
	}
}
