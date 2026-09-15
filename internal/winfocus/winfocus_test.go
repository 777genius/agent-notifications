package winfocus

import (
	"strings"
	"testing"
)

func TestEncodeDecodeURIRoundTrip(t *testing.T) {
	cases := []FocusContext{
		{HWND: 123456, PID: 4242, Title: "✳ Claude Code", Folder: "my-project"},
		{HWND: 0, PID: 0, Title: "", Folder: "only-folder"},
		{WarpURL: "warp://session/6b7be92641ae8ced80188a4d87e4b200"},
		{Title: "spaces and / slashes & ampersands"},
		{HWND: 9007199254740991}, // large handle value
		{},
	}
	for _, want := range cases {
		uri := want.EncodeURI()
		if !strings.HasPrefix(uri, ProtocolScheme+":") {
			t.Fatalf("EncodeURI = %q, want %q prefix", uri, ProtocolScheme+":")
		}
		got, err := DecodeURI(uri)
		if err != nil {
			t.Fatalf("DecodeURI(%q) error: %v", uri, err)
		}
		if got != want {
			t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, want)
		}
	}
}

func TestEncodeURIPayloadIsURISafe(t *testing.T) {
	// RawURLEncoding must not emit characters that break a URI or the "%1"
	// registry command substitution.
	uri := FocusContext{Title: "C:\\path with spaces/+=&", Folder: "x"}.EncodeURI()
	payload := strings.TrimPrefix(uri, ProtocolScheme+":")
	for _, bad := range []string{"/", "+", "=", " "} {
		if strings.Contains(payload, bad) {
			t.Errorf("payload %q contains unsafe %q", payload, bad)
		}
	}
}

func TestDecodeURITolerantOfShellMangling(t *testing.T) {
	base := FocusContext{PID: 77, Folder: "repo"}
	payload := strings.TrimPrefix(base.EncodeURI(), ProtocolScheme+":")

	variants := []string{
		ProtocolScheme + ":" + payload,
		strings.ToUpper(ProtocolScheme) + ":" + payload, // scheme case-insensitive
		ProtocolScheme + "://" + payload,                // authority separator
		ProtocolScheme + ":" + payload + "/",            // trailing slash
		"  " + ProtocolScheme + ":" + payload + "\n",    // surrounding whitespace
	}
	for _, v := range variants {
		got, err := DecodeURI(v)
		if err != nil {
			t.Fatalf("DecodeURI(%q) error: %v", v, err)
		}
		if got != base {
			t.Errorf("DecodeURI(%q) = %+v, want %+v", v, got, base)
		}
	}
}

func TestDecodeURIEmptyAndInvalid(t *testing.T) {
	if c, err := DecodeURI(ProtocolScheme + ":"); err != nil || c.HasTarget() {
		t.Errorf("empty payload: got %+v err %v, want zero context no error", c, err)
	}
	if _, err := DecodeURI(ProtocolScheme + ":!!!not-base64!!!"); err == nil {
		t.Error("expected error for invalid base64 payload")
	}
}

func TestHasTarget(t *testing.T) {
	if (FocusContext{}).HasTarget() {
		t.Error("zero context should not have a target")
	}
	for _, c := range []FocusContext{{HWND: 1}, {PID: 1}, {Title: "x"}, {Folder: "x"}, {WarpURL: "warp://session/6b7be92641ae8ced80188a4d87e4b200"}} {
		if !c.HasTarget() {
			t.Errorf("%+v should have a target", c)
		}
	}
}
