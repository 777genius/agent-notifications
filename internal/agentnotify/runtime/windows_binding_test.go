package runtime

import (
	"encoding/json"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Breakage: strict policy drops/mixes the Windows generation or accepts unknown
// and incomplete nested binding data that origin later treats as authority.
func TestStrictWindowsPolicyBinding(t *testing.T) {
	s := installruntime.PolicySnapshot{
		Installation: installruntime.InstalledSnapshot{Enabled: true},
		Fields: map[string]json.RawMessage{
			"schemaVersion": json.RawMessage(`1`),
			"enabled":       json.RawMessage(`true`),
		},
	}
	global := []byte(`{"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":true}}}`)
	s.Fields["route"] = json.RawMessage(`{"localRouting":true,"windowsCallbackSnapshot":{"snapshotPath":"\\\\?\\C:\\TEST\\generation.wne","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`)
	p, e := ValidatePreparedSetupPolicy(s, global)
	if e != nil || p.Route.Windows.SnapshotPath != `\\?\C:\TEST\generation.wne` || p.Route.Windows.SHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal(p, e)
	}
	s.Fields["route"] = json.RawMessage(`{"localRouting":true,"windowsCallbackSnapshot":{"snapshotPath":"changed","sha256":"changed"}}`)
	if p.Route.Windows.SnapshotPath != `\\?\C:\TEST\generation.wne` {
		t.Fatal("captured policy mutated")
	}
	for _, binding := range []string{`null`, `{}`, `{"snapshotPath":"x"}`, `{"snapshotPath":"x","sha256":"x","uri":"codex://other"}`} {
		s.Fields["route"] = json.RawMessage(`{"windowsCallbackSnapshot":` + binding + `}`)
		if _, e = ValidatePreparedSetupPolicy(s, global); e == nil {
			t.Fatal("invalid binding accepted", binding)
		}
	}
}
