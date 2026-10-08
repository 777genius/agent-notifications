package runtime

import (
	"encoding/json"
	"testing"
)

// Breakage: strict policy drops/mixes the Windows generation or accepts unknown
// and incomplete nested binding data that origin later treats as authority.
func TestStrictWindowsPolicyBinding(t *testing.T) {
	b := &Backend{opts: options(t)}
	s := snapshot("A")
	s.Fields["route"] = json.RawMessage(`{"localRouting":true,"windowsCallbackSnapshot":{"snapshotPath":"\\\\?\\C:\\TEST\\generation.wne","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`)
	p, e := b.policy(s)
	if e != nil || p.Route.Windows.SnapshotPath != `\\?\C:\TEST\generation.wne` || p.Route.Windows.SHA256 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal(p, e)
	}
	s.Fields["route"] = json.RawMessage(`{"localRouting":true,"windowsCallbackSnapshot":{"snapshotPath":"changed","sha256":"changed"}}`)
	if p.Route.Windows.SnapshotPath != `\\?\C:\TEST\generation.wne` {
		t.Fatal("captured policy mutated")
	}
	for _, binding := range []string{`null`, `{}`, `{"snapshotPath":"x"}`, `{"snapshotPath":"x","sha256":"x","uri":"codex://other"}`} {
		s.Fields["route"] = json.RawMessage(`{"windowsCallbackSnapshot":` + binding + `}`)
		if _, e = b.policy(s); e == nil {
			t.Fatal("invalid binding accepted", binding)
		}
	}
}
