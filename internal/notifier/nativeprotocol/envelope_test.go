package nativeprotocol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const capabilityFixture = `{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`
const receiptFixture = `{"schemaVersion":1,"correlationID":"00000000-0000-4000-8000-000000000001","nonce":"00000000-0000-4000-8000-000000000002","notificationID":"00000000-0000-4000-8000-000000000001","status":"submitted","reason":"os_accepted","retrySafe":false}`

func TestSharedSwiftProtocolFixtures(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "swift-notifier", "Tests", "Fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if c, err := DecodeCapabilities(read("native-v1.capabilities.json")); err != nil || !c.Supports(1, "none") {
		t.Fatalf("Swift capability contract mismatch: %v", err)
	}
	if _, err := DecodeReceipt(read("native-v1.receipt.json"), "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"); err != nil {
		t.Fatalf("Swift receipt contract mismatch: %v", err)
	}
}

func TestCapabilitiesNegotiateOnlyAdvertisedProtocolAndAction(t *testing.T) {
	c, err := DecodeCapabilities([]byte(capabilityFixture))
	if err != nil || !c.Supports(1, "none") || c.Supports(2, "none") || c.Supports(1, "desktop_thread_v1") {
		t.Fatalf("unexpected capabilities: %+v, %v", c, err)
	}
	for _, input := range []string{
		"", "Usage: native helper", capabilityFixture + "{}",
		strings.Replace(capabilityFixture, `"schemaVersion":1`, `"schemaVersion":2`, 1),
		strings.Replace(capabilityFixture, `"schemaVersion":1`, `"schemaVersion":1,"schema\u0056ersion":1`, 1),
		strings.Replace(capabilityFixture, `"actionKinds":["none"]`, `"actionKinds":["none","none"]`, 1),
		strings.Replace(capabilityFixture, `"actionKinds":["none"]`, `"actionKinds":[null]`, 1),
		strings.Replace(capabilityFixture, `"receiptSupport":true`, `"receiptSupport":null`, 1),
		strings.Replace(capabilityFixture, `,"explicitFeatureEnabledByDefault":false`, "", 1),
		strings.Replace(capabilityFixture, `"explicitFeatureEnabledByDefault":false`, `"explicitFeatureEnabledByDefault":true`, 1),
		strings.Replace(capabilityFixture, `"backend":`, `"extra":1,"backend":`, 1),
		strings.Repeat(" ", MaxEnvelopeBytes) + capabilityFixture,
	} {
		if _, err := DecodeCapabilities([]byte(input)); !errors.Is(err, ErrInvalidEnvelope) {
			t.Errorf("invalid capability reply accepted (length %d)", len(input))
		}
	}
}

func TestReceiptRequiresAttemptCorrelationAndConsistentOutcome(t *testing.T) {
	if r, err := DecodeReceipt([]byte(receiptFixture), "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"); err != nil || r.Status != "submitted" {
		t.Fatalf("valid receipt rejected: %+v, %v", r, err)
	}
	for _, ids := range [][2]string{{"attempt-b", "00000000-0000-4000-8000-000000000002"}, {"00000000-0000-4000-8000-000000000001", "nonce-b"}, {"", ""}} {
		if _, err := DecodeReceipt([]byte(receiptFixture), ids[0], ids[1]); err == nil {
			t.Error("wrong attempt identity accepted")
		}
	}
	for _, change := range []struct {
		field string
		value any
	}{
		{"notificationID", "attempt-b"}, {"status", "unknown"}, {"status", "suppressed"},
		{"reason", "timeout"}, {"retrySafe", true}, {"retrySafe", nil},
		{"schemaVersion", 2}, {"body", "private text must not appear in errors"},
	} {
		var raw map[string]any
		if err := json.Unmarshal([]byte(receiptFixture), &raw); err != nil {
			t.Fatal(err)
		}
		raw[change.field] = change.value
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeReceipt(data, "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"); !errors.Is(err, ErrInvalidEnvelope) {
			t.Errorf("accepted inconsistent receipt field %s", change.field)
		}
	}
	unknown := strings.Replace(strings.Replace(receiptFixture, `"submitted"`, `"unknown"`, 1), `"os_accepted"`, `"timeout"`, 1)
	if r, err := DecodeReceipt([]byte(unknown), "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"); err != nil || r.RetrySafe {
		t.Fatal("unknown must remain an explicit non-retryable outcome")
	}
}
