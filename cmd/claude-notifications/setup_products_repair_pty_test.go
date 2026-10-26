//go:build linux || darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Exercise the public bootstrap flow, not just the repair API: normal
// confirmation used to fail before it could authorize any installer stage.
func TestBootstrapRepairsPrunedSharedCacheBeforeConfirmation(t *testing.T) {
	for _, answer := range []string{"n", "y"} {
		t.Run(answer, func(t *testing.T) {
			f := newBootstrapFixture(t)
			f.releaseAssets()
			requireBootstrapSuccess(t, f.noTTY("--product", "claude", "--skip-agent-notify"))
			ledger := f.ownership()
			liveRoot := ledger.Consumers["claude-hooks"].RuntimeRoot
			oldRoot := filepath.Join(filepath.Dir(liveRoot), "1.0.0")
			if oldRoot == liveRoot {
				t.Fatal("TEST old version must differ")
			}
			oldFile := filepath.Join(oldRoot, "bin", "TEST-retained-runtime")
			if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control(), RuntimeRoot: oldRoot, Owner: ledger.Owner, ConsumerID: "TEST-retained",
				Files: []installruntime.File{{Path: oldFile, Data: []byte("TEST retained" + installruntime.WriterProtocolMarker), Mode: 0700}}}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(oldRoot); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(f.control(), "ownership.json"))
			if err != nil {
				t.Fatal(err)
			}
			steps := []promptStep{{"[y/n]", answer + "\n", nil}}
			if answer == "y" {
				steps = append(steps, promptStep{"comma-separated", "claude\n", nil}, promptStep{"[y/n]", "y\n", nil})
			}
			r := f.terminal([]string{"--plain", "--skip-agent-notify"}, steps...)
			if r.code != 0 || !strings.Contains(r.screen, "Restore missing notification files?") {
				t.Fatalf("bootstrap repair: %+v", r)
			}
			if answer == "n" {
				after, err := os.ReadFile(filepath.Join(f.control(), "ownership.json"))
				if err != nil || !bytes.Equal(before, after) || strings.Contains(r.screen, "Install notifications for") {
					t.Fatalf("declined repair continued/mutated installation: %v %+v", err, r)
				}
				if _, err := os.Lstat(oldRoot); !os.IsNotExist(err) {
					t.Fatalf("declined repair recreated old cache: %v", err)
				}
				return
			}
			if _, err := installruntime.ReadInstalledSnapshot(f.control()); err != nil {
				t.Fatalf("bootstrap left invalid installation: %v %+v", err, r)
			}
			if !strings.Contains(r.screen, "Missing notification files restored.") {
				t.Fatalf("repair was not reported: %+v", r)
			}
		})
	}
}
