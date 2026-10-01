//go:build darwin

package main

import (
	"github.com/777genius/agent-notifications/internal/geminiinstall"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"path/filepath"
)

func newGeminiDesktopPort(gate geminiGate) notification.DeliveryPort {
	return notifier.NewStructuredDelivery(geminiinstall.NativeInstallation{ControlRoot: gate.args.ControlRoot, Executable: gate.executable, Binding: gate.args.Binding, Expected: gate.expected}, filepath.Join(gate.args.ControlRoot, "gemini-native-spool"))
}
