package config

import (
	"fmt"
	"sort"

	"github.com/777genius/agent-notifications/internal/desktoptemplate"
)

// DesktopTemplates holds the desktop templates that apply to one status.
// A nil field means "not configured": that component keeps its default layout.
type DesktopTemplates struct {
	Title, Header, Body *string
}

// Any reports whether at least one template is configured.
func (t DesktopTemplates) Any() bool {
	return t.Title != nil || t.Header != nil || t.Body != nil
}

// References reports whether any configured template uses {name}.
func (t DesktopTemplates) References(name string) bool {
	for _, tmpl := range []*string{t.Title, t.Header, t.Body} {
		if tmpl != nil && desktoptemplate.References(*tmpl, name) {
			return true
		}
	}
	return false
}

// DesktopTemplatesFor resolves the templates for status: a per-status
// template overrides the notifications.desktop one, field by field.
func (c *Config) DesktopTemplatesFor(status string) DesktopTemplates {
	if c == nil {
		return DesktopTemplates{}
	}
	desktop := c.Notifications.Desktop
	t := DesktopTemplates{Title: desktop.TitleTemplate, Header: desktop.HeaderTemplate, Body: desktop.BodyTemplate}
	if info, ok := c.Statuses[status]; ok {
		if info.TitleTemplate != nil {
			t.Title = info.TitleTemplate
		}
		if info.HeaderTemplate != nil {
			t.Header = info.HeaderTemplate
		}
		if info.BodyTemplate != nil {
			t.Body = info.BodyTemplate
		}
	}
	return t
}

// AnyDesktopTemplateReferences reports whether any global or per-status
// desktop template uses {name}, for lookups done before the status is known.
func (c *Config) AnyDesktopTemplateReferences(name string) bool {
	if c == nil {
		return false
	}
	d := c.Notifications.Desktop
	if (DesktopTemplates{Title: d.TitleTemplate, Header: d.HeaderTemplate, Body: d.BodyTemplate}).References(name) {
		return true
	}
	for _, info := range c.Statuses {
		if (DesktopTemplates{Title: info.TitleTemplate, Header: info.HeaderTemplate, Body: info.BodyTemplate}).References(name) {
			return true
		}
	}
	return false
}

func validateDesktopTemplates(prefix string, title, header, body *string) error {
	for _, field := range []struct {
		name     string
		template *string
		kind     desktoptemplate.Kind
	}{
		{"titleTemplate", title, desktoptemplate.Title},
		{"headerTemplate", header, desktoptemplate.Header},
		{"bodyTemplate", body, desktoptemplate.Body},
	} {
		if field.template == nil {
			continue
		}
		if err := desktoptemplate.Validate(*field.template, field.kind); err != nil {
			return fmt.Errorf("%s.%s: %w", prefix, field.name, err)
		}
	}
	return nil
}

func (c *Config) validateDesktopTemplates() error {
	d := c.Notifications.Desktop
	if err := validateDesktopTemplates("notifications.desktop", d.TitleTemplate, d.HeaderTemplate, d.BodyTemplate); err != nil {
		return err
	}
	statuses := make([]string, 0, len(c.Statuses))
	for status := range c.Statuses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses) // deterministic error for multiple bad statuses
	for _, status := range statuses {
		info := c.Statuses[status]
		if err := validateDesktopTemplates("statuses."+status, info.TitleTemplate, info.HeaderTemplate, info.BodyTemplate); err != nil {
			return err
		}
	}
	return nil
}
