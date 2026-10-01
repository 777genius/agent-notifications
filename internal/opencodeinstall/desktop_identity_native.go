//go:build darwin || windows

package opencodeinstall

import "errors"

const shortcutName = "OpenCode Notifications.lnk"

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
