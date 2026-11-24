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
// when no earlier placeholder was rendered. A matching bracket pair ((), [],
// {} or <>) around one or more placeholders forms a group that renders like a
// single placeholder: it keeps both brackets while any placeholder inside it
// renders, and it is dropped whole, with one adjacent separator, when all of
// them are empty. So "{statusTitle} [{sessionName}]" renders just the status
// title when there is no session name, and "[{folder} {branch}]" renders
// "[project]" outside a git repository. Brackets that do not enclose a
// placeholder, or have no partner, stay literal. Values are inserted
// literally. Unknown placeholders render empty; Validate rejects them before
// a template reaches Render.
func Render(template string, values map[string]string) string {
	out, _ := renderItems(parse(template), values)
	return out
}

const (
	openBrackets  = "([{<"
	closeBrackets = ")]}>"
)

// item is literal text, a placeholder, or a bracket group that encloses at
// least one placeholder.
type item struct {
	text        string
	placeholder string
	group       *group
}

type group struct {
	open, close string
	items       []item
}

func (it item) slot() bool { return it.placeholder != "" || it.group != nil }

// parse splits template into items, pairing brackets with a stack. A pair is a
// group only when it encloses a placeholder; otherwise it is literal text.
func parse(template string) []item {
	type frame struct {
		open  byte
		items []item
		slots bool
	}
	stack := []*frame{{}}
	appendText := func(f *frame, text string) {
		if n := len(f.items); n > 0 && !f.items[n-1].slot() {
			f.items[n-1].text += text
			return
		}
		f.items = append(f.items, item{text: text})
	}
	// flatten moves an unpaired frame's content into its parent as literal
	// opening text followed by its items.
	flatten := func(parent, f *frame, closing string) {
		appendText(parent, string(f.open))
		for _, it := range f.items {
			if it.slot() {
				parent.items = append(parent.items, it)
				parent.slots = true
			} else {
				appendText(parent, it.text)
			}
		}
		if closing != "" {
			appendText(parent, closing)
		}
	}
	matches := placeholderPattern.FindAllStringSubmatchIndex(template, -1)
	pos := 0
	for i := 0; i <= len(matches); i++ {
		end := len(template)
		if i < len(matches) {
			end = matches[i][0]
		}
		for ; pos < end; pos++ {
			c := template[pos]
			top := stack[len(stack)-1]
			switch {
			case strings.IndexByte(openBrackets, c) >= 0:
				stack = append(stack, &frame{open: c})
			case len(stack) > 1 && strings.IndexByte(closeBrackets, c) >= 0 &&
				strings.IndexByte(openBrackets, top.open) == strings.IndexByte(closeBrackets, c):
				stack = stack[:len(stack)-1]
				parent := stack[len(stack)-1]
				if top.slots {
					parent.items = append(parent.items, item{group: &group{open: string(top.open), close: string(c), items: top.items}})
					parent.slots = true
				} else {
					flatten(parent, top, string(c))
				}
			default:
				appendText(top, template[pos:pos+1])
			}
		}
		if i < len(matches) {
			top := stack[len(stack)-1]
			top.items = append(top.items, item{placeholder: template[matches[i][2]:matches[i][3]]})
			top.slots = true
			pos = matches[i][1]
		}
	}
	for len(stack) > 1 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		flatten(stack[len(stack)-1], top, "")
	}
	return stack[0].items
}

// renderItems renders a sequence and reports whether any placeholder in it
// rendered a value.
func renderItems(items []item, values map[string]string) (string, bool) {
	first, last := -1, -1
	for i, it := range items {
		if it.slot() {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	var out strings.Builder
	if first < 0 {
		for _, it := range items {
			out.WriteString(it.text)
		}
		return out.String(), false
	}
	for _, it := range items[:first] {
		out.WriteString(it.text)
	}
	rendered := false
	sep := ""
	for _, it := range items[first : last+1] {
		if !it.slot() {
			sep += it.text
			continue
		}
		value := ""
		if it.group != nil {
			if inner, ok := renderItems(it.group.items, values); ok {
				value = it.group.open + inner + it.group.close
			}
		} else {
			value = values[it.placeholder]
		}
		if value != "" {
			if rendered {
				out.WriteString(sep)
			}
			out.WriteString(value)
			rendered = true
		}
		sep = ""
	}
	for _, it := range items[last+1:] {
		out.WriteString(it.text)
	}
	return out.String(), rendered
}
