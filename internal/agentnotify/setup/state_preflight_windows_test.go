//go:build windows

package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
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

func TestWindowsInspectCoexistsWithOpenJournalAndSpoolHandles(t *testing.T) {
	o, req := windowsNoneFixture(t)
	global, err := os.ReadFile(o.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(windowsContext(t), o, req)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedGeneration = result.Generation

	for _, tc := range []struct {
		name   string
		path   string
		access uint32
		share  uint32
	}{
		{"journal lock", filepath.Join("journal", "lock"), windows.GENERIC_READ | windows.GENERIC_WRITE, windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE},
		{"spool lock", filepath.Join("native-spool", ".spool.lock"), windows.GENERIC_READ | windows.GENERIC_WRITE, windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE},
		{"snapshot delete access", filepath.Join("journal", "journal.json"), windows.DELETE | windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ | windows.FILE_SHARE_DELETE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := windows.UTF16PtrFromString(filepath.Join(o.ControlRoot, "state", tc.path))
			if err != nil {
				t.Fatal(err)
			}
			h, err := windows.CreateFile(path, tc.access, tc.share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(h) }()
			if err := Inspect(windowsContext(t), o, req, global); err != nil {
				t.Fatalf("read-only preflight refused healthy state with an open consumer handle: %v", err)
			}
		})
	}
}
