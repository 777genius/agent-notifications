//go:build windows

package notifier

import (
	"context"
	"os"

	"github.com/777genius/agent-notifications/internal/winfocus"
)

var captureLocalWindowsTerminal = winfocus.CaptureContext

func (n *Notifier) sendNativeDesktopContext(ctx context.Context, sound, title, body, subtitle, _, cwd, icon string, opts sendOptions) error {
	traceTestDesktopPhase(ctx, testNativeStarted)
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
	traceTestDesktopPhase(ctx, testFocusReady)
	traceTestDesktopPhase(ctx, testToastStarted)
	err := submitWindowsToast(ctx, toast)
	traceTestDesktopPhase(ctx, testToastReturned)
	if err != nil {
		return err
	}
	traceTestDesktopPhase(ctx, testSoundStarted)
	err = n.playSoundContext(ctx, sound, opts)
	traceTestDesktopPhase(ctx, testSoundReturned)
	return err
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
