package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestSetupValidationUsesReaderDefaultsAndRejectsExplicitZeros(t *testing.T) {
	path := filepath.Join(t.TempDir(), "global.json")
	if err := os.WriteFile(path, []byte(`{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := installruntime.PolicySnapshot{Fields: map[string]json.RawMessage{"schemaVersion": json.RawMessage(`1`), "enabled": json.RawMessage(`true`)}}
	for _, raw := range []string{`{"burst":2}`, `{}`, `{"sessionPerMinute":0,"runtimePerMinute":0,"burst":0}`, `{"burst":null}`, `{"burst":2,"burst":3}`} {
		s.Fields["rates"] = json.RawMessage(raw)
		p, err := ValidateSetupPolicy(s, path)
		if raw == `{"burst":2}` {
			if err != nil || p.Rates != (journal.RatePolicy{SessionPerMinute: 6, RuntimePerMinute: 30, Burst: 2}) {
				t.Fatal(p.Rates, err)
			}
		} else if raw == `{}` {
			if err != nil || p.Rates != (journal.RatePolicy{SessionPerMinute: 6, RuntimePerMinute: 30, Burst: 3}) {
				t.Fatal(p.Rates, err)
			}
		} else if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
