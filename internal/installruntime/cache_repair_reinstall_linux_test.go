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
