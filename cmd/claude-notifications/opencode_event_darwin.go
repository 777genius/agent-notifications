//go:build darwin

package main

import (
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
)

func newOpenCodeDesktopPort(h *opencodeevent.Handoff, root string) notification.DeliveryPort {
	// Parent's packaged Darwin clock cell must prove RAW/Mach coordinate equality.
	// No qualified product cell exists yet. The held native ref avoids reentry.
	return newOpenCodeHeldDesktop(h.NativeInstallation(), filepath.Join(root, "opencode-native-spool"))
}
