package hooks

import "github.com/777genius/agent-notifications/internal/sessionname"

type hookNotificationDetails struct {
	title         string
	question      string
	branch        string
	branchFetched bool // an empty captured branch is still a completed lookup
}

func (h *Handler) notificationDetails(ev Event, insight *TurnInsight) hookNotificationDetails {
	var details hookNotificationDetails
	if insight != nil {
		details.question = insight.Question
	}
	if h.cfg.IsSessionLabelEnabled() {
		details.title = sessionname.CleanNativeTitle(ev.Session.Title)
		if details.title == "" {
			details.title = sessionname.NativeTitle(string(ev.Product), ev.Session.SessionID, ev.Session.TranscriptPath)
		}
	}
	return details
}
