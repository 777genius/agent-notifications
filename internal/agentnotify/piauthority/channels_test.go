package piauthority

import (
	"encoding/json"
	"testing"
)

func TestPiConsentRequiresCompleteCanonicalBooleanMembers(t *testing.T) {
	for _, raw := range []string{`null`, `true`, `{"desktop":true}`, `{"desktop":null,"webhook":true}`, `{"desktop":"true","webhook":false}`, `{"Desktop":true,"webhook":false}`, `{"desktop":true,"webhook":false,"WEBHOOK":true}`} {
		t.Run(raw, func(t *testing.T) {
			route, _ := json.Marshal(map[string]json.RawMessage{ChannelKey: json.RawMessage(raw)})
			if _, _, _, err := channelChoices(route); err == nil {
				t.Fatal("ambiguous/incomplete Pi consent admitted")
			}
		})
	}
	for _, value := range []struct{ desktop, webhook bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		route, _ := json.Marshal(map[string]any{ChannelKey: map[string]bool{"desktop": value.desktop, "webhook": value.webhook}, "foreign": map[string]bool{"keep": true}})
		d, w, present, err := channelChoices(route)
		if err != nil || !present || d != value.desktop || w != value.webhook {
			t.Fatal("complete consent lost", err)
		}
	}
}
