package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/windowscallback"
)

const WindowsGenerationWriterFloor = 5
const WindowsWriterProtocolMarker = "agent-notifications-managed-writer-protocol-v5"

// WindowsChange is the concrete participant carried by the existing owner
// transaction journal. Fresh unique registry/shortcut objects have absent
// preimages; partial or replaced foreign objects never grant ownership.
type WindowsChange struct {
	Snapshot windowscallback.Snapshot
	Binding  windowscallback.Binding
	Phase    string
	Applied  int
	Observed bool
	End      uint64 `json:"-"`
}

func validateWindowsPayload(w *WindowsChange) error {
	if w == nil {
		return nil
	}
	data, e := windowscallback.EncodeSnapshot(w.Snapshot)
	if e != nil || windowscallback.Digest(data) != w.Binding.SHA256 || w.Binding.SnapshotPath != w.Snapshot.CanonicalRoot+`\generation.wne` {
		return fmt.Errorf("invalid Windows generation participant")
	}
	switch w.Phase {
	case "preparing", "participants_applied", "commit_decided", "binding_published", "committed", "rollback_decided":
	default:
		return fmt.Errorf("invalid Windows participant decision")
	}
	if w.Applied < 0 || w.Applied > 3 || w.Applied > 0 && !w.Observed || (windowsCommitDecided(w) || w.Phase == "participants_applied") && (!w.Observed || w.Applied != 3) {
		return fmt.Errorf("invalid Windows participant progress")
	}
	return nil
}
func validateWindowsChange(w *WindowsChange, root string) error {
	if e := validateWindowsPayload(w); e != nil {
		return e
	}
	if w == nil {
		return nil
	}
	return validateWindowsRoot(w, root)
}
func (w *WindowsChange) UnmarshalJSON(data []byte) error {
	type plain WindowsChange
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&value); e != nil {
		return e
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing Windows participant")
	}
	*w = WindowsChange(value)
	return nil
}

func windowsCommitDecided(w *WindowsChange) bool {
	return w != nil && (w.Phase == "commit_decided" || w.Phase == "binding_published" || w.Phase == "committed")
}
func advanceWindows(ctx context.Context, root string, tx *transaction, fault func(string) error) error {
	if tx.Windows == nil {
		return nil
	}
	w := tx.Windows
	if w.End == 0 {
		w.End = windowsGenerationDeadline(ctx)
	}
	if e := validateWindowsChange(w, root); e != nil {
		return e
	}
	save := func(phase string) error {
		if e := writeTransaction(filepath.Join(root, "transaction.json"), *tx); e != nil {
			return e
		}
		if fault != nil {
			return fault("windows:" + phase)
		}
		return nil
	}
	if w.Phase == "rollback_decided" {
		if !tx.Rollback {
			return fmt.Errorf("Windows reverse decision requires reverse owner journal")
		}
		if !w.Observed {
			return nil
		}
		if e := ensureWindowsGeneration(ctx, w); e != nil {
			return e
		}
		return restoreWindowsGeneration(ctx, w)
	}
	if e := ensureWindowsGeneration(ctx, w); e != nil {
		return e
	}
	if !w.Observed {
		if e := windowsGenerationOperation(ctx, w, "observe"); e != nil {
			return e
		}
		w.Observed = true
		if e := save("observed_absent_preimages"); e != nil {
			return e
		}
	}
	if windowsCommitDecided(w) {
		return windowsGenerationOperation(ctx, w, "readback")
	}
	modes := []string{"apply-clsid", "apply-aumid", "apply-shortcut"}
	for i, mode := range modes {
		// The journal's absent preimage+intent precedes each exclusive mutation.
		// Readback/reapply also verifies crash-after-mutation before completion.
		if w.Applied < i+1 {
			if e := save("intent:" + mode); e != nil {
				return e
			}
		}
		if w.Applied >= i+1 {
			mode = "verify-" + mode[len("apply-"):]
		}
		if e := windowsGenerationOperation(ctx, w, mode); e != nil {
			return e
		}
		if fault != nil {
			if e := fault("windows:mutated:" + mode); e != nil {
				return e
			}
		}
		if w.Applied < i+1 {
			w.Applied = i + 1
			if e := save("applied:" + mode); e != nil {
				return e
			}
		}
	}
	if e := windowsGenerationOperation(ctx, w, "readback"); e != nil {
		return e
	}
	if !windowsCommitDecided(w) {
		w.Phase = "participants_applied"
		if e := save(w.Phase); e != nil {
			return e
		}
		w.Phase = "commit_decided"
		if e := save(w.Phase); e != nil {
			return e
		}
	}
	return nil
}

func validateWindowsRetained(l Ledger) error {
	if len(l.WindowsRetained) > windowscallback.MaxGenerations {
		return windowscallback.ErrCapacity
	}
	if len(l.WindowsRetained) > 0 && (l.Schema != 5 || l.WriterFloor < WindowsGenerationWriterFloor) {
		return windowscallback.ErrUnavailable
	}
	seen := map[string]bool{}
	for _, b := range l.WindowsRetained {
		if !windowscallback.ValidBinding(b) || seen[b.SnapshotPath] {
			return windowscallback.ErrUnavailable
		}
		seen[b.SnapshotPath] = true
	}
	return nil
}
