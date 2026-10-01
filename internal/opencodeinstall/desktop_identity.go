package opencodeinstall

import "errors"

// DesktopProduct selects one of the two installer-owned observer identities.
// Config and native event data cannot supply an AUMID, shortcut or namespace.
type DesktopProduct uint8

const (
	OpenCodeDesktop DesktopProduct = iota + 1
	GeminiDesktop
	OpenCodeToastAppID = "Genius.AgentNotifications.OpenCode"
	GeminiToastAppID   = "Genius.AgentNotifications.Gemini"
	GeminiConsumerID   = "gemini-notifications"
	shortcutName       = "OpenCode Notifications.lnk"
)

type desktopIdentity struct {
	consumer, label, appID, shortcut, spool string
}

func identityFor(product DesktopProduct) (desktopIdentity, error) {
	switch product {
	case OpenCodeDesktop:
		return desktopIdentity{consumerID, "OpenCode", OpenCodeToastAppID, shortcutName, "opencode-native-spool"}, nil
	case GeminiDesktop:
		return desktopIdentity{GeminiConsumerID, "Gemini", GeminiToastAppID, "Gemini Notifications.lnk", "gemini-native-spool"}, nil
	default:
		return desktopIdentity{}, errors.New("unknown notification desktop product")
	}
}
