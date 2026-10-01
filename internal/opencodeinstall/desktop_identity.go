package opencodeinstall

// DesktopProduct selects one of the two installer-owned observer identities.
// Config and native event data cannot supply an AUMID, shortcut or namespace.
type DesktopProduct uint8

const (
	OpenCodeDesktop DesktopProduct = iota + 1
	GeminiDesktop
	OpenCodeToastAppID = "Genius.AgentNotifications.OpenCode"
	GeminiToastAppID   = "Genius.AgentNotifications.Gemini"
	GeminiConsumerID   = "gemini-notifications"
)
