package agentnotify

import (
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

func validationCaller() origin.Context {
	return origin.Context{Provider: "codex", Namespace: "local", SessionID: "A", Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.InterfaceUnknown}
}
func TestUnicodeAndLimits(t *testing.T) {
	for _, tt := range []struct {
		name, title, body string
		ok                bool
	}{
		{"literal", "--help -execute [important] $(echo) \"", "body", true},
		{"formats", "👩\u200d💻 می\u200cخواهم", "\U000e0067\U000e007f", true},
		{"title_boundary", strings.Repeat("é", 128), "", true},
		{"title_over", strings.Repeat("é", 128) + "a", "", false},
		{"body_boundary", "title", strings.Repeat("é", 2048), true},
		{"body_over", "title", strings.Repeat("é", 2048) + "a", false},
		{"body_lines", "title", "\tline\nnext\u2028", true},
		{"title_lf", "line\nnext", "", false},
		{"title_tab", "line\tnext", "", false},
		{"title_separator", "line\u2028next", "", false},
		{"title_paragraph", "line\u2029next", "", false},
		{"body_cr", "title", "\r", false},
		{"nul", "title", "\x00", false},
		{"c1", "title", "\u0085", false},
		{"utf8", "title", string([]byte{0xff}), false},
		{"empty_title", "", "body", false},
		{"spaces", "  ", "  ", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := Payload{Title: tt.title, Body: tt.body, Category: "info"}
			v, reason := validate(p, validationCaller())
			if (reason == "") != tt.ok {
				t.Fatal(reason)
			}
			if tt.ok && (v.Title != p.Title || v.Body != p.Body || v.Navigation != notification.Required) {
				t.Fatal("changed literal input")
			}
		})
	}
	for r := rune(0); r <= 0x9f; r++ {
		if r > 0x1f && r < 0x7f {
			continue
		}
		if content("a"+string(r), 256, false) {
			t.Fatalf("title Cc U+%04X", r)
		}
		if content(string(r), 4096, true) != (r == '\n' || r == '\t') {
			t.Fatalf("body Cc U+%04X", r)
		}
	}
	a := Payload{Title: "é", Category: "info", Navigation: notification.Required}
	b := a
	b.Title = "e\u0301"
	if digest(a) == digest(b) {
		t.Fatal("normalized Unicode")
	}
	a.Title = " x "
	b.Title = "x"
	if digest(a) == digest(b) {
		t.Fatal("trimmed Unicode")
	}
}
func TestEnumsAndIDs(t *testing.T) {
	base := Payload{Title: "x", Category: "info"}
	o := validationCaller()
	for _, modify := range []func(*Payload, *origin.Context){
		func(p *Payload, _ *origin.Context) { p.Category = "urgent" },
		func(p *Payload, _ *origin.Context) { p.Navigation = "shell" },
		func(p *Payload, _ *origin.Context) { id := ""; p.RequestID = &id },
		func(p *Payload, _ *origin.Context) { id := strings.Repeat("a", 257); p.RequestID = &id },
		func(_ *Payload, o *origin.Context) { o.SessionID = strings.Repeat("a", 257) },
		func(_ *Payload, o *origin.Context) { o.CallID = "\u0080" },
		func(_ *Payload, o *origin.Context) { o.Provenance = "claimed_by_payload" },
		func(_ *Payload, o *origin.Context) { o.SessionID = "" },
	} {
		p, c := base, o
		modify(&p, &c)
		if _, reason := validate(p, c); reason == "" {
			t.Fatal("accepted invalid input")
		}
	}
	id := strings.Repeat("é", 128)
	base.RequestID = &id
	o.SessionID = id
	o.CallID = id
	if _, reason := validate(base, o); reason != "" {
		t.Fatal(reason)
	}
}
