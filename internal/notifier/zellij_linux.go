//go:build linux

// ABOUTME: Fills the zellij half of the focus hints the Linux daemon acts on.
// ABOUTME: Linux-only because FocusHints is the daemon's IPC payload, and the daemon is Linux-only.
package notifier

import (
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/logging"
)

func applyZellijFocusHints(cfg *config.Config, hints *daemon.FocusHints) {
	hints.ZellijSession, hints.ZellijPaneID = daemon.GetZellijFocusHints()
	if hints.ZellijSession == "" {
		return
	}

	hints.ZellijMode = resolveZellijFocusMode(cfg.Notifications.Desktop.ZellijFocus)
	if hints.ZellijMode != daemon.ZellijFocusModeTab {
		return
	}

	// dump-layout costs an exec the pane path never pays, so the tab name is
	// read only once the tab action is settled on.
	tabName, _, err := GetZellijTabTarget()
	if err != nil {
		// An action with no target fails at click time and is logged as a click
		// failure; decline it here instead.
		logging.Debug("zellij tab focus selected but tab name unavailable: %v", err)
		hints.ZellijMode = daemon.ZellijFocusModeOff
		return
	}
	hints.ZellijTabName = tabName
}
