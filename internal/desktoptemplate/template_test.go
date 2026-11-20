package desktoptemplate

import (
	"strings"
	"testing"
)

func TestRenderCollapsesEmptyPlaceholders(t *testing.T) {
	values := map[string]string{"a": "A", "b": "B", "c": "C"}
	for _, tc := range []struct{ template, want string }{
		{"{a} · {b}", "A · B"},
		{"{missing} · {b}", "B"},
		{"{a} · {missing}", "A"},
		{"{a} · {missing} · {c}", "A · C"},
		{"{missing} · {missing}", ""},
		{"{a} in {missing} on {c}", "A on C"},
		{"[{a}] {missing}", "[A]"},
		{"[{missing}]", ""},
		{"{a} [{missing}]", "A"},
		{"{a} ({b})", "A (B)"},
		{"{missing} ({b})", "(B)"},
		{"{a}: {missing} ({c})", "A (C)"},
		{"x {a} y", "x A y"},
		{"prefix {missing} suffix", "prefix  suffix"},
		{"{header}\n{b}", "B"},
		{"no placeholders", "no placeholders"},
		{"", ""},
		{"{ a } {a} {", "{ a } A {"},
		{"{a}{b}{c}", "ABC"},
		// A bracket pair around several placeholders keeps both brackets
		// while any of them renders, and goes whole when all are empty.
		{"[{a} {missing}]", "[A]"},
		{"[{missing} {a}]", "[A]"},
		{"[{a} {b}]", "[A B]"},
		{"{a} [{missing} {missing}]", "A"},
		{"{a} ({b} [{missing}])", "A (B)"},
		{"{a} ({missing} [{missing}])", "A"},
		{"{a} ( {missing} )", "A"},
		// Unpaired or placeholder-free brackets stay literal.
		{"[{a}", "[A"},
		{"{a}]", "A]"},
		{"(x) {a}", "(x) A"},
		{"[{a})", "[A)"},
		{"{a} · {missing} · {c}", "A · C"},
		{"«{a}» — {c}", "«A» — C"},
	} {
		if got := Render(tc.template, values); got != tc.want {
			t.Errorf("Render(%q) = %q, want %q", tc.template, got, tc.want)
		}
	}
}

func TestRenderDoesNotReinterpretValues(t *testing.T) {
	got := Render("{a} {b}", map[string]string{"a": "{b}", "b": "x"})
	if got != "{b} x" {
		t.Fatalf("values must be literal, got %q", got)
	}
}

func TestValidate(t *testing.T) {
	all := "{statusTitle}{status}{sessionName}{sessionId}{folder}{repo}{branch}{question}{body}{actions}{agent}"
	for _, kind := range []Kind{Title, Header, Body} {
		if err := Validate(all, kind); err != nil {
			t.Fatalf("kind %d: %v", kind, err)
		}
		if err := Validate("{ not a placeholder } {", kind); err != nil {
			t.Fatalf("literal braces rejected: %v", err)
		}
		if err := Validate("{sessionID}", kind); err == nil || !strings.Contains(err.Error(), "{sessionID}") {
			t.Fatalf("unknown placeholder accepted or unnamed: %v", err)
		}
	}
	if err := Validate("{header}\n{body}", Body); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{Title, Header} {
		if err := Validate("{header}", kind); err == nil {
			t.Fatalf("kind %d accepted {header}", kind)
		}
	}
}
