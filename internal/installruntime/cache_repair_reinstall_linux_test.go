//go:build linux

package installruntime

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// Issue #585: Claude's marketplace reinstall can restore a Git-shipped macOS
// placeholder link and ordinary 0644 skill file while dropping the owned Linux
// executable. The exact managed recovery payload still exists outside the cache.
func TestCacheRepairReinstalledLinuxMarketplaceReset(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("issue #585 describes Linux amd64 marketplace assets")
	}
	f := newCacheRepairFixture(t)
	executable := filepath.Join(f.current, "bin", "claude-notifications-linux-amd64")
	alias := filepath.Join(f.current, "bin", "claude-notifications")
	skill := filepath.Join(f.current, "skills", "agent-notifications", "SKILL.md")
	payload := []byte("TEST managed Linux executable\n" + WriterProtocolMarker)
	skillBytes := []byte("# TEST managed plugin skill\n")
	before, err := Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.current, Owner: f.ledger.Owner, ConsumerID: "claude-hooks", RefreshOnly: true,
		Files: []File{
			{Path: executable, Data: payload, Mode: 0700},
			{Path: alias, Link: filepath.Base(executable)},
			{Path: skill, Data: skillBytes, Mode: 0600},
		}})
	if err != nil {
		t.Fatal(err)
	}

	// Recreate the marketplace-installed filesystem state, without changing
	// the external ownership ledger or authenticated recovery blob.
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("claude-notifications-darwin-arm64", alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skill, 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewCacheRepair(f.ctx, f.control)
	if err != nil {
		t.Fatalf("exact marketplace reset must be eligible for repair: %v", err)
	}
	if len(plan.Paths) != 3 {
		t.Fatalf("expected Linux executable, launcher and skill, got %v", plan.Paths)
	}
	after, err := RepairCache(f.ctx, f.control, plan)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation+1 || !reflect.DeepEqual(after.Files, before.Files) ||
		!reflect.DeepEqual(after.Consumers, before.Consumers) {
		t.Fatal("repair changed recorded ownership instead of restoring its bytes")
	}
	for _, path := range []string{executable, alias, skill} {
		got, err := Fingerprint(path)
		if err != nil || got != before.Files[path] {
			t.Fatalf("restored %s has wrong fingerprint: got=%+v err=%v", path, got, err)
		}
	}
	healthy, err := PreviewCacheRepair(f.ctx, f.control)
	if err != nil || len(healthy.Paths) != 0 {
		t.Fatalf("second repair must be a no-op: %+v %v", healthy, err)
	}
}

// A marketplace reset authorizes only the precise shipped link and byte-identical
// public skill mode. Any unknown edits, files or recovery payloads fail closed.
func TestCacheRepairReinstalledLinuxMarketplaceRejectsForeignState(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("issue #585 describes Linux amd64 marketplace assets")
	}
	for _, scenario := range []string{"edited-skill", "custom-link", "wrong-mode", "binary-present", "missing-replica", "linked-skill", "concurrent-edit"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCacheRepairFixture(t)
			executable := filepath.Join(f.current, "bin", "claude-notifications-linux-amd64")
			alias := filepath.Join(f.current, "bin", "claude-notifications")
			skill := filepath.Join(f.current, "skills", "agent-notifications", "SKILL.md")
			payload := []byte("TEST managed Linux executable\n" + WriterProtocolMarker)
			skillBytes := []byte("# TEST managed plugin skill\n")
			ledger, err := Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.current,
				Owner: f.ledger.Owner, ConsumerID: "claude-hooks", RefreshOnly: true,
				Files: []File{
					{Path: executable, Data: payload, Mode: 0700},
					{Path: alias, Link: filepath.Base(executable)},
					{Path: skill, Data: skillBytes, Mode: 0600},
				}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "binary-present" {
				if err := os.Remove(executable); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			link := "claude-notifications-darwin-arm64"
			if scenario == "custom-link" {
				link = "custom-linux"
			}
			if err := os.Symlink(link, alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(skill, 0644); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "edited-skill":
				if err := os.WriteFile(skill, []byte("user change\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "wrong-mode":
				if err := os.Chmod(skill, 0666); err != nil {
					t.Fatal(err)
				}
			case "linked-skill":
				if err := os.Remove(skill); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(alias), skill); err != nil {
					t.Fatal(err)
				}
			case "missing-replica":
				if err := os.Remove(filepath.Join(f.control, "cache-repair.blobs", ledger.Files[executable].SHA256)); err != nil {
					t.Fatal(err)
				}
			}
			var preview CacheRepairPlan
			if scenario == "concurrent-edit" {
				preview, err = PreviewCacheRepair(f.ctx, f.control)
				if err != nil || len(preview.Paths) != 3 {
					t.Fatalf("valid pre-edit observation failed: %+v %v", preview, err)
				}
				if err := os.WriteFile(skill, []byte("concurrent edit\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := diagnosticTree(t, f.root)
			if scenario == "concurrent-edit" {
				_, err = RepairCache(f.ctx, f.control, preview)
			} else {
				preview, err = PreviewCacheRepair(f.ctx, f.control)
				if err == nil {
					_, err = RepairCache(f.ctx, f.control, preview)
				}
			}
			if err == nil {
				t.Fatal("foreign marketplace reset was accepted")
			}
			if !reflect.DeepEqual(before, diagnosticTree(t, f.root)) {
				t.Fatal("rejected repair mutated installation")
			}
		})
	}
}
