package agentnotify

import (
	"crypto/sha256"
	"encoding/json"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

func validate(p Payload, o origin.Context) (Payload, string) {
	if p.Navigation == "" {
		p.Navigation = notification.Required
	}
	if p.Navigation != notification.Required && p.Navigation != notification.BestEffort && p.Navigation != notification.None {
		return p, "invalid_navigation"
	}
	if p.Category != "info" && p.Category != "attention" && p.Category != "progress" {
		return p, "invalid_category"
	}
	if !content(p.Title, 256, false) || !content(p.Body, 4096, true) {
		return p, "invalid_content"
	}
	if p.RequestID != nil && !origin.Text(*p.RequestID, 256, true) {
		return p, "invalid_request_id"
	}
	if e := o.Validate(p.Navigation); e != nil {
		return p, e.Error()
	}
	total := len(p.Title) + len(p.Body) + len(p.Category) + len(p.Navigation) + len(o.Provider) + len(o.Namespace) + len(o.SessionID) + len(o.AnonymousCaller) + len(o.CallID) + len(o.Provenance) + len(o.Locality) + len(o.Interface)
	if p.RequestID != nil {
		total += len(*p.RequestID)
	}
	if total > 16<<10 {
		return p, "request_too_large"
	}
	return p, ""
}
func content(s string, max int, body bool) bool {
	if len(s) > max || !utf8.ValidString(s) || (!body && s == "") {
		return false
	}
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) && (!body || (r != '\n' && r != '\t')) {
			return false
		}
		if !body && (r == '\u2028' || r == '\u2029') {
			return false
		}
	}
	return true
}
func digest(p Payload) journal.Digest {
	// Canonical typed representation: JSON escaping preserves exact scalar bytes;
	// transport duplicate-key and unpaired-surrogate checks precede this boundary.
	b, _ := json.Marshal(struct {
		Version               int
		Title, Body, Category string
		Navigation            notification.Navigation
	}{1, p.Title, p.Body, p.Category, p.Navigation})
	return sha256.Sum256(b)
}
