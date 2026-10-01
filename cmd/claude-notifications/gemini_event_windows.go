//go:build windows

package main

import (
	"context"
	"github.com/777genius/agent-notifications/internal/geminiinstall"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func newGeminiDesktopPort(gate geminiGate) notification.DeliveryPort {
	port := notifier.NewTrustedWindowsToastDelivery(notifier.SystemBootClock{}, geminiinstall.GeminiToastAppID, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return geminiinstall.WindowsShortcutReady(gate.args.ControlRoot, gate.executable)
	})
	return geminiLeasedDesktop{gate: gate, port: port}
}
