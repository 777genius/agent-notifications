//go:build !windows

package installruntime

import (
	"context"

	"github.com/777genius/agent-notifications/internal/windowscallback"
)

func validateWindowsRoot(*WindowsChange, string) error { return windowscallback.ErrUnavailable }
func ensureWindowsGeneration(context.Context, *WindowsChange) error {
	return windowscallback.ErrUnavailable
}
func windowsGenerationOperation(context.Context, *WindowsChange, string) error {
	return windowscallback.ErrUnavailable
}
func restoreWindowsGeneration(context.Context, *WindowsChange) error {
	return windowscallback.ErrUnavailable
}

func windowsGenerationDeadline(context.Context) uint64 { return 0 }
