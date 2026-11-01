//go:build !windows

package winfocus

import (
	"context"
	"errors"
)

// errWindowsOnly is returned by the Win32-backed operations on non-Windows
// builds. The pure URI codec in winfocus.go remains available everywhere so it
// can be unit-tested cross-platform.
var errWindowsOnly = errors.New("winfocus: only supported on windows")

// CaptureFocusContext is a no-op on non-Windows platforms.
func CaptureFocusContext(cwd string) (FocusContext, bool)         { return FocusContext{}, false }
func CaptureContext(context.Context, string) (FocusContext, bool) { return FocusContext{}, false }

// Focus is a no-op on non-Windows platforms.
func Focus(ctx FocusContext) error { return errWindowsOnly }

// EnsureRegistered is a no-op on non-Windows platforms.
func EnsureRegistered() error { return errWindowsOnly }

// HideConsole is a no-op on non-Windows platforms.
func HideConsole() {}
