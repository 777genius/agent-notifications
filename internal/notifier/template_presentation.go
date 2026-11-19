package notifier

import (
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/desktoptemplate"
	"github.com/777genius/agent-notifications/internal/notification"
)

// TemplateData carries the literal placeholder values for desktop templates.
// It affects presentation only, never routing, focus or consent. When given,
// it is the complete value set: an empty field renders as an empty placeholder.
type TemplateData struct {
	SessionName string
	SessionID   string
	Folder      string
	Repo        string
	Branch      string
	Question    string
	Body        string // summary text without the action segment
	Actions     string
	Agent       string
}

// WithTemplateData supplies placeholder values for configured desktop templates.
func WithTemplateData(data TemplateData) SendOption {
	return func(o *sendOptions) { o.templateData = &data }
}

const templateLineLimit = 100

// desktopPresentation resolves title, subtitle and body for one delivery.
// structuredSubtitle reports whether backends without a native subtitle put
// the subtitle on the first body line. Without configured templates this is
// exactly the historical hook/legacy presentation.
func (n *Notifier) desktopPresentation(status analyzer.Status, message, statusTitle string, opts sendOptions) (presentation legacyDesktopPresentation, structuredSubtitle bool) {
	presentation = legacyPresentation(status, message, statusTitle, n.cfg.IsSessionLabelEnabled())
	if opts.hookPresentation != nil {
		presentation = hookPresentation(status, *opts.hookPresentation, statusTitle, n.cfg.IsSessionLabelEnabled())
	}
	structuredSubtitle = opts.hookPresentation != nil
	if templates := n.cfg.DesktopTemplatesFor(string(status)); templates.Any() {
		values := templatePlaceholderValues(status, statusTitle, message, opts)
		presentation.Content = applyDesktopTemplates(templates, values, presentation.Content)
		// An explicit headerTemplate is always shown, also on the legacy path.
		structuredSubtitle = structuredSubtitle || templates.Header != nil
	}
	return presentation, structuredSubtitle
}

// templatePlaceholderValues builds the placeholder map. Explicit template
// data is authoritative; without it, the hook presentation (or the legacy
// bracket envelope) supplies what it has.
func templatePlaceholderValues(status analyzer.Status, statusTitle, legacyMessage string, opts sendOptions) map[string]string {
	var data TemplateData
	switch {
	case opts.templateData != nil:
		data = *opts.templateData
	case opts.hookPresentation != nil:
		hp := opts.hookPresentation
		data = TemplateData{SessionName: hp.SessionName, Folder: hp.Folder, Branch: hp.Branch, Question: hp.Question, Body: hp.Body}
	default:
		sessionName, branchFolder, body := extractSessionInfo(legacyMessage)
		data = TemplateData{SessionName: sessionName, Body: body}
		if parts := strings.SplitN(branchFolder, " ", 2); len(parts) == 2 {
			data.Branch, data.Folder = parts[0], parts[1]
		} else {
			data.Folder = branchFolder
		}
	}
	if data.Folder == "." {
		data.Folder = ""
	}
	return map[string]string{
		desktoptemplate.StatusTitle: statusTitle,
		desktoptemplate.Status:      string(status),
		desktoptemplate.SessionName: shortenTitle(data.SessionName, templateLineLimit),
		desktoptemplate.SessionID:   data.SessionID,
		desktoptemplate.Folder:      data.Folder,
		desktoptemplate.Repo:        data.Repo,
		desktoptemplate.Branch:      data.Branch,
		desktoptemplate.Question:    data.Question,
		desktoptemplate.BodyText:    data.Body,
		desktoptemplate.Actions:     data.Actions,
		desktoptemplate.Agent:       data.Agent,
	}
}

// applyDesktopTemplates replaces the configured components of the default
// presentation. Unconfigured components keep their default text. Title and
// header stay single lines within the existing 100-rune limit; a title that
// renders empty keeps the default. A body that places {header} consumes the
// context line, so no backend shows it twice.
func applyDesktopTemplates(templates config.DesktopTemplates, values map[string]string, content notification.Content) notification.Content {
	if templates.Title != nil {
		if title := shortenTitle(desktoptemplate.Render(*templates.Title, values), templateLineLimit); title != "" {
			content.Title = title
		}
	}
	if templates.Header != nil {
		content.Subtitle = shortenTitle(desktoptemplate.Render(*templates.Header, values), templateLineLimit)
	}
	if templates.Body != nil {
		values[desktoptemplate.HeaderText] = content.Subtitle
		content.Body = desktoptemplate.Render(*templates.Body, values)
		if desktoptemplate.References(*templates.Body, desktoptemplate.HeaderText) {
			content.Subtitle = ""
		}
	}
	return content
}
