//go:build windows

package installruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/windowscallback"
)

func validateWindowsRoot(w *WindowsChange, root string) error {
	expected := filepath.Join(`\\?\`+strings.TrimPrefix(root, `\\?\`), "windows-callback", w.Snapshot.Generation)
	if w.Snapshot.CanonicalRoot != expected {
		return windowscallback.ErrUnavailable
	}
	return nil
}
func StageWindowsGeneration(ctx context.Context, root string) (*WindowsChange, error) {
	end := windowscallback.Deadline(ctx)
	s, b, e := windowscallback.Plan(ctx, root, end)
	if e != nil {
		return nil, e
	}
	return &WindowsChange{Snapshot: s, Binding: b, Phase: "preparing", End: end}, nil
}
func ensureWindowsGeneration(ctx context.Context, w *WindowsChange) error {
	end := w.End
	g, e := windowscallback.Open(ctx, w.Binding, end)
	if e == nil {
		return g.Close()
	}
	if errors.Is(e, windowscallback.ErrUnknown) || windowsCommitDecided(w) {
		return e
	}
	if e = windowscallback.Bootstrap(ctx, w.Snapshot, end); e != nil {
		return e
	}
	g, e = windowscallback.Open(ctx, w.Binding, end)
	if e == nil {
		return g.Close()
	}
	return e
}
func windowsGenerationOperation(ctx context.Context, w *WindowsChange, mode string) error {
	end := w.End
	g, e := windowscallback.Open(ctx, w.Binding, end)
	if e != nil {
		return e
	}
	_, e = g.Operator(ctx, mode, nil, end)
	return errors.Join(e, g.Close())
}
func restoreWindowsGeneration(ctx context.Context, w *WindowsChange) error {
	for _, mode := range []string{"restore-shortcut", "restore-aumid", "restore-clsid"} {
		if e := windowsGenerationOperation(ctx, w, mode); e != nil {
			return e
		}
	}
	return nil
}

func windowsGenerationDeadline(ctx context.Context) uint64 { return windowscallback.Deadline(ctx) }
