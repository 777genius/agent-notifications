package nativeprotocol

import (
	"strings"
	"testing"
)

func TestPermissionStrictEnvelope(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	good := `{"schemaVersion":1,"correlationID":"` + id + `","nonce":"` + id + `","backend":"macos.usernotifications","permission":"allowed"}`
	if _, err := DecodePermission([]byte(good), id, id); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{good + good, strings.Replace(good, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1), strings.Replace(good, "allowed", "future", 1), strings.Replace(good, `"permission":"allowed"`, `"permission":null`, 1), strings.Replace(good, `"nonce":"`+id+`"`, `"nonce":"00000000-0000-4000-8000-000000000002"`, 1), strings.Replace(good, "allowed", string([]byte{255}), 1), good + strings.Repeat(" ", MaxEnvelopeBytes), strings.Replace(good, `"schemaVersion":1`, `"extra":1,"schemaVersion":1`, 1)} {
		if _, err := DecodePermission([]byte(bad), id, id); err != ErrInvalidEnvelope {
			t.Fatalf("expected clean error: %v", err)
		}
	}
}
