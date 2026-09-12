package nativeprotocol

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func requestFixture(t *testing.T) Request {
	t.Helper()
	data, err := os.ReadFile("testdata/native-v1.request.json")
	if err != nil {
		t.Fatal(err)
	}
	var r Request
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestPR3SharedRequestAndTypedAction(t *testing.T) {
	r := requestFixture(t)
	data, err := EncodeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("testdata/native-v1.request.json")
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	json.Unmarshal(original, &a)
	json.Unmarshal(data, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("native no-action fixture mismatch")
	}
	raw, err := os.ReadFile("testdata/desktop-thread-v1.actions.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Valid []struct {
			Action DesktopThreadAction
			URI    string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Valid) != 1 {
		t.Fatal("unexpected shared fixture")
	}
	r.Action = &fixture.Valid[0].Action
	data, err = EncodeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct{ Action DesktopThreadAction }
	json.Unmarshal(data, &encoded)
	if !reflect.DeepEqual(encoded.Action, *r.Action) {
		t.Fatal("typed action bytes changed")
	}
	r.Action.CorrelationID = r.Nonce
	if _, err = EncodeRequest(r); err == nil {
		t.Fatal("uncorrelated action accepted")
	}
}
func TestPR3LiteralBytesAndBounds(t *testing.T) {
	for _, literal := range []string{"--help", "-execute", "[important]", "👩‍💻 می‌روم 🏴\U000e0067\U000e0062\U000e007f", `"$()<> &`} {
		r := requestFixture(t)
		r.Title = literal
		r.Body = literal + "\n\tline\u2028\u2029"
		r.Subtitle = literal
		data, err := EncodeRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		var actual struct{ Title, Body, Subtitle string }
		json.Unmarshal(data, &actual)
		if actual.Title != r.Title || actual.Body != r.Body || actual.Subtitle != r.Subtitle {
			t.Fatal("literal bytes changed")
		}
	}
	for _, s := range []string{strings.Repeat("<", 4096), strings.Repeat("&", 4096), strings.Repeat("👩", 1024)} {
		r := requestFixture(t)
		r.Body = s
		if _, err := EncodeRequest(r); err != nil {
			t.Fatal("valid body byte boundary rejected", err)
		}
		r.Body += "x"
		if _, err := EncodeRequest(r); err == nil {
			t.Fatal("oversized decoded body")
		}
	}
	for _, bad := range []string{"\x00", "\r", "\x1b", "\x7f", "\u0085", "\u009f", string([]byte{255})} {
		r := requestFixture(t)
		r.Body = bad
		if _, err := EncodeRequest(r); err == nil {
			t.Fatal("control/invalid UTF-8 accepted")
		}
	}
	for _, bad := range []string{"\n", "\t", "\u2028", "\u2029", strings.Repeat("é", 129)} {
		r := requestFixture(t)
		r.Title = bad
		if _, err := EncodeRequest(r); err == nil {
			t.Fatal("invalid title accepted")
		}
	}
}
func TestPR3FutureCapabilitiesRemainNegotiable(t *testing.T) {
	raw := strings.Replace(capabilityFixture, `["none"]`, `["none","desktop_thread_v1","future_action_v2"]`, 1)
	caps, err := DecodeCapabilities([]byte(raw))
	if err != nil || !caps.Supports(1, "desktop_thread_v1") {
		t.Fatal("valid future actions rejected", err)
	}
}
