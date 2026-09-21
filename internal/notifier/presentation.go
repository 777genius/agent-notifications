package notifier

import (
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/notification"
)

// legacyPresentation is the only presentation boundary that parses hook labels
// and translates analyzer status. Explicit delivery accepts literal content.
type legacyDesktopPresentation struct {
	notification.Content
	TimeSensitive bool
}

func legacyPresentation(status analyzer.Status, message, statusTitle string, sessionLabel bool) legacyDesktopPresentation {
	// Extract session name, git branch and folder name from message
	// Format: "[session-name|branch folder] actual message" or "[session-name folder] actual message"
	sessionName, gitBranch, cleanMessage := extractSessionInfo(message)

	// Build clean title (status only + session name)
	// Format: "✅ Completed [peak]" or "✅ Completed"
	title := statusTitle
	if sessionName != "" && sessionLabel {
		title = fmt.Sprintf("%s [%s]", title, sessionName)
	}

	// Build subtitle from branch and folder name
	// Format: "main · notification_plugin_go" or just folder name
	var subtitle string
	if gitBranch != "" {
		// gitBranch may contain "branch folder" (space-separated from hooks.go format)
		parts := strings.SplitN(gitBranch, " ", 2)
		if len(parts) == 2 {
			subtitle = fmt.Sprintf("%s \u00B7 %s", parts[0], parts[1])
		} else {
			subtitle = gitBranch
		}
	}

	timeSensitive := isTimeSensitiveStatus(status)

	return legacyDesktopPresentation{Content: notification.Content{Title: title, Body: cleanMessage, Subtitle: subtitle}, TimeSensitive: timeSensitive}
}
