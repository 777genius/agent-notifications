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
	// Format: "✅ [peak]" or "✅ Completed"
	title := statusTitle
	if sessionName != "" && sessionLabel {
		title = appendSessionLabel(status, title, sessionName)
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

func hookPresentation(status analyzer.Status, content HookPresentation, statusTitle string, sessionLabel bool) legacyDesktopPresentation {
	title := statusTitle
	subtitle := joinContext(content.Branch, content.Folder)
	if content.Question != "" && status == analyzer.StatusQuestion {
		question := shortenTitle(content.Question, 80)
		switch statusTitle {
		case "❓ Question":
			title = "❓ " + question
		case "Question", "":
			title = question
		default:
			title += ": " + question
		}
		subtitle = content.Folder
		if sessionLabel {
			subtitle = joinContext(shortenTitle(content.SessionName, 100), content.Folder)
		}
	} else if sessionLabel && content.SessionName != "" {
		title = appendSessionLabel(status, title, shortenTitle(content.SessionName, 100))
	}
	return legacyDesktopPresentation{
		Content:       notification.Content{Title: title, Subtitle: subtitle, Body: content.Body},
		TimeSensitive: isTimeSensitiveStatus(status),
	}
}

// HookDesktopContent shares literal hook presentation with trusted integrations.
// Delivery policy, consent and routing remain the caller's responsibility.
func HookDesktopContent(status analyzer.Status, content HookPresentation, statusTitle string, sessionLabel bool) notification.Content {
	return hookPresentation(status, content, statusTitle, sessionLabel).Content
}

// Platforms without a native subtitle keep literal context in the body.
func desktopBodyWithSubtitle(content notification.Content) string {
	if content.Subtitle == "" {
		return content.Body
	}
	if content.Body == "" {
		return content.Subtitle
	}
	return content.Subtitle + "\n" + content.Body
}

// appendSessionLabel keeps custom status titles intact while avoiding a redundant
// default completion word when the session name already identifies the task.
func appendSessionLabel(status analyzer.Status, statusTitle, sessionName string) string {
	if status == analyzer.StatusTaskComplete && statusTitle == "✅ Completed" && strings.TrimSpace(sessionName) != "" {
		statusTitle = "✅"
	}
	return statusTitle + " [" + sessionName + "]"
}

func joinContext(first, second string) string {
	if second == "." {
		second = ""
	}
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + " · " + second
}

func shortenTitle(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return string(runes)
}
