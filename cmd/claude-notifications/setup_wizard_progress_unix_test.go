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
