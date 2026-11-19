// Package desktoptemplate renders the optional desktop notification templates
// (notifications.desktop.titleTemplate, headerTemplate and bodyTemplate).
//
// A template is literal text with {name} placeholders. A placeholder that
// renders empty is dropped together with one adjacent separator, so
// "{branch} · {folder}" outside a git repository renders just the folder.
// Text in braces that is not a {name} placeholder (for example "{ x }" or a
// lone "{") stays literal; an unknown {name} is a configuration error.
package desktoptemplate

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind identifies which notification component a template renders.
type Kind int

const (
	// Title renders the notification headline.
	Title Kind = iota
	// Header renders the context line: the native macOS subtitle, or the
	// first body line on backends without a subtitle.
	Header
	// Body renders the notification body.
	Body
)

// Placeholder names. Header is valid only in body templates.
const (
	StatusTitle = "statusTitle"
	Status      = "status"
	SessionName = "sessionName"
	SessionID   = "sessionId"
	Folder      = "folder"
	Repo        = "repo"
	Branch      = "branch"
	Question    = "question"
	BodyText    = "body"
	Actions     = "actions"
	Agent       = "agent"
	HeaderText  = "header"
)

var known = map[string]bool{
	StatusTitle: true, Status: true, SessionName: true, SessionID: true,
	Folder: true, Repo: true, Branch: true, Question: true,
	BodyText: true, Actions: true, Agent: true, HeaderText: true,
}

var placeholderPattern = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9]*)\}`)

// Validate rejects unknown placeholders, and {header} outside a body template.
func Validate(template string, kind Kind) error {
	for _, m := range placeholderPattern.FindAllStringSubmatch(template, -1) {
		name := m[1]
		if !known[name] {
			return fmt.Errorf("unknown placeholder {%s}", name)
		}
		if name == HeaderText && kind != Body {
			return fmt.Errorf("placeholder {header} is only valid in bodyTemplate")
		}
	}
	return nil
}

// References reports whether template contains the placeholder {name}.
func References(template, name string) bool {
	return strings.Contains(template, "{"+name+"}")
}

// Render substitutes values into template.
//
// The text before the first placeholder and after the last one is kept, and
// the text between two placeholders is a separator. An empty placeholder is
// dropped together with the separator before it, or the separator after it
// when no earlier placeholder was rendered. Opening brackets directly before a
// placeholder and closing brackets directly after it wrap that placeholder and
// are dropped with it, so "{statusTitle} [{sessionName}]" renders just the
// status title when there is no session name. Values are inserted literally.
// Unknown placeholders render empty; Validate rejects them before a template
// reaches Render.
func Render(template string, values map[string]string) string {
	matches := placeholderPattern.FindAllStringSubmatchIndex(template, -1)
	if len(matches) == 0 {
		return template
	}
	type slot struct{ open, value, close, sep string }
	slots := make([]slot, len(matches))
	prefix := template[:matches[0][0]]
	suffix := template[matches[len(matches)-1][1]:]
	for i, m := range matches {
		slots[i].value = values[template[m[2]:m[3]]]
		if i == 0 {
			prefix, slots[i].open = splitTrailing(prefix, openBrackets)
		} else {
			between := template[matches[i-1][1]:m[0]]
			slots[i-1].close, between = splitLeading(between, closeBrackets)
			slots[i].sep, slots[i].open = splitTrailing(between, openBrackets)
		}
	}
	slots[len(slots)-1].close, suffix = splitLeading(suffix, closeBrackets)

	var out strings.Builder
	out.WriteString(prefix)
	rendered := false
	for _, s := range slots {
		if s.value == "" {
			continue
		}
		if rendered {
			out.WriteString(s.sep)
		}
		out.WriteString(s.open + s.value + s.close)
		rendered = true
	}
	out.WriteString(suffix)
	return out.String()
}

const (
	openBrackets  = "([{<"
	closeBrackets = ")]}>"
)

// splitLeading splits s after its leading run of characters from set.
func splitLeading(s, set string) (run, rest string) {
	i := 0
	for i < len(s) && strings.IndexByte(set, s[i]) >= 0 {
		i++
	}
	return s[:i], s[i:]
}

// splitTrailing splits s before its trailing run of characters from set.
func splitTrailing(s, set string) (rest, run string) {
	i := len(s)
	for i > 0 && strings.IndexByte(set, s[i-1]) >= 0 {
		i--
	}
	return s[:i], s[i:]
}
