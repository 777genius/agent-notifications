package strictjson

import (
	"errors"
	"strings"
	"testing"
)

func TestValidation(t *testing.T) {
	budget := Budget{Bytes: 65536, Depth: 16, Entries: 10000}
	for _, s := range []string{`{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, string([]byte{'"', 255, '"'}), `{} {}`, `[`, strings.Repeat(" ", 65537), strings.Repeat("[", 18) + strings.Repeat("]", 18)} {
		if e := Validate([]byte(s), budget); !errors.Is(e, ErrInvalid) {
			t.Fatalf("expected sanitized error, got %v", e)
		}
	}
	for _, s := range []string{`{"text":"--help $(x) [important] 🐈‍⬛","body":"a\nb\t"}`, `"\ud83d\ude00"`, `{"a":{},"b":{"a":1}}`, `"\\ud800"`} {
		if e := Validate([]byte(s), budget); e != nil {
			t.Fatal(e)
		}
	}
}

func TestBudgets(t *testing.T) {
	for _, tc := range []struct {
		text   string
		budget Budget
	}{
		{`{}`, Budget{1, 16, 10}}, {`[1,2]`, Budget{100, 16, 1}}, {`{"a":1,"b":2}`, Budget{100, 16, 1}}, {`[[[0]]]`, Budget{100, 1, 10}},
	} {
		if Validate([]byte(tc.text), tc.budget) != ErrInvalid {
			t.Fatal("budget accepted")
		}
	}
}
