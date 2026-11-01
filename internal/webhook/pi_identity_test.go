package webhook

import "testing"

func TestPiWebhookIdentity(t *testing.T) {
	if normalizeAgentSource("pi") != "pi" || agentDisplayName("pi") != "Pi" {
		t.Fatal("Pi identity was lost")
	}
	if agentDisplayName("") != "Claude" {
		t.Fatal("legacy identity changed")
	}
}
