//go:build linux

package main

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func newGeminiDesktopPort(gate geminiGate) notification.DeliveryPort {
	return geminiLeasedDesktop{gate: gate, port: notifier.NewFreedesktopDelivery(notifier.SystemBootClock{})}
}
