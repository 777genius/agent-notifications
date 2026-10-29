//go:build linux

package notifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/commandrun"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/warpfocus"
	"github.com/google/uuid"
)

var contextPlainLinuxDelivery = func(clock BootClock) notification.DeliveryPort { return NewFreedesktopDelivery(clock) }

func (n *Notifier) sendNativeDesktopContext(ctx context.Context, sound, title, body, subtitle, _, cwd, icon string, opts sendOptions) error {
	if subtitle != "" {
		body = subtitle + "\n" + body
	}
	if !n.cfg.Notifications.Desktop.ClickToFocus {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("delivery_deadline_required")
		}
		// The plain delivery protocol keeps its existing 15s horizon. Sound
		// still belongs to the caller's original overall notification budget.
		deliveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		clock := SystemBootClock{}
		boot, now, err := clock.Now()
		if err != nil {
			return err
		}
		end, _ := deliveryCtx.Deadline()
		remaining := time.Until(end)
		if remaining <= 0 || remaining > 15*time.Second {
			return errors.New("invalid_delivery_deadline")
		}
		r := contextPlainLinuxDelivery(clock).Deliver(deliveryCtx, notification.Request{CorrelationID: uuid.NewString(), Content: notification.Content{Title: title, Body: body, Category: "info"}, Deadline: notification.Deadline{BootID: boot, NotAfter: now + remaining.Seconds()}, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}, Navigation: notification.None, Silent: true})
		if r.Status != "submitted" {
			return errors.New("desktop_handoff_unconfirmed")
		}
		return n.playSoundContext(ctx, sound, opts)
	}
	if !daemon.StartDaemonOnDemandContext(ctx) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return daemon.ErrDaemonNotAvailable
	}
	client, err := daemon.NewClient()
	if err != nil {
		return err
	}
	hints := daemon.FocusHints{}
	if n.cfg.Notifications.Desktop.ClickToFocus {
		target := daemon.GetTerminalName()
		windowID := daemon.GetX11WindowID(target)
		if session := os.Getenv("XDG_SESSION_TYPE"); session != "" && session != "x11" {
			windowID = ""
		}
		pane, socket := daemon.GetWezTermFocusHints(target)
		hints = daemon.FocusHints{TerminalName: target, FolderName: daemon.GetFocusFolderName(target, cwd), ProjectPath: daemon.GetFocusProjectPath(target, cwd), IDEPID: daemon.GetFocusIDEPID(target), WindowID: windowID, WindowTitle: daemon.GetExactWindowTitleContext(ctx, target), WezTermPaneID: pane, WezTermSocket: socket, WarpFocusURL: warpfocus.FromEnvContext(ctx)}
		applyZellijFocusHintsContext(ctx, n.cfg, &hints)
	}
	response, err := client.SendNotificationContext(ctx, title, body, icon, hints, 30)
	if err != nil {
		return err
	}
	if response == nil || !response.Success || response.NotificationID == 0 {
		return errors.New("desktop_handoff_unconfirmed")
	}
	return n.playSoundContext(ctx, sound, opts)
}
func hasNativeTerminal(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	windowID, windowKnown := parseWindowID(os.Getenv("WINDOWID"))
	return (windowKnown && windowID != 0) || daemon.HasKnownTerminalContext()
}
func desktopDNDContext(ctx context.Context) bool { return desktopIsDoNotDisturbContext(ctx) }
func terminalHasFocusContext(parent context.Context, _, cwd string) bool {
	ctx, cancel := context.WithTimeout(parent, activeWindowQueryTimeout)
	defer cancel()
	if class, pid, ok := detectJetBrainsIDE(); ok {
		project := daemon.GetFocusProjectPath(class, cwd)
		if project == "" {
			return false
		}
		tool := "kdotool"
		if os.Getenv("XDG_SESSION_TYPE") == "x11" {
			tool = "xdotool"
		}
		out, err := commandrun.Output(ctx, exec.Command(tool, "getactivewindow", "getwindowpid", "getwindowname"))
		if err != nil {
			return false
		}
		window, err := parseWindowInfo(string(out))
		return err == nil && window.pid == pid && daemon.JetBrainsTitleMatches(window.title, daemon.GetFocusFolderName(class, cwd), project)
	}
	ours, ok := parseWindowID(os.Getenv("WINDOWID"))
	if !ok {
		return false
	}
	out, err := commandrun.Output(ctx, exec.Command("xdotool", "getactivewindow"))
	if err != nil {
		return false
	}
	active, ok := parseWindowID(strings.TrimSpace(string(out)))
	return ok && ours == active
}
