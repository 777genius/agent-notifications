//go:build windows

package notifier

import (
	"context"
	"os"

	"github.com/777genius/agent-notifications/internal/winfocus"
)

var captureLocalWindowsTerminal = winfocus.CaptureContext

func (n *Notifier) sendNativeDesktopContext(ctx context.Context, sound, title, body, subtitle, _, cwd, icon string, opts sendOptions) error {
	if subtitle != "" {
		body = subtitle + "\n" + body
	}
	toast := windowsToastPayload{AppID: windowsToastAppID, Title: title, Body: body, Icon: icon, Silent: true}
	if n.cfg.Notifications.Desktop.ClickToFocus {
		if err := winfocus.EnsureRegistered(); err != nil {
			return err
		}
		if focus, ok := winfocus.CaptureContext(ctx, cwd); ok && focus.HasTarget() {
			toast.ActivationType, toast.ActivationArguments = "protocol", focus.EncodeURI()
		}
	}
	if err := submitWindowsToast(ctx, toast); err != nil {
		return err
	}
	return n.playSoundContext(ctx, sound, opts)
}
func hasNativeTerminal(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if os.Getenv("WT_SESSION") != "" {
		return true
	}
	focus, ok := captureLocalWindowsTerminal(ctx, "")
	return ok && focus.HWND != 0 && focus.PID != 0
}
func desktopDNDContext(context.Context) bool { return false }
func terminalHasFocusContext(ctx context.Context, session, cwd string) bool {
	return ctx.Err() == nil && terminalHasFocus(session, cwd)
}
