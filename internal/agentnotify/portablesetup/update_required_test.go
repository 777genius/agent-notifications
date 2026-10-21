package portablesetup

import (
	"errors"
	"fmt"
	"testing"
)

// The controlled-removal signal must route to the update path: without it a
// legacy→renamed MCP key upgrade dead-ends in portable_preflight_failed.
func TestIsUpdateRequiredSignals(t *testing.T) {
	for _, msg := range []string{
		"managed package drift; run update separately",
		"binding is sticky to the current source; use switch to change source",
		"recorded MCP selection is no longer supported; use an explicit update for controlled removal: agent-notify",
		"source is sticky to the installed revision; is sticky to",
	} {
		if !IsUpdateRequired(errors.New(msg)) {
			t.Fatalf("not recognized as update-required: %s", msg)
		}
	}
	wrapped := fmt.Errorf("preview: %w", errors.New("recorded MCP selection is no longer supported; use an explicit update for controlled removal: agent_notifications"))
	if !IsUpdateRequired(wrapped) {
		t.Fatal("wrapped controlled-removal signal not recognized")
	}
	if IsUpdateRequired(errors.New("managed package was changed or is missing; refusing silent update")) {
		t.Fatal("unrelated refusal recognized as update-required")
	}
}
