//go:build !linux && !darwin && !windows

package notifier

import (
	"context"
	"errors"
)

func (*Notifier) sendNativeDesktopContext(context.Context, string, string, string, string, string, string, string, sendOptions) error {
	return errors.New("unsupported_platform")
}
func hasNativeTerminal(context.Context) bool                       { return false }
func desktopDNDContext(context.Context) bool                       { return false }
func terminalHasFocusContext(context.Context, string, string) bool { return false }
