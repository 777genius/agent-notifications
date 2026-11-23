//go:build windows

package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestWindowsInspectChecksInitializationBeforeEnable(t *testing.T) {
	t.Run("fresh and healthy remain ready", func(t *testing.T) {
		o, req := windowsNoneFixture(t)
		global, err := os.ReadFile(o.GlobalConfig)
		if err != nil {
			t.Fatal(err)
		}
		if err := Inspect(windowsContext(t), o, req, global); err != nil {
			t.Fatalf("fresh setup refused: %v", err)
		}
		result, err := Apply(windowsContext(t), o, req)
		if err != nil {
			t.Fatal(err)
		}
		req.ExpectedGeneration = result.Generation
		if err := Inspect(windowsContext(t), o, req, global); err != nil {
			t.Fatalf("healthy existing setup refused: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(o.ControlRoot, "state", "journal")); err != nil {
			t.Fatal(err)
		}
		if err := Inspect(windowsContext(t), o, req, global); err == nil {
			t.Fatal("lost Windows journal incorrectly marked ready")
		} else if e, ok := err.(*Error); !ok || e.Reason != "initialization_recovery_required" {
			t.Fatalf("wrong lost-journal classification: %v", err)
		}
	})
	t.Run("enabled policy without owner does not reset state", func(t *testing.T) {
		o, req := windowsNoneFixture(t)
		snap, err := installruntime.ReadPolicySnapshot(windowsContext(t), o.ControlRoot)
		if err != nil {
			t.Fatal(err)
		}
		generation := snap.Installation.Ledger.Generation
		on := true
		if _, err := installruntime.Commit(windowsContext(t), installruntime.Request{
			ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot,
			Owner: o.Owner, ConsumerID: o.ConsumerID, RefreshOnly: true,
			PolicyOnly: true, PolicyEnabled: &on, ExpectedGeneration: &generation,
		}); err != nil {
			t.Fatal(err)
		}
		req.ExpectedGeneration = generation + 1
		global, err := os.ReadFile(o.GlobalConfig)
		if err != nil {
			t.Fatal(err)
		}
		if err := Inspect(windowsContext(t), o, req, global); err == nil {
			t.Fatal("unowned enabled policy incorrectly called ready")
		} else if e, ok := err.(*Error); !ok || e.Reason != "initialization_recovery_required" {
			t.Fatalf("wrong legacy-state classification: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(o.ControlRoot, "state")); !os.IsNotExist(err) {
			t.Fatal("preflight created or adopted unowned state:", err)
		}
	})
}
