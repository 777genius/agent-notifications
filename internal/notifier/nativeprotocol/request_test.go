package nativeprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCanonicalSwiftRequestFixturesMatchProducerCopies(t *testing.T) {
	for _, name := range []string{"native-v1.request.json", "desktop-thread-v1.actions.json"} {
		t.Run(name, func(t *testing.T) {
			canonical, err := os.ReadFile(filepath.Join("..", "..", "..", "swift-notifier", "Tests", "Fixtures", name))
			if err != nil {
				t.Fatal(err)
			}
			copy, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, copy) {
				t.Fatal("Go producer fixture differs from canonical Swift fixture")
			}
		})
	}
}

func TestCanonicalInvalidTypedActionsCannotBeProduced(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "swift-notifier", "Tests", "Fixtures", "desktop-thread-v1.actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct{ Invalid []json.RawMessage }
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Invalid) == 0 {
		t.Fatal("missing invalid action fixtures")
	}
	for i, rawAction := range fixtures.Invalid {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(rawAction, &fields); err != nil {
				t.Fatal(err)
			}
			for key := range fields {
				switch key {
				case "type", "schemaVersion", "threadID", "routeKind", "bundleID", "teamID", "applicationPath", "correlationID":
				default:
					// Unknown raw keys are a Swift decoder contract. The typed
					// Go producer has no field that could serialize them.
					t.Skip("raw-only fixture has no typed producer representation")
				}
			}
			var action DesktopThreadAction
			if err := json.Unmarshal(rawAction, &action); err != nil {
				var mismatch *json.UnmarshalTypeError
				if !errors.As(err, &mismatch) {
					t.Fatal(err)
				}
				// Boolean schemaVersion, for example, cannot exist in the
				// producer's integer field; this proves representation only.
				return
			}
			r := requestFixture(t)
			r.Action = &action
			if _, err := EncodeRequest(r); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("invalid typed action was not rejected: %v", err)
			}
		})
	}
}

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
	if err := json.Unmarshal(original, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
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
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoded.Action, *r.Action) {
		t.Fatal("typed action bytes changed")
	}
	r.Action.CorrelationID = r.Nonce
	if _, err = EncodeRequest(r); err == nil {
		t.Fatal("uncorrelated action accepted")
	}
}
func TestPR3LiteralBytesAndBounds(t *testing.T) {
	for _, literal := range []string{"--help", "-execute", "[important]", "👩‍💻 می\u200cروم 🏴\U000e0067\U000e0062\U000e007f", `"$()<> &`} {
		r := requestFixture(t)
		r.Title = literal
		r.Body = literal + "\n\tline\u2028\u2029"
		r.Subtitle = literal
		data, err := EncodeRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		var actual struct{ Title, Body, Subtitle string }
		if err := json.Unmarshal(data, &actual); err != nil {
			t.Fatal(err)
		}
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
