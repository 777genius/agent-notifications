package installruntime

import (
	"encoding/json"
	"testing"
)

func TestRawPolicyRejectsUnicodeEquivalentKeys(t *testing.T) {
	if unambiguousPolicyValue(map[string]any{"route": map[string]any{
		"desktop": true, "deſktop": false,
	}}) {
		t.Fatal("Unicode-folded desktop key collision accepted")
	}
	if !unambiguousPolicyValue(map[string]any{"route": map[string]any{
		"desktop": true, "webhook": false,
	}}) {
		t.Fatal("distinct fields refused")
	}
	before := map[string]json.RawMessage{
		"schemaVersion": json.RawMessage("1"),
		"enabled": json.RawMessage("false"),
		"route": json.RawMessage(`{"desktop":true,"deſktop":false}`),
	}
	after := []byte(`{"schemaVersion":1,"enabled":false,"route":{"deſktop":false,"desktop":true}}`)
	if err := validateRawPolicyDocument(before, after); err == nil {
		t.Fatal("raw policy reordering changed effective desktop consent")
	}
}
