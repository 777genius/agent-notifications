package config

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestStrictDocument(t *testing.T) {
	for _, s := range []string{"", " ", "null", "[]", "{} {}", "{}x", `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, "\xef\xbb\xbf{}", string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}), `{"schemaVersion":null}`, `{"schemaVersion":1.5}`, `{"schemaVersion":"1"}`, `{"schemaVersion":0}`, `{"schemaVersion":-1}`, strings.Repeat("[", 129) + strings.Repeat("]", 129), strings.Repeat(" ", MaxDocumentBytes+1)} {
		if _, err := ParseDocument([]byte(s), "/fixture", true); err == nil {
			t.Errorf("accepted invalid input of length %d", len(s))
		}
	}
	_, err := ParseDocument([]byte(`{"schemaVersion":3}`), "/fixture", true)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigUnsupportedSchema {
		t.Fatalf("schema: %v", err)
	}
}
func TestRawPreservationAndRevision(t *testing.T) {
	b := []byte("{\n\"future\": {\"n\":90071992547409931234,\"a\":[null,false,0]},\"notifications\":{\"webhook\":{\"url\":\"${SECRET}\"}}\n}")
	d, err := ParseDocument(b, "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, d.Bytes()) || d.SchemaVersion() != 1 {
		t.Fatal("changed raw")
	}
	copy := d.Bytes()
	copy[0] = 'x'
	raw := d.Raw()
	delete(raw, "future")
	if !bytes.Equal(b, d.Bytes()) || d.Raw()["future"] == nil {
		t.Fatal("mutable document")
	}
	for _, p := range []struct {
		path   string
		exists bool
	}{{"/other", true}, {"/fixture", false}} {
		o, _ := ParseDocument(b, p.path, p.exists)
		if o.Revision() == d.Revision() {
			t.Fatal("revision not bound")
		}
	}
}
func TestKnownCaseAmbiguity(t *testing.T) {
	for _, s := range []string{`{"Notifications":{"desktop":{"Volume":0}}}`, `{"future":{"X":1,"x":2}}`} {
		d, err := ParseDocument([]byte(s), "/fixture", true)
		if err != nil || d.ValidateEditable() != nil {
			t.Fatalf("unambiguous: %v", err)
		}
	}
	for _, s := range []string{`{"notifications":{},"Notifications":{}}`, `{"statuses":{"question":{"sound":"a","Sound":"b"}}}`, `{"notifications":{"webhook":{"retry":{"enabled":true,"Enabled":false}}}}`} {
		d, err := ParseDocument([]byte(s), "/fixture", true)
		if err != nil {
			t.Fatal(err)
		}
		if d.ValidateEditable() == nil {
			t.Fatal("ambiguous edit allowed")
		}
	}
}

func TestDocumentBoundariesAndHugeUnknownNumbers(t *testing.T) {
	good := `{"future":` + strings.Repeat("[", MaxDocumentDepth-1) + `0` + strings.Repeat("]", MaxDocumentDepth-1) + `}`
	if _, err := ParseDocument([]byte(good), "/fixture", true); err != nil {
		t.Fatal("rejected maximum depth", err)
	}
	bad := `{"future":` + strings.Repeat("[", MaxDocumentDepth) + `0` + strings.Repeat("]", MaxDocumentDepth) + `}`
	if _, err := ParseDocument([]byte(bad), "/fixture", true); err == nil {
		t.Fatal("accepted excessive depth")
	}
	max := append([]byte(`{}`), bytes.Repeat([]byte(" "), MaxDocumentBytes-2)...)
	if _, err := ParseDocument(max, "/fixture", true); err != nil {
		t.Fatal("rejected maximum size", err)
	}
	raw := []byte(`{"notifications":{"webhook":{"payloadFields":{"huge":1e999,"n":9007199254740993001}}}}`)
	d, err := ParseDocument(raw, "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, d.Bytes()) {
		t.Fatal("unknown number changed")
	}
	_, err = ParseDocument([]byte(`{"schemaVersion":999999999999999999999999999999999}`), "/fixture", true)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigUnsupportedSchema {
		t.Fatalf("large schema: %v", err)
	}
}
