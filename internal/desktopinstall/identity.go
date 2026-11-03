// Package desktopinstall owns fixed native notification identities and their
// installer-owned platform assets. Registration and channel policy stay with
// each integration; these operations never acquire a delivery lease.
package desktopinstall

// DesktopProduct selects an installer-owned observer identity.
// Config and native event data cannot supply an AUMID, shortcut or namespace.
type DesktopProduct uint8

const (
	OpenCodeDesktop DesktopProduct = iota + 1
	GeminiDesktop
	CopilotVSCodeDesktop
	CursorDesktop
	OpenCodeToastAppID      = "Genius.AgentNotifications.OpenCode"
	GeminiToastAppID        = "Genius.AgentNotifications.Gemini"
	CopilotVSCodeToastAppID = "Genius.AgentNotifications.CopilotVSCode"
	CursorToastAppID        = "Genius.AgentNotifications.Cursor"
	OpenCodeConsumerID      = "opencode-notifications"
	GeminiConsumerID        = "gemini-notifications"
)
