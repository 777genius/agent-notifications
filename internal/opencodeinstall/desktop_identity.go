package opencodeinstall

import "github.com/777genius/agent-notifications/internal/desktopinstall"

// Compatibility aliases retain the installer entry points used by existing callers.
type DesktopProduct = desktopinstall.DesktopProduct

const (
	OpenCodeDesktop         = desktopinstall.OpenCodeDesktop
	GeminiDesktop           = desktopinstall.GeminiDesktop
	CopilotVSCodeDesktop    = desktopinstall.CopilotVSCodeDesktop
	CursorDesktop           = desktopinstall.CursorDesktop
	OpenCodeToastAppID      = desktopinstall.OpenCodeToastAppID
	GeminiToastAppID        = desktopinstall.GeminiToastAppID
	CopilotVSCodeToastAppID = desktopinstall.CopilotVSCodeToastAppID
	CursorToastAppID        = desktopinstall.CursorToastAppID
	GeminiConsumerID        = desktopinstall.GeminiConsumerID
)
