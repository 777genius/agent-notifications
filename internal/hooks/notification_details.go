package hooks

import (
	"github.com/777genius/agent-notifications/internal/desktoptemplate"
	"github.com/777genius/agent-notifications/internal/sessionname"
)

type hookNotificationDetails struct {
	title         string
	question      string
	branch        string
	branchFetched bool // an empty captured branch is still a completed lookup
	// templateName is the native session name for {sessionName} when
	// showSessionLabel is off. It never changes the default layout.
	templateName string
}

func (h *Handler) notificationDetails(ev Event, insight *TurnInsight) hookNotificationDetails {
	var details hookNotificationDetails
	if insight != nil {
		details.question = insight.Question
	}
	labels := h.cfg.IsSessionLabelEnabled()
	if labels || h.cfg.AnyDesktopTemplateReferences(desktoptemplate.SessionName) {
		name := sessionname.CleanNativeTitle(ev.Session.Title)
		if name == "" {
			name = sessionname.NativeTitle(string(ev.Product), ev.Session.SessionID, ev.Session.TranscriptPath)
		}
		if labels {
			details.title = name
		} else {
			details.templateName = name
		}
	}
	return details
}
