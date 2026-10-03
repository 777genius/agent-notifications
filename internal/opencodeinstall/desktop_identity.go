package opencodeinstall

// DesktopProduct selects an installer-owned observer identity.
// Config and native event data cannot supply an AUMID, shortcut or namespace.
type DesktopProduct uint8

const (
	OpenCodeDesktop DesktopProduct = iota + 1
	GeminiDesktop
	CopilotVSCodeDesktop
	OpenCodeToastAppID      = "Genius.AgentNotifications.OpenCode"
	GeminiToastAppID        = "Genius.AgentNotifications.Gemini"
	CopilotVSCodeToastAppID = "Genius.AgentNotifications.CopilotVSCode"
	GeminiConsumerID        = "gemini-notifications"
)
